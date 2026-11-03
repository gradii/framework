package has_one

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasOneTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestHasOne_<method without the test
// prefix>. The corpus is mock-driven: getRelation() builds new HasOne($builder, $parent,
// 'table.foreign_key', 'id') over Mockery models — the parent carries key 1 and username
// "taylor", the related stub carries the foreign_key attribute. Where a test has a real
// observable, the port recreates that setup on the harness sqlite database — a users parent and a
// phones table carrying the foreign_key column — seeding and reading through the ORM, and loading
// relations explicitly (Load / With) because Go has no property-access magic. Tests whose only
// assertion is a mock interaction are skipped as PHP-only.
//
// Laravel behaviour cited below was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled:
//
//   - the relation's own read query is
//     `select * from "phones" where "phones"."foreign_key" = ? and "phones"."foreign_key" is not null`
//   - addEagerConstraints over parents 1 and 2 compiles
//     `select * from "phones" where "phones"."foreign_key" in (1, 2)`
//   - getRelationExistenceCountQuery compiles
//     `select count(*) from "phones" where "users"."id" = "phones"."foreign_key"`
//   - make(['name' => 'taylor']) yields attributes {"name":"taylor","foreign_key":1}
//   - withDefault()->getResults() on a missing phone yields a fresh instance carrying
//     {"foreign_key":1} (exists false); the closure receives ($instance, $parent); the array form
//     force-fills the instance; an existing phone row wins over the default
//   - is(null) is false
//
// # How the Go ORM diverges from Laravel's (each recorded on the skipped test)
//
//   - The RelationWriter now has Make — HasOneOrMany::make's unsaved instance carrying the
//     attributes with the parent key pre-set — so the make test runs for real below; the relation
//     object's ForceCreate (HasOneOrMany::forceCreate, the 1:1 alias of Create — no guard exists
//     to bypass) now covers the forceCreate test, so it runs for real below as well.
//   - The relation is()/isNot() comparison now runs on the relation object Query.Related returns
//     (ComparesRelatedModels' key + table + connection rules), so the is tests run for real below.

// --- models ------------------------------------------------------------------

// hasOneUser ports the corpus's $this->parent — getRelation() configures it with key 1 and
// username "taylor".
type hasOneUser struct {
	ID       uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Username string `gorm:"column:username"`

	Phone *hasOnePhone `gorm:"-"`
}

func (hasOneUser) TableName() string { return "users" }

func (hasOneUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// Laravel: new HasOne($builder, $parent, 'table.foreign_key', 'id'); the port keeps the
		// stub's foreign_key column name on a real related table.
		"Phone": contractsorm.HasOne{Related: &hasOnePhone{}, ForeignKey: "foreign_key", LocalKey: "id"},
	}
}

// hasOnePhone ports EloquentHasOneModelStub, the related model whose foreign_key attribute the
// relation writes.
type hasOnePhone struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey uint64 `gorm:"column:foreign_key"`
	Name       string `gorm:"column:name"`
}

func (hasOnePhone) TableName() string { return "phones" }

// hasOneDefaultUser declares the corpus's withDefault() relation: a user with no phone row loads
// a fresh related instance whose foreign key is pre-set to the parent key. The dynamic variants
// below fill the instance through a closure (the second also receives the parent model); the
// array variant force-fills attributes.
type hasOneDefaultUser struct {
	ID       uint64       `gorm:"column:id;primaryKey;autoIncrement"`
	Username string       `gorm:"column:username"`
	Phone    *hasOnePhone `gorm:"-"`
}

func (hasOneDefaultUser) TableName() string { return "users" }

func (hasOneDefaultUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Phone": contractsorm.HasOne{Related: &hasOnePhone{}, ForeignKey: "foreign_key", LocalKey: "id", WithDefault: true},
	}
}

// hasOneDynamicDefaultUser is withDefault(function ($newModel) { $newModel->username = 'taylor';
// }) — the callback fills the fresh instance; the port's phone column is name.
type hasOneDynamicDefaultUser struct {
	ID       uint64       `gorm:"column:id;primaryKey;autoIncrement"`
	Username string       `gorm:"column:username"`
	Phone    *hasOnePhone `gorm:"-"`
}

func (hasOneDynamicDefaultUser) TableName() string { return "users" }

func (hasOneDynamicDefaultUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Phone": contractsorm.HasOne{
			Related: &hasOnePhone{}, ForeignKey: "foreign_key", LocalKey: "id",
			WithDefault: func(related, parent any) any {
				related.(*hasOnePhone).Name = "taylor"
				return nil
			},
		},
	}
}

// hasOneDynamicDefaultParentUser is withDefault(function ($newModel, $parentModel) {
// $newModel->username = $parentModel->username; }) — the callback also receives the parent model.
type hasOneDynamicDefaultParentUser struct {
	ID       uint64       `gorm:"column:id;primaryKey;autoIncrement"`
	Username string       `gorm:"column:username"`
	Phone    *hasOnePhone `gorm:"-"`
}

func (hasOneDynamicDefaultParentUser) TableName() string { return "users" }

func (hasOneDynamicDefaultParentUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Phone": contractsorm.HasOne{
			Related: &hasOnePhone{}, ForeignKey: "foreign_key", LocalKey: "id",
			WithDefault: func(related, parent any) any {
				related.(*hasOnePhone).Name = parent.(*hasOneDynamicDefaultParentUser).Username
				return nil
			},
		},
	}
}

// hasOneArrayDefaultUser is withDefault(['username' => 'taylor']) — force-fill.
type hasOneArrayDefaultUser struct {
	ID       uint64       `gorm:"column:id;primaryKey;autoIncrement"`
	Username string       `gorm:"column:username"`
	Phone    *hasOnePhone `gorm:"-"`
}

func (hasOneArrayDefaultUser) TableName() string { return "users" }

func (hasOneArrayDefaultUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Phone": contractsorm.HasOne{Related: &hasOnePhone{}, ForeignKey: "foreign_key", LocalKey: "id", WithDefault: map[string]any{"name": "taylor"}},
	}
}

// --- schema and seeding ------------------------------------------------------

// hasOneSchema creates the tables getRelation() implies: a users parent and a phones table
// carrying the foreign_key column.
func hasOneSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, username varchar)`,
		`create table phones (id integer primary key autoincrement, foreign_key integer not null, name varchar)`,
	)
}

// hasOneSeedUser seeds the corpus's parent: id 1, username taylor.
func hasOneSeedUser(t *testing.T, query contractsorm.Query) hasOneUser {
	t.Helper()

	user := hasOneUser{Username: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))
	require.NotZero(t, user.ID)

	return user
}

// --- ports of the corpus -----------------------------------------------------

func TestHasOne_HasOneWithDefault(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneDefaultUser{Username: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	// Laravel: $relation->withDefault()->getResults() — when the relation query's first() finds
	// nothing, a new related instance is returned with the foreign key pre-set to the parent key
	// (1). Oracle: the instance carries {"foreign_key":1} and exists false; nothing else is set.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Phone"))
	require.NotNil(t, user.Phone, "a missing phone must load the default instance")
	assert.Equal(t, user.ID, user.Phone.ForeignKey)
	assert.Empty(t, user.Phone.Name)

	// Oracle (eager): every parent is seeded with the default before the query, and match replaces
	// the ones a phone was found for — an existing row wins; the unmatched parent keeps its
	// default, foreign key included.
	second := hasOneDefaultUser{Username: "abigail"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&second))
	phone := hasOnePhone{ForeignKey: user.ID, Name: "phone-1"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&phone))

	found := []hasOneDefaultUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneDefaultUser{}).With("Phone").OrderBy("id", "asc").Get(&found))
	require.Len(t, found, 2)
	require.NotNil(t, found[0].Phone)
	assert.Equal(t, phone.ID, found[0].Phone.ID)
	assert.Equal(t, "phone-1", found[0].Phone.Name)
	require.NotNil(t, found[1].Phone)
	assert.Equal(t, second.ID, found[1].Phone.ForeignKey)
	assert.Empty(t, found[1].Phone.Name)
}

func TestHasOne_HasOneWithDynamicDefault(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneDynamicDefaultUser{Username: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	// Laravel: withDefault(function ($newModel) { $newModel->username = 'taylor'; }) — the
	// callback configures the default instance returned when the query is null. Oracle: the
	// callback runs after the foreign key is set — {"foreign_key":1,"name":"taylor"}.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Phone"))
	require.NotNil(t, user.Phone)
	assert.Equal(t, "taylor", user.Phone.Name)
	assert.Equal(t, user.ID, user.Phone.ForeignKey)
}

func TestHasOne_HasOneWithDynamicDefaultUseParentModel(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneDynamicDefaultParentUser{Username: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	// Laravel: withDefault(function ($newModel, $parentModel) { $newModel->username =
	// $parentModel->username; }) — the dynamic default also receives the parent model. Oracle:
	// username copied from the parent, foreign key still pre-set.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Phone"))
	require.NotNil(t, user.Phone)
	assert.Equal(t, "taylor", user.Phone.Name)
	assert.Equal(t, user.ID, user.Phone.ForeignKey)
}

func TestHasOne_HasOneWithArrayDefault(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneArrayDefaultUser{Username: "abigail"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	// Laravel: withDefault(['username' => 'taylor']) — the attribute array is force-filled onto
	// the default instance returned when the query is null. Oracle: {"foreign_key":1,
	// "name":"taylor"} — force-fill composes with the pre-set foreign key.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Phone"))
	require.NotNil(t, user.Phone)
	assert.Equal(t, "taylor", user.Phone.Name)
	assert.Equal(t, user.ID, user.Phone.ForeignKey)
}

func TestHasOne_MakeMethodDoesNotSaveNewModel(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneSeedUser(t, query)

	// Laravel: $relation->make(['name' => 'taylor']) — newInstance carrying the attributes, the
	// foreign key set from the parent (1), and no save.
	phone := hasOnePhone{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Phone").Make(map[string]any{"name": "taylor"}, &phone))

	assert.Equal(t, "taylor", phone.Name)
	assert.Equal(t, user.ID, phone.ForeignKey, "make pre-sets the foreign key from the parent")
	assert.Zero(t, phone.ID, "make must not persist the instance")

	count, err := cloneRelationQuery(t, query).Model(&hasOnePhone{}).Count()
	require.NoError(t, err)
	assert.EqualValues(t, 0, count, "make must not insert a row")
}

func TestHasOne_SaveMethodSetsForeignKeyOnModel(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneSeedUser(t, query)

	// Laravel: $relation->save($model) — setForeignAttributesForCreate($model) then
	// $model->save(); the corpus asserts the model's foreign_key attribute is 1 afterwards.
	phone := hasOnePhone{Name: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Phone").Save(&phone))

	assert.Equal(t, user.ID, phone.ForeignKey, "Save must write the parent key into the child's foreign key")
	assert.NotZero(t, phone.ID, "Save must persist the child and read its generated key back")

	var reloaded hasOnePhone
	require.NoError(t, cloneRelationQuery(t, query).Where("foreign_key", user.ID).First(&reloaded))
	assert.Equal(t, "taylor", reloaded.Name)
}

func TestHasOne_CreateMethodProperlyCreatesNewModel(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneSeedUser(t, query)

	// Laravel: $relation->create(['name' => 'taylor']) — related->newInstance(attributes), the
	// foreign key set from the parent (1), then save.
	phone := hasOnePhone{Name: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Phone").Create(&phone))

	assert.Equal(t, user.ID, phone.ForeignKey)
	assert.NotZero(t, phone.ID)

	var reloaded hasOnePhone
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOnePhone{}).Where("id", phone.ID).First(&reloaded))
	assert.Equal(t, "taylor", reloaded.Name)
	assert.Equal(t, user.ID, reloaded.ForeignKey)
}

func TestHasOne_ForceCreateMethodProperlyCreatesNewModel(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneSeedUser(t, query)

	// Laravel: $relation->forceCreate(['name' => 'taylor']) — the related model is force-created
	// with the attributes plus foreign_key = parent key (1), bypassing the mass-assignment guard.
	// The Go ORM keeps no guard, so the relation object's ForceCreate is the 1:1 alias of Create;
	// the oracle (v12.55.1, executed; /tmp/lalign_write_quiet_force.php) measured the same
	// saving/creating/created/saved events with the foreign key set.
	relation, ok := cloneRelationQuery(t, query).Related(&user, "Phone").(contractsorm.HasOneRelation)
	require.True(t, ok)

	phone := hasOnePhone{Name: "taylor"}
	require.NoError(t, relation.ForceCreate(&phone))

	assert.Equal(t, user.ID, phone.ForeignKey)
	assert.NotZero(t, phone.ID)

	var reloaded hasOnePhone
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOnePhone{}).Where("id", phone.ID).First(&reloaded))
	assert.Equal(t, "taylor", reloaded.Name)
	assert.Equal(t, user.ID, reloaded.ForeignKey)
}

func TestHasOne_RelationIsProperlyInitialized(t *testing.T) {
	// Laravel: initRelation([$model], 'foo') calls $model->setRelation('foo', null) on every
	// parent, so an unmatched eager load leaves the relation key defined.
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the mocked setRelation('foo', null) interaction on each parent — an Eloquent model-object call; a Go model's relation field is populated by Load/With and no initializer call is observable")
}

func TestHasOne_EagerConstraintsAreProperlyAdded(t *testing.T) {
	// Laravel: addEagerConstraints([$model1, $model2]) must constrain the relation query to the
	// parents' keys via whereIntegerInRaw('table.foreign_key', [1, 2]) (oracle: the real query
	// compiles to `select * from "phones" where "phones"."foreign_key" in (1, 2)`).
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the whereIntegerInRaw('table.foreign_key', [1, 2]) call on the mocked relation query — internal eager-load plumbing; the Go eager loader builds that constraint itself and exposes no hook to observe the call")
}

func TestHasOne_ModelsAreProperlyMatchedToParents(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	// Laravel builds four parents (ids 1-4) and three results (foreign keys 1, 2 and a stringable
	// object casting to '4'), calls $relation->match(...) and asserts parents 1, 2 and 4 receive
	// their result while parent 3 receives null. The Go eager loader is the same matcher behind
	// With — the public eager-load entry — so the port observes it through a real eager load over
	// the four parents and their three related rows.
	users := make([]hasOneUser, 4)
	for i := range users {
		users[i] = hasOneUser{ID: uint64(i + 1), Username: fmt.Sprintf("user%d", i+1)}
		require.NoError(t, cloneRelationQuery(t, query).Create(&users[i]))
	}

	phones := []hasOnePhone{
		{ForeignKey: users[0].ID, Name: "phone-1"},
		{ForeignKey: users[1].ID, Name: "phone-2"},
		{ForeignKey: users[3].ID, Name: "phone-4"},
	}
	for i := range phones {
		require.NoError(t, cloneRelationQuery(t, query).Create(&phones[i]))
	}

	found := []hasOneUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneUser{}).With("Phone").Get(&found))
	require.Len(t, found, 4)

	require.NotNil(t, found[0].Phone)
	assert.Equal(t, uint64(1), found[0].Phone.ForeignKey)
	assert.Equal(t, "phone-1", found[0].Phone.Name)

	require.NotNil(t, found[1].Phone)
	assert.Equal(t, uint64(2), found[1].Phone.ForeignKey)
	assert.Equal(t, "phone-2", found[1].Phone.Name)

	assert.Nil(t, found[2].Phone, "the parent without a result must be matched to null")

	require.NotNil(t, found[3].Phone)
	// Laravel additionally covers a stringable foreign key value ((string) fk === '4'); a typed
	// Go model reads the column as an integer, so the string cast itself has no counterpart.
	assert.Equal(t, uint64(4), found[3].Phone.ForeignKey)
	assert.Equal(t, "phone-4", found[3].Phone.Name)
}

func TestHasOne_RelationCountQueryCanBeBuilt(t *testing.T) {
	// Laravel: getRelationExistenceCountQuery($builder, $builder) selects count(*) via an
	// Expression, correlates the parent key with the foreign key through whereColumn, and resets
	// the select bindings (oracle: the real chain compiles to `select count(*) from "phones"
	// where "users"."id" = "phones"."foreign_key"`).
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the mocked builder chain getRelationExistenceCountQuery sets up (select count(*), whereColumn(parent key = foreign key), select-binding reset) — internal relation plumbing; the Go ORM builds count subqueries internally and exposes no such chain to observe")
}

// --- relation-object is() / isNot() ------------------------------------------

// The corpus's is() tests drive the comparison through mocked models: getRelation() builds the
// HasOne over parent key 1 and each model mock returns foreign_key plus its table (or 'table.two')
// and connection name (or 'connection.two'). The relation object Query.Related returns now carries
// the same comparison, so each test below asserts it for real — the model arguments are typed Go
// stubs declaring what the mocks returned, and the connection cases use a related class that
// declares 'connection' so the equality weighs a non-empty value (Eloquent's newRelatedInstance
// rule: the related side takes its own declaration, else the declaring model's).

// hasOneConnectedUser is the corpus's parent for the is() tests whose related mock declared
// connection 'connection'.
type hasOneConnectedUser struct {
	ID    uint64                `gorm:"column:id;primaryKey;autoIncrement"`
	Phone *hasOneConnectedPhone `gorm:"-"`
}

func (hasOneConnectedUser) TableName() string { return "users" }

func (hasOneConnectedUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Phone": contractsorm.HasOne{Related: &hasOneConnectedPhone{}, ForeignKey: "foreign_key", LocalKey: "id"},
	}
}

// hasOneConnectedPhone is the corpus's related mock: the phones table with connection
// 'connection'.
type hasOneConnectedPhone struct {
	ID         uint64 `gorm:"column:id;primaryKey"`
	ForeignKey uint64 `gorm:"column:foreign_key"`
}

func (hasOneConnectedPhone) TableName() string  { return "phones" }
func (hasOneConnectedPhone) Connection() string { return "connection" }

// hasOneStringKeyPhone shares the table under a text key — the corpus's string-'1' model key.
type hasOneStringKeyPhone struct {
	ID         uint64 `gorm:"column:id;primaryKey"`
	ForeignKey string `gorm:"column:foreign_key"`
}

func (hasOneStringKeyPhone) TableName() string  { return "phones" }
func (hasOneStringKeyPhone) Connection() string { return "connection" }

// hasOneOtherTablePhone shares the connection but not the table — the corpus's 'table.two' model.
type hasOneOtherTablePhone struct {
	ID         uint64 `gorm:"column:id;primaryKey"`
	ForeignKey uint64 `gorm:"column:foreign_key"`
}

func (hasOneOtherTablePhone) TableName() string  { return "phones.two" }
func (hasOneOtherTablePhone) Connection() string { return "connection" }

// hasOneOtherConnPhone shares the table but not the connection — the corpus's 'connection.two'
// model.
type hasOneOtherConnPhone struct {
	ID         uint64 `gorm:"column:id;primaryKey"`
	ForeignKey uint64 `gorm:"column:foreign_key"`
}

func (hasOneOtherConnPhone) TableName() string  { return "phones" }
func (hasOneOtherConnPhone) Connection() string { return "connection.two" }

func TestHasOne_IsNotNull(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: $relation->is(null) is false, without ever touching the related model's table or
	// connection (oracle: ho is(null) false).
	rel := cloneRelationQuery(t, query).Related(&hasOneConnectedUser{ID: 1}, "Phone").(contractsorm.HasOneRelation)
	assert.False(t, rel.Is(nil))
}

func TestHasOne_IsModel(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is true when the model's foreign_key (1) matches the parent key (1) and
	// the model carries the related model's table and connection name (oracle: ho conn is(conn fk
	// 1) true).
	rel := cloneRelationQuery(t, query).Related(&hasOneConnectedUser{ID: 1}, "Phone").(contractsorm.HasOneRelation)
	assert.True(t, rel.Is(&hasOneConnectedPhone{ForeignKey: 1}))
}

func TestHasOne_IsModelWithStringRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is true when the model's foreign_key is the string '1' — compareKeys
	// casts to int when either side is an int, so '1' matches the parent key 1 (oracle: ho is(fk
	// "1") true).
	rel := cloneRelationQuery(t, query).Related(&hasOneConnectedUser{ID: 1}, "Phone").(contractsorm.HasOneRelation)
	assert.True(t, rel.Is(&hasOneStringKeyPhone{ForeignKey: "1"}))
}

func TestHasOne_IsNotModelWithNullRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false (short-circuiting before table/connection checks) when the
	// model's foreign_key attribute is null. A value-typed Go key cannot hold NULL, so the zero
	// takes the empty-key reading compareKeys gives the PHP null (oracle: ho is(fk null) false).
	rel := cloneRelationQuery(t, query).Related(&hasOneConnectedUser{ID: 1}, "Phone").(contractsorm.HasOneRelation)
	assert.False(t, rel.Is(&hasOneConnectedPhone{}))
}

func TestHasOne_IsNotModelWithAnotherRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the model's foreign_key (2) differs from the parent key
	// (1) (oracle: ho is(fk 2) false).
	rel := cloneRelationQuery(t, query).Related(&hasOneConnectedUser{ID: 1}, "Phone").(contractsorm.HasOneRelation)
	assert.False(t, rel.Is(&hasOneConnectedPhone{ForeignKey: 2}))
}

func TestHasOne_IsNotModelWithAnotherTable(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the model's table ('table.two', the port's "phones.two")
	// differs from the related model's table, even though the foreign key matches (oracle:
	// ho is(other table fk 1) false).
	rel := cloneRelationQuery(t, query).Related(&hasOneConnectedUser{ID: 1}, "Phone").(contractsorm.HasOneRelation)
	assert.False(t, rel.Is(&hasOneOtherTablePhone{ForeignKey: 1}))
}

func TestHasOne_IsNotModelWithAnotherConnection(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: is($model) is false when the model's connection ('connection.two') differs from
	// the related model's connection ('connection'), even though key and table match (oracle:
	// ho is(other conn fk 1) false).
	rel := cloneRelationQuery(t, query).Related(&hasOneConnectedUser{ID: 1}, "Phone").(contractsorm.HasOneRelation)
	assert.False(t, rel.Is(&hasOneOtherConnPhone{ForeignKey: 1}))
}
