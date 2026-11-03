package has_many_through_create_or_first

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasManyThroughCreateOrFirstTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestHasManyThroughCreateOrFirst_<method without the test prefix>.
//
// The corpus drives create-or-first writes through a hasManyThrough relation: the parent model
// relates child through pivot with hasManyThrough(Child, Pivot, 'parent_id', 'pivot_id'), so the
// through query joins "pivot" on "pivot"."id" = "child"."pivot_id" and constrains
// "pivot"."parent_id" to the parent's id. Every test mocks the connection; its observables are the
// instance the create-or-first flow returns (wasRecentlyCreated and its attribute values) plus the
// SQL the flow runs. The mock-pinned SQL, re-measured with the oracle
// (.claude/laravel-oracle/oracle.php, illuminate/database v12.55.1, sqlite), so a future port could
// be checked against execution rather than recall:
//
//   - the joined lookup select: `select "child".*, "pivot"."parent_id" as "laravel_through_key" from
//     "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = ?
//     and ("attr" = ?) limit 1`
//   - the "created just now" fallback re-select: the same with `("attr" = ? and "val" = ?)`
//   - the create insert: `insert into "child" ("attr", "val", "updated_at", "created_at") values
//     (?, ?, ?, ?)`
//   - updateOrCreate's save of a found row: `update "child" set "val" = ?, "updated_at" = ? where
//     "id" = ?`
//
// # How the port realises Laravel's mocked flows
//
//   - The Relation(model, name) writer dispatches hasManyThrough kinds into the create-or-first
//     family, so FirstOrNew / FirstOrCreate / CreateOrFirst / UpdateOrCreate run against the
//     harness's real sqlite database: the lookup is the joined select above, the create inserts the
//     related row exactly as the corpus's mocked insert does (no through key on the far row —
//     HasOneOrManyThrough carries no setForeignAttributesForCreate), and the retry re-reads through
//     the join.
//   - The lost races ("created just now") stage the concurrent commit with a one-shot BEFORE
//     INSERT trigger: it commits the winner row when the API's insert is attempted, so the real
//     child(attr) unique index trips and the catch's joined re-select finds it (no write PDO: the
//     corpus's mocked select passes useReadPdo true on this path) — the mock-era sequence the
//     corpus asserts.
//   - The through-key alias (laravel_through_key) has no Go counterpart: a hydrated model carries
//     its own columns only (see the sibling has_many_through port).

// --- models ------------------------------------------------------------------

// hasManyThroughCreateOrFirstParent ports HasManyThroughCreateOrFirstTestParentModel (table
// "parent"): child through pivot on parent_id -> pivot_id, the corpus's hasManyThrough() keys.
type hasManyThroughCreateOrFirstParent struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`

	Children []*hasManyThroughCreateOrFirstChild `gorm:"-"`
}

func (hasManyThroughCreateOrFirstParent) TableName() string { return "parent" }

func (hasManyThroughCreateOrFirstParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Children": contractsorm.HasManyThrough{
			Related: &hasManyThroughCreateOrFirstChild{}, Through: &hasManyThroughCreateOrFirstPivot{},
			FirstKey: "parent_id", SecondKey: "pivot_id",
		},
	}
}

// hasManyThroughCreateOrFirstPivot ports HasManyThroughCreateOrFirstTestPivotModel (table "pivot").
type hasManyThroughCreateOrFirstPivot struct {
	ID       uint `gorm:"column:id;primaryKey;autoIncrement"`
	ParentID uint `gorm:"column:parent_id"`
}

func (hasManyThroughCreateOrFirstPivot) TableName() string { return "pivot" }

// hasManyThroughCreateOrFirstChild ports HasManyThroughCreateOrFirstTestChildModel (table "child").
type hasManyThroughCreateOrFirstChild struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	PivotID   uint             `gorm:"column:pivot_id"`
	Attr      string           `gorm:"column:attr"`
	Val       string           `gorm:"column:val"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasManyThroughCreateOrFirstChild) TableName() string { return "child" }

// hasManyThroughCreateOrFirstSchema creates the three tables the corpus's mocks imply: parent(id);
// pivot(id, parent_id); child(id, pivot_id, attr, val, created_at, updated_at). The corpus never
// migrates — it mocks the connection — so the DDL is reconstructed from the mock SQL and the
// relation's keys. pivot_id is nullable: the through create inserts the related row without a
// through key, so a created row has none, while seeded rows carry the key the joined read resolves.
func hasManyThroughCreateOrFirstSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table parent (id integer primary key autoincrement)`,
		`create table pivot (id integer primary key autoincrement, parent_id integer not null)`,
		`create table child (id integer primary key autoincrement, pivot_id integer null, attr varchar not null, val varchar not null, created_at datetime null, updated_at datetime null)`,
	)
}

// hasManyThroughCreateOrFirstFreezeNow mirrors the corpus's setUp: Carbon::setTestNow('2023-01-01
// 00:00:00'), undone when the test ends.
func hasManyThroughCreateOrFirstFreezeNow(t *testing.T) *carbon.Carbon {
	t.Helper()

	now := carbon.Parse("2023-01-01 00:00:00")
	carbon.SetTestNow(now)
	t.Cleanup(carbon.ClearTestNow)

	return now
}

// hasManyThroughCreateOrFirstSeedPivot writes the pivot row a related row joins through.
func hasManyThroughCreateOrFirstSeedPivot(t *testing.T, query contractsorm.Query, id, parentID uint) {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstPivot{}).Create(map[string]any{
		"id": id, "parent_id": parentID,
	}))
}

// hasManyThroughCreateOrFirstSeedChild writes a related row attached to its pivot with the
// corpus's frozen timestamps, standing in for the row the corpus's mocked select returns.
func hasManyThroughCreateOrFirstSeedChild(t *testing.T, query contractsorm.Query, id, pivotID uint, attr, val string, now *carbon.Carbon) hasManyThroughCreateOrFirstChild {
	t.Helper()

	child := hasManyThroughCreateOrFirstChild{
		ID:        id,
		PivotID:   pivotID,
		Attr:      attr,
		Val:       val,
		CreatedAt: carbon.NewDateTime(now),
		UpdatedAt: carbon.NewDateTime(now),
	}
	require.NoError(t, cloneRelationQuery(t, query).Create(&child))

	return child
}

// hasManyThroughCreateOrFirstStageCreatedJustNow stages the lost race of the two "created just
// now" tests: a one-shot BEFORE INSERT trigger on child inserts the row the concurrent writer
// would have committed — attached to pivot 456, attr 'foo', val 'bar', the frozen timestamps —
// disarming itself first so its own insert cannot re-fire it. The trigger then aborts the API's
// insert with RAISE(FAIL) carrying the unique-constraint message — FAIL is SQLite's only abort
// that keeps a statement's prior changes, so the staged winner survives the failed insert — and
// the catch's joined re-read finds it exactly as after a real lost race (no write PDO: the
// corpus's mocked select passes useReadPdo true).
func hasManyThroughCreateOrFirstStageCreatedJustNow(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table race_gate (armed integer not null)`,
		`insert into race_gate (armed) values (1)`,
		`create trigger child_created_just_now before insert on child when (select armed from race_gate) = 1 begin update race_gate set armed = 0; insert into child (id, pivot_id, attr, val, created_at, updated_at) values (789, 456, 'foo', 'bar', '2023-01-01 00:00:00', '2023-01-01 00:00:00'); select raise(fail, 'UNIQUE constraint failed: child.pivot_id, child.attr'); end`,
	)
}

// --- ports of the corpus -----------------------------------------------------

// Laravel: $parent->children()->createOrFirst(['attr' => 'foo'], ['val' => 'bar']) on a parent with
// id 123 that does not exist yet; the insert succeeds and the result has wasRecentlyCreated=true.

func TestHasManyThroughCreateOrFirst_CreateOrFirstMethodCreatesNewRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}

	// Laravel: createOrFirst(['attr' => 'foo'], ['val' => 'bar']) inserts immediately — the insert
	// carries no through key, exactly as the corpus's mocked insert — and the result has
	// wasRecentlyCreated=true.
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	require.NotZero(t, result.ID, "a successful insert returns the created model")
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Laravel: $parent->children()->createOrFirst(['attr' => 'foo'], ['val' => 'bar']) when the insert
// hits a unique constraint; the catch re-selects by attributes and returns wasRecentlyCreated=false.

func TestHasManyThroughCreateOrFirst_CreateOrFirstMethodRetrievesExistingRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	// The seeded row is what the mocked UniqueConstraintViolationException stands for: the real
	// child(attr) unique index makes the insert attempt collide with it, and the catch re-selects it
	// through the join (no write PDO: useReadPdo true in the corpus's mock).
	runRelationSchema(t, query, `create unique index child_attr_unique on child (attr)`)
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	seeded := hasManyThroughCreateOrFirstSeedChild(t, query, 789, 456, "foo", "bar", now)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, seeded.ID, result.ID, "the insert lost the race and the re-read returned the winner")
	assert.Equal(t, uint(456), result.PivotID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Laravel: $parent->children()->firstOrCreate(['attr' => 'foo'], ['val' => 'bar']) with an empty
// lookup; the create inserts the merged attributes and the result has wasRecentlyCreated=true.

func TestHasManyThroughCreateOrFirst_FirstOrCreateMethodCreatesNewRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}

	// Laravel: the joined lookup comes back empty and the create inserts the merged attributes; the
	// result has wasRecentlyCreated=true.
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	require.NotZero(t, result.ID, "the created row's key must be read back")
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))
	assert.True(t, result.UpdatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Laravel: $parent->children()->firstOrCreate(['attr' => 'foo'], ['val' => 'bar']) where the
// lookup finds the existing row; it is returned untouched with wasRecentlyCreated=false.

func TestHasManyThroughCreateOrFirst_FirstOrCreateMethodRetrievesExistingRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	seeded := hasManyThroughCreateOrFirstSeedChild(t, query, 789, 456, "foo", "bar", now)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, seeded.ID, result.ID, "the joined lookup returns the row untouched")
	assert.Equal(t, uint(456), result.PivotID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Laravel: $parent->children()->firstOrCreate(['attr' => 'foo'], ['val' => 'bar']) when the row was
// created between the lookup and the insert; the unique violation sends createOrFirst's catch to
// re-select by attributes plus values.

func TestHasManyThroughCreateOrFirst_FirstOrCreateMethodRetrievesRecordCreatedJustNow(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	runRelationSchema(t, query, `create unique index child_attr_unique on child (attr)`)
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	hasManyThroughCreateOrFirstStageCreatedJustNow(t, query)

	// Laravel's mocked sequence: the joined lookup misses, the insert throws, and createOrFirst's
	// catch re-selects by the merged attributes through the join (useReadPdo true in the corpus).
	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, uint(789), result.ID, "the catch's re-read must return the winner of the race")
	assert.Equal(t, uint(456), result.PivotID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	require.NotNil(t, result.UpdatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Laravel: $parent->children()->updateOrCreate(['attr' => 'foo'], ['val' => 'baz']) with an empty
// lookup; firstOrCreate creates the row with val 'baz' and the result has wasRecentlyCreated=true.

func TestHasManyThroughCreateOrFirst_UpdateOrCreateMethodCreatesNewRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}

	// Laravel: the lookup misses, the create inserts the merged attributes (val 'baz') and the
	// fresh instance is not saved a second time.
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").UpdateOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &result))

	require.NotZero(t, result.ID, "the created row's key must be read back")
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "baz", result.Val)
	require.NotNil(t, result.CreatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))

	reloaded := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", result.ID).First(&reloaded))
	assert.Equal(t, "foo", reloaded.Attr)
	assert.Equal(t, "baz", reloaded.Val)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Laravel: $parent->children()->updateOrCreate(['attr' => 'foo'], ['val' => 'baz']) where the
// lookup finds the row; it is filled with the values and saved.

func TestHasManyThroughCreateOrFirst_UpdateOrCreateMethodUpdatesExistingRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	seeded := hasManyThroughCreateOrFirstSeedChild(t, query, 789, 456, "foo", "bar", now)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").UpdateOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &result))

	assert.Equal(t, seeded.ID, result.ID)
	assert.Equal(t, uint(456), result.PivotID)
	assert.Equal(t, "baz", result.Val, "the found row is filled with the values and saved")

	reloaded := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", seeded.ID).First(&reloaded))
	assert.Equal(t, "foo", reloaded.Attr)
	assert.Equal(t, "baz", reloaded.Val)
	assert.Equal(t, uint(456), reloaded.PivotID)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Laravel: the method name says updateOrCreate but the body calls
// $parent->children()->firstOrCreate(['attr' => 'foo'], ['val' => 'bar']) with the lookup empty and
// the insert hitting a unique violation; the fallback re-select returns wasRecentlyCreated=false.

func TestHasManyThroughCreateOrFirst_UpdateOrCreateMethodUpdatesRecordCreatedJustNow(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	runRelationSchema(t, query, `create unique index child_attr_unique on child (attr)`)
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	hasManyThroughCreateOrFirstStageCreatedJustNow(t, query)

	// The corpus's test body calls firstOrCreate (the UpdateOrCreate name is a copy-paste leftover):
	// lookup miss -> insert -> UniqueConstraintViolationException -> re-select by attrs plus values
	// -> wasRecentlyCreated=false. The name is kept so the port stays one-to-one with the corpus.
	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Children").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, uint(789), result.ID)
	assert.Equal(t, uint(456), result.PivotID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	require.NotNil(t, result.CreatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}
