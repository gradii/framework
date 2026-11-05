package morph_one_of_many

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
)

// This file ports Laravel's tests/Database/DatabaseEloquentMorphOneOfManyTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestMorphOneOfMany_<method without the
// test prefix>. The corpus's two tables mirror createSchema() in sqlite DDL — morphs('stateful')
// becomes the stateful_type + stateful_id column pair. Rows are seeded through the ORM, and
// relations are loaded explicitly through OrmBuilder.Load — the harness's lazy-loading rule, since
// Go has no property-access magic.
//
// # Morph identity
//
// Laravel's default morph class is the parent's FQCN, while this framework's default morph value is
// the parent's table name (resolveMorphValue falls back to it when no MorphClass() method or morph
// map registration intervenes). Where the corpus asserts MorphOneOfManyTestProduct::class on a
// state's stateful_type, the port asserts "products" — the same observable (the created row carries
// the parent model's morph identity) expressed in this framework's representation.
//
// # Oracle
//
// The corpus pins one SQL string (testEagerLoadingAppliesConstraintsToInnerJoinSubQuery); it was
// measured with the oracle (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1),
// not recalled, and the oracle also measured the SQL behind the behaviour tests. The pinned string —
// $relation->getOneOfManySubQuery()->toSql() after addEagerConstraints([$product]) — was:
//
//	select MAX("states"."id") as "id_aggregate", "states"."stateful_id", "states"."stateful_type"
//	from "states" where "states"."stateful_type" = ? and "states"."stateful_id" = ? and
//	"states"."stateful_id" is not null and "states"."stateful_id" in (1) and "states"."stateful_type"
//	= ? group by "states"."stateful_id", "states"."stateful_type"
//
// The relation's own read query joins that subquery aliased "current_state" (Laravel's guessed
// relation name) ON id_aggregate = id and the two morph columns; whereHas('current_state') carries
// the same join inside its exists subquery; and withExists('current_state') compiles the join inside
// `exists (...) as "current_state_exists"`.
//
// # How our morph one-of-many still diverges from Laravel's
//
// The join is carried on all three paths, and the eager read now carries the parent-key in-list
// inside the group subquery the way Laravel's addEagerConstraints writes it — asserted from the
// query log, since getOneOfManySubQuery itself has no Go accessor. What still differs: the
// subquery alias follows the
// relation key in snake case, where Laravel aliases by its guessed PHP method name (equal for the
// corpus's snake_case methods; the withoutGlobalScopes shortcut's "latestOfMany" guess has no Go
// counterpart); and EXISTS bodies project `select 1` (the ORM's standing EXISTS convention) where
// Laravel projects `select *`. The aggregate column "<column>_aggregate", the group-by columns
// and the join conditions match the oracle byte for byte.

// --- models ------------------------------------------------------------------

// morphOneOfManyProduct ports MorphOneOfManyTestProduct: only the id column is real; the relation
// destinations are kept out of the table by gorm:"-".
type morphOneOfManyProduct struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`

	States          []*morphOneOfManyState `gorm:"-"`
	CurrentState    *morphOneOfManyState   `gorm:"-"`
	CurrentFooState *morphOneOfManyState   `gorm:"-"`
}

func (morphOneOfManyProduct) TableName() string { return "products" }

func (morphOneOfManyProduct) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"States": contractsorm.MorphMany{Related: &morphOneOfManyState{}, Name: "stateful"},
		// current_state(): morphOne(...)->ofMany(), the aggregate defaulting to id MAX.
		"CurrentState": contractsorm.MorphOne{
			Related: &morphOneOfManyState{}, Name: "stateful",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		// current_foo_state(): ofMany(['id' => 'max'], where type = foo).
		"CurrentFooState": contractsorm.MorphOne{
			Related: &morphOneOfManyState{}, Name: "stateful",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("id", "MAX").Where("type", "foo")
			},
		},
	}
}

// morphOneOfManyState ports MorphOneOfManyTestState. StatefulID/StatefulType are the
// morphs('stateful') column pair.
type morphOneOfManyState struct {
	ID           uint   `gorm:"column:id;primaryKey;autoIncrement"`
	StatefulID   uint   `gorm:"column:stateful_id"`
	StatefulType string `gorm:"column:stateful_type"`
	State        string `gorm:"column:state"`
	Type         string `gorm:"column:type"`
}

func (morphOneOfManyState) TableName() string { return "states" }

// morphOneOfManyAggregateDest is the scan target for the withExists reads: Laravel exposes
// current_state_exists / current_foo_state_exists as dynamic attributes on the product model, while
// a Go model's table columns are fixed — the alias destinations live on a separate struct or every
// products insert would try to write the pseudo-columns.
type morphOneOfManyAggregateDest struct {
	ID                    uint `gorm:"column:id"`
	CurrentStateExists    bool `gorm:"column:current_state_exists"`
	CurrentFooStateExists bool `gorm:"column:current_foo_state_exists"`
}

// --- schema and seeding ------------------------------------------------------

// morphOneOfManySchema mirrors the corpus's createSchema() in sqlite DDL.
func morphOneOfManySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table products (id integer primary key autoincrement)`,
		`create table states (id integer primary key autoincrement, stateful_type varchar not null, stateful_id integer not null, state varchar not null, type varchar null)`,
	)
}

// morphOneOfManySeedProduct creates the single products row the corpus's create() would: id 1.
func morphOneOfManySeedProduct(t *testing.T, query contractsorm.Query) morphOneOfManyProduct {
	t.Helper()

	// The corpus's products table carries only the auto-increment id, and the ORM compiles an
	// all-defaults struct insert as `insert into "products" () values ()`, which sqlite rejects.
	// The helper therefore writes the explicit id the auto increment would assign; every assertion
	// observes the same rows.
	product := morphOneOfManyProduct{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Model(&product).Create(map[string]any{"id": 1}))

	return product
}

// morphOneOfManyCreateState ports $product->states()->create([...]) for the given attributes.
func morphOneOfManyCreateState(t *testing.T, query contractsorm.Query, product *morphOneOfManyProduct, state morphOneOfManyState) morphOneOfManyState {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Relation(product, "States").Create(&state))

	return state
}

// --- ports of the corpus -----------------------------------------------------

func TestMorphOneOfMany_EagerLoadingAppliesConstraintsToInnerJoinSubQuery(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)
	_ = morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "draft"})

	// Laravel pins $relation->getOneOfManySubQuery()->toSql() after addEagerConstraints([$product]);
	// the oracle prints the string in the file header, where the in-list sits INSIDE the group
	// subquery. The Go ORM has no addEagerConstraints probe (a live relation cannot take an eager
	// in-list), so the port runs the real eager read that constraint belongs to and asserts the
	// statement the loader emits, measured with the oracle for the same read (keys [1]). The
	// oracle's morph-type `?` binds this framework's morph value ("products"), which the query log
	// interpolates.
	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	found := []morphOneOfManyProduct{}
	require.NoError(t, withContext.WithContext(ctx).Model(&morphOneOfManyProduct{}).With("CurrentState").Get(&found))
	require.Len(t, found, 1)
	require.NotNil(t, found[0].CurrentState)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "the main read and the eager read")
	assert.Equal(t, `select "states".* from "states" inner join (select MAX("states"."id") as "id_aggregate", "states"."stateful_id", "states"."stateful_type" from "states" where "states"."stateful_id" in (1) and "states"."stateful_type" = 'products' group by "states"."stateful_id", "states"."stateful_type") as "current_state" on "current_state"."id_aggregate" = "states"."id" and "current_state"."stateful_type" = "states"."stateful_type" and "current_state"."stateful_id" = "states"."stateful_id"`, logs[1].Query)
}

func TestMorphOneOfMany_ReceivingModel(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)
	_ = morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "draft"})
	_ = morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "active"})

	// Laravel reads $product->current_state, which lazy-loads; the port loads explicitly.
	require.NoError(t, cloneRelationQuery(t, query).Load(&product, "CurrentState"))
	require.NotNil(t, product.CurrentState)
	assert.Equal(t, "active", product.CurrentState.State)
}

func TestMorphOneOfMany_MorphType(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)
	_ = morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "draft"})
	_ = morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "active"})

	// Laravel: $state = $product->states()->make(['state' => 'foo']); $state->stateful_type = 'bar';
	// $state->save(); FindOrNew is the relation writer's make: with no row at the id it fills dest
	// and pre-sets the FK (and morph type) without persisting, and the override + Save below then
	// writes the row carrying the foreign morph type.
	state := morphOneOfManyState{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&product, "States").FindOrNew(999, &state))
	require.Equal(t, uint(1), state.StatefulID)
	state.State = "foo"
	state.StatefulType = "bar"
	require.NoError(t, cloneRelationQuery(t, query).Save(&state))
	require.NotZero(t, state.ID, "Save must have inserted the foreign-type row")

	require.NoError(t, cloneRelationQuery(t, query).Load(&product, "CurrentState"))
	require.NotNil(t, product.CurrentState)
	assert.Equal(t, "active", product.CurrentState.State)
}

func TestMorphOneOfMany_ForceCreateMorphType(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)

	// Laravel: $state = $product->states()->forceCreate(['state' => 'active']); the Go ORM has no
	// mass-assignment guard, so the relation object's ForceCreate differs from Create only in
	// dropping a relation's withAttributes pending pair (MorphOneOrMany.php:78-84; none recorded
	// here) — with the morph override (MorphOneOrMany.php:78 / MorphMany.php:63) stamping the
	// type, the oracle measured imageable_id 1 plus the parent's morph class.
	relation, ok := cloneRelationQuery(t, query).Related(&product, "States").(contractsorm.MorphManyRelation)
	require.True(t, ok)

	state := morphOneOfManyState{State: "active"}
	require.NoError(t, relation.ForceCreate(&state))
	require.NotZero(t, state.ID)
	// The FK and morph columns are set on the created model from the parent.
	assert.Equal(t, uint(1), state.StatefulID)
	assert.Equal(t, "products", state.StatefulType)

	require.NoError(t, cloneRelationQuery(t, query).Load(&product, "CurrentState"))
	require.NotNil(t, product.CurrentState)
	// Laravel asserts $product->current_state->stateful_type === MorphOneOfManyTestProduct::class —
	// the created row carries the parent's default morph identity, which this framework represents
	// as the parent's table name.
	assert.Equal(t, "products", product.CurrentState.StatefulType)
}

func TestMorphOneOfMany_Exists(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)
	previousState := morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "draft"})
	currentState := morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "active"})

	// Laravel: whereHas('current_state', whereKey(previous_state))->exists() is false because the
	// one-of-many join inside the exists subquery narrows it to the current state; constraining by
	// the current state's key is true.
	exists, err := cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WhereHas("CurrentState", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("states.id", previousState.ID)
	}).Exists()
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WhereHas("CurrentState", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("states.id", currentState.ID)
	}).Exists()
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestMorphOneOfMany_WithWhereHas(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)
	previousState := morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "draft"})
	currentState := morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "active"})

	// Laravel: withWhereHas('current_state', whereKey(previous_state))->exists() is false, and the
	// current state's key yields one product with the relation loaded.
	exists, err := cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithWhereHas("CurrentState", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("states.id", previousState.ID)
	}).Exists()
	require.NoError(t, err)
	assert.False(t, exists)

	products := []morphOneOfManyProduct{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithWhereHas("CurrentState", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("states.id", currentState.ID)
	}).Get(&products))
	require.Len(t, products, 1)
	require.NotNil(t, products[0].CurrentState)
	assert.Equal(t, currentState.State, products[0].CurrentState.State)
}

func TestMorphOneOfMany_WithWhereRelation(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)
	currentState := morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "active"})

	// Laravel's withWhereRelation is whereRelation(...)->with(callback); this ORM exposes the pair
	// through WithWhereHas, which builds exactly that (existence check + eager load under the same
	// callback), so the port calls it directly.
	activeState := func(q contractsorm.Query) contractsorm.Query {
		return q.Where("state", "active")
	}

	exists, err := cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithWhereHas("CurrentState", activeState).Exists()
	require.NoError(t, err)
	assert.True(t, exists)

	products := []morphOneOfManyProduct{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithWhereHas("CurrentState", activeState).Get(&products))

	require.Len(t, products, 1)
	require.NotNil(t, products[0].CurrentState)
	assert.Equal(t, currentState.State, products[0].CurrentState.State)
}

func TestMorphOneOfMany_WithExists(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)

	found := morphOneOfManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithExists("CurrentState").First(&found))
	assert.False(t, found.CurrentStateExists)

	_ = morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "draft"})

	require.NoError(t, cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithExists("CurrentState").First(&found))
	assert.True(t, found.CurrentStateExists)
}

func TestMorphOneOfMany_WithExistsWithConstraintsInJoinSubSelect(t *testing.T) {
	query := newRelationQuery(t)
	morphOneOfManySchema(t, query)

	product := morphOneOfManySeedProduct(t, query)

	found := morphOneOfManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithExists("CurrentFooState").First(&found))
	assert.False(t, found.CurrentFooStateExists)

	_ = morphOneOfManyCreateState(t, query, &product, morphOneOfManyState{State: "draft", Type: "foo"})

	require.NoError(t, cloneRelationQuery(t, query).Model(&morphOneOfManyProduct{}).WithExists("CurrentFooState").First(&found))
	assert.True(t, found.CurrentFooStateExists)
}
