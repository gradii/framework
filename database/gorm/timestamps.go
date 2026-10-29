package gorm

import (
	"reflect"
	"time"

	"github.com/gradii/framework/database/internal/scan"
	"github.com/gradii/framework/support/carbon"
)

func timestampPointer(typ reflect.Type) bool {
	if typ.Kind() != reflect.Pointer {
		return false
	}
	base := indirectType(typ)
	return base.ConvertibleTo(reflect.TypeFor[time.Time]()) || base == reflect.TypeFor[carbon.DateTime]()
}

// Replace the field rather than scanning into its existing pointer. A caller
// may have supplied the same pointer for CreatedAt, UpdatedAt, or another model.
func assignTimestampPointer(target reflect.Value, source any) error {
	value := reflect.ValueOf(source)
	if value.IsValid() && value.Type() == target.Type() {
		if value.IsNil() {
			source = nil
		} else {
			// A direct pointer assignment would retain the caller's alias.
			source = value.Elem().Interface()
		}
	}
	replacement := reflect.New(target.Type()).Elem()
	if err := scan.Assign(replacement, source); err != nil {
		return err
	}
	target.Set(replacement)
	return nil
}

// timestampValue builds a value in the model field's type. Each invocation
// owns its pointers, even when several fields use the same clock instant.
func (f *schemaField) timestampValue(now time.Time) (any, error) {
	value := reflect.New(f.FieldType).Elem()
	var source any = now
	// Existing integer timestamp models store Unix seconds.
	switch indirectType(f.FieldType).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		source = now.Unix()
	}
	if err := scan.Assign(value, source); err != nil {
		return nil, err
	}
	return value.Interface(), nil
}

// Only automatic timestamp fields use these semantic zero rules. Invalid
// Carbon values retain their error instead of being silently replaced by now.
func zeroTimestamp(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case time.Time:
		return value.IsZero()
	case *time.Time:
		return value == nil || value.IsZero()
	case carbon.DateTime:
		return value.Carbon == nil || (value.Error == nil && value.StdTime().IsZero())
	case *carbon.DateTime:
		return value == nil || value.Carbon == nil || (value.Error == nil && value.StdTime().IsZero())
	default:
		return reflect.ValueOf(value).IsZero()
	}
}

// Save already obtained ordinary assignments from the model itself. Only its
// automatic update fields may have acquired a new value while preparing SQL.
func (r *Query) assignSavedTimestamps(model any, assignments map[string]any) error {
	value := reflect.ValueOf(model)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil
	}
	schema, err := parseSchemaWithTable(model, r.declaredTable())
	if err != nil {
		return err
	}
	for _, field := range schema.Fields {
		if field.AutoUpdateTime == 0 {
			continue
		}
		stamp, exists := assignments[field.DBName]
		if !exists {
			continue
		}
		if err := field.Set(r.ctx, value, stamp); err != nil {
			return err
		}
	}
	return nil
}
