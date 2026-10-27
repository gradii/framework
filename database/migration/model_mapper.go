package migration

import (
	"cmp"
	"database/sql/driver"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	contractsschema "github.com/gradii/framework/contracts/database/schema"
	"github.com/gradii/framework/database/internal/annotation"
	"github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/str"
)

const tablePrefix = "table."

// dataTypeMapping maps SQL type names to blueprint methods. Used for the values of a `type:` tag
// that are not one of the primitive kinds.
var dataTypeMapping = map[string]string{
	"jsonb":     contractsschema.MethodJsonb,
	"json":      contractsschema.MethodJson,
	"text":      contractsschema.MethodText,
	"binary":    contractsschema.MethodBinary,
	"varbinary": contractsschema.MethodBinary,
	"blob":      contractsschema.MethodBinary,
	"decimal":   contractsschema.MethodDecimal,
	"numeric":   contractsschema.MethodDecimal,
	"uuid":      contractsschema.MethodUuid,
	"ulid":      contractsschema.MethodUlid,
	"date":      contractsschema.MethodDate,
	"time":      contractsschema.MethodTime,
}

// stringTypePrefixes contains SQL string type prefixes for type detection.
var stringTypePrefixes = []string{"char", "varchar", "nvarchar", "varchar2", "nchar"}

// dataTypeKeys is dataTypeMapping's keys ordered longest first. Go randomises map iteration, and a
// value such as "jsonb" would otherwise match both the "jsonb" and the "json" entry depending on
// where the loop happened to start, making generated migrations unstable.
var dataTypeKeys = func() []string {
	keys := make([]string, 0, len(dataTypeMapping))
	for key := range dataTypeMapping {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})

	return keys
}()

// Generate creates migration schema lines from a model struct.
//
// The model is read through the framework's own field annotations (database/internal/annotation.ParseAnnotation)
// and Go's type system, not through GORM's schema parser: the column name comes from `column:` or
// the snake_cased field name, the column type from the Go type with `type:` and `migration:`
// overriding it, and primary keys, indexes and the automatic timestamps from their annotations.
//
// It returns the table name and the schema lines, in which the columns come first and the index and
// constraint definitions follow after an empty separator line.
func Generate(model any) (string, []string, error) {
	modelType := modelStructType(model)
	if modelType == nil {
		return "", nil, errors.SchemaInvalidModel
	}

	table := modelTable(modelType)
	if table == "" {
		return "", nil, errors.SchemaInvalidModel
	}

	fields := primaryKeys(parseModelFields(modelType))
	if len(fields) == 0 {
		return "", nil, errors.SchemaInvalidModel
	}

	var (
		lines []string
		seen  = make(map[string]bool)
	)
	for _, field := range fields {
		if field.annotation.Skip || field.annotation.SkipMigration || seen[field.column] {
			continue
		}
		seen[field.column] = true

		if line := renderColumn(field); line != "" {
			lines = append(lines, line)
		}
	}

	if indexLines := renderIndexes(table, fields); len(indexLines) > 0 {
		lines = append(lines, "", strings.Join(indexLines, "\n"))
	}

	if len(lines) == 0 {
		return "", nil, errors.SchemaInvalidModel
	}

	return table, lines, nil
}

// modelField is one column the generator emits.
type modelField struct {
	field      reflect.StructField
	column     string
	annotation annotation.Annotation

	// primaryKey and autoIncrement are decided across the whole model, not from a single field, so
	// they are filled in by primaryKeys once every field is known.
	primaryKey    bool
	autoIncrement bool
}

// modelStructType unwraps pointers, slices and arrays down to the struct type they carry, returning
// nil when the value does not lead to a struct.
func modelStructType(model any) reflect.Type {
	if model == nil {
		return nil
	}

	modelType := reflect.TypeOf(model)
	for modelType != nil && (modelType.Kind() == reflect.Pointer || modelType.Kind() == reflect.Slice || modelType.Kind() == reflect.Array) {
		modelType = modelType.Elem()
	}

	if modelType == nil || modelType.Kind() != reflect.Struct {
		return nil
	}

	return modelType
}

// modelTable returns the table a model maps to, using the name the model declares and falling back
// to its snake_cased struct name. No pluralizer is involved, matching how the ORM resolves tables.
func modelTable(modelType reflect.Type) string {
	if named, ok := reflect.New(modelType).Interface().(contractsorm.ModelWithTableName); ok {
		if name := strings.TrimSpace(named.TableName()); name != "" {
			return name
		}
	}

	return str.Of(modelType.Name()).Snake().String()
}

// parseModelFields flattens a model into its columns, in declaration order.
//
// An embedded struct contributes its own fields at the point it is declared, which is why a model
// composing orm.Model emits created_at, updated_at and id before its own columns.
func parseModelFields(modelType reflect.Type) []modelField {
	var fields []modelField

	for index := 0; index < modelType.NumField(); index++ {
		structField := modelType.Field(index)
		if !structField.IsExported() {
			continue
		}

		annotation := annotation.ParseAnnotation(structField.Tag.Get("gorm"))
		if annotation.Skip {
			continue
		}

		if structField.Anonymous && structField.Type.Kind() == reflect.Struct {
			fields = append(fields, parseModelFields(structField.Type)...)
			continue
		}

		fields = append(fields, modelField{
			field:      structField,
			column:     columnName(structField, annotation),
			annotation: annotation,
		})
	}

	return fields
}

// columnName resolves a field's database column.
func columnName(structField reflect.StructField, annotation annotation.Annotation) string {
	if annotation.Column != "" {
		return annotation.Column
	}

	return str.Of(structField.Name).Snake().String()
}

// primaryKeys marks which columns form the primary key and which of them auto-increments.
//
// A field tagged primaryKey is part of the key; when no field carries the tag, a column named "id"
// is the key, which is the convention every model here follows. A key auto-increments only when it
// is the sole key and holds an integer, so a composite key stays a plain column pair.
func primaryKeys(fields []modelField) []modelField {
	keyCount := 0
	for index := range fields {
		if fields[index].annotation.PrimaryKey {
			fields[index].primaryKey = true
			keyCount++
		}
	}

	if keyCount == 0 {
		for index := range fields {
			if fields[index].column == "id" {
				fields[index].primaryKey = true
				keyCount++
				break
			}
		}
	}

	if keyCount == 1 {
		for index := range fields {
			if fields[index].primaryKey && isIntegerKind(fields[index].field.Type) {
				fields[index].autoIncrement = true
			}
		}
	}

	return fields
}

// renderColumn renders one column definition and its modifiers.
func renderColumn(field modelField) string {
	method, args := columnMethod(field)
	if method == "" {
		return ""
	}

	b := &atom{Builder: strings.Builder{}}
	b.Grow(64)
	b.WriteString(tablePrefix)
	b.WriteMethod(method, append([]any{field.column}, args...)...)

	if method == contractsschema.MethodDecimal {
		if scale := tagInt(field.field, "scale"); scale > 0 {
			b.WriteMethod(contractsschema.MethodPlaces, scale)
		}
		if precision := tagInt(field.field, "precision"); precision > 0 {
			b.WriteMethod(contractsschema.MethodTotal, precision)
		}
	}

	// An unsigned tag on an already-unsigned method would be rejected by the blueprint, so it is
	// only applied to the signed ones.
	if !strings.Contains(method, "Unsigned") && tagPresent(field.field, "unsigned") {
		b.WriteMethod(contractsschema.MethodUnsigned)
	}

	if isNullable(field) {
		b.WriteMethod(contractsschema.MethodNullable)
	}

	if defaultValue := tagValue(field.field, "default"); defaultValue != "" {
		b.WriteMethod(contractsschema.MethodDefault, parseVal(trimQuotes(defaultValue)))
	}
	if comment := tagValue(field.field, "comment"); comment != "" {
		b.WriteMethod(contractsschema.MethodComment, trimQuotes(comment))
	}

	return b.String()
}

// isNullable reports whether the column admits NULL: a Go pointer, or a type designed for a
// nullable column. A primary key and an explicit NOT NULL always win.
func isNullable(field modelField) bool {
	if field.primaryKey || tagPresent(field.field, "not null") {
		return false
	}

	return field.field.Type.Kind() == reflect.Pointer || isSQLNullType(field.field.Type)
}

// columnMethod picks the blueprint method for a column, and any arguments it takes.
//
// Priority: an explicit `migration:` method, then the model's automatic key, then an explicit
// `type:` tag, and finally the Go type.
func columnMethod(field modelField) (string, []any) {
	if method := tagValue(field.field, "migration"); method != "" {
		return str.Of(method).Studly().String(), nil
	}

	if field.autoIncrement {
		if typeSize(field.field.Type) <= 32 {
			return contractsschema.MethodIncrements, nil
		}
		return contractsschema.MethodBigIncrements, nil
	}

	if typeTag := tagValue(field.field, "type"); typeTag != "" {
		return typeMethod(typeTag)
	}

	return goTypeMethod(field.field.Type)
}

// typeMethod maps the value of a `type:` tag to a blueprint method.
func typeMethod(typeTag string) (string, []any) {
	lower := strings.ToLower(strings.TrimSpace(typeTag))

	switch lower {
	case "bool", "boolean":
		return contractsschema.MethodBoolean, nil
	case "int", "integer":
		return contractsschema.MethodInteger, nil
	case "uint":
		return contractsschema.MethodUnsignedInteger, nil
	case "float":
		return contractsschema.MethodFloat, nil
	case "double":
		return contractsschema.MethodDouble, nil
	case "string", "varchar":
		return contractsschema.MethodString, nil
	case "bytes", "blob":
		return contractsschema.MethodBinary, nil
	}

	if strings.HasPrefix(lower, "enum") {
		return contractsschema.MethodEnum, []any{parseEnum(typeTag)}
	}

	for _, prefix := range stringTypePrefixes {
		if strings.HasPrefix(lower, prefix) {
			if size := parseTypeSize(lower); size > 0 {
				return contractsschema.MethodString, []any{size}
			}
			return contractsschema.MethodString, nil
		}
	}

	if strings.HasPrefix(lower, "timestamp") || strings.HasPrefix(lower, "datetime") {
		if strings.Contains(lower, "tz") {
			return contractsschema.MethodTimestampTz, nil
		}
		return contractsschema.MethodTimestamp, nil
	}

	for _, key := range dataTypeKeys {
		if strings.Contains(lower, key) {
			return dataTypeMapping[key], nil
		}
	}

	return contractsschema.MethodText, nil
}

// goTypeMethod infers a blueprint method from a Go type.
func goTypeMethod(fieldType reflect.Type) (string, []any) {
	fieldType = indirectType(fieldType)

	switch fieldType.Kind() {
	case reflect.Bool:
		return contractsschema.MethodBoolean, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intMethod(typeSize(fieldType), false), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return intMethod(typeSize(fieldType), true), nil
	case reflect.Float32:
		return contractsschema.MethodFloat, nil
	case reflect.Float64:
		return contractsschema.MethodDouble, nil
	case reflect.String:
		return contractsschema.MethodString, nil
	case reflect.Slice:
		if indirectType(fieldType.Elem()).Kind() == reflect.Uint8 {
			return contractsschema.MethodBinary, nil
		}
	case reflect.Map:
		return contractsschema.MethodJson, nil
	case reflect.Struct:
		// A struct that carries its database value — a timestamp wrapper, sql.NullString, a
		// soft-delete marker — is described by the value it stores.
		if valueType := valuerValueType(fieldType); valueType != nil {
			return goTypeMethod(valueType)
		}
		if fieldType.ConvertibleTo(reflect.TypeOf(time.Time{})) {
			return contractsschema.MethodTimestampTz, nil
		}
	}

	return contractsschema.MethodText, nil
}

// valuerValueType returns the type a struct carries as its database value, or nil when the struct
// is not a driver.Valuer.
//
// The carried type is found by walking the struct's fields in declaration order and taking the
// first scalar or time-like one. Calling Value() would not work, because a zero value such as
// sql.NullString reports nil and would tell us nothing.
//
// The walk descends through nested structs and pointers, and reads unexported fields: wrappers are
// routinely a struct embedding a pointer to the type that actually stores the time, as carbon's
// LayoutType does with *Carbon.
func valuerValueType(fieldType reflect.Type) reflect.Type {
	valuerType := reflect.TypeOf((*driver.Valuer)(nil)).Elem()
	if !fieldType.Implements(valuerType) && !reflect.PointerTo(fieldType).Implements(valuerType) {
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
		fieldType_ := fieldType.Field(index).Type

		switch indirectType(fieldType_).Kind() {
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64, reflect.String:
			return fieldType_
		case reflect.Struct:
			if isTimeType(fieldType_) {
				return fieldType_
			}
			if carried := carriedType(fieldType_, depth+1); carried != nil {
				return carried
			}
		case reflect.Pointer, reflect.Slice:
			if indirectType(fieldType_).Kind() == reflect.Struct && isTimeType(fieldType_) {
				return fieldType_
			}
			if carried := carriedType(fieldType_, depth+1); carried != nil {
				return carried
			}
		}
	}

	return nil
}

// isTimeType reports whether a type holds a time.Time, either directly or as its underlying type.
func isTimeType(fieldType reflect.Type) bool {
	fieldType = indirectType(fieldType)
	if fieldType == nil {
		return false
	}

	return fieldType == reflect.TypeOf(time.Time{}) || fieldType.ConvertibleTo(reflect.TypeOf(time.Time{}))
}

// indirectType unwraps pointers and arrays down to the type they carry.
func indirectType(fieldType reflect.Type) reflect.Type {
	for fieldType != nil && (fieldType.Kind() == reflect.Pointer || fieldType.Kind() == reflect.Array) {
		fieldType = fieldType.Elem()
	}

	return fieldType
}

// isIntegerKind reports whether a type is one of Go's integers.
func isIntegerKind(fieldType reflect.Type) bool {
	switch indirectType(fieldType).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

// typeSize returns the bit width the blueprint methods need for a Go type.
func typeSize(fieldType reflect.Type) int {
	switch indirectType(fieldType).Kind() {
	case reflect.Int8, reflect.Uint8:
		return 8
	case reflect.Int16, reflect.Uint16:
		return 16
	case reflect.Int32, reflect.Uint32, reflect.Float32:
		return 32
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64, reflect.Float64:
		return 64
	default:
		return 0
	}
}

// renderIndexes emits the composite primary key, the field indexes and the unique constraints.
func renderIndexes(table string, fields []modelField) []string {
	var (
		lines []string
		seen  = make(map[string]bool)
	)

	add := func(key, line string) {
		if !seen[key] {
			seen[key] = true
			lines = append(lines, line)
		}
	}

	var primaryColumns []string
	for _, field := range fields {
		if field.primaryKey {
			primaryColumns = append(primaryColumns, field.column)
		}
	}
	if len(primaryColumns) > 1 {
		add("PK:"+strings.Join(primaryColumns, ","), formatIndex(contractsschema.IndexMethodPrimary, primaryColumns, ""))
	}

	// Indexes are sorted by name so regenerating a migration from an unchanged model produces the
	// same file.
	type namedIndex struct {
		key    string
		method string
		column string
		name   string
	}
	var indexes []namedIndex
	for _, field := range fields {
		if field.annotation.UniqueIndex {
			indexes = append(indexes, namedIndex{
				key:    contractsschema.IndexClassUnique + ":" + field.column,
				method: contractsschema.IndexMethodUnique,
				column: field.column,
				name:   cmp.Or(field.annotation.UniqueIndexName, defaultIndexName(table, field.column)),
			})
		}
		if field.annotation.Index {
			indexes = append(indexes, namedIndex{
				key:    "INDEX:" + field.column,
				method: contractsschema.IndexMethodIndex,
				column: field.column,
				name:   cmp.Or(field.annotation.IndexName, defaultIndexName(table, field.column)),
			})
		}
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i].name < indexes[j].name })
	for _, index := range indexes {
		add(index.key, formatIndex(index.method, []string{index.column}, index.name))
	}

	return lines
}

// defaultIndexName builds the index name used when the tag does not provide one.
func defaultIndexName(table, column string) string {
	return "idx_" + table + "_" + column
}

// tagValue returns the value of a `gorm` tag element, or "" when it is absent.
func tagValue(field reflect.StructField, key string) string {
	for _, item := range strings.Split(field.Tag.Get("gorm"), ";") {
		name, value, found := strings.Cut(strings.TrimSpace(item), ":")
		if found && strings.EqualFold(strings.TrimSpace(name), key) {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

// tagPresent reports whether a `gorm` tag carries an element, with or without a value.
func tagPresent(field reflect.StructField, key string) bool {
	for _, item := range strings.Split(field.Tag.Get("gorm"), ";") {
		name, value, found := strings.Cut(strings.TrimSpace(item), ":")
		if !found {
			name = item
			value = ""
		}
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return !strings.EqualFold(strings.TrimSpace(value), "false")
		}
	}

	return false
}

// tagInt returns a numeric `gorm` tag element, or 0 when it is absent or unparsable.
func tagInt(field reflect.StructField, key string) int {
	value, err := strconv.Atoi(tagValue(field, key))
	if err != nil {
		return 0
	}

	return value
}

func intMethod(size int, unsigned bool) string {
	switch {
	case size <= 8:
		if unsigned {
			return contractsschema.MethodUnsignedTinyInteger
		}
		return contractsschema.MethodTinyInteger
	case size <= 16:
		if unsigned {
			return contractsschema.MethodUnsignedSmallInteger
		}
		return contractsschema.MethodSmallInteger
	case size <= 32:
		if unsigned {
			return contractsschema.MethodUnsignedInteger
		}
		return contractsschema.MethodInteger
	default:
		if unsigned {
			return contractsschema.MethodUnsignedBigInteger
		}
		return contractsschema.MethodBigInteger
	}
}

// formatIndex renders an index or constraint definition.
func formatIndex(method string, columns []string, name string) string {
	b := &atom{Builder: strings.Builder{}}
	b.WriteString(tablePrefix)

	args := make([]any, len(columns))
	for index, column := range columns {
		args[index] = column
	}
	b.WriteMethod(method, args...)

	if name != "" {
		b.WriteMethod(contractsschema.IndexMethodName, name)
	}

	return b.String()
}

func parseEnum(def string) []any {
	start, end := strings.IndexByte(def, '('), strings.LastIndexByte(def, ')')
	if start == -1 || end <= start+1 {
		return nil
	}

	var values []any
	var buf strings.Builder
	inQuote := false

	for _, r := range def[start+1 : end] {
		if r == '\'' {
			inQuote = !inQuote
			continue
		}
		if r == ',' && !inQuote {
			values = append(values, parseVal(buf.String()))
			buf.Reset()
			continue
		}
		buf.WriteRune(r)
	}
	if buf.Len() > 0 {
		values = append(values, parseVal(buf.String()))
	}
	return values
}

func parseVal(s string) any {
	s = strings.TrimSpace(s)
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if strings.Contains(s, ".") {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}

func trimQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[0] == s[len(s)-1] {
		return s[1 : len(s)-1]
	}
	return s
}

func parseTypeSize(s string) int {
	start, end := strings.IndexByte(s, '('), strings.IndexByte(s, ')')
	if start > -1 && end > start {
		// Just grab the first number "varchar(255)" -> 255
		parts := strings.SplitN(s[start+1:end], ",", 2)
		if n, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
			return n
		}
	}
	return 0
}

// isSQLNullType checks whether a type is one designed for a nullable column:
//
//   - database/sql.Null* (e.g. sql.NullString, sql.NullInt64, sql.NullTime)
//   - the framework's soft-delete type (orm.DeletedAt)
//
// For any other type, nullability is expressed with a pointer.
func isSQLNullType(fieldType reflect.Type) bool {
	if fieldType.Kind() != reflect.Struct {
		return false
	}

	if fieldType.PkgPath() == "database/sql" && strings.HasPrefix(fieldType.Name(), "Null") {
		return true
	}

	// The framework's DeletedAt type.
	return fieldType == reflect.TypeFor[orm.DeletedAt]()
}

// atom is a string builder wrapper for generating Blueprint method chains.
type atom struct {
	strings.Builder
}

// WriteMethod appends a method call with arguments to the builder.
// Automatically adds a dot prefix if needed for method chaining.
func (r *atom) WriteMethod(name string, args ...any) {
	if r.Len() > 0 && r.String()[r.Len()-1] != '.' {
		r.WriteByte('.')
	}
	r.WriteString(name)
	r.WriteByte('(')
	for i, arg := range args {
		if i > 0 {
			r.WriteString(", ")
		}
		r.WriteValue(arg)
	}
	r.WriteByte(')')
}

// WriteValue formats and writes a single value to the builder.
// Handles strings, integers, floats, booleans, nil, and slices.
func (r *atom) WriteValue(v any) {
	switch val := v.(type) {
	case nil:
		r.WriteString("nil")
	case string:
		r.WriteString(strconv.Quote(val))
	case int:
		r.WriteString(strconv.Itoa(val))
	case int64:
		r.WriteString(strconv.FormatInt(val, 10))
	case float64:
		r.WriteString(strconv.FormatFloat(val, 'f', -1, 64))
	case bool:
		r.WriteString(strconv.FormatBool(val))
	case []any:
		r.WriteString("[]any{")
		for i, item := range val {
			if i > 0 {
				r.WriteString(", ")
			}
			r.WriteValue(item)
		}
		r.WriteByte('}')
	default:
		_, _ = fmt.Fprintf(r, "%v", val)
	}
}
