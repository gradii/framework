package driver

import (
	"database/sql"
	"fmt"
	"net/url"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	mssqlDriver "github.com/microsoft/go-mssqldb"
	// The SQLite driver registers itself as "sqlite3" and carries the engine as embedded wasm, so
	// no cgo is needed and no shared library has to be found at run time.
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/gradii/framework/contracts/database"
	"github.com/gradii/framework/errors"
)

// sqliteDriverName is the name the SQLite driver registers itself under.
const sqliteDriverName = "sqlite3"

// openDialect opens the pool for one configured connection.
//
// This is the framework's own replacement for the dialector a driver module used to hand over: the
// connection fields in database.Config are enough to build a DSN, so the framework opens the pool
// itself and a driver module is left supplying only its grammar, processor and Docker helper.
//
// A connection with neither a host nor a dsn has nothing to open. That is the state of a fresh
// application before any credentials exist, so it reports (nil, nil) rather than an error.
func openDialect(config database.Config) (*sql.DB, error) {
	switch driverDialect(config.Driver) {
	case "mysql":
		return openMySQL(config)
	case "postgres":
		return openPostgres(config)
	case "sqlserver":
		return openSqlServer(config)
	case "sqlite":
		return openSQLite(config)
	default:
		return nil, errors.DatabaseDialectNotSupported.Args(config.Driver)
	}
}

// mysqlDSN renders the data source name go-sql-driver/mysql parses. An explicit dsn wins; otherwise
// the fields are assembled exactly as the MySQL driver module assembled them, parseTime included,
// because the ORM scans timestamps into time.Time.
func mysqlDSN(config database.Config) string {
	if config.Dsn != "" {
		return config.Dsn
	}
	if config.Host == "" {
		return ""
	}

	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=true&loc=%s&multiStatements=true",
		config.Username, config.Password, config.Host, config.Port, config.Database,
		config.Charset, url.QueryEscape(config.Timezone))
}

func openMySQL(config database.Config) (*sql.DB, error) {
	dsn := mysqlDSN(config)
	if dsn == "" {
		return nil, nil
	}

	parsed, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}

	connector, err := mysqlDriver.NewConnector(parsed)
	if err != nil {
		return nil, err
	}

	return sql.OpenDB(connector), nil
}

// postgresDSN renders the data source name pgx parses, carrying the search path so a model resolves
// against the configured schema rather than whatever the server defaults to.
func postgresDSN(config database.Config) string {
	if config.Dsn != "" {
		return config.Dsn
	}
	if config.Host == "" {
		return ""
	}

	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&timezone=%s&search_path=%s",
		config.Username, config.Password, config.Host, config.Port, config.Database,
		config.Sslmode, config.Timezone, config.Schema)
}

func openPostgres(config database.Config) (*sql.DB, error) {
	dsn := postgresDSN(config)
	if dsn == "" {
		return nil, nil
	}

	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// A migration that adds or removes a column changes a statement's result type, and the server
	// then rejects the cached plan with "cached plan must not change result type". The driver module
	// set simple protocol for this reason; it belongs to the connection, not to the ORM.
	parsed.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	return sql.OpenDB(stdlib.GetConnector(*parsed)), nil
}

// sqlServerDSN renders the data source name go-mssqldb parses.
func sqlServerDSN(config database.Config) string {
	if config.Dsn != "" {
		return config.Dsn
	}
	if config.Host == "" {
		return ""
	}

	return fmt.Sprintf("sqlserver://%s:%s@%s:%d?database=%s&charset=%s&timezone=%s&MultipleActiveResultSets=true",
		config.Username, config.Password, config.Host, config.Port, config.Database,
		config.Charset, config.Timezone)
}

func openSqlServer(config database.Config) (*sql.DB, error) {
	dsn := sqlServerDSN(config)
	if dsn == "" {
		return nil, nil
	}

	connector, err := mssqlDriver.NewConnector(dsn)
	if err != nil {
		return nil, err
	}

	return sql.OpenDB(connector), nil
}

// openSQLite opens a file-backed SQLite database. The driver registers itself with database/sql, so
// the pool is opened by name rather than through a connector.
func openSQLite(config database.Config) (*sql.DB, error) {
	dsn := config.Dsn
	if dsn == "" {
		dsn = config.Database
	}
	if dsn == "" {
		return nil, nil
	}

	return sql.Open(sqliteDriverName, dsn)
}
