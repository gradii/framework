package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the JSON "contains key" cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php: testWhereJsonContainsKeyMySql,
// testWhereJsonContainsKeyPostgres, testWhereJsonContainsKeySqlite and
// testWhereJsonContainsKeySqlServer.
//
// whereJsonContainsKey already pushed a JsonContainsKey descriptor that each dialect's
// compileJsonContainsKey renders (the base grammar refuses it), so what these cases add is the or-form,
// Builder::orWhereJsonContainsKey(), which is the entry point this file pins: Laravel's
// orWhereJsonContainsKey delegates to whereJsonContainsKey with the "or" boolean and adds no binding of
// its own.
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go, and the expectations are the corpus SQL
// verbatim, with our "?" placeholders and per-dialect identifier quoting.

// TestWhereJsonContainsKeyMySql ports Laravel's testWhereJsonContainsKeyMySql.
func TestWhereJsonContainsKeyMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"table qualified key", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("users.options->languages", BooleanAnd, false)
		}, "select * from `users` where ifnull(json_contains_path(`users`.`options`, 'one', '$.\"languages\"'), 0)"},
		{"nested key", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->language->primary", BooleanAnd, false)
		}, "select * from `users` where ifnull(json_contains_path(`options`, 'one', '$.\"language\".\"primary\"'), 0)"},
		{"or where key", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonContainsKey("options->languages")
		}, "select * from `users` where `id` = ? or ifnull(json_contains_path(`options`, 'one', '$.\"languages\"'), 0)"},
		{"indexed path", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->languages[0][1]", BooleanAnd, false)
		}, "select * from `users` where ifnull(json_contains_path(`options`, 'one', '$.\"languages\"[0][1]'), 0)"},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonContainsKey("options->languages").GetBindings())
}

// TestWhereJsonContainsKeyPostgres ports Laravel's testWhereJsonContainsKeyPostgres.
func TestWhereJsonContainsKeyPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"table qualified key", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("users.options->languages", BooleanAnd, false)
		}, `select * from "users" where coalesce(("users"."options")::jsonb ?? 'languages', false)`},
		{"nested key", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->language->primary", BooleanAnd, false)
		}, `select * from "users" where coalesce(("options"->'language')::jsonb ?? 'primary', false)`},
		{"or where key", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonContainsKey("options->languages")
		}, `select * from "users" where "id" = ? or coalesce(("options")::jsonb ?? 'languages', false)`},
		{"indexed path", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->languages[0][1]", BooleanAnd, false)
		}, `select * from "users" where case when jsonb_typeof(("options"->'languages'->0)::jsonb) = 'array' then jsonb_array_length(("options"->'languages'->0)::jsonb) >= 2 else false end`},
		{"negative index", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->languages[-1]", BooleanAnd, false)
		}, `select * from "users" where case when jsonb_typeof(("options"->'languages')::jsonb) = 'array' then jsonb_array_length(("options"->'languages')::jsonb) >= 1 else false end`},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonContainsKey("options->languages").GetBindings())
}

// TestWhereJsonContainsKeySqlite ports Laravel's testWhereJsonContainsKeySqlite.
func TestWhereJsonContainsKeySqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"table qualified key", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("users.options->languages", BooleanAnd, false)
		}, `select * from "users" where json_type("users"."options", '$."languages"') is not null`},
		{"nested key", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->language->primary", BooleanAnd, false)
		}, `select * from "users" where json_type("options", '$."language"."primary"') is not null`},
		{"or where key", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonContainsKey("options->languages")
		}, `select * from "users" where "id" = ? or json_type("options", '$."languages"') is not null`},
		{"indexed path", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->languages[0][1]", BooleanAnd, false)
		}, `select * from "users" where json_type("options", '$."languages"[0][1]') is not null`},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonContainsKey("options->languages").GetBindings())
}

// TestWhereJsonContainsKeySqlServer ports Laravel's testWhereJsonContainsKeySqlServer.
func TestWhereJsonContainsKeySqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"table qualified key", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("users.options->languages", BooleanAnd, false)
		}, `select * from [users] where 'languages' in (select [key] from openjson([users].[options]))`},
		{"nested key", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->language->primary", BooleanAnd, false)
		}, `select * from [users] where 'primary' in (select [key] from openjson([options], '$."language"'))`},
		{"or where key", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonContainsKey("options->languages")
		}, `select * from [users] where [id] = ? or 'languages' in (select [key] from openjson([options]))`},
		{"indexed path", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonContainsKey("options->languages[0][1]", BooleanAnd, false)
		}, `select * from [users] where 1 in (select [key] from openjson([options], '$."languages"[0]'))`},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonContainsKey("options->languages").GetBindings())
}
