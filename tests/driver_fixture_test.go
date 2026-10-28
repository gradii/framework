package tests

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/contracts/database"
	contractsdocker "github.com/gradii/framework/contracts/testing/docker"
	"github.com/gradii/framework/errors"
	mocksconfig "github.com/gradii/framework/mocks/config"
	mocksdocker "github.com/gradii/framework/mocks/testing/docker"
	"github.com/gradii/framework/support/str"
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
)

// These cases port the four drivers' Docker suites to test-only provisioning.
// SQL is executed through their native connectors, and each destructive case
// receives a database created by this test run.
var fixtureDriverCases = []struct {
	name, env, createUsers, foreignID, catalog string
	port                                       int
}{
	{
		name: mysql.Name, env: "GRADII_TEST_MYSQL_DSN", port: 3306,
		createUsers: "CREATE TABLE users (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		foreignID:   "BIGINT UNSIGNED",
		catalog:     "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN ('users', 'posts', 'user_names')",
	},
	{
		name: postgres.Name, env: "GRADII_TEST_POSTGRES_DSN", port: 5432,
		createUsers: "CREATE TABLE users (id SERIAL PRIMARY KEY NOT NULL, name VARCHAR(255) NOT NULL)",
		foreignID:   "INTEGER",
		catalog:     "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('users', 'posts', 'user_names')",
	},
	{
		name:        sqlite.Name,
		createUsers: "CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, name VARCHAR(255) NOT NULL)",
		foreignID:   "INTEGER",
		catalog:     "SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table', 'view') AND name IN ('users', 'posts', 'user_names')",
	},
	{
		name: sqlserver.Name, env: "GRADII_TEST_SQLSERVER_DSN", port: 1433,
		createUsers: "CREATE TABLE users (id BIGINT NOT NULL IDENTITY(1,1) PRIMARY KEY, name VARCHAR(255) NOT NULL)",
		foreignID:   "BIGINT",
		catalog:     "SELECT COUNT(*) FROM sys.objects WHERE type IN ('U', 'V') AND name IN ('users', 'posts', 'user_names')",
	},
}

func TestDriverFixtureConfigBuildReuse(t *testing.T) {
	for _, tc := range fixtureDriverCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(tc.env, "")
			}
			fixture := newDatabaseFixture(tc.name, "fixture_database", "fixture_user", "fixture_password", nil, "default")
			want := contractsdocker.DatabaseConfig{Driver: tc.name, Database: "fixture_database"}
			if tc.name != sqlite.Name {
				want.Host, want.Port, want.Username, want.Password = "127.0.0.1", tc.port, "fixture_user", "fixture_password"
			}
			require.Equal(t, want, fixture.Config())
			require.Equal(t, tc.name, fixture.Driver())
			if tc.name == sqlite.Name {
				return
			}

			image := mocksdocker.NewImageDriver(t)
			image.EXPECT().Build().Return(nil).Once()
			image.EXPECT().Config().Return(contractsdocker.ImageConfig{
				ContainerID: "fixture_container", ExposedPorts: []string{"18080:8080", "19000:" + strconv.Itoa(tc.port) + "/tcp"},
			}).Once()
			fixture.imageDriver = image
			require.NoError(t, fixture.Build())
			want.ContainerID, want.Port = "fixture_container", 19000
			require.Equal(t, want, fixture.Config())

			require.NoError(t, fixture.Reuse("shared_container", 19001))
			want.ContainerID, want.Port = "shared_container", 19001
			require.Equal(t, want, fixture.Config())
			for _, port := range []int{-1, 0, 65536} {
				require.EqualError(t, fixture.Reuse("invalid_container", port), errors.DatabaseFixtureInvalidPort.Args(port).Error())
				require.Equal(t, want, fixture.Config())
			}
			// The server belongs to the shared Container, so Shutdown cannot stop it.
			require.NoError(t, fixture.Shutdown())
		})
	}
}

func TestDriverFixtureBuildErrors(t *testing.T) {
	for _, tc := range fixtureDriverCases {
		if tc.name == sqlite.Name {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, "")
			t.Run("image fails", func(t *testing.T) {
				fixture := newDatabaseFixture(tc.name, "fixture_database", "user", "password", nil, "default")
				before := fixture.Config()
				image := mocksdocker.NewImageDriver(t)
				image.EXPECT().Build().Return(errors.TestingImageNoContainerId.Args(tc.name)).Once()
				fixture.imageDriver = image
				require.EqualError(t, fixture.Build(), errors.TestingImageNoContainerId.Args(tc.name).Error())
				require.Equal(t, before, fixture.Config())
			})
			t.Run("missing mapped port", func(t *testing.T) {
				fixture := newDatabaseFixture(tc.name, "fixture_database", "user", "password", nil, "default")
				before := fixture.Config()
				image := mocksdocker.NewImageDriver(t)
				image.EXPECT().Build().Return(nil).Once()
				image.EXPECT().Config().Return(contractsdocker.ImageConfig{ContainerID: "fixture_container", ExposedPorts: []string{"19000:8080"}}).Once()
				fixture.imageDriver = image
				require.Error(t, fixture.Build())
				require.Equal(t, before, fixture.Config())
			})
		})
	}
}

func TestDriverFixtureImageOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command recorder uses a POSIX shell")
	}
	for _, tc := range fixtureDriverCases {
		if tc.name == sqlite.Name {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, "")
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "arguments")
			t.Setenv("GRADII_FIXTURE_DOCKER_ARGS", argsFile)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GRADII_FIXTURE_DOCKER_ARGS\"\nprintf '%s\\n' fixture_container\n"), 0700))

			fixture := newDatabaseFixture(tc.name, "fixture_database", "user", "password", nil, "default")
			port := "19000:" + strconv.Itoa(tc.port)
			fixture.Image(contractsdocker.Image{
				Repository: "fixture/custom", Tag: "pinned", Env: []string{"FIXTURE_CUSTOM=yes"},
				ExposedPorts: []string{port}, ShmSize: "2g", Args: []string{"--custom-argument"},
			})
			require.NoError(t, fixture.Build())
			arguments, err := os.ReadFile(argsFile)
			require.NoError(t, err)
			require.Equal(t, []string{"run", "--rm", "-d", "--shm-size", "2g", "-e", "FIXTURE_CUSTOM=yes", "-p", port, "fixture/custom:pinned", "--custom-argument"}, strings.Fields(string(arguments)))
			require.Equal(t, "fixture_container", fixture.Config().ContainerID)
			require.Equal(t, 19000, fixture.Config().Port)
		})
	}
}

// Ports from an explicitly supplied DSN must survive stale container-cache data.
func TestDriverFixtureExternalConfig(t *testing.T) {
	dsns := map[string]string{
		mysql.Name:     "external_user:external_password@tcp(127.0.0.2:19000)/existing_database",
		postgres.Name:  "postgres://external_user:external_password@127.0.0.2:19000/existing_database?sslmode=disable",
		sqlserver.Name: "sqlserver://external_user:external_password@127.0.0.2:19000?database=existing_database",
	}
	for _, tc := range fixtureDriverCases {
		if tc.name == sqlite.Name {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, dsns[tc.name])
			fixture := newDatabaseFixture(tc.name, "fixture_database", "user", "password", nil, "default")
			fixture.imageDriver = mocksdocker.NewImageDriver(t)
			want := contractsdocker.DatabaseConfig{Driver: tc.name, Database: "fixture_database", Host: "127.0.0.2", Port: 19000, Username: "external_user", Password: "external_password"}
			require.Equal(t, want, fixture.Config())
			require.NoError(t, fixture.Build())
			require.NoError(t, fixture.Reuse("stale_container", 19001))
			require.Equal(t, want, fixture.Config())
			require.EqualError(t, fixture.Fresh(), errors.DatabaseFixtureUnowned.Args("fixture_database").Error())
			require.NoError(t, fixture.Shutdown())
		})
	}
}

// Ports mysql/postgres/sqlite/sqlserver Test_Build_Config_AddData_Fresh_Shutdown.
func TestDriverFixtureLifecycle(t *testing.T) {
	for _, tc := range fixtureDriverCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := buildDriverFixture(t, tc.name)
			require.NoError(t, fixture.Ready())
			cfg := fixture.Config()
			require.Equal(t, tc.name, cfg.Driver)
			require.NotEmpty(t, cfg.Database)
			if tc.name != sqlite.Name {
				require.NotEmpty(t, cfg.Host)
				require.NotEmpty(t, cfg.Username)
				require.NotEmpty(t, cfg.Password)
				require.Positive(t, cfg.Port)
			}
			db := connectDriverFixture(t, fixture)
			_, err := db.Exec(tc.createUsers)
			require.NoError(t, err)
			result, err := db.Exec("INSERT INTO users (name) VALUES ('goravel')")
			require.NoError(t, err)
			rows, err := result.RowsAffected()
			require.NoError(t, err)
			require.EqualValues(t, 1, rows)
			var name string
			require.NoError(t, db.QueryRow("SELECT name FROM users").Scan(&name))
			require.Equal(t, "goravel", name)
			assertFixtureCount(t, db, tc.catalog, 1)

			// Fresh also has to remove views and tables with foreign keys.
			_, err = db.Exec("CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id " + tc.foreignID + ", CONSTRAINT posts_user_id_foreign FOREIGN KEY (user_id) REFERENCES users(id))")
			require.NoError(t, err)
			_, err = db.Exec("CREATE VIEW user_names AS SELECT name FROM users")
			require.NoError(t, err)
			assertFixtureCount(t, db, tc.catalog, 3)
			require.NoError(t, fixture.Fresh())
			assertFixtureCount(t, db, tc.catalog, 0)
			require.NoError(t, fixture.Fresh())
			require.NoError(t, db.Close())
			require.NoError(t, fixture.Shutdown())
			if tc.name == sqlite.Name {
				require.NoFileExists(t, cfg.Database)
			} else {
				require.NoError(t, fixture.Ready())
			}
		})
	}
}

// Ports each driver's TestDatabase: the child is immediately usable and isolated.
func TestDriverFixtureDatabase(t *testing.T) {
	for _, tc := range fixtureDriverCases {
		t.Run(tc.name, func(t *testing.T) {
			parent := buildDriverFixture(t, tc.name)
			parentConfig := parent.Config()
			db := connectDriverFixture(t, parent)
			_, err := db.Exec(tc.createUsers)
			require.NoError(t, err)
			name := "fixture_child_" + str.Random(12)
			if tc.name == sqlite.Name {
				name = filepath.Join(t.TempDir(), name)
			}
			childDriver, err := parent.Database(name)
			require.NoError(t, err)
			child := childDriver.(*databaseFixture)
			cleanupDriverFixture(t, child)
			want := parentConfig
			want.Database = name
			require.Equal(t, want, child.Config())
			require.Equal(t, parentConfig, parent.Config())
			require.NoError(t, child.Ready())
			childDB := connectDriverFixture(t, child)
			assertFixtureCount(t, childDB, tc.catalog, 0)
			_, err = childDB.Exec(tc.createUsers)
			require.NoError(t, err)
			require.NoError(t, child.Fresh())
			assertFixtureCount(t, childDB, tc.catalog, 0)
			assertFixtureCount(t, db, tc.catalog, 1)
		})
	}
}

// Ports both TestReady branches from mysql, postgres and sqlserver.
func TestDriverFixtureReady(t *testing.T) {
	for _, tc := range fixtureDriverCases {
		if tc.name == sqlite.Name {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			fixture := buildDriverFixture(t, tc.name)
			for _, hasWriters := range []bool{true, false} {
				t.Run(fmt.Sprintf("write_config_%t", hasWriters), func(t *testing.T) {
					config := mocksconfig.NewConfig(t)
					copy := *fixture
					copy.config, copy.connection = config, "fixture_connection"
					path := "database.connections.fixture_connection"
					if hasWriters {
						writers := []database.Config{{Host: "writer_one", Username: "user_one"}, {Host: "writer_two", Port: 1, Username: "user_two"}}
						want := append([]database.Config(nil), writers...)
						for i := range want {
							want[i].Port = fixture.Config().Port
						}
						config.EXPECT().Get(path + ".write").Return(writers).Once()
						config.EXPECT().Add(path+".write", want).Once()
					} else {
						config.EXPECT().Get(path + ".write").Return(nil).Once()
						config.EXPECT().Add(path+".port", fixture.Config().Port).Once()
					}
					require.NoError(t, copy.Ready())
				})
			}
		})
	}
}

func TestDriverFixtureSqliteOwnershipAndCleanup(t *testing.T) {
	t.Run("existing files are not adopted", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "existing.sqlite")
		contents := []byte("existing database contents")
		require.NoError(t, os.WriteFile(path, contents, 0600))
		fixture := newSqliteFixture(path)
		require.ErrorIs(t, fixture.Build(), os.ErrExist)
		require.EqualError(t, fixture.Fresh(), errors.DatabaseFixtureUnowned.Args(path).Error())
		require.EqualError(t, fixture.Shutdown(), errors.DatabaseFixtureUnowned.Args(path).Error())
		actual, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, contents, actual)
	})
	t.Run("owned database and sidecars are removed", func(t *testing.T) {
		fixture := buildDriverFixture(t, sqlite.Name)
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			require.NoError(t, os.WriteFile(fixture.Config().Database+suffix, []byte("fixture sidecar"), 0600))
		}
		require.NoError(t, fixture.Shutdown())
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			require.NoFileExists(t, fixture.Config().Database+suffix)
		}
		require.NoError(t, fixture.Shutdown())
		require.EqualError(t, fixture.Fresh(), errors.DatabaseFixtureUnowned.Args(fixture.Config().Database).Error())
	})
}

func buildDriverFixture(t *testing.T, driver string) *databaseFixture {
	t.Helper()
	if driver == sqlite.Name {
		fixture := newSqliteFixture(filepath.Join(t.TempDir(), "fixture.sqlite"))
		require.NoError(t, fixture.Build())
		cleanupDriverFixture(t, fixture)
		return fixture
	}
	if testing.Short() {
		t.Skip("database service lifecycle tests require a test server")
	}
	if driver == sqlserver.Name && runtime.GOARCH != "amd64" && os.Getenv("GRADII_TEST_SQLSERVER_DSN") == "" {
		t.Skip("SQL Server requires an amd64 host or GRADII_TEST_SQLSERVER_DSN")
	}
	fixture := newDatabaseFixture(driver, testDatabase, testUsername, testPassword, nil, "fixture_connection")
	container := NewContainer(fixture)
	if isMariaDBTest(driver) {
		container.name = "MariaDB_11.8"
		fixture.Image(contractsdocker.Image{
			Repository: "mariadb", Tag: "11.8", ExposedPorts: []string{"3306"},
			Env: []string{"MYSQL_ROOT_PASSWORD=" + testPassword, "MYSQL_DATABASE=" + testDatabase, "MYSQL_USER=" + testUsername, "MYSQL_PASSWORD=" + testPassword},
		})
	}
	databaseDriver, err := container.Build()
	require.NoError(t, err)
	fixture = databaseDriver.(*databaseFixture)
	cleanupDriverFixture(t, fixture)
	return fixture
}

func cleanupDriverFixture(t *testing.T, fixture *databaseFixture) {
	t.Helper()
	t.Cleanup(func() {
		if fixture.Driver() == sqlite.Name {
			assert.NoError(t, fixture.Shutdown())
			return
		}
		// Only this run's successfully-created fixture is eligible for DROP.
		_, owned := fixtureDatabases.Load(fixture.key())
		if assert.True(t, owned, "fixture must own its database before cleanup") && assert.NoError(t, dropDatabase(fixture.Config())) {
			fixtureDatabases.Delete(fixture.key())
		}
	})
}

func connectDriverFixture(t *testing.T, fixture *databaseFixture) *sql.DB {
	t.Helper()
	db, err := fixture.connect(false)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	return db
}

func assertFixtureCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(query).Scan(&count))
	require.Equal(t, want, count)
}
