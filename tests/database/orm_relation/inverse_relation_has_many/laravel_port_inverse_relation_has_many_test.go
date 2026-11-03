package inverse_relation_has_many

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentInverseRelationHasManyTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestInverseRelationHasMany_<method without the test prefix>. The corpus's two tables mirror
// createSchema(); rows are seeded through the ORM, and relations are loaded explicitly through
// OrmBuilder.Load — the harness's lazy-loading rule, since Go has no property-access magic.
//
// # The inverse feature, and how it is ported
//
// The corpus pins Laravel's ->inverse('user'): every relation load and write links each related
// model back to the parent — after $user->posts loads, each post's `user` relation is
// relationLoaded and holds the very same $user instance without another query — and createMany /
// saveMany / makeMany stamp the same link on their results (HasOneOrMany applies it from
// matchOneOrMany, save, saveMany, create, forceCreate and make). The Go declaration is the
// relation descriptor's Inverse field, and every read path — Load / LoadMissing (lazy), With
// (eager, including the one-of-many forms) and the relation query itself (Related(...).Get /
// First, the Go form of $user->posts()->get()) — stores the parent instance on the child's
// relation, so the read-path ports assert the corpus pair directly: NotNil + assertSame, with no
// second query. The oracle measured the same (probe _inverse_writeback_probe.php: lazy and eager reads
// link all children to the same parent instance, and the child's access runs no query; a
// hasOne(...)->latestOfMany() relation links too). The write paths link too: the foreign-key stamp
// (database/gorm's setRelationFKOnChild, the Go form of HasOneOrMany::setForeignAttributesForCreate)
// ends with SupportsInverseRelations::applyInverseRelationToModel — the call HasOneOrMany::make
// (:65) and ::create (:393) make, and the trailing call setForeignAttributesForCreate makes (:506)
// — so save / saveMany / create / createMany / make / makeMany / forceCreate stamp the parent
// instance onto every child in memory, and the write-path ports assert the corpus pair directly
// too. Freshly probed (oracle, illuminate/database v12.55.1, executed,
// /tmp/lalign-defaults-inverse/inverse_writeback_runtime.php): create / make / save / forceCreate
// / createMany / makeMany / saveMany all report same-instance true; a
// withoutChaperone()->create()/make() reports relationLoaded false.
//
// The corpus checks behaviour, not compiled SQL: it has no ->toSql() / ->toRawSql() pins. The
// oracle (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1) was still used
// to measure the SQL behind that behaviour, and the relations below cite those measurements:
// $user->posts() and $user->firstPost() (posts()->one()) compile `select * from "test_posts" where
// "test_posts"."user_id" = ? and "test_posts"."user_id" is not null`; $user->lastPost() joins the
// one-of-many aggregate — `select "test_posts".* from "test_posts" inner join (select
// MAX("test_posts"."id") as "id_aggregate", "test_posts"."user_id" from "test_posts" where
// "test_posts"."user_id" = ? and "test_posts"."user_id" is not null group by
// "test_posts"."user_id") as "latestOfMany" on "latestOfMany"."id_aggregate" = "test_posts"."id"
// and "latestOfMany"."user_id" = "test_posts"."user_id" where "test_posts"."user_id" = ? and
// "test_posts"."user_id" is not null`; the child read $post->user() is `select * from "test_users"
// where "test_users"."id" = ?`. The inverse declaration itself is metadata only: the oracle
// confirms getInverseRelationship() is 'user' on all three parent relations while none of the SQL
// changes.
//
// makeMany, the corpus's bulk maker of unsaved children, runs RelationWriter.MakeMany — the same
// write path, so the made children carry the FK and the inverse link (probed: id 0, fk set,
// nothing inserted, relationLoaded true). createManyQuietly has no quiet variant; its port calls
// CreateMany, because the ORM's relation writes dispatch no model events at all and the corpus
// registers no observers, making the two observably identical.

// --- models ------------------------------------------------------------------

// inverseHasManyUser ports HasManyInverseUserModel. posts() and lastPost() carry Laravel's
// ->inverse('user'), which the Go ORM cannot express (see the header); firstPost() is
// posts()->one() — a HasOne over the same foreign key. The relation destinations are kept out of
// the table by gorm:"-".
type inverseHasManyUser struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts     []*inverseHasManyPost `gorm:"-"`
	LastPost  *inverseHasManyPost   `gorm:"-"`
	FirstPost *inverseHasManyPost   `gorm:"-"`
}

func (inverseHasManyUser) TableName() string { return "test_users" }

func (inverseHasManyUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// posts(): hasMany(HasManyInversePostModel::class, 'user_id')->inverse('user').
		"Posts": contractsorm.HasMany{Related: &inverseHasManyPost{}, ForeignKey: "user_id", Inverse: "User"},
		// lastPost(): hasOne(..., 'user_id')->latestOfMany()->inverse('user') — the row with the
		// highest id.
		"LastPost": contractsorm.HasOne{
			Related: &inverseHasManyPost{}, ForeignKey: "user_id", Inverse: "User",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.LatestOfMany() },
		},
		// firstPost(): posts()->one() — the same foreign key, one row; Laravel's one() copies the
		// has-many's inverse onto the new HasOne.
		"FirstPost": contractsorm.HasOne{Related: &inverseHasManyPost{}, ForeignKey: "user_id", Inverse: "User"},
	}
}

// inverseHasManyPost ports HasManyInversePostModel.
type inverseHasManyPost struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	User *inverseHasManyUser `gorm:"-"`
}

func (inverseHasManyPost) TableName() string { return "test_posts" }

func (inverseHasManyPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// user(): belongsTo(HasManyInverseUserModel::class, 'user_id').
		"User": contractsorm.BelongsTo{Related: &inverseHasManyUser{}, ForeignKey: "user_id"},
	}
}

// --- schema and seeding ------------------------------------------------------

// inverseHasManySchema mirrors the corpus's createSchema() in sqlite DDL: test_users carries an
// auto-incrementing id and timestamps; test_posts the same plus the not-null user_id that
// foreignId('user_id') creates (no FK constraint — the corpus never calls constrained()).
func inverseHasManySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table test_users (id integer primary key autoincrement, created_at datetime null, updated_at datetime null)`,
		`create table test_posts (id integer primary key autoincrement, user_id integer not null, created_at datetime null, updated_at datetime null)`,
	)
}

// inverseHasManySeedUser creates one user, writing the explicit id the auto increment would
// assign: the users table carries only the id and timestamps, and an all-defaults struct insert
// compiles as `insert into "test_users" () values ()`, which sqlite rejects.
func inverseHasManySeedUser(t *testing.T, query contractsorm.Query, id uint) inverseHasManyUser {
	t.Helper()

	user := inverseHasManyUser{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&user).Create(map[string]any{"id": id}))

	return user
}

// inverseHasManySeedUserWithPosts ports the corpus's `factory()->count(3)->withPosts()->create()`:
// a user and its posts, each post written through the plain ORM with the user's key already on it
// (the corpus's post factory recycles the parent into its user_id definition — a plain insert,
// not a relation write). The returned posts are in insert order, so the last element is the
// newest and the first is the one firstPost's one-row read finds.
func inverseHasManySeedUserWithPosts(t *testing.T, query contractsorm.Query, id uint, count int) (inverseHasManyUser, []*inverseHasManyPost) {
	t.Helper()

	user := inverseHasManySeedUser(t, query, id)

	posts := make([]*inverseHasManyPost, 0, count)
	for i := 0; i < count; i++ {
		post := inverseHasManyPost{UserID: user.ID}
		require.NoError(t, cloneRelationQuery(t, query).Create(&post))
		require.NotZero(t, post.ID)
		posts = append(posts, &post)
	}

	return user, posts
}

// inverseHasManyLoadUser reads the child's User relation explicitly — the read a consumer triggers
// through $post->user. Kept for the pre-save read of the updating test, where the child was
// written by a plain ORM create (not a relation write) and therefore carries no in-memory link.
func inverseHasManyLoadUser(t *testing.T, query contractsorm.Query, post *inverseHasManyPost) *inverseHasManyUser {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Load(post, "User"))
	require.NotNil(t, post.User)

	return post.User
}

// inverseHasManyAssertChaperoned asserts the corpus's inverse pair for a loaded child:
// assertTrue($post->relationLoaded('user')) and assertSame($user, $post->user) — the loader
// stores the parent instance itself, so no further query runs.
func inverseHasManyAssertChaperoned(t *testing.T, post *inverseHasManyPost, user *inverseHasManyUser) {
	t.Helper()

	assert.Equal(t, user.ID, post.UserID)
	require.NotNil(t, post.User, "the inverse declaration must link the child back to the parent")
	assert.Same(t, user, post.User)
}

// --- ports of the corpus -----------------------------------------------------

func TestInverseRelationHasMany_HasManyInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	for id := uint(1); id <= 3; id++ {
		inverseHasManySeedUserWithPosts(t, query, id, 3)
	}

	users := []inverseHasManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasManyUser{}).Get(&users))
	require.Len(t, users, 3)

	for i := range users {
		// Laravel: assertFalse($user->relationLoaded('posts')) — in Go the field stays
		// unpopulated until Load runs.
		assert.Nil(t, users[i].Posts)

		// Laravel lazy-loads through property access ($user->posts); the port loads explicitly.
		require.NoError(t, cloneRelationQuery(t, query).Load(&users[i], "Posts"))
		require.Len(t, users[i].Posts, 3)

		for _, post := range users[i].Posts {
			inverseHasManyAssertChaperoned(t, post, &users[i])
		}
	}
}

func TestInverseRelationHasMany_HasManyInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	for id := uint(1); id <= 3; id++ {
		inverseHasManySeedUserWithPosts(t, query, id, 3)
	}

	users := []inverseHasManyUser{}
	// Laravel: HasManyInverseUserModel::with('posts')->get().
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasManyUser{}).With("Posts").Get(&users))
	require.Len(t, users, 3)

	for i := range users {
		// Laravel: $user->getRelation('posts') — the eager load lands in the Posts field.
		require.Len(t, users[i].Posts, 3)

		for _, post := range users[i].Posts {
			inverseHasManyAssertChaperoned(t, post, &users[i])
		}
	}
}

func TestInverseRelationHasMany_HasLatestOfManyInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	newest := map[uint]uint{}
	for id := uint(1); id <= 3; id++ {
		_, posts := inverseHasManySeedUserWithPosts(t, query, id, 3)
		newest[id] = posts[len(posts)-1].ID
	}

	users := []inverseHasManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasManyUser{}).Get(&users))
	require.Len(t, users, 3)

	for i := range users {
		// Laravel: assertFalse($user->relationLoaded('lastPost')) before the property read.
		assert.Nil(t, users[i].LastPost)

		require.NoError(t, cloneRelationQuery(t, query).Load(&users[i], "LastPost"))
		require.NotNil(t, users[i].LastPost)

		// latestOfMany resolves to the user's highest-id post — the MAX(id) aggregate join the
		// oracle prints behind the relation.
		assert.Equal(t, newest[users[i].ID], users[i].LastPost.ID)

		inverseHasManyAssertChaperoned(t, users[i].LastPost, &users[i])
	}
}

func TestInverseRelationHasMany_HasLatestOfManyInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	newest := map[uint]uint{}
	for id := uint(1); id <= 3; id++ {
		_, posts := inverseHasManySeedUserWithPosts(t, query, id, 3)
		newest[id] = posts[len(posts)-1].ID
	}

	users := []inverseHasManyUser{}
	// Laravel: HasManyInverseUserModel::with('lastPost')->get().
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasManyUser{}).With("LastPost").Get(&users))
	require.Len(t, users, 3)

	for i := range users {
		// Laravel: $user->getRelation('lastPost').
		require.NotNil(t, users[i].LastPost)

		assert.Equal(t, newest[users[i].ID], users[i].LastPost.ID)

		inverseHasManyAssertChaperoned(t, users[i].LastPost, &users[i])
	}
}

func TestInverseRelationHasMany_OneOfManyInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	oldest := map[uint]uint{}
	for id := uint(1); id <= 3; id++ {
		_, posts := inverseHasManySeedUserWithPosts(t, query, id, 3)
		oldest[id] = posts[0].ID
	}

	users := []inverseHasManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasManyUser{}).Get(&users))
	require.Len(t, users, 3)

	for i := range users {
		// Laravel: assertFalse($user->relationLoaded('firstPost')) before the property read.
		assert.Nil(t, users[i].FirstPost)

		require.NoError(t, cloneRelationQuery(t, query).Load(&users[i], "FirstPost"))
		require.NotNil(t, users[i].FirstPost)

		// posts()->one() reads one row of the same foreign-key query — the first inserted.
		assert.Equal(t, oldest[users[i].ID], users[i].FirstPost.ID)

		inverseHasManyAssertChaperoned(t, users[i].FirstPost, &users[i])
	}
}

func TestInverseRelationHasMany_OneOfManyInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	oldest := map[uint]uint{}
	for id := uint(1); id <= 3; id++ {
		_, posts := inverseHasManySeedUserWithPosts(t, query, id, 3)
		oldest[id] = posts[0].ID
	}

	users := []inverseHasManyUser{}
	// Laravel: HasManyInverseUserModel::with('firstPost')->get().
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasManyUser{}).With("FirstPost").Get(&users))
	require.Len(t, users, 3)

	for i := range users {
		// Laravel: $user->getRelation('firstPost').
		require.NotNil(t, users[i].FirstPost)

		assert.Equal(t, oldest[users[i].ID], users[i].FirstPost.ID)

		inverseHasManyAssertChaperoned(t, users[i].FirstPost, &users[i])
	}
}

func TestInverseRelationHasMany_HasManyInverseRelationIsProperlySetToParentWhenMakingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user := inverseHasManySeedUser(t, query, 1)

	// Laravel: $user->posts()->makeMany(array_fill(0, 3, [])) — three new posts with the FK preset
	// and the inverse relation wired, never persisted (oracle: makeMany.all-same true).
	posts := make([]inverseHasManyPost, 0, 3)
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").MakeMany([]map[string]any{{}, {}, {}}, &posts))
	require.Len(t, posts, 3)

	for i := range posts {
		assert.Zero(t, posts[i].ID, "makeMany() builds the children without persisting them")
		inverseHasManyAssertChaperoned(t, &posts[i], &user)
	}
}

func TestInverseRelationHasMany_HasManyInverseRelationIsProperlySetToParentWhenCreatingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user := inverseHasManySeedUser(t, query, 1)

	posts := make([]inverseHasManyPost, 3)
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&posts))

	for i := range posts {
		require.NotZero(t, posts[i].ID)
		inverseHasManyAssertChaperoned(t, &posts[i], &user)
	}
}

func TestInverseRelationHasMany_HasManyInverseRelationIsProperlySetToParentWhenCreatingManyQuietly(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user := inverseHasManySeedUser(t, query, 1)

	// Laravel: createManyQuietly — createMany inside Model::withoutEvents. RelationWriter has no
	// quiet variant, but quietness only suppresses model events, and the ORM's relation writes
	// dispatch none (the corpus registers no observers), so CreateMany is the observably identical
	// call.
	posts := make([]inverseHasManyPost, 3)
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&posts))

	for i := range posts {
		require.NotZero(t, posts[i].ID)
		inverseHasManyAssertChaperoned(t, &posts[i], &user)
	}
}

func TestInverseRelationHasMany_HasManyInverseRelationIsProperlySetToParentWhenSavingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user := inverseHasManySeedUser(t, query, 1)

	// Laravel: $posts = array_fill(0, 3, new HasManyInversePostModel); $user->posts()->saveMany($posts).
	posts := make([]inverseHasManyPost, 3)
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").SaveMany(&posts))

	for i := range posts {
		require.NotZero(t, posts[i].ID)
		inverseHasManyAssertChaperoned(t, &posts[i], &user)
	}
}

func TestInverseRelationHasMany_HasManyInverseRelationIsProperlySetToParentWhenUpdatingMany(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user := inverseHasManySeedUser(t, query, 1)

	// Laravel: HasManyInversePostModel::factory()->count(3)->create() — the factory definition
	// gives each post its own brand-new user (user_id => User::factory()).
	posts := make([]*inverseHasManyPost, 0, 3)
	for id := uint(2); id <= 4; id++ {
		other := inverseHasManySeedUser(t, query, id)
		post := inverseHasManyPost{UserID: other.ID}
		require.NoError(t, cloneRelationQuery(t, query).Create(&post))
		posts = append(posts, &post)
	}

	for i := range posts {
		// Laravel: assertTrue($user->isNot($post->user)) — reading $post->user now resolves to
		// the factory's own user, not $user.
		assert.NotEqual(t, user.ID, inverseHasManyLoadUser(t, query, posts[i]).ID)
	}

	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").SaveMany(&posts))

	for i := range posts {
		// Laravel: assertSame($user, $post->user) — saveMany stamped the inverse link.
		inverseHasManyAssertChaperoned(t, posts[i], &user)
	}
}

// --- relation read path (Related) --------------------------------------------
//
// The corpus's read paths above go through the loaders; the same write-back runs on every
// execution of the relation query itself — the after-query callback chaperone() registers. The
// tests below drive Related(...) directly, the Go form of $user->posts() / ->first(), including
// the one-of-many read and the widened read the oracle measured (direct_read.php: get() and
// first() link, latestPost()->first() links, a constrained ->where(...)->get() links; widened.php:
// a widened posts()->orWhere('user_id', $b->id)->get() links all 5 rows of the result — B's rows
// included — to $a).

func TestInverseRelationHasMany_HasManyInverseRelationIsSetOnRelationQueryGet(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user, _ := inverseHasManySeedUserWithPosts(t, query, 1, 3)

	// Laravel: $user->posts()->get().
	posts := []*inverseHasManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "Posts").Get(&posts))
	require.Len(t, posts, 3)

	for _, post := range posts {
		inverseHasManyAssertChaperoned(t, post, &user)
	}
}

func TestInverseRelationHasMany_HasManyInverseRelationIsSetOnRelationQueryFirst(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user, _ := inverseHasManySeedUserWithPosts(t, query, 1, 3)

	// Laravel: $user->posts()->first() — Builder::first() is limit(1)->get().
	post := inverseHasManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "Posts").First(&post))

	inverseHasManyAssertChaperoned(t, &post, &user)
}

func TestInverseRelationHasMany_LatestOfManyInverseRelationIsSetOnRelationQueryFirst(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user, posts := inverseHasManySeedUserWithPosts(t, query, 1, 3)
	newest := posts[len(posts)-1].ID

	// Laravel: $user->latestPost()->first() — the one-of-many read links too (oracle:
	// latestPost()->first() linked=true).
	last := inverseHasManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "LastPost").First(&last))
	assert.Equal(t, newest, last.ID)

	inverseHasManyAssertChaperoned(t, &last, &user)
}

func TestInverseRelationHasMany_HasManyInverseRelationIsSetOnConstrainedRelationQueryGet(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	user, _ := inverseHasManySeedUserWithPosts(t, query, 1, 3)

	// Laravel: $user->posts()->where('id', '>', 0)->get() — a constrained get() links every
	// returned model too (oracle: linked=true).
	posts := []*inverseHasManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "Posts").Where("id", ">", 0).Get(&posts))
	require.Len(t, posts, 3)

	for _, post := range posts {
		inverseHasManyAssertChaperoned(t, post, &user)
	}
}

func TestInverseRelationHasMany_WidenedRelationQueryLinksEveryReturnedRow(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasManySchema(t, query)

	userA, _ := inverseHasManySeedUserWithPosts(t, query, 1, 3)
	userB, _ := inverseHasManySeedUserWithPosts(t, query, 2, 2)

	// Laravel: $a->posts()->orWhere('user_id', $b->id)->get() — the after-query callback sees the
	// actual result set, so every row is linked to $a, B's rows included (oracle widened.php:
	// count=5, all-linked-to-A=true, B-rows-also-linked-to-A=true).
	all := []*inverseHasManyPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&userA, "Posts").OrWhere("user_id", userB.ID).Get(&all))
	require.Len(t, all, 5)

	linkedToA, linkedToB := 0, 0
	for _, post := range all {
		require.NotNil(t, post.User, "every row of the widened result must carry the parent as its inverse relation")
		assert.Same(t, &userA, post.User)

		switch post.UserID {
		case userA.ID:
			linkedToA++
		case userB.ID:
			linkedToB++
		}
	}
	assert.Equal(t, 3, linkedToA, "the result holds the parent's own rows")
	assert.Equal(t, 2, linkedToB, "and the other parent's rows the widening added")
}
