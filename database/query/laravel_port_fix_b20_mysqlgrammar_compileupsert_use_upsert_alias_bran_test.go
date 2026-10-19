package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports Laravel's testUpsertMethod, testUpsertMethodWithUpdateColumns and the
// use_upsert_alias=true half of testPreservedAreAppliedByUpsert
// (tests/Database/DatabaseQueryBuilderTest.php, corpus L4111-4174 and L4606-4626).
//
// Laravel reads use_upsert_alias from the connection in MySqlGrammar::compileUpsert; here the flag
// lives on the builder and is set with SetUseUpsertAlias, then the statement is compiled through
// ToUpsertSQL (Laravel's Builder::upsert hands the same values to compileUpsert).
//
// The shared laravelCase/runLaravelCases and dialectCase/runDialectCases helpers compile a select via
// ToSQL, so they cannot express an upsert; like the package's other upsert tests, these assert the
// compiled SQL and bindings directly.

// mysqlUpsertBuilder returns a MySQL builder over "users"; withAlias enables the connection's
// use_upsert_alias setting, which the grammar reads through the builder.
func mysqlUpsertBuilder(withAlias bool) *Builder {
	b := NewBuilder(NewMySqlGrammar("")).From("users", "")
	if withAlias {
		b.SetUseUpsertAlias(true)
	}
	return b
}

// TestMySqlUpsertMethodUseUpsertAlias ports the MySQL half of Laravel's testUpsertMethod
// (corpus L4111-4142): with use_upsert_alias off the copied columns read through values(); with it on
// the insert is aliased and the columns read through `laravel_upsert_alias`.
func TestMySqlUpsertMethodUseUpsertAlias(t *testing.T) {
	values := []map[string]any{{"email": "foo", "name": "bar"}, {"name": "bar2", "email": "foo2"}}
	// upsert($values, 'email') derives $update from the first record's keys, so the update list is
	// the value-copying form for both columns.
	update := []UpsertUpdate{{Column: "email"}, {Column: "name"}}

	t.Run("use_upsert_alias=false", func(t *testing.T) {
		sql, bindings := mysqlUpsertBuilder(false).ToUpsertSQL(values, []string{"email"}, update)

		assert.Equal(t, "insert into `users` (`email`, `name`) values (?, ?), (?, ?) "+
			"on duplicate key update `email` = values(`email`), `name` = values(`name`)", sql)
		assert.Equal(t, []any{"foo", "bar", "foo2", "bar2"}, bindings)
	})

	t.Run("use_upsert_alias=true", func(t *testing.T) {
		sql, bindings := mysqlUpsertBuilder(true).ToUpsertSQL(values, []string{"email"}, update)

		assert.Equal(t, "insert into `users` (`email`, `name`) values (?, ?), (?, ?) as laravel_upsert_alias "+
			"on duplicate key update `email` = `laravel_upsert_alias`.`email`, `name` = `laravel_upsert_alias`.`name`", sql)
		assert.Equal(t, []any{"foo", "bar", "foo2", "bar2"}, bindings)
	})
}

// TestMySqlUpsertMethodWithUpdateColumnsUseUpsertAlias ports the MySQL half of Laravel's
// testUpsertMethodWithUpdateColumns (corpus L4143-4174).
func TestMySqlUpsertMethodWithUpdateColumnsUseUpsertAlias(t *testing.T) {
	values := []map[string]any{{"email": "foo", "name": "bar"}, {"name": "bar2", "email": "foo2"}}
	update := []UpsertUpdate{{Column: "name"}}

	t.Run("use_upsert_alias=false", func(t *testing.T) {
		sql, bindings := mysqlUpsertBuilder(false).ToUpsertSQL(values, []string{"email"}, update)

		assert.Equal(t, "insert into `users` (`email`, `name`) values (?, ?), (?, ?) "+
			"on duplicate key update `name` = values(`name`)", sql)
		assert.Equal(t, []any{"foo", "bar", "foo2", "bar2"}, bindings)
	})

	t.Run("use_upsert_alias=true", func(t *testing.T) {
		sql, bindings := mysqlUpsertBuilder(true).ToUpsertSQL(values, []string{"email"}, update)

		assert.Equal(t, "insert into `users` (`email`, `name`) values (?, ?), (?, ?) as laravel_upsert_alias "+
			"on duplicate key update `name` = `laravel_upsert_alias`.`name`", sql)
		assert.Equal(t, []any{"foo", "bar", "foo2", "bar2"}, bindings)
	})
}

// TestMySqlPreservedAreAppliedByUpsertUseUpsertAlias ports the use_upsert_alias=true half of Laravel's
// testPreservedAreAppliedByUpsert (corpus L4606-4626), which the before-query work item left out. The
// from() the callback supplies is still the one compiled; the alias branch is what is added here.
func TestMySqlPreservedAreAppliedByUpsertUseUpsertAlias(t *testing.T) {
	b := NewBuilder(NewMySqlGrammar(""))
	b.SetUseUpsertAlias(true)
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })

	sql, bindings := b.ToUpsertSQL([]map[string]any{{"email": "foo"}}, []string{"id"}, []UpsertUpdate{{Column: "email"}})

	assert.Equal(t, "insert into `users` (`email`) values (?) as laravel_upsert_alias "+
		"on duplicate key update `email` = `laravel_upsert_alias`.`email`", sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestMySqlUpsertAliasKeepsBoundUpdates pins the numeric-vs-string key split Laravel's compileUpsert
// makes: an update entry that binds its own value stays "col = ?" whether or not the alias is on; only
// the value-copying form switches to the alias. (Oracle: bound alias=true -> `name` = ?.)
func TestMySqlUpsertAliasKeepsBoundUpdates(t *testing.T) {
	values := []map[string]any{{"email": "foo", "name": "bar"}}
	update := []UpsertUpdate{{Column: "name", Value: "baz", Bound: true}}

	sql, bindings := mysqlUpsertBuilder(true).ToUpsertSQL(values, []string{"email"}, update)

	assert.Equal(t, "insert into `users` (`email`, `name`) values (?, ?) as laravel_upsert_alias "+
		"on duplicate key update `name` = ?", sql)
	assert.Equal(t, []any{"foo", "bar", "baz"}, bindings)
}

// TestMySqlUpsertAliasOffByDefault pins that a builder without the flag compiles the plain values()
// form, so existing callers are unaffected.
func TestMySqlUpsertAliasOffByDefault(t *testing.T) {
	sql, _ := mysqlUpsertBuilder(false).ToUpsertSQL(
		[]map[string]any{{"email": "foo"}}, []string{"id"}, []UpsertUpdate{{Column: "email"}})

	assert.Equal(t, "insert into `users` (`email`) values (?) on duplicate key update `email` = values(`email`)", sql)
}
