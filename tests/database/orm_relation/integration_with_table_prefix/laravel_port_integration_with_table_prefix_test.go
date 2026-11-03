package integration_with_table_prefix

import (
	"context"
	"path/filepath"
	"testing"

	frameworkconfig "github.com/gradii/framework/config"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkdb "github.com/gradii/framework/database/db"
	databasegorm "github.com/gradii/framework/database/gorm"
	"github.com/gradii/framework/support/carbon"
	"github.com/gradii/framework/support/str"
	"github.com/gradii/framework/testing/utils"

	"github.com/gradii/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file ports Laravel's tests/Database/DatabaseEloquentIntegrationWithTablePrefixTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestIntegrationWithTablePrefix_<method without the test prefix>.
//
// The corpus is the with-table-prefix half of the Eloquent integration suite. setUp() opens one
// sqlite database, sets the connection's table prefix to "prefix_" (setTablePrefix) and creates
// users / friends / posts / photos through the schema builder, so every physical table is named
// prefix_*. Its single test seeds two users through the model and reads one back with
// Model::fromQuery over a raw, hand-prefixed SELECT.
//
// The port keeps every observable: the schema is the one setUp()'s builders execute (statements
// below, quoted identifiers and nullability included), the rows are seeded through the ORM's
// Create, and the read runs the same raw SQL and hydrates the same model type. The connection's
// prefix is what makes the seeded rows land in prefix_users - a Go model declares only
// TableName() = "users", exactly as EloquentTestUser declares only $table = 'users', and the sqlite
// driver applies database.connections.<connection>.prefix when it renders a statement. The shared
// harness's newRelationQuery hard-wires its connection config without a prefix, so this file
// carries newPrefixedRelationQuery, the harness constructor with that one key added; the harness
// copy in this package stays byte-identical to the shared one apart from its package clause, and
// cloneRelationQuery / runRelationSchema are its helpers, unchanged. tearDown()'s
// Relation::morphMap([], false) clears process-wide morph state this port never sets, so it has no
// counterpart.
//
// # The statements behind the corpus, measured with the oracle
//
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1.) These are the
// statements setUp() actually executes on the prefixed sqlite connection, captured from the query
// log; the port's runRelationSchema statements are these, byte for byte:
//
//	create table "prefix_users" ("id" integer primary key autoincrement not null, "email" varchar not null, "created_at" datetime, "updated_at" datetime)
//	create table "prefix_friends" ("user_id" integer not null, "friend_id" integer not null)
//	create table "prefix_posts" ("id" integer primary key autoincrement not null, "user_id" integer not null, "parent_id" integer, "name" varchar not null, "created_at" datetime, "updated_at" datetime)
//	create table "prefix_photos" ("id" integer primary key autoincrement not null, "imageable_type" varchar not null, "imageable_id" integer not null, "name" varchar not null, "created_at" datetime, "updated_at" datetime)
//	create index "photos_imageable_type_imageable_id_index" on "prefix_photos" ("imageable_type", "imageable_id")
//
// The model's own statements with the prefix set (identical at the builder level and through an
// Eloquent model):
//
//	select * from "prefix_users"
//	insert into "prefix_users" ("email", "updated_at", "created_at") values (?, ?, ?)
//
// The test asserts none of that SQL text; it asserts the corpus's outcomes against the real
// database, and the prefix is exercised by construction - without it the inserts would address a
// "users" table that does not exist.
//
// # Mapping notes
//
//	EloquentTestUser::create(...)             -> cloneRelationQuery(t, query).Create(&user)
//	EloquentTestUser::fromQuery($sql, $binds) -> <query>.Raw($sql, $binds...).Get(&users)
//	                                       Laravel runs the raw SQL on the model's connection and
//	                                       hydrates the rows into models; Raw carries the statement
//	                                       verbatim and Get fills the destination, so the same rows
//	                                       become the same model type.
//	Collection instance / $models[0] type     -> the destination is []*integrationWithTablePrefixUser:
//	                                       in Go the collection and its element type are the
//	                                       declared slice type.
//
// No test of this corpus is skipped: every asserted behaviour is reachable through the Go ORM.

// integrationWithTablePrefixUser ports EloquentTestUser ($table = 'users', $guarded = [], default
// timestamps). The bare table name is the point: the connection's prefix turns it into
// prefix_users when a statement renders. Its $casts = ['birthday' => 'datetime'] has no column to
// act on in this corpus - the with-table-prefix schema gives users only id, email and the
// timestamps - and $guarded has no Go counterpart, as goravel has no mass-assignment guard.
type integrationWithTablePrefixUser struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (integrationWithTablePrefixUser) TableName() string { return "users" }

// newPrefixedRelationQuery is the harness's newRelationQuery with the corpus's table prefix on the
// connection: the same fresh file-backed sqlite database and pooled single connection, plus
// database.connections.<connection>.prefix = "prefix_", the key the sqlite driver reads when it
// builds its pool. newRelationQuery itself carries no prefix and the corpus cannot run without one,
// so this constructor stands in for the one call site.
func newPrefixedRelationQuery(t *testing.T) contractsorm.Query {
	t.Helper()

	connection := "integration_with_table_prefix_" + str.Random(6)
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "relation.db")) + "?_pragma=foreign_keys(1)"

	config := frameworkconfig.NewApplication("")
	config.Add("database.default", connection)
	config.Add("database.pool", map[string]any{"max_open_conns": 1, "max_idle_conns": 1})
	config.Add("database.connections."+connection, map[string]any{
		"driver": sqlite.Name,
		"dsn":    dsn,
		"prefix": "prefix_",
		"via": func() (contractsdriver.Driver, error) {
			return sqlite.NewSqlite(config, nil, connection), nil
		},
	})

	ctx := context.Background()
	log := utils.NewTestLog()

	db, err := frameworkdb.BuildDB(ctx, config, log, connection, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Purge()) })

	query, _, err := databasegorm.BuildQuery(ctx, config, connection, log, nil, nil)
	require.NoError(t, err)

	return query
}

// integrationWithTablePrefixSchema mirrors setUp()'s createSchema(): the four tables, created
// through the prefixed schema builder, so each physical name carries the prefix. The statements are
// the oracle's captured execution, quoted identifiers and all.
func integrationWithTablePrefixSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table "prefix_users" ("id" integer primary key autoincrement not null, "email" varchar not null, "created_at" datetime, "updated_at" datetime)`,
		`create table "prefix_friends" ("user_id" integer not null, "friend_id" integer not null)`,
		`create table "prefix_posts" ("id" integer primary key autoincrement not null, "user_id" integer not null, "parent_id" integer, "name" varchar not null, "created_at" datetime, "updated_at" datetime)`,
		`create table "prefix_photos" ("id" integer primary key autoincrement not null, "imageable_type" varchar not null, "imageable_id" integer not null, "name" varchar not null, "created_at" datetime, "updated_at" datetime)`,
		`create index "photos_imageable_type_imageable_id_index" on "prefix_photos" ("imageable_type", "imageable_id")`,
	)
}

// TestIntegrationWithTablePrefix_BasicModelHydration ports testBasicModelHydration: two models are
// created through the (prefixed) model, one is read back through a raw query, and the result is a
// one-model collection of the model's type carrying the queried email.
func TestIntegrationWithTablePrefix_BasicModelHydration(t *testing.T) {
	query := newPrefixedRelationQuery(t)
	integrationWithTablePrefixSchema(t, query)

	// EloquentTestUser::create(['email' => 'taylorotwell@gmail.com']); the generated key coming
	// back is what shows the insert addressed prefix_users.
	taylor := integrationWithTablePrefixUser{Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&taylor))
	require.NotZero(t, taylor.ID, "the prefixed insert must read the generated key back")

	// EloquentTestUser::create(['email' => 'abigailotwell@gmail.com']).
	abigail := integrationWithTablePrefixUser{Email: "abigailotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&abigail))
	require.NotZero(t, abigail.ID, "the prefixed insert must read the generated key back")

	// EloquentTestUser::fromQuery('SELECT * FROM prefix_users WHERE email = ?', ['abigailotwell@gmail.com']).
	var models []*integrationWithTablePrefixUser
	require.NoError(t, cloneRelationQuery(t, query).
		Raw("SELECT * FROM prefix_users WHERE email = ?", "abigailotwell@gmail.com").
		Get(&models))

	// Laravel: assertInstanceOf(Collection::class, $models) and assertCount(1, $models).
	require.Len(t, models, 1)
	// Laravel: assertInstanceOf(EloquentTestUser::class, $models[0]).
	require.NotNil(t, models[0])
	// Laravel: assertSame('abigailotwell@gmail.com', $models[0]->email).
	assert.Equal(t, "abigailotwell@gmail.com", models[0].Email)
}
