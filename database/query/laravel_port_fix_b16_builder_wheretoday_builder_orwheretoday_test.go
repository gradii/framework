package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/support/carbon"
)

// This file ports the "where today" cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// testWhereTodayMySQL, testPassingArrayToWhereTodayMySQL, testWhereTodaySqlServer and
// testPassingArrayToWhereTodaySqlServer.
//
// whereToday / orWhereToday own no grammar: Laravel's Builder::whereToday() delegates to
// whereTodayBeforeOrAfter(), which appends one 'Date' descriptor per column (carrying today's date in
// "Y-m-d" form) through addDateBasedWhere(), and each dialect's whereDate() then compiles it. The
// corpus therefore renders exactly like a whereDate() against a fixed value, and each case below
// rebuilds the corpus chain through our Builder and asserts the exact SQL the corpus asserts, over the
// identifier quoting of the dialect the corpus names, with the binding list asserted inline.
//
// Laravel freezes the clock with Carbon::setTestNow('2022-04-20 12:34:56.123456'), which is what makes
// "today" the 20th of April 2022 in the assertions; the port freezes goravel's clock the same way
// through carbon.SetTestNow and restores it afterwards.
//
// It reuses laravelCase/runLaravelCases and dialectCase/runDialectCases from laravel_port_test.go.

// freezeToday pins the clock the way the corpus's Carbon::setTestNow does, so whereToday() resolves to
// '2022-04-20' for the duration of the test.
func freezeToday(t *testing.T) {
	t.Helper()

	carbon.SetTestNow(carbon.Parse("2022-04-20 12:34:56.123456"))
	t.Cleanup(carbon.ClearTestNow)
}

// TestWhereTodayMySQL ports Laravel's testWhereTodayMySQL.
func TestWhereTodayMySQL(t *testing.T) {
	freezeToday(t)

	runDialectCases(t, []dialectCase{
		{"whereToday", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereToday("published_at", BooleanAnd)
		}, "select * from `posts` where date(`published_at`) = ?"},
		{"where id = 1 orWhereToday", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWhereToday("published_at")
		}, "select * from `posts` where `id` = ? or date(`published_at`) = ?"},
	})

	assert.Equal(t, []any{"2022-04-20"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("posts", "").
		WhereToday("published_at", BooleanAnd).GetBindings())
	assert.Equal(t, []any{1, "2022-04-20"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("posts", "").
		Where("id", "=", 1).OrWhereToday("published_at").GetBindings())
}

// TestPassingArrayToWhereTodayMySQL ports Laravel's testPassingArrayToWhereTodayMySQL: one clause is
// added per entry of the column array, all joined by the same boolean.
func TestPassingArrayToWhereTodayMySQL(t *testing.T) {
	freezeToday(t)

	runDialectCases(t, []dialectCase{
		{"whereToday array", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereToday([]any{"published_at", "held_at"}, BooleanAnd)
		}, "select * from `posts` where date(`published_at`) = ? and date(`held_at`) = ?"},
		{"orWhereToday array", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWhereToday([]any{"published_at", "held_at"})
		}, "select * from `posts` where `id` = ? or date(`published_at`) = ? or date(`held_at`) = ?"},
	})

	assert.Equal(t, []any{"2022-04-20", "2022-04-20"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("posts", "").
		WhereToday([]any{"published_at", "held_at"}, BooleanAnd).GetBindings())
	assert.Equal(t, []any{1, "2022-04-20", "2022-04-20"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("posts", "").
		Where("id", "=", 1).OrWhereToday([]any{"published_at", "held_at"}).GetBindings())
}

// TestWhereTodaySqlServer ports Laravel's testWhereTodaySqlServer: SqlServerGrammar overrides whereDate()
// to render cast(... as date), which is what a whereToday() clause compiles through.
func TestWhereTodaySqlServer(t *testing.T) {
	freezeToday(t)

	runDialectCases(t, []dialectCase{
		{"whereToday", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereToday("published_at", BooleanAnd)
		}, "select * from [posts] where cast([published_at] as date) = ?"},
		{"where id = 1 orWhereToday", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWhereToday("published_at")
		}, "select * from [posts] where [id] = ? or cast([published_at] as date) = ?"},
	})

	assert.Equal(t, []any{"2022-04-20"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("posts", "").
		WhereToday("published_at", BooleanAnd).GetBindings())
	assert.Equal(t, []any{1, "2022-04-20"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("posts", "").
		Where("id", "=", 1).OrWhereToday("published_at").GetBindings())
}

// TestPassingArrayToWhereTodaySqlServer ports Laravel's testPassingArrayToWhereTodaySqlServer.
func TestPassingArrayToWhereTodaySqlServer(t *testing.T) {
	freezeToday(t)

	runDialectCases(t, []dialectCase{
		{"whereToday array", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereToday([]any{"published_at", "held_at"}, BooleanAnd)
		}, "select * from [posts] where cast([published_at] as date) = ? and cast([held_at] as date) = ?"},
		{"orWhereToday array", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWhereToday([]any{"published_at", "held_at"})
		}, "select * from [posts] where [id] = ? or cast([published_at] as date) = ? or cast([held_at] as date) = ?"},
	})

	assert.Equal(t, []any{"2022-04-20", "2022-04-20"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("posts", "").
		WhereToday([]any{"published_at", "held_at"}, BooleanAnd).GetBindings())
	assert.Equal(t, []any{1, "2022-04-20", "2022-04-20"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("posts", "").
		Where("id", "=", 1).OrWhereToday([]any{"published_at", "held_at"}).GetBindings())
}

// TestWhereTodayBaseGrammar is our own companion to the corpus cases: whereToday owns no grammar, so the
// base grammar (what the corpus getBuilder() uses) renders it through the same date() wrapper as
// whereDate(). It is asserted over runLaravelCases to cover the plain, non-dialect path.
func TestWhereTodayBaseGrammar(t *testing.T) {
	freezeToday(t)

	runLaravelCases(t, []laravelCase{
		{"whereToday", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereToday("published_at", BooleanAnd)
		}, `select * from "posts" where date("published_at") = ?`},
		{"orWhereToday", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWhereToday("published_at")
		}, `select * from "posts" where "id" = ? or date("published_at") = ?`},
		{"whereToday array", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereToday([]string{"published_at", "held_at"}, BooleanAnd)
		}, `select * from "posts" where date("published_at") = ? and date("held_at") = ?`},
	})

	assert.Equal(t, []any{"2022-04-20"}, newTestBuilder().Select("*").From("posts", "").
		WhereToday("published_at", BooleanAnd).GetBindings())
}
