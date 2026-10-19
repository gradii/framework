package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the "or where month" cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// the orWhereMonth slice of testDateBasedOrWheresAcceptsTwoArguments (the orWhereDate / orWhereDay /
// orWhereYear slices belong to sibling Builder entry points), plus testOrWhereMonthMySql,
// testOrWhereMonthPostgres and testOrWhereMonthSqlServer.
//
// orWhereMonth has no grammar of its own: Laravel's Builder::orWhereMonth() delegates to whereMonth() with
// the "or" boolean, and each dialect's whereMonth() override (PostgresGrammar here, SqlServer in the base
// grammar) then compiles it. The corpus therefore only differs from whereMonth in the joining boolean, and
// every case rebuilds the chain through our Builder and asserts the exact SQL the corpus asserts, over
// the dialect and identifier quoting the corpus uses.
//
// It reuses laravelCase/runLaravelCases and dialectCase/runDialectCases from laravel_port_test.go.

// TestOrWhereMonthMySql ports Laravel's testOrWhereMonthMySql.
func TestOrWhereMonthMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5).OrWhereMonth("created_at", "=", 6)
		}, "select * from `users` where month(`created_at`) = ? or month(`created_at`) = ?"},
	})

	assert.Equal(t, []any{"05", "06"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5).OrWhereMonth("created_at", "=", 6).GetBindings())
}

// TestOrWhereMonthPostgres ports Laravel's testOrWhereMonthPostgres.
func TestOrWhereMonthPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5).OrWhereMonth("created_at", "=", 6)
		}, `select * from "users" where extract(month from "created_at") = ? or extract(month from "created_at") = ?`},
	})

	assert.Equal(t, []any{"05", "06"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5).OrWhereMonth("created_at", "=", 6).GetBindings())
}

// TestOrWhereMonthSqlServer ports Laravel's testOrWhereMonthSqlServer.
func TestOrWhereMonthSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5).OrWhereMonth("created_at", "=", 6)
		}, "select * from [users] where month([created_at]) = ? or month([created_at]) = ?"},
	})

	assert.Equal(t, []any{"05", "06"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5).OrWhereMonth("created_at", "=", 6).GetBindings())
}

// TestOrWhereMonthAcceptsTwoArguments ports the orWhereMonth slice of Laravel's
// testDateBasedOrWheresAcceptsTwoArguments: the two-argument form leaves the operator to default ("="),
// which PrepareValueAndOperator supplies, exactly as the and-form whereMonth does.
func TestOrWhereMonthAcceptsTwoArguments(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"two arguments", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 1).OrWhereMonth("created_at", 1)
		}, "select * from `users` where `id` = ? or month(`created_at`) = ?"},
	})

	assert.Equal(t, []any{1, "01"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", 1).OrWhereMonth("created_at", 1).GetBindings())
}
