package gorm

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"time"

	gormio "gorm.io/gorm"
	gormschema "gorm.io/gorm/schema"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
	basedatabase "github.com/goravel/framework/support/database"
)

// This file holds the framework's own model metadata resolution. It replaces the parts of
// gorm.io/gorm/schema the ORM used to rely on: a model's columns, its soft-delete column and its
// table name are now derived here.
//
// Table names come from the model itself through orm.ModelWithTableName rather than from a naming
// strategy, so no pluralizer is involved: a model states the table it maps to, and `make:model`
// writes that method into every generated model. Column names keep following the
// `gorm:"column:..."` convention, because the struct tags stay the mapping mechanism by choice.

// valuerInterface is the interface a type implements to carry its own database value.
var valuerInterface = reflect.TypeOf((*driver.Valuer)(nil)).Elem()

// declaredTableName returns the table name the model declares through TableName(), or "" when it
// declares none.
//
// Both receiver forms are honoured. A pointer reaches a value-receiver method through its method
// set, and a value reaches a pointer-receiver method through a fresh addressable copy, which is
// why the value case allocates one. Extra pointer levels are walked down as well: the insert paths
// accept a pointer to a pointer, and the model underneath is the one that declares the table. A
// slice or array names the table of the model it carries, because a query's destination is a slice
// of models often enough — Paginate takes one — and the element is the model that declares it.
func declaredTableName(model any) string {
	if model == nil {
		return ""
	}

	// A typed nil that reaches an interface is non-nil as an interface value, so the assertion below
	// would succeed and call TableName() on a nil receiver - which panics when the method dereferences
	// its receiver. The nil check therefore has to precede the assertion, not follow it.
	value := reflect.ValueOf(model)
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}

	if named, ok := model.(contractsorm.ModelWithTableName); ok {
		return strings.TrimSpace(named.TableName())
	}

	if named, ok := value.Interface().(contractsorm.ModelWithTableName); ok {
		return strings.TrimSpace(named.TableName())
	}

	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		// The element may itself be a pointer ([]*T), so allocate a zero T and let the recursive call
		// walk it; allocating a *T would hand the next call a typed nil and lose the declaration.
		element := value.Type().Elem()
		for element.Kind() == reflect.Pointer {
			element = element.Elem()
		}

		return declaredTableName(reflect.New(element).Interface())
	}

	if value.Kind() == reflect.Struct {
		if named, ok := reflect.New(value.Type()).Interface().(contractsorm.ModelWithTableName); ok {
			return strings.TrimSpace(named.TableName())
		}
	}

	return ""
}

// modelColumnName maps a struct field to its database column: the `column:` element of the gorm
// tag when present, otherwise the snake_cased field name.
//
// It delegates to the event helpers so the ORM keeps a single column-naming rule rather than two
// that can drift apart.
func modelColumnName(field reflect.StructField) string {
	return structNameToDbColumnName(field.Name, field.Tag.Get("gorm"))
}

// softDeleteColumn returns the database column of the model's soft-delete field, or false when the
// model has none. Callers use that to decide whether a soft-delete predicate belongs in a query.
func softDeleteColumn(model any) (string, bool) {
	field, ok := findStructField(model, func(candidate reflect.StructField) bool {
		return isSoftDeleteType(candidate.Type)
	})
	if !ok {
		return "", false
	}

	return modelColumnName(field), true
}

// softDeleteFieldType returns the type of the model's soft-delete field, or false when the model
// declares none.
func softDeleteFieldType(model any) (reflect.Type, bool) {
	field, ok := findStructField(model, func(candidate reflect.StructField) bool {
		return isSoftDeleteType(candidate.Type)
	})
	if !ok {
		return nil, false
	}

	return field.Type, true
}

// isSoftDeleteType reports whether a field marks soft deletion: the framework's own DeletedAt, or
// gorm.DeletedAt for models that still declare it.
func isSoftDeleteType(fieldType reflect.Type) bool {
	return fieldType == reflect.TypeOf(gormio.DeletedAt{}) || fieldType == reflect.TypeOf(basedatabase.DeletedAt{})
}

// findStructField locates the first exported field satisfying predicate, descending into embedded
// structs so composed base structs such as SoftDeletes and Timestamps are reachable.
func findStructField(model any, predicate func(reflect.StructField) bool) (reflect.StructField, bool) {
	structType := indirectStructType(reflect.TypeOf(model))
	if structType == nil {
		return reflect.StructField{}, false
	}

	return findFieldIn(structType, predicate)
}

func findFieldIn(structType reflect.Type, predicate func(reflect.StructField) bool) (reflect.StructField, bool) {
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if !field.IsExported() {
			continue
		}

		if predicate(field) {
			return field, true
		}

		if !field.Anonymous {
			continue
		}

		embedded := field.Type
		if embedded.Kind() == reflect.Pointer {
			embedded = embedded.Elem()
		}
		if embedded.Kind() != reflect.Struct {
			continue
		}

		if found, ok := findFieldIn(embedded, predicate); ok {
			return found, true
		}
	}

	return reflect.StructField{}, false
}

// indirectStructType unwraps pointers, slices and arrays down to the struct type they carry,
// returning nil when the value does not lead to a struct.
func indirectStructType(valueType reflect.Type) reflect.Type {
	for valueType != nil && (valueType.Kind() == reflect.Pointer || valueType.Kind() == reflect.Slice || valueType.Kind() == reflect.Array) {
		valueType = valueType.Elem()
	}

	if valueType == nil || valueType.Kind() != reflect.Struct {
		return nil
	}

	return valueType
}

// indirectType unwraps pointers and arrays down to the type they carry.
func indirectType(fieldType reflect.Type) reflect.Type {
	for fieldType != nil && (fieldType.Kind() == reflect.Pointer || fieldType.Kind() == reflect.Array) {
		fieldType = fieldType.Elem()
	}

	return fieldType
}

// isTimeType reports whether a type holds a time.Time, directly or as its underlying type.
func isTimeType(fieldType reflect.Type) bool {
	fieldType = indirectType(fieldType)
	if fieldType == nil {
		return false
	}

	return fieldType == reflect.TypeOf(time.Time{}) || fieldType.ConvertibleTo(reflect.TypeOf(time.Time{}))
}

// valuerValueType returns the type a struct carries as its database value, or nil when the struct is
// not a driver.Valuer.
//
// The carried type is found by walking the struct's fields in declaration order and taking the
// first scalar or time-like one. Calling Value() would not work, because a zero value such as
// sql.NullString reports nil and would tell us nothing. The walk descends through nested structs and
// pointers and reads unexported fields, since a wrapper is routinely a struct embedding a pointer to
// the type that actually stores the value.
func valuerValueType(fieldType reflect.Type) reflect.Type {
	if !fieldType.Implements(valuerInterface) && !reflect.PointerTo(fieldType).Implements(valuerInterface) {
		return nil
	}

	return carriedType(fieldType, 0)
}

// carriedType finds the scalar or time a type stores, descending through structs and pointers.
// The depth bound keeps a self-referential type from looping forever.
func carriedType(fieldType reflect.Type, depth int) reflect.Type {
	if fieldType == nil || depth > 4 {
		return nil
	}

	fieldType = indirectType(fieldType)
	if fieldType.Kind() != reflect.Struct {
		return nil
	}

	for index := 0; index < fieldType.NumField(); index++ {
		candidate := fieldType.Field(index).Type

		switch indirectType(candidate).Kind() {
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64, reflect.String:
			return candidate
		case reflect.Struct:
			if isTimeType(candidate) {
				return candidate
			}
			if carried := carriedType(candidate, depth+1); carried != nil {
				return carried
			}
		case reflect.Pointer, reflect.Slice:
			if indirectType(candidate).Kind() == reflect.Struct && isTimeType(candidate) {
				return candidate
			}
			if carried := carriedType(candidate, depth+1); carried != nil {
				return carried
			}
		}
	}

	return nil
}

// tablePrefix reads the connection's table prefix from the naming strategy GORM is configured
// with, which is where the prefix lives while GORM still owns the connection.
func tablePrefix(db *gormio.DB) string {
	if db == nil || db.Config == nil {
		return ""
	}

	if naming, ok := db.Config.NamingStrategy.(gormschema.NamingStrategy); ok {
		return naming.TablePrefix
	}

	return ""
}
