package tests

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	nativemysql "github.com/go-sql-driver/mysql"
	contractsconfig "github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsdocker "github.com/gradii/framework/contracts/testing/docker"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/process"
	supportdocker "github.com/gradii/framework/support/docker"
	testingdocker "github.com/gradii/framework/testing/docker"
	"github.com/gradii/mysql"
	mysqlconfig "github.com/gradii/mysql/contracts"
	"github.com/gradii/postgres"
	postgresconfig "github.com/gradii/postgres/contracts"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
	sqlserverconfig "github.com/gradii/sqlserver/contracts"
	"github.com/jackc/pgx/v5"
	"github.com/microsoft/go-mssqldb/msdsn"
)

var (
	fixtureDatabases sync.Map
)

// databaseFixture keeps test provisioning outside the runtime database modules.
// Containers are deliberately shared by Container; this fixture owns only the
// databases it creates, and never stops a reused server.
type databaseFixture struct {
	databaseConfig contractsdocker.DatabaseConfig
	config         contractsconfig.Config
	connection     string
	imageDriver    contractsdocker.ImageDriver
	initialError   error
	external       bool
}

var _ contractsdocker.DatabaseDriver = (*databaseFixture)(nil)

func newDatabaseFixture(driver, name, username, password string, config contractsconfig.Config, connection string) *databaseFixture {
	r := &databaseFixture{
		databaseConfig: contractsdocker.DatabaseConfig{Driver: driver, Host: "127.0.0.1", Database: name, Username: username, Password: password},
		config:         config, connection: connection,
	}
	var image contractsdocker.Image
	switch driver {
	case mysql.Name:
		r.databaseConfig.Port = 3306
		image = contractsdocker.Image{Repository: "mysql", Tag: "latest", ExposedPorts: []string{"3306"}, Env: []string{"MYSQL_ROOT_PASSWORD=" + password, "MYSQL_DATABASE=" + name}}
		if username != "root" {
			image.Env = append(image.Env, "MYSQL_USER="+username, "MYSQL_PASSWORD="+password)
		}
	case postgres.Name:
		r.databaseConfig.Port = 5432
		image = contractsdocker.Image{Repository: "postgres", Tag: "latest", ExposedPorts: []string{"5432"}, Env: []string{"POSTGRES_USER=" + username, "POSTGRES_PASSWORD=" + password, "POSTGRES_DB=" + name}, Args: []string{"-c", "max_connections=1000"}}
	case sqlserver.Name:
		r.databaseConfig.Port = 1433
		image = contractsdocker.Image{Repository: "mcr.microsoft.com/mssql/server", Tag: "2022-latest", ExposedPorts: []string{"1433"}, Env: []string{"ACCEPT_EULA=Y", "MSSQL_SA_PASSWORD=" + password, "MSSQL_MEMORY_LIMIT_MB=2048"}}
	case sqlite.Name:
		return newSqliteFixture(name)
	default:
		r.initialError = errors.DatabaseFixtureInvalidDriver.Args(driver)
		return r
	}
	r.Image(image)
	r.initialError = r.externalConfig()
	return r
}

func newSqliteFixture(name string) *databaseFixture {
	return &databaseFixture{databaseConfig: contractsdocker.DatabaseConfig{Driver: sqlite.Name, Database: name}}
}

func (r *databaseFixture) Build() error {
	if r.initialError != nil {
		return r.initialError
	}
	if r.Driver() == sqlite.Name {
		// Exclusive creation prevents a fixture from adopting an existing file.
		file, err := os.OpenFile(r.databaseConfig.Database, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		fixtureDatabases.Store(r.key(), true)
		return r.Ready()
	}
	if r.external {
		return nil
	}
	port := r.databaseConfig.Port
	if err := r.imageDriver.Build(); err != nil {
		return err
	}
	image := r.imageDriver.Config()
	port, err := strconv.Atoi(supportdocker.ExposedPort(image.ExposedPorts, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return r.Reuse(image.ContainerID, port)
}

func (r *databaseFixture) Config() contractsdocker.DatabaseConfig { return r.databaseConfig }
func (r *databaseFixture) Driver() string                         { return r.databaseConfig.Driver }

func (r *databaseFixture) Database(name string) (contractsdocker.DatabaseDriver, error) {
	if r.Driver() == sqlite.Name {
		child := newSqliteFixture(name)
		return child, child.Build()
	}
	quoted, err := r.quote(name)
	if err != nil {
		return nil, err
	}
	admin, err := r.connect(true)
	if err != nil {
		return nil, err
	}
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quoted); err != nil {
		return nil, err
	}
	child := *r
	child.databaseConfig.Database = name
	fixtureDatabases.Store(child.key(), true)
	switch r.Driver() {
	case mysql.Name:
		user := r.identifier(r.databaseConfig.Username) + "@'%'"
		if _, err := admin.ExecContext(ctx, "GRANT ALL PRIVILEGES ON "+quoted+".* TO "+user); err != nil {
			return nil, err
		}
	case sqlserver.Name:
		if !strings.EqualFold(r.databaseConfig.Username, "sa") {
			user, err := r.quote(r.databaseConfig.Username)
			if err != nil {
				return nil, err
			}
			var exists int
			if err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.server_principals WHERE name = @p1", r.databaseConfig.Username).Scan(&exists); err != nil {
				return nil, err
			}
			if exists == 0 {
				password := "'" + strings.ReplaceAll(r.databaseConfig.Password, "'", "''") + "'"
				if _, err := admin.ExecContext(ctx, "CREATE LOGIN "+user+" WITH PASSWORD = "+password); err != nil {
					return nil, err
				}
			}
			if _, err := admin.ExecContext(ctx, "USE "+quoted+"; CREATE USER "+user+" FOR LOGIN "+user+"; ALTER ROLE db_owner ADD MEMBER "+user); err != nil {
				return nil, err
			}
		}
	}
	return &child, nil
}

func (r *databaseFixture) Fresh() error {
	if _, owned := fixtureDatabases.Load(r.key()); !owned {
		return errors.DatabaseFixtureUnowned.Args(r.databaseConfig.Database)
	}
	db, err := r.connect(false)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if r.Driver() == postgres.Name {
		_, err := db.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
		return err
	}

	var query string
	switch r.Driver() {
	case mysql.Name:
		if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
			return err
		}
		defer db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1")
		query = "SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()"
	case sqlite.Name:
		if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		defer db.ExecContext(ctx, "PRAGMA foreign_keys=ON")
		query = "SELECT name, type FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'"
	case sqlserver.Name:
		// Foreign keys can span tables and schemas, so drop constraints first.
		rows, err := db.QueryContext(ctx, "SELECT OBJECT_SCHEMA_NAME(parent_object_id), OBJECT_NAME(parent_object_id), name FROM sys.foreign_keys")
		if err != nil {
			return err
		}
		var constraints []string
		for rows.Next() {
			var schema, table, name string
			if err := rows.Scan(&schema, &table, &name); err != nil {
				rows.Close()
				return err
			}
			constraints = append(constraints, "ALTER TABLE "+r.identifier(schema)+"."+r.identifier(table)+" DROP CONSTRAINT "+r.identifier(name))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, statement := range constraints {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		query = "SELECT QUOTENAME(SCHEMA_NAME(schema_id)) + '.' + QUOTENAME(name), type FROM sys.objects WHERE type IN ('U', 'V') AND is_ms_shipped = 0 ORDER BY type DESC"
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	var statements []string
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			rows.Close()
			return err
		}
		object := "TABLE"
		if strings.EqualFold(strings.TrimSpace(kind), "view") || strings.TrimSpace(kind) == "V" {
			object = "VIEW"
		}
		if r.Driver() != sqlserver.Name {
			name = r.identifier(name)
		}
		statements = append(statements, "DROP "+object+" "+name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *databaseFixture) Image(image contractsdocker.Image) {
	r.imageDriver = testingdocker.NewImageDriver(image, process.New())
}

func (r *databaseFixture) Ready() error {
	db, err := r.connect(false)
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	if r.config != nil {
		key := "database.connections." + r.connection + ".write"
		writers := r.config.Get(key)
		switch values := writers.(type) {
		case []database.Config:
			copied := append([]database.Config(nil), values...)
			for i := range copied {
				copied[i].Port = r.databaseConfig.Port
			}
			r.config.Add(key, copied)
		case []mysqlconfig.Config:
			copied := append([]mysqlconfig.Config(nil), values...)
			for i := range copied {
				copied[i].Port = r.databaseConfig.Port
			}
			r.config.Add(key, copied)
		case []postgresconfig.Config:
			copied := append([]postgresconfig.Config(nil), values...)
			for i := range copied {
				copied[i].Port = r.databaseConfig.Port
			}
			r.config.Add(key, copied)
		case []sqlserverconfig.Config:
			copied := append([]sqlserverconfig.Config(nil), values...)
			for i := range copied {
				copied[i].Port = r.databaseConfig.Port
			}
			r.config.Add(key, copied)
		default:
			r.config.Add("database.connections."+r.connection+".port", r.databaseConfig.Port)
		}
	}
	return nil
}

func (r *databaseFixture) Reuse(containerID string, port int) error {
	if r.initialError != nil {
		return r.initialError
	}
	if r.external {
		return nil
	}
	if port < 1 || port > 65535 {
		return errors.DatabaseFixtureInvalidPort.Args(port)
	}
	r.databaseConfig.ContainerID, r.databaseConfig.Port = containerID, port
	return nil
}

func (r *databaseFixture) Shutdown() error {
	if r.Driver() != sqlite.Name {
		return nil
	}
	if _, owned := fixtureDatabases.Load(r.key()); !owned {
		if _, err := os.Stat(r.databaseConfig.Database); os.IsNotExist(err) {
			return nil
		}
		return errors.DatabaseFixtureUnowned.Args(r.databaseConfig.Database)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(r.databaseConfig.Database + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	fixtureDatabases.Delete(r.key())
	return nil
}

func (r *databaseFixture) connect(admin bool) (*sql.DB, error) {
	if r.initialError != nil {
		return nil, r.initialError
	}
	cfg := r.databaseConfig
	endpoint := database.Config{Driver: cfg.Driver, Host: cfg.Host, Port: cfg.Port, Database: cfg.Database, Username: cfg.Username, Password: cfg.Password, Charset: "utf8mb4", Timezone: "UTC", Schema: "public", Sslmode: "disable"}
	if admin {
		switch cfg.Driver {
		case mysql.Name:
			endpoint.Username, endpoint.Database = "root", "mysql"
		case postgres.Name:
			endpoint.Database = "postgres"
		case sqlserver.Name:
			endpoint.Username, endpoint.Database = "sa", "master"
		}
	}
	var connector sqldriver.Connector
	var err error
	switch cfg.Driver {
	case mysql.Name:
		connector, err = mysql.NewConnector(endpoint)
	case postgres.Name:
		connector, err = postgres.NewConnector(endpoint)
	case sqlite.Name:
		connector, err = sqlite.NewConnector(endpoint)
	case sqlserver.Name:
		connector, err = sqlserver.NewConnector(endpoint)
	default:
		return nil, errors.DatabaseFixtureInvalidDriver.Args(cfg.Driver)
	}
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	for {
		attempt, stop := context.WithTimeout(ctx, 3*time.Second)
		err = db.PingContext(attempt)
		stop()
		if err == nil {
			return db, nil
		}
		select {
		case <-ctx.Done():
			db.Close()
			return nil, err
		case <-time.After(time.Second):
		}
	}
}

func (r *databaseFixture) quote(name string) (string, error) {
	if name == "" || len(name) > 63 {
		return "", errors.DatabaseFixtureInvalidName.Args(name)
	}
	for _, char := range name {
		if char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return "", errors.DatabaseFixtureInvalidName.Args(name)
		}
	}
	return r.identifier(name), nil
}

func (r *databaseFixture) identifier(name string) string {
	switch r.Driver() {
	case mysql.Name:
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	case sqlserver.Name:
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	default:
		return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
	}
}

func (r *databaseFixture) key() string {
	cfg := r.databaseConfig
	if cfg.Driver == sqlite.Name {
		path, _ := filepath.Abs(cfg.Database)
		return cfg.Driver + ":" + path
	}
	return fmt.Sprintf("%s:%s:%d:%s", cfg.Driver, cfg.Host, cfg.Port, cfg.Database)
}

// DSNs select an existing test server. Database(name) still creates a separate
// database, and never adopts the DSN's database for destructive cleanup.
func (r *databaseFixture) externalConfig() error {
	var env string
	switch r.Driver() {
	case mysql.Name:
		env = "GRADII_TEST_MYSQL_DSN"
	case postgres.Name:
		env = "GRADII_TEST_POSTGRES_DSN"
	case sqlserver.Name:
		env = "GRADII_TEST_SQLSERVER_DSN"
	}
	dsn := os.Getenv(env)
	if dsn == "" {
		return nil
	}
	cfg := &r.databaseConfig
	switch r.Driver() {
	case mysql.Name:
		parsed, err := nativemysql.ParseDSN(dsn)
		if err != nil {
			return err
		}
		host, port, err := net.SplitHostPort(parsed.Addr)
		if err != nil {
			return err
		}
		cfg.Port, err = strconv.Atoi(port)
		if err != nil {
			return err
		}
		cfg.Host, cfg.Username, cfg.Password = host, parsed.User, parsed.Passwd
	case postgres.Name:
		parsed, err := pgx.ParseConfig(dsn)
		if err != nil {
			return err
		}
		cfg.Host, cfg.Port, cfg.Username, cfg.Password = parsed.Host, int(parsed.Port), parsed.User, parsed.Password
	case sqlserver.Name:
		parsed, err := msdsn.Parse(dsn)
		if err != nil {
			return err
		}
		cfg.Host, cfg.Port, cfg.Username, cfg.Password = parsed.Host, int(parsed.Port), parsed.User, parsed.Password
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.DatabaseFixtureInvalidPort.Args(cfg.Port)
	}
	r.external = true
	return nil
}

type fixtureDriver struct{ contractsdriver.Driver }

func withDatabaseFixture(driver contractsdriver.Driver) contractsdriver.Driver {
	return &fixtureDriver{Driver: driver}
}

func (r *fixtureDriver) Dialect() contractsdriver.Dialect {
	return r.Driver.(contractsdriver.DriverWithDialect).Dialect()
}

func (r *fixtureDriver) SetServerVersion(resolve func() (string, error)) {
	if driver, ok := r.Driver.(contractsdriver.DriverWithServerVersion); ok {
		driver.SetServerVersion(resolve)
	}
}

func (r *fixtureDriver) Docker() (contractsdocker.DatabaseDriver, error) {
	writers := r.Driver.Pool().Writers
	if len(writers) == 0 {
		return nil, errors.DatabaseFixtureEmptyPool
	}
	cfg := writers[0]
	return &databaseFixture{databaseConfig: contractsdocker.DatabaseConfig{Driver: cfg.Driver, Host: cfg.Host, Port: cfg.Port, Database: cfg.Database, Username: cfg.Username, Password: cfg.Password}}, nil
}
