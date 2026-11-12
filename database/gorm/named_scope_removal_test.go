package gorm

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
)

// The built-in soft-delete predicate is addressable by the name Laravel registers it under, so a
// caller drops exactly that scope while the model's user-defined global scopes stay applied —
// Laravel's withoutGlobalScope(SoftDeletingScope::class). Laravel keys a Scope instance by
// get_class (Concerns/HasGlobalScopes::addGlobalScope), which is why the name is the class's
// fully-qualified name; the port's contractsorm.SoftDeletingScope carries that exact key. This
// file pins the named-removal semantics on the compiled non-eager path; the eager and through
// paths are pinned in eager_soft_delete_test.go / eager_through_soft_delete_test.go.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe.php §A/§B):
// getGlobalScopes() reports ["Illuminate\\Database\\Eloquent\\SoftDeletingScope", "email_scope"];
// the default read compiles `... where "sr_users"."deleted_at" is null and "email" = ?`;
// withoutGlobalScope(SoftDeletingScope::class) compiles `... where "email" = ?` (predicate gone,
// the user scope kept); withoutGlobalScope('email_scope') keeps `"sr_users"."deleted_at" is null`;
// withoutGlobalScopes() compiles `select * from "sr_users"`; onlyTrashed() compiles `... where
// "sr_users"."deleted_at" is not null and "email" = ?`; and withoutGlobalScope(SoftDeletingScope)
// plus onlyTrashed() keeps the `is not null`.

// namedScopeSoftUser declares the framework's soft-delete field and one named user scope, so
// a removal must be shown to drop exactly one of them. The "nsu_scope" scope used to be declared
// GlobalScopes(); scopes mount on the builder with ApplyScope now, so the tests mount
// namedScopeUserScope under the same name themselves (namedScopeQuery, or the relation's OnQuery).
type namedScopeSoftUser struct {
	ID     uint
	UserID uint
	Name   string

	databaseorm.SoftDeletes
}

func (namedScopeSoftUser) TableName() string { return "nsu_users" }

// namedScopeUserScope is the "nsu_scope" user scope the removal pins mount on the builder.
func namedScopeUserScope(q contractsorm.Query) contractsorm.Query { return q.Where("name", "x") }

// namedScopeQuery is compiledQuery over namedScopeSoftUser with "nsu_scope" mounted on the builder
// — the mount the model used to declare — so every removal gate is exercised.
func namedScopeQuery(t *testing.T) *OrmBuilder {
	t.Helper()

	return compiledQuery(t, &namedScopeSoftUser{}, Conditions{}).ApplyScope("nsu_scope", namedScopeUserScope).(*OrmBuilder)
}

// namedScopeSQL compiles the model's read after folding in its global scopes, the way a normal read
// does (addGlobalScopes then compileSelect), so every removal gate is exercised.
func namedScopeSQL(t *testing.T, q *OrmBuilder) (string, []any) {
	t.Helper()

	statement, bindings, err := q.compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

// TestSoftDeleteNamedScope_RemovalByNameAndWildcard pins every removal shape against the built-in
// predicate and a user scope side by side.
func TestSoftDeleteNamedScope_RemovalByNameAndWildcard(t *testing.T) {
	t.Run("default keeps both", func(t *testing.T) {
		statement, _ := namedScopeSQL(t, namedScopeQuery(t))
		// Oracle /tmp/r14si/probe.out N1: SoftDeletingScope registers first, so it applies first.
		assert.Equal(t, `select * from "nsu_users" where "nsu_users"."deleted_at" is null and "name" = ?`, statement)
	})

	t.Run("removing the SoftDeletingScope name drops only the predicate", func(t *testing.T) {
		q := namedScopeQuery(t).
			WithoutGlobalScopes(contractsorm.SoftDeletingScope).(*OrmBuilder)

		statement, _ := namedScopeSQL(t, q)
		assert.Equal(t, `select * from "nsu_users" where "name" = ?`, statement)
	})

	t.Run("the literal Laravel key removes it too", func(t *testing.T) {
		q := namedScopeQuery(t).
			WithoutGlobalScopes("Illuminate\\Database\\Eloquent\\SoftDeletingScope").(*OrmBuilder)

		statement, _ := namedScopeSQL(t, q)
		assert.Equal(t, `select * from "nsu_users" where "name" = ?`, statement)
	})

	t.Run("removing a user scope keeps the predicate", func(t *testing.T) {
		q := namedScopeQuery(t).
			WithoutGlobalScopes("nsu_scope").(*OrmBuilder)

		statement, _ := namedScopeSQL(t, q)
		assert.Equal(t, `select * from "nsu_users" where "nsu_users"."deleted_at" is null`, statement)
	})

	t.Run("withoutGlobalScopes() drops both", func(t *testing.T) {
		q := namedScopeQuery(t).WithoutGlobalScopes().(*OrmBuilder)

		statement, _ := namedScopeSQL(t, q)
		assert.Equal(t, `select * from "nsu_users"`, statement)
	})

	t.Run("onlyTrashed renders is not null", func(t *testing.T) {
		q := namedScopeQuery(t).OnlyTrashed().(*OrmBuilder)

		statement, _ := namedScopeSQL(t, q)
		// Oracle /tmp/r14si/probe.out N2/N3: onlyTrashed writes its predicate at call time, before
		// the user scope applies.
		assert.Equal(t, `select * from "nsu_users" where "nsu_users"."deleted_at" is not null and "name" = ?`, statement)
	})

	t.Run("named removal plus onlyTrashed keeps is not null", func(t *testing.T) {
		q := namedScopeQuery(t).
			WithoutGlobalScopes(contractsorm.SoftDeletingScope).(*OrmBuilder).OnlyTrashed().(*OrmBuilder)

		statement, _ := namedScopeSQL(t, q)
		// Oracle /tmp/r14si/probe.out N2/N3: onlyTrashed writes its predicate at call time, before
		// the user scope applies.
		assert.Equal(t, `select * from "nsu_users" where "nsu_users"."deleted_at" is not null and "name" = ?`, statement)
	})
}

// TestSoftDeleteNamedScope_ConstantCarriesTheLaravelKey pins the two registry-name constants to the
// keys Laravel uses: the SoftDeletingScope class's FQCN (get_class of the registered instance) and
// the literal 'SoftDeletableHasManyThrough' performJoin registers. A drift here would silently
// unname the built-ins. Oracle (illuminate/database v12.55.1, real sqlite,
// /tmp/r12/scope_removal_probe.php §A + /tmp/r12/scope_removal_probe3.php §E).
func TestSoftDeleteNamedScope_ConstantCarriesTheLaravelKey(t *testing.T) {
	assert.Equal(t, "Illuminate\\Database\\Eloquent\\SoftDeletingScope", contractsorm.SoftDeletingScope)
	assert.Equal(t, "SoftDeletableHasManyThrough", contractsorm.SoftDeletableHasManyThrough)
}

// namedScopeEagerUser is the parent of the eager read; its related model carries BOTH a user scope
// and the soft-delete field, so a named removal must drop one and keep the other.
type namedScopeEagerUser struct {
	ID    uint
	Posts []*namedScopeSoftUser `gorm:"-"`
}

func (namedScopeEagerUser) TableName() string { return "nsu_users" }

func (namedScopeEagerUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &namedScopeSoftUser{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.ApplyScope("nsu_scope", namedScopeUserScope) }},
	}
}

// TestSoftDeleteNamedScope_EagerKeepsUserScopes pins the eager path's named removal: the relation's
// own mount survives (it reaches the eager child) and only the soft-delete predicate is dropped — the
// split the eager runner's binding gate keeps by skipping the related-model bind only for the
// all-scope "*", never for a single name. Oracle (illuminate/database v12.55.1, real sqlite,
// /tmp/r12/scope_removal_probe2.php §F9): the eager read drops `deleted_at is null`; §F4 shows a
// named related removal leaves the through predicate, and §E shows withTrashedParents keeps it —
// each removal reaches exactly its own scope.
func TestSoftDeleteNamedScope_EagerKeepsUserScopes(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &namedScopeEagerUser{})
	desc, err := relations.ResolveRelation(&namedScopeEagerUser{}, "Posts")
	require.NoError(t, err)

	parents := []reflect.Value{reflect.ValueOf(&namedScopeEagerUser{ID: 1}).Elem()}
	callback := func(query contractsorm.Query) contractsorm.Query {
		return query.WithoutGlobalScopes(contractsorm.SoftDeletingScope)
	}
	require.NoError(t, q.loadHasOneOrMany(parents, &namedScopeEagerUser{}, desc, eagerLoadEntry{Relation: "Posts", Callback: callback}, nil, true))

	recorded := recorder.last(t)
	assert.Equal(t, `select * from "nsu_users" where "nsu_users"."user_id" in (?) and "name" = ?`, recorded.sql)
	assert.Equal(t, driverBindings([]any{uint(1), "x"}), recorded.args)
}
