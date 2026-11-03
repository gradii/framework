package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	databaseorm "github.com/gradii/framework/database/orm"
)

// These tests pin the clause shape of the inverse polymorphic existence subquery and the soft-delete
// predicate it must add per type. The shape is the oracle's (illuminate/database v12.55.1): one
// `(<type> = ? and exists (...))` alternative per type in a group of its own —
// `where (("photos"."imageable_type" = ? and exists (select * from "posts" where
// "photos"."imageable_id" = "posts"."id")))` for one type, the pairs ORed for more — with the
// parent's morph id leading the correlation. A MorphTo subquery iterates the morph map, so each
// requested type decides its own predicate: a type carrying the framework's own soft-delete type
// gets a "not deleted" clause inside the subquery, which is what Laravel's SoftDeletingScope does
// to the BelongsTo it builds per type.

// relSoftMorphPost is a morph type carrying the framework's own soft-delete type.
type relSoftMorphPost struct {
	ID        uint
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at"`
}

func (relSoftMorphPost) TableName() string { return "rel_soft_morph_posts" }

// A MorphTo (inverse polymorphic) existence condition over a framework-soft-delete type must carry
// that type's predicate inside the subquery. Without it, hasMorph/whereHasMorph matches a parent
// whose only related row of that type is soft-deleted, which is a wrong result.
func TestCompileMorphToExistenceSubquery_FrameworkSoftDeletePredicate(t *testing.T) {
	q := newRelQueryWith(t, &morphImage{})
	got := toQuery(q.HasMorph("Imageable", []any{&relSoftMorphPost{}}))

	sql, _, err := got.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "morph_images" where (("morph_images"."imageable_type" = ? and exists (select * from "rel_soft_morph_posts" where "morph_images"."imageable_id" = "rel_soft_morph_posts"."id" and "rel_soft_morph_posts"."deleted_at" is null)))`, sql)
}

// Multiple types read as alternatives: the pairs are joined with "or", the shape Laravel's hasMorph
// renders per type (oracle: `where (("photos"."imageable_type" = ? and exists (...)) or (...))`).
func TestCompileMorphToExistenceSubquery_MultipleTypesAreAlternatives(t *testing.T) {
	q := newRelQueryWith(t, &morphImage{})
	got := toQuery(q.HasMorph("Imageable", []any{&relSoftMorphPost{}, &relUser{}}))

	sql, _, err := got.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "morph_images" where (("morph_images"."imageable_type" = ? and exists (select * from "rel_soft_morph_posts" where "morph_images"."imageable_id" = "rel_soft_morph_posts"."id" and "rel_soft_morph_posts"."deleted_at" is null)) or ("morph_images"."imageable_type" = ? and exists (select * from "rel_users" where "morph_images"."imageable_id" = "rel_users"."id")))`, sql)
}
