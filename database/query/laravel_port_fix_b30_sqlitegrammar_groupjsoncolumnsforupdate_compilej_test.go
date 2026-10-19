package query

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// This file ports the SQLite half of Laravel's "update wrapping JSON" corpus rows, which
// SQLiteGrammar::compileUpdateColumns (Query/Grammars/SQLiteGrammar.php:307-322) compiles by grouping:
//
//   - groupJsonColumnsForUpdate (SQLiteGrammar.php:354-363) folds every JSON selector of one column into
//     a single nested document, at the path str_replace('->', '.', Str::after($key, '.')), in
//     first-encounter column order.
//   - compileJsonPatch (SQLiteGrammar.php:374-377) then names the wrapped column rather than the
//     extraction inside it: json_patch(ifnull("options", json('{}')), json(?)).
//   - the reject(json selectors)->merge($jsonGroups) step appends each group last, overwriting in place a
//     plain column that shares its name; the plain assignments keep their own order in front.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php: testSQLiteUpdateWrappingNestedJsonArray (L4864-4881) and
// testSQLiteUpdateWrappingJsonPathArrayIndex (L4882-4896). Every SQL string and binding list below was
// confirmed against the oracle at .claude/laravel-oracle/oracle.php (illuminate/database v12.55.1); the
// two agree.
//
// The shared laravelCase/runLaravelCases and dialectCase/runDialectCases helpers compile a select via
// ToSQL, so they cannot express an update; like the package's other upsert/update tests these assert the
// compiled SQL and bindings directly.

var b30Date = time.Date(2019, 8, 6, 0, 0, 0, 0, time.UTC)

// TestSQLiteUpdateWrappingNestedJsonArray ports Laravel's testSQLiteUpdateWrappingNestedJsonArray (corpus
// L4864-4881): the three options-> selectors fold into one json_patch on "options", appended after the
// non-selector assignments (group_id, created_at), and the Raw renders inline with no binding.
func TestSQLiteUpdateWrappingNestedJsonArray(t *testing.T) {
	values := UpdateValues{
		{Column: "options->name", Value: "Taylor"},
		{Column: "group_id", Value: Raw("45")},
		{Column: "options->security", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "options->sharing->twitter", Value: "username"},
		{Column: "created_at", Value: b30Date},
	}

	sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").ToUpdateSQL(values)

	assert.Equal(t,
		`update "users" set "group_id" = 45, "created_at" = ?, "options" = json_patch(ifnull("options", json('{}')), json(?))`,
		sql)
	assert.Equal(t, []any{
		b30Date,
		`{"name":"Taylor","security":{"2fa":false,"presets":["laravel","vue"]},"sharing":{"twitter":"username"}}`,
	}, bindings)
}

// TestSQLiteUpdateWrappingJsonPathArrayIndex ports Laravel's testSQLiteUpdateWrappingJsonPathArrayIndex
// (corpus L4882-4896): each column's selector folds into one patch, in first-encounter order, and a
// bracketed path segment stays a literal object key inside the grouped document.
//
// The corpus statement's trailing `where json_extract("options", '$[1]."2fa"') = true` renders its JSON
// path through the shared Wrap::jsonPath, whose bracketed-segment fix ('$[1]."2fa"' rather than the
// present '$."[1]"."2fa"') is the separate b31 item. It is deliberately not asserted here so the pre-b31
// rendering is not pinned; only b30's SET list and the corpus bindings are.
func TestSQLiteUpdateWrappingJsonPathArrayIndex(t *testing.T) {
	values := UpdateValues{
		{Column: "options->[1]->2fa", Value: false},
		{Column: "meta->tags[0][2]", Value: "large"},
	}

	sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").ToUpdateSQL(values)

	assert.Equal(t,
		`update "users" set "options" = json_patch(ifnull("options", json('{}')), json(?)), "meta" = json_patch(ifnull("meta", json('{}')), json(?))`,
		sql)
	assert.Equal(t, []any{`{"[1]":{"2fa":false}}`, `{"tags[0][2]":"large"}`}, bindings)

	// The where clause's boolean is emitted inline (`= true`) and contributes no binding, so the full
	// corpus statement's binding list stays the same two values.
	_, withWhere := NewBuilder(NewSqliteGrammar("")).From("users", "").Where("options->[1]->2fa", true).ToUpdateSQL(values)
	assert.Equal(t, []any{`{"[1]":{"2fa":false}}`, `{"tags[0][2]":"large"}`}, withWhere)
}
