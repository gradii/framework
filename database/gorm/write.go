package gorm

import (
	"reflect"
	"sort"
	"strings"

	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file inserts rows through the framework's own compiler.
//
// GORM used to build and run inserts, populating the generated key on the way back. That is done
// here instead: the column values come from the model, the statement from database/query, and the
// generated key is read back from the statement when the dialect returns it or from the driver
// otherwise.

// insertRecords returns one column/value map per row to insert.
//
// A struct is mapped column by column; a map is taken as written, because its keys already name
// columns. Slices and pointers are unwrapped so a batch insert works the same way as a single row.
// indirectValue dereferences pointers until it reaches the value they address.
//
// GORM accepted a pointer to a pointer wherever it accepted a model, and the integration module relies
// on it (TestWith_ChunkedIN writes `u := &User{...}` and then `Create(&u)`), while reflect.Indirect
// dereferences only once and therefore reported the surviving pointer as an unsupported type.
func indirectValue(value reflect.Value) reflect.Value {
	for value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}

	return value
}

func (r *OrmBuilder) insertRecords(value any) ([]map[string]any, error) {
	if value == nil {
		return nil, errors.DatabaseUnsupportedType.Args("nil", "struct, map, or a slice of them")
	}

	reflected := indirectValue(reflect.ValueOf(value))

	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		records := make([]map[string]any, 0, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			item := reflected.Index(index)
			if item.Kind() == reflect.Struct && item.CanAddr() {
				item = item.Addr()
			}
			element, err := r.insertRecords(item.Interface())
			if err != nil {
				return nil, err
			}
			records = append(records, element...)
		}

		return records, nil
	case reflect.Map:
		if record, ok := value.(map[string]any); ok {
			return []map[string]any{r.resolveRecordColumns(record)}, nil
		}

		return nil, errors.DatabaseUnsupportedType.Args(reflected.Type().String(), "map[string]any")
	case reflect.Struct:
		record, err := r.structRecord(value)
		if err != nil {
			return nil, err
		}

		return []map[string]any{r.filterInsertColumns(record)}, nil
	default:
		return nil, errors.DatabaseUnsupportedType.Args(reflected.Type().String(), "struct, map, or a slice of them")
	}
}

// structRecord maps a model to the column values an insert needs.
//
// The columns come from the model's own schema rather than from a reflection walk over every
// exported field, so a relation field — which carries no column — is left out instead of being
// inserted as one.
// declaredTable returns the table the query names explicitly with Table(), or "" when it names
// none. A caller who names the table satisfies the model's table requirement, so schema resolution
// does not have to demand TableName() as well.
func (r *OrmBuilder) declaredTable() string {
	source := r.query.TableSource()
	if source == nil {
		return ""
	}

	if name, ok := source.(string); ok {
		return name
	}

	// An already-qualified table is named by an expression; its raw SQL is the name.
	return querybuilder.ExpressionValue(source)
}

func (r *OrmBuilder) structRecord(value any) (map[string]any, error) {
	reflected := indirectValue(reflect.ValueOf(value))
	if !reflected.IsValid() || reflected.Kind() != reflect.Struct {
		return nil, errors.DatabaseUnsupportedType.Args(reflect.TypeOf(value).String(), "struct")
	}
	// Value models cannot be written back, but their SQL record still needs
	// populated automatic fields. Slice elements keep their original address.
	if !reflected.CanSet() {
		copy := reflect.New(reflected.Type()).Elem()
		copy.Set(reflected)
		reflected = copy
	}

	schema, err := parseSchemaWithTable(value, r.declaredTable())
	if err != nil {
		return nil, err
	}

	// Eloquent stamps the automatic timestamps only while the model uses timestamps (performInsert's
	// updateTimestamps gate); a WithoutTimestamps scope or the model's own flag leaves the columns to
	// the caller. With the stamping withheld, an unset automatic column is dropped from the record —
	// Eloquent's insert carries no such attribute, so the database's own default applies — while a
	// value the caller set is written. The Go zero value stands in for "unset".
	timestamps := r.usesTimestamps(value)
	if timestamps {
		if err := r.applyInsertTimestamps(reflected, schema); err != nil {
			return nil, err
		}
	}

	record := make(map[string]any, len(schema.Fields))
	for _, field := range schema.Fields {
		fieldValue, _ := field.BindingValueOf(r.ctx, reflected)
		if !timestamps && (field.AutoCreateTime != 0 || field.AutoUpdateTime != 0) && zeroTimestamp(fieldValue) {
			// A column WithZero names is written all the same, as SQL NULL — Eloquent's null —
			// never a raw '0000-00-00' time.
			if r.writesZero(schema, field.DBName) {
				record[field.DBName] = nil
			}

			continue
		}
		record[field.DBName] = fieldValue
	}

	return record, nil
}

// applyInsertTimestamps fills the model's automatic timestamps, so a column the caller left unset is
// populated and one the caller set is left alone.
func (r *OrmBuilder) applyInsertTimestamps(value reflect.Value, schema *modelSchema) error {
	now := carbon.Now().StdTime()

	for _, field := range schema.Fields {
		if field.AutoCreateTime == 0 && field.AutoUpdateTime == 0 {
			continue
		}

		if current, _ := field.ValueOf(r.ctx, value); !zeroTimestamp(current) {
			continue
		}

		stamp, err := field.timestampValue(now)
		if err != nil {
			return err
		}
		if err := field.Set(r.ctx, value, stamp); err != nil {
			return err
		}
	}
	return nil
}

// filterInsertColumns applies the query's select and omit lists to a record, and leaves a primary
// key the caller did not set out of the row, so the database assigns it.
//
// Select and Omit name columns the way the model does, so a Go field name has to be resolved to the
// column it maps to before the record — which is keyed by column — can be filtered. Left unresolved,
// Select("Name") would match no key and drop every column, and Omit("Avatar") would delete nothing.
//
// A record built from a struct carries every declared field, but an Eloquent insert carries only the
// attributes the model holds — HasAttributes::getAttributesForInsert (:2027) returns getAttributes
// (:2015), the $attributes bag a caller fills by assignment. A Go struct has no attribute bag, so,
// as everywhere else in this port, a field left at its zero value stands for an attribute the model
// does not carry and is omitted, while a non-zero field — or a non-nil pointer to a zero, the typed
// presence marker — is carried. A Select list overrides this: a column named outright, or every
// column under a wildcard, is written whatever it holds, the port's column-scoped write, and the map
// form keeps its own presence because its keys are the attributes. Oracle v12.55.1, executed
// (/tmp/r13/insert_columns_probe.php): a new model with 2 of its 12 columns set inserts
// `("name", "email", "updated_at", "created_at")`; one with nothing set inserts
// `("updated_at", "created_at")`; an explicitly assigned null/false/zero joins the bag, a shape a
// plain Go struct cannot express (its zero is indistinguishable from an unset field) and the map
// form carries.
//
// Eloquent sends the primary key column only when the model's key attribute is set: a fresh
// incrementing model omits it, and so does a key no one has assigned yet, whatever its Go type.
// A struct field carries no "set" flag, so the zero value stands in for unset and a zero sole
// primary key is left out whatever its type. That also drops a key an application set to its own
// zero value, which Eloquent would keep; a struct cannot tell that apart from an unset field.
func (r *OrmBuilder) filterInsertColumns(record map[string]any) map[string]any {
	schema := r.insertSchema()

	columns := selectStringColumns(r.query.Selects())
	wildcard := hasWildcard(columns)
	if len(columns) > 0 && !wildcard {
		selected := make(map[string]any, len(columns))
		for _, column := range columns {
			column = resolveInsertColumn(schema, column)
			if value, ok := record[column]; ok {
				selected[column] = value
			}
		}
		record = selected
	} else if len(columns) == 0 {
		// No select list: the model's attribute-bag analogue. A field left at its zero value
		// stands for an attribute the model does not carry, so the insert omits the column the
		// way Eloquent omits a never-assigned attribute; a non-zero field, or a non-nil pointer
		// to a zero, is carried. An explicit select list (named columns, or a wildcard) skips
		// this and writes its columns whatever they hold.
		for column, value := range record {
			if insertValueUnset(value) && !r.writesZero(schema, column) {
				delete(record, column)
			}
		}
	}

	for _, column := range r.conditions.omit {
		delete(record, resolveInsertColumn(schema, column))
	}

	if column := r.solePrimaryKeyColumn(); column != "" {
		if value, ok := record[column]; ok && isZeroValue(value) && !r.explicitlyWritesZero(schema, column) {
			delete(record, column)
		}
	}

	return record
}

// explicitlyWritesZero is writesZero without the wildcard: a sole primary key's zero joins an insert
// only when WithZero names its column, never through "*", since a zero key is the database's to assign.
func (r *OrmBuilder) explicitlyWritesZero(schema *modelSchema, column string) bool {
	for _, name := range r.conditions.withZero {
		if name != "*" && resolveInsertColumn(schema, name) == column {
			return true
		}
	}

	return false
}

// insertValueUnset reports whether a bound record value stands for an attribute the model does not
// carry — the zero-value analogue of Eloquent's absent-attribute case. A JSON column's value binds
// through a jsonColumnValuer wrapper, whose non-nil interface would read as present whatever it
// wraps, so the wrapper is unwrapped to the value it carries before the zero test: a zero JSON
// column is an absent attribute like any other.
func insertValueUnset(value any) bool {
	if wrapped, ok := value.(jsonColumnValuer); ok {
		return isZeroValue(wrapped.Valuer)
	}

	return isZeroValue(value)
}

// resolveRecordColumns rewrites a caller-supplied record so its keys name the columns the model
// declares.
//
// A map handed to Create is keyed the way the caller wrote it -- a Go field name such as "Avatar" --
// while every later step, and the statement itself, expects the column ("avatar"). Only the struct
// path produced a column-keyed record, so a map's key reached the statement verbatim and a Go field
// name became a column the table does not have, which the dialect rejected (PostgreSQL answers
// 42703). A name the model does not declare is left as written, so a raw column or expression still
// passes through.
func (r *OrmBuilder) resolveRecordColumns(record map[string]any) map[string]any {
	schema := r.insertSchema()
	if schema == nil {
		return record
	}

	resolved := make(map[string]any, len(record))
	for key, value := range record {
		column := resolveInsertColumn(schema, key)
		resolved[column] = modelColumnBinding(schema, column, value)
	}

	return resolved
}

// resolveInsertColumn maps a name to the column the model declares, accepting a Go field name, a
// column name, or a table-qualified column ("users.name").
//
// resolveColumn matches only the first two, so a qualified name resolved to itself, matched no
// column-keyed record key, and was silently dropped by Select or kept by Omit; unwrapping the table
// part closes that edge. Only a qualifier whose remainder is a declared column is unwrapped, so an
// expression that happens to contain a dot, or any name the model does not know, is left alone.
func resolveInsertColumn(schema *modelSchema, name string) string {
	if schema == nil {
		return name
	}

	if column := resolveColumn(schema, name); column != name {
		return column
	}

	if _, column, ok := strings.Cut(name, "."); ok {
		if field, exists := schema.FieldsByDBName[resolveColumn(schema, column)]; exists {
			return field.DBName
		}
	}

	return name
}

// insertSchema is the schema of the model the insert writes to, or nil when the query names no model,
// in which case identifiers are taken as written.
func (r *OrmBuilder) insertSchema() *modelSchema {
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

// hasWildcard reports whether a select list contains a bare "*", which selects every column and so
// restricts nothing.
func hasWildcard(columns []string) bool {
	for _, column := range columns {
		if column == "*" {
			return true
		}
	}

	return false
}

// solePrimaryKeyColumn returns the column of a sole primary key, which an insert leaves out when the
// caller has not set it. A composite key names more than one column, so no single one can stand for
// it and the model's columns are taken as they are.
func (r *OrmBuilder) solePrimaryKeyColumn() string {
	if field := r.primaryKeyField(); field != nil {
		return field.DBName
	}

	return ""
}

// primaryKeyField returns the model's sole primary key field, so the insert key read-back can be
// typed by the column it belongs to. It is nil when the query names no model or the model has a
// composite key, in which case no single key is there to read back.
func (r *OrmBuilder) primaryKeyField() *schemaField {
	schema := r.insertSchema()
	if schema == nil || len(schema.PrimaryFields) != 1 {
		return nil
	}

	return schema.PrimaryFields[0]
}

// isIntegerKeyType reports whether a primary key field's Go type is an integer kind, the only kind
// a database auto-generates and reports back through the driver.
func isIntegerKeyType(fieldType reflect.Type) bool {
	fieldType = indirectType(fieldType)
	if fieldType == nil {
		return false
	}

	switch fieldType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

// isZeroValue reports whether a mapped column value holds the zero value of its own type.
func isZeroValue(value any) bool {
	if value == nil {
		return true
	}

	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return true
	}

	return reflected.IsZero()
}

// insertRows inserts the records and returns the generated keys, one per record.
//
// Each record is inserted by its own statement: a dialect that returns the generated key does so
// for one row, so a statement carrying a whole batch could not report the keys back.
func (r *OrmBuilder) insertRows(records []map[string]any) ([]any, error) {
	keys := make([]any, 0, len(records))

	for _, record := range records {
		key, err := r.insertRow(record)
		if err != nil {
			return nil, err
		}

		keys = append(keys, key)
	}

	return keys, nil
}

// uniformInsertRecords gives every record in a batch the same columns, the union across the batch,
// filling a column a record does not carry with nil.
//
// A record built from a struct omits a field left at its zero value, so a batch built from a slice of
// structs can differ column by column: the first record may carry a name while a later one also carries
// a pointer column the first left nil. The grammar takes the column list from the first record and
// reuses it for every record, as Laravel's compileInsert does, so a column only a later record carries
// never reaches the statement. Execution does not drop it, because it inserts a row at a time with the
// record's own columns, so without this the reported statement of a batch would disagree with what
// execution writes. Giving every record the union makes the reported statement carry every column
// execution writes; a nil stands for a column a row does not carry, which is the value an absent field
// already has. A single record is returned untouched, so a one-row insert stays byte for byte what it
// was.
//
// It mirrors database/db's guard of the same name, which fixes the same class on the facade path.
func uniformInsertRecords(records []map[string]any) []map[string]any {
	if len(records) < 2 {
		return records
	}

	columns := make(map[string]struct{})
	for _, record := range records {
		for column := range record {
			columns[column] = struct{}{}
		}
	}

	headers := make([]string, 0, len(columns))
	for column := range columns {
		headers = append(headers, column)
	}
	sort.Strings(headers)

	uniform := make([]map[string]any, len(records))
	for index, record := range records {
		row := make(map[string]any, len(headers))
		for _, column := range headers {
			row[column] = record[column]
		}
		uniform[index] = row
	}

	return uniform
}

// compileInsertAll renders records as one insert statement and its bindings.
//
// It is separate from running the statement so the SQL can be asserted without a connection, which is
// what catches a table that failed to resolve. The records are rendered together because the grammar
// builds one statement for the batch, even though execution inserts a row at a time to read its key
// back. The batch is first given the union of its columns, so a column only a later record carries is
// still reported rather than hidden behind the first record's column list.
func (r *OrmBuilder) compileInsertAll(records []map[string]any) (string, []any, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	records = uniformInsertRecords(records)
	records = insertGetIdValues(records)

	// The generated key is reported by column name: PostgreSQL appends `returning <key>` and SQL
	// Server an `output inserted.<key>` clause. Laravel falls back to "id" only when the caller
	// passes no name, but the model knows its own key, so a primary key whose column is not `id`
	// (say `gorm:"primaryKey;column:user_id"`) is named correctly instead of the statement asking
	// for a column the table does not have, which SQL Server rejects ("Invalid column name 'id'").
	sequence := r.solePrimaryKeyColumn()

	statement, bindings := builder.ToInsertGetIdSQL(records, sequence)
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return statement, bindings, nil
}

// insertGetIdValues hands the compiler the values list Eloquent's insertGetId receives for
// attribute arrays with no entries. performInsert on an incrementing model always calls
// insertGetId($attributes) — the empty-attribute guard sits in the non-incrementing branch — and an
// empty array reaches the grammar's empty-values branch, which renders Laravel's empty insert:
// `insert into <table> default values` (PostgreSQL appends `returning "<key>"`, SQL Server an
// `output inserted.<key>` clause, and MySQL spells it `() values ()`, as Laravel's own grammar
// does). Passing the one-record list through instead would compile the invalid `() values ()` on
// the other dialects (sqlite: near ")" syntax error).
//
// A batch whose records are all empty is that same empty attribute array asked for once per row:
// Create on a slice of empty incrementing models runs insertGetId([]) once per record, so the
// values list the batch is handed to the compiler as is the empty one however many records it
// holds. Laravel renders the literal batch `[[], []]` as `insert into <table> () values (), ()`
// (oracle, illuminate/database v12.55.1: the same on every dialect, because the empty column list
// columnizes to nothing and each record parameterizes to an empty tuple), and only MySQL accepts
// that statement — sqlite, PostgreSQL and SQL Server reject any empty column list (sqlite: near ")"
// syntax error) — so the literal batch would report a statement the target dialect cannot run
// while execution runs the empty insert. A batch carrying any column is returned untouched.
func insertGetIdValues(records []map[string]any) []map[string]any {
	if len(records) == 0 {
		return records
	}

	for _, record := range records {
		if len(record) > 0 {
			return records
		}
	}

	return nil
}

// compileInsert renders one record as an insert statement and its bindings.
func (r *OrmBuilder) compileInsert(record map[string]any) (string, []any, error) {
	return r.compileInsertAll([]map[string]any{record})
}

// insertRow inserts one record and returns its generated key, or nil when the dialect cannot
// report one.
func (r *OrmBuilder) insertRow(record map[string]any) (any, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return nil, err
	}
	pool, err := r.queryPool()
	if err != nil {
		return nil, err
	}

	connection := &insertConnection{query: r, pool: pool}
	return builder.InsertGetID(connection, insertGetIdValues([]map[string]any{record}), r.solePrimaryKeyColumn())
}

// readReturnedKey reads the key a `returning` insert reports back into the type of the primary key
// column it belongs to.
//
// Scanning the value into a fixed int64 assumed every primary key is an integer, so a string key — a
// ULID or UUID — failed to scan on PostgreSQL ("converting driver.Value type string ... to a int64")
// and, where it did not error, was coerced to a number. The target is built from the field's own Go
// type instead, so an integer key still comes back as an integer and a string key comes back as a
// string. A query that names no model has no type to go on, so the value is returned as read.
func (r *OrmBuilder) readReturnedKey(statement string, bindings []any) (any, error) {
	field := r.primaryKeyField()

	var target any = new(any)
	if field != nil {
		target = reflect.New(field.FieldType).Interface()
	}

	if err := r.writerScalar(target, statement, bindings); err != nil {
		return nil, err
	}

	return reflect.ValueOf(target).Elem().Interface(), nil
}

// assignInsertedKeys writes the generated keys back onto the destination, so a caller can read the
// id of the row it just created.
func (r *OrmBuilder) assignInsertedKeys(dest any, keys []any) {
	if len(keys) == 0 {
		return
	}

	reflected := reflect.Indirect(reflect.ValueOf(dest))
	if !reflected.IsValid() {
		return
	}

	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
		for index := 0; index < reflected.Len() && index < len(keys); index++ {
			r.assignInsertedKey(reflected.Index(index).Addr().Interface(), keys[index])
		}

		return
	}

	r.assignInsertedKey(dest, keys[0])
}

// assignInsertedKey sets the model's primary key field to the generated key.
func (r *OrmBuilder) assignInsertedKey(dest any, key any) {
	if dest == nil || key == nil {
		return
	}

	model := r.parentModel()
	if model == nil {
		model = dest
	}

	schema, err := parseSchemaWithTable(model, r.declaredTable())
	if err != nil || len(schema.PrimaryFields) == 0 {
		return
	}

	reflected := reflect.ValueOf(dest)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return
	}

	_ = schema.PrimaryFields[0].Set(r.ctx, reflected, key)
}

// execInsert runs the whole insert for a destination and populates its keys, reporting whether a row
// was written. The event hooks stay with the caller, which runs them around this call; an insert
// skipped by the empty-attribute guard reports false so the created hook is left out too.
func (r *OrmBuilder) execInsert(dest any) (bool, error) {
	records, err := r.insertRecords(dest)
	if err != nil {
		return false, err
	}
	if len(records) == 0 {
		return false, nil
	}

	if r.skipsEmptyNonIncrementingInsert(dest, records) {
		return false, nil
	}

	keys, err := r.insertRows(records)
	if err != nil {
		return false, err
	}

	r.assignInsertedKeys(dest, keys)

	return true, nil
}

// skipsEmptyNonIncrementingInsert mirrors the guard in Eloquent's Model::performInsert: a model
// whose table does not generate the key — no primary key column is declared, which is the Go form of
// $incrementing = false, since one sole primary key is what the generated-key read-back needs — and
// whose insert carries no attribute at all is left unsaved, without an insert and without the
// created event: `if (empty($attributes)) { return true; }`. A map handed to Create is the
// attribute array itself — create(['name' => null]) carries an attribute, so the guard lets that
// insert run — therefore only a map record with no key at all is the empty attribute set. A struct
// record names every declared field instead, and the Go zero stands in for an unset attribute there,
// the same stand-in the insert column filter documents, unless WithZero names the column.
func (r *OrmBuilder) skipsEmptyNonIncrementingInsert(dest any, records []map[string]any) bool {
	model := r.parentModel()
	if model == nil {
		model = dest
	}

	schema, err := parseSchemaWithTable(model, r.declaredTable())
	if err != nil || len(schema.PrimaryFields) > 0 {
		return false
	}

	// A map record is the attribute set itself, so any key means the insert has an attribute and
	// runs; a struct record carries every declared field, and each zero stands in for an unset
	// attribute.
	fromMap := indirectValue(reflect.ValueOf(dest)).Kind() == reflect.Map

	for _, record := range records {
		if fromMap {
			if len(record) > 0 {
				return false
			}

			continue
		}

		for column, value := range record {
			// A zero WithZero names is an assigned attribute, not an unset one: Eloquent's
			// create(['flag' => false]) inserts it (oracle v12.55.1, /tmp/r15v/probe_v.out P3).
			if !isZeroValue(value) || r.writesZero(schema, column) {
				return false
			}
		}
	}

	return true
}
