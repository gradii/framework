package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the JSON "contains" cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php: testWhereJsonContainsMySql,
// testWhereJsonContainsPostgres and testWhereJsonContainsSqlServer.
//
// Builder::whereJsonContains() already pushed a JsonContains descriptor that each dialect's
// compileJsonContains renders and prepared the JSON binding (see grammar_where.go and the per-dialect
// grammars). What these cases add is the or-form, Builder::orWhereJsonContains(), which Laravel defines as
// a one-line delegation to whereJsonContains() with the "or" boolean:
//
//     public function orWhereJsonContains($column, $value)
//     {
//         return $this->whereJsonContains($column, $value, 'or');
//     }
//
// Each dialect's compileJsonContains is the same one the "where" form already exercises, so no dialect
// override is introduced here; the tests pin the entry point and its binding behaviour.
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go, and the expectations are the corpus SQL
// verbatim, with our "?" placeholders and per-dialect identifier quoting.

// TestWhereJsonContainsMySql ports Laravel's testWhereJsonContainsMySql.
func TestWhereJsonContainsMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContains("options", []any{"en"}, core.BooleanAnd, false)
		}, "select * from `users` where json_contains(`options`, ?)"},
		{"where with a path", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContains("users.options->languages", []any{"en"}, core.BooleanAnd, false)
		}, "select * from `users` where json_contains(`users`.`options`, ?, '$.\"languages\"')"},
		{"or where", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonContains("options->languages", Raw(`'["en"]'`))
		}, "select * from `users` where `id` = ? or json_contains(`options`, '[\"en\"]', '$.\"languages\"')"},
	})

	// An array value is bound as JSON, exactly as Laravel's json_encode produces it.
	assert.Equal(t, []any{"[\"en\"]"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereJsonContains("options", []any{"en"}, core.BooleanAnd, false).GetBindings())
	// A Raw value is inlined and contributes no binding of its own; only the id remains.
	assert.Equal(t, []any{1}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonContains("options->languages", Raw(`'["en"]'`)).GetBindings())
}

// TestWhereJsonContainsPostgres ports Laravel's testWhereJsonContainsPostgres.
func TestWhereJsonContainsPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContains("options", []any{"en"}, core.BooleanAnd, false)
		}, `select * from "users" where ("options")::jsonb @> ?`},
		{"where with a path", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContains("users.options->languages", []any{"en"}, core.BooleanAnd, false)
		}, `select * from "users" where ("users"."options"->'languages')::jsonb @> ?`},
		{"or where", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonContains("options->languages", Raw(`'["en"]'`))
		}, `select * from "users" where "id" = ? or ("options"->'languages')::jsonb @> '["en"]'`},
	})

	assert.Equal(t, []any{"[\"en\"]"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereJsonContains("options", []any{"en"}, core.BooleanAnd, false).GetBindings())
	assert.Equal(t, []any{1}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonContains("options->languages", Raw(`'["en"]'`)).GetBindings())
}

// TestWhereJsonContainsSqlServer ports Laravel's testWhereJsonContainsSqlServer.
func TestWhereJsonContainsSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContains("options", true, core.BooleanAnd, false)
		}, `select * from [users] where ? in (select [value] from openjson([options]))`},
		{"where with a path", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContains("users.options->languages", "en", core.BooleanAnd, false)
		}, `select * from [users] where ? in (select [value] from openjson([users].[options], '$."languages"'))`},
		{"or where", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonContains("options->languages", Raw(`'en'`))
		}, `select * from [users] where [id] = ? or 'en' in (select [value] from openjson([options], '$."languages"'))`},
	})

	// SQL Server passes booleans as the strings "true"/"false".
	assert.Equal(t, []any{"true"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereJsonContains("options", true, core.BooleanAnd, false).GetBindings())
	assert.Equal(t, []any{1}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonContains("options->languages", Raw(`'en'`)).GetBindings())
}
