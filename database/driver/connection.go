package driver

import (
	"database/sql"

	"github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	instrumentationdatabase "github.com/gradii/framework/telemetry/instrumentation/database"
)

// Connection is the framework's handle on one configured database connection. It owns the pools the
// framework executes its compiled statements through — one writer pool and, when the connection is
// configured with replicas, one reader pool.
//
// Read/write routing lives here: a statement that only reads rows runs on the reader pool and every
// other one on the writer. The pools are opened by this package from the connection's own fields, so
// no ORM session sits between the framework and the database.
type Connection struct {
	identity   connectionIdentity
	resources  *cachedConnection
	name       string
	driver     string
	dialect    contractsdriver.Dialect
	writer     *sql.DB
	reader     *sql.DB
	instrument *instrumentationdatabase.Instrument
}

// Open returns the connection for a configured pool, building and caching it on first use.
//
// It returns a nil connection, without an error, when the connection carries no database name:
// that is the state of a fresh application before any credentials exist, and callers treat it as
// "no database" rather than as a failure.
func Open(config config.Config, logger contractslogger.Logger, pool database.Pool, connection string, telemetryResolver contractstelemetry.Resolver, dialects ...contractsdriver.Dialect) (*Connection, error) {
	resources, err := buildConnection(config, logger, pool, connection, telemetryResolver, dialects...)
	if err != nil {
		return nil, err
	}
	if resources == nil {
		return nil, nil
	}

	driverName := ""
	if len(pool.Writers) > 0 {
		driverName = pool.Writers[0].Driver
	}
	var dialect contractsdriver.Dialect
	if len(dialects) > 0 {
		dialect = dialects[0]
	}

	return &Connection{
		identity:   poolConnectionIdentity(pool, connection),
		resources:  resources,
		name:       connection,
		driver:     driverName,
		dialect:    dialect,
		writer:     resources.db,
		reader:     resources.reader,
		instrument: resources.instrument,
	}, nil
}

// Name returns the configured connection name this handle was opened for.
func (r *Connection) Name() string {
	return r.name
}

// IsCurrent reports whether this handle still belongs to the cached generation. Facade registries
// use it to discard handles invalidated through another DB or ORM entry point.
func (r *Connection) IsCurrent() bool {
	current, ok := connectionToDB.Load(r.identity)
	return ok && current == r.resources
}

// Purge closes this logical connection's current resources, leaving other aliases untouched. A handle
// whose generation was already replaced is a no-op, because purging by identity alone would close the
// pools another Open published in the meantime.
func (r *Connection) Purge() error {
	if !r.IsCurrent() {
		return nil
	}

	return purgeConnection(r.identity)
}

// Driver returns the driver name the connection is configured with.
func (r *Connection) Driver() string {
	return r.driver
}

func (r *Connection) SupportsServerVersion() bool {
	_, ok := r.dialect.(contractsdriver.DialectWithServerVersion)
	return ok
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

// Reader returns a logical connection's cached replica, or nil when it has none. Supply its pool
// when the driver's configured source has a different name. Managed callers use Connection.Reader.
func Reader(connection string, pools ...database.Pool) *sql.DB {
	if cached, ok := readPools.Load(namedConnectionIdentity(connection, pools)); ok {
		return cached.(cachedReader).pool
	}

	return nil
}
