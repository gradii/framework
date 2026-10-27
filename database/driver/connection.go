package driver

import (
	"database/sql"
	"sync"

	"github.com/goravel/framework/contracts/config"
	"github.com/goravel/framework/contracts/database"
	contractslogger "github.com/goravel/framework/contracts/database/logger"
	contractstelemetry "github.com/goravel/framework/contracts/telemetry"
	instrumentationdatabase "github.com/goravel/framework/telemetry/instrumentation/database"
)

// Connection is the framework's handle on one configured database connection. It owns the pools the
// framework executes its compiled statements through — one writer pool and, when the connection is
// configured with replicas, one reader pool.
//
// Read/write routing lives here: a statement that only reads rows runs on the reader pool and every
// other one on the writer. The pools are opened by this package from the connection's own fields, so
// no ORM session sits between the framework and the database.
type Connection struct {
	versionMu     sync.Mutex
	serverVersion string
	name          string
	driver        string
	writer        *sql.DB
	reader        *sql.DB
	instrument    *instrumentationdatabase.Instrument
}

// Open returns the connection for a configured pool, building and caching it on first use.
//
// It returns a nil connection, without an error, when the connection carries no database name:
// that is the state of a fresh application before any credentials exist, and callers treat it as
// "no database" rather than as a failure.
func Open(config config.Config, logger contractslogger.Logger, pool database.Pool, connection string, telemetryResolver contractstelemetry.Resolver) (*Connection, error) {
	writer, instrument, err := BuildConnection(config, logger, pool, connection, telemetryResolver)
	if err != nil {
		return nil, err
	}
	if writer == nil {
		return nil, nil
	}

	driverName := ""
	if len(pool.Writers) > 0 {
		driverName = pool.Writers[0].Driver
	}

	return &Connection{
		name:       connection,
		driver:     driverName,
		writer:     writer,
		reader:     Reader(connection),
		instrument: instrument,
	}, nil
}

// Name returns the configured connection name this handle was opened for.
func (r *Connection) Name() string {
	return r.name
}

// Driver returns the driver name the connection is configured with.
func (r *Connection) Driver() string {
	return r.driver
}

// Writer returns the pool writes execute through: inserts, updates, deletes and transactions.
func (r *Connection) Writer() *sql.DB {
	return r.writer
}

// Reader returns the pool compiled reads execute through. A connection configured without replicas
// reads through its writer, so this always returns a usable pool.
func (r *Connection) Reader() *sql.DB {
	if r.reader != nil {
		return r.reader
	}

	return r.writer
}

// Instrument returns the telemetry instrument bound to this connection, or nil when database
// telemetry is disabled.
func (r *Connection) Instrument() *instrumentationdatabase.Instrument {
	return r.instrument
}

// Reader returns the reader pool cached for a connection name, or nil when the connection has no
// usable replica — either none configured or none openable. It exists so the ORM, which holds only
// the connection name, can resolve the pool the facades execute their reads on; callers fall back to
// the writer on nil.
func Reader(connection string) *sql.DB {
	if cached, ok := readPools.Load(connection); ok {
		return cached.(cachedReader).pool
	}

	return nil
}

// resetReaders drops the cached reader pools without closing them, leaving any pool a caller
// still holds open, which mirrors what ResetConnections does for the writer.
func resetReaders() {
	readPools.Range(func(key, _ any) bool {
		readPools.Delete(key)

		return true
	})
}

// closeReaders drops the cached reader pools and closes them.
func closeReaders() {
	readPools.Range(func(key, value any) bool {
		readPools.Delete(key)

		if reader, ok := value.(cachedReader); ok {
			_ = reader.pool.Close()
		}

		return true
	})
}
