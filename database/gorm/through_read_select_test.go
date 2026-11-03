package gorm

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins the select list of a plain through relation read. Laravel's
// HasOneOrManyThrough::shouldSelect gives get()/first()/cursor()/find()/findMany() the related
// table's columns plus the through key aliased as laravel_through_key — oracle (illuminate/database
// v12.55.1, executed against sqlite): `select "posts".*, "users"."country_id" as
// "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where
// "users"."country_id" = ?` — so a hydrated model carries its own columns, not the joined through
// row's. The rel_* statements below re-measure the same shapes with the fixture's names
// (rel_countries/rel_users/rel_posts, second key rel_user_id). The probe snippets are saved at
// .claude/laravel-oracle/_through_read_select_probe.php and _has_one_through_users_probe.php.

// throughReadSelect is the rel_* form of the oracle's get() select: the related columns plus the
// through key aliased as laravel_through_key.
const throughReadSelect = `select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ?`

func TestThroughRead_PrunedColumnsKeepTheirAlias(t *testing.T) {
	q := newRelQueryWith(t, &relCountry{})
	rel := q.Related(&relCountry{ID: 1}, "Posts").Select("title", "body")
	sql := newRelationSQL(t, rel, &relPost{})

	// Oracle: $country->posts()->first(['title', 'body']) selects `"title", "body",
	// "users"."country_id" as "laravel_through_key"` (limit 1); get(['title', 'body']) the same
	// list without the limit.
	assert.Equal(t, `select "title", "body", "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ?`, sql)
}

func TestThroughRead_FirstAddsTheSelectAndTheAlias(t *testing.T) {
	query, recorder := newRecordingRelQuery(t, &relCountry{})

	post := relPost{}
	require.NoError(t, query.Related(&relCountry{ID: 1}, "Posts").First(&post))

	// Oracle: $country->posts()->first() runs the get() select with `limit 1`. compileFirst
	// additionally orders by the model's key when the caller gave none — this ORM's deterministic
	// first() convention — so the statement is asserted around the oracle part.
	statement := recorder.last(t).sql
	assert.True(t, strings.HasPrefix(statement, throughReadSelect), statement)
	assert.True(t, strings.HasSuffix(statement, "limit 1"), statement)
}

func TestThroughRead_FindWithASliceDelegatesToFindMany(t *testing.T) {
	returnRows := func(recorder *recordingSQLDriver) {
		recorder.returnRows(`from "rel_posts"`, []string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
			[]driver.Value{int64(1), "A title", int64(1), int64(1), int64(1)},
			[]driver.Value{int64(2), "Another title", int64(1), int64(1), int64(1)})
	}

	query, recorder := newRecordingRelQuery(t, &relCountry{})
	returnRows(recorder)

	var posts []relPost
	require.NoError(t, query.Related(&relCountry{ID: 1}, "Posts").Find(&posts, []any{1, 2}))

	// Oracle: $country->posts()->find([1, 2]) — and findMany([1, 2]) — run `... where
	// "users"."country_id" = ? and "posts"."id" in (?, ?)` with bindings 1, 1, 2, and collect
	// every match.
	assert.Equal(t, throughReadSelect+` and "rel_posts"."id" in (?, ?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), int64(1), int64(2)}, recorder.last(t).args)
	require.Len(t, posts, 2)
	assert.Equal(t, uint(1), posts[0].ID)
	assert.Equal(t, uint(2), posts[1].ID)
	assert.Equal(t, "A title", posts[0].Title)
	assert.Equal(t, "Another title", posts[1].Title)

	// The delegating call and findMany() compile the same statement.
	findMany, findManyRecorder := newRecordingRelQuery(t, &relCountry{})
	returnRows(findManyRecorder)

	var many []relPost
	require.NoError(t, findMany.Related(&relCountry{ID: 1}, "Posts").FindMany(&many, []any{1, 2}))
	assert.Equal(t, recorder.last(t).sql, findManyRecorder.last(t).sql)
	assert.Equal(t, recorder.last(t).args, findManyRecorder.last(t).args)
	require.Len(t, many, 2)
}

func TestThroughRead_FindByScalarKeyLimitsToOne(t *testing.T) {
	query, recorder := newRecordingRelQuery(t, &relCountry{})
	recorder.returnRows(`from "rel_posts"`, []string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
		[]driver.Value{int64(1), "A title", int64(1), int64(1), int64(1)})

	post := relPost{}
	require.NoError(t, query.Related(&relCountry{ID: 1}, "Posts").Find(&post, 1))

	// Oracle: $country->posts()->find(1) runs the relation's select with `and "posts"."id" = ?
	// limit 1` (bindings 1, 1); a key list stays unlimited (the slice test above). The rel_*
	// fixture measures the same shape with the fixture's names.
	assert.Equal(t, throughReadSelect+` and "rel_posts"."id" = ? limit 1`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), int64(1)}, recorder.last(t).args)
	assert.Equal(t, uint(1), post.ID)
	assert.Equal(t, "A title", post.Title)
}

func TestThroughRead_CursorHydratesTheRelatedModel(t *testing.T) {
	query, recorder := newRecordingRelQuery(t, &relCountry{})
	recorder.returnRows(`from "rel_posts"`, []string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
		[]driver.Value{int64(7), "A title", int64(1), int64(1), int64(1)})

	var posts []relPost
	for row := range query.Related(&relCountry{ID: 1}, "Posts").Cursor() {
		require.NoError(t, row.Err())

		post := relPost{}
		require.NoError(t, row.Scan(&post))

		posts = append(posts, post)
	}

	// Oracle: $country->posts()->cursor() runs the same select as get(), so every row hydrates
	// the related model from its own columns; the through key arrives as the laravel_through_key
	// alias and does not land on the model.
	assert.Equal(t, throughReadSelect, recorder.last(t).sql)
	require.Len(t, posts, 1)
	assert.Equal(t, uint(7), posts[0].ID)
	assert.Equal(t, "A title", posts[0].Title)
	assert.Equal(t, uint(1), posts[0].UserID)
	assert.Equal(t, uint(1), posts[0].RelUserID)
}

func TestThroughRead_PluckAndValueKeepTheirOwnSelect(t *testing.T) {
	t.Run("pluck", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &relCountry{})
		recorder.returnRows(`select "title"`, []string{"title"}, []driver.Value{"A title"})

		var titles []string
		require.NoError(t, query.Related(&relCountry{ID: 1}, "Posts").Pluck("title", &titles))

		// Oracle: $country->posts()->pluck('title') runs on the Eloquent builder and replaces the
		// select list — no laravel_through_key alias.
		assert.Equal(t, `select "title" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ?`, recorder.last(t).sql)
		assert.Equal(t, []string{"A title"}, titles)
	})

	t.Run("value", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &relCountry{})
		recorder.returnRows(`select "title"`, []string{"title"}, []driver.Value{"A title"})

		var title string
		require.NoError(t, query.Related(&relCountry{ID: 1}, "Posts").Value("title", &title))

		// Oracle: $country->posts()->value('title') is first(['title']) on the Eloquent builder:
		// `select "title" ... limit 1`.
		assert.Equal(t, `select "title" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ? limit 1`, recorder.last(t).sql)
		assert.Equal(t, "A title", title)
	})
}
