package with_attributes_pending

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentWithAttributesPendingTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestWithAttributesPending_<method without the test prefix>. The corpus is the asConditions: false
// flavour of Builder::withAttributes: the attributes are recorded as pending attributes without a
// where clause, then applied to the model the query produces — make() in the corpus's first and
// third tests, create() in the "ViaDb" one. Both producers are mirrors of Laravel's: Query.Make
// routes through newModelInstance() and Query.FirstOrNew merges the pending attributes on a miss.
//
// What a typed Go model can and cannot reproduce decides each port:
//
//   - Casts (is_admin => 'boolean', type => PendingAttributesEnum) map onto typed Go fields — a bool
//     field over the stored 1, a typed string constant over the stored 'int' — and make() applies
//     the pending attributes to the unsaved instance, so testAddsWithCasts and testAddsWithCastsViaDb
//     both assert the two cast reads.
//   - The set/get accessors (setFirstNameAttribute, getFirstNameAttribute, the lastName Attribute)
//     and the raw attribute bag (getAttributes()) are PHP mechanisms with no Go equivalent — the
//     ORM has no accessors or mutators and a typed model has no attribute bag — so the
//     first_name/last_name reads (accessor 'First'/'Last' over the stored 'first'/'last', and the
//     raw bag {"is_admin":1,"first_name":"first","last_name":"last","type":"int"}) are documented
//     instead of asserted.
//   - make() is the non-writing producer: it fills the destination from the pending attributes and
//     the attributes it is given (the given ones winning), executes no statement, and leaves no row
//     — oracle: $model->exists false and the table count 0 after ->make().
//
// Every Laravel value this file cites was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, with a Capsule-backed
// sqlite connection and the corpus's model copied verbatim), not recalled.

// pendingAttributesModel ports PendingAttributesModel. Laravel derives the table name from the class
// (pending_attributes_models — oracle-checked); a Go model declares it. The corpus's arbitrary
// attribute key 'a key' is carried by a field whose column is "a key": that is how a typed model
// represents an attribute whose name is not a field-shaped identifier.
type pendingAttributesModel struct {
	ID        uint64                `gorm:"column:id;primaryKey;autoIncrement"`
	AKey      string                `gorm:"column:a key"`
	IsAdmin   bool                  `gorm:"column:is_admin"`
	FirstName string                `gorm:"column:first_name"`
	LastName  string                `gorm:"column:last_name"`
	Type      pendingAttributesType `gorm:"column:type"`
}

// pendingAttributesType is the Go side of PendingAttributesEnum: a string-backed type whose single
// declared value is the corpus's internal case.
type pendingAttributesType string

const pendingAttributesInternal pendingAttributesType = "int"

func (pendingAttributesModel) TableName() string { return "pending_attributes_models" }

// pendingAttributesSchema mirrors the corpus's bootTable() — id, a boolean is_admin, first_name,
// last_name, type and nullable timestamps — in sqlite DDL, plus the "a key" column the AddsAttributes
// port fills: a typed Go model resolves an attribute through a struct field (AKey) whose column the
// create path also writes, so the corpus's arbitrary key needs both.
func pendingAttributesSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table pending_attributes_models (id integer primary key autoincrement, "a key" text, is_admin tinyint(1) not null, first_name varchar not null, last_name varchar not null, type varchar not null, created_at datetime null, updated_at datetime null)`,
	)
}

// TestWithAttributesPending_AddsAttributes ports testAddsAttributes: withAttributes(..., false)
// records the attribute, and make() fills the unsaved model with it. Oracle: the corpus's ->make()
// reads 'the value' back as $model->{'a key'} (getAttributes() is {"a key":"the value"}), $model
// ->exists is false and the table stays empty; the same query with the default asConditions compiles
// the qualified equality `"pending_attributes_models"."a key" = ?` while asConditions: false
// compiles none.
func TestWithAttributesPending_AddsAttributes(t *testing.T) {
	query := newRelationQuery(t)
	pendingAttributesSchema(t, query)

	key := "a key"
	value := "the value"

	model := pendingAttributesModel{}
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&pendingAttributesModel{}).
		WithAttributes(map[string]any{key: value}, false).
		Make(&model))

	assert.Equal(t, value, model.AKey)

	// make() writes nothing: the row count stays zero (oracle-measured).
	count, err := cloneRelationQuery(t, query).Model(&pendingAttributesModel{}).Count()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// The asConditions flavours, pinned against the oracle (sqlite): false leaves the select bare;
	// true adds the qualified equality, whose value the raw form renders inline.
	falseModel := pendingAttributesModel{}
	assert.Equal(t, `select * from "pending_attributes_models"`,
		cloneRelationQuery(t, query).
			Model(&pendingAttributesModel{}).
			WithAttributes(map[string]any{key: value}, false).
			ToSql().
			Get(&falseModel))

	trueModel := pendingAttributesModel{}
	assert.Equal(t, `select * from "pending_attributes_models" where "pending_attributes_models"."a key" = 'the value'`,
		cloneRelationQuery(t, query).
			Model(&pendingAttributesModel{}).
			WithAttributes(map[string]any{key: value}).
			ToRawSql().
			Get(&trueModel))
}

// TestWithAttributesPending_DoesNotAddWheres ports testDoesNotAddWheres: withAttributes(..., false)
// leaves the where clause empty. Laravel inspects toBase()->wheres; the port asserts the compiled
// statement instead — oracle: `select * from "pending_attributes_models"` (with the default
// asConditions the query compiles `select * from "pending_attributes_models" where
// "pending_attributes_models"."a key" = ?`).
func TestWithAttributesPending_DoesNotAddWheres(t *testing.T) {
	query := newRelationQuery(t)
	pendingAttributesSchema(t, query)

	key := "a key"
	value := "the value"

	model := pendingAttributesModel{}
	statement := cloneRelationQuery(t, query).
		Model(&pendingAttributesModel{}).
		WithAttributes(map[string]any{key: value}, false).
		ToSql().
		Get(&model)

	assert.Equal(t, `select * from "pending_attributes_models"`, statement)
}

// TestWithAttributesPending_AddsWithCasts ports testAddsWithCasts: withAttributes([...], false)
// records the attributes, and make() applies them to the unsaved instance. Oracle: the made model
// reads is_admin true (the 'boolean' cast over the stored 1) and type PendingAttributesEnum::internal
// (the enum cast over the stored 'int'); $model->exists is false and no row is written.
//
// The corpus's first_name 'First' / last_name 'Last' reads stay documented carve-outs: 'First'/'Last'
// come from the getFirstNameAttribute accessor and the lastName Attribute over the stored
// 'first'/'last', PHP model-class machinery a typed Go struct does not have, and getAttributes() is
// the raw bag {"is_admin":1,"first_name":"first","last_name":"last","type":"int"} with no typed-Go
// equivalent. A PHP int 1 does not assign to a Go bool field ("cannot assign int to IsAdmin.bool",
// probed), so the port records the Go-typed true — what the boolean cast would store.
func TestWithAttributesPending_AddsWithCasts(t *testing.T) {
	query := newRelationQuery(t)
	pendingAttributesSchema(t, query)

	model := pendingAttributesModel{}
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&pendingAttributesModel{}).
		WithAttributes(map[string]any{
			"is_admin":   true,
			"first_name": "FIRST",
			"last_name":  "LAST",
			"type":       pendingAttributesInternal,
		}, false).
		Make(&model))

	assert.True(t, model.IsAdmin)
	assert.Equal(t, pendingAttributesInternal, model.Type)
}

// TestWithAttributesPending_AddsWithCastsViaDb ports testAddsWithCastsViaDb: bootTable();
// withAttributes(..., false)->create(); re-read the row. Oracle: the insert carries the pending
// attributes, the row stores {"is_admin":1,"first_name":"first","last_name":"last","type":"int"},
// and the re-read model reads is_admin true, first_name 'First', last_name 'Last' and type
// PendingAttributesEnum::internal.
//
// The port keeps the write behaviour — the pending attributes reach the created row — and asserts
// the two read values a typed Go model reproduces exactly: is_admin reads true (a bool field over
// the stored 1) and type reads internal (the typed constant over the stored 'int'). Laravel's
// boolean cast takes the raw 1 as an attribute; a typed Go bool field rejects an int ("cannot
// assign int to IsAdmin.bool", probed), so the port writes the typed true. The first_name/last_name
// reads are PHP-only: 'First'/'Last' come from the getFirstNameAttribute/lastName accessors, and the
// stored 'first'/'last' from the matching setter/Attribute mutators, which a typed Go model does not
// have — its columns would keep the written 'FIRST'/'LAST'.
func TestWithAttributesPending_AddsWithCastsViaDb(t *testing.T) {
	query := newRelationQuery(t)
	pendingAttributesSchema(t, query)

	created := pendingAttributesModel{}
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&pendingAttributesModel{}).
		WithAttributes(map[string]any{
			"is_admin":   true,
			"first_name": "FIRST",
			"last_name":  "LAST",
			"type":       pendingAttributesInternal,
		}, false).
		Create(&created))

	reloaded := pendingAttributesModel{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&pendingAttributesModel{}).First(&reloaded))

	assert.True(t, reloaded.IsAdmin)
	assert.Equal(t, pendingAttributesInternal, reloaded.Type)
}
