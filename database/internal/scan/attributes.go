package scan

import (
	"reflect"

	"github.com/goravel/framework/database/internal/reflectx"
	"github.com/goravel/framework/errors"
)

// Attributes also accepts nested Go maps and collections produced by factories.
// It shares cached field paths with row mapping; SQL rows use Map or Row.
func Attributes(attributes map[string]any, dest any, mapper *reflectx.Mapper) error {
	return mapWith(attributes, dest, mapper, func(target reflect.Value, source any) error {
		return assignAttribute(target, source, mapper)
	})
}

func assignAttribute(target reflect.Value, source any, mapper *reflectx.Mapper) error {
	data := reflect.ValueOf(source)
	if !data.IsValid() {
		return Assign(target, source)
	}
	for data.Kind() == reflect.Pointer || data.Kind() == reflect.Interface {
		if data.IsNil() {
			return Assign(target, source)
		}
		data = data.Elem()
	}

	targetType := reflectx.Deref(target.Type())
	if data.Kind() == reflect.Map && data.Type().Key().Kind() == reflect.String &&
		(targetType.Kind() == reflect.Struct || (targetType.Kind() == reflect.Map && targetType.Key().Kind() == reflect.String)) {
		if data.IsNil() {
			return Assign(target, source)
		}
		attributes := make(map[string]any, data.Len())
		iterator := data.MapRange()
		for iterator.Next() {
			attributes[iterator.Key().String()] = iterator.Value().Interface()
		}
		return Attributes(attributes, target.Addr().Interface(), mapper)
	}

	if (data.Kind() == reflect.Slice || data.Kind() == reflect.Array) &&
		(targetType.Kind() == reflect.Slice || targetType.Kind() == reflect.Array) {
		// Bytes retain the binary and JSON conversion rules used by row reads.
		if data.Kind() == reflect.Slice && (data.IsNil() || data.Type().Elem().Kind() == reflect.Uint8) {
			return Assign(target, source)
		}
		var collection reflect.Value
		if targetType.Kind() == reflect.Slice {
			collection = reflect.MakeSlice(targetType, data.Len(), data.Len())
		} else {
			if data.Len() > targetType.Len() {
				return errors.OrmRowScanValueNotRepresentable.Args(source, data.Type(), target.Type())
			}
			collection = reflect.New(targetType).Elem()
		}
		for index := 0; index < data.Len(); index++ {
			if err := assignAttribute(collection.Index(index), data.Index(index).Interface(), mapper); err != nil {
				return err
			}
		}
		return Assign(target, collection.Interface())
	}

	return Assign(target, source)
}
