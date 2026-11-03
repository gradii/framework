package has_many

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file pins the relation-level take() / limit() branch of the has-many object
// (HasOneOrMany::limit, HasOneOrMany.php:555) against the real sqlite harness: an existing parent
// keeps the plain query limit, a parent that does not exist routes the value through the query
// builder's groupLimit — the row_number() over (partition by <foreign key>) rewrite the eager
// constraints rely on for an N-per-parent read. Oracle (illuminate/database v12.55.1, real
// sqlite, php .claude/laravel-oracle/oracle.php /tmp/followup-take-grouplimit/take_grouplimit.php):
//
//	H1  $existing->posts()->limit(3)->toSql()
//	    select * from "gl_posts" where "gl_posts"."user_id" = ? and "gl_posts"."user_id" is not null limit 3
//	H13 (new GlUser(['id' => 1]))->posts()->limit(3)->toSql()
//	    select * from (select *, row_number() over (partition by "gl_posts"."user_id") as "laravel_row"
//	    from "gl_posts" where "gl_posts"."user_id" = ? and "gl_posts"."user_id" is not null)
//	    as "laravel_table" where "laravel_row" <= 3 order by "laravel_row"
//	H10 (new GlUser)->posts()->limit(3)->get()->count() => 0  (the rewrite executes on sqlite)
//
// The Go object reads the parent key for Model::$exists (a Go model carries no flag): the seeded
// parent is the existing one, the zero-valued prototype is not. The prototype's key binds `= 0`
// where Laravel's null attribute compiles `is null` (H7) — the query log inlines the binding.

func TestHasMany_LimitOnAnExistingParentCompilesThePlainLimit(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	for _, name := range []string{"a", "b", "c"} {
		hasManySeedItem(t, query, &user, hasManyItem{Name: name})
	}

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	items := []hasManyItem{}
	require.NoError(t, withContext.WithContext(ctx).Related(&user, "Items").Take(2).Get(&items))
	assert.Len(t, items, 2, "the plain limit caps the existing parent's read")

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1)
	assert.Equal(t, `select * from "items" where "items"."foreign_key" = 1 and "items"."foreign_key" is not null limit 2`, logs[0].Query)
}

func TestHasMany_LimitOnANewParentCompilesTheGroupLimit(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	for _, name := range []string{"a", "b", "c"} {
		hasManySeedItem(t, query, &user, hasManyItem{Name: name})
	}

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	// A prototype parent: no key, so the relation routes the limit through groupLimit. The
	// statement executes on sqlite (H10) and, like the oracle's, matches no row.
	items := []hasManyItem{}
	require.NoError(t, withContext.WithContext(ctx).Related(&hasManyUser{}, "Items").Limit(2).Get(&items))
	assert.Empty(t, items)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1)
	assert.Equal(t, `select * from (select *, row_number() over (partition by "items"."foreign_key") as "laravel_row" from "items" where "items"."foreign_key" = 0 and "items"."foreign_key" is not null) as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`, logs[0].Query)
}
