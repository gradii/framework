package belongs_to_many_without_touching

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyWithoutTouchingTest.php
// (12.x) onto the framework ORM. The corpus declares a single test,
// testItWillNotTouchRelatedModelsWhenUpdatingChild, over the users, articles and article_user
// tables; one Go test ports it, named TestBelongsToManyWithoutTouching_<method without the test
// prefix>. It runs the corpus's model shape against the harness sqlite database and asserts the
// corpus's observations below on that harness.
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
// # The port's shape
//
// The class-level control is ported (database/orm.WithoutTouching / WithoutTouchingOn /
// IsIgnoringTouch), and the relation-level touch the corpus calls exists as RelationQuery.Touch —
// Relation::touch's Go form, whose no-column path consults the same ignore gate per call
// (database/gorm/relations' IgnoringTouch hook), so the corpus's observations run for real: the
// false -> true -> false transition around the closure, the suppressed touch running no statement
// (the mocked getUpdatedAtColumn / freshTimestampString / builder->update never-calls, observed as
// an untouched row with zero affected rows), and the unsuppressed touch stamping the attached row
// with the relation's constraint scoping the update — the same rows Laravel's BelongsToMany::touch
// reaches by selecting the pivot ids first (oracle above). The corpus's $touches = ['user'] is
// inert for this test — nothing saves the mock — and a Go save/delete touch declaration is the
// relation descriptor's Touches flag, which no fixture here needs. (An earlier revision of this
// port skipped, believing no Go relation call existed for the corpus's touch(); the relation-level
// Touch and its ignore gate were present and are exercised here.)

// --- models ------------------------------------------------------------------

// belongsToManyWithoutTouchingUser ports the corpus's User.
type belongsToManyWithoutTouchingUser struct {
	databaseorm.Model

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

// belongsToManyWithoutTouchingArticle ports the corpus's Article. Its $touches = ['user'] is never
// exercised by the test body (nothing saves the mock); Go carries a save/delete touch declaration
// as the relation descriptor's Touches flag (contracts BelongsTo / HasOne / HasMany.Touches), not
// on the model, so the fixture declares none — the relation itself is ported.
type belongsToManyWithoutTouchingArticle struct {
	databaseorm.Model

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
// the fixture reads it so the attach is shown to have landed.
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

	user := belongsToManyWithoutTouchingUser{Email: "taylorotwell@gmail.com"}
	user.ID = 1
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	article := belongsToManyWithoutTouchingArticle{Title: "An Article"}
	article.ID = 1
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

	// Backdate the article's updated_at so a touch is observable; an unattached second article
	// stands outside the relation constraint.
	_, err := cloneRelationQuery(t, query).Exec(`update articles set updated_at = ? where id = ?`, "2020-01-01 00:00:00", article.ID)
	require.NoError(t, err)

	unattached := belongsToManyWithoutTouchingArticle{Title: "Unattached"}
	unattached.ID = 2
	require.NoError(t, cloneRelationQuery(t, query).Create(&unattached))
	_, err = cloneRelationQuery(t, query).Exec(`update articles set updated_at = ? where id = ?`, "2020-01-01 00:00:00", unattached.ID)
	require.NoError(t, err)

	// Laravel: outside the scope isIgnoringTouch() is false — the related class's touches are on.
	require.False(t, databaseorm.IsIgnoringTouch(&article), "outside the scope the related class's touches are enabled")

	// Laravel: inside Model::withoutTouching(fn) isIgnoringTouch() is true and BelongsToMany::touch()
	// returns before touching anything — the mocked builder->update() the corpus forbids runs no
	// statement and the related row's timestamp stays put.
	require.NoError(t, databaseorm.WithoutTouching(func() error {
		assert.True(t, databaseorm.IsIgnoringTouch(&article), "withoutTouching reaches every model that embeds the base Model")

		result, err := cloneRelationQuery(t, query).Related(&user, "Articles").Touch()
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.EqualValues(t, 0, result.RowsAffected, "the suppressed relation touch runs no update")

		return nil
	}))

	assert.False(t, databaseorm.IsIgnoringTouch(&article), "the scope restores the state when it ends")

	reloaded := belongsToManyWithoutTouchingArticle{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", article.ID).First(&reloaded))
	require.NotNil(t, reloaded.UpdatedAt)
	assert.Equal(t, "2020-01-01 00:00:00", reloaded.UpdatedAt.ToDateTimeString(), "the suppressed touch left the related row untouched")

	// Outside the scope the touch stamps again — the oracle's unsuppressed call — and the update
	// reaches exactly the attached row: the unattached article stays put.
	result, err := cloneRelationQuery(t, query).Related(&user, "Articles").Touch()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.EqualValues(t, 1, result.RowsAffected, "outside the scope the constrained touch reaches the attached row")

	require.NoError(t, cloneRelationQuery(t, query).Where("id", article.ID).First(&reloaded))
	require.NotNil(t, reloaded.UpdatedAt)
	assert.NotEqual(t, "2020-01-01 00:00:00", reloaded.UpdatedAt.ToDateTimeString(), "the attached row's updated_at is stamped fresh")

	untouched := belongsToManyWithoutTouchingArticle{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", unattached.ID).First(&untouched))
	require.NotNil(t, untouched.UpdatedAt)
	assert.Equal(t, "2020-01-01 00:00:00", untouched.UpdatedAt.ToDateTimeString(), "the unattached article stays outside the relation constraint")
}
