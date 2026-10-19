package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/errors"
)

// This file ports the insert-or-ignore-using family of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php. Laravel asserts these through a connection spy: the
// statement handed to affectingStatement is the compiled SQL, which is what ToInsertOrIgnoreUsingSQL
// returns here, together with the cleaned bindings. The generic-grammar and SqlServer cases assert
// the RuntimeException the base grammar raises instead of SQL.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. The line ranges name the corpus method's slice.
// Every dialect string below is confirmed by the oracle at .claude/laravel-oracle/oracle.php through
// lr_grammar($dialect)->compileInsertOrIgnoreUsing($builder, $columns, $sql): Laravel executes
// insertOrIgnoreUsing rather than compiling it, so the grammar is called directly.
//
// The runLaravelCases/runDialectCases helpers are not used here: they assert a select's ToSQL(), and
// the writes area (laravel_port_writes_test.go) asserts the compiled statement and its bindings
// directly, which is the shape these cases need.

// insertUsingSub is the closure the corpus passes as the subquery. It renders `select bar from
// table2 where foreign_id = ?` in the dialect's own quoting, because the sub-builder shares the
// parent's grammar.
func insertUsingSub() func(*Builder) *Builder {
	return func(q *Builder) *Builder {
		return q.Select("bar").From("table2", "").Where("foreign_id", 5)
	}
}

// insertUsingSubNoColumns is the closure of the empty-columns cases: with no select its compiled
// subquery is `select * from table2 where foreign_id = ?`.
func insertUsingSubNoColumns() func(*Builder) *Builder {
	return func(q *Builder) *Builder {
		return q.From("table2", "").Where("foreign_id", 5)
	}
}

// TestInsertOrIgnoreUsingMethod ports Laravel's testInsertOrIgnoreUsingMethod (corpus L3916-3923):
// the generic grammar has no insert-or-ignore-using, so it records the refusal and returns no SQL.
func TestInsertOrIgnoreUsingMethod(t *testing.T) {
	b := newTestBuilder().From("users", "")
	b.InsertOrIgnoreUsing([]any{"email"}, "bar")

	sql, bindings := b.ToInsertOrIgnoreUsingSQL()

	assert.Empty(t, sql)
	assert.Empty(t, bindings)
	assert.ErrorContains(t, b.Error(), "does not support")
}

// TestSqlServerInsertOrIgnoreUsingMethod ports Laravel's testSqlServerInsertOrIgnoreUsingMethod
// (corpus L3924-3931): SQL Server inherits the base refusal.
func TestSqlServerInsertOrIgnoreUsingMethod(t *testing.T) {
	b := NewBuilder(NewSqlServerGrammar("")).From("users", "")
	b.InsertOrIgnoreUsing([]any{"email"}, "bar")

	sql, _ := b.ToInsertOrIgnoreUsingSQL()

	assert.Empty(t, sql)
	assert.ErrorContains(t, b.Error(), "does not support")
}

// TestMySqlInsertOrIgnoreUsingMethod ports Laravel's testMySqlInsertOrIgnoreUsingMethod (corpus
// L3932-3947).
func TestMySqlInsertOrIgnoreUsingMethod(t *testing.T) {
	b := NewBuilder(NewMySqlGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{"foo"}, insertUsingSub())

	sql, bindings := b.ToInsertOrIgnoreUsingSQL()

	assert.Equal(t, "insert ignore into `table1` (`foo`) select `bar` from `table2` where `foreign_id` = ?", sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestMySqlInsertOrIgnoreUsingWithEmptyColumns ports Laravel's
// testMySqlInsertOrIgnoreUsingWithEmptyColumns (corpus L3948-3963): an empty column list keeps the
// subquery's own projection, which compiles to `select *`.
func TestMySqlInsertOrIgnoreUsingWithEmptyColumns(t *testing.T) {
	b := NewBuilder(NewMySqlGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{}, insertUsingSubNoColumns())

	sql, bindings := b.ToInsertOrIgnoreUsingSQL()

	assert.Equal(t, "insert ignore into `table1` select * from `table2` where `foreign_id` = ?", sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestMySqlInsertOrIgnoreUsingInvalidSubquery ports Laravel's
// testMySqlInsertOrIgnoreUsingInvalidSubquery (corpus L3964-3970): a value that is not a builder, a
// callback or a string is rejected by the argument guard before any SQL is produced.
func TestMySqlInsertOrIgnoreUsingInvalidSubquery(t *testing.T) {
	b := NewBuilder(NewMySqlGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{"foo"}, []any{"bar"})

	sql, _ := b.ToInsertOrIgnoreUsingSQL()

	assert.Empty(t, sql)
	assert.ErrorIs(t, b.Error(), errors.QuerySubqueryTypeInvalid)
}

// TestPostgresInsertOrIgnoreUsingMethod ports Laravel's testPostgresInsertOrIgnoreUsingMethod
// (corpus L3971-3985): Postgres spells the ignore as a trailing conflict clause.
func TestPostgresInsertOrIgnoreUsingMethod(t *testing.T) {
	b := NewBuilder(NewPostgresGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{"foo"}, insertUsingSub())

	sql, bindings := b.ToInsertOrIgnoreUsingSQL()

	assert.Equal(t, `insert into "table1" ("foo") select "bar" from "table2" where "foreign_id" = ? on conflict do nothing`, sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestPostgresInsertOrIgnoreUsingWithEmptyColumns ports Laravel's
// testPostgresInsertOrIgnoreUsingWithEmptyColumns (corpus L3986-4000).
func TestPostgresInsertOrIgnoreUsingWithEmptyColumns(t *testing.T) {
	b := NewBuilder(NewPostgresGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{}, insertUsingSubNoColumns())

	sql, bindings := b.ToInsertOrIgnoreUsingSQL()

	assert.Equal(t, `insert into "table1" select * from "table2" where "foreign_id" = ? on conflict do nothing`, sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestPostgresInsertOrIgnoreUsingInvalidSubquery ports Laravel's
// testPostgresInsertOrIgnoreUsingInvalidSubquery (corpus L4001-4007).
func TestPostgresInsertOrIgnoreUsingInvalidSubquery(t *testing.T) {
	b := NewBuilder(NewPostgresGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{"foo"}, []any{"bar"})

	sql, _ := b.ToInsertOrIgnoreUsingSQL()

	assert.Empty(t, sql)
	assert.ErrorIs(t, b.Error(), errors.QuerySubqueryTypeInvalid)
}

// TestSQLiteInsertOrIgnoreUsingMethod ports Laravel's testSQLiteInsertOrIgnoreUsingMethod (corpus
// L4008-4023): SQLite spells the ignore as `insert or ignore`.
func TestSQLiteInsertOrIgnoreUsingMethod(t *testing.T) {
	b := NewBuilder(NewSqliteGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{"foo"}, insertUsingSub())

	sql, bindings := b.ToInsertOrIgnoreUsingSQL()

	assert.Equal(t, `insert or ignore into "table1" ("foo") select "bar" from "table2" where "foreign_id" = ?`, sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestSQLiteInsertOrIgnoreUsingWithEmptyColumns ports Laravel's
// testSQLiteInsertOrIgnoreUsingWithEmptyColumns (corpus L4024-4039).
func TestSQLiteInsertOrIgnoreUsingWithEmptyColumns(t *testing.T) {
	b := NewBuilder(NewSqliteGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{}, insertUsingSubNoColumns())

	sql, bindings := b.ToInsertOrIgnoreUsingSQL()

	assert.Equal(t, `insert or ignore into "table1" select * from "table2" where "foreign_id" = ?`, sql)
	assert.Equal(t, []any{5}, bindings)
}

// TestSQLiteInsertOrIgnoreUsingInvalidSubquery ports Laravel's
// testSQLiteInsertOrIgnoreUsingInvalidSubquery (corpus L4040-4046).
func TestSQLiteInsertOrIgnoreUsingInvalidSubquery(t *testing.T) {
	b := NewBuilder(NewSqliteGrammar("")).From("table1", "")
	b.InsertOrIgnoreUsing([]any{"foo"}, []any{"bar"})

	sql, _ := b.ToInsertOrIgnoreUsingSQL()

	assert.Empty(t, sql)
	assert.ErrorIs(t, b.Error(), errors.QuerySubqueryTypeInvalid)
}
