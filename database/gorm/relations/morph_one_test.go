// This file drives the MorphOne relation object's morph-field accessors — the Go form of
// MorphOneOrMany::getMorphType / getQualifiedMorphType / getMorphClass — and the declared
// contracts/database/orm.MorphOneRelation kind check, through a real *gorm.OrmBuilder on the
// recording transport. Expectations measured by the oracle (php .claude/laravel-oracle/oracle.php
// /tmp/morph_object_surface_probe.php, illuminate/database v12.55.1): `$post->morphOne(...)`
// reports getMorphType() 'imageable_type', getQualifiedMorphType() 'mp_images.imageable_type' and
// getMorphClass() the declaring class ('MpPost' plain; 'mp_post' under a morph map).
package relations_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// --- fixtures ------------------------------------------------------------------

// morphOneParent declares the MorphOne under test plus a plain HasOne sibling — the shared leaf
// the declared morph interface must reject.
type morphOneParent struct {
	ID      uint           `gorm:"column:id;primaryKey"`
	Name    string         `gorm:"column:name"`
	Image   *morphOneImage `gorm:"-"`
	Profile *morphOneImage `gorm:"-"`
}

func (morphOneParent) TableName() string { return "morph_one_posts" }

func (morphOneParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Image":   contractsorm.MorphOne{Related: &morphOneImage{}, Name: "imageable"},
		"Profile": contractsorm.HasOne{Related: &morphOneImage{}, ForeignKey: "imageable_id"},
	}
}

type morphOneImage struct {
	ID            uint   `gorm:"column:id;primaryKey"`
	ImageableID   uint   `gorm:"column:imageable_id"`
	ImageableType string `gorm:"column:imageable_type"`
}

func (morphOneImage) TableName() string { return "morph_one_images" }

// --- accessors from real Related() descriptors ---------------------------------

func TestMorphOne_MorphFieldAccessorsFromRealDescriptor(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)

	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphOneParent{ID: 9}, "Image").(contractsorm.MorphOneRelation)
	require.True(t, ok, "Related must narrow to the declared MorphOneRelation")

	// Oracle: morphOne getMorphType => 'imageable_type'; getQualifiedMorphType =>
	// 'mp_images.imageable_type'; getMorphClass plain => 'MpPost' (the Go convention reports the
	// declaring table name unaliased).
	require.Equal(t, "imageable_type", relation.MorphType())
	require.Equal(t, "morph_one_images.imageable_type", relation.QualifiedMorphType())
	require.Equal(t, "morph_one_posts", relation.MorphClass())
}

func TestMorphOne_MorphClassUsesMorphMapAlias(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)

	databaseorm.MorphMap(map[string]any{"morph_one_post": &morphOneParent{}}, false)

	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphOneParent{ID: 9}, "Image").(contractsorm.MorphOneRelation)
	require.True(t, ok, "Related must narrow to the declared MorphOneRelation")

	// Oracle: morphOne getMorphClass under a morph map => the alias ('mp_post' in the probe).
	require.Equal(t, "morph_one_post", relation.MorphClass())
}

func TestMorphOne_DeclaredInterfaceRejectsTheSharedLeaf(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	plain := builder.Related(&morphOneParent{ID: 9}, "Profile")
	_, morph := plain.(contractsorm.MorphOneRelation)
	require.False(t, morph, "a plain HasOne object must not satisfy the declared MorphOneRelation")
	_, hasOne := plain.(contractsorm.HasOneRelation)
	require.True(t, hasOne, "the shared leaf still narrows through the non-morph alias")
}
