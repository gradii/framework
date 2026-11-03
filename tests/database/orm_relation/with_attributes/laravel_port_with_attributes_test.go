package with_attributes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentWithAttributesTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestWithAttributes_<method without the
// test prefix>. The corpus's WithAttributesModel becomes withAttributesModel: a typed Go model
// declares a field per column, so the corpus's dynamic 'a key' attribute is the AKey field, the
// 'is_admin' => 'boolean' cast is the bool field, and the WithAttributesEnum cast is the
// withAttributesEnum string type. The corpus's bootTable() becomes withAttributesSchema.
//
// Laravel's make()/newModelInstance() maps onto Query.Make: it fills the unsaved destination from
// the pending attributes and the attributes it is given, the given ones winning, and writes nothing
// — the "make() writes nothing" guarantee is asserted with a row count. The model's accessor+mutator
// pair (first_name / last_name) and the getAttributes() raw attribute bag are PHP model-class
// machinery with no typed-Go equivalent; those assertions are skipped with that reason, noted on the
// tests.
//
// Every SQL expectation this file cites was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled.

// withAttributesSchema ports bootTable(): id, is_admin, first_name, last_name, type, timestamps.
// The "a key" column is the Go-side addition the typed model needs — Laravel's make()-based tests
// keep that attribute off the database entirely, but a Go struct field must name a real column
// for the inserted row (the ORM writes every declared column).
func withAttributesSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table with_attributes_models (id integer primary key autoincrement, "a key" varchar null, is_admin integer not null default 0, first_name varchar not null default '', last_name varchar not null default '', type varchar not null default '', created_at datetime null, updated_at datetime null)`,
	)
}

// withAttributesModel ports WithAttributesModel: AKey is the corpus's dynamic 'a key' attribute,
// IsAdmin carries the 'boolean' cast, and Type the WithAttributesEnum cast. The corpus's
// setFirstNameAttribute/getFirstNameAttribute pair and its lastName() accessor are PHP model-class
// code — a typed Go struct has no accessor hooks, so both fields hold their written values.
type withAttributesModel struct {
	ID        uint64             `gorm:"column:id;primaryKey;autoIncrement"`
	AKey      string             `gorm:"column:a key"`
	IsAdmin   bool               `gorm:"column:is_admin"`
	FirstName string             `gorm:"column:first_name"`
	LastName  string             `gorm:"column:last_name"`
	Type      withAttributesEnum `gorm:"column:type"`
}

func (withAttributesModel) TableName() string { return "with_attributes_models" }

// withAttributesEnum ports WithAttributesEnum; WithAttributesEnum::internal's backing value is 'int'.
type withAttributesEnum string

const withAttributesEnumInternal withAttributesEnum = "int"

// Laravel: WithAttributesModel::query()->withAttributes(['a key' => 'the value'])->make() — the
// made model carries the attribute and nothing is written. Oracle: $model->{'a key'} reads 'the
// value', $model->exists is false and the table count stays zero.
func TestWithAttributes_AddsAttributes(t *testing.T) {
	query := newRelationQuery(t)
	withAttributesSchema(t, query)

	model := withAttributesModel{}
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&model).
		WithAttributes(map[string]any{"a key": "the value"}).
		Make(&model))

	require.Equal(t, "the value", model.AKey)

	count, err := cloneRelationQuery(t, query).Model(&withAttributesModel{}).Count()
	require.NoError(t, err)
	require.Equal(t, int64(0), count, "make() writes nothing")
}

// Laravel: WithAttributesModel::query()->withAttributes(['a key' => 'the value']) and then
// toBase()->wheres must contain the equality {'with_attributes_models.a key', '=', 'the value',
// and}. The Go ORM exposes no wheres array; the closest observable is the compiled select, pinned
// from the oracle (Laravel's sqlite grammar) as
//
//	select * from "with_attributes_models" where "with_attributes_models"."a key" = ?
//
// and the port also executes the query, so the bound value — hidden behind the placeholder — is
// shown to filter the rows the way the where says.
func TestWithAttributes_AddsWheres(t *testing.T) {
	query := newRelationQuery(t)
	withAttributesSchema(t, query)

	// Two rows: only the first carries the attribute's value.
	matching := withAttributesModel{AKey: "the value"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&matching))
	other := withAttributesModel{AKey: "other"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&other))

	model := withAttributesModel{}
	require.Equal(t,
		`select * from "with_attributes_models" where "with_attributes_models"."a key" = ?`,
		cloneRelationQuery(t, query).
			Model(&model).
			WithAttributes(map[string]any{"a key": "the value"}).
			ToSql().
			Get(&model),
	)

	var rows []withAttributesModel
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&withAttributesModel{}).
		WithAttributes(map[string]any{"a key": "the value"}).
		Get(&rows))
	require.Len(t, rows, 1)
	require.Equal(t, "the value", rows[0].AKey)
}

// Laravel: withAttributes(['is_admin' => 1, 'first_name' => 'FIRST', 'last_name' => 'LAST',
// 'type' => WithAttributesEnum::internal])->make(), asserting the cast reads (true, the enum), the
// accessor reads ('First', 'Last') and the raw attribute bag (is_admin=1, first_name='first',
// last_name='last', type='int').
//
// The boolean and enum reads map to the typed Go fields and are ported (through Make, the Go
// merge point — see the file header). The rest is PHP-only, skipped per assertion:
//   - first_name 'First' / last_name 'Last': the model's accessor+mutator pair (strtolower on
//     write, ucfirst on read) is PHP model-class code a typed Go struct cannot carry;
//   - getAttributes(): a typed model has no raw attribute bag.
//
// A PHP int 1 does not assign to a Go bool field (field.Set reports "cannot assign int to
// IsAdmin.bool"), so the port passes the Go-typed value true — what the boolean cast would store.
func TestWithAttributes_AddsWithCasts(t *testing.T) {
	query := newRelationQuery(t)
	withAttributesSchema(t, query)

	model := withAttributesModel{}
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&model).
		WithAttributes(map[string]any{
			"is_admin":   true,
			"first_name": "FIRST",
			"last_name":  "LAST",
			"type":       withAttributesEnumInternal,
		}).
		Make(&model))

	assert.True(t, model.IsAdmin)
	assert.Equal(t, withAttributesEnumInternal, model.Type)
}

// Laravel: bootTable(); withAttributes([...])->create(); WithAttributesModel::first(), asserting
// is_admin true, first_name 'First', last_name 'Last' and the Type enum on the reloaded model.
//
// The port creates with the Go-typed pending values (see TestWithAttributes_AddsWithCasts for the
// int-1-to-bool note) and reloads. The boolean and enum reads map to the typed Go fields — sqlite
// stores 1 and 'int', and scanning them back yields the true flag and the enum value — so they are
// asserted on the reloaded model. The first_name 'First' / last_name 'Last' assertions are
// PHP-only: the accessor+mutator pair has no Go equivalent, so the reloaded typed model carries
// the written values; that assertion pair is skipped with that reason.
func TestWithAttributes_AddsWithCastsViaDb(t *testing.T) {
	query := newRelationQuery(t)
	withAttributesSchema(t, query)

	created := withAttributesModel{}
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&created).
		WithAttributes(map[string]any{
			"is_admin":   true,
			"first_name": "FIRST",
			"last_name":  "LAST",
			"type":       withAttributesEnumInternal,
		}).
		Create(&created))

	var model withAttributesModel
	require.NoError(t, cloneRelationQuery(t, query).First(&model))

	assert.True(t, model.IsAdmin)
	assert.Equal(t, withAttributesEnumInternal, model.Type)
}
