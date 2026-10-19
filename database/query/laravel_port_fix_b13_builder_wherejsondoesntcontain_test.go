package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the JSON "doesn't contain" cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php: testWhereJsonDoesntContainMySql,
// testWhereJsonDoesntContainPostgres, testWhereJsonDoesntContainSqlite and
// testWhereJsonDoesntContainSqlServer.
//
// The grammar already renders the negation (Grammar::whereJsonContains prefixes "not " when the
// descriptor's not flag is set, and each dialect's compileJsonContains supplies the base for it). What
// these cases pin is the pair of Builder entry points Laravel layers on top of that:
// Builder::whereJsonDoesntContain($column, $value, $boolean = 'and') delegates to whereJsonContains
// with the not flag set, letting it prepare the JSON binding, and
// Builder::orWhereJsonDoesntContain($column, $value) delegates to it with the "or" boolean.
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go, and the expectations are the corpus
// SQL verbatim, with our "?" placeholders and per-dialect identifier quoting.

// TestWhereJsonDoesntContainMySql ports Laravel's testWhereJsonDoesntContainMySql.
func TestWhereJsonDoesntContainMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContain("options->languages", []any{"en"}, BooleanAnd)
		}, "select * from `users` where not json_contains(`options`, ?, '$.\"languages\"')"},
		{"or where", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContain("options->languages", Raw(`'["en"]'`))
		}, "select * from `users` where `id` = ? or not json_contains(`options`, '[\"en\"]', '$.\"languages\"')"},
	})

	// An array value is bound as JSON, exactly as Laravel's json_encode produces it.
	assert.Equal(t, []any{"[\"en\"]"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereJsonDoesntContain("options->languages", []any{"en"}, BooleanAnd).GetBindings())
	// A Raw value is inlined and contributes no binding of its own.
	assert.Equal(t, []any{1}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContain("options->languages", Raw(`'["en"]'`)).GetBindings())
}

// TestWhereJsonDoesntContainPostgres ports Laravel's testWhereJsonDoesntContainPostgres.
func TestWhereJsonDoesntContainPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContain("options->languages", []any{"en"}, BooleanAnd)
		}, `select * from "users" where not ("options"->'languages')::jsonb @> ?`},
		{"or where", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContain("options->languages", Raw(`'["en"]'`))
		}, `select * from "users" where "id" = ? or not ("options"->'languages')::jsonb @> '["en"]'`},
	})

	assert.Equal(t, []any{"[\"en\"]"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereJsonDoesntContain("options->languages", []any{"en"}, BooleanAnd).GetBindings())
	assert.Equal(t, []any{1}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContain("options->languages", Raw(`'["en"]'`)).GetBindings())
}

// TestWhereJsonDoesntContainSqlite ports Laravel's testWhereJsonDoesntContainSqlite, which carries no
// "or" form; SQLite passes the scalar through unencoded.
func TestWhereJsonDoesntContainSqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContain("options", "en", BooleanAnd)
		}, `select * from "users" where not exists (select 1 from json_each("options") where "json_each"."value" is ?)`},
		{"where with a path", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContain("users.options->language", "en", BooleanAnd)
		}, `select * from "users" where not exists (select 1 from json_each("users"."options", '$."language"') where "json_each"."value" is ?)`},
	})

	assert.Equal(t, []any{"en"}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereJsonDoesntContain("options", "en", BooleanAnd).GetBindings())
}

// TestWhereJsonDoesntContainSqlServer ports Laravel's testWhereJsonDoesntContainSqlServer.
func TestWhereJsonDoesntContainSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereJsonDoesntContain("options->languages", "en", BooleanAnd)
		}, `select * from [users] where not ? in (select [value] from openjson([options], '$."languages"'))`},
		{"or where", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContain("options->languages", Raw(`'en'`))
		}, `select * from [users] where [id] = ? or not 'en' in (select [value] from openjson([options], '$."languages"'))`},
	})

	assert.Equal(t, []any{"en"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereJsonDoesntContain("options->languages", "en", BooleanAnd).GetBindings())
	assert.Equal(t, []any{1}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContain("options->languages", Raw(`'en'`)).GetBindings())
}
