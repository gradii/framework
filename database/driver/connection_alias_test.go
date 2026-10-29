package driver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAliasResourcesUseOneGenerationAcrossEntryPoints(t *testing.T) {
	t.Cleanup(CloseConnections)
	var version, queries atomic.Int64
	version.Store(1)
	pool := database.Pool{
		Writers: []database.Config{{Connection: "configured", Connector: versionConnector{&version, &queries}}},
		Readers: []database.Config{{Connector: healthyConnector{}}},
	}
	resolver := func() contractstelemetry.Telemetry { return nil }
	open := func(name string) *Connection {
		connection, err := Open(aliasPoolConfig{}, discardLogger{}, pool, name, resolver, aliasVersionDialect{})
		require.NoError(t, err)
		return connection
	}
	first, same, sibling := open("alias"), open("alias"), open("sibling")
	assert.Same(t, first.Writer(), same.Writer())
	assert.Same(t, first.Reader(), same.Reader())
	require.NotNil(t, first.Instrument())
	assert.Same(t, first.Instrument(), same.Instrument())
	assert.Same(t, first.Instrument(), Instrument("alias", pool))
	assert.NotSame(t, first.Writer(), sibling.Writer())
	assert.NotSame(t, first.Reader(), sibling.Reader())
	assert.NotSame(t, first.Instrument(), sibling.Instrument())
	for _, connection := range []*Connection{first, same} {
		got, err := connection.ServerVersion(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "1", got)
	}
	assert.EqualValues(t, 1, queries.Load(), "the version cache belongs to the shared pool generation")

	require.NoError(t, PurgeConnection("alias", pool))
	assert.False(t, first.IsCurrent())
	assert.False(t, same.IsCurrent())
	assert.ErrorContains(t, first.Writer().Ping(), "database is closed")
	assert.ErrorContains(t, first.Reader().Ping(), "database is closed")
	assert.Nil(t, Reader("alias", pool))
	assert.Nil(t, Instrument("alias", pool))
	require.NoError(t, sibling.Writer().Ping())
	require.NoError(t, sibling.Reader().Ping())

	version.Store(2)
	fresh := open("alias")
	assert.NotSame(t, first.Writer(), fresh.Writer())
	assert.NotSame(t, first.Reader(), fresh.Reader())
	assert.NotSame(t, first.Instrument(), fresh.Instrument())
	got, err := fresh.ServerVersion(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "2", got)
	assert.EqualValues(t, 2, queries.Load())

	// A handle from the replaced generation must not close the live one.
	require.NoError(t, first.Purge())
	assert.True(t, fresh.IsCurrent())
	require.NoError(t, fresh.Writer().Ping())
	require.NoError(t, fresh.Reader().Ping())
}

func TestAliasConcurrentOpenAndPurgeDoesNotLeaveStalePools(t *testing.T) {
	t.Cleanup(CloseConnections)
	pool := database.Pool{
		Writers: []database.Config{{Connection: "configured", Connector: healthyConnector{}}},
		Readers: []database.Config{{Connector: healthyConnector{}}},
	}
	var pools sync.Map
	var workers sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		workers.Go(func() {
			for iteration := 0; iteration < 30; iteration++ {
				if worker%3 == 0 {
					assert.NoError(t, PurgeConnection("concurrent_alias", pool))
					continue
				}
				connection, err := Open(aliasPoolConfig{}, discardLogger{}, pool, "concurrent_alias", nil)
				if !assert.NoError(t, err) {
					return
				}
				pools.Store(connection.Writer(), true)
				pools.Store(connection.Reader(), true)
				assert.NotSame(t, connection.Writer(), connection.Reader())
			}
		})
	}
	workers.Wait()

	fresh, err := Open(aliasPoolConfig{}, discardLogger{}, pool, "concurrent_alias", nil)
	require.NoError(t, err)
	require.True(t, fresh.IsCurrent())
	require.NoError(t, fresh.Writer().Ping())
	require.NoError(t, fresh.Reader().Ping())
	assert.Same(t, fresh.Reader(), Reader("concurrent_alias", pool))
	CloseConnections()
	pools.Range(func(value, _ any) bool {
		assert.ErrorContains(t, value.(*sql.DB).Ping(), "database is closed")
		return true
	})
}

type aliasPoolConfig struct{ config.Config }

func (aliasPoolConfig) GetInt(_ string, fallback ...int) int { return fallback[0] }
func (aliasPoolConfig) GetDuration(_ string, fallback ...time.Duration) time.Duration {
	return fallback[0]
}
func (aliasPoolConfig) GetBool(_ string, fallback ...bool) bool { return fallback[0] }

type aliasVersionDialect struct{ contractsdriver.Dialect }

func (aliasVersionDialect) ServerVersionSQL() string { return "select version()" }

type versionConnector struct{ version, queries *atomic.Int64 }

func (c versionConnector) Connect(context.Context) (driver.Conn, error) {
	return versionConnection{versionConnector: c}, nil
}
func (versionConnector) Driver() driver.Driver { return stubDriver{} }

type versionConnection struct {
	healthyConnection
	versionConnector
}

func (c versionConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.queries.Add(1)
	return &versionRows{value: strconv.FormatInt(c.version.Load(), 10)}, nil
}

type versionRows struct {
	value string
	done  bool
}

func (*versionRows) Columns() []string { return []string{"version"} }
func (*versionRows) Close() error      { return nil }
func (r *versionRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = r.value
	return nil
}
