package has_many

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasManyTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestHasMany_<method without the test
// prefix>. The corpus is mock-driven — getRelation() mocks a HasMany over a parent with id = 1
// whose related table's foreign column is `foreign_key` — so the schema the setUp implies is
// realized here as users(id) -> items(foreign_key): parents are seeded with plain inserts, related
// rows are seeded through the relationship writer, and every read runs against the harness's
// sqlite database.
//
// The corpus looks values up with the mock attribute arrays ['foo'] / ['foo' => 'bar'] and assigns
// ['baz' => 'qux']; on a real table those keys are column names, so the port maps them onto real
// columns (attrs -> name, values -> label). The oracle (.claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1) measured what the arrays compile to: where(['foo']) renders
// `where ("0" = ?)` — a numeric-keyed equality that can match nothing — while where(['foo' => 'bar'])
// renders `where ("foo" = ?)`.
//
// Laravel loads relations implicitly through property access; Go has no property magic, so the
// eager-loading tests call the load entry point explicitly (query.With("Items").Get). Laravel's
// initRelation/match are only observable through that same pipeline here: a childless parent ends
// up with an initialized, non-nil empty slice (the counterpart of initRelation's empty Collection),
// and the corpus's direct-match assertNull cannot be reproduced through the public API.
//
// # Where our ORM diverges
//
//   - The find-then-write family now branches on firstRow's found flag (relationFirst), so the
//     not-found branches of FirstOrNew / FirstOrCreate / UpdateOrCreate run for real below. The
//     insert-first CreateOrFirst retry is staged with a real unique index whose violation the
//     seeded winner row trips, after which the write-PDO re-read returns it.
//   - The relation writer now has Make / MakeMany / CreateOrFirst / Upsert, and the relation
//     object has ForceCreate (HasOneOrMany::forceCreate — the same insert as Create except it
//     drops a relation's withAttributes pending pair, since no mass-assignment guard exists to
//     bypass), so the forceCreate test runs for real below.
//   - The eager-constraint tests assert the internal whereIntegerInRaw/whereIn call on the relation
//     query (oracle: inline `in (1, 2)` for the raw form versus bound `in (?, ?)` for whereIn); the
//     Go eager loader is internal and exposes no constraint-level observable — skipped as PHP-only
//     assertions.

// --- models ------------------------------------------------------------------

// hasManyUser ports the corpus's parent model: an auto-incrementing integer key, id = 1 in
// getRelation(). Items is the relation destination, kept out of the table by gorm:"-".
type hasManyUser struct {
	ID    uint64         `gorm:"column:id;primaryKey;autoIncrement"`
	Items []*hasManyItem `gorm:"-"`
}

func (hasManyUser) TableName() string { return "users" }

func (hasManyUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Items": contractsorm.HasMany{Related: &hasManyItem{}, ForeignKey: "foreign_key"},
	}
}

// hasManyItem ports EloquentHasManyModelStub — the related model whose foreign column the corpus
// names `foreign_key`.
type hasManyItem struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey uint64 `gorm:"column:foreign_key"`
	Name       string `gorm:"column:name"`
	Label      string `gorm:"column:label"`
}

func (hasManyItem) TableName() string { return "items" }

// --- schema and seeding ------------------------------------------------------

// hasManySchema mirrors the tables the corpus's mocks imply: a parent keyed by id and a related
// table carrying the foreign column `foreign_key`.
func hasManySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table items (id integer primary key autoincrement, foreign_key integer, name text, label text)`,
	)
}

// hasManySeedUser creates the parent row the corpus's getRelation() pretends exists (id 1 by
// default usage), writing the explicit id because users carries only the auto-increment column.
func hasManySeedUser(t *testing.T, query contractsorm.Query, id uint64) hasManyUser {
	t.Helper()

	user := hasManyUser{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&user).Create(map[string]any{"id": id}))

	return user
}

// hasManySeedItem creates a related row through the relation writer, the way Laravel's corpus
// seeds rows with $user->items()->create([...]).
func hasManySeedItem(t *testing.T, query contractsorm.Query, user *hasManyUser, item hasManyItem) hasManyItem {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Relation(user, "Items").Create(&item))

	return item
}

// hasManyAllItems reads every related row back, for the "nothing was written" assertions the
// corpus expresses by never expecting save()/newInstance on its mocks.
func hasManyAllItems(t *testing.T, query contractsorm.Query) []hasManyItem {
	t.Helper()

	items := []hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyItem{}).Get(&items))

	return items
}

// --- ports of the corpus -----------------------------------------------------

// Laravel: make() returns an unsaved new related instance with the parent key set on the
// foreign attribute; the corpus asserts save() is never called on it.

func TestHasMany_MakeMethodDoesNotSaveNewModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	// Laravel: make(['name' => 'taylor']) returns an unsaved instance with foreign_key = 1.
	item := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").Make(map[string]any{"name": "taylor"}, &item))

	assert.Equal(t, "taylor", item.Name)
	assert.Equal(t, uint64(1), item.ForeignKey)
	assert.Zero(t, item.ID, "make must not persist the instance")
	assert.Empty(t, hasManyAllItems(t, query), "make must not insert a row")
}

// Laravel: makeMany() returns a Collection of make()d, unsaved instances, one per record.

func TestHasMany_MakeManyCreatesARelatedModelForEachRecord(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	items := []hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").MakeMany(
		[]map[string]any{{"name": "taylor"}, {"name": "colin"}}, &items))

	require.Len(t, items, 2, "one unsaved instance per record, in record order")
	assert.Equal(t, "taylor", items[0].Name)
	assert.Equal(t, "colin", items[1].Name)
	assert.Equal(t, uint64(1), items[0].ForeignKey)
	assert.Equal(t, uint64(1), items[1].ForeignKey)
	assert.Zero(t, items[0].ID)
	assert.Zero(t, items[1].ID)
	assert.Empty(t, hasManyAllItems(t, query), "makeMany must not insert any row")
}

// Laravel: $relation->create(['name' => 'taylor']) returns the created model — the attributes
// plus the parent key on foreign_key — persisted.
func TestHasMany_CreateMethodProperlyCreatesNewModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	created := hasManyItem{Name: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").Create(&created))

	assert.NotZero(t, created.ID)
	assert.Equal(t, "taylor", created.Name)
	assert.Equal(t, uint64(1), created.ForeignKey)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, "taylor", rows[0].Name)
	assert.Equal(t, uint64(1), rows[0].ForeignKey)
}

// Laravel: forceCreate(['name' => 'taylor']) creates the model with the parent key on foreign_key,
// bypassing the mass-assignment guard; the corpus reads foreign_key back as 1.
func TestHasMany_ForceCreateMethodProperlyCreatesNewModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	// The Go ORM applies no guard, so the relation object's ForceCreate differs from Create only
	// in dropping a relation's withAttributes pending pair (HasOneOrMany.php:414-419; none is
	// recorded here); the oracle (v12.55.1, executed; /tmp/lalign_write_quiet_force.php)
	// measured the same events with the foreign key set.
	relation, ok := cloneRelationQuery(t, query).Related(&user, "Items").(contractsorm.HasManyRelation)
	require.True(t, ok)

	created := hasManyItem{Name: "taylor"}
	require.NoError(t, relation.ForceCreate(&created))

	assert.NotZero(t, created.ID)
	assert.Equal(t, "taylor", created.Name)
	assert.Equal(t, uint64(1), created.ForeignKey)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, "taylor", rows[0].Name)
	assert.Equal(t, uint64(1), rows[0].ForeignKey)
}

// Laravel: findOrNew('foo') lets find() hit and returns the found model untouched (setAttribute is
// never called on it).
func TestHasMany_FindOrNewMethodFindsModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	seeded := hasManySeedItem(t, query, &user, hasManyItem{Name: "taylor"})

	// The corpus looks up an arbitrary key value ('foo'); the port uses the seeded integer key of
	// its auto-increment id column.
	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FindOrNew(seeded.ID, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, uint64(1), dest.ForeignKey)

	assert.Len(t, hasManyAllItems(t, query), 1)
}

// Laravel: findOrNew('foo') misses and returns a new related instance with foreign_key set,
// leaving the table empty.
func TestHasMany_FindOrNewMethodReturnsNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FindOrNew(uint64(999), &dest))

	assert.Zero(t, dest.ID)
	assert.Equal(t, uint64(1), dest.ForeignKey)

	assert.Empty(t, hasManyAllItems(t, query))
}

// Laravel: firstOrNew(['foo']) runs where(attrs)->first(), hits, and returns the found model
// without touching it (newInstance and setAttribute are never called).
func TestHasMany_FirstOrNewMethodFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	seeded := hasManySeedItem(t, query, &user, hasManyItem{Name: "taylor"})

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrNew(map[string]any{"name": "taylor"}, nil, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, uint64(1), dest.ForeignKey)

	assert.Len(t, hasManyAllItems(t, query), 1)
}

// Laravel: firstOrNew(['foo' => 'bar'], ['baz' => 'qux']) hits and returns the found model as-is —
// the values are not filled and nothing is saved.
func TestHasMany_FirstOrNewMethodWithValuesFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	seeded := hasManySeedItem(t, query, &user, hasManyItem{Name: "taylor"})

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrNew(map[string]any{"name": "taylor"}, map[string]any{"label": "qux"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Empty(t, dest.Label, "the values are not applied to a found model")

	assert.Len(t, hasManyAllItems(t, query), 1)
}

// Laravel: firstOrNew(['foo']) misses and returns a new instance carrying the attributes with
// foreign_key = 1, unsaved.

func TestHasMany_FirstOrNewMethodReturnsNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrNew(map[string]any{"name": "taylor"}, nil, &dest))

	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, uint64(1), dest.ForeignKey)
	assert.Zero(t, dest.ID, "the new instance must not be persisted")
	assert.Empty(t, hasManyAllItems(t, query))
}

// Laravel: firstOrNew(['foo' => 'bar'], ['baz' => 'qux']) misses and returns a new instance
// carrying attrs+values with foreign_key = 1, unsaved.

func TestHasMany_FirstOrNewMethodWithValuesCreatesNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrNew(
		map[string]any{"name": "taylor"}, map[string]any{"label": "qux"}, &dest))

	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, "qux", dest.Label)
	assert.Equal(t, uint64(1), dest.ForeignKey)
	assert.Zero(t, dest.ID)
	assert.Empty(t, hasManyAllItems(t, query))
}

// Laravel: firstOrCreate(['foo']) runs where(attrs)->first(), hits, and returns the found model
// without a save.
func TestHasMany_FirstOrCreateMethodFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	seeded := hasManySeedItem(t, query, &user, hasManyItem{Name: "taylor"})

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrCreate(map[string]any{"name": "taylor"}, nil, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, uint64(1), dest.ForeignKey)

	assert.Len(t, hasManyAllItems(t, query), 1)
}

// Laravel: firstOrCreate(['foo' => 'bar'], ['baz' => 'qux']) hits and returns the found model
// as-is (newInstance and save are never called).
func TestHasMany_FirstOrCreateMethodWithValuesFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	seeded := hasManySeedItem(t, query, &user, hasManyItem{Name: "taylor"})

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrCreate(map[string]any{"name": "taylor"}, map[string]any{"label": "qux"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Empty(t, dest.Label, "the values are not applied to a found model")

	assert.Len(t, hasManyAllItems(t, query), 1)
}

// Laravel: firstOrCreate(['foo']) misses and creates the model with foreign_key = 1.

func TestHasMany_FirstOrCreateMethodCreatesNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrCreate(map[string]any{"name": "taylor"}, nil, &dest))

	require.NotZero(t, dest.ID, "the created row's key must be read back")
	assert.Equal(t, uint64(1), dest.ForeignKey)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, "taylor", rows[0].Name)
	assert.Equal(t, uint64(1), rows[0].ForeignKey)
}

// Laravel: firstOrCreate(['foo' => 'bar'], ['baz' => 'qux']) misses and creates the model with
// attrs+values and foreign_key = 1.

func TestHasMany_FirstOrCreateMethodWithValuesCreatesNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").FirstOrCreate(
		map[string]any{"name": "taylor"}, map[string]any{"label": "qux"}, &dest))

	require.NotZero(t, dest.ID)
	assert.Equal(t, "qux", dest.Label, "values are overlaid on the created instance")
	assert.Equal(t, uint64(1), dest.ForeignKey)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, "taylor", rows[0].Name)
	assert.Equal(t, "qux", rows[0].Label)
	assert.Equal(t, uint64(1), rows[0].ForeignKey)
}

// Laravel: createOrFirst(['foo' => 'bar'], ['baz' => 'qux']) attempts the create, the unique
// constraint throws, and the fallback where(attrs)->first() returns the existing model.

func TestHasMany_CreateOrFirstMethodWithValuesFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	// The insert-first attempt must trip a real unique constraint: a related row is unique per
	// (parent, name), and the seeded row is what the concurrent writer committed first.
	runRelationSchema(t, query, `create unique index items_foreign_key_name_unique on items (foreign_key, name)`)
	seeded := hasManySeedItem(t, query, &user, hasManyItem{Name: "taylor", Label: "bar"})

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").CreateOrFirst(
		map[string]any{"name": "taylor"}, map[string]any{"label": "qux"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID, "the insert lost the race and the write-PDO re-read returned the winner")
	assert.Equal(t, "bar", dest.Label, "the values live on the insert; the winning row comes back untouched")
	assert.Len(t, hasManyAllItems(t, query), 1)
}

// Laravel: createOrFirst(['foo']) creates the model inside withSavepointIfNeeded with foreign_key
// set and never runs where/first.

func TestHasMany_CreateOrFirstMethodCreatesNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	// Laravel: createOrFirst(['foo']) inserts immediately — where/first are never called — and the
	// created model carries foreign_key = 1.
	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").CreateOrFirst(map[string]any{"name": "taylor"}, nil, &dest))

	require.NotZero(t, dest.ID, "a successful insert returns the created model")
	assert.Equal(t, uint64(1), dest.ForeignKey)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, "taylor", rows[0].Name)
	assert.Equal(t, uint64(1), rows[0].ForeignKey)
}

// Laravel: createOrFirst(['foo' => 'bar'], ['baz' => 'qux']) creates the model with attrs+values
// and foreign_key set, without a prior lookup.

func TestHasMany_CreateOrFirstMethodWithValuesCreatesNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").CreateOrFirst(
		map[string]any{"name": "taylor"}, map[string]any{"label": "qux"}, &dest))

	require.NotZero(t, dest.ID)
	assert.Equal(t, "qux", dest.Label, "values are part of the insert")
	assert.Equal(t, uint64(1), dest.ForeignKey)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, "taylor", rows[0].Name)
	assert.Equal(t, "qux", rows[0].Label)
}

// Laravel: updateOrCreate(['foo'], ['bar']) finds the model, fills the values onto it and saves —
// one row, updated in place.
func TestHasMany_UpdateOrCreateMethodFindsFirstModelAndUpdates(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	seeded := hasManySeedItem(t, query, &user, hasManyItem{Name: "taylor"})

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").UpdateOrCreate(map[string]any{"name": "taylor"}, map[string]any{"label": "bar"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "bar", dest.Label)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, seeded.ID, rows[0].ID)
	assert.Equal(t, "bar", rows[0].Label)
}

// Laravel: updateOrCreate(['foo'], ['bar']) misses and creates a model (wasRecentlyCreated) with
// attrs+values and foreign_key = 1.

func TestHasMany_UpdateOrCreateMethodCreatesNewModelWithForeignKeySet(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	dest := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").UpdateOrCreate(
		map[string]any{"name": "taylor"}, map[string]any{"label": "bar"}, &dest))

	require.NotZero(t, dest.ID, "the created row's key must be read back")
	assert.Equal(t, "bar", dest.Label)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 1)
	assert.Equal(t, "taylor", rows[0].Name)
	assert.Equal(t, "bar", rows[0].Label)
	assert.Equal(t, uint64(1), rows[0].ForeignKey)
}

// Laravel: the relation's upsert() fills the parent key into every row's foreign-key column
// before delegating to the query's upsert.

func TestHasMany_RelationUpsertFillsForeignKey(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)
	runRelationSchema(t, query, `create unique index items_name_unique on items (name)`)

	// Laravel's first call is the single-row form — attrs email/name become the port's name/label.
	_, err := cloneRelationQuery(t, query).Relation(&user, "Items").Upsert(
		[]map[string]any{{"name": "foo3", "label": "bar"}}, []string{"name"}, []string{"label"})
	require.NoError(t, err)

	stored := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Where("name", "foo3").First(&stored))
	assert.Equal(t, "bar", stored.Label)
	assert.Equal(t, uint64(1), stored.ForeignKey, "upsert must fill the parent key into every row")

	// Second call: an existing row (updated in place) plus a new one, every row carrying the key.
	_, err = cloneRelationQuery(t, query).Relation(&user, "Items").Upsert(
		[]map[string]any{{"name": "foo3", "label": "bar"}, {"label": "bar2", "name": "foo2"}},
		[]string{"name"}, []string{"label"})
	require.NoError(t, err)

	stored = hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Where("name", "foo3").First(&stored))
	assert.Equal(t, "bar", stored.Label)
	assert.Equal(t, uint64(1), stored.ForeignKey)

	second := hasManyItem{}
	require.NoError(t, cloneRelationQuery(t, query).Where("name", "foo2").First(&second))
	assert.Equal(t, "bar2", second.Label)
	assert.Equal(t, uint64(1), second.ForeignKey)
	assert.Len(t, hasManyAllItems(t, query), 2)
}

// Laravel: initRelation([$model], 'foo') calls setRelation('foo', new Collection) on every model,
// initializing the relation to an empty collection, and returns the models.
func TestHasMany_RelationIsProperlyInitialized(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	hasManySeedUser(t, query, 1)

	found := []hasManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyUser{}).With("Items").Get(&found))
	require.Len(t, found, 1)

	// The eager pipeline's counterpart observable: a parent whose eager result set is empty still
	// carries an initialized relation — a non-nil, empty slice.
	assert.NotNil(t, found[0].Items)
	assert.Empty(t, found[0].Items)
}

// Laravel: addEagerConstraints([$model1, $model2]) adds whereIntegerInRaw('table.foreign_key',
// [1, 2]) to the relation query for an integer local key.
func TestHasMany_EagerConstraintsAreProperlyAdded(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the relation's internal addEagerConstraints routes an integer local key through whereIntegerInRaw('table.foreign_key', [1, 2]) on the relation query (oracle: that compiles inline — `where \"foreign_key\" in (1, 2)` — versus whereIn's bound `in (?, ?)`); the Go eager loader is internal and exposes no constraint-level observable")
}

// Laravel: addEagerConstraints([$model1, $model2]) adds whereIn('table.foreign_key', [1, 2]) to
// the relation query for a string local key.
func TestHasMany_EagerConstraintsAreProperlyAddedWithStringKey(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the relation's internal addEagerConstraints routes a string local key through whereIn('table.foreign_key', [1, 2]) on the relation query (oracle: whereIntegerInRaw for an integer key compiles inline `in (1, 2)` while whereIn binds `in (?, ?)`); the Go eager loader is internal and exposes no constraint-level observable")
}

// Laravel: match() groups the results onto their parents by foreign key — one result for parent 1,
// both results for parent 2 — and leaves parent 3, which has none, with a null relation.
func TestHasMany_ModelsAreProperlyMatchedToParents(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)

	user1 := hasManySeedUser(t, query, 1)
	user2 := hasManySeedUser(t, query, 2)
	hasManySeedUser(t, query, 3)

	result1 := hasManySeedItem(t, query, &user1, hasManyItem{Name: "one"})
	result2 := hasManySeedItem(t, query, &user2, hasManyItem{Name: "two"})
	result3 := hasManySeedItem(t, query, &user2, hasManyItem{Name: "three"})

	found := []hasManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyUser{}).With("Items").Get(&found))
	require.Len(t, found, 3)

	require.Len(t, found[0].Items, 1)
	assert.Equal(t, result1.ID, found[0].Items[0].ID)
	assert.Equal(t, uint64(1), found[0].Items[0].ForeignKey)

	require.Len(t, found[1].Items, 2)
	assert.Equal(t, result2.ID, found[1].Items[0].ID)
	assert.Equal(t, result3.ID, found[1].Items[1].ID)
	assert.Equal(t, uint64(2), found[1].Items[0].ForeignKey)
	assert.Equal(t, uint64(2), found[1].Items[1].ForeignKey)

	// Laravel asserts assertNull($models[2]->foo): match() called directly, without initRelation,
	// leaves an unmatched parent's relation unset. Through the real eager pipeline Laravel
	// initializes the relation to an empty collection, which the port observes as a set-but-empty
	// slice.
	assert.Empty(t, found[2].Items)
}

// Laravel: createMany(records) creates each record through the relation and returns the created
// models in record order, each with the parent key on foreign_key.
func TestHasMany_CreateManyCreatesARelatedModelForEachRecord(t *testing.T) {
	query := newRelationQuery(t)
	hasManySchema(t, query)
	user := hasManySeedUser(t, query, 1)

	instances := []hasManyItem{{Name: "taylor"}, {Name: "colin"}}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Items").CreateMany(&instances))

	require.Len(t, instances, 2)
	assert.NotZero(t, instances[0].ID)
	assert.Equal(t, "taylor", instances[0].Name)
	assert.Equal(t, uint64(1), instances[0].ForeignKey)
	assert.NotZero(t, instances[1].ID)
	assert.Equal(t, "colin", instances[1].Name)
	assert.Equal(t, uint64(1), instances[1].ForeignKey)

	rows := hasManyAllItems(t, query)
	require.Len(t, rows, 2)
	assert.Equal(t, instances[0].ID, rows[0].ID)
	assert.Equal(t, instances[1].ID, rows[1].ID)
}
