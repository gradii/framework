package polymorphic_integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentPolymorphicIntegrationTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestPolymorphicIntegration_<method without the test prefix>. The four tables mirror the corpus's
// createSchema(); rows are seeded through the ORM, and relations are loaded explicitly through
// OrmBuilder.With — the harness's lazy-loading rule, since Go has no property-access magic.
//
// # How the framework's polymorphic machinery maps onto Laravel's
//
//   - Laravel resolves a `*_type` column by PHP class name. The Go ORM resolves it through the
//     morph map (orm.MorphMap / database/orm/morphmap): both models here are registered under
//     short aliases ("post" / "comment"), so the `*_type` columns carry those aliases where the
//     corpus stores the PHP class name, and MorphTo buckets and relation writes resolve through
//     the same registry. The rows each relation resolves to are identical.
//   - Laravel models declare default eager loads with `protected $with`. The Go ORM has no
//     model-level default eager load, so the port applies the same eager loads explicitly at the
//     query site: TestLikeWithSingleWith's `$with = ['likeable']` and TestComment's
//     `$with = ['commentable']` become With("Likeable") / the nested "Likeable.Commentable", and
//     TestLikeWithNestedWith's `$with = ['likeable.owner']` becomes With("Likeable.Owner") — the
//     same eager-load work `$with` drives. That leaves two like model variants whose only
//     difference is a `$with` default with no Go counterpart, so one polyLike model serves all
//     three.
//   - `relationLoaded('x')` has no Go equivalent: the observable is the relation field the load
//     populated, so the ports assert the field is non-nil and holds the expected row.
//
// Every SQL string cited below was measured with the oracle (.claude/laravel-oracle/oracle.php
// against illuminate/database v12.55.1, replaying this corpus's schema, models, and test bodies
// on sqlite in memory), never recalled. The seed's relation creates stamp the morph columns —
// comments: ("body", "user_id", "commentable_id", "commentable_type") values ("A comment body",
// 1, 1, "<TestPost class>"); likes: ("likeable_id", "likeable_type") values (1, "<TestComment
// class>") — a `$with = ['likeable']` read runs `select * from "likes" limit 1` then the comment
// bucket `select * from "comments" where "comments"."id" in (1)`, the comment's own
// `$with = ['commentable']` adds `select * from "posts" where "posts"."id" in (1)` during the
// bucket read, and `with('likeable.owner')` adds the nested
// `select * from "users" where "users"."id" in (1)`.

// --- models ------------------------------------------------------------------

// polyUser ports TestUser.
type polyUser struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`

	Posts []*polyPost `gorm:"-"`
}

func (polyUser) TableName() string { return "users" }

func (polyUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &polyPost{}, ForeignKey: "user_id"},
	}
}

// polyPost ports TestPost.
type polyPost struct {
	ID     uint   `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint   `gorm:"column:user_id"`
	Title  string `gorm:"column:title"`
	Body   string `gorm:"column:body"`

	Comments []*polyComment `gorm:"-"`
	Owner    *polyUser      `gorm:"-"`
	Likes    []*polyLike    `gorm:"-"`
}

func (polyPost) TableName() string { return "posts" }

func (polyPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Comments": contractsorm.MorphMany{Related: &polyComment{}, Name: "commentable"},
		"Owner":    contractsorm.BelongsTo{Related: &polyUser{}, ForeignKey: "user_id"},
		"Likes":    contractsorm.MorphMany{Related: &polyLike{}, Name: "likeable"},
	}
}

// polyComment ports TestComment; the corpus's `$with = ['commentable']` is the nested eager load
// the chained-relationships test applies explicitly.
type polyComment struct {
	ID              uint   `gorm:"column:id;primaryKey;autoIncrement"`
	CommentableID   uint   `gorm:"column:commentable_id"`
	CommentableType string `gorm:"column:commentable_type"`
	UserID          uint   `gorm:"column:user_id"`
	Body            string `gorm:"column:body"`

	Commentable any         `gorm:"-"`
	Owner       *polyUser   `gorm:"-"`
	Likes       []*polyLike `gorm:"-"`
}

func (polyComment) TableName() string { return "comments" }

func (polyComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner":       contractsorm.BelongsTo{Related: &polyUser{}, ForeignKey: "user_id"},
		"Commentable": contractsorm.MorphTo{Name: "commentable"},
		"Likes":       contractsorm.MorphMany{Related: &polyLike{}, Name: "likeable"},
	}
}

// polyLike ports TestLike and stands in for TestLikeWithSingleWith / TestLikeWithNestedWith,
// which differ from it only by a `$with` default the port applies explicitly on the query.
type polyLike struct {
	ID           uint   `gorm:"column:id;primaryKey;autoIncrement"`
	LikeableID   uint   `gorm:"column:likeable_id"`
	LikeableType string `gorm:"column:likeable_type"`

	Likeable any `gorm:"-"`
}

func (polyLike) TableName() string { return "likes" }

func (polyLike) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Likeable": contractsorm.MorphTo{Name: "likeable"},
	}
}

// --- schema and seeding ------------------------------------------------------

// polyMorphMap registers the two polymorphic aliases the `*_type` columns carry, the role the
// corpus's PHP class names play. Registration merges, so every test may call this directly.
func polyMorphMap() {
	databaseorm.MorphMap(map[string]any{
		"comment": &polyComment{},
		"post":    &polyPost{},
	})
}

// polySchema mirrors the corpus's createSchema() in sqlite DDL.
func polySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique, created_at datetime null, updated_at datetime null)`,
		`create table posts (id integer primary key autoincrement, user_id integer not null, title varchar not null, body text not null, created_at datetime null, updated_at datetime null)`,
		`create table comments (id integer primary key autoincrement, commentable_id integer not null, commentable_type varchar not null, user_id integer not null, body text not null, created_at datetime null, updated_at datetime null)`,
		`create table likes (id integer primary key autoincrement, likeable_id integer not null, likeable_type varchar not null, created_at datetime null, updated_at datetime null)`,
	)
}

// polySeedData ports seedData(): the user, the user's post, the post's comment, and the comment's
// like, each created through the parent relation like the corpus's `->posts()->create(...)` chain,
// with the corpus's exact values.
func polySeedData(t *testing.T, query contractsorm.Query) (polyPost, polyComment, polyLike) {
	t.Helper()

	taylor := polyUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&taylor).Create(&taylor))
	require.Equal(t, uint(1), taylor.ID)

	post := polyPost{Title: "A title", Body: "A body"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&taylor, "Posts").Create(&post))
	require.Equal(t, uint(1), post.UserID, "posts()->create() must set user_id from the parent")

	comment := polyComment{Body: "A comment body", UserID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").Create(&comment))
	require.Equal(t, post.ID, comment.CommentableID, "comments()->create() must set commentable_id")
	require.Equal(t, "post", comment.CommentableType, "comments()->create() must set commentable_type")

	like := polyLike{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&comment, "Likes").Create(&like))
	require.Equal(t, comment.ID, like.LikeableID, "likes()->create() must set likeable_id")
	require.Equal(t, "comment", like.LikeableType, "likes()->create() must set likeable_type")

	return post, comment, like
}

// polyFirstLike reads the likes table with the given eager loads and returns the single row,
// standing in for the corpus's TestLike*::first(). The seeds insert exactly one like, so the read
// row is the seeded one; Get (rather than First) drains its cursor before the eager loads run,
// which keeps the harness's one-connection pool from deadlocking.
func polyFirstLike(t *testing.T, query contractsorm.Query, with ...any) polyLike {
	t.Helper()

	likes := []polyLike{}
	q := cloneRelationQuery(t, query).Model(&polyLike{})
	if len(with) > 0 {
		q = q.With(with...)
	}
	require.NoError(t, q.Get(&likes))
	require.Len(t, likes, 1)

	return likes[0]
}

// --- ports of the corpus -----------------------------------------------------

func TestPolymorphicIntegration_ItLoadsRelationshipsAutomatically(t *testing.T) {
	query := newRelationQuery(t)
	polyMorphMap()
	polySchema(t, query)

	_, comment, like := polySeedData(t, query)

	// Laravel: TestLikeWithSingleWith::first() with `$with = ['likeable']`; the model-level
	// default is the explicit eager load here (oracle: `select * from "likes" limit 1` then the
	// comment bucket `select * from "comments" where "comments"."id" in (1)`).
	got := polyFirstLike(t, query, "Likeable")

	// Laravel asserts relationLoaded('likeable') and assertEquals(TestComment::first(),
	// $like->likeable); the Go observable of the loaded relation is the populated field.
	require.NotNil(t, got.Likeable)
	loaded, ok := got.Likeable.(*polyComment)
	require.True(t, ok, "likeable must resolve through the morph map to the comment model")
	assert.Equal(t, comment.ID, loaded.ID)
	assert.Equal(t, comment.CommentableID, loaded.CommentableID)
	assert.Equal(t, comment.CommentableType, loaded.CommentableType)
	assert.Equal(t, comment.UserID, loaded.UserID)
	assert.Equal(t, comment.Body, loaded.Body)

	// The like row carries the morph columns the seed's `->likes()->create([])` wrote.
	assert.Equal(t, comment.ID, like.LikeableID)
	assert.Equal(t, "comment", like.LikeableType)
}

func TestPolymorphicIntegration_ItLoadsChainedRelationshipsAutomatically(t *testing.T) {
	query := newRelationQuery(t)
	polyMorphMap()
	polySchema(t, query)

	post, _, _ := polySeedData(t, query)

	// Laravel: TestLikeWithSingleWith::first() loads likeable via `$with`, and the comment model's
	// own `$with = ['commentable']` then loads the chained relation; the port expresses both as
	// one nested eager load (oracle: the comment's `$with` runs during the morph bucket read as
	// `select * from "posts" where "posts"."id" in (1)`).
	got := polyFirstLike(t, query, "Likeable.Commentable")

	require.NotNil(t, got.Likeable)
	comment, ok := got.Likeable.(*polyComment)
	require.True(t, ok)

	// Laravel asserts the chained relation is loaded and equals TestPost::first().
	require.NotNil(t, comment.Commentable)
	parent, ok := comment.Commentable.(*polyPost)
	require.True(t, ok)
	assert.Equal(t, post.ID, parent.ID)
	assert.Equal(t, post.UserID, parent.UserID)
	assert.Equal(t, post.Title, parent.Title)
	assert.Equal(t, post.Body, parent.Body)
}

func TestPolymorphicIntegration_ItLoadsNestedRelationshipsAutomatically(t *testing.T) {
	query := newRelationQuery(t)
	polyMorphMap()
	polySchema(t, query)

	polySeedData(t, query)

	// Laravel: TestLikeWithNestedWith::first() with `$with = ['likeable.owner']` (oracle:
	// `select * from "likes" limit 1`, the comment bucket, then the nested
	// `select * from "users" where "users"."id" in (1)`).
	got := polyFirstLike(t, query, "Likeable.Owner")

	require.NotNil(t, got.Likeable)
	comment, ok := got.Likeable.(*polyComment)
	require.True(t, ok)

	// Laravel asserts the nested relation is loaded and equals TestUser::first().
	require.NotNil(t, comment.Owner)
	assert.Equal(t, uint(1), comment.Owner.ID)
	assert.Equal(t, "taylorotwell@gmail.com", comment.Owner.Email)
}

func TestPolymorphicIntegration_ItLoadsNestedRelationshipsOnDemand(t *testing.T) {
	query := newRelationQuery(t)
	polyMorphMap()
	polySchema(t, query)

	polySeedData(t, query)

	// Laravel: TestLike::with('likeable.owner')->first() — the explicit form of the eager load.
	// In the Go ORM this and the model-default variant are the same call, so both corpus tests
	// assert their observables off the same expression.
	got := polyFirstLike(t, query, "Likeable.Owner")

	require.NotNil(t, got.Likeable)
	comment, ok := got.Likeable.(*polyComment)
	require.True(t, ok)
	require.NotNil(t, comment.Owner)
	assert.Equal(t, uint(1), comment.Owner.ID)
	assert.Equal(t, "taylorotwell@gmail.com", comment.Owner.Email)
}

func TestPolymorphicIntegration_ItLoadsNestedMorphRelationshipsOnDemand(t *testing.T) {
	// Laravel: TestLike::with('likeable.owner')->get()->loadMorph('likeable', [
	// TestComment::class => ['commentable'], TestPost::class => 'comments']); it asserts each
	// likeable carries its owner plus the relation listed for its own morph type.
	t.Skip("our ORM diverges: the ORM query exposes no loadMorph — relations load per model (Load / LoadMissing) or per query (With), and nothing groups them by morph type on an already-loaded collection, so get()->loadMorph(...) cannot run; probe: the nearest single call, With(\"Likeable.Commentable\", \"Likeable.Comments\") over the two-typed like set, fails with either `polymorphic type \"A title\" is not registered in the morph map; call orm.MorphMap or implement MorphClass() on the target model` (the other type's bucket row read through the first type's column layout) or `relation \"Commentable\" not found on model *polymorphic_integration.polyPost`, depending on the bucket iteration order; oracle: loadMorph runs one read per related type (select * from \"posts\" where \"posts\".\"id\" in (1) for the comment's commentable, select * from \"comments\" where \"comments\".\"commentable_id\" in (1) and \"comments\".\"commentable_type\" = ? for the post's comments)")
}

func TestPolymorphicIntegration_ItLoadsNestedMorphRelationshipCountsOnDemand(t *testing.T) {
	// Laravel: TestLike::with('likeable.owner')->get()->loadMorphCount('likeable', [
	// TestComment::class => ['likes'], TestPost::class => 'comments']); it asserts likes_count 2 /
	// comments_count 1 / likes_count 2 across the three likes' likeables.
	t.Skip("our ORM diverges: the ORM query exposes no loadMorphCount and no per-collection count loading — WithCount takes relations of the queried model and cannot reach through a morphTo (probe: WithCount(\"Likeable.Likes\") fails with `relation \"Likeable\" on model polyLike has unsupported kind \"no related model\"`); oracle: Laravel reads one aggregate per morph type (select \"id\", (select count(*) from \"likes\" where \"comments\".\"id\" = \"likes\".\"likeable_id\" and \"likes\".\"likeable_type\" = ?) as \"likes_count\" from \"comments\" where \"comments\".\"id\" in (...), and the comments_count form over \"posts\"), so the per-type counts cannot be produced")
}
