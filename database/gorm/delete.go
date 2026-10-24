package gorm

import (
	"reflect"

	contractsdb "github.com/goravel/framework/contracts/database/db"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/database/query/core"
	basedatabase "github.com/goravel/framework/support/database"
)

// This file removes rows through the framework's own compiler.
//
// GORM used to run the statement and, before that, decide whether a delete was really an update:
// it recognised its own DeletedAt type and rewrote the statement for it. The framework's models
// carry support/database.DeletedAt, which implements none of those clause interfaces, so the
// decision is made here and the statement is compiled by database/query.

// deleteOrSoftDelete removes rows, rewriting the statement into a soft-delete update when the model
// declares the framework's soft-delete type.
func (r *Query) deleteOrSoftDelete() (*contractsdb.Result, error) {
	column, ok := r.softDeleteColumn()
	if !ok {
		return r.execDelete()
	}

	// The update is scoped the way a delete is, by the destination's key, because Delete(&user)
	// names the row through its destination rather than through Model().
	return r.updateScoped(map[string]any{column: r.softDeleteValue()}, true)
}

// softDeleteColumn returns the column a soft delete stamps, when this query's delete is rewritten
// into one.
//
// The rewrite is skipped when the caller asked for the trashed rows too, when the statement was
// unscoped, and when the model declares GORM's own soft-delete type, whose clause interfaces still
// decide that case.
func (r *Query) softDeleteColumn() (string, bool) {
	if r.conditions.withTrashed || isUnscoped(r.instance) || !r.usesFrameworkSoftDelete() {
		return "", false
	}

	model := r.parentModel()
	if model == nil {
		return "", false
	}

	return softDeleteColumn(model)
}

// softDeleteValue is the value a soft delete stamps on the row: the column's own type carrying the
// current time, so the driver writes the representation an insert would.
func (r *Query) softDeleteValue() basedatabase.DeletedAt {
	return basedatabase.DeletedAt{Time: r.instance.Config.NowFunc(), Valid: true}
}

// execDelete compiles the query as a delete and runs it.
func (r *Query) execDelete() (*contractsdb.Result, error) {
	statement, bindings, err := r.compileDelete()
	if err != nil {
		return nil, err
	}

	return r.execStatement(statement, bindings)
}

// hasWhereConditions reports whether the query's conditions constrain the rows the statement
// addresses, by a where clause, a relationship existence condition, or a raw statement.
//
// It deliberately ignores the soft-delete predicate: that one belongs to the model rather than to the
// call, and is never recorded here — applySoftDelete adds it to the compiled builder — so it does not
// make an otherwise unrestricted statement safe. A global scope's constraints are recorded here
// before the guard runs (compileDelete and compileUpdateScoped fold the scopes in first), which is why
// they do satisfy it.
func (r *Query) hasWhereConditions() bool {
	return len(r.conditions.where) > 0 || len(r.conditions.relations) > 0 || r.conditions.raw != nil
}

// applyUpdateKeyWhere scopes an update to the row its model identifies.
//
// GORM added this itself while building the assignments: when the query addressed a model carrying a
// key and the values being written were not that same model, the key joined the where clause. That is
// what makes `Model(user).Update(...)` touch one row, and what `Update("name", "x")` relied on.
func (r *Query) applyUpdateKeyWhere(builder *querybuilder.Builder) bool {
	if r.conditions.model == nil || sameModel(r.conditions.dest, r.conditions.model) {
		return false
	}

	return r.applyKeyWhere(builder, r.conditions.model)
}

// applyDeleteKeyWhere scopes a delete to the rows its destination and its model identify.
//
// A delete is named two ways, and GORM honoured both: `Delete(&user)` names the row through the
// destination, while `Model(user).Delete()` names it through the model. The destination is applied
// first, and the model joins it only when it is a different value.
func (r *Query) applyDeleteKeyWhere(builder *querybuilder.Builder) bool {
	applied := false

	if r.conditions.dest != nil {
		applied = r.applyKeyWhere(builder, r.conditions.dest)
	}
	if r.conditions.model != nil && !sameModel(r.conditions.dest, r.conditions.model) {
		applied = r.applyKeyWhere(builder, r.conditions.model) || applied
	}

	return applied
}

// applyKeyWhere constrains a statement to the rows a model value identifies, by its primary key. One
// row becomes an equality and many become an IN list.
func (r *Query) applyKeyWhere(builder *querybuilder.Builder, model any) bool {
	column, values := r.modelKey(model)
	if column == "" || len(values) == 0 {
		return false
	}

	if len(values) == 1 {
		builder.Where(column, values[0])

		return true
	}

	builder.WhereIn(column, values, core.BooleanAnd, false)

	return true
}

// modelKey reads a model value's primary key: the column it lives in, and the values it identifies.
// A struct yields one value and a slice yields one per element; a value carrying no key yields none.
func (r *Query) modelKey(model any) (string, []any) {
	if model == nil {
		return "", nil
	}

	reflected := reflect.Indirect(reflect.ValueOf(model))
	if !reflected.IsValid() {
		return "", nil
	}

	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
		return r.sliceKey(reflected)
	}

	return r.valueKey(reflected)
}

// valueKey reads one model value's primary key. The schema comes from the value's own type, which is
// also how the key column is named, so the two cannot disagree.
func (r *Query) valueKey(value reflect.Value) (string, []any) {
	schema, err := parseSchemaWithTable(r.instance, reflect.New(value.Type()).Interface(), r.declaredTable())
	if err != nil || len(schema.PrimaryFields) == 0 {
		return "", nil
	}

	field := schema.PrimaryFields[0]
	if key, zero := field.ValueOf(r.ctx, value); !zero && key != nil {
		return field.DBName, []any{key}
	}

	return field.DBName, nil
}

// sliceKey reads a slice of models' primary keys, one per element.
func (r *Query) sliceKey(values reflect.Value) (string, []any) {
	if values.Len() == 0 {
		return "", nil
	}

	column, _ := r.valueKey(reflect.Indirect(values.Index(0)))
	if column == "" {
		return "", nil
	}

	keys := make([]any, 0, values.Len())
	for index := 0; index < values.Len(); index++ {
		_, one := r.valueKey(reflect.Indirect(values.Index(index)))
		keys = append(keys, one...)
	}

	return column, keys
}

// sameModel reports whether two values are the very same model, by address when both are pointers. A
// value whose type cannot be compared is not the same model, which is the answer that keeps the key in
// the where clause.
func sameModel(left any, right any) bool {
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() {
		return !leftValue.IsValid() && !rightValue.IsValid()
	}

	if leftValue.Type() != rightValue.Type() || leftValue.Kind() != reflect.Pointer {
		return false
	}

	return leftValue.Pointer() == rightValue.Pointer()
}
