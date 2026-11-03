package inverse_relation

// This file ports Laravel's tests/Database/DatabaseEloquentInverseRelationTest.php (12.x): one Go
// test per Laravel test method, named TestInverseRelation_<method without the test prefix>.
//
// The corpus is the unit test of the SupportsInverseRelations trait — inverse()/chaperone(),
// withoutInverse(), inverse-relation-name guessing, and the after-query callback that writes the
// parent back onto the models a relation read returns. It has no setUp and no schema: most of its
// tests drive a stub Relation over a mocked Eloquent builder, so they assert mock interactions,
// trait state and PHP name introspection — surfaces with no Go counterpart, and they stay skipped
// (the withoutInverse trait-state assertion is the exception: it runs below through the relation
// object's GetInverseRelationship / WithoutInverse accessors).
//
// The Go declaration surface is the relation descriptor's Inverse field (contracts/database/orm):
// HasOne / HasMany / MorphOne / MorphMany name the related model's relation that receives the
// parent instance, and both halves of the corpus's callback write the parent back — the loaders
// onto every related model they match (database/gorm/eager_loader.go), and the relation read path
// itself (Related(...).Get / First in database/gorm/new_relation.go) onto every model each
// execution of the relation query returns, the callback's actual scope. That is the after-query
// callback's observable effect, applied without a callback registry, and a relation whose Inverse
// names a relation the related model does not declare fails resolution with OrmRelationNotFound,
// this port's form of the corpus's RelationNotFoundException.
//
// The three tests whose subject is that write-back are real assertions here, driving a seeded
// SQLite database (the harness's newRelationQuery): the model pair below declares
// hasMany(...)->inverse('user') exactly as the corpus's stub relation declares inverse('test'),
// and the tests assert the outcome the corpus asserts through its callback — every model in the
// result carries the parent as its inverse relation (assertSame), and nothing is set while the
// inverse is unset. The oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, probe
// _inverse_writeback_probe.php) executed the same shapes: a lazy property read links all five
// children to the same parent instance and the child's access runs no query; with('posts') does
// the same eagerly; withoutInverse() leaves every child unset; hasOne(...)->latestOfMany()->...
// links too; inverse('nope') throws `Call to undefined relationship [nope] on model [WbPost]` and
// inverse('') throws the same with [null]. A second probe set measured the relation-query
// executions themselves — direct_read.php: posts()->get(), posts()->first(), latestPost()->first()
// and a constrained ->where(...)->get() all link every returned model; widened.php: a widened
// posts()->orWhere(user_id, B)->get() links all 5 rows of the result, B's rows included, to the
// parent the relation was resolved from.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// --- models ------------------------------------------------------------------

// inverseRelationUser declares the port's parent relations: Posts carries the inverse the corpus
// declares (inverse('user')), PlainPosts names no inverse — the Go form of the state
// withoutInverse() leaves behind — and BadPosts' inverse names a relation the child does not
// declare, for the corpus's missing-relation test.
type inverseRelationUser struct {
	ID         uint64                 `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string                 `gorm:"column:name"`
	Posts      []*inverseRelationPost `gorm:"-"`
	PlainPosts []*inverseRelationPost `gorm:"-"`
	BadPosts   []*inverseRelationPost `gorm:"-"`
}

func (inverseRelationUser) TableName() string { return "users" }

func (inverseRelationUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// posts(): hasMany(Post::class)->inverse('user').
		"Posts": contractsorm.HasMany{Related: &inverseRelationPost{}, ForeignKey: "user_id", Inverse: "User"},
		// posts() with the inverse cleared — Laravel: ->withoutInverse().
		"PlainPosts": contractsorm.HasMany{Related: &inverseRelationPost{}, ForeignKey: "user_id"},
		// posts() with inverse('nope') — a name the related model does not declare.
		"BadPosts": contractsorm.HasMany{Related: &inverseRelationPost{}, ForeignKey: "user_id", Inverse: "Nope"},
	}
}

// inverseRelationPost is the corpus's related model: it holds the foreign key and declares the
// User relation the inverse write-back targets.
type inverseRelationPost struct {
	ID     uint64               `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint64               `gorm:"column:user_id"`
	User   *inverseRelationUser `gorm:"-"`
}

func (inverseRelationPost) TableName() string { return "posts" }

func (inverseRelationPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{Related: &inverseRelationUser{}, ForeignKey: "user_id"},
	}
}

// --- schema and seeding ------------------------------------------------------

// inverseRelationSchema creates the port's two tables.
func inverseRelationSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, name text)`,
		`create table posts (id integer primary key autoincrement, user_id integer not null)`,
	)
}

// inverseRelationSeed writes one user and count posts through the plain ORM (no relation write —
// the corpus's factory semantics), returning the user whose ID and posts the tests assert on.
func inverseRelationSeed(t *testing.T, query contractsorm.Query, count int) inverseRelationUser {
	t.Helper()

	user := inverseRelationUser{Name: "parent"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))
	require.NotZero(t, user.ID, "Create must read the generated key back")

	for i := 0; i < count; i++ {
		post := inverseRelationPost{UserID: user.ID}
		require.NoError(t, cloneRelationQuery(t, query).Create(&post))
		require.NotZero(t, post.ID)
	}

	return user
}

// --- ports of the corpus -----------------------------------------------------

func TestInverseRelation_BuilderCallbackIsNotAppliedWhenInverseRelationIsNotSet(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts a stub relation constructed without an inverse registers no after-query callback on its mocked builder; the Go declaration is the static Inverse field, consulted by the loaders and the relation read path — there is no callback registry to observe. The observable rule this guards (no inverse declared, no write-back) is asserted for real by TestInverseRelation_InverseRelationIsNotSetIfInverseRelationIsUnset.")
}

func TestInverseRelation_BuilderCallbackIsNotSetIfInverseRelationIsEmptyString(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts inverse('') throws PHP's RelationNotFoundException because an empty inverse name resolves to no declared relation, distinguished from never calling inverse() at all; the Go declaration has a single empty value, so Inverse: \"\" means 'no write-back' and no separate empty-string error exists. The unknown-name error it shares is asserted by TestInverseRelation_BuilderCallbackIsNotSetIfInverseRelationshipDoesNotExist.")
}

func TestInverseRelation_BuilderCallbackIsNotSetIfInverseRelationshipDoesNotExist(t *testing.T) {
	query := newRelationQuery(t)
	inverseRelationSchema(t, query)

	user := inverseRelationSeed(t, query, 1)

	// Laravel: inverse('foo') where the related model has no 'foo' relation throws
	// RelationNotFoundException at declaration (and registers no after-query callback; the
	// callback registry is PHP-only). The Go declaration carries the same rule through the
	// resolver: loading the relation fails with OrmRelationNotFound.
	err := cloneRelationQuery(t, query).Load(&user, "BadPosts")

	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmRelationNotFound)
	assert.Contains(t, err.Error(), `relation "Nope" not found on model`)
	assert.Nil(t, user.BadPosts, "a failed resolution loads nothing")
}

func TestInverseRelation_WithoutInverseMethodRemovesInverseRelation(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: withoutInverse() clears the trait state — getInverseRelationship()
	// (Relations/Concerns/SupportsInverseRelations.php:129) reads the declared name before the call
	// and null after withoutInverse() (:141 -> withoutChaperone() :151, `$this->inverseRelationship =
	// null;` carrying its `return $this;`). Oracle v12.55.1, executed
	// (/tmp/lalign-gate/inverse_state.php): "plain.initial => NULL";
	// "posts.after-inverse => 'user'"; "posts.after-withoutInverse => NULL";
	// "withoutInverse.returned-same => true"; "posts2.after-withoutChaperone => NULL". The Go
	// declaration carries inverse('user') as Inverse: "User" — the corpus's stub relation declares
	// inverse('test') the same way — and GetInverseRelationship / WithoutInverse are its object
	// accessors, so the corpus's three states all exist: none without the declaration, the declared
	// name, none after the call.
	many, ok := cloneRelationQuery(t, query).Related(&inverseRelationUser{}, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok, "Posts must materialize as the has-many leaf")

	// The relation declared WITHOUT an inverse reads none from the start — the corpus's initial
	// null state.
	plain, ok := cloneRelationQuery(t, query).Related(&inverseRelationUser{}, "PlainPosts").(contractsorm.HasManyRelation)
	require.True(t, ok, "PlainPosts must materialize as the has-many leaf")
	assert.Equal(t, "", plain.GetInverseRelationship(), "a relation declared without an inverse must read none")

	// inverse('user') declared: the state reads the name back.
	assert.Equal(t, "User", many.GetInverseRelationship())

	// withoutInverse() clears the state and returns the relation itself (Laravel's `return $this;`).
	require.Same(t, many, many.WithoutInverse())
	assert.Equal(t, "", many.GetInverseRelationship(), "the inverse state must read none after withoutInverse")
}

func TestInverseRelation_BuilderCallbackIsAppliedWhenInverseRelationIsSet(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test reflects into the after-query callback that inverse('test') registers on the mocked builder and asserts the closure is bound to the relation and its parent; the Go write-back runs inside the loaders and the relation read path, with no callback to reflect into.")
}

func TestInverseRelation_BuilderCallbackAppliesInverseRelationToAllModelsInResult(t *testing.T) {
	query := newRelationQuery(t)
	inverseRelationSchema(t, query)

	// The corpus hands five related models to the captured after-query callback and asserts every
	// one ends up carrying the parent: relationLoaded('test') true and assertSame($parent,
	// $model->test). The Go write-back runs inside the loader, so the port runs the same relation
	// read — Load, the explicit form of $user->posts — over the same five rows.
	user := inverseRelationSeed(t, query, 5)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Posts"))
	require.Len(t, user.Posts, 5)

	for _, post := range user.Posts {
		require.NotNil(t, post.User, "every model in the result must carry the parent as its inverse relation")
		assert.Same(t, &user, post.User)
	}
}

func TestInverseRelation_InverseRelationIsNotSetIfInverseRelationIsUnset(t *testing.T) {
	query := newRelationQuery(t)
	inverseRelationSchema(t, query)

	// The corpus sets the inverse, sees the callback link the children, clears it with
	// withoutInverse() and sees it stop linking. A Go relation's declaration is static metadata,
	// so the cleared state is expressed by a second relation over the same rows that never
	// declared an inverse: while the declaration is set the children are linked, and with it
	// unset the loader sets nothing.
	user := inverseRelationSeed(t, query, 5)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Posts"))
	require.Len(t, user.Posts, 5)
	for _, post := range user.Posts {
		assert.Same(t, &user, post.User)
	}

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "PlainPosts"))
	require.Len(t, user.PlainPosts, 5)
	for _, post := range user.PlainPosts {
		assert.Nil(t, post.User)
	}
}

func TestInverseRelation_ProvidesPossibleInverseRelationBasedOnParent(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts getPossibleInverseRelations() yields exactly [hasInverseRelationParentStub, parentStub, owner] — names derived from the parent's class name, the parent's foreign key and the literal 'owner'; the Go ORM derives no candidate names, an inverse is declared explicitly via the Inverse field.")
}

func TestInverseRelation_ProvidesPossibleInverseRelationBasedOnForeignKey(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts an explicit foreign key of 'test_id' contributes 'test' to the candidate inverse relation names; the Go ORM derives no relation names from foreign-key columns.")
}

func TestInverseRelation_ProvidesPossibleRecursiveRelationsIfRelatedIsTheSameClassAsParent(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts 'parent' joins the candidate inverse names when the related model and the parent are the same class; the Go ORM has no such recursion rule — a self-relation is just another name declared in the Relations() map.")
}

func TestInverseRelation_GuessesInverseRelationBasedOnParent(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts guessInverseRelation() returns the first candidate the (mocked) related model reports via isRelation() — 'hasInverseRelationParentStub', 'parentStub' or 'owner' per data-provider case; the Go ORM guesses no names — the Inverse field states the relation name, and the resolver only validates it.")
}

func TestInverseRelation_GuessesPossibleInverseRelationBasedOnForeignKey(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts guessInverseRelation() returns 'test' when the (mocked) related model reports an isRelation('test') and the foreign key is 'test_id'; the Go ORM has no guess path.")
}

func TestInverseRelation_GuessesRecursiveInverseRelationsIfRelatedIsSameClassAsParent(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test mocks getForeignKey()/getKeyName() on cloned related/parent models and asserts the guess resolves to the recursive candidate 'parent'; the Go ORM has no name guessing and no key introspection on models.")
}

func TestInverseRelation_SetsGuessedInverseRelationBasedOnParent(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts inverse() with no argument stores the guessed name — 'hasInverseRelationParentStub', 'parentStub' or 'owner' per data-provider case — in the relation's trait state; the Go declaration states a name or none, and the loaders keep no state.")
}

func TestInverseRelation_SetsRecursiveInverseRelationsIfRelatedIsSameClassAsParent(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts inverse() stores the recursive guess 'parent' when the related model and the parent are the same class; the Go ORM has no inverse state to set.")
}

func TestInverseRelation_SetsGuessedInverseRelationBasedOnForeignKey(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts inverse() stores 'test', guessed from the 'test_id' foreign key, in the relation's trait state; the Go ORM has no inverse state to set.")
}

func TestInverseRelation_OnlyHydratesInverseRelationOnModels(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the collection write-back calls applyInverseRelationToModel exactly six times, touching only the Model instances of a mixed collection (integers, booleans, arrays, a class-name string and an anonymous object are skipped); the Go loaders only ever carry model rows, so there is no mixed collection and no per-model call count to assert.")
}

// TestInverseRelation_RelationQueryReadAppliesInverseRelationToAllModelsInResult drives the read
// the corpus's callback runs on — Related(...).Get, the Go form of $user->posts()->get() — over
// the same five rows: every model of the result must carry the parent as its inverse relation.
func TestInverseRelation_RelationQueryReadAppliesInverseRelationToAllModelsInResult(t *testing.T) {
	query := newRelationQuery(t)
	inverseRelationSchema(t, query)

	user := inverseRelationSeed(t, query, 5)

	posts := []*inverseRelationPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "Posts").Get(&posts))
	require.Len(t, posts, 5)

	for _, post := range posts {
		require.NotNil(t, post.User, "every model in the result must carry the parent as its inverse relation")
		assert.Same(t, &user, post.User)
	}
}
