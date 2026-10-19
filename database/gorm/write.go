package gorm

import (
	"reflect"
	"sort"
	"strings"

	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
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

func (r *Query) insertRecords(value any) ([]map[string]any, error) {
	if value == nil {
		return nil, errors.DatabaseUnsupportedType.Args("nil", "struct, map, or a slice of them")
	}

	reflected := indirectValue(reflect.ValueOf(value))

	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		records := make([]map[string]any, 0, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			element, err := r.insertRecords(reflected.Index(index).Interface())
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
func (r *Query) declaredTable() string {
	if r.conditions.table != nil {
		return r.conditions.table.name
	}

	return ""
}

func (r *Query) structRecord(value any) (map[string]any, error) {
	reflected := indirectValue(reflect.ValueOf(value))
	if !reflected.IsValid() || reflected.Kind() != reflect.Struct {
		return nil, errors.DatabaseUnsupportedType.Args(reflect.TypeOf(value).String(), "struct")
	}

	schema, err := parseSchemaWithTable(r.instance, value, r.declaredTable())
	if err != nil {
		return nil, err
	}

	r.applyInsertTimestamps(reflected, schema)

	record := make(map[string]any, len(schema.Fields))
	for _, field := range schema.Fields {
		fieldValue, _ := field.ValueOf(r.ctx, reflected)
		record[field.DBName] = fieldValue
	}

	return record, nil
}

// applyInsertTimestamps fills the model's automatic timestamps, so a column the caller left unset is
// populated and one the caller set is left alone.
func (r *Query) applyInsertTimestamps(value reflect.Value, schema *modelSchema) {
	now := carbon.NewDateTime(carbon.Now())

	for _, field := range schema.Fields {
		if field.AutoCreateTime == 0 && field.AutoUpdateTime == 0 {
			continue
		}

		if _, zero := field.ValueOf(r.ctx, value); !zero {
			continue
		}

		_ = field.Set(r.ctx, value, now)
	}
}

// filterInsertColumns applies the query's select and omit lists to a record, and leaves a primary
// key the caller did not set out of the row, so the database assigns it.
//
// Select and Omit name columns the way the model does, so a Go field name has to be resolved to the
// column it maps to before the record — which is keyed by column — can be filtered. Left unresolved,
// Select("Name") would match no key and drop every column, and Omit("Avatar") would delete nothing.
//
// Eloquent sends the primary key column only when the model's key attribute is set: a fresh
// incrementing model omits it, and so does a key no one has assigned yet, whatever its Go type.
// A struct field carries no "set" flag, so the zero value stands in for unset and a zero sole
// primary key is left out whatever its type. That also drops a key an application set to its own
// zero value, which Eloquent would keep; a struct cannot tell that apart from an unset field.
func (r *Query) filterInsertColumns(record map[string]any) map[string]any {
	schema := r.insertSchema()

	if len(r.conditions.selectColumns) > 0 && !hasWildcard(r.conditions.selectColumns) {
		selected := make(map[string]any, len(r.conditions.selectColumns))
		for _, column := range r.conditions.selectColumns {
			column = resolveInsertColumn(schema, column)
			if value, ok := record[column]; ok {
				selected[column] = value
			}
		}
		record = selected
	}

	for _, column := range r.conditions.omit {
		delete(record, resolveInsertColumn(schema, column))
	}

	if column := r.solePrimaryKeyColumn(); column != "" {
		if value, ok := record[column]; ok && isZeroValue(value) {
			delete(record, column)
		}
	}

	return record
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
func (r *Query) resolveRecordColumns(record map[string]any) map[string]any {
	schema := r.insertSchema()
	if schema == nil {
		return record
	}

	resolved := make(map[string]any, len(record))
	for key, value := range record {
		resolved[resolveInsertColumn(schema, key)] = value
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
func (r *Query) insertSchema() *modelSchema {
	model := r.parentModel()
	if model == nil {
		return nil
	}

	schema, err := parseSchemaWithTable(r.instance, model, r.declaredTable())
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
func (r *Query) solePrimaryKeyColumn() string {
	if field := r.primaryKeyField(); field != nil {
		return field.DBName
	}

	return ""
}

// primaryKeyField returns the model's sole primary key field, so the insert key read-back can be
// typed by the column it belongs to. It is nil when the query names no model or the model has a
// composite key, in which case no single key is there to read back.
func (r *Query) primaryKeyField() *schemaField {
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
func (r *Query) insertRows(records []map[string]any) ([]any, error) {
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
func (r *Query) compileInsertAll(records []map[string]any) (string, []any, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	records = uniformInsertRecords(records)

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

	// SQL Server cannot report the generated key through LastInsertId - go-mssqldb refuses it by
	// design ("... use the OUTPUT clause ...") - so its statement carries an OUTPUT clause and reports
	// the key itself, the way PostgreSQL's grammar appends a returning clause. Laravel reads the key
	// back out of band in its SQL Server processor, but a following SCOPE_IDENTITY() query would run
	// on a pooled connection that may differ from the insert's, so the key has to travel with the
	// insert.
	if sqlServer, ok := r.queryGrammar.(*querybuilder.SqlServerGrammar); ok {
		statement = sqlServer.CompileInsertGetIdOutput(builder, records, sequence)
	}

	return statement, bindings, nil
}

// returnsKeyInStatement reports whether a compiled insert reports the generated key in the statement
// itself rather than through the driver's LastInsertId: PostgreSQL appends a `returning` clause and
// SQL Server an `output inserted.<key>` clause.
func returnsKeyInStatement(statement string) bool {
	lowered := strings.ToLower(statement)

	return strings.Contains(lowered, " returning ") || strings.Contains(lowered, " output inserted.")
}

// compileInsert renders one record as an insert statement and its bindings.
func (r *Query) compileInsert(record map[string]any) (string, []any, error) {
	return r.compileInsertAll([]map[string]any{record})
}

// insertRow inserts one record and returns its generated key, or nil when the dialect cannot
// report one.
func (r *Query) insertRow(record map[string]any) (any, error) {
	statement, bindings, err := r.compileInsert(record)
	if err != nil {
		return nil, err
	}

	pool, err := r.queryPool()
	if err != nil {
		return nil, err
	}

	// A dialect that returns the key puts it in the statement itself: PostgreSQL appends a
	// `returning` clause and SQL Server an `output inserted.<key>` clause. The others report it
	// through the driver. Either way the value is read back as the primary key column's own type,
	// because a key is not always an integer: a ULID or UUID primary key is a string.
	if returnsKeyInStatement(statement) {
		return r.readReturnedKey(statement, bindings)
	}

	begin := carbon.Now()
	result, err := pool.ExecContext(r.ctx, querybuilder.ReplacePlaceholders(statement, r.dbConfig.Driver), bindings...)
	if err != nil {
		r.trace(begin, statement, bindings, -1, err)

		return nil, err
	}

	r.trace(begin, statement, bindings, 1, nil)

	// A driver reports a generated key only for an auto-increment integer primary key. A string key
	// (a ULID or UUID) is supplied by the caller, and LastInsertId would hand back an unrelated rowid
	// that schemaField.Set then coerced into a one-byte string. Leave such a key alone.
	if field := r.primaryKeyField(); field != nil && !isIntegerKeyType(field.FieldType) {
		return nil, nil
	}

	lastID, err := result.LastInsertId()
	if err != nil {
		// The row is inserted either way; only the key is unavailable. A dialect that cannot serve
		// LastInsertId is supposed to have reported the key in the statement above, as SQL Server now
		// does, so reaching here means the driver genuinely could not supply a key. Report it rather
		// than hide it behind a nil result - a silent nil is what left the key unassigned.
		return nil, err
	}

	return lastID, nil
}

// readReturnedKey reads the key a `returning` insert reports back into the type of the primary key
// column it belongs to.
//
// Scanning the value into a fixed int64 assumed every primary key is an integer, so a string key — a
// ULID or UUID — failed to scan on PostgreSQL ("converting driver.Value type string ... to a int64")
// and, where it did not error, was coerced to a number. The target is built from the field's own Go
// type instead, so an integer key still comes back as an integer and a string key comes back as a
// string. A query that names no model has no type to go on, so the value is returned as read.
func (r *Query) readReturnedKey(statement string, bindings []any) (any, error) {
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
func (r *Query) assignInsertedKeys(dest any, keys []any) {
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
func (r *Query) assignInsertedKey(dest any, key any) {
	if dest == nil || key == nil {
		return
	}

	model := r.parentModel()
	if model == nil {
		model = dest
	}

	schema, err := parseSchemaWithTable(r.instance, model, r.declaredTable())
	if err != nil || len(schema.PrimaryFields) == 0 {
		return
	}

	reflected := reflect.ValueOf(dest)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return
	}

	_ = schema.PrimaryFields[0].Set(r.ctx, reflected, key)
}

// execInsert runs the whole insert for a destination and populates its keys. The event hooks stay
// with the caller, which runs them around this call.
func (r *Query) execInsert(dest any) error {
	records, err := r.insertRecords(dest)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	keys, err := r.insertRows(records)
	if err != nil {
		return err
	}

	r.assignInsertedKeys(dest, keys)

	return nil
}
