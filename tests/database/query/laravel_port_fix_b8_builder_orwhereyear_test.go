package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
)

// This file ports the "or where year" cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// the orWhereYear slice of testDateBasedOrWheresAcceptsTwoArguments (the orWhereDate / orWhereDay /
// orWhereMonth slices belong to sibling Builder entry points), plus testOrWhereYearMySql,
// testOrWhereYearPostgres and testOrWhereYearSqlServer.
//
// orWhereYear has no grammar of its own: Laravel's Builder::orWhereYear() delegates to whereYear() with
// the "or" boolean, and each dialect's whereYear() override (PostgresGrammar and SQLiteGrammar here)
// then compiles it. The corpus therefore only differs from whereYear in the joining boolean, and every
// case rebuilds the chain through our Builder and asserts the exact SQL the corpus asserts, over the
// dialect and identifier quoting the corpus uses.
//
// It reuses laravelCase/runLaravelCases and dialectCase/runDialectCases from laravel_port_test.go.

// TestOrWhereYearMySql ports Laravel's testOrWhereYearMySql.
func TestOrWhereYearMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", "=", 2014).OrWhereYear("created_at", "=", 2015)
		}, "select * from `users` where year(`created_at`) = ? or year(`created_at`) = ?"},
	})

	assert.Equal(t, []any{2014, 2015}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereYear("created_at", "=", 2014).OrWhereYear("created_at", "=", 2015).GetBindings())
}

// TestOrWhereYearPostgres ports Laravel's testOrWhereYearPostgres.
func TestOrWhereYearPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", "=", 2014).OrWhereYear("created_at", "=", 2015)
		}, `select * from "users" where extract(year from "created_at") = ? or extract(year from "created_at") = ?`},
	})

	assert.Equal(t, []any{2014, 2015}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereYear("created_at", "=", 2014).OrWhereYear("created_at", "=", 2015).GetBindings())
}

// TestOrWhereYearSqlServer ports Laravel's testOrWhereYearSqlServer.
func TestOrWhereYearSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", "=", 2014).OrWhereYear("created_at", "=", 2015)
		}, "select * from [users] where year([created_at]) = ? or year([created_at]) = ?"},
	})

	assert.Equal(t, []any{2014, 2015}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereYear("created_at", "=", 2014).OrWhereYear("created_at", "=", 2015).GetBindings())
}

// TestOrWhereYearAcceptsTwoArguments ports the orWhereYear slice of Laravel's
// testDateBasedOrWheresAcceptsTwoArguments: the two-argument form leaves the operator to default ("="),
// which PrepareValueAndOperator supplies, exactly as the and-form whereYear does.
func TestOrWhereYearAcceptsTwoArguments(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"two arguments", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 1).OrWhereYear("created_at", 1)
		}, "select * from `users` where `id` = ? or year(`created_at`) = ?"},
	})

	assert.Equal(t, []any{1, 1}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", 1).OrWhereYear("created_at", 1).GetBindings())
}
