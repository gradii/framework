package relations

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// makeCaptureBuilder records the MakeRelation calls the relation object's Make forwards — the fake
// stands in for *gorm.OrmBuilder, so only MakeRelation is ever reached; the embedded Builder
// interface supplies the rest of the surface.
type makeCaptureBuilder struct {
	Builder

	calls []makeCaptureCall
}

type makeCaptureCall struct {
	parent   any
	relation string
	attrs    map[string]any
	dest     any
}

func (b *makeCaptureBuilder) MakeRelation(parent any, relation string, attrs map[string]any, dest any) error {
	b.calls = append(b.calls, makeCaptureCall{parent: parent, relation: relation, attrs: attrs, dest: dest})

	return nil
}

// Make merges the builder's pending attributes underneath the caller's attributes — the caller's
// value wins — before forwarding, Laravel's HasOneOrMany::make through setForeignAttributesForCreate.
func TestHasOneOrManyRelation_Make_MergesPendingUnderCaller(t *testing.T) {
	builder := &makeCaptureBuilder{}
	parent := struct{ ID uint64 }{ID: 123}
	dest := struct{}{}

	pending := map[string]any{"a key": "a value", "b": "pending"}
	relation := Wrap(builder, &parent, "Children", RelationInfo{Kind: KindHasMany}, Hooks{
		PendingAttributes: func() map[string]any { return pending },
	}).(contractsorm.HasManyRelation)

	require.NoError(t, relation.Make(&dest, map[string]any{"a key": "the value"}))

	require.Len(t, builder.calls, 1)
	call := builder.calls[0]
	require.Equal(t, &parent, call.parent)
	require.Equal(t, "Children", call.relation)
	require.Equal(t, &dest, call.dest)
	require.Equal(t, map[string]any{"a key": "the value", "b": "pending"}, call.attrs)

	// The hook's map is left untouched — the merge copies.
	require.Equal(t, map[string]any{"a key": "a value", "b": "pending"}, pending)
}

// Without a pending hook (or with an empty pending map) the caller's attributes pass through
// untouched — including the nil map of a Make without arguments.
func TestHasOneOrManyRelation_Make_WithoutPendingPassesAttributesThrough(t *testing.T) {
	builder := &makeCaptureBuilder{}
	parent := struct{}{}
	dest := struct{}{}

	relation := Wrap(builder, &parent, "Children", RelationInfo{Kind: KindHasOne}, Hooks{}).(contractsorm.HasOneRelation)

	caller := map[string]any{"a key": "the value"}
	require.NoError(t, relation.Make(&dest, caller))
	require.NoError(t, relation.Make(&dest))

	require.Len(t, builder.calls, 2)
	require.Equal(t, caller, builder.calls[0].attrs)
	require.Nil(t, builder.calls[1].attrs)
}

// One() keeps the builder and the pending hook: the narrowed single leaf forwards to the very
// same builder and Make still merges the pending map — Laravel's HasMany::one() reusing the
// relation's query (HasMany.php:20), for HasMany and MorphMany alike.
func TestHasManyRelation_One_MakeSharesTheBuilderAndPending(t *testing.T) {
	builder := &makeCaptureBuilder{}
	parent := struct{}{}
	dest := struct{}{}

	many := Wrap(builder, &parent, "Children", RelationInfo{Kind: KindHasMany}, Hooks{
		PendingAttributes: func() map[string]any { return map[string]any{"a key": "a value"} },
	}).(contractsorm.HasManyRelation)

	one := many.One()
	require.NoError(t, one.Make(&dest, map[string]any{"b": "caller"}))

	require.Len(t, builder.calls, 1)
	require.Equal(t, &parent, builder.calls[0].parent)
	require.Equal(t, "Children", builder.calls[0].relation)
	require.Equal(t, map[string]any{"a key": "a value", "b": "caller"}, builder.calls[0].attrs)

	leaf, ok := one.(*hasOneRelationQuery)
	require.True(t, ok, "One must return the single-result leaf")
	require.Same(t, builder, leaf.Builder, "the narrowed leaf must reuse the same builder")

	// MorphMany narrows through its own leaf, not the hasMany leaf — morph_many.go:22, the Go
	// analogue of MorphMany::one() (vendor v12.55.1, MorphMany.php:20).
	morphMany := Wrap(builder, &parent, "Relatables", RelationInfo{Kind: KindMorphMany}, Hooks{
		PendingAttributes: func() map[string]any { return map[string]any{"c": "pending"} },
	}).(contractsorm.MorphManyRelation)
	require.IsType(t, (*morphManyRelationQuery)(nil), morphMany)

	morphOne := morphMany.One()
	require.NoError(t, morphOne.Make(&dest))

	require.Len(t, builder.calls, 2)
	require.Equal(t, map[string]any{"c": "pending"}, builder.calls[1].attrs)

	morphLeaf, ok := morphOne.(*hasOneRelationQuery)
	require.True(t, ok, "MorphMany One must return the single-result leaf")
	require.Equal(t, KindMorphOne, morphLeaf.info.Kind)
	require.Same(t, builder, morphLeaf.Builder, "the narrowed morph leaf must reuse the same builder")
}
