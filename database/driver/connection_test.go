package driver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormtests "gorm.io/gorm/utils/tests"

	"github.com/goravel/framework/contracts/database"
	mocksconfig "github.com/goravel/framework/mocks/config"
)

// poolConfigExpectations sets the only configuration the connection reads while opening a pool.
func poolConfigExpectations(config *mocksconfig.Config) {
	config.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Once()
	config.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Once()
	config.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Once()
	config.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Once()
}

// A reader entry the driver could not turn into a dialector — one carrying neither a database nor a
// dsn — cannot serve reads, but it must not abort the connection. Reads fall back to the writer,
// which is what Connection.Reader documents and what the connection did before reads were routed.
func TestOpen_UnusableReaderFallsBackToWriter(t *testing.T) {
	t.Cleanup(ResetConnections)

	config := mocksconfig.NewConfig(t)
	poolConfigExpectations(config)

	pool := database.Pool{
		Writers: []database.Config{
			{Driver: "postgres", Database: "app", Dialector: stubDialector{}},
		},
		Readers: []database.Config{
			{Driver: "postgres"}, // neither Database nor Dsn: the driver yields a nil Dialector
		},
	}

	conn, err := Open(config, discardLogger{}, pool, "reader_fallback", nil)
	assert.NoError(t, err)
	assert.NotNil(t, conn)
	assert.Same(t, conn.Writer(), conn.Reader())
}

// A first replica entry the driver could not turn into a dialector must not mask a valid replica
// configured behind it: compiled reads run on the first replica that can serve them, not on the
// writer merely because Readers[0] was unusable.
func TestOpen_UnusableFirstReaderFallsBackToNextReplica(t *testing.T) {
	t.Cleanup(CloseConnections)

	config := mocksconfig.NewConfig(t)
	config.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Times(2)
	config.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)

	pool := database.Pool{
		Writers: []database.Config{
			{Driver: "postgres", Database: "app", Dialector: stubDialector{}},
		},
		Readers: []database.Config{
			{Driver: "postgres"}, // neither Database nor Dsn: the driver yields a nil Dialector
			{Driver: "postgres", Database: "replica", Dialector: healthyReaderDialector{}},
		},
	}

	conn, err := Open(config, discardLogger{}, pool, "reader_next_replica", nil)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NotNil(t, Reader("reader_next_replica"))
	assert.Same(t, Reader("reader_next_replica"), conn.Reader())
	assert.NotSame(t, conn.Writer(), conn.Reader())
}

type healthyReaderConnector struct{ stubConnector }

func (healthyReaderConnector) Connect(context.Context) (driver.Conn, error) {
	return healthyReaderConnection{}, nil
}

type healthyReaderConnection struct{ driver.Conn }

func (healthyReaderConnection) Ping(context.Context) error { return nil }
func (healthyReaderConnection) Close() error               { return nil }

type healthyReaderDialector struct{ gormtests.DummyDialector }

func (healthyReaderDialector) Initialize(db *gorm.DB) error {
	if err := (gormtests.DummyDialector{}).Initialize(db); err != nil {
		return err
	}
	db.ConnPool = sql.OpenDB(healthyReaderConnector{})
	return nil
}

func TestOpenSkipsUnreachableReaderCandidates(t *testing.T) {
	t.Cleanup(CloseConnections)
	config := mocksconfig.NewConfig(t)
	poolConfigExpectations(config)
	pool := database.Pool{Readers: []database.Config{
		{Dialector: stubDialector{}},
		{Dialector: healthyReaderDialector{}},
	}}
	reader, err := openReader(config, pool, "reachable_reader")
	require.NoError(t, err)
	require.NotNil(t, reader)
	require.NoError(t, reader.Ping())
}

func TestOpenReturnsErrorWhenEveryConfiguredReaderIsUnreachable(t *testing.T) {
	t.Cleanup(CloseConnections)
	reader, err := openReader(nil, database.Pool{Readers: []database.Config{
		{Dialector: stubDialector{}}, {Dialector: stubDialector{}},
	}}, "unreachable_readers")
	require.ErrorIs(t, err, driver.ErrBadConn)
	assert.Nil(t, reader)
	assert.Nil(t, Reader("unreachable_readers"))
}
