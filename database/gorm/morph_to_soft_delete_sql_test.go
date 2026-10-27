package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/goravel/framework/support/database" // framework's own soft-delete type
)

// These tests pin the soft-delete predicate the inverse polymorphic existence subquery must add per
// type. A MorphTo subquery iterates the morph map, so each requested type decides its own predicate:
// a type carrying the framework's own soft-delete type gets a "not deleted" clause inside the
// subquery, which is what Laravel's SoftDeletingScope does to the BelongsTo it builds per type.

// relSoftMorphPost is a morph type carrying the framework's own soft-delete type.
type relSoftMorphPost struct {
	ID        uint
	DeletedAt database.DeletedAt `gorm:"column:deleted_at"`
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

	assert.Equal(t, `select * from "morph_images" where ("morph_images"."imageable_type" = ? and exists (select * from "rel_soft_morph_posts" where "rel_soft_morph_posts"."id" = "morph_images"."imageable_id" and "rel_soft_morph_posts"."deleted_at" IS NULL))`, sql)
}
