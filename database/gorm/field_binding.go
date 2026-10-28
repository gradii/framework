package gorm

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"reflect"

	"github.com/gradii/framework/database/internal/reflectx"
	"github.com/gradii/framework/database/types"
)

// BindingValueOf preserves the pointer method set of a column when extracting
// SQL parameters. Relation keys and other reflection consumers still use ValueOf.
func (f *schemaField) BindingValueOf(_ context.Context, model reflect.Value) (any, bool) {
	value := reflectx.FieldByIndexesReadOnly(model, f.index)
	if !value.IsValid() {
		return nil, true
	}

	zero := value.IsZero()
	if _, ok := value.Interface().(driver.Valuer); ok {
		return f.bindValue(value.Interface()), zero
	}
	if reflect.PointerTo(value.Type()).Implements(valuerInterface) {
		if value.CanAddr() {
			return f.bindValue(value.Addr().Interface()), zero
		}
		copy := reflect.New(value.Type())
		copy.Elem().Set(value)
		return f.bindValue(copy.Interface()), zero
	}
	return f.bindValue(value.Interface()), zero
}

// Explicit JSON columns carry text, even if the Valuer returns bytes. In
// particular SQL Server otherwise converts varbinary to nvarchar as UTF-16.
// This adapts a standard SQL value; it does not serialize arbitrary structs.
func (f *schemaField) bindValue(value any) any {
	if value != nil {
		if _, ok := value.(driver.Valuer); !ok && reflect.PointerTo(reflect.TypeOf(value)).Implements(valuerInterface) {
			copy := reflect.New(reflect.TypeOf(value))
			copy.Elem().Set(reflect.ValueOf(value))
			value = copy.Interface()
		}
	}
	if !f.jsonColumn {
		return value
	}
	if valuer, ok := value.(driver.Valuer); ok {
		return jsonColumnValuer{valuer}
	}
	if data, ok := value.([]byte); ok {
		return types.JSON(data)
	}
	return value
}

type jsonColumnValuer struct{ driver.Valuer }

func (v jsonColumnValuer) Value() (driver.Value, error) {
	value, err := driver.DefaultParameterConverter.ConvertValue(v.Valuer)
	if err != nil || value == nil {
		return value, err
	}
	switch data := value.(type) {
	case []byte:
		return types.JSON(data).Value()
	case string:
		return types.JSON(data).Value()
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return string(encoded), nil
	}
}

func modelColumnBinding(schema *modelSchema, column string, value any) any {
	if schema != nil {
		if field := schema.FieldsByDBName[column]; field != nil {
			return field.bindValue(value)
		}
	}
	return value
}
