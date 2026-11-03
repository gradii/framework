package belongs_to_many_expression

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyExpressionTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyExpression_<method without the test prefix>.
//
// The corpus's models are a morph-to-many pair: Post.tags() is
// morphToMany(Tag::class, 'taggable', 'taggables', 'taggable_id', 'tag_id', 'id', 'id'). Two posts
// and three tags share the taggables pivot, whose extra `type` column carries the per-post values
// t1..t3 set through sync(), while the pivot's taggable_type column carries the morph type. The
// corpus stores the PHP class name there; the Go port stores the parent's table name, "posts" — the
// framework's morph convention (probe: after the port's sync the pivot row reads
// {TagID:1 TaggableID:1 Type:t1 TaggableType:posts}, and the read filters on 'posts' the same way).
// Rows are seeded through the ORM — Relation(&post, "Tags").SyncWithPivot is the Go form of
// $post->tags()->sync([...]) — and relation reads go through Query.Related, the harness's explicit
// load.
//
// The corpus's first two tests call wherePivotNotIn with a Query\Expression column. The relation
// object now carries Eloquent's wherePivot family: a plain string column is qualified against the
// pivot table and an Expression column is spliced verbatim, unqualified — both tests run against
// the harness database. SQL expectations measured with the oracle (.claude/laravel-oracle/
// oracle.php against illuminate/database v12.55.1, sqlite; the parent post persisted with id
// 1 / 2):
//
//   - post 1: select * from "tags" inner join "taggables" on "tags"."id" = "taggables"."tag_id"
//     where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?
//     and tag_id || '_' || type not in (?)      (binds 1, the post class, '1_t1') -> 1 tag, key 2
//   - post 2: ... and taggables.tag_id || '_' || taggables.type not in (?)
//     (binds 2, the post class, '2_t2') -> 1 tag, key 3
//   - wherePivotNotIn qualifies a plain string against the pivot table but splices an Expression
//     verbatim, with no qualification and no parentheses around it.
//
// The port compiles the same clauses into the same statement; its read differs from the oracle's
// only in the join rendering every relation read in the port shares (`... inner join "taggables"
// ON "taggables"."tag_id" = "tags"."id"`), which these tests do not depend on — they assert the
// corpus's outcomes (the tag rows read), not the statement text.
//
// The global-scope tests below run for real: the tag model reads its scopes from a package variable
// (the Go stand-in for Model::addGlobalScope on the Tag class), and the relation read applies them
// through the same addGlobalScopes path an ordinary model query uses — the two tests show the
// scope's panic surfacing from Related(...).Get and withoutGlobalScopes() letting the read through.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	querybuilder "github.com/gradii/framework/database/query"
)

// --- models ------------------------------------------------------------------

// belongsToManyExpressionPost ports DatabaseEloquentBelongsToManyExpressionTestTestPost: a table
// with only an auto-incrementing id and no timestamps.
type belongsToManyExpressionPost struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`

	Tags []*belongsToManyExpressionTag `gorm:"-"`
}

func (belongsToManyExpressionPost) TableName() string { return "posts" }

// Relations declares the corpus's tags(): morphToMany(Tag, 'taggable', 'taggables', 'taggable_id',
// 'tag_id', 'id', 'id').
func (belongsToManyExpressionPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related:         &belongsToManyExpressionTag{},
			Name:            "taggable",
			Table:           "taggables",
			ForeignPivotKey: "taggable_id",
			RelatedPivotKey: "tag_id",
			ParentKey:       "id",
			RelatedKey:      "id",
		},
	}
}

// belongsToManyExpressionTag ports DatabaseEloquentBelongsToManyExpressionTestTestTag: a table with
// only an auto-incrementing id and no timestamps.
type belongsToManyExpressionTag struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`
}

func (belongsToManyExpressionTag) TableName() string { return "tags" }

// belongsToManyExpressionTagScopes is the Go stand-in for the corpus's
// Tag::addGlobalScope('default', ...): Go cannot attach a scope to a type after it is declared, so
// the tag model reads its global scopes from this package variable and the tests install — and
// clear — the throwing scope around the relation read. Nil by default, i.e. no scopes.
var belongsToManyExpressionTagScopes []contractsorm.GlobalScope

func (belongsToManyExpressionTag) GlobalScopes() []contractsorm.GlobalScope {
	return belongsToManyExpressionTagScopes
}

// --- schema and seeding ------------------------------------------------------

// belongsToManyExpressionSchema mirrors the corpus's createSchema(): posts and tags carry only an
// auto-incrementing id; the taggables pivot carries the corpus's extra `type` string column
// alongside the morph type column.
func belongsToManyExpressionSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table posts (id integer primary key autoincrement)`,
		`create table tags (id integer primary key autoincrement)`,
		`create table taggables (tag_id integer not null, taggable_id integer not null, type varchar(10) not null, taggable_type varchar not null)`,
	)
}

// belongsToManyExpressionSeedData ports seedData(): two posts, three tags, and a sync with pivot
// values per post.
//
// Rows are written with explicit ids: the corpus relies on the auto increment, but an all-defaults
// struct insert compiles as `insert into "tags" () values ()`, which sqlite rejects (Laravel
// compiles `default values`); explicit ids produce the same 1..n rows the auto increment would.
func belongsToManyExpressionSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for id := uint(1); id <= 2; id++ {
		post := belongsToManyExpressionPost{ID: id}
		require.NoError(t, cloneRelationQuery(t, query).Model(&post).Create(map[string]any{"id": id}))
	}
	for id := uint(1); id <= 3; id++ {
		tag := belongsToManyExpressionTag{ID: id}
		require.NoError(t, cloneRelationQuery(t, query).Model(&tag).Create(map[string]any{"id": id}))
	}

	p1 := belongsToManyExpressionPost{ID: 1}
	_, err := cloneRelationQuery(t, query).Relation(&p1, "Tags").SyncWithPivot(map[any]map[string]any{
		uint(1): {"type": "t1"},
		uint(2): {"type": "t2"},
	})
	require.NoError(t, err)

	p2 := belongsToManyExpressionPost{ID: 2}
	_, err = cloneRelationQuery(t, query).Relation(&p2, "Tags").SyncWithPivot(map[any]map[string]any{
		uint(2): {"type": "t2"},
		uint(3): {"type": "t3"},
	})
	require.NoError(t, err)
}

// belongsToManyExpressionFirstPost ports Post::query()->firstOrFail(): the first posts row.
func belongsToManyExpressionFirstPost(t *testing.T, query contractsorm.Query) belongsToManyExpressionPost {
	t.Helper()

	post := belongsToManyExpressionPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&post).FirstOrFail(&post))

	return post
}

// --- ports of the corpus -----------------------------------------------------

func TestBelongsToManyExpression_AmbiguousColumnsExpression(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyExpressionSchema(t, query)
	belongsToManyExpressionSeedData(t, query)

	post := belongsToManyExpressionFirstPost(t, query)

	// Laravel: tags()->wherePivotNotIn(new Expression("tag_id || '_' || type"), ['1_t1'])->get().
	relation, ok := cloneRelationQuery(t, query).Related(&post, "Tags").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")

	tags := []belongsToManyExpressionTag{}
	require.NoError(t, relation.
		WherePivotNotIn(querybuilder.Expression{Value: "tag_id || '_' || type"}, []any{"1_t1"}).
		Get(&tags))

	require.Len(t, tags, 1)
	assert.Equal(t, uint(2), tags[0].ID)
}

func TestBelongsToManyExpression_QualifiedColumnExpression(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyExpressionSchema(t, query)
	belongsToManyExpressionSeedData(t, query)

	// The corpus loads post 2 by key; the port reads the same row through FirstOrFail.
	post := belongsToManyExpressionPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&post).Where("id", 2).FirstOrFail(&post))

	// Laravel: tags()->wherePivotNotIn(
	//     new Expression("taggables.tag_id || '_' || taggables.type"), ['2_t2'])->get().
	relation, ok := cloneRelationQuery(t, query).Related(&post, "Tags").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")

	tags := []belongsToManyExpressionTag{}
	require.NoError(t, relation.
		WherePivotNotIn(querybuilder.Expression{Value: "taggables.tag_id || '_' || taggables.type"}, []any{"2_t2"}).
		Get(&tags))

	require.Len(t, tags, 1)
	assert.Equal(t, uint(3), tags[0].ID)
}

func TestBelongsToManyExpression_GlobalScopesAreAppliedToBelongsToManyRelation(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyExpressionSchema(t, query)
	belongsToManyExpressionSeedData(t, query)

	post := belongsToManyExpressionFirstPost(t, query)

	// Laravel: Tag::addGlobalScope('default', static fn () => throw ...). Go cannot add a scope to a
	// declared type, so the test installs the throwing scope on the model's package variable instead.
	belongsToManyExpressionTagScopes = []contractsorm.GlobalScope{
		{Name: "default", Scope: func(contractsorm.Query) contractsorm.Query { panic("Default global scope.") }},
	}
	t.Cleanup(func() { belongsToManyExpressionTagScopes = nil })

	// Laravel: expectExceptionMessage('Default global scope.') around $post->tags()->get(); the Go
	// scope throws by panicking, so the read is run under recover and the recovered value must be
	// the scope's message.
	var (
		tags      []belongsToManyExpressionTag
		recovered any
	)
	func() {
		defer func() { recovered = recover() }()
		_ = cloneRelationQuery(t, query).Related(&post, "Tags").Get(&tags)
	}()

	require.NotNil(t, recovered, "the tag model's global scope must run when the relation is read")
	assert.Equal(t, "Default global scope.", recovered)
}

func TestBelongsToManyExpression_GlobalScopesCanBeRemovedFromBelongsToManyRelation(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyExpressionSchema(t, query)
	belongsToManyExpressionSeedData(t, query)

	post := belongsToManyExpressionFirstPost(t, query)

	// The same installing scope as above: without the removal it would throw, and the assertion is
	// that withoutGlobalScopes() lets the read through.
	belongsToManyExpressionTagScopes = []contractsorm.GlobalScope{
		{Name: "default", Scope: func(contractsorm.Query) contractsorm.Query { panic("Default global scope.") }},
	}
	t.Cleanup(func() { belongsToManyExpressionTagScopes = nil })

	var tags []belongsToManyExpressionTag
	require.NoError(t, cloneRelationQuery(t, query).Related(&post, "Tags").WithoutGlobalScopes().Get(&tags))
	assert.NotEmpty(t, tags)
}
