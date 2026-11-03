package orm_relation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file pins the relation-object surface of the has-many-through relation — the
// relation-object round's through item — on top of the corpus in
// laravel_port_has_many_through_test.go:
//
//   - HasManyThrough::one() (HasManyThrough.php:25) converts the many-result relation into a
//     HasOneThrough that reuses the same builder, so its GetResults is the relation read narrowed
//     to a single row (limit 1). Eloquent realises the conversion by clearing the joins on the
//     shared query and constructing the HasOneThrough with constraints disabled — its
//     constructor's performJoin re-adds exactly the one join while the original where survives;
//     the desc copy the Go object performs reuses the builder untouched, which observes
//     identically.
//   - The promoted reads — First(), Where(..)->First() (Laravel's firstWhere()) and Find() — ride
//     the builder, so each carries the through-read shape: the related table's columns plus the
//     through key aliased laravel_through_key, the through join, and the parent-key constraint.
//
// Every statement below was measured with the oracle (illuminate/database v12.55.1, executed
// against sqlite with this file's corpus seed) and is pinned through the query log with the
// bindings inlined the way the log renders them. The one measured difference: the Go
// first-family compile (compileFirst) adds the framework's deterministic `order by
// "<related>"."<key>"` beside `limit 1`, where Laravel's first() carries none — Find's scalar
// form is byte-identical to the oracle's.
//
// Reads start from a fresh clone each time, the harness's rule: a compile narrows the builder it
// runs on (limit 1 and the key order), so reads never share a handle.

func TestHasManyThrough_OneConvertsToAHasOneThroughRead(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))

	ctx := databasedb.EnableQueryLog(context.Background())

	read := func() contractsorm.RelationQuery {
		withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
		require.True(t, ok, "the ORM query must support WithContext")

		return withContext.WithContext(ctx).Related(&country, "Posts")
	}

	many, ok := read().(contractsorm.HasManyThroughRelation)
	require.True(t, ok, "Related must return the has-many-through object")
	single := many.One()

	// Oracle: $country->posts()->one()->getResults() logs
	//   select "posts".*, "users"."country_id" as "laravel_through_key" from "posts"
	//   inner join "users" on "users"."id" = "posts"."user_id"
	//   where "users"."country_id" = ? limit 1   (bindings [1])
	// and returns the first Post — a single model, where the unconverted getResults() returns the
	// two-row Collection. (The Go statement carries the framework's key order, see the file head.)
	var post hasManyThroughPost
	require.NoError(t, single.GetResults(&post))
	assert.Equal(t, uint(1), post.ID, "the converted read returns the first matching row")
	assert.Equal(t, "A title", post.Title)

	// Oracle: one()->get() stays the many-row read — no limit, both posts (Collection count=2).
	posts := []hasManyThroughPost{}
	require.NoError(t, read().(contractsorm.HasManyThroughRelation).One().Get(&posts))

	// Oracle: the unconverted getResults() is the same many-row read (Collection count=2).
	all := []hasManyThroughPost{}
	require.NoError(t, read().GetResults(&all))

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 3, "one converted read, then the two many-row reads")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 1 order by "posts"."id" limit 1`, logs[0].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 1`, logs[1].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 1`, logs[2].Query)
	assert.Len(t, posts, 2)
	assert.Len(t, all, 2)
}

func TestHasManyThrough_RelationObjectReadsRideTheThroughReadConfig(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedFindFixture(t, query)

	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))

	ctx := databasedb.EnableQueryLog(context.Background())

	read := func() contractsorm.RelationQuery {
		withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
		require.True(t, ok, "the ORM query must support WithContext")

		return withContext.WithContext(ctx).Related(&country, "Posts")
	}

	// Oracle: $country->posts()->first() logs
	//   select "posts".*, "users"."country_id" as "laravel_through_key" from "posts"
	//   inner join "users" on "users"."id" = "posts"."user_id"
	//   where "users"."country_id" = ? limit 1   (bindings [1]).
	post := hasManyThroughPost{}
	require.NoError(t, read().First(&post))
	assert.Equal(t, uint(1), post.ID)
	assert.Equal(t, "A title", post.Title)

	// Oracle: $country->posts()->firstWhere('title', 'A title') — the where chain plus the
	// relation first(), bindings [1, "A title"].
	byTitle := hasManyThroughPost{}
	require.NoError(t, read().Where("title", "A title").First(&byTitle))
	assert.Equal(t, uint(1), byTitle.ID)

	// Oracle: $country->posts()->find(1) — the qualified related key against the through
	// constraint, bindings [1, 1]; the scalar find carries `limit 1` with no ordering.
	found := hasManyThroughPost{}
	require.NoError(t, read().Find(&found, 1))
	assert.Equal(t, uint(1), found.ID)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 3, "each promoted read is one statement")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 1 order by "posts"."id" limit 1`, logs[0].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 1 and "title" = 'A title' order by "posts"."id" limit 1`, logs[1].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 1 and "posts"."id" = 1 limit 1`, logs[2].Query)
}
