package inverse_relation_morph_many

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentInverseRelationMorphManyTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestInverseRelationMorphMany_<method without the test prefix>. The corpus's two tables mirror
// createSchema() in sqlite DDL (test_posts: id + timestamps; test_comments: id +
// morphs('commentable') + timestamps); rows are seeded through the ORM, and relations are loaded
// explicitly — the harness's lazy-loading rule, since Go has no property-access magic.
//
// # The inverse feature, and how it is ported
//
// Every test in this corpus pins Laravel's inverse() (chaperone) feature. The post's relations are
// morphMany(...)->inverse('commentable') / morphOne(...)->latestOfMany()->inverse('commentable') —
// or ->inverse() with the name guessed, which the oracle measured as 'commentable' — so after any
// relation query Eloquent wires the child's MorphTo relation in memory:
// $comment->relationLoaded('commentable') is true and $comment->commentable is the same parent
// instance, with no further query. The Go declaration is the relation's Inverse field
// (contracts/database/orm), and the loaders (database/gorm/eager_loader.go) apply the same
// write-back on every read path — Load, With, latestOfMany and one-of-many — storing the parent
// instance itself, so the read-path ports below assert assert.Same identity (oracle-re-verified by
// .claude/laravel-oracle/_inverse_writeback_probe.php). The relation writers still only fill the
// child's FK and morph-type columns (see setRelationFKOnChild), so the write-path ports
// (makeMany, createMany, createManyQuietly, savingMany, updatingMany) keep the corpus's observable
// outcome by re-reading the child's MorphTo relation and comparing keys, as their comments say.
// The name guessing in the guessed* tests is PHP-only relation-name introspection; its loading is
// ported with the rest under the measured name.
//
// Where the corpus calls a write API, the closest Go writer is used and the comment says so: the
// writer's MakeMany (unsaved children with the FK preset) applies no inverse, so the made children
// are built through FindOrNew with a key that cannot exist, and the quiet variant is CreateMany —
// the corpus asserts the same outcome for all of them (oracle-verified). Load cannot run for the
// unsaved children (probe: it fails with `id can't be empty`), so the one read on each made
// instance goes through the relation read path Related(...).First(...), which resolves the MorphTo
// from the in-memory FK columns.
//
// Every behavior this file asserts was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled: the
// oracle executed the corpus scenarios against a real sqlite database and reported, for lazy /
// eager / guessed loading, latestOfMany and one-of-many reads, makingMany, creatingMany,
// creatingManyQuietly, savingMany and updating, relationLoaded('commentable') true with
// $comment->commentable === $post; the guessed inverse name 'commentable'; makeMany() reporting
// exists=false with the FK [parent class, parent key] and the chaperone wired; createMany() /
// createManyQuietly() reporting exists=true with the chaperone wired; and the write statements
// `insert into "test_comments" ("commentable_id", "commentable_type", "updated_at", "created_at")
// values (?, ?, ?, ?)` and `update "test_comments" set "commentable_id" = ?, "updated_at" = ?
// where "id" = ?`. The corpus checks behaviour, not compiled SQL: it has no ->toSql() pins. The
// oracle was still used to measure the SQL behind that behaviour: $post->comments() /
// guessedComments() / firstComment() compile `select * from "test_comments" where
// "test_comments"."commentable_type" = ? and "test_comments"."commentable_id" = ? and
// "test_comments"."commentable_id" is not null`; $post->lastComment() joins the one-of-many
// aggregate — `select "test_comments".* from "test_comments" inner join (select
// MAX("test_comments"."id") as "id_aggregate", "test_comments"."commentable_id",
// "test_comments"."commentable_type" from "test_comments" where "test_comments"."commentable_type"
// = ? and "test_comments"."commentable_id" = ? and "test_comments"."commentable_id" is not null
// group by "test_comments"."commentable_id", "test_comments"."commentable_type") as "latestOfMany"
// on "latestOfMany"."id_aggregate" = "test_comments"."id" and "latestOfMany"."commentable_type" =
// "test_comments"."commentable_type" and "latestOfMany"."commentable_id" =
// "test_comments"."commentable_id" where "test_comments"."commentable_type" = ? and
// "test_comments"."commentable_id" = ? and "test_comments"."commentable_id" is not null`; the
// child read $comment->commentable() compiles `select * from "test_posts" where "test_posts"."id" =
// ?`. The inverse declaration itself is metadata only: the oracle confirms
// getInverseRelationship() is 'commentable' on the parent relations while none of the SQL changes.

// init registers the corpus's parent model in the global morph map under the value
// resolveMorphValue writes into commentable_type — the registry the MorphTo loader resolves back to
// *inverseMorphManyPost. Laravel stores the model class name and resolves it through class_exists();
// the morph map is the Go counterpart.
func init() {
	morphmap.Register(map[string]any{"test_posts": &inverseMorphManyPost{}})
}

// --- models ------------------------------------------------------------------

// inverseMorphManyPost ports MorphManyInversePostModel: the corpus's test_posts table (id plus the
// automatic timestamps) with its five morph relations — comments()/guessedComments() are the
// morphMany ->inverse('commentable') / ->inverse(), lastComment()/guessedLastComment() the
// morphOne ->latestOfMany() pair, and firstComment() is comments()->one() (which copies the
// inverse from the has-relation, as HasMany::one() does). The Go form states the measured inverse
// name on each relation. The guessed relations differ from the named ones only in the PHP
// name-introspection. The relation destinations are kept out of the table by gorm:"-".
type inverseMorphManyPost struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Comments           []*inverseMorphManyComment `gorm:"-"`
	GuessedComments    []*inverseMorphManyComment `gorm:"-"`
	LastComment        *inverseMorphManyComment   `gorm:"-"`
	GuessedLastComment *inverseMorphManyComment   `gorm:"-"`
	FirstComment       *inverseMorphManyComment   `gorm:"-"`
}

func (inverseMorphManyPost) TableName() string { return "test_posts" }

func (inverseMorphManyPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// comments(): morphMany(Comment::class, 'commentable')->inverse('commentable').
		"Comments": contractsorm.MorphMany{Related: &inverseMorphManyComment{}, Name: "commentable", Inverse: "Commentable"},
		// guessedComments(): the same relation with ->inverse(), i.e. the name guessed.
		"GuessedComments": contractsorm.MorphMany{Related: &inverseMorphManyComment{}, Name: "commentable", Inverse: "Commentable"},
		// lastComment(): morphOne(..., 'commentable')->latestOfMany()->inverse('commentable') — the
		// comment with the highest id.
		"LastComment": contractsorm.MorphOne{
			Related: &inverseMorphManyComment{}, Name: "commentable", Inverse: "Commentable",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.LatestOfMany() },
		},
		// guessedLastComment(): latestOfMany() with ->inverse().
		"GuessedLastComment": contractsorm.MorphOne{
			Related: &inverseMorphManyComment{}, Name: "commentable", Inverse: "Commentable",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.LatestOfMany() },
		},
		// firstComment(): comments()->one() — the same polymorphic foreign-key query, one row.
		"FirstComment": contractsorm.MorphOne{Related: &inverseMorphManyComment{}, Name: "commentable", Inverse: "Commentable"},
	}
}

// inverseMorphManyComment ports MorphManyInverseCommentModel: test_comments with the
// morphs('commentable') columns and the morphTo('commentable') relation. The MorphTo destination is
// an `any` field, the shape the loader fills with the concrete type the morph map resolves from
// commentable_type.
type inverseMorphManyComment struct {
	ID              uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CommentableType string           `gorm:"column:commentable_type"`
	CommentableID   uint             `gorm:"column:commentable_id"`
	CreatedAt       *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt       *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Commentable any `gorm:"-"`
}

func (inverseMorphManyComment) TableName() string { return "test_comments" }

func (inverseMorphManyComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// commentable(): morphTo('commentable').
		"Commentable": contractsorm.MorphTo{Name: "commentable"},
	}
}

// --- schema and seeding ------------------------------------------------------

// inverseMorphManySchema mirrors the corpus's createSchema() in sqlite DDL. morphs('commentable')
// expands to the commentable_type / commentable_id columns (Laravel also adds a composite index;
// no assertion in the corpus observes it).
func inverseMorphManySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table test_posts (id integer primary key autoincrement, created_at datetime null, updated_at datetime null)`,
		`create table test_comments (id integer primary key autoincrement, commentable_type varchar not null, commentable_id integer not null, created_at datetime null, updated_at datetime null)`,
	)
}

// inverseMorphManyCreatePost ports MorphManyInversePostModel::create(): a row carrying only the
// auto-assigned id and the automatic timestamps (the ORM stamps the model's timestamp fields, the
// way Eloquent does).
func inverseMorphManyCreatePost(t *testing.T, query contractsorm.Query) inverseMorphManyPost {
	t.Helper()

	post := inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&post))
	require.NotZero(t, post.ID, "Create must read the generated key back")

	return post
}

// inverseMorphManySeedPostWithComments ports the corpus's withComments() factory helper: a post
// and its comments, each comment written with the parent's key and morph value already on it (the
// corpus's comment factory recycles the parent through recycle($model) and names the parent class
// in commentable_type — a plain insert, not a relation write). The returned comments are in insert
// order, so the last element is the newest and the first is the one firstComment's one-row read
// finds.
func inverseMorphManySeedPostWithComments(t *testing.T, query contractsorm.Query, count int) (inverseMorphManyPost, []*inverseMorphManyComment) {
	t.Helper()

	post := inverseMorphManyCreatePost(t, query)

	comments := make([]*inverseMorphManyComment, 0, count)
	for i := 0; i < count; i++ {
		comment := inverseMorphManyComment{CommentableType: "test_posts", CommentableID: post.ID}
		require.NoError(t, cloneRelationQuery(t, query).Create(&comment))
		require.NotZero(t, comment.ID)
		comments = append(comments, &comment)
	}

	return post, comments
}

// inverseMorphManyLoadCommentable reads the child's MorphTo relation (Laravel's lazy
// $comment->commentable read). The write-path ports use it because the relation writers do not
// apply the inverse — the write-back lives in the loaders — so re-reading is how they observe the
// link. Returns the resolved parent.
func inverseMorphManyLoadCommentable(t *testing.T, query contractsorm.Query, comment *inverseMorphManyComment) *inverseMorphManyPost {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Load(comment, "Commentable"))
	parent, ok := comment.Commentable.(*inverseMorphManyPost)
	require.True(t, ok, "the MorphTo load must resolve test_posts through the morph map")

	return parent
}

// inverseMorphManyAssertResolvesToParent is the write-path substitute for Laravel's
// `$comment->relationLoaded('commentable') && $comment->commentable === $post`: the child carries
// the parent's key and morph value, and an explicit Load of the child's Commentable relation
// resolves to the parent row — the writer only fills the FK and type columns, so key equality is
// the observable it leaves behind.
func inverseMorphManyAssertResolvesToParent(t *testing.T, query contractsorm.Query, comment *inverseMorphManyComment, post *inverseMorphManyPost) {
	t.Helper()

	assert.Equal(t, post.ID, comment.CommentableID)
	assert.Equal(t, "test_posts", comment.CommentableType)
	assert.Equal(t, post.ID, inverseMorphManyLoadCommentable(t, query, comment).ID)
}

// inverseMorphManyAssertChaperoned asserts the corpus's inverse pair for a loaded child:
// assertTrue($comment->relationLoaded('commentable')) and assertSame($post, $comment->commentable)
// — the load write-back stores the parent instance itself, so the child's relation costs no query.
func inverseMorphManyAssertChaperoned(t *testing.T, comment *inverseMorphManyComment, post *inverseMorphManyPost) {
	t.Helper()

	assert.Equal(t, post.ID, comment.CommentableID)
	assert.Equal(t, "test_posts", comment.CommentableType)
	require.NotNil(t, comment.Commentable, "the inverse declaration must link the child back to the parent")
	assert.Same(t, post, comment.Commentable)
}

// --- ports of the corpus -----------------------------------------------------

func TestInverseRelationMorphMany_MorphManyInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	for i := 0; i < 3; i++ {
		inverseMorphManySeedPostWithComments(t, query, 3)
	}

	// MorphManyInversePostModel::all().
	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: assertFalse($post->relationLoaded('comments')) — in Go the field stays
		// unpopulated until Load runs.
		assert.Nil(t, posts[i].Comments)

		// Laravel lazy-loads through property access ($post->comments); the port loads explicitly.
		require.NoError(t, cloneRelationQuery(t, query).Load(&posts[i], "Comments"))
		require.Len(t, posts[i].Comments, 3)

		for _, comment := range posts[i].Comments {
			// Laravel: assertTrue($comment->relationLoaded('commentable')) and
			// assertSame($post, $comment->commentable) — the inverse link, applied by the loader.
			inverseMorphManyAssertChaperoned(t, comment, &posts[i])
		}
	}
}

func TestInverseRelationMorphMany_MorphManyInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	for i := 0; i < 3; i++ {
		inverseMorphManySeedPostWithComments(t, query, 3)
	}

	// Laravel: MorphManyInversePostModel::with('comments')->get().
	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).With("Comments").Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: $post->getRelation('comments') — the eager load lands in the Comments field.
		require.Len(t, posts[i].Comments, 3)

		for _, comment := range posts[i].Comments {
			inverseMorphManyAssertChaperoned(t, comment, &posts[i])
		}
	}
}

func TestInverseRelationMorphMany_MorphManyGuessedInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	// ->inverse() with no argument guesses the inverse relation name — the oracle measured the
	// guess for this corpus as 'commentable'. The guessing is PHP-only relation-name introspection;
	// what this test also exercises, and the port runs, is the loading through the guessedComments
	// relation and back through the child's MorphTo.
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	for i := 0; i < 3; i++ {
		inverseMorphManySeedPostWithComments(t, query, 3)
	}

	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: assertFalse($post->relationLoaded('guessedComments')).
		assert.Nil(t, posts[i].GuessedComments)

		// $post->guessedComments — the lazy load, explicit in Go.
		require.NoError(t, cloneRelationQuery(t, query).Load(&posts[i], "GuessedComments"))
		require.Len(t, posts[i].GuessedComments, 3)

		for _, comment := range posts[i].GuessedComments {
			// Laravel: assertTrue($comment->relationLoaded('commentable')) and
			// assertSame($post, $comment->commentable) — the loader write-back.
			inverseMorphManyAssertChaperoned(t, comment, &posts[i])
		}
	}
}

func TestInverseRelationMorphMany_MorphManyGuessedInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	// See the lazy guessed test above: the guessed name is PHP-only introspection, the loading it
	// drives is what is ported.
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	for i := 0; i < 3; i++ {
		inverseMorphManySeedPostWithComments(t, query, 3)
	}

	// Laravel: MorphManyInversePostModel::with('guessedComments')->get().
	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).With("GuessedComments").Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: $post->getRelation('guessedComments').
		require.Len(t, posts[i].GuessedComments, 3)

		for _, comment := range posts[i].GuessedComments {
			inverseMorphManyAssertChaperoned(t, comment, &posts[i])
		}
	}
}

func TestInverseRelationMorphMany_MorphLatestOfManyInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	newest := map[uint]uint{}
	for i := 0; i < 3; i++ {
		post, comments := inverseMorphManySeedPostWithComments(t, query, 3)
		newest[post.ID] = comments[len(comments)-1].ID
	}

	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: assertFalse($post->relationLoaded('lastComment')) before the property read.
		assert.Nil(t, posts[i].LastComment)

		require.NoError(t, cloneRelationQuery(t, query).Load(&posts[i], "LastComment"))
		require.NotNil(t, posts[i].LastComment)

		// latestOfMany resolves to the post's highest-id comment — the MAX(id) aggregate join the
		// oracle prints behind the relation.
		assert.Equal(t, newest[posts[i].ID], posts[i].LastComment.ID)

		inverseMorphManyAssertChaperoned(t, posts[i].LastComment, &posts[i])
	}
}

func TestInverseRelationMorphMany_MorphLatestOfManyInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	newest := map[uint]uint{}
	for i := 0; i < 3; i++ {
		post, comments := inverseMorphManySeedPostWithComments(t, query, 3)
		newest[post.ID] = comments[len(comments)-1].ID
	}

	// Laravel: MorphManyInversePostModel::with('lastComment')->get().
	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).With("LastComment").Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: $post->getRelation('lastComment').
		require.NotNil(t, posts[i].LastComment)

		assert.Equal(t, newest[posts[i].ID], posts[i].LastComment.ID)

		inverseMorphManyAssertChaperoned(t, posts[i].LastComment, &posts[i])
	}
}

func TestInverseRelationMorphMany_MorphLatestOfManyGuessedInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	// See the lazy guessed test above: the guessed name is PHP-only introspection, the loading it
	// drives is what is ported.
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	newest := map[uint]uint{}
	for i := 0; i < 3; i++ {
		post, comments := inverseMorphManySeedPostWithComments(t, query, 3)
		newest[post.ID] = comments[len(comments)-1].ID
	}

	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: assertFalse($post->relationLoaded('guessedLastComment')) before the property
		// read.
		assert.Nil(t, posts[i].GuessedLastComment)

		require.NoError(t, cloneRelationQuery(t, query).Load(&posts[i], "GuessedLastComment"))
		require.NotNil(t, posts[i].GuessedLastComment)

		assert.Equal(t, newest[posts[i].ID], posts[i].GuessedLastComment.ID)

		inverseMorphManyAssertChaperoned(t, posts[i].GuessedLastComment, &posts[i])
	}
}

func TestInverseRelationMorphMany_MorphLatestOfManyGuessedInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	// See the lazy guessed test above: the guessed name is PHP-only introspection, the loading it
	// drives is what is ported.
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	newest := map[uint]uint{}
	for i := 0; i < 3; i++ {
		post, comments := inverseMorphManySeedPostWithComments(t, query, 3)
		newest[post.ID] = comments[len(comments)-1].ID
	}

	// Laravel: MorphManyInversePostModel::with('guessedLastComment')->get().
	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).With("GuessedLastComment").Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: $post->getRelation('guessedLastComment').
		require.NotNil(t, posts[i].GuessedLastComment)

		assert.Equal(t, newest[posts[i].ID], posts[i].GuessedLastComment.ID)

		inverseMorphManyAssertChaperoned(t, posts[i].GuessedLastComment, &posts[i])
	}
}

func TestInverseRelationMorphMany_MorphOneOfManyInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	oldest := map[uint]uint{}
	for i := 0; i < 3; i++ {
		post, comments := inverseMorphManySeedPostWithComments(t, query, 3)
		oldest[post.ID] = comments[0].ID
	}

	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: assertFalse($post->relationLoaded('firstComment')) before the property read.
		assert.Nil(t, posts[i].FirstComment)

		require.NoError(t, cloneRelationQuery(t, query).Load(&posts[i], "FirstComment"))
		require.NotNil(t, posts[i].FirstComment)

		// comments()->one() reads one row of the same polymorphic foreign-key query — the first
		// inserted.
		assert.Equal(t, oldest[posts[i].ID], posts[i].FirstComment.ID)

		inverseMorphManyAssertChaperoned(t, posts[i].FirstComment, &posts[i])
	}
}

func TestInverseRelationMorphMany_MorphOneOfManyInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	oldest := map[uint]uint{}
	for i := 0; i < 3; i++ {
		post, comments := inverseMorphManySeedPostWithComments(t, query, 3)
		oldest[post.ID] = comments[0].ID
	}

	// Laravel: MorphManyInversePostModel::with('firstComment')->get().
	posts := []inverseMorphManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphManyPost{}).With("FirstComment").Get(&posts))
	require.Len(t, posts, 3)

	for i := range posts {
		// Laravel: $post->getRelation('firstComment').
		require.NotNil(t, posts[i].FirstComment)

		assert.Equal(t, oldest[posts[i].ID], posts[i].FirstComment.ID)

		inverseMorphManyAssertChaperoned(t, posts[i].FirstComment, &posts[i])
	}
}

func TestInverseRelationMorphMany_MorphManyInverseRelationIsProperlySetToParentWhenMakingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	post := inverseMorphManyCreatePost(t, query)

	// Laravel: $post->comments()->makeMany(array_fill(0, 3, [])) — three new comments with the FK
	// preset and the inverse relation wired, never persisted. The writer's MakeMany applies no
	// inverse, so the port keeps its pinned not-persisted construction path: FindOrNew with a key
	// that cannot exist fills dest with a new instance of the related model and pre-sets the FK
	// (and morph type).
	comments := make([]inverseMorphManyComment, 3)
	for i := range comments {
		require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").FindOrNew(0, &comments[i]))
	}

	for i := range comments {
		assert.Zero(t, comments[i].ID, "makeMany() builds the children without persisting them")

		// The FK preset makeMany() writes from the parent: the parent key and the parent's morph
		// identity (the oracle: [parent class, parent key]; the Go morph map registers test_posts).
		assert.Equal(t, post.ID, comments[i].CommentableID)
		assert.Equal(t, "test_posts", comments[i].CommentableType)

		// Laravel: assertTrue($comment->relationLoaded('commentable')) and
		// assertSame($post, $comment->commentable). Load cannot run for an unsaved child (probe:
		// `id can't be empty` — the eager loader reads the child's own key), so
		// $comment->commentable goes through the relation read path, which resolves the MorphTo
		// from the in-memory FK columns.
		resolved := inverseMorphManyPost{}
		require.NoError(t, cloneRelationQuery(t, query).Related(&comments[i], "Commentable").First(&resolved))
		assert.Equal(t, post.ID, resolved.ID)
	}
}

func TestInverseRelationMorphMany_MorphManyInverseRelationIsProperlySetToParentWhenCreatingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	post := inverseMorphManyCreatePost(t, query)

	// Laravel: $post->comments()->createMany(array_fill(0, 3, [])) — each comment is created with
	// the FK set from the parent. The oracle measures the statement as `insert into "test_comments"
	// ("commentable_id", "commentable_type", "updated_at", "created_at") values (?, ?, ?, ?)`.
	comments := make([]inverseMorphManyComment, 3)
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").CreateMany(&comments))

	for i := range comments {
		require.NotZero(t, comments[i].ID)
		inverseMorphManyAssertResolvesToParent(t, query, &comments[i], &post)
	}
}

func TestInverseRelationMorphMany_MorphManyInverseRelationIsProperlySetToParentWhenCreatingManyQuietly(t *testing.T) {
	// Laravel: createManyQuietly — createMany inside Model::withoutEvents. RelationWriter has no
	// quiet variant, but quietness only suppresses model events, and the ORM's relation writes
	// dispatch none (the corpus registers no observers), so CreateMany is the observably identical
	// call.
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	post := inverseMorphManyCreatePost(t, query)

	comments := make([]inverseMorphManyComment, 3)
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").CreateMany(&comments))

	for i := range comments {
		require.NotZero(t, comments[i].ID)
		inverseMorphManyAssertResolvesToParent(t, query, &comments[i], &post)
	}
}

func TestInverseRelationMorphMany_MorphManyInverseRelationIsProperlySetToParentWhenSavingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	post := inverseMorphManyCreatePost(t, query)

	// Laravel: $comments = array_fill(0, 3, new MorphManyInverseCommentModel);
	// $post->comments()->saveMany($comments).
	comments := make([]inverseMorphManyComment, 3)
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").SaveMany(&comments))

	for i := range comments {
		require.NotZero(t, comments[i].ID)
		inverseMorphManyAssertResolvesToParent(t, query, &comments[i], &post)
	}
}

func TestInverseRelationMorphMany_MorphManyInverseRelationIsProperlySetToParentWhenUpdatingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphManySchema(t, query)

	post := inverseMorphManyCreatePost(t, query)

	// Laravel: MorphManyInverseCommentModel::factory()->count(3)->create() — the factory definition
	// gives each comment its own brand-new post (commentable_id => MorphManyInversePostModel::
	// factory()).
	comments := make([]*inverseMorphManyComment, 0, 3)
	for i := 0; i < 3; i++ {
		other := inverseMorphManyCreatePost(t, query)
		comment := inverseMorphManyComment{CommentableType: "test_posts", CommentableID: other.ID}
		require.NoError(t, cloneRelationQuery(t, query).Create(&comment))
		comments = append(comments, &comment)
	}

	for i := range comments {
		// Laravel: assertTrue($post->isNot($comment->commentable)) — reading $comment->commentable
		// now resolves to the factory's own post, not $post.
		assert.NotEqual(t, post.ID, inverseMorphManyLoadCommentable(t, query, comments[i]).ID)
	}

	// Laravel: $post->comments()->saveMany($comments) — re-parents the existing comments. The
	// oracle measures the statement as `update "test_comments" set "commentable_id" = ?,
	// "updated_at" = ? where "id" = ?`.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").SaveMany(&comments))

	for i := range comments {
		// Laravel: assertSame($post, $comment->commentable) — saveMany stamped the inverse link.
		inverseMorphManyAssertResolvesToParent(t, query, comments[i], &post)
	}
}
