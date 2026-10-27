package gorm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// newRelationSQL compiles the statement the Related query would run, through the same path the ORM
// executes, and returns it. It used to hand the query to GORM in DryRun mode; compiling here pins the
// SQL a caller really gets.
func newRelationSQL(t *testing.T, q contractsorm.Query, dest any) string {
	t.Helper()
	gq, ok := q.(*Query)
	if !ok {
		t.Fatalf("expected *gorm.Query, got %T", q)
	}
	stmt, _, err := gq.dest(dest).addGlobalScopes().compileSelect()
	require.NoError(t, err)

	return stmt
}

func TestRelated_HasMany_Where(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 7}, "Books")
	sql := newRelationSQL(t, rel, &[]relBook{})
	assert.Contains(t, sql, "rel_books")
	assert.Contains(t, sql, "user_id")
}

func TestRelated_BelongsTo_Where(t *testing.T) {
	q := newRelQueryWith(t, &relBook{})
	rel := q.Related(&relBook{AuthorID: 5}, "Author")
	sql := newRelationSQL(t, rel, &[]relUser{})
	assert.Contains(t, sql, "rel_users")
	// BelongsTo: WHERE related.<pk> = parent.<fk>
	assert.Contains(t, strings.ToLower(sql), "id")
}

func TestRelated_MorphMany_AddsTypeFilter(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 9}, "Houses")
	sql := newRelationSQL(t, rel, &[]relHouse{})
	assert.Contains(t, sql, "houseable_id")
	assert.Contains(t, sql, "houseable_type")
}

func TestRelated_MorphOne_AddsTypeFilter(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 9}, "Logo")
	sql := newRelationSQL(t, rel, &relLogo{})
	assert.Contains(t, sql, "logoable_id")
	assert.Contains(t, sql, "logoable_type")
}

func TestRelated_HasManyThrough(t *testing.T) {
	q := newRelQueryWith(t, &relCountry{})
	rel := q.Related(&relCountry{ID: 1}, "Posts")
	sql := newRelationSQL(t, rel, &[]relPost{})
	assert.Contains(t, sql, "rel_posts")
	assert.Contains(t, strings.ToLower(sql), "inner join")
	assert.Contains(t, sql, "rel_users")
}

func TestRelated_NotPointer(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(relUser{ID: 1}, "Books") // value, not pointer
	gq, ok := rel.(*Query)
	assert.True(t, ok)
	assert.True(t, errors.Is(gq.conditions.err, errors.OrmRelationParentNotPointer))
}

func TestRelated_NilParent(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(nil, "Books")
	gq, ok := rel.(*Query)
	assert.True(t, ok)
	assert.True(t, errors.Is(gq.conditions.err, errors.OrmRelationParentNotPointer))
}

// A query guarded because its relation could not be resolved carries the error on itself, so whichever
// terminal runs on it reports the reason rather than an empty result.
func TestRelated_RelationNotFound(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 1}, "DoesNotExist")
	gq, ok := rel.(*Query)
	assert.True(t, ok)
	assert.True(t, errors.Is(gq.conditions.err, errors.OrmRelationNotFound))
}

// --- MorphTo --------------------------------------------------------------

// morphParentLikePost is a sample model registered in the morph map for MorphTo tests.
type morphParentLikePost struct {
	ID    uint
	Title string
}

func (morphParentLikePost) TableName() string { return "morph_parent_like_posts" }

func TestRelated_MorphTo_ResolvedViaMorphMap(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()
	morphmap.Register(map[string]any{"post": &morphParentLikePost{}})

	q := newRelQueryWith(t, &morphImage{})
	rel := q.Related(&morphImage{ID: 1, ImageableID: 42, ImageableType: "post"}, "Imageable")
	sql := newRelationSQL(t, rel, &morphParentLikePost{})
	assert.Contains(t, sql, "morph_parent_like_posts")
	assert.Contains(t, sql, "id")
}

func TestRelated_MorphTo_UnregisteredType(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()

	q := newRelQueryWith(t, &morphImage{})
	rel := q.Related(&morphImage{ID: 1, ImageableID: 42, ImageableType: "unknown"}, "Imageable")
	gq, ok := rel.(*Query)
	assert.True(t, ok)
	assert.True(t, errors.Is(gq.conditions.err, errors.OrmMorphTypeUnknown))
}

func TestRelated_MorphTo_EmptyType_YieldsZeroRowQuery(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()

	q := newRelQueryWith(t, &morphImage{})
	rel := q.Related(&morphImage{ID: 1, ImageableID: 0, ImageableType: ""}, "Imageable")
	gq, ok := rel.(*Query)
	assert.True(t, ok)
	// The zero-row guard compiles to a WHERE that cannot match.
	stmt, _, err := gq.Table("any_table").(*Query).compileSelect()
	require.NoError(t, err)
	assert.Contains(t, stmt, "1 = 0")
}

// --- MorphToMany ---------------------------------------------------------

func TestRelated_MorphToMany_AddsPivotTypeFilter(t *testing.T) {
	q := newRelQueryWith(t, &morphPost{})
	rel := q.Related(&morphPost{ID: 3}, "Tags")
	sql := newRelationSQL(t, rel, &[]morphTag{})
	assert.Contains(t, strings.ToLower(sql), "inner join")
	assert.Contains(t, sql, "taggables")
	assert.Contains(t, sql, "taggable_type")
}

// --- OnQuery hook ----------------------------------------------------------

// scopedUser declares a HasMany whose OnQuery scope filters out unpublished books. Every code
// path that builds a query for this relation (Related, eager load, existence) must apply
// the scope.
type scopedUser struct {
	ID    uint
	Books []*scopedBook `gorm:"-"`
}

func (scopedUser) TableName() string { return "scoped_users" }

func (scopedUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books": contractsorm.HasMany{
			Related:    &scopedBook{},
			ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.Where("published", true)
			},
		},
	}
}

type scopedBook struct {
	ID        uint
	UserID    uint
	Title     string
	Published bool
}

func (scopedBook) TableName() string { return "scoped_books" }

func TestRelated_OnQuery_AppliedToReturnedQuery(t *testing.T) {
	q := newRelQueryWith(t, &scopedUser{})
	rel := q.Related(&scopedUser{ID: 5}, "Books")
	sql := newRelationSQL(t, rel, &[]scopedBook{})
	// The generated WHERE must include both the FK constraint and the OnQuery's published=true.
	assert.Contains(t, sql, "user_id")
	assert.Contains(t, sql, "published")
}

func TestCompileExistenceSubquery_OnQuery_AppliedInExistenceCheck(t *testing.T) {
	q := newRelQueryWith(t, &scopedUser{})
	desc, err := resolveRelation(&scopedUser{}, "Books")
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	assert.Contains(t, sql, "published")
}
