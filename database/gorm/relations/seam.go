package relations

import (
	"database/sql/driver"
	"fmt"
	"reflect"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file is the relations-side half of the S2 seam widening (step 1): the plain-data predicate
// the row primitives take, and the pure helpers duplicated out of database/gorm because this
// package cannot import it (the import cycle) — each one is the measured dependency of a Tier-2
// write worker, listed in the decision table above the Builder interface in wrap.go. Behavior is
// byte-identical to the gorm original each comment names; the duplication is accepted by the
// governing directive (1:1 Laravel alignment over DRY).

// RowWhere is one raw predicate of a row primitive (UpdateRows / DeleteRows / SelectRows): the
// fragment and its bindings, appended to the table-scoped query exactly the way pivotWhere hands
// them to addWhere. The two shapes the pivot workers use: an equality passes the dialect-quoted
// column (Builder.Ident) followed by " = ?" with the value as its single argument; an IN list
// passes the unquoted qualified column (table.column) with the id slice as its single argument,
// which the compiler renders as a typed IN clause.
type RowWhere struct {
	Query string
	Args  []any
}

// IsValidParent reports whether v is a non-nil pointer to a struct — the parent / child guard
// every write shell opens with (database/gorm isValidParent, duplicated: pure reflection).
func IsValidParent(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false
	}
	return rv.Elem().Kind() == reflect.Struct
}

// DictionaryKey reduces any value to a canonical string for use as a map key, paving over the type
// mismatch between Go field types (uint, int64, string) and database-layer scan types (often int64
// or []byte). Pointer values unwrap to the value they address first, so a pointer FK keys the
// dictionary by value rather than by address, and a driver.Valuer keys on its Value(). It is
// database/gorm's dictKey, duplicated — the Go form of Eloquent's
// InteractsWithDictionary::getDictionaryKey the attach / sync diffs key their sets with.
func DictionaryKey(v any) string {
	v = dictionaryKeyValue(v)
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// valuerType is the interface a type implements to carry its own database value.
var valuerType = reflect.TypeOf((*driver.Valuer)(nil)).Elem()

// dictionaryKeyValue unwraps the pointer layers a key may carry and reduces a driver.Valuer to the
// value it keys on (database/gorm derefKeyValue, duplicated). It is kept apart from derefValue —
// the comparison helper compareRelationKeys relies on — because that one deliberately leaves a
// Valuer wrapper in place.
func dictionaryKeyValue(v any) any {
	for {
		rv := reflect.ValueOf(v)
		if !rv.IsValid() {
			return nil
		}
		if rv.Kind() == reflect.Pointer && rv.IsNil() {
			return nil
		}
		// A driver.Valuer — sql.NullString, sql.NullInt64, a custom column type — carries its
		// key in its Value(); stringifying the wrapper itself can never equal the related row's
		// key.
		if valuer, ok := v.(driver.Valuer); ok {
			return valuerKeyValue(valuer)
		}
		if rv.Kind() != reflect.Pointer {
			if reflect.PointerTo(rv.Type()).Implements(valuerType) {
				// Value() lives on the pointer receiver and the key read off the field is a copy;
				// take its address the way the binding path does.
				addr := reflect.New(rv.Type())
				addr.Elem().Set(rv)

				return valuerKeyValue(addr.Interface().(driver.Valuer))
			}

			return v
		}
		v = rv.Elem().Interface()
	}
}

// valuerKeyValue reduces a driver.Valuer to the value it keys on: nil when the wrapper is invalid
// or Value() errors — absent, exactly like a nil pointer — the string form of []byte text, and the
// driver value itself (string, int64) otherwise (database/gorm unwrapValuerValue, duplicated).
func valuerKeyValue(valuer driver.Valuer) any {
	value, err := valuer.Value()
	if err != nil || value == nil {
		return nil
	}
	if data, ok := value.([]byte); ok {
		return string(data)
	}

	return value
}

// BuilderOf unwraps a query handle to the Builder behind it: a relation object reports the builder
// it forwards to through UnwrapBuilder — walked first, because the object embeds Builder and would
// otherwise satisfy the assertion itself — and a Builder is returned as it stands. It reports false
// for any other implementation (a mock, a foreign driver, a nil handle), the way database/gorm's
// ormBuilderOf does for the concrete *OrmBuilder; the write workers' (*OrmBuilder) casts re-home
// onto this assertion.
func BuilderOf(query any) (Builder, bool) {
	if unwrapper, ok := query.(interface{ UnwrapBuilder() contractsorm.Query }); ok {
		return BuilderOf(unwrapper.UnwrapBuilder())
	}
	if built, ok := query.(Builder); ok {
		return built, true
	}

	return nil, false
}

// ModelTypeName reports the Go type name of the struct a model value leads to — pointers, slices
// and arrays unwrapped — the modelSchema.Name database/gorm's parseSchema records and the
// OrmRelationUnsupported messages of the FK writers carry ("no FK field ..." on model X). "" when
// the value leads to no struct.
func ModelTypeName(model any) string {
	valueType := reflect.TypeOf(model)
	for valueType != nil && (valueType.Kind() == reflect.Pointer || valueType.Kind() == reflect.Slice || valueType.Kind() == reflect.Array) {
		valueType = valueType.Elem()
	}
	if valueType == nil || valueType.Kind() != reflect.Struct {
		return ""
	}

	return valueType.Name()
}
