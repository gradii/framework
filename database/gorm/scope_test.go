package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// TestCompileSelect_AppliesGlobalScopes pins that a model's global scopes reach the compiled
// statement. They used to be attached only to the statement GORM was given, which the compiled path
// does not read, so a scoped model would have queried every row.
func TestCompileSelect_AppliesGlobalScopes(t *testing.T) {
	query := compiledQuery(t, &ModelWithGlobalScopes{}, Conditions{}).addGlobalScopes()

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	// The scopes apply in the order their names sort in, so the statement reads the same each time.
	assert.Contains(t, statement, `"active" = ?`)
	assert.Contains(t, statement, `"verified" = ?`)
	assert.Equal(t, []any{true, true}, bindings)
}

// TestCompileSelect_AppliesUserScopes pins the same for a scope the caller queued outright.
func TestCompileSelect_AppliesUserScopes(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{1}}},
	})

	scoped, ok := query.Scopes(func(scoped contractsorm.Query) contractsorm.Query {
		return scoped.Where("name", "scoped")
	}).(*Query)
	require.True(t, ok)

	statement, bindings, err := scoped.compileSelect()
	require.NoError(t, err)

	// A scope starts from the conditions the query already carries, so its condition follows the
	// caller's, which is the order the statement reads in.
	assert.Contains(t, statement, `"id" = ?`)
	assert.Contains(t, statement, `"name" = ?`)
	assert.Equal(t, []any{1, "scoped"}, bindings)
}

// TestCompileSelect_SkipsExcludedGlobalScopes pins that WithoutGlobalScopes still decides which scopes
// apply, now that the compiled statement is the one that runs.
func TestCompileSelect_SkipsExcludedGlobalScopes(t *testing.T) {
	excluded, ok := compiledQuery(t, &ModelWithGlobalScopes{}, Conditions{}).
		WithoutGlobalScopes("active").(*Query)
	require.True(t, ok)

	statement, _, err := excluded.addGlobalScopes().compileSelect()
	require.NoError(t, err)

	assert.NotContains(t, statement, `"active" = ?`)
	assert.Contains(t, statement, `"verified" = ?`)
}

// TestCompileDelete_AppliesGlobalScopes pins that a model's global scopes are folded in before the
// missing-where guard, so a scoped delete is compiled rather than refused.
//
// The guard used to read the query's own conditions only, while the scope's where landed on a copy
// built inside newBuilder, so Model(&Scoped{}).Delete() returned errors.OrmMissingWhereClause even
// though the statement it would have run was constrained by the scope.
func TestCompileDelete_AppliesGlobalScopes(t *testing.T) {
	query := compiledQuery(t, &ModelWithGlobalScopes{}, Conditions{}).addGlobalScopes()

	statement, bindings, err := query.compileDelete()
	require.NoError(t, err)

	assert.Contains(t, statement, "delete from")
	assert.Contains(t, statement, `"active" = ?`)
	assert.Contains(t, statement, `"verified" = ?`)
	assert.Equal(t, []any{true, true}, bindings)
}

// TestCompileUpdate_AppliesGlobalScopes pins the same for an update, which is the other statement the
// guard refuses when nothing names the rows.
func TestCompileUpdate_AppliesGlobalScopes(t *testing.T) {
	query := compiledQuery(t, &ModelWithGlobalScopes{}, Conditions{}).addGlobalScopes()

	statement, bindings, err := query.compileUpdate(map[string]any{"name": "x"})
	require.NoError(t, err)

	assert.Contains(t, statement, "update")
	assert.Contains(t, statement, `"active" = ?`)
	assert.Contains(t, statement, `"verified" = ?`)
	assert.Equal(t, []any{"x", true, true}, bindings)
}

// TestCompileDelete_ScopeWithoutWhereStillRefused pins that the guard reads what the scopes produced,
// not merely that scopes were queued: a scope that constrains nothing does not make an unrestricted
// delete safe, so the statement is still refused.
func TestCompileDelete_ScopeWithoutWhereStillRefused(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).Scopes(func(scoped contractsorm.Query) contractsorm.Query {
		return scoped.OrderBy("id")
	}).(*Query)

	_, _, err := query.compileDelete()
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)
}
