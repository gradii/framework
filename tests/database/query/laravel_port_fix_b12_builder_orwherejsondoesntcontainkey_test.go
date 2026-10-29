package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
)

// This file pins Builder::orWhereJsonDoesntContainKey(), the "or" entry point Laravel layers on top of
// whereJsonDoesntContainKey() (Illuminate/Database/Query/Builder.php:2183). Laravel defines it as
//
//	orWhereJsonDoesntContainKey($column) { return $this->whereJsonDoesntContainKey($column, 'or'); }
//
// which routes through whereJsonContainsKey($column, 'or', true): an "or" boolean and the negating `not`
// flag, no binding of its own. Our Builder mirrors that (where.go): OrWhereJsonDoesntContainKey delegates
// to WhereJsonDoesntContainKey(column, BooleanOr), i.e. WhereJsonContainsKey(column, BooleanOr, true).
//
// The corpus carries these assertions inside testWhereJsonDoesntContainKeyMySql/Postgres/Sqlite/SqlServer
// (the full method is ported in laravel_port_fix_b11_*; the "or" assertions are reproduced here under
// entry-point-specific test names so this item's own surface is pinned). The expectations are the corpus
// SQL verbatim, with our "?" placeholders and per-dialect identifier quoting; dialectCase/runDialectCases
// come from laravel_port_test.go.

// TestOrWhereJsonDoesntContainKeyMySql pins the MySQL "or" form of whereJsonDoesntContainKey.
func TestOrWhereJsonDoesntContainKeyMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"or where key", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages")
		}, "select * from `users` where `id` = ? or not ifnull(json_contains_path(`options`, 'one', '$.\"languages\"'), 0)"},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages").GetBindings())
}

// TestOrWhereJsonDoesntContainKeyPostgres pins the Postgres "or" form of whereJsonDoesntContainKey.
func TestOrWhereJsonDoesntContainKeyPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"or where key", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages")
		}, `select * from "users" where "id" = ? or not coalesce(("options")::jsonb ?? 'languages', false)`},
	})

	assert.Equal(t, []any{1}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		Where("id", "=", 1).OrWhereJsonDoesntContainKey("options->languages").GetBindings())
}

// TestOrWhereJsonDoesntContainKeySqlite pins the SQLite "or" form of whereJsonDoesntContainKey, both for a
// plain key and for an indexed path (the corpus asserts both).
func TestOrWhereJsonDoesntContainKeySqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
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

// TestOrWhereJsonDoesntContainKeySqlServer pins the SqlServer "or" form of whereJsonDoesntContainKey, both
// for a plain key and for an indexed path (the corpus asserts both).
func TestOrWhereJsonDoesntContainKeySqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
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
