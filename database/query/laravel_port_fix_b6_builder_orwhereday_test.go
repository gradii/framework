package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the "or where day" cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// the orWhereDay slice of testDateBasedOrWheresAcceptsTwoArguments (the orWhereDate / orWhereMonth /
// orWhereYear slices belong to sibling Builder entry points), plus testOrWhereDayMySql,
// testOrWhereDayPostgres and testOrWhereDaySqlServer.
//
// orWhereDay has no grammar of its own: Laravel's Builder::orWhereDay() delegates to whereDay() with the
// "or" boolean, and each dialect's whereDay() override (PostgresGrammar here, SqlServer in the base
// grammar) then compiles it. The corpus therefore only differs from whereDay in the joining boolean, and
// every case rebuilds the chain through our Builder and asserts the exact SQL the corpus asserts, over
// the dialect and identifier quoting the corpus uses.
//
// It reuses laravelCase/runLaravelCases and dialectCase/runDialectCases from laravel_port_test.go.

// TestOrWhereDayMySql ports Laravel's testOrWhereDayMySql.
func TestOrWhereDayMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1).OrWhereDay("created_at", "=", 2)
		}, "select * from `users` where day(`created_at`) = ? or day(`created_at`) = ?"},
	})

	assert.Equal(t, []any{"01", "02"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1).OrWhereDay("created_at", "=", 2).GetBindings())
}

// TestOrWhereDayPostgres ports Laravel's testOrWhereDayPostgres.
func TestOrWhereDayPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1).OrWhereDay("created_at", "=", 2)
		}, `select * from "users" where extract(day from "created_at") = ? or extract(day from "created_at") = ?`},
	})

	assert.Equal(t, []any{"01", "02"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1).OrWhereDay("created_at", "=", 2).GetBindings())
}

// TestOrWhereDaySqlServer ports Laravel's testOrWhereDaySqlServer.
func TestOrWhereDaySqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1).OrWhereDay("created_at", "=", 2)
		}, "select * from [users] where day([created_at]) = ? or day([created_at]) = ?"},
	})

	assert.Equal(t, []any{"01", "02"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1).OrWhereDay("created_at", "=", 2).GetBindings())
}

// TestOrWhereDayAcceptsTwoArguments ports the orWhereDay slice of Laravel's
// testDateBasedOrWheresAcceptsTwoArguments: the two-argument form leaves the operator to default ("="),
// which PrepareValueAndOperator supplies, exactly as the and-form whereDay does.
func TestOrWhereDayAcceptsTwoArguments(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"two arguments", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 1).OrWhereDay("created_at", 1)
		}, "select * from `users` where `id` = ? or day(`created_at`) = ?"},
	})

	assert.Equal(t, []any{1, "01"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", 1).OrWhereDay("created_at", 1).GetBindings())
}
