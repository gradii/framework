// Package relations_test drives the MorphPivot port through a real *gorm.OrmBuilder on the same
// recording transport as the Pivot tests: SQL expectations are pinned against executed oracle
// probes (/tmp/morphpivotprobes/state_probe.php, sql_probe.php, short_id_probe.php,
// illuminate/database v12.55.1). No file named MorphPivotTest.php exists in either reference tree
// (find over /Users/dog/OpenSource/framework and the vendor illuminate/database tree); the
// morph-pivot cases of the corpus live in DatabaseEloquentIntegrationTest::testMorphPivotsCanBeRefreshed,
// EloquentPivotSerializationTest::testMorphPivotCanBeSerializedAndRestored /
// testCollectionOfMorphPivotsCanBeSerializedAndRestored, and
// EloquentPivotEventsTest::testCustomMorphPivotClassDetachAttributes — the serialization pair's
// mechanism (6-segment queueable id + 6-segment restoration) is pinned here; the relation-writer
// wiring those cases exercise end-to-end belongs to the MorphToMany surface.
package relations_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	relations "github.com/gradii/framework/database/gorm/relations"
	frameworkerrors "github.com/gradii/framework/errors"
)

// --- fixtures -------------------------------------------------------------------------------------

// morphPivotTestClass is the class-string the morph column pins — the value a MorphToMany
// relation stamps as morphClass.
const morphPivotTestClass = "App\\Models\\Post"

// newMorphPivot builds the morph pivot the way MorphPivot::fromAttributes does over the given
// parent; tests stamp keys and morph state through withMorphKeys.
func newMorphPivot(t *testing.T, builder relations.Builder, parent any, attributes map[string]any, exists bool) *relations.MorphPivot {
	t.Helper()

	pivot, err := relations.FromMorphAttributes(builder, parent, attributes, "role_user", exists)
	require.NoError(t, err)

	return pivot
}

// withMorphKeys stamps the conventional key pair and morph state. The statements are separate
// because Go method promotion returns the base *Pivot from the fluent base setters — the Go-ism
// of embedding: the state mutates identically, the chain just ends at the base type.
func withMorphKeys(pivot *relations.MorphPivot) *relations.MorphPivot {
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetMorphType("taggable_type")
	pivot.SetMorphClass(morphPivotTestClass)

	return pivot
}

// morphTouchSpy wraps the real builder and records the AsPivot hooks the pivot calls: the
// touchOwners walk and the event names. Everything else is promoted to the real *gorm.OrmBuilder.
type morphTouchSpy struct {
	relations.Builder

	touches int
	events  []string
}

func (s *morphTouchSpy) PivotTouchOwners(pivot any) error {
	s.touches++

	return s.Builder.PivotTouchOwners(pivot)
}

func (s *morphTouchSpy) FirePivotEvent(name string, pivot any) error {
	s.events = append(s.events, name)

	return s.Builder.FirePivotEvent(name, pivot)
}

// --- construction / state -------------------------------------------------------------------------

func TestMorphPivot_ConstructionAndSetters(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	pivot := newMorphPivot(t, builder, parent, map[string]any{"foo": "bar", "created_at": "2015-09-12"}, true)

	// Oracle v12.55.1, executed (state probe): a fresh MorphPivot's morphType property is null,
	// and the constructed instance carries the table, exists flag, parent link and the
	// attribute-driven timestamps flag exactly like the base Pivot.
	assert.Equal(t, "role_user", pivot.GetTable())
	assert.True(t, pivot.Exists())
	assert.Same(t, parent, pivot.GetPivotParent())
	assert.True(t, pivot.UsesTimestamps())
	assert.Empty(t, pivot.GetDirty())
	assert.Equal(t, "", pivot.MorphType())

	// Oracle: setMorphType and setMorphClass return the same instance (fluent setters).
	assert.Same(t, pivot, pivot.SetMorphType("taggable_type"))
	assert.Same(t, pivot, pivot.SetMorphClass(morphPivotTestClass))
	assert.Equal(t, "taggable_type", pivot.MorphType())

	// Oracle: after the setters the attribute bag still reads the original keys — the morph state
	// lives in the declared (non-attribute) properties and dirties nothing.
	assert.Equal(t, map[string]any{"foo": "bar", "created_at": "2015-09-12"}, pivot.Attributes())
	assert.Nil(t, pivot.GetAttribute("taggable_type"))
	assert.Empty(t, pivot.GetDirty())
}

func TestMorphPivot_FromMorphRawAttributes(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	// MorphPivot::fromRawAttributes with the base constructor under static dispatch: the raw bag
	// never syncs with exists=false, so every raw key reads dirty, and no morph state is implied.
	raw, err := relations.FromMorphRawAttributes(builder, parent, map[string]any{"foo": "bar", "created_at": "foo"}, "role_user", false)
	require.NoError(t, err)
	assert.Equal(t, "foo", raw.GetAttribute("created_at"))
	assert.True(t, raw.UsesTimestamps())
	assert.False(t, raw.Exists())
	assert.Equal(t, map[string]any{"foo": "bar", "created_at": "foo"}, raw.GetDirty())
	assert.Equal(t, "", raw.MorphType())

	synced, err := relations.FromMorphRawAttributes(builder, parent, map[string]any{"foo": "bar"}, "role_user", true)
	require.NoError(t, err)
	assert.Empty(t, synced.GetDirty())
	assert.True(t, synced.Exists())
}

func TestMorphPivot_GetTableFallback(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)

	// Oracle v12.55.1, executed: `(new MorphPivot)->getTable()` reports "morph_pivot" —
	// class_basename(MorphPivot) through Str::snake(Str::singular()).
	bare := relations.NewMorphPivot(builder)
	assert.Equal(t, "morph_pivot", bare.GetTable())

	bare.SetTable("taggables")
	assert.Equal(t, "taggables", bare.GetTable())
}

func TestMorphPivot_PivotKeysPinTheTypeFirst(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{}, true))

	// contracts PivotKeyed: AsPivot's pair plus the morph type first — the order
	// MorphPivot::setKeysForSelectQuery writes them; oracle, executed: `$morphPivot->fresh()`
	// compiles `select * from "taggables" where "taggable_type" = ? and "taggable_id" = ? and
	// "tag_id" = ? limit 1`.
	var keyed contractsorm.PivotKeyed = pivot
	assert.Equal(t, []string{"taggable_type", "foreign", "other"}, keyed.PivotKeys())
}

func TestMorphPivot_QueueableID(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	// Oracle v12.55.1, executed (state probe): the six-segment
	// foreignKey:value:relatedKey:value:morphType:morphClass form.
	pivot := withMorphKeys(newMorphPivot(t, builder, parent, map[string]any{"foreign": "fv", "other": "ov"}, true))
	assert.Equal(t, "foreign:fv:other:ov:taggable_type:"+morphPivotTestClass, pivot.GetQueueableID())

	// An own key wins (getKey()) ...
	keyed := withMorphKeys(newMorphPivot(t, builder, parent, map[string]any{"id": uint64(7)}, true))
	assert.EqualValues(t, 7, keyed.GetQueueableID())

	// ... null values render empty segments (sprintf %s) ...
	nulls := newMorphPivot(t, builder, parent, map[string]any{"foreign": nil, "other": nil}, true)
	nulls.SetPivotKeys("foreign", "other")
	nulls.SetMorphType("t")
	nulls.SetMorphClass("C")
	assert.Equal(t, "foreign::other::t:C", nulls.GetQueueableID())

	// ... and an unset class leaves the last segment empty.
	classless := newMorphPivot(t, builder, parent, map[string]any{"foreign": "f"}, true)
	classless.SetPivotKeys("foreign", "other")
	classless.SetMorphType("t")
	assert.Equal(t, "foreign:f:other::t:", classless.GetQueueableID())
}

func TestMorphPivot_SetKeysForSelectQuerySingleMorphClause(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true))

	// MorphPivot::setKeysForSelectQuery (MorphPivot.php:44-52): the morph where first, then the
	// base pair. The method is protected in PHP, so the shape is pinned directly here from the
	// source order — the public SQL paths are the save / delete statements probed below.
	query, err := builder.NewPivotQuery("role_user", "")
	require.NoError(t, err)

	var dest []map[string]any
	require.NoError(t, pivot.SetKeysForSelectQuery(query).Get(&dest))

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "taggable_type" = ? and "foreign" = ? and "other" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[0])
	assert.EqualValues(t, "fv", statements[0].args[1])
	assert.EqualValues(t, "ov", statements[0].args[2])

	// With an own key the base branch is the primary-key where, morph clause first.
	keyed := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(7)}, true))
	query, err = builder.NewPivotQuery("role_user", "")
	require.NoError(t, err)
	require.NoError(t, keyed.SetKeysForSelectQuery(query).Get(&dest))

	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "taggable_type" = ? and "id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[0])
	assert.EqualValues(t, 7, statements[0].args[1])
}

// --- Save ------------------------------------------------------------------------------------------

func TestMorphPivot_SaveExistingUsesMorphWhere(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true))
	pivot.SetAttribute("note", "changed")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed (SQL probe): the morph clause TWICE — MorphPivot::
	// setKeysForSaveQuery adds one and Pivot::setKeysForSaveQuery re-dispatches
	// $this->setKeysForSelectQuery into the morph override for the second — before the pair.
	assert.Equal(t, `update "role_user" set "note" = ? where "taggable_type" = ? and "taggable_type" = ? and "foreign" = ? and "other" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 5)
	assert.EqualValues(t, "changed", statements[0].args[0])
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[1])
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[2])
	assert.EqualValues(t, "fv", statements[0].args[3])
	assert.EqualValues(t, "ov", statements[0].args[4])

	assert.True(t, pivot.Exists())
	assert.Empty(t, pivot.GetDirty())
}

func TestMorphPivot_SaveSkipsTheStatementWhenClean(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true))
	require.NoError(t, pivot.Save())

	// Oracle v12.55.1, executed: an untouched pivot's save returns true without a statement.
	assert.Empty(t, recorder.take())
}

func TestMorphPivot_CleanSaveSkipsTheUpdatePath(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)
	spy := &morphTouchSpy{Builder: builder}

	// Oracle v12.55.1, executed (verifier probe /tmp/morphverify/p1_saveflow.php, cases B/E):
	// Model::save gates performUpdate on isDirty() - a clean timestamped morph pivot emits no
	// statement and no updated_at stamp, and fires only [saving, saved].
	pivot := withMorphKeys(newMorphPivot(t, spy, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov", "created_at": "2015-09-12 00:00:00"}, true))
	require.NoError(t, pivot.Save())

	assert.Empty(t, recorder.take())
	assert.Equal(t, []string{"saving", "saved"}, spy.events)
	assert.Nil(t, pivot.GetAttribute("updated_at"), "a clean save never stamps updated_at")
}

func TestMorphPivot_SaveNewEmptyAttributesIsANoOp(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)
	spy := &morphTouchSpy{Builder: builder}

	// Oracle v12.55.1, executed (verifier probe /tmp/morphverify/p8_empty_save.php):
	// performInsert's empty-attribute early return — no statement, no created event, exists
	// stays false; finishSave still fires saved (no touch: the pivot reads clean).
	pivot := withMorphKeys(newMorphPivot(t, spy, &pivotTestParent{ID: 1}, map[string]any{}, false))
	require.NoError(t, pivot.Save())

	assert.Empty(t, recorder.take())
	assert.Equal(t, []string{"saving", "creating", "saved"}, spy.events)
	assert.False(t, pivot.Exists(), "performInsert returns before exists is set")
	assert.Equal(t, 0, spy.touches)
}

func TestMorphPivot_SaveStampsUpdatedAtWithMorphWhere(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov", "created_at": "2015-09-12 00:00:00"}, true))
	pivot.SetAttribute("note", "n2")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `update "role_user" set "note" = ?, "updated_at" = ? where
	// "taggable_type" = ? and "taggable_type" = ? and "foreign" = ? and "other" = ?` — an
	// existing pivot stamps only updated_at.
	assert.Equal(t, `update "role_user" set "note" = ?, "updated_at" = ? where "taggable_type" = ? and "taggable_type" = ? and "foreign" = ? and "other" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 6)
	assert.EqualValues(t, "n2", statements[0].args[0])
	stamp, ok := statements[0].args[1].(time.Time)
	require.True(t, ok, "expected a timestamp binding, got %T", statements[0].args[1])
	assert.WithinDuration(t, time.Now(), stamp, time.Minute)
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[2])
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[3])
	assert.EqualValues(t, "fv", statements[0].args[4])
	assert.EqualValues(t, "ov", statements[0].args[5])
	assert.False(t, pivot.IsDirty("created_at"), "created_at is never dirtied on the update path")
}

func TestMorphPivot_SaveOwnKeyUsesMorphWhere(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(7), "foreign": "f7"}, true))
	pivot.SetAttribute("note", "n3")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `update "role_user" set "note" = ? where "taggable_type" = ? and
	// "taggable_type" = ? and "id" = ?`.
	assert.Equal(t, `update "role_user" set "note" = ? where "taggable_type" = ? and "taggable_type" = ? and "id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 4)
	assert.EqualValues(t, "n3", statements[0].args[0])
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[1])
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[2])
	assert.EqualValues(t, 7, statements[0].args[3])
}

func TestMorphPivot_SaveNewInsertsWithoutMorphColumns(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "nf", "other": "no"}, false))
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `insert into "role_user" ("foreign", "other") values (?, ?)` —
	// the declared (non-attribute) morph properties are NOT part of the saved attributes.
	assert.Equal(t, `insert into "role_user" ("foreign", "other") values (?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, "nf", statements[0].args[0])
	assert.EqualValues(t, "no", statements[0].args[1])

	assert.True(t, pivot.Exists())
	assert.Equal(t, "foreign:nf:other:no:taggable_type:"+morphPivotTestClass, pivot.GetQueueableID())
}

// --- touchOwners -----------------------------------------------------------------------------------

func TestMorphPivot_TouchOwnersOnSaveAndOwnKeyDelete(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	spy := &morphTouchSpy{Builder: builder}
	parent := &pivotTestParent{ID: 1}

	// finishSave's dirty branch: an update save walks touchOwners (base-pinned flow, kept by the
	// morph port).
	updated := withMorphKeys(newMorphPivot(t, spy, parent, map[string]any{"foreign": "fv", "other": "ov"}, true))
	updated.SetAttribute("note", "n")
	require.NoError(t, updated.Save())
	assert.Equal(t, 1, spy.touches)

	// A timestamped insert dirties the pivot and walks touchOwners too.
	spy.touches = 0
	inserted := withMorphKeys(newMorphPivot(t, spy, parent, map[string]any{"foreign": "nf", "other": "no", "created_at": "2015-09-12 00:00:00"}, false))
	require.NoError(t, inserted.Save())
	assert.Equal(t, 1, spy.touches)

	// Model::delete's own-key branch (MorphPivot::delete delegates with parent::delete()) keeps
	// the walk.
	spy.touches = 0
	keyed := withMorphKeys(newMorphPivot(t, spy, parent, map[string]any{"id": uint64(7)}, true))
	_, err := keyed.Delete()
	require.NoError(t, err)
	assert.Equal(t, 1, spy.touches)
}

func TestMorphPivot_DeleteNoKeyOmitsTouchOwners(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	spy := &morphTouchSpy{Builder: builder}
	parent := &pivotTestParent{ID: 1}

	// The quirk (MorphPivot.php:56-75): the keyless override runs deleting -> getDeleteQuery ->
	// morph where -> delete -> deleted with NO touchOwners step; AsPivot's keyless delete has one.
	pivot := withMorphKeys(newMorphPivot(t, spy, parent, map[string]any{"foreign": "fv", "other": "ov"}, true))
	_, err := pivot.Delete()
	require.NoError(t, err)
	assert.Equal(t, 0, spy.touches, "MorphPivot::delete omits touchOwners on the keyless path")
	assert.Equal(t, []string{"deleting", "deleted"}, spy.events)

	// Contrast: the base Pivot's keyless delete walks touchOwners (AsPivot.php:130,140) — the spy
	// counts it, so the omission above is morph-specific, not a dead spy.
	spy.events = nil
	base, err := relations.FromAttributes(spy, parent, map[string]any{"foreign": "fv", "other": "ov"}, "role_user", true)
	require.NoError(t, err)
	base.SetPivotKeys("foreign", "other")
	_, err = base.Delete()
	require.NoError(t, err)
	assert.Equal(t, 1, spy.touches)
}

// --- Delete ----------------------------------------------------------------------------------------

func TestMorphPivot_DeleteWithoutKey(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true))

	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed (SQL probe): the morph where is appended after the key-pair
	// group.
	assert.Equal(t, `delete from "role_user" where ("foreign" = ? and "other" = ?) and "taggable_type" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, "fv", statements[0].args[0])
	assert.EqualValues(t, "ov", statements[0].args[1])
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[2])

	assert.False(t, pivot.Exists())
}

func TestMorphPivot_DeleteOwnKeyUsesMorphWhere(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(9)}, true))

	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `delete from "role_user" where "taggable_type" = ? and
	// "taggable_type" = ? and "id" = ?` — Model::delete's path through the morph keys.
	assert.Equal(t, `delete from "role_user" where "taggable_type" = ? and "taggable_type" = ? and "id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[0])
	assert.EqualValues(t, morphPivotTestClass, statements[0].args[1])
	assert.EqualValues(t, 9, statements[0].args[2])
	assert.False(t, pivot.Exists())
}

func TestMorphPivot_DeleteOwnKeyNotExistingEmitsNothing(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(9)}, false))

	// Oracle v12.55.1, executed: Model::delete returns before any event or statement when the
	// model does not exist.
	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 0, rows)
	assert.Empty(t, recorder.take())
}

func TestMorphPivot_DeleteWithoutKeyNotExistingStillRuns(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "nope", "other": "nope"}, false))

	// Oracle v12.55.1, executed: the keyless override has no exists check — the statement still
	// runs and exists turns false.
	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `delete from "role_user" where ("foreign" = ? and "other" = ?) and "taggable_type" = ?`, statements[0].query)
	assert.False(t, pivot.Exists())
}

func TestMorphPivot_DeleteWithUnsetMorphClassRendersNullCheck(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetMorphType("taggable_type")

	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: an unset morphClass reads as null —
	// `... and "taggable_type" is null` with bindings ["fv","ov"].
	assert.Equal(t, `delete from "role_user" where ("foreign" = ? and "other" = ?) and "taggable_type" is null`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, "fv", statements[0].args[0])
	assert.EqualValues(t, "ov", statements[0].args[1])
}

// --- restoration -----------------------------------------------------------------------------------

func TestMorphPivot_Restoration(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{}, true))

	var dest []map[string]any

	// Oracle v12.55.1, executed (state probe): the six-segment id explodes into three pair wheres
	// in segment order — the morph where LAST on this path.
	query, err := pivot.NewQueryForRestoration("foreign:f1:other:o1:taggable_type:A")
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "foreign" = ? and "other" = ? and "taggable_type" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, "f1", statements[0].args[0])
	assert.EqualValues(t, "o1", statements[0].args[1])
	assert.EqualValues(t, "A", statements[0].args[2])

	// Oracle: the collection form joins one three-where group per id with orWhere, the first
	// group included.
	query, err = pivot.NewQueryForRestoration([]any{"foreign:f1:other:o1:taggable_type:A", "foreign:f2:other:o2:taggable_type:B"})
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where ("foreign" = ? and "other" = ? and "taggable_type" = ?) or ("foreign" = ? and "other" = ? and "taggable_type" = ?)`, statements[0].query)
	require.Len(t, statements[0].args, 6)
	assert.EqualValues(t, "f1", statements[0].args[0])
	assert.EqualValues(t, "o1", statements[0].args[1])
	assert.EqualValues(t, "A", statements[0].args[2])
	assert.EqualValues(t, "f2", statements[0].args[3])
	assert.EqualValues(t, "o2", statements[0].args[4])
	assert.EqualValues(t, "B", statements[0].args[5])

	// Oracle: segments past the sixth are ignored.
	query, err = pivot.NewQueryForRestoration("foreign:f1:other:o1:taggable_type:A:EXTRA")
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "foreign" = ? and "other" = ? and "taggable_type" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, "A", statements[0].args[2])

	// Oracle: a scalar id without ":" falls to the base path — Model::newQueryForRestoration's
	// whereKey over the pivot's qualified key.
	query, err = pivot.NewQueryForRestoration(7)
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "role_user"."id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 1)
	assert.EqualValues(t, 7, statements[0].args[0])

	// Divergence (upstream v12.55.1, executed — state probe: FATAL infinite recursion on this
	// input, MorphPivot.php:162): the Go port completes the intended Model whereKey list instead
	// of reproducing the stack overflow.
	query, err = pivot.NewQueryForRestoration([]any{7, 8})
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "role_user"."id" in (?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, 7, statements[0].args[0])
	assert.EqualValues(t, 8, statements[0].args[1])
}

func TestMorphPivot_RestorationInvalidID(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	pivot := withMorphKeys(newMorphPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{}, true))

	// PHP warns on the missing offsets and silently drops the null segments (oracle, executed —
	// /tmp/morphpivotprobes/short_id_probe.php: 'foreign:f1:other:o1' fires "Undefined array key
	// 4/5" and only the two pair wheres compile). Go fails loudly, the base port's policy.
	_, err := pivot.NewQueryForRestoration("foreign:f1:other:o1")
	assert.ErrorIs(t, err, frameworkerrors.OrmMorphPivotRestorationIDInvalid)

	_, err = pivot.NewQueryForRestoration([]any{"foreign:f1:other:o1:taggable_type:A", "foreign:f2"})
	assert.ErrorIs(t, err, frameworkerrors.OrmMorphPivotRestorationIDInvalid)
}

// --- events ----------------------------------------------------------------------------------------

func TestMorphPivot_Events(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	observer := &pivotTestObserver{}
	builder.Observe(&relations.MorphPivot{}, observer)

	// Oracle v12.55.1, executed (/tmp/morphpivotprobes/events_probe.php): listeners fire under
	// the instance class only — a MorphPivot save logs ["morph.saving"] with a Pivot-registered
	// listener silent, and a custom subclass logs ["evprobe.saving"] alone. This ORM's observer
	// matcher is exact-type too, so a Pivot-registered observer must stay silent for a morph
	// pivot's events.
	baseObserver := &pivotTestObserver{}
	builder.Observe(&relations.Pivot{}, baseObserver)
	parent := &pivotTestParent{ID: 1}

	// Model::save: saving -> updating -> updated -> saved on the update path.
	pivot := withMorphKeys(newMorphPivot(t, builder, parent, map[string]any{"foreign": "fv", "other": "ov"}, true))
	pivot.SetAttribute("note", "changed")
	require.NoError(t, pivot.Save())
	assert.Equal(t, []string{"saving", "updating", "updated", "saved"}, observer.events)

	// Model::save: saving -> creating -> created -> saved on the insert path.
	observer.events = nil
	fresh := withMorphKeys(newMorphPivot(t, builder, parent, map[string]any{"foreign": "nf"}, false))
	require.NoError(t, fresh.Save())
	assert.Equal(t, []string{"saving", "creating", "created", "saved"}, observer.events)

	// MorphPivot::delete: deleting -> deleted (no touch step on the keyless path).
	observer.events = nil
	_, err := pivot.Delete()
	require.NoError(t, err)
	assert.Equal(t, []string{"deleting", "deleted"}, observer.events)

	// The base-Pivot observer stayed silent throughout: morph events dispatch under the concrete
	// type only (probe above).
	assert.Empty(t, baseObserver.events)
}
