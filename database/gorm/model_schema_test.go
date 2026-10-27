package gorm

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type SchemaInner struct {
	CreatedAt time.Time `gorm:"column:created_at"`
}

type SchemaOuter struct {
	ID uint `gorm:"primaryKey"`
	SchemaInner
	Name    string
	Skipped string `gorm:"-"`
}

func (SchemaOuter) TableName() string { return "schema_outers" }

type SchemaPointerInner struct {
	Code string `gorm:"column:code"`
}

type SchemaPointerOuter struct {
	ID uint
	*SchemaPointerInner
}

func (SchemaPointerOuter) TableName() string { return "schema_pointer_outers" }

func TestParseModelSchema_FlattensEmbeddedFieldsInPlace(t *testing.T) {
	schema, err := parseSchema(&SchemaOuter{})
	assert.NoError(t, err)

	assert.Equal(t, "SchemaOuter", schema.Name)
	assert.Equal(t, "schema_outers", schema.Table)

	columns := make([]string, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		columns = append(columns, field.DBName)
	}

	// The embedded struct contributes its column where it is declared, and gorm:"-" fields are left
	// out entirely.
	assert.Equal(t, []string{"id", "created_at", "name"}, columns)
	assert.Contains(t, schema.FieldsByDBName, "created_at")
	assert.NotContains(t, schema.FieldsByDBName, "skipped")
}

func TestParseModelSchema_PointerEmbed(t *testing.T) {
	schema, err := parseSchema(&SchemaPointerOuter{})
	assert.NoError(t, err)

	columns := make([]string, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		columns = append(columns, field.DBName)
	}
	assert.Equal(t, []string{"id", "code"}, columns)

	code := schema.FieldsByDBName["code"]
	assert.NotNil(t, code)

	// A nil embedded pointer reports the zero value rather than panicking.
	value, zero := code.ValueOf(context.Background(), reflect.ValueOf(&SchemaPointerOuter{}))
	assert.True(t, zero)
	assert.Nil(t, value)

	populated := &SchemaPointerOuter{SchemaPointerInner: &SchemaPointerInner{Code: "abc"}}
	value, zero = code.ValueOf(context.Background(), reflect.ValueOf(populated))
	assert.False(t, zero)
	assert.Equal(t, "abc", value)
}

func TestSchemaField_ValueOfReportsZero(t *testing.T) {
	schema, err := parseSchema(&SchemaOuter{})
	assert.NoError(t, err)

	name := schema.FieldsByDBName["name"]

	_, zero := name.ValueOf(context.Background(), reflect.ValueOf(&SchemaOuter{}))
	assert.True(t, zero)

	value, zero := name.ValueOf(context.Background(), reflect.ValueOf(&SchemaOuter{Name: "John"}))
	assert.False(t, zero)
	assert.Equal(t, "John", value)
}

func TestSchemaField_SetConvertsValues(t *testing.T) {
	schema, err := parseSchema(&SchemaOuter{})
	assert.NoError(t, err)

	target := &SchemaOuter{}

	// A value whose type differs but converts, as the pivot hydration produces.
	assert.NoError(t, schema.FieldsByDBName["id"].Set(context.Background(), reflect.ValueOf(target), uint64(7)))
	assert.Equal(t, uint(7), target.ID)

	assert.NoError(t, schema.FieldsByDBName["name"].Set(context.Background(), reflect.ValueOf(target), "Jane"))
	assert.Equal(t, "Jane", target.Name)

	// nil clears the field instead of failing.
	assert.NoError(t, schema.FieldsByDBName["name"].Set(context.Background(), reflect.ValueOf(target), nil))
	assert.Equal(t, "", target.Name)
}

func TestSchemaField_SetThroughPointerEmbed(t *testing.T) {
	schema, err := parseSchema(&SchemaPointerOuter{})
	assert.NoError(t, err)

	target := &SchemaPointerOuter{}
	assert.NoError(t, schema.FieldsByDBName["code"].Set(context.Background(), reflect.ValueOf(target), "xyz"))

	assert.NotNil(t, target.SchemaPointerInner)
	assert.Equal(t, "xyz", target.Code)
}

func TestParseModelSchema_RejectsNonStructs(t *testing.T) {
	_, err := parseSchema(nil)
	assert.Error(t, err)

	_, err = parseSchema(123)
	assert.Error(t, err)
}
