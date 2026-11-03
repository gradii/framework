package factory

// This file ports Laravel's tests/Database/DatabaseEloquentFactoryTest.php (12.x) onto the
// framework's factory (database/factory) and ORM: one Go test per Laravel test method, named
// TestFactory_<method without the test prefix>. The corpus's setUp schema (users, posts, comments,
// roles, role_user) is created with runRelationSchema; rows are seeded through the ORM, and the
// portable tests call the Go factory's own API over the harness's query.
//
// # Which Laravel calls map to which Go calls
//
// The Go factory keeps only the core of Laravel's surface: a model satisfies factory.Model by
// returning, from Factory(), a factory whose Definition() is the default attribute map keyed by Go
// field name; the factory itself (database/factory.FactoryImpl) offers Count(n) / Create /
// CreateQuietly / Make over a query handle. The portable calls translate as:
//
//   - new()->create() / new()->createOne() / times(n)->create()  ->  Create, with Count(n) for the counted form
//   - new()->create(['name' => ...]) / set('name', ...)->create()  ->  Create with an attribute map
//   - createMany(list)  ->  one Create per list record (the Go factory has no createMany)
//   - make() / makeOne()  ->  Make;  all()  ->  Find
//
// Everything else this corpus exercises — raw(), lazy(), sequences, has()/for()/hasAttached(),
// recycle(), afterMaking()/afterCreating() callbacks, withoutParents(), trashed(), insert(), and the
// factory/model name-resolution machinery — has no Go counterpart, so those tests are skipped with
// the concrete reason instead of being weakened, and a skip-heavy outcome is expected for this
// corpus. The missing factory surface is recorded in the port's divergences.
//
// # SQL
//
// The corpus asserts behaviour, not compiled SQL: no test pins a SQL string. The oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1) was nevertheless used to
// measure the statement a factory-created users row compiles to —
//
//	insert into "users" ("name", "options", "created_at", "updated_at") values (?, ?, ?, ?)
//
// — which is the column set the ported Create path writes: the definition's name/options plus the
// model's automatic timestamps. Laravel inserts one row per model (timed creations and createMany
// loop save()), and Count(n).Create(&slice) likewise executes one insert per row.

import (
	"testing"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsfactory "github.com/gradii/framework/contracts/database/factory"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkfactory "github.com/gradii/framework/database/factory"
	"github.com/gradii/framework/support/carbon"
)

// --- models ------------------------------------------------------------------

// factoryUser ports FactoryTestUser's table shape: an auto-incrementing id, a name, nullable
// options and timestamps. The corpus's model also hides options, appends upper_case_name and
// eager-loads posts; no ported test observes any of that, and the Go ORM models none of it.
type factoryUser struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Name      string           `gorm:"column:name" db:"name"`
	Options   *string          `gorm:"column:options" db:"options"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at" db:"created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at" db:"updated_at"`
}

func (factoryUser) TableName() string { return "users" }

func (factoryUser) Factory() contractsfactory.Factory { return factoryUserFactory{} }

// factoryUserFactory ports FactoryTestUserFactory::definition(): a faker name and a null options.
type factoryUserFactory struct{}

func (factoryUserFactory) Definition() map[string]any {
	faker := gofakeit.New(0)

	return map[string]any{
		"Name":    faker.Name(),
		"Options": nil,
	}
}

// factorySchema mirrors the corpus setUp's createSchema() — the five tables every test in the
// Laravel suite migrates first, in sqlite DDL.
func factorySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, name varchar not null, options varchar null, created_at datetime null, updated_at datetime null)`,
		`create table posts (id integer primary key autoincrement, user_id integer not null, title varchar not null, deleted_at datetime null, created_at datetime null, updated_at datetime null)`,
		`create table comments (id integer primary key autoincrement, commentable_id integer not null, commentable_type varchar not null, user_id integer not null, body varchar not null, deleted_at datetime null, created_at datetime null, updated_at datetime null)`,
		`create table roles (id integer primary key autoincrement, name varchar not null, created_at datetime null, updated_at datetime null)`,
		`create table role_user (role_id integer not null, user_id integer not null, admin varchar not null default 'N')`,
	)
}

// --- ported tests -------------------------------------------------------------

// Ports test_basic_model_can_be_created: every creation form persists a model through the Go
// factory (create/createOne/set/createMany/times -> Create, with Count for the counted forms).
func TestFactory_BasicModelCanBeCreated(t *testing.T) {
	query := newRelationQuery(t)
	factorySchema(t, query)

	fa := frameworkfactory.NewFactoryImpl(cloneRelationQuery(t, query))

	// $user = FactoryTestUserFactory::new()->create();
	var user factoryUser
	require.NoError(t, fa.Create(&user))
	require.NotZero(t, user.ID, "create must persist the model and read the generated key back")

	// $user = FactoryTestUserFactory::new()->createOne();
	var userOne factoryUser
	require.NoError(t, fa.Create(&userOne))
	require.NotZero(t, userOne.ID)

	// $user = FactoryTestUserFactory::new()->create(['name' => 'Taylor Otwell']);
	var taylor factoryUser
	require.NoError(t, fa.Create(&taylor, map[string]any{"Name": "Taylor Otwell"}))
	assert.Equal(t, "Taylor Otwell", taylor.Name)

	// $user = FactoryTestUserFactory::new()->set('name', 'Taylor Otwell')->create();
	var setTaylor factoryUser
	require.NoError(t, fa.Create(&setTaylor, map[string]any{"Name": "Taylor Otwell"}))
	assert.Equal(t, "Taylor Otwell", setTaylor.Name)

	// $users = FactoryTestUserFactory::new()->createMany([['name' => 'Taylor Otwell'],
	// ['name' => 'Jeffrey Way']]);  The Go factory has no createMany: one Create per record is the
	// same persistence step Laravel loops, and the corpus asserts only the collection and its count.
	var many []factoryUser
	for _, name := range []string{"Taylor Otwell", "Jeffrey Way"} {
		var created factoryUser
		require.NoError(t, fa.Create(&created, map[string]any{"Name": name}))
		many = append(many, created)
	}
	require.Len(t, many, 2)
	require.NotZero(t, many[0].ID)

	// $users = FactoryTestUserFactory::new()->createMany(2);
	var counted []factoryUser
	require.NoError(t, fa.Count(2).Create(&counted))
	require.Len(t, counted, 2)
	require.NotZero(t, counted[0].ID)

	// $users = FactoryTestUserFactory::times(2)->createMany();  (asserted twice in the corpus)
	var twice []factoryUser
	require.NoError(t, fa.Count(2).Create(&twice))
	require.Len(t, twice, 2)
	require.NotZero(t, twice[0].ID)

	var twiceAgain []factoryUser
	require.NoError(t, fa.Count(2).Create(&twiceAgain))
	require.Len(t, twiceAgain, 2)
	require.NotZero(t, twiceAgain[0].ID)

	// $users = FactoryTestUserFactory::times(3)->createMany([two records]);  The list caps the
	// count, so two models are created; the port creates one model per record, the same rows.
	var capped []factoryUser
	for _, name := range []string{"Taylor Otwell", "Jeffrey Way"} {
		var created factoryUser
		require.NoError(t, fa.Create(&created, map[string]any{"Name": name}))
		capped = append(capped, created)
	}
	require.Len(t, capped, 2)
	require.NotZero(t, capped[0].ID)

	// $users = FactoryTestUserFactory::new()->createMany();
	var single []factoryUser
	require.NoError(t, fa.Create(&single))
	require.Len(t, single, 1)
	require.NotZero(t, single[0].ID)

	// $users = FactoryTestUserFactory::times(10)->create();
	var ten []factoryUser
	require.NoError(t, fa.Count(10).Create(&ten))
	require.Len(t, ten, 10)
}

// Ports test_make_creates_unpersisted_model_instance: Make fills a model from the definition
// without persisting it, so the table stays empty.
func TestFactory_MakeCreatesUnpersistedModelInstance(t *testing.T) {
	query := newRelationQuery(t)
	factorySchema(t, query)

	fa := frameworkfactory.NewFactoryImpl(cloneRelationQuery(t, query))

	// $user = FactoryTestUserFactory::new()->makeOne();
	var user factoryUser
	require.NoError(t, fa.Make(&user))
	assert.Zero(t, user.ID)
	assert.NotEmpty(t, user.Name)

	// $user = FactoryTestUserFactory::new()->make(['name' => 'Taylor Otwell']);
	var taylor factoryUser
	require.NoError(t, fa.Make(&taylor, map[string]any{"Name": "Taylor Otwell"}))
	assert.Equal(t, "Taylor Otwell", taylor.Name)

	// $this->assertCount(0, FactoryTestUser::all());
	var users []factoryUser
	require.NoError(t, cloneRelationQuery(t, query).Find(&users))
	assert.Len(t, users, 0)
}

// --- skipped tests ------------------------------------------------------------
//
// Each test below has no executable Go equivalent; the reason names what the Laravel test asserts
// and what is missing.

func TestFactory_ExpandedClosureAttributesAreResolvedAndPassedToClosures(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts PHP closures used as factory attributes are resolved, with a later closure receiving the resolved sibling attributes; the Go factory maps plain values only, so a closure value has nothing to resolve")
}

func TestFactory_ExpandedClosureAttributeReturningAFactoryIsResolved(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts a closure attribute returning a factory is resolved into the related model (post->user); the Go factory neither invokes closure attributes nor resolves factory values into relations")
}

func TestFactory_BasicModelAttributesCanBeCreated(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no raw(); attributes are observable only after Make maps them onto a model, not as an override-merged map")
}

func TestFactory_ExpandedModelAttributesCanBeCreated(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no raw(), and no relationship expansion — the corpus's post raw() resolves the user_id factory value into an integer key")
}

func TestFactory_LazyModelAttributesCanBeCreated(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no lazy(); there is no deferred, callable form that produces a model when invoked")
}

func TestFactory_MultipleModelAttributesCanBeCreated(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no raw(); the corpus reads ten attribute arrays from times(10), and Count only multiplies models")
}

func TestFactory_AfterCreatingAndMakingCallbacksAreCalled(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no afterMaking()/afterCreating() callbacks, so the hooks that record the created model cannot fire")
}

func TestFactory_HasManyRelationship(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has() relationship expansion, so a factory cannot create child posts for its parents (nor pass the parent to child state mutations or callbacks)")
}

func TestFactory_BelongsToRelationship(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no for() relationship expansion; a belongs-to parent is never created from a factory value")
}

func TestFactory_BelongsToRelationshipWithExistingModelInstance(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no for(); an existing model cannot be bound to the factory as the parent of the created rows")
}

func TestFactory_BelongsToRelationshipWithExistingModelInstanceWithRelationshipNameImpliedFromModel(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no for() and no relation-name inference — relations are named by their key in the model's Relations() map, so 'the name implied from the model class' has no counterpart")
}

func TestFactory_MorphToRelationship(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no for() morph-to expansion, so a comment cannot create its commentable post and set the morph type")
}

func TestFactory_MorphToRelationshipWithExistingModelInstance(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no for() morph-to expansion, so an existing commentable model cannot be bound to the created comments")
}

func TestFactory_BelongsToManyRelationship(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no hasAttached() belonging-to-many expansion (nor the afterCreating callback it passes), so pivot rows with attributes cannot be created through a factory")
}

func TestFactory_BelongsToManyRelationshipRelatedModelsSetOnInstanceWhenTouchingOwner(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no hasAttached(), so the factory-created role cannot attach the user through the pivot the corpus reads back through $role->users; the corpus's assertions need no touches, only the missing hasAttached()")
}

func TestFactory_RelationCanBeLoadedBeforeModelIsCreated(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no for() and no afterMaking() hook, so loading a relation before the model is created cannot run inside a factory")
}

func TestFactory_BelongsToManyRelationshipWithExistingModelInstances(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no hasAttached(); existing role models cannot be attached to factory-created users")
}

func TestFactory_BelongsToManyRelationshipWithExistingModelInstancesUsingArray(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no hasAttached(); the array form (records exported with toArray()) is the same missing API")
}

func TestFactory_BelongsToManyRelationshipWithExistingModelInstancesWithRelationshipNameImpliedFromModel(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no hasAttached() and no relation-name inference from the model class")
}

func TestFactory_Sequences(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no sequence()/Sequence state cycling — the corpus alternates definition overrides by index, including pivot attributes for hasAttached()")
}

func TestFactory_CountedSequence(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no forEachSequence(); its count is explicit (Count(n)), so the assertion that a three-entry sequence implies count 3 cannot run")
}

func TestFactory_SequenceWithHasManyRelationship(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no sequence() and no has(), so the interleaved user/post sequence and the per-parent index the closure reads cannot be produced by a factory")
}

func TestFactory_CrossJoinSequences(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no CrossJoinSequence/crossJoinSequence() cartesian state")
}

func TestFactory_ResolveNestedModelFactories(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): Factory::resolveFactoryName derives factory class names from model class names; a Go model names its factory explicitly through Factory(), so class-name resolution cannot run")
}

func TestFactory_ResolveNestedModelNameFromFactory(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): Factory::modelName reads the model class a factory is bound to; the Go factory carries no model name")
}

func TestFactory_ResolveNonAppNestedModelFactories(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): Factory::resolveFactoryName resolves model class names against a configurable namespace; Go models name their factories explicitly through Factory()")
}

func TestFactory_ModelHasFactory(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): factory-name guessing (guessFactoryNamesUsing + HasFactory) resolves a factory class from the model class at runtime; in Go the model returns its factory from Factory()")
}

func TestFactory_DynamicHasAndForMethods(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): hasPosts(3)/forAuthor() are __call dynamic methods resolved against the model's relations through factory expansion; Go has no dynamic method dispatch")
}

func TestFactory_CanBeMacroable(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): Macroable adds methods to a factory instance at runtime; Go types have no runtime method table")
}

func TestFactory_FactoryCanConditionallyExecuteCode(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no when()/unless() conditional callbacks")
}

func TestFactory_DynamicTrashedStateForSoftdeletesModels(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no trashed() dynamic state (and no Carbon test clock); the corpus's deleted_at defaults to now minus a day, which a port would have to write explicitly")
}

func TestFactory_DynamicTrashedStateRespectsExistingState(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no trashed(); the corpus's comment factory overrides the deleted_at state to one week ago, which has no factory counterpart")
}

func TestFactory_DynamicTrashedStateThrowsExceptionWhenNotASoftdeletesModel(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no trashed() (nor dynamic state names), so the check that makes a non-soft-deleting model throw BadMethodCallException cannot run")
}

func TestFactory_ModelInstancesCanBeUsedInPlaceOfNestedFactories(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no recycle() model pool; parents are never created implicitly from nested factories, so there are none to recycle")
}

func TestFactory_ForMethodRecyclesModels(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no recycle() and no for(), so an existing user cannot be reused instead of creating a second one")
}

func TestFactory_HasMethodDoesNotReassignTheParent(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no recycle()/has(); the corpus relies on the recycled post not being reassigned as a parent")
}

func TestFactory_MultipleModelsCanBeProvidedToRecycle(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no recycle() (nor the implicit parent creation it feeds), so a pool of models cannot be provided")
}

func TestFactory_RecycledModelsCanBeCombinedWithMultipleCalls(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no recycle(), so recycled models cannot be combined across several calls")
}

func TestFactory_NoModelsCanBeProvidedToRecycle(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no recycle() (not even the empty-list form), and it creates no parents implicitly where Laravel falls back to them")
}

func TestFactory_CanDisableRelationships(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory does not expand parent factories by default, so there is no withoutParents() to disable them; a post's user_id is whatever the definition or attributes set")
}

func TestFactory_CanDisableRelationshipsExplicitlyByModelName(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no withoutParents() (nor class-name parent filtering), so no parent expansion exists to disable by model name")
}

func TestFactory_CanDisableRelationshipsExplicitlyByAttributeName(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no withoutParents() (nor attribute-name filtering); foreign keys are set explicitly through the attribute map")
}

func TestFactory_CanDisableRelationshipsExplicitlyByBothAttributeNameAndModelName(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no withoutParents() and no parent expansion, so the mixed attribute-name/model-name filter has no counterpart")
}

func TestFactory_CanDefaultToWithoutParents(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has neither expandRelationshipsByDefault() nor dontExpandRelationshipsByDefault(); it never expands parent factories")
}

func TestFactory_FactoryModelNamesCorrect(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): #[UseFactory] attributes and a static $factory property resolve a model's factory by reflection, and factory()->modelName() reads a class name; Go models declare Factory() explicitly and the factory carries no model name")
}

func TestFactory_FactoryGlobalModelResolver(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): guessModelNamesUsing derives model class names from factory class names; the Go factory has no name resolver")
}

func TestFactory_FactoryModelHasManyRelationshipHasPendingAttributes(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(), so the pending withAttributes a has-many relation definition carries cannot be applied to factory-created children")
}

func TestFactory_FactoryModelHasManyRelationshipHasPendingAttributesOverride(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(); the corpus overrides the relation's pending attributes with the child factory's state, which cannot run through a factory")
}

func TestFactory_FactoryModelHasOneRelationshipHasPendingAttributes(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(), so a has-one pending-attribute relation cannot be created through a factory")
}

func TestFactory_FactoryModelHasOneRelationshipHasPendingAttributesOverride(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(); the has-one override form cannot run")
}

func TestFactory_FactoryModelBelongsToManyRelationshipHasPendingAttributes(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(), so a belongs-to-many pending-attribute relation (with its pivot) cannot be created through a factory")
}

func TestFactory_FactoryModelBelongsToManyRelationshipHasPendingAttributesOverride(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(); the belongs-to-many override form cannot run")
}

func TestFactory_FactoryModelMorphManyRelationshipHasPendingAttributes(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(), so a morph-many pending-attribute relation cannot be created through a factory")
}

func TestFactory_FactoryModelMorphManyRelationshipHasPendingAttributesOverride(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no has(); the morph-many override form cannot run")
}

func TestFactory_FactoryCanInsert(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no insert(); the corpus also reads an accessor-appended attribute (upper_case_name), which the Go ORM does not model")
}

func TestFactory_FactoryCanInsertWithHidden(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no insert(), and the test's point — insert() persists an attribute hidden on the model — has no counterpart (the Go ORM models no $hidden list)")
}

func TestFactory_FactoryCanInsertWithArrayCasts(t *testing.T) {
	t.Skip("our ORM diverges: the Go factory has no insert(); array casts (options written to the table as JSON) are applied on model writes, not by the factory")
}
