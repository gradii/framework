package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// statementScopes mounts the two scopes the pins in this file compile against — "active" then
// "verified", in that order — the way the model that used to declare them did. Scopes mount on the
// builder (ApplyScope) rather than on a model, so the pins keep asserting the same statements.
func statementScopes(query contractsorm.Query) *OrmBuilder {
	return query.
		ApplyScope("active", func(q contractsorm.Query) contractsorm.Query { return q.Where("active", true) }).
		ApplyScope("verified", func(q contractsorm.Query) contractsorm.Query { return q.Where("verified", true) }).(*OrmBuilder)
}

// TestCompileSelect_AppliesMountedScopes pins that the scopes a query carries reach the compiled
// statement. They used to be attached only to the statement GORM was given, which the compiled path
// does not read, so a scoped query would have read every row.
func TestCompileSelect_AppliesMountedScopes(t *testing.T) {
	query := statementScopes(compiledQuery(t, &scopedStatementModel{}, Conditions{}))

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	// The scopes apply in mount order, so the statement reads the same each time.
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

// TestCompileSelect_SkipsARemovedScope pins that a removal decides which scopes apply, now that the
// compiled statement is the one that runs.
func TestCompileSelect_SkipsARemovedScope(t *testing.T) {
	excluded, ok := statementScopes(compiledQuery(t, &scopedStatementModel{}, Conditions{})).
		RemoveScope("active").(*OrmBuilder)
	require.True(t, ok)

	statement, _, err := excluded.compileSelect()
	require.NoError(t, err)

	assert.NotContains(t, statement, `"active" = ?`)
	assert.Contains(t, statement, `"verified" = ?`)
}

// TestCompileDelete_AppliesMountedScopes pins that the scopes a query carries are folded in before
// the missing-where guard, so a scoped delete is compiled rather than refused.
//
// The guard used to read the query's own conditions only, while the scope's where landed on a copy
// built inside newBuilder, so a scoped Delete() returned errors.OrmMissingWhereClause even though the
// statement it would have run was constrained by the scope.
func TestCompileDelete_AppliesMountedScopes(t *testing.T) {
	query := statementScopes(compiledQuery(t, &scopedStatementModel{}, Conditions{}))

	statement, bindings, err := query.compileDelete()
	require.NoError(t, err)

	assert.Contains(t, statement, "delete from")
	assert.Contains(t, statement, `"active" = ?`)
	assert.Contains(t, statement, `"verified" = ?`)
	assert.Equal(t, []any{true, true}, bindings)
}

// TestCompileUpdate_AppliesMountedScopes pins the same for an update, which is the other statement
// the guard refuses when nothing names the rows.
func TestCompileUpdate_AppliesMountedScopes(t *testing.T) {
	query := statementScopes(compiledQuery(t, &scopedStatementModel{}, Conditions{}))

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

// orderedScopes mounts its scopes with the later name first, so the compiled where order shows
// whether the ORM kept the mount order or sorted the names.
type orderedScopeModel struct {
	ID uint
}

func (orderedScopeModel) TableName() string { return "model_with_ordered_global_scopes" }

func orderedScopes(query contractsorm.Query) *OrmBuilder {
	return query.
		ApplyScope("z_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("z_col", 1) }).
		ApplyScope("a_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("a_col", 2) }).(*OrmBuilder)
}

// TestCompileSelect_AppliesMountedScopesInMountOrder pins that scopes apply in the order they were
// mounted — Laravel's registration order — rather than in name order.
func TestCompileSelect_AppliesMountedScopesInMountOrder(t *testing.T) {
	query := orderedScopes(compiledQuery(t, &orderedScopeModel{}, Conditions{}))

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Contains(t, statement, `where "z_col" = ? and "a_col" = ?`)
	assert.Equal(t, []any{1, 2}, bindings)

	// Removal still names the scope by its identity.
	excluded, ok := orderedScopes(compiledQuery(t, &orderedScopeModel{}, Conditions{})).
		RemoveScope("z_scope").(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err = excluded.compileSelect()
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

// scopeOrderContrastModel is the table the deferred-orX pin compiles against: its table matches the
// oracle probe's model so the pinned statement byte-matches.
type scopeOrderContrastModel struct {
	ID uint
}

func (scopeOrderContrastModel) TableName() string { return "rel_users" }

// orXScope is the or-chain the deferred pin mounts: a mounted scope applies when the statement
// compiles (applyScopes), so it lands after every where the caller wrote — the contrast to the
// immediate Scopes() above.
func orXScope(query contractsorm.Query) contractsorm.Query {
	return query.Where("x", 1).OrWhere("y", 2)
}

// TestCompileSelect_MountedScopesStayDeferred pins the contrast the local-scope fix must keep: a
// mounted scope is registered the way withGlobalScope() registers it and applied when the statement
// compiles (applyScopes), so it lands after every where the caller wrote — the shape the oracle
// records for a deferred scope. Oracle (/tmp/scope_order_repin.php): where('c', 1) plus a registered
// orX global scope -> where "c" = ? and ("x" = ? or "y" = ?).
func TestCompileSelect_MountedScopesStayDeferred(t *testing.T) {
	query := compiledQuery(t, &scopeOrderContrastModel{}, Conditions{}).
		Where("c", 1).
		ApplyScope("or_x", orXScope).(*OrmBuilder)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "c" = ? and ("x" = ? or "y" = ?)`, statement)
	assert.Equal(t, []any{1, 1, 2}, bindings)
}

// withRelatedScope is a scope whose with() must reach the query the statement is compiled from: the
// read's eager-load pass runs from that query, not from a copy.
func withRelatedScope(query contractsorm.Query) contractsorm.Query { return query.With("related") }

// eagerLoadScopeModel is the table the scope-added eager-load pin compiles against.
type eagerLoadScopeModel struct {
	ID uint
}

func (eagerLoadScopeModel) TableName() string { return "model_with_eager_load_global_scope" }

// TestCompileSelect_FoldsScopeEagerLoadsBack pins that a scope's with() lands on the compiled query:
// Laravel's applyScopes returns the scoped builder, and get()/first() run their eager-load pass from
// it, so the scope-added loads must survive the compile.
func TestCompileSelect_FoldsScopeEagerLoadsBack(t *testing.T) {
	query := compiledQuery(t, &eagerLoadScopeModel{}, Conditions{}).
		ApplyScope("with_related", withRelatedScope).(*OrmBuilder)

	_, _, err := query.compileSelect()
	require.NoError(t, err)

	require.Len(t, query.conditions.eagerLoad, 1)
	assert.Equal(t, "related", query.conditions.eagerLoad[0].Relation)
}
