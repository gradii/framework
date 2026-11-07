package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// The built-in soft-delete predicate is composed as Laravel's SoftDeletingScope: the first global
// scope applyScopes applies (bootSoftDeletes registers it before any booted() scope), through
// callScope's addNewWheresWithinGroup, so the wheres written before it are wrapped in one group
// exactly when they carry an "or", and a user scope applies after it. onlyTrashed is a macro, not a
// scope: its whereNotNull is written at call position and nothing is regrouped for it.
//
// Oracle (illuminate/database v12.55.1, real sqlite): /tmp/r14si/probe.php -> /tmp/r14si/probe.out,
// labels quoted next to each case.

type scpPost struct {
	ID       uint
	SdUserID uint
	databaseorm.SoftDeletes
}

func (scpPost) TableName() string { return "sd_posts" }

type scpSdUser struct {
	ID    uint
	Email string
	Posts []*scpPost `gorm:"-"`
	databaseorm.SoftDeletes
}

func (scpSdUser) TableName() string { return "sd_users" }

func (scpSdUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &scpPost{}, ForeignKey: "sd_user_id"},
	}
}

type scpSrUser struct {
	ID     uint
	Email  string
	Active int
	databaseorm.SoftDeletes
}

func (scpSrUser) TableName() string { return "sr_users" }

func (scpSrUser) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "active", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("active", 1) }},
	}
}

type scpSoUser struct {
	ID     uint
	Email  string
	Region string
	databaseorm.SoftDeletes
}

func (scpSoUser) TableName() string { return "so_users" }

func (scpSoUser) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "region", Scope: func(q contractsorm.Query) contractsorm.Query {
			return q.Where("region", "x").OrWhere("region", "y")
		}},
	}
}

func scpSelect(t *testing.T, q contractsorm.Query) (string, []any) {
	t.Helper()

	statement, bindings, err := q.(*OrmBuilder).addGlobalScopes().compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

func TestSoftDeleteScopeComposition_Select(t *testing.T) {
	sd := func() *OrmBuilder { return compiledQuery(t, &scpSdUser{}, Conditions{}) }
	sr := func() *OrmBuilder { return compiledQuery(t, &scpSrUser{}, Conditions{}) }
	so := func() *OrmBuilder { return compiledQuery(t, &scpSoUser{}, Conditions{}) }

	cases := []struct {
		name     string
		query    contractsorm.Query
		sql      string
		bindings []any
	}{
		// P1: no or, flat.
		{"P1 flat", sd().Where("email", "a"),
			`select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is null`, []any{"a"}},
		// P2: an or-chain is grouped.
		{"P2 grouped", sd().Where("email", "a").OrWhere("email", "b"),
			`select * from "sd_users" where ("email" = ? or "email" = ?) and "sd_users"."deleted_at" is null`, []any{"a", "b"}},
		// P3: withTrashed removes the scope, nothing is regrouped.
		{"P3 withTrashed ungrouped", sd().WithTrashed().Where("email", "a").OrWhere("email", "b"),
			`select * from "sd_users" where "email" = ? or "email" = ?`, []any{"a", "b"}},
		// P4: onlyTrashed is written at call position, ungrouped.
		{"P4 onlyTrashed ungrouped", sd().OnlyTrashed().Where("email", "a").OrWhere("email", "b"),
			`select * from "sd_users" where "sd_users"."deleted_at" is not null and "email" = ? or "email" = ?`, []any{"a", "b"}},
		// P5: onlyTrashed after a where keeps its call position.
		{"P5 onlyTrashed call position", sd().Where("email", "a").OnlyTrashed(),
			`select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is not null`, []any{"a"}},
		// P6: the existence clause is part of the grouped slice.
		{"P6 or + whereHas grouped together", sd().Where("email", "a").OrWhere("email", "b").WhereHas("Posts"),
			`select * from "sd_users" where ("email" = ? or "email" = ? and exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, []any{"a", "b"}},
		// P7
		{"P7 whereHas or orWhereHas grouped", sd().WhereHas("Posts").OrWhereHas("Posts"),
			`select * from "sd_users" where (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null) or exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)) and "sd_users"."deleted_at" is null`, nil},
		// S1-S6: the predicate precedes the user scope.
		{"S1 scope only", sr(),
			`select * from "sr_users" where "sr_users"."deleted_at" is null and "active" = ?`, []any{1}},
		{"S2 scope where", sr().Where("email", "a"),
			`select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is null and "active" = ?`, []any{"a", 1}},
		{"S3 scope orWhere", sr().Where("email", "a").OrWhere("email", "b"),
			`select * from "sr_users" where ("email" = ? or "email" = ?) and "sr_users"."deleted_at" is null and "active" = ?`, []any{"a", "b", 1}},
		{"S4 scope withTrashed", sr().WithTrashed().Where("email", "a"),
			`select * from "sr_users" where "email" = ? and "active" = ?`, []any{"a", 1}},
		{"S5 scope onlyTrashed", sr().OnlyTrashed().Where("email", "a"),
			`select * from "sr_users" where "sr_users"."deleted_at" is not null and "email" = ? and "active" = ?`, []any{"a", 1}},
		{"S6 scope onlyTrashed or grouped by the user scope", sr().OnlyTrashed().Where("email", "a").OrWhere("email", "b"),
			`select * from "sr_users" where ("sr_users"."deleted_at" is not null and "email" = ? or "email" = ?) and "active" = ?`, []any{"a", "b", 1}},
		// O1-O3: a user scope containing an or.
		{"O1 or-scope only", so(),
			`select * from "so_users" where "so_users"."deleted_at" is null and ("region" = ? or "region" = ?)`, []any{"x", "y"}},
		{"O2 or-scope where", so().Where("email", "a"),
			`select * from "so_users" where "email" = ? and "so_users"."deleted_at" is null and ("region" = ? or "region" = ?)`, []any{"a", "x", "y"}},
		{"O3 or-scope orWhere", so().Where("email", "a").OrWhere("email", "b"),
			`select * from "so_users" where ("email" = ? or "email" = ?) and "so_users"."deleted_at" is null and ("region" = ? or "region" = ?)`, []any{"a", "b", "x", "y"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statement, bindings := scpSelect(t, tc.query)
			assert.Equal(t, tc.sql, statement)
			if tc.bindings == nil {
				assert.Empty(t, bindings)
			} else {
				assert.Equal(t, tc.bindings, bindings)
			}
		})
	}
}

// Q (update / soft-delete update): the write statements compose the predicate the same way.
func TestSoftDeleteScopeComposition_Writes(t *testing.T) {
	q := compiledQuery(t, &scpSdUser{}, Conditions{}).Where("email", "a").OrWhere("email", "b").(*OrmBuilder)

	statement, _, err := q.compileUpdate(map[string]any{"email": "z"})
	require.NoError(t, err)
	assert.Contains(t, statement, `where ("email" = ? or "email" = ?) and "sd_users"."deleted_at" is null`)

	statement, _, err = q.compileSoftDeleteUpdate(map[string]any{"deleted_at": "now"})
	require.NoError(t, err)
	assert.Contains(t, statement, `where ("email" = ? or "email" = ?) and "sd_users"."deleted_at" is null`)
}

type scpSrPost struct {
	ID       uint
	SrUserID uint
	databaseorm.SoftDeletes
}

func (scpSrPost) TableName() string { return "sr_posts" }

// scpSrPostsUser is scpSrUser with a posts relation, for onlyTrashed against an existence clause
// under a user scope.
type scpSrPostsUser struct {
	ID     uint
	Email  string
	Active int
	Posts  []*scpSrPost `gorm:"-"`
	databaseorm.SoftDeletes
}

func (scpSrPostsUser) TableName() string { return "sr_users" }

func (scpSrPostsUser) GlobalScopes() []contractsorm.GlobalScope {
	return scpSrUser{}.GlobalScopes()
}

func (scpSrPostsUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &scpSrPost{}, ForeignKey: "sr_user_id"},
	}
}

// scpSr2User registers two user scopes after the built-in SoftDeletingScope.
type scpSr2User struct {
	ID     uint
	Email  string
	Active int
	databaseorm.SoftDeletes
}

func (scpSr2User) TableName() string { return "sr_users" }

func (scpSr2User) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "active", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("active", 1) }},
		{Name: "mail", Scope: func(q contractsorm.Query) contractsorm.Query { return q.WhereNotNull("email") }},
	}
}

// TestSoftDeleteScopeComposition_OrderAndOnlyTrashedPosition pins the two facets the round-14
// composition mutants target: the default predicate is applied before every user scope (not after
// them, not between them), and each onlyTrashed call writes its "is not null" at its call position -
// neither at the end of the where list nor through a scope regroup - ordered against the relation
// existence clauses by call order, including the tie where both were recorded at the same where
// count.
//
// Oracle (illuminate/database v12.55.1, real sqlite), probes and outputs under
// /Users/dog/.claude/jobs/013c27a0/tmp/r15-probes/r15-r14-mutants/: probe.out (O4, P2-P7),
// probe2.out (P8, P9), probe3.out (R1-R5), probe5.out (N1-N3; N4 below is the N2 closure behind a
// parent where+onlyTrashed, probe6.out); labels quoted next to each case.
func TestSoftDeleteScopeComposition_OrderAndOnlyTrashedPosition(t *testing.T) {
	sd := func() *OrmBuilder { return compiledQuery(t, &scpSdUser{}, Conditions{}) }
	sr := func() *OrmBuilder { return compiledQuery(t, &scpSrUser{}, Conditions{}) }
	srp := func() *OrmBuilder { return compiledQuery(t, &scpSrPostsUser{}, Conditions{}) }
	sr2 := func() *OrmBuilder { return compiledQuery(t, &scpSr2User{}, Conditions{}) }

	sdExists := `exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null)`
	srExists := `exists (select * from "sr_posts" where "sr_users"."id" = "sr_posts"."sr_user_id" and "sr_posts"."deleted_at" is null)`

	cases := []struct {
		name     string
		query    contractsorm.Query
		sql      string
		bindings []any
	}{
		{"O4 predicate precedes both user scopes", sr2().Where("email", "a"),
			`select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is null and "active" = ? and "email" is not null`, []any{"a", 1}},
		{"P9 onlyTrashed precedes both user scopes", sr2().OnlyTrashed().Where("email", "a"),
			`select * from "sr_users" where "sr_users"."deleted_at" is not null and "email" = ? and "active" = ? and "email" is not null`, []any{"a", 1}},
		{"P2 where then onlyTrashed", sr().Where("email", "a").OnlyTrashed(),
			`select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is not null and "active" = ?`, []any{"a", 1}},
		{"P3 onlyTrashed between two wheres", sr().Where("email", "a").OnlyTrashed().Where("id", 1),
			`select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is not null and "id" = ? and "active" = ?`, []any{"a", 1, 1}},
		{"P4 orWhere then onlyTrashed, grouped by the user scope", sr().Where("email", "a").OrWhere("email", "b").OnlyTrashed(),
			`select * from "sr_users" where ("email" = ? or "email" = ? and "sr_users"."deleted_at" is not null) and "active" = ?`, []any{"a", "b", 1}},
		{"P6 onlyTrashed inside an or-chain", sr().Where("email", "a").OnlyTrashed().OrWhere("email", "b"),
			`select * from "sr_users" where ("email" = ? and "sr_users"."deleted_at" is not null or "email" = ?) and "active" = ?`, []any{"a", "b", 1}},
		{"P7 onlyTrashed alone", sr().OnlyTrashed(),
			`select * from "sr_users" where "sr_users"."deleted_at" is not null and "active" = ?`, []any{1}},
		{"P8 each onlyTrashed call writes its own predicate", sr().Where("email", "a").OnlyTrashed().Where("id", 1).OnlyTrashed(),
			`select * from "sr_users" where "email" = ? and "sr_users"."deleted_at" is not null and "id" = ? and "sr_users"."deleted_at" is not null and "active" = ?`, []any{"a", 1, 1}},
		{"R1 onlyTrashed then whereHas", sd().OnlyTrashed().WhereHas("Posts"),
			`select * from "sd_users" where "sd_users"."deleted_at" is not null and ` + sdExists, nil},
		{"R2 whereHas then onlyTrashed", sd().WhereHas("Posts").OnlyTrashed(),
			`select * from "sd_users" where ` + sdExists + ` and "sd_users"."deleted_at" is not null`, nil},
		{"R3 where, onlyTrashed, whereHas", sd().Where("email", "a").OnlyTrashed().WhereHas("Posts"),
			`select * from "sd_users" where "email" = ? and "sd_users"."deleted_at" is not null and ` + sdExists, []any{"a"}},
		{"R4 whereHas, where, onlyTrashed", sd().WhereHas("Posts").Where("email", "a").OnlyTrashed(),
			`select * from "sd_users" where ` + sdExists + ` and "email" = ? and "sd_users"."deleted_at" is not null`, []any{"a"}},
		{"N1 closure onlyTrashed keeps its call position inside the group", sr().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed().Where("email", "a") }),
			`select * from "sr_users" where "id" = ? and ("sr_users"."deleted_at" is not null and "email" = ?) and "sr_users"."deleted_at" is null and "active" = ?`, []any{1, "a", 1}},
		{"N2 closure where then onlyTrashed", sr().Where("id", 1).Where(func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "a").OnlyTrashed() }),
			`select * from "sr_users" where "id" = ? and ("email" = ? and "sr_users"."deleted_at" is not null) and "sr_users"."deleted_at" is null and "active" = ?`, []any{1, "a", 1}},
		{"N3 parent onlyTrashed does not reach the closure group", sr().OnlyTrashed().Where(func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "a") }),
			`select * from "sr_users" where "sr_users"."deleted_at" is not null and ("email" = ?) and "active" = ?`, []any{"a", 1}},
		{"N4 parent and closure onlyTrashed each write their own", sr().Where("id", 1).OnlyTrashed().Where(func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "a").OnlyTrashed() }),
			`select * from "sr_users" where "id" = ? and "sr_users"."deleted_at" is not null and ("email" = ? and "sr_users"."deleted_at" is not null) and "active" = ?`, []any{1, "a", 1}},
		{"R5 onlyTrashed, whereHas, where under a user scope", srp().OnlyTrashed().WhereHas("Posts").Where("email", "a"),
			`select * from "sr_users" where "sr_users"."deleted_at" is not null and ` + srExists + ` and "email" = ? and "active" = ?`, []any{"a", 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statement, bindings := scpSelect(t, tc.query)
			assert.Equal(t, tc.sql, statement)
			if tc.bindings == nil {
				assert.Empty(t, bindings)
			} else {
				assert.Equal(t, tc.bindings, bindings)
			}
		})
	}
}
