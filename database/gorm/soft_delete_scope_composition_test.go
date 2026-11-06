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
