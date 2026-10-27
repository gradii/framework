package query

import (
	"testing"
	"time"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the "update wrapping JSON" family of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php for the ordered update-values entry point.
//
// Laravel's Builder::update() receives an associative array whose insertion order fixes both the SET
// list and the order of the bound values (Builder.php:3897; the grammars map over that array). Ours
// took a map[string]any, so the order was lost and CompileUpdateColumns fell back to sorting the keys.
// ToUpdateSQL now accepts an ordered UpdateValues and every grammar iterates it in order; a plain
// map[string]any is still accepted and is ordered by column name, which is the behaviour the rest of
// the corpus relies on.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. The line ranges name the corpus method's slice.
//
// Only the part of each row the ordering entry point reaches is asserted here:
//   - testMySqlUpdateWrappingJsonArray (L4718-4744), testPostgresUpdateWrappingJsonArray (L4814-4831),
//     testPostgresUpdateWrappingJsonPathArrayIndex (L4832-4846) and testSQLiteUpdateWrappingJsonArray
//     (L4847-4863) produce the corpus SQL exactly once the values are ordered. Their bindings are not
//     asserted because Laravel json_encodes json-selector scalars and array values in
//     prepareBindingsForUpdate; that is the separate JSON-encoding item (b21), not the ordering item,
//     and it changes those binding values.
//   - testMySqlUpdateWrappingJsonPathArrayIndex (L4745-4767) is not asserted: a bracketed JSON path
//     segment must render as '$[1]."2fa"', which is the separate shared schema.Wrap.JsonPath item (b31).
//   - testSQLiteUpdateWrappingNestedJsonArray (L4864-4881) and testSQLiteUpdateWrappingJsonPathArrayIndex
//     (L4882-4896) are not asserted: they additionally need SqliteGrammar::groupJsonColumnsForUpdate and
//     the same path-rendering fix (items b30 and b31).
//
// The SQL strings below were confirmed against the oracle at .claude/laravel-oracle/oracle.php.

// TestOrderedUpdateValuesKeepInsertionOrder covers the entry point itself: UpdateValues fixes the SET
// order, the binding order and, because a set binding precedes the where bindings, the whole binding
// list — exactly the array order Laravel keeps in Builder::update().
func TestOrderedUpdateValuesKeepInsertionOrder(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").Where("id", 1).
		ToUpdateSQL(core.UpdateValues{
			{Column: "name", Value: "n"},
			{Column: "email", Value: "e"},
		})

	assert.Equal(t, `update "users" set "name" = ?, "email" = ? where "id" = ?`, sql)
	assert.Equal(t, []any{"n", "e", 1}, bindings)
}

// TestUpdateValuesMapFallbackKeepsSorting documents the fallback: a caller that still passes a plain
// map gets the historic sorted order, so existing call sites are unaffected by the ordered entry point.
func TestUpdateValuesMapFallbackKeepsSorting(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").Where("id", 1).
		ToUpdateSQL(map[string]any{"name": "n", "email": "e"})

	assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "id" = ?`, sql)
	assert.Equal(t, []any{"e", "n", 1}, bindings)
}

// TestOrderedUpdateValuesAcrossDialects exercises the ordered binding path on every dialect that
// overrides prepareBindingsForUpdate, none of which may reorder the set bindings.
func TestOrderedUpdateValuesAcrossDialects(t *testing.T) {
	values := core.UpdateValues{
		{Column: "name", Value: "n"},
		{Column: "email", Value: "e"},
	}

	grammars := map[string]Grammar{
		"base":      NewBaseGrammar(""),
		"mysql":     NewMySqlGrammar(""),
		"postgres":  NewPostgresGrammar(""),
		"sqlite":    NewSqliteGrammar(""),
		"sqlserver": NewSqlServerGrammar(""),
	}

	for name, grammar := range grammars {
		t.Run(name, func(t *testing.T) {
			_, bindings := NewBuilder(grammar).From("users", "").Where("id", 1).ToUpdateSQL(values)

			assert.Equal(t, []any{"n", "e", 1}, bindings)
		})
	}
}

// TestMySqlUpdateWrappingJsonArray ports Laravel's testMySqlUpdateWrappingJsonArray (corpus L4718-4744):
// the SET list stays in the array's order — options, meta, group_id, created_at — rather than being
// sorted, and the array-valued json selector keeps its cast(? as json) rendering.
func TestMySqlUpdateWrappingJsonArray(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "meta->tags", Value: []any{"white", "large"}},
		{Column: "group_id", Value: Raw("45")},
		{Column: "created_at", Value: time.Date(2019, 8, 6, 0, 0, 0, 0, time.UTC)},
	}

	sql, _ := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("active", 1).ToUpdateSQL(values)

	assert.Equal(t, "update `users` set `options` = ?, `meta` = json_set(`meta`, '$.\"tags\"', cast(? as json)), `group_id` = 45, `created_at` = ? where `active` = ?", sql)
}

// TestPostgresUpdateWrappingJsonArray ports Laravel's testPostgresUpdateWrappingJsonArray (corpus
// L4814-4831): the SET list stays in the array's order.
func TestPostgresUpdateWrappingJsonArray(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "meta->tags", Value: []any{"white", "large"}},
		{Column: "group_id", Value: Raw("45")},
		{Column: "created_at", Value: time.Date(2019, 8, 6, 0, 0, 0, 0, time.UTC)},
	}

	sql, _ := NewBuilder(NewPostgresGrammar("")).From("users", "").ToUpdateSQL(values)

	assert.Equal(t, `update "users" set "options" = ?, "meta" = jsonb_set("meta"::jsonb, '{"tags"}', ?), "group_id" = 45, "created_at" = ?`, sql)
}

// TestPostgresUpdateWrappingJsonPathArrayIndex ports Laravel's testPostgresUpdateWrappingJsonPathArrayIndex
// (corpus L4832-4846): both jsonb_set paths already match; the ordering fixes the SET list.
func TestPostgresUpdateWrappingJsonPathArrayIndex(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options->[1]->2fa", Value: false},
		{Column: "meta->tags[0][2]", Value: "large"},
	}

	sql, _ := NewBuilder(NewPostgresGrammar("")).From("users", "").Where("options->[1]->2fa", true).ToUpdateSQL(values)

	assert.Equal(t, `update "users" set "options" = jsonb_set("options"::jsonb, '{1,"2fa"}', ?), "meta" = jsonb_set("meta"::jsonb, '{"tags",0,2}', ?) where ("options"->1->'2fa')::jsonb = 'true'::jsonb`, sql)
}

// TestSQLiteUpdateWrappingJsonArray ports Laravel's testSQLiteUpdateWrappingJsonArray (corpus L4847-4863):
// the SET list stays in the array's order.
func TestSQLiteUpdateWrappingJsonArray(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options", Value: map[string]any{"2fa": false, "presets": []any{"laravel", "vue"}}},
		{Column: "group_id", Value: Raw("45")},
		{Column: "created_at", Value: time.Date(2019, 8, 6, 0, 0, 0, 0, time.UTC)},
	}

	sql, _ := NewBuilder(NewSqliteGrammar("")).From("users", "").ToUpdateSQL(values)

	assert.Equal(t, `update "users" set "options" = ?, "group_id" = 45, "created_at" = ?`, sql)
}
