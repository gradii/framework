package local_scopes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentLocalScopesTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named
// TestLocalScopes_<method without the test prefix>.
//
// The corpus's setUp only boots an in-memory sqlite connection, and its tests never touch the
// database: each builds $model->newQuery() plus a local scope — the dynamically-called ->active() /
// ->type('foo') — and pins toSql() alongside getBindings(). Laravel's local scopes map here to
// scope methods on the model that take and return a contractsorm.Query, applied through the
// builder's Scopes(...); each port executes its scoped query against the harness's real sqlite
// database so the pinned statement also runs, and the seeded rows prove the bound values.
//
// Every SQL expectation this file asserts was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, sqlite dialect, driving
// the corpus's own EloquentLocalScopesTestModel), never recalled from our own output:
//
//	active()               -> select * from "table" where "active" = ?          bindings [true]
//	type('foo')            -> select * from "table" where "type" = ?            bindings ["foo"]
//	active()->type('foo')  -> select * from "table" where "active" = ? and "type" = ?
//	                          bindings [true, "foo"]
//	whereNot('firstWhere', true)->orWhere('secondWhere', true)->active()
//	    -> select * from "table" where (not "firstWhere" = ? or "secondWhere" = ?) and "active" = ?
//	       bindings [true, true, true]
//	where('firstWhere', true)->orWhereNot('secondWhere', true)->active()
//	    -> select * from "table" where ("firstWhere" = ? or not "secondWhere" = ?) and "active" = ?
//	       bindings [true, true, true]
//
// The Go ORM exposes no bindings accessor (Laravel's getBindings()), so a binding value is asserted
// through the rows its statement returns rather than read back; the binding-inlined raw form is not
// asserted because our ToRawSql renders booleans as true/false where Laravel inlines 1/0.
//
// # How our ORM diverges from Laravel's
//
//   - hasNamedScope() is PHP method-existence introspection; in Go a local scope exists only as the
//     function value passed to Scopes(...).

// --- models ------------------------------------------------------------------

// localScopesTestModel ports EloquentLocalScopesTestModel. The corpus's scope methods become plain
// methods on the model that receive and return a contractsorm.Query; a scope is applied through
// Scopes(...), the Go ORM's entry point for locally-defined scopes.
type localScopesTestModel struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Active bool   `gorm:"column:active"`
	Type   string `gorm:"column:type"`

	// The two booleans the corpus's whereNot/orWhereNot chains compare against.
	FirstWhere  bool `gorm:"column:firstWhere"`
	SecondWhere bool `gorm:"column:secondWhere"`
}

func (localScopesTestModel) TableName() string { return "table" }

// ScopeActive ports EloquentLocalScopesTestModel::scopeActive($query).
func (localScopesTestModel) ScopeActive(query contractsorm.Query) contractsorm.Query {
	return query.Where("active", true)
}

// ScopeType ports EloquentLocalScopesTestModel::scopeType($query, $type). Go has no dynamic method
// call, so a parameterized scope is applied through a closure over its argument.
func (localScopesTestModel) ScopeType(query contractsorm.Query, value string) contractsorm.Query {
	return query.Where("type", value)
}

// --- schema and seeding ------------------------------------------------------

// localScopesSchema creates the "table" the corpus's model addresses. The corpus's setUp creates
// only the connection and its tests never query; the port adds this DDL so the pinned statements
// execute against the harness's sqlite database.
func localScopesSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query, `create table "table" (id integer primary key autoincrement, "active" integer not null default 0, "type" text not null default '', "firstWhere" integer not null default 0, "secondWhere" integer not null default 0)`)
}

// localScopesSeed inserts three rows separating every pinned predicate: active = true matches rows
// 1 and 2, type = 'foo' matches rows 1 and 3, and only row 1 matches both.
func localScopesSeed(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, row := range []localScopesTestModel{
		{Active: true, Type: "foo"},
		{Active: true, Type: "bar"},
		{Active: false, Type: "foo"},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&row))
	}
}

// localScopesIDs collects the ids of the returned rows for a set comparison.
func localScopesIDs(rows []localScopesTestModel) []uint64 {
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}

	return ids
}

// localScopesNestingSeed inserts four rows for the two scope-nesting tests: rows 1 and 2 satisfy
// both negated chains, row 3 satisfies only the firstWhere side (the whereNot form excludes it
// while the orWhereNot form keeps it), and row 4 is inactive.
func localScopesNestingSeed(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, row := range []localScopesTestModel{
		{Active: true, FirstWhere: true, SecondWhere: true},
		{Active: true, FirstWhere: false, SecondWhere: false},
		{Active: true, FirstWhere: true, SecondWhere: false},
		{Active: false, FirstWhere: false, SecondWhere: true},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&row))
	}
}

// --- ports of the corpus -----------------------------------------------------

func TestLocalScopes_CanCheckExistenceOfLocalScope(t *testing.T) {
	// Laravel: $model->hasNamedScope('active') and hasNamedScope('type') are true and
	// hasNamedScope('nonExistentLocalScope') is false — method_exists introspection over the PHP
	// scopeActive / scopeType methods.
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts hasNamedScope() method-existence introspection for 'active', 'type' and 'nonExistentLocalScope' — a compiled Go program cannot look a scope up by name, because a local scope exists only as the function value passed to Scopes(...)")
}

func TestLocalScopes_LocalScopeIsApplied(t *testing.T) {
	query := newRelationQuery(t)
	localScopesSchema(t, query)
	localScopesSeed(t, query)

	model := localScopesTestModel{}

	// Laravel: $model->newQuery()->active() pins
	//   select * from "table" where "active" = ?  (bindings [true], oracle).
	scoped := cloneRelationQuery(t, query).Model(&model).Scopes(localScopesTestModel{}.ScopeActive)
	assert.Equal(t, `select * from "table" where "active" = ?`, scoped.ToSql().Get(&model))

	// The true binding selects rows 1 and 2 — the seeded active = false row stays out.
	got := []localScopesTestModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&localScopesTestModel{}).Scopes(localScopesTestModel{}.ScopeActive).Get(&got))
	assert.ElementsMatch(t, []uint64{1, 2}, localScopesIDs(got))
}

func TestLocalScopes_DynamicLocalScopeIsApplied(t *testing.T) {
	query := newRelationQuery(t)
	localScopesSchema(t, query)
	localScopesSeed(t, query)

	model := localScopesTestModel{}
	typeScope := func(query contractsorm.Query) contractsorm.Query {
		return localScopesTestModel{}.ScopeType(query, "foo")
	}

	// Laravel: $model->newQuery()->type('foo') pins
	//   select * from "table" where "type" = ?  (bindings ["foo"], oracle).
	scoped := cloneRelationQuery(t, query).Model(&model).Scopes(typeScope)
	assert.Equal(t, `select * from "table" where "type" = ?`, scoped.ToSql().Get(&model))

	// The 'foo' binding selects rows 1 and 3 — the seeded 'bar' row stays out.
	got := []localScopesTestModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&localScopesTestModel{}).Scopes(typeScope).Get(&got))
	assert.ElementsMatch(t, []uint64{1, 3}, localScopesIDs(got))
}

func TestLocalScopes_LocalScopesCanChained(t *testing.T) {
	query := newRelationQuery(t)
	localScopesSchema(t, query)
	localScopesSeed(t, query)

	model := localScopesTestModel{}
	typeScope := func(query contractsorm.Query) contractsorm.Query {
		return localScopesTestModel{}.ScopeType(query, "foo")
	}

	// Laravel: $model->newQuery()->active()->type('foo') pins
	//   select * from "table" where "active" = ? and "type" = ?
	// (bindings [true, "foo"], oracle) — the scopes apply in chain order.
	scoped := cloneRelationQuery(t, query).Model(&model).Scopes(localScopesTestModel{}.ScopeActive, typeScope)
	assert.Equal(t, `select * from "table" where "active" = ? and "type" = ?`, scoped.ToSql().Get(&model))

	// Only row 1 is both active = true and type = 'foo'.
	got := []localScopesTestModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&localScopesTestModel{}).Scopes(localScopesTestModel{}.ScopeActive, typeScope).Get(&got))
	require.Len(t, got, 1)
	assert.Equal(t, uint64(1), got[0].ID)
	assert.True(t, got[0].Active)
	assert.Equal(t, "foo", got[0].Type)
}

func TestLocalScopes_LocalScopeNestingDoesntDoubleFirstWhereClauseNegation(t *testing.T) {
	query := newRelationQuery(t)
	localScopesSchema(t, query)
	localScopesNestingSeed(t, query)

	model := localScopesTestModel{}

	// Laravel: $model->newQuery()->whereNot('firstWhere', true)->orWhere('secondWhere', true)->active().
	// Oracle (sqlite, illuminate/database v12.55.1) pins
	//   select * from "table" where (not "firstWhere" = ? or "secondWhere" = ?) and "active" = ?
	// with bindings [true, true, true]: applying the active scope groups the pre-existing or-chain
	// through Eloquent's callScope / addNewWheresWithinGroup, and the group takes the first where's
	// "and not" minus its " not", so the negation is not doubled.
	scoped := cloneRelationQuery(t, query).Model(&model).WhereNot("firstWhere", true).OrWhere("secondWhere", true).Scopes(localScopesTestModel{}.ScopeActive)
	assert.Equal(t, `select * from "table" where (not "firstWhere" = ? or "secondWhere" = ?) and "active" = ?`, scoped.ToSql().Get(&model))

	// The read proves the group: rows 1 and 2 satisfy (not firstWhere or secondWhere) and are
	// active; row 3 (firstWhere only) and the inactive row 4 stay out.
	got := []localScopesTestModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&localScopesTestModel{}).WhereNot("firstWhere", true).OrWhere("secondWhere", true).Scopes(localScopesTestModel{}.ScopeActive).Get(&got))
	assert.ElementsMatch(t, []uint64{1, 2}, localScopesIDs(got))
}

func TestLocalScopes_LocalScopeNestingGroupsOrNotWhereClause(t *testing.T) {
	query := newRelationQuery(t)
	localScopesSchema(t, query)
	localScopesNestingSeed(t, query)

	model := localScopesTestModel{}

	// Laravel: $model->newQuery()->where('firstWhere', true)->orWhereNot('secondWhere', true)->active().
	// Oracle (sqlite, illuminate/database v12.55.1) pins
	//   select * from "table" where ("firstWhere" = ? or not "secondWhere" = ?) and "active" = ?
	// with bindings [true, true, true]: the "or not" connector makes the group, and the active
	// scope's where follows it.
	scoped := cloneRelationQuery(t, query).Model(&model).Where("firstWhere", true).OrWhereNot("secondWhere", true).Scopes(localScopesTestModel{}.ScopeActive)
	assert.Equal(t, `select * from "table" where ("firstWhere" = ? or not "secondWhere" = ?) and "active" = ?`, scoped.ToSql().Get(&model))

	// The read proves the group: rows 1, 2 and 3 satisfy (firstWhere or not secondWhere) and are
	// active; the inactive row 4 stays out.
	got := []localScopesTestModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&localScopesTestModel{}).Where("firstWhere", true).OrWhereNot("secondWhere", true).Scopes(localScopesTestModel{}.ScopeActive).Get(&got))
	assert.ElementsMatch(t, []uint64{1, 2, 3}, localScopesIDs(got))
}
