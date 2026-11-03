package morph_to_many

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	querybuilder "github.com/gradii/framework/database/query"
)

// This file ports Laravel's tests/Database/DatabaseEloquentMorphToManyTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named
// TestMorphToMany_<method without the test prefix>. The corpus builds its relation from mocked
// models; the port stands up the schema those mocks imply — the taggable parent (posts, plus
// videos for the cross-type check), the tags table, and the taggables pivot carrying taggable_id /
// taggable_type / tag_id / foo — seeds rows through the ORM, and observes the same outcomes on the
// real database. The relation is exercised through Relation(parent, "Tags") and explicit
// With / Load, the harness's rules, since Go has no property-access magic.
//
// Where the corpus asserts get_class($parent) as the morph type, the port declares the model's
// morph class explicitly (MorphClass() "post" / "video") — the Go ORM's equivalent of the class
// identity stored in *_type.
//
// Every SQL expectation cited below was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled: a real
// morphToMany relation over a real sqlite connection for the eager constraints, and the sqlite
// grammar's compileInsert / compileDelete for the write statements.

// --- models ------------------------------------------------------------------

// morphToManyPost is the corpus's taggable parent (EloquentMorphToManyModelStub): the table the
// MorphToMany relation hangs off. Laravel's parent mock answers getMorphClass() with its class
// name; the port pins the equivalent stable alias with MorphClass().
type morphToManyPost struct {
	ID   uint              `gorm:"column:id;primaryKey;autoIncrement"`
	Tags []*morphToManyTag `gorm:"-"`
}

func (morphToManyPost) TableName() string { return "posts" }

func (morphToManyPost) MorphClass() string { return "post" }

// Relations declares the corpus's MorphToMany: name "taggable", table "taggables", foreign pivot
// "taggable_id", related pivot "tag_id", keys "id" — exactly the arguments the corpus's
// getRelation() passes to new MorphToMany(...).
func (morphToManyPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related:         &morphToManyTag{},
			Name:            "taggable",
			Table:           "taggables",
			ForeignPivotKey: "taggable_id",
			RelatedPivotKey: "tag_id",
			ParentKey:       "id",
			RelatedKey:      "id",
		},
	}
}

// morphToManyVideo is a second taggable kind sharing the pivot table. It exists so the eager
// constraint on taggable_type can be observed: a video pivot row reusing a post's key must not
// leak into the post's eager-loaded tags.
type morphToManyVideo struct {
	ID   uint              `gorm:"column:id;primaryKey;autoIncrement"`
	Tags []*morphToManyTag `gorm:"-"`
}

func (morphToManyVideo) TableName() string { return "videos" }

func (morphToManyVideo) MorphClass() string { return "video" }

func (morphToManyVideo) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related:         &morphToManyTag{},
			Name:            "taggable",
			Table:           "taggables",
			ForeignPivotKey: "taggable_id",
			RelatedPivotKey: "tag_id",
			ParentKey:       "id",
			RelatedKey:      "id",
		},
	}
}

// morphToManyTag ports the corpus's related model (the mocked tags model): an id-only table.
type morphToManyTag struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`
}

func (morphToManyTag) TableName() string { return "tags" }

// morphToManyTaggableRow reads a taggables pivot row back; foo is the attach test's extra column
// and is nullable, so it scans as sql.NullString.
type morphToManyTaggableRow struct {
	TaggableID   uint           `gorm:"column:taggable_id"`
	TaggableType string         `gorm:"column:taggable_type"`
	TagID        uint           `gorm:"column:tag_id"`
	Foo          sql.NullString `gorm:"column:foo"`
}

func (morphToManyTaggableRow) TableName() string { return "taggables" }

// --- schema and seeding ------------------------------------------------------

// morphToManySchema mirrors the tables the corpus's mocked relation implies: the parent tables,
// the id-only tags table, and the morph pivot (taggable_id + taggable_type + tag_id, plus the
// attach test's foo column).
func morphToManySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table posts (id integer primary key autoincrement)`,
		`create table videos (id integer primary key autoincrement)`,
		`create table tags (id integer primary key autoincrement)`,
		`create table taggables (taggable_id integer not null, taggable_type varchar not null, tag_id integer not null, foo varchar null)`,
	)
}

// morphToManySeedPost creates a posts row. As in the corpus's id-only tables, the explicit id is
// written through the Model(...).Create(map) form, which avoids the all-defaults struct insert
// sqlite rejects.
func morphToManySeedPost(t *testing.T, query contractsorm.Query, id uint) morphToManyPost {
	t.Helper()

	post := morphToManyPost{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&post).Create(map[string]any{"id": id}))

	return post
}

// morphToManySeedVideo creates a videos row, the cross-type parent of the eager test.
func morphToManySeedVideo(t *testing.T, query contractsorm.Query, id uint) morphToManyVideo {
	t.Helper()

	video := morphToManyVideo{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&video).Create(map[string]any{"id": id}))

	return video
}

// morphToManySeedTag creates a tags row.
func morphToManySeedTag(t *testing.T, query contractsorm.Query, id uint) morphToManyTag {
	t.Helper()

	tag := morphToManyTag{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&tag).Create(map[string]any{"id": id}))

	return tag
}

// morphToManyAttach ports $parent->tags()->attach([...]) without pivot attributes.
func morphToManyAttach(t *testing.T, query contractsorm.Query, parent any, ids ...any) {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Relation(parent, "Tags").Attach(ids))
}

// morphToManyPivotRows reads every taggables row back, ordered by tag_id for stable assertions.
func morphToManyPivotRows(t *testing.T, query contractsorm.Query) []morphToManyTaggableRow {
	t.Helper()

	rows := []morphToManyTaggableRow{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphToManyTaggableRow{}).OrderBy("tag_id").Get(&rows))

	return rows
}

// --- ports of the corpus -----------------------------------------------------

func TestMorphToMany_EagerConstraintsAreProperlyAdded(t *testing.T) {
	// Laravel drives addEagerConstraints([$model1, $model2]) against a mocked relation and asserts
	// the two constraints it adds: whereIntegerInRaw('taggables.taggable_id', [1, 2]) and
	// where('taggables.taggable_type', get_class($parent)). The oracle compiles the eager read of a
	// real relation parented at key 1 as
	//   select * from "tags" inner join "taggables" on "tags"."id" = "taggables"."tag_id"
	//   where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?
	//   and "taggables"."taggable_id" in (1, 2) and "taggables"."taggable_type" = ?
	// with bindings [1, OracleMtpPost, OracleMtpPost]: the last two WHERE clauses are the eager
	// constraints, the parents' keys and the parent's morph class. The port observes them through
	// the public eager load — each parent must receive exactly the tags the (taggable_id in keys,
	// taggable_type = morph class) predicate selects. A video pivot row reusing post 1's key pins
	// the type half of the constraint.
	query := newRelationQuery(t)
	morphToManySchema(t, query)

	post1 := morphToManySeedPost(t, query, 1)
	post2 := morphToManySeedPost(t, query, 2)
	video1 := morphToManySeedVideo(t, query, 1)
	_ = morphToManySeedTag(t, query, 1)
	_ = morphToManySeedTag(t, query, 2)
	_ = morphToManySeedTag(t, query, 3)

	morphToManyAttach(t, query, &post1, uint(1))
	morphToManyAttach(t, query, &post2, uint(2))
	morphToManyAttach(t, query, &video1, uint(3))

	// Laravel: with('tags')->get(). Get drains its cursor before the eager query runs; First would
	// hold its single-row cursor open and deadlock on the harness's one-connection pool.
	posts := []morphToManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphToManyPost{}).With("Tags").OrderBy("id").Get(&posts))
	require.Len(t, posts, 2)

	require.Len(t, posts[0].Tags, 1)
	assert.Equal(t, uint(1), posts[0].Tags[0].ID)

	require.Len(t, posts[1].Tags, 1)
	assert.Equal(t, uint(2), posts[1].Tags[0].ID)
}

func TestMorphToMany_AttachInsertsPivotTableRecord(t *testing.T) {
	// Laravel: $relation->attach(2, ['foo' => 'bar']); the corpus's query mock asserts one insert
	// of the record ['taggable_id' => 1, 'taggable_type' => get_class($parent), 'tag_id' => 2,
	// 'foo' => 'bar']. The oracle compiles that record as
	//   insert into "taggables" ("taggable_id", "taggable_type", "tag_id", "foo") values (?, ?, ?, ?)
	// (Laravel's real attach path orders the same four keys as tag_id, taggable_id, taggable_type,
	// foo; the port asserts the row's values, not the statement's column order.)
	// AttachWithPivot is the Go form of attach($id, $attributes); the port reads the single pivot
	// row back and asserts every column.
	query := newRelationQuery(t)
	morphToManySchema(t, query)

	post := morphToManySeedPost(t, query, 1)
	_ = morphToManySeedTag(t, query, 2)

	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Tags").
		AttachWithPivot(map[any]map[string]any{uint(2): {"foo": "bar"}}))

	rows := morphToManyPivotRows(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, uint(1), rows[0].TaggableID)
	assert.Equal(t, "post", rows[0].TaggableType)
	assert.Equal(t, uint(2), rows[0].TagID)
	assert.Equal(t, sql.NullString{String: "bar", Valid: true}, rows[0].Foo)
}

func TestMorphToMany_DetachRemovesPivotTableRecord(t *testing.T) {
	// Laravel: $relation->detach([1, 2, 3]); the corpus's query mock asserts the delete chain
	// where('taggables.taggable_id', 1), where('taggable_type', get_class($parent)),
	// whereIn('taggables.tag_id', [1, 2, 3]). The oracle compiles it as
	//   delete from "taggables" where "taggables"."taggable_id" = ? and "taggable_type" = ?
	//   and "taggables"."tag_id" in (?, ?, ?)
	// with bindings [1, OracleMtpPost, 1, 2, 3]. Real Laravel returns the number of affected rows
	// (the corpus's mocked delete returns true, which assertTrue accepts); the port asserts the
	// affected count and that only tag 4 survives. A video pivot row reusing post 1's key and
	// tag 1 pins the taggable_type half of the delete predicate: without it the video row
	// would be deleted too and the count would be 4.
	query := newRelationQuery(t)
	morphToManySchema(t, query)

	post := morphToManySeedPost(t, query, 1)
	video := morphToManySeedVideo(t, query, 1)
	_ = morphToManySeedTag(t, query, 1)
	_ = morphToManySeedTag(t, query, 2)
	_ = morphToManySeedTag(t, query, 3)
	_ = morphToManySeedTag(t, query, 4)
	morphToManyAttach(t, query, &post, uint(1), uint(2), uint(3), uint(4))
	morphToManyAttach(t, query, &video, uint(1))

	affected, err := cloneRelationQuery(t, query).Relation(&post, "Tags").Detach(uint(1), uint(2), uint(3))
	require.NoError(t, err)
	assert.Equal(t, int64(3), affected)

	rows := morphToManyPivotRows(t, query)
	require.Len(t, rows, 2)
	assert.Equal(t, uint(1), rows[0].TaggableID)
	assert.Equal(t, "video", rows[0].TaggableType)
	assert.Equal(t, uint(1), rows[0].TagID)
	assert.Equal(t, uint(1), rows[1].TaggableID)
	assert.Equal(t, "post", rows[1].TaggableType)
	assert.Equal(t, uint(4), rows[1].TagID)
}

func TestMorphToMany_DetachMethodClearsAllPivotRecordsWhenNoIDsAreGiven(t *testing.T) {
	// Laravel: $relation->detach() with no ids; the corpus's query mock asserts the delete chain
	// where('taggables.taggable_id', 1), where('taggable_type', get_class($parent)) with whereIn
	// never called. The oracle compiles it as
	//   delete from "taggables" where "taggables"."taggable_id" = ? and "taggable_type" = ?
	// with bindings [1, OracleMtpPost]. The port detaches all of parent 1's records and observes
	// that a second parent's pivot row and a cross-type video row reusing parent 1's key both
	// survive — the taggable_id and taggable_type halves of the predicate the mock asserts.
	query := newRelationQuery(t)
	morphToManySchema(t, query)

	postOne := morphToManySeedPost(t, query, 1)
	postTwo := morphToManySeedPost(t, query, 2)
	video := morphToManySeedVideo(t, query, 1)
	_ = morphToManySeedTag(t, query, 1)
	_ = morphToManySeedTag(t, query, 2)
	_ = morphToManySeedTag(t, query, 3)
	_ = morphToManySeedTag(t, query, 9)
	morphToManyAttach(t, query, &postOne, uint(1), uint(2), uint(3))
	morphToManyAttach(t, query, &postTwo, uint(9))
	morphToManyAttach(t, query, &video, uint(1))

	affected, err := cloneRelationQuery(t, query).Relation(&postOne, "Tags").Detach()
	require.NoError(t, err)
	assert.Equal(t, int64(3), affected)

	rows := morphToManyPivotRows(t, query)
	require.Len(t, rows, 2)
	assert.Equal(t, uint(1), rows[0].TaggableID)
	assert.Equal(t, "video", rows[0].TaggableType)
	assert.Equal(t, uint(1), rows[0].TagID)
	assert.Equal(t, uint(2), rows[1].TaggableID)
	assert.Equal(t, "post", rows[1].TaggableType)
	assert.Equal(t, uint(9), rows[1].TagID)
}

func TestMorphToMany_QueryExpressionCanBePassedToDifferentPivotQueryBuilderClauses(t *testing.T) {
	// Laravel passes a query Expression column through six pivot-clause helpers — wherePivot,
	// withPivotValue, wherePivotBetween, wherePivotIn, wherePivotNull, orderByPivot — and asserts
	// through Mockery that the underlying builder receives each call with the Expression forwarded
	// untouched. The port asserts the statement the same six calls compile: the expression is
	// spliced verbatim into every clause, in call order. Oracle (illuminate/database v12.55.1,
	// sqlite):
	//   select * from "tags" inner join "taggables" on "tags"."id" = "taggables"."tag_id"
	//   where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?
	//   and CONCAT(foo, '_', bar) = ? and CONCAT(foo, '_', bar) = ?
	//   and CONCAT(foo, '_', bar) between ? and ? and CONCAT(foo, '_', bar) in (?)
	//   and CONCAT(foo, '_', bar) is null order by CONCAT(foo, '_', bar) asc
	// with bindings [1, the parent class, 'pivot_value' five times]. The port's statement differs
	// from the oracle's only in the join rendering every relation read in the port shares
	// (`ON "taggables"."tag_id" = "tags"."id"`, oracle: `on "tags"."id" = "taggables"."tag_id"`)
	// and in the relation read's aliased select list.
	query := newRelationQuery(t)
	morphToManySchema(t, query)

	post := morphToManySeedPost(t, query, 1)

	expression := querybuilder.Expression{Value: "CONCAT(foo, '_', bar)"}
	relation, ok := cloneRelationQuery(t, query).Related(&post, "Tags").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")

	relation = relation.
		WherePivot(expression, "=", "pivot_value").
		WithPivotValue(expression, "pivot_value").
		WherePivotBetween(expression, "pivot_value", "pivot_value").
		WherePivotIn(expression, []any{"pivot_value"}).
		WherePivotNull(expression).
		OrderByPivot(expression)

	tags := []morphToManyTag{}
	assert.Equal(t, `select "tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."tag_id" as "pivot_tag_id", "taggables"."taggable_type" as "pivot_taggable_type" from "tags" inner join "taggables" ON "taggables"."tag_id" = "tags"."id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ? and CONCAT(foo, '_', bar) = ? and CONCAT(foo, '_', bar) = ? and CONCAT(foo, '_', bar) between ? and ? and CONCAT(foo, '_', bar) in (?) and CONCAT(foo, '_', bar) is null order by CONCAT(foo, '_', bar) asc`, relation.ToSql().Get(&tags))
}

// TestMorphToMany_EagerLimitPartitionsByThePivotParentKey ports the eager limit an Eloquent
// `with('tags', fn ($q) => $q->limit(2))` compiles onto a MorphToMany relation. The eager read is
// the joined relation query (the related table joined to the pivot, wearing the aliased pivot select
// list), and the relation-level limit compiles Laravel's pivot-partitioned group limit —
// row_number() over (partition by <qualified foreign pivot key>) — so each parent receives at most
// two tags. Oracle (illuminate/database v12.55.1, executed against sqlite from
// /tmp/eager-m2m-limit/probe.php, M1): the rewrite partitions by "taggables"."taggable_id" and keeps
// the morph-type predicate inside it.
func TestMorphToMany_EagerLimitPartitionsByThePivotParentKey(t *testing.T) {
	query := newRelationQuery(t)
	morphToManySchema(t, query)

	post1 := morphToManySeedPost(t, query, 1)
	post2 := morphToManySeedPost(t, query, 2)
	_ = morphToManySeedTag(t, query, 1)
	_ = morphToManySeedTag(t, query, 2)
	_ = morphToManySeedTag(t, query, 3)

	morphToManyAttach(t, query, &post1, uint(1), uint(2), uint(3))
	morphToManyAttach(t, query, &post2, uint(1), uint(2), uint(3))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	posts := []morphToManyPost{}
	require.NoError(t, withContext.WithContext(ctx).Model(&morphToManyPost{}).OrderBy("id").
		With("Tags", func(q contractsorm.Query) contractsorm.Query { return q.Limit(2) }).
		Get(&posts))
	require.Len(t, posts, 2)

	// The pivot-partitioned group limit caps every parent at two tags — the eager read actually runs
	// the derived table against sqlite.
	require.Len(t, posts[0].Tags, 2)
	require.Len(t, posts[1].Tags, 2)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "the parent read and the joined eager read")
	assert.Equal(t,
		`select * from (select "tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."tag_id" as "pivot_tag_id", "taggables"."taggable_type" as "pivot_taggable_type", row_number() over (partition by "taggables"."taggable_id") as "laravel_row" from "tags" inner join "taggables" ON "taggables"."tag_id" = "tags"."id" where "taggables"."taggable_id" in (1, 2) and "taggables"."taggable_type" = 'post') as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`,
		logs[1].Query)
}
