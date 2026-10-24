package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the Postgres half of the "update columns" corpus rows, whose SQL is fixed by
// PostgresGrammar::compileUpdateColumns (Query/Grammars/PostgresGrammar.php:421-433):
//
//	$column = last(explode('.', $key));
//	if ($this->isJsonSelector($key)) {
//	    return $this->compileJsonUpdateColumn($column, $value);
//	}
//	return $this->wrap($column).' = '.$this->parameter($value);
//
// The key is reduced to its last dot-segment before it is wrapped, so a qualified update column such
// as "users.email" assigns to the bare "email", and a qualified JSON selector keeps only the column
// part. The isJsonSelector test still runs against the full key, exactly as Laravel does; it is the
// reduced column that compileJsonUpdateColumn receives. Only Postgres reduces — Grammar, MySqlGrammar
// and SqlServerGrammar compileUpdateColumns wrap the key unchanged, so no other dialect changes here.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. The SQL strings were confirmed against the oracle at
// .claude/laravel-oracle/oracle.php.
//
// The corpus rows are update statements, so they go through ToUpdateSQL rather than the ToSQL the
// laravelCase/runLaravelCases and dialectCase/runDialectCases helpers drive; those helpers render a
// select and cannot carry an update's value list, so the cases below follow the direct form the other
// ordered-update tests (b3, b21) use.

// TestUpdateMethodWithoutJoinsOnPostgres ports Laravel's testUpdateMethodWithoutJoinsOnPostgres
// (corpus L4257-4274). Each case reduces the update key to its last dot-segment; a selectRaw binding
// contributes nothing to the update, so the binding list is the two set values followed by the where.
func TestUpdateMethodWithoutJoinsOnPostgres(t *testing.T) {
	t.Run("qualified column is reduced", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").Where("id", "=", 1).
			ToUpdateSQL(core.UpdateValues{
				{Column: "users.email", Value: "foo"},
				{Column: "name", Value: "bar"},
			})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "id" = ?`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("a select binding is not part of the update", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").Where("id", "=", 1).
			SelectRaw("?", []any{"ignore"}).
			ToUpdateSQL(core.UpdateValues{
				{Column: "users.email", Value: "foo"},
				{Column: "name", Value: "bar"},
			})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "id" = ?`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("a multi-segment qualifier keeps only the column", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users.users", "").Where("id", "=", 1).
			SelectRaw("?", []any{"ignore"}).
			ToUpdateSQL(core.UpdateValues{
				{Column: "users.users.email", Value: "foo"},
				{Column: "name", Value: "bar"},
			})

		assert.Equal(t, `update "users"."users" set "email" = ?, "name" = ? where "id" = ?`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})
}

// TestPostgresUpdateWrappingJson ports Laravel's testPostgresUpdateWrappingJson (corpus L4801-4813).
// The first case is the qualified JSON selector: "users.options->name->first_name" reduces to
// "options->name->first_name" so jsonb_set targets "options", not the qualified identifier. The second
// case's key has no dot and is unaffected.
func TestPostgresUpdateWrappingJson(t *testing.T) {
	t.Run("qualified json selector is reduced", func(t *testing.T) {
		sql, _ := NewBuilder(NewPostgresGrammar("")).From("users", "").
			ToUpdateSQL(core.UpdateValues{{Column: "users.options->name->first_name", Value: "John"}})

		assert.Equal(t, `update "users" set "options" = jsonb_set("options"::jsonb, '{"name","first_name"}', ?)`, sql)
	})

	t.Run("unqualified json selector is unaffected", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
			ToUpdateSQL(core.UpdateValues{{Column: "options->language", Value: Raw("'null'")}})

		assert.Equal(t, `update "users" set "options" = jsonb_set("options"::jsonb, '{"language"}', 'null')`, sql)
		assert.Empty(t, bindings)
	})
}
