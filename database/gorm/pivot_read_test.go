package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file pins the many-to-many read shape end to end: the aliased pivot select the relation
// read compiles (BelongsToMany::shouldSelect), the hydration of the related model's declared Pivot
// struct off those aliases (hydratePivotRelation / migratePivotAttributes), the reads that
// deliberately keep their own select list, and the pivot id-overwrite the aliases prevent.
// Oracle (illuminate/database v12.55.1, executed against sqlite): `$user->friends()->get()`
// compiles `select "users".*, "friends"."user_id" as "pivot_user_id", "friends"."friend_id" as
// "pivot_friend_id" from "users" inner join "friends" on "users"."id" = "friends"."friend_id"
// where "friends"."user_id" = ?`; a `->withPivot('friend_level_id')` adds
// `"friends"."friend_level_id" as "pivot_friend_level_id"`; get([$columns]) keeps the caller's
// columns and still appends the aliases; value()/pluck()/exists() carry no aliases.

// --- fixtures ----------------------------------------------------------------

// prUser mirrors the corpus's custom-pivot user: a self-referencing Friends read whose related
// model declares the pivot struct prFriendPivot — the Go form of ->withPivot(...) plus the pivot
// class's own relations.
type prUser struct {
	ID   uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`

	Friends []*prUser `gorm:"-"`
	Posts   []*prPost `gorm:"-"`
	Pivot   prFriendPivot
}

func (prUser) TableName() string { return "pr_users" }

func (prUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Friends": contractsorm.Many2Many{Related: &prUser{}, Table: "pr_friends", ForeignPivotKey: "user_id", RelatedPivotKey: "friend_id"},
		"Posts":   contractsorm.HasMany{Related: &prPost{}, ForeignKey: "user_id"},
	}
}

// prFriendPivot ports EloquentTestFriendPivot: the pivot row's columns plus the relations the
// corpus traverses ($pivot->level->level, $pivot->friend->name).
type prFriendPivot struct {
	UserID        uint     `gorm:"column:user_id"`
	FriendID      uint     `gorm:"column:friend_id"`
	FriendLevelID *uint    `gorm:"column:friend_level_id"`
	Level         *prLevel `gorm:"-"`
	Friend        *prUser  `gorm:"-"`
}

func (prFriendPivot) TableName() string { return "pr_friends" }

// PivotKeys names the (foreign key, related key) pair, the columns AsPivot::setKeysForSelectQuery
// matches a pivot row on.
func (prFriendPivot) PivotKeys() []string { return []string{"user_id", "friend_id"} }

func (prFriendPivot) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Level":  contractsorm.BelongsTo{Related: &prLevel{}, ForeignKey: "friend_level_id"},
		"Friend": contractsorm.BelongsTo{Related: &prUser{}, ForeignKey: "friend_id"},
	}
}

type prLevel struct {
	ID    uint   `gorm:"column:id;primaryKey"`
	Level string `gorm:"column:level"`
}

func (prLevel) TableName() string { return "pr_levels" }

type prPost struct {
	ID     uint   `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint   `gorm:"column:user_id"`
	Title  string `gorm:"column:title"`
}

func (prPost) TableName() string { return "pr_posts" }

// prAuthor / prArticle pin the id-overwrite defect: the pivot table carries its own id column
// (author_id would collide with a plain select *), so the aliased select is what keeps the
// related article's id on the hydrated model.
type prAuthor struct {
	ID       uint           `gorm:"column:id;primaryKey;autoIncrement"`
	Articles []*prArticle   `gorm:"-"`
	Pivot    prArticlePivot `gorm:"-"`
}

func (prAuthor) TableName() string { return "pr_authors" }

func (prAuthor) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Articles": contractsorm.Many2Many{Related: &prArticle{}, Table: "pr_article_user", ForeignPivotKey: "author_id", RelatedPivotKey: "article_id"},
	}
}

type prArticle struct {
	ID    uint   `gorm:"column:id;primaryKey"`
	Title string `gorm:"column:title"`
}

func (prArticle) TableName() string { return "pr_articles" }

type prArticlePivot struct {
	ID        uint `gorm:"column:id"`
	AuthorID  uint `gorm:"column:author_id"`
	ArticleID uint `gorm:"column:article_id"`
}

func (prArticlePivot) TableName() string { return "pr_article_user" }

func (prArticlePivot) PivotKeys() []string { return []string{"author_id", "article_id"} }

// --- select shape ------------------------------------------------------------

func TestPivotRead_Many2ManySelectCarriesTheAliasedPivotColumns(t *testing.T) {
	q := newRelQueryWith(t, &prUser{})
	rel := q.Related(&prUser{ID: 7}, "Friends")
	sql := newRelationSQL(t, rel, &[]prUser{})

	// The related columns plus the two pivot keys and the Pivot-struct columns, each aliased
	// pivot_<column> — the oracle's get() shape with these fixture names.
	assert.Equal(t, `select "pr_users".*, "pr_friends"."user_id" as "pivot_user_id", "pr_friends"."friend_id" as "pivot_friend_id", "pr_friends"."friend_level_id" as "pivot_friend_level_id" from "pr_users" inner join "pr_friends" ON "pr_friends"."friend_id" = "pr_users"."id" where "pr_friends"."user_id" = ?`, sql)
}

func TestPivotRead_CallerColumnsKeepTheAliases(t *testing.T) {
	q := newRelQueryWith(t, &prUser{})
	rel := q.Related(&prUser{ID: 7}, "Friends").Select("name")
	sql := newRelationSQL(t, rel, &[]prUser{})

	// Oracle: get(['name'])/first(['name']) keep the caller's columns and still append the alias
	// list — the addSelect(shouldSelect($columns)) of Laravel's get() path.
	assert.Equal(t, `select "name", "pr_friends"."user_id" as "pivot_user_id", "pr_friends"."friend_id" as "pivot_friend_id", "pr_friends"."friend_level_id" as "pivot_friend_level_id" from "pr_users" inner join "pr_friends" ON "pr_friends"."friend_id" = "pr_users"."id" where "pr_friends"."user_id" = ?`, sql)
}

func TestPivotRead_IdOverwriteIsPreventedByTheAliasedSelect(t *testing.T) {
	q := newRelQueryWith(t, &prAuthor{})
	rel := q.Related(&prAuthor{ID: 1}, "Articles")
	sql := newRelationSQL(t, rel, &[]prArticle{})

	// The pivot table's own id column is not selected at all — the related table's columns come
	// first and every pivot column is aliased — so a plain `select *` can no longer let
	// pr_article_user.id overwrite pr_articles.id on hydration.
	assert.Equal(t, `select "pr_articles".*, "pr_article_user"."author_id" as "pivot_author_id", "pr_article_user"."article_id" as "pivot_article_id" from "pr_articles" inner join "pr_article_user" ON "pr_article_user"."article_id" = "pr_articles"."id" where "pr_article_user"."author_id" = ?`, sql)
	assert.NotContains(t, sql, "select *")
}

func TestPivotRead_ModelIDSurvivesTheHydration(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prAuthor{})
	recorder.returnRows(`from "pr_articles"`,
		[]string{"id", "title", "pivot_author_id", "pivot_article_id"},
		[]driver.Value{int64(2), "Second article", int64(1), int64(2)})

	articles := []prArticle{}
	require.NoError(t, q.Related(&prAuthor{ID: 1}, "Articles").Get(&articles))
	require.Len(t, articles, 1)
	assert.Equal(t, uint(2), articles[0].ID)
	assert.Equal(t, "Second article", articles[0].Title)
}

// find($id) on a many-to-many relation is whereKey($id)->first() over the aliased pivot select: the
// read is one row and its statement carries the key condition plus `limit 1`. Oracle (sqlite,
// illuminate/database v12.55.1): $user->friends()->find(2) compiles `... where "friends"."user_id"
// = ? and "users"."id" = ? limit 1` (bindings 1, 2); find([2, 3]) stays unlimited.
func TestPivotRead_FindByScalarKeyLimitsToOne(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`,
		[]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
		[]driver.Value{int64(2), "abigail", int64(1), int64(2), int64(1)})

	friend := prUser{}
	require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").Find(&friend, 2))

	assert.Equal(t, `select "pr_users".*, "pr_friends"."user_id" as "pivot_user_id", "pr_friends"."friend_id" as "pivot_friend_id", "pr_friends"."friend_level_id" as "pivot_friend_level_id" from "pr_users" inner join "pr_friends" ON "pr_friends"."friend_id" = "pr_users"."id" where "pr_friends"."user_id" = ? and "pr_users"."id" = ? limit 1`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), int64(2)}, recorder.last(t).args)
	assert.Equal(t, uint(2), friend.ID)
	assert.Equal(t, uint(1), friend.Pivot.UserID)
	assert.Equal(t, uint(2), friend.Pivot.FriendID)
}

// --- hydration ---------------------------------------------------------------

func TestPivotRead_GetHydratesTheDeclaredPivotStruct(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`,
		[]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
		[]driver.Value{int64(2), "abigail", int64(1), int64(2), int64(1)})

	friends := []prUser{}
	require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").Get(&friends))

	require.Len(t, friends, 1)
	assert.Equal(t, uint(2), friends[0].ID)
	assert.Equal(t, "abigail", friends[0].Name)
	assert.Equal(t, uint(1), friends[0].Pivot.UserID)
	assert.Equal(t, uint(2), friends[0].Pivot.FriendID)
	require.NotNil(t, friends[0].Pivot.FriendLevelID)
	assert.Equal(t, uint(1), *friends[0].Pivot.FriendLevelID)
}

func TestPivotRead_FirstAndSoleHydrate(t *testing.T) {
	for _, read := range []struct {
		name string
		run  func(t *testing.T, q *OrmBuilder) prUser
	}{
		{"First", func(t *testing.T, q *OrmBuilder) prUser {
			friend := prUser{}
			require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").First(&friend))

			return friend
		}},
		{"Sole", func(t *testing.T, q *OrmBuilder) prUser {
			friend := prUser{}
			require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").Sole(&friend))

			return friend
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, &prUser{})
			recorder.returnRows(`from "pr_users"`,
				[]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
				[]driver.Value{int64(2), "abigail", int64(1), int64(2), int64(2)})

			friend := read.run(t, q)
			assert.Equal(t, uint(2), friend.ID)
			assert.Equal(t, uint(1), friend.Pivot.UserID)
			assert.Equal(t, uint(2), friend.Pivot.FriendID)
			require.NotNil(t, friend.Pivot.FriendLevelID)
			assert.Equal(t, uint(2), *friend.Pivot.FriendLevelID)
		})
	}
}

func TestPivotRead_ChunkHydratesEveryPage(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`,
		[]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
		[]driver.Value{int64(2), "abigail", int64(1), int64(2), int64(1)})

	friends := []prUser{}
	pages := 0
	require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").Chunk(2, &friends, func(page int) error {
		pages++
		require.Len(t, friends, 1)
		assert.Equal(t, uint(1), friends[0].Pivot.UserID)
		assert.Equal(t, uint(2), friends[0].Pivot.FriendID)

		return nil
	}))
	assert.Equal(t, 1, pages)
}

func TestPivotRead_EachHydratesTheCurrentModel(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`,
		[]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
		[]driver.Value{int64(2), "abigail", int64(1), int64(2), int64(1)})

	friend := prUser{}
	seen := 0
	require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").Each(&friend, func(index int) error {
		seen++
		assert.Equal(t, uint(2), friend.ID)
		assert.Equal(t, uint(1), friend.Pivot.UserID)
		assert.Equal(t, uint(2), friend.Pivot.FriendID)

		return nil
	}))
	assert.Equal(t, 1, seen)
}

func TestPivotRead_CursorHydratesEachStreamedRow(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prUser{})
	recorder.returnRows(`from "pr_users"`,
		[]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
		[]driver.Value{int64(2), "abigail", int64(1), int64(2), int64(1)})

	seen := 0
	for row := range q.Related(&prUser{ID: 1}, "Friends").Cursor() {
		require.NoError(t, row.Err())

		friend := prUser{}
		require.NoError(t, row.Scan(&friend))
		assert.Equal(t, uint(2), friend.ID)
		assert.Equal(t, uint(1), friend.Pivot.UserID)
		assert.Equal(t, uint(2), friend.Pivot.FriendID)
		seen++
	}
	assert.Equal(t, 1, seen)
}

// --- reads that keep their own select list -----------------------------------

func TestPivotRead_ValueAndPluckKeepTheirOwnSelect(t *testing.T) {
	t.Run("value", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &prUser{})
		recorder.returnRows(`select "name"`, []string{"name"}, []driver.Value{"abigail"})

		name := ""
		require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").Value("name", &name))
		assert.Equal(t, "abigail", name)

		stmt := recorder.at(t, 0)
		assert.Equal(t, `select "name" from "pr_users" inner join "pr_friends" ON "pr_friends"."friend_id" = "pr_users"."id" where "pr_friends"."user_id" = ? limit 1`, stmt.sql)
	})

	t.Run("pluck", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &prUser{})
		recorder.returnRows(`select "name"`, []string{"name"}, []driver.Value{"abigail"})

		names := []string{}
		require.NoError(t, q.Related(&prUser{ID: 1}, "Friends").Pluck("name", &names))
		assert.Equal(t, []string{"abigail"}, names)

		stmt := recorder.at(t, 0)
		assert.Equal(t, `select "name" from "pr_users" inner join "pr_friends" ON "pr_friends"."friend_id" = "pr_users"."id" where "pr_friends"."user_id" = ?`, stmt.sql)
	})
}

// --- pivot structs resolve their own relations -------------------------------

func TestPivotStruct_ResolvesItsOwnRelations(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &prFriendPivot{})
	recorder.returnRows(`from "pr_levels"`, []string{"id", "level"}, []driver.Value{int64(1), "friend"})

	levelID := uint(1)
	pivot := prFriendPivot{UserID: 1, FriendID: 2, FriendLevelID: &levelID}
	require.NoError(t, q.Load(&pivot, "Level"))

	require.NotNil(t, pivot.Level)
	assert.Equal(t, uint(1), pivot.Level.ID)
	assert.Equal(t, "friend", pivot.Level.Level)

	stmt := recorder.at(t, 0)
	assert.Equal(t, `select * from "pr_levels" where "pr_levels"."id" in (?)`, stmt.sql)
}
