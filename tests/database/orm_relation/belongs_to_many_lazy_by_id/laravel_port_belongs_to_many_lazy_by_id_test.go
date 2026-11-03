package belongs_to_many_lazy_by_id

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyLazyByIdTest.php (12.x) onto
// the framework ORM. The corpus declares a single test, testBelongsToLazyById, over the users,
// articles and article_user tables, exercising the belongsToMany relation's lazyById chunked
// iteration; one Go test ports it, named TestBelongsToManyLazyById_<method without the test prefix>.
//
// # The corpus
//
// setUp() creates users (auto-incrementing id, unique email), articles (auto-incrementing aid,
// title) and the article_user pivot (article_id -> articles.aid, user_id -> users.id). seedData()
// creates user 1 (taylorotwell@gmail.com), inserts articles 1-3 by explicit aid — the Article model
// is the string-keyed, non-incrementing shape ($primaryKey = 'aid', $keyType = 'string',
// $incrementing = false) — and syncs the pivot to [3, 1, 2] through $user->articles().
// testBelongsToLazyById then walks $user->articles()->lazyById(1)->each(...), asserting each visited
// model's aid equals its 1-based visit index and that exactly three models were visited: lazyById
// must visit the related rows once each, ascending by the related key, one row per page.
//
// # The port
//
// Query.Related returns a query whose LazyById walks the keyset of the related key qualified by its
// table — `"articles"."aid"` — the defaults BelongsToMany::orderedChunkById installs for the joined
// read, handing each hydrated model to the callback one at a time. The oracle
// (illuminate/database v12.55.1, executed against sqlite with this seed) measures four statements
// for lazyById(1) over three rows: page one filters `"articles"."aid" is not null`, every later
// page `"articles"."aid" > ?` with bindings [1,1], [1,2], [1,3] — the fourth, past aid 3, is the
// empty page that ends the loop — and each page carries the pivot aliases and orders by
// `"articles"."aid"` ascending with limit 1. The test pins all four through the query log; the
// framework's join fragment renders the ON operands in the opposite order from Laravel's
// (`"article_user"."article_id" = "articles"."aid"` where the oracle prints
// `"articles"."aid" = "article_user"."article_id"`). The iteration visits aid 1, 2, 3 and stops
// with count 3.

// --- models ------------------------------------------------------------------

// belongsToManyLazyByIdUser ports BelongsToManyLazyByIdTestTestUser.
type belongsToManyLazyByIdUser struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`

	Articles []*belongsToManyLazyByIdArticle `gorm:"-"`
}

func (belongsToManyLazyByIdUser) TableName() string { return "users" }

func (belongsToManyLazyByIdUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// articles(): belongsToMany(Article::class, 'article_user', 'user_id', 'article_id').
		// Laravel resolves the related key from the Article model ($primaryKey = 'aid'); the Go
		// relation spells RelatedKey out, since it defaults to "id" (without it the join reads
		// "article_user"."article_id" = "articles"."id" and sqlite fails: no such column).
		"Articles": contractsorm.Many2Many{
			Related: &belongsToManyLazyByIdArticle{}, Table: "article_user",
			ForeignPivotKey: "user_id", RelatedPivotKey: "article_id", RelatedKey: "aid",
		},
	}
}

// belongsToManyLazyByIdArticle ports BelongsToManyLazyByIdTestTestArticle: primary key aid, declared
// string-keyed and non-incrementing. Those PHP-only flags cast the hydrated key; they do not reach
// the DDL or the relation SQL, so the Go model keeps the integer key.
type belongsToManyLazyByIdArticle struct {
	ID    uint   `gorm:"column:aid;primaryKey"`
	Title string `gorm:"column:title"`
}

func (belongsToManyLazyByIdArticle) TableName() string { return "articles" }

// --- schema and seeding ------------------------------------------------------

// belongsToManyLazyByIdSchema mirrors the corpus's createSchema(): the pivot carries Laravel's
// foreign keys onto articles.aid and users.id (the harness enables the foreign_keys pragma).
func belongsToManyLazyByIdSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique)`,
		`create table articles (aid integer primary key autoincrement, title varchar not null)`,
		`create table article_user (article_id integer not null, user_id integer not null, foreign key (article_id) references articles (aid), foreign key (user_id) references users (id))`,
	)
}

// belongsToManyLazyByIdSeedData ports seedData(): user 1, articles 1-3, then sync([3, 1, 2])
// through the relation writer. The pivot's insertion order differs from the key order lazyById
// walks, which is the point of the corpus test. Laravel bulk-inserts the articles through the query
// builder; the port creates them row by row — same rows, same ids.
func belongsToManyLazyByIdSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	user := belongsToManyLazyByIdUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	for aid := uint(1); aid <= 3; aid++ {
		article := belongsToManyLazyByIdArticle{ID: aid, Title: "Another title"}
		require.NoError(t, cloneRelationQuery(t, query).Create(&article))
	}

	_, err := cloneRelationQuery(t, query).Relation(&user, "Articles").Sync([]any{3, 1, 2})
	require.NoError(t, err)
}

// --- port of the corpus ------------------------------------------------------

// TestBelongsToManyLazyById_BelongsToLazyById ports testBelongsToLazyById.
func TestBelongsToManyLazyById_BelongsToLazyById(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyLazyByIdSchema(t, query)
	belongsToManyLazyByIdSeedData(t, query)

	// Laravel: $user = User::query()->first().
	user := belongsToManyLazyByIdUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&user).First(&user))
	require.Equal(t, uint(1), user.ID)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	article := belongsToManyLazyByIdArticle{}
	i := 0
	require.NoError(t, withContext.WithContext(ctx).Related(&user, "Articles").LazyById(1, &article, func(index int) error {
		i++
		require.Equal(t, i, int(article.ID))
		require.Equal(t, i-1, index)

		return nil
	}))
	require.Equal(t, 3, i)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 4, "lazyById(1) issues one statement per page plus the empty page that ends the loop")
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."aid" where "article_user"."user_id" = 1 and "articles"."aid" is not null order by "articles"."aid" asc limit 1`, logs[0].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."aid" where "article_user"."user_id" = 1 and "articles"."aid" > 1 order by "articles"."aid" asc limit 1`, logs[1].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."aid" where "article_user"."user_id" = 1 and "articles"."aid" > 2 order by "articles"."aid" asc limit 1`, logs[2].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."aid" where "article_user"."user_id" = 1 and "articles"."aid" > 3 order by "articles"."aid" asc limit 1`, logs[3].Query)
}
