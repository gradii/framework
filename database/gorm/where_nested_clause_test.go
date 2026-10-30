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
// with(...) written inside the closure still loads. The merge is applied while the statement is
// compiled, which is the same object Find then runs the loads on.
func TestWhere_ClosureNestsAndMergesEagerLoads(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).
		Where(func(nested contractsorm.Query) contractsorm.Query {
			return nested.Where("id", 1).With("Books")
		}).(*Query)

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
		Where(func(nested contractsorm.Query) contractsorm.Query { return nested.With("Books") }).(*Query)

	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users"`, sql)
	assert.Empty(t, bindings)
	require.Len(t, query.conditions.eagerLoad, 1)
}
