package gorm

import (
	"context"
	"reflect"
	"time"

	"github.com/goravel/framework/database/internal/annotation"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/str"
)

// modelSchema describes how a model maps to a table.
//
// It replaces gorm.io/gorm/schema for the ORM's own use. Only the members the ORM actually reads are
// implemented: the table, the columns in declaration order, a lookup by column name, and reading
// and writing a column's value on a model value. GORM's richer metadata — data types, sizes,
// defaults, serializers, index definitions and relationship parsing — is deliberately absent,
// because the ORM never consults it.
type modelSchema struct {
	// Name is the Go type name of the model, used in diagnostics.
	Name string
	// Table is the table the model maps to, without the connection prefix.
	Table string
	// Fields are the model's columns, in declaration order with embedded structs flattened in
	// place. Non-column fields, such as relations tagged `gorm:"-"`, are left out.
	Fields []*schemaField
	// FieldsByDBName looks a column up by its database name.
	FieldsByDBName map[string]*schemaField
	// FieldsByName looks a column up by its Go field name.
	FieldsByName map[string]*schemaField
	// PrimaryFields are the model's primary key columns, in declaration order. A model that tags no
	// field falls back to its "id" column, which is the convention every model here follows.
	PrimaryFields []*schemaField
}

// schemaField is one column of a model.
type schemaField struct {
	// primaryKey is filled in by collectPrimaryFields once every field is known.
	primaryKey bool
	// Name is the Go field name.
	Name string
	// DBName is the column the field maps to.
	DBName string
	// FieldType is the Go type of the field.
	FieldType reflect.Type
	// AutoCreateTime is non-zero when the field is stamped on insert, and AutoUpdateTime when it is
	// stamped on update as well. Their values are only ever compared against zero, so they record
	// "stamped" rather than a specific unit.
	AutoCreateTime int
	AutoUpdateTime int
	// index is the path from the struct root to the field. A negative entry stands for a pointer
	// that has to be dereferenced on the way down, which is how embedded pointer structs are
	// reached.
	index []int
}

// parseSchema builds a model's schema. The table comes from the model's own declaration,
// exactly as the ORM resolves it elsewhere.
func parseSchema(model any) (*modelSchema, error) {
	return parseSchemaWithTable(model, "")
}

// parseSchemaWithTable builds a model's schema, using table when the caller has already resolved
// one. An explicit Table("name") on the query is that case: the caller named the table on the
// query, so the model does not have to declare TableName() as well — the same exemption the read
// path already gives through applyTable's short-circuit. With an empty table the model's own
// declaration is required, exactly as before.
func parseSchemaWithTable(model any, table string) (*modelSchema, error) {
	// reflect.TypeOf(nil) is nil, so a nil model has to be rejected before its type is described.
	if model == nil {
		return nil, errors.OrmQueryInvalidModel.Args("nil")
	}

	modelType := indirectStructType(reflect.TypeOf(model))
	if modelType == nil {
		return nil, errors.OrmQueryInvalidModel.Args(reflect.TypeOf(model).String())
	}

	// The table is resolved through the same path the queries use, so a model that declares no
	// TableName() fails with the same named error the queries report — unless the caller already
	// named the table, in which case there is nothing to resolve.
	if table == "" {
		resolved, err := tableNameFor(model)
		if err != nil {
			return nil, err
		}
		table = resolved
	}

	schema := &modelSchema{
		Name:           modelType.Name(),
		Table:          table,
		FieldsByDBName: make(map[string]*schemaField),
		FieldsByName:   make(map[string]*schemaField),
	}

	collectSchemaFields(schema, modelType, nil)
	collectPrimaryFields(schema)

	return schema, nil
}

// collectSchemaFields walks a struct, appending its columns. prefix carries the path already
// travelled, so an embedded struct's columns resolve against the outer value.
func collectSchemaFields(schema *modelSchema, modelType reflect.Type, prefix []int) {
	for position := 0; position < modelType.NumField(); position++ {
		field := modelType.Field(position)
		if !field.IsExported() {
			continue
		}

		annotation := annotation.ParseAnnotation(field.Tag.Get("gorm"))
		if annotation.Skip {
			continue
		}

		primaryKey := annotation.PrimaryKey

		// A pointer embed is reached by dereferencing, which the negative index records.
		if field.Type.Kind() == reflect.Pointer {
			if embedded := indirectStructType(field.Type); field.Anonymous && embedded != nil && !isColumnType(field.Type) {
				collectSchemaFields(schema, embedded, append(append([]int(nil), prefix...), -position-1))

				continue
			}
		} else if field.Anonymous && !isColumnType(field.Type) {
			collectSchemaFields(schema, field.Type, append(append([]int(nil), prefix...), position))

			continue
		}

		column := annotation.Column
		if column == "" {
			column = str.Of(field.Name).Snake().String()
		}

		schemaField := &schemaField{
			primaryKey: primaryKey,
			Name:       field.Name,
			DBName:     column,
			FieldType:  field.Type,
			index:      append(append([]int(nil), prefix...), position),
		}
		if annotation.AutoCreateTime {
			schemaField.AutoCreateTime = stampedField
		}
		if annotation.AutoUpdateTime {
			schemaField.AutoUpdateTime = stampedField
		}

		schema.Fields = append(schema.Fields, schemaField)
		schema.FieldsByDBName[column] = schemaField
		schema.FieldsByName[field.Name] = schemaField
	}
}

// stampedField marks a field the ORM stamps automatically. Only its non-zero-ness is read.
const stampedField = 1

// collectPrimaryFields records the model's primary key columns, falling back to the "id" column
// when no field is tagged, which is how the models in this framework are written.
func collectPrimaryFields(schema *modelSchema) {
	for _, field := range schema.Fields {
		if field.primaryKey {
			schema.PrimaryFields = append(schema.PrimaryFields, field)
		}
	}

	if len(schema.PrimaryFields) > 0 {
		return
	}

	if idField, ok := schema.FieldsByDBName["id"]; ok {
		idField.primaryKey = true
		schema.PrimaryFields = append(schema.PrimaryFields, idField)
	}
}

// isColumnType reports whether a type is stored in a column rather than flattened into its own
// columns: a time, or a type that carries its database value itself.
func isColumnType(fieldType reflect.Type) bool {
	if isTimeType(fieldType) {
		return true
	}

	fieldType = indirectType(fieldType)

	return valuerValueType(fieldType) != nil || reflect.PointerTo(fieldType).Implements(valuerInterface) || fieldType.Implements(valuerInterface)
}

// ValueOf reads the field's value from a model value, reporting whether it holds the zero value.
func (f *schemaField) ValueOf(_ context.Context, value reflect.Value) (any, bool) {
	value = indirectValue(value)

	for place, index := range f.index {
		if index >= 0 {
			value = value.Field(index)

			continue
		}

		value = value.Field(-index - 1)
		if value.IsNil() {
			return nil, true
		}
		if place < len(f.index)-1 {
			value = value.Elem()
		}
	}

	return value.Interface(), value.IsZero()
}

// ReflectValueOf returns the settable field on a model value, allocating any pointer it has to pass
// through. A pointer that ends the path is allocated but not dereferenced, so setting through it
// writes to the pointer field itself.
func (f *schemaField) ReflectValueOf(value reflect.Value) reflect.Value {
	value = indirectValue(value)
	if !value.IsValid() || value.Kind() != reflect.Struct {
		// A nil or unrecognised destination cannot be written to; Set treats the invalid value as
		// "not settable" and returns without touching anything.
		return reflect.Value{}
	}

	for place, index := range f.index {
		if index >= 0 {
			value = value.Field(index)

			continue
		}

		value = value.Field(-index - 1)
		if value.IsNil() {
			value.Set(reflect.New(value.Type().Elem()))
		}
		if place < len(f.index)-1 {
			value = value.Elem()
		}
	}

	return value
}

// scanSourceInto loads source into target through target's own Scan method, reporting whether
// the target carried one and accepted the value.
func scanSourceInto(target reflect.Value, source any) bool {
	scanner, ok := target.Interface().(interface{ Scan(any) error })
	if !ok {
		return false
	}

	return scanner.Scan(source) == nil
}

// Set writes a value into the field, converting it when the Go types differ.
func (f *schemaField) Set(_ context.Context, value reflect.Value, source any) error {
	target := f.ReflectValueOf(value)
	if !target.CanSet() {
		return nil
	}

	if source == nil {
		target.Set(reflect.Zero(target.Type()))

		return nil
	}

	sourceValue := reflect.ValueOf(source)
	sourceType := sourceValue.Type()

	if sourceType.AssignableTo(target.Type()) {
		if sourceValue.Kind() == reflect.Pointer && sourceValue.Elem().Kind() == reflect.Pointer {
			sourceValue = reflect.Indirect(sourceValue)
		}

		target.Set(sourceValue)

		return nil
	}

	if sourceType.ConvertibleTo(target.Type()) {
		target.Set(sourceValue.Convert(target.Type()))

		return nil
	}

	if target.Type().Kind() == reflect.Pointer {
		elementType := target.Type().Elem()
		pointer := reflect.New(elementType)

		switch {
		case sourceType.AssignableTo(elementType):
			pointer.Elem().Set(sourceValue)
		case sourceType.ConvertibleTo(elementType):
			pointer.Elem().Set(sourceValue.Convert(elementType))
		case sourceType == reflect.TypeOf(time.Time{}) && scanSourceInto(pointer, source):
			// A column type that carries its own database representation — the framework's
			// carbon timestamps (*carbon.DateTime and its siblings) — loads a plain time.Time
			// through its own Scan, exactly as the driver loads such a column. time.Time is
			// neither assignable nor convertible to that type, so without this the pointer
			// branch would fall through to the mismatch error.
		default:
			return errors.OrmFieldTypeMismatch.Args(sourceType.String(), f.Name, target.Type().String())
		}

		target.Set(pointer)

		return nil
	}

	return errors.OrmFieldTypeMismatch.Args(sourceType.String(), f.Name, target.Type().String())
}
