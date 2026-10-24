package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file pins the shared schema.Wrap.JsonPath rendering that Laravel's JSON path array-index corpus
// rows depend on. Both rows write a JSON selector whose path holds a bare bracketed segment —
// options->[1]->2fa and meta->tags[0][2] — and assert the exact path string the grammar renders.
//
// Laravel builds that path in Concerns/CompilesJsonPaths::wrapJsonPathSegment(): a segment ending in a
// run of array brackets is emitted verbatim, with the key before the brackets quoted and the brackets
// left unquoted, while a segment that is nothing but brackets is emitted unquoted as-is. So a leading
// bracket renders as '$[1]."2fa"' and a keyed bracket as '$."tags"[0][2]'. The shared
// database/schema/wrap.go Wrap.JsonPath now matches: previously a bracket-only segment fell through to
// the quoted-key branch and rendered as '$."[1]".…', quoting the bracket. One fix there serves both the
// MySQL and the SQLite rows.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569. The line ranges name the
// corpus method's slice; the SQL and bindings were confirmed against the oracle at
// .claude/laravel-oracle/oracle.php (illuminate/database v12.55.1), and the two agree.
//
// The dialectCase/runDialectCases helper is not reused: it compiles the builder with ToSQL(), which
// this package maps to CompileSelect, whereas these rows are update statements reached through
// ToUpdateSQL that also assert their binding list. The sibling update rows in
// laravel_port_fix_b3_ordered_update_values_entry_point_toupdatesql_co_test.go assert directly too.

// TestMySqlUpdateWrappingJsonPathArrayIndex ports Laravel's testMySqlUpdateWrappingJsonPathArrayIndex
// (corpus L4745-4767): a leading bracketed segment renders as '$[1]."2fa"' inside json_set, the boolean
// value is inlined as false, and the array-indexed key renders as '$."tags"[0][2]'.
func TestMySqlUpdateWrappingJsonPathArrayIndex(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options->[1]->2fa", Value: false},
		{Column: "meta->tags[0][2]", Value: "large"},
	}

	sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("active", 1).ToUpdateSQL(values)

	assert.Equal(t, "update `users` set `options` = json_set(`options`, '$[1].\"2fa\"', false), `meta` = json_set(`meta`, '$.\"tags\"[0][2]', ?) where `active` = ?", sql)
	assert.Equal(t, []any{"large", 1}, bindings)
}

// TestSQLiteUpdateWrappingJsonPathArrayIndexWithWhereClause ports Laravel's
// testSQLiteUpdateWrappingJsonPathArrayIndex (corpus L4882-4896). Its sibling
// TestSQLiteUpdateWrappingJsonPathArrayIndex in laravel_port_fix_b30_…_test.go asserts the SET list and
// bindings and deliberately leaves the trailing where unasserted (that row was the b30 grouping item);
// this one asserts the full corpus statement, so the fixed '$[1]."2fa"' path is covered inside the
// where clause's json_extract as well as inside the grouped json_patch assignments. The json boolean
// where keeps its inlined true and contributes no binding, so the binding list stays the two nested
// documents.
func TestSQLiteUpdateWrappingJsonPathArrayIndexWithWhereClause(t *testing.T) {
	values := core.UpdateValues{
		{Column: "options->[1]->2fa", Value: false},
		{Column: "meta->tags[0][2]", Value: "large"},
	}

	sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").Where("options->[1]->2fa", true).ToUpdateSQL(values)

	assert.Equal(t, `update "users" set "options" = json_patch(ifnull("options", json('{}')), json(?)), "meta" = json_patch(ifnull("meta", json('{}')), json(?)) where json_extract("options", '$[1]."2fa"') = true`, sql)
	assert.Equal(t, []any{`{"[1]":{"2fa":false}}`, `{"tags[0][2]":"large"}`}, bindings)
}
