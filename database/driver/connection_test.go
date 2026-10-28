package driver

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/contracts/database"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	"github.com/gradii/framework/contracts/log"
	mocksconfig "github.com/gradii/framework/mocks/config"
	"github.com/gradii/framework/support/carbon"
)

// poolConfigExpectations sets the only configuration the connection reads while opening a pool.
func poolConfigExpectations(config *mocksconfig.Config) {
	config.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Once()
	config.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Once()
	config.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Once()
	config.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Once()
}

// A reader entry that cannot serve reads — one whose pool refuses every connection — must not abort
// the connection. Reads fall back to the writer, which is what Connection.Reader documents.
func TestOpen_UnusableReaderFallsBackToWriter(t *testing.T) {
	t.Cleanup(ResetConnections)

	config := mocksconfig.NewConfig(t)
	poolConfigExpectations(config)

	pool := database.Pool{
		Writers: []database.Config{
			{Driver: "postgres", Database: "app", Connector: healthyConnector{}},
		},
		Readers: []database.Config{
			// Neither a connector nor a host: the framework has nothing to open, so the entry is
			// skipped rather than failing the connection.
			{Driver: "postgres"},
		},
	}

	conn, err := Open(config, discardLogger{}, pool, "reader_fallback", nil)
	assert.NoError(t, err)
	assert.NotNil(t, conn)
	assert.Same(t, conn.Writer(), conn.Reader())
}

// An unreachable first replica must not mask a valid replica configured behind it: reads run on the
// first replica that can serve them, not on the writer merely because Readers[0] was unreachable.
func TestOpen_UnusableFirstReaderFallsBackToNextReplica(t *testing.T) {
	t.Cleanup(CloseConnections)

	config := mocksconfig.NewConfig(t)
	config.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Times(2)
	config.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)

	pool := database.Pool{
		Writers: []database.Config{
			{Driver: "postgres", Database: "app", Connector: healthyConnector{}},
		},
		Readers: []database.Config{
			{Driver: "postgres"},
			{Driver: "postgres", Database: "replica", Connector: healthyConnector{}},
		},
	}

	conn, err := Open(config, discardLogger{}, pool, "reader_next_replica", nil)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NotNil(t, Reader("reader_next_replica"))
	assert.Same(t, Reader("reader_next_replica"), conn.Reader())
	assert.NotSame(t, conn.Writer(), conn.Reader())
}

func TestOpenSkipsUnreachableReaderCandidates(t *testing.T) {
	t.Cleanup(CloseConnections)
	config := mocksconfig.NewConfig(t)
	poolConfigExpectations(config)
	pool := database.Pool{Readers: []database.Config{
		{Connector: stubConnector{}},
		{Connector: healthyConnector{}},
	}}
	reader, err := openReader(config, pool, "reachable_reader")
	require.NoError(t, err)
	require.NotNil(t, reader)
	require.NoError(t, reader.Ping())
}

func TestOpenReturnsErrorWhenEveryConfiguredReaderIsUnreachable(t *testing.T) {
	t.Cleanup(CloseConnections)
	reader, err := openReader(nil, database.Pool{Readers: []database.Config{
		{Connector: stubConnector{}}, {Connector: stubConnector{}},
	}}, "unreachable_readers")
	require.ErrorIs(t, err, driver.ErrBadConn)
	assert.Nil(t, reader)
	assert.Nil(t, Reader("unreachable_readers"))
}

// The lifecycle helpers own the cached pools: ResetConnections drops the cache but leaves a pool
// open for callers that still hold it, CloseConnections closes them.
func TestCloseConnections_ClosesPool(t *testing.T) {
	t.Cleanup(CloseConnections)

	writer, _, err := BuildConnection(stubConfig(t), discardLogger{}, stubPool(), "primary", nil)
	require.NoError(t, err)

	CloseConnections()

	assert.ErrorContains(t, writer.Ping(), "database is closed")
}

func TestResetConnections_KeepsPoolOpen(t *testing.T) {
	t.Cleanup(CloseConnections)

	first, _, err := BuildConnection(stubConfig(t), discardLogger{}, unreachablePool(), "primary", nil)
	require.NoError(t, err)

	ResetConnections()

	// The cache no longer holds the pool, but the pool itself is still open: the connection fails on
	// its connector, not because the pool was closed under a caller still holding it.
	err = first.Ping()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "database is closed")

	second, _, err := BuildConnection(stubConfig(t), discardLogger{}, unreachablePool(), "primary", nil)
	require.NoError(t, err)
	assert.NotSame(t, first, second)
}

// A connection built without telemetry carries no instrument, and the lifecycle helpers still work.
func TestResetConnections_NilInstrument(t *testing.T) {
	t.Cleanup(CloseConnections)

	_, instrument, err := BuildConnection(stubConfig(t), discardLogger{}, stubPool(), "primary", nil)
	require.NoError(t, err)
	assert.Nil(t, instrument)

	assert.NotPanics(t, ResetConnections)
}

// A connection with no database name opens nothing and reports no error: that is a fresh
// application before any credentials exist.
func TestBuildConnection_NoDatabaseOpensNothing(t *testing.T) {
	// No pool settings are read: the connection is recognised as unconfigured before it opens.
	writer, instrument, err := BuildConnection(mocksconfig.NewConfig(t), discardLogger{}, database.Pool{
		Writers: []database.Config{{Driver: "postgres"}},
	}, "unconfigured", nil)
	require.NoError(t, err)
	assert.Nil(t, writer)
	assert.Nil(t, instrument)
}

func TestOpenScopesSharedAliasesToConfiguredConnections(t *testing.T) {
	t.Cleanup(CloseConnections)
	firstPool, secondPool := stubPool(), stubPool()
	firstPool.Writers[0].Connection = "first_configured_connection"
	secondPool.Writers[0].Connection = "second_configured_connection"
	first, err := Open(stubConfig(t), discardLogger{}, firstPool, "shared_alias", nil)
	require.NoError(t, err)
	second, err := Open(stubConfig(t), discardLogger{}, secondPool, "shared_alias", nil)
	require.NoError(t, err)
	assert.NotSame(t, first.Writer(), second.Writer())
	alias, err := Open(stubConfig(t), discardLogger{}, firstPool, "another_alias", nil)
	require.NoError(t, err)
	assert.NotSame(t, first.Writer(), alias.Writer())
	sameAlias, err := Open(mocksconfig.NewConfig(t), discardLogger{}, firstPool, "shared_alias", nil)
	require.NoError(t, err)
	assert.Same(t, first.Writer(), sameAlias.Writer())

	require.NoError(t, first.Purge())
	assert.ErrorContains(t, first.Writer().Ping(), "database is closed")
	assert.False(t, sameAlias.IsCurrent())
	require.NoError(t, alias.Writer().Ping())
	require.NoError(t, second.Writer().Ping())

	fresh, err := Open(stubConfig(t), discardLogger{}, firstPool, "shared_alias", nil)
	require.NoError(t, err)
	assert.NotSame(t, first.Writer(), fresh.Writer())
	assert.True(t, fresh.IsCurrent())
}

// stubConfig answers only what opening a pool reads, with telemetry off.
func stubConfig(t *testing.T) *mocksconfig.Config {
	t.Helper()

	config := mocksconfig.NewConfig(t)
	poolConfigExpectations(config)

	return config
}

func stubPool() database.Pool {
	return database.Pool{
		Writers: []database.Config{
			{Driver: "postgres", Database: "app", Connector: healthyConnector{}},
		},
	}
}

// unreachablePool is a pool that opens but can never lease a connection, standing in for a server
// that is down while the pool handle is still valid.
func unreachablePool() database.Pool {
	return database.Pool{
		Writers: []database.Config{
			{Driver: "postgres", Database: "app", Connector: stubConnector{}},
		},
	}
}

// healthyConnector opens a pool whose connections answer a ping, which is all the connection tests
// ask of a pool.
type healthyConnector struct{}

func (healthyConnector) Connect(context.Context) (driver.Conn, error) {
	return healthyConnection{}, nil
}
func (healthyConnector) Driver() driver.Driver { return stubDriver{} }

type healthyConnection struct{}

func (healthyConnection) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (healthyConnection) Close() error                        { return nil }
func (healthyConnection) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

// Ping is what openReader checks before it accepts a replica.
func (healthyConnection) Ping(context.Context) error { return nil }

// stubConnector opens a pool whose connections can never be leased, standing in for an unreachable
// replica.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) { return nil, driver.ErrBadConn }
func (stubConnector) Driver() driver.Driver                        { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return nil, driver.ErrBadConn }

// discardLogger satisfies the framework's database logger without recording anything, which is what
// these connection tests need: they assert on pool lifecycle, not on log output.
type discardLogger struct{}

func (discardLogger) Level(contractslogger.Level) contractslogger.Logger          { return discardLogger{} }
func (discardLogger) Log() log.Log                                                { return nil }
func (discardLogger) Infof(context.Context, string, ...any)                       {}
func (discardLogger) Warningf(context.Context, string, ...any)                    {}
func (discardLogger) Errorf(context.Context, string, ...any)                      {}
func (discardLogger) Panicf(context.Context, string, ...any)                      {}
func (discardLogger) Trace(context.Context, *carbon.Carbon, string, int64, error) {}
