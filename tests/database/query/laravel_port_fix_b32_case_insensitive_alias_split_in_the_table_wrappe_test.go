package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
)

// This file pins the case-insensitive "as" connector Laravel's table wrapper uses.
//
// Laravel's Grammar::wrapTable() (src/Illuminate/Database/Grammar.php:49-59) detects an aliased table
// with stripos($table, ' as '), and wrapAliasedTable() (:127) splits it with preg_split('/\s+as\s+/i',
// $value) — so "translations AS t" aliases exactly like "translations as t". Our
// database/schema/wrap.go Wrap.Table() looked for the literal lowercase " as ", so an upper-case
// connector fell through to the plain-table branch and rendered as one quoted identifier.
//
// The delete path needs no change: Grammar::compileDeleteWithJoins() (Query/Grammars/Grammar.php:1410-
// 1412) takes the already-wrapped table and does last(explode(' as ', $table)), which works because the
// wrapper normalises the connector to lowercase " as ". Our BaseGrammar.CompileDeleteWithJoins mirrors
// that split, so once Wrap.Table() recognises the connector the delete alias follows.
//
// The dialectCase/runDialectCases and laravelCase/runLaravelCases helpers compile a builder through
// ToSQL(), which maps to CompileSelect; the delete rows here are reached through ToDeleteSQL(), so
// they assert directly, as the sibling b31 rows do.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569, methods
// testJoinAliasesWithPrefix (DatabaseQueryBuilderTest.php:161-167) and testDeleteWithJoinMethod
// (:4425-4497). The expected SQL and bindings were confirmed against .claude/laravel-oracle/oracle.php
// (illuminate/database v12.55.1).

// TestJoinAliasesWithPrefix ports Laravel's testJoinAliasesWithPrefix: a prefixed connection aliases
// the join table with an upper-case "AS", and the prefix lands on both the table and its alias. The
// prefixed builder is built directly rather than through runLaravelCases, which always uses the
// un-prefixed newTestBuilder().
func TestJoinAliasesWithPrefix(t *testing.T) {
	sql := NewBuilder(NewBaseGrammar("prefix_")).From("services", "").Select("*").
		Join("translations AS t", "t.item_id", "=", "services.id", "inner", false).
		ToSQL()

	assert.Equal(t, `select * from "prefix_services" inner join "prefix_translations" as "prefix_t" on "prefix_t"."item_id" = "prefix_services"."id"`, sql)
}

// TestDeleteWithJoinMethod ports Laravel's testDeleteWithJoinMethod. The dialect delete paths ignore
// each other's quirks here: SQLite rewrites joins (and a limit) into a rowid subquery, while MySQL,
// SQL Server and the base grammar compile the delete from the join source. The rows that matter for
// this fix are the ones whose "from" is an aliased table spelled with " as " / " AS ".
func TestDeleteWithJoinMethod(t *testing.T) {
	t.Run("sqlite joins with where", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").
			Join("contacts", "users.id", "=", "contacts.id", "inner", false).
			Where("users.email", "foo").OrderBy("users.id", "asc").Limit(1).
			ToDeleteSQL()

		assert.Equal(t, `delete from "users" where "rowid" in (select "users"."rowid" from "users" inner join "contacts" on "users"."id" = "contacts"."id" where "users"."email" = ? order by "users"."id" asc limit 1)`, sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})

	t.Run("sqlite aliased", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users as u", "").
			Join("contacts as c", "u.id", "=", "c.id", "inner", false).
			ToDeleteSQL()

		assert.Equal(t, `delete from "users" as "u" where "rowid" in (select "u"."rowid" from "users" as "u" inner join "contacts" as "c" on "u"."id" = "c"."id")`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("mysql joins", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").
			Join("contacts", "users.id", "=", "contacts.id", "inner", false).
			Where("email", "foo").OrderBy("id", "asc").Limit(1).
			ToDeleteSQL()

		assert.Equal(t, "delete `users` from `users` inner join `contacts` on `users`.`id` = `contacts`.`id` where `email` = ?", sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})

	t.Run("mysql aliased with upper case as", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users AS a", "").
			Join("users AS b", "a.id", "=", "b.user_id", "inner", false).
			Where("email", "foo").OrderBy("id", "asc").Limit(1).
			ToDeleteSQL()

		assert.Equal(t, "delete `a` from `users` as `a` inner join `users` as `b` on `a`.`id` = `b`.`user_id` where `email` = ?", sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})

	t.Run("sqlserver joins", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users", "").
			Join("contacts", "users.id", "=", "contacts.id", "inner", false).
			Where("email", "foo").
			ToDeleteSQL()

		assert.Equal(t, "delete [users] from [users] inner join [contacts] on [users].[id] = [contacts].[id] where [email] = ?", sql)
		assert.Equal(t, []any{"foo"}, bindings)
	})
}
