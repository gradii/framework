package global_scopes

// This file ports Laravel's tests/Database/DatabaseEloquentGlobalScopesTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestGlobalScopes_<method without the
// test prefix>.
//
// The corpus's setUp only boots an in-memory sqlite connection. Eleven of its tests then pin
// $model->newQuery()->toSql() alongside getBindings() and never touch the database; four exercise
// Eloquent's scope nesting and has(). Every ported test also runs its pinned statement against the
// harness's real sqlite database: the "table" the corpus's models address is created with plain
// DDL, three rows are seeded through the ORM, and the rows the scoped statement returns stand in
// for the corpus's getBindings() assertion, because the Go ORM exposes no bindings accessor.
//
// # Scope mapping
//
// Laravel registers global scopes on the model — boot() / attributes / addGlobalScopes([...]) — and
// the port used to declare all of them in one place, the model's GlobalScopes(). That mechanism is
// gone: a scope is mounted on the builder that runs it, with ApplyScope(name, fn), and removed with
// RemoveScope(name). Every test below therefore mounts the scopes its corpus model registered, at the
// query it builds; the name is the scope's identity, so the corpus's removals map directly:
// withoutGlobalScope(ActiveScope::class) and withoutGlobalScope('active_scope') become
// RemoveScope("ActiveScope") and RemoveScope("active_scope"). What each test asserts — the pinned
// statement and the rows it returns — is unchanged.
//
// The registration SHAPES the corpus exercises no longer exist in Go and their tests are gone with
// them: addGlobalScope(ActiveScope::class) (the class-string form), #[ScopedBy] on the model, a
// #[ScopedBy] inherited from a trait, and addGlobalScopes([...]) array registration. What survives of
// each is the mount itself, which the closure test pins.
//
// One Go-side shape differs from Laravel and is spelled out at its test:
//
//   - WithoutGlobalScopes() with no names removes every scope (the builder's "*" wildcard, like
//     Laravel's withoutGlobalScopes()); there is no withoutGlobalScopesExcept, so the
//     except-specified test passes the other scope's key instead, which removes exactly that scope.
//
// The mounted scopes apply in mount order — the order the corpus's boot() registered them, and the
// nesting tests below observe it (or_scope is mounted before active_scope, as the child boot()
// registered it before the parent's).
//
// # Oracle
//
// Every SQL expectation this file asserts was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, sqlite dialect, driving
// the corpus's own model classes), never recalled from our own output:
//
//	ActiveScope instance / class-string / #[ScopedBy] / trait #[ScopedBy]
//	    -> select * from "table" where "active" = ?                       bindings [1]
//	withoutGlobalScope(ActiveScope::class)
//	    -> select * from "table"                                          bindings []
//	closure + 'active_scope' (and the array registration)
//	    -> select * from "table" where "active" = ? order by "name" asc   bindings [1]
//	withoutGlobalScope('active_scope')
//	    -> select * from "table" order by "name" asc                      bindings []
//	withoutGlobalScopes() (instance and static forms)
//	    -> select * from "table"                                          bindings []
//	withoutGlobalScopesExcept(['active_scope']) (instance and static forms)
//	    -> select * from "table" where "active" = ?                       bindings [1]
//
// The nesting tests' oracle strings are quoted at their tests below.
//
// # Scope application
//
// Scopes() applies a scope the way Eloquent's Builder::callScope does: after each scope the where
// list is sliced at the count from before the scope ran (Builder::addNewWheresWithinGroup), and a
// slice that carries an or-connector becomes one nested where whose own connector is the slice's
// first, " not" stripped (Builder::groupWhereSliceForScope); a slice without one joins flat.
//
// The existence subquery behind Has matches the oracle: it selects `*`, applies the related model's
// global scopes inside the subquery, and keeps the exists clause at the call-order position of the
// Has() call - so the has() test asserts the oracle string and the rows the statement returns.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// --- models ------------------------------------------------------------------

// globalScopesTestModel ports EloquentGlobalScopesTestModel, whose boot() registers
// addGlobalScope(new ActiveScope). The scope's identity is its name, "ActiveScope", the way Laravel
// keys an instance scope by its class name.
type globalScopesTestModel struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Active int    `gorm:"column:active"`
}

func (globalScopesTestModel) TableName() string { return "table" }

func (globalScopesTestModel) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "ActiveScope", Scope: func(query contractsorm.Query) contractsorm.Query { return query.Where("active", 1) }},
	}
}

// globalScopesClosureModel ports EloquentClosureGlobalScopesTestModel: a bare closure scope that
// orders by name (Laravel keys it by closure identity; here it is named "order_by_name") and the
// named 'active_scope' that filters active = 1. The corpus's two local scopes, approved() and
// orApproved(), become the scope methods below, applied through Scopes(...) — the entry point the
// Go ORM declares for locally-defined scopes; the nesting tests below exercise them.
type globalScopesClosureModel struct {
	ID            uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Active        int    `gorm:"column:active"`
	Name          string `gorm:"column:name"`
	Approved      int    `gorm:"column:approved"`
	ShouldApprove int    `gorm:"column:should_approve"`
	Foo           string `gorm:"column:foo"`
	Bar           string `gorm:"column:bar"`
}

func (globalScopesClosureModel) TableName() string { return "table" }

// globalScopesClosureScopes mounts the two scopes EloquentClosureGlobalScopesTestModel registered —
// the bare orderBy-name closure, then 'active_scope' — on the query the test builds.
func globalScopesClosureScopes(query contractsorm.Query) contractsorm.Query {
	return query.
		ApplyScope("order_by_name", func(query contractsorm.Query) contractsorm.Query { return query.OrderBy("name") }).
		ApplyScope("active_scope", func(query contractsorm.Query) contractsorm.Query { return query.Where("active", 1) })
}

// globalScopesActiveScope is the scope the corpus registers as ActiveScope on its own model (and as
// 'active_scope' through the closure/array registrations).
func globalScopesActiveScope(query contractsorm.Query) contractsorm.Query {
	return query.Where("active", 1)
}

// globalScopesOrderByName is the bare orderBy('name') closure scope.
func globalScopesOrderByName(query contractsorm.Query) contractsorm.Query {
	return query.OrderBy("name")
}

// ScopeApproved ports EloquentClosureGlobalScopesTestModel::scopeApproved.
func (globalScopesClosureModel) ScopeApproved(query contractsorm.Query) contractsorm.Query {
	return query.Where("approved", 1).OrWhere("should_approve", 0)
}

// ScopeOrApproved ports EloquentClosureGlobalScopesTestModel::scopeOrApproved, whose first where is
// an orWhere.
func (globalScopesClosureModel) ScopeOrApproved(query contractsorm.Query) contractsorm.Query {
	return query.OrWhere("approved", 1).OrWhere("should_approve", 0)
}

// globalScopesWithOrModel ports EloquentClosureGlobalScopesWithOrTestModel: 'or_scope' and the
// select closure are registered on the child, order_by_name and active_scope on the parent — in
// Go all four are declared in one slice, in that registration order.
type globalScopesWithOrModel struct {
	ID       uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Active   int    `gorm:"column:active"`
	Name     string `gorm:"column:name"`
	Email    string `gorm:"column:email"`
	Password string `gorm:"column:password"`
	Col1     string `gorm:"column:col1"`
	Col2     string `gorm:"column:col2"`
}

func (globalScopesWithOrModel) TableName() string { return "table" }

// globalScopesOrScopes mounts the four scopes EloquentClosureGlobalScopesWithOrTestModel registered,
// in registration order: 'or_scope' and the select closure (registered on the child), then
// order_by_name and active_scope (the parent's).
func globalScopesOrScopes(query contractsorm.Query) contractsorm.Query {
	return query.
		ApplyScope("or_scope", func(query contractsorm.Query) contractsorm.Query {
			return query.Where("email", "taylor@gmail.com").OrWhere("email", "someone@else.com")
		}).
		ApplyScope("select_email_password", func(query contractsorm.Query) contractsorm.Query {
			return query.Select("email", "password")
		}).
		ApplyScope("order_by_name", globalScopesOrderByName).
		ApplyScope("active_scope", globalScopesActiveScope)
}

// globalScopesWithRelationModel ports EloquentGlobalScopesWithRelationModel: table2, the closure
// model's scopes, and related() — hasMany(EloquentGlobalScopesTestModel, 'related_id') with a
// where('foo', 'bar') constraint, declared here through the relation's OnQuery callback.
type globalScopesWithRelationModel struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Name   string `gorm:"column:name"`
	Active int    `gorm:"column:active"`
	Bar    string `gorm:"column:bar"`
}

func (globalScopesWithRelationModel) TableName() string { return "table2" }

// globalScopesRelationScopes mounts the two scopes EloquentGlobalScopesWithRelationModel registered:
// order_by_name, then active_scope.
func globalScopesRelationScopes(query contractsorm.Query) contractsorm.Query {
	return query.
		ApplyScope("order_by_name", globalScopesOrderByName).
		ApplyScope("active_scope", globalScopesActiveScope)
}

func (globalScopesWithRelationModel) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Related": contractsorm.HasMany{
			Related:    &globalScopesTestModel{},
			ForeignKey: "related_id",
			OnQuery:    func(query contractsorm.Query) contractsorm.Query { return query.Where("foo", "bar") },
		},
	}
}

// --- schema and seeding ------------------------------------------------------

// globalScopesSchema creates the "table" the corpus's models address — one wide table so every
// model's columns exist, since the corpus points several classes at the same table name — plus the
// relation model's "table2".
func globalScopesSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table "table" (id integer primary key autoincrement, "active" integer not null default 0, "name" text not null default '', "email" text not null default '', "password" text not null default '', "approved" integer not null default 0, "should_approve" integer not null default 0, "foo" text not null default '', "bar" text not null default '', "col1" text not null default '', "col2" text not null default '', "related_id" integer null)`,
		`create table "table2" (id integer primary key autoincrement, "name" text not null default '', "active" integer not null default 0, "bar" text not null default '')`,
	)
}

// globalScopesSeedRows inserts three rows: an active = 0 row named "b" between two active rows
// named "a" and "c", so the scoped reads separate both the filter and the ordering from the
// insertion order "b, a, c".
func globalScopesSeedRows(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, row := range []globalScopesClosureModel{
		{Active: 0, Name: "b"},
		{Active: 1, Name: "a"},
		{Active: 1, Name: "c"},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&row))
	}
}

// globalScopesNames collects the names of the returned rows.
func globalScopesNames(rows []globalScopesClosureModel) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}

	return names
}

// globalScopesOrSeedRows inserts the or-nesting test's rows: "a" and "b" carry the col1/col2 values
// the chained predicates read, "c" only the email the or_scope matches, and the inactive "d" carries
// both column values but must be excluded by active_scope.
func globalScopesOrSeedRows(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, row := range []globalScopesWithOrModel{
		{Active: 1, Name: "a", Email: "taylor@gmail.com", Col1: "val1"},
		{Active: 1, Name: "b", Email: "someone@else.com", Col2: "val2"},
		{Active: 1, Name: "c", Email: "taylor@gmail.com"},
		{Active: 0, Name: "d", Email: "taylor@gmail.com", Col1: "val1", Col2: "val2"},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&row))
	}
}

// globalScopesOrEmails collects the emails of the or-model rows: the select scope keeps only the
// address and password, so the address is the observable.
func globalScopesOrEmails(rows []globalScopesWithOrModel) []string {
	emails := make([]string, 0, len(rows))
	for _, row := range rows {
		emails = append(emails, row.Email)
	}

	return emails
}

// globalScopesApprovedSeedRows inserts the rows for the two scope-nesting tests: r1 and r2 satisfy
// both the (foo or bar) chain and the scope's (approved or should_approve) chain, r3 only the
// leading chain, r4 only the scope's chain, and r5 neither.
func globalScopesApprovedSeedRows(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, row := range []globalScopesClosureModel{
		{Name: "r1", Foo: "foo", Approved: 1, ShouldApprove: 1},
		{Name: "r2", Bar: "bar", Approved: 1, ShouldApprove: 1},
		{Name: "r3", Foo: "foo", Approved: 0, ShouldApprove: 1},
		{Name: "r4", Approved: 1, ShouldApprove: 0},
		{Name: "r5", Approved: 0, ShouldApprove: 1},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&row))
	}
}

// --- ports of the corpus -----------------------------------------------------

func TestGlobalScopes_GlobalScopeIsApplied(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)

	model := globalScopesTestModel{}

	// Laravel: $model->newQuery() pins
	//   select * from "table" where "active" = ?  (bindings [1], oracle). The scope the model
	// registered as ActiveScope is mounted here, where the model declared it before.
	scoped := func(q contractsorm.Query) contractsorm.Query {
		return q.ApplyScope("ActiveScope", globalScopesActiveScope)
	}
	assert.Equal(t, `select * from "table" where "active" = ?`, scoped(cloneRelationQuery(t, query).Model(&model)).ToSql().Get(&model))

	// The active = 1 binding selects the two active rows — the seeded active = 0 row stays out.
	got := []globalScopesTestModel{}
	require.NoError(t, scoped(cloneRelationQuery(t, query).Model(&globalScopesTestModel{})).Get(&got))
	require.Len(t, got, 2)
	for _, row := range got {
		assert.Equal(t, 1, row.Active)
	}
}

func TestGlobalScopes_GlobalScopeCanBeRemoved(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)

	// Laravel: $model->newQuery()->withoutGlobalScope(ActiveScope::class) pins
	//   select * from "table"  (bindings [], oracle). In Go the scope's identity is its
	//GlobalScopes() name, so the class is removed by the name "ActiveScope".
	unscoped := globalScopesTestModel{}
	scoped := func(q contractsorm.Query) contractsorm.Query {
		return q.ApplyScope("ActiveScope", globalScopesActiveScope).RemoveScope("ActiveScope")
	}
	assert.Equal(t, `select * from "table"`, scoped(cloneRelationQuery(t, query).Model(&unscoped)).ToSql().Get(&unscoped))

	// With the scope gone every seeded row is visible again.
	got := []globalScopesTestModel{}
	require.NoError(t, scoped(cloneRelationQuery(t, query).Model(&globalScopesTestModel{})).Get(&got))
	assert.Len(t, got, 3)
}

func TestGlobalScopes_ClosureGlobalScopeIsApplied(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)

	model := globalScopesClosureModel{}

	// Laravel: newQuery() with the orderBy-name closure and 'active_scope' pins
	//   select * from "table" where "active" = ? order by "name" asc  (bindings [1], oracle).
	scoped := func(q contractsorm.Query) contractsorm.Query { return globalScopesClosureScopes(q) }
	assert.Equal(t, `select * from "table" where "active" = ? order by "name" asc`, scoped(cloneRelationQuery(t, query).Model(&model)).ToSql().Get(&model))

	// The read proves both scopes: only the active rows return, ordered by name — "a, c", not the
	// insertion order "a, b, c".
	got := []globalScopesClosureModel{}
	require.NoError(t, scoped(cloneRelationQuery(t, query).Model(&globalScopesClosureModel{})).Get(&got))
	assert.Equal(t, []string{"a", "c"}, globalScopesNames(got))
}

func TestGlobalScopes_ClosureGlobalScopeCanBeRemoved(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)

	// Laravel: newQuery()->withoutGlobalScope('active_scope') pins
	//   select * from "table" order by "name" asc  (bindings [], oracle) — the named scope is
	// removed by its name while the closure scope stays.
	unscoped := globalScopesClosureModel{}
	scoped := func(q contractsorm.Query) contractsorm.Query {
		return globalScopesClosureScopes(q).RemoveScope("active_scope")
	}
	assert.Equal(t, `select * from "table" order by "name" asc`, scoped(cloneRelationQuery(t, query).Model(&unscoped)).ToSql().Get(&unscoped))

	// Every row returns now; the order-by scope still sorts them.
	got := []globalScopesClosureModel{}
	require.NoError(t, scoped(cloneRelationQuery(t, query).Model(&globalScopesClosureModel{})).Get(&got))
	assert.Equal(t, []string{"a", "b", "c"}, globalScopesNames(got))
}

func TestGlobalScopes_GlobalScopeCanBeRemovedAfterTheQueryIsExecuted(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)

	model := globalScopesClosureModel{}
	q := globalScopesClosureScopes(cloneRelationQuery(t, query).Model(&model))

	// Laravel: $query->toSql() before the removal pins
	//   select * from "table" where "active" = ? order by "name" asc  (bindings [1], oracle).
	assert.Equal(t, `select * from "table" where "active" = ? order by "name" asc`, q.ToSql().Get(&globalScopesClosureModel{}))

	// Laravel: $query->withoutGlobalScope('active_scope'); $query->toSql() again pins
	//   select * from "table" order by "name" asc  (bindings [], oracle) — removing from a builder
	// whose toSql() already ran still changes the next statement. In Go RemoveScope hands back the
	// builder carrying the removal.
	q = q.RemoveScope("active_scope")
	assert.Equal(t, `select * from "table" order by "name" asc`, q.ToSql().Get(&globalScopesClosureModel{}))

	got := []globalScopesClosureModel{}
	require.NoError(t, q.Get(&got))
	assert.Equal(t, []string{"a", "b", "c"}, globalScopesNames(got))
}

func TestGlobalScopes_AllGlobalScopesCanBeRemoved(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)

	// Laravel: $model->newQuery()->withoutGlobalScopes() pins
	//   select * from "table"  (bindings [], oracle).
	unscoped := func(q contractsorm.Query) contractsorm.Query {
		return globalScopesClosureScopes(q).RemoveScope("order_by_name").RemoveScope("active_scope")
	}
	instanceModel := globalScopesClosureModel{}
	assert.Equal(t, `select * from "table"`, unscoped(cloneRelationQuery(t, query).Model(&instanceModel)).ToSql().Get(&instanceModel))

	// Laravel: EloquentClosureGlobalScopesTestModel::withoutGlobalScopes() pins the same string. The
	// static call funnels through Model::__callStatic into newQuery()->withoutGlobalScopes(); with
	// no static forwarder in Go, both corpus assertions are spelled the same way here.
	staticModel := globalScopesClosureModel{}
	assert.Equal(t, `select * from "table"`, unscoped(cloneRelationQuery(t, query).Model(&staticModel)).ToSql().Get(&staticModel))

	// With every scope removed the read returns all three rows and nothing orders them, so the
	// assertion is on the set.
	got := []globalScopesClosureModel{}
	require.NoError(t, unscoped(cloneRelationQuery(t, query).Model(&globalScopesClosureModel{})).Get(&got))
	assert.ElementsMatch(t, []string{"a", "b", "c"}, globalScopesNames(got))
}

func TestGlobalScopes_AllGlobalScopesCanBeRemovedExceptSpecified(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)

	// Laravel: $model->newQuery()->withoutGlobalScopesExcept(['active_scope']) pins
	//   select * from "table" where "active" = ?  (bindings [1], oracle): the order-by scope is
	// removed, the named one stays. The Go builder has no Except form — WithoutGlobalScopes removes
	// the names it is given — so the port removes the other scope's key, which is the same removal.
	scoped := func(q contractsorm.Query) contractsorm.Query {
		return globalScopesClosureScopes(q).RemoveScope("order_by_name")
	}
	instanceModel := globalScopesClosureModel{}
	assert.Equal(t, `select * from "table" where "active" = ?`, scoped(cloneRelationQuery(t, query).Model(&instanceModel)).ToSql().Get(&instanceModel))

	// Laravel: EloquentClosureGlobalScopesTestModel::withoutGlobalScopesExcept(['active_scope'])
	// pins the same string.
	staticModel := globalScopesClosureModel{}
	assert.Equal(t, `select * from "table" where "active" = ?`, scoped(cloneRelationQuery(t, query).Model(&staticModel)).ToSql().Get(&staticModel))

	// The surviving scope filters; with the order-by scope removed the read asserts the set.
	got := []globalScopesClosureModel{}
	require.NoError(t, scoped(cloneRelationQuery(t, query).Model(&globalScopesClosureModel{})).Get(&got))
	assert.ElementsMatch(t, []string{"a", "c"}, globalScopesNames(got))
}

func TestGlobalScopes_GlobalScopesWithOrWhereConditionsAreNested(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesOrSeedRows(t, query)

	model := globalScopesWithOrModel{}

	// Laravel pins, for $model->newQuery():
	//   select "email", "password" from "table" where ("email" = ? or "email" = ?) and "active" = ?
	//   order by "name" asc                                          (bindings ['taylor@gmail.com', 'someone@else.com', 1], oracle)
	// and for the chained where('col1', 'val1')->orWhere('col2', 'val2'):
	//   select "email", "password" from "table" where ("col1" = ? or "col2" = ?)
	//   and ("email" = ? or "email" = ?) and "active" = ? order by "name" asc
	//   (bindings ['val1', 'val2', 'taylor@gmail.com', 'someone@else.com', 1], oracle).
	scoped := globalScopesOrScopes(cloneRelationQuery(t, query).Model(&model))
	assert.Equal(t, `select "email", "password" from "table" where ("email" = ? or "email" = ?) and "active" = ? order by "name" asc`, scoped.ToSql().Get(&model))

	// The read proves the scopes applied in mount order — or_scope before active_scope — and
	// nested: only the active rows whose email the or_scope matches return, in name order "a, b, c".
	got := []globalScopesWithOrModel{}
	require.NoError(t, globalScopesOrScopes(cloneRelationQuery(t, query).Model(&globalScopesWithOrModel{})).Get(&got))
	assert.Equal(t, []string{"taylor@gmail.com", "someone@else.com", "taylor@gmail.com"}, globalScopesOrEmails(got))

	chained := globalScopesOrScopes(cloneRelationQuery(t, query).Model(&model)).Where("col1", "val1").OrWhere("col2", "val2")
	assert.Equal(t, `select "email", "password" from "table" where ("col1" = ? or "col2" = ?) and ("email" = ? or "email" = ?) and "active" = ? order by "name" asc`, chained.ToSql().Get(&model))

	// The caller's or-chain became its own nested group ahead of the email group, so the read is
	// the intersection: "a" and "b" carry a matching col1/col2, while "c" (no matching column) and
	// the inactive "d" stay out.
	got = nil
	require.NoError(t, globalScopesOrScopes(cloneRelationQuery(t, query).Model(&globalScopesWithOrModel{})).Where("col1", "val1").OrWhere("col2", "val2").Get(&got))
	assert.Equal(t, []string{"taylor@gmail.com", "someone@else.com"}, globalScopesOrEmails(got))
}

func TestGlobalScopes_RegularScopesWithOrWhereConditionsAreNested(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesApprovedSeedRows(t, query)

	// Laravel: withoutGlobalScopes()->where('foo', 'foo')->orWhere('bar', 'bar')->approved() pins
	// (the port has no automatic scopes to remove here: a scope is mounted only if the caller mounts
	// it, so the wildcard call stays as the corpus shape and removes nothing)
	//   select * from "table" where ("foo" = ? or "bar" = ?) and ("approved" = ? or "should_approve" = ?)
	//   (bindings ['foo', 'bar', 1, 0], oracle): Eloquent's callScope groups the pre-existing
	// or-chain and the scope's own or-chain into separate nested wheres.
	chained := cloneRelationQuery(t, query).Model(&globalScopesClosureModel{}).WithoutGlobalScopes().Where("foo", "foo").OrWhere("bar", "bar").Scopes(globalScopesClosureModel{}.ScopeApproved)
	assert.Equal(t, `select * from "table" where ("foo" = ? or "bar" = ?) and ("approved" = ? or "should_approve" = ?)`, chained.ToSql().Get(&globalScopesClosureModel{}))

	// The read proves the intersection: r1 and r2 satisfy both or-chains; r3 (foo only) and r4
	// (approved only) stay out of the and-form.
	got := []globalScopesClosureModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&globalScopesClosureModel{}).WithoutGlobalScopes().Where("foo", "foo").OrWhere("bar", "bar").Scopes(globalScopesClosureModel{}.ScopeApproved).Get(&got))
	assert.ElementsMatch(t, []string{"r1", "r2"}, globalScopesNames(got))
}

func TestGlobalScopes_ScopesStartingWithOrBooleanArePreserved(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesApprovedSeedRows(t, query)

	// Laravel: withoutGlobalScopes()->where('foo', 'foo')->orWhere('bar', 'bar')->orApproved() pins
	// (as above: nothing automatic is mounted, so the wildcard call removes nothing)
	//   select * from "table" where ("foo" = ? or "bar" = ?) or ("approved" = ? or "should_approve" = ?)
	//   (bindings ['foo', 'bar', 1, 0], oracle): the scope's leading orWhere is preserved as the
	// nested group's boolean.
	chained := cloneRelationQuery(t, query).Model(&globalScopesClosureModel{}).WithoutGlobalScopes().Where("foo", "foo").OrWhere("bar", "bar").Scopes(globalScopesClosureModel{}.ScopeOrApproved)
	assert.Equal(t, `select * from "table" where ("foo" = ? or "bar" = ?) or ("approved" = ? or "should_approve" = ?)`, chained.ToSql().Get(&globalScopesClosureModel{}))

	// The read proves the union: r3 (foo only) and r4 (approved only) join r1 and r2; only r5,
	// which satisfies neither chain, stays out.
	got := []globalScopesClosureModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&globalScopesClosureModel{}).WithoutGlobalScopes().Where("foo", "foo").OrWhere("bar", "bar").Scopes(globalScopesClosureModel{}.ScopeOrApproved).Get(&got))
	assert.ElementsMatch(t, []string{"r1", "r2", "r3", "r4"}, globalScopesNames(got))
}

func TestGlobalScopes_HasQueryWhereBothModelsHaveGlobalScopes(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRows(t, query)
	globalScopesSeedRelationRows(t, query)

	model := globalScopesWithRelationModel{}

	// Laravel: EloquentGlobalScopesWithRelationModel::has('related')->where('bar', 'baz') pins
	//   select * from "table2" where exists (select * from "table" where "table2"."id" = "table"."related_id"
	//   and "foo" = ? and "active" = ?) and "bar" = ? and "active" = ? order by "name" asc
	//   (bindings ['bar', 1, 'baz', 1], oracle): the related subquery carries the related model's
	// ActiveScope, the whole exists clause leads the parent's wheres - the position the has() call
	// recorded - and the parent's global scopes trail them.
	scoped := func(q contractsorm.Query) contractsorm.Query { return globalScopesRelationScopes(q) }
	assert.Equal(t, `select * from "table2" where exists (select * from "table" where "table2"."id" = "table"."related_id" and "foo" = ? and "active" = ?) and "bar" = ? and "active" = ? order by "name" asc`, scoped(cloneRelationQuery(t, query).Model(&model)).Has("Related").Where("bar", "baz").ToSql().Get(&model))

	// The read proves the three nested predicates at once: of the four seeded table2 rows, only the
	// two that are active, carry bar = "baz", and have a related row with foo = "bar" and active = 1
	// return, ordered by the parent scope's name - "a", "c", not the insertion order.
	got := []globalScopesWithRelationModel{}
	require.NoError(t, scoped(cloneRelationQuery(t, query).Model(&globalScopesWithRelationModel{})).Has("Related").Where("bar", "baz").Get(&got))
	require.Len(t, got, 2)
	assert.Equal(t, []string{"a", "c"}, globalScopesRelationNames(got))
}

// globalScopesRelationSeedRow seeds the related side of the has() test into "table" without widening
// globalScopesTestModel, whose other tests read rows whose related_id is NULL into it.
type globalScopesRelationSeedRow struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Active    int    `gorm:"column:active"`
	Foo       string `gorm:"column:foo"`
	RelatedID uint64 `gorm:"column:related_id"`
}

func (globalScopesRelationSeedRow) TableName() string { return "table" }

// globalScopesSeedRelationRows seeds the has() test's two tables. The four table2 rows make every
// predicate observable: "a" and "c" qualify, "b" fails the bar = "baz" filter, "d" fails the parent
// ActiveScope, and "a" also carries two decoy related rows (a wrong foo, a wrong active) that must
// not widen or veto its qualification. The related rows are inserted against the created parents'
// keys; the parents go in "c" before "a", so the name order the parent scope asks for differs from
// the insertion order.
func globalScopesSeedRelationRows(t *testing.T, query contractsorm.Query) {
	t.Helper()

	c := &globalScopesWithRelationModel{Name: "c", Active: 1, Bar: "baz"}
	require.NoError(t, cloneRelationQuery(t, query).Create(c))

	a := &globalScopesWithRelationModel{Name: "a", Active: 1, Bar: "baz"}
	require.NoError(t, cloneRelationQuery(t, query).Create(a))

	b := &globalScopesWithRelationModel{Name: "b", Active: 1, Bar: "other"}
	require.NoError(t, cloneRelationQuery(t, query).Create(b))

	d := &globalScopesWithRelationModel{Name: "d", Active: 0, Bar: "baz"}
	require.NoError(t, cloneRelationQuery(t, query).Create(d))

	for _, row := range []globalScopesRelationSeedRow{
		{RelatedID: c.ID, Foo: "bar", Active: 1},
		{RelatedID: a.ID, Foo: "bar", Active: 1},
		{RelatedID: a.ID, Foo: "nope", Active: 1},
		{RelatedID: a.ID, Foo: "bar", Active: 0},
		{RelatedID: b.ID, Foo: "bar", Active: 1},
		{RelatedID: d.ID, Foo: "bar", Active: 1},
	} {
		row := row
		require.NoError(t, cloneRelationQuery(t, query).Create(&row))
	}
}

// globalScopesRelationNames collects the names of the relation-model rows.
func globalScopesRelationNames(rows []globalScopesWithRelationModel) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}

	return names
}
