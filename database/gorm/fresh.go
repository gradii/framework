package gorm

import (
	"database/sql"
	"reflect"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
)

// This file implements Eloquent's Model::fresh and Model::refresh. Both re-read the model's row
// with newQueryWithoutScopes() — global scopes, the soft-delete filter included, are skipped — on
// the write connection, matched on setKeysForSelectQuery: the primary key, or, for a pivot model,
// the (foreign key, related key) pair AsPivot::setKeysForSelectQuery writes (MorphPivot::setKeys-
// ForSelectQuery first pins the morph type). fresh() returns a new instance (null when the model
// is not stored); refresh() re-reads in place through firstOrFail — a missing row reports
// errors.OrmModelNotFoundNoIds, firstOrFail's id-less message — and re-loads the relations the
// model already carries loaded, pivot relations excepted; in Go those are the non-nil relation
// fields. (Oracle, illuminate/database v12.55.1, executed against sqlite: `$user->fresh()`
// compiles `select * from "users" where
// "id" = ? limit 1`; `$pivot->fresh()` `select * from "friends" where "user_id" = ? and
// "friend_id" = ? limit 1`; `$morphPivot->fresh()` `select * from "taggables" where "taggable_type"
// = ? and "taggable_id" = ? and "tag_id" = ? limit 1`; a model whose key is empty yields null
// without a statement.)

// freshKey is one column/value pair a fresh/refresh read matches on, in the order the keys are
// written: the primary key columns, or PivotKeys' columns for a pivot struct.
type freshKey struct {
	column string
	value  any
}

// Fresh reads model's row back from the database into dest — a new instance — mirroring
// Eloquent's Model::fresh. dest is a non-nil pointer to the same model type; a model without a
// key (see freshKeys) or one the database no longer holds leaves dest zero-valued, Laravel's
// null. with names relations to eager-load on the fresh instance.
func (r *OrmBuilder) Fresh(model any, dest any, with ...string) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() || destValue.Elem().Kind() != reflect.Struct {
		return errors.OrmQueryModelNotPointer
	}

	query, _, keys, stored, err := r.freshReadQuery(model, with)
	if err != nil {
		return err
	}
	if !stored {
		destValue.Elem().SetZero()

		return nil
	}
	_ = keys

	query = query.buildConditions()

	statement, bindings, err := query.compileSelect()
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	destValue.Elem().SetZero()
	if _, err := query.scanRows(rows, dest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	// The single-row scan leaves the result set open; close it before the with() eager loads run,
	// so a small connection pool cannot deadlock on the fresh read's own cursor.
	_ = rows.Close()

	// first() fires the retrieved event and runs the with() eager loads, Laravel's fresh path.
	return query.retrieved(dest)
}

// Refresh re-reads model's row into model itself, mirroring Eloquent's Model::refresh: the same
// key-matched, scope-skipping read as Fresh, applied in place; a model without a key is returned
// unchanged and a row the database no longer holds reports errors.OrmModelNotFoundNoIds,
// firstOrFail's id-less message. Every relation the model carries loaded is re-loaded afterwards,
// refresh()'s reload of its relations. with names relations to eager-load when the row is found.
func (r *OrmBuilder) Refresh(model any) error {
	query, schema, _, stored, err := r.freshReadQuery(model, nil)
	if err != nil {
		return err
	}
	if !stored {
		return nil
	}

	query = query.buildConditions()

	statement, bindings, err := query.compileSelect()
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	count, err := query.scanRows(rows, model)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if count == 0 {
		// firstOrFail answers a miss with ModelNotFoundException and no ids — "No query results
		// for model [X]." (oracle: a refresh() on a missing row throws exactly that) — the
		// id-less message errors.OrmModelNotFoundNoIds renders, the form ValueOrFail uses.
		return errors.OrmModelNotFoundNoIds.Args(schema.Name)
	}

	// firstOrFail's read runs to its single row; the result set is closed before the relation
	// reloads issue their own statements for the same reason as Fresh.
	_ = rows.Close()
	if err := query.retrieved(model); err != nil {
		return err
	}

	return r.reloadLoadedRelations(model)
}

// freshReadQuery resolves the keyed, scope-skipping select both Fresh and Refresh run: the model
// is matched on its fresh keys, global scopes are dropped the way newQueryWithoutScopes drops
// them, the soft-delete predicate is lifted with them (the base query carries none of its own
// global scopes), the read runs on the write connection, and the statement is limited to one row.
// stored is false — no query to run — when the model carries no key value at all, the Go reading
// of Eloquent's ! exists short-circuit.
func (r *OrmBuilder) freshReadQuery(model any, with []string) (*OrmBuilder, *modelSchema, []freshKey, bool, error) {
	modelValue := reflect.ValueOf(model)
	if modelValue.Kind() != reflect.Pointer || modelValue.IsNil() || modelValue.Elem().Kind() != reflect.Struct {
		return nil, nil, nil, false, errors.OrmQueryModelNotPointer
	}

	schema, err := parseSchema(model)
	if err != nil {
		return nil, nil, nil, false, err
	}

	keys, stored, err := r.freshKeys(model, schema, modelValue.Elem())
	if err != nil {
		return nil, nil, nil, false, err
	}
	if !stored {
		return nil, schema, nil, false, nil
	}

	query := r.fresh()
	if built, ok := query.Model(model).(*OrmBuilder); ok {
		query = built
	}
	query = query.WithoutGlobalScopes("*").(*OrmBuilder)
	query = query.WithTrashed().(*OrmBuilder)
	query = query.UseWritePdo().(*OrmBuilder)
	for _, key := range keys {
		query = query.Where(key.column, key.value).(*OrmBuilder)
	}
	for _, relation := range with {
		query = query.With(relation).(*OrmBuilder)
	}
	query.query.Limit(1)

	return query, schema, keys, true, nil
}

// freshKeys resolves the columns and values a fresh/refresh read matches on: the model's primary
// keys, or — for a pivot struct, which carries no primary key — the columns its PivotKeys method
// names, in the order it names them. stored reports whether every key carries a value; a keyless
// model is Eloquent's not-stored case, so there is nothing to match a row with.
func (r *OrmBuilder) freshKeys(model any, schema *modelSchema, value reflect.Value) ([]freshKey, bool, error) {
	columns := make([]string, 0, len(schema.PrimaryFields))
	for _, field := range schema.PrimaryFields {
		columns = append(columns, field.DBName)
	}
	if len(columns) == 0 {
		keyed, ok := model.(contractsorm.PivotKeyed)
		if !ok {
			return nil, false, errors.OrmModelKeyMissing.Args(schema.Name)
		}
		columns = keyed.PivotKeys()
		if len(columns) == 0 {
			return nil, false, errors.OrmModelKeyMissing.Args(schema.Name)
		}
	}

	keys := make([]freshKey, 0, len(columns))
	for _, column := range columns {
		field, ok := schema.FieldsByDBName[column]
		if !ok {
			return nil, false, errors.OrmModelKeyColumnNotFound.Args(schema.Name, column)
		}

		keyValue, _ := field.ValueOf(r.ctx, value)
		if emptyKeyValue(keyValue) {
			return nil, false, nil
		}

		keys = append(keys, freshKey{column: column, value: keyValue})
	}

	return keys, true, nil
}

// emptyKeyValue reports whether a key column's value is empty. Eloquent reads the model's exists
// flag for its fresh null branch; a Go model keeps no such flag, so a missing value on any key
// column stands in — a zero id (or a nil pointer field) was never stored.
func emptyKeyValue(value any) bool {
	if value == nil {
		return true
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.String:
		return reflected.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint() == 0
	case reflect.Pointer, reflect.Interface:
		return reflected.IsNil()
	}

	return false
}

// reloadLoadedRelations re-runs Load for every relation the model currently carries loaded — a
// non-nil relation field, in field order — which is refresh()'s reload of its relations. Pivot
// relations are not relation fields in Go and stay untouched, matching refresh()'s exclusion of
// Pivot instances from the reload.
func (r *OrmBuilder) reloadLoadedRelations(model any) error {
	declaredRelations, ok := relations.TryGetRelations(model)
	if !ok || len(declaredRelations) == 0 {
		return nil
	}

	value := reflect.ValueOf(model).Elem()
	valueType := value.Type()
	for position := 0; position < valueType.NumField(); position++ {
		fieldType := valueType.Field(position)
		if !fieldType.IsExported() {
			continue
		}
		if _, declared := declaredRelations[fieldType.Name]; !declared {
			continue
		}
		if !relationFieldLoaded(value.Field(position)) {
			continue
		}

		loader := r.fresh()
		if err := loader.Load(model, fieldType.Name); err != nil {
			return err
		}
	}

	return nil
}

// relationFieldLoaded reports whether a relation field carries a loaded relation: a non-nil
// pointer, slice, map or interface. Eloquent reads its relations registry; Go models keep no
// registry, so a non-nil field is the observable — the loaders always leave one behind, an empty
// loaded relation being a non-nil empty slice.
func relationFieldLoaded(field reflect.Value) bool {
	switch field.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		return !field.IsNil()
	}

	return false
}
