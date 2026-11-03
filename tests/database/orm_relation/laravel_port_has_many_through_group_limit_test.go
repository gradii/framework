package orm_relation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file pins the relation-level take() / limit() branch of the has-many-through object
// (HasOneOrManyThrough::limit, HasOneOrManyThrough.php:772) against the real sqlite harness on
// the corpus seed of laravel_port_has_many_through_test.go: an existing far parent keeps the plain
// query limit, a far parent that does not exist routes the value through groupLimit partitioned
// by the through table's first key (getQualifiedFirstKeyName, :813). Oracle (illuminate/database
// v12.55.1, real sqlite, php .claude/laravel-oracle/oracle.php
// /tmp/followup-take-grouplimit/take_grouplimit.php):
//
//	T1 $country->posts()->limit(3)->toSql()
//	   select * from "gl_tposts" inner join "gl_tusers" on "gl_tusers"."id" = "gl_tposts"."user_id"
//	   where "gl_tusers"."country_id" = ? limit 3
//	T5 (new GlCountry)->posts()->limit(3)->toSql()
//	   select * from (select *, row_number() over (partition by "gl_tusers"."country_id") as "laravel_row"
//	   from "gl_tposts" inner join "gl_tusers" on "gl_tusers"."id" = "gl_tposts"."user_id"
//	   where "gl_tusers"."country_id" is null) as "laravel_table" where "laravel_row" <= 3 order by "laravel_row"
//	E2 the executed get() wears shouldSelect inside the rewrite:
//	   select * from (select "gl_tposts".*, "gl_tusers"."country_id" as "laravel_through_key",
//	   row_number() over (partition by "gl_tusers"."country_id") as "laravel_row" from ...
//	T8 (new GlCountry)->posts()->limit(3)->get()->count() => 0, T9 existing => 3
//
// The Go object reads the far parent's key for Model::$exists; the prototype's key binds `= 0`
// where Laravel's null attribute compiles `is null` — the query log inlines the binding.

func TestHasManyThrough_LimitOnAnExistingParentCompilesThePlainLimit(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	posts := []hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&country, "Posts").Take(1).Get(&posts))
	require.Len(t, posts, 1, "the plain limit caps the existing parent's read")
	assert.Equal(t, "A title", posts[0].Title)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 1 limit 1`, logs[0].Query)
}

func TestHasManyThrough_LimitOnANewParentCompilesTheGroupLimit(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	// A prototype far parent: no key, so the relation partitions by "users"."country_id" — the
	// through first key — and the statement executes on sqlite (T8) matching no row.
	posts := []hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&hasManyThroughCountry{}, "Posts").Limit(1).Get(&posts))
	assert.Empty(t, posts)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1)
	assert.Equal(t, `select * from (select "posts".*, "users"."country_id" as "laravel_through_key", row_number() over (partition by "users"."country_id") as "laravel_row" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 0) as "laravel_table" where "laravel_row" <= 1 order by "laravel_row"`, logs[0].Query)
}

// TestHasManyThrough_EagerLimitCompilesTheGroupLimit pins the eager form for the through family:
// the callback runs on the relation object with a non-existing far parent, so limit(1) partitions
// per country by the through table's first key — and the eager read itself is the joined shape
// addEagerConstraints filters. Oracle (illuminate/database v12.55.1, real sqlite, php
// .claude/laravel-oracle/oracle.php /tmp/eager-callback-limit/eager_callback_limit.php E2):
//
//	GlCountry::with(['posts' => fn ($q) => $q->limit(1)])->get() executes
//	select * from (select "gl_tposts".*, "gl_tusers"."country_id" as "laravel_through_key",
//	row_number() over (partition by "gl_tusers"."country_id") as "laravel_row" from "gl_tposts"
//	inner join "gl_tusers" on "gl_tusers"."id" = "gl_tposts"."user_id"
//	where "gl_tusers"."country_id" in (1, 2)) as "laravel_table" where "laravel_row" <= 1
//	order by "laravel_row"
//
// and each country carries one post.
func TestHasManyThrough_EagerLimitCompilesTheGroupLimit(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	found := []hasManyThroughCountry{}
	require.NoError(t, withContext.WithContext(ctx).Model(&hasManyThroughCountry{}).OrderBy("id").
		With("Posts", func(q contractsorm.Query) contractsorm.Query { return q.Limit(1) }).
		Get(&found))
	require.Len(t, found, 2)
	assert.Len(t, found[0].Posts, 1, "the eager callback's limit caps rows per far parent")
	assert.Len(t, found[1].Posts, 1)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 3, "the parent read, the through-keys read that feeds the mapping, then the joined group-limited read")
	assert.Equal(t, `select "country_id", "id" from "users" where "users"."country_id" in (1, 2)`, logs[1].Query)
	assert.Equal(t, `select * from (select "posts".*, "users"."country_id" as "laravel_through_key", row_number() over (partition by "users"."country_id") as "laravel_row" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" in (1, 2)) as "laravel_table" where "laravel_row" <= 1 order by "laravel_row"`, logs[2].Query)
}
