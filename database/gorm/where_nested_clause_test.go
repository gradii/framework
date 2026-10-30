package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The closure form of Where mirrors Eloquent's Builder::where() (Builder.php:352-365): the callback
// receives a nested query, its wheres become one parenthesized group through addNestedWhereQuery,
// and the eager loads it queued are merged into the parent (array_merge of getEagerLoads()), so a
// with(...) written inside the closure still loads. The whole branch runs when Where is called, the
// way Eloquent's does, not when the statement is compiled.
func TestWhere_ClosureNestsAndMergesEagerLoads(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).
		Where(func(nested contractsorm.Query) contractsorm.Query {
			return nested.Where("id", 1).With("Books")
		}).(*OrmBuilder)

	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where ("id" = ?)`, sql)
	assert.Equal(t, []any{1}, bindings)

	require.Len(t, query.conditions.eagerLoad, 1, "the closure's with(...) must survive the group")
	assert.Equal(t, "Books", query.conditions.eagerLoad[0].relation)
}

// A closure that only scopes the group without adding a where leaves no group behind, matching the
// count guard Eloquent's addNestedWhereQuery has.
func TestWhere_ClosureWithoutWheresLeavesNoGroup(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).
		Where(func(nested contractsorm.Query) contractsorm.Query { return nested.With("Books") }).(*OrmBuilder)

	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users"`, sql)
	assert.Empty(t, bindings)
	require.Len(t, query.conditions.eagerLoad, 1)
}

// The closure runs when Where is called — Eloquent's closure branch executes inline — so compiling
// the same query twice does not run it again.
func TestWhere_ClosureRunsAtCallTime(t *testing.T) {
	calls := 0
	query := newRelQueryWith(t, &relUser{}).Where(func(nested contractsorm.Query) contractsorm.Query {
		calls++

		return nested.Where("id", 1)
	}).(*OrmBuilder)
	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	require.Equal(t, 1, calls)

	_, _, err := query.compileSelect()
	require.NoError(t, err)
	_, _, err = query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, 1, calls)
}

// OrWhere with a closure hands the group the "or" connector, the way orWhere() calls where() with
// the boolean.
func TestOrWhere_ClosureGroupKeepsTheOrConnector(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).Where("id", 1).
		OrWhere(func(nested contractsorm.Query) contractsorm.Query {
			return nested.Where("name", "John")
		}).(*OrmBuilder)
	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where "id" = ? or ("name" = ?)`, sql)
	assert.Equal(t, []any{1, "John"}, bindings)
}
