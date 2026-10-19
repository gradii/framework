package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the "aggregates-gap" area of Laravel's tests/Database/QueryBuilderTest.php
// (laravel/framework 12.x, commit 0fc645c6737626a1b1636cd234192b0d78b76569): the eight PORTABLE
// aggregates rows the port plan lists for this area.
//
// Corpus methods ported here:
//   testAggregateFunctions, testAggregateResetFollowedByGet, testAggregateResetFollowedByGetWithColumns,
//   testAggregateResetFollowedBySelectGet, testAggregateWithSubSelect, testHavingAggregate,
//   testSqlServerExists, testUnionAggregate.
//
// Every one of these corpus methods drives its assertion through a mocked connection and processor, but
// what it pins is the SQL text handed to Connection::select() (plus, in testAggregateWithSubSelect, the
// compiled subselect and the binding list). The builder has no execution side, so the ports assert the
// compiled statement and the bindings; the mocked rows and the value the processor extracts are out of
// reach here and are deliberately not asserted.
//
// The corpus calls count(), sum(), max(), min(), avg() and get(); the builder compiles those through
// Builder.ToAggregateSQL, Builder.ToExistsSQL and Builder.ToSelectSQL, which is what the cases below use.
// Cases built on the corpus getBuilder() use the base grammar (newTestBuilder); dialect cases keep their
// own identifier quoting — backticks for MySQL, [ ] for SQL Server, double quotes elsewhere — and, as in
// every other port in this package, keep '?' placeholders in every dialect.

// TestAggregateFunctions ports Laravel's testAggregateFunctions: each aggregate shortcut compiles to
// "select <fn>(<column>) as aggregate", and exists/doesntExist compile to the exists form.
func TestAggregateFunctions(t *testing.T) {
	aggregates := []struct {
		name     string
		function string
		columns  []any
		want     string
	}{
		{"count", "count", []any{"*"}, `select count(*) as aggregate from "users"`},
		{"max", "max", []any{"id"}, `select max("id") as aggregate from "users"`},
		{"min", "min", []any{"id"}, `select min("id") as aggregate from "users"`},
		{"sum", "sum", []any{"id"}, `select sum("id") as aggregate from "users"`},
		{"avg", "avg", []any{"id"}, `select avg("id") as aggregate from "users"`},
		// average() is Laravel's alias for avg(); the corpus pins the same statement for both.
		{"average", "avg", []any{"id"}, `select avg("id") as aggregate from "users"`},
	}

	for _, aggregate := range aggregates {
		t.Run(aggregate.name, func(t *testing.T) {
			sql, bindings := newTestBuilder().From("users", "").
				ToAggregateSQL(Aggregate{Function: aggregate.function, Columns: aggregate.columns})

			assert.Equal(t, aggregate.want, sql)
			assert.Empty(t, bindings)
		})
	}

	t.Run("exists", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").ToExistsSQL()

		assert.Equal(t, `select exists(select * from "users") as "exists"`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("doesnt exist", func(t *testing.T) {
		// Laravel's doesntExist() runs the same exists statement and negates the flag: the corpus pins
		// the identical SQL for both calls and only varies the mocked row ([['exists' => 0]]), so the
		// compiled statement is the same here too.
		sql, bindings := newTestBuilder().From("users", "").ToExistsSQL()

		assert.Equal(t, `select exists(select * from "users") as "exists"`, sql)
		assert.Empty(t, bindings)
	})
}

// TestSqlServerExistsBare ports Laravel's testSqlServerExists, whose builder has no where clause. The
// name carries "Bare" because TestSqlServerExists (laravel_port_test.go) already pins the shape SQL
// Server gives an exists query with a where clause.
func TestSqlServerExistsBare(t *testing.T) {
	sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users", "").ToExistsSQL()

	assert.Equal(t, "select top 1 1 [exists] from [users]", sql)
	assert.Empty(t, bindings)
}

// TestAggregateResetFollowedByGet ports Laravel's testAggregateResetFollowedByGet: an aggregate is
// applied to the builder and then get() compiles the builder's own select list, untouched.
func TestAggregateResetFollowedByGet(t *testing.T) {
	builder := newTestBuilder().From("users", "").Select("column1", "column2")

	count, _ := builder.ToAggregateSQL(Aggregate{Function: "count", Columns: []any{"*"}})
	assert.Equal(t, `select count(*) as aggregate from "users"`, count)

	sum, _ := builder.ToAggregateSQL(Aggregate{Function: "sum", Columns: []any{"id"}})
	assert.Equal(t, `select sum("id") as aggregate from "users"`, sum)

	// ToAggregateSQL compiles a copy, so the aggregate never reaches this builder and the columns
	// selected before count() are still the ones get() selects.
	selectSQL, _ := builder.ToSelectSQL()
	assert.Equal(t, `select "column1", "column2" from "users"`, selectSQL)
}

// TestAggregateResetFollowedBySelectGet ports Laravel's testAggregateResetFollowedBySelectGet: count()
// takes its column without setting the select list, and the select that follows is what get() compiles.
func TestAggregateResetFollowedBySelectGet(t *testing.T) {
	builder := newTestBuilder().From("users", "")

	count, _ := builder.ToAggregateSQL(Aggregate{Function: "count", Columns: []any{"column1"}})
	assert.Equal(t, `select count("column1") as aggregate from "users"`, count)

	builder.Select("column2", "column3")

	selectSQL, _ := builder.ToSelectSQL()
	assert.Equal(t, `select "column2", "column3" from "users"`, selectSQL)
}

// TestAggregateResetFollowedByGetWithColumns ports Laravel's testAggregateResetFollowedByGetWithColumns:
// the columns are passed to get() itself, which sets them before compiling.
func TestAggregateResetFollowedByGetWithColumns(t *testing.T) {
	builder := newTestBuilder().From("users", "")

	count, _ := builder.ToAggregateSQL(Aggregate{Function: "count", Columns: []any{"column1"}})
	assert.Equal(t, `select count("column1") as aggregate from "users"`, count)

	builder.Select("column2", "column3")

	selectSQL, _ := builder.ToSelectSQL()
	assert.Equal(t, `select "column2", "column3" from "users"`, selectSQL)
}

// TestAggregateWithSubSelect ports Laravel's testAggregateWithSubSelect: a selectSub adds the compiled
// subselect and its binding to the builder, and count() still compiles to the plain aggregate.
func TestAggregateWithSubSelect(t *testing.T) {
	builder := newTestBuilder().From("users", "").SelectSub(func(query *Builder) *Builder {
		return query.From("posts", "").Select("foo", "bar").Where("title", "foo")
	}, "post")

	sql, _ := builder.ToAggregateSQL(Aggregate{Function: "count", Columns: []any{"*"}})
	assert.Equal(t, `select count(*) as aggregate from "users"`, sql)

	// The corpus asserts the compiled subselect through the grammar rather than the builder, and the
	// bindings the subselect contributed. It does not assert the bindings of the count() call itself,
	// so this port does not either.
	selects := builder.Selects()
	assert.Len(t, selects, 1)

	column, ok := selects[0].(Expression)
	assert.True(t, ok)
	assert.Equal(t, `(select "foo", "bar" from "posts" where "title" = ?) as "post"`, builder.Grammar().GetValue(column))

	assert.Equal(t, []any{"foo"}, builder.GetBindings())
}

// TestHavingAggregate ports Laravel's testHavingAggregate: an aggregate over a query with a having
// clause wraps the query into a derived table, the same shape a union aggregate takes.
func TestHavingAggregate(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).From("posts", "")
	builder.SelectSub(func(query *Builder) *Builder {
		return query.From("videos", "").Select("count(*)").
			WhereColumn("posts.id", "=", "videos.post_id", BooleanAnd)
	}, "videos_count")
	builder.Having("videos_count", ">", 1)

	sql, bindings := builder.ToAggregateSQL(Aggregate{Function: "count", Columns: []any{"*"}})

	assert.Equal(t, "select count(*) as aggregate from (select (select `count(*)` from `videos` where `posts`.`id` = `videos`.`post_id`) as `videos_count` from `posts` having `videos_count` > ?) as `temp_table`", sql)
	assert.Equal(t, []any{1}, bindings)
}

// TestUnionAggregateAggregatesGap ports Laravel's testUnionAggregate: an aggregate over a union wraps
// the union into a derived table named temp_table, which each dialect spells its own way. The name
// carries the area suffix because TestUnionAggregate (laravel_port_test.go) already exists and asserts a
// different statement.
func TestUnionAggregateAggregatesGap(t *testing.T) {
	count := Aggregate{Function: "count", Columns: []any{"*"}}

	t.Run("mysql", func(t *testing.T) {
		builder := NewBuilder(NewMySqlGrammar("")).From("posts", "")
		builder.Union(NewBuilder(NewMySqlGrammar("")).From("videos", ""), false)

		sql, bindings := builder.ToAggregateSQL(count)

		assert.Equal(t, "select count(*) as aggregate from ((select * from `posts`) union (select * from `videos`)) as `temp_table`", sql)
		assert.Empty(t, bindings)
	})

	t.Run("mysql with selected columns", func(t *testing.T) {
		builder := NewBuilder(NewMySqlGrammar("")).From("posts", "").Select("id")
		builder.Union(NewBuilder(NewMySqlGrammar("")).From("videos", "").Select("id"), false)

		sql, bindings := builder.ToAggregateSQL(count)

		assert.Equal(t, "select count(*) as aggregate from ((select `id` from `posts`) union (select `id` from `videos`)) as `temp_table`", sql)
		assert.Empty(t, bindings)
	})

	t.Run("postgres", func(t *testing.T) {
		builder := NewBuilder(NewPostgresGrammar("")).From("posts", "")
		builder.Union(NewBuilder(NewPostgresGrammar("")).From("videos", ""), false)

		sql, bindings := builder.ToAggregateSQL(count)

		assert.Equal(t, `select count(*) as aggregate from ((select * from "posts") union (select * from "videos")) as "temp_table"`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("sqlite", func(t *testing.T) {
		builder := NewBuilder(NewSqliteGrammar("")).From("posts", "")
		builder.Union(NewBuilder(NewSqliteGrammar("")).From("videos", ""), false)

		sql, bindings := builder.ToAggregateSQL(count)

		assert.Equal(t, `select count(*) as aggregate from (select * from (select * from "posts") union select * from (select * from "videos")) as "temp_table"`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("sqlserver", func(t *testing.T) {
		builder := NewBuilder(NewSqlServerGrammar("")).From("posts", "")
		builder.Union(NewBuilder(NewSqlServerGrammar("")).From("videos", ""), false)

		sql, bindings := builder.ToAggregateSQL(count)

		assert.Equal(t, "select count(*) as aggregate from (select * from (select * from [posts]) as [temp_table] union select * from (select * from [videos]) as [temp_table]) as [temp_table]", sql)
		assert.Empty(t, bindings)
	})
}
