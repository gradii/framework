package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file pins the global-scope-removal semantics of the existence and count subqueries
// (whereHas / orWhereHas / whereDoesntHave / withCount) - the subquery-path twin of round 12's
// named-removal gate on the main path (candidate 42). Laravel runs the caller's closure on the
// subquery (Builder::has -> callScope) and merges the relation's own removals
// (addHasWhere -> mergeConstraintsFrom -> withoutGlobalScopes($from->removedScopes())), so a
// withTrashed()/onlyTrashed()/withoutGlobalScope left by either governs the related model's
// deferred global scopes inside the exists body exactly as on the main path. Before the alignment
// applyRelationScope and relationScopeQuery carried only where clauses, so a removal was dropped and
// the subquery always kept the default "is null".
//
// Oracle (illuminate/database v12.55.1, real sqlite, executed): /tmp/r13/scope_removal_probe5.php
// (callback removals G1/G2/G3/G4), /tmp/r13/scope_removal_probe6.php (relation-default OnQuery
// removals) and /tmp/r13gate/deep_probe.php (the [constraints][deleted_at][user scope] order). The
// fixtures are relDeepOwner -> relDeepMid (framework soft delete + an "active" global scope).

// subqueryScopeOwner declares the relation-default (OnQuery) forms on the rel_deep_owners table, so
// a relation's own withTrashed() / onlyTrashed() / withoutGlobalScope reaches the existence subquery.
type subqueryScopeOwner struct {
	ID   uint
	Mids []*relDeepMid `gorm:"-"`
}

func (subqueryScopeOwner) TableName() string { return "rel_deep_owners" }

func (subqueryScopeOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Mids": contractsorm.HasMany{Related: &relDeepMid{}, ForeignKey: "owner_id"},
		"TrashedMids": contractsorm.HasMany{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() }},
		"OnlyTrashedMids": contractsorm.HasMany{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }},
		"NamedRmMids": contractsorm.HasMany{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes("active") }},
	}
}

func subquerySelectSQL(t *testing.T, q *OrmBuilder, dest any) string {
	t.Helper()

	sql, _, err := q.dest(dest).addGlobalScopes().compileSelect()
	require.NoError(t, err)

	return sql
}

// WithTrashed() inside the closure drops the SoftDeletingScope, so the default "is null" is gone
// while the related model's own user scope stays - oracle probe5 G3.
func TestSubqueryScopeRemoval_CallbackWithTrashedDropsTheSoftDeletePredicate(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.WithTrashed()
	}).(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "active" = ?)`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// OnlyTrashed() inside the closure replaces the default predicate with "is not null" - oracle G3.
func TestSubqueryScopeRemoval_CallbackOnlyTrashedEmitsIsNotNull(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.OnlyTrashed()
	}).(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?)`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// withoutGlobalScope(SoftDeletingScope) inside the closure drops only that predicate - oracle G1.
func TestSubqueryScopeRemoval_CallbackWithoutGlobalScopeSoftDeleting(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.WithoutGlobalScopes(contractsorm.SoftDeletingScope)
	}).(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "active" = ?)`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// withoutGlobalScopes() inside the closure drops every scope, the user scope included - oracle G1.
func TestSubqueryScopeRemoval_CallbackWithoutGlobalScopesDropsUserScopeToo(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.WithoutGlobalScopes()
	}).(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id")`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// withoutGlobalScope("active") inside the closure drops only the named user scope, keeping the
// SoftDeletingScope predicate - oracle G2.
func TestSubqueryScopeRemoval_CallbackNamedUserScopeRemoval(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.WithoutGlobalScopes("active")
	}).(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null)`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// A relation's default query (OnQuery) withTrashed() reaches the existence subquery - oracle probe6.
func TestSubqueryScopeRemoval_RelationDefaultWithTrashedPropagates(t *testing.T) {
	q := newRelQueryWith(t, &subqueryScopeOwner{}).WhereHas("TrashedMids").(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "active" = ?)`, subquerySelectSQL(t, q, &[]subqueryScopeOwner{}))
}

// A relation's default query (OnQuery) onlyTrashed() reaches the existence subquery - oracle probe6.
func TestSubqueryScopeRemoval_RelationDefaultOnlyTrashedPropagates(t *testing.T) {
	q := newRelQueryWith(t, &subqueryScopeOwner{}).WhereHas("OnlyTrashedMids").(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?)`, subquerySelectSQL(t, q, &[]subqueryScopeOwner{}))
}

// A relation's default query (OnQuery) withoutGlobalScope("active") drops only the named user scope
// inside the existence subquery - oracle probe6.
func TestSubqueryScopeRemoval_RelationDefaultNamedUserScopeRemoval(t *testing.T) {
	q := newRelQueryWith(t, &subqueryScopeOwner{}).WhereHas("NamedRmMids").(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null)`, subquerySelectSQL(t, q, &[]subqueryScopeOwner{}))
}

// The related model's SoftDeletingScope is registered before its own user scopes (the trait boots
// first), so the default predicate sits ahead of the user scope - the order the deep-chain oracle
// probe pins (the same order the re-pinned relation_sql_test.go deep tests now assert).
func TestSubqueryScopeRemoval_SoftDeletePredicatePrecedesTheUserScope(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids").(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// The aggregate (count) subquery shares the existence builder, so a withCount() closure's
// onlyTrashed() reaches the count subquery too - oracle probe5 G1 (withCount).
func TestSubqueryScopeRemoval_WithCountCallbackOnlyTrashed(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).
		WithCount(contractsorm.RelationCount{Name: "Mids", Callback: func(q contractsorm.Query) contractsorm.Query {
			return q.OnlyTrashed()
		}}).(*OrmBuilder)

	assert.Equal(t, `select "rel_deep_owners".*, (select count(*) from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?) as "mids_count" from "rel_deep_owners"`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// ---------------------------------------------------------------------------
// round-13 verification: the remaining relation-API forms x removals
// ---------------------------------------------------------------------------

// relOwnerOnlyTrashed carries the relation-default onlyTrashed() under the SAME "Mids" key as
// relDeepOwner, so the string-only WithExists can reach a relation default that flips the predicate
// to "is not null".
type relOwnerOnlyTrashed struct {
	ID   uint
	Mids []*relDeepMid `gorm:"-"`
}

func (relOwnerOnlyTrashed) TableName() string { return "rel_deep_owners" }

func (relOwnerOnlyTrashed) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Mids": contractsorm.HasMany{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }},
	}
}

// orWhereHas carries the closure's onlyTrashed() into the exists body exactly as whereHas does -
// oracle (/tmp/r13v/probe.php and scope_removal_probe5.php G1).
func TestSubqueryScopeRemoval_OrWhereHasCarriesTheClosureRemoval(t *testing.T) {
	q := toQuery(newRelQueryWith(t, &relDeepOwner{}).Where("x", 1)).OrWhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.OnlyTrashed()
	}).(*OrmBuilder)

	assert.Equal(t, `select * from "rel_deep_owners" where "x" = ? or exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?)`, subquerySelectSQL(t, q, &[]relDeepOwner{}))
}

// whereDoesntHave: the negation wraps the same exists body, so the removal still governs it - oracle
// /tmp/r13v/probe.php.
func TestSubqueryScopeRemoval_WhereDoesntHaveCarriesTheClosureRemoval(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WhereDoesntHave("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.OnlyTrashed()
	}).(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where not exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?)`, subquerySelectSQL(t, q, &[]relDeepOwner{}))

	q2 := newRelQueryWith(t, &relDeepOwner{}).WhereDoesntHave("Mids", func(q contractsorm.Query) contractsorm.Query {
		return q.WithoutGlobalScopes("active")
	}).(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where not exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null)`, subquerySelectSQL(t, q2, &[]relDeepOwner{}))
}

// withCount: the count subquery shares the existence builder, so a closure's withTrashed() / named
// removal reaches it - oracle /tmp/r13v/probe.php withCount rows.
func TestSubqueryScopeRemoval_WithCountCarriesTheClosureRemovals(t *testing.T) {
	cases := []struct {
		name     string
		callback func(contractsorm.Query) contractsorm.Query
		want     string
	}{
		{"withTrashed", func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() },
			`select "rel_deep_owners".*, (select count(*) from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "active" = ?) as "mids_count" from "rel_deep_owners"`},
		{"namedRm", func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes("active") },
			`select "rel_deep_owners".*, (select count(*) from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null) as "mids_count" from "rel_deep_owners"`},
	}

	for _, c := range cases {
		q := newRelQueryWith(t, &relDeepOwner{}).WithCount(contractsorm.RelationCount{Name: "Mids", Callback: c.callback}).(*OrmBuilder)
		assert.Equal(t, c.want, subquerySelectSQL(t, q, &[]relDeepOwner{}), c.name)
	}
}

// withExists: the existence subquery is the one WithCount builds; the port wraps it in a
// CASE WHEN ... THEN 1 ELSE 0 END (the documented cross-dialect divergence from Laravel's
// `exists(select ...)` - SQL Server has no boolean literal), so the removals reach the inner
// subquery while the wrapper differs by design. Oracle for the inner subquery: /tmp/r13v/probe.php
// withExists rows.
func TestSubqueryScopeRemoval_WithExistsCarriesTheRemoval(t *testing.T) {
	q := newRelQueryWith(t, &relDeepOwner{}).WithExists("Mids").(*OrmBuilder)
	assert.Equal(t, `select "rel_deep_owners".*, CASE WHEN EXISTS (select 1 from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null and "active" = ?) THEN 1 ELSE 0 END AS "mids_exists" from "rel_deep_owners"`, subquerySelectSQL(t, q, &[]relDeepOwner{}))

	q2 := newRelQueryWith(t, &relOwnerOnlyTrashed{}).WithExists("Mids").(*OrmBuilder)
	assert.Equal(t, `select "rel_deep_owners".*, CASE WHEN EXISTS (select 1 from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?) THEN 1 ELSE 0 END AS "mids_exists" from "rel_deep_owners"`, subquerySelectSQL(t, q2, &[]relOwnerOnlyTrashed{}))
}

// ---------------------------------------------------------------------------
// round-13 verification: the related model's user global scopes reach the ofMany aggregate
// ---------------------------------------------------------------------------

// relLatestOwner* declare a has-one latestOfMany over relDeepMid (soft delete + the "active" user
// scope), so the aggregate subquery's scopes are observable. relLatestOwnerRm / relLatestOwnerRmAll
// declare the relation's own removals (withoutGlobalScope("active") / withoutGlobalScopes()).
type relLatestOwner struct {
	ID  uint
	Mid *relDeepMid `gorm:"-"`
}

func (relLatestOwner) TableName() string { return "rel_deep_owners" }

func (relLatestOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"LatestMid": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") }},
	}
}

type relLatestOwnerRm struct {
	ID  uint
	Mid *relDeepMid `gorm:"-"`
}

func (relLatestOwnerRm) TableName() string { return "rel_deep_owners" }

func (relLatestOwnerRm) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"LatestMid": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes("active").OfMany("", "") }},
	}
}

type relLatestOwnerRmAll struct {
	ID  uint
	Mid *relDeepMid `gorm:"-"`
}

func (relLatestOwnerRmAll) TableName() string { return "rel_deep_owners" }

func (relLatestOwnerRmAll) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"LatestMid": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes().OfMany("", "") }},
	}
}

// TestOneOfManyAggregate_CarriesRelatedUserScopes pins the ofMany aggregate subquery carrying the
// related model's SoftDeletingScope AND its remaining user global scopes (SoftDeletingScope first),
// the way Laravel's applyScopes folds them into the aggregate; the relation's own removals govern
// both, while a caller's closure removal reaches only the outer query. Oracle:
// /tmp/r13v/ofmany4_probe.php (default / relation-removal / relation-withoutAllScopes / caller
// onlyTrashed rows).
func TestOneOfManyAggregate_CarriesRelatedUserScopes(t *testing.T) {
	q := newRelQueryWith(t, &relLatestOwner{}).WhereHas("LatestMid").(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "latest_mid" on "latest_mid"."id_aggregate" = "rel_deep_mids"."id" and "latest_mid"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, subquerySelectSQL(t, q, &[]relLatestOwner{}))

	q2 := newRelQueryWith(t, &relLatestOwnerRm{}).WhereHas("LatestMid").(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null group by "rel_deep_mids"."owner_id") as "latest_mid" on "latest_mid"."id_aggregate" = "rel_deep_mids"."id" and "latest_mid"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null)`, subquerySelectSQL(t, q2, &[]relLatestOwnerRm{}))

	q3 := newRelQueryWith(t, &relLatestOwnerRmAll{}).WhereHas("LatestMid").(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" group by "rel_deep_mids"."owner_id") as "latest_mid" on "latest_mid"."id_aggregate" = "rel_deep_mids"."id" and "latest_mid"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id")`, subquerySelectSQL(t, q3, &[]relLatestOwnerRmAll{}))

	q4 := newRelQueryWith(t, &relLatestOwner{}).WhereHas("LatestMid", func(q contractsorm.Query) contractsorm.Query {
		return q.OnlyTrashed()
	}).(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "latest_mid" on "latest_mid"."id_aggregate" = "rel_deep_mids"."id" and "latest_mid"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?)`, subquerySelectSQL(t, q4, &[]relLatestOwner{}))
}

// The lazy relation read shares oneOfManyJoin, so the aggregate carries the same scopes (oracle
// /tmp/r13v/ofmany4_probe.php `->latestMid()` row; the outer query's scope order is the port's
// documented soft-delete-last convention, so only the aggregate is byte-compared here).
func TestOneOfManyAggregate_LazyReadCarriesRelatedUserScopes(t *testing.T) {
	q := newRelQueryWith(t, &relDeepMid{})
	rel := q.Related(&relLatestOwner{ID: 1}, "LatestMid")
	sql := newRelationSQL(t, rel, &[]relDeepMid{})
	assert.Contains(t, sql, `from "rel_deep_mids" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id"`)

	q2 := newRelQueryWith(t, &relDeepMid{})
	rel2 := q2.Related(&relLatestOwnerRm{ID: 1}, "LatestMid")
	sql2 := newRelationSQL(t, rel2, &[]relDeepMid{})
	assert.Contains(t, sql2, `from "rel_deep_mids" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "rel_deep_mids"."deleted_at" is null group by "rel_deep_mids"."owner_id"`)
}

// withCount on an ofMany relation shares the aggregate, so the count body carries the related user
// scope too (oracle /tmp/r13v/ofmany4_probe.php withCount(latestMid) row).
func TestOneOfManyAggregate_WithCountCarriesRelatedUserScope(t *testing.T) {
	q := newRelQueryWith(t, &relLatestOwner{}).WithCount("LatestMid").(*OrmBuilder)
	assert.Equal(t, `select "rel_deep_owners".*, (select count(*) from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "latest_mid" on "latest_mid"."id_aggregate" = "rel_deep_mids"."id" and "latest_mid"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null and "active" = ?) as "latest_mid_count" from "rel_deep_owners"`, subquerySelectSQL(t, q, &[]relLatestOwner{}))
}

// TestSubqueryScopeRemoval_RelationDefaultAndCallbackRemovalsMerge pins the merge of a relation's
// default-query removal with the caller closure's removal - the cross-side case the single-sided
// pins above leave open. Laravel folds the relation's removedScopes into the closure's builder
// (addHasWhere -> mergeConstraintsFrom -> withoutGlobalScopes($from->removedScopes())), so a name
// removed by the default and a trashed flag set by the closure both govern the exists body. Oracle
// (illuminate/database v12.55.1, real sqlite, /tmp/r14v/probe_combo.php): TrashedMids + callback
// withoutGlobalScope("active") drops every predicate; NamedRmMids + callback onlyTrashed keeps just
// the "is not null".
func TestSubqueryScopeRemoval_RelationDefaultAndCallbackRemovalsMerge(t *testing.T) {
	q := newRelQueryWith(t, &subqueryScopeOwner{}).WhereHas("TrashedMids", func(q contractsorm.Query) contractsorm.Query {
		return q.WithoutGlobalScopes("active")
	}).(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id")`, subquerySelectSQL(t, q, &[]subqueryScopeOwner{}))

	q2 := newRelQueryWith(t, &subqueryScopeOwner{}).WhereHas("NamedRmMids", func(q contractsorm.Query) contractsorm.Query {
		return q.OnlyTrashed()
	}).(*OrmBuilder)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null)`, subquerySelectSQL(t, q2, &[]subqueryScopeOwner{}))
}
