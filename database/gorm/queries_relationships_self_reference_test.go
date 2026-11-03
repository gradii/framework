package gorm

// Self-referencing existence SQL, pinned against the oracle
// (.claude/laravel-oracle/oracle.php, illuminate/database v12.55.1; probes under
// /tmp/selfref-oracle/). The oracle: a level whose table equals the enclosing from string wears
// `from "t" as "laravel_reserved_N"` with the enclosing references kept plain; a nested level
// compares its from against the enclosing from string - once that string is aliased the nested
// level keeps the plain name and correlates back to the alias; a three-level chain aliases again
// at the third level (laravel_reserved_1), because there the enclosing from is plain again; a
// wherePivot-scoped relation merges its pivot predicate into the exists body last.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

type selfRefUser struct {
	ID    uint
	Email string

	Friends []*selfRefUser `gorm:"-"`
	Roles   []*selfRefRole `gorm:"-"`
}

func (selfRefUser) TableName() string { return "users" }

func (selfRefUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Friends":    contractsorm.Many2Many{Related: &selfRefUser{}, Table: "friends", ForeignPivotKey: "user_id", RelatedPivotKey: "friend_id"},
		"FriendsOne": contractsorm.Many2Many{Related: &selfRefUser{}, Table: "friends", ForeignPivotKey: "user_id", RelatedPivotKey: "friend_id", OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery { return q.Where("user_id", 1) }},
		"FriendsTwo": contractsorm.Many2Many{Related: &selfRefUser{}, Table: "friends", ForeignPivotKey: "user_id", RelatedPivotKey: "friend_id", OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery { return q.Where("user_id", 2) }},
		"Roles":      contractsorm.Many2Many{Related: &selfRefRole{}, Table: "role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id"},
		"RolesScoped": contractsorm.Many2Many{Related: &selfRefRole{}, Table: "role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id", OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery {
			return q.Where("flag", 1)
		}},
	}
}

type selfRefRole struct {
	ID uint
}

func (selfRefRole) TableName() string { return "roles" }

type selfRefPost struct {
	ID       uint
	ParentID uint

	ChildPosts []*selfRefPost `gorm:"-"`
	ParentPost *selfRefPost   `gorm:"-"`
}

func (selfRefPost) TableName() string { return "posts" }

func (selfRefPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"ChildPosts": contractsorm.HasMany{Related: &selfRefPost{}, ForeignKey: "parent_id"},
		"ParentPost": contractsorm.BelongsTo{Related: &selfRefPost{}, ForeignKey: "parent_id"},
	}
}

// compileSelfRefExistence compiles the statement build() chains on the model's query. The alias
// counter is reset first: Laravel's Relation::$selfJoinCount is process-wide, so the oracle pins
// read with the index normalised (its first allocation per statement), which resetting here
// reproduces exactly.
func compileSelfRefExistence(t *testing.T, model any, build func(contractsorm.Query) contractsorm.Query) (string, []any) {
	t.Helper()

	query := build(compiledQuery(t, model, Conditions{}))
	selfJoinAliasCount.Store(0)

	statement, bindings, err := query.(*OrmBuilder).compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

func TestSelfReferencingExistence_BelongsToMany(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefUser{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("Friends")
	})

	assert.Equal(t, `select * from "users" where exists (select * from "users" as "laravel_reserved_0" inner join "friends" on "laravel_reserved_0"."id" = "friends"."friend_id" where "users"."id" = "friends"."user_id")`, statement)
	assert.Empty(t, bindings)
}

func TestSelfReferencingExistence_BelongsToManyWhereHasCallback(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefUser{}, func(q contractsorm.Query) contractsorm.Query {
		return q.WhereHas("Friends", func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "abigailotwell@gmail.com") })
	})

	assert.Equal(t, `select * from "users" where exists (select * from "users" as "laravel_reserved_0" inner join "friends" on "laravel_reserved_0"."id" = "friends"."friend_id" where "users"."id" = "friends"."user_id" and "email" = ?)`, statement)
	assert.Equal(t, []any{"abigailotwell@gmail.com"}, bindings)
}

func TestSelfReferencingExistence_BelongsToManyNested(t *testing.T) {
	statement, _ := compileSelfRefExistence(t, &selfRefUser{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("Friends.Friends")
	})

	assert.Equal(t, `select * from "users" where exists (select * from "users" as "laravel_reserved_0" inner join "friends" on "laravel_reserved_0"."id" = "friends"."friend_id" where "users"."id" = "friends"."user_id" and exists (select * from "users" inner join "friends" on "users"."id" = "friends"."friend_id" where "laravel_reserved_0"."id" = "friends"."user_id"))`, statement)
}

func TestSelfReferencingExistence_BelongsToManyNestedCallback(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefUser{}, func(q contractsorm.Query) contractsorm.Query {
		return q.WhereHas("Friends.Friends", func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "foo@gmail.com") })
	})

	assert.Equal(t, `select * from "users" where exists (select * from "users" as "laravel_reserved_0" inner join "friends" on "laravel_reserved_0"."id" = "friends"."friend_id" where "users"."id" = "friends"."user_id" and exists (select * from "users" inner join "friends" on "users"."id" = "friends"."friend_id" where "laravel_reserved_0"."id" = "friends"."user_id" and "email" = ?))`, statement)
	assert.Equal(t, []any{"foo@gmail.com"}, bindings)
}

func TestSelfReferencingExistence_BelongsToManyWithWherePivotScope(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefUser{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("FriendsOne")
	})

	assert.Equal(t, `select * from "users" where exists (select * from "users" as "laravel_reserved_0" inner join "friends" on "laravel_reserved_0"."id" = "friends"."friend_id" where "users"."id" = "friends"."user_id" and "friends"."user_id" = ?)`, statement)
	assert.Equal(t, []any{1}, bindings)
}

func TestSelfReferencingExistence_BelongsToManyNestedWithWherePivotScopes(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefUser{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("FriendsOne.FriendsTwo")
	})

	assert.Equal(t, `select * from "users" where exists (select * from "users" as "laravel_reserved_0" inner join "friends" on "laravel_reserved_0"."id" = "friends"."friend_id" where "users"."id" = "friends"."user_id" and exists (select * from "users" inner join "friends" on "users"."id" = "friends"."friend_id" where "laravel_reserved_0"."id" = "friends"."user_id" and "friends"."user_id" = ?) and "friends"."user_id" = ?)`, statement)
	assert.Equal(t, []any{2, 1}, bindings)
}

func TestSelfReferencingExistence_BelongsToManyWithWherePivotScopeOnOtherTable(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefUser{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("RolesScoped")
	})

	assert.Equal(t, `select * from "users" where exists (select * from "roles" inner join "role_user" on "roles"."id" = "role_user"."role_id" where "users"."id" = "role_user"."user_id" and "role_user"."flag" = ?)`, statement)
	assert.Equal(t, []any{1}, bindings)
}

func TestSelfReferencingExistence_BelongsTo(t *testing.T) {
	statement, _ := compileSelfRefExistence(t, &selfRefPost{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("ParentPost")
	})

	assert.Equal(t, `select * from "posts" where exists (select * from "posts" as "laravel_reserved_0" where "laravel_reserved_0"."id" = "posts"."parent_id")`, statement)
}

func TestSelfReferencingExistence_BelongsToWhereHasCallback(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefPost{}, func(q contractsorm.Query) contractsorm.Query {
		return q.WhereHas("ParentPost", func(q contractsorm.Query) contractsorm.Query { return q.Where("name", "Parent Post") })
	})

	assert.Equal(t, `select * from "posts" where exists (select * from "posts" as "laravel_reserved_0" where "laravel_reserved_0"."id" = "posts"."parent_id" and "name" = ?)`, statement)
	assert.Equal(t, []any{"Parent Post"}, bindings)
}

func TestSelfReferencingExistence_BelongsToNested(t *testing.T) {
	statement, _ := compileSelfRefExistence(t, &selfRefPost{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("ParentPost.ParentPost")
	})

	assert.Equal(t, `select * from "posts" where exists (select * from "posts" as "laravel_reserved_0" where "laravel_reserved_0"."id" = "posts"."parent_id" and exists (select * from "posts" where "laravel_reserved_0"."parent_id" = "posts"."id"))`, statement)
}

func TestSelfReferencingExistence_HasMany(t *testing.T) {
	statement, _ := compileSelfRefExistence(t, &selfRefPost{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("ChildPosts")
	})

	assert.Equal(t, `select * from "posts" where exists (select * from "posts" as "laravel_reserved_0" where "posts"."id" = "laravel_reserved_0"."parent_id")`, statement)
}

func TestSelfReferencingExistence_HasManyNestedCallback(t *testing.T) {
	statement, bindings := compileSelfRefExistence(t, &selfRefPost{}, func(q contractsorm.Query) contractsorm.Query {
		return q.WhereHas("ChildPosts.ChildPosts", func(q contractsorm.Query) contractsorm.Query { return q.Where("name", "Child Post") })
	})

	assert.Equal(t, `select * from "posts" where exists (select * from "posts" as "laravel_reserved_0" where "posts"."id" = "laravel_reserved_0"."parent_id" and exists (select * from "posts" where "laravel_reserved_0"."id" = "posts"."parent_id" and "name" = ?))`, statement)
	assert.Equal(t, []any{"Child Post"}, bindings)
}

func TestSelfReferencingExistence_ThreeLevelChainAliasesEveryOtherLevel(t *testing.T) {
	statement, _ := compileSelfRefExistence(t, &selfRefPost{}, func(q contractsorm.Query) contractsorm.Query {
		return q.Has("ChildPosts.ChildPosts.ChildPosts")
	})

	assert.Equal(t, `select * from "posts" where exists (select * from "posts" as "laravel_reserved_0" where "posts"."id" = "laravel_reserved_0"."parent_id" and exists (select * from "posts" where "laravel_reserved_0"."id" = "posts"."parent_id" and exists (select * from "posts" as "laravel_reserved_1" where "posts"."id" = "laravel_reserved_1"."parent_id")))`, statement)
}

func TestSelfReferencingExistence_WithWhereHasKeepsTheConstraint(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "abigailotwell@gmail.com") }
	query := compiledQuery(t, &selfRefUser{}, Conditions{}).WithWhereHas("Friends", callback).(*OrmBuilder)

	selfJoinAliasCount.Store(0)
	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "users" where exists (select * from "users" as "laravel_reserved_0" inner join "friends" on "laravel_reserved_0"."id" = "friends"."friend_id" where "users"."id" = "friends"."user_id" and "email" = ?)`, statement)
	assert.Equal(t, []any{"abigailotwell@gmail.com"}, bindings)

	require.Len(t, query.conditions.eagerLoad, 1)
	assert.Equal(t, "Friends", query.conditions.eagerLoad[0].Relation)
	assert.NotNil(t, query.conditions.eagerLoad[0].Callback, "the eager load carries the same constraint")
}
