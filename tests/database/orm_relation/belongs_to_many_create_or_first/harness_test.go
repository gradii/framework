// Package orm_relation ports the relation corpora of Laravel's Eloquent test suite (the tests/Database
// directory of laravel/framework) onto the framework's ORM, running each ported test against a real
// sqlite database.
//
// # Porting rule: relations are loaded explicitly
//
// Laravel lazy-loads a relation through property access: reading $user->posts runs the relation query
// and caches the result on the model. Go has no property-access magic, so a ported test must call the
// load function explicitly before asserting a relation's contents:
//
//   - query.Load(&user, "posts")        loads the relation even when it is already loaded
//   - query.LoadMissing(&user, "posts") loads it only when it is not already loaded
//   - query.Relation(&user, "posts")    the Relation(model, name) writer, for relation writes
//   - query.With("posts").Find(&users)  eager loading, Laravel's with('posts')
//
// # Harness
//
// Every call to newRelationQuery opens a fresh sqlite database and returns an ORM query bound to it,
// so tests never share rows or schema. runRelationSchema creates the corpus's tables with plain DDL.
package belongs_to_many_create_or_first

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	frameworkconfig "github.com/gradii/framework/config"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkdb "github.com/gradii/framework/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	databasegorm "github.com/gradii/framework/database/gorm"
	"github.com/gradii/framework/support/str"
	"github.com/gradii/framework/testing/utils"

	"github.com/gradii/sqlite"
	sqlitecontracts "github.com/gradii/sqlite/contracts"
	"github.com/stretchr/testify/require"
)

// newRelationQuery opens a fresh sqlite database for one test and returns an ORM query bound to it.
//
// The database is a file under t.TempDir() — removed with the test — addressed by the driver's
// "file:" DSN form: unlike ":memory:", every pooled connection then sees the same database. The DSN
// pragma turns on foreign key enforcement for every connection, as Laravel's sqlite connection does
// by default, and database.pool is capped at one connection to keep a test's statements sequential.
func newRelationQuery(t *testing.T) contractsorm.Query {
	t.Helper()

	connection := "orm_relation_" + str.Random(6)
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "relation.db")) + "?_pragma=foreign_keys(1)"

	config := frameworkconfig.NewApplication("")
	config.Add("database.default", connection)
	config.Add("database.pool", map[string]any{"max_open_conns": 1, "max_idle_conns": 1})
	config.Add("database.connections."+connection, map[string]any{
		"driver": sqlite.Name,
		"dsn":    dsn,
		"via": func() (contractsdriver.Driver, error) {
			return sqlite.NewSqlite(config, nil, connection), nil
		},
	})

	ctx := context.Background()
	log := utils.NewTestLog()

	// The DB handle owns the connection: BuildQuery below shares the same pool, so the purge at
	// cleanup closes both.
	db, err := frameworkdb.BuildDB(ctx, config, log, connection, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Purge()) })

	query, _, err := databasegorm.BuildQuery(ctx, config, connection, log, nil, nil)
	require.NoError(t, err)

	return query
}

// cloneRelationQuery returns an independent copy of the test's query handle. Every seeding or
// assertion chain starts with one, the way the Orm facade's Query() hands out a fresh builder per
// call: without it, a Where one chain wrote would stay on the handle and narrow the next query.
func cloneRelationQuery(t *testing.T, query contractsorm.Query) contractsorm.Query {
	t.Helper()

	cloner, ok := query.(contractsorm.QueryWithClone)
	require.True(t, ok, "the ORM query must support Clone so one chain cannot leak into the next")

	return cloner.Clone()
}

// runRelationSchema executes DDL statements, one statement per string — sqlite executes a single
// statement per Exec — against the test's database.
func runRelationSchema(t *testing.T, query contractsorm.Query, statements ...string) {
	t.Helper()

	for _, statement := range statements {
		_, err := query.Exec(statement)
		require.NoError(t, err, statement)
	}
}

// relationSmokeUser is the corpus's plain users row: an auto-incrementing integer key and a name.
type relationSmokeUser struct {
	ID   uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Name string `gorm:"column:name" db:"name"`
}

func (relationSmokeUser) TableName() string { return "users" }

// TestRelationHarnessSmoke proves the harness end to end: a fresh database, a schema through
// runRelationSchema, an insert through the ORM, and a read-back on a cloned handle.
func TestRelationHarnessSmoke(t *testing.T) {
	query := newRelationQuery(t)
	runRelationSchema(t, query, `create table users (id integer primary key autoincrement, name text)`)

	user := relationSmokeUser{Name: "Taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))
	require.NotZero(t, user.ID, "Create must read the generated key back")

	var reloaded relationSmokeUser
	require.NoError(t, cloneRelationQuery(t, query).Where("id", user.ID).First(&reloaded))
	require.Equal(t, user.ID, reloaded.ID)
	require.Equal(t, "Taylor", reloaded.Name)
}

// splitRelationHandle is the read/write-split fixture: one ORM query whose reads resolve to a
// reader pool pointed at a second sqlite file while its writes land in the writer pool — Laravel's
// $readPdo/$pdo split — plus the raw writer and reader pools a test seeds and asserts through.
type splitRelationHandle struct {
	query  contractsorm.Query
	writer *sql.DB
	reader *sql.DB
}

// newSplitRelationQuery opens a sqlite connection whose reader pool is a *different* file from its
// writer pool: writes land in writeDsn, reads resolve to readDsn. The read replica is a plain file,
// so a test can leave it stale and drive the paths only a genuine read/write divergence reaches —
// Laravel's useWritePdo re-reads, which a single-connection harness can never exercise because a
// single file's read always sees its own write.
//
// No ORM plumbing is added: the seam already exists. A driver's Pool() already carries Readers and
// Writers, executor.readPool() already routes compiled reads to connection.Reader(), and the sqlite
// driver already reads database.connections.<c>.read / .write to fill them (the same split
// TestOrmReadWriteSeparate builds). It was simply unreachable from the corpus harness, which opens
// one file and lets the reader fall back to the writer.
//
// The fixture asserts the split is real (writer and reader are distinct pools) and returns the raw
// pools; reads through the returned query go to reader, writes to writer.
func newSplitRelationQuery(t *testing.T, writeDsn, readDsn string) splitRelationHandle {
	t.Helper()

	connection := "orm_relation_split_" + str.Random(6)

	config := frameworkconfig.NewApplication("")
	config.Add("database.default", connection)
	config.Add("database.pool", map[string]any{"max_open_conns": 1, "max_idle_conns": 1})
	config.Add("database.connections."+connection, map[string]any{
		"driver": sqlite.Name,
		"dsn":    writeDsn,
		"via": func() (contractsdriver.Driver, error) {
			return sqlite.NewSqlite(config, nil, connection), nil
		},
	})
	// The replica: database.connections.<c>.read is the sqlite driver's reader pool, so the ORM's
	// read routing (executor.readPool -> connection.Reader()) resolves here, and only useWritePdo
	// sends a read back to the writer.
	config.Add("database.connections."+connection+".read", []sqlitecontracts.Config{{Dsn: readDsn}})

	ctx := context.Background()
	log := utils.NewTestLog()

	// The DB handle owns the connection: BuildQuery below shares the same pool, so the purge at
	// cleanup closes both.
	db, err := frameworkdb.BuildDB(ctx, config, log, connection, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Purge()) })

	driver := sqlite.NewSqlite(config, nil, connection)
	pool := driver.Pool()
	require.NotEmpty(t, pool.Readers, "the driver must expose a reader pool from the read config")

	dialect, err := databasedriver.ResolveDialect(driver)
	require.NoError(t, err)
	// Open (not BuildQuery) so the fixture holds the connection's raw pools; BuildQuery resolves the
	// same cached handle by identity, so one Open serves both the query and the raw handles.
	conn, err := databasedriver.Open(config, frameworkdb.NewLogger(config, log), pool, connection, nil, dialect)
	require.NoError(t, err)
	require.NotSame(t, conn.Writer(), conn.Reader(), "the reader pool must be a distinct handle from the writer")

	query, _, err := databasegorm.BuildQuery(ctx, config, connection, log, nil, nil)
	require.NoError(t, err)

	return splitRelationHandle{query: query, writer: conn.Writer(), reader: conn.Reader()}
}

// countRows returns how many rows a table holds in the given raw pool — the split fixture's
// instrument for "nothing was written here".
func countRows(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()

	var n int64
	require.NoError(t, db.QueryRowContext(context.Background(), "select count(*) from "+table).Scan(&n))

	return n
}
