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
func (r *OrmBuilder) WhereKey(id any) contractsorm.Query {
	return r.whereKey(id, false)
}

// WhereKeyNot excludes the row(s) with the given primary key. Mirrors Builder::whereKeyNot.
func (r *OrmBuilder) WhereKeyNot(id any) contractsorm.Query {
	return r.whereKey(id, true)
}

// Except excludes the given models by primary key. Mirrors Builder::except, which is whereKeyNot
// over the models' keys.
func (r *OrmBuilder) Except(models any) contractsorm.Query {
	return r.whereKey(models, true)
}

func (r *OrmBuilder) whereKey(id any, not bool) contractsorm.Query {
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
func (r *OrmBuilder) qualifiedPrimaryKeyColumn() string {
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
func (r *OrmBuilder) keyValues(id any) ([]any, bool) {
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
func (r *OrmBuilder) Latest(column ...string) contractsorm.Query {
	return r.orderByTimestamp(column, "desc")
}

// Oldest orders by the column ascending, defaulting to the model's created-at column. Mirrors
// Builder::oldest.
func (r *OrmBuilder) Oldest(column ...string) contractsorm.Query {
	return r.orderByTimestamp(column, "asc")
}

func (r *OrmBuilder) orderByTimestamp(column []string, direction string) contractsorm.Query {
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
func (r *OrmBuilder) createdAtColumn() string {
	schema := r.insertSchema()
	if schema == nil {
		return ""
	}

	return schemaCreatedAtColumn(schema)
}

// schemaCreatedAtColumn scans a parsed schema for the column stamped only on insert — the model's
// created-at column, "" when the schema declares none.
func schemaCreatedAtColumn(schema *modelSchema) string {
	for _, field := range schema.Fields {
		if field.AutoCreateTime != 0 && field.AutoUpdateTime == 0 {
			return field.DBName
		}
	}

	return ""
}

// modelCreatedAtColumn resolves a model's created-at column from its schema — the column
// Relation::createdAt reads off the parent model (`$this->parent->getCreatedAtColumn()`,
// Relations/Relation.php:373; Eloquent's Model::getCreatedAtColumn reports static::CREATED_AT).
// "" when the model declares none.
func modelCreatedAtColumn(model any) (string, error) {
	schema, err := parseSchema(model)
	if err != nil {
		return "", err
	}

	return schemaCreatedAtColumn(schema), nil
}

// updatedAtColumn returns the column stamped on update, which is the model's updated-at column,
// or "" when the model declares none.
func (r *OrmBuilder) updatedAtColumn() string {
	schema := r.insertSchema()
	if schema == nil {
		return ""
	}

	return schemaUpdatedAtColumn(schema)
}

// schemaUpdatedAtColumn scans a parsed schema for the column stamped on update — the model's
// updated-at column, "" when the schema declares none.
func schemaUpdatedAtColumn(schema *modelSchema) string {
	for _, field := range schema.Fields {
		if field.AutoUpdateTime != 0 {
			return field.DBName
		}
	}

	return ""
}

// modelUpdatedAtColumn resolves a model's updated-at column from its schema — the column
// Relation::updatedAt reads off the parent model (`$this->parent->getUpdatedAtColumn()`,
// Relations/Relation.php:383; Eloquent's Model::getUpdatedAtColumn reports static::UPDATED_AT).
// "" when the model declares none.
func modelUpdatedAtColumn(model any) (string, error) {
	schema, err := parseSchema(model)
	if err != nil {
		return "", err
	}

	return schemaUpdatedAtColumn(schema), nil
}

// --- single-value and single-row reads --------------------------------------------------------

// Value retrieves one column of the first row, read back by the column's bare name. Mirrors
// Builder::value, which is first([$column]) — the scopes are applied before the onceWithColumns
// decision, exactly as in pluck() — so a selection the caller pre-set or a global scope wrote
// survives and the single column is installed only when the scoped query carries none (oracle
// pins in singleColumnFirstQuery).
func (r *OrmBuilder) Value(column string, dest any) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer")
	}

	scoped := r.singleColumnFirstQuery(column)

	statement, bindings, err := scoped.compileSelect()
	if err != nil {
		return err
	}

	// value() is first([$column]) read back, and first() answers a missing row with null rather than
	// an exception, so a read that matches nothing leaves dest as it was and reports no error. A
	// kept select list that no longer carries the column answers null the same way: the read is
	// keyed by the result columns' bare names, so a missing one leaves dest as it was. That is also
	// what First does here.
	if err := scoped.queryColumnValue(dest, column, statement, bindings); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	return nil
}

// singleColumnFirstQuery prepares the read Value and ValueOrFail share: first([$column]) with
// the select list narrowed to the column for this statement only and the read limited to one
// row. It runs on the Eloquent builder in Laravel, not the relation, so the through read's
// laravel_through_key alias is not appended: the oracle measures `select "title" from "posts"
// inner join "users" ... limit 1` for value() and for valueOrFail() alike, and a many-to-many
// read's alias list is dropped the same way (`select "email" from "users" inner join "friends"
// ... limit 1`).
func (r *OrmBuilder) singleColumnFirstQuery(column string) *OrmBuilder {
	// Eloquent's value() and valueOrFail() read through first([$column]) -> limit(1)->get($columns):
	// the one-row limit is applied before get() runs applyScopes(), so the scopes run over it and a
	// scope's own limit() overrides the read's (oracle, illuminate/database v12.55.1, executed
	// against sqlite: a scope doing selectRaw('name')->limit(5) then value('users.email') compiles
	// `select name from "users" limit 5` and answers null, while value('users.name') reads "Taylor"
	// back; a scope doing limit(0) compiles `select "users"."email" from "users" limit 0`, answers
	// null, and makes valueOrFail throw ModelNotFoundException). The global scopes are applied
	// before onceWithColumns decides: a scope's select()/selectRaw() is already in the columns and
	// survives, and the single column is installed only when nothing is set then — the same
	// onceWithColumns guard pluck() uses (oracle, illuminate/database v12.55.1, executed against
	// sqlite: a scope doing selectRaw('name') then value('email') compiles `select name from "users"
	// limit 1` and answers null; scope select('name') + value('email') compiles `select "name" from
	// "users" limit 1` and answers null; select('id','email')->value('email') keeps `select "id",
	// "email" from "users" limit 1` and reads the email back by name; a withCount('posts') list is
	// kept as well and value('email') reads it back by name).
	query := r.withoutThroughReadSelect().withoutPivotReadSelect()
	query.query.Limit(1)

	scoped := query.addGlobalScopes().withScopesApplied().buildConditions()

	if scoped.query.Selects() == nil && len(scoped.conditions.selectSubs) == 0 {
		scoped.query.SetSelects([]any{column})
		_ = scoped.query.SetBindings(nil, "select")
		scoped.conditions.raw = nil
	}

	return scoped
}

// queryColumnValue runs a compiled single-row read and sets dest to the requested column of the
// first row, the way Builder::value reads $result->{Str::afterLast($column, '.')} back from
// first([$column]). The row is keyed by its result columns' bare names (pluckRow), so the column is
// found whether the statement selected it alone, as part of a caller's select list, or as part of a
// scope's or a withCount list's; a kept list that does not carry it answers null, which leaves dest
// as it was. A read that matches no row reports sql.ErrNoRows, which Value answers like first()'s
// null and ValueOrFail as a model miss.
func (r *OrmBuilder) queryColumnValue(dest any, column string, statement string, bindings []any) error {
	rows, err := r.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}

		return sql.ErrNoRows
	}

	resultColumns, err := rows.Columns()
	if err != nil {
		return err
	}

	row, err := pluckRow(resultColumns, rows)
	if err != nil {
		return err
	}

	value, ok := row[stripTableForPluck(column)]
	if !ok {
		return nil
	}

	destValue := reflect.ValueOf(dest)
	destValue.Elem().Set(pluckValue(value, destValue.Elem().Type()))

	return nil
}

// Pluck retrieves a single column from the database, mirroring Laravel's pluck($column, $key): the
// select list becomes the plucked column — plus the key column when one is given — but only when
// the scoped query carries no selection, because Builder::pluck installs the pair through
// onceWithColumns, which writes the columns solely when they are null and restores them when the
// read completes: a select() the caller already wrote survives verbatim, and the installed pair
// lives only for this statement, so the query can still be executed afterwards (oracle,
// illuminate/database v12.55.1, executed against sqlite: select('name')->pluck('email') compiles
// `select "name" from "users"`; `$q->pluck('email'); $q->pluck('name')` compiles `select "email"
// from "users"` then `select "name" from "users"`). A queued selectSub — a WithCount / WithMax
// and the like — counts as an installed select list, because withAggregate wrote `from.*` and the
// subselect into the columns before pluck ran (oracle: withCount('posts')->pluck('email') compiles
// `select "users".*, (select count(*) ...) as "posts_count" from "users"`). The fold matches
// pluckFromArrayColumn, reading a row's columns by name, and a key equal to the column reuses the
// value (Laravel's onceWithColumns collapses that pair to a one-column select). dest receives the
// values, a pointer to a slice for the flat form or a pointer to a map keyed by the key column's
// values.
func (r *OrmBuilder) Pluck(column string, dest any, key ...string) error {
	keyColumn := ""
	if len(key) > 0 {
		keyColumn = key[0]
	}

	// The global scopes are folded in before the onceWithColumns decision, the way pluck() reads
	// through toBase() — applyScopes()->getQuery() — in Eloquent's Builder: a scope's
	// select()/selectRaw() is already in the columns when the decision runs (oracle,
	// illuminate/database v12.55.1, executed against sqlite: scope selectRaw('name') +
	// pluck('email') compiles `select name from "users"` and yields [null,null]; scope
	// select('name') + pluck('email') compiles `select "name" from "users"` and yields [null,null];
	// a keyed pluck('email','id') keeps the same scope-written list and folds {"":null}).
	query := r.addGlobalScopes().withScopesApplied().buildConditions()

	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer to slice")
	}

	destType := destValue.Elem().Type()
	if keyColumn == "" {
		if destType.Kind() != reflect.Slice {
			return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer to slice")
		}
	} else if destType.Kind() != reflect.Map {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer to map")
	}

	columns := []any{column}
	if keyColumn != "" && keyColumn != column {
		columns = append(columns, keyColumn)
	}

	// onceWithColumns writes the columns only when the scoped query carries none, and restores them
	// once the callback returns, so the pair is installed under both rules here: a select list the
	// caller pre-set survives, a scope-written list survives (see above), and a queued selectSub
	// counts as one, because withAggregate wrote `from.*` and the subselect into Laravel's columns
	// before pluck ran (oracle, illuminate/database v12.55.1, executed against sqlite:
	// withCount('posts')->pluck('email') compiles `select "users".*, (select count(*) from "posts"
	// where "users"."id" = "posts"."user_id") as "posts_count" from "users"`). withScopesApplied
	// hands back a private copy, which also satisfies the restore half of onceWithColumns: the pair
	// installed here is written back when the read completes because the copy is dropped, never
	// because the receiver was mutated (oracle: `$q->pluck('email'); $q->pluck('name')` compiles
	// `select "email" from "users"` then `select "name" from "users"`; `pluck('email')` followed by
	// `get()` compiles `select * from "users"`). The fold below reads the plucked column out of the
	// kept statement by name either way — from the pair, from the caller's select list, from a
	// scope's list, or from `from.*`.
	if query.query.Selects() == nil && len(query.conditions.selectSubs) == 0 {
		query.query.SetSelects(columns)
		_ = query.query.SetBindings(nil, "select")
		query.conditions.raw = nil
	}

	// pluck() is forwarded to the Eloquent builder in Laravel, which writes its own select list —
	// or keeps the caller's — without going through the relation, so neither relation read's
	// aliases are appended: the oracle measures `select "title" from "posts" inner join "users"
	// ...` for a through read and `select "email" from "users" inner join "friends" ...` for a
	// many-to-many read.
	query = query.withoutThroughReadSelect().withoutPivotReadSelect()

	statement, bindings, err := query.compileSelect()
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	// The fold reads each row's columns by name — the way pluckFromArrayColumn keys its results —
	// so the requested column is found whether it came from the pair installed above or from a
	// select list the caller pre-set; a column the statement did not carry reads as null, which
	// the Go destination holds as its zero value.
	resultColumns, err := rows.Columns()
	if err != nil {
		return err
	}
	valueKey := stripTableForPluck(column)
	keyKey := stripTableForPluck(keyColumn)

	// The scanned value is converted to the destination's type, because a driver may report a
	// column as a different but equivalent Go type than the destination holds.
	target := destValue.Elem()

	if keyColumn != "" {
		keyType := destType.Key()
		elementType := destType.Elem()
		shared := keyColumn == column

		if target.IsNil() {
			target.Set(reflect.MakeMap(destType))
		}

		for rows.Next() {
			row, err := pluckRow(resultColumns, rows)
			if err != nil {
				return err
			}

			value := row[valueKey]
			if shared {
				target.SetMapIndex(pluckValue(value, keyType), pluckValue(value, elementType))
				continue
			}

			target.SetMapIndex(pluckValue(row[keyKey], keyType), pluckValue(value, elementType))
		}

		if err := rows.Err(); err != nil {
			return err
		}

		return nil
	}

	elementType := destType.Elem()
	for rows.Next() {
		row, err := pluckRow(resultColumns, rows)
		if err != nil {
			return err
		}

		target = reflect.Append(target, pluckValue(row[valueKey], elementType))
	}

	if err := rows.Err(); err != nil {
		return err
	}

	destValue.Elem().Set(target)

	return nil
}

// pluckRow scans one row into a column-keyed map, the way pluckFromArrayColumn reads a result row:
// the keys are the result columns' bare names, so the requested column is found whether the pluck
// installed the select list or a caller had already set one.
func pluckRow(columns []string, rows *sql.Rows) (map[string]any, error) {
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}

	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}

	row := make(map[string]any, len(columns))
	for index, name := range columns {
		row[stripTableForPluck(name)] = values[index]
	}

	return row, nil
}

// stripTableForPluck strips the table name or alias from a column identifier, mirroring Laravel's
// Builder::stripTableForPluck: a plucked result set is keyed by the bare column name only, so the
// requested `users.email` (or `users.email AS user_email`) and the driver's own column name land
// on the same key.
func stripTableForPluck(column string) string {
	lower := strings.ToLower(column)
	separator := "."
	if strings.Contains(lower, " as ") {
		separator = " as "
	}

	if index := strings.LastIndex(lower, separator); index != -1 {
		return column[index+len(separator):]
	}

	return column
}

// pluckValue converts a scanned column value to the destination's type, the way the flat pluck
// converts a column to its slice element; a value that cannot be converted stays the zero value of
// the target type.
func pluckValue(value any, targetType reflect.Type) reflect.Value {
	element := reflect.New(targetType).Elem()
	if converted := castKey(value, targetType); converted != nil {
		convertedValue := reflect.ValueOf(converted)
		switch {
		case convertedValue.Type().AssignableTo(targetType):
			element.Set(convertedValue)
		case convertedValue.Type().ConvertibleTo(targetType):
			element.Set(convertedValue.Convert(targetType))
		}
	}

	return element
}

// Sole retrieves the one row the query matches. Mirrors BuildsQueries::sole: the statement is limited
// to two rows, so no row and more than one row are each reported as their own error.
func (r *OrmBuilder) Sole(dest any) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer")
	}

	rows := reflect.New(reflect.SliceOf(destValue.Elem().Type()))

	query := r.clone()
	query.query.Limit(2)
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
func (r *OrmBuilder) FindMany(dest any, ids []any, columns ...any) error {
	if len(ids) == 0 {
		return nil
	}

	// The key list goes on a copy, so the query is left as it was; dest names the model the keys
	// belong to when the query carries none (Find(&users, ids) on a bare query).
	scoped := r.clone().dest(dest).WhereKey(ids)
	query, ok := scoped.(*OrmBuilder)
	if !ok {
		return errors.OrmQueryInvalidModel.Args(fmt.Sprintf("%T", scoped))
	}

	return query.Get(dest, columns...)
}

// --- pending attributes -------------------------------------------------------------------------

// WithAttributes records attributes for every model the query creates and, unless asConditions is
// false, adds each as a where equality. Mirrors Builder::withAttributes. The attributes may be a map
// or a single column followed by its value; a trailing bool is asConditions.
func (r *OrmBuilder) WithAttributes(attributes any, args ...any) contractsorm.Query {
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
func (r *OrmBuilder) qualifyColumn(column string) string {
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
// values win: an attribute the model already carries a non-zero value for is left alone, and so is
// a zero the query's WithZero list names, the only way a struct can say it carries that attribute.
// The Or* entry points name their map keys the same way (withZeroFor), so a false, 0 or null a
// caller passes there survives the merge a later create() runs. Eloquent: array_merge($pending,
// $attributes) and setForeignAttributesForCreate's array_key_exists; oracle v12.55.1, executed,
// /tmp/r15b/pending.php P1-P4, P7, R1-R8, R10.
func (r *OrmBuilder) applyPendingAttributes(dest any) error {
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
		if _, zero := field.ValueOf(r.ctx, reflected); !zero || r.writesZero(schema, field.DBName) {
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
func (r *OrmBuilder) Increment(column string, args ...any) (*contractsdb.Result, error) {
	return r.step(column, "+", args)
}

// Decrement subtracts amount (default 1) from the column of every matching row, refreshing the
// model's updated-at column and writing any extra values. Mirrors Builder::decrement.
func (r *OrmBuilder) Decrement(column string, args ...any) (*contractsdb.Result, error) {
	return r.step(column, "-", args)
}

func (r *OrmBuilder) step(column, operator string, args []any) (*contractsdb.Result, error) {
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
	// ORM's update path does the same through addAutoUpdateTime, so the map goes straight in
	// (oracle v12.55.1, executed: /tmp/massupdate_scope_guard_probe.php quotes the stamp in the
	// increment SQL).
	//
	// The step runs on a copy carrying withoutTouch: neither Builder::increment nor
	// Model::increment propagates touches (they never reach finishSave), while the model-update
	// branch the step shares with Update does; the copy keeps the caller's query as it was.
	stepped := r.clone()
	conditions := stepped.conditions
	conditions.withoutTouch = true
	stepped.setConditions(conditions)

	return stepped.Update(stepped.stepValues(column, operator, args))
}

// stepValues builds the assignment map an increment or decrement writes: the extra values, plus the
// column rewritten to a raw `<column> <op> <amount>` expression. An extra value naming the
// incremented column replaces the raw expression, the way incrementEach's
// `array_merge($columns, $extra)` lets the extra win. The arguments are the ones step has already
// validated.
func (r *OrmBuilder) stepValues(column, operator string, args []any) map[string]any {
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

	resolved := resolveColumn(r.updateSchema(), column)
	values := map[string]any{
		resolved: querybuilder.Raw(r.queryGrammar.Wrap(resolved) + " " + operator + " " + amount),
	}
	// The extra values merge over the raw expression the way incrementEach ends in
	// `$this->update(array_merge($columns, $extra))`: an extra naming the incremented column
	// replaces the raw expression with the caller's value (oracle v12.55.1, executed:
	// /tmp/inc_extra_probe.php — `Builder::increment extra names the column` compiles
	// `update "users" set "count" = ?, "updated_at" = ?` and stores the extra value).
	maps.Copy(values, extra)

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
// Mirrors Builder::touch: a model without an updated-at column runs no statement and returns false
// (oracle v12.55.1, executed, /tmp/r15b/touch.php T1/T2), which this signature spells as a zero-row
// result and no error. A query with no model at all has no Laravel touch to mirror
// (DB::table('t')->touch() is a BadMethodCallException, T9) and reports the named error.
//
// On a query bound to a model — Model(m) — the touch also walks m's Touches owners once the row
// is stamped: Eloquent's Model::touch saves the model, so its save path runs touchOwners, while
// the unbound builder form only stamps, exactly as Builder::touch does.
func (r *OrmBuilder) Touch(column ...string) (*contractsdb.Result, error) {
	name := ""
	if len(column) > 0 {
		name = column[0]
	}
	if name == "" {
		// Eloquent's Builder::touch reads usesTimestamps before it decides: an ignored model touches
		// nothing (Laravel's `return false`), rather than failing over a missing column.
		if !r.usesTimestamps(r.parentModel()) {
			return &contractsdb.Result{}, nil
		}
		schema := r.insertSchema()
		if schema == nil {
			return nil, errors.OrmQueryTimestampColumnNotFound.Args(modelName(r.parentModel()), "updated at")
		}
		if name = schemaUpdatedAtColumn(schema); name == "" {
			return &contractsdb.Result{}, nil
		}
	}

	return r.Update(map[string]any{resolveColumn(r.updateSchema(), name): carbon.NewDateTime(carbon.Now())})
}

// Upsert inserts the rows, updating the listed columns of a row whose uniqueBy columns already
// exist. Mirrors Builder::upsert on a model: timestamps are added to every row, the created-at
// column is never in the update list, and the updated-at column always is.
func (r *OrmBuilder) Upsert(values any, uniqueBy []string, update ...any) (*contractsdb.Result, error) {
	query := r.addGlobalScopes().buildConditions()

	records, err := query.insertRecords(values)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return &contractsdb.Result{}, nil
	}

	records = uniformInsertRecords(records)
	// Eloquent's addTimestampsToUpsertValues stamps the columns onto every row only while the model
	// uses timestamps.
	if query.usesTimestamps(query.parentModel()) {
		query.addUpsertTimestamps(records)
	}

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
func (r *OrmBuilder) addUpsertTimestamps(records []map[string]any) {
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
func (r *OrmBuilder) upsertUpdates(records []map[string]any, update []any) ([]core.UpsertUpdate, bool, error) {
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
			// Eloquent's addUpdatedAtToUpsertColumns joins updated-at to the update list only while
			// the model uses timestamps.
			if r.usesTimestamps(r.parentModel()) && updatedAt != "" && !hasUpsertColumn(result, updatedAt) {
				result = append(result, core.UpsertUpdate{Column: updatedAt})
			}

			return result, false, nil
		default:
			return nil, false, errors.DatabaseUnsupportedType.Args(reflect.TypeOf(update[0]).String(), "[]string or map[string]any")
		}
	}

	if r.usesTimestamps(r.parentModel()) && updatedAt != "" && !slices.Contains(columns, updatedAt) {
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
