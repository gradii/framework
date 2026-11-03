package belongs_to_many_sync_touches_parent

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManySyncTouchesParentTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManySyncTouchesParent_<method without the test prefix>. The three tables mirror the
// corpus's createSchema() in sqlite DDL — articles, article_user, users — and the users and the
// article are seeded through the ORM.
//
// The corpus's relation runs through a custom Pivot class (TestArticleUser, $touches = ['article'])
// via ->using(...)->withTimestamps(). The Go ORM has no pivot model class: the class's two
// behaviours are declared on the relation instead, as PivotTimestamps (stamp the pivot rows) and
// Touches (bump the parent's updated_at after a pivot write that changed rows), and the pivot probe
// struct below only reads pivot rows back.
//
// # Clock mapping
//
// The corpus freezes the clock with Carbon::setTestNow and asserts exact timestamps: the attaching
// sync leaves the article's updated_at at '2021-07-19 10:13:14', the detaching sync moves it to
// '2021-07-20 19:13:14', and the users' own updated_at values differ from it. The Go port freezes
// the same two instants with carbon.SetTestNow: inserts and updates — touchIfTouching's parent bump
// included — stamp through support/carbon, so the corpus's exact claims hold. basePivotRow (the
// pivot rows' own timestamps) still stamps the wall clock; the ported test asserts no pivot
// timestamps, so that divergence stays outside the asserted surface. The users are seeded before
// the corpus freezes its clock, so their values are seed-time stamps, different from either
// frozen instant.
//
// Oracle (illuminate/database v12.55.1, executed against sqlite with this corpus's classes): the
// attaching sync saves each pivot row through the custom class, and each save touches its owner —
// insert into "article_user" (...) followed by update "articles" set "updated_at" = ? where
// "articles"."id" = ? (bindings ['2021-07-19 10:13:14', 1]) — so attach touches the parent. The
// detaching sync (test now '2021-07-20 19:13:14') deletes through the same class, and
// Model::delete() touches its owner before deleting: update "articles" set "updated_at" = ? where
// "articles"."id" = ? (bindings ['2021-07-20 19:13:14', '1']), then delete from "article_user"
// where ("article_id" = ? and "user_id" = ?) (bindings ['1', 3]). The result is detached = ['3']
// (count 1), attached = [], updated = []; after refresh the article's updated_at is
// '2021-07-20 19:13:14' and no user's updated_at is.
//
// The corpus passes the Article class as belongsToMany's related model — inert for the assertions,
// since the corpus casts the detached id to string and both corpus models declare $keyType =
// 'string' — so the Go relation names the real related model, User; the corpus's explicit (string)
// cast is ported as a format of whatever value the ORM returns.

// --- models ------------------------------------------------------------------

// belongsToManySyncTouchesParentArticle ports the corpus's TestArticle ($table = 'articles',
// $keyType = 'string', $incrementing = false).
type belongsToManySyncTouchesParentArticle struct {
	databaseorm.Timestamps

	ID    string `gorm:"column:id;primaryKey"`
	Title string `gorm:"column:title"`

	Users []*belongsToManySyncTouchesParentUser `gorm:"-"`
}

func (belongsToManySyncTouchesParentArticle) TableName() string { return "articles" }

func (belongsToManySyncTouchesParentArticle) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// users(): the corpus's belongsToMany(..., 'article_user', 'article_id', 'user_id')
		// ->using(TestArticleUser)->withTimestamps(). PivotTimestamps mirrors withTimestamps();
		// Touches mirrors the pivot class's $touches = ['article'] — a pivot save or delete
		// touches this article (Laravel runs one touch per pivot row; the Go writer touches once
		// after the batch, with the same observable outcome). The bump is gated by
		// isIgnoringTouch on this class — the gate every per-pivot touch rides through
		// Relation::touch — so a withoutTouching scope suppresses the single bump too.
		"Users": contractsorm.Many2Many{
			Related:         &belongsToManySyncTouchesParentUser{},
			Table:           "article_user",
			ForeignPivotKey: "article_id",
			RelatedPivotKey: "user_id",
			PivotTimestamps: true,
			Touches:         true,
		},
	}
}

// belongsToManySyncTouchesParentUser ports the corpus's TestUser ($table = 'users'; the PHP
// declares $keyType = 'string' and $incrementing = false over an integer key, so the Go model keeps
// the integer key the table and the pivot's user_id column use).
type belongsToManySyncTouchesParentUser struct {
	databaseorm.Timestamps

	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`

	Articles []*belongsToManySyncTouchesParentArticle `gorm:"-"`
}

func (belongsToManySyncTouchesParentUser) TableName() string { return "users" }

func (belongsToManySyncTouchesParentUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// articles(): the corpus's inverse relation. The pivot class touches 'article', which in
		// this direction is the related model, not the parent the Go Touches flag bumps, so the
		// inverse declares none. The corpus never syncs this direction.
		"Articles": contractsorm.Many2Many{
			Related:         &belongsToManySyncTouchesParentArticle{},
			Table:           "article_user",
			ForeignPivotKey: "user_id",
			RelatedPivotKey: "article_id",
			PivotTimestamps: true,
		},
	}
}

// belongsToManySyncTouchesParentArticleUser ports the corpus's custom pivot class TestArticleUser
// ($table = 'article_user', $touches = ['article']); see the file header for where its behaviours
// live in the Go ORM. The struct exists only to read pivot rows back in assertions.
type belongsToManySyncTouchesParentArticleUser struct {
	ArticleID string `gorm:"column:article_id"`
	UserID    uint   `gorm:"column:user_id"`
}

func (belongsToManySyncTouchesParentArticleUser) TableName() string { return "article_user" }

// --- schema and seeding ------------------------------------------------------

// belongsToManySyncTouchesParentSchema mirrors the corpus's createSchema() in sqlite DDL.
func belongsToManySyncTouchesParentSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table articles (id varchar not null primary key, title varchar not null, created_at datetime null, updated_at datetime null)`,
		`create table article_user (article_id varchar not null, user_id integer not null, created_at datetime null, updated_at datetime null, foreign key (article_id) references articles (id), foreign key (user_id) references users (id))`,
		`create table users (id integer primary key autoincrement, email varchar not null unique, created_at datetime null, updated_at datetime null)`)
}

// belongsToManySyncTouchesParentSeedData mirrors seedData(): the three users, ids 1, 2 and 3.
func belongsToManySyncTouchesParentSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, user := range []*belongsToManySyncTouchesParentUser{
		{ID: 1, Email: "taylorotwell@gmail.com"},
		{ID: 2, Email: "anonymous@gmail.com"},
		{ID: 3, Email: "anoni-mous@gmail.com"},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(user))
	}
}

// belongsToManySyncTouchesParentArticleUpdatedAt re-reads article 1 ($article->refresh()).
func belongsToManySyncTouchesParentArticleUpdatedAt(t *testing.T, query contractsorm.Query) *carbon.DateTime {
	t.Helper()

	var article belongsToManySyncTouchesParentArticle
	require.NoError(t, cloneRelationQuery(t, query).Where("id", "1").First(&article))
	require.NotNil(t, article.UpdatedAt)

	return article.UpdatedAt
}

// belongsToManySyncTouchesParentUserUpdatedAt re-reads one user (TestUser::find($id)) and formats
// updated_at the way the corpus does.
func belongsToManySyncTouchesParentUserUpdatedAt(t *testing.T, query contractsorm.Query, id uint) string {
	t.Helper()

	var user belongsToManySyncTouchesParentUser
	require.NoError(t, cloneRelationQuery(t, query).Where("id", id).First(&user))
	require.NotNil(t, user.UpdatedAt)

	return user.UpdatedAt.ToDateTimeString()
}

// belongsToManySyncTouchesParentPivotUserIDs reads article 1's pivot rows ordered by user id.
func belongsToManySyncTouchesParentPivotUserIDs(t *testing.T, query contractsorm.Query) []uint {
	t.Helper()

	var rows []belongsToManySyncTouchesParentArticleUser
	require.NoError(t, cloneRelationQuery(t, query).Where("article_id", "1").OrderBy("user_id").Get(&rows))

	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID)
	}

	return ids
}

// --- ports of the corpus -----------------------------------------------------

// Laravel testSyncWithDetachedValuesShouldTouch: seedData(); Carbon::setTestNow('2021-07-19
// 10:13:14'); $article = TestArticle::create(['id' => 1, 'title' => 'uuid title']);
// $article->users()->sync([1, 2, 3]); assertSame('2021-07-19 10:13:14', $article->updated_at);
// Carbon::setTestNow('2021-07-20 19:13:14'); $result = $article->users()->sync([1, 2]);
// assertCount(1, $result['detached']); assertSame('3', (string) first); $article->refresh();
// assertSame('2021-07-20 19:13:14', $article->updated_at); users 1-3 assertNotSame that value. See
// the file header for the clock mapping and the oracle-measured touch paths.
func TestBelongsToManySyncTouchesParent_SyncWithDetachedValuesShouldTouch(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManySyncTouchesParentSchema(t, query)
	belongsToManySyncTouchesParentSeedData(t, query)

	// Carbon::setTestNow('2021-07-19 10:13:14') — the Go port freezes the same instant through
	// support/carbon, which every stamp on this path honours (touchIfTouching's parent bump
	// included).
	carbon.SetTestNow(carbon.Parse("2021-07-19 10:13:14"))
	t.Cleanup(carbon.ClearTestNow)

	article := belongsToManySyncTouchesParentArticle{ID: "1", Title: "uuid title"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&article))

	// $article->users()->sync([1, 2, 3]);
	_, err := cloneRelationQuery(t, query).Relation(&article, "Users").Sync([]any{1, 2, 3})
	require.NoError(t, err)

	// $this->assertSame('2021-07-19 10:13:14', $article->updated_at->format('Y-m-d H:i:s'));
	require.NotNil(t, article.UpdatedAt)
	assert.Equal(t, "2021-07-19 10:13:14", article.UpdatedAt.ToDateTimeString())

	// The attaching sync wrote one pivot row per user (the corpus's attach through the custom
	// class); this is the state the detaching sync below starts from.
	require.Equal(t, []uint{1, 2, 3}, belongsToManySyncTouchesParentPivotUserIDs(t, query))

	user1Before := belongsToManySyncTouchesParentUserUpdatedAt(t, query, 1)
	user2Before := belongsToManySyncTouchesParentUserUpdatedAt(t, query, 2)
	user3Before := belongsToManySyncTouchesParentUserUpdatedAt(t, query, 3)

	// Carbon::setTestNow('2021-07-20 19:13:14');
	carbon.SetTestNow(carbon.Parse("2021-07-20 19:13:14"))

	// $result = $article->users()->sync([1, 2]);
	result, err := cloneRelationQuery(t, query).Relation(&article, "Users").Sync([]any{1, 2})
	require.NoError(t, err)

	// $this->assertCount(1, collect($result['detached']));
	// $this->assertSame('3', (string) collect($result['detached'])->first());
	require.Len(t, result.Detached, 1)
	assert.Equal(t, "3", fmt.Sprintf("%v", result.Detached[0]))

	// $article->refresh(); $this->assertSame('2021-07-20 19:13:14', $article->updated_at->format('Y-m-d H:i:s'));
	refreshedAt := belongsToManySyncTouchesParentArticleUpdatedAt(t, query)
	assert.Equal(t, "2021-07-20 19:13:14", refreshedAt.ToDateTimeString())

	// $user1 = TestUser::find(1); assertNotSame('2021-07-20 19:13:14', $user1->updated_at); likewise
	// users 2 and 3: the touch bumps the article only, so each user keeps its seed-time stamp and
	// differs from the article's freshly-touched one.
	assert.Equal(t, user1Before, belongsToManySyncTouchesParentUserUpdatedAt(t, query, 1))
	assert.Equal(t, user2Before, belongsToManySyncTouchesParentUserUpdatedAt(t, query, 2))
	assert.Equal(t, user3Before, belongsToManySyncTouchesParentUserUpdatedAt(t, query, 3))
	assert.NotEqual(t, "2021-07-20 19:13:14", belongsToManySyncTouchesParentUserUpdatedAt(t, query, 1))
	assert.NotEqual(t, "2021-07-20 19:13:14", belongsToManySyncTouchesParentUserUpdatedAt(t, query, 2))
	assert.NotEqual(t, "2021-07-20 19:13:14", belongsToManySyncTouchesParentUserUpdatedAt(t, query, 3))

	// The detaching sync removed user 3's pivot row and kept 1 and 2.
	require.Equal(t, []uint{1, 2}, belongsToManySyncTouchesParentPivotUserIDs(t, query))
}

// TestBelongsToManySyncTouchesParent_SyncInsideWithoutTouchingDoesNotBump ports an oracle probe of
// this suite's models (the corpus itself has no such test): a sync that detaches a pivot row inside
// a scope that ignores the article's touches still deletes the row — the custom pivot's delete
// touches its owner per row, and the gate that suppresses the touch is Relation::touch's
// isIgnoringTouch check on the bumped class — while the article's updated_at stays at the
// attaching sync's stamp. Oracle (illuminate/database v12.55.1, sqlite, Eloquent::withoutTouching):
// `delete from "article_user" where ("article_id" = ? and "user_id" = ?)` runs with no `update
// "articles"` and the article keeps `2021-07-19 10:13:14`. The Go writer's single post-batch bump
// carries the same gate.
func TestBelongsToManySyncTouchesParent_SyncInsideWithoutTouchingDoesNotBump(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManySyncTouchesParentSchema(t, query)
	belongsToManySyncTouchesParentSeedData(t, query)

	carbon.SetTestNow(carbon.Parse("2021-07-19 10:13:14"))
	t.Cleanup(carbon.ClearTestNow)

	article := belongsToManySyncTouchesParentArticle{ID: "1", Title: "uuid title"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&article))

	// The attaching sync outside any scope bumps the article to the first frozen instant.
	_, err := cloneRelationQuery(t, query).Relation(&article, "Users").Sync([]any{1, 2})
	require.NoError(t, err)
	require.NotNil(t, article.UpdatedAt)
	require.Equal(t, "2021-07-19 10:13:14", article.UpdatedAt.ToDateTimeString())

	// Carbon::setTestNow('2021-07-20 19:13:14') — the scoped detach below must not stamp it.
	carbon.SetTestNow(carbon.Parse("2021-07-20 19:13:14"))

	// Eloquent::withoutTouching in the oracle probe; the Go stand-in for the article's class is
	// WithoutTouchingOn([TestArticle]) — the fixture embeds Timestamps, not the base Model, so the
	// Model-scoped WithoutTouching does not reach it.
	require.NoError(t, databaseorm.WithoutTouchingOn([]any{&belongsToManySyncTouchesParentArticle{}}, func() error {
		_, err := cloneRelationQuery(t, query).Relation(&article, "Users").Sync([]any{1})
		return err
	}))

	// The pivot row is gone — the detach still wrote — but the article's timestamps stayed put,
	// in the row and on the in-memory instance.
	require.Equal(t, []uint{1}, belongsToManySyncTouchesParentPivotUserIDs(t, query))
	assert.Equal(t, "2021-07-19 10:13:14", belongsToManySyncTouchesParentArticleUpdatedAt(t, query).ToDateTimeString())
	require.NotNil(t, article.UpdatedAt)
	assert.Equal(t, "2021-07-19 10:13:14", article.UpdatedAt.ToDateTimeString())

	// Control: the same write outside the scope bumps the article again.
	carbon.SetTestNow(carbon.Parse("2021-07-21 09:09:09"))
	_, err = cloneRelationQuery(t, query).Relation(&article, "Users").Sync([]any{1, 2})
	require.NoError(t, err)
	assert.Equal(t, "2021-07-21 09:09:09", belongsToManySyncTouchesParentArticleUpdatedAt(t, query).ToDateTimeString())
}
