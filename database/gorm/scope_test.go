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

	// The scopes apply in declaration order — the order the model's GlobalScopes() lists them — so
	// the statement reads the same each time.
	assert.Contains(t, statement, `"active" = ?`)
	assert.Contains(t, statement, `"verified" = ?`)
	assert.Equal(t, []any{true, true}, bindings)
}

// TestCompileSelect_AppliesUserScopes pins the same for a scope the caller passes to Scopes(): the
// scope is applied to the query when Scopes() is called — Eloquent's Builder::scopes — so its
// condition follows the caller's.
func TestCompileSelect_AppliesUserScopes(t *testing.T) {
	query := compiledQueryWhere(t, &relUser{}, []contractsdriver.Where{{Query: "id", Args: []any{1}}}...)

	scoped, ok := query.Scopes(func(scoped contractsorm.Query) contractsorm.Query {
		return scoped.Where("name", "scoped")
	}).(*OrmBuilder)
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
		WithoutGlobalScopes("active").(*OrmBuilder)
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
// not merely that a scope ran: a scope that constrains nothing does not make an unrestricted delete
// safe, so the statement is still refused.
func TestCompileDelete_ScopeWithoutWhereStillRefused(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).Scopes(func(scoped contractsorm.Query) contractsorm.Query {
		return scoped.OrderBy("id")
	}).(*OrmBuilder)

	_, _, err := query.compileDelete()
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)
}

// modelWithOrderedGlobalScopes declares its scopes with the later name first, so the compiled where
// order shows whether the ORM kept the declaration order or sorted the names.
type modelWithOrderedGlobalScopes struct {
	ID uint
}

func (modelWithOrderedGlobalScopes) TableName() string { return "model_with_ordered_global_scopes" }

func (modelWithOrderedGlobalScopes) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "z_scope", Scope: func(query contractsorm.Query) contractsorm.Query { return query.Where("z_col", 1) }},
		{Name: "a_scope", Scope: func(query contractsorm.Query) contractsorm.Query { return query.Where("a_col", 2) }},
	}
}

// TestCompileSelect_AppliesGlobalScopesInDeclarationOrder pins that a model's scopes apply in the
// order GlobalScopes() declares them — Laravel's registration order — rather than in name order.
func TestCompileSelect_AppliesGlobalScopesInDeclarationOrder(t *testing.T) {
	query := compiledQuery(t, &modelWithOrderedGlobalScopes{}, Conditions{}).addGlobalScopes()

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Contains(t, statement, `where "z_col" = ? and "a_col" = ?`)
	assert.Equal(t, []any{1, 2}, bindings)

	// Removal still names the scope by its identity.
	excluded, ok := compiledQuery(t, &modelWithOrderedGlobalScopes{}, Conditions{}).
		WithoutGlobalScopes("z_scope").(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err = excluded.addGlobalScopes().compileSelect()
	require.NoError(t, err)

	assert.Contains(t, statement, `where "a_col" = ?`)
	assert.NotContains(t, statement, `"z_col"`)
	assert.Equal(t, []any{2}, bindings)
}

// TestCompileSelect_GroupsAScopesOrChain pins Eloquent's callScope grouping: a scope's or-chain is
// rebuilt as its own nested where, and the chain it was appended to becomes one too.
func TestCompileSelect_GroupsAScopesOrChain(t *testing.T) {
	scoped, ok := compiledQuery(t, &relUser{}, Conditions{}).
		Where("foo", "foo").OrWhere("bar", "bar").
		Scopes(func(query contractsorm.Query) contractsorm.Query {
			return query.Where("approved", 1).OrWhere("should_approve", 0)
		}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := scoped.compileSelect()
	require.NoError(t, err)

	assert.Contains(t, statement, `where ("foo" = ? or "bar" = ?) and ("approved" = ? or "should_approve" = ?)`)
	assert.Equal(t, []any{"foo", "bar", 1, 0}, bindings)
}

// TestCompileSelect_KeepsALeadingOrBoolean pins that a scope whose first where is an orWhere keeps
// the "or" as its group's connector instead of flattening into the surrounding clauses.
func TestCompileSelect_KeepsALeadingOrBoolean(t *testing.T) {
	scoped, ok := compiledQuery(t, &relUser{}, Conditions{}).
		Where("foo", "foo").OrWhere("bar", "bar").
		Scopes(func(query contractsorm.Query) contractsorm.Query {
			return query.OrWhere("approved", 1).OrWhere("should_approve", 0)
		}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := scoped.compileSelect()
	require.NoError(t, err)

	assert.Contains(t, statement, `where ("foo" = ? or "bar" = ?) or ("approved" = ? or "should_approve" = ?)`)
	assert.Equal(t, []any{"foo", "bar", 1, 0}, bindings)
}

// TestCompileSelect_ScopesApplyImmediately pins Laravel's local-scope immediacy: Builder::scopes()
// runs each scope through callScope at call time, so a where chained after the call lands after the
// scope's clauses. Having a scope's clauses land last is Laravel's deferred withGlobalScope shape,
// pinned separately below. Oracle (illuminate/database v12.55.1, /tmp/scope_order_repin.php):
//
//	->orX()->where('c', 1)                  -> where ("x" = ? or "y" = ?) and "c" = ?       [1,2,1]
//	->orX()->where('c', 1)->orWhere('d', 2) -> where ("x" = ? or "y" = ?) and "c" = ? or "d" = ? [1,2,1,2]
//	->where('c', 1)->orX()                  -> where "c" = ? and ("x" = ? or "y" = ?)       [1,1,2]
func TestCompileSelect_ScopesApplyImmediately(t *testing.T) {
	orX := func(scoped contractsorm.Query) contractsorm.Query {
		return scoped.Where("x", 1).OrWhere("y", 2)
	}

	scoped, ok := compiledQuery(t, &relUser{}, Conditions{}).Scopes(orX).Where("c", 1).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := scoped.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where ("x" = ? or "y" = ?) and "c" = ?`, statement)
	assert.Equal(t, []any{1, 2, 1}, bindings)

	scoped, ok = compiledQuery(t, &relUser{}, Conditions{}).Scopes(orX).Where("c", 1).OrWhere("d", 2).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err = scoped.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where ("x" = ? or "y" = ?) and "c" = ? or "d" = ?`, statement)
	assert.Equal(t, []any{1, 2, 1, 2}, bindings)

	scoped, ok = compiledQuery(t, &relUser{}, Conditions{}).Where("c", 1).Scopes(orX).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err = scoped.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "c" = ? and ("x" = ? or "y" = ?)`, statement)
	assert.Equal(t, []any{1, 1, 2}, bindings)
}

// TestCompileSelect_ScopesLeadingOrAppliesImmediately pins a scope whose first where is an orWhere
// when the scope runs at call time: the group keeps the "or" as its boolean and is the statement's
// first clause, so a where chained afterwards follows it. Oracle (/tmp/scope_order_repin.php):
// ->orApproved()->where('c', 1) -> where ("approved" = ? or "should_approve" = ?) and "c" = ?.
func TestCompileSelect_ScopesLeadingOrAppliesImmediately(t *testing.T) {
	orApproved := func(scoped contractsorm.Query) contractsorm.Query {
		return scoped.OrWhere("approved", 1).OrWhere("should_approve", 0)
	}

	scoped, ok := compiledQuery(t, &relUser{}, Conditions{}).Scopes(orApproved).Where("c", 1).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := scoped.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where ("approved" = ? or "should_approve" = ?) and "c" = ?`, statement)
	assert.Equal(t, []any{1, 0, 1}, bindings)
}

// scopeOrderContrastModel carries a model global scope whose or-chain must stay deferred — the
// contrast to the immediate Scopes() above. Its table matches the oracle probe's model so the
// pinned statement byte-matches.
type scopeOrderContrastModel struct {
	ID uint
}

func (scopeOrderContrastModel) TableName() string { return "rel_users" }

func (scopeOrderContrastModel) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "or_x", Scope: func(query contractsorm.Query) contractsorm.Query { return query.Where("x", 1).OrWhere("y", 2) }},
	}
}

// TestCompileSelect_GlobalScopesStayDeferred pins the contrast the local-scope fix must keep: a
// model's global scope is registered the way withGlobalScope() registers it and applied when the
// statement compiles (applyScopes), so it lands after every where the caller wrote — the shape the
// oracle records for a deferred scope. Oracle (/tmp/scope_order_repin.php): where('c', 1) plus a
// registered orX global scope -> where "c" = ? and ("x" = ? or "y" = ?).
func TestCompileSelect_GlobalScopesStayDeferred(t *testing.T) {
	query := compiledQuery(t, &scopeOrderContrastModel{}, Conditions{}).Where("c", 1).(*OrmBuilder).addGlobalScopes()

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "c" = ? and ("x" = ? or "y" = ?)`, statement)
	assert.Equal(t, []any{1, 1, 2}, bindings)
}

// modelWithEagerLoadGlobalScope declares a global scope whose with() must reach the query the
// statement is compiled from: the read's eager-load pass runs from that query, not from a copy.
type modelWithEagerLoadGlobalScope struct {
	ID uint
}

func (modelWithEagerLoadGlobalScope) TableName() string { return "model_with_eager_load_global_scope" }

func (modelWithEagerLoadGlobalScope) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "with_related", Scope: func(query contractsorm.Query) contractsorm.Query { return query.With("related") }},
	}
}

// TestCompileSelect_FoldsScopeEagerLoadsBack pins that a scope's with() lands on the compiled query:
// Laravel's applyScopes returns the scoped builder, and get()/first() run their eager-load pass from
// it, so the scope-added loads must survive the compile.
func TestCompileSelect_FoldsScopeEagerLoadsBack(t *testing.T) {
	query := compiledQuery(t, &modelWithEagerLoadGlobalScope{}, Conditions{}).addGlobalScopes()

	_, _, err := query.compileSelect()
	require.NoError(t, err)

	require.Len(t, query.conditions.eagerLoad, 1)
	assert.Equal(t, "related", query.conditions.eagerLoad[0].Relation)
}
