package belongs_to_many_each_by_id

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyEachByIdTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyEachById_<method without the test prefix>. The corpus declares one test —
// testBelongsToEachById — over three tables (users, articles, and the article_user pivot), seeded
// through the ORM. The only relation touch is eachById's callback, and a ported test runs that
// relation explicitly: Go has no property-access magic.
//
// The corpus's eachById now runs for real: Query.Related returns a query whose EachById walks the
// keyset of the related model's qualified key, the defaults BelongsToMany::orderedChunkById
// installs for the joined read, hydrating the page's models before the callback. The oracle
// (illuminate/database v12.55.1, executed against sqlite with this seed) measures one statement —
// the 1000-row default page —
//
//	select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id"
//	as "pivot_article_id" from "articles" inner join "article_user" on "articles"."id" =
//	"article_user"."article_id" where "article_user"."user_id" = ? and "articles"."id" is not null
//	order by "articles"."id" asc limit 1000                      (bindings [1])
//
// which the test pins through the query log, with the framework's join fragment rendering the ON
// operands in the opposite order (`"article_user"."article_id" = "articles"."id"`). The callback
// visits the articles in key order — 1, 2, 3 — and eachById's index argument is the model's
// 0-based ordinal across the walk.

// --- models ------------------------------------------------------------------

// belongsToManyEachByIdUser ports BelongsToManyEachByIdTestTestUser.
type belongsToManyEachByIdUser struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`

	Articles []*belongsToManyEachByIdArticle `gorm:"-"`
}

func (belongsToManyEachByIdUser) TableName() string { return "users" }

func (belongsToManyEachByIdUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// articles(): belongsToMany(Article::class, 'article_user', 'user_id', 'article_id').
		"Articles": contractsorm.Many2Many{
			Related: &belongsToManyEachByIdArticle{}, Table: "article_user",
			ForeignPivotKey: "user_id", RelatedPivotKey: "article_id",
		},
	}
}

// belongsToManyEachByIdArticle ports BelongsToManyEachByIdTestTestArticle. The corpus marks it
// $keyType = 'string' / $incrementing = false; neither reaches the DDL or the relation SQL — they
// only cast the hydrated PHP key — so the Go model keeps the integer primary key the
// articles table declares.
type belongsToManyEachByIdArticle struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Title string `gorm:"column:title"`
}

func (belongsToManyEachByIdArticle) TableName() string { return "articles" }

// --- schema and seeding ------------------------------------------------------

// belongsToManyEachByIdSchema mirrors the corpus's createSchema(): the pivot carries Laravel's
// foreign keys onto users and articles (the harness enables the foreign_keys pragma).
func belongsToManyEachByIdSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique)`,
		`create table articles (id integer primary key autoincrement, title varchar not null)`,
		`create table article_user (id integer primary key autoincrement, article_id integer not null, user_id integer not null, foreign key (article_id) references articles (id), foreign key (user_id) references users (id))`,
	)
}

// belongsToManyEachByIdSeedData ports seedData(): user 1, articles 1-3, then sync([3, 1, 2]). The
// pivot's insertion order differs from the key order eachById walks, which is the point of the
// corpus test. Laravel bulk-inserts the articles through the query builder; the port creates them
// row by row — same rows, same ids.
func belongsToManyEachByIdSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	user := belongsToManyEachByIdUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	for id := uint(1); id <= 3; id++ {
		article := belongsToManyEachByIdArticle{ID: id, Title: "Another title"}
		require.NoError(t, cloneRelationQuery(t, query).Create(&article))
	}

	_, err := cloneRelationQuery(t, query).Relation(&user, "Articles").Sync([]any{3, 1, 2})
	require.NoError(t, err)
}

// --- port of the corpus ------------------------------------------------------

// Laravel: seedData(); $user->articles()->eachById(cb) — the callback visits every related article
// in key order (1, 2, 3) and the corpus's counter ends at 3. Laravel's count defaults to 1000; the
// Go form takes it explicitly.
func TestBelongsToManyEachById_BelongsToEachById(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyEachByIdSchema(t, query)
	belongsToManyEachByIdSeedData(t, query)

	// Laravel: $user = User::query()->first().
	user := belongsToManyEachByIdUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&user).First(&user))
	require.Equal(t, uint(1), user.ID)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	article := belongsToManyEachByIdArticle{}
	var (
		ids     []uint
		indexes []int
	)
	require.NoError(t, withContext.WithContext(ctx).Related(&user, "Articles").EachById(1000, &article, func(index int) error {
		ids = append(ids, article.ID)
		indexes = append(indexes, index)

		return nil
	}))

	assert.Equal(t, []uint{1, 2, 3}, ids)
	assert.Equal(t, []int{0, 1, 2}, indexes)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "eachById reads the relation in one 1000-row page")
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" is not null order by "articles"."id" asc limit 1000`, logs[0].Query)
}
