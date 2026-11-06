package belongs_to_many_create_or_first

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkdb "github.com/gradii/framework/database/db"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyCreateOrFirstTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyCreateOrFirst_<method without the test prefix>.
//
// The corpus drives a Mockery-mocked connection (whose insert / select / update expectations pin
// the compiled SQL) and, in two tests, a Mockery partial relation (whose method-call expectations
// pin the delegation). None of its tests run against a real database. The port therefore runs
// every scenario against the harness sqlite database through the relation writers (Query.Relation
// - the Go BelongsToMany write surface), pinning each statement the corpus's expectations describe;
// the two scenarios whose assertion is the method call itself stay skipped as PHP-only, with fresh
// oracle output in their reasons. Every SQL string the corpus pins was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled:
//
//   - pivot-joined relation read (firstOrCreate's relation-scoped first(), createOrFirst's final
//     fallback): select "related_table".*, "pivot_table"."source_id" as "pivot_source_id",
//     "pivot_table"."related_id" as "pivot_related_id" from "related_table" inner join
//     "pivot_table" on "related_table"."id" = "pivot_table"."related_id" where
//     "pivot_table"."source_id" = ? and ("attr" = ?) limit 1
//   - plain related read (firstOrCreate's global first(), createOrFirst's post-violation read):
//     select * from "related_table" where ("attr" = ?) limit 1
//   - related insert (createOrFirst's try, firstOrCreate's create):
//     insert into "related_table" ("attr", "val", "updated_at", "created_at") values (?, ?, ?, ?)
//   - pivot insert (attach): insert into "pivot_table" ("related_id", "source_id") values (?, ?)
//   - related update (updateOrCreate): update "related_table" set "val" = ?, "updated_at" = ?
//     where "id" = ?
//
// Laravel's setUp freezes the clock at 2023-01-01 00:00:00 (Carbon::setTestNow). The Go side has
// the same facility - support/carbon.SetTestNow, which the ORM's insert stamping reads
// (database/gorm stamps autoCreateTime/autoUpdateTime fields with carbon.Now()) - and the tests
// below freeze it to reproduce the corpus timestamps.
//
// # The D1 repair and what the port pins now
//
// The m2m leaf methods (database/gorm/relations/many2many.go:17/:21/:25/:33 -> the relation
// writer's shells) used to reject Many2Many in three of the four write shells; the D1 item of the
// 1:1 alignment round ported BelongsToMany::findOrNew (BelongsToMany.php:621), firstOrNew (:637),
// firstOrCreate (:655) and createOrFirst (:681) semantics into the shells for Many2Many /
// MorphToMany, so every scenario below runs on the real writer. Each test's comment carries the
// statements and outcomes re-measured by /tmp/lalign_btm_trio_probe.php (illuminate/database
// v12.55.1, executed against a real sqlite database with the corpus's three tables plus the unique
// constraints its Mockery connection simulates):
//
//   - createOrFirst, new related: insert related + insert pivot, wasRecentlyCreated = true, no
//     pivot on the instance.
//   - createOrFirst, existing related: the related insert trips the unique constraint (the failed
//     statement is unlogged), the row is read back by attributes, attached, and returned - no
//     pivot on the instance.
//   - createOrFirst, attached meanwhile: the same read-back, the attach trips the pivot's unique
//     constraint, and the row is re-read through the pivot-joined relation on the write PDO -
//     pivot hydrated on the instance.
//   - firstOrCreate, attached: the pivot-joined relation read itself finds the row - pivot
//     hydrated, nothing written.
//   - firstOrCreate, unattached: the relation read misses, the plain related read finds the row
//     and firstOrCreate attaches it - no pivot on the instance.
//
// The findOrNew / firstOrNew halves of the repaired surface are not covered by this corpus file -
// they live in belongs_to_many_trio_test.go, next to it, with the same probe citations.
//
// # The follow-up: updateOrCreate is firstOrCreate + a conditional fill/save
//
// BelongsToMany::updateOrCreate (BelongsToMany.php:707) tap()s firstOrCreate and, when the
// instance was not recently created, fills the values and saves it (:709-712). The writer's m2m
// arm used to be a plain related-table search; it now runs the ported firstOrCreate
// (database/gorm/relation_writes.go firstOrCreateMany2Many), so a related row that exists but is
// not yet attached gets attached before the update. Re-measured by
// /tmp/followup-uo-m2m/probe.php (illuminate/database v12.55.1, executed against a real sqlite
// database over the same three tables):
//
//   - updateOrCreate, attached: the pivot-joined relation read hits (pivot hydrated), then
//     `update "related_table" set "val" = ?, "updated_at" = ? where "id" = ?`; no pivot write.
//   - updateOrCreate, unattached: the pivot-joined read misses, the plain related read hits, the
//     pivot row is inserted (`insert into "pivot_table" ("related_id", "source_id") values (?, ?)`),
//     then the same update; no pivot on the instance.
//   - updateOrCreate, miss: both reads miss, createOrFirst inserts the related row with
//     attrs+values and the pivot row; wasRecentlyCreated = true, so no update follows.
//
// UpdateOrCreate's update path still does not re-stamp updated_at itself: the struct save stamps
// an automatic timestamp only when its field is empty (database/gorm/update.go addAutoUpdateTime),
// so the loaded value is written back unchanged. The corpus's own loaded value is the frozen now,
// where write-back and re-stamp coincide, so the ported test below reproduces this scenario's
// pinned update.
//
// The ORM has no wasRecentlyCreated counterpart, so the corpus's assertions on it translate to
// "no insert happened" / "the row was created".

func TestBelongsToManyCreateOrFirst_CreateOrFirstMethodCreatesNewRelated(t *testing.T) {
	now := freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	// Laravel: related()->createOrFirst(['attr' => 'foo'], ['val' => 'bar']) inserts the related
	// row (probe: `insert into "related_table" ("attr", "val", "updated_at", "created_at") values
	// (?, ?, ?, ?)` bindings ["foo","bar","2023-01-01 00:00:00","2023-01-01 00:00:00"]) then the
	// pivot row (probe: `insert into "pivot_table" ("related_id", "source_id") values (?, ?)`) and
	// returns the instance - wasRecentlyCreated = true, toArray {attr: foo, val: bar,
	// created_at/updated_at: 2023-01-01T00:00:00.000000Z}, pivot=(no pivot loaded). The port's
	// observable for wasRecentlyCreated = true is that the related row was created.
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	// The corpus pins id 456 (its mocked connection returns that lastInsertId); the port asserts
	// the real generated key and that the pivot row names it.
	require.NotZero(t, result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	belongsToManyCreateOrFirstAssertFrozen(t, now, result.CreatedAt, "result.created_at")
	belongsToManyCreateOrFirstAssertFrozen(t, now, result.UpdatedAt, "result.updated_at")
	assert.Zero(t, result.Pivot, "the create path must not hydrate the pivot")

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1)
	assert.Equal(t, uint64(123), pivots[0].SourceID)
	assert.Equal(t, result.ID, pivots[0].RelatedID)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "createOrFirst must insert exactly one related row")
}

func TestBelongsToManyCreateOrFirst_CreateOrFirstMethodAssociatesExistingRelated(t *testing.T) {
	now := freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))

	// Laravel: the related insert trips the unique constraint (the failed statement is unlogged),
	// the row is read back by attributes (probe: `select * from "related_table" where
	// ("attr" = ?) limit 1` bindings ["foo"]), attached (probe: `insert into "pivot_table"
	// ("related_id", "source_id") values (?, ?)`), and the instance is returned -
	// wasRecentlyCreated = false, pivot=(no pivot loaded).
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, uint64(456), result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	belongsToManyCreateOrFirstAssertFrozen(t, now, result.CreatedAt, "result.created_at")
	belongsToManyCreateOrFirstAssertFrozen(t, now, result.UpdatedAt, "result.updated_at")
	assert.Zero(t, result.Pivot, "the read-back path must not hydrate the pivot")

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1)
	assert.Equal(t, uint64(123), pivots[0].SourceID)
	assert.Equal(t, uint64(456), pivots[0].RelatedID)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "the failed insert must not leave a second related row")
}

func TestBelongsToManyCreateOrFirst_FirstOrCreateMethodRetrievesExistingRelatedAlreadyAssociated(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").Attach([]any{uint64(456)}))

	// Laravel: firstOrCreate's relation-scoped read - (clone $this)->where($attributes)->first(),
	// the pivot-joined relation read (probe: `select "related_table".*, "pivot_table"."source_id"
	// as "pivot_source_id", "pivot_table"."related_id" as "pivot_related_id" from "related_table"
	// inner join "pivot_table" on "related_table"."id" = "pivot_table"."related_id" where
	// "pivot_table"."source_id" = ? and ("attr" = ?) limit 1` bindings [123,"foo"]) - finds the
	// attached row itself and returns it: wasRecentlyCreated = false, pivot {source_id: 123,
	// related_id: 456} hydrated on the instance (probe), nothing written.
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, uint64(456), result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	assert.Equal(t, uint64(123), result.Pivot.SourceID, "the relation read must hydrate the pivot")
	assert.Equal(t, uint64(456), result.Pivot.RelatedID)

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "the attached hit must not insert")
}

func TestBelongsToManyCreateOrFirst_CreateOrFirstMethodRetrievesExistingRelatedAssociatedJustNow(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").Attach([]any{uint64(456)}))

	// Laravel: the related insert trips the unique constraint (unlogged), the row is read back by
	// attributes (probe: `select * from "related_table" where ("attr" = ?) limit 1`), the pivot
	// attach trips the pivot's unique constraint (unlogged), and the second catch re-reads the row
	// through the pivot-joined relation on the write PDO (probe: the pivot-joined select above
	// with bindings [123,"foo"]) - the instance carries the hydrated pivot {source_id: 123,
	// related_id: 456} and wasRecentlyCreated = false. Re-measured for round 6 by
	// /tmp/attach_dedup_probe.php D1 (illuminate/database v12.55.1, real sqlite): after the plain
	// read-back the code attempts the pivot INSERT (no pre-read — Laravel's attach has none), it
	// trips the pivot's unique index, and the write-PDO pivot-joined read returns the attached
	// row. The Go-side instrument for the attempt is the query log: our logger records the failed
	// statement, so an `insert into "pivot_table"` entry appears between the two selects
	// (Laravel's failed statements are unlogged).
	ctx := frameworkdb.EnableQueryLog(context.Background())
	wc, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, wc.WithContext(ctx).Relation(source, "Related").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, uint64(456), result.ID)
	assert.Equal(t, uint64(123), result.Pivot.SourceID, "the second catch must re-read through the pivot join")
	assert.Equal(t, uint64(456), result.Pivot.RelatedID)

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "no related row may be inserted on the violation path")

	insertAt := -1
	for i, l := range frameworkdb.GetQueryLog(ctx) {
		if strings.HasPrefix(l.Query, `insert into "pivot_table"`) {
			insertAt = i
		}
	}
	assert.NotEqual(t, -1, insertAt, "the second catch attempts the pivot insert — no existence pre-read (round 6, oracle D1)")
}

func TestBelongsToManyCreateOrFirst_FirstOrCreateMethodRetrievesExistingRelatedAndAssociatesIt(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))

	// Laravel: the relation-scoped read misses (probe: the pivot-joined select with bindings
	// [123,"foo"] returns no row), the global related read finds the row (probe: `select * from
	// "related_table" where ("attr" = ?) limit 1` bindings ["foo"]), firstOrCreate attaches it
	// (probe: `insert into "pivot_table" ("related_id", "source_id") values (?, ?)`) and returns
	// it without pivot values and with wasRecentlyCreated = false (probe: pivot=(no pivot loaded)).
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").FirstOrCreate(
		map[string]any{"attr": "foo"}, map[string]any{"val": "bar"}, &result))

	assert.Equal(t, uint64(456), result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val)
	assert.Zero(t, result.Pivot, "the attach path must not hydrate the pivot")

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1)
	assert.Equal(t, uint64(123), pivots[0].SourceID)
	assert.Equal(t, uint64(456), pivots[0].RelatedID)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "the attach must not insert a related row")
}

func TestBelongsToManyCreateOrFirst_FirstOrCreateMethodFallsBackToCreateOrFirst(t *testing.T) {
	// Laravel: an anonymous subclass swaps the relation for a Mockery partial mock whose
	// createOrFirst is stubbed to return a canned instance, then asserts firstOrCreate calls it
	// with (['attr' => 'foo'], ['val' => 'bar'], [], true).
	t.Skip("PHP-only assertion (no Go equivalent): the assertion is the method call itself - a Mockery partial's createOrFirst expectation over a canned return value, not a database outcome; Go has no partial-mock relation objects. The fallback the corpus pins now exists and is exercised for real: the writer's FirstOrCreate funnels a double miss into CreateOrFirst (database/gorm/relation_writes.go, the m2m branch), and TestBelongsToManyCreateOrFirst_CreateOrFirstMethodCreatesNewRelated pins the same insert + attach statements against sqlite (oracle v12.55.1, executed: `insert into \"related_table\" (\"attr\", \"val\", \"updated_at\", \"created_at\") values (?, ?, ?, ?)`; `insert into \"pivot_table\" (\"related_id\", \"source_id\") values (?, ?)`)")
}

func TestBelongsToManyCreateOrFirst_UpdateOrCreateMethodCreatesNewRelated(t *testing.T) {
	// Laravel: the same anonymous subclass stubs the relation's firstOrCreate to return a canned
	// instance with wasRecentlyCreated = true, then asserts updateOrCreate calls it with
	// (['attr' => 'foo'], ['val' => 'baz'], [], true) and returns the stub unchanged (val stays
	// 'bar').
	t.Skip("PHP-only assertion (no Go equivalent): the assertion is the firstOrCreate method-call expectation on a Mockery partial plus the stub's canned return value - val stays 'bar' although the values carry 'baz', because the stub's wasRecentlyCreated = true skips the fill/save - not a database outcome; Go has no partial-mock relation objects. The branch itself is real now: the writer's UpdateOrCreate m2m arm is Laravel's firstOrCreate + conditional fill/save (database/gorm/relation_writes.go firstOrCreateMany2Many), and TestBelongsToManyCreateOrFirst_UpdateOrCreateMethodCreatesNewRelatedAndAttaches pins the fresh-create outcome against sqlite (oracle v12.55.1, executed, /tmp/followup-uo-m2m/probe.php updateOrCreate.miss: related insert + pivot insert, wasRecentlyCreated = true, no update)")
}

func TestBelongsToManyCreateOrFirst_UpdateOrCreateMethodUpdatesExistingRelated(t *testing.T) {
	now := freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	// The corpus stubs the relation's firstOrCreate (a Mockery partial) to return the existing
	// instance - id 456, attr foo, val bar, timestamps 2023-01-01 00:00:00, wasRecentlyCreated
	// false - then expects the update the writer compiles (oracle: update "related_table" set
	// "val" = ?, "updated_at" = ? where "id" = ? with bindings ['baz', '2023-01-01 00:00:00',
	// 456]) and asserts the result's toArray {id: 456, attr: 'foo', val: 'baz',
	// created_at/updated_at: 2023-01-01T00:00:00.000000Z}. The port seeds that row instead of
	// stubbing the read, and seeds it UNATTACHED, so the real firstOrCreate runs its second level
	// (probe updateOrCreate.unattached: the pivot-joined select with bindings [123,"foo"] misses,
	// `select * from "related_table" where ("attr" = ?) limit 1` hits, `insert into "pivot_table"
	// ("related_id", "source_id") values (?, ?)` attaches it), then wasRecentlyCreated = false
	// fills val = baz and saves (the pinned update). The loaded updated_at already is the frozen
	// now, so our Save writing back the loaded value produces the value the corpus pins.
	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))

	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var updated belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &updated))

	// toArray, field by field; wasRecentlyCreated = false has no Go counterpart, so its observable
	// is that the path never inserts.
	assert.Equal(t, uint64(456), updated.ID)
	assert.Equal(t, "foo", updated.Attr)
	assert.Equal(t, "baz", updated.Val)
	belongsToManyCreateOrFirstAssertFrozen(t, now, updated.CreatedAt, "updated.created_at")
	belongsToManyCreateOrFirstAssertFrozen(t, now, updated.UpdatedAt, "updated.updated_at")

	stored := belongsToManyCreateOrFirstRelated{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", 456).First(&stored))
	assert.Equal(t, "foo", stored.Attr)
	assert.Equal(t, "baz", stored.Val)
	belongsToManyCreateOrFirstAssertFrozen(t, now, stored.CreatedAt, "stored.created_at")
	belongsToManyCreateOrFirstAssertFrozen(t, now, stored.UpdatedAt, "stored.updated_at")
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "the update path must not insert")

	// firstOrCreate's second level attached the found row (probe: the pivot insert precedes the
	// update); the instance came from the plain related read, so no pivot is hydrated on it.
	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1, "updateOrCreate must attach a found-but-unattached related row")
	assert.Equal(t, uint64(123), pivots[0].SourceID)
	assert.Equal(t, uint64(456), pivots[0].RelatedID)
	assert.Zero(t, updated.Pivot, "the attach path must not hydrate the pivot")
}

func TestBelongsToManyCreateOrFirst_UpdateOrCreateMethodUpdatesAnAttachedRelated(t *testing.T) {
	now := freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").Attach([]any{uint64(456)}))

	// Laravel: updateOrCreate = firstOrCreate + conditional fill/save (BelongsToMany.php:707). The
	// pivot-joined relation read finds the attached row itself (probe updateOrCreate.attached:
	// `select "related_table".*, "pivot_table"."source_id" as "pivot_source_id",
	// "pivot_table"."related_id" as "pivot_related_id" from "related_table" inner join
	// "pivot_table" on "related_table"."id" = "pivot_table"."related_id" where
	// "pivot_table"."source_id" = ? and ("attr" = ?) limit 1` bindings [123,"foo"]), hydrating the
	// pivot; wasRecentlyCreated is false, so the values are filled and saved (probe: `update
	// "related_table" set "val" = ?, "updated_at" = ? where "id" = ?` bindings ["baz",
	// "2023-01-01 00:00:00", 456]). No pivot row is written and the instance keeps its pivot
	// {source_id: 123, related_id: 456}.
	var updated belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &updated))

	assert.Equal(t, uint64(456), updated.ID)
	assert.Equal(t, "foo", updated.Attr)
	assert.Equal(t, "baz", updated.Val)
	assert.Equal(t, uint64(123), updated.Pivot.SourceID, "an attached hit comes through the pivot-joined read, pivot hydrated")
	assert.Equal(t, uint64(456), updated.Pivot.RelatedID)
	belongsToManyCreateOrFirstAssertFrozen(t, now, updated.CreatedAt, "updated.created_at")
	belongsToManyCreateOrFirstAssertFrozen(t, now, updated.UpdatedAt, "updated.updated_at")

	stored := belongsToManyCreateOrFirstRelated{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", 456).First(&stored))
	assert.Equal(t, "baz", stored.Val)

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1, "an attached hit must not write a second pivot row")
	assert.Equal(t, uint64(123), pivots[0].SourceID)
	assert.Equal(t, uint64(456), pivots[0].RelatedID)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "the update path must not insert")
}

func TestBelongsToManyCreateOrFirst_UpdateOrCreateMethodCreatesNewRelatedAndAttaches(t *testing.T) {
	now := freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	// Laravel: both firstOrCreate reads miss (probe updateOrCreate.miss: the pivot-joined select,
	// then `select * from "related_table" where ("attr" = ?) limit 1`), createOrFirst inserts the
	// related row with attrs+values (probe: `insert into "related_table" ("attr", "val",
	// "updated_at", "created_at") values (?, ?, ?, ?)` bindings ["new","bar","2023-01-01 00:00:00",
	// "2023-01-01 00:00:00"]) and the pivot row (probe: `insert into "pivot_table" ("related_id",
	// "source_id") values (?, ?)`); wasRecentlyCreated = true skips the fill/save - no update
	// follows - and pivot=(no pivot loaded). The corpus's own testUpdateOrCreateMethodCreatesNewRelated
	// pins this branch through a Mockery partial (kept skipped above); this is its real-database
	// counterpart.
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var created belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &created))

	require.NotZero(t, created.ID)
	assert.Equal(t, "foo", created.Attr)
	assert.Equal(t, "baz", created.Val)
	belongsToManyCreateOrFirstAssertFrozen(t, now, created.CreatedAt, "created.created_at")
	belongsToManyCreateOrFirstAssertFrozen(t, now, created.UpdatedAt, "created.updated_at")
	assert.Zero(t, created.Pivot, "the create path must not hydrate the pivot")

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1, "the fresh create must be attached")
	assert.Equal(t, uint64(123), pivots[0].SourceID)
	assert.Equal(t, created.ID, pivots[0].RelatedID)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "updateOrCreate must insert exactly one related row")
}

// --- models and helpers for the ported test -------------------------------------------------

// belongsToManyCreateOrFirstRelated ports BelongsToManyCreateOrFirstTestRelatedModel: table
// "related_table", $guarded = [], and the default timestamps (the autoCreateTime/autoUpdateTime
// tags play Laravel's $timestamps role). The Pivot field is the Go form of the default Eloquent
// Pivot the corpus's PHP models carry implicitly.
type belongsToManyCreateOrFirstRelated struct {
	ID        uint64                          `gorm:"column:id;primaryKey;autoIncrement"`
	Attr      string                          `gorm:"column:attr"`
	Val       string                          `gorm:"column:val"`
	CreatedAt *carbon.DateTime                `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime                `gorm:"autoUpdateTime;column:updated_at"`
	Pivot     belongsToManyCreateOrFirstPivot `gorm:"-"`
}

func (belongsToManyCreateOrFirstRelated) TableName() string { return "related_table" }

// belongsToManyCreateOrFirstPivot is the related model's pivot destination. Declaring it turns on
// the many-to-many read's pivot hydration (preparePivotHydration), which the probe pins as a
// `pivot` entry {source_id, related_id} on the relation reads.
type belongsToManyCreateOrFirstPivot struct {
	SourceID  uint64 `gorm:"column:source_id"`
	RelatedID uint64 `gorm:"column:related_id"`
}

func (belongsToManyCreateOrFirstPivot) TableName() string { return "pivot_table" }

// belongsToManyCreateOrFirstPivotRow reads pivot_table's rows directly - the port's instrument for
// the "nothing was written" / "the pivot row exists" assertions the corpus's insert expectations
// describe.
type belongsToManyCreateOrFirstPivotRow struct {
	SourceID  uint64 `gorm:"column:source_id"`
	RelatedID uint64 `gorm:"column:related_id"`
}

func (belongsToManyCreateOrFirstPivotRow) TableName() string { return "pivot_table" }

// belongsToManyCreateOrFirstSource ports BelongsToManyCreateOrFirstTestSourceModel: table
// "source_table" and related() = belongsToMany(Related, 'pivot_table', 'source_id', 'related_id').
type belongsToManyCreateOrFirstSource struct {
	ID      uint64                               `gorm:"column:id;primaryKey;autoIncrement"`
	Related []*belongsToManyCreateOrFirstRelated `gorm:"-"`
}

func (belongsToManyCreateOrFirstSource) TableName() string { return "source_table" }

func (belongsToManyCreateOrFirstSource) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Related": contractsorm.Many2Many{
			Related:         &belongsToManyCreateOrFirstRelated{},
			Table:           "pivot_table",
			ForeignPivotKey: "source_id",
			RelatedPivotKey: "related_id",
		},
	}
}

// belongsToManyCreateOrFirstSchema creates the tables the corpus's three models imply: source_table
// (id), related_table (id, attr, val, timestamps) and pivot_table (source_id, related_id). The
// corpus's Mockery connection throws UniqueConstraintViolationException for the related insert
// (createOrFirst's first catch) and the pivot attach (the second catch); against a real database
// those paths need the constraints the simulated exceptions stand in for.
func belongsToManyCreateOrFirstSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query, belongsToManyCreateOrFirstSchemaStatements()...)
}

// belongsToManyCreateOrFirstSchemaStatements returns the corpus DDL, one statement per string, so a
// single-connection handle and the read/write-split fixture's reader file can be built from one
// list. belongsToManyCreateOrFirstSchema runs it against the ORM handle (which routes DDL to the
// writer); the split fixture replays the same list against its raw reader pool.
func belongsToManyCreateOrFirstSchemaStatements() []string {
	return []string{
		`create table source_table (id integer primary key autoincrement)`,
		`create table related_table (id integer primary key autoincrement, attr text, val text, created_at datetime null, updated_at datetime null)`,
		`create unique index related_table_attr_unique on related_table (attr)`,
		`create table pivot_table (source_id integer not null, related_id integer not null)`,
		`create unique index pivot_table_source_related_unique on pivot_table (source_id, related_id)`,
	}
}

// freezeBelongsToManyCreateOrFirstClock pins the clock the way the corpus's setUp does
// (Carbon::setTestNow('2023-01-01 00:00:00')).
func freezeBelongsToManyCreateOrFirstClock(t *testing.T) *carbon.Carbon {
	t.Helper()

	now := carbon.Parse("2023-01-01 00:00:00")
	carbon.SetTestNow(now)
	t.Cleanup(carbon.ClearTestNow)

	return now
}

// belongsToManyCreateOrFirstCount counts related_table's rows, the port's observable for the
// corpus's wasRecentlyCreated = false on the update path.
func belongsToManyCreateOrFirstCount(t *testing.T, query contractsorm.Query) int64 {
	t.Helper()

	count, err := cloneRelationQuery(t, query).Model(&belongsToManyCreateOrFirstRelated{}).Count()
	require.NoError(t, err)

	return count
}

// belongsToManyCreateOrFirstPivotRows returns pivot_table's rows - the port's read of the corpus's
// `insert into "pivot_table" ...` expectations.
func belongsToManyCreateOrFirstPivotRows(t *testing.T, query contractsorm.Query) []belongsToManyCreateOrFirstPivotRow {
	t.Helper()

	var rows []belongsToManyCreateOrFirstPivotRow
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyCreateOrFirstPivotRow{}).Get(&rows))

	return rows
}

// belongsToManyCreateOrFirstAssertFrozen pins a model timestamp to the frozen instant, the port of
// the corpus's '2023-01-01T00:00:00.000000Z' array entries.
func belongsToManyCreateOrFirstAssertFrozen(t *testing.T, now *carbon.Carbon, value *carbon.DateTime, label string) {
	t.Helper()

	require.NotNil(t, value, label+": the model must carry a timestamp")
	assert.True(t, now.StdTime().Equal(value.StdTime()), label+": want the frozen 2023-01-01 00:00:00, got %s", value.StdTime())
}

// --- verifier follow-ups ---------------------------------------------------------------------
//
// Three checks the adversarial verification added on top of the ported corpus:
//
//  1. the statement order and the "no trailing update on a fresh create" clause of
//     BelongsToMany::updateOrCreate are pinned through the query log, not just the final state — the
//     update is what wasRecentlyCreated = true suppresses (BelongsToMany.php:709-712).
//  2. the unique-constraint read-back branch (createOrFirst's first catch) is reached by racing a
//     Creating observer; Laravel's !wasRecentlyCreated path then fills and saves the read-back row
//     (oracle v12.55.1, executed /tmp/verify-uo-m2m/readback.php: related insert, plain read-back,
//     pivot insert, update).
//  3. updateOrCreate saves with ['touch' => false], so a related model's own $touches owners are not
//     stamped (oracle v12.55.1, executed /tmp/verify-uo-m2m/touch.php T1/T3: owner.updated_at is left
//     alone, where a plain $related->save() stamps it — T2).

func TestBelongsToManyCreateOrFirst_UpdateOrCreateStatementOrder(t *testing.T) {
	freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	ctxFor := func() (context.Context, contractsorm.Query) {
		ctx := frameworkdb.EnableQueryLog(context.Background())
		wc, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
		require.True(t, ok, "the ORM query must support WithContext")
		return ctx, wc.WithContext(ctx)
	}
	updates := func(ctx context.Context) int {
		n := 0
		for _, l := range frameworkdb.GetQueryLog(ctx) {
			if strings.HasPrefix(l.Query, `update "related_table"`) {
				n++
			}
		}
		return n
	}

	// Attached hit: the pivot-joined read, then exactly one update — no pivot write.
	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").Attach([]any{uint64(456)}))
	ctx, wq := ctxFor()
	var attached belongsToManyCreateOrFirstRelated
	require.NoError(t, wq.Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &attached))
	logs := frameworkdb.GetQueryLog(ctx)
	require.NotEmpty(t, logs, "the query log must capture the relation write")
	assert.Contains(t, logs[0].Query, `inner join "pivot_table"`, "the attached hit starts with the pivot-joined relation read")
	assert.Equal(t, 1, updates(ctx), "an attached hit issues exactly one update")

	// Unattached hit: the attach (pivot insert) precedes the single update. The attached phase
	// already wrote val=baz onto the shared row, so the row is reset to the probe's seed state
	// (val=bar): the update exists because the fill changes the row.
	_, err := cloneRelationQuery(t, query).Exec(`delete from pivot_table`)
	require.NoError(t, err)
	_, err = cloneRelationQuery(t, query).Exec(`update "related_table" set "val" = 'bar' where "id" = 456`)
	require.NoError(t, err)
	ctx2, wq2 := ctxFor()
	var unattached belongsToManyCreateOrFirstRelated
	require.NoError(t, wq2.Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &unattached))
	insertAt, updateAt := -1, -1
	for i, l := range frameworkdb.GetQueryLog(ctx2) {
		if strings.HasPrefix(l.Query, `insert into "pivot_table"`) {
			insertAt = i
		}
		if strings.HasPrefix(l.Query, `update "related_table"`) {
			updateAt = i
		}
	}
	require.NotEqual(t, -1, insertAt, "the unattached hit must attach")
	require.NotEqual(t, -1, updateAt, "the unattached hit must update")
	assert.Less(t, insertAt, updateAt, "the attach precedes the update (probe updateOrCreate.unattached)")
	assert.Equal(t, 1, updates(ctx2), "the unattached hit issues exactly one update")

	// An unattached hit whose values change nothing: the attach still runs, and Laravel's
	// fill/save stops at Model::save's isDirty gate — no trailing update (oracle, executed,
	// /tmp/save11/btm_updateorcreate_probe.php C2: join select miss, plain select hit, pivot
	// insert, and no update).
	_, err = cloneRelationQuery(t, query).Exec(`delete from pivot_table`)
	require.NoError(t, err)
	ctx2b, wq2b := ctxFor()
	var unchanged belongsToManyCreateOrFirstRelated
	require.NoError(t, wq2b.Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &unchanged))
	// Since 2026-11-06 the port keeps no record of loaded values (user decision: the implicit
	// original tracking was removed), so Save writes every non-zero field; it cannot tell a clean model from a changed one.
	assert.Equal(t, 1, updates(ctx2b), "without loaded-value tracking the fill/save always updates")
	attachInserts := 0
	for _, l := range frameworkdb.GetQueryLog(ctx2b) {
		if strings.HasPrefix(l.Query, `insert into "pivot_table"`) {
			attachInserts++
		}
	}
	assert.Equal(t, 1, attachInserts, "the attach still runs")

	// Miss: no update follows the create — wasRecentlyCreated = true skips the fill/save.
	_, err = cloneRelationQuery(t, query).Exec(`delete from pivot_table`)
	require.NoError(t, err)
	_, err = cloneRelationQuery(t, query).Exec(`delete from related_table`)
	require.NoError(t, err)
	ctx3, wq3 := ctxFor()
	var created belongsToManyCreateOrFirstRelated
	require.NoError(t, wq3.Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "new"}, map[string]any{"val": "baz"}, &created))
	assert.Equal(t, 0, updates(ctx3), "a fresh create writes no trailing update (wasRecentlyCreated = true)")
	attachedPivot := false
	for _, l := range frameworkdb.GetQueryLog(ctx3) {
		if strings.HasPrefix(l.Query, `insert into "pivot_table"`) {
			attachedPivot = true
		}
	}
	assert.True(t, attachedPivot, "the fresh create is still attached")
}

// uoRaceObserver races a related-row insert in front of the model's own insert, so createOrFirst's
// try hits the related-table unique constraint and the read-back branch runs.
type uoRaceObserver struct {
	fired  int
	insert string
}

func (o *uoRaceObserver) Created(contractsorm.Event) error      { return nil }
func (o *uoRaceObserver) Updated(contractsorm.Event) error      { return nil }
func (o *uoRaceObserver) Deleted(contractsorm.Event) error      { return nil }
func (o *uoRaceObserver) ForceDeleted(contractsorm.Event) error { return nil }

func (o *uoRaceObserver) Creating(e contractsorm.Event) error {
	o.fired++
	if o.fired == 1 {
		_, err := e.Query().Exec(o.insert)
		return err
	}
	return nil
}

func TestBelongsToManyCreateOrFirst_UpdateOrCreateFillsAUniqueConstraintReadBack(t *testing.T) {
	now := freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	observable, ok := query.(contractsorm.QueryWithObserver)
	require.True(t, ok, "the ORM query must support observers")
	race := &uoRaceObserver{
		insert: `insert into related_table (id, attr, val, created_at, updated_at) values (999,'race','old','2023-01-01 00:00:00','2023-01-01 00:00:00')`,
	}
	observable.Observe(&belongsToManyCreateOrFirstRelated{}, race)

	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var got belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "race"}, map[string]any{"val": "won"}, &got))

	require.Equal(t, 1, race.fired, "the related insert must have raced the unique constraint")
	require.Equal(t, uint64(999), got.ID, "the read-back row is the one the violation named")
	assert.Equal(t, "won", got.Val, "wasRecentlyCreated = false, so the read-back is filled and saved (BelongsToMany.php:709-712)")
	assert.Zero(t, got.Pivot, "the read-back comes from the plain related read: no pivot")
	belongsToManyCreateOrFirstAssertFrozen(t, now, got.UpdatedAt, "got.updated_at")

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1, "the read-back is attached")
	assert.Equal(t, uint64(999), pivots[0].RelatedID)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "no second related row")
}

// uoTouchOwner / uoTouchRelated / uoTouchSource carry the $touches declaration the oracle touch
// probe pins: the related model declares its owner as touched, so a plain save would stamp the
// owner's updated_at, while updateOrCreate must not.
type uoTouchOwner struct {
	ID        uint64           `gorm:"column:id;primaryKey"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (uoTouchOwner) TableName() string { return "uo_touch_owners" }

type uoTouchRelated struct {
	ID        uint64           `gorm:"column:id;primaryKey;autoIncrement"`
	Attr      string           `gorm:"column:attr"`
	Val       string           `gorm:"column:val"`
	OwnerID   uint64           `gorm:"column:owner_id"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (uoTouchRelated) TableName() string { return "uo_touch_related" }

func (uoTouchRelated) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &uoTouchOwner{}, ForeignKey: "owner_id", Touches: true},
	}
}

type uoTouchSource struct {
	ID      uint64            `gorm:"column:id;primaryKey;autoIncrement"`
	Related []*uoTouchRelated `gorm:"-"`
}

func (uoTouchSource) TableName() string { return "uo_touch_source" }

func (uoTouchSource) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Related": contractsorm.Many2Many{
			Related:         &uoTouchRelated{},
			Table:           "uo_touch_pivot",
			ForeignPivotKey: "source_id",
			RelatedPivotKey: "related_id",
		},
	}
}

func TestBelongsToManyCreateOrFirst_UpdateOrCreateDoesNotTouchTheRelatedModelsOwners(t *testing.T) {
	freezeBelongsToManyCreateOrFirstClock(t)
	query := newRelationQuery(t)
	runRelationSchema(t, query,
		`create table uo_touch_owners (id integer primary key, updated_at datetime null)`,
		`create table uo_touch_source (id integer primary key autoincrement)`,
		`create table uo_touch_related (id integer primary key autoincrement, attr text, val text, owner_id integer null, created_at datetime null, updated_at datetime null)`,
		`create unique index uo_touch_related_attr_uq on uo_touch_related (attr)`,
		`create table uo_touch_pivot (source_id integer not null, related_id integer not null)`,
		`create unique index uo_touch_pivot_uq on uo_touch_pivot (source_id, related_id)`,
	)
	_, err := cloneRelationQuery(t, query).Exec(`insert into uo_touch_owners (id, updated_at) values (1, '2022-01-01 00:00:00')`)
	require.NoError(t, err)
	_, err = cloneRelationQuery(t, query).Exec(`insert into uo_touch_related (id, attr, val, owner_id) values (456, 'foo', 'bar', 1)`)
	require.NoError(t, err)
	source := &uoTouchSource{ID: 123}
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").Attach([]any{uint64(456)}))

	// Laravel BelongsToMany::updateOrCreate (BelongsToMany.php:711) saves with ['touch' => false];
	// the related model declares $touches = ['owner'], yet the owner's updated_at is not stamped
	// (oracle v12.55.1, executed /tmp/verify-uo-m2m/touch.php T1/T3).
	var updated uoTouchRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").UpdateOrCreate(map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &updated))
	assert.Equal(t, "baz", updated.Val)

	var owner uoTouchOwner
	require.NoError(t, cloneRelationQuery(t, query).Model(&uoTouchOwner{}).Where("id", 1).First(&owner))
	require.NotNil(t, owner.UpdatedAt, "the owner's updated_at must still be the seeded value")
	assert.True(t, owner.UpdatedAt.StdTime().Equal(carbon.Parse("2022-01-01 00:00:00").StdTime()),
		"updateOrCreate saves with touch=false: the related model's own $touches owner is not stamped, got %s", owner.UpdatedAt.StdTime())
}
