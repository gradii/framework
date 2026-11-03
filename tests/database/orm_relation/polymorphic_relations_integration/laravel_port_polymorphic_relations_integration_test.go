package polymorphic_relations_integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentPolymorphicRelationsIntegrationTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestPolymorphicRelationsIntegration_<method without the test prefix>. The four tables mirror the
// corpus's createSchema() — posts, images and tags, each id + timestamps, and the taggables pivot
// carrying eloquent_many_to_many_polymorphic_test_tag_id / taggable_id / taggable_type — and the
// corpus's rows are created through the ORM with the same attach calls. Relations are loaded
// explicitly through OrmBuilder.Load and With, the harness's lazy-loading rule: $post->tags becomes
// Load(&post, "Tags"), since Go has no property-access magic.
//
// Every outcome this file asserts — the six morph-pivot counts, the two eager-load results, and the
// chunkById page/iteration behaviour the third test asserts — was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), which executed the
// corpus's scenarios against a real sqlite database; the SQL quoted in comments is the oracle's
// executed statement text, not recalled.

// --- models ------------------------------------------------------------------

// polyRelationPost ports EloquentManyToManyPolymorphicTestPost: the posts table (id + timestamps)
// with its tags() morphToMany relation. MorphClass pins the value written to and matched against
// taggable_type — the Go counterpart of the class name Laravel stores (the oracle's pivot rows
// carry "EloquentManyToManyPolymorphicTestPost").
type polyRelationPost struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Tags []*polyRelationTag `gorm:"-"`
}

func (polyRelationPost) TableName() string { return "posts" }

func (polyRelationPost) MorphClass() string { return "EloquentManyToManyPolymorphicTestPost" }

// Relations declares tags(): morphToMany(EloquentManyToManyPolymorphicTestTag::class, 'taggable').
// The corpus's pivot column for the related key is the class-derived
// eloquent_many_to_many_polymorphic_test_tag_id, so RelatedPivotKey is set explicitly; the morph id
// column taggable_id is the one the 'taggable' name derives.
func (polyRelationPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related:         &polyRelationTag{},
			Name:            "taggable",
			Table:           "taggables",
			ForeignPivotKey: "taggable_id",
			RelatedPivotKey: "eloquent_many_to_many_polymorphic_test_tag_id",
		},
	}
}

// polyRelationImage ports EloquentManyToManyPolymorphicTestImage: the images table with the same
// tags() morphToMany shape, distinguished only by its morph class.
type polyRelationImage struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Tags []*polyRelationTag `gorm:"-"`
}

func (polyRelationImage) TableName() string { return "images" }

func (polyRelationImage) MorphClass() string { return "EloquentManyToManyPolymorphicTestImage" }

func (polyRelationImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related:         &polyRelationTag{},
			Name:            "taggable",
			Table:           "taggables",
			ForeignPivotKey: "taggable_id",
			RelatedPivotKey: "eloquent_many_to_many_polymorphic_test_tag_id",
		},
	}
}

// polyRelationTag ports EloquentManyToManyPolymorphicTestTag: the tags table with its posts() and
// images() morphedByMany relations. For the inverse direction the Go declaration is parent-relative:
// ForeignPivotKey names the pivot column referencing the parent (the tag) and RelatedPivotKey the
// column referencing the related (post / image) — the roles Laravel swaps internally for
// morphedByMany, as the oracle's read
//
//	select "posts".*, ... from "posts" inner join "taggables" on "posts"."id" = "taggables"."taggable_id"
//	where "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" = ? and "taggables"."taggable_type" = ?
//
// shows. The morph value is resolved from the related model's MorphClass, which is what keeps
// $tag->posts and $tag->images apart.
type polyRelationTag struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts  []*polyRelationPost  `gorm:"-"`
	Images []*polyRelationImage `gorm:"-"`
}

func (polyRelationTag) TableName() string { return "tags" }

func (polyRelationTag) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.MorphedByMany{
			Related:         &polyRelationPost{},
			Name:            "taggable",
			Table:           "taggables",
			ForeignPivotKey: "eloquent_many_to_many_polymorphic_test_tag_id",
			RelatedPivotKey: "taggable_id",
		},
		"Images": contractsorm.MorphedByMany{
			Related:         &polyRelationImage{},
			Name:            "taggable",
			Table:           "taggables",
			ForeignPivotKey: "eloquent_many_to_many_polymorphic_test_tag_id",
			RelatedPivotKey: "taggable_id",
		},
	}
}

// polyRelationPivot is the read-back shape for the taggables table: it pins the morph id and type
// columns the attaches write (taggable_id and the class-name taggable_type the oracle measured, the
// corpus registering no morph map).
type polyRelationPivot struct {
	TagID        uint   `gorm:"column:eloquent_many_to_many_polymorphic_test_tag_id"`
	TaggableID   uint   `gorm:"column:taggable_id"`
	TaggableType string `gorm:"column:taggable_type"`
}

func (polyRelationPivot) TableName() string { return "taggables" }

// --- schema and seeding ------------------------------------------------------

// polyRelationSchema mirrors the corpus's createSchema() in sqlite DDL: three id + timestamps
// tables and the morph pivot, whose three columns the corpus's integer()/string() calls leave
// NOT NULL.
func polyRelationSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table posts (id integer primary key autoincrement, created_at datetime null, updated_at datetime null)`,
		`create table images (id integer primary key autoincrement, created_at datetime null, updated_at datetime null)`,
		`create table tags (id integer primary key autoincrement, created_at datetime null, updated_at datetime null)`,
		`create table taggables (eloquent_many_to_many_polymorphic_test_tag_id integer not null, taggable_id integer not null, taggable_type varchar not null)`,
	)
}

// polyRelationCreatePost ports EloquentManyToManyPolymorphicTestPost::create(): a row carrying the
// auto-assigned id and the automatic timestamps.
func polyRelationCreatePost(t *testing.T, query contractsorm.Query) polyRelationPost {
	t.Helper()

	post := polyRelationPost{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&post))
	require.NotZero(t, post.ID, "Create must read the generated key back")

	return post
}

// polyRelationCreateImage ports EloquentManyToManyPolymorphicTestImage::create().
func polyRelationCreateImage(t *testing.T, query contractsorm.Query) polyRelationImage {
	t.Helper()

	image := polyRelationImage{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&image))
	require.NotZero(t, image.ID, "Create must read the generated key back")

	return image
}

// polyRelationCreateTag ports EloquentManyToManyPolymorphicTestTag::create().
func polyRelationCreateTag(t *testing.T, query contractsorm.Query) polyRelationTag {
	t.Helper()

	tag := polyRelationTag{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&tag))
	require.NotZero(t, tag.ID, "Create must read the generated key back")

	return tag
}

// polyRelationAttach ports $parent->tags()->attach($id): writes one pivot row (the morph type from
// the parent's MorphClass) and skips an id already attached.
func polyRelationAttach(t *testing.T, query contractsorm.Query, parent any, id uint) {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Relation(parent, "Tags").Attach([]any{id}))
}

// --- ports of the corpus -----------------------------------------------------

// Laravel: create post / image / tag / tag2; $post->tags()->attach($tag->id) twice (tag and tag2),
// $image->tags()->attach($tag->id); then assertCount(2, $post->tags), (1, $image->tags),
// (1, $tag->posts), (1, $tag->images), (1, $tag2->posts) and (0, $tag2->images). Oracle (real sqlite
// execution): the six counts are exactly these; each attach compiles the insert
//
//	insert into "taggables" ("eloquent_many_to_many_polymorphic_test_tag_id", "taggable_id", "taggable_type") values (?, ?, ?)
//
// and each lazy read the morph-pinned join, e.g.
//
//	select "tags".*, ... from "tags" inner join "taggables" on "tags"."id" = "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?   (bindings [1, "EloquentManyToManyPolymorphicTestPost"])
//	select "images".*, ... from "images" inner join "taggables" on "images"."id" = "taggables"."taggable_id" where "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" = ? and "taggables"."taggable_type" = ?   (bindings [1, "EloquentManyToManyPolymorphicTestImage"])
//
// The tag->posts / tag->images split is what pins the type half: both pivots reference tag 1, and
// only the morph type separates the post's row from the image's; the port reads the three pivot
// rows back as well, pinning the stored taggable_id / class-name taggable_type values.
func TestPolymorphicRelationsIntegration_Creation(t *testing.T) {
	query := newRelationQuery(t)
	polyRelationSchema(t, query)

	post := polyRelationCreatePost(t, query)
	image := polyRelationCreateImage(t, query)
	tag := polyRelationCreateTag(t, query)
	tag2 := polyRelationCreateTag(t, query)

	polyRelationAttach(t, query, &post, tag.ID)
	polyRelationAttach(t, query, &post, tag2.ID)
	polyRelationAttach(t, query, &image, tag.ID)

	// $post->tags — the lazy load, explicit in Go; every Load re-reads.
	require.NoError(t, cloneRelationQuery(t, query).Load(&post, "Tags"))
	assert.Len(t, post.Tags, 2)

	require.NoError(t, cloneRelationQuery(t, query).Load(&image, "Tags"))
	assert.Len(t, image.Tags, 1)

	require.NoError(t, cloneRelationQuery(t, query).Load(&tag, "Posts"))
	assert.Len(t, tag.Posts, 1)

	require.NoError(t, cloneRelationQuery(t, query).Load(&tag, "Images"))
	assert.Len(t, tag.Images, 1)

	require.NoError(t, cloneRelationQuery(t, query).Load(&tag2, "Posts"))
	assert.Len(t, tag2.Posts, 1)

	// assertCount(0, $tag2->images): tag2 is attached to the post only, so the image-typed read is
	// empty.
	require.NoError(t, cloneRelationQuery(t, query).Load(&tag2, "Images"))
	assert.Len(t, tag2.Images, 0)

	// The pivot rows the three attaches wrote, as the oracle measured them on real sqlite: post 1
	// carries tags 1 and 2 typed "EloquentManyToManyPolymorphicTestPost", image 1 carries tag 1
	// typed "EloquentManyToManyPolymorphicTestImage" — the class names, since the corpus registers
	// no morph map. This pins the morph columns and their values, which the counts above can only
	// infer from the relation reads.
	pivots := []polyRelationPivot{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&polyRelationPivot{}).Get(&pivots))
	assert.ElementsMatch(t, []polyRelationPivot{
		{TagID: tag.ID, TaggableID: post.ID, TaggableType: "EloquentManyToManyPolymorphicTestPost"},
		{TagID: tag2.ID, TaggableID: post.ID, TaggableType: "EloquentManyToManyPolymorphicTestPost"},
		{TagID: tag.ID, TaggableID: image.ID, TaggableType: "EloquentManyToManyPolymorphicTestImage"},
	}, pivots)
}

// Laravel: post + tag, attach; Post::with('tags')->whereId(1)->first() and
// Tag::with('posts')->whereId(1)->first(); both relations are loaded and cross-reference the other
// row's key. Oracle (real sqlite execution): relationLoaded('tags') and relationLoaded('posts') are
// both true, with ids 1/1; the eager reads compile as
//
//	select "tags".*, ... where "taggables"."taggable_id" in (1) and "taggables"."taggable_type" = ?                              (bindings ["EloquentManyToManyPolymorphicTestPost"])
//	select "posts".*, ... where "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" in (1) and "taggables"."taggable_type" = ?   (bindings ["EloquentManyToManyPolymorphicTestPost"])
func TestPolymorphicRelationsIntegration_EagerLoading(t *testing.T) {
	query := newRelationQuery(t)
	polyRelationSchema(t, query)

	post := polyRelationCreatePost(t, query)
	tag := polyRelationCreateTag(t, query)
	polyRelationAttach(t, query, &post, tag.ID)

	// Laravel re-reads each row with with('…')->whereId(1)->first(). The port reads through Get:
	// First(&struct) leaves its single-row cursor open while retrieved() runs the eager query, which
	// deadlocks on the harness's one-connection pool, while Get drains its cursor before the eager
	// query runs. The observable is the same — the requested row, with the relation attached.
	posts := []polyRelationPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&polyRelationPost{}).With("Tags").Where("id", 1).Get(&posts))
	require.Len(t, posts, 1)

	tags := []polyRelationTag{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&polyRelationTag{}).With("Posts").Where("id", 1).Get(&tags))
	require.Len(t, tags, 1)

	// Laravel asserts relationLoaded('tags') / relationLoaded('posts'); in Go the eager load shows as
	// the relation field being populated by the query, before any explicit Load runs.
	require.Len(t, posts[0].Tags, 1)
	require.Len(t, tags[0].Posts, 1)
	assert.Equal(t, tag.ID, posts[0].Tags[0].ID)
	assert.Equal(t, post.ID, tags[0].Posts[0].ID)
}

// Laravel: post + tag1/tag2/tag3, attach all three; $post->tags()->chunkById(2, cb) — the callback
// fires twice, the pages' first ids are 1 and 3, and the final count is 3. Oracle (real sqlite
// execution): two pages; page 1
//
//	select "tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" as "pivot_eloquent_many_to_many_polymorphic_test_tag_id", "taggables"."taggable_type" as "pivot_taggable_type" from "tags" inner join "taggables" on "tags"."id" = "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ? and "tags"."id" is not null order by "tags"."id" asc limit 2
//
// with bindings [1, "EloquentManyToManyPolymorphicTestPost"]; page 2 replaces the is-not-null check
// by `and "tags"."id" > ?` (bindings [1, "EloquentManyToManyPolymorphicTestPost", 2]).
//
// The flip re-ran the oracle (php .claude/laravel-oracle/oracle.php /tmp/polyrel-chunk/oracle_stmts.php,
// illuminate/database v12.55.1): callbacks 2, pages [1, 2] / [3], total 3, exactly the two statements
// above. The port pins the same two page statements through the query log, Go-rendered: same select
// list, predicates, order and limit, with the join operand order reversed
// ("taggables"."eloquent_many_to_many_polymorphic_test_tag_id" = "tags"."id" where the oracle prints
// the other order) and the bindings inlined as literals.
func TestPolymorphicRelationsIntegration_ChunkById(t *testing.T) {
	query := newRelationQuery(t)
	polyRelationSchema(t, query)

	post := polyRelationCreatePost(t, query)
	tag := polyRelationCreateTag(t, query)
	tag2 := polyRelationCreateTag(t, query)
	tag3 := polyRelationCreateTag(t, query)
	polyRelationAttach(t, query, &post, tag.ID)
	polyRelationAttach(t, query, &post, tag2.ID)
	polyRelationAttach(t, query, &post, tag3.ID)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	tags := []polyRelationTag{}
	callbacks, total := 0, 0
	require.NoError(t, withContext.WithContext(ctx).Related(&post, "Tags").ChunkById(2, &tags, func(page int) error {
		callbacks++
		total += len(tags)
		require.Equal(t, callbacks, page, "the callback's page is the 1-based page number")

		ids := make([]uint, 0, len(tags))
		for _, item := range tags {
			ids = append(ids, item.ID)
		}

		switch callbacks {
		case 1:
			require.Equal(t, []uint{tag.ID, tag2.ID}, ids, "page 1 carries tags 1 and 2")
		case 2:
			require.Equal(t, []uint{tag3.ID}, ids, "page 2 carries tag 3")
		}

		return nil
	}))
	assert.Equal(t, 2, callbacks, "chunkById(2) fires the callback once per page")
	assert.Equal(t, 3, total, "the walk scans all three attached tags")

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "chunkById(2) over three attached tags issues two page statements")
	assert.Equal(t, `select "tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" as "pivot_eloquent_many_to_many_polymorphic_test_tag_id", "taggables"."taggable_type" as "pivot_taggable_type" from "tags" inner join "taggables" ON "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" = "tags"."id" where "taggables"."taggable_id" = 1 and "taggables"."taggable_type" = 'EloquentManyToManyPolymorphicTestPost' and "tags"."id" is not null order by "tags"."id" asc limit 2`, logs[0].Query)
	assert.Equal(t, `select "tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" as "pivot_eloquent_many_to_many_polymorphic_test_tag_id", "taggables"."taggable_type" as "pivot_taggable_type" from "tags" inner join "taggables" ON "taggables"."eloquent_many_to_many_polymorphic_test_tag_id" = "tags"."id" where "taggables"."taggable_id" = 1 and "taggables"."taggable_type" = 'EloquentManyToManyPolymorphicTestPost' and "tags"."id" > 2 order by "tags"."id" asc limit 2`, logs[1].Query)
}
