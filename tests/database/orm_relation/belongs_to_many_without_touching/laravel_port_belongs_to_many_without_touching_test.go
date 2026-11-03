package belongs_to_many_without_touching

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyWithoutTouchingTest.php
// (12.x) onto the framework ORM. The corpus declares a single test,
// testItWillNotTouchRelatedModelsWhenUpdatingChild, over the users, articles and article_user
// tables; one Go test ports it, named TestBelongsToManyWithoutTouching_<method without the test
// prefix>. It runs the corpus's model shape against the harness sqlite database, probes the
// closest runnable chain, and skips on the divergence recorded below.
//
// # The corpus
//
// The test runs entirely on Mockery. It takes a partial Article mock whose getUpdatedAtColumn and
// freshTimestampString must never be called, asserts Article::isIgnoringTouch() is false, then
// inside Model::withoutTouching(fn): isIgnoringTouch() is true and a BelongsToMany — parent: a User
// mock whose id attribute is 1; related: the Article mock; table 'article_users' — built over a
// mocked Builder has touch() called, with the builder's update() required never to run. After the
// closure isIgnoringTouch() is false again. The behavior under the mocks: while the related class
// is ignoring touches, BelongsToMany::touch() returns before touching anything
// (`if ($this->related->isIgnoringTouch()) { return; }`), so the related models' timestamps stay
// put.
//
// # Oracle measurements (illuminate/database v12.55.1; the corpus's models executed against a real
// sqlite database: user 1 with article 1 attached through article_user)
//
//   - outside any scope Article::isIgnoringTouch() is false; inside Model::withoutTouching it is
//     true; after the closure it is false again.
//   - inside the scope $user->articles()->touch() issues 0 queries and the article's updated_at
//     stays at its backdated '2020-01-01 00:00:00' — the mocked never-calls, observed on a real
//     connection.
//   - outside the scope the same call issues 2 queries: select "article_id" from "article_user"
//     where "article_user"."user_id" = ? (bindings [1]) and then update "articles" set
//     "updated_at" = ? where "articles"."id" in (1) (bindings [now]) — the statement the
//     suppressed call never runs.
//
// # How our ORM diverges
//
// Go models carry the class-level touch control (database/orm.WithoutTouching / WithoutTouchingOn /
// IsIgnoringTouch), but there is no relation-level touch for a scope to gate — the
// RelationWriter surface ends at UpdateExistingPivot, so BelongsToMany::touch() has no Go call to
// reach, and the corpus model's $touches = ['user'] metadata has no Go field either. The ORM's
// nearest touch is Query.Touch, an unconditional explicit write, and on the relation side
// Many2Many.Touches, a pivot-write flag that bumps the parent — the opposite side of
// BelongsToMany::touch(); neither can be suppressed. Probe against the harness database: the
// corpus's users/articles/article_user fixture seeds and attaches through the relation writers and
// Related reads the attached article; after backdating the article, Query.Touch bumps its
// updated_at with 1 row affected, with no scope able to suppress it — where Laravel's suppressed
// touch issues no query at all (oracle above).

// --- models ------------------------------------------------------------------

// belongsToManyWithoutTouchingUser ports the corpus's User.
type belongsToManyWithoutTouchingUser struct {
	databaseorm.Timestamps

	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`

	Articles []*belongsToManyWithoutTouchingArticle `gorm:"-"`
}

func (belongsToManyWithoutTouchingUser) TableName() string { return "users" }

func (belongsToManyWithoutTouchingUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// articles(): belongsToMany(Article::class, 'article_user', 'user_id', 'article_id').
		"Articles": contractsorm.Many2Many{
			Related: &belongsToManyWithoutTouchingArticle{}, Table: "article_user",
			ForeignPivotKey: "user_id", RelatedPivotKey: "article_id",
		},
	}
}

// belongsToManyWithoutTouchingArticle ports the corpus's Article. Its $touches = ['user'] is
// PHP-only metadata with no Go field (see the file header); the relation itself is ported.
type belongsToManyWithoutTouchingArticle struct {
	databaseorm.Timestamps

	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Title string `gorm:"column:title"`

	Users []*belongsToManyWithoutTouchingUser `gorm:"-"`
}

func (belongsToManyWithoutTouchingArticle) TableName() string { return "articles" }

func (belongsToManyWithoutTouchingArticle) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// users(): belongsToMany(User::class, 'article_user', 'article_id', 'user_id').
		"Users": contractsorm.Many2Many{
			Related: &belongsToManyWithoutTouchingUser{}, Table: "article_user",
			ForeignPivotKey: "article_id", RelatedPivotKey: "user_id",
		},
	}
}

// belongsToManyWithoutTouchingArticleUser reads pivot rows back; the corpus never reads the pivot,
// it exists so the probe can show the attach landed.
type belongsToManyWithoutTouchingArticleUser struct {
	ArticleID uint `gorm:"column:article_id"`
	UserID    uint `gorm:"column:user_id"`
}

func (belongsToManyWithoutTouchingArticleUser) TableName() string { return "article_user" }

// --- schema and seeding ------------------------------------------------------

// belongsToManyWithoutTouchingSchema is the schema the corpus's models imply: users and articles
// carry Laravel's timestamps (the mocked getUpdatedAtColumn / freshTimestampString answers imply
// article's updated_at column), and article_user is the pivot; the mocked relation's table string
// is 'article_users', while the corpus models' relation — the one ported here — uses 'article_user'.
func belongsToManyWithoutTouchingSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique, created_at datetime null, updated_at datetime null)`,
		`create table articles (id integer primary key autoincrement, title varchar not null, created_at datetime null, updated_at datetime null)`,
		`create table article_user (user_id integer not null, article_id integer not null, foreign key (user_id) references users (id), foreign key (article_id) references articles (id))`,
	)
}

// belongsToManyWithoutTouchingSeed seeds user 1 and article 1 and attaches them through the
// relation writer — the state the corpus's parent mock (id 1) and related mock describe.
func belongsToManyWithoutTouchingSeed(t *testing.T, query contractsorm.Query) (belongsToManyWithoutTouchingUser, belongsToManyWithoutTouchingArticle) {
	t.Helper()

	user := belongsToManyWithoutTouchingUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	article := belongsToManyWithoutTouchingArticle{ID: 1, Title: "An Article"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&article))

	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Articles").Attach([]any{article.ID}))

	return user, article
}

// --- port of the corpus ------------------------------------------------------

// TestBelongsToManyWithoutTouching_ItWillNotTouchRelatedModelsWhenUpdatingChild ports
// testItWillNotTouchRelatedModelsWhenUpdatingChild.
func TestBelongsToManyWithoutTouching_ItWillNotTouchRelatedModelsWhenUpdatingChild(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithoutTouchingSchema(t, query)
	user, article := belongsToManyWithoutTouchingSeed(t, query)

	// The fixture: one pivot row links user 1 to article 1, and the related read the mocked
	// relation stands in for returns the article — the row Laravel's touch() would pluck the id of.
	pivot := []belongsToManyWithoutTouchingArticleUser{}
	require.NoError(t, cloneRelationQuery(t, query).Where("user_id", user.ID).Get(&pivot))
	require.Len(t, pivot, 1)
	require.Equal(t, article.ID, pivot[0].ArticleID)

	related := []belongsToManyWithoutTouchingArticle{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "Articles").Get(&related))
	require.Len(t, related, 1)
	require.Equal(t, article.ID, related[0].ID)

	// Probe: backdate the article's updated_at so a touch is observable, then run the ORM's touch.
	// There is no withoutTouching scope in the Go ORM and no relation-level touch: Query.Touch is
	// the only call to reach, and it writes unconditionally — the divergence the skip below records.
	_, err := cloneRelationQuery(t, query).Exec(`update articles set updated_at = ? where id = ?`, "2020-01-01 00:00:00", article.ID)
	require.NoError(t, err)

	result, err := cloneRelationQuery(t, query).Model(&belongsToManyWithoutTouchingArticle{ID: article.ID}).Touch()
	require.NoError(t, err)
	require.EqualValues(t, 1, result.RowsAffected)

	var reloaded belongsToManyWithoutTouchingArticle
	require.NoError(t, cloneRelationQuery(t, query).Where("id", article.ID).First(&reloaded))
	require.NotNil(t, reloaded.UpdatedAt)
	require.NotEqual(t, "2020-01-01 00:00:00", reloaded.UpdatedAt.ToDateTimeString())

	t.Skip("our ORM diverges: the class-level control is ported (database/orm.WithoutTouching / WithoutTouchingOn / IsIgnoringTouch), but the corpus runs entirely on Mockery — its BelongsToMany::touch() call and the builder->update() it must never run have no Go relation call to make (the RelationWriter surface ends at UpdateExistingPivot), and the corpus model's $touches = ['user'] metadata has no Go field; the nearest call, Query.Touch, writes updated_at unconditionally and no scope suppresses it (probe: the corpus fixture seeds and attaches, Related reads the article, and Query.Touch on the backdated row reports 1 row affected), where Laravel inside Model::withoutTouching issues no query at all (oracle, illuminate/database v12.55.1, executed against sqlite: isIgnoringTouch false -> true -> false; suppressed touch 0 queries with updated_at unchanged; unsuppressed touch selects the pivot's article_id then runs update \"articles\" set \"updated_at\" = ? where \"articles\".\"id\" in (1))")
}
