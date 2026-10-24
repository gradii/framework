package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the binding half of Laravel's testAggregateWithSubSelect
// (tests/Database/DatabaseQueryBuilderTest.php, laravel/framework 12.x) and pins the surrounding
// behaviour of the copy Builder::aggregate() compiles.
//
// testAggregateWithSubSelect hands the connection "select count(*) as aggregate from \"users\"" with
// an EMPTY binding list, even though the builder itself still carries the subselect's "foo" binding
// (the corpus asserts exactly that through its last two lines). The ported case in
// laravel_port_gap_aggregates_gap_test.go discards the bindings ToAggregateSQL returns, so nothing
// covered the difference; the cases below assert them.
//
// Laravel produces that empty list before compiling, in aggregate():
//
//	$this->cloneWithout($this->unions || $this->havings ? [] : ['columns'])
//	     ->cloneWithoutBindings($this->unions || $this->havings ? [] : ['select'])
//	     ->setAggregate($function, $columns)
//	     ->get($columns);
//
// The columns go because the aggregate selects none of its own (compileColumns is skipped whenever an
// aggregate is set), and the select bindings go with them so the executor is never handed a value
// whose placeholder is no longer in the SQL. When the query has unions or havings the select list
// survives inside the derived table and so do its bindings. setAggregate() additionally clears the
// orders, and their bindings, whenever the query has no groups.
//
// Every expectation below was measured on illuminate/database v12.55.1 through the oracle, on all
// four dialects: the SQL and the bindings handed to Connection::select().

// aggregateSubSelect builds the query testAggregateWithSubSelect builds: a selectSub whose inner
// where contributes one "select" binding.
func aggregateSubSelect(b *Builder) *Builder {
	return b.From("users", "").SelectSub(func(query *Builder) *Builder {
		return query.From("posts", "").Select("foo", "bar").Where("title", "foo")
	}, "post")
}

// TestAggregateWithSubSelectBindings ports the binding assertion testAggregateWithSubSelect makes on
// the mocked connection: count() issues its aggregate with no bindings at all.
func TestAggregateWithSubSelectBindings(t *testing.T) {
	builder := aggregateSubSelect(newTestBuilder())

	sql, bindings := builder.ToAggregateSQL(core.Aggregate{Function: "count", Columns: []any{"*"}})

	assert.Equal(t, `select count(*) as aggregate from "users"`, sql)
	// Emphatically empty: the subselect's "foo" must not reach the executor. A nil and an empty
	// slice both render as an empty argument list, so assert emptiness rather than identity.
	assert.Empty(t, bindings)

	// The builder the caller still holds is untouched, which is what the corpus asserts next: its
	// select list is intact and still carries the binding the subselect contributed.
	selects := builder.Selects()
	assert.Len(t, selects, 1)
	assert.Equal(t, []any{"foo"}, builder.GetBindings())
}

// TestAggregateBindingsPerDialect repeats the shape above where the identifier quoting differs, so a
// dialect-specific compile path cannot quietly keep the binding Laravel drops.
func TestAggregateBindingsPerDialect(t *testing.T) {
	cases := []struct {
		name    string
		grammar Grammar
		want    string
	}{
		{"base", NewBaseGrammar(""), `select count(*) as aggregate from "users"`},
		{"mysql", NewMySqlGrammar(""), "select count(*) as aggregate from `users`"},
		{"postgres", NewPostgresGrammar(""), `select count(*) as aggregate from "users"`},
		{"sqlite", NewSqliteGrammar(""), `select count(*) as aggregate from "users"`},
		{"sqlserver", NewSqlServerGrammar(""), "select count(*) as aggregate from [users]"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := aggregateSubSelect(NewBuilder(test.grammar))

			sql, bindings := builder.ToAggregateSQL(core.Aggregate{Function: "count", Columns: []any{"*"}})

			assert.Equal(t, test.want, sql)
			assert.Empty(t, bindings)
		})
	}
}

// TestAggregateKeepsSelectBindingsWithUnion pins the other half of Laravel's condition: with a union
// the select list lives on inside the derived table, so its placeholder is still in the SQL and its
// binding must be kept.
func TestAggregateKeepsSelectBindingsWithUnion(t *testing.T) {
	cases := []struct {
		name    string
		grammar Grammar
		want    string
	}{
		{
			"mysql", NewMySqlGrammar(""),
			"select count(*) as aggregate from ((select (select `foo`, `bar` from `posts` where `title` = ?) as `post` from `users`) union (select * from `videos`)) as `temp_table`",
		},
		{
			"postgres", NewPostgresGrammar(""),
			`select count(*) as aggregate from ((select (select "foo", "bar" from "posts" where "title" = ?) as "post" from "users") union (select * from "videos")) as "temp_table"`,
		},
		{
			"sqlite", NewSqliteGrammar(""),
			`select count(*) as aggregate from (select * from (select (select "foo", "bar" from "posts" where "title" = ?) as "post" from "users") union select * from (select * from "videos")) as "temp_table"`,
		},
		{
			"sqlserver", NewSqlServerGrammar(""),
			"select count(*) as aggregate from (select * from (select (select [foo], [bar] from [posts] where [title] = ?) as [post] from [users]) as [temp_table] union select * from (select * from [videos]) as [temp_table]) as [temp_table]",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := aggregateSubSelect(NewBuilder(test.grammar))
			builder.Union(NewBuilder(test.grammar).From("videos", ""), false)

			sql, bindings := builder.ToAggregateSQL(core.Aggregate{Function: "count", Columns: []any{"*"}})

			assert.Equal(t, test.want, sql)
			assert.Equal(t, []any{"foo"}, bindings)
		})
	}
}

// TestAggregateKeepsSelectBindingsWithHaving pins the same condition for havings: the select list is
// compiled inside the derived table, followed by the having bindings in clause order.
func TestAggregateKeepsSelectBindingsWithHaving(t *testing.T) {
	cases := []struct {
		name    string
		grammar Grammar
		want    string
	}{
		{
			"mysql", NewMySqlGrammar(""),
			"select count(*) as aggregate from (select (select `foo`, `bar` from `posts` where `title` = ?) as `post` from `users` group by `team` having `team` > ?) as `temp_table`",
		},
		{
			"postgres", NewPostgresGrammar(""),
			`select count(*) as aggregate from (select (select "foo", "bar" from "posts" where "title" = ?) as "post" from "users" group by "team" having "team" > ?) as "temp_table"`,
		},
		{
			"sqlite", NewSqliteGrammar(""),
			`select count(*) as aggregate from (select (select "foo", "bar" from "posts" where "title" = ?) as "post" from "users" group by "team" having "team" > ?) as "temp_table"`,
		},
		{
			"sqlserver", NewSqlServerGrammar(""),
			"select count(*) as aggregate from (select (select [foo], [bar] from [posts] where [title] = ?) as [post] from [users] group by [team] having [team] > ?) as [temp_table]",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := aggregateSubSelect(NewBuilder(test.grammar))
			builder.GroupBy("team").Having("team", ">", 1)

			sql, bindings := builder.ToAggregateSQL(core.Aggregate{Function: "count", Columns: []any{"*"}})

			assert.Equal(t, test.want, sql)
			assert.Equal(t, []any{"foo", 1}, bindings)
		})
	}
}

// TestAggregateDropsOrdersWithoutGroups pins setAggregate()'s other effect: when the query has no
// groups the orders, and the bindings they carry, are cleared before compiling, so an aggregate over
// an ordered query has no order by clause. With a group by the order survives.
func TestAggregateDropsOrdersWithoutGroups(t *testing.T) {
	count := core.Aggregate{Function: "count", Columns: []any{"*"}}

	t.Run("no groups", func(t *testing.T) {
		builder := aggregateSubSelect(newTestBuilder()).OrderBy("name", "asc")

		sql, bindings := builder.ToAggregateSQL(count)

		assert.Equal(t, `select count(*) as aggregate from "users"`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("no groups, raw order binding", func(t *testing.T) {
		builder := aggregateSubSelect(newTestBuilder()).OrderByRaw("id = ?", []any{2})

		sql, bindings := builder.ToAggregateSQL(count)

		// Laravel compiles no order by at all, so the raw order's binding must not survive either.
		assert.Equal(t, `select count(*) as aggregate from "users"`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("with groups", func(t *testing.T) {
		builder := aggregateSubSelect(newTestBuilder()).GroupBy("team").OrderBy("name", "asc")

		sql, bindings := builder.ToAggregateSQL(count)

		assert.Equal(t, `select count(*) as aggregate from "users" group by "team" order by "name" asc`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("the builder keeps its orders", func(t *testing.T) {
		builder := aggregateSubSelect(newTestBuilder()).OrderBy("name", "asc")

		_, _ = builder.ToAggregateSQL(count)

		// The aggregate is compiled on a copy, so a get() that follows still orders.
		assert.Equal(t, `select (select "foo", "bar" from "posts" where "title" = ?) as "post" from "users" order by "name" asc`, builder.ToSQL())
	})
}

// TestAggregateBindingsWithWhere pins that the components the aggregate does compile keep their
// bindings: only the select bindings (and the orders, without groups) are dropped.
func TestAggregateBindingsWithWhere(t *testing.T) {
	builder := aggregateSubSelect(newTestBuilder()).Where("active", 1)

	sql, bindings := builder.ToAggregateSQL(core.Aggregate{Function: "count", Columns: []any{"*"}})

	assert.Equal(t, `select count(*) as aggregate from "users" where "active" = ?`, sql)
	assert.Equal(t, []any{1}, bindings)
}
