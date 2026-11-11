package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
)

// This file pins the builder-local scope surface: ApplyScope mounts a named scope on one query and
// RemoveScope removes it — Laravel's withGlobalScope($name, $scope) / withoutGlobalScope($name) on
// the builder rather than on the model. The scope belongs to the query, runs when a statement is
// prepared from it, and is copied by Clone(); nothing here reaches a query built elsewhere, which is
// the difference from the model-declared global scopes the same machinery used to serve.
//
// The model-declared form is pinned next door (scope_test.go, named_scope_removal_test.go); the two
// share the application step (withScopesApplied) and the removal gate (scopeRemoved), so the
// grouping, ordering and removal semantics below are the same semantics those files pin.
//
// Oracle (illuminate/database v12.55.1, real sqlite,
// /Users/dog/.claude/jobs/013c27a0/tmp/phaseb/builder_scope_probe.php -> builder_scope_probe.out):
// withGlobalScope on the builder mounts in call order and re-mounting a name replaces its callback in
// place (A: `where "z_col" = ? and "b_col" = ?`); withoutGlobalScope drops the entry so a later mount
// appends last (B: `where "b_col" = ? and "z_col" = ?`); a scope removing a later one makes it not run
// (C: `where "a_col" = ?`); a removal leaves a clause a scope already wrote (D: onlyTrashed then
// withoutGlobalScope(SoftDeletingScope::class) keeps `"deleted_at" is not null`); an or-chain groups
// as its own nested where (E); and removing a name nothing registered is a no-op that the later mount
// still applies (G).

// appliedScopeUser is a soft-delete model that declares no scopes of its own: the built-in
// soft-delete predicate is then the only thing that constrains a query on it besides what the
// builder mounts.
type appliedScopeUser struct {
	ID   uint
	Name string

	databaseorm.SoftDeletes
}

func (appliedScopeUser) TableName() string { return "asu_users" }

// appliedScopeSQL compiles the read a caller's query performs, which is where the mounted scopes
// land: compileSelect goes through newBuilder, and newBuilder applies them on the statement's copy.
func appliedScopeSQL(t *testing.T, q *OrmBuilder) (string, []any) {
	t.Helper()

	statement, bindings, err := q.compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

// TestApplyScope_MountsInOrderAndAppliesOnCompile pins the mount order as the application order, the
// way a model's scopes applied in registration order, and pins that the receiver keeps its mounts
// across compiles: the statement is built from a copy, so nothing is consumed.
func TestApplyScope_MountsInOrderAndAppliesOnCompile(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).
		ApplyScope("first_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("a_col", 1) }).
		ApplyScope("second_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("b_col", 2) }).(*OrmBuilder)

	statement, bindings := appliedScopeSQL(t, query)
	assert.Contains(t, statement, `where "a_col" = ? and "b_col" = ?`)
	assert.Equal(t, []any{1, 2}, bindings)

	// A second compile reads the same statement: the mounts stay on the query.
	again, bindings := appliedScopeSQL(t, query)
	assert.Equal(t, statement, again)
	assert.Equal(t, []any{1, 2}, bindings)
}

// TestApplyScope_RemountReplacesInPlace pins the identity rule: the name is the scope's key, so
// mounting it again replaces the callback and keeps the position the first mount gave it.
func TestApplyScope_RemountReplacesInPlace(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).
		ApplyScope("first_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("a_col", 1) }).
		ApplyScope("second_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("b_col", 2) }).
		ApplyScope("first_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("z_col", 9) }).(*OrmBuilder)

	statement, bindings := appliedScopeSQL(t, query)
	assert.Contains(t, statement, `where "z_col" = ? and "b_col" = ?`)
	assert.NotContains(t, statement, `"a_col"`)
	assert.Equal(t, []any{9, 2}, bindings)
}

// TestApplyScope_AfterRemoveScopeIsAFreshMount pins the other half of the identity rule: RemoveScope
// drops the entry, so mounting the name again appends it at the end — a fresh mount, not a
// resurrection of the old position — and clears the removal it recorded.
func TestApplyScope_AfterRemoveScopeIsAFreshMount(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).
		ApplyScope("first_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("a_col", 1) }).
		ApplyScope("second_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("b_col", 2) }).
		RemoveScope("first_scope").
		ApplyScope("first_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("z_col", 9) }).(*OrmBuilder)

	statement, bindings := appliedScopeSQL(t, query)
	assert.Contains(t, statement, `where "b_col" = ? and "z_col" = ?`)
	assert.Equal(t, []any{2, 9}, bindings)
}

// TestRemoveScope_DropsTheBuiltInSoftDeleteScope pins that the removal reaches the predicate the
// framework mounts by itself: the soft-delete scope is mounted when the statement is prepared, after
// the caller's removal was recorded, so the removal is what governs it — the remove-before-resolve
// rule, expressed through the name Laravel registers the scope under.
func TestRemoveScope_DropsTheBuiltInSoftDeleteScope(t *testing.T) {
	defaultStatement, _ := appliedScopeSQL(t, compiledQuery(t, &appliedScopeUser{}, Conditions{}))
	assert.Contains(t, defaultStatement, `"asu_users"."deleted_at" is null`)

	removed, ok := compiledQuery(t, &appliedScopeUser{}, Conditions{}).
		RemoveScope(contractsorm.SoftDeletingScope).(*OrmBuilder)
	require.True(t, ok)

	statement, _ := appliedScopeSQL(t, removed)
	assert.Equal(t, `select * from "asu_users"`, statement)
}

// TestRemoveScope_GovernsAScopeMountedLater pins that the removal is recorded even when nothing is
// mounted under the name yet: a model scope queued afterwards — the mount a statement's preparation
// performs — is skipped, the way withoutGlobalScope unsets a registration that arrives later.
func TestRemoveScope_GovernsAScopeMountedLater(t *testing.T) {
	query, ok := compiledQuery(t, &namedScopeSoftUser{}, Conditions{}).RemoveScope("nsu_scope").(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.addGlobalScopes().compileSelect()
	require.NoError(t, err)

	assert.NotContains(t, statement, `"name" = ?`)
	assert.Contains(t, statement, `"nsu_users"."deleted_at" is null`)
	assert.Empty(t, bindings)
}

// TestRemoveScope_LeavesClausesAlreadyWritten pins that a removal is not retroactive: OnlyTrashed
// writes its "is not null" at the call position, and removing the soft-delete scope afterwards
// neither drops that clause nor brings the default predicate back.
func TestRemoveScope_LeavesClausesAlreadyWritten(t *testing.T) {
	query, ok := compiledQuery(t, &appliedScopeUser{}, Conditions{}).
		OnlyTrashed().RemoveScope(contractsorm.SoftDeletingScope).(*OrmBuilder)
	require.True(t, ok)

	statement, _ := appliedScopeSQL(t, query)
	assert.Contains(t, statement, `"asu_users"."deleted_at" is not null`)
	assert.NotContains(t, statement, `"asu_users"."deleted_at" is null`)
}

// TestApplyScope_AScopeThatRemovesALaterScopeSkipsIt pins what happens when a scope removes another
// one while the statement is being prepared: the removal decides that the later scope does not run,
// and the clauses the earlier scope already wrote stay.
func TestApplyScope_AScopeThatRemovesALaterScopeSkipsIt(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).
		ApplyScope("first_scope", func(q contractsorm.Query) contractsorm.Query {
			return q.Where("a_col", 1).RemoveScope("second_scope")
		}).
		ApplyScope("second_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("b_col", 2) }).(*OrmBuilder)

	statement, bindings := appliedScopeSQL(t, query)
	assert.Contains(t, statement, `where "a_col" = ?`)
	assert.NotContains(t, statement, `"b_col"`)
	assert.Equal(t, []any{1}, bindings)
}

// TestApplyScope_BelongsToTheBuilder pins ownership: Clone() copies the mounts and the copy's own
// mounts never reach the original, while a query built from the same connection — what
// Orm.Query() hands a caller — starts with none.
func TestApplyScope_BelongsToTheBuilder(t *testing.T) {
	original := compiledQuery(t, &relUser{}, Conditions{}).
		ApplyScope("first_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("a_col", 1) }).(*OrmBuilder)

	cloned, ok := original.Clone().(*OrmBuilder)
	require.True(t, ok)
	cloned = cloned.ApplyScope("second_scope", func(q contractsorm.Query) contractsorm.Query { return q.Where("b_col", 2) }).(*OrmBuilder)

	statement, bindings := appliedScopeSQL(t, original)
	assert.Contains(t, statement, `where "a_col" = ?`)
	assert.NotContains(t, statement, `"b_col"`)
	assert.Equal(t, []any{1}, bindings)

	statement, bindings = appliedScopeSQL(t, cloned)
	assert.Contains(t, statement, `where "a_col" = ? and "b_col" = ?`)
	assert.Equal(t, []any{1, 2}, bindings)

	statement, bindings = appliedScopeSQL(t, compiledQuery(t, &relUser{}, Conditions{}))
	assert.NotContains(t, statement, `"a_col"`)
	assert.Empty(t, bindings)
}

// TestApplyScope_NilMountsNothing pins the degenerate call: a nil callback has nothing to run, so it
// mounts nothing and records no removal.
func TestApplyScope_NilMountsNothing(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).ApplyScope("noop_scope", nil).(*OrmBuilder)

	statement, _ := appliedScopeSQL(t, query)
	assert.Equal(t, `select * from "rel_users"`, statement)
}

// TestApplyScope_GroupsAnOrChain pins Eloquent's callScope grouping for a mounted scope: the
// scope's or-chain becomes its own nested where, and the chain it was appended to becomes one too —
// the grouping a model's scope gets, since both run through applyScope.
func TestApplyScope_GroupsAnOrChain(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).
		Where("foo", "foo").OrWhere("bar", "bar").
		ApplyScope("or_scope", func(q contractsorm.Query) contractsorm.Query {
			return q.Where("approved", 1).OrWhere("should_approve", 0)
		}).(*OrmBuilder)

	statement, bindings := appliedScopeSQL(t, query)
	assert.Contains(t, statement, `where ("foo" = ? or "bar" = ?) and ("approved" = ? or "should_approve" = ?)`)
	assert.Equal(t, []any{"foo", "bar", 1, 0}, bindings)
}

// TestApplyScope_ErrorReachesTheStatement pins that a clause the scope could not render is reported
// by the statement rather than swallowed, the way a model scope's is.
func TestApplyScope_ErrorReachesTheStatement(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).
		ApplyScope("broken_scope", func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Nope", func(q contractsorm.Query) contractsorm.Query { return q })
		}).(*OrmBuilder)

	_, _, err := query.compileSelect()
	assert.ErrorIs(t, err, errors.OrmRelationNotFound)
}

// TestApplyScope_ReplacesTheBuiltInSoftDeleteScope pins the other edge of the built-in name: a mount
// under it takes the place of the predicate the framework would have mounted, rather than stacking
// with it — Laravel's withGlobalScope(SoftDeletingScope::class, ...) replaces the model's
// registration under the same key.
func TestApplyScope_ReplacesTheBuiltInSoftDeleteScope(t *testing.T) {
	query := compiledQuery(t, &appliedScopeUser{}, Conditions{}).
		ApplyScope(contractsorm.SoftDeletingScope, func(q contractsorm.Query) contractsorm.Query {
			return q.Where("name", "mine")
		}).(*OrmBuilder)

	statement, bindings := appliedScopeSQL(t, query)
	assert.Equal(t, `select * from "asu_users" where "name" = ?`, statement)
	assert.Equal(t, []any{"mine"}, bindings)
}
