package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
)

// A related model's USER-DEFINED global scopes must reach every eager read, exactly as Laravel's do:
// Relation::noConstraints builds the eager child from the related model's newQuery(), whose
// registered global scopes applyScopes() folds in when the read runs (Builder::eagerLoadRelation ->
// Relation::getEager -> get()). The compiled eager runners (runRelatedQueryCompiled /
// runMany2ManyQueryCompiled) bind the related model and queue its scopes through addGlobalScopes —
// the same path the non-eager relation read (Related(...).Get) already uses — gated by the query's
// own removals (WithoutGlobalScopes()/WithoutGlobalScope(name)).
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r10/global_scope_eager_probe.php):
//   E1  User::with('posts')  -> `select * from "gs_posts" where "gs_posts"."user_id" in (1, 2) and
//       "visible" = ? and "archived_at" is null` (binds [1]) — the scopes land after the eager IN
//       constraint, in declaration order.
//   E8  belongsTo eager       -> `... where "gs_groups"."id" in (1) and "visible" = ?`.
//   E9  m2m eager             -> the joined read appends `and "visible" = ?` after the pivot IN.
//   E11 through eager         -> the related child appends `and "visible" = ?`.
//   E3/E5 withoutGlobalScopes (callback and relation-declared) -> the scopes are gone.
//   E4/E6 withoutGlobalScope('visible_scope') -> only `"archived_at" is null` survives.
// These pins drive the loaders directly, so the assertion is on the statement the loader built and
// ran rather than on a child the test rebuilt by hand.

// eagerScopePost is the related model the hasMany / through eager reads point at: it declares the
// two user global scopes the oracle probe mirrors, in the order Laravel's booted() registers them.
// It carries no soft-delete field, so only the user scopes reach the statement.
type eagerScopePost struct {
	ID         uint
	UserID     uint
	Title      string
	Visible    int
	ArchivedAt *string
}

func (eagerScopePost) TableName() string { return "eager_scope_posts" }

func (eagerScopePost) GlobalScopes() []contractsorm.GlobalScope {
	return eagerScopeScopes()
}

// eagerScopeRole is the related model the many-to-many eager read points at, with the same scopes.
type eagerScopeRole struct {
	ID         uint
	Name       string
	Visible    int
	ArchivedAt *string
}

func (eagerScopeRole) TableName() string { return "eager_scope_roles" }

func (eagerScopeRole) GlobalScopes() []contractsorm.GlobalScope {
	return eagerScopeScopes()
}

// eagerScopeAuthor is the related model the belongsTo eager read points at, with the same scopes.
type eagerScopeAuthor struct {
	ID         uint
	Name       string
	Visible    int
	ArchivedAt *string
}

func (eagerScopeAuthor) TableName() string { return "eager_scope_authors" }

func (eagerScopeAuthor) GlobalScopes() []contractsorm.GlobalScope {
	return eagerScopeScopes()
}

// eagerScopeTPost is the related model the through eager read points at, with the same scopes.
type eagerScopeTPost struct {
	ID               uint
	EagerScopeUserID uint `gorm:"column:eager_scope_user_id"`
	Title            string
	Visible          int
	ArchivedAt       *string
}

func (eagerScopeTPost) TableName() string { return "eager_scope_tposts" }

func (eagerScopeTPost) GlobalScopes() []contractsorm.GlobalScope {
	return eagerScopeScopes()
}

// eagerScopeScopes is the pair every scoped fixture declares: a plain where ("visible_scope") then an
// is-null predicate ("archived_scope"), in declaration order.
func eagerScopeScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "visible_scope", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("visible", 1) }},
		{Name: "archived_scope", Scope: func(q contractsorm.Query) contractsorm.Query { return q.WhereNull("archived_at") }},
	}
}

// eagerScopeUser is the parent of the has-many and many-to-many eager reads. PostsPlain / PostsNoVisible
// declare the scope removal on the relation itself (Laravel's relation-level withoutGlobalScopes()).
type eagerScopeUser struct {
	ID             uint
	Name           string
	Posts          []*eagerScopePost `gorm:"-"`
	PostsPlain     []*eagerScopePost `gorm:"-"`
	PostsNoVisible []*eagerScopePost `gorm:"-"`
	Roles          []*eagerScopeRole `gorm:"-"`
}

func (eagerScopeUser) TableName() string { return "eager_scope_users" }

func (eagerScopeUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &eagerScopePost{}, ForeignKey: "user_id"},
		"PostsPlain": contractsorm.HasMany{Related: &eagerScopePost{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes() }},
		"PostsNoVisible": contractsorm.HasMany{Related: &eagerScopePost{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes("visible_scope") }},
		"Roles": contractsorm.Many2Many{Related: &eagerScopeRole{}, Table: "eager_scope_role_user"},
	}
}

// eagerScopeBook is the parent of the belongsTo eager read.
type eagerScopeBook struct {
	ID       uint
	AuthorID uint
	Author   *eagerScopeAuthor `gorm:"-"`
}

func (eagerScopeBook) TableName() string { return "eager_scope_books" }

func (eagerScopeBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &eagerScopeAuthor{}, ForeignKey: "author_id"},
	}
}

// eagerScopeCountry is the parent of the through eager read.
type eagerScopeCountry struct {
	ID    uint
	Name  string
	Posts []*eagerScopeTPost `gorm:"-"`
}

func (eagerScopeCountry) TableName() string { return "eager_scope_countries" }

func (eagerScopeCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related:   &eagerScopeTPost{},
			Through:   &eagerScopeTUser{},
			FirstKey:  "eager_scope_country_id",
			SecondKey: "eager_scope_user_id",
		},
	}
}

type eagerScopeTUser struct {
	ID                  uint
	EagerScopeCountryID uint `gorm:"column:eager_scope_country_id"`
}

func (eagerScopeTUser) TableName() string { return "eager_scope_tusers" }

// eagerScopeParents returns two parent values carrying the keys the loaders read.
func eagerScopeParents() []reflect.Value {
	return []reflect.Value{
		reflect.ValueOf(&eagerScopeUser{ID: 1}).Elem(),
		reflect.ValueOf(&eagerScopeUser{ID: 2}).Elem(),
	}
}

// TestEagerGlobalScopes_HasMany pins the has-many eager child: the related model's scopes are
// appended after the eager IN constraint, in declaration order (oracle E1).
func TestEagerGlobalScopes_HasMany(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &eagerScopeUser{})
	desc, err := relations.ResolveRelation(&eagerScopeUser{}, "Posts")
	require.NoError(t, err)

	require.NoError(t, q.loadHasOneOrMany(eagerScopeParents(), &eagerScopeUser{}, desc, eagerLoadEntry{Relation: "Posts"}, nil, true))

	recorded := recorder.last(t)
	assert.Equal(t, `select * from "eager_scope_posts" where "eager_scope_posts"."user_id" in (?, ?) and "visible" = ? and "archived_at" is null`, recorded.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2), 1}), recorded.args)
}

// TestEagerGlobalScopes_BelongsTo pins the belongsTo eager child (oracle E8).
func TestEagerGlobalScopes_BelongsTo(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &eagerScopeBook{})
	desc, err := relations.ResolveRelation(&eagerScopeBook{}, "Author")
	require.NoError(t, err)

	parents := []reflect.Value{reflect.ValueOf(&eagerScopeBook{ID: 1, AuthorID: 7}).Elem()}
	require.NoError(t, q.loadBelongsTo(parents, &eagerScopeBook{}, desc, eagerLoadEntry{Relation: "Author"}, nil))

	recorded := recorder.last(t)
	assert.Equal(t, `select * from "eager_scope_authors" where "eager_scope_authors"."id" in (?) and "visible" = ? and "archived_at" is null`, recorded.sql)
	assert.Equal(t, driverBindings([]any{uint(7), 1}), recorded.args)
}

// TestEagerGlobalScopes_Many2Many pins the many-to-many eager joined read (oracle E9).
func TestEagerGlobalScopes_Many2Many(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &eagerScopeUser{})
	desc, err := relations.ResolveRelation(&eagerScopeUser{}, "Roles")
	require.NoError(t, err)

	recorder.returnRows(`from "eager_scope_roles"`, []string{"id", "name", "pivot_eager_scope_user_id"},
		[]driver.Value{int64(9), "admin", int64(1)})

	require.NoError(t, q.loadMany2Many(eagerScopeParents(), &eagerScopeUser{}, desc, eagerLoadEntry{Relation: "Roles"}, nil))

	joined := recorder.at(t, 0)
	assert.Equal(t, `select "eager_scope_roles".*, "eager_scope_role_user"."eager_scope_user_id" as "pivot_eager_scope_user_id", "eager_scope_role_user"."eager_scope_role_id" as "pivot_eager_scope_role_id" from "eager_scope_roles" inner join "eager_scope_role_user" ON "eager_scope_role_user"."eager_scope_role_id" = "eager_scope_roles"."id" where "eager_scope_role_user"."eager_scope_user_id" in (?, ?) and "visible" = ? and "archived_at" is null`, joined.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2), 1}), joined.args)
}

// TestEagerGlobalScopes_Many2ManyRemoval pins that the callback's removal gate reaches the m2m child.
func TestEagerGlobalScopes_Many2ManyRemoval(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &eagerScopeUser{})
	desc, err := relations.ResolveRelation(&eagerScopeUser{}, "Roles")
	require.NoError(t, err)

	recorder.returnRows(`from "eager_scope_roles"`, []string{"id", "name", "pivot_eager_scope_user_id"},
		[]driver.Value{int64(9), "admin", int64(1)})

	cb := func(query contractsorm.Query) contractsorm.Query { return query.WithoutGlobalScopes() }
	require.NoError(t, q.loadMany2Many(eagerScopeParents(), &eagerScopeUser{}, desc, eagerLoadEntry{Relation: "Roles", Callback: cb}, nil))

	joined := recorder.at(t, 0)
	assert.Equal(t, `select "eager_scope_roles".*, "eager_scope_role_user"."eager_scope_user_id" as "pivot_eager_scope_user_id", "eager_scope_role_user"."eager_scope_role_id" as "pivot_eager_scope_role_id" from "eager_scope_roles" inner join "eager_scope_role_user" ON "eager_scope_role_user"."eager_scope_role_id" = "eager_scope_roles"."id" where "eager_scope_role_user"."eager_scope_user_id" in (?, ?)`, joined.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), joined.args)
}

// TestEagerGlobalScopes_Through pins the through eager related child (oracle E11).
func TestEagerGlobalScopes_Through(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &eagerScopeCountry{})
	desc, err := relations.ResolveRelation(&eagerScopeCountry{}, "Posts")
	require.NoError(t, err)

	recorder.returnRows(`from "eager_scope_tusers"`, []string{desc.FirstKey, desc.SecondLocalKey},
		[]driver.Value{int64(1), int64(7)})

	parents := []reflect.Value{
		reflect.ValueOf(&eagerScopeCountry{ID: 1}).Elem(),
		reflect.ValueOf(&eagerScopeCountry{ID: 2}).Elem(),
	}
	require.NoError(t, q.loadThrough(parents, &eagerScopeCountry{}, desc, eagerLoadEntry{Relation: "Posts"}, nil, true))

	related := recorder.at(t, 1)
	assert.Equal(t, `select "eager_scope_tposts".*, "eager_scope_tusers"."eager_scope_country_id" as "laravel_through_key" from "eager_scope_tposts" inner join "eager_scope_tusers" on "eager_scope_tusers"."id" = "eager_scope_tposts"."eager_scope_user_id" where "eager_scope_tusers"."eager_scope_country_id" in (?, ?) and "visible" = ? and "archived_at" is null`, related.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2), 1}), related.args)
}

// TestEagerGlobalScopes_Removal pins every removal shape on the has-many eager read: the callback
// (withoutGlobalScopes() drops all, withoutGlobalScope(name) drops one) and the same declared on the
// relation (oracle E3/E4/E5/E6).
func TestEagerGlobalScopes_Removal(t *testing.T) {
	cases := []struct {
		name     string
		relation string
		callback contractsorm.RelationCallback
		sql      string
		bindings []driver.Value
	}{
		{
			name:     "callback removes every scope",
			relation: "Posts",
			callback: func(query contractsorm.Query) contractsorm.Query { return query.WithoutGlobalScopes() },
			sql:      `select * from "eager_scope_posts" where "eager_scope_posts"."user_id" in (?)`,
			bindings: driverBindings([]any{uint(1)}),
		},
		{
			name:     "callback removes one named scope",
			relation: "Posts",
			callback: func(query contractsorm.Query) contractsorm.Query { return query.WithoutGlobalScopes("visible_scope") },
			sql:      `select * from "eager_scope_posts" where "eager_scope_posts"."user_id" in (?) and "archived_at" is null`,
			bindings: driverBindings([]any{uint(1)}),
		},
		{
			name:     "relation removes every scope",
			relation: "PostsPlain",
			sql:      `select * from "eager_scope_posts" where "eager_scope_posts"."user_id" in (?)`,
			bindings: driverBindings([]any{uint(1)}),
		},
		{
			name:     "relation removes one named scope",
			relation: "PostsNoVisible",
			sql:      `select * from "eager_scope_posts" where "eager_scope_posts"."user_id" in (?) and "archived_at" is null`,
			bindings: driverBindings([]any{uint(1)}),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, &eagerScopeUser{})
			desc, err := relations.ResolveRelation(&eagerScopeUser{}, c.relation)
			require.NoError(t, err)

			parents := []reflect.Value{reflect.ValueOf(&eagerScopeUser{ID: 1}).Elem()}
			require.NoError(t, q.loadHasOneOrMany(parents, &eagerScopeUser{}, desc, eagerLoadEntry{Relation: c.relation, Callback: c.callback}, nil, true))

			recorded := recorder.last(t)
			assert.Equal(t, c.sql, recorded.sql)
			assert.Equal(t, c.bindings, recorded.args)
		})
	}
}

// TestEagerGlobalScopes_UnscopedModelUnchanged is the control: a related model that declares no
// user scopes reads exactly as before, so the change adds clauses only where Laravel does.
func TestEagerGlobalScopes_UnscopedModelUnchanged(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})
	desc, err := relations.ResolveRelation(&relUser{}, "Books")
	require.NoError(t, err)

	parents := []reflect.Value{
		reflect.ValueOf(&relUser{ID: 1}).Elem(),
		reflect.ValueOf(&relUser{ID: 2}).Elem(),
	}
	require.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc, eagerLoadEntry{Relation: "Books"}, nil, true))

	assert.Equal(t, `select * from "rel_books" where "rel_books"."user_id" in (?, ?)`, recorder.last(t).sql)
}
