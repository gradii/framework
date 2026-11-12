package gorm

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
)

// Round 15b: soft-delete and scope composition beyond the main where list. Every expectation below
// is the statement illuminate/database v12.55.1 compiles against real sqlite, byte-compared;
// probes and their outputs (labels quoted next to each case):
//
//	/Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle.php -> oracle.out             G1-G6, W1-W13, V4, V5, X1-X6, O1-O8, L1-L3
//	/Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle_sub.php -> oracle_sub.out     S1-S5
//	/Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle_kinds.php -> oracle_kinds.out the five other relation kinds (H|kind|case, C|kind|case)
//	/Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle_eager.php -> oracle_eager.out E1, E2
//	/Users/dog/.claude/jobs/013c27a0/tmp/r15-probes/probe_through.php -> /Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/probe_through.out T5, T6
//
// The of-many rows use the port's join alias (the relation name in snake case) where Laravel's
// latestOfMany() shows "latestOfMany": that naming difference is the separate, already documented
// alias gap (see TestOneOfManyAggregate_LazyReadCarriesRelatedUserScopes); every other byte is the
// oracle's.

type scrSrUserTrashedScope struct {
	ID    uint
	Email string
	databaseorm.SoftDeletes
}

func (scrSrUserTrashedScope) TableName() string { return "sr_users" }

// scrTrashedOnlyScope is the "trashedOnly" scope the trashed fixtures used to declare.
func scrTrashedOnlyScope(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }

type scrSrUserTrashedMidScope struct {
	ID    uint
	Email string
	databaseorm.SoftDeletes
}

func (scrSrUserTrashedMidScope) TableName() string { return "sr_users" }

// scrActiveTrashedScope is the "activeTrashed" scope the mid fixture used to declare.
func scrActiveTrashedScope(q contractsorm.Query) contractsorm.Query {
	return q.Where("active", 1).OnlyTrashed().Where("id", 9)
}

type scrSrUserTrashedOrScope struct {
	ID    uint
	Email string
	databaseorm.SoftDeletes
}

func (scrSrUserTrashedOrScope) TableName() string { return "sr_users" }

// scrOrTrashedScope is the "orTrashed" scope the or fixture used to declare.
func scrOrTrashedScope(q contractsorm.Query) contractsorm.Query {
	return q.Where("active", 1).OrWhere("active", 2).OnlyTrashed()
}

type scrCase struct {
	name     string
	query    contractsorm.Query
	sql      string
	bindings []any
}

func runScrCases(t *testing.T, cases []scrCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statement, bindings, err := tc.query.(*OrmBuilder).compileSelect()
			require.NoError(t, err)
			assert.Equal(t, tc.sql, statement)
			if tc.bindings == nil {
				assert.Empty(t, bindings)
			} else {
				assert.Equal(t, tc.bindings, bindings)
			}
		})
	}
}

// TestScopeComposition_OnlyTrashedInsideAGlobalScope pins Laravel's onlyTrashed macro called from a
// user global scope: SoftDeletingScope is the first applied scope, so its "is null" is already on
// the query and the macro's removal comes too late; the macro's whereNotNull lands where the scope
// called it, among the scope's own wheres, before callScope regroups them (G3 wraps it inside the
// scope's or-group). A caller's onlyTrashed / withTrashed removes the scope first (G4, G5), and
// removing the user scope by name leaves the plain read (G6).
func TestScopeComposition_OnlyTrashedInsideAGlobalScope(t *testing.T) {
	trashedScope := func() *OrmBuilder {
		return compiledQuery(t, &scrSrUserTrashedScope{}, Conditions{}).
			ApplyScope("trashedOnly", scrTrashedOnlyScope).(*OrmBuilder)
	}

	runScrCases(t, []scrCase{
		{"G1 scope calls onlyTrashed", trashedScope().Where("email", "a"), `select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is null and "sr_users"."deleted_at" is not null`, []any{"a"}},
		{"G2 onlyTrashed between the scope's own wheres", compiledQuery(t, &scrSrUserTrashedMidScope{}, Conditions{}).ApplyScope("activeTrashed", scrActiveTrashedScope).(*OrmBuilder).Where("email", "a").OrWhere("email", "b"), `select * from "sr_users" where ("email" = ? or "email" = ?) and "sr_users"."deleted_at" is null and "active" = ? and "sr_users"."deleted_at" is not null and "id" = ?`, []any{"a", "b", 1, 9}},
		{"G3 onlyTrashed inside the scope's or-group", compiledQuery(t, &scrSrUserTrashedOrScope{}, Conditions{}).ApplyScope("orTrashed", scrOrTrashedScope).(*OrmBuilder).Where("email", "a"), `select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is null and ("active" = ? or "active" = ? and "sr_users"."deleted_at" is not null)`, []any{"a", 1, 2}},
		{"G4 caller onlyTrashed and scope onlyTrashed", trashedScope().OnlyTrashed().Where("email", "a"), `select * from "sr_users" where "sr_users"."deleted_at" is not null and "email" = ? and "sr_users"."deleted_at" is not null`, []any{"a"}},
		{"G5 caller withTrashed and scope onlyTrashed", trashedScope().WithTrashed().Where("email", "a"), `select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is not null`, []any{"a"}},
		{"G6 the scope removed by name", trashedScope().RemoveScope("trashedOnly").Where("email", "a"), `select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is null`, []any{"a"}},
	})
}

// TestScopeComposition_WhereHasInsideAWhereClosure pins an existence clause a where closure wrote:
// Laravel's whereHas writes its exists onto the closure's builder at call time, so it is part of the
// nested group at its call position - with or, not, a scoping callback, a nested closure and an
// onlyTrashed beside it - and the bindings keep that order.
func TestScopeComposition_WhereHasInsideAWhereClosure(t *testing.T) {
	sd := func() *OrmBuilder { return compiledQuery(t, &scpSdUser{}, Conditions{}) }

	runScrCases(t, []scrCase{
		{"W1 closure whereHas", sd().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Posts")
		}), `select * from "sd_users" where "id" = ? and (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, []any{1}},
		{"W2 closure where then whereHas", sd().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a").WhereHas("Posts")
		}), `select * from "sd_users" where "id" = ? and ("email" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, []any{1, "a"}},
		{"W3 orWhere closure whereHas then where", sd().Where("id", 1).OrWhere(func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Posts").Where("email", "a")
		}), `select * from "sd_users" where ("id" = ? or (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null) and "email" = ?)) and "sd_users"."deleted_at" is null`, []any{1, "a"}},
		{"W4 closure where then orWhereHas", sd().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a").OrWhereHas("Posts")
		}), `select * from "sd_users" where "id" = ? and ("email" = ? or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, []any{1, "a"}},
		{"W5 closure whereDoesntHave then where", sd().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.WhereDoesntHave("Posts").Where("email", "a")
		}), `select * from "sd_users" where "id" = ? and (not exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null) and "email" = ?) and "sd_users"."deleted_at" is null`, []any{1, "a"}},
		{"W6 closure whereHas with a callback between wheres", sd().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a").WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 3) }).Where("id", 4)
		}), `select * from "sd_users" where "id" = ? and ("email" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and "id" = ?) and "sd_users"."deleted_at" is null`, []any{1, "a", 3, 4}},
		{"W7 orWhere closure orWhereHas then a parent where", sd().Where("id", 1).OrWhere(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a").OrWhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 3) })
		}).Where("id", 4), `select * from "sd_users" where ("id" = ? or ("email" = ? or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null)) and "id" = ?) and "sd_users"."deleted_at" is null`, []any{1, "a", 3, 4}},
		{"W8 whereHas in a closure nested in a closure", sd().Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.WhereHas("Posts").Where("email", "a") })
		}), `select * from "sd_users" where ((exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null) and "email" = ?)) and "sd_users"."deleted_at" is null`, []any{"a"}},
		{"V4 closure whereHas then onlyTrashed", sd().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Posts").OnlyTrashed()
		}), `select * from "sd_users" where "id" = ? and (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null) and "sd_users"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, []any{1}},
		{"V5 closure onlyTrashed then whereHas", sd().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.OnlyTrashed().WhereHas("Posts")
		}), `select * from "sd_users" where "id" = ? and ("sd_users"."deleted_at" is not null and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, []any{1}},
		{"W9 whereNot closure whereHas", sd().Where("id", 1).WhereNot(func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Posts")
		}), `select * from "sd_users" where "id" = ? and not (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, []any{1}},
		{"W10 orWhereNot closure where then whereHas", sd().Where("id", 1).OrWhereNot(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a").WhereHas("Posts")
		}), `select * from "sd_users" where ("id" = ? or not ("email" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null))) and "sd_users"."deleted_at" is null`, []any{1, "a"}},
		{"W11 whereNot closure onlyTrashed then where", sd().Where("id", 1).WhereNot(func(q contractsorm.Query) contractsorm.Query {
			return q.OnlyTrashed().Where("email", "a")
		}), `select * from "sd_users" where "id" = ? and not ("sd_users"."deleted_at" is not null and "email" = ?) and "sd_users"."deleted_at" is null`, []any{1, "a"}},
	})
}

// TestScopeComposition_WhereClosureGroupWithTheModelFromTheDestination pins the same closure groups
// on a query that names no model and learns it from the destination when it compiles - the shape
// Query().Where(...).Get(&users) has - so the deferred existence clause and onlyTrashed predicate are
// rendered then, against the destination's model (oracle W2, W12, W13).
func TestScopeComposition_WhereClosureGroupWithTheModelFromTheDestination(t *testing.T) {
	destOnly := func() *OrmBuilder { return compiledQuery(t, nil, Conditions{}) }
	compile := func(q contractsorm.Query, dest any) (string, []any) {
		statement, bindings, err := q.(*OrmBuilder).dest(dest).compileSelect()
		require.NoError(t, err)

		return statement, bindings
	}

	statement, bindings := compile(destOnly().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
		return q.Where("email", "a").WhereHas("Posts")
	}), &[]scpSdUser{})
	assert.Equal(t, `select * from "sd_users" where "id" = ? and ("email" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, statement)
	assert.Equal(t, []any{1, "a"}, bindings)

	// The scope the model used to declare is mounted here: a dest-only query resolves its model from
	// the destination (the soft-delete predicate below proves it), and the mount lands after the
	// caller's wheres the way a deferred scope does.
	statement, bindings = compile(destOnly().ApplyScope("trashedOnly", scrTrashedOnlyScope).Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query {
		return q.Where("email", "a").OnlyTrashed()
	}), &[]scrSrUserTrashedScope{})
	assert.Equal(t, `select * from "sr_users" where "id" = ? and ("email" = ? and "sr_users"."deleted_at" is not null) and "sr_users"."deleted_at" is null and "sr_users"."deleted_at" is not null`, statement)
	assert.Equal(t, []any{1, "a"}, bindings)

	// Compiling the same query twice writes the deferred clauses once each time.
	query := destOnly().Where(func(q contractsorm.Query) contractsorm.Query { return q.WhereHas("Posts") }).(*OrmBuilder)
	first, _ := compile(query, &[]scpSdUser{})
	second, _ := compile(query, &[]scpSdUser{})
	assert.Equal(t, `select * from "sd_users" where (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, first)
	assert.Equal(t, first, second)
}

// TestScopeComposition_OnlyTrashedIsPositionalInsideASubquery pins the onlyTrashed macro inside an
// existence / count subquery: the caller closure's call writes "is not null" at its call position
// among the closure's wheres (inside the closure's or-group when it has one, once per call), and no
// "is null" is written for the removed scope. The relation's own default query (OnQuery) behaves
// the same way among its own wheres, which follow the caller's closure (O6, O8).
func TestScopeComposition_OnlyTrashedIsPositionalInsideASubquery(t *testing.T) {
	sd := func() *OrmBuilder { return compiledQuery(t, &scpSdUser{}, Conditions{}) }
	owner := func() *OrmBuilder { return newRelQueryWith(t, &scrOfManyOwner{}) }

	runScrCases(t, []scrCase{
		{"X1 whereHas closure where, onlyTrashed, where", sd().WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query {
			return q.Where("id", 1).OnlyTrashed().Where("id", 2)
		}), `select * from "sd_users" where exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is not null and "id" = ?) and "sd_users"."deleted_at" is null`, []any{1, 2}},
		{"X2 whereHas closure where then onlyTrashed", sd().WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query {
			return q.Where("id", 1).OnlyTrashed()
		}), `select * from "sd_users" where exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, []any{1}},
		{"X4 withCount closure where, onlyTrashed, where", sd().WithCount(contractsorm.RelationCount{Name: "Posts", Callback: func(q contractsorm.Query) contractsorm.Query {
			return q.Where("id", 1).OnlyTrashed().Where("id", 2)
		}}), `select "sd_users".*, (select count(*) from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is not null and "id" = ?) as "posts_count" from "sd_users" where "sd_users"."deleted_at" is null`, []any{1, 2}},
		{"X5 onlyTrashed inside the closure's or-group", sd().WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query {
			return q.OnlyTrashed().Where("id", 1).OrWhere("id", 2)
		}), `select * from "sd_users" where exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and ("sd_posts"."deleted_at" is not null and "id" = ? or "id" = ?)) and "sd_users"."deleted_at" is null`, []any{1, 2}},
		{"X6 each onlyTrashed call writes its own predicate", sd().WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query {
			return q.OnlyTrashed().OnlyTrashed()
		}), `select * from "sd_users" where exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is not null and "sd_posts"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, nil},
		{"O6 relation default where, onlyTrashed, where", owner().WhereHas("OtWhere"), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "id" = ? and "rel_deep_mids"."deleted_at" is not null and "id" = ? and "active" = ?)`, []any{5, 6, 1}},
		{"O8 caller closure then the relation default's positional onlyTrashed", owner().WhereHas("OtWhere", func(q contractsorm.Query) contractsorm.Query {
			return q.Where("id", 7)
		}), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "id" = ? and "id" = ? and "rel_deep_mids"."deleted_at" is not null and "id" = ? and "active" = ?)`, []any{7, 5, 6, 1}},
	})
}

// TestScopeComposition_DeferredClausesInsideASubqueryCallback pins the clauses a whereHas / withCount
// callback defers - a where closure carrying an onlyTrashed, a whereHas, a where closure carrying a
// whereHas - rendered inside the subquery against the related model, at their call positions and
// ahead of the related model's scopes. The closure's onlyTrashed removes no scope from the subquery
// (S1, S5 keep "is null"): the macro ran on the closure's own builder.
func TestScopeComposition_DeferredClausesInsideASubqueryCallback(t *testing.T) {
	owner := func() *OrmBuilder { return newRelQueryWith(t, &relDeepOwner{}) }

	runScrCases(t, []scrCase{
		{"S1 where closure with onlyTrashed", owner().WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.Where("id", 1).OnlyTrashed() })
		}), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and ("id" = ? and "rel_deep_mids"."deleted_at" is not null) and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, []any{1, 1}},
		{"S2 whereHas inside the callback", owner().WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Leaves")
		}), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, []any{1}},
		{"S3 where closure with whereHas", owner().WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.WhereHas("Leaves") })
		}), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null)) and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, []any{1}},
		{"S4 plain where closure", owner().WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.Where("id", 1) })
		}), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and ("id" = ?) and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, []any{1, 1}},
		{"S5 withCount where closure with onlyTrashed", owner().WithCount(contractsorm.RelationCount{Name: "Mids", Callback: func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.Where("id", 1).OnlyTrashed() })
		}}), `select "rel_deep_owners".*, (select count(*) from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and ("id" = ? and "rel_deep_mids"."deleted_at" is not null) and "rel_deep_mids"."deleted_at" is null and "active" = ?) as "mids_count" from "rel_deep_owners"`, []any{1, 1}},
	})
}

// scrOfManyOwner declares latestOfMany relations over relDeepMid (soft delete + the "active" user
// scope) whose removal or trashed switch is written AFTER OfMany, the order Laravel's
// latestOfMany()->withoutGlobalScope(...) has, plus one written before it (WtBefore) and a plain
// has-many whose default query calls onlyTrashed between two wheres (OtWhere).
type scrOfManyOwner struct {
	ID  uint
	Mid *relDeepMid `gorm:"-"`
}

func (scrOfManyOwner) TableName() string { return "rel_deep_owners" }

// scrActiveScope is the related model's "active" scope mounted explicitly: scopes mount on the
// builder with ApplyScope now, so the relation's default query mounts it the way the model
// used to declare it.
func scrActiveScope(q contractsorm.Query) contractsorm.Query { return q.Where("active", 1) }

func (scrOfManyOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"RmAfter": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.ApplyScope("active", scrActiveScope).OfMany("", "").WithoutGlobalScopes("active")
			}},
		"RmAllAfter": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.ApplyScope("active", scrActiveScope).OfMany("", "").WithoutGlobalScopes()
			}},
		"WtAfter": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.ApplyScope("active", scrActiveScope).OfMany("", "").WithTrashed()
			}},
		"OtAfter": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.ApplyScope("active", scrActiveScope).OfMany("", "").OnlyTrashed()
			}},
		"WtBefore": contractsorm.HasOne{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed().OfMany("", "") }},
		"OtWhere": contractsorm.HasMany{Related: &relDeepMid{}, ForeignKey: "owner_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.Where("id", 5).OnlyTrashed().Where("id", 6) }},
	}
}

// TestScopeComposition_RemovalAfterOfManyGovernsTheOuterQueryOnly pins R15-9: Laravel's
// newOneOfManySubQuery takes the removed scopes when ofMany runs, so a removal or trashed switch
// written after OfMany leaves the aggregate with the related model's full scopes and governs only
// the outer relation query - on the existence path (O1-O4), the count path (O7) and the relation
// read (L1-L3). A switch written before OfMany still reaches the aggregate (O5).
func TestScopeComposition_RemovalAfterOfManyGovernsTheOuterQueryOnly(t *testing.T) {
	owner := func() *OrmBuilder { return newRelQueryWith(t, &scrOfManyOwner{}) }

	runScrCases(t, []scrCase{
		{"O1 named removal after OfMany", owner().WhereHas("RmAfter"), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "rm_after" on "rm_after"."id_aggregate" = "rel_deep_mids"."id" and "rm_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null)`, []any{1}},
		{"O2 remove-all after OfMany", owner().WhereHas("RmAllAfter"), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "rm_all_after" on "rm_all_after"."id_aggregate" = "rel_deep_mids"."id" and "rm_all_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id")`, []any{1}},
		{"O3 withTrashed after OfMany", owner().WhereHas("WtAfter"), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "wt_after" on "wt_after"."id_aggregate" = "rel_deep_mids"."id" and "wt_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "active" = ?)`, []any{1, 1}},
		{"O4 onlyTrashed after OfMany", owner().WhereHas("OtAfter"), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "ot_after" on "ot_after"."id_aggregate" = "rel_deep_mids"."id" and "ot_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is not null and "active" = ?)`, []any{1, 1}},
		{"O5 withTrashed before OfMany", owner().WhereHas("WtBefore"), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "active" = ? group by "rel_deep_mids"."owner_id") as "wt_before" on "wt_before"."id_aggregate" = "rel_deep_mids"."id" and "wt_before"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "active" = ?)`, []any{1, 1}},
		{"O7 withCount named removal after OfMany", owner().WithCount("RmAfter"), `select "rel_deep_owners".*, (select count(*) from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "rm_after" on "rm_after"."id_aggregate" = "rel_deep_mids"."id" and "rm_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "rel_deep_mids"."deleted_at" is null) as "rm_after_count" from "rel_deep_owners"`, []any{1}},
	})

	reads := []struct {
		name     string
		relation string
		sql      string
	}{
		{"L1 named removal after OfMany", "RmAfter", `select "rel_deep_mids".* from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "rm_after" on "rm_after"."id_aggregate" = "rel_deep_mids"."id" and "rm_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "rel_deep_mids"."deleted_at" is null`},
		{"L2 onlyTrashed after OfMany", "OtAfter", `select "rel_deep_mids".* from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "ot_after" on "ot_after"."id_aggregate" = "rel_deep_mids"."id" and "ot_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "rel_deep_mids"."deleted_at" is not null and "active" = ?`},
		{"L3 withTrashed after OfMany", "WtAfter", `select "rel_deep_mids".* from "rel_deep_mids" inner join (select MAX("rel_deep_mids"."id") as "id_aggregate", "rel_deep_mids"."owner_id" from "rel_deep_mids" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "rel_deep_mids"."deleted_at" is null and "active" = ? group by "rel_deep_mids"."owner_id") as "wt_after" on "wt_after"."id_aggregate" = "rel_deep_mids"."id" and "wt_after"."owner_id" = "rel_deep_mids"."owner_id" where "rel_deep_mids"."owner_id" = ? and "rel_deep_mids"."owner_id" is not null and "active" = ?`},
	}
	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			rel := newRelQueryWith(t, &relDeepMid{}).Related(&scrOfManyOwner{ID: 1}, read.relation)
			assert.Equal(t, read.sql, newRelationSQL(t, rel, &[]relDeepMid{}))
		})
	}
}

// scrKindTag is the related model of every kind below: framework soft delete plus an "active" user
// scope; scrKindUser reaches it through BelongsTo, Many2Many, MorphToMany, HasOneThrough and
// HasManyThrough (through scrKindMid).
type scrKindTag struct {
	ID      uint
	RkMidID uint
	Active  int
	databaseorm.SoftDeletes
}

func (scrKindTag) TableName() string { return "rk_tags" }

func (scrKindTag) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "active", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("active", 1) }},
	}
}

type scrKindMid struct {
	ID       uint
	RkUserID uint
}

func (scrKindMid) TableName() string { return "rk_mids" }

type scrKindUser struct {
	ID          uint
	RkTagID     uint
	Tag         *scrKindTag   `gorm:"-"`
	Tags        []*scrKindTag `gorm:"-"`
	Mtags       []*scrKindTag `gorm:"-"`
	ThroughTag  *scrKindTag   `gorm:"-"`
	ThroughTags []*scrKindTag `gorm:"-"`
}

func (scrKindUser) TableName() string { return "rk_users" }

func (scrKindUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tag":         contractsorm.BelongsTo{Related: &scrKindTag{}, ForeignKey: "rk_tag_id"},
		"Tags":        contractsorm.Many2Many{Related: &scrKindTag{}, Table: "rk_tag_user", ForeignPivotKey: "rk_user_id", RelatedPivotKey: "rk_tag_id"},
		"Mtags":       contractsorm.MorphToMany{Related: &scrKindTag{}, Name: "taggable", Table: "rk_taggables", ForeignPivotKey: "taggable_id", RelatedPivotKey: "rk_tag_id"},
		"ThroughTag":  contractsorm.HasOneThrough{Related: &scrKindTag{}, Through: &scrKindMid{}, FirstKey: "rk_user_id", SecondKey: "rk_mid_id"},
		"ThroughTags": contractsorm.HasManyThrough{Related: &scrKindTag{}, Through: &scrKindMid{}, FirstKey: "rk_user_id", SecondKey: "rk_mid_id"},
	}
}

// TestScopeComposition_CallerRemovalsAcrossRelationKinds pins R15-10: a caller closure's removals
// govern the related model's scopes inside the whereHas / withCount body of every remaining kind
// exactly as for HasMany - withTrashed and a SoftDeletingScope removal drop the "is null", a named
// removal drops that user scope, withoutGlobalScopes() drops both, and onlyTrashed writes its
// "is not null" at its call position.
func TestScopeComposition_CallerRemovalsAcrossRelationKinds(t *testing.T) {
	callbacks := map[string]func(contractsorm.Query) contractsorm.Query{
		"none":           nil,
		"withTrashed":    func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() },
		"onlyTrashedPos": func(q contractsorm.Query) contractsorm.Query { return q.Where("id", 1).OnlyTrashed().Where("id", 2) },
		"rmActive":       func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes("active") },
		"rmSoftDelete": func(q contractsorm.Query) contractsorm.Query {
			return q.WithoutGlobalScopes(contractsorm.SoftDeletingScope)
		},
		"rmAll": func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes() },
	}
	user := func() *OrmBuilder { return newRelQueryWith(t, &scrKindUser{}) }
	has := func(relation, callback string) contractsorm.Query {
		if callbacks[callback] == nil {
			return user().WhereHas(relation)
		}

		return user().WhereHas(relation, callbacks[callback])
	}
	count := func(relation, callback string) contractsorm.Query {
		return user().WithCount(contractsorm.RelationCount{Name: relation, Callback: callbacks[callback]})
	}

	runScrCases(t, []scrCase{
		{"H tag none", has("Tag", "none"), `select * from "rk_users" where exists (select * from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id" and "rk_tags"."deleted_at" is null and "active" = ?)`, []any{1}},
		{"H tag withTrashed", has("Tag", "withTrashed"), `select * from "rk_users" where exists (select * from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id" and "active" = ?)`, []any{1}},
		{"H tag onlyTrashedPos", has("Tag", "onlyTrashedPos"), `select * from "rk_users" where exists (select * from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?)`, []any{1, 2, 1}},
		{"H tag rmActive", has("Tag", "rmActive"), `select * from "rk_users" where exists (select * from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id" and "rk_tags"."deleted_at" is null)`, nil},
		{"H tag rmSoftDelete", has("Tag", "rmSoftDelete"), `select * from "rk_users" where exists (select * from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id" and "active" = ?)`, []any{1}},
		{"H tag rmAll", has("Tag", "rmAll"), `select * from "rk_users" where exists (select * from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id")`, nil},
		{"C tag onlyTrashedPos", count("Tag", "onlyTrashedPos"), `select "rk_users".*, (select count(*) from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?) as "tag_count" from "rk_users"`, []any{1, 2, 1}},
		{"C tag rmActive", count("Tag", "rmActive"), `select "rk_users".*, (select count(*) from "rk_tags" where "rk_users"."rk_tag_id" = "rk_tags"."id" and "rk_tags"."deleted_at" is null) as "tag_count" from "rk_users"`, nil},
		{"H tags none", has("Tags", "none"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id" and "rk_tags"."deleted_at" is null and "active" = ?)`, []any{1}},
		{"H tags withTrashed", has("Tags", "withTrashed"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id" and "active" = ?)`, []any{1}},
		{"H tags onlyTrashedPos", has("Tags", "onlyTrashedPos"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?)`, []any{1, 2, 1}},
		{"H tags rmActive", has("Tags", "rmActive"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id" and "rk_tags"."deleted_at" is null)`, nil},
		{"H tags rmSoftDelete", has("Tags", "rmSoftDelete"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id" and "active" = ?)`, []any{1}},
		{"H tags rmAll", has("Tags", "rmAll"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id")`, nil},
		{"C tags onlyTrashedPos", count("Tags", "onlyTrashedPos"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?) as "tags_count" from "rk_users"`, []any{1, 2, 1}},
		{"C tags rmActive", count("Tags", "rmActive"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_tag_user" on "rk_tags"."id" = "rk_tag_user"."rk_tag_id" where "rk_users"."id" = "rk_tag_user"."rk_user_id" and "rk_tags"."deleted_at" is null) as "tags_count" from "rk_users"`, nil},
		{"H mtags none", has("Mtags", "none"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ? and "rk_tags"."deleted_at" is null and "active" = ?)`, []any{"rk_users", 1}},
		{"H mtags withTrashed", has("Mtags", "withTrashed"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ? and "active" = ?)`, []any{"rk_users", 1}},
		{"H mtags onlyTrashedPos", has("Mtags", "onlyTrashedPos"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ? and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?)`, []any{"rk_users", 1, 2, 1}},
		{"H mtags rmActive", has("Mtags", "rmActive"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ? and "rk_tags"."deleted_at" is null)`, []any{"rk_users"}},
		{"H mtags rmSoftDelete", has("Mtags", "rmSoftDelete"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ? and "active" = ?)`, []any{"rk_users", 1}},
		{"H mtags rmAll", has("Mtags", "rmAll"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ?)`, []any{"rk_users"}},
		{"C mtags onlyTrashedPos", count("Mtags", "onlyTrashedPos"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ? and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?) as "mtags_count" from "rk_users"`, []any{"rk_users", 1, 2, 1}},
		{"C mtags rmActive", count("Mtags", "rmActive"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_taggables" on "rk_tags"."id" = "rk_taggables"."rk_tag_id" where "rk_users"."id" = "rk_taggables"."taggable_id" and "rk_taggables"."taggable_type" = ? and "rk_tags"."deleted_at" is null) as "mtags_count" from "rk_users"`, []any{"rk_users"}},
		{"H throughTag none", has("ThroughTag", "none"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "rk_tags"."deleted_at" is null and "active" = ?)`, []any{1}},
		{"H throughTag withTrashed", has("ThroughTag", "withTrashed"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "active" = ?)`, []any{1}},
		{"H throughTag onlyTrashedPos", has("ThroughTag", "onlyTrashedPos"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?)`, []any{1, 2, 1}},
		{"H throughTag rmActive", has("ThroughTag", "rmActive"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "rk_tags"."deleted_at" is null)`, nil},
		{"H throughTag rmSoftDelete", has("ThroughTag", "rmSoftDelete"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "active" = ?)`, []any{1}},
		{"H throughTag rmAll", has("ThroughTag", "rmAll"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id")`, nil},
		{"C throughTag onlyTrashedPos", count("ThroughTag", "onlyTrashedPos"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?) as "through_tag_count" from "rk_users"`, []any{1, 2, 1}},
		{"C throughTag rmActive", count("ThroughTag", "rmActive"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "rk_tags"."deleted_at" is null) as "through_tag_count" from "rk_users"`, nil},
		{"H throughTags none", has("ThroughTags", "none"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "rk_tags"."deleted_at" is null and "active" = ?)`, []any{1}},
		{"H throughTags withTrashed", has("ThroughTags", "withTrashed"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "active" = ?)`, []any{1}},
		{"H throughTags onlyTrashedPos", has("ThroughTags", "onlyTrashedPos"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?)`, []any{1, 2, 1}},
		{"H throughTags rmActive", has("ThroughTags", "rmActive"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "rk_tags"."deleted_at" is null)`, nil},
		{"H throughTags rmSoftDelete", has("ThroughTags", "rmSoftDelete"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "active" = ?)`, []any{1}},
		{"H throughTags rmAll", has("ThroughTags", "rmAll"), `select * from "rk_users" where exists (select * from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id")`, nil},
		{"C throughTags onlyTrashedPos", count("ThroughTags", "onlyTrashedPos"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "id" = ? and "rk_tags"."deleted_at" is not null and "id" = ? and "active" = ?) as "through_tags_count" from "rk_users"`, []any{1, 2, 1}},
		{"C throughTags rmActive", count("ThroughTags", "rmActive"), `select "rk_users".*, (select count(*) from "rk_tags" inner join "rk_mids" on "rk_mids"."id" = "rk_tags"."rk_mid_id" where "rk_users"."id" = "rk_mids"."rk_user_id" and "rk_tags"."deleted_at" is null) as "through_tags_count" from "rk_users"`, nil},
	})
}

type scrEagerPost struct {
	ID      uint
	UserID  uint
	Visible int
	databaseorm.SoftDeletes
}

func (scrEagerPost) TableName() string { return "ro_posts" }

// scrVisibleScope is the "visible" scope the eager fixtures used to declare on the related model;
// the relations below mount it on their own query, which is where the scope belongs now.
func scrVisibleScope(q contractsorm.Query) contractsorm.Query { return q.Where("visible", 1) }

type scrEagerUser struct {
	ID    uint
	Posts []*scrEagerPost `gorm:"-"`
	Post  *scrEagerPost   `gorm:"-"`
}

func (scrEagerUser) TableName() string { return "ro_users" }

func (scrEagerUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &scrEagerPost{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.ApplyScope("visible", scrVisibleScope) }},
		"Post": contractsorm.HasOne{Related: &scrEagerPost{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.ApplyScope("visible", scrVisibleScope) }},
	}
}

// TestScopeComposition_PlainEagerReadOfASoftDeletableScopedModel pins the plain (non-of-many) eager
// read of a relation whose related model carries the framework soft delete and whose own query
// carries a mounted user scope: the soft-delete predicate, as the first mounted rule, precedes the
// scope after the eager key list (oracle
// E1/E2: `where "ro_posts"."user_id" in (1) and "ro_posts"."deleted_at" is null and "visible" = ?`;
// the port binds the eager keys, the shape every eager pin in this package asserts).
func TestScopeComposition_PlainEagerReadOfASoftDeletableScopedModel(t *testing.T) {
	for _, tc := range []struct {
		relation string
		isMany   bool
	}{{"Posts", true}, {"Post", false}} {
		t.Run(tc.relation, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, &scrEagerUser{})
			desc, err := relations.ResolveRelation(&scrEagerUser{}, tc.relation)
			require.NoError(t, err)

			parents := []reflect.Value{reflect.ValueOf(&scrEagerUser{ID: 1}).Elem()}
			require.NoError(t, q.loadHasOneOrMany(parents, &scrEagerUser{}, desc, eagerLoadEntry{Relation: tc.relation}, nil, tc.isMany))

			recorded := recorder.last(t)
			assert.Equal(t, `select * from "ro_posts" where "ro_posts"."user_id" in (?) and "ro_posts"."deleted_at" is null and "visible" = ?`, recorded.sql)
			assert.Equal(t, driverBindings([]any{uint(1), 1}), recorded.args)
		})
	}
}

type scrThroughComment struct {
	ID      uint
	TPostID uint
	databaseorm.SoftDeletes
}

func (scrThroughComment) TableName() string { return "t_comments" }

// scrThroughPost is the through model: framework soft delete plus a "published" user scope, which
// Laravel's performJoin does not carry onto the through read (only the soft-delete named scope).
type scrThroughPost struct {
	ID        uint
	TUserID   uint
	Published string
	databaseorm.SoftDeletes
}

func (scrThroughPost) TableName() string { return "t_posts" }

func (scrThroughPost) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "published", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("published", "yes") }},
	}
}

type scrThroughUser struct {
	ID            uint
	Comments      []*scrThroughComment `gorm:"-"`
	LatestComment *scrThroughComment   `gorm:"-"`
	databaseorm.SoftDeletes
}

func (scrThroughUser) TableName() string { return "t_users" }

func (scrThroughUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Comments": contractsorm.HasManyThrough{Related: &scrThroughComment{}, Through: &scrThroughPost{}, FirstKey: "t_user_id", SecondKey: "t_post_id"},
		"LatestComment": contractsorm.HasOneThrough{Related: &scrThroughComment{}, Through: &scrThroughPost{}, FirstKey: "t_user_id", SecondKey: "t_post_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") }},
	}
}

// TestScopeComposition_EagerThroughReadLeavesTheThroughUserScopesOut pins that a through model's
// user global scopes reach neither the eager through read nor its of-many aggregate - Laravel's
// performJoin adds only the named soft-delete scope for the through model (oracle T6 / T5:
// `... "t_posts"."t_user_id" in (1, 2) and "t_comments"."deleted_at" is null and
// "t_posts"."deleted_at" is null`, no `"published" = ?` anywhere).
func TestScopeComposition_EagerThroughReadLeavesTheThroughUserScopesOut(t *testing.T) {
	parents := func() []reflect.Value {
		return []reflect.Value{
			reflect.ValueOf(&scrThroughUser{ID: 1}).Elem(),
			reflect.ValueOf(&scrThroughUser{ID: 2}).Elem(),
		}
	}

	t.Run("T6 plain through", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &scrThroughUser{})
		desc, err := relations.ResolveRelation(&scrThroughUser{}, "Comments")
		require.NoError(t, err)

		recorder.returnRows(`from "t_posts"`, []string{desc.FirstKey, desc.SecondLocalKey}, []driver.Value{int64(1), int64(1)})
		require.NoError(t, q.loadThrough(parents(), &scrThroughUser{}, desc, eagerLoadEntry{Relation: "Comments"}, nil, true))

		related := recorder.last(t)
		assert.Equal(t, `select "t_comments".*, "t_posts"."t_user_id" as "laravel_through_key" from "t_comments" inner join "t_posts" on "t_posts"."id" = "t_comments"."t_post_id" where "t_posts"."t_user_id" in (?, ?) and "t_comments"."deleted_at" is null and "t_posts"."deleted_at" is null`, related.sql)
		assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), related.args)
	})

	t.Run("T5 of-many through", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &scrThroughUser{})
		desc, err := relations.ResolveRelation(&scrThroughUser{}, "LatestComment")
		require.NoError(t, err)

		recorder.returnRows(`from "t_posts"`, []string{desc.FirstKey, desc.SecondLocalKey}, []driver.Value{int64(1), int64(1)})
		require.NoError(t, q.loadThrough(parents(), &scrThroughUser{}, desc, eagerLoadEntry{Relation: "LatestComment"}, nil, false))

		// The statement from its from clause on is the oracle's (alias aside). Its select list lacks
		// Laravel's `"t_posts"."t_user_id" as "laravel_through_key"` - a separate select-list gap of
		// the eager of-many through read, not a scope-composition one, so it is left out of this pin.
		related := recorder.last(t)
		assert.True(t, strings.HasSuffix(related.sql, ` from "t_comments" inner join "t_posts" on "t_posts"."id" = "t_comments"."t_post_id" inner join (select MAX("t_comments"."id") as "id_aggregate", "t_posts"."t_user_id" from "t_comments" inner join "t_posts" on "t_posts"."id" = "t_comments"."t_post_id" where "t_posts"."t_user_id" in (1, 2) and "t_comments"."deleted_at" is null and "t_posts"."deleted_at" is null group by "t_posts"."t_user_id") as "latest_comment" on "latest_comment"."id_aggregate" = "t_comments"."id" and "latest_comment"."t_user_id" = "t_posts"."t_user_id" where "t_comments"."deleted_at" is null and "t_posts"."deleted_at" is null`), related.sql)
		assert.NotContains(t, related.sql, `"published"`)
	})
}

// TestScopeComposition_WhereClosureDoesNotInheritTheParentExistenceClauses pins that a where
// closure's nested group carries only the existence clauses the closure wrote itself: Laravel hands
// the closure a fresh builder (newQueryWithoutRelationships), so a whereHas / has the parent called
// before the closure stays where the parent wrote it and is not rendered a second time inside the
// group - on the main path (D1, D2, B1) and inside a whereHas callback (C5, C6). Oracle
// (illuminate/database v12.55.1, real sqlite):
// /Users/dog/.claude/jobs/013c27a0/tmp/r15b-verify-probes/inherit.php -> inherit.out.
func TestScopeComposition_WhereClosureDoesNotInheritTheParentExistenceClauses(t *testing.T) {
	sd := func() *OrmBuilder { return compiledQuery(t, &scpSdUser{}, Conditions{}) }
	owner := func() *OrmBuilder { return newRelQueryWith(t, &relDeepOwner{}) }
	posts := `exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)`

	runScrCases(t, []scrCase{
		{"D1 whereHas then a plain where closure", sd().WhereHas("Posts").Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a")
		}), `select * from "sd_users" where ` + posts + ` and ("email" = ?) and "sd_users"."deleted_at" is null`, []any{"a"}},
		{"D2 has then an or closure carrying an or", sd().Has("Posts").OrWhere(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a").OrWhere("email", "b")
		}), `select * from "sd_users" where (` + posts + ` or ("email" = ? or "email" = ?)) and "sd_users"."deleted_at" is null`, []any{"a", "b"}},
		{"B1 whereHas then a closure with its own whereHas", sd().WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 2) }).Where(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("email", "a").WhereHas("Posts", func(p contractsorm.Query) contractsorm.Query { return p.Where("id", 3) })
		}).Where("id", 4), `select * from "sd_users" where exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null) and ("email" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null)) and "id" = ? and "sd_users"."deleted_at" is null`, []any{2, "a", 3, 4}},
		{"C5 callback whereHas then a plain where closure", owner().WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Leaves").Where(func(q2 contractsorm.Query) contractsorm.Query { return q2.Where("id", 1) })
		}), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) and ("id" = ?) and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, []any{1, 1}},
		{"C6 callback whereHas then an or closure", owner().WhereHas("Mids", func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Leaves", func(l contractsorm.Query) contractsorm.Query { return l.Where("a", 2) }).OrWhere(func(q2 contractsorm.Query) contractsorm.Query {
				return q2.Where("id", 1).OrWhere("id", 2)
			})
		}), `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves"."deleted_at" is null) or ("id" = ? or "id" = ?)) and "rel_deep_mids"."deleted_at" is null and "active" = ?)`, []any{2, 1, 2, 1}},
	})
}

// scrTrashedScopePost is a related model whose user global scope calls onlyTrashed; scrTrashedScopeUser
// reaches it through a plain has-many and a latestOfMany has-one.
type scrTrashedScopePost struct {
	ID       uint
	SdUserID uint
	databaseorm.SoftDeletes
}

func (scrTrashedScopePost) TableName() string { return "sd_posts" }

// scrTrashedOnlyPostScope is the "trashedOnly" scope of the related fixtures. The model still
// declares it, because the existence / count bodies read it through the related-model replay
// (applyRelatedGlobalScopes, removed in the next step); the lazy reads below mount it on the relation
// query, which is where a plain read picks a scope up now.
func scrTrashedOnlyPostScope(q contractsorm.Query) contractsorm.Query {
	return q.Where("id", ">", 0).OnlyTrashed()
}

func (scrTrashedScopePost) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "trashedOnly", Scope: scrTrashedOnlyPostScope},
	}
}

// scrTrashedOrScopePost's first scope calls onlyTrashed inside its own or-chain, and a second scope
// follows it.
type scrTrashedOrScopePost struct {
	ID       uint
	SdUserID uint
	databaseorm.SoftDeletes
}

func (scrTrashedOrScopePost) TableName() string { return "sd_posts" }

func (scrTrashedOrScopePost) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "orTrashed", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("id", 1).OrWhere("id", 2).OnlyTrashed() }},
		{Name: "second", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("id", "<", 9) }},
	}
}

type scrTrashedScopeUser struct {
	ID          uint
	Email       string
	Tposts      []*scrTrashedScopePost   `gorm:"-"`
	Oposts      []*scrTrashedOrScopePost `gorm:"-"`
	LatestTpost *scrTrashedScopePost     `gorm:"-"`
	databaseorm.SoftDeletes
}

func (scrTrashedScopeUser) TableName() string { return "sd_users" }

func (scrTrashedScopeUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tposts": contractsorm.HasMany{Related: &scrTrashedScopePost{}, ForeignKey: "sd_user_id"},
		"Oposts": contractsorm.HasMany{Related: &scrTrashedOrScopePost{}, ForeignKey: "sd_user_id"},
		"LatestTpost": contractsorm.HasOne{Related: &scrTrashedScopePost{}, ForeignKey: "sd_user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") }},
	}
}

// TestScopeComposition_RelatedScopeCallingOnlyTrashedInsideASubquery pins G1's shape on the related
// side: a related model's user scope that calls onlyTrashed runs after SoftDeletingScope inside the
// existence / count body and the of-many aggregate, so the "is null" stays and the macro's "is not
// null" lands among the scope's own wheres at its call position - inside the scope's or-group when
// it has one, ahead of the next scope (E10) - (E1-E3, E5-E8), exactly as the lazy
// relation read composes it (E4, E9). Oracle (illuminate/database v12.55.1, real sqlite):
// /Users/dog/.claude/jobs/013c27a0/tmp/r15b-verify-probes/paths.php -> paths.out (E1-E4) and
// related_scope.php -> related_scope.out (E5-E10); the of-many rows use the port's join alias.
func TestScopeComposition_RelatedScopeCallingOnlyTrashedInsideASubquery(t *testing.T) {
	user := func() *OrmBuilder { return compiledQuery(t, &scrTrashedScopeUser{}, Conditions{}) }
	correlate := `"sd_users"."id" = "sd_posts"."sd_user_id"`

	runScrCases(t, []scrCase{
		{"E1 whereHas", user().WhereHas("Tposts"), `select * from "sd_users" where exists (select * from "sd_posts" where ` + correlate + ` and "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, []any{0}},
		{"E2 withCount", user().WithCount("Tposts"), `select "sd_users".*, (select count(*) from "sd_posts" where ` + correlate + ` and "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null) as "tposts_count" from "sd_users" where "sd_users"."deleted_at" is null`, []any{0}},
		{"E3 whereHas with a callback", user().WhereHas("Tposts", func(q contractsorm.Query) contractsorm.Query { return q.Where("id", 5) }), `select * from "sd_users" where exists (select * from "sd_posts" where ` + correlate + ` and "id" = ? and "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, []any{5, 0}},
		{"E5 caller withTrashed", user().WhereHas("Tposts", func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() }), `select * from "sd_users" where exists (select * from "sd_posts" where ` + correlate + ` and "id" > ? and "sd_posts"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, []any{0}},
		{"E6 the scope removed by name", user().WhereHas("Tposts", func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes("trashedOnly") }), `select * from "sd_users" where exists (select * from "sd_posts" where ` + correlate + ` and "sd_posts"."deleted_at" is null) and "sd_users"."deleted_at" is null`, nil},
		{"E7 caller onlyTrashed too", user().WhereHas("Tposts", func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed().Where("id", 4) }), `select * from "sd_users" where exists (select * from "sd_posts" where ` + correlate + ` and "sd_posts"."deleted_at" is not null and "id" = ? and "id" > ? and "sd_posts"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, []any{4, 0}},
		{"E8 whereHas of-many", user().WhereHas("LatestTpost"), `select * from "sd_users" where exists (select * from "sd_posts" inner join (select MAX("sd_posts"."id") as "id_aggregate", "sd_posts"."sd_user_id" from "sd_posts" where "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null group by "sd_posts"."sd_user_id") as "latest_tpost" on "latest_tpost"."id_aggregate" = "sd_posts"."id" and "latest_tpost"."sd_user_id" = "sd_posts"."sd_user_id" where ` + correlate + ` and "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null) and "sd_users"."deleted_at" is null`, []any{0, 0}},
		{"E10 the scope's or-group, then a second scope", user().WhereHas("Oposts", func(q contractsorm.Query) contractsorm.Query { return q.Where("sd_user_id", 3) }), `select * from "sd_users" where exists (select * from "sd_posts" where ` + correlate + ` and "sd_user_id" = ? and "sd_posts"."deleted_at" is null and ("id" = ? or "id" = ? and "sd_posts"."deleted_at" is not null) and "id" < ?) and "sd_users"."deleted_at" is null`, []any{3, 1, 2, 9}},
	})

	t.Run("E4 lazy read", func(t *testing.T) {
		rel := newRelQueryWith(t, &scrTrashedScopePost{}).Related(&scrTrashedScopeUser{ID: 1}, "Tposts")
		// The scope the related model used to declare mounts on the relation query here: the lazy
		// read is a plain read, and a plain read carries the mounts the builder has.
		built, ok := ormBuilderOf(rel)
		require.True(t, ok)
		built.ApplyScope("trashedOnly", scrTrashedOnlyPostScope)
		assert.Equal(t, `select * from "sd_posts" where "sd_posts"."sd_user_id" = ? and "sd_posts"."sd_user_id" is not null and "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null`, newRelationSQL(t, rel, &[]scrTrashedScopePost{}))
	})
	t.Run("E9 lazy of-many read", func(t *testing.T) {
		rel := newRelQueryWith(t, &scrTrashedScopePost{}).Related(&scrTrashedScopeUser{ID: 1}, "LatestTpost")
		built, ok := ormBuilderOf(rel)
		require.True(t, ok)
		built.ApplyScope("trashedOnly", scrTrashedOnlyPostScope)
		assert.Equal(t, `select "sd_posts".* from "sd_posts" inner join (select MAX("sd_posts"."id") as "id_aggregate", "sd_posts"."sd_user_id" from "sd_posts" where "sd_posts"."sd_user_id" = ? and "sd_posts"."sd_user_id" is not null and "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null group by "sd_posts"."sd_user_id") as "latest_tpost" on "latest_tpost"."id_aggregate" = "sd_posts"."id" and "latest_tpost"."sd_user_id" = "sd_posts"."sd_user_id" where "sd_posts"."sd_user_id" = ? and "sd_posts"."sd_user_id" is not null and "sd_posts"."deleted_at" is null and "id" > ? and "sd_posts"."deleted_at" is not null`, newRelationSQL(t, rel, &[]scrTrashedScopePost{}))
	})
}
