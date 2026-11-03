package belongs_to

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestBelongsTo_<method without the
// test prefix>. The corpus drives one BelongsTo($builder, $parent, 'foreign_key', 'id',
// 'relation') whose builder answers `where "relation"."id" = 'foreign.value'`; almost every test
// pins that arrangement through Mockery, so the ports fall into three groups:
//
//   - The real FK writes — associate by model, associate by id, dissociate — run against the
//     harness sqlite database through Query.Relation(parent, "Owner"), the Go relation writer.
//   - testModelsAreProperlyMatchedToParents becomes an eager load: the corpus's four parents and
//     four results turn into rows, and With("Owner") runs the match by dictionary key.
//   - Mock-interaction tests (addEagerConstraints' whereIntegerInRaw / whereIn call shapes, and
//     initRelation's setRelation) stay skipped with "PHP-only assertion (no Go equivalent)"; the
//     is / isNot predicates now run against the relation object (typed stubs carry the corpus's
//     string keys and mocked tables/connections).
//   - The three withDefault tests run real assertions against orm.BelongsTo.WithDefault: a missing
//     owner (or a null foreign key) loads a fresh related instance, the closure / array variants
//     fill it, and a matched row still wins. Pinned by execution with the oracle
//     (.claude/laravel-oracle/oracle.php, illuminate/database v12.55.1): withDefault()->getResults()
//     yields a fresh instance with attributes [] and exists false; the dynamic and array forms fill
//     it; a query hit returns the real row; `$parent->relation()->first()` — the Go Related() query —
//     ignores the default.
//
// Every SQL string this file cites was measured with the oracle (.claude/laravel-oracle/oracle.php
// against illuminate/database v12.55.1), not recalled: the relation read path is
// `select * from "relation" where "relation"."id" = ?` with binding 'foreign.value';
// addEagerConstraints renders `select * from "relation" where "relation"."id" in (0, 0)` for the
// corpus's ['foreign.value', 'foreign.value.two'] keys (whereIntegerInRaw casts each key with
// (int), so the corpus's string keys all compile to 0 — the mock pins the pre-cast call),
// `in (5, 0)` for [5 (BackedEnum cast), 'foreign.value'], and `where 0 = 1` for an empty key set
// on both the whereIntegerInRaw (int key type) and whereIn (string key type) branches. The match
// fixtures map to keys 1, 2, 3 and Bar(5), and withDefault() builds a fresh related instance
// that the closure / array variants fill.

// --- models ------------------------------------------------------------------

// belongsToRelation ports the corpus's related table `relation`, keyed by id.
type belongsToRelation struct {
	ID   uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`
}

func (belongsToRelation) TableName() string { return "relation" }

// belongsToParent ports EloquentBelongsToModelStub in the writer tests: the model holding
// foreign_key. The column is nullable so dissociate can restore the null the corpus asserts.
type belongsToParent struct {
	ID         uint64             `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey *uint64            `gorm:"column:foreign_key"`
	Owner      *belongsToRelation `gorm:"-"`
}

func (belongsToParent) TableName() string { return "parents" }

func (belongsToParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &belongsToRelation{}, ForeignKey: "foreign_key", OwnerKey: "id"},
	}
}

// belongsToMatchParent ports the parent of testModelsAreProperlyMatchedToParents. The corpus's one
// stub carries the foreign key's four representations — int, a __toString object, a BackedEnum
// cast — because PHP coerces them in the dictionary key. A Go column has one type, so the key is
// text here and the loader's dictKey normalisation performs the same textual comparison.
type belongsToMatchParent struct {
	ID         uint64             `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey string             `gorm:"column:foreign_key"`
	Owner      *belongsToRelation `gorm:"-"`
}

func (belongsToMatchParent) TableName() string { return "match_parents" }

func (belongsToMatchParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &belongsToRelation{}, ForeignKey: "foreign_key", OwnerKey: "id"},
	}
}

// belongsToDefaultParent declares the corpus's withDefault() BelongsTo: a missing owner — or an
// empty foreign key, the Go zero standing in for the corpus's null — loads a fresh instance of
// the related model. The closure and array variants below carry the dynamic / array forms; the
// port's related username column is name.
type belongsToDefaultParent struct {
	ID         uint64             `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey uint64             `gorm:"column:foreign_key"`
	Owner      *belongsToRelation `gorm:"-"`
}

func (belongsToDefaultParent) TableName() string { return "parents" }

func (belongsToDefaultParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &belongsToRelation{}, ForeignKey: "foreign_key", OwnerKey: "id", WithDefault: true},
	}
}

// belongsToClosureDefaultParent is withDefault(function ($newModel) { $newModel->username =
// 'taylor'; }): the callback fills the fresh instance in place and its nil return keeps it.
type belongsToClosureDefaultParent struct {
	ID         uint64             `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey uint64             `gorm:"column:foreign_key"`
	Owner      *belongsToRelation `gorm:"-"`
}

func (belongsToClosureDefaultParent) TableName() string { return "parents" }

func (belongsToClosureDefaultParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{
			Related: &belongsToRelation{}, ForeignKey: "foreign_key", OwnerKey: "id",
			WithDefault: func(related, parent any) any {
				related.(*belongsToRelation).Name = "taylor"
				return nil
			},
		},
	}
}

// belongsToArrayDefaultParent is withDefault(['username' => 'taylor']) — force-fill.
type belongsToArrayDefaultParent struct {
	ID         uint64             `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey uint64             `gorm:"column:foreign_key"`
	Owner      *belongsToRelation `gorm:"-"`
}

func (belongsToArrayDefaultParent) TableName() string { return "parents" }

func (belongsToArrayDefaultParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &belongsToRelation{}, ForeignKey: "foreign_key", OwnerKey: "id", WithDefault: map[string]any{"name": "taylor"}},
	}
}

// --- schema and seeding ------------------------------------------------------

// belongsToSchema is the schema getRelation() implies for the writer tests: the related relation
// table and a parent table carrying the nullable foreign_key.
func belongsToSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table relation (id integer primary key autoincrement, name varchar not null)`,
		`create table parents (id integer primary key autoincrement, foreign_key integer null)`,
	)
}

// belongsToMatchSchema is the schema for the match port: the same relation table plus the
// text-keyed parent table.
func belongsToMatchSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table relation (id integer primary key autoincrement, name varchar not null)`,
		`create table match_parents (id integer primary key autoincrement, foreign_key varchar not null)`,
	)
}

// belongsToSeedOwners creates the two related rows the writer tests associate between.
func belongsToSeedOwners(t *testing.T, query contractsorm.Query) (belongsToRelation, belongsToRelation) {
	t.Helper()

	owner1 := belongsToRelation{ID: 1, Name: "first"}
	owner2 := belongsToRelation{ID: 2, Name: "second"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&owner1))
	require.NoError(t, cloneRelationQuery(t, query).Create(&owner2))

	return owner1, owner2
}

// --- ports of the corpus -----------------------------------------------------

func TestBelongsTo_BelongsToWithDefault(t *testing.T) {
	query := newRelationQuery(t)
	belongsToSchema(t, query)
	owner1, _ := belongsToSeedOwners(t, query)

	// Laravel: $relation->withDefault(); the mocked builder's first() returns null; getResults()
	// must return the related model's newInstance(). Oracle (foreign key pointing at no row): the
	// default is a fresh instance of the related model — attributes [], exists false — with
	// nothing set.
	missingParent := belongsToDefaultParent{ID: 1, ForeignKey: 99}
	require.NoError(t, cloneRelationQuery(t, query).Create(&missingParent))

	require.NoError(t, cloneRelationQuery(t, query).Load(&missingParent, "Owner"))
	require.NotNil(t, missingParent.Owner, "a missing owner must load the default instance")
	assert.Zero(t, missingParent.Owner.ID)
	assert.Empty(t, missingParent.Owner.Name)

	// Oracle (null foreign key): getResults short-circuits on is_null($foreignKey) and returns the
	// default without consulting the related table at all — the port's zero foreign key takes the
	// same short-circuit.
	nullParent := belongsToDefaultParent{ID: 2}
	require.NoError(t, cloneRelationQuery(t, query).Create(&nullParent))

	require.NoError(t, cloneRelationQuery(t, query).Load(&nullParent, "Owner"))
	require.NotNil(t, nullParent.Owner)
	assert.Zero(t, nullParent.Owner.ID)

	// Oracle (existing row): the query result wins over the default — initRelation seeds the
	// default on every parent and match overwrites the matched ones.
	linkedParent := belongsToDefaultParent{ID: 3, ForeignKey: owner1.ID}
	require.NoError(t, cloneRelationQuery(t, query).Create(&linkedParent))

	require.NoError(t, cloneRelationQuery(t, query).Load(&linkedParent, "Owner"))
	require.NotNil(t, linkedParent.Owner)
	assert.Equal(t, owner1.ID, linkedParent.Owner.ID)
	assert.Equal(t, "first", linkedParent.Owner.Name)

	// Oracle (eager): the same merge as Laravel's eager load — every parent is seeded with the
	// default before the query and match overwrites the ones it answered.
	found := []belongsToDefaultParent{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToDefaultParent{}).With("Owner").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 3)
	require.NotNil(t, found[0].Owner)
	assert.Zero(t, found[0].Owner.ID, "the parent whose key answered nothing keeps the default")
	require.NotNil(t, found[2].Owner)
	assert.Equal(t, owner1.ID, found[2].Owner.ID)
}

func TestBelongsTo_BelongsToWithDynamicDefault(t *testing.T) {
	query := newRelationQuery(t)
	belongsToSchema(t, query)

	// Laravel: withDefault(function ($newModel) { $newModel->username = 'taylor'; }); getResults()
	// returns the new model carrying the callback's attribute. Oracle: the closure fills the fresh
	// instance — {"name":"taylor"} on the port's related model — and its nil return keeps it.
	parent := belongsToClosureDefaultParent{ID: 1, ForeignKey: 99}
	require.NoError(t, cloneRelationQuery(t, query).Create(&parent))

	require.NoError(t, cloneRelationQuery(t, query).Load(&parent, "Owner"))
	require.NotNil(t, parent.Owner)
	assert.Equal(t, "taylor", parent.Owner.Name)
	assert.Zero(t, parent.Owner.ID)
}

func TestBelongsTo_BelongsToWithArrayDefault(t *testing.T) {
	query := newRelationQuery(t)
	belongsToSchema(t, query)

	// Laravel: withDefault(['username' => 'taylor']); getResults() force-fills the new model.
	// Oracle: the array default fills the fresh instance — {"name":"taylor"} here.
	parent := belongsToArrayDefaultParent{ID: 1, ForeignKey: 99}
	require.NoError(t, cloneRelationQuery(t, query).Create(&parent))

	require.NoError(t, cloneRelationQuery(t, query).Load(&parent, "Owner"))
	require.NotNil(t, parent.Owner)
	assert.Equal(t, "taylor", parent.Owner.Name)
	assert.Zero(t, parent.Owner.ID)
}

func TestBelongsTo_EagerConstraintsAreProperlyAdded(t *testing.T) {
	// Laravel pins, through a mocked builder, one whereIntegerInRaw('relation.id',
	// ['foreign.value', 'foreign.value.two']) call built from the parents' distinct non-null
	// foreign keys. Oracle: the real call casts each key with (int) and renders
	// `select * from "relation" where "relation"."id" in (0, 0)`.
	t.Skip("PHP-only assertion (no Go equivalent): a mocked builder pins the parents' distinct non-null foreign keys into one whereIntegerInRaw('relation.id', ['foreign.value', 'foreign.value.two']) call; the Go eager loader compiles its IN clause internally (oracle: `... in (0, 0)`)")
}

func TestBelongsTo_IdsInEagerConstraintsCanBeZero(t *testing.T) {
	// Laravel pins, through a mocked builder, whereIntegerInRaw('relation.id',
	// [0, 'foreign.value']): an integer zero foreign key stays in the key list. Oracle: the real
	// call renders `select * from "relation" where "relation"."id" in (0, 0)`.
	t.Skip("PHP-only assertion (no Go equivalent): a mocked builder pins that an integer zero foreign key stays in the eager key list - whereIntegerInRaw('relation.id', [0, 'foreign.value']); the Go loader builds the same IN clause with no observable seam")
}

func TestBelongsTo_IdsInEagerConstraintsCanBeBackedEnum(t *testing.T) {
	// Laravel pins, through a mocked builder, whereIntegerInRaw('relation.id',
	// [5, 'foreign.value']): a BackedEnum-cast foreign key contributes its value. Oracle: the real
	// call renders `select * from "relation" where "relation"."id" in (5, 0)`.
	t.Skip("PHP-only assertion (no Go equivalent): a mocked builder pins that a BackedEnum-cast foreign key contributes its value (5) to the eager key list - whereIntegerInRaw('relation.id', [5, 'foreign.value']); Go models have no attribute casts and the call is mock-only")
}

func TestBelongsTo_RelationIsProperlyInitialized(t *testing.T) {
	// Laravel: initRelation([$model], 'foo') must call setRelation('foo', null) on every model
	// (the relation has no default) and return the models.
	t.Skip("PHP-only assertion (no Go equivalent): asserts initRelation seeds each model's relation slot with the relation's default (null here) via setRelation and returns the models; the Go eager loader initializes relation fields internally and exposes no initRelation call")
}

func TestBelongsTo_ModelsAreProperlyMatchedToParents(t *testing.T) {
	query := newRelationQuery(t)
	belongsToMatchSchema(t, query)

	// The corpus's four results: ids 1, 2, 3, and a Bar enum whose value is 5 (result4's id cast).
	// Oracle (match over the corpus fixtures): model0 -> 1, model1 -> 2, model2 -> 3, model3 ->
	// Bar(5), matching the corpus's four assertions.
	for _, row := range []belongsToRelation{
		{ID: 1, Name: "one"},
		{ID: 2, Name: "two"},
		{ID: 3, Name: "three"},
		{ID: 5, Name: "five"},
	} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&row))
	}

	// The corpus's four parents: foreign keys 1, 2, an object whose __toString is '3', and 5. The
	// Go port seeds them as the text column's values; the eager loader normalises parent FK and
	// owner key through dictKey, which is the same textual comparison PHP's dictionary performs
	// via getDictionaryKey (__toString) and enum_value (the cast).
	for i, fk := range []string{"1", "2", "3", "5"} {
		require.NoError(t, cloneRelationQuery(t, query).Create(&belongsToMatchParent{ID: uint64(i + 1), ForeignKey: fk}))
	}

	// Laravel: $relation->match($models, new Collection($results), 'foo'); the Go counterpart is
	// the eager load, whose match step pairs each result to its parent by dictionary key. The port
	// reads through Get: First with an eager load would leave its single-row cursor open while the
	// eager query runs, against the harness's one-connection pool.
	found := []belongsToMatchParent{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToMatchParent{}).With("Owner").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 4)

	// assertEquals(1, $models[0]->foo->getAttribute('id'))
	require.NotNil(t, found[0].Owner)
	assert.Equal(t, uint64(1), found[0].Owner.ID)

	// assertEquals(2, $models[1]->foo->getAttribute('id'))
	require.NotNil(t, found[1].Owner)
	assert.Equal(t, uint64(2), found[1].Owner.ID)

	// assertSame('3', (string) $models[2]->foo->getAttribute('id')) — the parent's foreign key was
	// an object whose string form is '3'; the text FK "3" normalises to the same dictionary key as
	// the related row's integer 3.
	require.NotNil(t, found[2].Owner)
	assert.Equal(t, uint64(3), found[2].Owner.ID)

	// assertEquals(5, $models[3]->foo->getAttribute('id')->value) — the result's id was a Bar enum
	// cast with value 5; the Go row carries the resolved key 5.
	require.NotNil(t, found[3].Owner)
	assert.Equal(t, uint64(5), found[3].Owner.ID)
}

func TestBelongsTo_AssociateMethodSetsForeignKeyOnModel(t *testing.T) {
	query := newRelationQuery(t)
	belongsToSchema(t, query)

	_, owner2 := belongsToSeedOwners(t, query)

	fk := uint64(1)
	parent := belongsToParent{ID: 1, ForeignKey: &fk}
	require.NoError(t, cloneRelationQuery(t, query).Create(&parent))

	// Laravel: $relation->associate($associate) — the mocked parent expects
	// setAttribute('foreign_key', 1) and setRelation('relation', $associate). The Go writer sets
	// the FK from the owner's id; two divergences are documented in the file's divergences list:
	// it persists the parent immediately (probe: the row reads foreign_key 2 after the call),
	// where Laravel's associate only mutates the in-memory model until save(); and it does not
	// cache the owner on the model, so the setRelation half has no counterpart.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Owner").Associate(&owner2))
	require.NotNil(t, parent.ForeignKey)
	assert.Equal(t, uint64(2), *parent.ForeignKey)
}

func TestBelongsTo_DissociateMethodUnsetsForeignKeyOnModel(t *testing.T) {
	query := newRelationQuery(t)
	belongsToSchema(t, query)
	belongsToSeedOwners(t, query)

	fk := uint64(1)
	parent := belongsToParent{ID: 1, ForeignKey: &fk}
	require.NoError(t, cloneRelationQuery(t, query).Create(&parent))

	// Laravel: dissociate() sets the foreign_key attribute to null and the relation to null (both
	// mocked). The Go writer clears the FK to nil on the nullable column — probe: the field is nil
	// and the persisted row's foreign_key is NULL — and, as with associate, persists the parent
	// and manages no relation field, so the setRelation(null) half has no counterpart.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Owner").Dissociate())
	assert.Nil(t, parent.ForeignKey)
}

func TestBelongsTo_AssociateMethodSetsForeignKeyOnModelById(t *testing.T) {
	query := newRelationQuery(t)
	belongsToSchema(t, query)
	belongsToSeedOwners(t, query)

	fk := uint64(2)
	parent := belongsToParent{ID: 1, ForeignKey: &fk}
	require.NoError(t, cloneRelationQuery(t, query).Create(&parent))

	// Laravel: associate(1) — the id form — sets foreign_key to 1 and always unsets the relation
	// cache, regardless of dirtiness (isDirty is never consulted). The Go writer takes a related
	// instance, not a bare id (probe: Associate(2) and Associate(uint64(2)) return "Related:
	// parent must be a non-nil pointer to a struct"), so a key-only instance drives the same FK
	// write; the parent's relation field stays nil, which is the end state the by-id form leaves.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Owner").Associate(&belongsToRelation{ID: 1}))
	require.NotNil(t, parent.ForeignKey)
	assert.Equal(t, uint64(1), *parent.ForeignKey)
	assert.Nil(t, parent.Owner)
}

func TestBelongsTo_DefaultEagerConstraintsWhenIncrementing(t *testing.T) {
	// Laravel pins, via a mocked builder, whereIntegerInRaw('relation.id', []) for parents whose
	// foreign keys are all null, when the related model auto-increments with an int key. Oracle:
	// the real call renders `select * from "relation" where 0 = 1`.
	t.Skip("PHP-only assertion (no Go equivalent): a mocked builder pins an all-null key set to whereIntegerInRaw('relation.id', []) for an int-keyed related model; the Go loader compiles the empty IN internally (oracle: `where 0 = 1`)")
}

func TestBelongsTo_DefaultEagerConstraintsWhenIncrementingAndNonIntKeyType(t *testing.T) {
	// Laravel pins, via a mocked builder, whereIn('relation.id', []) — the whereIn branch, not
	// whereIntegerInRaw — for an all-null key set when the related model's key type is not int.
	// Oracle: the real call renders `select * from "relation" where 0 = 1`.
	t.Skip("PHP-only assertion (no Go equivalent): a mocked builder pins an empty key set to the whereIn('relation.id', []) branch when the related model's key type is not int; the Go loader has no observable seam (oracle: `where 0 = 1`)")
}

func TestBelongsTo_DefaultEagerConstraintsWhenNotIncrementing(t *testing.T) {
	// In 12.x this test's body is identical to the incrementing case: a mocked builder pins
	// whereIntegerInRaw('relation.id', []) for an all-null key set. Oracle: `where 0 = 1`.
	t.Skip("PHP-only assertion (no Go equivalent): identical in 12.x to the incrementing case — a mocked builder pins whereIntegerInRaw('relation.id', []) for an all-null key set")
}

// --- relation-object is() / isNot() ------------------------------------------

// The corpus pins is()/isNot() through mocked Models carrying the string key 'foreign.value'. The
// port keeps the comparison but gives it typed shapes: the parent's foreign key is a real column,
// so the integer and string halves of compareKeys' cast matrix become typed stubs sharing the
// 'relation' table (the same matrix is pinned through the real builder in
// database/gorm/relations/belongs_to_test.go). The connection cases use a related class declaring
// 'relation' — the corpus's mock connection name — so the equality weighs a non-empty value.

// belongsToConnectedParent is the corpus's parent for the is() tests: its related mock declared
// table 'relation' and connection 'relation'.
type belongsToConnectedParent struct {
	ID         uint64                      `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey *uint64                     `gorm:"column:foreign_key"`
	Owner      *belongsToConnectedRelation `gorm:"-"`
}

func (belongsToConnectedParent) TableName() string { return "parents" }

func (belongsToConnectedParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &belongsToConnectedRelation{}, ForeignKey: "foreign_key", OwnerKey: "id"},
	}
}

// belongsToConnectedRelation is the corpus's related mock: table 'relation' with connection
// 'relation'.
type belongsToConnectedRelation struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (belongsToConnectedRelation) TableName() string  { return "relation" }
func (belongsToConnectedRelation) Connection() string { return "relation" }

// belongsToStringFkParent carries the parent foreign key as text — the corpus's string parent key.
type belongsToStringFkParent struct {
	ID         uint64                      `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey string                      `gorm:"column:foreign_key"`
	Owner      *belongsToConnectedRelation `gorm:"-"`
}

func (belongsToStringFkParent) TableName() string { return "parents" }

func (belongsToStringFkParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &belongsToConnectedRelation{}, ForeignKey: "foreign_key", OwnerKey: "id"},
	}
}

// belongsToStringKeyRelation shares the 'relation' table under a text key — the corpus's string
// model key.
type belongsToStringKeyRelation struct {
	ID string `gorm:"column:id;primaryKey"`
}

func (belongsToStringKeyRelation) TableName() string  { return "relation" }
func (belongsToStringKeyRelation) Connection() string { return "relation" }

// belongsToOtherTableRelation shares the connection but not the table — the corpus's 'table.two'
// model.
type belongsToOtherTableRelation struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (belongsToOtherTableRelation) TableName() string  { return "relation.two" }
func (belongsToOtherTableRelation) Connection() string { return "relation" }

// belongsToOtherConnRelation shares the table but not the connection — the corpus's
// 'connection.two' model.
type belongsToOtherConnRelation struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (belongsToOtherConnRelation) TableName() string  { return "relation" }
func (belongsToOtherConnRelation) Connection() string { return "relation.two" }

func TestBelongsTo_IsNotNull(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: assertFalse($relation->is(null)) — a null model never matches, and the related
	// connection is never consulted (oracle: bt is(null) false).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.False(t, rel.Is(nil))
}

func TestBelongsTo_IsModel(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is true when the parent key equals the model's key (string-coerced) and
	// the related table and connection names both equal the model's (oracle: bt conn is(conn id 1)
	// true).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.True(t, rel.Is(&belongsToConnectedRelation{ID: 1}))
}

func TestBelongsTo_IsModelWithIntegerParentKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is true when the parent key is the integer 1 and the model's key the
	// string '1' — compareKeys casts to int when either side is an int (oracle: bt is(id "1")
	// true).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.True(t, rel.Is(&belongsToStringKeyRelation{ID: "1"}))
}

func TestBelongsTo_IsModelWithIntegerRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is true when the parent key is the string '1' and the model's key the
	// integer 1 (oracle: bt str-fk is(id 1) true).
	rel := cloneRelationQuery(t, query).Related(&belongsToStringFkParent{ID: 1, ForeignKey: "1"}, "Owner").(contractsorm.BelongsToRelation)
	assert.True(t, rel.Is(&belongsToConnectedRelation{ID: 1}))
}

func TestBelongsTo_IsModelWithIntegerKeys(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is true with integer keys on both sides (oracle: bt is(id 1) true).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.True(t, rel.Is(&belongsToConnectedRelation{ID: 1}))
}

func TestBelongsTo_IsNotModelWithNullParentKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the parent's foreign key is null — compareKeys treats an
	// empty key as no match (oracle: bt null-fk is(id 1) false).
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1}, "Owner").(contractsorm.BelongsToRelation)
	assert.False(t, rel.Is(&belongsToConnectedRelation{ID: 1}))
}

func TestBelongsTo_IsNotModelWithNullRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the model's key is null. The Go key is value-typed, so
	// the zero takes the empty-key reading compareKeys gives the PHP null (oracle: bt is(id null)
	// false).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.False(t, rel.Is(&belongsToConnectedRelation{}))
}

func TestBelongsTo_IsNotModelWithAnotherKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the parent key ('foreign.value') and the model's key
	// ('foreign.value.two') differ (oracle: bt is(id 2) false).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.False(t, rel.Is(&belongsToConnectedRelation{ID: 2}))
}

func TestBelongsTo_IsNotModelWithAnotherTable(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the keys are equal but the model's table differs (oracle:
	// bt is(other table id 1) false).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.False(t, rel.Is(&belongsToOtherTableRelation{ID: 1}))
}

func TestBelongsTo_IsNotModelWithAnotherConnection(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the keys and tables are equal but the model's connection
	// differs (oracle: bt is(other conn id 1) false).
	fk := uint64(1)
	rel := cloneRelationQuery(t, query).Related(&belongsToConnectedParent{ID: 1, ForeignKey: &fk}, "Owner").(contractsorm.BelongsToRelation)
	assert.False(t, rel.Is(&belongsToOtherConnRelation{ID: 1}))
}
