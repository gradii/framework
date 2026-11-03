package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

// This file pins the key-list forms of FindOr and FindOrFail. Laravel gives a through relation its
// own findOr/findOrFail (HasOneOrManyThrough), whose array form delegates to findMany — one
// whereKey IN read — returns the collection when every unique id resolves, and otherwise answers
// with the callback (findOr) or a ModelNotFoundException naming the model and every requested id
// (findOrFail). The oracle probe (oracle_checkout.php at laravel/framework 12.43.1, cross-checked
// against the vendored illuminate/database 12.55.1) measured the relation read as `select "posts".*,
// "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" =
// "posts"."user_id" where "users"."country_id" = ? and "posts"."id" in (?, ?)` (bindings 1, 1, 2),
// the miss as `No query results for model [HasManyThroughTestPost] 1, 2`, and findOr([])/findOrFail([])
// as an empty collection with no statement. A plain Builder-level read differs (its whereKey inlines
// integer ids and its findOrFail lists only the ids that are missing); the ORM's single query form
// follows the relation semantics the ported corpus exercises.

// FindOrFail with a key list reads one whereKey IN statement; every key resolving returns the rows
// and a short result reports the model and every requested id through errors.OrmModelNotFound,
// which still matches errors.OrmRecordNotFound.
func TestFindOrFailWithKeyList(t *testing.T) {
	t.Run("every key resolves", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"},
			[]driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})

		var users []getColumnsUser
		require.NoError(t, query.FindOrFail(&users, []any{1, 2}))

		assert.Equal(t, `select * from "users" where "users"."id" in (?, ?)`, recorder.last(t).sql)
		assert.Equal(t, []driver.Value{int64(1), int64(2)}, recorder.last(t).args)
		require.Len(t, users, 2)
		assert.Equal(t, "a", users[0].Name)
		assert.Equal(t, "b", users[1].Name)
	})

	t.Run("a key is missing", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"}, []driver.Value{int64(1), "a"})

		var users []getColumnsUser
		err := query.FindOrFail(&users, []any{1, 2})

		require.Error(t, err)
		assert.ErrorIs(t, err, errors.OrmRecordNotFound)
		assert.ErrorIs(t, err, errors.OrmModelNotFound)
		assert.EqualError(t, err, "No query results for model [gorm.getColumnsUser] 1, 2")
		assert.Empty(t, users)
		assert.Equal(t, 1, recorder.count())
	})

	t.Run("an empty key list reads nothing", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)

		var users []getColumnsUser
		require.NoError(t, query.FindOrFail(&users, []any{}))
		assert.Empty(t, users)
		assert.Zero(t, recorder.count())
	})
}

// FindOr with a key list answers a short result with the callback; the callback's result replaces
// the collection, so dest is left alone. An empty list returns the empty collection without
// calling the callback.
func TestFindOrWithKeyList(t *testing.T) {
	t.Run("every key resolves and the callback stays unrun", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"},
			[]driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})

		var users []getColumnsUser
		called := false
		require.NoError(t, query.FindOr(&users, []any{1, 2}, func() error { called = true; return nil }))

		assert.False(t, called)
		require.Len(t, users, 2)
		assert.Equal(t, `select * from "users" where "users"."id" in (?, ?)`, recorder.last(t).sql)
	})

	t.Run("a missing key runs the callback and leaves dest alone", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"}, []driver.Value{int64(1), "a"})

		var users []getColumnsUser
		called := false
		require.NoError(t, query.FindOr(&users, []any{1, 2}, func() error { called = true; return nil }))

		assert.True(t, called)
		assert.Empty(t, users)
		assert.Equal(t, 1, recorder.count())
	})

	t.Run("an empty key list reads nothing and stays unrun", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)

		var users []getColumnsUser
		called := false
		require.NoError(t, query.FindOr(&users, []any{}, func() error { called = true; return nil }))

		assert.False(t, called)
		assert.Empty(t, users)
		assert.Zero(t, recorder.count())
	})
}

// The through read the corpus exercises: one whereKey IN over the join, and a short result naming
// the related model. relCountry's Posts is a HasManyThrough through relUser (rel_posts.rel_user_id
// = rel_users.id, rel_users.rel_country_id = parent.id).
func TestFindOrFailWithKeyListOnAThroughRelation(t *testing.T) {
	t.Run("every key resolves", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &relCountry{})
		recorder.returnRows(`from "rel_posts"`, []string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
			[]driver.Value{int64(1), "A", int64(1), int64(1), int64(1)},
			[]driver.Value{int64(2), "B", int64(1), int64(1), int64(1)})

		var posts []relPost
		require.NoError(t, query.Related(&relCountry{ID: 1}, "Posts").FindOrFail(&posts, []any{1, 2}))

		assert.Equal(t,
			`select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ? and "rel_posts"."id" in (?, ?)`,
			recorder.last(t).sql)
		assert.Equal(t, []driver.Value{int64(1), int64(1), int64(2)}, recorder.last(t).args)
		require.Len(t, posts, 2)
	})

	t.Run("a key is missing", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &relCountry{})
		recorder.returnRows(`from "rel_posts"`, []string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
			[]driver.Value{int64(1), "A", int64(1), int64(1), int64(1)})

		var posts []relPost
		err := query.Related(&relCountry{ID: 1}, "Posts").FindOrFail(&posts, []any{1, 2})

		require.Error(t, err)
		assert.ErrorIs(t, err, errors.OrmRecordNotFound)
		assert.EqualError(t, err, "No query results for model [gorm.relPost] 1, 2")
		assert.Empty(t, posts)
	})
}

// FindOr with a key list on a through relation: findMany's read plus HasOneOrManyThrough::findOr's
// callback-on-short-result semantics, on the same qualified-key select the other reads wear.
func TestFindOrWithKeyListOnAThroughRelation(t *testing.T) {
	query, recorder := newRecordingRelQuery(t, &relCountry{})
	recorder.returnRows(`from "rel_posts"`, []string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
		[]driver.Value{int64(1), "A", int64(1), int64(1), int64(1)},
		[]driver.Value{int64(2), "B", int64(1), int64(1), int64(1)})

	var posts []relPost
	called := false
	require.NoError(t, query.Related(&relCountry{ID: 1}, "Posts").FindOr(&posts, []any{1, 2}, func() error { called = true; return nil }))

	assert.False(t, called)
	assert.Equal(t,
		`select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ? and "rel_posts"."id" in (?, ?)`,
		recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), int64(1), int64(2)}, recorder.last(t).args)
	require.Len(t, posts, 2)
}
