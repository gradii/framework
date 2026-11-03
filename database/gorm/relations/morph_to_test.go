// This file drives the MorphTo relation object's morph-field accessors — the Go form of
// MorphTo::getMorphType and the owner key (BelongsTo::getOwnerKeyName) — and the declared
// contracts/database/orm.MorphToRelation kind check, through a real *gorm.OrmBuilder on the
// recording transport. Expectations measured by the oracle (php .claude/laravel-oracle/oracle.php
// /tmp/morph_object_surface_probe.php, illuminate/database v12.55.1): morphTo getMorphType =>
// 'commentable_type'; getOwnerKeyName => NULL on the eager form (no type set), 'id' once the type
// column names a class, 'uuid' for an explicit morphTo(..., 'uuid') owner key.
//
// The trashed-family section pins the object-path half of the surface item morphport-morph-to-surface
// landed: the promoted Query methods (WithTrashed / OnlyTrashed) and the declaration flags
// (contractsorm.MorphTo.WithTrashed / OnlyTrashed) applied by database/gorm's newMorphToQuery.
// Expectations measured by the oracle (php .claude/laravel-oracle/oracle.php
// /tmp/morphport-probes/trashed.php, illuminate/database v12.55.1, sqlite): the default read
// carries `"mpt_posts"."deleted_at" is null`; withTrashed drops the predicate; onlyTrashed renders
// `is not null`.
package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// --- fixtures ------------------------------------------------------------------

type morphToChild struct {
	ID            uint   `gorm:"column:id;primaryKey"`
	ImageableID   uint   `gorm:"column:imageable_id"`
	ImageableType string `gorm:"column:imageable_type"`
	OwnerID       uint   `gorm:"column:owner_id"`
	OwnerType     string `gorm:"column:owner_type"`
	AuthorID      *uint  `gorm:"column:author_id"`
}

func (morphToChild) TableName() string { return "morph_to_children" }

func (morphToChild) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{Name: "imageable"},
		"Owner":     contractsorm.MorphTo{Name: "owner", OwnerKey: "uuid"},
		"Author":    contractsorm.BelongsTo{Related: &morphToAuthor{}, ForeignKey: "author_id"},
	}
}

type morphToAuthor struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (morphToAuthor) TableName() string { return "morph_to_authors" }

// morphToSoftTarget is a related class carrying the framework's soft-delete type — the shape the
// trashed family reads against.
type morphToSoftTarget struct {
	ID        uint                  `gorm:"column:id;primaryKey"`
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at"`
}

func (morphToSoftTarget) TableName() string { return "morph_to_soft_targets" }

// morphToTrashedChild declares the trashed-scope family on the relation options — the static Go
// form of the relation-method chain Laravel buffers and replays per type.
type morphToTrashedChild struct {
	ID            uint   `gorm:"column:id;primaryKey"`
	ImageableID   uint   `gorm:"column:imageable_id"`
	ImageableType string `gorm:"column:imageable_type"`
}

func (morphToTrashedChild) TableName() string { return "morph_to_children" }

func (morphToTrashedChild) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{Name: "imageable", WithTrashed: true},
		"GhostOnly": contractsorm.MorphTo{Name: "ghost", TypeColumn: "imageable_type", IDColumn: "imageable_id", OnlyTrashed: true},
	}
}

// registerMorphTargets installs the related classes the trashed-family reads resolve — the morph
// map is the registry every MorphTo read resolves the type column's value through. Each test
// registers and cleans up: sibling relation tests reset the process-wide registry, so a package
// init would not survive to this file's tests.
func registerMorphTargets(t *testing.T) {
	t.Helper()

	morphmap.Reset()
	t.Cleanup(morphmap.Reset)
	morphmap.Register(map[string]any{
		"morph_to_soft_target": &morphToSoftTarget{},
		"morph_to_author":      &morphToAuthor{},
	})
}

// --- accessors from real Related() descriptors ---------------------------------

func TestMorphTo_MorphFieldAccessorsFromRealDescriptor(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphToChild{}, "Imageable").(contractsorm.MorphToRelation)
	require.True(t, ok, "Related must narrow to the declared MorphToRelation")

	// Oracle: morphTo getMorphType => 'commentable_type' (the stored column, unqualified for
	// MorphTo); the owner key of the instance form (type column set) resolves to the related's
	// getKeyName() => 'id' — the default the Go descriptor pins (descriptorFromMorphTo).
	require.Equal(t, "imageable_type", relation.MorphType())
	require.Equal(t, "id", relation.MorphOwnerKey())
}

func TestMorphTo_MorphOwnerKeyExplicit(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	relation, ok := builder.Related(&morphToChild{}, "Owner").(contractsorm.MorphToRelation)
	require.True(t, ok, "Related must narrow to the declared MorphToRelation")

	// Oracle: an explicit morphTo(..., 'uuid') owner key => 'uuid'.
	require.Equal(t, "owner_type", relation.MorphType())
	require.Equal(t, "uuid", relation.MorphOwnerKey())
}

func TestMorphTo_DeclaredInterfaceRejectsTheSharedLeaf(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	plain := builder.Related(&morphToChild{}, "Author")
	_, morph := plain.(contractsorm.MorphToRelation)
	require.False(t, morph, "a plain BelongsTo object must not satisfy the declared MorphToRelation")
	_, belongsTo := plain.(contractsorm.BelongsToRelation)
	require.True(t, belongsTo, "the shared leaf still narrows through the non-morph alias")
}

// --- trashed family: object-path reads -----------------------------------------

func TestMorphTo_ObjectReadAppliesTheRelatedSoftDeletePredicate(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	registerMorphTargets(t)

	parent := morphToChild{ID: 1, ImageableID: 5, ImageableType: "morph_to_soft_target"}
	rel := builder.Related(&parent, "Imageable")

	dest := morphToSoftTarget{}
	require.NoError(t, rel.First(&dest))

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle (illuminate/database v12.55.1, sqlite, /tmp/morphport-probes/trashed.php): the
	// instance-form morphTo read compiles `select * from "mpt_posts" where "mpt_posts"."id" = ?
	// and "mpt_posts"."deleted_at" is null limit 1` — the related model's SoftDeletingScope
	// applies to the relation query. (Go's compiled First appends the model-key order by when the
	// query sets none — see compileFirst — so the Go statement below carries
	// `order by "morph_to_soft_targets"."id"` before the limit.)
	assert.Equal(t, `select * from "morph_to_soft_targets" where "morph_to_soft_targets"."id" = ? and "morph_to_soft_targets"."deleted_at" is null order by "morph_to_soft_targets"."id" limit 1`, statements[0].query)
	require.Len(t, statements[0].args, 1)
	assert.EqualValues(t, 5, deref(statements[0].args[0]))
}

func TestMorphTo_ObjectWithTrashedDropsThePredicate(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	registerMorphTargets(t)

	parent := morphToChild{ID: 1, ImageableID: 5, ImageableType: "morph_to_soft_target"}
	rel := builder.Related(&parent, "Imageable")

	dest := morphToSoftTarget{}
	// WithTrashed is the promoted Query method, mutating the wrapped builder in place — Laravel's
	// immediate when(true, callback) half for the instance form.
	require.NoError(t, rel.WithTrashed().First(&dest))

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle (same probe): `$comment->commentable()->withTrashed()->first()` compiles
	// `select * from "mpt_posts" where "mpt_posts"."id" = ? limit 1`.
	assert.Equal(t, `select * from "morph_to_soft_targets" where "morph_to_soft_targets"."id" = ? order by "morph_to_soft_targets"."id" limit 1`, statements[0].query)
}

func TestMorphTo_ObjectOnlyTrashedInvertsThePredicate(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	registerMorphTargets(t)

	parent := morphToChild{ID: 1, ImageableID: 5, ImageableType: "morph_to_soft_target"}
	rel := builder.Related(&parent, "Imageable")

	dest := morphToSoftTarget{}
	require.NoError(t, rel.OnlyTrashed().First(&dest))

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle (same probe): `->onlyTrashed()->first()` compiles `... and "mpt_posts"."deleted_at"
	// is not null limit 1`.
	assert.Equal(t, `select * from "morph_to_soft_targets" where "morph_to_soft_targets"."id" = ? and "morph_to_soft_targets"."deleted_at" is not null order by "morph_to_soft_targets"."id" limit 1`, statements[0].query)
}

func TestMorphTo_ObjectTrashedFamilyIsANoOpWithoutSoftDeletes(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	registerMorphTargets(t)

	// A related class without the framework soft-delete type: every flag is a no-op — the state
	// Laravel's macro-existence check leaves (`$query->hasMacro('withTrashed') ? ... : $query`);
	// oracle: the MptVideo type resolves normally under a decorated relation.
	parent := morphToChild{ID: 1, ImageableID: 3, ImageableType: "morph_to_author"}
	rel := builder.Related(&parent, "Imageable")

	dest := morphToAuthor{}
	require.NoError(t, rel.WithTrashed().First(&dest))

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "morph_to_authors" where "morph_to_authors"."id" = ? order by "morph_to_authors"."id" limit 1`, statements[0].query)
	assert.NotContains(t, statements[0].query, "deleted_at")

	dest2 := morphToAuthor{}
	require.NoError(t, rel.OnlyTrashed().First(&dest2))

	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.NotContains(t, statements[0].query, "deleted_at")
}

// --- trashed family: declaration flags reach the object-path read --------------

func TestMorphTo_DeclarationTrashedFlagsReachTheObjectRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	registerMorphTargets(t)

	parent := morphToTrashedChild{ID: 1, ImageableID: 5, ImageableType: "morph_to_soft_target"}
	rel := builder.Related(&parent, "Imageable")

	dest := morphToSoftTarget{}
	require.NoError(t, rel.First(&dest))

	statements := recorder.take()
	require.Len(t, statements, 1)
	// The declaration's WithTrashed (the static form of the buffered macro) reaches
	// newMorphToQuery: no predicate — oracle `... where "mpt_posts"."id" = ? limit 1`.
	assert.Equal(t, `select * from "morph_to_soft_targets" where "morph_to_soft_targets"."id" = ? order by "morph_to_soft_targets"."id" limit 1`, statements[0].query)

	rel2 := builder.Related(&parent, "GhostOnly")
	dest2 := morphToSoftTarget{}
	require.NoError(t, rel2.First(&dest2))

	statements = recorder.take()
	require.Len(t, statements, 1)
	// OnlyTrashed: the inverted predicate — oracle `... "mpt_posts"."deleted_at" is not null ...`.
	assert.Equal(t, `select * from "morph_to_soft_targets" where "morph_to_soft_targets"."id" = ? and "morph_to_soft_targets"."deleted_at" is not null order by "morph_to_soft_targets"."id" limit 1`, statements[0].query)
}
