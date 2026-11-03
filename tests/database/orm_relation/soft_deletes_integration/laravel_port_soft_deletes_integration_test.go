package soft_deletes_integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentSoftDeletesIntegrationTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestSoftDeletesIntegration_<method without the test prefix>. The five tables mirror the corpus's
// createSchema(); rows are seeded through the ORM, and relations are loaded explicitly through
// OrmBuilder.Load — the harness's lazy-loading rule, since Go has no property-access magic.
//
// Every SQL expectation this file cites was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled:
//
//	relation toSql:   select * from "posts" where "posts"."user_id" = ? and "posts"."user_id" is not
//	                  null and "posts"."deleted_at" is null
//	newQuery toSql:   select * from "users" where "users"."deleted_at" is null
//	orWhere toSql:    select * from "users" where ("email" = ? or "email" = ?) and "users"."deleted_at" is null
//	withCount:        (select count(*) from "posts" where "users"."id" = "posts"."user_id" and
//	                  "posts"."deleted_at" is null) as "posts_count", with "is not null" under
//	                  onlyTrashed and no predicate under withTrashed
//	cursor order:     Laravel's cursorPaginate defaults the order to the primary key
//
// Where the ORM cannot reproduce Laravel's SQL or behaviour the test is skipped with the concrete
// divergence and its probe, rather than weakened. The recurring gaps this corpus hits:
//
//   - the relation load path (Load / With / LoadMissing) never applies the related model's
//     soft-delete predicate: the loader builds a table-only child query, so soft-deleted related
//     rows are hydrated, where Laravel's lazy load filters them (probe: after soft-deleting one of
//     two posts, Load returns len 2 with the trashed row's deleted_at set; Related().First and
//     Related().Count filter the same read correctly);
//   - onlyTrashed() / withTrashed() callbacks and relation default scopes do not reach existence or
//     aggregate subqueries — applyRelationScope carries only where clauses, and the subquery always
//     carries the default "deleted_at is null" (probes under the whereHas / withCount ports);
//   - OrWhere is not grouped against the appended soft-delete predicate, and the delete/restore
//     paths do not sync the in-memory model (no getOriginal snapshot).

// The morph map registers the corpus's two user kinds under stable aliases; the comments table's
// owner_type column carries them. MorphTo eager loads resolve the per-row type through this
// registry, so both kinds must be registered for the polymorphic ports.
func init() {
	databaseorm.MorphMap(map[string]any{
		"soft_deletes_test_user":        &softDeletesTestUser{},
		"test_user_without_soft_delete": &testUserWithoutSoftDelete{},
	})
}

// softDeletesSkipLoadGap is the recurrence the four relation-lazy-load ports share; each appends
// its own probe.
const softDeletesSkipLoadGap = "our ORM diverges: the relation load path (Load / With) never applies the related model's soft-delete predicate — the loader builds a table-only child query, so soft-deleted rows are hydrated where Laravel's lazy load filters them; "

const softDeletesSkipPHPOnly = "PHP-only assertion (no Go equivalent): "

// --- models ------------------------------------------------------------------

// softDeletesTestUser ports SoftDeletesTestUser: users with the framework's soft-delete field and
// the corpus's four relations.
type softDeletesTestUser struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	GroupID   uint             `gorm:"column:group_id"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	SelfReferencing []*softDeletesTestUser  `gorm:"-"`
	Posts           []*softDeletesTestPost  `gorm:"-"`
	Address         *softDeletesTestAddress `gorm:"-"`
	Group           *softDeletesTestGroup   `gorm:"-"`
}

func (softDeletesTestUser) TableName() string { return "users" }

func (softDeletesTestUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// self_referencing(): hasMany(self, user_id)->onlyTrashed().
		"SelfReferencing": contractsorm.HasMany{Related: &softDeletesTestUser{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }},
		"Posts":   contractsorm.HasMany{Related: &softDeletesTestPost{}, ForeignKey: "user_id"},
		"Address": contractsorm.HasOne{Related: &softDeletesTestAddress{}, ForeignKey: "user_id"},
		"Group":   contractsorm.BelongsTo{Related: &softDeletesTestGroup{}, ForeignKey: "group_id"},
	}
}

// softDeletesTestUserWithTrashedPosts ports SoftDeletesTestUserWithTrashedPosts: posts() with the
// withTrashed() default scope.
type softDeletesTestUserWithTrashedPosts struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	GroupID   uint             `gorm:"column:group_id"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts []*softDeletesTestPost `gorm:"-"`
}

func (softDeletesTestUserWithTrashedPosts) TableName() string { return "users" }

func (softDeletesTestUserWithTrashedPosts) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &softDeletesTestPost{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() }},
	}
}

// testUserWithoutSoftDelete ports TestUserWithoutSoftDelete: the same users table without the
// soft-delete field, so its deletes are hard deletes.
type testUserWithoutSoftDelete struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	GroupID   uint             `gorm:"column:group_id"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (testUserWithoutSoftDelete) TableName() string { return "users" }

func (testUserWithoutSoftDelete) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// posts(): hasMany(SoftDeletesTestPost::class, 'user_id').
		"Posts": contractsorm.HasMany{Related: &softDeletesTestPost{}, ForeignKey: "user_id"},
	}
}

// softDeletesTestPost ports SoftDeletesTestPost.
type softDeletesTestPost struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	Title     string           `gorm:"column:title"`
	Priority  int              `gorm:"column:priority"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Comments []*softDeletesTestComment `gorm:"-"`
}

func (softDeletesTestPost) TableName() string { return "posts" }

func (softDeletesTestPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Comments": contractsorm.HasMany{Related: &softDeletesTestComment{}, ForeignKey: "post_id"},
	}
}

// softDeletesTestComment ports SoftDeletesTestComment / SoftDeletesTestCommentWithTrashed (the two
// corpus classes differ in name only). Its Owner is a soft-deleting user, resolved from owner_type.
type softDeletesTestComment struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID   uint             `gorm:"column:owner_id"`
	OwnerType string           `gorm:"column:owner_type"`
	PostID    uint             `gorm:"column:post_id"`
	Body      string           `gorm:"column:body"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Owner *softDeletesTestUser `gorm:"-"`
}

func (softDeletesTestComment) TableName() string { return "comments" }

func (softDeletesTestComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.MorphTo{Name: "owner"},
	}
}

// testCommentWithoutSoftDelete ports TestCommentWithoutSoftDelete: comments without the
// soft-delete field, owner resolved to the soft-deleting user.
type testCommentWithoutSoftDelete struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID   uint             `gorm:"column:owner_id"`
	OwnerType string           `gorm:"column:owner_type"`
	PostID    uint             `gorm:"column:post_id"`
	Body      string           `gorm:"column:body"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Owner *softDeletesTestUser `gorm:"-"`
}

func (testCommentWithoutSoftDelete) TableName() string { return "comments" }

func (testCommentWithoutSoftDelete) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.MorphTo{Name: "owner"},
	}
}

// softDeletesTestCommentForPlainUser is the corpus's SoftDeletesTestCommentWithTrashed used with a
// TestUserWithoutSoftDelete owner: a soft-deleting comment whose Owner field holds the plain user
// type (Go relation fields are typed; PHP's dynamic attributes are not).
type softDeletesTestCommentForPlainUser struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID   uint             `gorm:"column:owner_id"`
	OwnerType string           `gorm:"column:owner_type"`
	PostID    uint             `gorm:"column:post_id"`
	Body      string           `gorm:"column:body"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Owner *testUserWithoutSoftDelete `gorm:"-"`
}

func (softDeletesTestCommentForPlainUser) TableName() string { return "comments" }

func (softDeletesTestCommentForPlainUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.MorphTo{Name: "owner"},
	}
}

// softDeletesTestAddress ports SoftDeletesTestAddress.
type softDeletesTestAddress struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	Address   string           `gorm:"column:address"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (softDeletesTestAddress) TableName() string { return "addresses" }

// softDeletesTestGroup ports SoftDeletesTestGroup.
type softDeletesTestGroup struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (softDeletesTestGroup) TableName() string { return "groups" }

// --- schema and seeding ------------------------------------------------------

// softDeletesTestSchema mirrors the corpus's createSchema() in sqlite DDL: every table carries
// timestamps() and softDeletes(); users.email is unique.
func softDeletesTestSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, user_id integer null, group_id integer null, email varchar not null unique, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
		`create table posts (id integer primary key autoincrement, user_id integer not null, title varchar not null, priority integer not null default 0, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
		`create table comments (id integer primary key autoincrement, owner_id integer null, owner_type varchar null, post_id integer not null, body varchar not null, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
		`create table addresses (id integer primary key autoincrement, user_id integer not null, address varchar not null, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
		`create table groups (id integer primary key autoincrement, name varchar not null, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
	)
}

// softDeletesCreateUsers ports createUsers(): taylor (id 1, user_id pointing at abigail) is created
// and soft deleted, abigail (id 2) stays live. The corpus's explicit ids are written through map
// inserts carrying them.
func softDeletesCreateUsers(t *testing.T, query contractsorm.Query) (softDeletesTestUser, softDeletesTestUser) {
	t.Helper()

	taylor := softDeletesTestUser{ID: 1, Email: "taylorotwell@gmail.com", UserID: 2}
	require.NoError(t, cloneRelationQuery(t, query).Model(&taylor).Create(map[string]any{"id": uint(1), "email": "taylorotwell@gmail.com", "user_id": uint(2)}))

	abigail := softDeletesTestUser{ID: 2, Email: "abigailotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&abigail).Create(map[string]any{"id": uint(2), "email": "abigailotwell@gmail.com"}))

	_, err := cloneRelationQuery(t, query).Delete(&taylor)
	require.NoError(t, err)

	return taylor, abigail
}

// softDeletesCreatePost ports $user->posts()->create(['title' => ...]).
func softDeletesCreatePost(t *testing.T, query contractsorm.Query, user *softDeletesTestUser, title string) softDeletesTestPost {
	t.Helper()

	post := softDeletesTestPost{Title: title}
	require.NoError(t, cloneRelationQuery(t, query).Relation(user, "Posts").Create(&post))

	return post
}

// softDeletesFindUser ports SoftDeletesTestUser::where('email', ...)->first().
func softDeletesFindUser(t *testing.T, query contractsorm.Query, email string) softDeletesTestUser {
	t.Helper()

	user := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Where("email", email).First(&user))

	return user
}

// --- ports of the corpus -----------------------------------------------------

func TestSoftDeletesIntegration_SoftDeletesAreNotRetrieved(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&users))
	require.Len(t, users, 1)
	assert.Equal(t, uint(2), users[0].ID)

	// find(1) is null: a miss leaves the destination empty (the Go read reports no row as an empty
	// model rather than nil).
	found := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&found, 1))
	assert.Zero(t, found.ID)
}

func TestSoftDeletesIntegration_SoftDeletesAreNotRetrievedFromBaseQuery(t *testing.T) {
	// Laravel: SoftDeletesTestUser::query()->toBase() returns the underlying query builder, and its
	// get() reads one row. Go is statically typed — there is no separate base-builder type to assert
	// with instanceof — so the port asserts the scoped read itself: the same query, read through the
	// model, returns the one non-deleted row.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	rows := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&rows))
	assert.Len(t, rows, 1)
}

func TestSoftDeletesIntegration_SoftDeletesAreNotRetrievedFromRelationshipBaseQuery(t *testing.T) {
	// Same toBase()/instanceof note as the base-query port: the observable is the relation read.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	_, abigail := softDeletesCreateUsers(t, query)

	first := softDeletesCreatePost(t, query, &abigail, "Foo")
	second := softDeletesCreatePost(t, query, &abigail, "Bar")
	_, err := cloneRelationQuery(t, query).Delete(&second)
	require.NoError(t, err)

	posts := []softDeletesTestPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&abigail, "Posts").Get(&posts))
	assert.Len(t, posts, 1)
	assert.Equal(t, first.ID, posts[0].ID)
}

// freezeSoftDeletesClock pins the clock the way the corpus's setUp does, so the query log's inlined
// updated_at binding is deterministic.
func freezeSoftDeletesClock(t *testing.T) {
	t.Helper()

	carbon.SetTestNow(carbon.Parse("2023-01-01 00:00:00"))
	t.Cleanup(carbon.ClearTestNow)
}

func TestSoftDeletesIntegration_SoftDeletesAreNotRetrievedFromBuilderHelpers(t *testing.T) {
	// Laravel runs the soft-delete scope through chunk(2, cb), pluck('email'), paginate(2),
	// simplePaginate(2), cursorPaginate(2), increment('id') and decrement('id'), asserting each
	// helper still sees only the one non-deleted user. Flipped by the decisions-round gate once
	// both blockers of the old skip existed on the Query contract (Chunk, SimplePaginate).
	// Divergence kept explicit: Go's CursorPaginate requires an explicit OrderBy where Laravel
	// defaults the cursor order to the primary key, so the port orders by id.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	_, abigail := softDeletesCreateUsers(t, query)

	chunks, seen := 0, 0
	chunk := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Chunk(2, &chunk, func(page int) error {
		chunks++
		seen += len(chunk)
		return nil
	}))
	assert.Equal(t, 1, chunks)
	assert.Equal(t, 1, seen)

	emails := []string{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Pluck("email", &emails))
	assert.Equal(t, []string{abigail.Email}, emails)

	page := []softDeletesTestUser{}
	var total int64
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Paginate(1, 2, &page, &total))
	assert.Len(t, page, 1)
	assert.Equal(t, int64(1), total)

	simple := []softDeletesTestUser{}
	hasMore, err := cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).SimplePaginate(1, 2, &simple)
	require.NoError(t, err)
	assert.Len(t, simple, 1)
	assert.False(t, hasMore)

	cursorRows := []softDeletesTestUser{}
	_, err = cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).OrderBy("id").CursorPaginate(2, "", &cursorRows)
	require.NoError(t, err)
	assert.Len(t, cursorRows, 1)

	// The corpus's increment('id') / decrement('id') address the trashed user by email and report 0
	// affected rows, and the bare scoped pair reports 1: the soft-delete scope joins the WHERE, so
	// the trashed row is never touched and the one live row is stepped. The four statements below pin
	// the compiled SET list against the oracle (illuminate/database v12.55.1, executed: each carries
	// `"updated_at" = ?`); the clock is frozen so the log's inlined timestamp is deterministic.
	freezeSoftDeletesClock(t)

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	affected, err := withContext.WithContext(ctx).Model(&softDeletesTestUser{}).Where("email", "taylorotwell@gmail.com").Increment("id")
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected.RowsAffected)

	affected, err = withContext.WithContext(ctx).Model(&softDeletesTestUser{}).Where("email", "taylorotwell@gmail.com").Decrement("id")
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected.RowsAffected)

	// A bare scoped increment / decrement was once asserted here as a divergence: the mass-update
	// guard refused it (errors.OrmMissingWhereClause) because the scope-supplied WHERE was not
	// counted. Laravel compiles `update "users" set "id" = "id" + 1, "updated_at" = ? where
	// "users"."deleted_at" is null` and reports 1 affected row (oracle, sqlite) - the one live user -
	// and so does the guard now that the compiled WHERE clause is what it reads.
	affected, err = withContext.WithContext(ctx).Model(&softDeletesTestUser{}).Increment("id")
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected.RowsAffected)

	affected, err = withContext.WithContext(ctx).Model(&softDeletesTestUser{}).Decrement("id")
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected.RowsAffected)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 4)
	assert.Equal(t, `update "users" set "id" = "id" + 1, "updated_at" = '2023-01-01 00:00:00' where "email" = 'taylorotwell@gmail.com' and "users"."deleted_at" is null`, logs[0].Query)
	assert.Equal(t, `update "users" set "id" = "id" - 1, "updated_at" = '2023-01-01 00:00:00' where "email" = 'taylorotwell@gmail.com' and "users"."deleted_at" is null`, logs[1].Query)
	assert.Equal(t, `update "users" set "id" = "id" + 1, "updated_at" = '2023-01-01 00:00:00' where "users"."deleted_at" is null`, logs[2].Query)
	assert.Equal(t, `update "users" set "id" = "id" - 1, "updated_at" = '2023-01-01 00:00:00' where "users"."deleted_at" is null`, logs[3].Query)
}

func TestSoftDeletesIntegration_WithTrashedReturnsAllRecords(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&users))
	assert.Len(t, users, 2)

	// Laravel additionally asserts the result is an Eloquent instance; the Go equivalent is a
	// hydrated model.
	found := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).WithTrashed().Find(&found, 1))
	assert.Equal(t, uint(1), found.ID)
}

func TestSoftDeletesIntegration_WithTrashedAcceptsAnArgument(t *testing.T) {
	// withTrashed(false) keeps the scope and withTrashed(true) drops it. The Go WithTrashed() takes
	// no argument — it always drops the scope — so withTrashed(true) ports to WithTrashed() and
	// withTrashed(false) to the default scoped chain; both observables are asserted.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	scoped := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&scoped))
	assert.Len(t, scoped, 1)

	all := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&all))
	assert.Len(t, all, 2)
}

func TestSoftDeletesIntegration_DeleteSetsDeletedColumn(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	// deleted_at is a Carbon instance on the trashed row; the Go DeletedAt carries a valid time.
	taylor := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).WithTrashed().Find(&taylor, 1))
	assert.True(t, taylor.DeletedAt.Valid)
	assert.False(t, taylor.DeletedAt.Time.IsZero())

	abigail := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&abigail, 2))
	assert.False(t, abigail.DeletedAt.Valid)
}

func TestSoftDeletesIntegration_ForceDeleteActuallyDeletesRecords(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	user := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&user, 2))
	res, err := cloneRelationQuery(t, query).ForceDelete(&user)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&users))
	require.Len(t, users, 1)
	assert.Equal(t, uint(1), users[0].ID)
}

func TestSoftDeletesIntegration_ForceDeleteUpdateExistsProperty(t *testing.T) {
	// Laravel asserts the in-memory $user->exists flag flips true -> false around forceDelete().
	t.Skip("our ORM diverges: the Go ORM's models carry no exists flag — model-level persistence state has no Go counterpart, so the property cannot be observed; the row-level effect (forceDelete removes the record) is asserted by the sibling ports")
}

func TestSoftDeletesIntegration_ForceDeleteDoesntUpdateExistsPropertyIfFailed(t *testing.T) {
	// Laravel replaces newModelQuery() with a Mockery spy whose forceDelete throws, then asserts the
	// exists flag stays true.
	t.Skip(softDeletesSkipPHPOnly + "the test asserts a PHP mock interaction (Mockery shouldReceive on newModelQuery) and the in-memory exists flag surviving a throwing forceDelete — Go closures cannot spy on the query the ORM builds internally, and the model has no exists flag")
}

func TestSoftDeletesIntegration_ForceDestroyFullyDeletesRecord(t *testing.T) {
	// forceDestroy(2) is the model's static destroy: whereKey(2)->forceDelete(). The port pins the
	// key with WhereKey and forces the delete.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	res, err := cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WhereKey(uint(2)).ForceDelete()
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&users))
	require.Len(t, users, 1)
	assert.Equal(t, uint(1), users[0].ID)

	found := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&found, 2))
	assert.Zero(t, found.ID)
}

func TestSoftDeletesIntegration_ForceDestroyDeletesAlreadyDeletedRecord(t *testing.T) {
	// forceDestroy(1) targets the already-trashed row; ForceDelete forces WithTrashed, so the row
	// is reachable and is removed.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	res, err := cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WhereKey(uint(1)).ForceDelete()
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&users))
	require.Len(t, users, 1)
	assert.Equal(t, uint(2), users[0].ID)

	found := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&found, 1))
	assert.Zero(t, found.ID)
}

func TestSoftDeletesIntegration_ForceDestroyDeletesMultipleRecords(t *testing.T) {
	// forceDestroy([1, 2]): a slice stands for several keys, so WhereKey renders an IN list.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	res, err := cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WhereKey([]any{uint(1), uint(2)}).ForceDelete()
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.RowsAffected)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&users))
	assert.Empty(t, users)
}

func TestSoftDeletesIntegration_ForceDestroyDeletesRecordsFromCollection(t *testing.T) {
	// forceDestroy(collect([1, 2])): a PHP Collection of ids is the same list in Go — a slice.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	res, err := cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WhereKey([]any{uint(1), uint(2)}).ForceDelete()
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.RowsAffected)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&users))
	assert.Empty(t, users)
}

func TestSoftDeletesIntegration_ForceDestroyDeletesRecordsFromEloquentCollection(t *testing.T) {
	// forceDestroy(SoftDeletesTestUser::all()): the collection holds the one live row (id 2), and
	// ForceDelete with the model slice keys the statement by the slice's ids.
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&users))
	require.Len(t, users, 1)

	res, err := cloneRelationQuery(t, query).ForceDelete(&users)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	left := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&left))
	require.Len(t, left, 1)
	assert.Equal(t, uint(1), left[0].ID)

	found := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&found, 2))
	assert.Zero(t, found.ID)
}

func TestSoftDeletesIntegration_RestoreRestoresRecords(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	taylor := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).WithTrashed().Find(&taylor, 1))

	// $taylor->trashed(): the framework's observable is the deleted_at field carrying a value.
	assert.True(t, taylor.DeletedAt.Valid)

	// restore(): the Go Restore is scoped like a delete, so the trashed row is reached through
	// WithTrashed() — the same pair the corpus itself uses for the relation-level restore.
	res, err := cloneRelationQuery(t, query).WithTrashed().Restore(&taylor)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&users))
	require.Len(t, users, 2)

	// $users->find(1) / find(2): the port re-reads each row and checks deleted_at is null.
	first := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&first, 1))
	assert.False(t, first.DeletedAt.Valid)

	second := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&second, 2))
	assert.False(t, second.DeletedAt.Valid)
}

func TestSoftDeletesIntegration_OnlyTrashedOnlyReturnsTrashedRecords(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).OnlyTrashed().Get(&users))
	require.Len(t, users, 1)
	assert.Equal(t, uint(1), users[0].ID)
}

func TestSoftDeletesIntegration_OnlyWithoutTrashedOnlyReturnsTrashedRecords(t *testing.T) {
	// Laravel: withoutTrashed()->get() and withTrashed()->withoutTrashed()->get() both read the one
	// non-deleted user.
	t.Skip("our ORM diverges: there is no WithoutTrashed(), and WithTrashed() sets a flag that cannot be unset — the oracle shows Laravel compiling `and \"users\".\"deleted_at\" is null` for both withoutTrashed() and withTrashed()->withoutTrashed(), while the Go WithTrashed().Get() returns both rows (probe: len 2)")
}

func TestSoftDeletesIntegration_FirstOrNew(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	result := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).FirstOrNew(&result, map[string]any{"email": "taylorotwell@gmail.com"}))
	// assertNull($result->id): a new, unsaved model — the Go key is zero.
	assert.Zero(t, result.ID)
	assert.Equal(t, "taylorotwell@gmail.com", result.Email)

	result = softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().FirstOrNew(&result, map[string]any{"email": "taylorotwell@gmail.com"}))
	assert.Equal(t, uint(1), result.ID)
}

func TestSoftDeletesIntegration_FindOrNew(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	// findOrNew(1): a hit hydrates the row; a miss returns a blank model. The Go FindOr is the same
	// read with a no-op callback — Laravel's bare new instance is the untouched destination.
	result := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).FindOr(&result, 1, func() error { return nil }))
	assert.Zero(t, result.ID)

	result = softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).WithTrashed().FindOr(&result, 1, func() error { return nil }))
	assert.Equal(t, uint(1), result.ID)
}

func TestSoftDeletesIntegration_FirstOrCreate(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	result := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().FirstOrCreate(&result, map[string]any{"email": "taylorotwell@gmail.com"}))
	assert.Equal(t, "taylorotwell@gmail.com", result.Email)
	assert.Equal(t, uint(1), result.ID)

	all := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&all))
	assert.Len(t, all, 1)

	result = softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).FirstOrCreate(&result, map[string]any{"email": "foo@bar.com"}))
	assert.Equal(t, "foo@bar.com", result.Email)

	all = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&all))
	assert.Len(t, all, 2)

	all = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&all))
	assert.Len(t, all, 3)
}

func TestSoftDeletesIntegration_CreateOrFirst(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	result := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().CreateOrFirst(&result, map[string]any{"email": "taylorotwell@gmail.com"}))
	assert.Equal(t, "taylorotwell@gmail.com", result.Email)

	all := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&all))
	assert.Len(t, all, 1)

	result = softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).CreateOrFirst(&result, map[string]any{"email": "foo@bar.com"}))
	assert.Equal(t, "foo@bar.com", result.Email)

	all = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&all))
	assert.Len(t, all, 2)

	trashed := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&trashed))
	assert.Len(t, trashed, 3)
}

func TestSoftDeletesIntegration_UpdateModelAfterSoftDeleting(t *testing.T) {
	// Laravel pins Carbon::setTestNow($now), soft-deletes model 2, then asserts
	// $userModel->getOriginal('deleted_at') === $now and that the model equals a freshly loaded row.
	t.Skip("our ORM diverges: the delete path does not sync the in-memory model — there is no getOriginal() snapshot and DeletedAt is not stamped on the model, so the original-attribute assertion and the attribute-equality assertion against a freshly loaded row cannot be reproduced; probe: after Delete(&user) the model's DeletedAt.Valid stays false while WithTrashed().Find reads deleted_at set on the row")
}

func TestSoftDeletesIntegration_RestoreAfterSoftDelete(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	userModel := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&userModel, 2))

	_, err := cloneRelationQuery(t, query).Delete(&userModel)
	require.NoError(t, err)

	// $userModel->restore(): the Go Restore is scoped like a delete, so the trashed row is reached
	// through WithTrashed() — the pairing the corpus itself uses on relation builders.
	res, err := cloneRelationQuery(t, query).WithTrashed().Restore(&userModel)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	found := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&found, 2))
	assert.Equal(t, userModel.ID, found.ID)
}

func TestSoftDeletesIntegration_SoftDeleteAfterRestoring(t *testing.T) {
	// Laravel restores the trashed model 1, then compares the model's deleted_at and
	// getOriginal('deleted_at') against freshly read rows, deletes again, and compares the stamps.
	t.Skip("our ORM diverges: the delete and restore paths do not sync the in-memory model (no getOriginal(); a loaded DeletedAt keeps its value), so the comparisons between the model's deleted_at / getOriginal('deleted_at') and freshly read rows cannot be reproduced; probe: after WithTrashed().Restore(&user) the DB row's deleted_at is null while the in-memory DeletedAt.Valid is unchanged")
}

func TestSoftDeletesIntegration_ModifyingBeforeSoftDeletingAndRestoring(t *testing.T) {
	// Laravel sets email foo@bar.com, soft-deletes, restores, and asserts the modified email made
	// it to the row — Eloquent's restore()->save() persists dirty attributes.
	t.Skip("our ORM diverges: Delete and Restore write only the deleted_at (and updated_at) columns — a changed attribute is not persisted by restore the way Eloquent's save() persists dirty attributes; probe: set email 'foo@bar.com', WithTrashed().Restore after Delete, read back → 'abigailotwell@gmail.com'")
}

func TestSoftDeletesIntegration_UpdateOrCreate(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	// Not found: created carrying attributes+values.
	result := softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).UpdateOrCreate(&result, map[string]any{"email": "foo@bar.com"}, map[string]any{"email": "bar@baz.com"}))
	assert.Equal(t, "bar@baz.com", result.Email)

	all := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&all))
	assert.Len(t, all, 2)

	// Found on the trashed row through WithTrashed(): the flag carries into Save, so the value is
	// written to the trashed row and no new row appears.
	result = softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().UpdateOrCreate(&result, map[string]any{"email": "taylorotwell@gmail.com"}, map[string]any{"email": "foo@bar.com"}))
	assert.Equal(t, "foo@bar.com", result.Email)

	all = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Get(&all))
	assert.Len(t, all, 2)

	all = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WithTrashed().Get(&all))
	assert.Len(t, all, 3)
}

func TestSoftDeletesIntegration_HasOneRelationshipCanBeSoftDeleted(t *testing.T) {
	// Laravel: create the address, delete it on the builder (address()->delete()), and assert
	// $abigail->address is null; withTrashed()->restore() makes it visible again; deleting on the
	// model hides it; forceDelete() removes it for good.
	t.Skip(softDeletesSkipLoadGap + "probe: after `Related(&abigail, \"Address\").Delete()` (1 row) a re-read plus `Load(&abigail, \"Address\")` still hydrates the address, so the `$abigail->address` null assertions have no Go counterpart (the builder delete, withTrashed first, restore, model delete and force delete steps themselves behave as the corpus asserts)")
}

func TestSoftDeletesIntegration_BelongsToRelationshipCanBeSoftDeleted(t *testing.T) {
	// Laravel: associate the group, delete it on the builder, assert $abigail->group is null;
	// restore; delete on the model; force delete — the group relation must follow the soft-delete
	// scope at every step.
	t.Skip(softDeletesSkipLoadGap + "probe: after `Related(&abigail, \"Group\").Delete()` (1 row) `Load(&abigail, \"Group\")` still hydrates the group (name admin), so the `$abigail->group` null assertions have no Go counterpart (associate/save, withTrashed first, restore and force delete behave as the corpus asserts)")
}

func TestSoftDeletesIntegration_HasManyRelationshipCanBeSoftDeleted(t *testing.T) {
	// Laravel: create two posts, delete 'Second Title' on the builder, assert the fresh load has one
	// post; restore; force delete 'Second Title' and assert one post remains.
	t.Skip(softDeletesSkipLoadGap + "probe: after deleting 'Second Title', `Load(&abigail, \"Posts\")` returns len 2 — the trashed row included — where Laravel's `$abigail->posts` is len 1; the builder delete, withTrashed read (len 2), restore and force delete steps behave as asserted (Related().Count reads 1 / 2 / 1)")
}

func TestSoftDeletesIntegration_RelationToSqlAppliesSoftDelete(t *testing.T) {
	// Laravel pins $abigail->posts()->toSql() to
	//   select * from "posts" where "posts"."user_id" = ? and "posts"."user_id" is not null and
	//   "posts"."deleted_at" is null
	// (oracle-measured above; the is not null guard is HasOneOrMany::addConstraints).
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	_, abigail := softDeletesCreateUsers(t, query)

	posts := []softDeletesTestPost{}
	sql := cloneRelationQuery(t, query).Related(&abigail, "Posts").ToSql().Get(&posts)
	assert.Equal(t, `select * from "posts" where "posts"."user_id" = ? and "posts"."user_id" is not null and "posts"."deleted_at" is null`, sql)
}

func TestSoftDeletesIntegration_RelationExistsAndDoesntExistHonorsSoftDelete(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	_, abigail := softDeletesCreateUsers(t, query)

	post := softDeletesCreatePost(t, query, &abigail, "First Title")

	// doesntExist() is the negation of Exists(), the only predicate the Go query exposes, so each
	// corpus stage — exists() plus doesntExist() — is one read asserted for both halves.
	exists, err := cloneRelationQuery(t, query).Related(&abigail, "Posts").Exists()
	require.NoError(t, err)
	assert.True(t, exists)

	// $abigail->posts()->first()->delete()
	_, err = cloneRelationQuery(t, query).Delete(&post)
	require.NoError(t, err)

	exists, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Exists()
	require.NoError(t, err)
	assert.False(t, exists)

	// $abigail->posts()->withTrashed()->restore()
	res, err := cloneRelationQuery(t, query).Related(&abigail, "Posts").WithTrashed().Restore()
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	exists, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Exists()
	require.NoError(t, err)
	assert.True(t, exists)

	// $abigail->posts()->first()->forceDelete()
	first := softDeletesTestPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&abigail, "Posts").First(&first))
	res, err = cloneRelationQuery(t, query).ForceDelete(&first)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	exists, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Exists()
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestSoftDeletesIntegration_RelationCountHonorsSoftDelete(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	_, abigail := softDeletesCreateUsers(t, query)

	softDeletesCreatePost(t, query, &abigail, "First Title")
	softDeletesCreatePost(t, query, &abigail, "Second Title")

	count, err := cloneRelationQuery(t, query).Related(&abigail, "Posts").Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// $abigail->posts()->where('title', 'Second Title')->delete()
	res, err := cloneRelationQuery(t, query).Related(&abigail, "Posts").Where("title", "Second Title").Delete()
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	count, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// $abigail->posts()->withTrashed()->restore(). The builder restore stamps deleted_at = null on
	// every row the relation matches — trashed or not — so both posts are updated: oracle-measured
	// (illuminate/database v12.55.1: restore() on a user with one of two posts trashed returns 2).
	// The corpus asserts the count that follows, not the affected total.
	res, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").WithTrashed().Restore()
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.RowsAffected)

	count, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// $abigail->posts()->where('title', 'Second Title')->forceDelete()
	res, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Where("title", "Second Title").ForceDelete()
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)

	count, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestSoftDeletesIntegration_RelationAggregatesHonorsSoftDelete(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	_, abigail := softDeletesCreateUsers(t, query)

	for _, post := range []struct {
		title    string
		priority int
	}{{"First Title", 2}, {"Second Title", 4}, {"Third Title", 6}} {
		created := softDeletesTestPost{Title: post.title, Priority: post.priority}
		require.NoError(t, cloneRelationQuery(t, query).Relation(&abigail, "Posts").Create(&created))
	}

	read := func() (float64, float64, float64, float64) {
		var min, max, sum, avg float64
		require.NoError(t, cloneRelationQuery(t, query).Related(&abigail, "Posts").Min("priority", &min))
		require.NoError(t, cloneRelationQuery(t, query).Related(&abigail, "Posts").Max("priority", &max))
		require.NoError(t, cloneRelationQuery(t, query).Related(&abigail, "Posts").Sum("priority", &sum))
		require.NoError(t, cloneRelationQuery(t, query).Related(&abigail, "Posts").Avg("priority", &avg))

		return min, max, sum, avg
	}
	assertAggregates := func(min, max, sum, avg float64) {
		gotMin, gotMax, gotSum, gotAvg := read()
		assert.Equal(t, min, gotMin)
		assert.Equal(t, max, gotMax)
		assert.Equal(t, sum, gotSum)
		assert.Equal(t, avg, gotAvg)
	}

	assertAggregates(2, 6, 12, 4) // before soft delete

	// $abigail->posts()->where('title', 'First Title')->delete()
	res, err := cloneRelationQuery(t, query).Related(&abigail, "Posts").Where("title", "First Title").Delete()
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)
	assertAggregates(4, 6, 10, 5) // after soft delete

	// $abigail->posts()->withTrashed()->restore(). As in the count port, the restore updates every
	// matched row — all three posts — and oracle v12.55.1 returns 3 for this scenario; the corpus
	// asserts the aggregate reads that follow.
	res, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").WithTrashed().Restore()
	require.NoError(t, err)
	assert.Equal(t, int64(3), res.RowsAffected)
	assertAggregates(2, 6, 12, 4) // after restore

	// $abigail->posts()->where('title', 'Third Title')->forceDelete()
	res, err = cloneRelationQuery(t, query).Related(&abigail, "Posts").Where("title", "Third Title").ForceDelete()
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.RowsAffected)
	assertAggregates(2, 4, 6, 3) // after force delete
}

func TestSoftDeletesIntegration_SoftDeleteIsAppliedToNewQuery(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel pins (new SoftDeletesTestUser)->newQuery()->toSql() to
	//   select * from "users" where "users"."deleted_at" is null
	// (oracle-measured above).
	assert.Equal(t,
		`select * from "users" where "users"."deleted_at" is null`,
		cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).ToSql().Get(&softDeletesTestUser{}))
}

func TestSoftDeletesIntegration_SecondLevelRelationshipCanBeSoftDeleted(t *testing.T) {
	// Laravel: post->comments()->create(...), delete the comments on the post's builder, then assert
	// the post's loaded comments are empty and withTrashed()->get() has one.
	t.Skip(softDeletesSkipLoadGap + "probe: after `Related(&post, \"Comments\").Delete()` (1 row) `Load(&post, \"Comments\")` returns len 1 where Laravel's `->comments` is empty; the withTrashed read behaves as asserted (len 1)")
}

func TestSoftDeletesIntegration_WhereHasWithDeletedRelationship(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	abigail := softDeletesFindUser(t, query, "abigailotwell@gmail.com")
	post := softDeletesCreatePost(t, query, &abigail, "First Title")

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Where("email", "taylorotwell@gmail.com").Has("Posts").Get(&users))
	assert.Len(t, users, 0)

	users = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Where("email", "abigailotwell@gmail.com").Has("Posts").Get(&users))
	assert.Len(t, users, 1)

	users = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Where("email", "doesnt@exist.com").OrHas("Posts").Get(&users))
	assert.Len(t, users, 1)

	users = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "First Title") }).Get(&users))
	assert.Len(t, users, 1)

	users = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "Another Title") }).Get(&users))
	assert.Len(t, users, 0)

	users = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Where("email", "doesnt@exist.com").OrWhereHas("Posts", func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "First Title") }).Get(&users))
	assert.Len(t, users, 1)

	// With Post Deleted...
	_, err := cloneRelationQuery(t, query).Delete(&post)
	require.NoError(t, err)

	users = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Has("Posts").Get(&users))
	assert.Len(t, users, 0)
}

func TestSoftDeletesIntegration_WhereHasWithNestedDeletedRelationshipAndOnlyTrashedCondition(t *testing.T) {
	// Laravel: the post is soft-deleted, then whereHas('posts', onlyTrashed()) and
	// whereHas('posts', withTrashed()) each match abigail.
	t.Skip("our ORM diverges: an onlyTrashed()/withTrashed() callback inside WhereHas does not reach the existence subquery — applyRelationScope carries only where clauses and the subquery always adds the default `is null` predicate; probe: whereHas('posts', onlyTrashed) and whereHas('posts', withTrashed) each count 0 where Laravel counts 1 and 1 (oracle: the subquery predicate becomes `is not null` under onlyTrashed and is dropped under withTrashed)")
}

func TestSoftDeletesIntegration_WhereHasWithNestedDeletedRelationship(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	abigail := softDeletesFindUser(t, query, "abigailotwell@gmail.com")
	post := softDeletesCreatePost(t, query, &abigail, "First Title")
	comment := softDeletesTestComment{Body: "Comment Body"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").Create(&comment))
	_, err := cloneRelationQuery(t, query).Delete(&comment)
	require.NoError(t, err)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).Has("Posts.Comments").Get(&users))
	assert.Len(t, users, 0)

	users = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).DoesntHave("Posts.Comments").Get(&users))
	assert.Len(t, users, 1)
}

func TestSoftDeletesIntegration_WhereDoesntHaveWithNestedDeletedRelationship(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	users := []softDeletesTestUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestUser{}).DoesntHave("Posts.Comments").Get(&users))
	assert.Len(t, users, 1)
}

func TestSoftDeletesIntegration_WhereHasWithNestedDeletedRelationshipAndWithTrashedCondition(t *testing.T) {
	// Laravel: SoftDeletesTestUserWithTrashedPosts' posts() carries withTrashed(), so a soft-deleted
	// post still satisfies has('posts').
	t.Skip("our ORM diverges: a relation's default scope (OnQuery) is lost inside the existence subquery — the withTrashed() flag the callback sets is not carried and the subquery keeps the default `is null`; probe: Has(\"Posts\") counts 0 where Laravel counts 1")
}

func TestSoftDeletesIntegration_WithCountWithNestedDeletedRelationshipAndOnlyTrashedCondition(t *testing.T) {
	// Laravel: three posts (one trashed) and withCount variants — plain 2, onlyTrashed 1, withTrashed
	// 3, withTrashed+where 1, where 0 — each ordered by the postsCount alias.
	t.Skip("our ORM diverges: WithCount scoping callbacks lose onlyTrashed()/withTrashed() (only where clauses reach the count subquery, which always adds the default `is null`), so the counts are 2/2/0 where Laravel's are 1/3/1 (probe; the plain count 2 and the un-trashed where count 0 do match) — and OrderBy(\"postsCount\") renders `order by \"postsCount\"`, which this sqlite driver rejects with `no such column: \"postsCount\"` where Laravel's sqlite resolves the double-quoted name as a constant")
}

func TestSoftDeletesIntegration_OrWhereWithSoftDeleteConstraint(t *testing.T) {
	// Laravel: where('email', taylor) orWhere('email', abigail) then pluck('email') is [abigail] —
	// the soft-delete scope must not let the trashed row through the OR.
	t.Skip("our ORM diverges: OrWhere is not grouped against the appended soft-delete predicate — probe: the chain compiles `where \"email\" = ? or \"email\" = ? and \"users\".\"deleted_at\" is null` and Pluck('email') returns [taylorotwell@gmail.com abigailotwell@gmail.com]; the oracle groups Eloquent's own wheres first (`where (\"email\" = ? or \"email\" = ?) and \"users\".\"deleted_at\" is null`), so Laravel returns [abigailotwell@gmail.com]")
}

func TestSoftDeletesIntegration_MorphToWithTrashed(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	abigail := softDeletesFindUser(t, query, "abigailotwell@gmail.com")
	post := softDeletesCreatePost(t, query, &abigail, "First Title")

	comment := softDeletesTestComment{Body: "Comment Body", OwnerID: abigail.ID, OwnerType: "soft_deletes_test_user"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").Create(&comment))

	_, err := cloneRelationQuery(t, query).Delete(&abigail)
	require.NoError(t, err)

	// with(['owner' => fn($q) => $q->withoutGlobalScope(SoftDeletingScope::class)]): the Go
	// equivalent scope removal is WithoutGlobalScopes(). Reads use Get because a single-row First
	// leaves its cursor open while the eager query runs, which deadlocks on the harness's
	// one-connection pool.
	comments := []softDeletesTestComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestComment{}).
		With("Owner", func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes() }).Get(&comments))
	require.Len(t, comments, 1)
	require.NotNil(t, comments[0].Owner)
	assert.Equal(t, abigail.Email, comments[0].Owner.Email)

	// with(['owner' => fn($q) => $q->withTrashed()])
	comments = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestComment{}).
		With("Owner", func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() }).Get(&comments))
	require.Len(t, comments, 1)
	require.NotNil(t, comments[0].Owner)
	assert.Equal(t, abigail.Email, comments[0].Owner.Email)

	// The comment class without soft deletes, with the same withTrashed() constraint.
	plainComments := []testCommentWithoutSoftDelete{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&testCommentWithoutSoftDelete{}).
		With("Owner", func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() }).Get(&plainComments))
	require.Len(t, plainComments, 1)
	require.NotNil(t, plainComments[0].Owner)
	assert.Equal(t, abigail.Email, plainComments[0].Owner.Email)
}

func TestSoftDeletesIntegration_MorphToWithBadMethodCall(t *testing.T) {
	// Laravel expects a BadMethodCallException from $q->thisMethodDoesNotExist() inside the morphTo
	// eager-load constraint.
	t.Skip(softDeletesSkipPHPOnly + "the test asserts PHP's __call magic raising BadMethodCallException for an undefined method on the relation query — Go relation callbacks are statically typed functions, so an undefined method cannot be invoked")
}

func TestSoftDeletesIntegration_MorphToWithConstraints(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)
	softDeletesCreateUsers(t, query)

	abigail := softDeletesFindUser(t, query, "abigailotwell@gmail.com")
	post := softDeletesCreatePost(t, query, &abigail, "First Title")

	comment := softDeletesTestComment{Body: "Comment Body", OwnerID: abigail.ID, OwnerType: "soft_deletes_test_user"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").Create(&comment))

	comments := []softDeletesTestComment{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestComment{}).
		With("Owner", func(q contractsorm.Query) contractsorm.Query { return q.Where("email", "taylorotwell@gmail.com") }).Get(&comments))
	require.Len(t, comments, 1)
	assert.Nil(t, comments[0].Owner)
}

func TestSoftDeletesIntegration_MorphToWithoutConstraints(t *testing.T) {
	// Laravel: with('owner') loads abigail before she is deleted, and yields null after — the morphTo
	// eager load must apply the owner model's soft-delete scope.
	t.Skip("our ORM diverges: the MorphTo eager load ignores the owner model's soft-delete scope (the per-type bucket query is built table-only, so no predicate is added); probe: with abigail soft-deleted, With(\"Owner\") still hydrates her — owner email 'abigailotwell@gmail.com' where Laravel's owner is null (the pre-delete half is reproduced: the owner loads)")
}

func TestSoftDeletesIntegration_MorphToNonSoftDeletingModel(t *testing.T) {
	query := newRelationQuery(t)
	softDeletesTestSchema(t, query)

	taylor := testUserWithoutSoftDelete{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&taylor).Create(map[string]any{"id": uint(1), "email": "taylorotwell@gmail.com"}))

	post := softDeletesTestPost{Title: "First Title"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&taylor, "Posts").Create(&post))

	comment := softDeletesTestCommentForPlainUser{Body: "Comment Body", OwnerID: 1, OwnerType: "test_user_without_soft_delete"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Comments").Create(&comment))

	comments := []softDeletesTestCommentForPlainUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestCommentForPlainUser{}).With("Owner").Get(&comments))
	require.Len(t, comments, 1)
	require.NotNil(t, comments[0].Owner)
	assert.Equal(t, taylor.Email, comments[0].Owner.Email)

	// $taylor->delete(): the model carries no soft deletes, so this is a hard delete.
	_, err := cloneRelationQuery(t, query).Delete(&taylor)
	require.NoError(t, err)

	comments = nil
	require.NoError(t, cloneRelationQuery(t, query).Model(&softDeletesTestCommentForPlainUser{}).With("Owner").Get(&comments))
	require.Len(t, comments, 1)
	assert.Nil(t, comments[0].Owner)
}

func TestSoftDeletesIntegration_SelfReferencingRelationshipWithSoftDeletes(t *testing.T) {
	// Laravel: abigail's self_referencing (hasMany(self, user_id)->onlyTrashed()) holds taylor; then
	// whereHas('self_referencing') must count one.
	t.Skip("our ORM diverges: a relation's onlyTrashed() default scope is lost in the existence subquery — probe: WhereHas(\"SelfReferencing\") counts 0 where Laravel counts 1, although the Related read path (which carries the scope) returns taylor, and taylor's own list is empty")
}
