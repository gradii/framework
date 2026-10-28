package driver

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/color"
	instrumentationdatabase "github.com/gradii/framework/telemetry/instrumentation/database"
)

// connectionIdentity separates the requested name from the driver's configured source. Two aliases
// have independent lifecycles, while DB and ORM openings of the same alias share its resources.
type connectionIdentity struct {
	configured string
	logical    string
}

func poolConnectionIdentity(pool database.Pool, logical string) connectionIdentity {
	configured := logical
	if len(pool.Writers) > 0 && pool.Writers[0].Connection != "" {
		configured = pool.Writers[0].Connection
	}
	return connectionIdentity{configured: configured, logical: logical}
}

func namedConnectionIdentity(logical string, pools []database.Pool) connectionIdentity {
	if len(pools) > 0 {
		return poolConnectionIdentity(pools[0], logical)
	}
	return poolConnectionIdentity(database.Pool{}, logical)
}

// cachedConnection is one generation of a logical connection. Its writer, reader, telemetry and
// version cache are published together so a concurrent purge cannot mix generations during Open.
type cachedConnection struct {
	db         *sql.DB
	reader     *sql.DB
	instrument *instrumentationdatabase.Instrument
	versionMu  sync.Mutex
	version    string
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
func BuildConnection(config config.Config, logger contractslogger.Logger, pool database.Pool, connection string, telemetryResolver contractstelemetry.Resolver, dialects ...contractsdriver.Dialect) (*sql.DB, *instrumentationdatabase.Instrument, error) {
	cached, err := buildConnection(config, logger, pool, connection, telemetryResolver, dialects...)
	if err != nil || cached == nil {
		return nil, nil, err
	}
	return cached.db, cached.instrument, nil
}

func buildConnection(config config.Config, _ contractslogger.Logger, pool database.Pool, connection string, telemetryResolver contractstelemetry.Resolver, dialects ...contractsdriver.Dialect) (*cachedConnection, error) {
	identity := poolConnectionIdentity(pool, connection)
	if cached, ok := connectionToDB.Load(identity); ok {
		return cached.(*cachedConnection), nil
	}
	connectionToDBLock.Lock()
	defer connectionToDBLock.Unlock()
	if cached, ok := connectionToDB.Load(identity); ok {
		return cached.(*cachedConnection), nil
	}

	if len(pool.Writers) == 0 {
		return nil, errors.DatabaseConfigNotFound
	}

	if pool.Writers[0].Database == "" && pool.Writers[0].Dsn == "" && pool.Writers[0].Connector == nil {
		return nil, nil
	}

	writer, err := openPool(pool.Writers[0], dialects...)
	if err != nil {
		return nil, err
	}
	if writer == nil {
		return nil, nil
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
	reader, err := openReaderForIdentity(config, pool, identity, dialects...)
	if err != nil {
		_ = writer.Close()
		instrument.Shutdown()

		return nil, err
	}

	cached := &cachedConnection{db: writer, reader: reader, instrument: instrument}
	connectionToDB.Store(identity, cached)
	return cached, nil
}

// Instrument returns the telemetry instrument cached for a logical connection. Supply its pool
// when the driver's configured source has a different name. Managed callers use Connection.Instrument.
func Instrument(connection string, pools ...database.Pool) *instrumentationdatabase.Instrument {
	cached, ok := connectionToDB.Load(namedConnectionIdentity(connection, pools))
	if !ok {
		return nil
	}

	return cached.(*cachedConnection).instrument
}

// openPool builds one connection's pool: the connector the driver supplied when there is one, and
// otherwise the framework's own dialect opener.
func openPool(connection database.Config, dialects ...contractsdriver.Dialect) (*sql.DB, error) {
	if connection.Connector != nil {
		return sql.OpenDB(connection.Connector), nil
	}
	if len(dialects) > 0 && dialects[0] != nil {
		connector, err := dialects[0].NewConnector(connection)
		if err != nil {
			return nil, err
		}
		if connector == nil {
			return nil, errors.DatabaseConfigNotFound
		}
		return sql.OpenDB(connector), nil
	}

	return nil, errors.DatabaseDialectRequired.Args(connection.Driver)
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
func openReader(config config.Config, pool database.Pool, connection string, dialects ...contractsdriver.Dialect) (*sql.DB, error) {
	connectionToDBLock.Lock()
	defer connectionToDBLock.Unlock()
	return openReaderForIdentity(config, pool, poolConnectionIdentity(pool, connection), dialects...)
}

// The caller holds connectionToDBLock until the writer and its reader are published together.
func openReaderForIdentity(config config.Config, pool database.Pool, identity connectionIdentity, dialects ...contractsdriver.Dialect) (*sql.DB, error) {
	if len(pool.Readers) == 0 {
		return nil, nil
	}

	if cached, ok := readPools.Load(identity); ok {
		return cached.(cachedReader).pool, nil
	}

	var failures []error
	// Laravel shuffles candidate hosts and tries the next after a connection failure.
	for _, index := range rand.Perm(len(pool.Readers)) {
		candidate := pool.Readers[index]
		if candidate.Connector == nil && candidate.Host == "" && candidate.Dsn == "" && driverDialect(candidate.Driver) != "sqlite" {
			continue
		}

		reader, err := openPool(candidate, dialects...)
		if err != nil {
			failures = append(failures, err)

			continue
		}
		if reader == nil {
			color.Warningln(fmt.Sprintf("database: read connection %s entry %d cannot serve reads because it carries no host and no dsn; skipping it", identity.logical, index))

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
		readPools.Store(identity, cachedReader{pool: reader})

		return reader, nil
	}

	return nil, errors.Join(failures...)
}

func drainConnections() ([]*cachedConnection, []*sql.DB) {
	connectionToDBLock.Lock()
	defer connectionToDBLock.Unlock()

	var stale []*cachedConnection
	connectionToDB.Range(func(key, value any) bool {
		if cached, ok := value.(*cachedConnection); ok {
			stale = append(stale, cached)
		}
		connectionToDB.Delete(key)

		return true
	})

	var readers []*sql.DB
	readPools.Range(func(key, value any) bool {
		readers = append(readers, value.(cachedReader).pool)
		readPools.Delete(key)
		return true
	})
	return stale, readers
}

// ResetConnections drops the cached connections and unregisters their telemetry, leaving the pools
// open for callers that may still hold them. Reader pools are dropped from the cache as well, and
// likewise left open.
func ResetConnections() {
	connections, _ := drainConnections()
	for _, cached := range connections {
		cached.instrument.Shutdown()
	}
}

// CloseConnections drops the cached connections, unregisters their telemetry and closes the pools,
// the reader pools included. Call it only when the connections are idle.
func CloseConnections() {
	connections, readers := drainConnections()
	for _, cached := range connections {
		cached.instrument.Shutdown()
		if cached.db == nil {
			continue
		}
		if err := cached.db.Close(); err != nil {
			color.Warningln("database: close connection: " + err.Error())
		}
	}

	for _, reader := range readers {
		_ = reader.Close()
	}
}
