package driver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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
			{Driver: "postgres", Database: "replica", Dialector: stubDialector{}},
		},
	}

	conn, err := Open(config, discardLogger{}, pool, "reader_next_replica", nil)
	assert.NoError(t, err)
	assert.NotNil(t, conn)
	assert.NotNil(t, Reader("reader_next_replica"))
	assert.Same(t, Reader("reader_next_replica"), conn.Reader())
	assert.NotSame(t, conn.Writer(), conn.Reader())
}
