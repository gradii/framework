package driver

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	gormio "gorm.io/gorm"

	"github.com/goravel/framework/contracts/config"
	"github.com/goravel/framework/contracts/database"
	contractslogger "github.com/goravel/framework/contracts/database/logger"
	contractstelemetry "github.com/goravel/framework/contracts/telemetry"
	"github.com/goravel/framework/support/color"
	instrumentationdatabase "github.com/goravel/framework/telemetry/instrumentation/database"
)

// Connection is the framework's handle on one configured database connection. It owns the pools the
// framework executes its compiled statements through and, while the ORM still runs on GORM, the GORM
// session that shares the writer pool — so a connection costs one writer pool and, when it is
// configured with replicas, one reader pool.
//
// Read/write routing lives here: a statement that only reads rows runs on the reader pool and every
// other one on the writer. GORM's dbresolver keeps supplying replicas for the statements that still
// run through the ORM session.
//
// A replica whose entry carries neither a database nor a dsn has no dialector and cannot be opened;
// it is skipped, with a warning, so an unusable first entry neither fails the connection nor masks a
// valid replica behind it: reads run on the first replica that can serve them, or on the writer when
// none can. Only the first usable replica serves compiled reads, and because GORM's dbresolver opens
// that same dialector again for its own statements, the first usable replica holds two pools at once
// and can reach twice database.pool.max_open_conns while the ORM is still executing.
type Connection struct {
	name       string
	driver     string
	writer     *sql.DB
	reader     *sql.DB
	instance   *gormio.DB
	instrument *instrumentationdatabase.Instrument
}

// readPools caches a connection's reader pool by connection name. The ORM holds a GORM session
// rather than a Connection, so this package-level lookup is what lets its compiled reads reach the
// same reader the facades execute on.
var readPools sync.Map

// Open returns the connection for a configured pool, building and caching it on first use.
//
// It returns a nil connection, without an error, when the connection carries no database name:
// that is the state of a fresh application before any credentials exist, and callers treat it as
// "no database" rather than as a failure.
func Open(config config.Config, logger contractslogger.Logger, pool database.Pool, connection string, telemetryResolver contractstelemetry.Resolver) (*Connection, error) {
	instance, instrument, err := BuildGorm(config, logger, pool, connection, telemetryResolver)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, nil
	}

	// The pool is the one GORM opened, reached through its base connection. Sharing it keeps the
	// framework and the ORM on a single pool.
	writer, err := instance.DB()
	if err != nil {
		return nil, err
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
		instance:   instance,
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

// Gorm returns the GORM session sharing this connection's pool. It exists so the ORM can keep
// executing while it is migrated onto the framework's own builder; nothing new should call it.
func (r *Connection) Gorm() *gormio.DB {
	return r.instance
}

// Instrument returns the telemetry instrument bound to this connection, or nil when database
// telemetry is disabled.
func (r *Connection) Instrument() *instrumentationdatabase.Instrument {
	return r.instrument
}

// openReader opens, on first use, the pool the framework's compiled reads run on for a connection:
// the first replica its driver pool carries that can serve reads. A pool without replicas has no
// reader, and nil is returned so callers fall back to the writer.
//
// A replica entry the driver could not turn into a dialector — one carrying neither a database nor a
// dsn — cannot serve reads and is skipped, with a warning, for the next usable replica; only when no
// entry is usable does the connection fall back to its writer. Skipping rather than stopping at the
// first entry keeps a valid replica configured behind a misconfigured one serving reads, and the
// warning makes the misconfiguration visible without failing BuildGorm/Open, and with it application
// boot. Laravel is equally lenient, inheriting the connection's own settings for an incomplete read
// block rather than raising. The pool is opened once per connection and cached.
func openReader(config config.Config, pool database.Pool, connection string) (*sql.DB, error) {
	if len(pool.Readers) == 0 {
		return nil, nil
	}

	if cached, ok := readPools.Load(connection); ok {
		return cached.(*sql.DB), nil
	}

	readerDialector := firstUsableDialector(connection, pool.Readers)
	if readerDialector == nil {
		return nil, nil
	}

	// Opening the reader's dialector is how a driver module turns its configuration into a pool;
	// the GORM session it yields is discarded, only its base pool is kept.
	instance, err := gormio.Open(readerDialector, &gormio.Config{DisableAutomaticPing: true})
	if err != nil {
		return nil, err
	}

	sqlDB, err := instance.DB()
	if err != nil {
		return nil, err
	}

	// The reader carries the same limits as the writer.
	sqlDB.SetMaxIdleConns(config.GetInt("database.pool.max_idle_conns", 10))
	sqlDB.SetMaxOpenConns(config.GetInt("database.pool.max_open_conns", 100))
	sqlDB.SetConnMaxIdleTime(config.GetDuration("database.pool.conn_max_idletime", 3600) * time.Second)
	sqlDB.SetConnMaxLifetime(config.GetDuration("database.pool.conn_max_lifetime", 3600) * time.Second)

	readPools.Store(connection, sqlDB)

	return sqlDB, nil
}

// firstUsableDialector returns the dialector of the first replica entry a driver could open, warning
// about each entry it skips. An entry whose Dialector is nil — one carrying neither a database nor a
// dsn — cannot serve reads, so it is skipped rather than treated as "no replica": that would hide the
// misconfiguration and let an unusable first entry mask a valid replica configured behind it. It
// returns nil when no entry can serve reads, leaving the caller to fall back to the writer.
func firstUsableDialector(connection string, readers []database.Config) gormio.Dialector {
	for index, reader := range readers {
		if reader.Dialector != nil {
			return reader.Dialector
		}

		color.Warningln(fmt.Sprintf("database: read connection %s entry %d cannot serve reads because its driver produced no dialector; skipping it", connection, index))
	}

	return nil
}

// Reader returns the reader pool cached for a connection name, or nil when the connection has no
// usable replica — either none configured or none openable. It exists so the ORM, which
// holds only a GORM session, can resolve the pool the facades execute their reads on; callers fall
// back to the writer on nil.
func Reader(connection string) *sql.DB {
	if cached, ok := readPools.Load(connection); ok {
		return cached.(*sql.DB)
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

		if pool, ok := value.(*sql.DB); ok {
			_ = pool.Close()
		}

		return true
	})
}
