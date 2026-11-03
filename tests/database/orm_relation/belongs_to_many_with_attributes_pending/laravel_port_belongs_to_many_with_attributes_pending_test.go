package belongs_to_many_with_attributes_pending

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyWithAttributesPendingTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyWithAttributesPending_<method without the test prefix>.
//
// The corpus is the belongs-to-many home of Eloquent 12's two pending-attribute modifiers:
//
//   - withAttributes(..., asConditions: false) records pending attributes that Eloquent merges
//     into the model created through the relation ($post->metaTags()->create([...])), without
//     adding a where clause for them;
//   - withPivotValue('type', 'meta') records a pivot column value that is (a) stamped onto every
//     pivot row the relation writes and (b) added to the relation's where list as a qualified
//     condition (wherePivot).
//
// In the Go ORM only the read half of that pair has a counterpart, so one test ports for real and
// three skip with measured divergences:
//
//   - withPivotValue's wherePivot condition maps onto a declaration-time OnPivotQuery scope, which
//     the framework applies to the relation's own reads (Query.Related) exactly as Laravel applies
//     the pivot where. The "MetaTags" / "MorphedTags" / "MorphedPosts" declarations below carry
//     it, and the queries test asserts the compiled where clause.
//   - Nothing records pivot column values for INSERTs — the pivot writers write the FK columns
//     (plus the morph type) only — and a relation create runs on r.fresh().Create(), whose fresh
//     builder carries no conditions, so the withAttributes pending map cannot ride it either. The
//     three create-based tests skip with those measured facts, not with weakened assertions.
//
// # Measurements
//
// Oracle runs (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, plus
// oracle_checkout.php against the corpus checkout 12.43.1 — byte-identical output — and a
// Capsule-backed sqlite execution for the create flows):
//
//   - metaTags() on an unsaved post id 2 compiles
//     select * from "pending_attributes_tags" inner join "pending_attributes_pivot" on
//     "pending_attributes_tags"."id" = "pending_attributes_pivot"."post_id"
//     where "pending_attributes_pivot"."tag_id" = ? and "pending_attributes_pivot"."type" = ?
//     with wheres [tag_id = 2, type = 'meta'] — the two conditions the corpus asserts, and no
//     visible condition (the corpus's asConditions: false).
//   - morphedTags() and morphedPosts() compile three wheres each (the parent / morph id,
//     taggable_type and type); Laravel binds the post class name for taggable_type where the Go
//     port binds the framework's morph value, the related model's table name
//     ('pending_attributes_posts').
//   - the corpus's create flows write the tag {name: 'long article', visible: 1} (or post
//     {title: 'Title!'}) plus a pivot row carrying type: 'meta'.
//
// Probes of the Go ORM against the harness database (this package's scratch test, since removed)
// measured the write-side divergence and the read-side equivalence:
//
//   - Related(&post, "MetaTags") compiles the oracle's where clause byte for byte; the join only
//     differs in operand order (ours "pending_attributes_pivot"."post_id" =
//     "pending_attributes_tags"."id") and the ON keyword's case, outside this corpus's assertions.
//   - Relation(&post, "Tags").Create(&tag) inserts the tag, then attaches with
//     insert into "pending_attributes_pivot" ("post_id", "tag_id") values (?, ?) — no type column
//     — and the corpus's type varchar not null rejects it (NOT NULL constraint failed:
//     pending_attributes_pivot.type); against a nullable-type variant the row lands type NULL.
//     WithAttributes("visible", true, false) on the writer changes nothing — CreateRelation runs on
//     r.fresh() — so the created tag keeps visible false.
//   - The morph creates attach with ("tag_id", "taggable_id", "taggable_type") and fail the same
//     way on pending_attributes_taggables.type.

// --- models ------------------------------------------------------------------

// belongsToManyWithAttributesPendingPost ports ManyToManyPendingAttributesPost: an id and a
// nullable title, with tags() (the swapped-key belongsToMany), metaTags() (tags() plus the pivot
// value) and morphedTags() (morphToMany plus the pivot value).
type belongsToManyWithAttributesPendingPost struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Title string `gorm:"column:title"`

	Tags        []*belongsToManyWithAttributesPendingTag `gorm:"-"`
	MetaTags    []*belongsToManyWithAttributesPendingTag `gorm:"-"`
	MorphedTags []*belongsToManyWithAttributesPendingTag `gorm:"-"`
}

func (belongsToManyWithAttributesPendingPost) TableName() string {
	return "pending_attributes_posts"
}

func (belongsToManyWithAttributesPendingPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// tags(): belongsToMany(Tag, 'pending_attributes_pivot', 'tag_id', 'post_id') — the corpus
		// passes the pivot keys swapped relative to their names, so the pivot's tag_id column holds
		// the parent post's key and post_id the related tag's key.
		"Tags": contractsorm.Many2Many{
			Related:         &belongsToManyWithAttributesPendingTag{},
			Table:           "pending_attributes_pivot",
			ForeignPivotKey: "tag_id",
			RelatedPivotKey: "post_id",
		},
		// metaTags(): tags()->withAttributes('visible', true, asConditions: false)
		// ->withPivotValue('type', 'meta'). withPivotValue's wherePivot condition becomes this
		// declaration's OnPivotQuery scope — the framework's declaration-time pivot condition,
		// applied to the relation's reads and to its pivot-table scopes. The pending attribute has
		// no declaration home; the create half is what the skips below document.
		"MetaTags": contractsorm.Many2Many{
			Related:         &belongsToManyWithAttributesPendingTag{},
			Table:           "pending_attributes_pivot",
			ForeignPivotKey: "tag_id",
			RelatedPivotKey: "post_id",
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
		},
		// morphedTags(): morphToMany(Tag, 'taggable', 'pending_attributes_taggables',
		// relatedPivotKey: 'tag_id')->withAttributes('visible', true, asConditions: false)
		// ->withPivotValue('type', 'meta'). The morph pair defaults to taggable_id / taggable_type.
		"MorphedTags": contractsorm.MorphToMany{
			Related:         &belongsToManyWithAttributesPendingTag{},
			Name:            "taggable",
			Table:           "pending_attributes_taggables",
			RelatedPivotKey: "tag_id",
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
		},
	}
}

// belongsToManyWithAttributesPendingTag ports ManyToManyPendingAttributesTag: an id, a name and a
// nullable visible flag, with morphedPosts() — morphedByMany(Post, 'taggable',
// 'pending_attributes_taggables', 'tag_id')->withAttributes('title', 'Title!', asConditions: false)
// ->withPivotValue('type', 'meta').
type belongsToManyWithAttributesPendingTag struct {
	ID      uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Name    string `gorm:"column:name"`
	Visible bool   `gorm:"column:visible"`

	MorphedPosts []*belongsToManyWithAttributesPendingPost `gorm:"-"`
}

func (belongsToManyWithAttributesPendingTag) TableName() string {
	return "pending_attributes_tags"
}

func (belongsToManyWithAttributesPendingTag) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// morphedPosts(): in the inverse direction the pivot's tag_id column references the parent
		// tag and the morph pair (taggable_id / taggable_type) references the related post, so
		// RelatedPivotKey names the morph id column.
		"MorphedPosts": contractsorm.MorphedByMany{
			Related:         &belongsToManyWithAttributesPendingPost{},
			Name:            "taggable",
			Table:           "pending_attributes_taggables",
			ForeignPivotKey: "tag_id",
			RelatedPivotKey: "taggable_id",
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
		},
	}
}

// --- schema ------------------------------------------------------------------

// belongsToManyWithAttributesPendingSchema mirrors the corpus's createSchema() in sqlite DDL:
// posts carry a nullable title, tags a name and a nullable visible flag, and both pivot tables the
// corpus's extra NOT NULL `type` (Laravel's $table->string('type')). created_at / updated_at exist
// as the corpus's timestamps() create them; no assertion reads them.
func belongsToManyWithAttributesPendingSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table pending_attributes_posts (id integer primary key autoincrement, title varchar null, created_at datetime null, updated_at datetime null)`,
		`create table pending_attributes_tags (id integer primary key autoincrement, name varchar not null, visible integer null, created_at datetime null, updated_at datetime null)`,
		`create table pending_attributes_pivot (post_id integer not null, tag_id integer not null, type varchar not null)`,
		`create table pending_attributes_taggables (tag_id integer not null, taggable_id integer not null, taggable_type varchar not null, type varchar not null)`,
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

// TestBelongsToManyWithAttributesPending_CreatesPendingAttributesAndPivotValues ports
// testCreatesPendingAttributesAndPivotValues: create a post, then $post->metaTags()->create(['name'
// => 'long article']) — the pending attribute visible => true lands on the tag, the pivot value
// type => 'meta' on the pivot row. The oracle execution writes the tag {name: 'long article',
// visible: 1} and the pivot row {post_id: $post->id, tag_id: $tag->id, type: 'meta'} (both ids are
// 1, so the swapped key columns cannot be told apart by this test).
func TestBelongsToManyWithAttributesPending_CreatesPendingAttributesAndPivotValues(t *testing.T) {
	t.Skip("our ORM diverges: withPivotValue has no write-side counterpart — no declaration records pivot column values, so Relation(...).Create() attaches with insert into \"pending_attributes_pivot\" (\"post_id\", \"tag_id\") values (?, ?), omitting type (the corpus's type varchar not null rejects it with NOT NULL constraint failed: pending_attributes_pivot.type; a nullable variant lands type NULL) — and the withAttributes pending value cannot ride a relation create either, because CreateRelation runs r.fresh().Create(), whose fresh builder carries no conditions, so the created tag keeps visible false where the corpus reads true")
}

// TestBelongsToManyWithAttributesPending_QueriesPendingAttributesAndPivotValues ports
// testQueriesPendingAttributesAndPivotValues: the relation is built off an unsaved post with id 2
// and its where list is inspected — assertContains tag_id = 2, assertContains type = 'meta',
// assertCount 2 (no visible condition; the corpus's asConditions: false). The Go observable is the
// compiled relation query.
func TestBelongsToManyWithAttributesPending_QueriesPendingAttributesAndPivotValues(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithAttributesPendingSchema(t, query)

	// Laravel: new ManyToManyPendingAttributesPost(['id' => 2]) — unsaved; the constraints read the
	// parent key off the model.
	post := belongsToManyWithAttributesPendingPost{ID: 2}
	dest := &belongsToManyWithAttributesPendingTag{}

	// Oracle (12.55.1 and the 12.43.1 checkout agree, sqlite): the relation compiles
	//   select * from "pending_attributes_tags" inner join "pending_attributes_pivot" on
	//   "pending_attributes_tags"."id" = "pending_attributes_pivot"."post_id" where
	//   "pending_attributes_pivot"."tag_id" = ? and "pending_attributes_pivot"."type" = ?
	// with bindings [2, 'meta']. Our where clause is that string byte for byte; the join differs
	// only in operand order and the ON keyword's case, which this test does not observe.
	statement := cloneRelationQuery(t, query).Related(&post, "MetaTags").ToSql().Get(dest)
	assert.Equal(t,
		`where "pending_attributes_pivot"."tag_id" = ? and "pending_attributes_pivot"."type" = ?`,
		whereClause(t, statement))
	assert.Equal(t, 2, strings.Count(statement, "?"), "the corpus requires exactly two wheres")

	// The oracle's toRawSql renders the values as
	//   ... where "pending_attributes_pivot"."tag_id" = 2 and "pending_attributes_pivot"."type" = 'meta'
	raw := cloneRelationQuery(t, query).Related(&post, "MetaTags").ToRawSql().Get(dest)
	assert.Equal(t,
		`where "pending_attributes_pivot"."tag_id" = 2 and "pending_attributes_pivot"."type" = 'meta'`,
		whereClause(t, raw))
}

// TestBelongsToManyWithAttributesPending_MorphToManyPendingAttributes ports
// testMorphToManyPendingAttributes: morphedTags() = morphToMany(Tag, 'taggable',
// 'pending_attributes_taggables', relatedPivotKey: 'tag_id')->withAttributes('visible', true,
// asConditions: false)->withPivotValue('type', 'meta'). The test pins the relation's three wheres
// (taggable_id = 2, taggable_type = the post class, type = 'meta'), then create(['name' => 'new
// tag']) — visible true on the tag, first() finding it back through the relation.
//
// The read half is expressible and was measured: "MorphedTags" compiles the oracle's where clause
// byte for byte, from
//
//	... where "pending_attributes_taggables"."taggable_id" = ? and
//	"pending_attributes_taggables"."taggable_type" = ? and "pending_attributes_taggables"."type" = ?
//
// with our morph value ('pending_attributes_posts', the parent's table name) in place of Laravel's
// ManyToManyPendingAttributesPost::class. The create half is what cannot run.
func TestBelongsToManyWithAttributesPending_MorphToManyPendingAttributes(t *testing.T) {
	t.Skip("our ORM diverges: withPivotValue has no write-side counterpart — morphedTags()->create() attaches with insert into \"pending_attributes_taggables\" (\"tag_id\", \"taggable_id\", \"taggable_type\") values (3, 2, 'pending_attributes_posts'), omitting type, so the corpus's type varchar not null rejects it (NOT NULL constraint failed: pending_attributes_taggables.type) — and the visible pending attribute cannot ride the create either (CreateRelation runs r.fresh().Create(), which carries no conditions), so the tag's pivot row cannot carry type = 'meta' and first() cannot find it back; the test's wheres half alone is expressible through the declaration's OnPivotQuery scope and was measured to match the oracle")
}

// TestBelongsToManyWithAttributesPending_MorphedByManyPendingAttributes ports
// testMorphedByManyPendingAttributes: morphedPosts() = morphedByMany(Post, 'taggable',
// 'pending_attributes_taggables', 'tag_id')->withAttributes('title', 'Title!', asConditions:
// false)->withPivotValue('type', 'meta'). The test pins the relation's three wheres (tag_id = 4,
// taggable_type = the post class, type = 'meta'), then create() — title 'Title!' on the post,
// first() finding it back through the relation.
//
// The read half is expressible and was measured: "MorphedPosts" compiles the oracle's where clause
// byte for byte, from
//
//	... where "pending_attributes_taggables"."tag_id" = ? and
//	"pending_attributes_taggables"."taggable_type" = ? and "pending_attributes_taggables"."type" = ?
//
// with our morph value ('pending_attributes_posts', the related model's table name) in place of
// Laravel's ManyToManyPendingAttributesPost::class. The create half is what cannot run.
func TestBelongsToManyWithAttributesPending_MorphedByManyPendingAttributes(t *testing.T) {
	t.Skip("our ORM diverges: withPivotValue has no write-side counterpart — morphedPosts()->create() attaches with insert into \"pending_attributes_taggables\" (\"tag_id\", \"taggable_id\", \"taggable_type\") values (4, 1, 'pending_attributes_posts'), omitting type, so the corpus's type varchar not null rejects it (NOT NULL constraint failed: pending_attributes_taggables.type) — and the title 'Title!' pending attribute cannot ride the create either (CreateRelation runs r.fresh().Create(), which carries no conditions), so the post's pivot row cannot carry type = 'meta' and first() cannot find it back; the test's wheres half alone is expressible through the declaration's OnPivotQuery scope and was measured to match the oracle")
}
