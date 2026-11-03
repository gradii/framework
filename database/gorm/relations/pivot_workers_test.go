package relations_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbcontract "github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
)

// The S2 step-3 port (pivot_workers.go): the many-to-many / pivot write workers, now free
// functions over (Builder, RelationInfo), pinned on the real *gorm.OrmBuilder of the recording
// transport. The statement shapes the shells compile end-to-end live in
// pivot_workers_shapes_test.go (the before/after log); here each worker's own guards, projections
// and statement fragments are held directly.

// --- ResolvePivot / KindName -------------------------------------------------------------------

func TestPivotWorkers_ResolvePivot_GuardsAndProjection(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	_, _, err := relations.ResolvePivot(builder, pwParent{ID: 7}, "Kids", "Attach")
	require.True(t, errors.Is(err, errors.OrmRelationParentNotPointer), "%v", err)

	_, _, err = relations.ResolvePivot(builder, &pwParent{ID: 7}, "Nope", "Attach")
	require.True(t, errors.Is(err, errors.OrmRelationNotFound), "%v", err)

	_, _, err = relations.ResolvePivot(builder, &sqlUser{ID: 7}, "Books", "Sync")
	require.True(t, errors.Is(err, errors.OrmRelationKindNotSupported), "%v", err)
	assert.Contains(t, err.Error(), `operation "Sync" is not supported on relation "Books" (kind "hasMany")`)

	info, parentVal, err := relations.ResolvePivot(builder, &pwParent{ID: 7}, "Kids", "Attach")
	require.NoError(t, err)
	assert.Equal(t, relations.KindMany2Many, info.Kind)
	assert.Equal(t, uint(7), parentVal)
}

func TestPivotWorkers_KindName(t *testing.T) {
	cases := map[relations.Kind]string{
		relations.KindHasOne:         "hasOne",
		relations.KindHasMany:        "hasMany",
		relations.KindBelongsTo:      "belongsTo",
		relations.KindMany2Many:      "many2Many",
		relations.KindMorphOne:       "morphOne",
		relations.KindMorphMany:      "morphMany",
		relations.KindMorphTo:        "morphTo",
		relations.KindMorphToMany:    "morphToMany",
		relations.KindHasOneThrough:  "hasOneThrough",
		relations.KindHasManyThrough: "hasManyThrough",
	}
	for kind, want := range cases {
		assert.Equal(t, want, relations.KindName(kind))
	}
	assert.Equal(t, "kind=999", relations.KindName(relations.Kind(999)))
}

// --- BasePivotRow ------------------------------------------------------------------------------

func TestPivotWorkers_BasePivotRow_StampsKeysAndMorphType(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")
	row := relations.BasePivotRow(info, uint(7), uint(99), nil)
	assert.Equal(t, uint(7), row["parent_id"])
	assert.Equal(t, uint(99), row["kid_id"])
	_, hasCreated := row["created_at"]
	_, hasUpdated := row["updated_at"]
	assert.False(t, hasCreated)
	assert.False(t, hasUpdated)

	morph := wwInfo(t, builder, &pwParent{ID: 7}, "Tags")
	mrow := relations.BasePivotRow(morph, uint(3), uint(11), nil)
	assert.Equal(t, uint(3), mrow["taggable_id"])
	assert.Equal(t, "pw_parents", mrow["taggable_type"])
	assert.Equal(t, uint(11), mrow["tag_id"])

	overlaid := relations.BasePivotRow(info, uint(7), uint(99), map[string]any{"priority": "high"})
	assert.Equal(t, "high", overlaid["priority"])
	assert.Equal(t, uint(7), overlaid["parent_id"])
}

func TestPivotWorkers_BasePivotRow_StampsResolvedTimestampsAndAttrsWin(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")
	info.PivotCreatedAtColumn = "created_at"
	info.PivotUpdatedAtColumn = "updated_at"

	row := relations.BasePivotRow(info, uint(7), uint(99), nil)
	created, hasCreated := row["created_at"]
	assert.True(t, hasCreated)
	assert.IsType(t, time.Time{}, created)
	_, hasUpdated := row["updated_at"]
	assert.True(t, hasUpdated)

	custom := "2024-01-01 00:00:00"
	row = relations.BasePivotRow(info, uint(7), uint(99), map[string]any{"created_at": custom})
	assert.Equal(t, custom, row["created_at"], "caller-supplied attrs win")

	info.PivotCreatedAtColumn = ""
	row = relations.BasePivotRow(info, uint(7), uint(99), nil)
	_, hasCreated = row["created_at"]
	assert.False(t, hasCreated, "no created_at column resolved")
	_, hasUpdated = row["updated_at"]
	assert.True(t, hasUpdated, "updated_at only")
}

// --- DoAttach / DoAttachWithPivot --------------------------------------------------------------

// DoAttach performs no pivot-existence read: Laravel's attach has none — one INSERT carries every
// id (InteractsWithPivotTable::attach :337). Oracle v12.55.1, executed,
// /tmp/attach_dedup_probe.php: A1a/B0 logs are the single INSERT, no SELECT; A2 attach([1,1]) ->
// one INSERT of two rows; B1/B2 a duplicate against an existing row raises the pivot's unique
// constraint; C1 an empty attach is query-free here (the shell still touches; C1).
func TestPivotWorkers_DoAttach_InsertsWithoutAnyExistenceRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")

	n, err := relations.DoAttach(builder, info, uint(7), []any{uint(5), uint(6)}, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	statements := recorder.take()
	require.Len(t, statements, 1, "the attach is a single INSERT — no pivot-existence read")
	assert.Equal(t, `insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?), (?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 4)
	assert.EqualValues(t, 5, deref(statements[0].args[0]))
	assert.EqualValues(t, 7, deref(statements[0].args[1]))
	assert.EqualValues(t, 6, deref(statements[0].args[2]))
	assert.EqualValues(t, 7, deref(statements[0].args[3]))

	// A duplicate within one call is an insert record each (oracle A2: attach([1,1]) -> two rows).
	n, err = relations.DoAttach(builder, info, uint(7), []any{uint(5), uint(5)}, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?), (?, ?)`, statements[0].query)

	// Empty ids stay a no-op: Laravel's insert([]) runs no statement.
	n, err = relations.DoAttach(builder, info, uint(7), nil, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	assert.Empty(t, recorder.take())
}

// The map-shaped entry walks in sortedAttrsIDs order — a Go map carries no insertion order, and
// the insert must not inherit Go's randomized iteration order.
func TestPivotWorkers_DoAttachWithPivot_CanonicalRowOrderAndNoRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")

	n, err := relations.DoAttachWithPivot(builder, info, uint(7), map[any]map[string]any{
		uint(6): {"role": "b"},
		uint(5): {"role": "a"},
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	statements := recorder.take()
	require.Len(t, statements, 1, "no existence read before the insert")
	assert.Equal(t, `insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?), (?, ?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 6)
	assert.EqualValues(t, 5, deref(statements[0].args[0]))
	assert.EqualValues(t, 7, deref(statements[0].args[1]))
	assert.Equal(t, "a", deref(statements[0].args[2]))
	assert.EqualValues(t, 6, deref(statements[0].args[3]))
	assert.EqualValues(t, 7, deref(statements[0].args[4]))
	assert.Equal(t, "b", deref(statements[0].args[5]))
}

// The WithPivotValue-recorded pairs ride the INSERT through baseAttachRecord's values loop: the
// pairs sit between the timestamps and the caller's attributes (recorded defaults, the caller
// wins, the last recorded pair for a column winning, a non-string column skipped) — oracle
// v12.55.1, executed, /tmp/r7pv/attach_pv_probe.php A1/A2/A2b/A3/A7.
func TestPivotWorkers_DoAttach_StampsRecordedPivotValues(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")

	n, err := relations.DoAttach(builder, info, uint(7), []any{uint(5)}, nil,
		relations.PivotValue{Column: "role", Value: "gold"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, 5, deref(statements[0].args[0]))
	assert.EqualValues(t, 7, deref(statements[0].args[1]))
	assert.Equal(t, "gold", deref(statements[0].args[2]))

	// The caller's attributes win over the recorded pair (oracle A2).
	n, err = relations.DoAttach(builder, info, uint(7), []any{uint(6)}, map[any]map[string]any{uint(6): {"role": "caller"}},
		relations.PivotValue{Column: "role", Value: "gold"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, "caller", deref(statements[0].args[2]))

	// The last recorded pair for a column wins (oracle A3); a non-string column cannot key a
	// row and is skipped.
	n, err = relations.DoAttach(builder, info, uint(7), []any{uint(6)}, nil,
		relations.PivotValue{Column: "role", Value: "a"},
		relations.PivotValue{Column: "role", Value: "b"},
		relations.PivotValue{Column: 7, Value: "x"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?)`, statements[0].query)
	assert.Equal(t, "b", deref(statements[0].args[2]))
}

// --- AllPivotIDs -------------------------------------------------------------------------------

// AllPivotIDs is the sync-family current-ids read: `select *` (Laravel getCurrentlyAttachedPivots),
// the related key plucked from the column-keyed rows — oracle v12.55.1, executed,
// /tmp/lalign_synccore_order_probe.php C1.
func TestPivotWorkers_AllPivotIDs_ReadShapes(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")

	ids, err := relations.AllPivotIDs(builder, info, uint(7))
	require.NoError(t, err)
	assert.Empty(t, ids)
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ?`, statements[0].query)

	builder, recorder = newRelationTestBuilder(t)
	morph := wwInfo(t, builder, &pwParent{ID: 7}, "Tags")
	_, err = relations.AllPivotIDs(builder, morph, uint(7))
	require.NoError(t, err)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "pw_taggables" where "pw_taggables"."taggable_id" = ? and "pw_taggables"."taggable_type" = ?`, statements[0].query)
}

// --- TogglePivotIDs ---------------------------------------------------------------------------

// TogglePivotIDs keeps toggle's read: the related key plucked at the query level —
// `select "tag_id"` (InteractsWithPivotTable::toggle via newPivotQuery()->pluck
// ($this->relatedPivotKey)) — where the sync-family read is AllPivotIDs' `select *`.
// Oracle v12.55.1, executed, /tmp/lalign_synccore_order_probe.php C2.
func TestPivotWorkers_TogglePivotIDs_ReadShapes(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")

	ids, err := relations.TogglePivotIDs(builder, info, uint(7))
	require.NoError(t, err)
	assert.Empty(t, ids)
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ?`, statements[0].query)

	builder, recorder = newRelationTestBuilder(t)
	morph := wwInfo(t, builder, &pwParent{ID: 7}, "Tags")
	_, err = relations.TogglePivotIDs(builder, morph, uint(7))
	require.NoError(t, err)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select "tag_id" from "pw_taggables" where "pw_taggables"."taggable_id" = ? and "pw_taggables"."taggable_type" = ?`, statements[0].query)
}

// --- DoDetach ----------------------------------------------------------------------------------

func TestPivotWorkers_DoDetach_GuardsAndShapes(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")

	// nil ids is Laravel's detach(null): delete every pivot row of the parent.
	n, err := relations.DoDetach(builder, info, uint(7), nil)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ?`, statements[0].query)

	// An explicitly empty id list is detach([]) — 0, no statement.
	builder, recorder = newRelationTestBuilder(t)
	info = wwInfo(t, builder, &pwParent{ID: 7}, "Kids")
	n, err = relations.DoDetach(builder, info, uint(7), []any{})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	assert.Empty(t, recorder.take())

	// A single empty nested slice reads the same way.
	n, err = relations.DoDetach(builder, info, uint(7), []any{[]any{}})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	assert.Empty(t, recorder.take())

	// A []byte is not treated as an empty list: the statement still runs.
	_, err = relations.DoDetach(builder, info, uint(7), []any{[]byte{}})
	require.NoError(t, err)
	assert.Len(t, recorder.take(), 1)

	// ids -> the typed IN clause.
	builder, recorder = newRelationTestBuilder(t)
	info = wwInfo(t, builder, &pwParent{ID: 7}, "Kids")
	_, err = relations.DoDetach(builder, info, uint(7), []any{uint(5), uint(6)})
	require.NoError(t, err)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, 7, deref(statements[0].args[0]))
	assert.EqualValues(t, 5, deref(statements[0].args[1]))
	assert.EqualValues(t, 6, deref(statements[0].args[2]))

	// morph -> the type predicate between the parent key and the IN list.
	builder, recorder = newRelationTestBuilder(t)
	morph := wwInfo(t, builder, &pwParent{ID: 7}, "Tags")
	_, err = relations.DoDetach(builder, morph, uint(7), []any{uint(5)})
	require.NoError(t, err)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `delete from "pw_taggables" where "pw_taggables"."taggable_id" = ? and "pw_taggables"."taggable_type" = ? and "pw_taggables"."tag_id" in (?)`, statements[0].query)

	// scoped -> the scope predicate leads.
	builder, recorder = newRelationTestBuilder(t)
	scoped := wwInfo(t, builder, &pwParent{ID: 7}, "Scoped")
	_, err = relations.DoDetach(builder, scoped, uint(7), nil)
	require.NoError(t, err)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `delete from "pw_parent_kids" where "pw_parent_kids"."role" = ? and "pw_parent_kids"."parent_id" = ?`, statements[0].query)
}

// --- DoUpdateExistingPivot ---------------------------------------------------------------------

func TestPivotWorkers_DoUpdateExistingPivot_ShapeAndTimestampInjection(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	info := wwInfo(t, builder, &pwParent{ID: 7}, "Kids")

	// No attrs and no resolved updated_at column — the early no-op.
	n, err := relations.DoUpdateExistingPivot(builder, info, uint(7), uint(5), map[string]any{})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	assert.Empty(t, recorder.take())

	// Attrs -> the UPDATE.
	n, err = relations.DoUpdateExistingPivot(builder, info, uint(7), uint(5), map[string]any{"role": "x"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `update "pw_parent_kids" set "role" = ? where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?)`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.Equal(t, "x", deref(statements[0].args[0]))
	assert.EqualValues(t, 7, deref(statements[0].args[1]))
	assert.EqualValues(t, 5, deref(statements[0].args[2]))

	// A resolved updated_at column with empty attrs injects the clock.
	info.PivotUpdatedAtColumn = "updated_at"
	builder, recorder = newRelationTestBuilder(t)
	_, err = relations.DoUpdateExistingPivot(builder, info, uint(7), uint(5), map[string]any{})
	require.NoError(t, err)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `update "pw_parent_kids" set "updated_at" = ? where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?)`, statements[0].query)
	assert.IsType(t, time.Time{}, deref(statements[0].args[0]))

	// A caller-supplied updated_at wins over the injected clock.
	fixed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	builder, recorder = newRelationTestBuilder(t)
	_, err = relations.DoUpdateExistingPivot(builder, info, uint(7), uint(5), map[string]any{"updated_at": fixed})
	require.NoError(t, err)
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `update "pw_parent_kids" set "updated_at" = ? where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?)`, statements[0].query)
	assert.Equal(t, fixed, deref(statements[0].args[0]))
}

// --- CastKeys / CastKey ------------------------------------------------------------------------

func TestPivotWorkers_CastKey(t *testing.T) {
	uintT := reflect.TypeFor[uint]()
	intT := reflect.TypeFor[int]()
	int64T := reflect.TypeFor[int64]()
	stringT := reflect.TypeFor[string]()
	float64T := reflect.TypeFor[float64]()

	cases := []struct {
		name string
		in   any
		t    reflect.Type
		want any
	}{
		{"nil value passthrough", nil, uintT, nil},
		{"nil type passthrough", uint(7), nil, uint(7)},
		{"same-type passthrough", uint(7), uintT, uint(7)},
		{"int -> uint", int(7), uintT, uint(7)},
		{"int64 -> uint", int64(42), uintT, uint(42)},
		{"uint -> int", uint(7), intT, int(7)},
		{"uint -> int64", uint(7), int64T, int64(7)},
		{"float -> int", float64(3), intT, int(3)},
		{"string numeric -> uint", "42", uintT, uint(42)},
		{"string numeric -> int (negative)", "-7", intT, int(-7)},
		{"string numeric -> float", "3.14", float64T, float64(3.14)},
		{"int -> string (decimal, not Unicode)", int(65), stringT, "65"},
		{"[]byte -> string", []byte("abc"), stringT, "abc"},
		{"non-numeric string -> uint passthrough", "abc", uintT, "abc"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, relations.CastKey(tt.in, tt.t))
		})
	}
}

func TestPivotWorkers_CastKeys(t *testing.T) {
	uintT := reflect.TypeFor[uint]()

	assert.Nil(t, relations.CastKeys(nil, uintT))
	assert.Equal(t, []any{}, relations.CastKeys([]any{}, uintT))
	assert.Equal(t, []any{uint(1), uint(2), uint(3), uint(4)}, relations.CastKeys([]any{int(1), int64(2), "3", uint(4)}, uintT))
	assert.Equal(t, []any{int(1), "2"}, relations.CastKeys([]any{int(1), "2"}, nil))
}

// --- SyncResultChanged -------------------------------------------------------------------------

func TestPivotWorkers_SyncResultChanged(t *testing.T) {
	assert.False(t, relations.SyncResultChanged(&dbcontract.SyncResult{}))
	assert.True(t, relations.SyncResultChanged(&dbcontract.SyncResult{Attached: []any{1}}))
	assert.True(t, relations.SyncResultChanged(&dbcontract.SyncResult{Detached: []any{2}}))
	assert.True(t, relations.SyncResultChanged(&dbcontract.SyncResult{Updated: []any{3}}))
	assert.True(t, relations.SyncResultChanged(&dbcontract.SyncResult{Attached: []any{1}, Detached: []any{2}, Updated: []any{3}}))
}
