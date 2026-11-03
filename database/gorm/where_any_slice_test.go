package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file pins the WhereAll / WhereAny / WhereNone family. Each repeats one condition across
// several columns inside one nested group, and the whole family compiles through the typed builder
// methods, so a column name is wrapped by the grammar exactly as the sibling WhereIn entry point
// wraps it (`"name" in (?, ?)`).
//
// An IN (or NOT IN) whose value is a slice expands to one placeholder per element, per column, the
// same shape database/db's WhereAll / WhereAny / WhereNone compile: `("name" in (?, ?) or
// "email" in (?, ?))`. Before the family was typed, these entry points built a raw fragment with a
// literal "?", which bound the whole slice to that one placeholder; the compiled list was never
// expanded (`name IN 'one'`) and every driver rejected the statement (postgres "unused argument",
// mysql/sqlite a syntax error near "?").

func TestWhereAny_ExpandsInSliceIntoOneBindingPerValue(t *testing.T) {
	q := inQuery(t).WhereAny([]string{"name", "email"}, "IN", []any{"one", "two"}).(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where ("name" in (?, ?) or "email" in (?, ?))`, sql)
	assert.Equal(t, []any{"one", "two", "one", "two"}, bindings)
}

func TestWhereAll_ExpandsInSliceIntoOneBindingPerValue(t *testing.T) {
	q := inQuery(t).WhereAll([]string{"name", "email"}, "IN", []any{"one", "two"}).(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where ("name" in (?, ?) and "email" in (?, ?))`, sql)
	assert.Equal(t, []any{"one", "two", "one", "two"}, bindings)
}

func TestWhereNone_ExpandsInSliceIntoOneBindingPerValue(t *testing.T) {
	q := inQuery(t).WhereNone([]string{"name", "email"}, "IN", []any{"one", "two"}).(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where not ("name" in (?, ?) or "email" in (?, ?))`, sql)
	assert.Equal(t, []any{"one", "two", "one", "two"}, bindings)
}

func TestWhereAny_ScalarOperatorIsUnchanged(t *testing.T) {
	q := inQuery(t).Where("name", "zero").WhereAny([]string{"name", "email"}, "=", "x").(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where "name" = ? and ("name" = ? or "email" = ?)`, sql)
	assert.Equal(t, []any{"zero", "x", "x"}, bindings)
}

// An empty IN list has no placeholder to expand to: joining zero placeholders would emit the
// malformed `name IN ()` that every driver rejects. Each route keeps the neutral constant the
// compiler already gives the sibling WhereIn / WhereNotIn - "0 = 1" for an empty IN (matches
// nothing), "1 = 1" for an empty NOT IN (matches everything) - binding nothing, so one shape holds
// across every route. Laravel's whereIn renders the same; verified against
// .claude/laravel-oracle/oracle.php.
func TestWhereAny_EmptyInSliceMatchesNothing(t *testing.T) {
	q := inQuery(t).WhereAny([]string{"name"}, "IN", []any{}).(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where (0 = 1)`, sql)
	assert.Empty(t, bindings)
}

func TestWhereAll_EmptyInSliceMatchesNothing(t *testing.T) {
	q := inQuery(t).WhereAll([]string{"name"}, "IN", []any{}).(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where (0 = 1)`, sql)
	assert.Empty(t, bindings)
}

func TestWhereNone_EmptyInSliceMatchesEverything(t *testing.T) {
	q := inQuery(t).WhereNone([]string{"name"}, "IN", []any{}).(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where not (0 = 1)`, sql)
	assert.Empty(t, bindings)
}

func TestWhereAny_EmptyNotInSliceMatchesEverything(t *testing.T) {
	q := inQuery(t).WhereAny([]string{"name"}, "NOT IN", []any{}).(*OrmBuilder)

	sql, bindings := compileInClause(t, q)
	assert.Equal(t, `select * from "rel_users" where (1 = 1)`, sql)
	assert.Empty(t, bindings)
}
