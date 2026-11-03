package morph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// This file ports Laravel's tests/Database/DatabaseEloquentMorphTest.php (12.x) onto the framework
// ORM: one Go test per Laravel test method, named TestMorph_<method without the test prefix>.
//
// The corpus is entirely mock-based: it extends PHPUnit's TestCase and never creates a schema. Its
// relation shapes are a MorphOne / MorphMany whose related table carries morph_id / morph_type
// columns, so the port gives those mocks a real schema (parents in `users` and `posts`, related
// rows in `morph_items` with morph_id / morph_type / name / email / foo / baz) and asserts the same
// observable outcomes against a real sqlite database: the rows a morph relation read returns, the
// rows each morph-parent type may see (the type column is the discriminator), and the foreign-key +
// morph columns a create / findOrNew / firstOrNew / firstOrCreate / updateOrCreate write sets on
// the model and the row. Relations are read explicitly (Related / With) because Go has no
// property-access magic.
//
// Every SQL expectation cited below was measured with the oracle (.claude/laravel-oracle/oracle.php
// against illuminate/database v12.55.1), never recalled. Where the Go RelationWriter has no
// counterpart API used to diverge — upsert without the relation, make, createOrFirst and the
// not-found branch of firstOrNew/firstOrCreate were absent — those flows now run for real below
// (the insert-first retry is staged with a real unique index and a seeded winner row). The seven
// relation is()/isNot() tests now assert ComparesRelatedModels' comparison on the relation object
// for real, so no test in this file is skipped any more.
//
// One SQL-shape divergence, probed and documented rather than skipped: our morph read path renders
// `where "morph_id" = ? and "morph_type" = ?`, one predicate fewer than Laravel's
// `where "table"."morph_type" = ? and "table"."morph_id" = ? and "table"."morph_id" is not null`
// (oracle). The missing whereNotNull(foreign key) is behaviourally invisible: a row whose morph_id
// is NULL satisfies neither `morph_id = ?` nor the explicit not-null filter, so the constraints
// tests below observe the same result set either way.
//
// A second divergence has been repaired in relation_writes.go: the find-then-write family now
// branches on firstRow's found flag (relationFirst), so a miss overlays the attributes and the
// values, sets the morph keys and creates — the not-found tests below assert that flow against the
// real database instead of a zeroed destination.

// The corpus's find-criteria shapes (['foo']) compile to the placeholder-free `where ("0" = ?)`
// (oracle) — a mock-era artefact with no meaningful column. The find-then-write ports below use a
// real criterion column `foo` instead, keeping the same find-then-new / find-then-update contract.

// --- models ------------------------------------------------------------------

// morphUser ports the corpus's EloquentMorphResetModelStub as a real parent model (table `users`,
// the port's name for the mocked parent of testMorphOneSetsProperConstraints). The two relation
// fields hold explicitly loaded results and stay out of the table via gorm:"-".
type morphUser struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`

	MorphOne  *morphItem   `gorm:"-"`
	MorphMany []*morphItem `gorm:"-"`
}

func (morphUser) TableName() string { return "users" }

func (morphUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// The corpus builds MorphOne($builder, $parent, 'table.morph_type', 'table.morph_id',
		// 'id'); Name "morph" derives exactly those morph_type / morph_id columns.
		"MorphOne":  contractsorm.MorphOne{Related: &morphItem{}, Name: "morph"},
		"MorphMany": contractsorm.MorphMany{Related: &morphItem{}, Name: "morph"},
	}
}

// morphPost is the second parent type the discriminator stands against: a users row and a posts
// row can share the same morph_id, and only the morph_type column separates their related rows.
type morphPost struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

func (morphPost) TableName() string { return "posts" }

func (morphPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"MorphOne":  contractsorm.MorphOne{Related: &morphItem{}, Name: "morph"},
		"MorphMany": contractsorm.MorphMany{Related: &morphItem{}, Name: "morph"},
	}
}

// morphItem ports the related mock: the corpus's "table" holding morph_id / morph_type plus the
// attribute columns its create / firstOrNew / updateOrCreate calls touch.
type morphItem struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	MorphID   uint64 `gorm:"column:morph_id"`
	MorphType string `gorm:"column:morph_type"`
	Name      string `gorm:"column:name"`
	Email     string `gorm:"column:email"`
	Foo       string `gorm:"column:foo"`
	Baz       string `gorm:"column:baz"`
}

func (morphItem) TableName() string { return "morph_items" }

// --- schema and seeding ------------------------------------------------------

// morphSchema mirrors the schema the corpus's mocks imply: an id-only parent table per morph type
// and one related table carrying the morph columns. morph_id is nullable so a parentless row can
// exercise the not-null side of the constraints.
func morphSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table posts (id integer primary key autoincrement)`,
		`create table morph_items (id integer primary key autoincrement, morph_id integer null, morph_type varchar null, name varchar null, email varchar null, foo varchar null, baz varchar null)`,
	)
}

// morphSeedParent creates an id-only parent row. The corpus's parents are mocked with only
// getAttribute('id') set; the explicit id is written because an all-defaults struct insert
// compiles without columns and sqlite rejects it.
func morphSeedParent(t *testing.T, query contractsorm.Query, model any, id uint64) {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Model(model).Create(map[string]any{"id": id}))
}

// morphCreateRelated ports $parent->relation()->create(...): the framework pre-sets the foreign
// key (and the morph type) on the child, then inserts it.
func morphCreateRelated(t *testing.T, query contractsorm.Query, parent any, relation string, item *morphItem) {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Relation(parent, relation).Create(item))
}

// morphReloadItem reads a morph_items row back through a plain query, outside any relation scope.
func morphReloadItem(t *testing.T, query contractsorm.Query, id uint64) morphItem {
	t.Helper()

	item := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphItem{}).Where("id", id).First(&item))

	return item
}

// morphItemCount counts every morph_items row, to prove a find-then-write did or did not insert.
func morphItemCount(t *testing.T, query contractsorm.Query) int64 {
	t.Helper()

	count, err := cloneRelationQuery(t, query).Model(&morphItem{}).Count()
	require.NoError(t, err)

	return count
}

// --- ports of the corpus -----------------------------------------------------

// Laravel: getOneRelation() constructs MorphOne($builder, $parent, 'table.morph_type',
// 'table.morph_id', 'id') and the constructor's constraints are asserted through the mock query —
// where('table.morph_type', get_class($parent)), where('table.morph_id', '=', 1) and
// whereNotNull('table.morph_id'). Oracle: the constructed relation renders
//
//	select * from "table" where "table"."morph_type" = ? and "table"."morph_id" = ? and
//	"table"."morph_id" is not null        bindings: [parent class, 1]
//
// The port runs the real constraints: a read through the relation returns exactly the parent's own
// rows — not the other morph type's rows that share the parent key, not another parent's rows, and
// not the parentless row.
func TestMorph_MorphOneSetsProperConstraints(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)

	morphSeedParent(t, query, &morphUser{}, 1)
	morphSeedParent(t, query, &morphPost{}, 1)
	morphSeedParent(t, query, &morphUser{}, 2)

	user := morphUser{ID: 1}
	post := morphPost{ID: 1}
	otherUser := morphUser{ID: 2}

	mine := morphItem{Name: "mine"}
	morphCreateRelated(t, query, &user, "MorphOne", &mine)

	// Same morph_id (1), another morph type: only the type column separates the parents.
	decoy := morphItem{Name: "post's"}
	morphCreateRelated(t, query, &post, "MorphOne", &decoy)

	// Same morph type, another parent key.
	other := morphItem{Name: "other user's"}
	morphCreateRelated(t, query, &otherUser, "MorphOne", &other)

	// A row with no parent: `morph_id = ?` and Laravel's whereNotNull both exclude it.
	_, err := cloneRelationQuery(t, query).Exec("insert into morph_items (name) values ('orphan')")
	require.NoError(t, err)

	items := []morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "MorphOne").Get(&items))

	require.Len(t, items, 1)
	assert.Equal(t, mine.ID, items[0].ID)
	assert.Equal(t, uint64(1), items[0].MorphID)
	assert.Equal(t, "users", items[0].MorphType)
}

// Laravel: addEagerConstraints([$model1, $model2]) must add the key-id constraint — whereIn for a
// string parent key type / whereIntegerInRaw for the int default — plus
// where('table.morph_type', class). Oracle (string key type):
//
//	select * from "table" where "table"."morph_type" = ? and "table"."morph_id" = ? and
//	"table"."morph_id" is not null and "table"."morph_id" in (?, ?) and "table"."morph_type" = ?
//	bindings: [Class, "1", 1, 2, Class]
//
// Oracle (int key type): the same statement with the ids inlined — `and "table"."morph_id" in
// (1, 2) and "table"."morph_type" = ?`, bindings [Class, 1, Class]. The port eager-loads two
// parents and observes each parent's own row; the same-morph_id row of the other morph type never
// joins another parent's result.
func TestMorph_MorphOneEagerConstraintsAreProperlyAdded(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)

	morphSeedParent(t, query, &morphUser{}, 1)
	morphSeedParent(t, query, &morphUser{}, 2)
	morphSeedParent(t, query, &morphPost{}, 1)

	user1 := morphUser{ID: 1}
	user2 := morphUser{ID: 2}
	post := morphPost{ID: 1}

	first := morphItem{Name: "first"}
	morphCreateRelated(t, query, &user1, "MorphOne", &first)
	second := morphItem{Name: "second"}
	morphCreateRelated(t, query, &user2, "MorphOne", &second)
	decoy := morphItem{Name: "post's"}
	morphCreateRelated(t, query, &post, "MorphOne", &decoy)

	found := []morphUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphUser{}).With("MorphOne").Get(&found))
	require.Len(t, found, 2)

	require.NotNil(t, found[0].MorphOne)
	require.NotNil(t, found[1].MorphOne)
	assert.Equal(t, first.ID, found[0].MorphOne.ID)
	assert.Equal(t, "users", found[0].MorphOne.MorphType)
	assert.Equal(t, second.ID, found[1].MorphOne.ID)
	assert.Equal(t, "users", found[1].MorphOne.MorphType)
}

// Laravel: getManyRelation() — same construction as MorphOne with the MorphMany class. Oracle:
// identical SQL and bindings to MorphOne's constraints. The port reads through MorphMany and
// observes the parent's rows only.
func TestMorph_MorphManySetsProperConstraints(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)

	morphSeedParent(t, query, &morphUser{}, 1)
	morphSeedParent(t, query, &morphPost{}, 1)
	morphSeedParent(t, query, &morphUser{}, 2)

	user := morphUser{ID: 1}
	post := morphPost{ID: 1}
	otherUser := morphUser{ID: 2}

	first := morphItem{Name: "first"}
	morphCreateRelated(t, query, &user, "MorphMany", &first)
	second := morphItem{Name: "second"}
	morphCreateRelated(t, query, &user, "MorphMany", &second)

	decoy := morphItem{Name: "post's"}
	morphCreateRelated(t, query, &post, "MorphMany", &decoy)
	other := morphItem{Name: "other user's"}
	morphCreateRelated(t, query, &otherUser, "MorphMany", &other)

	items := []morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "MorphMany").Get(&items))

	require.Len(t, items, 2)
	assert.Equal(t, first.ID, items[0].ID)
	assert.Equal(t, second.ID, items[1].ID)
	assert.Equal(t, uint64(1), items[0].MorphID)
	assert.Equal(t, "users", items[0].MorphType)
}

// Laravel: addEagerConstraints for MorphMany, asserting whereIntegerInRaw('table.morph_id', [1, 2])
// for the int parent key plus where('table.morph_type', class). Oracle:
//
//	select * from "table" where "table"."morph_type" = ? and "table"."morph_id" = ? and
//	"table"."morph_id" is not null and "table"."morph_id" in (1, 2) and "table"."morph_type" = ?
//	bindings: [Class, 1, Class]
func TestMorph_MorphManyEagerConstraintsAreProperlyAdded(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)

	morphSeedParent(t, query, &morphUser{}, 1)
	morphSeedParent(t, query, &morphUser{}, 2)
	morphSeedParent(t, query, &morphPost{}, 1)

	user1 := morphUser{ID: 1}
	user2 := morphUser{ID: 2}
	post := morphPost{ID: 1}

	a := morphItem{Name: "a"}
	b := morphItem{Name: "b"}
	morphCreateRelated(t, query, &user1, "MorphMany", &a)
	morphCreateRelated(t, query, &user1, "MorphMany", &b)
	c := morphItem{Name: "c"}
	morphCreateRelated(t, query, &user2, "MorphMany", &c)
	// A posts parent with the same key as user 1: its row may not leak into user 1's result.
	decoy := morphItem{Name: "decoy"}
	morphCreateRelated(t, query, &post, "MorphMany", &decoy)

	found := []morphUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphUser{}).With("MorphMany").Get(&found))
	require.Len(t, found, 2)

	require.Len(t, found[0].MorphMany, 2)
	require.Len(t, found[1].MorphMany, 1)
	assert.Equal(t, a.ID, found[0].MorphMany[0].ID)
	assert.Equal(t, b.ID, found[0].MorphMany[1].ID)
	assert.Equal(t, c.ID, found[1].MorphMany[0].ID)
}

// Laravel: $relation->upsert([...], ...) must hand the query rows that each carry the foreign key
// and the morph type — MorphOneOrMany::upsert merges morph_type into every row and
// HasOneOrMany::upsert merges the foreign key, before the query-level upsert.

func TestMorph_MorphRelationUpsertFillsForeignKey(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)
	// email is the corpus's uniqueBy column; a real unique index backs the on-conflict claim.
	runRelationSchema(t, query, `create unique index morph_items_email_unique on morph_items (email)`)

	user := morphUser{ID: 1}
	// First call: the corpus's single-row form — one row, updated in place by email.
	_, err := cloneRelationQuery(t, query).Relation(&user, "MorphOne").Upsert(
		[]map[string]any{{"email": "foo3", "name": "bar"}}, []string{"email"}, []string{"name"})
	require.NoError(t, err)

	stored := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphItem{}).Where("email", "foo3").First(&stored))
	assert.Equal(t, "bar", stored.Name)
	assert.Equal(t, uint64(1), stored.MorphID, "upsert must fill the parent key into every row")
	assert.Equal(t, "users", stored.MorphType, "upsert must fill the morph type into every row")

	// Second call: two rows — the existing email updated in place, a new email inserted — each
	// carrying the same foreign key and morph type.
	_, err = cloneRelationQuery(t, query).Relation(&user, "MorphOne").Upsert(
		[]map[string]any{{"email": "foo3", "name": "bar"}, {"name": "bar2", "email": "foo2"}},
		[]string{"email"}, []string{"name"})
	require.NoError(t, err)

	stored = morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphItem{}).Where("email", "foo3").First(&stored))
	assert.Equal(t, "bar", stored.Name)

	second := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphItem{}).Where("email", "foo2").First(&second))
	assert.Equal(t, "bar2", second.Name)
	assert.Equal(t, uint64(1), second.MorphID)
	assert.Equal(t, "users", second.MorphType)
	assert.Equal(t, int64(2), morphItemCount(t, query))
}

// Laravel: make(['name' => 'taylor']) must return an unsaved instance with morph_id 1 and
// morph_type set (and never save).

func TestMorph_MakeFunctionOnMorph(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		Make(map[string]any{"name": "taylor"}, &dest))

	assert.Equal(t, "taylor", dest.Name)
	assert.Zero(t, dest.ID, "make must not persist the instance")
	assert.Equal(t, uint64(1), dest.MorphID, "the morph foreign key is pre-set from the parent")
	assert.Equal(t, "users", dest.MorphType, "the morph type is pre-set from the parent")
	assert.Equal(t, int64(0), morphItemCount(t, query))
}

// Laravel: create(['name' => 'taylor']) must set morph_id 1 and morph_type on the created model and
// save once. The port observes the created model's morph columns in memory and the persisted row.
func TestMorph_CreateFunctionOnMorph(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	item := morphItem{Name: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").Create(&item))

	require.NotZero(t, item.ID, "Create must read the generated key back")
	assert.Equal(t, uint64(1), item.MorphID)
	assert.Equal(t, "users", item.MorphType)

	reloaded := morphReloadItem(t, query, item.ID)
	assert.Equal(t, "taylor", reloaded.Name)
	assert.Equal(t, uint64(1), reloaded.MorphID)
	assert.Equal(t, "users", reloaded.MorphType)
}

// Laravel: findOrNew('foo') delegates to the relation-scoped find; a found model is returned
// untouched — newInstance never runs and nothing is saved. The corpus's string 'foo' key becomes
// the seeded row's real id in the port.
func TestMorph_FindOrNewMethodFindsModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	seeded := morphItem{Name: "taylor"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").FindOrNew(seeded.ID, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, int64(1), morphItemCount(t, query), "finding must not insert a row")
}

// Laravel: findOrNew('foo') with no matching row must return a new model carrying morph_id 1 and
// morph_type — and never save it.
func TestMorph_FindOrNewMethodReturnsNewModelWithMorphKeysSet(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").FindOrNew(uint64(123), &dest))

	assert.Zero(t, dest.ID, "the new model must not be persisted")
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)
	assert.Equal(t, int64(0), morphItemCount(t, query))
}

// Laravel: firstOrNew(['foo']) runs where($attributes)->first() and returns the found model
// untouched. The corpus's ['foo'] list compiles to the placeholder-free `where ("0" = ?)` (oracle),
// a mock-era artefact; the port passes a real criterion, {"foo": "bar"}, on the same
// find-then-new contract.
func TestMorph_FirstOrNewMethodFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	seeded := morphItem{Name: "taylor", Foo: "bar"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrNew(map[string]any{"foo": "bar"}, nil, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, int64(1), morphItemCount(t, query), "finding must not insert a row")
}

// Laravel: firstOrNew(['foo' => 'bar'], ['baz' => 'qux']) — the values are only used when the model
// must be created, so a found model keeps its stored attributes.
func TestMorph_FirstOrNewMethodWithValueFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	seeded := morphItem{Name: "taylor", Foo: "bar"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrNew(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "", dest.Baz, "values are only overlaid on a newly-created instance")
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: firstOrNew(['foo']) with no matching row must return a new instance carrying the
// attributes and morph keys — and never save it. The repaired not-found branch detects the miss
// through firstRow's found flag, overlays the attributes, and pre-sets the morph keys on the
// unsaved instance.

func TestMorph_FirstOrNewMethodReturnsNewModelWithMorphKeysSet(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrNew(map[string]any{"foo": "bar"}, nil, &dest))

	assert.Zero(t, dest.ID, "the new instance must not be persisted")
	assert.Equal(t, "bar", dest.Foo)
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)
	assert.Equal(t, int64(0), morphItemCount(t, query))
}

// Laravel: firstOrNew(['foo' => 'bar'], ['baz' => 'qux']) with no matching row must return a new
// instance carrying attributes plus values and the morph keys — and never save it. The same
// repaired not-found branch as the case above: the values overlay the attributes on the new
// instance.

func TestMorph_FirstOrNewMethodWithValuesReturnsNewModelWithMorphKeysSet(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrNew(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	assert.Zero(t, dest.ID, "the new instance must not be persisted")
	assert.Equal(t, "bar", dest.Foo)
	assert.Equal(t, "qux", dest.Baz)
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)
	assert.Equal(t, int64(0), morphItemCount(t, query))
}

// Laravel: firstOrCreate(['foo']) finds the matching row and creates nothing.
func TestMorph_FirstOrCreateMethodFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	seeded := morphItem{Name: "taylor", Foo: "bar"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrCreate(map[string]any{"foo": "bar"}, nil, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, int64(1), morphItemCount(t, query), "finding must not insert a row")
}

// Laravel: firstOrCreate(['foo' => 'bar'], ['baz' => 'qux']) finds the matching row (values unused).
func TestMorph_FirstOrCreateMethodWithValuesFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	seeded := morphItem{Name: "taylor", Foo: "bar"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrCreate(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "", dest.Baz, "values are only overlaid when the model must be created")
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: firstOrCreate(['foo']) with no match creates the row with the attributes, morph_id 1 and
// morph_type set. The repaired not-found branch routes the miss through createOrFirst: the insert
// carries the overlay and the pre-set morph keys, and the created key is read back.

func TestMorph_FirstOrCreateMethodCreatesNewMorphModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrCreate(map[string]any{"foo": "bar"}, nil, &dest))

	require.NotZero(t, dest.ID, "the created row's key must be read back")
	assert.Equal(t, "bar", dest.Foo)
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)

	reloaded := morphReloadItem(t, query, dest.ID)
	assert.Equal(t, "bar", reloaded.Foo)
	assert.Equal(t, uint64(1), reloaded.MorphID)
	assert.Equal(t, "users", reloaded.MorphType)
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: firstOrCreate(['foo' => 'bar'], ['baz' => 'qux']) with no match creates the row with
// attributes plus values and the morph keys. The same repaired not-found branch as the case above:
// the values ride the insert.

func TestMorph_FirstOrCreateMethodWithValuesCreatesNewMorphModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		FirstOrCreate(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	require.NotZero(t, dest.ID)
	assert.Equal(t, "bar", dest.Foo)
	assert.Equal(t, "qux", dest.Baz, "values are overlaid on the created instance")
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)

	reloaded := morphReloadItem(t, query, dest.ID)
	assert.Equal(t, "bar", reloaded.Foo)
	assert.Equal(t, "qux", reloaded.Baz)
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: createOrFirst(['foo']) attempts the insert first, catches the unique-key violation and
// re-queries with useWritePdo — the model the second read finds is returned.

func TestMorph_CreateOrFirstMethodFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)
	// The insert-first attempt must trip a real unique constraint: (morph_type, morph_id, foo) is
	// unique for a morph parent, and the seeded row is what a concurrent writer committed first.
	runRelationSchema(t, query, `create unique index morph_items_parent_foo_unique on morph_items (morph_type, morph_id, foo)`)

	user := morphUser{ID: 1}
	seeded := morphItem{Foo: "bar", Name: "taylor"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		CreateOrFirst(map[string]any{"foo": "bar"}, nil, &dest))

	assert.Equal(t, seeded.ID, dest.ID, "the insert lost the race and the write-PDO re-read returned the winner")
	assert.Equal(t, "taylor", dest.Name)
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: createOrFirst(['foo' => 'bar'], ['baz' => 'qux']) — same retry path with attrs + values.

func TestMorph_CreateOrFirstMethodWithValuesFindsFirstModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)
	runRelationSchema(t, query, `create unique index morph_items_parent_foo_unique on morph_items (morph_type, morph_id, foo)`)

	user := morphUser{ID: 1}
	seeded := morphItem{Foo: "bar", Name: "taylor"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		CreateOrFirst(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID, "the values live on the insert; the retry re-read returns the stored row")
	assert.Equal(t, "", dest.Baz, "the winning row is returned untouched")
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: createOrFirst(['foo']) with a successful insert returns the created model (no re-read).

func TestMorph_CreateOrFirstMethodCreatesNewMorphModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		CreateOrFirst(map[string]any{"foo": "bar"}, nil, &dest))

	require.NotZero(t, dest.ID, "a successful insert returns the created model")
	assert.Equal(t, "bar", dest.Foo)
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)

	reloaded := morphReloadItem(t, query, dest.ID)
	assert.Equal(t, "bar", reloaded.Foo)
	assert.Equal(t, uint64(1), reloaded.MorphID)
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: createOrFirst(['foo' => 'bar'], ['baz' => 'qux']) with a successful insert returns the
// created model (no re-read).

func TestMorph_CreateOrFirstMethodWithValuesCreatesNewMorphModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		CreateOrFirst(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	require.NotZero(t, dest.ID)
	assert.Equal(t, "bar", dest.Foo)
	assert.Equal(t, "qux", dest.Baz, "values are part of the insert")
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)

	reloaded := morphReloadItem(t, query, dest.ID)
	assert.Equal(t, "qux", reloaded.Baz)
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: updateOrCreate(['foo'], ['bar']) fills the found model with the values and saves it. The
// find criteria translate as in firstOrNew (real `foo` column).
func TestMorph_UpdateOrCreateMethodFindsFirstModelAndUpdates(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	seeded := morphItem{Name: "taylor", Foo: "bar"}
	morphCreateRelated(t, query, &user, "MorphOne", &seeded)

	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		UpdateOrCreate(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	assert.Equal(t, seeded.ID, dest.ID)
	assert.Equal(t, "qux", dest.Baz)
	assert.Equal(t, "taylor", dest.Name, "only the values overlay the found row")
	assert.Equal(t, int64(1), morphItemCount(t, query), "updating must not insert a row")

	reloaded := morphReloadItem(t, query, seeded.ID)
	assert.Equal(t, "qux", reloaded.Baz)
	assert.Equal(t, "taylor", reloaded.Name)
}

// Laravel: updateOrCreate(['foo'], ['bar']) with no match creates the row with the attributes,
// values, morph_id 1 and morph_type set. The repaired lookup-miss path routes through createOrFirst
// with the full overlay, so the created row carries foo 'bar', baz 'qux', morph_id 1 and the morph
// type.

func TestMorph_UpdateOrCreateMethodCreatesNewMorphModel(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	user := morphUser{ID: 1}
	dest := morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").
		UpdateOrCreate(map[string]any{"foo": "bar"}, map[string]any{"baz": "qux"}, &dest))

	require.NotZero(t, dest.ID, "the created row's key must be read back")
	assert.Equal(t, "bar", dest.Foo, "the attrs are part of the created row")
	assert.Equal(t, "qux", dest.Baz, "the values are part of the created row")
	assert.Equal(t, uint64(1), dest.MorphID)
	assert.Equal(t, "users", dest.MorphType)

	reloaded := morphReloadItem(t, query, dest.ID)
	assert.Equal(t, "bar", reloaded.Foo)
	assert.Equal(t, "qux", reloaded.Baz)
	assert.Equal(t, uint64(1), reloaded.MorphID)
	assert.Equal(t, "users", reloaded.MorphType)
	assert.Equal(t, int64(1), morphItemCount(t, query))
}

// Laravel: getNamespacedRelation('namespace') registers Relation::morphMap(['namespace' =>
// EloquentModelNamespacedStub::class]) and asserts create() sets morph_type to the alias. Oracle:
// with the map registered, $parent->getMorphClass() === 'namespace' and the relation's constraints
// bind morph_type = 'namespace' (bindings ['namespace', 1]). The corpus's tearDown
// (Relation::morphMap([], false)) resets the registry; the port resets it via t.Cleanup.
func TestMorph_CreateFunctionOnNamespacedMorph(t *testing.T) {
	query := newRelationQuery(t)
	morphSchema(t, query)
	morphSeedParent(t, query, &morphUser{}, 1)

	databaseorm.MorphMap(map[string]any{"namespace": &morphUser{}}, false)
	t.Cleanup(morphmap.Reset)

	user := morphUser{ID: 1}
	item := morphItem{Name: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "MorphOne").Create(&item))

	assert.Equal(t, "namespace", item.MorphType)
	assert.Equal(t, uint64(1), item.MorphID)

	reloaded := morphReloadItem(t, query, item.ID)
	assert.Equal(t, "namespace", reloaded.MorphType)

	// The alias is also what the read path filters by.
	items := []morphItem{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "MorphOne").Get(&items))
	require.Len(t, items, 1)
	assert.Equal(t, item.ID, items[0].ID)
}

// --- relation-object is() / isNot() ------------------------------------------

// The corpus's is() tests pair the getOneRelation() MorphOne (parent key 1) with mocked Models
// returning morph_id plus a table and connection name. The relation object Query.Related returns
// now carries the same comparison, so each test below asserts it for real — the model arguments
// are typed Go stubs declaring what the mocks returned, and the connection cases use a related
// class declaring 'connection' so the equality weighs a non-empty value.

// morphConnectedUser is the corpus's parent for the is() tests whose related mock declared
// connection 'connection'.
type morphConnectedUser struct {
	ID       uint64              `gorm:"column:id;primaryKey;autoIncrement"`
	MorphOne *morphConnectedItem `gorm:"-"`
}

func (morphConnectedUser) TableName() string { return "users" }

func (morphConnectedUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"MorphOne": contractsorm.MorphOne{Related: &morphConnectedItem{}, Name: "morph"},
	}
}

// morphConnectedItem is the corpus's related mock: morph_items with connection 'connection'.
type morphConnectedItem struct {
	ID      uint64 `gorm:"column:id;primaryKey"`
	MorphID uint64 `gorm:"column:morph_id"`
}

func (morphConnectedItem) TableName() string  { return "morph_items" }
func (morphConnectedItem) Connection() string { return "connection" }

// morphStringKeyItem shares the table under a text key — the corpus's string-'1' model key.
type morphStringKeyItem struct {
	ID      uint64 `gorm:"column:id;primaryKey"`
	MorphID string `gorm:"column:morph_id"`
}

func (morphStringKeyItem) TableName() string  { return "morph_items" }
func (morphStringKeyItem) Connection() string { return "connection" }

// morphOtherTableItem shares the connection but not the table — the corpus's 'table.two' model.
type morphOtherTableItem struct {
	ID      uint64 `gorm:"column:id;primaryKey"`
	MorphID uint64 `gorm:"column:morph_id"`
}

func (morphOtherTableItem) TableName() string  { return "morph_items.two" }
func (morphOtherTableItem) Connection() string { return "connection" }

// morphOtherConnItem shares the table but not the connection — the corpus's 'connection.two'
// model.
type morphOtherConnItem struct {
	ID      uint64 `gorm:"column:id;primaryKey"`
	MorphID uint64 `gorm:"column:morph_id"`
}

func (morphOtherConnItem) TableName() string  { return "morph_items" }
func (morphOtherConnItem) Connection() string { return "connection.two" }

func TestMorph_IsNotNull(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: relation->is(null) is false — getRelated()/getTable() are never consulted (oracle:
	// mo is(null) false).
	rel := cloneRelationQuery(t, query).Related(&morphConnectedUser{ID: 1}, "MorphOne").(contractsorm.MorphOneRelation)
	assert.False(t, rel.Is(nil))
}

func TestMorph_IsModel(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: relation->is($model) is true when the model's morph_id attribute equals the parent
	// key (1) and its table and connection match the related model's (oracle: mo conn is(conn mid
	// 1) true).
	rel := cloneRelationQuery(t, query).Related(&morphConnectedUser{ID: 1}, "MorphOne").(contractsorm.MorphOneRelation)
	assert.True(t, rel.Is(&morphConnectedItem{MorphID: 1}))
}

func TestMorph_IsModelWithStringRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: the same is() comparison casts keys when either side is an int, so string '1'
	// matches the parent key 1 (oracle: mo is(mid "1") true).
	rel := cloneRelationQuery(t, query).Related(&morphConnectedUser{ID: 1}, "MorphOne").(contractsorm.MorphOneRelation)
	assert.True(t, rel.Is(&morphStringKeyItem{MorphID: "1"}))
}

func TestMorph_IsNotModelWithNullRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: a model whose morph_id attribute is null is not the related instance. The Go key is
	// value-typed, so the zero takes the empty-key reading compareKeys gives the PHP null (oracle:
	// mo is(mid null) false).
	rel := cloneRelationQuery(t, query).Related(&morphConnectedUser{ID: 1}, "MorphOne").(contractsorm.MorphOneRelation)
	assert.False(t, rel.Is(&morphConnectedItem{}))
}

func TestMorph_IsNotModelWithAnotherRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: a model whose morph_id is 2 is not the related instance of a parent keyed 1
	// (oracle: mo is(mid 2) false).
	rel := cloneRelationQuery(t, query).Related(&morphConnectedUser{ID: 1}, "MorphOne").(contractsorm.MorphOneRelation)
	assert.False(t, rel.Is(&morphConnectedItem{MorphID: 2}))
}

func TestMorph_IsNotModelWithAnotherTable(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: a model on another table ('table.two', the port's "morph_items.two") is not the
	// related instance even with the right key (oracle: mo is(other table mid 1) false).
	rel := cloneRelationQuery(t, query).Related(&morphConnectedUser{ID: 1}, "MorphOne").(contractsorm.MorphOneRelation)
	assert.False(t, rel.Is(&morphOtherTableItem{MorphID: 1}))
}

func TestMorph_IsNotModelWithAnotherConnection(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: a model on another connection ('connection.two') is not the related instance even
	// with the right key and table (oracle: mo is(other conn mid 1) false).
	rel := cloneRelationQuery(t, query).Related(&morphConnectedUser{ID: 1}, "MorphOne").(contractsorm.MorphOneRelation)
	assert.False(t, rel.Is(&morphOtherConnItem{MorphID: 1}))
}
