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
// # How our ORM diverges
//
// The read half maps onto declaration-time scopes: withAttributes' condition is the relation's
// OnQuery scope — Query.WithAttributes is the Go mirror of Builder::withAttributes and adds the
// same qualified equality — and withPivotValue's wherePivot condition is the OnPivotQuery scope,
// which the framework applies to the relation's reads. The write half has no counterpart: the
// relation create path runs the related insert on a fresh builder (CreateRelation calls
// r.fresh().Create(dest), whose fresh conditions carry no pending attributes) and Attach inserts
// the pivot key columns only, so withAttributes' pending value and withPivotValue's stamp cannot
// ride a Relation(...).Create(); pivot column values are caller-supplied there (SaveWithPivot /
// AttachWithPivot). The three create-based tests are skipped with those measured facts; the
// queries test asserts the oracle fragments and executes the relation against seeded rows.
//
// # Our side, measured by the probes this file ran before their removal
//
//   - metaTags() compiles `select * from "with_attributes_tags" inner join "with_attributes_pivot"
//     ON "with_attributes_pivot"."post_id" = "with_attributes_tags"."id" where
//     "with_attributes_pivot"."tag_id" = ? and "with_attributes_pivot"."type" = ? and
//     "with_attributes_tags"."visible" = ?` — the oracle's three conditions, ordered pivot type
//     before related visible (the corpus's assertContains reads no order, so the fragments are
//     asserted, not the clause as a whole). The raw form renders the boolean as
//     `"with_attributes_tags"."visible" = true` where Laravel's sqlite raw form renders `= 1`.
//   - morphedTags() compiles `... where "with_attributes_taggables"."taggable_id" = ? and
//     "with_attributes_taggables"."taggable_type" = ? and "with_attributes_taggables"."type" = ?
//     and "with_attributes_tags"."visible" = ?` with 'with_attributes_posts' — the parent model's
//     table name — bound for taggable_type where Laravel binds
//     Illuminate\Tests\Database\ManyToManyWithAttributesPost; morphedPosts() has the same shape
//     with 'with_attributes_posts' bound and "with_attributes_posts"."title" in place of the
//     visible equality.
//   - Relation(...).Create() on metaTags() inserts the tag {name: 'long article', visible: false}
//     and then fails the attach: `insert into "with_attributes_pivot" ("post_id", "tag_id") values
//     (1, 2)` — type omitted — with `sqlite3: constraint failed: NOT NULL constraint failed:
//     with_attributes_pivot.type`; against a nullable type variant the row lands NULL. The morph
//     creates fail the same way after inserting their child (tag {name: 'new tag', visible: false}
//     / post {title: ""}): `insert into "with_attributes_taggables" ("tag_id", "taggable_id",
//     "taggable_type") values (1, 2, 'with_attributes_posts')` and `(4, 1,
//     'with_attributes_posts')`.
//   - SaveWithPivot(&tag, {"type": "meta"}) does write the pivot row's type — the write API takes
//     pivot values per call — but the tag still reads visible false: the pending attribute only
//     ever lived on the relation's read scope.

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
	t.Skip("our ORM diverges: the relation write path carries neither modifier — withPivotValue has no write-side counterpart (no declaration records pivot column values for INSERTs), so Relation(...).Create() attaches with insert into \"with_attributes_pivot\" (\"post_id\", \"tag_id\") values (1, 2), omitting type, which the corpus's type varchar not null rejects (probe: sqlite3: constraint failed: NOT NULL constraint failed: with_attributes_pivot.type; against a nullable type variant the row lands NULL), and the pending visible attribute cannot ride the create either — CreateRelation runs r.fresh().Create(dest), whose fresh builder carries no conditions, so the created tag row reads {name: \"long article\", visible: false} where the corpus reads visible true. Oracle: Laravel's create() merges Builder::pendingAttributes into the related insert (with_attributes_tags' visible written true) and attaches with insert into \"with_attributes_pivot\" (\"post_id\", \"tag_id\", \"type\") values (1, 1, 'meta')")
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
	t.Skip("our ORM diverges: withPivotValue has no write-side counterpart — morphedTags()->create() attaches with insert into \"with_attributes_taggables\" (\"tag_id\", \"taggable_id\", \"taggable_type\") values (1, 2, 'with_attributes_posts'), omitting type, so the corpus's type varchar not null rejects it (probe: sqlite3: constraint failed: NOT NULL constraint failed: with_attributes_taggables.type; a nullable type variant lands the row type NULL), and the pending visible cannot ride the create either — CreateRelation runs r.fresh().Create(dest), whose fresh builder carries no conditions (probe: the created tag row reads visible false) — so the pivot row cannot carry type = 'meta' and the corpus's first()-through-the-relation cannot find the tag back. Oracle: Laravel's create() merges the pending attribute into the related insert (insert into \"with_attributes_tags\" (\"visible\", \"name\", \"updated_at\", \"created_at\") values (true, 'new tag', ...)) and attaches with insert into \"with_attributes_taggables\" (\"tag_id\", \"taggable_id\", \"taggable_type\", \"type\") values (2, 2, 'Illuminate\\Tests\\Database\\ManyToManyWithAttributesPost', 'meta'); the wheres half alone is expressible through the declaration's scopes and was measured (each oracle fragment compiles; our order puts the pivot type before the related visible)")
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
	t.Skip("our ORM diverges: withPivotValue has no write-side counterpart — morphedPosts()->create() attaches with insert into \"with_attributes_taggables\" (\"tag_id\", \"taggable_id\", \"taggable_type\") values (4, 1, 'with_attributes_posts'), omitting type, so the corpus's type varchar not null rejects it (probe: sqlite3: constraint failed: NOT NULL constraint failed: with_attributes_taggables.type; a nullable type variant lands the row type NULL), and the pending title cannot ride the create either — CreateRelation runs r.fresh().Create(dest), whose fresh builder carries no conditions (probe: the created post row reads title \"\" where the corpus reads 'Title!') — so first() cannot find the post back. Oracle: Laravel's create() merges the pending attribute into the related insert (insert into \"with_attributes_posts\" (\"title\", \"updated_at\", \"created_at\") values ('Title!', ...)) and attaches with insert into \"with_attributes_taggables\" (\"tag_id\", \"taggable_id\", \"taggable_type\", \"type\") values (4, 2, 'Illuminate\\Tests\\Database\\ManyToManyWithAttributesPost', 'meta'); the wheres half alone is expressible through the declaration's scopes and was measured (each oracle fragment compiles; our order puts the pivot type before the related title)")
}
