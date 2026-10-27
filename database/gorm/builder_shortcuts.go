package gorm

import (
	"database/sql"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file ports the Eloquent\Builder conveniences that have a counterpart in this ORM's shape:
// the key-scoped wheres, the timestamp orderings, the single-value and single-row reads, the
// pending attributes a builder remembers for the models it creates, and the atomic column writes.
// Each is a thin layer over a method the ORM already has, which is also what it is in Laravel.

// --- primary key wheres -------------------------------------------------------------------------

// WhereKey constrains the query to the row(s) with the given primary key. Mirrors Builder::whereKey:
// a model stands for its own key, a slice for several keys, and the column is qualified by the
// model's table so a joined statement still compares the model's own id.
func (r *Query) WhereKey(id any) contractsorm.Query {
	return r.whereKey(id, false)
}

// WhereKeyNot excludes the row(s) with the given primary key. Mirrors Builder::whereKeyNot.
func (r *Query) WhereKeyNot(id any) contractsorm.Query {
	return r.whereKey(id, true)
}

// Except excludes the given models by primary key. Mirrors Builder::except, which is whereKeyNot
// over the models' keys.
func (r *Query) Except(models any) contractsorm.Query {
	return r.whereKey(models, true)
}

func (r *Query) whereKey(id any, not bool) contractsorm.Query {
	// The column is table-qualified but left bare: the compiler quotes each segment itself, and a
	// name that arrived already quoted would be quoted a second time.
	column := r.qualifiedPrimaryKeyColumn()
	if column == "" {
		return r.withError(errors.OrmQueryPrimaryKeyNotFound.Args(modelName(r.parentModel())))
	}

	keys, isList := r.keyValues(id)
	if isList {
		if not {
			return r.WhereNotIn(column, keys)
		}

		return r.WhereIn(column, keys)
	}

	operator := "="
	if not {
		operator = "!="
	}

	var key any
	if len(keys) == 1 {
		key = keys[0]
	}

	return r.Where(column, operator, key)
}

// qualifiedPrimaryKeyColumn returns the model's first primary key column prefixed with its table,
// unquoted ("users.id"), or "" when the model declares none.
func (r *Query) qualifiedPrimaryKeyColumn() string {
	model := r.parentModel()
	if model == nil {
		return ""
	}

	schema, err := parseSchema(model)
	if err != nil || len(schema.PrimaryFields) == 0 {
		return ""
	}

	return schema.Table + "." + schema.PrimaryFields[0].DBName
}

// keyValues reads the key(s) an argument stands for. A model yields its primary key, read through
// the model's schema so an untagged ID column counts as the key the way it does everywhere else in
// the ORM; a slice of models or of scalars yields one key per element; and a scalar yields itself.
// The second result reports whether the argument was a list, which chooses IN over equality.
func (r *Query) keyValues(id any) ([]any, bool) {
	if id == nil {
		return []any{nil}, false
	}

	reflected := reflect.Indirect(reflect.ValueOf(id))
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		keys := make([]any, 0, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			element := reflect.Indirect(reflected.Index(index))
			if element.Kind() == reflect.Struct {
				if _, key := r.valueKey(element); len(key) > 0 {
					keys = append(keys, key...)
				}

				continue
			}
			keys = append(keys, element.Interface())
		}

		return keys, true
	case reflect.Struct:
		_, key := r.valueKey(reflected)
		if len(key) == 0 {
			return []any{nil}, false
		}

		return key, false
	default:
		return []any{id}, false
	}
}

// --- timestamp orderings ------------------------------------------------------------------------

// Latest orders by the column descending, defaulting to the model's created-at column. Mirrors
// Builder::latest.
func (r *Query) Latest(column ...string) contractsorm.Query {
	return r.orderByTimestamp(column, "desc")
}

// Oldest orders by the column ascending, defaulting to the model's created-at column. Mirrors
// Builder::oldest.
func (r *Query) Oldest(column ...string) contractsorm.Query {
	return r.orderByTimestamp(column, "asc")
}

func (r *Query) orderByTimestamp(column []string, direction string) contractsorm.Query {
	name := ""
	if len(column) > 0 {
		name = column[0]
	}
	if name == "" {
		// Laravel falls back to the literal created_at when the model declares none.
		name = r.createdAtColumn()
		if name == "" {
			name = "created_at"
		}
	}

	return r.OrderBy(name, direction)
}

// createdAtColumn returns the column stamped only on insert, which is the model's created-at
// column, or "" when the model declares none.
func (r *Query) createdAtColumn() string {
	schema := r.insertSchema()
	if schema == nil {
		return ""
	}

	for _, field := range schema.Fields {
		if field.AutoCreateTime != 0 && field.AutoUpdateTime == 0 {
			return field.DBName
		}
	}

	return ""
}

// updatedAtColumn returns the column stamped on update, which is the model's updated-at column,
// or "" when the model declares none.
func (r *Query) updatedAtColumn() string {
	schema := r.insertSchema()
	if schema == nil {
		return ""
	}

	for _, field := range schema.Fields {
		if field.AutoUpdateTime != 0 {
			return field.DBName
		}
	}

	return ""
}

// --- single-value and single-row reads --------------------------------------------------------

// Value retrieves one column of the first row. Mirrors Builder::value, which is first([$column])
// read back by the column's bare name.
func (r *Query) Value(column string, dest any) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer")
	}

	query := r.clone()
	query.builder.SetSelects([]any{column})
	_ = query.builder.SetBindings(nil, "select")
	query.conditions.raw = nil
	query.builder.Limit(1)

	scoped := query.addGlobalScopes().buildConditions()

	statement, bindings, err := scoped.compileSelect()
	if err != nil {
		return err
	}

	// value() is first([$column]) read back, and first() answers a missing row with null rather than
	// an exception, so a read that matches nothing leaves dest as it was and reports no error. That
	// is also what First does here.
	if err := scoped.queryScalar(dest, statement, bindings); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	return nil
}

// Sole retrieves the one row the query matches. Mirrors BuildsQueries::sole: the statement is limited
// to two rows, so no row and more than one row are each reported as their own error.
func (r *Query) Sole(dest any) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer")
	}

	rows := reflect.New(reflect.SliceOf(destValue.Elem().Type()))

	query := r.clone()
	query.builder.Limit(2)
	if err := query.Find(rows.Interface()); err != nil {
		return err
	}

	switch count := rows.Elem().Len(); count {
	case 0:
		return errors.OrmRecordNotFound
	case 1:
		destValue.Elem().Set(rows.Elem().Index(0))

		return nil
	default:
		return errors.OrmMultipleRecordsFound.Args(count)
	}
}

// FindMany retrieves the rows whose primary key is one of ids. Mirrors Builder::findMany: an empty
// list reads nothing.
func (r *Query) FindMany(dest any, ids []any, columns ...any) error {
	if len(ids) == 0 {
		return nil
	}

	query, ok := r.WhereKey(ids).(*Query)
	if !ok {
		return errors.OrmQueryInvalidModel.Args(fmt.Sprintf("%T", r.WhereKey(ids)))
	}

	return query.Get(dest, columns...)
}

// --- pending attributes -------------------------------------------------------------------------

// WithAttributes records attributes for every model the query creates and, unless asConditions is
// false, adds each as a where equality. Mirrors Builder::withAttributes. The attributes may be a map
// or a single column followed by its value; a trailing bool is asConditions.
func (r *Query) WithAttributes(attributes any, args ...any) contractsorm.Query {
	asConditions := true
	mapping, ok := asColumnMap(attributes)
	if !ok {
		column, isString := attributes.(string)
		if !isString || len(args) == 0 {
			return r.withError(errors.OrmQueryInvalidParameter)
		}

		mapping = map[string]any{column: args[0]}
		args = args[1:]
	}
	if len(args) > 0 {
		if flag, isBool := args[len(args)-1].(bool); isBool {
			asConditions = flag
		}
	}

	if asConditions {
		for _, column := range slices.Sorted(maps.Keys(mapping)) {
			r.Where(r.qualifyColumn(column), mapping[column])
		}
	}

	if r.conditions.pendingAttributes == nil {
		r.conditions.pendingAttributes = make(map[string]any, len(mapping))
	}
	maps.Copy(r.conditions.pendingAttributes, mapping)

	return r
}

// qualifyColumn prefixes a column with the model's table, as Model::qualifyColumn does, so a where
// written by a shortcut compares the model's own column when the statement also joins another
// table. A Go field name is resolved to its column first; a name that already carries a table, or a
// query with no model, is left as written.
func (r *Query) qualifyColumn(column string) string {
	if strings.Contains(column, ".") {
		return column
	}

	schema := r.insertSchema()
	if schema == nil || schema.Table == "" {
		return column
	}

	return schema.Table + "." + resolveColumn(schema, column)
}

// applyPendingAttributes writes the recorded attributes onto a model about to be created, as
// newModelInstance merges $pendingAttributes into the attributes it is given. The caller's own
// values win: an attribute the model already carries a non-zero value for is left alone.
func (r *Query) applyPendingAttributes(dest any) error {
	if len(r.conditions.pendingAttributes) == 0 || dest == nil {
		return nil
	}

	reflected := reflect.Indirect(reflect.ValueOf(dest))
	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
		for index := 0; index < reflected.Len(); index++ {
			element := reflected.Index(index)
			if element.Kind() != reflect.Pointer {
				element = element.Addr()
			}
			if err := r.applyPendingAttributes(element.Interface()); err != nil {
				return err
			}
		}

		return nil
	}

	if mapping, ok := asColumnMap(dest); ok {
		for column, value := range r.conditions.pendingAttributes {
			if _, present := mapping[column]; !present {
				mapping[column] = value
			}
		}

		return nil
	}

	schema, err := parseSchema(dest)
	if err != nil {
		return err
	}

	for _, column := range slices.Sorted(maps.Keys(r.conditions.pendingAttributes)) {
		field, ok := schema.FieldsByName[column]
		if !ok {
			field, ok = schema.FieldsByDBName[column]
		}
		if !ok {
			continue
		}
		if _, zero := field.ValueOf(r.ctx, reflected); !zero {
			continue
		}

		if err := field.Set(r.ctx, reflect.ValueOf(dest), r.conditions.pendingAttributes[column]); err != nil {
			return err
		}
	}

	return nil
}

// --- atomic column writes -----------------------------------------------------------------------

// Increment adds amount (default 1) to the column of every matching row, refreshing the model's
// updated-at column and writing any extra values. Mirrors Builder::increment: args are an optional
// amount followed by an optional map of extra columns.
func (r *Query) Increment(column string, args ...any) (*contractsdb.Result, error) {
	return r.step(column, "+", args)
}

// Decrement subtracts amount (default 1) from the column of every matching row, refreshing the
// model's updated-at column and writing any extra values. Mirrors Builder::decrement.
func (r *Query) Decrement(column string, args ...any) (*contractsdb.Result, error) {
	return r.step(column, "-", args)
}

func (r *Query) step(column, operator string, args []any) (*contractsdb.Result, error) {
	for _, arg := range args {
		if _, ok := asColumnMap(arg); ok {
			continue
		}
		if _, ok := numericString(arg); ok {
			continue
		}

		method := "increment"
		if operator == "-" {
			method = "decrement"
		}

		return nil, errors.OrmQueryIncrementNonNumeric.Args(arg, method)
	}

	// Laravel's addUpdatedAtColumn stamps updated_at unless the extra values already name it; the
	// ORM's update path does the same through addAutoUpdateTime, so the map goes straight in.
	return r.Update(r.stepValues(column, operator, args))
}

// stepValues builds the assignment map an increment or decrement writes: the extra values, plus the
// column rewritten to a raw `<column> <op> <amount>` expression. The arguments are the ones step has
// already validated.
func (r *Query) stepValues(column, operator string, args []any) map[string]any {
	amount := "1"
	var extra map[string]any

	for _, arg := range args {
		if mapping, ok := asColumnMap(arg); ok {
			extra = mapping

			continue
		}
		if value, ok := numericString(arg); ok {
			amount = value
		}
	}

	values := make(map[string]any, len(extra)+1)
	maps.Copy(values, extra)
	resolved := resolveColumn(r.updateSchema(), column)
	values[resolved] = querybuilder.Raw(r.queryGrammar.Wrap(resolved) + " " + operator + " " + amount)

	return values
}

// numericString renders a numeric amount for the raw expression, refusing anything else the way
// Builder::increment refuses a non-numeric value.
func numericString(value any) (string, bool) {
	switch amount := value.(type) {
	case int:
		return strconv.Itoa(amount), true
	case int8, int16, int32, int64:
		return strconv.FormatInt(reflect.ValueOf(amount).Int(), 10), true
	case uint, uint8, uint16, uint32, uint64:
		return strconv.FormatUint(reflect.ValueOf(amount).Uint(), 10), true
	case float32:
		return strconv.FormatFloat(float64(amount), 'f', -1, 32), true
	case float64:
		return strconv.FormatFloat(amount, 'f', -1, 64), true
	default:
		return "", false
	}
}

// Touch sets the model's updated-at column, or the named column, to now on every matching row.
// Mirrors Builder::touch: a model without an updated-at column touches nothing and reports it.
func (r *Query) Touch(column ...string) (*contractsdb.Result, error) {
	name := ""
	if len(column) > 0 {
		name = column[0]
	}
	if name == "" {
		name = r.updatedAtColumn()
		if name == "" {
			return nil, errors.OrmQueryTimestampColumnNotFound.Args(modelName(r.parentModel()), "updated at")
		}
	}

	return r.Update(map[string]any{resolveColumn(r.updateSchema(), name): carbon.NewDateTime(carbon.Now())})
}

// Upsert inserts the rows, updating the listed columns of a row whose uniqueBy columns already
// exist. Mirrors Builder::upsert on a model: timestamps are added to every row, the created-at
// column is never in the update list, and the updated-at column always is.
func (r *Query) Upsert(values any, uniqueBy []string, update ...any) (*contractsdb.Result, error) {
	query := r.addGlobalScopes().buildConditions()

	records, err := query.insertRecords(values)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return &contractsdb.Result{}, nil
	}

	records = uniformInsertRecords(records)
	query.addUpsertTimestamps(records)

	updates, insertOnly, err := query.upsertUpdates(records, update)
	if err != nil {
		return nil, err
	}
	if insertOnly {
		statement, bindings, err := query.compileInsertAll(records)
		if err != nil {
			return nil, err
		}

		return query.execStatement(statement, bindings)
	}

	builder, err := query.newBuilder()
	if err != nil {
		return nil, err
	}

	statement, bindings := builder.ToUpsertSQL(records, uniqueBy, updates)
	if err := builder.Error(); err != nil {
		return nil, err
	}

	return query.execStatement(statement, bindings)
}

// addUpsertTimestamps stamps the model's timestamp columns onto every row, the way
// addTimestampsToUpsertValues does: a value the caller supplied is kept.
func (r *Query) addUpsertTimestamps(records []map[string]any) {
	schema := r.insertSchema()
	if schema == nil {
		return
	}

	now := carbon.Now()
	for _, field := range schema.Fields {
		if field.AutoCreateTime == 0 && field.AutoUpdateTime == 0 {
			continue
		}

		// The value takes the field's own representation: an integer field holds a Unix timestamp,
		// anything else the DateTime the rest of the write path stamps.
		var stamp any = carbon.NewDateTime(now)
		if isIntegerKeyType(field.FieldType) {
			stamp = now.Timestamp()
		}

		for _, record := range records {
			if value, ok := record[field.DBName]; !ok || value == nil || isZeroValue(value) {
				record[field.DBName] = stamp
			}
		}
	}
}

// upsertUpdates resolves the update list the way addUpdatedAtToUpsertColumns does: no list means
// every inserted column except created-at, an explicit list is used as given, and updated-at joins
// either. An empty explicit list means insert only.
func (r *Query) upsertUpdates(records []map[string]any, update []any) ([]core.UpsertUpdate, bool, error) {
	schema := r.insertSchema()
	createdAt := r.createdAtColumn()
	updatedAt := r.updatedAtColumn()

	var columns []string
	if len(update) == 0 || update[0] == nil {
		for _, column := range slices.Sorted(maps.Keys(records[0])) {
			if column != createdAt {
				columns = append(columns, column)
			}
		}
	} else {
		switch given := update[0].(type) {
		case []string:
			if len(given) == 0 {
				return nil, true, nil
			}
			for _, column := range given {
				columns = append(columns, resolveColumn(schema, column))
			}
		case map[string]any:
			result := make([]core.UpsertUpdate, 0, len(given)+1)
			for _, column := range slices.Sorted(maps.Keys(given)) {
				result = append(result, core.UpsertUpdate{Column: resolveColumn(schema, column), Value: given[column], Bound: true})
			}
			if updatedAt != "" && !hasUpsertColumn(result, updatedAt) {
				result = append(result, core.UpsertUpdate{Column: updatedAt})
			}

			return result, false, nil
		default:
			return nil, false, errors.DatabaseUnsupportedType.Args(reflect.TypeOf(update[0]).String(), "[]string or map[string]any")
		}
	}

	if updatedAt != "" && !slices.Contains(columns, updatedAt) {
		columns = append(columns, updatedAt)
	}

	result := make([]core.UpsertUpdate, 0, len(columns))
	for _, column := range columns {
		result = append(result, core.UpsertUpdate{Column: column})
	}

	return result, false, nil
}

func hasUpsertColumn(updates []core.UpsertUpdate, column string) bool {
	for _, update := range updates {
		if update.Column == column {
			return true
		}
	}

	return false
}

// modelName names a model for an error message, or "<nil>" when the query has none.
func modelName(model any) string {
	if model == nil {
		return "<nil>"
	}

	return reflect.Indirect(reflect.ValueOf(model)).Type().String()
}
