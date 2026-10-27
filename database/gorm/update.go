package gorm

import (
	"context"
	"reflect"

	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file turns the values an update accepts into the columns it sets.
//
// GORM did this in ConvertToAssignments: it read the values against its parsed schema, filtered them
// through the select and omit lists, stamped the automatic update timestamp and pulled the model's
// primary key into the where clause. All of that is reproduced here against the ORM's own model
// metadata, so an update no longer needs GORM to build its statement.

// updateAssignments turns the values accepted by Update into the column/value pairs an update sets.
//
// Three shapes are accepted, matching the ORM's own signature: a column with its value, which Update
// passes on as a one-entry map; a map whose keys name columns or fields; and a model, whose non-zero
// columns are written. The query's select and omit lists filter the result, and the model's automatic
// update timestamp is stamped on, so a row records when it was written even though the caller only
// named the columns they cared about.
func (r *Query) updateAssignments(values any) (map[string]any, error) {
	schema := r.updateSchema()
	filter, restricted := r.selectAndOmitColumns(schema)

	raw := values
	if mapping, ok := values.(map[string]any); ok && r.grammar != nil {
		compiled, err := r.grammar.CompileJsonColumnsUpdate(mapping)
		if err != nil {
			return nil, errors.OrmJsonColumnUpdateInvalid.Args(err)
		}

		raw = compiled
	}

	// reflect.Indirect panics on a nil interface, and a nil model has no columns to write either way.
	if raw == nil {
		return nil, errors.DatabaseUnsupportedType.Args("nil", "map[string]any or struct")
	}

	assignments := make(map[string]any)
	reflected := indirectValue(reflect.ValueOf(raw))

	switch reflected.Kind() {
	case reflect.Map:
		mapping, ok := asColumnMap(raw)
		if !ok {
			return nil, errors.DatabaseUnsupportedType.Args(reflected.Type().String(), "map[string]any")
		}

		for key, value := range mapping {
			column := resolveColumn(schema, key)
			if !columnIncluded(filter, restricted, column, false) {
				continue
			}

			assignments[column] = value
		}
	case reflect.Struct:
		assignFromStruct(r.ctx, assignments, filter, restricted, schema, reflected)
	default:
		return nil, errors.DatabaseUnsupportedType.Args(reflected.Type().String(), "map[string]any or struct")
	}

	addAutoUpdateTime(assignments, filter, restricted, schema)

	return assignments, nil
}

// saveAssignments returns every column a model carries, zero values included, which is what makes Save
// overwrite the row rather than merge into it. The query's Select and Omit lists still apply.
func (r *Query) saveAssignments(value any) (map[string]any, error) {
	schema := r.updateSchema()
	filter, restricted := r.selectAndOmitColumns(schema)

	if value == nil {
		return nil, errors.DatabaseUnsupportedType.Args("nil", "struct or map")
	}

	if mapping, ok := asColumnMap(value); ok {
		assignments := make(map[string]any, len(mapping))
		for key, item := range mapping {
			column := resolveColumn(schema, key)
			if !columnIncluded(filter, restricted, column, false) {
				continue
			}

			assignments[column] = item
		}

		addAutoUpdateTime(assignments, filter, restricted, schema)

		return assignments, nil
	}

	reflected := indirectValue(reflect.ValueOf(value))
	if reflected.Kind() != reflect.Struct {
		return nil, errors.DatabaseUnsupportedType.Args(reflected.Type().String(), "struct or map")
	}

	assignments := make(map[string]any)
	if schema != nil {
		for _, field := range schema.Fields {
			if field.primaryKey {
				continue
			}
			if !columnIncluded(filter, restricted, field.DBName, false) {
				continue
			}

			fieldValue, _ := field.ValueOf(r.ctx, reflected)
			assignments[field.DBName] = fieldValue
		}
	}

	addAutoUpdateTime(assignments, filter, restricted, schema)

	return assignments, nil
}

// updateSchema describes the model an update addresses, or nil when the query names none. A map-keyed
// update still reaches a table through the query's own table, so a missing model is not an error.
func (r *Query) updateSchema() *modelSchema {
	model := r.parentModel()
	if model == nil {
		return nil
	}

	schema, err := parseSchemaWithTable(model, r.declaredTable())
	if err != nil {
		return nil
	}

	return schema
}

// selectAndOmitColumns returns the query's column filter keyed by database column, and whether Select
// restricted the writable columns.
//
// Select marks a column writable and Omit marks it unwritable; the two cannot both be given, which the
// caller has already checked. Only Select restricts: naming columns narrows the update to them, while
// omitting one withholds it and leaves every other column writable. A wildcard selects everything, so
// it restricts nothing.
func (r *Query) selectAndOmitColumns(schema *modelSchema) (map[string]bool, bool) {
	columns := make(map[string]bool)
	restricted := false
	selectAll := false

	for _, item := range r.builder.Selects() {
		if column, ok := item.(string); ok {
			if column == "*" {
				selectAll = true
				continue
			}

			columns[resolveColumn(schema, column)] = true
			restricted = true
		}
	}
	if selectAll {
		restricted = false
		// A wildcard explicitly selects every field, including fields holding zero values.
		if schema != nil {
			for _, field := range schema.Fields {
				columns[field.DBName] = true
			}
		}
	}
	for _, column := range r.conditions.omit {
		columns[resolveColumn(schema, column)] = false
	}

	return columns, restricted
}

// columnIncluded reports whether an update may write a column.
//
// A column the filter names follows the filter. One it does not name is written unless Select narrowed
// the update, except for the automatic update timestamp, which is always stamped: naming one column
// should not stop the row recording when it changed.
func columnIncluded(filter map[string]bool, restricted bool, column string, auto bool) bool {
	if decision, ok := filter[column]; ok {
		return decision
	}

	return !restricted || auto
}

// columnSelected reports whether the filter names a column outright, as opposed to leaving it to the
// default: a column named by Select is written whatever it holds.
func columnSelected(filter map[string]bool, column string) bool {
	decision, ok := filter[column]

	return ok && decision
}

// resolveColumn maps a Go field name or a column name to the column the model declares, leaving a
// name the model does not know untouched, which is what a map keyed by a raw column relies on.
func resolveColumn(schema *modelSchema, name string) string {
	if schema == nil {
		return name
	}

	if field, ok := schema.FieldsByName[name]; ok {
		return field.DBName
	}
	if field, ok := schema.FieldsByDBName[name]; ok {
		return field.DBName
	}

	return name
}

// assignFromStruct reads a model's columns into the assignment set.
//
// Only fields holding a non-zero value are written, which is what makes `Update(User{Name: "x"})`
// change the name alone. The primary key is left out: it identifies the row rather than describing it,
// and the row it identifies is named by the where clause.
func assignFromStruct(ctx context.Context, assignments map[string]any, filter map[string]bool, restricted bool, schema *modelSchema, value reflect.Value) {
	if schema == nil {
		return
	}

	for _, field := range schema.Fields {
		if field.primaryKey {
			continue
		}

		fieldValue, zero := field.ValueOf(ctx, value)

		// A column the caller named outright is written even when it holds the zero value, which is how
		// Select("name").Update(model) clears a column; every other column is written only when it
		// carries something. This mirrors the assignment rule GORM applied.
		if zero && !columnSelected(filter, field.DBName) {
			continue
		}
		if !columnIncluded(filter, restricted, field.DBName, false) {
			continue
		}

		assignments[field.DBName] = fieldValue
	}
}

// addAutoUpdateTime stamps the model's automatic update timestamp on the assignment set.
//
// It is added when the model declares one and the caller supplied no value for it, so a row always
// records when it was written. A value the caller did supply is kept.
func addAutoUpdateTime(assignments map[string]any, filter map[string]bool, restricted bool, schema *modelSchema) {
	if schema == nil {
		return
	}

	now := carbon.NewDateTime(carbon.Now())

	for _, field := range schema.Fields {
		if field.AutoUpdateTime == 0 {
			continue
		}
		if !columnIncluded(filter, restricted, field.DBName, true) {
			continue
		}
		if value, ok := assignments[field.DBName]; ok && value != nil {
			continue
		}

		assignments[field.DBName] = now
	}
}

// asColumnMap reads a value as a column-keyed map, the shape the ORM accepts alongside a model.
func asColumnMap(value any) (map[string]any, bool) {
	if mapping, ok := value.(map[string]any); ok {
		return mapping, true
	}

	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String {
		return nil, false
	}

	mapping := make(map[string]any, reflected.Len())
	iterator := reflected.MapRange()
	for iterator.Next() {
		mapping[iterator.Key().String()] = iterator.Value().Interface()
	}

	return mapping, true
}
