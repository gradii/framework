package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins the SQL the ORM emits for the IN predicates. WhereIn / OrWhereIn / WhereNotIn /
// OrWhereNotIn must reach the compiler as a column plus a slice argument — the shape the relation
// loaders already use (see relatedChild) — so the compiler expands the list into one placeholder per
// value: `"name" in (?, ?)`.
//
// Before this, the four methods built a raw "name IN ?" fragment, which took the WhereRaw branch in
// applySingleWhere and bound the whole slice to that single placeholder. The compiled statement was
// therefore malformed ("... IN 'one'") and every IN predicate failed at the driver
// (sqlite3: "near \"?\"") and returned no rows. Laravel's whereIn renders the same `in (?, ?)`,
// verified against .claude/laravel-oracle/oracle.php.

// compileInClause renders q and returns the statement with its positional bindings. The destination
// is a map slice so the projection stays "select *" and the table comes from the query itself.
func compileInClause(t *testing.T, q *Query) (string, []any) {
	t.Helper()

	sql, bindings, err := q.dest(&[]map[string]any{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)

	return sql, bindings
}

// inQuery returns a query addressed at rel_users, the shape a caller has after Model/Table.
func inQuery(t *testing.T) *Query {
	t.Helper()

	return newRelQueryWith(t, &relUser{}).Table("rel_users").(*Query)
}

func TestWhereIn_ExpandsSliceIntoOneBindingPerValue(t *testing.T) {
	q := inQuery(t).WhereIn("name", []any{"one", "two"}).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "name" in (?, ?)`, sql)
	assert.Equal(t, []any{"one", "two"}, bindings)
}

func TestOrWhereIn_ExpandsSliceIntoOneBindingPerValue(t *testing.T) {
	q := inQuery(t).Where("name", "zero").OrWhereIn("name", []any{"one", "two"}).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "name" = ? or "name" in (?, ?)`, sql)
	assert.Equal(t, []any{"zero", "one", "two"}, bindings)
}

func TestWhereNotIn_ExpandsSliceIntoOneBindingPerValue(t *testing.T) {
	q := inQuery(t).WhereNotIn("name", []any{"one", "two"}).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "name" not in (?, ?)`, sql)
	assert.Equal(t, []any{"one", "two"}, bindings)
}

func TestOrWhereNotIn_ExpandsSliceIntoOneBindingPerValue(t *testing.T) {
	q := inQuery(t).Where("name", "zero").OrWhereNotIn("name", []any{"one"}).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "name" = ? or "name" not in (?)`, sql)
	assert.Equal(t, []any{"zero", "one"}, bindings)
}

// The sibling WhereIn / WhereNotIn routes already rendered an empty list as a constant rather than
// "in ()"; these pin them so all routes agree (see the WhereAny/WhereAll/WhereNone empty pins).
func TestWhereIn_EmptySliceMatchesNothing(t *testing.T) {
	q := inQuery(t).WhereIn("name", []any{}).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where 0 = 1`, sql)
	assert.Empty(t, bindings)
}

func TestWhereNotIn_EmptySliceMatchesEverything(t *testing.T) {
	q := inQuery(t).WhereNotIn("name", []any{}).(*Query)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where 1 = 1`, sql)
	assert.Empty(t, bindings)
}
