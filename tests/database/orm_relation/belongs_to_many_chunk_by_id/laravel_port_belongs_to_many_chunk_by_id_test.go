package belongs_to_many_chunk_by_id

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyChunkByIdTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyChunkById_<method without the test prefix>. The three tables mirror the corpus's
// createSchema() — users, articles, article_user — with the corpus's seed rows created through the
// ORM and the pivot written through the relation writer's Sync (Laravel's sync([3, 1, 2])).
//
// Both corpus tests drive a chunked read on the belongs-to-many relation — chunkById(1) and
// chunkByIdDesc(1) — and both now run for real: Query.Related returns a query whose ChunkById
// walks the keyset of the related model's qualified key, the defaults BelongsToMany::orderedChunkById
// installs for the joined read (column = the related key qualified by its table, alias = the bare
// key name). The page SQL is the oracle's (illuminate/database v12.55.1, executed against sqlite
// with this seed) and is pinned through the query log: the select list carries `"articles".*` plus
// the pivot keys aliased pivot_user_id / pivot_article_id, page one filters
// `"articles"."id" is not null`, every later page `"articles"."id" > ?` (or `< ?` descending) with
// the previous page's last id, and each page orders by `"articles"."id"` ascending (descending) and
// limits to one. Laravel's loop issues one final empty page after the last full page — the fourth
// statement below — which is where the walk ends. The framework's join fragment renders the ON
// operands in the opposite order from Laravel's (`"article_user"."article_id" = "articles"."id"`
// where the oracle prints `"articles"."id" = "article_user"."article_id"`); the predicates, order,
// limit and select list are otherwise the oracle's.

// --- models ------------------------------------------------------------------

// belongsToManyChunkByIdUser ports BelongsToManyChunkByIdTestTestUser.
type belongsToManyChunkByIdUser struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`

	Articles []*belongsToManyChunkByIdArticle `gorm:"-"`
}

func (belongsToManyChunkByIdUser) TableName() string { return "users" }

func (belongsToManyChunkByIdUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// articles(): belongsToMany(Article::class, 'article_user', 'user_id', 'article_id').
		"Articles": contractsorm.Many2Many{
			Related:         &belongsToManyChunkByIdArticle{},
			Table:           "article_user",
			ForeignPivotKey: "user_id",
			RelatedPivotKey: "article_id",
		},
	}
}

// belongsToManyChunkByIdArticle ports BelongsToManyChunkByIdTestTestArticle, whose PHP model declares
// $keyType = 'string' and $incrementing = false; the seeded ids are the integers 1, 2 and 3 either
// way, so the Go model keeps the integer key.
type belongsToManyChunkByIdArticle struct {
	ID    uint   `gorm:"column:id;primaryKey"`
	Title string `gorm:"column:title"`
}

func (belongsToManyChunkByIdArticle) TableName() string { return "articles" }

// --- schema and seeding ------------------------------------------------------

// belongsToManyChunkByIdSchema mirrors the corpus's createSchema() in sqlite DDL.
func belongsToManyChunkByIdSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique)`,
		`create table articles (id integer primary key autoincrement, title varchar not null)`,
		`create table article_user (id integer primary key autoincrement, article_id integer not null, user_id integer not null, foreign key (article_id) references articles (id), foreign key (user_id) references users (id))`)
}

// belongsToManyChunkByIdSeed mirrors seedData(): the one user, the three articles, sync([3, 1, 2]).
func belongsToManyChunkByIdSeed(t *testing.T, query contractsorm.Query) belongsToManyChunkByIdUser {
	t.Helper()

	user := belongsToManyChunkByIdUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	for _, article := range []belongsToManyChunkByIdArticle{
		{ID: 1, Title: "Another title"},
		{ID: 2, Title: "Another title"},
		{ID: 3, Title: "Another title"},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&article))
	}

	_, err := cloneRelationQuery(t, query).Relation(&user, "Articles").Sync([]any{3, 1, 2})
	require.NoError(t, err)

	return user
}

// --- ports of the corpus -----------------------------------------------------

// Laravel: seedData(); $user->articles()->chunkById(1, cb) — the callback fires three times, the
// page's first id equalling 1, 2, 3, and the test ends with i === 3 (the callback's counter).
func TestBelongsToManyChunkById_BelongsToChunkById(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyChunkByIdSchema(t, query)
	user := belongsToManyChunkByIdSeed(t, query)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	articles := []belongsToManyChunkByIdArticle{}
	i := 0
	require.NoError(t, withContext.WithContext(ctx).Related(&user, "Articles").ChunkById(1, &articles, func(page int) error {
		i++
		require.Len(t, articles, 1)
		require.Equal(t, i, int(articles[0].ID))

		return nil
	}))
	require.Equal(t, 3, i)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 4, "chunkById(1) issues one statement per page plus the empty terminator page")
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" is not null order by "articles"."id" asc limit 1`, logs[0].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" > 1 order by "articles"."id" asc limit 1`, logs[1].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" > 2 order by "articles"."id" asc limit 1`, logs[2].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" > 3 order by "articles"."id" asc limit 1`, logs[3].Query)
}

// Laravel: seedData(); $user->articles()->chunkByIdDesc(1, cb) — the callback fires three times,
// the page's first id being 3, 2, 1 (the corpus asserts 3 - i), and the test ends with i === 3.
func TestBelongsToManyChunkById_BelongsToChunkByIdDesc(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyChunkByIdSchema(t, query)
	user := belongsToManyChunkByIdSeed(t, query)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	articles := []belongsToManyChunkByIdArticle{}
	i := 0
	require.NoError(t, withContext.WithContext(ctx).Related(&user, "Articles").ChunkByIdDesc(1, &articles, func(page int) error {
		require.Len(t, articles, 1)
		require.Equal(t, 3-i, int(articles[0].ID))
		i++

		return nil
	}))
	require.Equal(t, 3, i)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 4, "chunkByIdDesc(1) issues one statement per page plus the empty terminator page")
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" is not null order by "articles"."id" desc limit 1`, logs[0].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" < 3 order by "articles"."id" desc limit 1`, logs[1].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" < 2 order by "articles"."id" desc limit 1`, logs[2].Query)
	assert.Equal(t, `select "articles".*, "article_user"."user_id" as "pivot_user_id", "article_user"."article_id" as "pivot_article_id" from "articles" inner join "article_user" ON "article_user"."article_id" = "articles"."id" where "article_user"."user_id" = 1 and "articles"."id" < 1 order by "articles"."id" desc limit 1`, logs[3].Query)
}
