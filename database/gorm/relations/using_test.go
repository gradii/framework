package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	relations "github.com/gradii/framework/database/gorm/relations"
	querybuilder "github.com/gradii/framework/database/query"
)

// This file drives the pivot-construction surface — Using / NewPivot / NewExistingPivot (item
// decide-light-using) — through a real *gorm.OrmBuilder on the recording transport, over the
// many-to-many fixtures in many2many_test.go. Every behavior was pinned by the oracle (php
// .claude/laravel-oracle/oracle.php /tmp/decide-light-using/using_probe.php, illuminate/database
// v12.55.1, sqlite; output /tmp/decide-light-using/using_probe.out):
//
//   - U2a/U2b: the default newPivot / newExistingPivot are Pivot instances carrying the
//     relation's table, the stamped key pair, the parent link, the related model and the
//     fromAttributes clean dirty state;
//   - U2e: the morph kinds default to MorphPivot with the morph type/value merged into the
//     attributes (caller attributes win) and the morph stamps in place;
//   - U2c/U2f: a Using factory replaces the class for both the plain and the morph relations;
//   - U3a: using() does NOT change the relation read's statement;
//   - C21: base newPivot merges the withPivotValue-recorded pairs (recorded values first, the
//     caller's attributes winning, the last recorded pair per column winning — IWPT:629), while
//     the morph override keeps ignoring them (the extension probe /tmp/reverify429/pv_probe.php).

// usingTestPivot is the foreign factory result — a custom pivot shape outside relations.
type usingTestPivot struct {
	attributes map[string]any
	table      string
	exists     bool
}

func (u *usingTestPivot) PivotKeys() []string        { return []string{"custom_fk", "custom_rk"} }
func (u *usingTestPivot) Attributes() map[string]any { return u.attributes }
func (u *usingTestPivot) GetTable() string           { return u.table }
func (u *usingTestPivot) Exists() bool               { return u.exists }

func TestMany2Many_NewPivotDefaults(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	relation := mmRolesRelation(t, builder, &mmUser{ID: 1})

	pivot, err := relation.NewPivot(map[string]any{"level": "gold"}, false)
	require.NoError(t, err)

	typed, ok := pivot.(*relations.Pivot)
	require.True(t, ok)
	assert.Equal(t, "mm_user_roles", typed.GetTable())
	assert.Equal(t, []string{"user_id", "role_id"}, typed.PivotKeys())
	assert.Equal(t, map[string]any{"level": "gold"}, typed.Attributes())
	assert.False(t, typed.Exists())
	assert.False(t, typed.IsDirty()) // oracle U2a dirty []
	assert.Same(t, relation.GetParent(), typed.GetPivotParent())
	assert.Same(t, relation.GetRelated(), typed.GetPivotRelated())

	existing, err := relation.NewExistingPivot(map[string]any{"level": "gold"})
	require.NoError(t, err)
	assert.True(t, existing.(*relations.Pivot).Exists()) // oracle U2b exists true

	factoryPivot := &usingTestPivot{table: "mm_user_roles"}
	var capturedTable string
	var capturedExists bool
	viaFactory, err := relation.Using(func(parent any, attributes map[string]any, table string, exists bool) (contractsorm.PivotModel, error) {
		capturedTable, capturedExists = table, exists

		return factoryPivot, nil
	}).NewPivot(map[string]any{"level": "gold"}, false)
	require.NoError(t, err)
	assert.Same(t, factoryPivot, viaFactory) // oracle U2c: the custom class replaces Pivot
	assert.Equal(t, "mm_user_roles", capturedTable)
	assert.False(t, capturedExists)
}

func TestMany2Many_MorphNewPivotDefaults(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	relation := mmTagsRelation(t, builder, &mmPost{ID: 1})

	pivot, err := relation.NewPivot(map[string]any{"type": "t"}, false)
	require.NoError(t, err)

	typed, ok := pivot.(*relations.MorphPivot)
	require.True(t, ok)
	assert.Equal(t, "taggable_type", typed.MorphType())
	assert.Equal(t, map[string]any{"type": "t", "taggable_type": "mm_post"}, typed.Attributes()) // oracle U2e
	assert.Equal(t, []string{"taggable_type", "taggable_id", "tag_id"}, typed.PivotKeys())
	assert.False(t, typed.Exists())
	assert.False(t, typed.IsDirty())
	assert.Same(t, relation.GetRelated(), typed.GetPivotRelated())

	// The pair override through the contract: NewExistingPivot must run the morph construction
	// (a promoted base method would miss the merge — Go has no virtual dispatch).
	existing, err := relation.NewExistingPivot(map[string]any{"tag_id": 2})
	require.NoError(t, err)
	existingTyped := existing.(*relations.MorphPivot)
	assert.True(t, existingTyped.Exists())
	assert.Equal(t, "taggable_type", existingTyped.MorphType())
	assert.Equal(t, map[string]any{"tag_id": 2, "taggable_type": "mm_post"}, existingTyped.Attributes())
}

func TestMany2Many_UsingKeepsTheReadFrame(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	relation := mmTagsRelation(t, builder, &mmPost{ID: 1}).Using(func(any, map[string]any, string, bool) (contractsorm.PivotModel, error) {
		return nil, nil
	})

	// Oracle U3a: the read statement is identical with and without using(); the factory is a
	// construction-time seam, never part of the read.
	tags := []mmTag{}
	statement := mmReadStatement(t, recorder, relation, &tags)
	assert.Equal(t, mmMorphReadBase, statement.query)
	require.Len(t, statement.args, 2)
	assert.EqualValues(t, 1, deref(statement.args[0]))
	assert.Equal(t, "mm_post", statement.args[1])
}

// TestMany2Many_NewPivotMergesRecordedPivotValues pins the withPivotValue half of the pivot
// construction on the real relation object: base newPivot merges the recorded "pivot column =
// value" pairs into the attributes FIRST — IWPT:629 `array_merge(array_column($this->pivotValues,
// 'value', 'column'), $attributes)` — recorded values as defaults and caller attributes winning.
// Oracle (v12.55.1, executed, /tmp/reverify429/pv_probe.php — the decide-light-using probe
// extended for this item): `C21 base attrs => ['level' => 'pv']`, `C21 base dirty => []`,
// `C21 caller wins => ['level' => 'caller']`, `C21 caller extra => ['level' => 'pv', 'other' =>
// 'x']`, `C21 duplicate last wins => ['level' => 'b']`, `C21 base existing attrs => ['level' =>
// 'pv']`, `C21 using-class dirty => ['level' => 'pv']`.
func TestMany2Many_NewPivotMergesRecordedPivotValues(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	relation := mmRolesRelation(t, builder, &mmUser{ID: 1}).WithPivotValue("level", "pv")

	pivot, err := relation.NewPivot(nil, false)
	require.NoError(t, err)
	typed := pivot.(*relations.Pivot)
	assert.Equal(t, map[string]any{"level": "pv"}, typed.Attributes())
	assert.False(t, typed.IsDirty())

	overridden, err := relation.NewPivot(map[string]any{"level": "caller", "other": "x"}, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"level": "caller", "other": "x"}, overridden.(*relations.Pivot).Attributes())

	duplicated := mmRolesRelation(t, builder, &mmUser{ID: 1}).
		WithPivotValue("level", "a").
		WithPivotValue("level", "b")
	dup, err := duplicated.NewPivot(nil, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"level": "b"}, dup.(*relations.Pivot).Attributes())

	existing, err := relation.NewExistingPivot(nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"level": "pv"}, existing.(*relations.Pivot).Attributes())
	assert.True(t, existing.(*relations.Pivot).Exists())

	// The Using factory receives the merged bag — Laravel merges before the Model::newPivot
	// dispatch — and fromRawAttributes keeps it dirty while !exists (`C21 using-class dirty`).
	var captured map[string]any
	viaFactory, err := relation.Using(func(parent any, attributes map[string]any, table string, exists bool) (contractsorm.PivotModel, error) {
		captured = attributes

		return relations.FromRawAttributes(nil, parent, attributes, table, exists)
	}).NewPivot(nil, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"level": "pv"}, captured)
	assert.Equal(t, map[string]any{"level": "pv"}, viaFactory.(*relations.Pivot).GetDirty())

	// The frozen nil-value swap records the pair the clause applied — the parent pivot key paired
	// with the argument column.
	swapped, err := mmRolesRelation(t, builder, &mmUser{ID: 7}).WithPivotValue("77", nil).NewPivot(nil, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"user_id": "77"}, swapped.(*relations.Pivot).Attributes())

	// A non-string recorded column (a database/query Expression) cannot become an attribute key:
	// PHP's array_column raises TypeError at the merge (oracle: `C21 expression attrs threw =>
	// [TypeError, "Cannot access offset of type Illuminate\Database\Query\Expression on
	// array"]`), so the Go merge skips the entry and the construction keeps working.
	unmergeable, err := mmRolesRelation(t, builder, &mmUser{ID: 1}).
		WithPivotValue(querybuilder.Expression{Value: "CONCAT(foo, '_', bar)"}, "pv").
		NewPivot(nil, false)
	require.NoError(t, err)
	assert.Empty(t, unmergeable.(*relations.Pivot).Attributes())

	// The recorded list is per relation object — a fresh relation merges nothing.
	fresh, err := mmRolesRelation(t, builder, &mmUser{ID: 1}).NewPivot(nil, false)
	require.NoError(t, err)
	assert.Empty(t, fresh.(*relations.Pivot).Attributes())
}

// TestMany2Many_MorphNewPivotIgnoresRecordedPivotValues pins the other half of the same oracle:
// the MorphToMany::newPivot override merges only the morph type/value pair
// (`array_merge([$this->morphType => $this->morphClass], $attributes)`, MorphToMany.php:160) and
// never the recorded withPivotValue pairs — `C21 morph attrs => ['taggable_type' => 'PvPost']`,
// `C21 morph dirty => []`, `C21 morph caller attrs => ['taggable_type' => 'PvPost', 'level' =>
// 'caller']`, `C21 morph existing attrs => ['taggable_type' => 'PvPost']`.
func TestMany2Many_MorphNewPivotIgnoresRecordedPivotValues(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	relation := mmTagsRelation(t, builder, &mmPost{ID: 1})
	relation.WithPivotValue("level", "pv") // promoted call: the recording lands on the shared base object

	pivot, err := relation.NewPivot(nil, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"taggable_type": "mm_post"}, pivot.(*relations.MorphPivot).Attributes())
	assert.False(t, pivot.(*relations.MorphPivot).IsDirty())

	existing, err := relation.NewExistingPivot(nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"taggable_type": "mm_post"}, existing.(*relations.MorphPivot).Attributes())

	overridden, err := relation.NewPivot(map[string]any{"level": "caller"}, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"taggable_type": "mm_post", "level": "caller"}, overridden.(*relations.MorphPivot).Attributes())
}
