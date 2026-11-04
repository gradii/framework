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
// Round 10 ports BOTH halves. The pending attribute is declared through OnQuery's
// Query.WithAttributes("visible", true, false) — the asConditions: false form records the pending
// pair while adding no where, exactly as Laravel's withAttributes does (Builder.php:1929) — and
// the create path merges it into the related INSERT (relationCreateSession, mirroring
// BelongsToMany::create's array_merge($pendingAttributes, $attributes)). withPivotValue's recorded
// pair is declared through PivotValues (Laravel's BelongsToMany::$pivotValues), which the create /
// attach writes stamp onto the pivot row; its wherePivot condition is the OnPivotQuery scope. The
// three create-based tests now run and assert both effects.
//
// # Oracle measurements (illuminate/database v12.55.1, sqlite; /tmp/r10/pending_create_probe.php)
//
//   - pendingTags()->create(['name' => 'pending']) writes the tag visible true (the pending pair
//     survives asConditions: false) and the pivot row type 'meta'; the relation read carries no
//     visible condition (only tag_id and type, the corpus's assertCount(2)).
//   - morphedTags()->create(['name' => 'new tag']) writes the tag visible true and the taggables
//     row (..., 'meta'); morphedPosts()->create() writes the post title 'Title!' and (..., 'meta').
//
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
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("visible", true, false)
			},
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
			PivotValues: []contractsorm.PivotValue{{Column: "type", Value: "meta"}},
		},
		// morphedTags(): morphToMany(Tag, 'taggable', 'pending_attributes_taggables',
		// relatedPivotKey: 'tag_id')->withAttributes('visible', true, asConditions: false)
		// ->withPivotValue('type', 'meta'). The morph pair defaults to taggable_id / taggable_type.
		"MorphedTags": contractsorm.MorphToMany{
			Related:         &belongsToManyWithAttributesPendingTag{},
			Name:            "taggable",
			Table:           "pending_attributes_taggables",
			RelatedPivotKey: "tag_id",
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("visible", true, false)
			},
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
			PivotValues: []contractsorm.PivotValue{{Column: "type", Value: "meta"}},
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
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("title", "Title!", false)
			},
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("type", "meta")
			},
			PivotValues: []contractsorm.PivotValue{{Column: "type", Value: "meta"}},
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
	query := newRelationQuery(t)
	belongsToManyWithAttributesPendingSchema(t, query)

	post := belongsToManyWithAttributesPendingPost{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&post))

	tag := belongsToManyWithAttributesPendingTag{Name: "long article"}
	require.NoError(t, relatedBelongsToMany(t, query, &post, "MetaTags").Create(&tag))

	assert.Equal(t, "long article", tag.Name)
	assert.True(t, tag.Visible, "the pending visible => true lands on the related row even with asConditions: false")

	pivot := withAttributesPendingPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Where("post_id", post.ID).Where("tag_id", tag.ID).First(&pivot))
	assert.Equal(t, "meta", pivot.Type, "the declared withPivotValue pair stamps the pivot row")
	assert.Equal(t, post.ID, pivot.PostID)
	assert.Equal(t, tag.ID, pivot.TagID)
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
	query := newRelationQuery(t)
	belongsToManyWithAttributesPendingSchema(t, query)

	post := belongsToManyWithAttributesPendingPost{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&post))

	tag := belongsToManyWithAttributesPendingTag{Name: "new tag"}
	require.NoError(t, relatedBelongsToMany(t, query, &post, "MorphedTags").Create(&tag))

	assert.True(t, tag.Visible, "the pending visible => true lands on the morphed tag insert")
	assert.Equal(t, "new tag", tag.Name)

	found := []belongsToManyWithAttributesPendingTag{}
	require.NoError(t, relatedBelongsToMany(t, query, &post, "MorphedTags").Get(&found))
	require.Len(t, found, 1)
	assert.Equal(t, tag.ID, found[0].ID, "first() finds the created tag back through the relation")
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
	query := newRelationQuery(t)
	belongsToManyWithAttributesPendingSchema(t, query)

	tag := belongsToManyWithAttributesPendingTag{ID: 4}

	post := belongsToManyWithAttributesPendingPost{}
	require.NoError(t, relatedBelongsToMany(t, query, &tag, "MorphedPosts").Create(&post))

	assert.Equal(t, "Title!", post.Title, "the pending title => 'Title!' lands on the morphed post insert")

	found := []belongsToManyWithAttributesPendingPost{}
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

// withAttributesPendingPivotRow reads a pending_attributes_pivot row back for the create
// assertions.
type withAttributesPendingPivotRow struct {
	PostID uint   `gorm:"column:post_id"`
	TagID  uint   `gorm:"column:tag_id"`
	Type   string `gorm:"column:type"`
}

func (withAttributesPendingPivotRow) TableName() string { return "pending_attributes_pivot" }
