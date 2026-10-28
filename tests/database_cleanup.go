package tests

import (
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"sync"

	"github.com/gradii/framework/contracts/testing/docker"
	"github.com/gradii/framework/support/color"
	"github.com/gradii/framework/support/file"
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
)

// A test context gets a database of its own, and the driver modules create it
// with no way to drop it again. The container outlives the test binary, so
// without this every run leaves about a hundred databases behind: measured on
// 2026-10-19, one container held 6245 `goravel_*` databases after a few days,
// which slows every CREATE DATABASE and, through catalog bloat, every
// checkpoint. The records below name exactly what this process created, so a
// concurrent run's databases are never touched; a pattern match on `goravel_*`
// would delete those too.
var (
	createdDatabasesMu sync.Mutex
	createdDatabases   []docker.DatabaseConfig
)

// databaseNamePattern bounds what cleanup will act on: the names the harness
// generates are `goravel_` plus six random alphanumerics, and only such a name
// may reach a DROP statement.
var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func recordCreatedDatabase(config docker.DatabaseConfig) {
	if config.Database == "" || config.Driver == "" {
		return
	}

	createdDatabasesMu.Lock()
	defer createdDatabasesMu.Unlock()

	createdDatabases = append(createdDatabases, config)
}

// dropCreatedDatabases removes the databases this process created. It runs
// once the tests are over and never fails the run: a database that cannot be
// dropped is logged, because it is debris for the next run rather than a test
// result.
func dropCreatedDatabases() {
	createdDatabasesMu.Lock()
	configs := createdDatabases
	createdDatabases = nil
	createdDatabasesMu.Unlock()

	for _, config := range configs {
		if err := dropDatabase(config); err != nil {
			color.Warningf("could not drop the test database %q: %v", config.Database, err)
		}
	}
}

func dropDatabase(config docker.DatabaseConfig) error {
	if !databaseNamePattern.MatchString(config.Database) {
		return fmt.Errorf("refusing to drop a database whose name is not a generated one: %q", config.Database)
	}

	switch config.Driver {
	case sqlite.Name:
		// A SQLite context is a file in the package directory.
		return file.Remove(config.Database)
	case postgres.Name:
		return dropPostgresDatabase(config)
	case mysql.Name:
		return dropMysqlDatabase(config)
	case sqlserver.Name:
		return dropSqlserverDatabase(config)
	}

	return nil
}

func dropPostgresDatabase(config docker.DatabaseConfig) error {
	// Connect to the database the tests were provisioned with rather than the
	// one being dropped: a database cannot be dropped from a connection to
	// itself. Terminating its backends first is what `WITH (FORCE)` does, in a
	// form that also runs on a server older than 13.
	dsn := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(config.Username, config.Password),
		Host:     fmt.Sprintf("%s:%d", config.Host, config.Port),
		Path:     "/" + testDatabase,
		RawQuery: "sslmode=disable",
	}

	db, err := sql.Open("pgx", dsn.String())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", config.Database); err != nil {
		return err
	}

	_, err = db.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, config.Database))
	return err
}

func dropMysqlDatabase(config docker.DatabaseConfig) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", config.Username, config.Password, config.Host, config.Port, testDatabase)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", config.Database))
	return err
}

func dropSqlserverDatabase(config docker.DatabaseConfig) error {
	// The SQL Server container is reached as `sa`, and a database cannot be
	// dropped while a connection is open to it, so the statement runs against
	// master and takes the database single-user first.
	dsn := fmt.Sprintf("sqlserver://sa:%s@%s:%d?database=master", config.Password, config.Host, config.Port)

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", config.Database)); err != nil {
		return err
	}

	_, err = db.Exec(fmt.Sprintf("DROP DATABASE [%s]", config.Database))
	return err
}
