package has_many_create_or_first

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasManyCreateOrFirstTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestHasManyCreateOrFirst_<method without the test prefix>.
//
// The corpus drives a HasMany relation over a mocked connection: setUp freezes Carbon's clock at
// 2023-01-01 00:00:00, the parent model lives in memory with id 123, and each test pins the SQL the
// flow issues — a lookup select, an insert of attr/val/parent_id/timestamps and, for updateOrCreate,
// an update of the found row. The oracle (.claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1) confirmed those three statements compile exactly as the corpus
// writes them:
//
//   - lookup: select * from "child_table" where "child_table"."parent_id" = ? and
//     "child_table"."parent_id" is not null and ("attr" = ?) limit 1
//   - insert: insert into "child_table" ("attr", "val", "parent_id", "updated_at", "created_at")
//     values (?, ?, ?, ?, ?)
//   - update: update "child_table" set "val" = ?, "updated_at" = ? where "id" = ?
//
// The port runs the same flows against the harness's real sqlite database: the parent row (and any
// pre-existing child) is seeded through the ORM, the flow is invoked through the Relation(model,
// name) writer in database/gorm/relation_writes.go, and the returned model plus the child_table
// rows are asserted. The clock is frozen with carbon.SetTestNow to the corpus's instant, so the
// timestamps a created or refreshed row lands on match the corpus's expected 2023-01-01 00:00:00.
//
// # How the port realises Laravel's mocked flows
//
//   - The relation writer now exposes CreateOrFirst: the insert-first flow runs for real, and its
//     UniqueConstraintViolationException fallback is staged against the schema's real
//     (parent_id, attr) unique index — a seeded winner row trips it, and the catch's write-PDO
//     re-read returns it.
//   - FirstOrCreateRelation / UpdateOrCreateRelation branch on firstRow's found flag now, so a
//     lookup miss creates the row with the parent key and the values (the creates-new flows below).
//   - The two "created just now" races stage the mock-era interleaving — lookup miss, insert,
//     unique violation, re-read of the row committed meanwhile — with a one-shot BEFORE INSERT
//     trigger standing in for the concurrent writer: when the API's insert is attempted the trigger
//     first commits the winner row (and disarms itself), so the insert trips the unique index and
//     the catch re-reads the winner.
//
// # wasRecentlyCreated
//
// Laravel asserts the model's wasRecentlyCreated flag; a Go model has no such state. Where the
// corpus asserts it, the port asserts its observable meaning — whether the call inserted a row —
// through the child_table row count and the returned model's key (a retrieved row keeps the seeded
// key and child_table keeps exactly one row).

// --- models ------------------------------------------------------------------

// hasManyCreateOrFirstParent ports HasManyCreateOrFirstTestParentModel (table parent_table).
type hasManyCreateOrFirstParent struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`
}

func (hasManyCreateOrFirstParent) TableName() string { return "parent_table" }

func (hasManyCreateOrFirstParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Children": contractsorm.HasMany{Related: &hasManyCreateOrFirstChild{}, ForeignKey: "parent_id"},
	}
}

// hasManyCreateOrFirstChild ports HasManyCreateOrFirstTestChildModel (table child_table).
type hasManyCreateOrFirstChild struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	ParentID  uint             `gorm:"column:parent_id"`
	Attr      string           `gorm:"column:attr"`
	Val       string           `gorm:"column:val"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasManyCreateOrFirstChild) TableName() string { return "child_table" }

// --- schema and seeding ------------------------------------------------------

// hasManyCreateOrFirstSchema creates the two tables the corpus's mocks imply: parent_table holds
// the parent's id, child_table holds the related rows. The unique index on (parent_id, attr) is
// what the corpus's mocked insert throwing UniqueConstraintViolationException presumes of the
// real-world table, and it makes any accidental duplicate insert in the ported flows fail loudly
// instead of silently adding a second row.
func hasManyCreateOrFirstSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table parent_table (id integer primary key autoincrement)`,
		`create table child_table (id integer primary key autoincrement, parent_id integer not null references parent_table(id), attr varchar not null, val varchar not null, created_at datetime null, updated_at datetime null)`,
		`create unique index child_table_parent_id_attr_unique on child_table (parent_id, attr)`,
	)
}

// hasManyCreateOrFirstFreezeNow mirrors the corpus's setUp: Carbon::setTestNow('2023-01-01
// 00:00:00'), undone when the test ends.
func hasManyCreateOrFirstFreezeNow(t *testing.T) *carbon.Carbon {
	t.Helper()

	now := carbon.Parse("2023-01-01 00:00:00")
	carbon.SetTestNow(now)
	t.Cleanup(carbon.ClearTestNow)

	return now
}

// hasManyCreateOrFirstStageCreatedJustNow stages the lost race of the two "created just now"
// tests: a one-shot BEFORE INSERT trigger on child_table inserts the row the concurrent writer
// would have committed (id 456, attr 'foo', val 'bar', the frozen timestamps), disarming itself
// first so its own insert cannot re-fire it. The trigger then aborts the API's insert with
// RAISE(FAIL) carrying the unique-constraint message — FAIL is SQLite's only abort that keeps a
// statement's prior changes, so the staged winner survives the failed insert (a plain constraint
// violation would roll it back with the statement) — and the create-or-first catch re-reads the
// winner over the write PDO exactly as it would after a real lost race.
func hasManyCreateOrFirstStageCreatedJustNow(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table race_gate (armed integer not null)`,
		`insert into race_gate (armed) values (1)`,
		`create trigger child_table_created_just_now before insert on child_table when (select armed from race_gate) = 1 begin update race_gate set armed = 0; insert into child_table (id, parent_id, attr, val, created_at, updated_at) values (456, 123, 'foo', 'bar', '2023-01-01 00:00:00', '2023-01-01 00:00:00'); select raise(fail, 'UNIQUE constraint failed: child_table.parent_id, child_table.attr'); end`,
	)
}

// hasManyCreateOrFirstSeedParent creates the parent row the child inserts' foreign key needs. The
// corpus holds the parent in memory only; the port also stores the row so the FK constraint is
// satisfied, with the same id the in-memory model carries.
func hasManyCreateOrFirstSeedParent(t *testing.T, query contractsorm.Query, id uint) hasManyCreateOrFirstParent {
	t.Helper()

	parent := hasManyCreateOrFirstParent{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{"id": id}))

	return parent
}

// hasManyCreateOrFirstSeedChild writes a child row with an explicit key and the corpus's frozen
// timestamps, standing in for the row the corpus's mocked select returns.
func hasManyCreateOrFirstSeedChild(t *testing.T, query contractsorm.Query, id, parentID uint, attr, val string, now *carbon.Carbon) hasManyCreateOrFirstChild {
	t.Helper()

	child := hasManyCreateOrFirstChild{
		ID:        id,
		ParentID:  parentID,
		Attr:      attr,
		Val:       val,
		CreatedAt: carbon.NewDateTime(now),
		UpdatedAt: carbon.NewDateTime(now),
	}
	require.NoError(t, cloneRelationQuery(t, query).Create(&child))

	return child
}

// --- ports of the corpus -----------------------------------------------------

func TestHasManyCreateOrFirst_CreateOrFirstMethodCreatesNewRecord(t *testing.T) {
	now := hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)

	// Laravel: createOrFirst(['attr' => 'foo'], ['val' => 'bar']) inserts immediately — no lookup —
	// and returns the created model with wasRecentlyCreated=true.
	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	require.NotZero(t, result.ID, "a successful insert returns the created model")
	assert.Equal(t, uint(123), result.ParentID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

func TestHasManyCreateOrFirst_CreateOrFirstMethodRetrievesExistingRecord(t *testing.T) {
	now := hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)
	// The seeded row is what the mocked insert's UniqueConstraintViolationException stands for: the
	// real (parent_id, attr) unique index makes the insert attempt collide with it, and the catch's
	// write-PDO re-read returns it with wasRecentlyCreated=false.
	seeded := hasManyCreateOrFirstSeedChild(t, query, 456, 123, "foo", "bar", now)

	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, seeded.ID, result.ID, "the insert lost the race and the re-read returned the winner")
	assert.Equal(t, uint(123), result.ParentID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

func TestHasManyCreateOrFirst_FirstOrCreateMethodCreatesNewRecord(t *testing.T) {
	now := hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)

	// Laravel: the lookup (the oracle-checked select) comes back empty and the create inserts the
	// merged attributes with parent_id 123; the result has wasRecentlyCreated=true.
	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	require.NotZero(t, result.ID, "the created row's key must be read back")
	assert.Equal(t, uint(123), result.ParentID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Corpus: children()->firstOrCreate(['attr' => 'foo'], ['val' => 'bar']) where the mocked lookup
// (the oracle-checked select) already returns the row id 456 with attr 'foo', val 'bar' and the
// frozen timestamps; nothing is inserted and the values are not applied. The port seeds exactly
// that row and observes the same: the returned model is the stored row and child_table still holds
// one row.
func TestHasManyCreateOrFirst_FirstOrCreateMethodRetrievesExistingRecord(t *testing.T) {
	now := hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)
	seeded := hasManyCreateOrFirstSeedChild(t, query, 456, 123, "foo", "bar", now)

	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	// wasRecentlyCreated false: the returned model is the seeded row (same key, untouched values)
	// and child_table still holds one row.
	assert.Equal(t, seeded.ID, result.ID)
	assert.Equal(t, uint(123), result.ParentID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

func TestHasManyCreateOrFirst_FirstOrCreateMethodRetrievesRecordCreatedJustNow(t *testing.T) {
	now := hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)
	hasManyCreateOrFirstStageCreatedJustNow(t, query)

	// Laravel's mocked sequence: the lookup misses, the insert throws
	// UniqueConstraintViolationException, and the write-PDO re-read returns the row created just
	// now with wasRecentlyCreated=false.
	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, uint(456), result.ID, "the write-PDO re-read must return the winner of the race")
	assert.Equal(t, uint(123), result.ParentID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

func TestHasManyCreateOrFirst_UpdateOrCreateMethodCreatesNewRecord(t *testing.T) {
	hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)

	// Laravel: the lookup misses, firstOrCreate creates the merged attributes (including the update
	// values) with parent_id 123, and the fresh instance is not saved a second time.
	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").UpdateOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	require.NotZero(t, result.ID, "the created row's key must be read back")
	assert.Equal(t, uint(123), result.ParentID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)

	reloaded := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", result.ID).First(&reloaded))
	assert.Equal(t, "foo", reloaded.Attr)
	assert.Equal(t, "bar", reloaded.Val)
	assert.Equal(t, uint(123), reloaded.ParentID)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Corpus: children()->updateOrCreate(['attr' => 'foo'], ['val' => 'baz']) where the mocked lookup
// returns the row (wasRecentlyCreated=false), so the instance is filled with the values and saved —
// `update "child_table" set "val" = ?, "updated_at" = ? where "id" = ?`, oracle-checked. The port
// seeds the row and observes the returned model carry val 'baz' while the stored row was refreshed
// in place, not duplicated.
func TestHasManyCreateOrFirst_UpdateOrCreateMethodUpdatesExistingRecord(t *testing.T) {
	now := hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)
	seeded := hasManyCreateOrFirstSeedChild(t, query, 456, 123, "foo", "bar", now)

	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").UpdateOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &result))

	assert.Equal(t, seeded.ID, result.ID)
	assert.Equal(t, uint(123), result.ParentID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "baz", result.Val)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	// The stored row was updated in place — one row, new val, timestamps at the frozen instant.
	reloaded := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", 456).First(&reloaded))
	assert.Equal(t, "foo", reloaded.Attr)
	assert.Equal(t, "baz", reloaded.Val)
	require.NotNil(t, reloaded.CreatedAt)
	require.NotNil(t, reloaded.UpdatedAt)
	assert.True(t, reloaded.CreatedAt.StdTime().Equal(now.StdTime()))
	assert.True(t, reloaded.UpdatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

func TestHasManyCreateOrFirst_UpdateOrCreateMethodUpdatesRecordCreatedJustNow(t *testing.T) {
	now := hasManyCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyCreateOrFirstSchema(t, query)

	parent := hasManyCreateOrFirstSeedParent(t, query, 123)
	hasManyCreateOrFirstStageCreatedJustNow(t, query)

	// Laravel's mocked sequence: lookup miss, insert, UniqueConstraintViolationException, re-read of
	// the row created just now (wasRecentlyCreated=false), then the values are filled onto it and
	// saved — one row, updated in place.
	result := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").UpdateOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &result))

	assert.Equal(t, uint(456), result.ID)
	assert.Equal(t, "baz", result.Val)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	reloaded := hasManyCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", 456).First(&reloaded))
	assert.Equal(t, "foo", reloaded.Attr)
	assert.Equal(t, "baz", reloaded.Val)
	assert.Equal(t, uint(123), reloaded.ParentID)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}
