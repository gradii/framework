package has_many_through_create_or_first

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file pins the relation-object surface of the through create-or-first family — the
// relation-object round's through item — on the models of the sibling corpus file (parent ->
// pivot -> child, hasManyThrough(Child, Pivot, 'parent_id', 'pivot_id')). Each of the object's
// FirstOrNew / FirstOrCreate / UpdateOrCreate / CreateOrFirst forwards is pinned with one miss
// and one hit shape, with the statements measured against the oracle (illuminate/database
// v12.55.1, executed against sqlite) on the same schema and pinned through the query log with the
// bindings inlined (the frozen test now, 2023-01-01 00:00:00, renders exactly as the oracle's
// executed statements do).
//
// The oracle's shapes:
//
//	firstOrNew    miss -> the joined lookup select, nothing else; the returned instance carries
//	                     attrs+values, unsaved; hit -> the lookup select, the row untouched
//	firstOrCreate miss -> the lookup select, then insert into "child" ("attr", "val",
//	                     "updated_at", "created_at") with the merged attributes; hit -> the
//	                     lookup select only
//	createOrFirst miss -> the insert with no lookup first; lost race -> the insert, then the
//	                     catch's re-read of the attrs (bindings 123, "foo")
//	updateOrCreate miss -> the lookup select, then the insert; hit -> the lookup select, then
//	                     update "child" set "val" = ?, "updated_at" = ? where "id" = ?
//
// The joined lookup select is
//
//	select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child"
//	inner join "pivot" on "pivot"."id" = "child"."pivot_id"
//	where "pivot"."parent_id" = ? [and the attribute bag] limit 1
//
// Measured differences between the Go port's pinned emissions and the oracle's (pre-existing
// shell / compile behavior — the object layer only forwards):
//
//   - the attribute bag renders as plain and-chained wheres — `and "attr" = ?` — where Eloquent
//     nests it (`and ("attr" = ?)`); the same truth table on an and-only list;
//   - every first-family compile (the lookup and the promoted first()) carries the framework's
//     deterministic `order by "<related>"."<key>"` beside `limit 1`, where Laravel's has none;
//   - the insert emits every non-key column in sorted order (the hit update now matches Eloquent's
//     dirty-only shape): the through create stamps pivot_id 0 where Laravel's attribute-bag insert
//     omits the column;
//   - the query log records the lost race's failed insert attempt; Laravel's log skips a
//     statement that threw. The entries stay in execution order.

// hasManyThroughObject narrows a handle's Related to the has-many-through object surface.
func hasManyThroughObject(t *testing.T, handle contractsorm.Query, parent any) contractsorm.HasManyThroughRelation {
	t.Helper()

	rel, ok := handle.Related(parent, "Children").(contractsorm.HasManyThroughRelation)
	require.True(t, ok, "Related must return the has-many-through object")

	return rel
}

// --- firstOrNew --------------------------------------------------------------

// Oracle: $parent->children()->firstOrNew(['attr'=>'new'], ['val'=>'nv']) on an empty child
// table runs the lookup select `... where "pivot"."parent_id" = ? and ("attr" = ?) limit 1`
// (bindings 123, "new") and returns an unsaved instance carrying attrs+values — no insert.

func TestHasManyThroughObject_FirstOrNewMissFillsTheInstanceWithoutPersisting(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)
	hasManyThroughCreateOrFirstFreezeNow(t)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).FirstOrNew(
		&result, map[string]any{"attr": "new"}, map[string]any{"val": "nv"}))

	assert.Zero(t, result.ID, "firstOrNew never persists")
	assert.Equal(t, "new", result.Attr)
	assert.Equal(t, "nv", result.Val)
	assert.Nil(t, result.CreatedAt)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "the miss is the lookup select alone")
	assert.Equal(t, `select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = 123 and "attr" = 'new' order by "child"."id" limit 1`, logs[0].Query)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(0), rows)
}

// Oracle: firstOrNew(['attr'=>'foo'], ['val'=>'ignored']) with child 789 (foo/bar) seeded returns
// the row untouched — the values are ignored on a hit — on the lookup select alone.

func TestHasManyThroughObject_FirstOrNewHitReturnsTheRow(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	seeded := hasManyThroughCreateOrFirstSeedChild(t, query, 789, 456, "foo", "bar", now)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).FirstOrNew(
		&result, map[string]any{"attr": "foo"}, map[string]any{"val": "ignored"}))

	assert.Equal(t, seeded.ID, result.ID)
	assert.Equal(t, uint(456), result.PivotID)
	assert.Equal(t, "bar", result.Val, "a hit returns the stored row untouched")

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1)
	assert.Equal(t, `select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = 123 and "attr" = 'foo' order by "child"."id" limit 1`, logs[0].Query)
}

// --- firstOrCreate -----------------------------------------------------------

// Oracle: $parent->children()->firstOrCreate(['attr'=>'fresh'], ['val'=>'fv']) on an empty child
// table runs the lookup select, then `insert into "child" ("attr", "val", "updated_at",
// "created_at") values (?, ?, ?, ?)` with the frozen now — the through create carries no parent
// key on the related row.

func TestHasManyThroughObject_FirstOrCreateMissCreatesTheRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).FirstOrCreate(
		&result, map[string]any{"attr": "fresh"}, map[string]any{"val": "fv"}))

	require.NotZero(t, result.ID)
	assert.Equal(t, "fresh", result.Attr)
	assert.Equal(t, "fv", result.Val)
	require.NotNil(t, result.CreatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "the miss is the lookup select plus the insert")
	assert.Equal(t, `select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = 123 and "attr" = 'fresh' order by "child"."id" limit 1`, logs[0].Query)
	assert.Equal(t, `insert into "child" ("attr", "created_at", "pivot_id", "updated_at", "val") values ('fresh', '2023-01-01 00:00:00', 0, '2023-01-01 00:00:00', 'fv')`, logs[1].Query)

	rows, err := cloneRelationQuery(t, query).Model(&hasManyThroughCreateOrFirstChild{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, int64(1), rows)
}

// Oracle: firstOrCreate(['attr'=>'foo'], ['val'=>'ignored']) with child 789 seeded returns the row
// untouched on the lookup select alone.

func TestHasManyThroughObject_FirstOrCreateHitReturnsTheRow(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	seeded := hasManyThroughCreateOrFirstSeedChild(t, query, 789, 456, "foo", "bar", now)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).FirstOrCreate(
		&result, map[string]any{"attr": "foo"}, map[string]any{"val": "ignored"}))

	assert.Equal(t, seeded.ID, result.ID)
	assert.Equal(t, "bar", result.Val, "a hit returns the stored row untouched")

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "the hit is the lookup select alone")
	assert.Equal(t, `select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = 123 and "attr" = 'foo' order by "child"."id" limit 1`, logs[0].Query)
}

// --- createOrFirst -----------------------------------------------------------

// Oracle: $parent->children()->createOrFirst(['attr'=>'direct'], ['val'=>'dv']) inserts
// immediately — `insert into "child" ("attr", "val", "updated_at", "created_at") values (?, ?, ?,
// ?)` — with no lookup first.

func TestHasManyThroughObject_CreateOrFirstMissInsertsWithoutALookup(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).CreateOrFirst(
		&result, map[string]any{"attr": "direct"}, map[string]any{"val": "dv"}))

	require.NotZero(t, result.ID)
	assert.Equal(t, "direct", result.Attr)
	assert.Equal(t, "dv", result.Val)
	require.NotNil(t, result.CreatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "createOrFirst attempts the insert with no lookup first")
	assert.Equal(t, `insert into "child" ("attr", "created_at", "pivot_id", "updated_at", "val") values ('direct', '2023-01-01 00:00:00', 0, '2023-01-01 00:00:00', 'dv')`, logs[0].Query)
}

// Oracle: createOrFirst(['attr'=>'foo'], ['val'=>'ignored']) whose insert loses a unique race
// re-reads `... where "pivot"."parent_id" = ? and ("attr" = ?) limit 1` (bindings 123, "foo")
// and returns the winner.

func TestHasManyThroughObject_CreateOrFirstLostRaceReadsTheWinnerBack(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	runRelationSchema(t, query, `create unique index child_attr_unique on child (attr)`)
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	seeded := hasManyThroughCreateOrFirstSeedChild(t, query, 789, 456, "foo", "bar", now)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).CreateOrFirst(
		&result, map[string]any{"attr": "foo"}, map[string]any{"val": "ignored"}))

	assert.Equal(t, seeded.ID, result.ID, "the catch's re-read returns the unique-constraint winner")
	assert.Equal(t, uint(456), result.PivotID)
	assert.Equal(t, "bar", result.Val)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "the failed insert attempt, then the catch's re-read")
	assert.Equal(t, `insert into "child" ("attr", "created_at", "pivot_id", "updated_at", "val") values ('foo', '2023-01-01 00:00:00', 0, '2023-01-01 00:00:00', 'ignored')`, logs[0].Query)
	assert.Equal(t, `select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = 123 and "attr" = 'foo' order by "child"."id" limit 1`, logs[1].Query)
}

// --- updateOrCreate ----------------------------------------------------------

// Oracle: $parent->children()->updateOrCreate(['attr'=>'upd'], ['val'=>'uv']) on an empty child
// table runs the lookup select, then the insert with the merged values.

func TestHasManyThroughObject_UpdateOrCreateMissCreatesTheRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}
	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).UpdateOrCreate(
		&result, map[string]any{"attr": "upd"}, map[string]any{"val": "uv"}))

	require.NotZero(t, result.ID)
	assert.Equal(t, "upd", result.Attr)
	assert.Equal(t, "uv", result.Val)
	require.NotNil(t, result.CreatedAt)
	assert.True(t, result.CreatedAt.StdTime().Equal(now.StdTime()))

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "the miss is the lookup select plus the insert")
	assert.Equal(t, `select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = 123 and "attr" = 'upd' order by "child"."id" limit 1`, logs[0].Query)
	assert.Equal(t, `insert into "child" ("attr", "created_at", "pivot_id", "updated_at", "val") values ('upd', '2023-01-01 00:00:00', 0, '2023-01-01 00:00:00', 'uv')`, logs[1].Query)
}

// Oracle: updateOrCreate(['attr'=>'foo'], ['val'=>'baz']) with child 789 (foo/bar) seeded fills
// the found row with the values and saves it — `update "child" set "val" = ?, "updated_at" = ?
// where "id" = ?` — a hit shape.

func TestHasManyThroughObject_UpdateOrCreateHitUpdatesTheRecord(t *testing.T) {
	now := hasManyThroughCreateOrFirstFreezeNow(t)
	query := newRelationQuery(t)
	hasManyThroughCreateOrFirstSchema(t, query)

	hasManyThroughCreateOrFirstSeedPivot(t, query, 456, 123)
	seeded := hasManyThroughCreateOrFirstSeedChild(t, query, 789, 456, "foo", "bar", now)

	parent := hasManyThroughCreateOrFirstParent{ID: 123}

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	result := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, hasManyThroughObject(t, withContext.WithContext(ctx), &parent).UpdateOrCreate(
		&result, map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}))

	assert.Equal(t, seeded.ID, result.ID)
	assert.Equal(t, "baz", result.Val)

	reloaded := hasManyThroughCreateOrFirstChild{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", seeded.ID).First(&reloaded))
	assert.Equal(t, "baz", reloaded.Val, "the found row is filled with the values and saved")

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "the hit is the lookup select plus the update")
	assert.Equal(t, `select "child".*, "pivot"."parent_id" as "laravel_through_key" from "child" inner join "pivot" on "pivot"."id" = "child"."pivot_id" where "pivot"."parent_id" = 123 and "attr" = 'foo' order by "child"."id" limit 1`, logs[0].Query)
	// The dirty-only update (Model::performUpdate; oracle re-measured for this item at
	// /tmp/save11/through_update_probe.php: `update "child" set "val" = ?, "updated_at" = ? where
	// "id" = ?`): only the filled val changed, updated_at is stamped at save time, and the loaded
	// row's attr / created_at / pivot_id stay out of the statement.
	assert.Equal(t, `update "child" set "updated_at" = '2023-01-01 00:00:00', "val" = 'baz' where "id" = 789`, logs[1].Query)
}
