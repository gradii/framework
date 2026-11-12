package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// This file pins Model::fresh / Model::refresh: the key-matched, scope-skipping, write-PDO read
// (`select * from "users" where "id" = ? limit 1`, oracle illuminate/database v12.55.1 executed
// against sqlite), the pivot form matched on the AsPivot/MorphPivot key columns, the not-stored
// short-circuit, firstOrFail's model-not-found report, and refresh()'s reload of loaded relations.

// prScopedUser is the model of the scoped read: the "hide_taylor" scope it used to declare is
// mounted on the builder with ApplyScope now, so a fresh read that bypasses scopes can be told
// apart from a scoped read.
type prScopedUser struct {
	ID    uint   `gorm:"column:id;primaryKey"`
	Email string `gorm:"column:email"`
}

func (prScopedUser) TableName() string { return "pr_users" }

// prHideTaylor is the "hide_taylor" scope the test mounts on the builder, the shape the model used
// to declare.
func prHideTaylor(query contractsorm.Query) contractsorm.Query {
	return query.Where("email", "!=", "taylorotwell@gmail.com")
}

// prTagPivot is the morph pivot form: MorphPivot::setKeysForSelectQuery pins the morph type first,
// then AsPivot's (foreign key, related key) pair.
type prTagPivot struct {
	TaggableType string  `gorm:"column:taggable_type"`
	TaggableID   uint    `gorm:"column:taggable_id"`
	TagID        uint    `gorm:"column:tag_id"`
	Taxonomy     *string `gorm:"column:taxonomy"`
}

func (prTagPivot) TableName() string { return "pr_taggables" }

func (prTagPivot) PivotKeys() []string {
	return []string{"taggable_type", "taggable_id", "tag_id"}
}

// prKeyless declares neither a primary key nor PivotKeys — fresh/refresh have nothing to match on.
type prKeyless struct {
	Name string `gorm:"column:name"`
}

func (prKeyless) TableName() string { return "pr_keyless" }

func TestFresh_ReadsBackByKeyWithoutGlobalScopes(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prScopedUser{})
	q = q.ApplyScope("hide_taylor", prHideTaylor).(*OrmBuilder)
	recorder.returnRows(`from "pr_users"`, []string{"id", "email"},
		[]driver.Value{int64(1), "taylorotwell@gmail.com"})

	// Sanity: a scoped read carries the scope's predicate... in the compiled statement.
	scoped := newRelQueryWith(t, &prScopedUser{}).ApplyScope("hide_taylor", prHideTaylor).(*OrmBuilder)
	scopedSQL := newRelationSQL(t, scoped.Model(&prScopedUser{}), &prScopedUser{})
	assert.Contains(t, scopedSQL, "email")

	stored := prScopedUser{ID: 1, Email: "taylorotwell@gmail.com"}
	fresh := prScopedUser{}
	require.NoError(t, q.Fresh(&stored, &fresh))

	assert.Equal(t, uint(1), fresh.ID)
	assert.Equal(t, "taylorotwell@gmail.com", fresh.Email)
	require.Equal(t, 1, recorder.count())

	stmt := recorder.at(t, 0)
	assert.Equal(t, `select * from "pr_users" where "id" = ? limit 1`, stmt.sql)
	assert.Equal(t, []driver.Value{int64(1)}, stmt.args)
}

func TestFresh_NotStoredModelLeavesDestZeroWithoutAStatement(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})

	stored := prUser{}
	fresh := prUser{ID: 99, Name: "stale"}
	require.NoError(t, q.Fresh(&stored, &fresh))

	assert.Zero(t, fresh, "a model with no key reads null — the Go form leaves dest zero")
	assert.Equal(t, 0, recorder.count(), "no statement runs for a keyless model")
}

func TestFresh_RowGoneLeavesDestZero(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})

	stored := prUser{ID: 7}
	fresh := prUser{ID: 7, Name: "stale"}
	require.NoError(t, q.Fresh(&stored, &fresh))

	assert.Zero(t, fresh)
	assert.Equal(t, 1, recorder.count())
}

func TestRefresh_UpdatesTheReceiverInPlace(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prScopedUser{})
	recorder.returnRows(`from "pr_users"`, []string{"id", "email"},
		[]driver.Value{int64(1), "dev@mathieutu.ovh"})

	stored := prScopedUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, q.Refresh(&stored))

	assert.Equal(t, "dev@mathieutu.ovh", stored.Email)
	stmt := recorder.at(t, 0)
	assert.Equal(t, `select * from "pr_users" where "id" = ? limit 1`, stmt.sql)
}

func TestRefresh_RowGoneReportsModelNotFound(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &prUser{})

	stored := prUser{ID: 7}
	err := q.Refresh(&stored)
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
	// firstOrFail's message names the model and carries no ids — refresh() inherits it (oracle:
	// a refresh() on a missing row throws "No query results for model [User].").
	assert.Equal(t, "No query results for model [prUser].", err.Error())
}

func TestRefresh_NotStoredModelReturnsItUnchanged(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})

	stored := prUser{}
	require.NoError(t, q.Refresh(&stored))
	assert.Equal(t, 0, recorder.count())
}

func TestRefresh_ReloadsLoadedRelations(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`, []string{"id", "name"}, []driver.Value{int64(1), "taylor"})
	recorder.returnRows(`from "pr_posts"`, []string{"id", "user_id", "title"}, []driver.Value{int64(9), int64(1), "First Post"})

	model := prUser{ID: 1, Name: "stale"}
	model.Posts = []*prPost{} // loaded-but-empty: the reload must still run
	require.NoError(t, q.Refresh(&model))

	assert.Equal(t, "taylor", model.Name)
	require.Equal(t, 2, recorder.count(), "the refresh read then the relation reload")
	reload := recorder.at(t, 1)
	assert.Contains(t, reload.sql, `from "pr_posts"`)
	require.Len(t, model.Posts, 1)
	assert.Equal(t, "First Post", model.Posts[0].Title)
}

func TestFresh_UnloadedRelationsAreNotTouched(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`, []string{"id", "name"}, []driver.Value{int64(1), "taylor"})

	model := prUser{ID: 1} // Posts stays nil — not loaded
	require.NoError(t, q.Refresh(&model))

	assert.Equal(t, 1, recorder.count(), "only the refresh read runs")
	assert.Nil(t, model.Posts)
}

func TestFresh_PivotStructMatchesOnPivotKeys(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prFriendPivot{})
	recorder.returnRows(`from "pr_friends"`, []string{"user_id", "friend_id", "friend_level_id"},
		[]driver.Value{int64(1), int64(2), int64(2)})

	levelID := uint(1)
	pivot := prFriendPivot{UserID: 1, FriendID: 2, FriendLevelID: &levelID}
	fresh := prFriendPivot{}
	require.NoError(t, q.Fresh(&pivot, &fresh))

	require.NotNil(t, fresh.FriendLevelID)
	assert.Equal(t, uint(2), *fresh.FriendLevelID)
	stmt := recorder.at(t, 0)
	assert.Equal(t, `select * from "pr_friends" where "user_id" = ? and "friend_id" = ? limit 1`, stmt.sql)
	assert.Equal(t, []driver.Value{int64(1), int64(2)}, stmt.args)

	// Refresh() re-reads the row in place.
	require.NoError(t, q.Refresh(&pivot))
	require.NotNil(t, pivot.FriendLevelID)
	assert.Equal(t, uint(2), *pivot.FriendLevelID)
}

func TestFresh_MorphPivotKeysPinTheTypeFirst(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prTagPivot{})
	taxonomy := "primary"
	recorder.returnRows(`from "pr_taggables"`, []string{"taggable_type", "taggable_id", "tag_id", "taxonomy"},
		[]driver.Value{"post", int64(1), int64(1), "primary"})

	pivot := prTagPivot{TaggableType: "post", TaggableID: 1, TagID: 1, Taxonomy: nil}
	fresh := prTagPivot{}
	require.NoError(t, q.Fresh(&pivot, &fresh))

	assert.Equal(t, taxonomy, *fresh.Taxonomy)
	stmt := recorder.at(t, 0)
	assert.Equal(t, `select * from "pr_taggables" where "taggable_type" = ? and "taggable_id" = ? and "tag_id" = ? limit 1`, stmt.sql)
	assert.Equal(t, []driver.Value{"post", int64(1), int64(1)}, stmt.args)
}

func TestFresh_WithEagerLoadsTheRelations(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`, []string{"id", "name"}, []driver.Value{int64(1), "taylor"})
	recorder.returnRows(`from "pr_posts"`, []string{"id", "user_id", "title"}, []driver.Value{int64(9), int64(1), "First Post"})

	stored := prUser{ID: 1}
	fresh := prUser{}
	require.NoError(t, q.Fresh(&stored, &fresh, "Posts"))

	assert.Equal(t, "taylor", fresh.Name)
	require.Len(t, fresh.Posts, 1)
	assert.Equal(t, "First Post", fresh.Posts[0].Title)
	require.Equal(t, 2, recorder.count())
}

func TestFresh_KeylessModelIsRefused(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prKeyless{})

	model := prKeyless{Name: "x"}
	fresh := prKeyless{}
	err := q.Fresh(&model, &fresh)
	assert.ErrorIs(t, err, errors.OrmModelKeyMissing)
	assert.Equal(t, 0, recorder.count())

	err = q.Refresh(&model)
	assert.ErrorIs(t, err, errors.OrmModelKeyMissing)
}

func TestRefresh_PivotReloadsItsOwnLoadedRelations(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prFriendPivot{})
	recorder.returnRows(`from "pr_friends"`, []string{"user_id", "friend_id", "friend_level_id"},
		[]driver.Value{int64(1), int64(2), int64(2)})
	recorder.returnRows(`from "pr_levels"`, []string{"id", "level"}, []driver.Value{int64(2), "friend"})

	// Eloquent's refresh() reloads the instance's own loaded relations; on a pivot instance the
	// declared struct fields are that registry, so the pivot's loaded Level reloads after the
	// in-place re-read (oracle: $friend->pivot->refresh() re-reads the pivot row on the
	// (foreign key, related key) pair and then reloads the pivot's loaded relations).
	levelID := uint(1)
	pivot := prFriendPivot{UserID: 1, FriendID: 2, FriendLevelID: &levelID, Level: &prLevel{ID: 1, Level: "stale"}}
	require.NoError(t, q.Refresh(&pivot))

	require.Equal(t, 2, recorder.count(), "the refresh read then the pivot's relation reload")
	require.NotNil(t, pivot.FriendLevelID)
	assert.Equal(t, uint(2), *pivot.FriendLevelID)
	require.NotNil(t, pivot.Level)
	assert.Equal(t, uint(2), pivot.Level.ID)
	assert.Equal(t, "friend", pivot.Level.Level)
	reload := recorder.at(t, 1)
	assert.Equal(t, `select * from "pr_levels" where "pr_levels"."id" in (?)`, reload.sql)
}
