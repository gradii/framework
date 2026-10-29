package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
)

// This file ports the "writes-gap" group of Laravel's tests/Database/DatabaseQueryBuilderTest.php: the
// write statements the earlier port left out. Laravel asserts the SQL a grammar compiles for a builder,
// so these do too, over the generic grammar (double quotes, "?" bindings) unless a dialect is named.
// The corpus spells its dialect cases through getBuilder()/getSqliteBuilder()/getMySqlBuilder()/
// getSqlServerBuilder(); the base grammar is newTestBuilder() and each dialect case names its grammar.

// TestDeleteMethod ports Laravel's testDeleteMethod. Laravel executes delete(), so the port compiles the
// same builder state through ToDeleteSQL. Corpus delete($id) adds a where on the table-qualified primary
// key ($this->from.'.id'), which Where("users.id", ...) reproduces; its select binding is dropped, as
// prepareBindingsForDelete skips the "select" group.
func TestDeleteMethod(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("email", "=", "foo").ToDeleteSQL()

		assert.Equal(t, `delete from "users" where "email" = ?`, sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})

	t.Run("base with an id", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("users.id", "=", 1).ToDeleteSQL()

		assert.Equal(t, `delete from "users" where "users"."id" = ?`, sql)
		assert.Equal(t, []any{1}, bindings)
	})

	t.Run("base with an id after a raw select", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			SelectRaw("?", []any{"ignore"}).
			Where("users.id", "=", 1).
			ToDeleteSQL()

		assert.Equal(t, `delete from "users" where "users"."id" = ?`, sql)
		assert.Equal(t, []any{1}, bindings)
	})

	t.Run("sqlite rewrites a limited delete through rowid", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").
			Where("email", "=", "foo").
			OrderBy("id", "asc").
			Limit(1).
			ToDeleteSQL()

		assert.Equal(t, `delete from "users" where "rowid" in (select "users"."rowid" from "users" where "email" = ? order by "id" asc limit 1)`, sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})

	t.Run("mysql keeps order by and limit on the delete", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").
			Where("email", "=", "foo").
			OrderBy("id", "asc").
			Limit(1).
			ToDeleteSQL()

		assert.Equal(t, "delete from `users` where `email` = ? order by `id` asc limit 1", sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})

	t.Run("sqlserver", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users", "").
			Where("email", "=", "foo").
			ToDeleteSQL()

		assert.Equal(t, "delete from [users] where [email] = ?", sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})

	t.Run("sqlserver turns a limit into top", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users", "").
			Where("email", "=", "foo").
			OrderBy("id", "asc").
			Limit(1).
			ToDeleteSQL()

		assert.Equal(t, "delete top (1) from [users] where [email] = ?", sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})
}

// TestMySqlUpdateWrappingJson ports Laravel's testMySqlUpdateWrappingJson: a dotted update key becomes a
// json_set() on the MySQL grammar, and the set bindings precede the where binding.
func TestMySqlUpdateWrappingJson(t *testing.T) {
	sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("active", "=", 1).
		ToUpdateSQL(map[string]any{"name->first_name": "John", "name->last_name": "Doe"})

	assert.Equal(t, "update `users` set `name` = json_set(`name`, '$.\"first_name\"', ?), `name` = json_set(`name`, '$.\"last_name\"', ?) where `active` = ?", sql)
	assert.Equal(t, []any{"John", "Doe", 1}, bindings)
}

// TestMySqlUpdateWrappingNestedJson ports Laravel's testMySqlUpdateWrappingNestedJson: every path
// segment after the column joins the JSON path, so the column is reached only once.
func TestMySqlUpdateWrappingNestedJson(t *testing.T) {
	sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("active", "=", 1).
		ToUpdateSQL(map[string]any{"meta->name->first_name": "John", "meta->name->last_name": "Doe"})

	assert.Equal(t, "update `users` set `meta` = json_set(`meta`, '$.\"name\".\"first_name\"', ?), `meta` = json_set(`meta`, '$.\"name\".\"last_name\"', ?) where `active` = ?", sql)
	assert.Equal(t, []any{"John", "Doe", 1}, bindings)
}

// TestTruncateMethodWithPrefix ports Laravel's testTruncateMethodWithPrefix: the prefix reaches both the
// truncate table name and, on SQLite, the sqlite_sequence row the corpus maps to "prefix_users".
func TestTruncateMethodWithPrefix(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		statements := NewBuilder(NewBaseGrammar("prefix_")).From("users", "").ToTruncateSQL()

		assert.Len(t, statements, 1)
		assert.Equal(t, `truncate table "prefix_users"`, statements[0].SQL)
		assert.Empty(t, statements[0].Bindings)
	})

	t.Run("sqlite", func(t *testing.T) {
		statements := NewBuilder(NewSqliteGrammar("prefix_")).From("users", "").ToTruncateSQL()

		assert.Len(t, statements, 2)
		assert.Equal(t, "delete from sqlite_sequence where name = ?", statements[0].SQL)
		assert.Equal(t, []any{"prefix_users"}, statements[0].Bindings)
		assert.Equal(t, `delete from "prefix_users"`, statements[1].SQL)
		assert.Empty(t, statements[1].Bindings)
	})
}
