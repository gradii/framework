package relations

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The pivot-construction surface's projection-level pins (item decide-light-using): Using /
// NewPivot / NewExistingPivot on a bare relation object — the default constructors (this
// package's Pivot / MorphPivot, Laravel's class defaults), the factory dispatch (the Go stand-in
// for using(SomePivot::class)), the relation's post-construction stamping, and the morph
// override's attribute merge. Every value was measured by the oracle (php
// .claude/laravel-oracle/oracle.php /tmp/decide-light-using/using_probe.php, illuminate/database
// v12.55.1, sqlite; output /tmp/decide-light-using/using_probe.out); the real-descriptor half
// lives in using_test.go.

type usingUnitUser struct{ ID uint }
type usingUnitRole struct{ ID uint }
type usingUnitTag struct{ ID uint }

// usingUnitPivot is a factory result outside this package's own types: it satisfies PivotModel
// and must come back untouched — no post-stamping is possible through the interface.
type usingUnitPivot struct {
	attributes map[string]any
	table      string
	exists     bool
}

func (u *usingUnitPivot) PivotKeys() []string        { return []string{"user_id", "role_id"} }
func (u *usingUnitPivot) Attributes() map[string]any { return u.attributes }
func (u *usingUnitPivot) GetTable() string           { return u.table }
func (u *usingUnitPivot) Exists() bool               { return u.exists }

// usingUnitCapture records the arguments a factory received.
type usingUnitCapture struct {
	parent     any
	attributes map[string]any
	table      string
	exists     bool
	calls      int
}

func (c *usingUnitCapture) capture(parent any, attributes map[string]any, table string, exists bool) {
	c.parent, c.attributes, c.table, c.exists = parent, attributes, table, exists
	c.calls++
}

func usingUnitMany2ManyRelation(parent any) contractsorm.BelongsToManyRelation {
	return Wrap(nil, parent, "Roles", RelationInfo{
		Kind:            KindMany2Many,
		PivotTable:      "role_user",
		PivotParentRef:  RefKey{ForeignColumn: "user_id"},
		PivotRelatedRef: RefKey{ForeignColumn: "role_id"},
		RelatedModel:    &usingUnitRole{},
	}, Hooks{}).(contractsorm.BelongsToManyRelation)
}

func usingUnitMorphRelation(parent any) contractsorm.MorphToManyRelation {
	return Wrap(nil, parent, "Tags", RelationInfo{
		Kind:            KindMorphToMany,
		PivotTable:      "taggables",
		MorphTypeColumn: "taggable_type",
		MorphValue:      "unit_post",
		PivotParentRef:  RefKey{ForeignColumn: "taggable_id"},
		PivotRelatedRef: RefKey{ForeignColumn: "tag_id"},
		RelatedModel:    &usingUnitTag{},
	}, Hooks{}).(contractsorm.MorphToManyRelation)
}

func TestUsing_DefaultConstruction(t *testing.T) {
	parent := &usingUnitUser{ID: 7}
	rel := usingUnitMany2ManyRelation(parent)

	// Oracle U2a: the default newPivot is a Pivot with the given attributes, exists false, dirty
	// [] (fromAttributes syncs the original bag), the relation's table, the stamped key pair, the
	// parent link and the related model; U2b: newExistingPivot is the same with exists true.
	pivot, err := rel.NewPivot(map[string]any{"level": "gold"}, false)
	require.NoError(t, err)

	typed, ok := pivot.(*Pivot)
	require.True(t, ok, "the class default must construct relations.Pivot")
	assert.Equal(t, "role_user", typed.GetTable())
	assert.Equal(t, []string{"user_id", "role_id"}, typed.PivotKeys())
	assert.Equal(t, map[string]any{"level": "gold"}, typed.Attributes())
	assert.False(t, typed.Exists())
	assert.False(t, typed.IsDirty())
	assert.Same(t, parent, typed.GetPivotParent())
	assert.Same(t, rel.GetRelated(), typed.GetPivotRelated())

	existing, err := rel.NewExistingPivot(map[string]any{"level": "gold"})
	require.NoError(t, err)
	typedExisting := existing.(*Pivot)
	assert.True(t, typedExisting.Exists())
	assert.False(t, typedExisting.IsDirty())
}

func TestUsing_FactoryDispatchAndPassThrough(t *testing.T) {
	parent := &usingUnitUser{ID: 7}
	rel := usingUnitMany2ManyRelation(parent)

	capture := &usingUnitCapture{}
	foreign := &usingUnitPivot{table: "custom_table"}
	factory := func(p any, attributes map[string]any, table string, exists bool) (contractsorm.PivotModel, error) {
		capture.capture(p, attributes, table, exists)

		return foreign, nil
	}

	// Oracle U1b: using() returns the relation itself; a nil factory clears back to the default.
	assert.Same(t, rel, rel.Using(factory))

	pivot, err := rel.NewPivot(map[string]any{"level": "gold"}, false)
	require.NoError(t, err)

	// The factory received (parent, attributes, table, exists) — the arguments Model::newPivot
	// hands a custom class (Model.php:1726) — and its foreign result is returned untouched.
	assert.Equal(t, 1, capture.calls)
	assert.Same(t, parent, capture.parent)
	assert.Equal(t, map[string]any{"level": "gold"}, capture.attributes)
	assert.Equal(t, "role_user", capture.table)
	assert.False(t, capture.exists)
	assert.Same(t, foreign, pivot)

	_, err = rel.NewExistingPivot(map[string]any{"level": "silver"})
	require.NoError(t, err)
	assert.Equal(t, 2, capture.calls)
	assert.True(t, capture.exists)

	defaultPivot, err := rel.Using(nil).NewPivot(map[string]any{}, false)
	require.NoError(t, err)
	_, ok := defaultPivot.(*Pivot)
	assert.True(t, ok, "a nil factory restores the class default")
	assert.Equal(t, 2, capture.calls)
}

func TestUsing_FactoryStampsPortOwnPivots(t *testing.T) {
	rel := usingUnitMany2ManyRelation(&usingUnitUser{ID: 7})

	// A factory returning this package's own *Pivot gets the relation's post-construction
	// stamping (setPivotKeys -> setRelatedModel, IWPT:640-642); fromRawAttributes semantics keep
	// the attributes dirty while !exists (oracle U2c), and sync while exists (oracle U2d).
	factory := func(p any, attributes map[string]any, table string, exists bool) (contractsorm.PivotModel, error) {
		return FromRawAttributes(nil, p, attributes, table, exists)
	}

	pivot, err := rel.Using(factory).NewPivot(map[string]any{"level": "gold"}, false)
	require.NoError(t, err)

	typed := pivot.(*Pivot)
	assert.Equal(t, []string{"user_id", "role_id"}, typed.PivotKeys())
	assert.Same(t, rel.GetRelated(), typed.GetPivotRelated())
	assert.True(t, typed.IsDirty())
	assert.Equal(t, map[string]any{"level": "gold"}, typed.GetDirty())

	existing, err := rel.NewExistingPivot(map[string]any{"level": "gold"})
	require.NoError(t, err)
	assert.False(t, existing.(*Pivot).IsDirty())
}

func TestUsing_FactoryErrorPropagates(t *testing.T) {
	rel := usingUnitMany2ManyRelation(&usingUnitUser{ID: 1})
	sentinel := errors.New("pivot factory failed")
	rel = rel.Using(func(any, map[string]any, string, bool) (contractsorm.PivotModel, error) {
		return nil, sentinel
	})

	pivot, err := rel.NewPivot(nil, false)
	assert.Nil(t, pivot)
	assert.ErrorIs(t, err, sentinel)
}

func TestUsing_MorphConstructionOverride(t *testing.T) {
	rel := usingUnitMorphRelation(&usingUnitUser{ID: 7})

	// Oracle U2e: the morph default is MorphPivot — the morph type/value pair merges into the
	// attributes first (MorphToMany::newPivot), the caller's attributes win, and the pivot
	// carries the keys, the related model and the morph pair (dirty [] — fromAttributes
	// semantics).
	pivot, err := rel.NewPivot(map[string]any{"taggable_id": 1, "tag_id": 2}, false)
	require.NoError(t, err)

	typed, ok := pivot.(*MorphPivot)
	require.True(t, ok, "the morph class default must construct relations.MorphPivot")
	assert.Equal(t, "taggable_type", typed.MorphType())
	assert.Equal(t, map[string]any{"taggable_id": 1, "tag_id": 2, "taggable_type": "unit_post"}, typed.Attributes())
	assert.Equal(t, []string{"taggable_type", "taggable_id", "tag_id"}, typed.PivotKeys())
	assert.Equal(t, "taggable_id:1:tag_id:2:taggable_type:unit_post", typed.GetQueueableID())
	assert.False(t, typed.IsDirty())
	assert.Same(t, rel.GetRelated(), typed.GetPivotRelated())

	// The caller's attribute wins the merge — Laravel's
	// array_merge([$this->morphType => $this->morphClass], $attributes).
	overridden, err := rel.NewPivot(map[string]any{"taggable_type": "unit_override"}, false)
	require.NoError(t, err)
	assert.Equal(t, "unit_override", overridden.(*MorphPivot).Attributes()["taggable_type"])

	// The pair override: NewExistingPivot reaches the same morph construction (Go embedding has
	// no virtual dispatch, so the pair is declared together) — exists true, morph stamps kept.
	existing, err := rel.NewExistingPivot(nil)
	require.NoError(t, err)
	existingTyped := existing.(*MorphPivot)
	assert.True(t, existingTyped.Exists())
	assert.Equal(t, "taggable_type", existingTyped.MorphType())
	assert.Equal(t, []string{"taggable_type", "taggable_id", "tag_id"}, existingTyped.PivotKeys())

	// Oracle U2f: a Using factory receives the MERGED attributes; a *MorphPivot result is stamped
	// (keys, related model, morph pair), a foreign result passes through.
	capture := &usingUnitCapture{}
	factoryPivot := NewMorphPivot(nil)
	factory := func(p any, attributes map[string]any, table string, exists bool) (contractsorm.PivotModel, error) {
		capture.capture(p, attributes, table, exists)

		return factoryPivot, nil
	}

	viaFactory, err := rel.Using(factory).NewPivot(map[string]any{"tag_id": 9}, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"tag_id": 9, "taggable_type": "unit_post"}, capture.attributes)
	assert.Equal(t, "taggables", capture.table)
	assert.Same(t, factoryPivot, viaFactory)
	assert.Equal(t, []string{"taggable_type", "taggable_id", "tag_id"}, viaFactory.(*MorphPivot).PivotKeys())
	assert.Equal(t, "taggable_id::tag_id::taggable_type:unit_post", viaFactory.(*MorphPivot).GetQueueableID())

	foreign := &usingUnitPivot{table: "custom_table"}
	foreignResult, err := rel.Using(func(any, map[string]any, string, bool) (contractsorm.PivotModel, error) {
		return foreign, nil
	}).NewPivot(map[string]any{"tag_id": 9}, false)
	require.NoError(t, err)
	assert.Same(t, foreign, foreignResult)
}
