package tests

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	nativemysql "github.com/go-sql-driver/mysql"
	frameworkconfig "github.com/gradii/framework/config"
	"github.com/gradii/framework/contracts/database"
	contractsdb "github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/contracts/database/driver"
	contractsschema "github.com/gradii/framework/contracts/database/schema"
	frameworkdb "github.com/gradii/framework/database/db"
	frameworkschema "github.com/gradii/framework/database/schema"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/testing/utils"
	"github.com/gradii/mysql"
	mysqlconfig "github.com/gradii/mysql/contracts"
	"github.com/gradii/postgres"
	postgresconfig "github.com/gradii/postgres/contracts"
	"github.com/stretchr/testify/require"
)

func newScopeReviewSchema(t *testing.T, kind string, settings map[string]any) (*frameworkschema.Schema, contractsdb.DB) {
	t.Helper()
	name := fmt.Sprintf("scope_review_%d", time.Now().UnixNano())
	config := frameworkconfig.NewApplication("")
	config.Add("database.default", name)
	config.Add("database.pool", map[string]any{"max_open_conns": 2, "max_idle_conns": 2})
	settings["driver"] = kind
	settings["via"] = func() (driver.Driver, error) {
		if kind == mysql.Name {
			return mysql.NewMysql(config, nil, nil, name), nil
		}
		return postgres.NewPostgres(config, nil, nil, name), nil
	}
	config.Add("database.connections."+name, settings)
	log := utils.NewTestLog()
	db, err := frameworkdb.BuildDB(context.Background(), config, log, name, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Purge()) })
	databaseDriver, err := settings["via"].(func() (driver.Driver, error))()
	require.NoError(t, err)
	s, err := frameworkschema.NewSchema(config, log, nil, databaseDriver, nil, db)
	require.NoError(t, err)
	return s, db
}

func TestDriverScopeReviewMySQL(t *testing.T) {
	inside := buildDriverFixture(t, mysql.Name)
	outside := buildDriverFixture(t, mysql.Name)
	admin, err := inside.connect(true)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	// A regression must be unable to touch any database other than these two
	// disposable fixtures, even if it accidentally queries every visible schema.
	user := fmt.Sprintf("scope_%d", time.Now().UnixNano())
	password := fmt.Sprintf("Review_%d!", time.Now().UnixNano())
	_, err = admin.Exec("create user '" + user + "'@'%' identified by '" + password + "'")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("drop user '" + user + "'@'%'")
		require.NoError(t, err)
	})
	for _, fixture := range []*databaseFixture{inside, outside} {
		_, err = admin.Exec("grant all privileges on " + fixture.identifier(fixture.Config().Database) + ".* to '" + user + "'@'%'")
		require.NoError(t, err)
	}
	dsn := func(name string) string {
		cfg := nativemysql.NewConfig()
		cfg.Net, cfg.Addr = "tcp", net.JoinHostPort(inside.Config().Host, strconv.Itoa(inside.Config().Port))
		cfg.User, cfg.Passwd, cfg.DBName = user, password, name
		return cfg.FormatDSN()
	}
	insideName, outsideName := inside.Config().Database, outside.Config().Database
	quote := inside.identifier
	create := func(t *testing.T, name string) {
		t.Helper()
		_, err := admin.Exec("create table " + quote(name) + ".review_rows (id integer primary key)")
		require.NoError(t, err)
		_, err = admin.Exec("insert into " + quote(name) + ".review_rows values (1)")
		require.NoError(t, err)
		_, err = admin.Exec("create definer='" + user + "'@'%' view " + quote(name) + ".review_view as select id from " + quote(name) + ".review_rows")
		require.NoError(t, err)
	}
	assertObjects := func(t *testing.T, name string, want int) {
		t.Helper()
		var count int
		require.NoError(t, admin.QueryRow("select count(*) from information_schema.tables where table_schema=? and table_name in ('review_rows','review_view')", name).Scan(&count))
		require.Equal(t, want, count)
	}
	create(t, outsideName)
	for _, mode := range []string{"dsn only", "dsn overrides fields", "connector overrides fields"} {
		t.Run(mode, func(t *testing.T) {
			create(t, insideName)
			settings := map[string]any{"dsn": dsn(insideName), "read": []mysqlconfig.Config{{Dsn: dsn(outsideName)}}}
			if mode != "dsn only" {
				settings["database"] = outsideName
			}
			if mode == "connector overrides fields" {
				connector, err := mysql.NewConnector(database.Config{Dsn: dsn(insideName)})
				require.NoError(t, err)
				settings["connector"], settings["dsn"] = connector, dsn(outsideName)
			}
			s, _ := newScopeReviewSchema(t, mysql.Name, settings)
			require.NoError(t, s.DropAllViews())
			require.NoError(t, s.DropAllTables())
			assertObjects(t, insideName, 0)
			assertObjects(t, outsideName, 2)
		})
	}
	t.Run("unknown active database never emits DDL", func(t *testing.T) {
		s, db := newScopeReviewSchema(t, mysql.Name, map[string]any{"dsn": dsn(""), "database": outsideName})
		var ddl []string
		db.BeforeExecuting(func(_ context.Context, sql string, _ []any) error {
			if strings.HasPrefix(strings.ToLower(sql), "drop ") {
				ddl = append(ddl, sql)
			}
			return nil
		})
		require.ErrorIs(t, s.DropAllViews(), errors.SchemaCurrentNamespaceUnavailable)
		require.ErrorIs(t, s.DropAllTables(), errors.SchemaCurrentNamespaceUnavailable)
		require.Empty(t, ddl)
		assertObjects(t, outsideName, 2)
	})
	t.Run("already pinned session owns current database", func(t *testing.T) {
		create(t, insideName)
		s, _ := newScopeReviewSchema(t, mysql.Name, map[string]any{"dsn": dsn(insideName)})
		require.NoError(t, s.WithWriteConnection(func(scoped contractsschema.Schema) error {
			require.NoError(t, scoped.Sql("use "+quote(outsideName)))
			require.NoError(t, scoped.DropAllViews())
			return scoped.DropAllTables()
		}))
		assertObjects(t, outsideName, 0)
		assertObjects(t, insideName, 2)
	})
}

func scopePostgresDSN(fixture *databaseFixture, path string) string {
	cfg := fixture.Config()
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), Path: "/" + cfg.Database, User: url.UserPassword(cfg.Username, cfg.Password)}
	values := url.Values{"sslmode": {"disable"}, "search_path": {path}}
	u.RawQuery = values.Encode()
	return u.String()
}

func TestDriverScopeReviewPostgres(t *testing.T) {
	fixture := buildDriverFixture(t, postgres.Name)
	admin := connectDriverFixture(t, fixture)
	quote := fixture.identifier
	var username string
	require.NoError(t, admin.QueryRow("select current_user").Scan(&username))
	for _, name := range []string{"review_tenant", "tenant,quoted", username} {
		_, err := admin.Exec("create schema if not exists " + quote(name))
		require.NoError(t, err)
	}
	create := func(name, prefix string) {
		qualified := func(suffix string) string { return quote(name) + "." + quote(prefix+suffix) }
		_, err := admin.Exec("create table " + qualified("_rows") + " (id integer primary key)")
		require.NoError(t, err)
		_, err = admin.Exec("create view " + qualified("_view") + " as select id from " + qualified("_rows"))
		require.NoError(t, err)
		_, err = admin.Exec("create type " + qualified("_enum") + " as enum ('a','b')")
		require.NoError(t, err)
	}
	assertObjects := func(name, prefix string, want int) {
		var count int
		require.NoError(t, admin.QueryRow("select (select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace where n.nspname=$1 and c.relname in ($2,$3)) + (select count(*) from pg_type t join pg_namespace n on n.oid=t.typnamespace where n.nspname=$1 and t.typname=$4)", name, prefix+"_rows", prefix+"_view", prefix+"_enum").Scan(&count))
		require.Equal(t, want, count)
	}
	create("public", "keep")
	for _, tc := range []struct {
		name, path string
		schemas    []string
		connector  bool
	}{
		{name: "dsn overrides stale config", path: "review_tenant", schemas: []string{"review_tenant"}},
		{name: "connector owns search path", path: "review_tenant", schemas: []string{"review_tenant"}, connector: true},
		{name: "dsn username expands user", path: `"$user"`, schemas: []string{username}},
		{name: "multiple and quoted schemas", path: `review_tenant, "tenant,quoted"`, schemas: []string{"review_tenant", "tenant,quoted"}},
		{name: "no effective schema", path: "review_nonexistent_schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range tc.schemas {
				create(name, "review")
			}
			settings := map[string]any{"dsn": scopePostgresDSN(fixture, tc.path), "schema": "public", "search_path": "public", "read": []postgresconfig.Config{{Dsn: scopePostgresDSN(fixture, "public")}}}
			if tc.path == `"$user"` {
				settings["search_path"] = "$user"
			}
			if tc.connector {
				connector, err := postgres.NewConnector(database.Config{Dsn: scopePostgresDSN(fixture, tc.path)})
				require.NoError(t, err)
				settings["connector"], settings["dsn"] = connector, scopePostgresDSN(fixture, "public")
			}
			s, db := newScopeReviewSchema(t, postgres.Name, settings)
			var ddl []string
			db.BeforeExecuting(func(_ context.Context, sql string, _ []any) error {
				if strings.HasPrefix(strings.ToLower(sql), "drop ") {
					ddl = append(ddl, sql)
				}
				return nil
			})
			names, err := s.GetCurrentSchemaListing()
			require.NoError(t, err)
			require.Equal(t, len(tc.schemas), len(names))
			require.ElementsMatch(t, tc.schemas, names)
			require.NoError(t, s.DropAllViews())
			require.NoError(t, s.DropAllTables())
			require.NoError(t, s.DropAllTypes())
			for _, name := range tc.schemas {
				assertObjects(name, "review", 0)
			}
			assertObjects("public", "keep", 3)
			if len(tc.schemas) == 0 {
				require.Empty(t, ddl)
			}
		})
	}
	t.Run("pinned session search path change", func(t *testing.T) {
		create("review_tenant", "review")
		s, _ := newScopeReviewSchema(t, postgres.Name, map[string]any{"dsn": scopePostgresDSN(fixture, "public")})
		require.NoError(t, s.WithWriteConnection(func(scoped contractsschema.Schema) error {
			require.NoError(t, scoped.Sql("set search_path to review_tenant"))
			require.NoError(t, scoped.DropAllViews())
			require.NoError(t, scoped.DropAllTables())
			return scoped.DropAllTypes()
		}))
		assertObjects("review_tenant", "review", 0)
		assertObjects("public", "keep", 3)
	})
}
