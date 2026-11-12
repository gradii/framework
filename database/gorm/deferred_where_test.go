package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
)

// Round 15c: the clauses a query can only render once its model is known - an OnlyTrashed
// predicate and a Has / WhereHas / DoesntHave existence clause, alone or inside a where closure -
// are deferred entries in the query's single where list, each with one marker in its "where"
// bindings, rendered in place when a statement compiles. Laravel writes the same clauses onto
// $this->wheres at call time (SoftDeletingScope::addOnlyTrashed calls whereNotNull,
// Query\Builder::addWhereExistsQuery appends the exists), so the statement is just list order. These
// pins hold the properties the in-place rendering must keep: it never changes the query it renders
// from, a clone is independent of it, ToSql and the executed statement agree, and an entry that
// renders nothing leaves the bindings aligned.

// deferredScopeUser is the model behind the "hasPosts" scope (deferredHasPostsScope), whose where
// closure calls whereHas — the shape oracle D1 compiles (/tmp/r15c-probes/scope_deferred.php). The
// scope used to be declared GlobalScopes(); scopes mount on the builder with ApplyScope now, so the
// test mounts it at its query-construction site.
type deferredScopeUser struct {
	ID     uint
	Email  string
	Active int
	Posts  []*scpPost `gorm:"-"`
	databaseorm.SoftDeletes
}

func (deferredScopeUser) TableName() string { return "sd_users" }

func (deferredScopeUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &scpPost{}, ForeignKey: "sd_user_id"},
	}
}

// deferredHasPostsScope is the "hasPosts" scope the D1 case mounts on the builder.
func deferredHasPostsScope(q contractsorm.Query) contractsorm.Query {
	return q.Where("active", 1).Where(func(q2 contractsorm.Query) contractsorm.Query {
		return q2.Where("id", 2).WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 3) })
	}).Where("id", 4)
}

// deferredTrashedScopeUser is the model behind the "closureTrashed" scope
// (deferredClosureTrashedScope), whose where closure calls onlyTrashed (oracle D2).
type deferredTrashedScopeUser struct {
	ID    uint
	Email string
	databaseorm.SoftDeletes
}

func (deferredTrashedScopeUser) TableName() string { return "sd_users" }

// deferredClosureTrashedScope is the "closureTrashed" scope the D2 case mounts on the builder.
func deferredClosureTrashedScope(q contractsorm.Query) contractsorm.Query {
	return q.Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.Where("id", 2).OnlyTrashed() }).Where("id", 4)
}

// deferredRichQuery builds a query carrying every kind of deferred entry: a whereHas with a
// callback binding, an onlyTrashed between two wheres, a where closure holding a whereHas and an
// onlyTrashed, a closure nested in a closure holding a whereHas, and a trailing orWhereHas.
func deferredRichQuery(t *testing.T) *OrmBuilder {
	t.Helper()

	query := compiledQuery(t, &scpSdUser{}, Conditions{}).
		Where("id", 1).
		WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 2) }).
		OnlyTrashed().
		Where("email", "a").
		Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("id", 3).WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 4) }).OnlyTrashed()
		}).
		Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(q2 contractsorm.Query) contractsorm.Query {
				return q2.WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 5) }).Where("id", 6)
			})
		}).
		OrWhereHas("Posts")

	return query.(*OrmBuilder)
}

const deferredRichWhere = `"id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and "sd_users"."deleted_at" is not null and "email" = ? and ("id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and "sd_users"."deleted_at" is not null) and ((exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and "id" = ?)) or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)`

func compileDeferred(t *testing.T, query *OrmBuilder) (string, []any) {
	t.Helper()

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

// TestDeferredWhere_CompilingTwiceRendersTheSameStatement pins that rendering the deferred entries
// happens on the statement's copy: compiling the same query twice - and compiling it again after a
// later where - renders every deferred clause exactly once, with its bindings at its position.
func TestDeferredWhere_CompilingTwiceRendersTheSameStatement(t *testing.T) {
	query := deferredRichQuery(t)
	before := snapshotWheres(query.query)
	require.True(t, containsDeferredWhere(query.query.State().Wheres))

	first, firstBindings := compileDeferred(t, query)
	assert.Equal(t, before, snapshotWheres(query.query), "rendering left the persistent where list and bindings as they were")
	second, secondBindings := compileDeferred(t, query)
	assert.Equal(t, before, snapshotWheres(query.query))

	assert.Equal(t, `select * from "sd_users" where `+deferredRichWhere, first)
	assert.Equal(t, []any{1, 2, "a", 3, 4, 5, 6}, firstBindings)
	assert.Equal(t, first, second)
	assert.Equal(t, firstBindings, secondBindings)

	// A count compiled from the same query renders the same where clause.
	countSQL, countBindings, err := query.compileAggregate("count", []any{"*"})
	require.NoError(t, err)
	assert.Equal(t, `select count(*) as aggregate from "sd_users" where `+deferredRichWhere, countSQL)
	assert.Equal(t, firstBindings, countBindings)

	// A where written after the compiles lands after every deferred clause, once.
	third, thirdBindings := compileDeferred(t, query.Where("id", 7).(*OrmBuilder))
	assert.Equal(t, `select * from "sd_users" where `+deferredRichWhere+` and "id" = ?`, third)
	assert.Equal(t, []any{1, 2, "a", 3, 4, 5, 6, 7}, thirdBindings)
}

// whereSnapshot is a deep copy of a builder's where list - nested groups included - and its "where"
// bindings, so a test can pin that compiling left them as they were.
type whereSnapshot struct {
	Wheres   []whereSnapshotEntry
	Bindings []any
}

type whereSnapshotEntry struct {
	Type    string
	Boolean string
	Column  any
	Value   any
	SQL     any
	Group   *whereSnapshot
}

func snapshotWheres(builder interface {
	State() *core.State
}) *whereSnapshot {
	state := builder.State()
	out := &whereSnapshot{Bindings: append([]any(nil), state.Bindings["where"]...)}
	for _, where := range state.Wheres {
		entry := whereSnapshotEntry{Type: string(where.Type), Boolean: where.Boolean, Column: where.Column, Value: where.Value, SQL: where.SQL}
		if where.Type == core.WhereNestedType && where.Query != nil {
			entry.Group = snapshotWheres(where.Query)
		}
		out.Wheres = append(out.Wheres, entry)
	}

	return out
}

// deferredAltUser is a second soft-deletable owner of sd_posts, so one query can be compiled for two
// destinations.
type deferredAltUser struct {
	ID    uint
	Posts []*scpPost `gorm:"-"`
	databaseorm.SoftDeletes
}

func (deferredAltUser) TableName() string { return "alt_users" }

func (deferredAltUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &scpPost{}, ForeignKey: "alt_user_id"},
	}
}

// TestDeferredWhere_TheSameQueryCompilesAgainstEachDestination pins that a query learning its model
// from the destination renders its deferred entries per compile: compiled for one model and then for
// another, each statement correlates to its own model, which only holds when the first compile wrote
// nothing back.
func TestDeferredWhere_TheSameQueryCompilesAgainstEachDestination(t *testing.T) {
	query := compiledQuery(t, nil, Conditions{}).Where("id", 1).
		WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 2) }).
		Where(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }).(*OrmBuilder)
	compile := func(dest any) (string, []any) {
		statement, bindings, err := query.dest(dest).compileSelect()
		require.NoError(t, err)

		return statement, bindings
	}

	statement, bindings := compile(&[]scpSdUser{})
	assert.Equal(t, `select * from "sd_users" where "id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and ("sd_users"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, statement)
	assert.Equal(t, []any{1, 2}, bindings)

	statement, bindings = compile(&[]deferredAltUser{})
	assert.Equal(t, `select * from "alt_users" where "id" = ? and exists (select * from "sd_posts" where "alt_users"."id" = "sd_posts"."alt_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and ("alt_users"."deleted_at" is not null) and "alt_users"."deleted_at" is null`, statement)
	assert.Equal(t, []any{1, 2}, bindings)

	_, _, err := query.dest(&[]relUser{}).compileSelect()
	assert.Error(t, err, "rel_users has no Posts relation, so its render fails instead of reusing an earlier render")

	statement, _ = compile(&[]scpSdUser{})
	assert.Equal(t, `select * from "sd_users" where "id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and ("sd_users"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, statement)
}

// TestDeferredWhere_ACloneDoesNotSeeLaterEntries pins that a clone owns its where list: a deferred
// entry written to the original after the clone - at the top level or inside a closure - does not
// reach the clone, and one written to the clone does not reach the original.
func TestDeferredWhere_ACloneDoesNotSeeLaterEntries(t *testing.T) {
	original := compiledQuery(t, &scpSdUser{}, Conditions{}).Where("id", 1).(*OrmBuilder)
	clone := original.Clone().(*OrmBuilder)

	original.WhereHas("Posts").Where(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() })
	clone.Where("email", "a").OrWhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 2) })

	const originalSQL = `select * from "sd_users" where "id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null) and ("sd_users"."deleted_at" is not null) and "sd_users"."deleted_at" is null`

	statement, bindings := compileDeferred(t, original)
	assert.Equal(t, originalSQL, statement)
	assert.Equal(t, []any{1}, bindings)

	statement, bindings = compileDeferred(t, clone)
	assert.Equal(t, `select * from "sd_users" where ("id" = ? and "email" = ? or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, statement)
	assert.Equal(t, []any{1, "a", 2}, bindings)

	// A clone taken after the deferred entries carries them and compiles them on its own; what the
	// original writes afterwards stays on the original.
	carried := original.Clone().(*OrmBuilder)
	original.Where("email", "z").WhereHas("Posts")
	statement, bindings = compileDeferred(t, carried)
	assert.Equal(t, originalSQL, statement)
	assert.Equal(t, []any{1}, bindings)
}

// TestDeferredWhere_ToSqlThenExecuteAgree pins that rendering for ToSql leaves the query able to run
// the same statement: ToSql().Get and the Get that follows it on the same query compile identical
// SQL - here with the model learned from the destination - and the executed statement carries the
// bindings in placeholder order.
func TestDeferredWhere_ToSqlThenExecuteAgree(t *testing.T) {
	query, recorder := newRecordingRelQuery(t, nil)
	query = query.Where("id", 1).
		WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 2) }).
		Where(func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "a").OnlyTrashed() }).
		Where("id", 3).(*OrmBuilder)

	var users []scpSdUser
	rendered := query.ToSql().Get(&users)
	require.NoError(t, query.Get(&users))

	executed := recorder.last(t)
	assert.Equal(t, rendered, executed.sql)
	assert.Equal(t, `select * from "sd_users" where "id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and ("email" = ? and "sd_users"."deleted_at" is not null) and "id" = ? and "sd_users"."deleted_at" is null`, executed.sql)
	assert.Equal(t, []driver.Value{int64(1), int64(2), "a", int64(3)}, executed.args)

	// Rendering again after the run still agrees.
	assert.Equal(t, rendered, query.ToSql().Get(&users))
}

// TestDeferredWhere_OnlyTrashedOnANonSoftDeleteModelKeepsBindingsAligned pins an entry that renders
// nothing: OnlyTrashed on a model without the framework's soft-delete type writes no predicate, so
// its entry and its binding marker are both removed - at the top level and inside a closure, where a
// group left empty disappears - and the bindings of the clauses around it stay in placeholder order.
// Port-only shape: Laravel's onlyTrashed is a SoftDeletingScope macro, so on such a model the call
// itself throws BadMethodCallException (oracle D4, /tmp/r15c-probes/scope_deferred.out).
func TestDeferredWhere_OnlyTrashedOnANonSoftDeleteModelKeepsBindingsAligned(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).
		Where("id", 1).
		OnlyTrashed().
		Where("name", "b").
		WhereHas("Books", func(q contractsorm.Query) contractsorm.Query { return q.Where("id", 3) }).
		Where(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed().Where("name", "c") }).
		Where(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }).
		Where("id", 5).(*OrmBuilder)

	statement, bindings := compileDeferred(t, query)
	assert.Equal(t, `select * from "rel_users" where "id" = ? and "name" = ? and exists (select * from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "id" = ?) and ("name" = ?) and "id" = ?`, statement)
	assert.Equal(t, []any{1, "b", 3, "c", 5}, bindings)
}

// TestDeferredWhere_EntryInsideAClosureInsideAUserScope pins a deferred entry two levels down: a
// scope's where closure calling whereHas (oracle D1) or onlyTrashed (oracle D2), the scope mounted
// on the builder with ApplyScope — the mount deferredScopeUser / deferredTrashedScopeUser used to
// declare. The scope applies after SoftDeletingScope, so the "is null" precedes the scope's wheres,
// and the closure's clause renders inside the closure's group at its call position.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r15c-probes/scope_deferred.php ->
// scope_deferred.out):
//
//	D1|select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is null and "active" = ? and ("id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null)) and "id" = ?|["a",1,2,3,4]
//	D2|select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is null and ("id" = ? and "sd_users"."deleted_at" is not null) and "id" = ?|["a",2,4]
func TestDeferredWhere_EntryInsideAClosureInsideAUserScope(t *testing.T) {
	d1 := func() *OrmBuilder {
		return compiledQuery(t, &deferredScopeUser{}, Conditions{}).ApplyScope("hasPosts", deferredHasPostsScope).(*OrmBuilder)
	}
	d2 := func() *OrmBuilder {
		return compiledQuery(t, &deferredTrashedScopeUser{}, Conditions{}).ApplyScope("closureTrashed", deferredClosureTrashedScope).(*OrmBuilder)
	}

	runScrCases(t, []scrCase{
		{"D1 closure whereHas inside a user scope", d1().Where("email", "a"),
			`select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is null and "active" = ? and ("id" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null)) and "id" = ?`,
			[]any{"a", 1, 2, 3, 4}},
		{"D2 closure onlyTrashed inside a user scope", d2().Where("email", "a"),
			`select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is null and ("id" = ? and "sd_users"."deleted_at" is not null) and "id" = ?`,
			[]any{"a", 2, 4}},
	})

	// The scoped query compiles the same statement twice.
	query := d1().Where("email", "a").(*OrmBuilder)
	first, firstBindings := compileDeferred(t, query)
	second, secondBindings := compileDeferred(t, query)
	assert.Equal(t, first, second)
	assert.Equal(t, firstBindings, secondBindings)
}

// deferredOrHasScopeUser is the model behind the "activeOrPosts" scope (deferredActiveOrPostsScope),
// whose or-chain ends in orWhereHas (oracle E1, E2, /tmp/r15c-probes/scope_edges.php). The scope
// used to be declared GlobalScopes(); the test mounts it on the builder with ApplyScope now.
type deferredOrHasScopeUser struct {
	ID     uint
	Email  string
	Active int
	Posts  []*scpPost `gorm:"-"`
	databaseorm.SoftDeletes
}

func (deferredOrHasScopeUser) TableName() string { return "sd_users" }

func (deferredOrHasScopeUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &scpPost{}, ForeignKey: "sd_user_id"},
	}
}

// deferredActiveOrPostsScope is the "activeOrPosts" scope the E1/E2 cases mount on the builder.
func deferredActiveOrPostsScope(q contractsorm.Query) contractsorm.Query {
	return q.Where("active", 1).OrWhereHas("Posts")
}

// deferredComment, deferredScopedPost and deferredRelScopeUser model a related model whose own
// global scope calls whereHas, read through an existence / count subquery (oracle E3, E4).
type deferredComment struct {
	ID       uint
	SdPostID uint
}

func (deferredComment) TableName() string { return "sd_comments" }

type deferredScopedPost struct {
	ID       uint
	SdUserID uint
	Comments []*deferredComment `gorm:"-"`
	databaseorm.SoftDeletes
}

func (deferredScopedPost) TableName() string { return "sd_posts" }

func (deferredScopedPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Comments": contractsorm.HasMany{Related: &deferredComment{}, ForeignKey: "sd_post_id"},
	}
}

func (deferredScopedPost) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "commented", Scope: func(q contractsorm.Query) contractsorm.Query { return q.WhereHas("Comments") }},
	}
}

type deferredRelScopeUser struct {
	ID    uint
	Posts []*deferredScopedPost `gorm:"-"`
	databaseorm.SoftDeletes
}

func (deferredRelScopeUser) TableName() string { return "sd_users" }

func (deferredRelScopeUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &deferredScopedPost{}, ForeignKey: "sd_user_id"},
	}
}

// TestDeferredWhere_ScopeExistenceRendersInTheScopesSlice pins an existence clause a scope writes:
// it is part of the scope's slice where Laravel's callScope regroups it, so a mounted scope's
// or-chain ending in orWhereHas becomes one group (E1, E2), and a related model's scope calling
// whereHas renders inside the existence / count subquery, correlated to the related table (E3, E4).
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r15c-probes/scope_edges.php ->
// scope_edges.out):
//
//	E1|select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is null and ("active" = ? or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null))|["a",1]
//	E2|select * from "sd_users" where ("email" = ? or "email" = ?) and "sd_users"."deleted_at" is null and ("active" = ? or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null))|["a","b",1]
//	E3|select * from "sd_users" where exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null and exists (select * from "sd_comments" where "sd_posts"."id" = "sd_comments"."sd_post_id")) and "sd_users"."deleted_at" is null|[]
//	E4|select "sd_users".*, (select count(*) from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null and exists (select * from "sd_comments" where "sd_posts"."id" = "sd_comments"."sd_post_id")) as "posts_count" from "sd_users" where "sd_users"."deleted_at" is null|[]
func TestDeferredWhere_ScopeExistenceRendersInTheScopesSlice(t *testing.T) {
	orHas := func() *OrmBuilder {
		return compiledQuery(t, &deferredOrHasScopeUser{}, Conditions{}).ApplyScope("activeOrPosts", deferredActiveOrPostsScope).(*OrmBuilder)
	}
	relScope := func() *OrmBuilder { return compiledQuery(t, &deferredRelScopeUser{}, Conditions{}) }

	runScrCases(t, []scrCase{
		{"E1 scope where then orWhereHas", orHas().Where("email", "a"),
			`select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is null and ("active" = ? or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null))`,
			[]any{"a", 1}},
		{"E2 the same under a caller or-chain", orHas().Where("email", "a").OrWhere("email", "b"),
			`select * from "sd_users" where ("email" = ? or "email" = ?) and "sd_users"."deleted_at" is null and ("active" = ? or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null))`,
			[]any{"a", "b", 1}},
		{"E3 related scope whereHas inside whereHas", relScope().WhereHas("Posts"),
			`select * from "sd_users" where exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null and exists (select * from "sd_comments" where "sd_posts"."id" = "sd_comments"."sd_post_id")) and "sd_users"."deleted_at" is null`,
			nil},
		{"E4 related scope whereHas inside withCount", relScope().WithCount("Posts"),
			`select "sd_users".*, (select count(*) from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null and exists (select * from "sd_comments" where "sd_posts"."id" = "sd_comments"."sd_post_id")) as "posts_count" from "sd_users" where "sd_users"."deleted_at" is null`,
			nil},
	})
}

// deferredPlainTrashedScopeUser is a model without the framework's soft-delete type behind the
// "trashedOnly" scope (deferredTrashedOnlyScope), which calls onlyTrashed and nothing else. The
// scope used to be declared GlobalScopes(); the guard test mounts it on the builder with ApplyScope
// now.
type deferredPlainTrashedScopeUser struct {
	ID   uint
	Name string
}

func (deferredPlainTrashedScopeUser) TableName() string { return "rel_users" }

// deferredTrashedOnlyScope is the "trashedOnly" scope the guard test mounts on the builder.
func deferredTrashedOnlyScope(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }

// TestDeferredWhere_MassWriteGuardCountsWhatRenders pins the mass-update / delete guard over deferred
// entries: an update or delete constrained only by a whereHas carries the rendered exists and runs,
// while one constrained only by an onlyTrashed on a model without the framework's soft-delete type -
// the caller's, or one a user scope wrote - renders no where at all and is refused, as an
// unconstrained statement is.
func TestDeferredWhere_MassWriteGuardCountsWhatRenders(t *testing.T) {
	statement, bindings, err := compiledQuery(t, &relUser{}, Conditions{}).WhereHas("Books").(*OrmBuilder).compileUpdate(map[string]any{"name": "x"})
	require.NoError(t, err)
	assert.Equal(t, `update "rel_users" set "name" = ? where exists (select * from "rel_books" where "rel_users"."id" = "rel_books"."user_id")`, statement)
	assert.Equal(t, []any{"x"}, bindings)

	statement, _, err = compiledQuery(t, &relUser{}, Conditions{}).WhereHas("Books").(*OrmBuilder).compileDelete()
	require.NoError(t, err)
	assert.Equal(t, `delete from "rel_users" where exists (select * from "rel_books" where "rel_users"."id" = "rel_books"."user_id")`, statement)

	_, _, err = compiledQuery(t, &relUser{}, Conditions{}).OnlyTrashed().(*OrmBuilder).compileUpdate(map[string]any{"name": "x"})
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)

	_, _, err = compiledQuery(t, &relUser{}, Conditions{}).OnlyTrashed().(*OrmBuilder).compileDelete()
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)

	_, _, err = compiledQuery(t, &deferredPlainTrashedScopeUser{}, Conditions{}).ApplyScope("trashedOnly", deferredTrashedOnlyScope).(*OrmBuilder).compileUpdate(map[string]any{"name": "x"})
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)

	_, _, err = compiledQuery(t, &deferredPlainTrashedScopeUser{}, Conditions{}).ApplyScope("trashedOnly", deferredTrashedOnlyScope).(*OrmBuilder).compileDelete()
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)
}

// deferredOfManyOwner declares a latestOfMany relation over relDeepMid (soft delete + the "active"
// user scope) whose chain after OfMany writes a where closure holding an onlyTrashed (oracle K1,
// /tmp/r15c-probes/ofmany_closure_trashed.php).
type deferredOfManyOwner struct {
	ID  uint
	Mid *relDeepMid `gorm:"-"`
}

func (deferredOfManyOwner) TableName() string { return "rel_deep_owners" }

func (deferredOfManyOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"OtClosureAfter": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("", "").Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.Where("id", 9).OnlyTrashed() })
			}},
	}
}

// TestDeferredWhere_ClosureAfterOfManyStaysOnTheRelationQuery pins that a where closure holding a
// deferred entry, chained after OfMany, stays on the relation query: it is Laravel's where chained
// after latestOfMany(), so the group - its onlyTrashed and its binding with it - stays on the relation
// query, and the aggregate subquery keeps the related model's full scopes. The port names the join
// alias after the relation (the documented alias gap); every other byte is the oracle's.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r15c-probes/ofmany_closure_trashed.php ->
// ofmany_closure_trashed.out):
//
//	K1|select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "latestOfMany" on "latestOfMany"."id_aggregate" = "rel_deep_mids"."id" and "latestOfMany"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and ("id" = ? and "rel_deep_mids"."deleted_at" is not null) and "rel_deep_mids"."deleted_at" is null and "active" = ?)|[1,9,1]
func TestDeferredWhere_ClosureAfterOfManyStaysOnTheRelationQuery(t *testing.T) {
	runScrCases(t, []scrCase{
		{"K1 whereHas over OfMany then a closure with onlyTrashed", newRelQueryWith(t, &deferredOfManyOwner{}).WhereHas("OtClosureAfter"),
			`select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "ot_closure_after" on "ot_closure_after"."id_aggregate" = "rel_deep_mids"."id" and "ot_closure_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and ("id" = ? and "rel_deep_mids"."deleted_at" is not null) and "rel_deep_mids"."deleted_at" is null and "active" = ?)`,
			[]any{1, 9, 1}},
	})
}
