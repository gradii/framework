package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the JSON "doesn't contain key" cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php: testWhereJsonDoesntContainKeyMySql,
// testWhereJsonDoesntContainKeyPostgres, testWhereJsonDoesntContainKeySqlite and
// testWhereJsonDoesntContainKeySqlServer.
//
// The grammar already renders the negation (Grammar::whereJsonContainsKey prefixes "not " when the
// descriptor's not flag is set, which each dialect's compileJsonContainsKey supplies the base for).
// What these cases pin is the Builder entry point Laravel adds on top of that:
// Builder::whereJsonDoesntContainKey($column, $boolean = 'and') delegates to whereJsonContainsKey with
// the not flag set and adds no binding of its own, and Builder::orWhereJsonDoesntContainKey($column)
// delegates to it with the "or" boolean.
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go, and the expectations are the corpus
// SQL verbatim, with our "?" placeholders and per-dialect identifier quoting.

// TestWhereJsonDoesntContainKeyMySql ports Laravel's testWhereJsonDoesntContainKeyMySql.
func TestWhereJsonDoesntContainKeyMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"key", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContainKey("options->languages", BooleanAnd)
		}, "select * from `users` where not ifnull(json_contains_path(`options`, 'one', '$.\"languages\"'), 0)"},
		{"or where key", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages")
		}, "select * from `users` where `id` = ? or not ifnull(json_contains_path(`options`, 'one', '$.\"languages\"'), 0)"},
		{"indexed path", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContainKey("options->languages[0][1]", BooleanAnd)
		}, "select * from `users` where not ifnull(json_contains_path(`options`, 'one', '$.\"languages\"[0][1]'), 0)"},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages").GetBindings())
}

// TestWhereJsonDoesntContainKeyPostgres ports Laravel's testWhereJsonDoesntContainKeyPostgres.
func TestWhereJsonDoesntContainKeyPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"key", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContainKey("options->languages", BooleanAnd)
		}, `select * from "users" where not coalesce(("options")::jsonb ?? 'languages', false)`},
		{"or where key", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages")
		}, `select * from "users" where "id" = ? or not coalesce(("options")::jsonb ?? 'languages', false)`},
		{"indexed path", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContainKey("options->languages[0][1]", BooleanAnd)
		}, `select * from "users" where not case when jsonb_typeof(("options"->'languages'->0)::jsonb) = 'array' then jsonb_array_length(("options"->'languages'->0)::jsonb) >= 2 else false end`},
		{"negative index", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContainKey("options->languages[-1]", BooleanAnd)
		}, `select * from "users" where not case when jsonb_typeof(("options"->'languages')::jsonb) = 'array' then jsonb_array_length(("options"->'languages')::jsonb) >= 1 else false end`},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages").GetBindings())
}

// TestWhereJsonDoesntContainKeySqlite ports Laravel's testWhereJsonDoesntContainKeySqlite.
func TestWhereJsonDoesntContainKeySqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"key", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContainKey("options->languages", BooleanAnd)
		}, `select * from "users" where not json_type("options", '$."languages"') is not null`},
		{"or where key", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages")
		}, `select * from "users" where "id" = ? or not json_type("options", '$."languages"') is not null`},
		{"or where indexed path", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages[0][1]")
		}, `select * from "users" where "id" = ? or not json_type("options", '$."languages"[0][1]') is not null`},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages").GetBindings())
}

// TestWhereJsonDoesntContainKeySqlServer ports Laravel's testWhereJsonDoesntContainKeySqlServer.
func TestWhereJsonDoesntContainKeySqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"key", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContainKey("options->languages", BooleanAnd)
		}, `select * from [users] where not 'languages' in (select [key] from openjson([options]))`},
		{"or where key", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages")
		}, `select * from [users] where [id] = ? or not 'languages' in (select [key] from openjson([options]))`},
		{"or where indexed path", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages[0][1]")
		}, `select * from [users] where [id] = ? or not 1 in (select [key] from openjson([options], '$."languages"[0]'))`},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages").GetBindings())
}
