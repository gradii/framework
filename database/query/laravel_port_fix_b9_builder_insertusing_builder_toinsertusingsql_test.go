package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/errors"
)

// This file ports the insert-using family of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php. Laravel asserts these through a connection spy: the
// statement handed to affectingStatement is the compiled SQL, which is what ToInsertUsingSQL returns
// here, together with the cleaned bindings. The invalid-subquery case asserts the
// InvalidArgumentException the argument guard throws instead of SQL.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. The line ranges name the corpus method's slice.
//
// Grammar::compileInsertUsing (Grammar.php:1244) is dialect-neutral: the oracle shows MySql, SQLite,
// Postgres and SqlServer all emit the same statement modulo quoting, so there is no per-dialect
// override to port. (Their insert-ignore-using variants build on it; that is the b1 work item, not
// this one.) Every string below is confirmed by the oracle at .claude/laravel-oracle/oracle.php via
// lr_grammar($dialect)->compileInsertUsing($outer, $columns, $subSql): Laravel executes insertUsing
// rather than compiling it, so the grammar is called directly there.
//
// The runLaravelCases/runDialectCases helpers are not used: they assert a select's ToSQL(), and this
// (like the b1 insert-or-ignore-using family) is a write whose compiled statement and bindings are
// compared directly, which is the shape these cases need.

// TestInsertUsingMethod ports Laravel's testInsertUsingMethod (corpus L3839-3853): the subquery is
// resolved from the closure and spliced into the insert after the target column list.
func TestInsertUsingMethod(t *testing.T) {
	b := newTestBuilder().From("table1", "")
	b.InsertUsing([]any{"foo"}, insertUsingSub())

	sql, bindings := b.ToInsertUsingSQL()

	assert.Equal(t, `insert into "table1" ("foo") select "bar" from "table2" where "foreign_id" = ?`, sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestInsertUsingWithEmptyColumns ports Laravel's testInsertUsingWithEmptyColumns (corpus L3854-3868):
// an empty column list keeps the subquery's own projection, which compiles to `select *`.
func TestInsertUsingWithEmptyColumns(t *testing.T) {
	b := newTestBuilder().From("table1", "")
	b.InsertUsing([]any{}, insertUsingSubNoColumns())

	sql, bindings := b.ToInsertUsingSQL()

	assert.Equal(t, `insert into "table1" select * from "table2" where "foreign_id" = ?`, sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestInsertUsingInvalidSubquery ports Laravel's testInsertUsingInvalidSubquery (corpus L3869-3875):
// a value that is not a builder, a callback or a string is rejected by the argument guard before any
// SQL is produced (parseSub's InvalidArgumentException, surfaced as QuerySubqueryTypeInvalid).
func TestInsertUsingInvalidSubquery(t *testing.T) {
	b := newTestBuilder().From("table1", "")
	b.InsertUsing([]any{"foo"}, []any{"bar"})

	sql, bindings := b.ToInsertUsingSQL()

	assert.Empty(t, sql)
	assert.Empty(t, bindings)
	assert.ErrorIs(t, b.Error(), errors.QuerySubqueryTypeInvalid)
}

// TestPreservedAreAppliedByInsertUsing ports Laravel's testPreservedAreAppliedByInsertUsing (corpus
// L4596-4605): the beforeQuery closure supplies the from() the insert targets, and insertUsing
// applies the queued callbacks at its head before resolving the subquery.
func TestPreservedAreAppliedByInsertUsing(t *testing.T) {
	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })
	b.InsertUsing([]any{"email"}, newTestBuilder())

	sql, bindings := b.ToInsertUsingSQL()

	assert.Equal(t, `insert into "users" ("email") select *`, sql)
	assert.Empty(t, bindings)
}
