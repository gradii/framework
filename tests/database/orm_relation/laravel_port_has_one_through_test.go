package orm_relation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasOneThroughIntegrationTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestHasOneThrough_<method without the test prefix>. The corpus's three tables (plus the _default
// tables its default-relation test migrates) mirror createSchema() in sqlite DDL; rows are seeded
// through the ORM, and relations are loaded explicitly through OrmBuilder.Load — the harness's
// lazy-loading rule, since Go has no property-access magic.
//
// The corpus checks behaviour, not compiled SQL: it has no ->toSql() / ->toRawSql() pins. The
// oracle (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1) was still used to
// measure the SQL behind that behaviour, and the comments below cite those measurements: the
// custom-key relation builder's own toSql is `select * from "contracts" inner join "users" on
// "users"."id" = "contracts"."user_id" where "users"."position_id" = ?`; the first()/get() and the
// chunk/cursor/each/lazy iteration paths rewrite the select to `"contracts".*, "users"."position_id"
// as "laravel_through_key"` (first() adding `limit 1`); first(['title', 'body']) selects `"title",
// "body", "users"."position_id" as "laravel_through_key"`; the intermediate-key relation joins
// `"users"."email" = "contracts"."email"` and filters on `"users"."position_short"` (its alias is
// `"users"."position_short" as "laravel_through_key"`); the default-keys relation, measured with
// the corpus's class names, joins `"users_default"."id" =
// "contracts_default"."has_one_through_default_test_user_id"` and filters on
// `"users_default"."has_one_through_default_test_position_id"`; and the ported whereHas test's
// existence subquery is `select * from "positions" where exists (select * from "contracts" inner
// join "users" on "users"."email" = "contracts"."email" where "positions"."shortname" =
// "users"."position_short" and "title" = ?)`.
//
// # How our has-one-through diverges from Laravel's
//
//   - Laravel's first()/get()/cursor() reads select `<related>.*` plus the through key aliased as
//     laravel_through_key, so a hydrated model carries its own columns only. Our Related read keeps
//     the builder's `select *` from the join, and the through row's same-named columns come last
//     and overwrite the related row's — probe (distinct emails): with contracts.email
//     contract@example.com and users.email user@example.com, Related(...).First reads back
//     user@example.com. The corpus fixture keeps contract 1's key and email equal to user 1's, so
//     the ported assertions observe the same values.
//   - ModelNotFoundException messages name the model and its ids; the Go ORM reports the fixed
//     sentinel errors.OrmRecordNotFound ("record not found").
//   - The ORM query exposes no chunk / each / lazy; those live on the raw database/db query
//     (Query.Chunk, Query.Each, Query.Lazy), and Query.Related returns an orm.Query, so those three
//     iteration ports cannot run the chain under test and are skipped with their probes. Cursor()
//     exists, but it streams the joined `select *` rows as raw db.Row values — no laravel_through_key,
//     and the through row's columns overwrite the related row's — not the hydrated models the corpus
//     asserts, so that port is skipped too.
//   - A typed Go model has no dynamic attribute bag, so the corpus's array_keys(getAttributes())
//     comparisons (including laravel_through_key, which this read chain never produces) have no
//     direct counterpart; the two column-list ports assert every real column is hydrated instead —
//     the same approach the has-many-through port takes.

// --- models ------------------------------------------------------------------

// hasOneThroughUser ports HasOneThroughTestUser.
type hasOneThroughUser struct {
	ID            uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Email         string           `gorm:"column:email"`
	PositionID    uint             `gorm:"column:position_id"`
	PositionShort string           `gorm:"column:position_short"`
	CreatedAt     *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt     *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasOneThroughUser) TableName() string { return "users" }

func (hasOneThroughUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Contract": contractsorm.HasOne{Related: &hasOneThroughContract{}, ForeignKey: "user_id"},
	}
}

// hasOneThroughContract ports HasOneThroughTestContract.
type hasOneThroughContract struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	Title     string           `gorm:"column:title"`
	Body      string           `gorm:"column:body"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasOneThroughContract) TableName() string { return "contracts" }

// hasOneThroughPosition ports HasOneThroughTestPosition: contract is reached through users with the
// corpus's explicit keys (users.position_id -> contracts.user_id, on positions.id).
type hasOneThroughPosition struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	Shortname string           `gorm:"column:shortname"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Contract *hasOneThroughContract `gorm:"-"`
}

func (hasOneThroughPosition) TableName() string { return "positions" }

func (hasOneThroughPosition) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Contract": contractsorm.HasOneThrough{
			Related: &hasOneThroughContract{}, Through: &hasOneThroughUser{},
			FirstKey: "position_id", SecondKey: "user_id",
		},
		// user(): the HasOne the corpus's seeding chains through. No destination field is needed —
		// the port only writes users rows through the relation writer.
		"User": contractsorm.HasOne{Related: &hasOneThroughUser{}, ForeignKey: "position_id"},
	}
}

// hasOneThroughIntermediatePosition ports HasOneThroughIntermediateTestPosition: the same tables
// related by positions.shortname -> users.position_short -> users.email -> contracts.email.
type hasOneThroughIntermediatePosition struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	Shortname string           `gorm:"column:shortname"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Contract *hasOneThroughContract `gorm:"-"`
}

func (hasOneThroughIntermediatePosition) TableName() string { return "positions" }

func (hasOneThroughIntermediatePosition) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Contract": contractsorm.HasOneThrough{
			Related: &hasOneThroughContract{}, Through: &hasOneThroughUser{},
			FirstKey: "position_short", SecondKey: "email", LocalKey: "shortname", SecondLocalKey: "email",
		},
		"User": contractsorm.HasOne{Related: &hasOneThroughUser{}, ForeignKey: "position_id"},
	}
}

// hasOneThroughDefaultUser ports HasOneThroughDefaultTestUser: the class-named default keys
// (has_one_through_default_test_*_id) are declared explicitly, because the Go ORM derives its
// defaults from table names where Laravel derives them from class names (oracle: with the corpus's
// class names the default relation joins "users_default"."id" =
// "contracts_default"."has_one_through_default_test_user_id").
type hasOneThroughDefaultUser struct {
	ID                uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Email             string           `gorm:"column:email"`
	DefaultPositionID uint             `gorm:"column:has_one_through_default_test_position_id"`
	CreatedAt         *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt         *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasOneThroughDefaultUser) TableName() string { return "users_default" }

func (hasOneThroughDefaultUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Contract": contractsorm.HasOne{Related: &hasOneThroughDefaultContract{}, ForeignKey: "has_one_through_default_test_user_id"},
	}
}

// hasOneThroughDefaultContract ports HasOneThroughDefaultTestContract. The contracts_default table
// carries no email column, so the ported model has no Email field either.
type hasOneThroughDefaultContract struct {
	ID            uint             `gorm:"column:id;primaryKey;autoIncrement"`
	DefaultUserID uint             `gorm:"column:has_one_through_default_test_user_id"`
	Title         string           `gorm:"column:title"`
	Body          string           `gorm:"column:body"`
	CreatedAt     *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt     *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasOneThroughDefaultContract) TableName() string { return "contracts_default" }

// hasOneThroughDefaultPosition ports HasOneThroughDefaultTestPosition.
type hasOneThroughDefaultPosition struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Contract *hasOneThroughDefaultContract `gorm:"-"`
}

func (hasOneThroughDefaultPosition) TableName() string { return "positions_default" }

func (hasOneThroughDefaultPosition) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Contract": contractsorm.HasOneThrough{
			Related: &hasOneThroughDefaultContract{}, Through: &hasOneThroughDefaultUser{},
			FirstKey: "has_one_through_default_test_position_id", SecondKey: "has_one_through_default_test_user_id",
		},
		"User": contractsorm.HasOne{Related: &hasOneThroughDefaultUser{}, ForeignKey: "has_one_through_default_test_position_id"},
	}
}

// hasOneThroughSoftUser ports HasOneThroughSoftDeletesTestUser: the users table with the
// framework's soft-delete field. The corpus's soft-delete test deletes through this class and then
// reads the position's contract through HasOneThroughSoftDeletesTestPosition, whose through parent
// is the plain HasOneThroughTestUser class — the relation never carries this trait.
type hasOneThroughSoftUser struct {
	databaseorm.SoftDeletes

	ID            uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Email         string           `gorm:"column:email"`
	PositionID    uint             `gorm:"column:position_id"`
	PositionShort string           `gorm:"column:position_short"`
	CreatedAt     *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt     *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasOneThroughSoftUser) TableName() string { return "users" }

// hasOneThroughSoftPosition ports HasOneThroughSoftDeletesTestPosition. In the corpus its related
// class (HasOneThroughSoftDeletesTestContract) differs from HasOneThroughTestContract only in name —
// neither carries SoftDeletes — so the port reuses hasOneThroughContract; the through model is the
// plain user class, exactly as the corpus declares it.
type hasOneThroughSoftPosition struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	Shortname string           `gorm:"column:shortname"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Contract *hasOneThroughContract `gorm:"-"`
}

func (hasOneThroughSoftPosition) TableName() string { return "positions" }

func (hasOneThroughSoftPosition) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Contract": contractsorm.HasOneThrough{
			Related: &hasOneThroughContract{}, Through: &hasOneThroughUser{},
			FirstKey: "position_id", SecondKey: "user_id",
		},
		"User": contractsorm.HasOne{Related: &hasOneThroughSoftUser{}, ForeignKey: "position_id"},
	}
}

// --- schema and seeding ------------------------------------------------------

// hasOneThroughSchema mirrors the corpus's createSchema() in sqlite DDL: users carries
// softDeletes(), and every table carries timestamps() (nullable datetime columns).
func hasOneThroughSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique, position_id integer unique null, position_short varchar not null, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
		`create table contracts (id integer primary key autoincrement, user_id integer not null unique, title varchar not null, body text not null, email varchar not null, created_at datetime null, updated_at datetime null)`,
		`create table positions (id integer primary key autoincrement, name varchar not null, shortname varchar not null, created_at datetime null, updated_at datetime null)`,
	)
}

// hasOneThroughDefaultSchema mirrors migrateDefault(): the tables backing the corpus's "default"
// hasOneThrough setup, whose foreign keys carry the class-derived names.
func hasOneThroughDefaultSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users_default (id integer primary key autoincrement, email varchar not null unique, has_one_through_default_test_position_id integer unique null, created_at datetime null, updated_at datetime null)`,
		`create table contracts_default (id integer primary key autoincrement, has_one_through_default_test_user_id integer not null unique, title varchar not null, body text not null, created_at datetime null, updated_at datetime null)`,
		`create table positions_default (id integer primary key autoincrement, name varchar not null, created_at datetime null, updated_at datetime null)`,
	)
}

// hasOneThroughSeedData ports seedData(): position 1 (ps) -> user 1 -> contract "A title", seeded
// through the corpus's relation-writer chains ($position->user()->create()->contract()->create()).
func hasOneThroughSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	position := hasOneThroughPosition{ID: 1, Name: "President", Shortname: "ps"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&position))

	user := hasOneThroughUser{ID: 1, Email: "taylorotwell@gmail.com", PositionShort: "ps"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&position, "User").Create(&user))

	contract := hasOneThroughContract{Title: "A title", Body: "A body", Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Contract").Create(&contract))
}

// hasOneThroughSeedDataExtended ports seedDataExtended(): position 2 (vp) -> user 2 -> contract
// "Example1 title1". The iteration ports that read it (chunk / cursor / each / lazy) are skipped
// below, so the helper only documents the corpus fixture.
func hasOneThroughSeedDataExtended(t *testing.T, query contractsorm.Query) {
	t.Helper()

	position := hasOneThroughPosition{ID: 2, Name: "Vice President", Shortname: "vp"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&position))

	user := hasOneThroughUser{ID: 2, Email: "example1@gmail.com", PositionShort: "vp"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&position, "User").Create(&user))

	contract := hasOneThroughContract{Title: "Example1 title1", Body: "Example1 body1", Email: "example1contract1@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Contract").Create(&contract))
}

// hasOneThroughSeedDefaultData ports seedDefaultData(): default position 1 -> user 1 -> contract
// "A title", on the _default tables.
func hasOneThroughSeedDefaultData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	position := hasOneThroughDefaultPosition{ID: 1, Name: "President"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&position))

	user := hasOneThroughDefaultUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&position, "User").Create(&user))

	contract := hasOneThroughDefaultContract{Title: "A title", Body: "A body"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Contract").Create(&contract))
}

// --- ports of the corpus -----------------------------------------------------

func TestHasOneThrough_ItLoadsAHasOneThroughRelationWithCustomKeys(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	position := hasOneThroughPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&position).First(&position))
	require.NoError(t, cloneRelationQuery(t, query).Load(&position, "Contract"))

	require.NotNil(t, position.Contract)
	assert.Equal(t, "A title", position.Contract.Title)
}

func TestHasOneThrough_ItLoadsADefaultHasOneThroughRelation(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughDefaultSchema(t, query)
	hasOneThroughSeedDefaultData(t, query)

	position := hasOneThroughDefaultPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&position).First(&position))
	require.NoError(t, cloneRelationQuery(t, query).Load(&position, "Contract"))

	require.NotNil(t, position.Contract)
	assert.Equal(t, "A title", position.Contract.Title)

	// Laravel also asserts the hydrated contract carries no 'email' key: the contracts_default table
	// has no email column and the Go model declares no Email field, so the absence is structural.
	//
	// Laravel drops the _default tables afterwards (resetDefault) to keep its shared in-memory
	// database clean; the harness gives every test its own database, so there is nothing to reset.
}

func TestHasOneThrough_ItLoadsARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	position := hasOneThroughIntermediatePosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&position).First(&position))
	require.NoError(t, cloneRelationQuery(t, query).Load(&position, "Contract"))

	require.NotNil(t, position.Contract)
	assert.Equal(t, "A title", position.Contract.Title)
}

func TestHasOneThrough_EagerLoadingARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	// Laravel: with('contract')->first(). The port reads through Get: First(&struct) with an eager
	// load leaves its single-row cursor open while the eager query runs, which deadlocks on the
	// harness's one-connection pool, while Get drains its cursor first. The observable is the same —
	// the one position row, with the relation attached.
	found := []hasOneThroughIntermediatePosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughIntermediatePosition{}).With("Contract").Get(&found))
	require.Len(t, found, 1)

	require.NotNil(t, found[0].Contract)
	assert.Equal(t, "A title", found[0].Contract.Title)
}

func TestHasOneThrough_WhereHasOnARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	positions := []hasOneThroughIntermediatePosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughIntermediatePosition{}).
		WhereHas("Contract", func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "A title") }).
		Get(&positions))

	assert.Len(t, positions, 1)
}

func TestHasOneThrough_WithWhereHasOnARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	positions := []hasOneThroughIntermediatePosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughIntermediatePosition{}).
		WithWhereHas("Contract", func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "A title") }).
		Get(&positions))

	require.Len(t, positions, 1)

	// Laravel asserts relationLoaded('contract') and contract.pluck('title')->unique() == ['A title'];
	// in Go the eager load shows as the field being populated by the query, and pluck('title') over
	// the contracts table (Model::__call forwards it to a fresh query) is that same single title.
	require.NotNil(t, positions[0].Contract)
	assert.Equal(t, "A title", positions[0].Contract.Title)

	var titles []string
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughContract{}).Pluck("title", &titles))
	assert.Equal(t, []string{"A title"}, titles)
}

func TestHasOneThrough_FirstOrFailThrowsAnException(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)

	position := hasOneThroughPosition{ID: 1, Name: "President", Shortname: "ps"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&position))
	user := hasOneThroughUser{ID: 1, Email: "taylorotwell@gmail.com", PositionShort: "ps"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&position, "User").Create(&user))

	// Laravel re-reads the position (first()) and calls the relation's firstOrFail(); the through
	// row exists but no contract does.
	found := hasOneThroughPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).First(&found))

	contract := hasOneThroughContract{}
	err := cloneRelationQuery(t, query).Related(&found, "Contract").FirstOrFail(&contract)
	require.Error(t, err)

	// Laravel throws ModelNotFoundException with the message "No query results for model
	// [Illuminate\Tests\Database\HasOneThroughTestContract]."; the Go ORM reports the fixed sentinel
	// errors.OrmRecordNotFound, whose message names no model.
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
}

func TestHasOneThrough_FindOrFailThrowsAnException(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)

	position := hasOneThroughPosition{ID: 1, Name: "President", Shortname: "ps"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&position))
	user := hasOneThroughUser{ID: 1, Email: "taylorotwell@gmail.com", PositionShort: "ps"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&position, "User").Create(&user))

	found := hasOneThroughPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).First(&found))

	contract := hasOneThroughContract{}
	err := cloneRelationQuery(t, query).Related(&found, "Contract").FindOrFail(&contract, 1)
	require.Error(t, err)

	// Laravel throws ModelNotFoundException naming the model and id; the Go ORM reports the fixed
	// sentinel errors.OrmRecordNotFound.
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
}

func TestHasOneThrough_FirstRetrievesFirstRecord(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	position := hasOneThroughPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&position).First(&position))

	contract := hasOneThroughContract{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&position, "Contract").First(&contract))
	assert.NotZero(t, contract.ID)
	assert.Equal(t, "A title", contract.Title)
}

func TestHasOneThrough_AllColumnsAreRetrievedByDefault(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	position := hasOneThroughPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&position).First(&position))

	contract := hasOneThroughContract{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&position, "Contract").First(&contract))

	// Laravel compares array_keys(getAttributes()) with ['id', 'user_id', 'title', 'body', 'email',
	// 'created_at', 'updated_at', 'laravel_through_key']. A typed Go model has no dynamic attribute
	// bag, so the port asserts every real column is hydrated; laravel_through_key is the through key
	// alias Laravel's first() selects alongside `"contracts".*` (oracle), which this read chain does
	// not produce (see the file header). The fixture keeps contract 1's key and email equal to
	// user 1's, so the joined read returns them unchanged (probe: with differing emails the through
	// row's value wins).
	assert.Equal(t, uint(1), contract.ID)
	assert.Equal(t, uint(1), contract.UserID)
	assert.Equal(t, "A title", contract.Title)
	assert.Equal(t, "A body", contract.Body)
	assert.Equal(t, "taylorotwell@gmail.com", contract.Email)
	assert.NotNil(t, contract.CreatedAt)
	assert.NotNil(t, contract.UpdatedAt)
}

func TestHasOneThrough_OnlyProperColumnsAreSelectedIfProvided(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	position := hasOneThroughPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&position).First(&position))

	// Laravel: $position->contract()->first(['title', 'body']). Go's First takes no column list, so
	// the columns are pinned on the chain before First; the observable — title and body hydrated and
	// nothing else — is the same. (Laravel's select also carries the laravel_through_key alias —
	// oracle: `"title", "body", "users"."position_id" as "laravel_through_key"` — which has no Go
	// counterpart; see the file header.)
	contract := hasOneThroughContract{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&position, "Contract").Select("title", "body").First(&contract))
	assert.Equal(t, "A title", contract.Title)
	assert.Equal(t, "A body", contract.Body)
	assert.Zero(t, contract.ID)
	assert.Zero(t, contract.UserID)
	assert.Equal(t, "", contract.Email)
}

func TestHasOneThrough_ChunkReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no Chunk method — chunking lives on the raw database/db query (Query.Chunk), and Query.Related returns an orm.Query, so the chain under test cannot run; probe: the equivalent Related(...).Get reads position 1's one contract (id 1, title \"A title\") in a single read, where Laravel's chunk(10) hands it back through its callback; the callback's expected attribute list also includes laravel_through_key, which our read chains do not produce")
}

func TestHasOneThrough_CursorReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: Cursor() streams the joined `select *` rows as raw db.Row values, not hydrated models — probe (fixture with contracts.email contract@example.com and users.email user@example.com): the row reads {id 1, user_id 1, title \"A title\", body \"A body\", email user@example.com, position_id 1, position_short \"ps\"}, with the through row's same-named columns overwriting the related row's and no laravel_through_key, where Laravel's cursor hydrates `\"contracts\".*` plus the laravel_through_key alias (the corpus asserts that attribute list)")
}

func TestHasOneThrough_EachReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no Each method — the 1000-row-chunked iterator lives on the raw database/db query (Query.Each), and Query.Related returns an orm.Query, so the chain under test cannot run; probe: the equivalent Related(...).Get reads position 1's one contract (title \"A title\") in a single read")
}

func TestHasOneThrough_LazyReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no Lazy method (it lives on the raw database/db query, Query.Lazy), so Laravel's lazy() stream cannot be run through Query.Related; probe: the equivalent Related(...).Get reads position 1's one contract in one read")
}

func TestHasOneThrough_IntermediateSoftDeletesAreIgnored(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	// Laravel: HasOneThroughSoftDeletesTestUser::first()->delete() — a soft delete of user 1
	// through the class that carries SoftDeletes. Probe: our Delete sets the row's deleted_at (the
	// instance's own DeletedAt field is not written back).
	softUser := hasOneThroughSoftUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughSoftUser{}).First(&softUser))
	_, err := cloneRelationQuery(t, query).Delete(&softUser)
	require.NoError(t, err)

	// The position's relation goes through the plain user class, which does not carry SoftDeletes,
	// so the soft delete is ignored and the contract still loads.
	position := hasOneThroughSoftPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&position).First(&position))
	require.NoError(t, cloneRelationQuery(t, query).Load(&position, "Contract"))

	require.NotNil(t, position.Contract)
	assert.Equal(t, "A title", position.Contract.Title)
}

func TestHasOneThrough_EagerLoadingLoadsRelatedModelsCorrectly(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughSchema(t, query)
	hasOneThroughSeedData(t, query)

	// Laravel: HasOneThroughSoftDeletesTestPosition::with('contract')->first(). The port reads
	// through Get: First(&struct) with an eager load leaves its single-row cursor open while
	// retrieved() runs the eager query, which deadlocks on the harness's one-connection pool, while
	// Get drains its cursor before the eager query runs. The observable is the same — the one
	// position row, with the relation attached.
	found := []hasOneThroughSoftPosition{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughSoftPosition{}).With("Contract").Get(&found))
	require.Len(t, found, 1)
	assert.Equal(t, "ps", found[0].Shortname)

	require.NotNil(t, found[0].Contract)
	assert.Equal(t, "A title", found[0].Contract.Title)
}
