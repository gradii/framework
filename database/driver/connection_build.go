package driver

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/config"
	"github.com/goravel/framework/contracts/database"
	contractslogger "github.com/goravel/framework/contracts/database/logger"
	contractstelemetry "github.com/goravel/framework/contracts/telemetry"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/color"
	instrumentationdatabase "github.com/goravel/framework/telemetry/instrumentation/database"
)

// cachedConnection holds an opened writer pool and the telemetry instrument bound to it.
type cachedConnection struct {
	db         *sql.DB
	instrument *instrumentationdatabase.Instrument
}

var (
	connectionToDB     sync.Map
	connectionToDBLock sync.Mutex
	pingWarning        sync.Once
)

// readPools caches a connection's reader pool by connection name, so the ORM and the facades resolve
// the same replica whichever entry point built the connection.
var readPools sync.Map

type cachedReader struct {
	pool *sql.DB
}

// BuildConnection opens and caches a connection's writer pool.
//
// The framework opens the pool itself from the connection's fields, which is what the dialector a
// driver module used to hand over did. A connection with no database name opens nothing and reports
// (nil, nil, nil): that is a fresh application before any credentials exist, and callers read it as
// "no database" rather than as a failure.
func BuildConnection(config config.Config, logger contractslogger.Logger, pool database.Pool, connection string, telemetryResolver contractstelemetry.Resolver) (*sql.DB, *instrumentationdatabase.Instrument, error) {
	if cached, ok := connectionToDB.Load(connection); ok {
		c := cached.(cachedConnection)
		return c.db, c.instrument, nil
	}

	if len(pool.Writers) == 0 {
		return nil, nil, errors.DatabaseConfigNotFound
	}

	if pool.Writers[0].Database == "" {
		return nil, nil, nil
	}

	connectionToDBLock.Lock()
	defer connectionToDBLock.Unlock()

	if cached, ok := connectionToDB.Load(connection); ok {
		c := cached.(cachedConnection)
		return c.db, c.instrument, nil
	}

	writer, err := openPool(pool.Writers[0])
	if err != nil {
		return nil, nil, err
	}
	if writer == nil {
		return nil, nil, nil
	}

	// A pool that cannot answer a ping is still returned: the connection is opened lazily on first
	// use anyway, and the warning names the misconfiguration without failing application boot.
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := writer.PingContext(pingCtx); err != nil {
		pingWarning.Do(func() {
			color.Warningln(err.Error())
		})
	}
	cancel()

	writer.SetMaxIdleConns(config.GetInt("database.pool.max_idle_conns", 10))
	writer.SetMaxOpenConns(config.GetInt("database.pool.max_open_conns", 100))
	writer.SetConnMaxIdleTime(config.GetDuration("database.pool.conn_max_idletime", 3600) * time.Second)
	writer.SetConnMaxLifetime(config.GetDuration("database.pool.conn_max_lifetime", 3600) * time.Second)

	var instrument *instrumentationdatabase.Instrument
	if telemetryResolver != nil && instrumentationdatabase.Enabled(config) {
		instrument = instrumentationdatabase.NewInstrument(pool, connection, telemetryResolver)
		instrument.SetDB(writer)
	}

	// The reader pool is opened next to the writer's, so the facades and the ORM resolve the same one
	// whichever entry point built this connection.
	if _, err := openReader(config, pool, connection); err != nil {
		_ = writer.Close()
		instrument.Shutdown()

		return nil, nil, err
	}

	connectionToDB.Store(connection, cachedConnection{db: writer, instrument: instrument})

	return writer, instrument, nil
}

// Instrument returns the telemetry instrument cached for a connection, or nil when telemetry is
// disabled or the connection has not been opened yet. The ORM's executor reads it while running a
// compiled statement, so it does not have to carry the instrument through every query it spawns.
func Instrument(connection string) *instrumentationdatabase.Instrument {
	cached, ok := connectionToDB.Load(connection)
	if !ok {
		return nil
	}

	return cached.(cachedConnection).instrument
}

// openPool builds one connection's pool: the connector the driver supplied when there is one, and
// otherwise the framework's own dialect opener.
func openPool(connection database.Config) (*sql.DB, error) {
	if connection.Connector != nil {
		return sql.OpenDB(connection.Connector), nil
	}

	return openDialect(connection)
}

// openReader opens, on first use, the pool the framework's compiled reads run on for a connection:
// a reachable replica selected from a shuffled candidate list. A pool without replicas has no
// reader, and nil is returned so callers fall back to the writer.
//
// A replica entry that cannot be opened is skipped, with a warning, for the next usable one; only
// when no entry is usable does the connection fall back to its writer. Skipping rather than stopping
// at the first entry keeps a valid replica configured behind a misconfigured one serving reads, and
// the warning makes the misconfiguration visible without failing BuildConnection and with it
// application boot. Laravel is equally lenient, inheriting the connection's own settings for an
// incomplete read block rather than raising.
func openReader(config config.Config, pool database.Pool, connection string) (*sql.DB, error) {
	if len(pool.Readers) == 0 {
		return nil, nil
	}

	if cached, ok := readPools.Load(connection); ok {
		return cached.(cachedReader).pool, nil
	}

	var failures []error
	// Laravel shuffles candidate hosts and tries the next after a connection failure.
	for _, index := range rand.Perm(len(pool.Readers)) {
		candidate := pool.Readers[index]

		reader, err := openPool(candidate)
		if err != nil {
			failures = append(failures, err)

			continue
		}
		if reader == nil {
			color.Warningln(fmt.Sprintf("database: read connection %s entry %d cannot serve reads because it carries no host and no dsn; skipping it", connection, index))

			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = reader.PingContext(ctx)
		cancel()
		if err != nil {
			_ = reader.Close()
			failures = append(failures, err)

			continue
		}

		reader.SetMaxIdleConns(config.GetInt("database.pool.max_idle_conns", 10))
		reader.SetMaxOpenConns(config.GetInt("database.pool.max_open_conns", 100))
		reader.SetConnMaxIdleTime(config.GetDuration("database.pool.conn_max_idletime", 3600) * time.Second)
		reader.SetConnMaxLifetime(config.GetDuration("database.pool.conn_max_lifetime", 3600) * time.Second)
		readPools.Store(connection, cachedReader{pool: reader})

		return reader, nil
	}

	return nil, errors.Join(failures...)
}

func drainConnections() []cachedConnection {
	connectionToDBLock.Lock()
	defer connectionToDBLock.Unlock()

	var stale []cachedConnection
	connectionToDB.Range(func(key, value any) bool {
		if cached, ok := value.(cachedConnection); ok {
			stale = append(stale, cached)
		}
		connectionToDB.Delete(key)

		return true
	})

	return stale
}

// ResetConnections drops the cached connections and unregisters their telemetry, leaving the pools
// open for callers that may still hold them. Reader pools are dropped from the cache as well, and
// likewise left open.
func ResetConnections() {
	for _, cached := range drainConnections() {
		cached.instrument.Shutdown()
	}

	resetReaders()
}

// CloseConnections drops the cached connections, unregisters their telemetry and closes the pools,
// the reader pools included. Call it only when the connections are idle.
func CloseConnections() {
	for _, cached := range drainConnections() {
		cached.instrument.Shutdown()
		if cached.db == nil {
			continue
		}
		if err := cached.db.Close(); err != nil {
			color.Warningln("database: close connection: " + err.Error())
		}
	}

	closeReaders()
}
