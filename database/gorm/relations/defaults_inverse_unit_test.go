package relations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The projection-level pins for the call-time WithDefault slot and the SupportsInverseRelations
// accessors (item lalign-defaults-inverse). Every behavior was measured by the oracle against
// illuminate/database v12.55.1 (php .claude/laravel-oracle/oracle.php
// /tmp/lalign-defaults-inverse/runtime_withdefault.php and inverse_writeback_runtime.php:
// hasOne.missing-row.attributes {"foreign_key":2}, hasOne.null-key.queries 0,
// hasOne.declared-then-runtime.name "runtime", gir.after-withoutChaperone NULL). The real-builder
// half lives in database/gorm's defaults_inverse_engine_test.go.

// defaultsHolder / defaultsRelatedModel are the minimal shapes these Wrap-level tests carry;
// nothing touches their tables (the read is stubbed through the Hooks).
type defaultsHolder struct{ ID uint64 }

type defaultsRelatedModel struct {
	ID  uint64
	Bio string
}

func TestHasOne_WithDefaultReplacesTheDeclarationSlot(t *testing.T) {
	var seen any
	hooks := Hooks{
		FirstRow: func(query contractsorm.Query, dest any) (bool, error) { return false, nil },
		BuildDefault: func(value any, parent any) (any, bool, error) {
			seen = value

			return &defaultsRelatedModel{Bio: "default"}, true, nil
		},
	}
	rel := Wrap(nil, &defaultsHolder{ID: 7}, "Profile", RelationInfo{
		Kind:        KindHasOne,
		WithDefault: map[string]any{"bio": "declared"},
	}, hooks).(contractsorm.HasOneRelation)

	// No call-time call: the declaration's value rides the single slot.
	var declared defaultsRelatedModel
	require.NoError(t, rel.GetResults(&declared))
	assert.Equal(t, map[string]any{"bio": "declared"}, seen)
	assert.Equal(t, "default", declared.Bio)

	// The call-time call replaces it — the one $withDefault slot both paths share (oracle:
	// hasOne.declared-then-runtime.name "runtime").
	rel.WithDefault(map[string]any{"bio": "runtime"})
	var runtimeValue defaultsRelatedModel
	require.NoError(t, rel.GetResults(&runtimeValue))
	assert.Equal(t, map[string]any{"bio": "runtime"}, seen)

	// false rides the slot too; its falsiness is the engine's reading (see the engine tests).
	rel.WithDefault(false)
	require.NoError(t, rel.GetResults(&defaultsRelatedModel{}))
	assert.Equal(t, false, seen)
}

// TestWithDefault_SurfaceOfTheKindLeaves pins which relation objects expose the call-time setter —
// Eloquent composes SupportsDefaultModels into HasOne (HasOne.php:22), MorphOne (MorphOne.php:22),
// BelongsTo (BelongsTo.php:36, MorphTo inherits) and HasOneThrough (HasOneThrough.php:24) — and
// that a call reaches the shared projection slot.
func TestWithDefault_SurfaceOfTheKindLeaves(t *testing.T) {
	morphOne := Wrap(nil, nil, "Image", RelationInfo{Kind: KindMorphOne}, Hooks{}).(contractsorm.MorphOneRelation)
	morphOne.WithDefault(map[string]any{"id": 1})
	assert.Equal(t, map[string]any{"id": 1}, morphOne.(*morphOneRelationQuery).info.WithDefault)

	belongs := Wrap(nil, nil, "User", RelationInfo{Kind: KindBelongsTo}, Hooks{}).(contractsorm.BelongsToRelation)
	belongs.WithDefault(true)
	assert.Equal(t, true, belongs.(*belongsToRelationQuery).info.WithDefault)

	morphTo := Wrap(nil, nil, "Commentable", RelationInfo{Kind: KindMorphTo}, Hooks{}).(contractsorm.MorphToRelation)
	morphTo.WithDefault(false)
	assert.Equal(t, false, morphTo.(*morphToRelationQuery).relationQuery.info.WithDefault)

	through := Wrap(nil, nil, "Car", RelationInfo{Kind: KindHasOneThrough}, Hooks{}).(contractsorm.HasOneThroughRelation)
	through.WithDefault(map[string]any{"name": "car"})
	assert.Equal(t, map[string]any{"name": "car"}, through.(*hasOneThroughRelationQuery).info.WithDefault)

	// The method rides the shared through base, so a HasManyThrough object carries the Go surface
	// superset — Laravel's HasManyThrough has no SupportsDefaultModels.
	many := Wrap(nil, nil, "Posts", RelationInfo{Kind: KindHasManyThrough}, Hooks{}).(contractsorm.HasManyThroughRelation)
	many.WithDefault(true)
	assert.Equal(t, true, many.(*hasManyThroughRelationQuery).info.WithDefault)
}

func TestHasOneOrMany_InverseAccessorsOverTheProjection(t *testing.T) {
	many := Wrap(nil, nil, "posts", RelationInfo{Kind: KindHasMany, Inverse: "User"}, Hooks{}).(contractsorm.HasManyRelation)
	assert.Equal(t, "User", many.GetInverseRelationship())

	// withoutChaperone returns the relation itself (Laravel's `return $this;`) and clears the
	// state; the leaf's own methods stay reachable through a re-narrowing assertion.
	assert.Same(t, many, many.WithoutChaperone())
	cleared, ok := many.WithoutChaperone().(contractsorm.HasManyRelation)
	require.True(t, ok, "the cleared object stays the same has-many leaf")
	assert.Same(t, many, cleared)
	assert.Equal(t, "", many.GetInverseRelationship())

	// withoutInverse is the alias (SupportsInverseRelations.php:141).
	alias := Wrap(nil, nil, "posts", RelationInfo{Kind: KindHasMany, Inverse: "User"}, Hooks{}).(contractsorm.HasManyRelation)
	assert.Same(t, alias, alias.WithoutInverse())
	assert.Equal(t, "", alias.GetInverseRelationship())

	// An undeclared relation reports no inverse relationship, and clearing it stays a no-op.
	plain := Wrap(nil, nil, "posts", RelationInfo{Kind: KindHasMany}, Hooks{}).(contractsorm.HasManyRelation)
	assert.Equal(t, "", plain.GetInverseRelationship())
	plain.WithoutInverse()
	assert.Equal(t, "", plain.GetInverseRelationship())

	// The morph siblings inherit the trait through the embedding chain (HasOneOrMany.php:23), and
	// their leaf-typed return keeps the morph accessors (MorphTo/MorphOne style $this).
	morph := Wrap(nil, nil, "comments", RelationInfo{Kind: KindMorphMany, Inverse: "Commentable"}, Hooks{}).(contractsorm.MorphManyRelation)
	assert.Equal(t, "Commentable", morph.GetInverseRelationship())
	assert.Same(t, morph, morph.WithoutChaperone())
	clearedMorph, ok := morph.WithoutChaperone().(contractsorm.MorphManyRelation)
	require.True(t, ok, "the cleared object stays the same morph-many leaf")
	assert.Same(t, morph, clearedMorph)
	assert.Equal(t, "", morph.GetInverseRelationship())
}
