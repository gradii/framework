package belongs_to_many_aggregate

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasequery "github.com/gradii/framework/database/query"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyAggregateTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyAggregate_<method without the test prefix>. The corpus's setUp creates five
// tables — orders / products / order_product (the m2m pivot, carrying quantity) and transactions /
// allocations (a self-referencing m2m pivot via from_id / to_id, carrying amount) — and seeds one
// order attached to three products with quantities 3, 4, 5 and one transaction allocated 300 / 400 /
// 500 to transactions 2 / 3 / 4. Each of its three tests reads a sum over the pivot through
// withSum('<relation> as <alias>', '<qualified column>').
//
// The Go chains mirror the corpus's: WithSum("Products as total_products", "order_product.quantity")
// resolves the relation-name rename and reads total_products = 12; the self-referencing
// WithSum("AllocatedTo as total_allocated", "allocations.amount") aliases the subquery's own table
// (laravel_reserved_N, normalised below) and reads 1200; the Expression column form
// (databasequery.Raw("allocations.amount * 2")) reads 2400.
//
// Every SQL expectation this file asserts was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, sqlite dialect):
//
//	withSum alias      select "orders".*, (select sum("order_product"."quantity") from "products"
//	                   inner join "order_product" on "products"."id" = "order_product"."product_id"
//	                   where "orders"."id" = "order_product"."order_id") as "total_products" from "orders"
//	withSum self       select "transactions".*, (select sum("allocations"."amount") from "transactions"
//	                   as "laravel_reserved_0" inner join "allocations" on "laravel_reserved_0"."id"
//	                   = "allocations"."to_id" where "transactions"."id" = "allocations"."from_id")
//	                   as "total_allocated" from "transactions"
//	withSum expression select ... sum(allocations.amount * 2) ... (laravel_reserved_1 in the same
//	                   process; normalised below through the corpus tests' own placeholder)
//
// The alias' numeric suffix tracks Laravel's process-wide Relation::$selfJoinCount; the SQL
// assertions normalise laravel_reserved_\d+ to self_alias_hash the way the corpus test replaces the
// dynamic hash.

var belongsToManyAggregateReservedAlias = regexp.MustCompile(`\blaravel_reserved_\d+\b`)

// normalizeBelongsToManyAggregateAlias replaces the dynamic self-join hash so the statement can be
// compared byte for byte.
func normalizeBelongsToManyAggregateAlias(sql string) string {
	return belongsToManyAggregateReservedAlias.ReplaceAllString(sql, "self_alias_hash")
}

// --- models ------------------------------------------------------------------

// belongsToManyAggregateOrder ports BelongsToManyAggregateTestTestOrder.
type belongsToManyAggregateOrder struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`

	Products []*belongsToManyAggregateProduct `gorm:"-"`
}

func (belongsToManyAggregateOrder) TableName() string { return "orders" }

func (belongsToManyAggregateOrder) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// products(): belongsToMany(Product::class, 'order_product', 'order_id', 'product_id')
		//   ->withPivot('quantity').
		"Products": contractsorm.Many2Many{
			Related:         &belongsToManyAggregateProduct{},
			Table:           "order_product",
			ForeignPivotKey: "order_id",
			RelatedPivotKey: "product_id",
		},
	}
}

// belongsToManyAggregateProduct ports BelongsToManyAggregateTestTestProduct: an id-only table.
type belongsToManyAggregateProduct struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`
}

func (belongsToManyAggregateProduct) TableName() string { return "products" }

// belongsToManyAggregateTransaction ports BelongsToManyAggregateTestTestTransaction: a
// self-referencing m2m over the allocations pivot (from_id -> transactions.id, to_id ->
// transactions.id).
type belongsToManyAggregateTransaction struct {
	ID    uint `gorm:"column:id;primaryKey;autoIncrement"`
	Value int  `gorm:"column:value"`

	AllocatedTo []*belongsToManyAggregateTransaction `gorm:"-"`
}

func (belongsToManyAggregateTransaction) TableName() string { return "transactions" }

func (belongsToManyAggregateTransaction) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// allocatedTo(): belongsToMany(self::class, 'allocations', 'from_id', 'to_id')
		//   ->withPivot('quantity').
		"AllocatedTo": contractsorm.Many2Many{
			Related:         &belongsToManyAggregateTransaction{},
			Table:           "allocations",
			ForeignPivotKey: "from_id",
			RelatedPivotKey: "to_id",
		},
	}
}

// belongsToManyAggregateDest is the scan target for the sum reads: Laravel exposes total_products /
// total_allocated as dynamic attributes on the model, while a Go model's table columns are fixed —
// the alias destinations must live on a separate struct or every insert would try to write the
// pseudo-columns.
type belongsToManyAggregateDest struct {
	ID             uint   `gorm:"column:id"`
	TotalProducts  *int64 `gorm:"column:total_products"`
	TotalAllocated *int64 `gorm:"column:total_allocated"`
}

// --- schema and seeding ------------------------------------------------------

// belongsToManyAggregateSchema mirrors the corpus's createSchema() in sqlite DDL.
func belongsToManyAggregateSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table orders (id integer primary key autoincrement)`,
		`create table products (id integer primary key autoincrement)`,
		`create table order_product (order_id integer not null, product_id integer not null, quantity integer not null)`,
		`create table transactions (id integer primary key autoincrement, value integer not null)`,
		`create table allocations (from_id integer not null, to_id integer not null, amount integer not null)`,
	)
}

// belongsToManyAggregateSeed ports seedData(): order 1 attached to products 1..3 with quantities
// 3 / 4 / 5, and transaction 1 allocated 300 / 400 / 500 to transactions 2 / 3 / 4. The corpus
// creates rows with explicit ids; the Go writes go through Create with the id column spelled out,
// because an all-defaults struct insert compiles as `insert into "orders" () values ()`, which
// sqlite rejects.
func belongsToManyAggregateSeed(t *testing.T, query contractsorm.Query) {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyAggregateOrder{}).
		Create(map[string]any{"id": 1}))
	for id := 1; id <= 3; id++ {
		require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyAggregateProduct{}).
			Create(map[string]any{"id": id}))
	}

	order := belongsToManyAggregateOrder{ID: 1}
	_, err := cloneRelationQuery(t, query).Relation(&order, "Products").SyncWithPivot(map[any]map[string]any{
		uint(1): {"quantity": 3},
		uint(2): {"quantity": 4},
		uint(3): {"quantity": 5},
	})
	require.NoError(t, err)

	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyAggregateTransaction{}).
		Create(map[string]any{"id": 1, "value": 1200}))
	for _, row := range []map[string]any{{"id": 2, "value": -300}, {"id": 3, "value": -400}, {"id": 4, "value": -500}} {
		require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyAggregateTransaction{}).Create(row))
	}

	transaction := belongsToManyAggregateTransaction{ID: 1}
	_, err = cloneRelationQuery(t, query).Relation(&transaction, "AllocatedTo").SyncWithPivot(map[any]map[string]any{
		uint(2): {"amount": 300},
		uint(3): {"amount": 400},
		uint(4): {"amount": 500},
	})
	require.NoError(t, err)
}

// --- ports of the corpus -----------------------------------------------------

func TestBelongsToManyAggregate_WithSumDifferentTables(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyAggregateSchema(t, query)
	belongsToManyAggregateSeed(t, query)

	// Laravel: Order::query()->withSum('products as total_products', 'order_product.quantity')
	// ->first() asserts total_products == 12.
	assert.Equal(t,
		`select "orders".*, (select sum("order_product"."quantity") from "products" inner join "order_product" on "products"."id" = "order_product"."product_id" where "orders"."id" = "order_product"."order_id") as "total_products" from "orders"`,
		cloneRelationQuery(t, query).Model(&belongsToManyAggregateOrder{}).
			WithSum("Products as total_products", "order_product.quantity").ToSql().Get(&belongsToManyAggregateOrder{}))

	dest := belongsToManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyAggregateOrder{}).
		WithSum("Products as total_products", "order_product.quantity").First(&dest))
	require.NotNil(t, dest.TotalProducts)
	assert.Equal(t, int64(12), *dest.TotalProducts)
}

func TestBelongsToManyAggregate_WithSumSameTable(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyAggregateSchema(t, query)
	belongsToManyAggregateSeed(t, query)

	// Laravel: Transaction::query()->withSum('allocatedTo as total_allocated', 'allocations.amount')
	// ->first() asserts total_allocated == 1200.
	assert.Equal(t,
		`select "transactions".*, (select sum("allocations"."amount") from "transactions" as "self_alias_hash" inner join "allocations" on "self_alias_hash"."id" = "allocations"."to_id" where "transactions"."id" = "allocations"."from_id") as "total_allocated" from "transactions"`,
		normalizeBelongsToManyAggregateAlias(cloneRelationQuery(t, query).Model(&belongsToManyAggregateTransaction{}).
			WithSum("AllocatedTo as total_allocated", "allocations.amount").ToSql().Get(&belongsToManyAggregateTransaction{})))

	dest := belongsToManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyAggregateTransaction{}).
		WithSum("AllocatedTo as total_allocated", "allocations.amount").First(&dest))
	require.NotNil(t, dest.TotalAllocated)
	assert.Equal(t, int64(1200), *dest.TotalAllocated)
}

func TestBelongsToManyAggregate_WithSumExpression(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyAggregateSchema(t, query)
	belongsToManyAggregateSeed(t, query)

	// Laravel: the same-table read with new Expression('allocations.amount * 2'); asserts
	// total_allocated == 2400.
	assert.Equal(t,
		`select "transactions".*, (select sum(allocations.amount * 2) from "transactions" as "self_alias_hash" inner join "allocations" on "self_alias_hash"."id" = "allocations"."to_id" where "transactions"."id" = "allocations"."from_id") as "total_allocated" from "transactions"`,
		normalizeBelongsToManyAggregateAlias(cloneRelationQuery(t, query).Model(&belongsToManyAggregateTransaction{}).
			WithSum("AllocatedTo as total_allocated", databasequery.Raw("allocations.amount * 2")).ToSql().Get(&belongsToManyAggregateTransaction{})))

	dest := belongsToManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyAggregateTransaction{}).
		WithSum("AllocatedTo as total_allocated", databasequery.Raw("allocations.amount * 2")).First(&dest))
	require.NotNil(t, dest.TotalAllocated)
	assert.Equal(t, int64(2400), *dest.TotalAllocated)
}
