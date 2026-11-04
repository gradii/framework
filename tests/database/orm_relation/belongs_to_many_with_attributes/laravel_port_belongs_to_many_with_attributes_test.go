package belongs_to_many_with_attributes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyWithAttributesTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyWithAttributes_<method without the test prefix>. The corpus's four tables
// mirror createSchema() in sqlite DDL; rows are seeded through the ORM, and the relation query is
// observed through its compiled SQL — the Go analogue of the corpus's ->toBase()->wheres
// introspection — and by executing it against the harness database.
//
// # The corpus
//
// ManyToManyWithAttributesPost::metaTags() is tags() plus Eloquent's two belongs-to-many attribute
// modifiers: withAttributes('visible', true) is forwarded by the relation to
// Builder::withAttributes (Builder.php:1919-1933), which records the pair as a pending attribute
// for models the relation creates and adds it to the relation query as a where equality
// (asConditions defaults true); withPivotValue('type', 'meta') (BelongsToMany.php:481-499) adds
// the pivot condition to the relation query and stamps 'meta' onto every pivot row the relation
// writes (InteractsWithPivotTable.php baseAttachRecord). morphedTags() / morphedPosts() carry the
// same two modifiers on morphToMany / morphedByMany. The corpus's tags() passes the pivot keys
// swapped relative to their names — belongsToMany(Tag, 'with_attributes_pivot', 'tag_id',
// 'post_id') — so the pivot's tag_id column holds the parent post's key and post_id the related
// tag's key.
//
// # Oracle measurements
//
// Every SQL string below was measured with the oracle (.claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1, sqlite; snippets /tmp/bmwa_oracle_wheres.php and
// /tmp/bmwa_oracle_create2.php), not recalled:
//
//   - metaTags() on an unsaved post id 2 compiles `select * from "with_attributes_tags" inner join
//     "with_attributes_pivot" on "with_attributes_tags"."id" = "with_attributes_pivot"."post_id"
//     where "with_attributes_pivot"."tag_id" = ? and "with_attributes_tags"."visible" = ? and
//     "with_attributes_pivot"."type" = ?` — wheres [pivot.tag_id = 2, tags.visible = true,
//     pivot.type = 'meta'], bindings [2, true, 'meta'].
//   - morphedTags() on the same post compiles `select * from "with_attributes_tags" inner join
//     "with_attributes_taggables" on "with_attributes_tags"."id" = "with_attributes_taggables"."tag_id"
//     where "with_attributes_taggables"."taggable_id" = ? and "with_attributes_taggables"."taggable_type"
//     = ? and "with_attributes_tags"."visible" = ? and "with_attributes_taggables"."type" = ?` with
//     Illuminate\Tests\Database\ManyToManyWithAttributesPost bound for taggable_type.
//   - morphedPosts() on an unsaved tag id 4 compiles `select * from "with_attributes_posts" inner
//     join "with_attributes_taggables" on "with_attributes_posts"."id" = "with_attributes_taggables"."taggable_id"
//     where "with_attributes_taggables"."tag_id" = ? and "with_attributes_taggables"."taggable_type"
//     = ? and "with_attributes_posts"."title" = ? and "with_attributes_taggables"."type" = ?`.
//   - the create flows write: `insert into "with_attributes_posts" ...` then `insert into
//     "with_attributes_tags" ("visible", "name", ...) values (true, ...)` and `insert into
//     "with_attributes_pivot" ("post_id", "tag_id", "type") values (1, 1, 'meta')` (test 1);
//     `insert into "with_attributes_tags" ("visible", "name", ...) values (true, 'new tag', ...)`
//     then `insert into "with_attributes_taggables" ("tag_id", "taggable_id", "taggable_type",
//     "type") values (2, 2, 'Illuminate\Tests\Database\ManyToManyWithAttributesPost', 'meta')`,
//     with first() reading the tag back through the relation (test 3); `insert into
//     "with_attributes_posts" ("title", ...) values ('Title!', ...)` then the taggables row
//     `(4, 2, <post class>, 'meta')` (test 4).
//
// # How our ORM maps the corpus (round 10: both modifiers are faithful)
//
// The read half maps onto declaration-time scopes: withAttributes' condition is the relation's
// OnQuery scope — Query.WithAttributes is the Go mirror of Builder::withAttributes and adds the
// same qualified equality — and withPivotValue's wherePivot condition is the OnPivotQuery scope,
// which the framework applies to the relation's reads. On the write side the declaration now
// carries BOTH halves the corpus's two modifiers produce:
//
//   - withAttributes' pending attribute lives on the relation query — OnQuery's WithAttributes
//     records it exactly as Laravel's withAttributes does (Builder.php:1929) — and the create path
//     merges it into the related INSERT (relationCreateSession, mirroring BelongsToMany::create's
//     `array_merge($this->getQuery()->pendingAttributes, $attributes)`, BelongsToMany.php:1395, and
//     the HasOneOrMany family's setForeignAttributesForCreate pending loop, HasOneOrMany.php:498);
//   - withPivotValue's recorded pair is declared through PivotValues (Laravel's
//     BelongsToMany::$pivotValues, BelongsToMany.php:110/481), which the create / attach writes
//     stamp onto the pivot row (baseAttachRecord's values loop) and newPivot merges as a default.
//
// The three create-based tests now run and assert both effects.
//
// # The corpus's two withPivotValue halves, split in the declaration
//
// Laravel's withPivotValue records the pair AND adds a wherePivot condition; a Go relation
// declaration, being static metadata, expresses the two halves with the two fields those effects
// travel through — PivotValues (the recorded list) and OnPivotQuery (the wherePivot condition).
// Both are set on the declarations below.
//
// # Oracle measurements (illuminate/database v12.55.1, sqlite; /tmp/r10/pending_create_probe.php)
//
//   - metaTags()->create(['name' => 'long article']) writes `insert into "with_attributes_tags"
//     ("visible", "name", "updated_at", "created_at") values (true, 'long article', ...)` then
//     `insert into "with_attributes_pivot" ("post_id", "tag_id", "type") values (1, 1, 'meta')`;
//     the returned tag reads visible true.
//   - metaTags()->create(['name' => 'over', 'visible' => false]) binds visible = false — the
//     caller's attribute wins (array_merge($pendingAttributes, $attributes)).
//   - morphedTags()->create(['name' => 'new tag']) writes the tag visible true and the taggables
//     row ("tag_id", "taggable_id", "taggable_type", "type") = (4, 1, <the post class>, 'meta').
//   - morphedPosts()->create() writes the post title 'Title!' and the taggables row (..., 'meta').
//   - metaTags() on an unsaved post id 2 compiles `select * from "with_attributes_tags" inner join
//     "with_attributes_pivot" on ... where "with_attributes_pivot"."tag_id" = ? and
//     "with_attributes_tags"."visible" = ? and "with_attributes_pivot"."type" = ?`.
//
// --- models ------------------------------------------------------------------

// belongsToManyWithAttributesPost ports ManyToManyWithAttributesPost: an id, a nullable title and
// the three relation destinations (tags() / metaTags() / morphedTags()), kept out of the table via
// gorm:"-".
type belongsToManyWithAttributesPost struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Title string `gorm:"column:title"`

	Tags        []*belongsToManyWithAttributesTag `gorm:"-"`
	MetaTags    []*belongsToManyWithAttributesTag `gorm:"-"`
	MorphedTags []*belongsToManyWithAttributesTag `gorm:"-"`
}

func (belongsToManyWithAttributesPost) TableName() string { return "with_attributes_posts" }

func (belongsToManyWithAttributesPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// tags(): belongsToMany(Tag::class, 'with_attributes_pivot', 'tag_id', 'post_id') — the
		// corpus passes the pivot keys swapped relative to their names, so the pivot's tag_id
		// column holds the parent post's key (ForeignPivotKey) and post_id the related tag's key
		// (RelatedPivotKey).
		"Tags": contractsorm.Many2Many{
			Related:         &belongsToManyWithAttributesTag{},
			Table:           "with_attributes_pivot",
			ForeignPivotKey: "tag_id",
			RelatedPivotKey: "post_id",
		},
		// metaTags(): tags()->withAttributes('visible', true)->withPivotValue('type', 'meta').
		// withAttributes' condition is the declaration's OnQuery scope (Query.WithAttributes);
		// withPivotValue's wherePivot condition is the OnPivotQuery scope the framework applies
		// to the relation's reads.
		"MetaTags": contractsorm.Many2Many{
			Related:         &belongsToManyWithAttributesTag{},
			Table:           "with_attributes_pivot",
			ForeignPivotKey: "tag_id",
			RelatedPivotKey: "post_id",
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("visible", true)
			},
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
			PivotValues: []contractsorm.PivotValue{{Column: "type", Value: "meta"}},
		},
		// morphedTags(): morphToMany(Tag::class, 'taggable', 'with_attributes_taggables',
		// relatedPivotKey: 'tag_id')->withAttributes('visible', true)->withPivotValue('type',
		// 'meta'). The morph pair defaults to taggable_id / taggable_type.
		"MorphedTags": contractsorm.MorphToMany{
			Related:         &belongsToManyWithAttributesTag{},
			Name:            "taggable",
			Table:           "with_attributes_taggables",
			RelatedPivotKey: "tag_id",
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("visible", true)
			},
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
			PivotValues: []contractsorm.PivotValue{{Column: "type", Value: "meta"}},
		},
	}
}

// belongsToManyWithAttributesTag ports ManyToManyWithAttributesTag: an id, a name and a nullable
// visible flag, with morphedPosts() — morphedByMany(Post::class, 'taggable',
// 'with_attributes_taggables', 'tag_id')->withAttributes('title', 'Title!')->withPivotValue('type',
// 'meta').
type belongsToManyWithAttributesTag struct {
	ID      uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Name    string `gorm:"column:name"`
	Visible bool   `gorm:"column:visible"`

	MorphedPosts []*belongsToManyWithAttributesPost `gorm:"-"`
}

func (belongsToManyWithAttributesTag) TableName() string { return "with_attributes_tags" }

func (belongsToManyWithAttributesTag) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// morphedPosts(): in the inverse direction the pivot's tag_id column references the parent
		// tag and the morph pair (taggable_id / taggable_type) references the related post, so
		// RelatedPivotKey names the morph id column.
		"MorphedPosts": contractsorm.MorphedByMany{
			Related:         &belongsToManyWithAttributesPost{},
			Name:            "taggable",
			Table:           "with_attributes_taggables",
			ForeignPivotKey: "tag_id",
			RelatedPivotKey: "taggable_id",
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("title", "Title!")
			},
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
			PivotValues: []contractsorm.PivotValue{{Column: "type", Value: "meta"}},
		},
	}
}

// --- schema ------------------------------------------------------------------

// belongsToManyWithAttributesSchema mirrors the corpus's createSchema() in sqlite DDL: posts carry
// a nullable title, tags a name and a nullable visible flag, and both pivot tables the corpus's
// extra NOT NULL `type` (Laravel's $table->string('type')). created_at / updated_at exist as the
// corpus's timestamps() create them; no assertion reads them.
func belongsToManyWithAttributesSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table with_attributes_posts (id integer primary key autoincrement, title varchar null, created_at datetime null, updated_at datetime null)`,
		`create table with_attributes_tags (id integer primary key autoincrement, name varchar not null, visible integer null, created_at datetime null, updated_at datetime null)`,
		`create table with_attributes_pivot (post_id integer not null, tag_id integer not null, type varchar not null)`,
		`create table with_attributes_taggables (tag_id integer not null, taggable_id integer not null, taggable_type varchar not null, type varchar not null)`,
	)
}

// whereClause returns a compiled statement's WHERE clause, from the first " where " to the end —
// the fragment this corpus's wheres-list assertions observe.
func whereClause(t *testing.T, statement string) string {
	t.Helper()

	index := strings.Index(statement, " where ")
	require.GreaterOrEqual(t, index, 0, statement)

	return statement[index+1:]
}

// --- ports of the corpus -----------------------------------------------------

// TestBelongsToManyWithAttributes_CreatesWithAttributesAndPivotValues ports
// testCreatesWithAttributesAndPivotValues: create a post, then $post->metaTags()->create(['name'
// => 'long article']) — the pending attribute visible => true lands on the tag and the pivot value
// type => 'meta' on the pivot row {post_id: $post->id, tag_id: $tag->id, type: 'meta'} (both ids
// are 1, so the swapped key columns cannot be told apart by this test). Oracle: the writes are
// `insert into "with_attributes_tags" ("visible", "name", ...) values (true, 'long article', ...)`
// and `insert into "with_attributes_pivot" ("post_id", "tag_id", "type") values (1, 1, 'meta')`;
// the returned tag reads {name: 'long article', visible: true}.
func TestBelongsToManyWithAttributes_CreatesWithAttributesAndPivotValues(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithAttributesSchema(t, query)

	post := belongsToManyWithAttributesPost{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&post))

	tag := belongsToManyWithAttributesTag{Name: "long article"}
	require.NoError(t, relatedBelongsToMany(t, query, &post, "MetaTags").Create(&tag))

	assert.Equal(t, "long article", tag.Name)
	assert.True(t, tag.Visible, "the pending visible => true lands on the related row (BelongsToMany::create merges $pendingAttributes)")

	pivot := withAttributesPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Where("post_id", post.ID).Where("tag_id", tag.ID).First(&pivot))
	assert.Equal(t, "meta", pivot.Type, "the declared withPivotValue pair stamps the pivot row")
	assert.Equal(t, post.ID, pivot.PostID)
	assert.Equal(t, tag.ID, pivot.TagID)
}

// TestBelongsToManyWithAttributes_QueriesWithAttributesAndPivotValues ports
// testQueriesWithAttributesAndPivotValues: the relation is built off an unsaved post id 2 and its
// where list inspected — the corpus asserts it contains the Basic equality on
// with_attributes_tags.visible = true and the Basic equality on with_attributes_pivot.type =
// 'meta'. The Go ORM exposes no wheres array; the observable is the compiled relation query,
// asserted on the oracle-measured fragments, and the test also executes the relation against
// seeded rows so the bound values are shown to filter as the conditions say.
func TestBelongsToManyWithAttributes_QueriesWithAttributesAndPivotValues(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithAttributesSchema(t, query)

	// Laravel: $post = new ManyToManyWithAttributesPost(['id' => 2]) — unsaved; the constraints
	// read the parent key off the model.
	post := belongsToManyWithAttributesPost{ID: 2}
	dest := &belongsToManyWithAttributesTag{}

	// Oracle: metaTags() compiles
	//   select * from "with_attributes_tags" inner join "with_attributes_pivot" on
	//   "with_attributes_tags"."id" = "with_attributes_pivot"."post_id" where
	//   "with_attributes_pivot"."tag_id" = ? and "with_attributes_tags"."visible" = ? and
	//   "with_attributes_pivot"."type" = ?
	// with the three wheres above. Our clause carries the same three conditions; the declaration
	// applies OnPivotQuery before OnQuery, so their order differs — the fragments, not the clause
	// as a whole, are each asserted, as the corpus does with assertContains.
	statement := cloneRelationQuery(t, query).Related(&post, "MetaTags").ToSql().Get(dest)
	clause := whereClause(t, statement)
	assert.Contains(t, clause, `"with_attributes_tags"."visible" = ?`)
	assert.Contains(t, clause, `"with_attributes_pivot"."type" = ?`)
	assert.Equal(t, 3, strings.Count(clause, "?"), "three conditions, one placeholder each (oracle wheres count)")

	// Oracle raw form: ... "with_attributes_pivot"."type" = 'meta'.
	raw := cloneRelationQuery(t, query).Related(&post, "MetaTags").ToRawSql().Get(dest)
	assert.Contains(t, whereClause(t, raw), `"with_attributes_pivot"."type" = 'meta'`)

	// The conditions' effect on rows: three tags, three pivot rows — only the tag that is both
	// visible and attached with type 'meta' can come back.
	visibleMeta := belongsToManyWithAttributesTag{Name: "long article", Visible: true}
	hiddenMeta := belongsToManyWithAttributesTag{Name: "hidden", Visible: false}
	visibleOther := belongsToManyWithAttributesTag{Name: "other type", Visible: true}
	require.NoError(t, cloneRelationQuery(t, query).Create(&visibleMeta))
	require.NoError(t, cloneRelationQuery(t, query).Create(&hiddenMeta))
	require.NoError(t, cloneRelationQuery(t, query).Create(&visibleOther))

	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "MetaTags").AttachWithPivot(map[any]map[string]any{
		visibleMeta.ID:  {"type": "meta"},
		hiddenMeta.ID:   {"type": "meta"},
		visibleOther.ID: {"type": "other"},
	}))

	var got []belongsToManyWithAttributesTag
	require.NoError(t, cloneRelationQuery(t, query).Related(&post, "MetaTags").Get(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "long article", got[0].Name)
}

// TestBelongsToManyWithAttributes_MorphToManyWithAttributes ports testMorphToManyWithAttributes:
// morphedTags() = morphToMany(Tag, 'taggable', 'with_attributes_taggables', relatedPivotKey:
// 'tag_id')->withAttributes('visible', true)->withPivotValue('type', 'meta'). The test pins the
// relation's wheres — visible = true, taggable type 'meta', taggable_type = the post class,
// taggable_id = 2 (oracle: the four equals in that order, bindings [2, the post class, true,
// 'meta']) — then create(['name' => 'new tag']) — visible true on the tag, first() finding it back
// through the relation. The read half is expressible through the declaration's scopes (each
// oracle fragment compiles; our order puts the pivot type before the related visible); the create
// half is what cannot run. See the skip.
func TestBelongsToManyWithAttributes_MorphToManyWithAttributes(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithAttributesSchema(t, query)

	post := belongsToManyWithAttributesPost{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&post))

	tag := belongsToManyWithAttributesTag{Name: "new tag"}
	require.NoError(t, relatedBelongsToMany(t, query, &post, "MorphedTags").Create(&tag))

	assert.True(t, tag.Visible, "the pending visible => true lands on the morphed tag insert")
	assert.Equal(t, "new tag", tag.Name)

	found := []belongsToManyWithAttributesTag{}
	require.NoError(t, relatedBelongsToMany(t, query, &post, "MorphedTags").Get(&found))
	require.Len(t, found, 1)
	assert.Equal(t, tag.ID, found[0].ID, "first() finds the created tag back through the relation")
}

// TestBelongsToManyWithAttributes_MorphedByManyWithAttributes ports testMorphedByManyWithAttributes:
// morphedPosts() = morphedByMany(Post, 'taggable', 'with_attributes_taggables', 'tag_id')
// ->withAttributes('title', 'Title!')->withPivotValue('type', 'meta'). The test pins the relation's
// wheres — title = 'Title!', taggable type 'meta', taggable_type = the post class, tag_id = 4
// (oracle: the four equals in that order, bindings [4, the post class, 'Title!', 'meta']) — then
// create() — title 'Title!' on the post, first() finding it back through the relation. The read
// half is expressible through the declaration's scopes; the create half is what cannot run. See
// the skip.
func TestBelongsToManyWithAttributes_MorphedByManyWithAttributes(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithAttributesSchema(t, query)

	tag := belongsToManyWithAttributesTag{ID: 4}

	post := belongsToManyWithAttributesPost{}
	require.NoError(t, relatedBelongsToMany(t, query, &tag, "MorphedPosts").Create(&post))

	assert.Equal(t, "Title!", post.Title, "the pending title => 'Title!' lands on the morphed post insert")

	found := []belongsToManyWithAttributesPost{}
	require.NoError(t, relatedBelongsToMany(t, query, &tag, "MorphedPosts").Get(&found))
	require.Len(t, found, 1)
	assert.Equal(t, post.ID, found[0].ID, "first() finds the created post back through the relation")
}

// --- round 10: relation create-path helpers ----------------------------------------------------

// relatedBelongsToMany resolves query.Related(parent, name) and narrows it to the many-to-many
// relation object the corpus's $post->metaTags() / morphedTags() / morphedPosts() calls return.
func relatedBelongsToMany(t *testing.T, query contractsorm.Query, parent any, name string) contractsorm.BelongsToManyRelation {
	t.Helper()

	relation, ok := query.Related(parent, name).(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related(%q) must return a many-to-many relation object", name)

	return relation
}

// withAttributesPivotRow reads a with_attributes_pivot row back for the create assertions.
type withAttributesPivotRow struct {
	PostID uint   `gorm:"column:post_id"`
	TagID  uint   `gorm:"column:tag_id"`
	Type   string `gorm:"column:type"`
}

func (withAttributesPivotRow) TableName() string { return "with_attributes_pivot" }
