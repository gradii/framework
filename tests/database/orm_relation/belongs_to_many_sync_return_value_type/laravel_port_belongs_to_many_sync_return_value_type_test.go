package belongs_to_many_sync_return_value_type

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManySyncReturnValueTypeTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManySyncReturnValueType_<method without the test prefix>. The corpus declares two
// tests over three tables — users, articles and the article_user pivot — and each asks the same
// two questions of one write: what type the ids in the returned "attached" change set carry, and
// what the withPivot('visible') column reads back as through the relation.
//
// # The corpus
//
// setUp() creates users (auto-incrementing id, unique email), articles (a string primary key — the
// model sets $keyType = 'string' and $incrementing = false) and article_user (article_id ->
// articles.id, user_id -> users.id, visible boolean defaulting to false). seedData() creates user 1
// (taylorotwell@gmail.com) and inserts three articles: a uuid, (string) (PHP_INT_MAX + 1) and '1'.
// Both tests pluck the article ids, run one write — sync($articleIDs), respectively
// syncWithPivotValues($articleIDs, ['visible' => true]) — then assert that every id of the returned
// attached list has the model key's type (gettype($id) === 'string'), and that each
// property-loaded article's pivot->visible stringifies to '0' (plain sync: the column default) or
// '1' (with pivot defaults).
//
// # Oracle measurements (illuminate/database v12.55.1, the corpus's setUp/seedData/tests executed
// against a real sqlite database, PHP 8.3.22)
//
//   - (string) (PHP_INT_MAX + 1) overflows to a float and casts to the science-notation string
//     "9.2233720368548E+18"; the plucked ids are
//     ["7b7306ae-5a02-46fa-a84c-9538f45c7dd4", "9.2233720368548E+18", "1"], every gettype string.
//   - sync($articleIDs) reports attached = all three ids, each gettype string, and detached /
//     updated empty. Its statements: `select * from "article_user" where "article_user"."user_id"
//     = ?` (bindings [1]), then one `insert into "article_user" ("article_id", "user_id") values
//     (?, ?)` per id with bindings [<id>, 1] — visible stays at the column default.
//   - syncWithPivotValues reports the same attached list; each insert becomes `insert into
//     "article_user" ("article_id", "user_id", "visible") values (?, ?, ?)` with bindings
//     [<id>, 1, true], stored as integer 1.
//   - each property-loaded article carries pivot->visible as an integer — 0 after plain sync, 1
//     after syncWithPivotValues — so (string) of it is '0' / '1'.
//   - the lazy load is one joined read: `select "articles".*, "article_user"."user_id" as
//     "pivot_user_id", "article_user"."article_id" as "pivot_article_id", "article_user"."visible"
//     as "pivot_visible" from "articles" inner join "article_user" on "articles"."id" =
//     "article_user"."article_id" where "article_user"."user_id" = ?` (bindings [1]) —
//     withPivot('visible') is what extends Laravel's always-selected pivot aliases with visible.
//   - compiled DDL (sqlite_master): `CREATE TABLE "articles" ("id" varchar not null, "title"
//     varchar not null, primary key ("id"))`; `CREATE TABLE "article_user" ("article_id" varchar
//     not null, "user_id" integer not null, "visible" tinyint(1) not null default '0', foreign
//     key("article_id") references "articles"("id"), foreign key("user_id") references
//     "users"("id"))`.
//
// # How the port maps the corpus onto the Go ORM
//
//   - sync($articleIDs) is Relation(&user, "Articles").Sync(ids); syncWithPivotValues($articleIDs,
//     attrs) is Relation(&user, "Articles").SyncWithPivotValues(ids, attrs). SyncResult.Attached
//     carries the attached ids cast to the related model's key type — the Go analogue of Laravel's
//     stringified change set — so the port asserts every element is a Go string, its
//     gettype($id) === 'string' check.
//   - Laravel lazy-loads $user->articles through property access; the port calls
//     Load(&user, "Articles") explicitly (the harness's lazy-loading rule). The Pivot struct field
//     on the related model is the Go form of withPivot('visible'): the loader selects its columns
//     and hydrates each loaded article's Pivot, where $article->pivot->visible is
//     article.Pivot.Visible.
//   - the corpus pins no SQL of its own; the statements above are oracle measurements cited for
//     reference.
//
// The corpus's per-element assertions are quantified over collections, so the port adds length
// guards above each loop: neither may pass vacuously over an empty change set or an unloaded
// relation.

// --- models ------------------------------------------------------------------

// belongsToManySyncReturnValueTypeUser ports BelongsToManySyncTestTestUser.
type belongsToManySyncReturnValueTypeUser struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Email string `gorm:"column:email" db:"email"`

	Articles []*belongsToManySyncReturnValueTypeArticle `gorm:"-"`
}

func (belongsToManySyncReturnValueTypeUser) TableName() string { return "users" }

func (belongsToManySyncReturnValueTypeUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// articles(): belongsToMany(Article::class, 'article_user', 'user_id', 'article_id')
		//   ->withPivot('visible').
		"Articles": contractsorm.Many2Many{
			Related: &belongsToManySyncReturnValueTypeArticle{}, Table: "article_user",
			ForeignPivotKey: "user_id", RelatedPivotKey: "article_id",
		},
	}
}

// belongsToManySyncReturnValueTypeArticle ports BelongsToManySyncTestTestArticle: $keyType =
// 'string' with $incrementing = false, so the Go model declares the string key the articles table
// carries. Its Pivot field is the Go form of withPivot('visible').
type belongsToManySyncReturnValueTypeArticle struct {
	ID    string `gorm:"column:id;primaryKey" db:"id"`
	Title string `gorm:"column:title" db:"title"`

	Pivot belongsToManySyncReturnValueTypePivot `gorm:"-"`
}

func (belongsToManySyncReturnValueTypeArticle) TableName() string { return "articles" }

// belongsToManySyncReturnValueTypePivot carries the article_user columns each loaded article is
// hydrated with: the two pivot keys and the withPivot column visible. Its TableName satisfies the
// schema parser when the loader builds the pivot hydration plan; the struct is never queried as a
// model of its own.
type belongsToManySyncReturnValueTypePivot struct {
	ArticleID string `gorm:"column:article_id" db:"article_id"`
	UserID    uint   `gorm:"column:user_id" db:"user_id"`
	Visible   int    `gorm:"column:visible" db:"visible"`
}

func (belongsToManySyncReturnValueTypePivot) TableName() string { return "article_user" }

// --- schema and seeding ------------------------------------------------------

// belongsToManySyncReturnValueTypeSchema mirrors the corpus's createSchema() in sqlite DDL, shaped
// like Laravel's compiled statements (the string primary key on articles, varchar article_id,
// integer user_id and `visible tinyint(1) not null default '0'` on the pivot).
func belongsToManySyncReturnValueTypeSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement not null, email varchar not null unique)`,
		`create table articles (id varchar not null, title varchar not null, primary key (id))`,
		`create table article_user (article_id varchar not null, user_id integer not null, visible tinyint(1) not null default '0', foreign key (article_id) references articles (id), foreign key (user_id) references users (id))`,
	)
}

// belongsToManySyncReturnValueTypeSeedData ports seedData(): user 1, then the three articles whose
// ids exercise the key-type casting — a uuid, the science-notation string PHP's
// (string) (PHP_INT_MAX + 1) produces, and '1'. Laravel bulk-inserts the articles through the
// query builder; the port creates them row by row — same rows, same ids.
func belongsToManySyncReturnValueTypeSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	user := belongsToManySyncReturnValueTypeUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	for _, article := range []belongsToManySyncReturnValueTypeArticle{
		{ID: "7b7306ae-5a02-46fa-a84c-9538f45c7dd4", Title: "uuid title"},
		{ID: "9.2233720368548E+18", Title: "Another title"},
		{ID: "1", Title: "Another title"},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&article))
	}
}

// belongsToManySyncReturnValueTypePluckArticleIDs ports
// BelongsToManySyncTestTestArticle::all()->pluck('id')->toArray(): one read of every article, in
// table order, taking just the ids — all Go strings, the model's string key type.
func belongsToManySyncReturnValueTypePluckArticleIDs(t *testing.T, query contractsorm.Query) []any {
	t.Helper()

	var articles []belongsToManySyncReturnValueTypeArticle
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManySyncReturnValueTypeArticle{}).Get(&articles))
	require.Len(t, articles, 3)

	ids := make([]any, 0, len(articles))
	for _, article := range articles {
		ids = append(ids, article.ID)
	}

	return ids
}

// belongsToManySyncReturnValueTypeFirstUser ports User::query()->first().
func belongsToManySyncReturnValueTypeFirstUser(t *testing.T, query contractsorm.Query) belongsToManySyncReturnValueTypeUser {
	t.Helper()

	user := belongsToManySyncReturnValueTypeUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&user).First(&user))
	require.Equal(t, uint(1), user.ID)

	return user
}

// --- ports of the corpus -----------------------------------------------------

// TestBelongsToManySyncReturnValueType_SyncReturnValueType ports testSyncReturnValueType.
func TestBelongsToManySyncReturnValueType_SyncReturnValueType(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManySyncReturnValueTypeSchema(t, query)
	belongsToManySyncReturnValueTypeSeedData(t, query)

	user := belongsToManySyncReturnValueTypeFirstUser(t, query)
	articleIDs := belongsToManySyncReturnValueTypePluckArticleIDs(t, query)

	// Laravel: $changes = $user->articles()->sync($articleIDs).
	changes, err := cloneRelationQuery(t, query).Relation(&user, "Articles").Sync(articleIDs)
	require.NoError(t, err)

	// Laravel: collect($changes['attached'])->map(fn ($id) => assertSame(gettype($id),
	// (new Article)->getKeyType())) — every attached id is a string. Oracle: attached holds all
	// three ids, each gettype string; the length guard keeps the loop from passing vacuously.
	require.Len(t, changes.Attached, 3)
	require.ElementsMatch(t, articleIDs, changes.Attached)
	for _, id := range changes.Attached {
		require.IsType(t, "", id, "each attached id must be a Go string, the related model's string key type")
	}

	// Laravel: $user->articles->each(fn ($article) => assertSame('0',
	// (string) $article->pivot->visible)) — plain sync leaves visible at its false/0 default.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Articles"))
	require.Len(t, user.Articles, 3)
	for _, article := range user.Articles {
		// Go's int zero value would let this "0" check pass even when the loader hydrated
		// nothing (in PHP, a pivot missing the column reads null, which fails the assertion);
		// assert the pivot's own key columns first so the value check cannot pass vacuously.
		require.Equal(t, article.ID, article.Pivot.ArticleID, "the loaded article's pivot must be hydrated from article_user")
		require.Equal(t, user.ID, article.Pivot.UserID, "the loaded article's pivot must be hydrated from article_user")
		require.Equal(t, "0", strconv.Itoa(article.Pivot.Visible))
	}
}

// TestBelongsToManySyncReturnValueType_SyncWithPivotDefaultsReturnValueType ports
// testSyncWithPivotDefaultsReturnValueType.
func TestBelongsToManySyncReturnValueType_SyncWithPivotDefaultsReturnValueType(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManySyncReturnValueTypeSchema(t, query)
	belongsToManySyncReturnValueTypeSeedData(t, query)

	user := belongsToManySyncReturnValueTypeFirstUser(t, query)
	articleIDs := belongsToManySyncReturnValueTypePluckArticleIDs(t, query)

	// Laravel: $changes = $user->articles()->syncWithPivotValues($articleIDs, ['visible' => true]).
	changes, err := cloneRelationQuery(t, query).Relation(&user, "Articles").SyncWithPivotValues(articleIDs, map[string]any{"visible": true})
	require.NoError(t, err)

	// Same attached-type check as above. Oracle: attached holds all three ids, each gettype string.
	require.Len(t, changes.Attached, 3)
	require.ElementsMatch(t, articleIDs, changes.Attached)
	for _, id := range changes.Attached {
		require.IsType(t, "", id, "each attached id must be a Go string, the related model's string key type")
	}

	// Laravel: each loaded article's pivot->visible stringifies to '1' — the defaults this call
	// wrote (stored as integer 1).
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Articles"))
	require.Len(t, user.Articles, 3)
	for _, article := range user.Articles {
		require.Equal(t, "1", strconv.Itoa(article.Pivot.Visible))
	}
}
