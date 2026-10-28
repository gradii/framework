package gorm

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/gradii/framework/database/types"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bindingDocument struct{ Data string }

func (v *bindingDocument) Value() (driver.Value, error) { return v.Data, nil }
func (v *bindingDocument) Scan(source any) error {
	if text, ok := source.(string); ok {
		v.Data = text
		return nil
	}
	return assert.AnError
}

type BindingEmbed struct {
	Document bindingDocument `gorm:"column:document"`
}
type bindingRecord struct{ *BindingEmbed }

func (bindingRecord) TableName() string { return "binding_records" }

func TestColumnBindingUsesReflectTraversalAndPointerMethodSet(t *testing.T) {
	schema, err := parseSchema(bindingRecord{})
	require.NoError(t, err)
	field := schema.FieldsByDBName["document"]
	record := bindingRecord{}
	value, zero := field.BindingValueOf(context.Background(), reflect.ValueOf(&record))
	require.Nil(t, value)
	require.True(t, zero)
	require.Nil(t, record.BindingEmbed, "reading a binding must not allocate an embedded pointer")
	record.BindingEmbed = &BindingEmbed{Document: bindingDocument{Data: `{"custom":true}`}}
	for _, source := range []any{record, &record} {
		value, _ = field.BindingValueOf(context.Background(), reflect.ValueOf(source))
		valuer, ok := value.(driver.Valuer)
		require.True(t, ok)
		bound, err := valuer.Value()
		require.NoError(t, err)
		require.Equal(t, `{"custom":true}`, bound)
	}
	type plain struct{ Document bindingDocument }
	plainSchema, err := parseSchemaWithTable(plain{}, "items")
	require.NoError(t, err)
	value, _ = plainSchema.Fields[0].BindingValueOf(context.Background(), reflect.ValueOf(plain{Document: bindingDocument{Data: "value model"}}))
	bound, err := value.(driver.Valuer).Value()
	require.NoError(t, err)
	require.Equal(t, "value model", bound)
}

func TestSerializerTagRequiresStandardInterfaces(t *testing.T) {
	type unsupported struct {
		Payload struct{ Name string } `gorm:"column:payload;serializer:json"`
	}
	_, err := parseSchemaWithTable(unsupported{}, "users")
	require.ErrorIs(t, err, errors.OrmSerializerRequiresInterfaces)
	type supported struct {
		Payload *bindingDocument `gorm:"column:payload;serializer:json"`
	}
	_, err = parseSchemaWithTable(supported{}, "users")
	require.NoError(t, err)
}

func TestSchemaFieldSetHonorsScannerAndPreservesItsError(t *testing.T) {
	type row struct {
		Payload  *bindingDocument
		Document types.JSON
	}
	schema, err := parseSchemaWithTable(row{}, "users")
	require.NoError(t, err)
	value := row{}
	field := schema.FieldsByDBName["payload"]
	require.ErrorIs(t, field.Set(context.Background(), reflect.ValueOf(&value), 17), assert.AnError)
	require.Nil(t, value.Payload)
	require.NoError(t, field.Set(context.Background(), reflect.ValueOf(&value), "text"))
	require.Equal(t, "text", value.Payload.Data)
	require.NoError(t, field.Set(context.Background(), reflect.ValueOf(&value), nil))
	require.Nil(t, value.Payload)
	jsonField := schema.FieldsByDBName["document"]
	require.Error(t, jsonField.Set(context.Background(), reflect.ValueOf(&value), []byte("invalid")))
	require.Nil(t, value.Document)
}
