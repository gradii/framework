package morph_to

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// This file extends the morph_to corpus with the MorphTo surface item morphport-morph-to-surface
// landed beyond Laravel's DatabaseEloquentMorphToTest.php: the per-class eager configuration
// (MorphTo::morphWith / morphWithCount / constrain — declaration-borne here as
// contractsorm.MorphTo.MorphWith / MorphWithCount / Constrain and consumed per bucket by the
// loader) and the trashed-scope family (MorphTo::withTrashed / withoutTrashed / onlyTrashed —
// contractsorm.MorphTo.WithTrashed / WithoutTrashed / OnlyTrashed). Every expectation was measured
// by executing the scenario through the oracle first (php .claude/laravel-oracle/oracle.php,
// illuminate/database v12.55.1): the per-class config in /tmp/morphport-probes/morph_with.php, the
// trashed family in /tmp/morphport-probes/trashed.php.

// --- parent models (one table; one declaration per per-class config) -------------

// morphSurfaceComment is the plain MorphTo parent — no per-class config.
type morphSurfaceComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`

	Commentable any `gorm:"-" db:"-"`
}

func (morphSurfaceComment) TableName() string { return "morph_surface_comments" }

func (morphSurfaceComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{Name: "commentable"},
	}
}

// morphSurfaceMorphWithComment declares MorphWith for the Post type — Laravel's
// $this->morphTo()->morphWith([Post::class => ['author']]).
type morphSurfaceMorphWithComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`

	Commentable any `gorm:"-" db:"-"`
}

func (morphSurfaceMorphWithComment) TableName() string { return "morph_surface_comments" }

func (morphSurfaceMorphWithComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{
			Name:      "commentable",
			MorphWith: map[string][]string{"morph_surface_post": {"Author"}},
		},
	}
}

// morphSurfaceCountComment declares MorphWithCount for the Post type — Laravel's
// $this->morphTo()->morphWithCount([Post::class => ['comments']]).
type morphSurfaceCountComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`

	Commentable any `gorm:"-" db:"-"`
}

func (morphSurfaceCountComment) TableName() string { return "morph_surface_comments" }

func (morphSurfaceCountComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{
			Name:           "commentable",
			MorphWithCount: map[string][]any{"morph_surface_post": {"Comments"}},
		},
	}
}

// morphSurfaceConstrainComment declares Constrain for the Post type — Laravel's
// $this->morphTo()->constrain([Post::class => fn ($query) => $query->where('title', '!=', 'blocked')]).
type morphSurfaceConstrainComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`

	Commentable any `gorm:"-" db:"-"`
}

func (morphSurfaceConstrainComment) TableName() string { return "morph_surface_comments" }

func (morphSurfaceConstrainComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{
			Name: "commentable",
			Constrain: map[string]contractsorm.RelationCallback{
				"morph_surface_post": func(query contractsorm.Query) contractsorm.Query {
					return query.Where("title", "!=", "blocked")
				},
			},
		},
	}
}

// morphSurfaceWithTrashedComment declares the withTrashed member of the trashed family.
type morphSurfaceWithTrashedComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`

	Commentable any `gorm:"-" db:"-"`
}

func (morphSurfaceWithTrashedComment) TableName() string { return "morph_surface_comments" }

func (morphSurfaceWithTrashedComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{Name: "commentable", WithTrashed: true},
	}
}

// morphSurfaceWithoutTrashedComment declares the withoutTrashed member of the trashed family.
type morphSurfaceWithoutTrashedComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`

	Commentable any `gorm:"-" db:"-"`
}

func (morphSurfaceWithoutTrashedComment) TableName() string { return "morph_surface_comments" }

func (morphSurfaceWithoutTrashedComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{Name: "commentable", WithoutTrashed: true},
	}
}

// morphSurfaceOnlyTrashedComment declares the onlyTrashed member of the trashed family.
type morphSurfaceOnlyTrashedComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`

	Commentable any `gorm:"-" db:"-"`
}

func (morphSurfaceOnlyTrashedComment) TableName() string { return "morph_surface_comments" }

func (morphSurfaceOnlyTrashedComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{Name: "commentable", OnlyTrashed: true},
	}
}

// --- related models -------------------------------------------------------------

// morphSurfacePost is the Post type — carries the framework soft-delete field (the trashed tests),
// the Author relation MorphWith loads and the Comments relation MorphWithCount counts.
// CommentsCount is the aggregate column the count sub-select hydrates.
type morphSurfacePost struct {
	databaseorm.SoftDeletes

	ID       uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Title    string `gorm:"column:title" db:"title"`
	AuthorID uint64 `gorm:"column:author_id" db:"author_id"`

	Author        *morphSurfaceAuthor `gorm:"-" db:"-"`
	CommentsCount int                 `gorm:"column:comments_count" db:"comments_count"`
}

func (morphSurfacePost) TableName() string { return "morph_surface_posts" }

func (morphSurfacePost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author":   contractsorm.BelongsTo{Related: &morphSurfaceAuthor{}, ForeignKey: "author_id"},
		"Comments": contractsorm.HasMany{Related: &morphSurfaceNestedComment{}, ForeignKey: "post_id"},
	}
}

// morphSurfaceVideo is the second morph type — no per-class config ever names it; it carries the
// same relation-shaped fields as Post so the per-class assertions can weigh absence.
type morphSurfaceVideo struct {
	ID    uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Title string `gorm:"column:title" db:"title"`

	Author        *morphSurfaceAuthor `gorm:"-" db:"-"`
	CommentsCount int                 `gorm:"column:comments_count" db:"comments_count"`
}

func (morphSurfaceVideo) TableName() string { return "morph_surface_videos" }

type morphSurfaceAuthor struct {
	ID   uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Name string `gorm:"column:name" db:"name"`
}

func (morphSurfaceAuthor) TableName() string { return "morph_surface_authors" }

type morphSurfaceNestedComment struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	PostID uint64 `gorm:"column:post_id" db:"post_id"`
	Body   string `gorm:"column:body" db:"body"`
}

func (morphSurfaceNestedComment) TableName() string { return "morph_surface_nested_comments" }

func init() {
	morphmap.Register(map[string]any{
		"morph_surface_post":  &morphSurfacePost{},
		"morph_surface_video": &morphSurfaceVideo{},
	})
}

// --- schema and seeding ---------------------------------------------------------

func morphSurfaceSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table morph_surface_comments (id integer primary key autoincrement, commentable_type varchar null, commentable_id integer null)`,
		`create table morph_surface_posts (id integer primary key autoincrement, title varchar null, author_id integer null, deleted_at datetime null)`,
		`create table morph_surface_videos (id integer primary key autoincrement, title varchar null)`,
		`create table morph_surface_authors (id integer primary key autoincrement, name varchar null)`,
		`create table morph_surface_nested_comments (id integer primary key autoincrement, post_id integer null, body varchar null)`,
	)
}

func morphSurfaceCreateAuthor(t *testing.T, query contractsorm.Query, id uint64, name string) {
	t.Helper()

	author := morphSurfaceAuthor{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&author).Create(map[string]any{"id": id, "name": name}))
}

func morphSurfaceCreatePost(t *testing.T, query contractsorm.Query, id uint64, title string, deleted bool) {
	t.Helper()

	post := morphSurfacePost{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&post).Create(map[string]any{"id": id, "title": title, "author_id": uint64(1)}))
	if deleted {
		_, err := cloneRelationQuery(t, query).Delete(&post)
		require.NoError(t, err)
	}
}

func morphSurfaceCreateVideo(t *testing.T, query contractsorm.Query, id uint64, title string) {
	t.Helper()

	video := morphSurfaceVideo{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&video).Create(map[string]any{"id": id, "title": title}))
}

func morphSurfaceCreateNestedComment(t *testing.T, query contractsorm.Query, id, postID uint64, body string) {
	t.Helper()

	nested := morphSurfaceNestedComment{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&nested).Create(map[string]any{"id": id, "post_id": postID, "body": body}))
}

// morphSurfaceCreateComment writes one parent row pointing at alias#targetID — the commentable
// columns the morph loader buckets on.
func morphSurfaceCreateComment(t *testing.T, query contractsorm.Query, id uint64, alias string, targetID uint64) {
	t.Helper()

	comment := morphSurfaceComment{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&comment).Create(map[string]any{
		"id": id, "commentable_type": alias, "commentable_id": targetID,
	}))
}

// --- per-class eager configuration ----------------------------------------------

// Oracle (/tmp/morphport-probes/morph_with.php): the Post type loads its author in a follow-up
// `select * from "mpw_authors" where "mpw_authors"."id" in (1)`; the unconfigured Video type
// receives no such query.
func TestMorphTo_MorphWithLoadsTheClassConfiguredRelations(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreateAuthor(t, query, 1, "taylor")
	morphSurfaceCreatePost(t, query, 1, "ok", false)
	morphSurfaceCreateVideo(t, query, 1, "v1")
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 1)
	morphSurfaceCreateComment(t, query, 2, "morph_surface_video", 1)

	found := []morphSurfaceMorphWithComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphSurfaceMorphWithComment{}).With("Commentable").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 2)

	post, ok := found[0].Commentable.(*morphSurfacePost)
	require.True(t, ok, "the first comment must resolve the Post type")
	require.NotNil(t, post.Author, "the Post type's morphWith list loads its author")
	assert.Equal(t, "taylor", post.Author.Name)

	video, ok := found[1].Commentable.(*morphSurfaceVideo)
	require.True(t, ok, "the second comment must resolve the Video type")
	assert.Nil(t, video.Author, "the unconfigured Video type still receives no author")
	assert.Equal(t, "v1", video.Title)
}

// Oracle (/tmp/morphport-probes/morph_with.php): the Post type's rows carry
// `select "mpw_posts".*, (select count(*) from "mpw_comments" where "mpw_posts"."id" =
// "mpw_comments"."post_id") as "comments_count" from "mpw_posts" where ... "mpw_posts"."id" in
// (...)` — the count column hydrates comments_count; the unconfigured Video type carries no count
// column.
func TestMorphTo_MorphWithCountLoadsTheClassConfiguredCounts(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 1, "ok", false)
	morphSurfaceCreateVideo(t, query, 1, "v1")
	morphSurfaceCreateNestedComment(t, query, 1, 1, "first")
	morphSurfaceCreateNestedComment(t, query, 2, 1, "second")
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 1)
	morphSurfaceCreateComment(t, query, 2, "morph_surface_video", 1)

	found := []morphSurfaceCountComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphSurfaceCountComment{}).With("Commentable").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 2)

	post, ok := found[0].Commentable.(*morphSurfacePost)
	require.True(t, ok, "the first comment must resolve the Post type")
	assert.Equal(t, 2, post.CommentsCount, "the Post type's morphWithCount counts its comments")

	video, ok := found[1].Commentable.(*morphSurfaceVideo)
	require.True(t, ok, "the second comment must resolve the Video type")
	assert.Zero(t, video.CommentsCount, "the unconfigured Video type receives no count column")
}

// Oracle (/tmp/morphport-probes/morph_with.php): `where "title" != ? and "mpw_posts"."id" in
// (1, 2)` — the constrained row drops out (the second comment's target resolves to null); the
// Video bucket compiles `where "mpw_videos"."id" in (1)` untouched.
func TestMorphTo_ConstrainAppliesTheClassConstraintToItsTypeQuery(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 1, "ok", false)
	morphSurfaceCreatePost(t, query, 2, "blocked", false)
	morphSurfaceCreateVideo(t, query, 1, "v1")
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 1)
	morphSurfaceCreateComment(t, query, 2, "morph_surface_post", 2)
	morphSurfaceCreateComment(t, query, 3, "morph_surface_video", 1)

	found := []morphSurfaceConstrainComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphSurfaceConstrainComment{}).With("Commentable").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 3)

	first, ok := found[0].Commentable.(*morphSurfacePost)
	require.True(t, ok)
	assert.Equal(t, "ok", first.Title)

	assert.Nil(t, found[1].Commentable, "the constrained-out post must not resolve")

	video, ok := found[2].Commentable.(*morphSurfaceVideo)
	require.True(t, ok)
	assert.Equal(t, "v1", video.Title, "the constraint applies to the Post type only")
}

// --- trashed family -------------------------------------------------------------

// Oracle (/tmp/morphport-probes/trashed.php): the default read excludes the ghost — the
// object-path read (Related) carries the related model's SoftDeletingScope and compiles
// `select * from "mpt_posts" where "mpt_posts"."id" = ? and "mpt_posts"."deleted_at" is null
// limit 1`. The eager / Load default carries the same predicate now that the compiled eager runner
// binds the related model to its child (eager_soft_delete_test.go); the trashed-family tests below
// pin the eager side.
func TestMorphTo_DefaultReadExcludesSoftDeletedTargets(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 2, "ghost", true)
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 2)

	ghost := morphSurfaceComment{ID: 1, CommentableType: "morph_surface_post", CommentableID: 2}
	dest := morphSurfacePost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&ghost, "Commentable").First(&dest))
	assert.Zero(t, dest.ID, "the default object read excludes the soft-deleted target")
}

// Oracle (/tmp/morphport-probes/trashed.php): withTrashed declared resolves the ghost on the lazy
// read — `select * from "mpt_posts" where "mpt_posts"."id" = ? limit 1`.
func TestMorphTo_WithTrashedResolvesSoftDeletedTargets(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 2, "ghost", true)
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 2)

	parent := morphSurfaceWithTrashedComment{ID: 1, CommentableType: "morph_surface_post", CommentableID: 2}
	require.NoError(t, cloneRelationQuery(t, query).Load(&parent, "Commentable"))
	post, ok := parent.Commentable.(*morphSurfacePost)
	require.True(t, ok)
	assert.Equal(t, uint64(2), post.ID)
}

// Oracle (/tmp/morphport-probes/trashed.php): onlyTrashed compiles `"mpt_posts"."deleted_at" is
// not null` — the ghost resolves, the live target stays null.
func TestMorphTo_OnlyTrashedResolvesOnlySoftDeletedTargets(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 1, "alive", false)
	morphSurfaceCreatePost(t, query, 2, "ghost", true)
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 2)
	morphSurfaceCreateComment(t, query, 2, "morph_surface_post", 1)

	ghostParent := morphSurfaceOnlyTrashedComment{ID: 1, CommentableType: "morph_surface_post", CommentableID: 2}
	require.NoError(t, cloneRelationQuery(t, query).Load(&ghostParent, "Commentable"))
	post, ok := ghostParent.Commentable.(*morphSurfacePost)
	require.True(t, ok)
	assert.Equal(t, uint64(2), post.ID)

	liveParent := morphSurfaceOnlyTrashedComment{ID: 2, CommentableType: "morph_surface_post", CommentableID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Load(&liveParent, "Commentable"))
	assert.Nil(t, liveParent.Commentable)
}

// Oracle (/tmp/morphport-probes/trashed.php): Laravel's per-type eager read carries
// `"mpt_posts"."deleted_at" is null` — withoutTrashed keeps that exclusion. The Go eager default now
// carries the same predicate (the runner binds the resolved type to the child), so the declaration is
// redundant with it rather than the thing that restores it.
func TestMorphTo_WithoutTrashedExcludesSoftDeletedTargetsOnTheEagerPath(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 1, "alive", false)
	morphSurfaceCreatePost(t, query, 2, "ghost", true)
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 2)
	morphSurfaceCreateComment(t, query, 2, "morph_surface_post", 1)

	found := []morphSurfaceWithoutTrashedComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphSurfaceWithoutTrashedComment{}).With("Commentable").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 2)
	assert.Nil(t, found[0].Commentable, "the ghost target is excluded")

	post, ok := found[1].Commentable.(*morphSurfacePost)
	require.True(t, ok)
	assert.Equal(t, uint64(1), post.ID)
}

// Oracle (/tmp/morphport-probes/trashed.php): the same eager read under withTrashed includes the
// ghost — `select * from "mpt_posts" where "mpt_posts"."id" in (2, 1)`.
func TestMorphTo_WithTrashedIncludesSoftDeletedTargetsOnTheEagerPath(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 1, "alive", false)
	morphSurfaceCreatePost(t, query, 2, "ghost", true)
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 2)
	morphSurfaceCreateComment(t, query, 2, "morph_surface_post", 1)

	found := []morphSurfaceWithTrashedComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphSurfaceWithTrashedComment{}).With("Commentable").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 2)

	ghost, ok := found[0].Commentable.(*morphSurfacePost)
	require.True(t, ok)
	assert.Equal(t, uint64(2), ghost.ID)

	alive, ok := found[1].Commentable.(*morphSurfacePost)
	require.True(t, ok)
	assert.Equal(t, uint64(1), alive.ID)
}

// The object-chain form of the family (oracle: `$comment->commentable()->withTrashed()->first()`
// and `->onlyTrashed()->first()` against the ghost/live posts).
func TestMorphTo_ObjectChainTrashedScope(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreatePost(t, query, 1, "alive", false)
	morphSurfaceCreatePost(t, query, 2, "ghost", true)

	ghost := morphSurfaceComment{ID: 1, CommentableType: "morph_surface_post", CommentableID: 2}

	plain := morphSurfacePost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&ghost, "Commentable").First(&plain))
	assert.Zero(t, plain.ID, "the default chain read excludes the soft-deleted target")

	trashed := morphSurfacePost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&ghost, "Commentable").WithTrashed().First(&trashed))
	assert.Equal(t, uint64(2), trashed.ID)

	only := morphSurfacePost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&ghost, "Commentable").OnlyTrashed().First(&only))
	assert.Equal(t, uint64(2), only.ID)

	alive := morphSurfaceComment{ID: 2, CommentableType: "morph_surface_post", CommentableID: 1}
	live := morphSurfacePost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&alive, "Commentable").OnlyTrashed().First(&live))
	assert.Zero(t, live.ID, "onlyTrashed leaves the live target out")
}

// TestMorphTo_MorphWithCollisionKeepsTheCallersNestedConstraint — a caller constraint on a nested
// path ("Commentable.Author") whose relation the class's MorphWith list also names: Laravel's
// getResultsByType merges the class list with array_merge, and the class list's entries are plain
// relation names (numeric keys), so they sit BESIDE the relation-keyed eager loads — they never
// overwrite them — and with()'s combineConstraints keeps the constrained read. The author query
// still carries the caller's where and the author stays null. Oracle (illuminate/database v12.55.1,
// executed, /tmp/morphverify-probes/collision_dotted.php): `with(['commentable.author' => fn ($q) =>
// $q->where('name', 'nobody')])` compiles `select * from "vd_authors" where "vd_authors"."id" in
// (1) and "name" = ?` and the author is null.
func TestMorphTo_MorphWithCollisionKeepsTheCallersNestedConstraint(t *testing.T) {
	query := newRelationQuery(t)
	morphSurfaceSchema(t, query)

	morphSurfaceCreateAuthor(t, query, 1, "taylor")
	morphSurfaceCreatePost(t, query, 1, "ok", false)
	morphSurfaceCreateComment(t, query, 1, "morph_surface_post", 1)

	found := []morphSurfaceMorphWithComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphSurfaceMorphWithComment{}).
		With("Commentable.Author", func(q contractsorm.Query) contractsorm.Query {
			return q.Where("name", "nobody")
		}).OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 1)

	post, ok := found[0].Commentable.(*morphSurfacePost)
	require.True(t, ok, "the comment must resolve the Post type")
	assert.Nil(t, post.Author, "the caller's nested constraint survives the morphWith collision")
}
