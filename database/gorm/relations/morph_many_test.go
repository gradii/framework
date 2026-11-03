// This file drives the MorphMany relation object's morph-field accessors — the Go form of
// MorphOneOrMany::getMorphType / getQualifiedMorphType / getMorphClass — and the declared
// contracts/database/orm.MorphManyRelation kind check, through a real *gorm.OrmBuilder on the
// recording transport. Expectations measured by the oracle (php .claude/laravel-oracle/oracle.php
// /tmp/morph_object_surface_probe.php, illuminate/database v12.55.1): `$post->morphMany(...)`
// reports getMorphType() 'commentable_type', getQualifiedMorphType() 'mp_comments.commentable_type'
// and getMorphClass() the declaring class ('MpPost' plain; 'mp_post' under a morph map).
package relations_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// --- fixtures ------------------------------------------------------------------

// morphManyParent declares the MorphMany under test plus a plain HasMany sibling — the shared
// leaf the declared morph interface must reject.
type morphManyParent struct {
	ID       uint                `gorm:"column:id;primaryKey"`
	Comments []*morphManyComment `gorm:"-"`
	Plain    []*morphManyComment `gorm:"-"`
}

func (morphManyParent) TableName() string { return "morph_many_posts" }

func (morphManyParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Comments": contractsorm.MorphMany{Related: &morphManyComment{}, Name: "commentable"},
		"Plain":    contractsorm.HasMany{Related: &morphManyComment{}, ForeignKey: "commentable_id"},
	}
}

type morphManyComment struct {
	ID              uint   `gorm:"column:id;primaryKey"`
	CommentableID   uint   `gorm:"column:commentable_id"`
	CommentableType string `gorm:"column:commentable_type"`
}

func (morphManyComment) TableName() string { return "morph_many_comments" }

// --- accessors from real Related() descriptors ---------------------------------

func TestMorphMany_MorphFieldAccessorsFromRealDescriptor(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)

	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphManyParent{ID: 4}, "Comments").(contractsorm.MorphManyRelation)
	require.True(t, ok, "Related must narrow to the declared MorphManyRelation")

	// Oracle: morphMany getMorphType => 'commentable_type'; getQualifiedMorphType =>
	// 'mp_comments.commentable_type'; getMorphClass plain => 'MpPost' (the declaring table name
	// unaliased here).
	require.Equal(t, "commentable_type", relation.MorphType())
	require.Equal(t, "morph_many_comments.commentable_type", relation.QualifiedMorphType())
	require.Equal(t, "morph_many_posts", relation.MorphClass())
}

func TestMorphMany_MorphClassUsesMorphMapAlias(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)

	databaseorm.MorphMap(map[string]any{"morph_many_post": &morphManyParent{}}, false)

	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphManyParent{ID: 4}, "Comments").(contractsorm.MorphManyRelation)
	require.True(t, ok, "Related must narrow to the declared MorphManyRelation")

	// Oracle: morphMany getMorphClass under a morph map => the alias ('mp_post' in the probe).
	require.Equal(t, "morph_many_post", relation.MorphClass())
}

func TestMorphMany_DeclaredInterfaceRejectsTheSharedLeaf(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	plain := builder.Related(&morphManyParent{ID: 4}, "Plain")
	_, morph := plain.(contractsorm.MorphManyRelation)
	require.False(t, morph, "a plain HasMany object must not satisfy the declared MorphManyRelation")
	_, hasMany := plain.(contractsorm.HasManyRelation)
	require.True(t, hasMany, "the shared leaf still narrows through the non-morph alias")
}
