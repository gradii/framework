// This file drives the MorphToMany / MorphedByMany relation object's morph-field accessors — the
// Go form of MorphToMany::getMorphType / getQualifiedMorphTypeName / getMorphClass / getInverse —
// and the declared contracts/database/orm.MorphToManyRelation kind check, through a real
// *gorm.OrmBuilder on the recording transport. Expectations measured by the oracle (php
// .claude/laravel-oracle/oracle.php /tmp/morph_object_surface_probe.php, illuminate/database
// v12.55.1): both forms report getMorphType() 'taggable_type' and getQualifiedMorphTypeName()
// 'taggables.taggable_type' (the default pivot table is the plural of the morph name);
// getMorphClass() is the declaring side's morph class ("MpPost" plain, "mp_post" under a morph
// map); getInverse() is false for morphToMany and true for morphedByMany.
package relations_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// --- fixtures ------------------------------------------------------------------

type morphToManyParent struct {
	ID   uint              `gorm:"column:id;primaryKey"`
	Tags []*morphToManyTag `gorm:"-"`
}

func (morphToManyParent) TableName() string { return "morph_to_many_posts" }

func (morphToManyParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{Related: &morphToManyTag{}, Name: "taggable"},
	}
}

type morphToManyTag struct {
	ID    uint                 `gorm:"column:id;primaryKey"`
	Posts []*morphToManyParent `gorm:"-"`
}

func (morphToManyTag) TableName() string { return "morph_to_many_tags" }

func (morphToManyTag) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts":      contractsorm.MorphedByMany{Related: &morphToManyParent{}, Name: "taggable"},
		"PlainPosts": contractsorm.Many2Many{Related: &morphToManyParent{}, Table: "morph_to_many_plain"},
	}
}

// --- accessors from real Related() descriptors ---------------------------------

func TestMorphToMany_MorphFieldAccessorsFromRealDescriptor(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)

	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphToManyParent{ID: 3}, "Tags").(contractsorm.MorphToManyRelation)
	require.True(t, ok, "Related must narrow to the declared MorphToManyRelation")

	// Oracle: morphToMany getMorphType => 'taggable_type'; getQualifiedMorphTypeName =>
	// 'taggables.taggable_type'; getMorphClass plain => 'MpPost' (the declaring table name
	// unaliased here); getInverse => false.
	require.Equal(t, "taggable_type", relation.MorphType())
	require.Equal(t, "taggables.taggable_type", relation.QualifiedMorphType())
	require.Equal(t, "morph_to_many_posts", relation.MorphClass())
	require.False(t, relation.Inverse())
}

func TestMorphedByMany_MorphFieldAccessorsFromRealDescriptor(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)

	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphToManyTag{ID: 8}, "Posts").(contractsorm.MorphToManyRelation)
	require.True(t, ok, "Related must narrow to the declared MorphToManyRelation")

	// Oracle: morphedByMany getMorphType => 'taggable_type'; getQualifiedMorphTypeName =>
	// 'taggables.taggable_type'; getMorphClass => the RELATED side's morph class ('MpPost');
	// getInverse => true.
	require.Equal(t, "taggable_type", relation.MorphType())
	require.Equal(t, "taggables.taggable_type", relation.QualifiedMorphType())
	require.Equal(t, "morph_to_many_posts", relation.MorphClass())
	require.True(t, relation.Inverse())
}

func TestMorphToMany_MorphClassUsesMorphMapAlias(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)

	databaseorm.MorphMap(map[string]any{"morph_to_many_post": &morphToManyParent{}}, false)

	builder, _ := newRelationTestBuilder(t)

	// Oracle: under a morph map both forms report the alias ('mp_post' in the probe) — the
	// forward form from the parent, the inverse form from the related side.
	forward, ok := builder.Related(&morphToManyParent{ID: 3}, "Tags").(contractsorm.MorphToManyRelation)
	require.True(t, ok)
	require.Equal(t, "morph_to_many_post", forward.MorphClass())

	inverse, ok := builder.Related(&morphToManyTag{ID: 8}, "Posts").(contractsorm.MorphToManyRelation)
	require.True(t, ok)
	require.Equal(t, "morph_to_many_post", inverse.MorphClass())
}

func TestMorphToMany_DeclaredInterfaceRejectsTheSharedLeaf(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	plain := builder.Related(&morphToManyTag{ID: 8}, "PlainPosts")
	_, morph := plain.(contractsorm.MorphToManyRelation)
	require.False(t, morph, "a plain Many2Many object must not satisfy the declared MorphToManyRelation")
	_, belongsToMany := plain.(contractsorm.BelongsToManyRelation)
	require.True(t, belongsToMany, "the shared leaf still narrows through the non-morph alias")
}

// TestMorphToMany_LimitAndTakeKeepTheMorphLeaf pins the leaf's Take/Limit overrides: PHP's
// `return $this` keeps the MorphToMany instance in the chain, where the promoted base methods would
// return the embedded BelongsToMany object. Only *morphToManyRelationQuery satisfies the declared
// MorphToManyRelation contract, so a chain the override did not keep fails the assertion. The
// round-8 verify flagged the pair as unpinned (an overlay deleting both methods left the suite
// green).
func TestMorphToMany_LimitAndTakeKeepTheMorphLeaf(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	relation := builder.Related(&morphToManyParent{ID: 3}, "Tags")
	_, ok := relation.(contractsorm.MorphToManyRelation)
	require.True(t, ok, "Related must return a MorphToMany relation object")

	_, ok = relation.Limit(2).(contractsorm.MorphToManyRelation)
	require.True(t, ok, "Limit must keep the MorphToMany leaf in the chain")

	_, ok = relation.Take(2).(contractsorm.MorphToManyRelation)
	require.True(t, ok, "Take must keep the MorphToMany leaf in the chain")
}
