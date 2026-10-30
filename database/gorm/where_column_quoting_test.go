package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/errors"
)

// This file pins the Laravel-faithful handling of column names and order directions in the ORM
// wrapper. Laravel wraps every column with the grammar, and rejects any order direction that is not
// asc or desc ("Order direction must be asc or desc"); the wrapper used to concatenate a
// caller-supplied column into the BETWEEN / IS NULL / WHERE group fragments and the ORDER BY
// direction straight into the SQL, so a column string like "1=1 OR id" or a direction like
// "asc; DROP TABLE users --" reached the statement verbatim (upstream #1002 fixed the same class
// upstream; the shapes here were measured against .claude/laravel-oracle/oracle.php).

func TestWhereBetween_QuotesTheColumn(t *testing.T) {
	q := inQuery(t).WhereBetween("1=1 OR id", 1, 2).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "1=1 OR id" between ? and ?`, sql)
	assert.Equal(t, []any{1, 2}, bindings)
}

func TestWhereNotBetween_QuotesTheColumn(t *testing.T) {
	q := inQuery(t).WhereNotBetween("1=1 OR id", 1, 2).(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "1=1 OR id" not between ? and ?`, sql)
}

func TestOrWhereBetween_KeepsTheOrConnector(t *testing.T) {
	q := inQuery(t).Where("name", "zero").OrWhereBetween("1=1 OR id", 1, 2).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "name" = ? or "1=1 OR id" between ? and ?`, sql)
	assert.Equal(t, []any{"zero", 1, 2}, bindings)
}

func TestWhereNull_QuotesTheColumn(t *testing.T) {
	q := inQuery(t).WhereNull("1=1 OR id").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "1=1 OR id" is null`, sql)
}

func TestWhereNotNull_QuotesTheColumn(t *testing.T) {
	q := inQuery(t).WhereNotNull("1=1 OR id").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "1=1 OR id" is not null`, sql)
}

func TestOrWhereNull_KeepsTheOrConnector(t *testing.T) {
	q := inQuery(t).Where("name", "zero").OrWhereNull("1=1 OR id").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "name" = ? or "1=1 OR id" is null`, sql)
}

func TestWhereAll_QuotesEveryColumn(t *testing.T) {
	q := inQuery(t).WhereAll([]string{"name", "email"}, "=", "x").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where ("name" = ? and "email" = ?)`, sql)
}

func TestWhereAny_QuotesEveryColumn(t *testing.T) {
	q := inQuery(t).WhereAny([]string{"name", "email"}, "=", "x").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where ("name" = ? or "email" = ?)`, sql)
}

// A single-column WhereNone renders the negated equals Laravel renders - not ("name" = ?) - rather
// than the "<>" its fragment used to emit.
func TestWhereNone_NegatesTheEquals(t *testing.T) {
	q := inQuery(t).WhereNone([]string{"name"}, "=", "x").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where not ("name" = ?)`, sql)
}

func TestWhereNone_NegatesTheWholeGroup(t *testing.T) {
	q := inQuery(t).WhereNone([]string{"name", "email"}, "LIKE", "a").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where not ("name" LIKE ? or "email" LIKE ?)`, sql)
}

func TestOrderBy_QuotesTheColumn(t *testing.T) {
	q := inQuery(t).OrderBy("id; DROP TABLE users --").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" order by "id; DROP TABLE users --" asc`, sql)
}

func TestOrderBy_AcceptsEitherDirection(t *testing.T) {
	q := inQuery(t).OrderBy("id", "DESC").(*Query)

	sql, _ := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" order by "id" desc`, sql)
}

// A direction that is not asc or desc is rejected the way Laravel rejects it, instead of being
// concatenated into the statement. OrderByRaw stays the escape hatch for a custom direction.
func TestOrderBy_RejectsACustomDirection(t *testing.T) {
	q := inQuery(t).OrderBy("id", "asc; DROP TABLE users --").(*Query)

	_, _, err := q.dest(&[]map[string]any{}).addGlobalScopes().compileSelect()
	assert.ErrorIs(t, err, errors.QueryOrderDirectionInvalid)

	raw := inQuery(t).OrderByRaw("id DESC NULLS LAST").(*Query)

	sql, _ := compileInClause(t, raw)
	assert.Equal(t, `select * from "rel_users" order by id DESC NULLS LAST`, sql)
}
