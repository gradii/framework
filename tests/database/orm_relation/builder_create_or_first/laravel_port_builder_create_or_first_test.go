package builder_create_or_first

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBuilderCreateOrFirstTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestBuilderCreateOrFirst_<method without the test prefix>.
//
// The corpus is a Mockery-driven Eloquent builder suite: every test mocks the connection and pins the
// statements the builder compiles, then asserts the returned model's attribute bag ($result->toArray())
// and wasRecentlyCreated. The port keeps the same observable outcomes against a real sqlite database:
// the schema the corpus's model implies (a "table" with id, attr, val, count and the timestamp
// columns), rows seeded through the ORM, the same builder calls, and assertions on the returned
// model's attributes and on the stored row. Laravel's setUp pins Carbon::setTestNow('2023-01-01
// 00:00:00'); the port freezes goravel's clock the same way, so created_at and updated_at are
// asserted against the frozen instant.
//
// # The statements behind the corpus, measured with the oracle
//
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, executed against a real
// sqlite database: /tmp/cof_builder_probe.php and /tmp/cof_builder_race_probe.php.) The corpus pins
// these strings in its mock expectations; the third argument of its mocked selects is useWritePdo,
// which the harness's single connection cannot observe.
//
//	firstOrCreate lookup   select * from "table" where ("attr" = ?) limit 1
//	create insert          insert into "table" ("attr", "val", "updated_at", "created_at") values (?, ?, ?, ?)
//	createOrFirst catch    select * from "table" where ("attr" = ?) limit 1   (after the insert's violation)
//	updateOrCreate save    update "table" set "val" = ?, "updated_at" = ? where "id" = ?
//	incrementOrCreate      update "table" set "count" = "count" + 1, "updated_at" = ? where "id" = ?
//	                       update "table" set "count" = "count" + 2, "val" = ?, "updated_at" = ? where "id" = ?
//
// Our compiled statements differ in shape — the lookup drops the nested group and adds the ORM's
// tie-breaker order ("select * from "table" where "attr" = ? order by "table"."id" limit 1"), the
// insert renders sorted columns and carries count explicitly ("insert into "table" ("attr", "count",
// "created_at", "updated_at", "val") values (?, ?, ?, ?, ?)"), and Save writes the dirty-only update the oracle measured ("update "table" set "val" = ?,
// "updated_at" = ? where "id" = ?" — the loaded row's unchanged columns stay out of the
// statement) — so the port asserts the corpus's outcomes (loaded
// rows, mutated attributes) rather than its SQL text; the resulting row state is identical.
//
// # createOrFirst and the "record created just now" race, on a real database
//
// Query.CreateOrFirst mirrors Builder::createOrFirst (insert first; a unique-constraint violation is
// caught and the row re-read over the write connection, inside a savepoint when a transaction is
// open), and FirstOrCreate / UpdateOrCreate route a lookup miss through the same catch, as Laravel's
// firstOrCreate does. The corpus's "retrieves the existing record" case needs only the unique index
// and a seeded row; its three "created just now" cases mock the insert to throw after the lookup
// missed — a concurrent writer won the race — and pin the re-select that follows, so on a real
// database the racing writer has to exist:
//
//   - A BEFORE INSERT trigger planting the conflicting row is NOT a stand-in. sqlite rolls the whole
//     failing statement back, the trigger's row included, so the re-select misses and Laravel
//     itself rethrows (oracle: "THREW UniqueConstraintViolationException ... [rows=0]" for
//     firstOrCreate, updateOrCreate and incrementOrCreate alike). An earlier skip reason here blamed
//     our ORM for that outcome; it is the trigger's.
//   - The port plants the row from a `creating` model event instead — a separate, autocommitted
//     statement that runs between the lookup miss and the insert, exactly where the corpus's
//     concurrent writer sits. The oracle's Laravel run with the same creating listener reproduces
//     the corpus's observables: firstOrCreate => select (miss), insert (violation), select (hit),
//     id 1, wasRecentlyCreated false, val bar, one stored row; updateOrCreate => the same, then
//     update "table" set "val" = ?, "updated_at" = ? where "id" = ?, val baz.
//
// # incrementOrCreate, ported
//
// Query.IncrementOrCreate ports Builder::incrementOrCreate — firstOrCreate($attributes, [$column =>
// $default]) and, unless the row was just created, Model::increment's step: the column moves in
// memory and the extra values fill before the updating event, the scope-less update keyed on the
// model compiles the raw `<column> + <step>` expression, the extras and the refreshed updated-at
// stamp, the updated event runs, and the incremented column re-baselines in the original bag, so a
// later Save writes the extras and updated_at alone. Laravel's wasRecentlyCreated signal rides
// FirstOrCreate internally (firstOrCreateWithCreated); a Go model carries no such flag. Oracle
// v12.55.1, executed: /tmp/cof_builder_probe.php (found => count+1 + updated_at, count 2; miss =>
// insert count=1, no update after it; step+extra => count+2 and val in one update),
// /tmp/cof_builder_race_probe.php (the creating-event race re-select => count 2),
// /tmp/cof_inc_mem_probe.php (the in-memory count/val sync, the stale in-memory updated_at, and a
// later save writing the extras alone), /tmp/cof_inc_events_probe.php (updating and updated alone).

// --- model -------------------------------------------------------------------

// builderCreateOrFirstTable ports EloquentBuilderCreateOrFirstTestModel: table "table", $guarded = []
// (every column mass assignable), and the default timestamps — goravel's autoCreateTime/autoUpdateTime
// tags play Laravel's $timestamps role.
type builderCreateOrFirstTable struct {
	ID        uint64           `gorm:"column:id;primaryKey;autoIncrement"`
	Attr      string           `gorm:"column:attr"`
	Val       string           `gorm:"column:val"`
	Count     int              `gorm:"column:count"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (builderCreateOrFirstTable) TableName() string { return "table" }

// builderCreateOrFirstRacingTable is the same model with the corpus's concurrent writer attached: its
// creating event runs builderCreateOrFirstRacingWriter, so the conflicting row lands between the
// lookup miss and the insert, the moment the corpus's mocked insert throws its
// UniqueConstraintViolationException. Only the "record created just now" ports use it.
type builderCreateOrFirstRacingTable struct {
	ID        uint64           `gorm:"column:id;primaryKey;autoIncrement"`
	Attr      string           `gorm:"column:attr"`
	Val       string           `gorm:"column:val"`
	Count     int              `gorm:"column:count"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (builderCreateOrFirstRacingTable) TableName() string { return "table" }

func (builderCreateOrFirstRacingTable) DispatchesEvents() map[contractsorm.EventType]func(contractsorm.Event) error {
	return map[contractsorm.EventType]func(contractsorm.Event) error{
		contractsorm.EventCreating: func(contractsorm.Event) error {
			if builderCreateOrFirstRacingWriter == nil {
				return nil
			}

			return builderCreateOrFirstRacingWriter()
		},
	}
}

// builderCreateOrFirstRacingWriter is the concurrent writer a "created just now" test arms (and
// disarms in its cleanup); the package's tests run sequentially, so the single slot is safe.
var builderCreateOrFirstRacingWriter func() error

// --- schema and helpers ------------------------------------------------------

// builderCreateOrFirstSchema creates the table the corpus's model implies. The unique index on attr
// is what makes the corpus's create-vs-conflict scenarios meaningful: it produces the violation the
// corpus's mocked insert throws.
func builderCreateOrFirstSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table "table" (id integer primary key autoincrement, attr varchar not null, val varchar, count integer not null default 0, created_at datetime null, updated_at datetime null)`,
		`create unique index "table_attr_unique" on "table" ("attr")`,
	)
}

// freezeBuilderCreateOrFirstClock pins the clock the way the corpus's setUp does.
func freezeBuilderCreateOrFirstClock(t *testing.T) *carbon.Carbon {
	t.Helper()

	now := carbon.Parse("2023-01-01 00:00:00")
	carbon.SetTestNow(now)
	t.Cleanup(carbon.ClearTestNow)

	return now
}

// builderCreateOrFirstSeedRow writes the existing "table" row the corpus's mocks return.
func builderCreateOrFirstSeedRow(t *testing.T, query contractsorm.Query, attr, val string) builderCreateOrFirstTable {
	t.Helper()

	row := builderCreateOrFirstTable{Attr: attr, Val: val}
	require.NoError(t, cloneRelationQuery(t, query).Create(&row))

	return row
}

// builderCreateOrFirstSeedIncrementRow writes the row the incrementOrCreate found-path tests start
// from: attr foo at the given count — the corpus's mocked select returns id 123, attr foo, count 1
// (val "bar" only in the extra-values case).
func builderCreateOrFirstSeedIncrementRow(t *testing.T, query contractsorm.Query, val string, count int) builderCreateOrFirstTable {
	t.Helper()

	row := builderCreateOrFirstTable{Attr: "foo", Val: val, Count: count}
	require.NoError(t, cloneRelationQuery(t, query).Create(&row))

	return row
}

// armBuilderCreateOrFirstRace installs the corpus's concurrent writer for one test: when the racing
// model's insert is about to run, the writer creates the row (attr foo, the given val, timestamps at
// the frozen clock) through its own handle, so the insert then trips the unique index.
func armBuilderCreateOrFirstRace(t *testing.T, query contractsorm.Query, val string) {
	t.Helper()

	builderCreateOrFirstRacingWriter = func() error {
		row := builderCreateOrFirstTable{Attr: "foo", Val: val}

		return cloneRelationQuery(t, query).Create(&row)
	}
	t.Cleanup(func() { builderCreateOrFirstRacingWriter = nil })
}

// armBuilderCreateOrFirstRaceIncrement is armBuilderCreateOrFirstRace for the incrementOrCreate
// just-now test: the corpus's re-select returns the row the racing writer created with count 1 (the
// failed insert carried the count default), so the planted row carries that count.
func armBuilderCreateOrFirstRaceIncrement(t *testing.T, query contractsorm.Query) {
	t.Helper()

	builderCreateOrFirstRacingWriter = func() error {
		row := builderCreateOrFirstTable{Attr: "foo", Count: 1}

		return cloneRelationQuery(t, query).Create(&row)
	}
	t.Cleanup(func() { builderCreateOrFirstRacingWriter = nil })
}

// builderCreateOrFirstCount counts the table's rows, the port's stand-in for observing whether an
// insert ran.
func builderCreateOrFirstCount(t *testing.T, query contractsorm.Query) int64 {
	t.Helper()

	count, err := cloneRelationQuery(t, query).Model(&builderCreateOrFirstTable{}).Count()
	require.NoError(t, err)

	return count
}

// builderCreateOrFirstReload reads a row back by its key.
func builderCreateOrFirstReload(t *testing.T, query contractsorm.Query, id uint64) builderCreateOrFirstTable {
	t.Helper()

	var row builderCreateOrFirstTable
	require.NoError(t, cloneRelationQuery(t, query).Where("id", id).First(&row))

	return row
}

// builderCreateOrFirstAssertFrozen pins a model timestamp to the frozen instant, the port of the
// corpus's '2023-01-01T00:00:00.000000Z' array entries.
func builderCreateOrFirstAssertFrozen(t *testing.T, now *carbon.Carbon, value *carbon.DateTime, label string) {
	t.Helper()

	require.NotNil(t, value, label+": the model must carry a timestamp")
	assert.True(t, now.StdTime().Equal(value.StdTime()), label+": want the frozen 2023-01-01 00:00:00, got %s", value.StdTime())
}

// --- ports of the corpus -----------------------------------------------------

func TestBuilderCreateOrFirst_CreateOrFirstMethodCreatesNewRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	assert.Equal(t, int64(0), builderCreateOrFirstCount(t, query), "the table starts empty")

	// Laravel: createOrFirst(['attr' => 'foo'], ['val' => 'bar']) inserts once — oracle: insert into
	// "table" ("attr", "val", "updated_at", "created_at") values (?, ?, ?, ?), no lookup before it —
	// with the mocked lastInsertId 123, and returns a model whose toArray carries attr, val and the
	// test-now timestamps, wasRecentlyCreated true. The real database assigns id 1; the generated key
	// being read back is the observable the corpus's 123 stands for.
	created := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&created).CreateOrFirst(&created, map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}))

	assert.NotZero(t, created.ID, "CreateOrFirst must read the generated key back")
	assert.Equal(t, "foo", created.Attr)
	assert.Equal(t, "bar", created.Val)
	assert.Equal(t, 0, created.Count)
	builderCreateOrFirstAssertFrozen(t, now, created.CreatedAt, "created.created_at")
	builderCreateOrFirstAssertFrozen(t, now, created.UpdatedAt, "created.updated_at")

	stored := builderCreateOrFirstReload(t, query, created.ID)
	assert.Equal(t, created.ID, stored.ID)
	assert.Equal(t, "foo", stored.Attr)
	assert.Equal(t, "bar", stored.Val)
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query))
}

func TestBuilderCreateOrFirst_CreateOrFirstMethodRetrievesExistingRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// The corpus's mocked insert throws UniqueConstraintViolationException and its mocked re-select
	// returns id 123, attr foo, val bar, timestamps at the test now. On the real database the seeded
	// row and the unique index produce the violation — oracle: the insert fails and createOrFirst
	// re-selects with select * from "table" where ("attr" = ?) limit 1 — and the found row is the
	// seeded one, wasRecentlyCreated false.
	existing := builderCreateOrFirstSeedRow(t, query, "foo", "bar")

	found := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).CreateOrFirst(&found, map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}))

	// Laravel asserts the returned model's toArray() equals the row, wasRecentlyCreated false. The Go
	// model has no such flag; the equivalent observable is that the found model carries the existing
	// row's own key and timestamps and that nothing was inserted.
	assert.Equal(t, existing.ID, found.ID)
	assert.Equal(t, "foo", found.Attr)
	assert.Equal(t, "bar", found.Val)
	assert.Equal(t, 0, found.Count)
	builderCreateOrFirstAssertFrozen(t, now, found.CreatedAt, "found.created_at")
	builderCreateOrFirstAssertFrozen(t, now, found.UpdatedAt, "found.updated_at")

	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query), "the caught violation must not leave a second row")
}

func TestBuilderCreateOrFirst_FirstOrCreateMethodRetrievesExistingRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// The corpus's mocked select returns the row it was told to: id 123, attr foo, val bar, both
	// timestamps at the test now.
	existing := builderCreateOrFirstSeedRow(t, query, "foo", "bar")

	found := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).FirstOrCreate(&found, map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}))

	// Laravel asserts the returned model's toArray() equals the row, wasRecentlyCreated false. The Go
	// model has no such flag; the equivalent observable is that the found model carries the existing
	// row's own key and timestamps and that nothing was inserted.
	assert.Equal(t, existing.ID, found.ID)
	assert.Equal(t, "foo", found.Attr)
	assert.Equal(t, "bar", found.Val)
	assert.Equal(t, 0, found.Count)
	builderCreateOrFirstAssertFrozen(t, now, found.CreatedAt, "found.created_at")
	builderCreateOrFirstAssertFrozen(t, now, found.UpdatedAt, "found.updated_at")

	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query), "the found path must not insert a second row")
}

func TestBuilderCreateOrFirst_FirstOrCreateMethodCreatesNewRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	assert.Equal(t, int64(0), builderCreateOrFirstCount(t, query), "the table starts empty")

	// Laravel: firstOrCreate(['attr' => 'foo'], ['val' => 'bar']) finds nothing, inserts attr and val
	// with the test-now timestamps and returns the model with the mocked lastInsertId 123,
	// wasRecentlyCreated true. The real database assigns id 1; the generated key being read back is
	// the observable the corpus's 123 stands for.
	created := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&created).FirstOrCreate(&created, map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}))

	assert.NotZero(t, created.ID, "Create must read the generated key back")
	assert.Equal(t, "foo", created.Attr)
	assert.Equal(t, "bar", created.Val)
	assert.Equal(t, 0, created.Count)
	builderCreateOrFirstAssertFrozen(t, now, created.CreatedAt, "created.created_at")
	builderCreateOrFirstAssertFrozen(t, now, created.UpdatedAt, "created.updated_at")

	stored := builderCreateOrFirstReload(t, query, created.ID)
	assert.Equal(t, created.ID, stored.ID)
	assert.Equal(t, "foo", stored.Attr)
	assert.Equal(t, "bar", stored.Val)
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query))
}

func TestBuilderCreateOrFirst_FirstOrCreateMethodRetrievesRecordCreatedJustNow(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// Laravel: the select misses, the insert throws a UniqueConstraintViolationException (a concurrent
	// writer won the race) and firstOrCreate re-selects the just-created row (id 123, attr foo, val
	// bar), wasRecentlyCreated false. The concurrent writer is the racing model's creating event
	// (see the file comment); oracle with the same listener: select (miss), insert (violation),
	// select (hit) => id 1, wasRecentlyCreated false, val bar, one stored row.
	armBuilderCreateOrFirstRace(t, query, "bar")

	found := builderCreateOrFirstRacingTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).FirstOrCreate(&found, map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}))

	assert.NotZero(t, found.ID, "the re-select must hand back the racing writer's row")
	assert.Equal(t, "foo", found.Attr)
	assert.Equal(t, "bar", found.Val)
	assert.Equal(t, 0, found.Count)
	builderCreateOrFirstAssertFrozen(t, now, found.CreatedAt, "found.created_at")
	builderCreateOrFirstAssertFrozen(t, now, found.UpdatedAt, "found.updated_at")

	stored := builderCreateOrFirstReload(t, query, found.ID)
	assert.Equal(t, "foo", stored.Attr)
	assert.Equal(t, "bar", stored.Val)
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query), "only the racing writer's row may exist")
}

func TestBuilderCreateOrFirst_UpdateOrCreateMethodUpdatesExistingRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// The corpus's mocked select returns id 123, attr foo, val bar, timestamps at the test now; the
	// mocked update sets val = baz and updated_at = the test now.
	existing := builderCreateOrFirstSeedRow(t, query, "foo", "bar")

	updated := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&updated).UpdateOrCreate(&updated, map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}))

	assert.Equal(t, existing.ID, updated.ID)
	assert.Equal(t, "foo", updated.Attr)
	assert.Equal(t, "baz", updated.Val)
	assert.Equal(t, 0, updated.Count)
	builderCreateOrFirstAssertFrozen(t, now, updated.CreatedAt, "updated.created_at")
	builderCreateOrFirstAssertFrozen(t, now, updated.UpdatedAt, "updated.updated_at")

	// Laravel's save() compiles 'update "table" set "val" = ?, "updated_at" = ? where "id" = ?'; the
	// port's Save now runs the same dirty-only update — the loaded model's unchanged columns stay
	// out of the statement — so the stored row ends up in the same state.
	stored := builderCreateOrFirstReload(t, query, existing.ID)
	assert.Equal(t, "foo", stored.Attr)
	assert.Equal(t, "baz", stored.Val)
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query), "the update path must not insert")
}

func TestBuilderCreateOrFirst_UpdateOrCreateMethodCreatesNewRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	assert.Equal(t, int64(0), builderCreateOrFirstCount(t, query), "the table starts empty")

	// Laravel: the select finds nothing, so updateOrCreate creates with attr and val (its
	// firstOrCreate path) and returns the model with the mocked lastInsertId 123, wasRecentlyCreated
	// true.
	created := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&created).UpdateOrCreate(&created, map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}))

	assert.NotZero(t, created.ID, "Create must read the generated key back")
	assert.Equal(t, "foo", created.Attr)
	assert.Equal(t, "bar", created.Val)
	assert.Equal(t, 0, created.Count)
	builderCreateOrFirstAssertFrozen(t, now, created.CreatedAt, "created.created_at")
	builderCreateOrFirstAssertFrozen(t, now, created.UpdatedAt, "created.updated_at")

	stored := builderCreateOrFirstReload(t, query, created.ID)
	assert.Equal(t, created.ID, stored.ID)
	assert.Equal(t, "foo", stored.Attr)
	assert.Equal(t, "bar", stored.Val)
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query))
}

func TestBuilderCreateOrFirst_UpdateOrCreateMethodUpdatesRecordCreatedJustNow(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// Laravel: the select misses, the insert (val baz) throws and is caught into a re-select of the
	// row created just now (val bar), then updateOrCreate fills val = baz and saves it — the mocked
	// update "table" set "val" = ?, "updated_at" = ? where "id" = ? — wasRecentlyCreated false. The
	// concurrent writer is the racing model's creating event; oracle with the same listener: select
	// (miss), insert (violation), select (hit), update ... set "val" = ? => val baz, one stored row.
	armBuilderCreateOrFirstRace(t, query, "bar")

	updated := builderCreateOrFirstRacingTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&updated).UpdateOrCreate(&updated, map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}))

	assert.NotZero(t, updated.ID, "the re-select must hand back the racing writer's row")
	assert.Equal(t, "foo", updated.Attr)
	assert.Equal(t, "baz", updated.Val)
	assert.Equal(t, 0, updated.Count)
	builderCreateOrFirstAssertFrozen(t, now, updated.CreatedAt, "updated.created_at")
	builderCreateOrFirstAssertFrozen(t, now, updated.UpdatedAt, "updated.updated_at")

	stored := builderCreateOrFirstReload(t, query, updated.ID)
	assert.Equal(t, "foo", stored.Attr)
	assert.Equal(t, "baz", stored.Val, "the racing writer's row must have been filled and saved")
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query), "only the racing writer's row may exist")
}

func TestBuilderCreateOrFirst_IncrementOrCreateMethodIncrementsExistingRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// Laravel: the mocked select returns id 123, attr foo, count 1, and the mocked raw('"count" + 1')
	// plus update pin the increment; oracle on a real sqlite database: select (hit), then
	// `update "table" set "count" = "count" + 1, "updated_at" = ? where "id" = ?` — count 2, one
	// stored row, wasRecentlyCreated false.
	existing := builderCreateOrFirstSeedIncrementRow(t, query, "", 1)

	incremented := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&incremented).IncrementOrCreate(&incremented, map[string]any{"attr": "foo"}, "count"))

	assert.Equal(t, existing.ID, incremented.ID)
	assert.Equal(t, "foo", incremented.Attr)
	assert.Equal(t, 2, incremented.Count, "the model must carry the stepped count, Laravel's in-memory $instance->count")
	builderCreateOrFirstAssertFrozen(t, now, incremented.CreatedAt, "incremented.created_at")
	builderCreateOrFirstAssertFrozen(t, now, incremented.UpdatedAt, "incremented.updated_at")

	stored := builderCreateOrFirstReload(t, query, existing.ID)
	assert.Equal(t, 2, stored.Count, "the raw expression must have stepped the stored row")
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query))
}

func TestBuilderCreateOrFirst_IncrementOrCreateMethodCreatesNewRecord(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	assert.Equal(t, int64(0), builderCreateOrFirstCount(t, query), "the table starts empty")

	// Laravel: incrementOrCreate(['attr' => 'foo']) finds nothing and creates the row with count
	// defaulted to 1 — oracle: insert into "table" ("attr", "count", "updated_at", "created_at")
	// values (?, ?, ?, ?) binding count 1, and no update after it — wasRecentlyCreated true. The
	// real database assigns id 1; the generated key being read back is the observable the corpus's
	// 123 stands for.
	created := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&created).IncrementOrCreate(&created, map[string]any{"attr": "foo"}))

	assert.NotZero(t, created.ID, "Create must read the generated key back")
	assert.Equal(t, "foo", created.Attr)
	assert.Equal(t, 1, created.Count, "the column default must ride the create")
	builderCreateOrFirstAssertFrozen(t, now, created.CreatedAt, "created.created_at")
	builderCreateOrFirstAssertFrozen(t, now, created.UpdatedAt, "created.updated_at")

	stored := builderCreateOrFirstReload(t, query, created.ID)
	assert.Equal(t, 1, stored.Count, "a record that was just created must not be stepped")
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query))
}

func TestBuilderCreateOrFirst_IncrementOrCreateMethodIncrementParametersArePassed(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// Laravel: incrementOrCreate(['attr' => 'foo'], step: 2, extra: ['val' => 'baz']) steps by the
	// given 2 and writes the extra column in the same update — oracle:
	// `update "table" set "count" = "count" + 2, "val" = ?, "updated_at" = ? where "id" = ?`,
	// count 3, val baz. The named arguments translate positionally: the default stays 1, the step
	// is 2.
	existing := builderCreateOrFirstSeedIncrementRow(t, query, "bar", 1)

	stepped := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&stepped).IncrementOrCreate(&stepped, map[string]any{"attr": "foo"}, 1, 2, map[string]any{"val": "baz"}))

	assert.Equal(t, existing.ID, stepped.ID)
	assert.Equal(t, "foo", stepped.Attr)
	assert.Equal(t, "baz", stepped.Val, "the extra values must be filled onto the model")
	assert.Equal(t, 3, stepped.Count)
	builderCreateOrFirstAssertFrozen(t, now, stepped.CreatedAt, "stepped.created_at")
	builderCreateOrFirstAssertFrozen(t, now, stepped.UpdatedAt, "stepped.updated_at")

	stored := builderCreateOrFirstReload(t, query, existing.ID)
	assert.Equal(t, 3, stored.Count, "the step must be the given 2, not the default 1")
	assert.Equal(t, "baz", stored.Val, "the extra column must be written by the same update")
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query))
}

func TestBuilderCreateOrFirst_IncrementOrCreateMethodRetrievesRecordCreatedJustNow(t *testing.T) {
	now := freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	// Laravel: the select misses, the insert (attr plus the count default) throws and is caught into
	// a re-select of the row created just now — count 1, wasRecentlyCreated false — which is then
	// stepped by the raw expression. The port arms the same race from the racing model's creating
	// event (see the file comment); oracle with the same listener: select (miss), insert
	// (violation), select (hit, count 1), `update "table" set "count" = "count" + 1,
	// "updated_at" = ? where "id" = ?` => count 2, one stored row.
	armBuilderCreateOrFirstRaceIncrement(t, query)

	found := builderCreateOrFirstRacingTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).IncrementOrCreate(&found, map[string]any{"attr": "foo"}))

	assert.NotZero(t, found.ID, "the re-select must hand back the racing writer's row")
	assert.Equal(t, "foo", found.Attr)
	assert.Equal(t, 2, found.Count, "the just-created row must be stepped, not treated as a create")
	builderCreateOrFirstAssertFrozen(t, now, found.CreatedAt, "found.created_at")
	builderCreateOrFirstAssertFrozen(t, now, found.UpdatedAt, "found.updated_at")

	stored := builderCreateOrFirstReload(t, query, found.ID)
	assert.Equal(t, 2, stored.Count)
	assert.Equal(t, int64(1), builderCreateOrFirstCount(t, query), "only the racing writer's row may exist")
}

// TestBuilderCreateOrFirst_IncrementOrCreateRebaselinesTheColumnSoALaterSaveIsExtrasOnly pins the
// syncOriginalAttribute call inside incrementModel (create_or_first.go): the incremented column
// re-baselines in the original bag, so a follow-up Save writes only the force-filled extra and the
// updated-at stamp, not the stepped column. The round-6 verify found this unpinned (a whole-package
// no-op mutant passed 56/56). Oracle v12.55.1, executed: /tmp/cof_inc_mem_probe.php - "a later save
// writing the extras alone".
func TestBuilderCreateOrFirst_IncrementOrCreateRebaselinesTheColumnSoALaterSaveIsExtrasOnly(t *testing.T) {
	freezeBuilderCreateOrFirstClock(t)
	query := newRelationQuery(t)
	builderCreateOrFirstSchema(t, query)

	existing := builderCreateOrFirstSeedIncrementRow(t, query, "", 1)

	stepped := builderCreateOrFirstTable{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&stepped).IncrementOrCreate(&stepped, map[string]any{"attr": "foo"}, 1, 2, map[string]any{"val": "baz"}))
	require.Equal(t, existing.ID, stepped.ID)
	require.Equal(t, 3, stepped.Count)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok)
	require.NoError(t, withContext.WithContext(ctx).Model(&stepped).Save(&stepped))

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "exactly one follow-up update")
	assert.Contains(t, logs[0].Query, `"val"`)
	assert.Contains(t, logs[0].Query, `"updated_at"`)
	assert.NotContains(t, logs[0].Query, `"count"`, "the re-baselined count column stays out of the SET list")

	stored := builderCreateOrFirstReload(t, query, existing.ID)
	assert.Equal(t, 3, stored.Count, "the follow-up save must not rewrite count")
	assert.Equal(t, "baz", stored.Val)
}
