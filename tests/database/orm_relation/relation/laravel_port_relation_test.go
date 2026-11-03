package relation

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentRelationTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestRelation_<method without the test
// prefix>, kept in corpus order.
//
// The corpus is a PHP-unit file about Eloquent's model-object relation machinery: the relations
// bag (setRelation/getRelation/unsetRelation/relationLoaded/withoutRelations), HasOne::touch()
// asserted through Mockery expectations, class-level touch suppression (withoutTouching /
// withoutTouchingOn / isIgnoringTouch), the PHP array semantics of Relation::morphMap, relation
// macros, and Attribute-versus-relation introspection. The touch ports below — the relation-scoped
// update, and the class-level suppression the relation's touch consults — build phones schemas to
// observe the update end to end; apart from them the tests open no schema and seed no rows, so
// there is no shared setUp fixture; where the tested machinery maps onto an API the Go ORM really
// has, the behaviour is exercised against that API, and where it does not the test is skipped
// with the concrete reason instead of being weakened.
//
// The one behaviour the Go ORM carries — the morph registry behind Relation::morphMap and
// Relation::getMorphAlias (orm.MorphMap / orm.MorphAlias) — is ported behaviourally. The
// Laravel-side expectations the port cites were measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), never recalled:
// getMorphAlias resolves a registered class-string to its alias and echoes an unknown class-string
// back unchanged; a numeric-array morph map derives each alias from the model's table name;
// numeric keys are kept verbatim; and toArray() drops a loaded relation whose value is not a
// model.

// --- shared skip reasons -----------------------------------------------------

const (
	// skipRelationModelObjectAPI covers the tests that exercise Eloquent's relations-bag
	// bookkeeping: the Go ORM keeps loaded relations in model struct fields instead.
	skipRelationModelObjectAPI = "our ORM diverges: loaded relations are model struct fields, not a relations bag — setRelation/getRelation/unsetRelation/relationLoaded/withoutRelations/unsetRelations have no Go API; loaded-ness is a zero-value check inside LoadMissing"

	// skipRelationMorphListForms covers the morphMap tests whose input is a PHP array shape.
	skipRelationMorphListForms = "PHP-only assertion (no Go equivalent): orm.MorphMap takes explicit string aliases, so the corpus's numeric-array morph-list forms (aliases derived from each model's table name; numeric keys kept verbatim) have no Go call form to exercise"

	// skipRelationToArray covers the toArray() serialization assertion.
	skipRelationToArray = "our ORM diverges: Go models have no toArray/serialization pipeline — the relationsToArray() rule that drops a loaded relation whose value is not a model has no Go counterpart"

	// skipRelationMacro covers the Relation::macro test.
	skipRelationMacro = "PHP-only assertion (no Go equivalent): the test asserts Relation::macro registers a runtime method on the class, which a Go type's fixed method set cannot carry"

	// skipRelationIsRelation covers the isRelation introspection test.
	skipRelationIsRelation = "PHP-only assertion (no Go equivalent): the test asserts isRelation distinguishes relationship methods from Attribute accessors by reflection; the Go ORM has no attribute-mutator concept — relations are declared as Relations() map entries"
)

// --- models ------------------------------------------------------------------

// relationMorphUser is the Go side of the corpus's 'user' => 'App\User' entry: Laravel registers
// the class-string, while the Go registry is keyed by the model's type.
type relationMorphUser struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
}

func (relationMorphUser) TableName() string { return "users" }

// relationMorphUnmapped stands in for the corpus's 'Does\Not\Exist': a model no registry entry
// covers.
type relationMorphUnmapped struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
}

func (relationMorphUnmapped) TableName() string { return "unmapped" }

// relationTouchUser is the corpus's parent mock (id 1) for the touch port: the model the relation
// hangs off.
type relationTouchUser struct {
	ID    uint64              `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Phone *relationTouchPhone `gorm:"-" db:"-"`
}

func (relationTouchUser) TableName() string { return "users" }

func (relationTouchUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Phone": contractsorm.HasOne{Related: &relationTouchPhone{}, ForeignKey: "foreign_key", LocalKey: "id"},
	}
}

// relationTouchPhone is the related model with Laravel's timestamps: the updated_at column
// getUpdatedAtColumn resolves, which the relation touch stamps.
type relationTouchPhone struct {
	ID         uint64           `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	ForeignKey uint64           `gorm:"column:foreign_key" db:"foreign_key"`
	UpdatedAt  *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at" db:"updated_at"`
}

func (relationTouchPhone) TableName() string { return "phones" }

// --- class-level touch suppression fixtures -----------------------------------

// The corpus drives the suppression through Eloquent's class-level statics (Model::withoutTouching,
// the stub class's withoutTouching, isIgnoringTouch). The Go port of that control lives in
// database/orm (WithoutTouching / WithoutTouchingOn / IsIgnoringTouch): a model extends another by
// embedding it — the way every fixture below embeds databaseorm.Model — and the relation-level
// Touch consults the gate, so the corpus's relation-scoped observations run for real.

// RelationTouchScopedPhone embeds the framework's base Model — the Go form of `extends Eloquent` —
// so a WithoutTouching scope entered on the base reaches it. The name is exported so the child
// below embeds an exported field: the orm's updated-at walk (database/orm.touching
// carriesUpdatedAt) only descends into exported anonymous fields, while embedsType itself is
// name-agnostic.
type RelationTouchScopedPhone struct {
	databaseorm.Model

	ForeignKey uint64 `gorm:"column:foreign_key" db:"foreign_key"`
}

func (RelationTouchScopedPhone) TableName() string { return "phones" }

// relationTouchAnotherPhone is the unrelated class whose touches stay enabled while the scoped
// class is suppressed.
type relationTouchAnotherPhone struct {
	databaseorm.Model

	ForeignKey uint64 `gorm:"column:foreign_key" db:"foreign_key"`
}

func (relationTouchAnotherPhone) TableName() string { return "another_phones" }

// relationTouchChildPhone extends RelationTouchScopedPhone the Go way — embedding it — the
// corpus's EloquentNoTouchingChildModelStub.
type relationTouchChildPhone struct {
	RelationTouchScopedPhone
}

func (relationTouchChildPhone) TableName() string { return "phones_child" }

// relationTouchScopedUser carries the parent half of the suppression tests: one relation per
// scoped, unrelated and embedding-child related class.
type relationTouchScopedUser struct {
	ID         uint64                     `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	Phone      *RelationTouchScopedPhone  `gorm:"-" db:"-"`
	Another    *relationTouchAnotherPhone `gorm:"-" db:"-"`
	PhoneChild *relationTouchChildPhone   `gorm:"-" db:"-"`
}

func (relationTouchScopedUser) TableName() string { return "users" }

func (relationTouchScopedUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Phone":      contractsorm.HasOne{Related: &RelationTouchScopedPhone{}, ForeignKey: "foreign_key", LocalKey: "id"},
		"Another":    contractsorm.HasOne{Related: &relationTouchAnotherPhone{}, ForeignKey: "foreign_key", LocalKey: "id"},
		"PhoneChild": contractsorm.HasOne{Related: &relationTouchChildPhone{}, ForeignKey: "foreign_key", LocalKey: "id"},
	}
}

// --- ports of the corpus -----------------------------------------------------

func TestRelation_SetRelationFail(t *testing.T) {
	// Laravel: setRelation('test', $model) and setRelation('foo', 'bar') on a bare model, then
	// assertArrayNotHasKey('foo', $parent->toArray()) — relationsToArray() emits only relation
	// values that are models (or null), so the string value is dropped. Measured with the oracle:
	// the toArray() keys are ['test'] and 'foo' is absent.
	t.Skip(skipRelationToArray)
}

func TestRelation_UnsetExistingRelation(t *testing.T) {
	// Laravel: setRelation('foo', $relation); unsetRelation('foo');
	// assertFalse($parent->relationLoaded('foo')).
	t.Skip(skipRelationModelObjectAPI)
}

func TestRelation_TouchMethodUpdatesRelatedTimestamps(t *testing.T) {
	query := newRelationQuery(t)
	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table phones (id integer primary key autoincrement, foreign_key integer not null, created_at datetime null, updated_at datetime null)`,
	)

	// Laravel's only assertion is a Mockery expectation: HasOne::touch() runs
	// $builder->update(['updated_at' => $now]) exactly once for the related table with global
	// scopes dropped (Relation::touch -> rawUpdate, Relations/Relation.php). The relation object
	// Query.Related returns carries that update for real now, so the port observes the equivalent
	// end state over a real schema: one constrained update reaches exactly the parent's related
	// row, and another parent's row stays untouched.
	first := relationTouchPhone{ForeignKey: 1}
	require.NoError(t, cloneRelationQuery(t, query).Create(&first))
	second := relationTouchPhone{ForeignKey: 2}
	require.NoError(t, cloneRelationQuery(t, query).Create(&second))
	for _, phone := range []relationTouchPhone{first, second} {
		_, err := cloneRelationQuery(t, query).Exec(`update phones set updated_at = ? where id = ?`, "2020-01-01 00:00:00", phone.ID)
		require.NoError(t, err)
	}

	parent := relationTouchUser{ID: 1}
	stampLowerBound := carbon.Now().ToDateTimeString()
	result, err := cloneRelationQuery(t, query).Related(&parent, "Phone").Touch()
	require.NoError(t, err)
	require.NotNil(t, result)
	stampUpperBound := carbon.Now().ToDateTimeString()
	assert.EqualValues(t, 1, result.RowsAffected, "one constrained update reaches exactly the parent's related row")

	reloaded := relationTouchPhone{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", first.ID).First(&reloaded))
	require.NotNil(t, reloaded.UpdatedAt)
	assert.NotEqual(t, "2020-01-01 00:00:00", reloaded.UpdatedAt.ToDateTimeString(), "the related row's updated_at is stamped fresh")
	assert.GreaterOrEqual(t, reloaded.UpdatedAt.ToDateTimeString(), stampLowerBound, "the stamp is the touch's fresh timestamp, not an older or zero value")
	assert.LessOrEqual(t, reloaded.UpdatedAt.ToDateTimeString(), stampUpperBound, "the stamp is the touch's fresh timestamp, not a future one")

	untouched := relationTouchPhone{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", second.ID).First(&untouched))
	require.NotNil(t, untouched.UpdatedAt)
	assert.Equal(t, "2020-01-01 00:00:00", untouched.UpdatedAt.ToDateTimeString(), "another parent's row is outside the relation constraint")
}

func TestRelation_CanDisableParentTouchingForAllModels(t *testing.T) {
	query := newRelationQuery(t)
	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table phones (id integer primary key autoincrement, foreign_key integer not null, created_at datetime null, updated_at datetime null)`,
	)
	first := RelationTouchScopedPhone{ForeignKey: 1}
	require.NoError(t, cloneRelationQuery(t, query).Create(&first))
	_, err := cloneRelationQuery(t, query).Exec(`update phones set updated_at = ? where id = ?`, "2020-01-01 00:00:00", first.ID)
	require.NoError(t, err)
	parent := relationTouchScopedUser{ID: 1}

	// Laravel: inside Model::withoutTouching(fn) isIgnoringTouch() is true and HasOne::touch()'s
	// update never runs; outside the scope isIgnoringTouch() is false again.
	require.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}), "outside the scope the related model's touches are enabled")

	require.NoError(t, databaseorm.WithoutTouching(func() error {
		assert.True(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}), "withoutTouching reaches every model that embeds the base Model")

		result, err := cloneRelationQuery(t, query).Related(&parent, "Phone").Touch()
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.EqualValues(t, 0, result.RowsAffected, "the suppressed relation touch runs no update")

		return nil
	}))

	assert.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}), "the scope restores the state when it ends")

	reloaded := RelationTouchScopedPhone{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", first.ID).First(&reloaded))
	require.NotNil(t, reloaded.UpdatedAt)
	assert.Equal(t, "2020-01-01 00:00:00", reloaded.UpdatedAt.ToDateTimeString(), "the suppressed touch left the related row untouched")

	result, err := cloneRelationQuery(t, query).Related(&parent, "Phone").Touch()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.EqualValues(t, 1, result.RowsAffected, "outside the scope the relation touch runs again")
}

func TestRelation_CanDisableTouchingForSpecificModel(t *testing.T) {
	query := newRelationQuery(t)
	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table phones (id integer primary key autoincrement, foreign_key integer not null, created_at datetime null, updated_at datetime null)`,
		`create table another_phones (id integer primary key autoincrement, foreign_key integer not null, created_at datetime null, updated_at datetime null)`,
	)
	first := RelationTouchScopedPhone{ForeignKey: 1}
	require.NoError(t, cloneRelationQuery(t, query).Create(&first))
	second := relationTouchAnotherPhone{ForeignKey: 2}
	require.NoError(t, cloneRelationQuery(t, query).Create(&second))
	_, err := cloneRelationQuery(t, query).Exec(`update phones set updated_at = ? where id = ?`, "2020-01-01 00:00:00", first.ID)
	require.NoError(t, err)
	_, err = cloneRelationQuery(t, query).Exec(`update another_phones set updated_at = ? where id = ?`, "2020-01-01 00:00:00", second.ID)
	require.NoError(t, err)
	parent := relationTouchScopedUser{ID: 1}
	anotherParent := relationTouchScopedUser{ID: 2}

	// Laravel: withoutTouching() on one model class suppresses its touches only — an unrelated
	// model class keeps isIgnoringTouch() false and its relation's update still runs.
	require.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}))
	require.False(t, databaseorm.IsIgnoringTouch(&relationTouchAnotherPhone{}))

	require.NoError(t, databaseorm.WithoutTouchingOn([]any{&RelationTouchScopedPhone{}}, func() error {
		assert.True(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}), "the entered class ignores touches")
		assert.False(t, databaseorm.IsIgnoringTouch(&relationTouchAnotherPhone{}), "an unrelated class keeps its touches")

		result, err := cloneRelationQuery(t, query).Related(&parent, "Phone").Touch()
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.EqualValues(t, 0, result.RowsAffected, "the suppressed class's relation touch runs no update")

		other, err := cloneRelationQuery(t, query).Related(&anotherParent, "Another").Touch()
		require.NoError(t, err)
		require.NotNil(t, other)
		assert.EqualValues(t, 1, other.RowsAffected, "the unrelated class's relation touch still runs")

		return nil
	}))

	assert.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}))
	assert.False(t, databaseorm.IsIgnoringTouch(&relationTouchAnotherPhone{}))

	reloadedOther := relationTouchAnotherPhone{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", second.ID).First(&reloadedOther))
	require.NotNil(t, reloadedOther.UpdatedAt)
	assert.NotEqual(t, "2020-01-01 00:00:00", reloadedOther.UpdatedAt.ToDateTimeString(), "the unrelated row was stamped inside the scope")
}

func TestRelation_ParentModelIsNotTouchedWhenChildModelIsIgnored(t *testing.T) {
	query := newRelationQuery(t)
	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table phones (id integer primary key autoincrement, foreign_key integer not null, created_at datetime null, updated_at datetime null)`,
		`create table phones_child (id integer primary key autoincrement, foreign_key integer not null, created_at datetime null, updated_at datetime null)`,
	)
	first := RelationTouchScopedPhone{ForeignKey: 1}
	require.NoError(t, cloneRelationQuery(t, query).Create(&first))
	child := relationTouchChildPhone{RelationTouchScopedPhone: RelationTouchScopedPhone{ForeignKey: 1}}
	require.NoError(t, cloneRelationQuery(t, query).Create(&child))
	_, err := cloneRelationQuery(t, query).Exec(`update phones set updated_at = ? where id = ?`, "2020-01-01 00:00:00", first.ID)
	require.NoError(t, err)
	_, err = cloneRelationQuery(t, query).Exec(`update phones_child set updated_at = ? where id = ?`, "2020-01-01 00:00:00", child.ID)
	require.NoError(t, err)
	parent := relationTouchScopedUser{ID: 1}

	// Laravel: suppressing a class reaches its subclasses — the child stub reports
	// isIgnoringTouch() true, so neither relation's update runs.
	require.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}))
	require.False(t, databaseorm.IsIgnoringTouch(&relationTouchChildPhone{}))

	require.NoError(t, databaseorm.WithoutTouchingOn([]any{&RelationTouchScopedPhone{}}, func() error {
		assert.True(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}))
		assert.True(t, databaseorm.IsIgnoringTouch(&relationTouchChildPhone{}), "the suppression reaches the embedding child")

		result, err := cloneRelationQuery(t, query).Related(&parent, "Phone").Touch()
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.EqualValues(t, 0, result.RowsAffected, "the base class's relation touch runs no update")

		childResult, err := cloneRelationQuery(t, query).Related(&parent, "PhoneChild").Touch()
		require.NoError(t, err)
		require.NotNil(t, childResult)
		assert.EqualValues(t, 0, childResult.RowsAffected, "the child class's relation touch runs no update")

		return nil
	}))

	assert.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}))
	assert.False(t, databaseorm.IsIgnoringTouch(&relationTouchChildPhone{}))
}

func TestRelation_IgnoredModelsStateIsResetWhenThereAreExceptions(t *testing.T) {
	// Laravel: withoutTouching() resets the ignored-class state in a finally arm, so after the
	// callback throws, isIgnoringTouch() is false again for both models.
	require.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}))
	require.False(t, databaseorm.IsIgnoringTouch(&relationTouchChildPhone{}))

	expected := errors.New("callback failed")
	err := databaseorm.WithoutTouchingOn([]any{&RelationTouchScopedPhone{}}, func() error {
		assert.True(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}))
		assert.True(t, databaseorm.IsIgnoringTouch(&relationTouchChildPhone{}), "the suppression reaches the embedding child")

		return expected
	})
	require.ErrorIs(t, err, expected)

	assert.False(t, databaseorm.IsIgnoringTouch(&RelationTouchScopedPhone{}), "the reset runs on exception")
	assert.False(t, databaseorm.IsIgnoringTouch(&relationTouchChildPhone{}))
}

func TestRelation_SettingMorphMapWithNumericArrayUsesTheTableNames(t *testing.T) {
	// Laravel: Relation::morphMap([EloquentRelationResetModelStub::class]) — a numeric array is a
	// PHP list, so each entry's alias is derived from the model's table name. Measured with the
	// oracle: morphMap() then returns ['reset' => '...RelationProbeReset'].
	t.Skip(skipRelationMorphListForms)
}

func TestRelation_SettingMorphMapWithNumericKeys(t *testing.T) {
	// Laravel: Relation::morphMap([1 => 'App\User']) — numeric keys make the array not a list, so
	// the map is stored verbatim. Measured with the oracle: morphMap() returns [1 => 'App\User'].
	t.Skip(skipRelationMorphListForms)
}

func TestRelation_GetMorphAlias(t *testing.T) {
	// Laravel: Relation::morphMap(['user' => 'App\User']);
	// assertSame('user', Relation::getMorphAlias('App\User'));
	// assertSame('Does\Not\Exist', Relation::getMorphAlias('Does\Not\Exist')). Measured with the
	// oracle: a registered class resolves to its alias, an unknown class-string is echoed back
	// unchanged. The Go registry is keyed by model type instead of class-name string, so
	// MorphAlias takes the model and reports the unregistered case as ok=false rather than echoing
	// an input that has no string form.
	databaseorm.MorphMap(map[string]any{"user": &relationMorphUser{}}, false)

	alias, ok := databaseorm.MorphAlias(&relationMorphUser{})
	require.True(t, ok)
	assert.Equal(t, "user", alias)

	_, ok = databaseorm.MorphAlias(&relationMorphUnmapped{})
	assert.False(t, ok)
}

func TestRelation_WithoutRelations(t *testing.T) {
	// Laravel: withoutRelations() clones the model and unsets the clone's relations (the original
	// keeps them); unsetRelations() unsets the instance's own relations and returns it.
	t.Skip(skipRelationModelObjectAPI + "; withoutRelations/unsetRelations are the clone-and-clear pair")
}

func TestRelation_Macroable(t *testing.T) {
	// Laravel: Relation::macro('foo', fn () => 'foo'); $relation->foo() then returns 'foo'.
	t.Skip(skipRelationMacro)
}

func TestRelation_IsRelationIgnoresAttribute(t *testing.T) {
	// Laravel: isRelation('parent') is true (parent() returns a BelongsTo) while
	// isRelation('field') is false (field() returns an Attribute accessor — an attribute mutator
	// is not a relationship).
	t.Skip(skipRelationIsRelation)
}
