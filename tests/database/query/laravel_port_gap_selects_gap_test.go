package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
)

// This file ports the selects-gap group of Laravel's tests/Database/DatabaseQueryBuilderTest.php
// (laravel/framework 12.x commit 0fc645c6737626a1b1636cd234192b0d78b76569). Each case rebuilds the
// corpus chain with our Builder and asserts the exact SQL string. SQL assertions go through
// runLaravelCases / runDialectCases; where the corpus also asserts bindings, they are asserted
// explicitly in the owning test because the shared helpers only compare the compiled SQL.

// TestFrom ports Laravel's testFrom.
func TestFrom(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"subquery from", func(b *Builder) *Builder {
			return b.From(NewBuilder(NewBaseGrammar("")).From("users", ""), "u")
		}, `select * from (select * from "users") as "u"`},
	})
	// The corpus repeats the same assertion with the inner builder wrapped in an EloquentBuilder.
	// We have no Eloquent layer, and EloquentBuilder::from() delegates straight to the query
	// builder, so the wrapped form compiles to the same SQL and has no separate Go entry point.
}

// TestFromRaw ports Laravel's testFromRaw.
func TestFromRaw(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"raw from", func(b *Builder) *Builder {
			return b.FromRaw(`(select max(last_seen_at) as last_seen_at from "user_sessions") as "sessions"`, nil)
		}, `select * from (select max(last_seen_at) as last_seen_at from "user_sessions") as "sessions"`},
	})
}

// TestFromRawOnSqlServer ports Laravel's testFromRawOnSqlServer. SQL Server's compileFrom emits a
// raw table expression verbatim instead of wrapping it in brackets.
func TestFromRawOnSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"sqlserver raw from", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.FromRaw(`dbo.[SomeNameWithRoundBrackets (test)]`, nil)
		}, `select * from dbo.[SomeNameWithRoundBrackets (test)]`},
	})
}

// TestFromRawWithWhereOnTheMainQuery ports Laravel's testFromRawWithWhereOnTheMainQuery.
func TestFromRawWithWhereOnTheMainQuery(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"raw from with where", func(b *Builder) *Builder {
			return b.FromRaw(`(select max(last_seen_at) as last_seen_at from "sessions") as "last_seen_at"`, nil).
				Where("last_seen_at", ">", "1520652582")
		}, `select * from (select max(last_seen_at) as last_seen_at from "sessions") as "last_seen_at" where "last_seen_at" > ?`},
	})

	b := newTestBuilder().FromRaw(`(select max(last_seen_at) as last_seen_at from "sessions") as "last_seen_at"`, nil).
		Where("last_seen_at", ">", "1520652582")
	assert.Equal(t, []any{"1520652582"}, b.GetBindings())
}

// TestFromSub ports Laravel's testFromSub.
func TestFromSub(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"from sub with where", func(b *Builder) *Builder {
			return b.FromSub(func(q *Builder) *Builder {
				return q.Select(Raw("max(last_seen_at) as last_seen_at")).From("user_sessions", "").Where("foo", "=", "1")
			}, "sessions").Where("bar", "<", "10")
		}, `select * from (select max(last_seen_at) as last_seen_at from "user_sessions" where "foo" = ?) as "sessions" where "bar" < ?`},
	})

	b := newTestBuilder().FromSub(func(q *Builder) *Builder {
		return q.Select(Raw("max(last_seen_at) as last_seen_at")).From("user_sessions", "").Where("foo", "=", "1")
	}, "sessions").Where("bar", "<", "10")
	assert.Equal(t, []any{"1", "10"}, b.GetBindings())

	// Corpus: fromSub(['invalid'], 'sessions') throws InvalidArgumentException. Our parseSub records
	// QuerySubqueryTypeInvalid for a non-queryable subquery instead.
	invalid := newTestBuilder().FromSub([]string{"invalid"}, "sessions")
	assert.ErrorIs(t, invalid.Error(), errors.QuerySubqueryTypeInvalid)
}

// TestFromSubWithPrefix ports Laravel's testFromSubWithPrefix. The corpus builds with
// getBuilder(prefix: 'prefix_'), which is the grammar prefix we pass to NewBaseGrammar.
func TestFromSubWithPrefix(t *testing.T) {
	b := NewBuilder(NewBaseGrammar("prefix_")).FromSub(func(q *Builder) *Builder {
		return q.Select(Raw("max(last_seen_at) as last_seen_at")).From("user_sessions", "").Where("foo", "=", "1")
	}, "sessions").Where("bar", "<", "10")

	assert.Equal(t, `select * from (select max(last_seen_at) as last_seen_at from "prefix_user_sessions" where "foo" = ?) as "prefix_sessions" where "bar" < ?`, b.ToSQL())
	assert.Equal(t, []any{"1", "10"}, b.GetBindings())
}

// TestFromSubWithoutBindings ports Laravel's testFromSubWithoutBindings.
func TestFromSubWithoutBindings(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"from sub without bindings", func(b *Builder) *Builder {
			return b.FromSub(func(q *Builder) *Builder {
				return q.Select(Raw("max(last_seen_at) as last_seen_at")).From("user_sessions", "")
			}, "sessions")
		}, `select * from (select max(last_seen_at) as last_seen_at from "user_sessions") as "sessions"`},
	})

	// Corpus: fromSub(['invalid'], 'sessions') throws InvalidArgumentException; our parseSub records
	// QuerySubqueryTypeInvalid instead.
	invalid := newTestBuilder().FromSub([]string{"invalid"}, "sessions")
	assert.ErrorIs(t, invalid.Error(), errors.QuerySubqueryTypeInvalid)
}

// TestGroupBys ports Laravel's testGroupBys.
func TestGroupBys(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"group by one", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").GroupBy("email")
		}, `select * from "users" group by "email"`},
		{"group by variadic", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").GroupBy("id", "email")
		}, `select * from "users" group by "id", "email"`},
		{"group by slice", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").GroupBy([]any{"id", "email"})
		}, `select * from "users" group by "id", "email"`},
		{"group by raw expression", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").GroupBy(Raw("DATE(created_at)"))
		}, `select * from "users" group by DATE(created_at)`},
		{"group by raw", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").GroupByRaw("DATE(created_at), ? DESC", []any{"foo"})
		}, `select * from "users" group by DATE(created_at), ? DESC`},
	})

	raw := newTestBuilder().From("users", "").Select("*").GroupByRaw("DATE(created_at), ? DESC", []any{"foo"})
	assert.Equal(t, []any{"foo"}, raw.GetBindings())

	ordering := newTestBuilder().
		HavingRaw("?", []any{"havingRawBinding"}, core.BooleanAnd).
		GroupByRaw("?", []any{"groupByRawBinding"}).
		WhereRaw("?", []any{"whereRawBinding"}, core.BooleanAnd)
	assert.Equal(t, []any{"whereRawBinding", "groupByRawBinding", "havingRawBinding"}, ordering.GetBindings())
}

// TestIgnoreIndexSqlite ports Laravel's testIgnoreIndexSqlite: SQLite drops an ignore hint.
func TestIgnoreIndexSqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"sqlite ignores ignore", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("foo").From("users", "").IgnoreIndex("test_index")
		}, `select "foo" from "users"`},
	})
}

// TestUseIndexSqlServer ports Laravel's testUseIndexSqlServer: SQL Server drops a plain use hint.
func TestUseIndexSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"sqlserver drops use", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("foo").From("users", "").UseIndex("test_index")
		}, `select [foo] from [users]`},
	})
}

// TestIgnoreIndexSqlServer ports Laravel's testIgnoreIndexSqlServer: SQL Server drops an ignore hint.
func TestIgnoreIndexSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"sqlserver drops ignore", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("foo").From("users", "").IgnoreIndex("test_index")
		}, `select [foo] from [users]`},
	})
}

// TestLatest ports Laravel's testLatest. Our Latest(column) takes the column explicitly; an empty
// string selects the created_at default the corpus relies on.
func TestLatest(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"latest defaults to created_at", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Latest("")
		}, `select * from "users" order by "created_at" desc`},
		{"latest with limit", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Latest("").Limit(1)
		}, `select * from "users" order by "created_at" desc limit 1`},
		{"latest on a column", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Latest("updated_at")
		}, `select * from "users" order by "updated_at" desc`},
	})
}

// TestOldest ports Laravel's testOldest.
func TestOldest(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"oldest defaults to created_at", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Oldest("")
		}, `select * from "users" order by "created_at" asc`},
		{"oldest on a column", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Oldest("updated_at")
		}, `select * from "users" order by "updated_at" asc`},
	})
}

// TestLimitsAndOffsets ports Laravel's testLimitsAndOffsets. Our Limit(int)/Offset(int) cannot carry
// the null the corpus passes: limit(null) leaves the clause unset (so it is the omitted call here),
// and offset(null) clamps to 0 exactly as offset(0) does.
func TestLimitsAndOffsets(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"offset then limit", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Offset(5).Limit(10)
		}, `select * from "users" limit 10 offset 5`},
		// corpus: limit(null) -> the clause is dropped.
		{"limit null omitted", func(b *Builder) *Builder {
			return b.From("users", "").Select("*")
		}, `select * from "users"`},
		{"limit zero", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Limit(0)
		}, `select * from "users" limit 0`},
		{"zero offset and limit", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Offset(0).Limit(0)
		}, `select * from "users" limit 0 offset 0`},
		// corpus: offset(-5)->limit(-10); a negative limit is rejected and a negative offset clamps to 0.
		{"negative clamps", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Offset(-5).Limit(-10)
		}, `select * from "users" offset 0`},
		// corpus: offset(null)->limit(null); the offset null clamps to 0.
		{"null offset clamps", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Offset(0)
		}, `select * from "users" offset 0`},
		// corpus: offset(5)->limit(null) -> only the offset survives.
		{"offset without limit", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Offset(5)
		}, `select * from "users" offset 5`},
	})
}

// TestOrderByInvalidDirectionParam ports Laravel's testOrderByInvalidDirectionParam. Laravel throws
// InvalidArgumentException from orderBy(); our OrderBy records QueryOrderDirectionInvalid and the
// fluent builder exposes it through Error().
func TestOrderByInvalidDirectionParam(t *testing.T) {
	b := newTestBuilder().From("users", "").Select("*").OrderBy("age", "asec")

	assert.ErrorIs(t, b.Error(), errors.QueryOrderDirectionInvalid)
}

// TestOrderBySubQueries ports Laravel's testOrderBySubQueries. Our OrderBy requires the direction
// explicitly (Laravel defaults it to asc), so the default-direction case passes "asc".
func TestOrderBySubQueries(t *testing.T) {
	sub := func(q *Builder) *Builder {
		return q.Select("created_at").From("logins", "").WhereColumn("user_id", "=", "users.id", core.BooleanAnd).Limit(1)
	}

	runLaravelCases(t, []laravelCase{
		{"order by closure defaults to asc", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy(func(q *Builder) *Builder {
				return q.Select("created_at").From("logins", "").WhereColumn("user_id", "=", "users.id", core.BooleanAnd).Limit(1)
			}, "asc")
		}, `select * from "users" order by (select "created_at" from "logins" where "user_id" = "users"."id" limit 1) asc`},
		{"order by closure desc", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy(func(q *Builder) *Builder {
				return q.Select("created_at").From("logins", "").WhereColumn("user_id", "=", "users.id", core.BooleanAnd).Limit(1)
			}, "desc")
		}, `select * from "users" order by (select "created_at" from "logins" where "user_id" = "users"."id" limit 1) desc`},
		{"order by desc closure", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderByDesc(sub)
		}, `select * from "users" order by (select "created_at" from "logins" where "user_id" = "users"."id" limit 1) desc`},
		{"order by subquery after union all", func(b *Builder) *Builder {
			videos := NewBuilder(NewBaseGrammar("")).From("videos", "").Select("*").Where("public", 1)
			order := NewBuilder(NewBaseGrammar("")).SelectRaw("field(category, ?, ?)", []any{"news", "opinion"})
			return b.From("posts", "").Select("*").Where("public", 1).UnionAll(videos).OrderBy(order, "asc")
		}, `(select * from "posts" where "public" = ?) union all (select * from "videos" where "public" = ?) order by (select field(category, ?, ?)) asc`},
	})

	videos := NewBuilder(NewBaseGrammar("")).From("videos", "").Select("*").Where("public", 1)
	order := NewBuilder(NewBaseGrammar("")).SelectRaw("field(category, ?, ?)", []any{"news", "opinion"})
	unionOrder := newTestBuilder().From("posts", "").Select("*").Where("public", 1).UnionAll(videos).OrderBy(order, "asc")
	assert.Equal(t, []any{1, 1, "news", "opinion"}, unionOrder.GetBindings())
}

// TestOrderBys ports Laravel's testOrderBys. The corpus clears $builder->orders directly to prove an
// unset order clause renders nothing; Reorder() clears orders the same way and is used here.
func TestOrderBys(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"order by two columns", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("email", "asc").OrderBy("age", "desc")
		}, `select * from "users" order by "email" asc, "age" desc`},
		{"cleared orders render nothing", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("email", "asc").Reorder(nil, "")
		}, `select * from "users"`},
		{"order by raw", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("email", "asc").OrderByRaw(`"age" ? desc`, []any{"foo"})
		}, `select * from "users" order by "email" asc, "age" ? desc`},
		{"order by desc", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderByDesc("name")
		}, `select * from "users" order by "name" desc`},
		{"order by raw after union all", func(b *Builder) *Builder {
			videos := NewBuilder(NewBaseGrammar("")).From("videos", "").Select("*").Where("public", 1)
			return b.From("posts", "").Select("*").Where("public", 1).UnionAll(videos).
				OrderByRaw("field(category, ?, ?) asc", []any{"news", "opinion"})
		}, `(select * from "posts" where "public" = ?) union all (select * from "videos" where "public" = ?) order by field(category, ?, ?) asc`},
	})

	raw := newTestBuilder().From("users", "").Select("*").OrderBy("email", "asc").OrderByRaw(`"age" ? desc`, []any{"foo"})
	assert.Equal(t, []any{"foo"}, raw.GetBindings())

	videos := NewBuilder(NewBaseGrammar("")).From("videos", "").Select("*").Where("public", 1)
	unionOrder := newTestBuilder().From("posts", "").Select("*").Where("public", 1).UnionAll(videos).
		OrderByRaw("field(category, ?, ?) asc", []any{"news", "opinion"})
	assert.Equal(t, []any{1, 1, "news", "opinion"}, unionOrder.GetBindings())
}

// TestOrderBysSqlServer ports Laravel's testOrderBysSqlServer.
func TestOrderBysSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"two columns", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("email", "asc").OrderBy("age", "desc")
		}, `select * from [users] order by [email] asc, [age] desc`},
		{"single column", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("email", "asc")
		}, `select * from [users] order by [email] asc`},
		{"order by desc", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderByDesc("name")
		}, `select * from [users] order by [name] desc`},
		{"order by raw", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderByRaw("[age] asc", nil)
		}, `select * from [users] order by [age] asc`},
		{"order by raw binding", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("email", "asc").OrderByRaw("[age] ? desc", []any{"foo"})
		}, `select * from [users] order by [email] asc, [age] ? desc`},
		{"offset and limit become fetch", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Offset(25).Limit(10).OrderByRaw("[email] desc", nil)
		}, `select * from [users] order by [email] desc offset 25 rows fetch next 10 rows only`},
	})
}

// TestReorder ports Laravel's testReorder.
func TestReorder(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"reorder clears", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("name", "asc").Reorder(nil, "")
		}, `select * from "users"`},
		{"reorder replaces", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("name", "asc").Reorder("email", "desc")
		}, `select * from "users" order by "email" desc`},
		{"reorder after union", func(b *Builder) *Builder {
			return b.From("first", "").Select("*").
				Union(NewBuilder(NewBaseGrammar("")).From("second", "").Select("*"), false).
				OrderBy("name", "asc")
		}, `(select * from "first") union (select * from "second") order by "name" asc`},
		{"reorder clears union orders", func(b *Builder) *Builder {
			return b.From("first", "").Select("*").
				Union(NewBuilder(NewBaseGrammar("")).From("second", "").Select("*"), false).
				OrderBy("name", "asc").Reorder(nil, "")
		}, `(select * from "first") union (select * from "second")`},
	})

	raw := newTestBuilder().From("users", "").Select("*").OrderByRaw("?", []any{true})
	assert.Equal(t, []any{true}, raw.GetBindings())
	// Corpus asserts assertEquals([], getBindings()); ours renders no bindings at all.
	assert.Empty(t, raw.Reorder(nil, "").GetBindings())
}

// TestSQLiteOrderBy ports Laravel's testSQLiteOrderBy.
func TestSQLiteOrderBy(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"sqlite order by", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("*").OrderBy("email", "desc")
		}, `select * from "users" order by "email" desc`},
	})
}
