package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsdatabase "github.com/goravel/framework/contracts/database"
)

// The integration module's TestWith_ChunkedIN writes `u := &User{…}` and then `Create(&u)`, a pointer
// to a pointer, and GORM accepted that shape. reflect.Indirect dereferences once, so the record
// builders used to report the surviving pointer as an unsupported type instead of inserting the row.
func newPointerTestQuery(t *testing.T) *Query {
	t.Helper()

	return NewQuery(context.Background(), nil, contractsdatabase.Config{}, newStubGormDB(t), nil, nil, nil, &Conditions{}, nil)
}

func TestInsertRecordsAcceptsPointerToPointer(t *testing.T) {
	query := newPointerTestQuery(t)

	outer := &SchemaOuter{Name: "pointer to pointer"}

	records, err := query.insertRecords(&outer)
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "pointer to pointer", records[0]["name"])
	}
}

func TestUpdateAssignmentsAcceptsPointerToPointer(t *testing.T) {
	modeled, ok := newPointerTestQuery(t).Model(&SchemaOuter{}).(*Query)
	assert.True(t, ok)

	outer := &SchemaOuter{Name: "update by pointer to pointer"}

	assignments, err := modeled.updateAssignments(&outer)
	assert.NoError(t, err)
	assert.Equal(t, "update by pointer to pointer", assignments["name"])
}

func TestSaveAssignmentsAcceptsPointerToPointer(t *testing.T) {
	modeled, ok := newPointerTestQuery(t).Model(&SchemaOuter{}).(*Query)
	assert.True(t, ok)

	outer := &SchemaOuter{Name: "save by pointer to pointer"}

	assignments, err := modeled.saveAssignments(&outer)
	assert.NoError(t, err)
	assert.Equal(t, "save by pointer to pointer", assignments["name"])
}

// A nil pointer must still be rejected rather than panicking.
func TestInsertRecordsRejectsNilPointer(t *testing.T) {
	query := newPointerTestQuery(t)

	var outer *SchemaOuter

	_, err := query.insertRecords(outer)
	assert.Error(t, err)
}

// The generated key is written back into the model through schemaField.Set, which walks the field
// path with reflect.Value.Field. Field panics on a pointer value, so the path has to be dereferenced
// fully there too: TestWith_ChunkedIN reached this panic after the insert itself succeeded.
func TestAssignInsertedKeysAcceptsPointerToPointer(t *testing.T) {
	modeled, ok := newPointerTestQuery(t).Model(&SchemaOuter{}).(*Query)
	assert.True(t, ok)

	outer := &SchemaOuter{}

	assert.NotPanics(t, func() {
		modeled.assignInsertedKeys(&outer, []any{uint(7)})
	})
	assert.Equal(t, uint(7), outer.ID)
}

// A nil destination must be refused rather than panicking.
func TestAssignInsertedKeyIgnoresNilDestination(t *testing.T) {
	modeled, ok := newPointerTestQuery(t).Model(&SchemaOuter{}).(*Query)
	assert.True(t, ok)

	var outer *SchemaOuter

	assert.NotPanics(t, func() {
		modeled.assignInsertedKeys(outer, []any{uint(7)})
	})
}
