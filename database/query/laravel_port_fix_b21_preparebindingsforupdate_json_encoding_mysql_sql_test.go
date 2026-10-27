package query

import (
	"testing"
	"time"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the binding half of the "update wrapping JSON" corpus rows, which Laravel computes in
// each grammar's prepareBindingsForUpdate override:
//
//   - MySqlGrammar::prepareBindingsForUpdate (Query/Grammars/MySqlGrammar.php:446-453): a JSON selector
//     bound to a bool compiles to an inline true/false literal, so its binding is dropped; every array
//     value is json-encoded.
//   - SQLiteGrammar::prepareBindingsForUpdate (SQLiteGrammar.php:407-420): the JSON selectors are
//     regrouped per column (groupJsonColumnsForUpdate), every array value is json-encoded, and the set
//     values precede the bindings of every clause except select.
//   - PostgresGrammar::prepareBindingsForUpdate (PostgresGrammar.php:626-634): every array value and
//     every non-expression JSON-selector value — scalars included — is json-encoded.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. The SQL strings were confirmed against the oracle at
// .claude/laravel-oracle/oracle.php (which also printed the exact binding lists reproduced below).
//
// The array rows' bindings were deliberately left unasserted by the ordering item (b3); their set order
// is already pinned there and only the encoding is added here. Where a SQL string still needs a later
// item it is called out on the case, and only the binding is pinned.

var b21Date = time.Date(2019, 8, 6, 0, 0, 0, 0, time.UTC)

// TestPrepareBindingsMySqlWrappingJsonArray ports Laravel's testMySqlUpdateWrappingJsonArray (corpus
// L4718-4744): the plain map and the json-selector array are both json-encoded, the Raw renders inline
// and contributes no binding, and the set bindings precede the where binding.
func TestPrepareBindingsMySqlWrappingJsonArray(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "meta->tags", Value: []any{"white", "large"}},
		{Column: "group_id", Value: Raw("45")},
		{Column: "created_at", Value: b21Date},
	}

	sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("active", 1).ToUpdateSQL(values)

	assert.Equal(t, "update `users` set `options` = ?, `meta` = json_set(`meta`, '$.\"tags\"', cast(? as json)), `group_id` = 45, `created_at` = ? where `active` = ?", sql)
	assert.Equal(t, []any{`{"2fa":false,"presets":["laravel","vue"]}`, `["white","large"]`, b21Date, 1}, bindings)
}

// TestPrepareBindingsMySqlWithJson ports Laravel's testMySqlUpdateWithJsonPreparesBindingsCorrectly
// (corpus L4768-4800): a JSON selector bound to a bool is emitted inline and dropped, while ints, nulls
// and Raw keep their behaviour.
func TestPrepareBindingsMySqlWithJson(t *testing.T) {
	t.Run("json bool is inline and dropped", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("id", "=", 0).
			ToUpdateSQL(core.UpdateValues{
				{Column: "options->enable", Value: false},
				{Column: "updated_at", Value: "2015-05-26 22:02:06"},
			})

		assert.Equal(t, "update `users` set `options` = json_set(`options`, '$.\"enable\"', false), `updated_at` = ? where `id` = ?", sql)
		assert.Equal(t, []any{"2015-05-26 22:02:06", 0}, bindings)
	})

	t.Run("json int is bound", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("id", "=", 0).
			ToUpdateSQL(core.UpdateValues{
				{Column: "options->size", Value: 45},
				{Column: "updated_at", Value: "2015-05-26 22:02:06"},
			})

		assert.Equal(t, "update `users` set `options` = json_set(`options`, '$.\"size\"', ?), `updated_at` = ? where `id` = ?", sql)
		assert.Equal(t, []any{45, "2015-05-26 22:02:06", 0}, bindings)
	})

	t.Run("json null is bound", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").
			ToUpdateSQL(core.UpdateValues{{Column: "options->size", Value: nil}})

		assert.Equal(t, "update `users` set `options` = json_set(`options`, '$.\"size\"', ?)", sql)
		assert.Equal(t, []any{nil}, bindings)
	})

	t.Run("json raw is inline", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").
			ToUpdateSQL(core.UpdateValues{{Column: "options->size", Value: Raw("45")}})

		assert.Equal(t, "update `users` set `options` = json_set(`options`, '$.\"size\"', 45)", sql)
		assert.Empty(t, bindings)
	})
}

// TestPrepareBindingsPostgresWrappingJson ports Laravel's testPostgresUpdateWrappingJson (corpus
// L4801-4813): a non-expression JSON-selector scalar is json-encoded, so a string is bound quoted. The
// first case's SQL still needs b29's dotted-prefix reduction (compileUpdateColumns' last(explode('.')),
// which strips the "users." qualifier); its binding is asserted here regardless. The Raw case renders
// inline with no binding and its SQL does not depend on that reduction.
func TestPrepareBindingsPostgresWrappingJson(t *testing.T) {
	t.Run("scalar json selector is quoted", func(t *testing.T) {
		_, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
			ToUpdateSQL(core.UpdateValues{{Column: "users.options->name->first_name", Value: "John"}})

		assert.Equal(t, []any{`"John"`}, bindings)
	})

	t.Run("raw json selector is inline", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
			ToUpdateSQL(core.UpdateValues{{Column: "options->language", Value: Raw("'null'")}})

		assert.Equal(t, `update "users" set "options" = jsonb_set("options"::jsonb, '{"language"}', 'null')`, sql)
		assert.Empty(t, bindings)
	})
}

// TestPrepareBindingsPostgresWrappingJsonArray ports the bindings of Laravel's
// testPostgresUpdateWrappingJsonArray (corpus L4814-4831): the plain map and the json-selector array are
// both json-encoded and the Raw is dropped. The SET order is pinned by b3.
func TestPrepareBindingsPostgresWrappingJsonArray(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "meta->tags", Value: []any{"white", "large"}},
		{Column: "group_id", Value: Raw("45")},
		{Column: "created_at", Value: b21Date},
	}

	_, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").ToUpdateSQL(values)

	assert.Equal(t, []any{`{"2fa":false,"presets":["laravel","vue"]}`, `["white","large"]`, b21Date}, bindings)
}

// TestPrepareBindingsPostgresWrappingJsonPathArrayIndex ports the bindings of Laravel's
// testPostgresUpdateWrappingJsonPathArrayIndex (corpus L4832-4846): both json-selector values, a bool
// and a string, are json-encoded. The where clause's boolean is a raw literal with no binding.
func TestPrepareBindingsPostgresWrappingJsonPathArrayIndex(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options->[1]->2fa", Value: false},
		{Column: "meta->tags[0][2]", Value: "large"},
	}

	_, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
		Where("options->[1]->2fa", true).ToUpdateSQL(values)

	assert.Equal(t, []any{"false", `"large"`}, bindings)
}

// TestPrepareBindingsSQLiteWrappingJsonArray ports Laravel's testSQLiteUpdateWrappingJsonArray (corpus
// L4847-4863): the plain map is json-encoded, the Raw drops out and the set bindings precede the where.
func TestPrepareBindingsSQLiteWrappingJsonArray(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "group_id", Value: Raw("45")},
		{Column: "created_at", Value: b21Date},
	}

	sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").ToUpdateSQL(values)

	assert.Equal(t, `update "users" set "options" = ?, "group_id" = 45, "created_at" = ?`, sql)
	assert.Equal(t, []any{`{"2fa":false,"presets":["laravel","vue"]}`, b21Date}, bindings)
}

// TestPrepareBindingsSQLiteWrappingNestedJsonArray ports the bindings of Laravel's
// testSQLiteUpdateWrappingNestedJsonArray (corpus L4864-4881): the JSON selectors fold into a single
// nested document per column, json-encoded as one value; the plain columns keep their own. The SQL
// needs the column grouping (b30); only the set bindings are pinned here.
func TestPrepareBindingsSQLiteWrappingNestedJsonArray(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options->name", Value: "Taylor"},
		{Column: "group_id", Value: Raw("45")},
		{Column: "options->security", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "options->sharing->twitter", Value: "username"},
		{Column: "created_at", Value: b21Date},
	}

	_, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").ToUpdateSQL(values)

	assert.Equal(t, []any{
		b21Date,
		`{"name":"Taylor","security":{"2fa":false,"presets":["laravel","vue"]},"sharing":{"twitter":"username"}}`,
	}, bindings)
}

// TestPrepareBindingsSQLiteWrappingJsonPathArrayIndex ports the bindings of Laravel's
// testSQLiteUpdateWrappingJsonPathArrayIndex (corpus L4882-4896): a bracketed path segment stays a
// literal object key inside the grouped document, json-encoded. The SQL needs the path rendering (b31)
// and the where clause needs nothing here; only the set bindings are pinned.
func TestPrepareBindingsSQLiteWrappingJsonPathArrayIndex(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options->[1]->2fa", Value: false},
		{Column: "meta->tags[0][2]", Value: "large"},
	}

	_, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").
		Where("options->[1]->2fa", true).ToUpdateSQL(values)

	assert.Equal(t, []any{`{"[1]":{"2fa":false}}`, `{"tags[0][2]":"large"}`}, bindings)
}
