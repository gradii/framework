package schema

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/database/query/processors"
	"github.com/gradii/framework/errors"
	"github.com/spf13/cast"
)

type schemaMetadataCache struct {
	mu      sync.Mutex
	version string
	stats   *bool
}

func (r *Schema) selectMetadata(statement string, bindings ...any) ([]processors.Row, error) {
	var rows []processors.Row
	var err error
	if connection := r.executionConnection(); connection != nil {
		err = connection.SelectFromWriteConnection(&rows, statement, bindings...)
	} else if r.orm != nil {
		err = r.orm.Query().UseWritePdo().Raw(statement, bindings...).Scan(&rows)
	} else {
		return nil, errors.DatabaseConnectionIsNil
	}
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		for name, value := range row {
			if bytes, ok := value.([]byte); ok {
				row[name] = string(bytes)
			}
		}
	}
	return rows, nil
}
func (r *Schema) metadataServerVersion() (string, error) {
	if r.db != nil {
		return r.db.ServerVersion()
	}
	r.metadataCache.mu.Lock()
	defer r.metadataCache.mu.Unlock()
	if r.metadataCache.version != "" {
		return r.metadataCache.version, nil
	}
	statements := map[string]string{"mysql": "select version() as version", "postgres": "select current_setting('server_version') as version",
		"sqlite": "select sqlite_version() as version", "sqlserver": "select cast(serverproperty('ProductVersion') as varchar(32)) as version"}
	statement, ok := statements[r.metadataDialect()]
	if !ok {
		return "", errors.SchemaDriverNotSupported.Args(r.driverName)
	}
	rows, err := r.selectMetadata(statement)
	if err != nil {
		return "", err
	}
	if len(rows) > 0 {
		r.metadataCache.version = cast.ToString(rows[0]["version"])
	}
	return r.metadataCache.version, nil
}
func (r *Schema) sqliteHasStats() (bool, error) {
	r.metadataCache.mu.Lock()
	defer r.metadataCache.mu.Unlock()
	if r.metadataCache.stats != nil {
		return *r.metadataCache.stats, nil
	}
	rows, err := r.selectMetadata("select exists (select 1 from pragma_compile_options where compile_options = 'ENABLE_DBSTAT_VTAB') as enabled")
	if err != nil {
		return false, err
	}
	value := len(rows) > 0 && metadataBool(rows[0]["enabled"])
	r.metadataCache.stats = &value
	return value, nil
}
func metadataBool(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	}
	valueString := cast.ToString(value)
	return valueString != "" && valueString != "0"
}
func metadataString(value any) *string {
	if value == nil {
		return nil
	}
	text := cast.ToString(value)
	return &text
}
func (r *Schema) metadataTable(reference string) (string, string, error) {
	parts := strings.Split(reference, ".")
	if len(parts) > 2 {
		return "", "", errors.SchemaErrorReferenceFormat
	}
	if len(parts) == 2 {
		return parts[0], r.prefix + parts[1], nil
	}
	return r.schema, r.prefix + reference, nil
}
func (r *Schema) metadataRows(kind, reference string, schemas []string) ([]processors.Row, error) {
	var schema, table string
	if reference != "" {
		var err error
		schema, table, err = r.metadataTable(reference)
		if err != nil {
			return nil, err
		}
	}
	statement, bindings, err := r.metadataSQL(kind, schema, table, schemas)
	if err != nil {
		return nil, err
	}
	return r.selectMetadata(statement, bindings...)
}

func (r *Schema) GetSchemas() ([]driver.Schema, error) {
	rows, err := r.metadataRows("schemas", "", nil)
	if err != nil {
		return nil, err
	}
	result := make([]driver.Schema, 0, len(rows))
	for _, row := range r.queryProcessor.ProcessSchemas(rows) {
		result = append(result, driver.Schema{Name: cast.ToString(row["name"]), Path: metadataString(row["path"]), Default: metadataBool(row["default"])})
	}
	return result, nil
}
func (r *Schema) GetCurrentSchemaListing() ([]string, error) {
	switch r.metadataDialect() {
	case "mysql":
		return []string{r.databaseName}, nil
	case "sqlite":
		return []string{"main"}, nil
	case "postgres":
		var configured any
		if r.config != nil {
			configured = r.config.Get("database.connections." + r.GetConnection() + ".search_path")
		}
		var names []string
		switch value := configured.(type) {
		case []string:
			names = append(names, value...)
		case string:
			if value != "" {
				names = strings.Split(value, ",")
			}
		}
		if len(names) == 0 {
			path := r.schema
			if path == "" {
				path = "public"
			}
			names = strings.Split(path, ",")
		}
		for i, name := range names {
			names[i] = strings.Trim(strings.TrimSpace(name), "\"")
			if names[i] == "$user" {
				names[i] = r.username
			}
		}
		return names, nil
	default:
		return nil, nil
	}
}
func (r *Schema) GetCurrentSchemaName() (string, error) {
	names, err := r.GetCurrentSchemaListing()
	if err != nil {
		return "", err
	}
	if len(names) > 0 {
		return names[0], nil
	}
	return "", nil
}

func (r *Schema) GetColumnsMetadata(table string) ([]driver.ColumnMetadata, error) {
	raw, err := r.metadataRows("columns", table, nil)
	if err != nil {
		return nil, err
	}
	var extraSQL []string
	if r.metadataDialect() == "sqlite" {
		schema, name, err := r.metadataTable(table)
		if err != nil {
			return nil, err
		}
		rows, err := r.selectMetadata("select sql from "+quoteSchemaIdentifier(sqliteSchemaName(schema))+".sqlite_master where type = 'table' and name = ?", name)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			extraSQL = []string{cast.ToString(rows[0]["sql"])}
		}
	}
	rows := r.queryProcessor.ProcessColumns(raw, extraSQL...)
	result := make([]driver.ColumnMetadata, 0, len(rows))
	for i, row := range rows {
		var generation *driver.ColumnGeneration
		if value, ok := row["generation"].(map[string]any); ok {
			generation = &driver.ColumnGeneration{Type: cast.ToString(value["type"]), Expression: metadataString(value["expression"])}
		}
		column := driver.ColumnMetadata{Name: cast.ToString(row["name"]), Type: cast.ToString(row["type"]), TypeName: cast.ToString(row["type_name"]),
			Collation: metadataString(row["collation"]), Nullable: metadataBool(row["nullable"]), Default: row["default"],
			AutoIncrement: metadataBool(row["auto_increment"]), Comment: metadataString(row["comment"]), Generation: generation}
		if i < len(raw) {
			column.Extra = cast.ToString(raw[i]["extra"])
		}
		result = append(result, column)
	}
	return result, nil
}
func (r *Schema) GetColumns(table string) ([]driver.Column, error) {
	if r.queryProcessor == nil {
		return r.legacyGetColumns(table)
	}
	columns, err := r.GetColumnsMetadata(table)
	if err != nil {
		return nil, err
	}
	result := make([]driver.Column, 0, len(columns))
	for _, column := range columns {
		legacy := driver.Column{Name: column.Name, Type: column.Type, TypeName: column.TypeName, Default: cast.ToString(column.Default),
			Nullable: column.Nullable, Autoincrement: column.AutoIncrement, Extra: column.Extra, Generation: column.Generation}
		if column.Comment != nil {
			legacy.Comment = *column.Comment
		}
		if column.Collation != nil {
			legacy.Collation = *column.Collation
		}
		result = append(result, legacy)
	}
	return result, nil
}
func (r *Schema) GetColumnType(table, column string, fullDefinition ...bool) (string, error) {
	columns, err := r.GetColumnsMetadata(table)
	if err != nil {
		return "", err
	}
	for _, value := range columns {
		if !strings.EqualFold(value.Name, column) {
			continue
		}
		if len(fullDefinition) > 0 && fullDefinition[0] {
			return value.Type, nil
		}
		return value.TypeName, nil
	}
	return "", errors.SchemaColumnNotFound.Args(column, table)
}
func (r *Schema) GetIndexes(table string) ([]driver.Index, error) {
	if r.queryProcessor == nil {
		return r.legacyGetIndexes(table)
	}
	rows, err := r.metadataRows("indexes", table, nil)
	if err != nil {
		return nil, err
	}
	result := make([]driver.Index, 0, len(rows))
	for _, row := range r.queryProcessor.ProcessIndexes(rows) {
		result = append(result, driver.Index{Name: cast.ToString(row["name"]), Columns: cast.ToStringSlice(row["columns"]),
			Type: cast.ToString(row["type"]), Unique: metadataBool(row["unique"]), Primary: metadataBool(row["primary"])})
	}
	return result, nil
}
func (r *Schema) GetForeignKeys(table string) ([]driver.ForeignKey, error) {
	if r.queryProcessor == nil {
		return r.legacyGetForeignKeys(table)
	}
	rows, err := r.metadataRows("foreignKeys", table, nil)
	if err != nil {
		return nil, err
	}
	result := make([]driver.ForeignKey, 0, len(rows))
	for _, row := range r.queryProcessor.ProcessForeignKeys(rows) {
		result = append(result, driver.ForeignKey{Name: cast.ToString(row["name"]), Columns: cast.ToStringSlice(row["columns"]),
			ForeignSchema: cast.ToString(row["foreign_schema"]), ForeignTable: cast.ToString(row["foreign_table"]),
			ForeignColumns: cast.ToStringSlice(row["foreign_columns"]), OnUpdate: cast.ToString(row["on_update"]), OnDelete: cast.ToString(row["on_delete"])})
	}
	return result, nil
}
func (r *Schema) GetTables(schemas ...string) ([]driver.Table, error) {
	if r.queryProcessor == nil {
		return r.legacyGetTables()
	}
	var rows []processors.Row
	var err error
	if r.metadataDialect() == "sqlite" {
		version, versionErr := r.metadataServerVersion()
		if versionErr != nil {
			return nil, versionErr
		}
		var major, minor int
		_, _ = fmt.Sscanf(version, "%d.%d", &major, &minor)
		if major < 3 || (major == 3 && minor < 37) {
			rows, err = r.sqliteLegacyTables(schemas)
		} else {
			rows, err = r.metadataRows("tables", "", schemas)
		}
	} else {
		rows, err = r.metadataRows("tables", "", schemas)
	}
	if err != nil {
		return nil, err
	}
	result := make([]driver.Table, 0, len(rows))
	for _, row := range r.queryProcessor.ProcessTables(rows) {
		result = append(result, driver.Table{Name: cast.ToString(row["name"]), Schema: cast.ToString(row["schema"]),
			SchemaQualifiedName: cast.ToString(row["schema_qualified_name"]), Size: cast.ToInt(row["size"]),
			Comment: cast.ToString(row["comment"]), Collation: cast.ToString(row["collation"]), Engine: cast.ToString(row["engine"])})
	}
	return result, nil
}
func (r *Schema) attachedSchemas(names []string) ([]string, error) {
	if len(names) > 0 {
		return names, nil
	}
	schemas, err := r.GetSchemas()
	if err != nil {
		return nil, err
	}
	for _, schema := range schemas {
		names = append(names, schema.Name)
	}
	return names, nil
}
func (r *Schema) sqliteLegacyTables(schemas []string) ([]processors.Row, error) {
	names, err := r.attachedSchemas(schemas)
	if err != nil {
		return nil, err
	}
	stats, err := r.sqliteHasStats()
	if err != nil {
		return nil, err
	}
	var rows []processors.Row
	for _, name := range names {
		statement := "select name, ? as schema from " + quoteSchemaIdentifier(name) + ".sqlite_master where type = 'table' and name not like 'sqlite\\_%' escape '\\' order by name"
		args := []any{name}
		if stats {
			statement = "select m.tbl_name as name, ? as schema, sum(s.pgsize) as size from " + quoteSchemaIdentifier(name) + ".sqlite_master as m join dbstat(?) as s on s.name = m.name where m.type in ('table', 'index') and m.tbl_name not like 'sqlite\\_%' escape '\\' group by m.tbl_name order by m.tbl_name"
			args = append(args, name)
		}
		set, err := r.selectMetadata(statement, args...)
		if err != nil {
			return nil, err
		}
		rows = append(rows, set...)
	}
	return rows, nil
}
func (r *Schema) GetViews(schemas ...string) ([]driver.View, error) {
	if r.queryProcessor == nil {
		return r.legacyGetViews()
	}
	var rows []processors.Row
	if r.metadataDialect() == "sqlite" {
		names, err := r.attachedSchemas(schemas)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			set, err := r.selectMetadata("select name, ? as schema, sql as definition from "+quoteSchemaIdentifier(name)+".sqlite_master where type = 'view' order by name", name)
			if err != nil {
				return nil, err
			}
			rows = append(rows, set...)
		}
	} else {
		var err error
		rows, err = r.metadataRows("views", "", schemas)
		if err != nil {
			return nil, err
		}
	}
	result := make([]driver.View, 0, len(rows))
	for _, row := range r.queryProcessor.ProcessViews(rows) {
		result = append(result, driver.View{Name: cast.ToString(row["name"]), Schema: cast.ToString(row["schema"]),
			SchemaQualifiedName: cast.ToString(row["schema_qualified_name"]), Definition: cast.ToString(row["definition"])})
	}
	return result, nil
}
func (r *Schema) GetTypes(schemas ...string) ([]driver.Type, error) {
	if r.queryProcessor == nil {
		return r.legacyGetTypes()
	}
	rows, err := r.metadataRows("types", "", schemas)
	if err != nil {
		return nil, err
	}
	result := make([]driver.Type, 0, len(rows))
	for _, row := range r.queryProcessor.ProcessTypes(rows) {
		result = append(result, driver.Type{Name: cast.ToString(row["name"]), Schema: cast.ToString(row["schema"]), SchemaQualifiedName: cast.ToString(row["schema_qualified_name"]),
			Type: cast.ToString(row["type"]), Category: cast.ToString(row["category"]), Implicit: metadataBool(row["implicit"])})
	}
	return result, nil
}

// GetTableListing accepts Laravel's optional schema selector and schema-qualified flag.
func (r *Schema) GetTableListing(options ...any) []string {
	var schemas []string
	qualified := true
	if len(options) > 0 && options[0] != nil {
		switch value := options[0].(type) {
		case string:
			schemas = []string{value}
		case []string:
			schemas = value
		default:
			r.log.Errorf("%v", errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", value), "schema string or []string"))
			return nil
		}
	}
	if len(options) > 1 {
		qualified, _ = options[1].(bool)
	}
	tables, err := r.GetTables(schemas...)
	if err != nil {
		r.log.Errorf("failed to get tables: %v", err)
		return nil
	}
	names := make([]string, 0, len(tables))
	for _, table := range tables {
		if qualified && table.SchemaQualifiedName != "" {
			names = append(names, table.SchemaQualifiedName)
		} else {
			names = append(names, table.Name)
		}
	}
	return names
}
func (r *Schema) HasIndex(table string, index any, indexType ...string) bool {
	indexes, err := r.GetIndexes(table)
	if err != nil {
		return false
	}
	for _, value := range indexes {
		matched := false
		switch index := index.(type) {
		case string:
			matched = value.Name == index
		case []string:
			matched = slices.Equal(value.Columns, index)
		}
		if !matched {
			continue
		}
		if len(indexType) == 0 || indexType[0] == "" {
			return true
		}
		switch indexType[0] {
		case "primary":
			return value.Primary
		case "unique":
			return value.Unique
		default:
			return value.Type == indexType[0]
		}
	}
	return false
}

func (r *Schema) HasTable(reference string) bool {
	if r.queryProcessor == nil {
		return r.legacyHasTable(reference)
	}
	schema, table, err := r.metadataTable(reference)
	if err != nil {
		return false
	}
	var statement string
	var values []any
	switch r.metadataDialect() {
	case "mysql":
		statement = "select exists (select 1 from information_schema.tables where table_schema = coalesce(nullif(?, ''), schema()) and table_name = ? and table_type in ('BASE TABLE', 'SYSTEM VERSIONED')) as `exists`"
		values = []any{schema, table}
	case "postgres":
		statement = "select exists (select 1 from pg_class c, pg_namespace n where n.nspname = coalesce(nullif(?, ''), current_schema()) and c.relname = ? and c.relkind in ('r', 'p') and n.oid = c.relnamespace) as \"exists\""
		values = []any{schema, table}
	case "sqlite":
		statement = "select exists (select 1 from " + quoteSchemaIdentifier(sqliteSchemaName(schema)) + ".sqlite_master where name = ? and type = 'table') as \"exists\""
		values = []any{table}
	case "sqlserver":
		if schema != "" {
			table = schema + "." + table
		}
		statement = "select (case when object_id(?, 'U') is null then 0 else 1 end) as [exists]"
		values = []any{table}
	}
	rows, err := r.selectMetadata(statement, values...)
	return err == nil && len(rows) > 0 && metadataBool(rows[0]["exists"])
}
func (r *Schema) HasView(table string) bool { return r.hasRelation(table, true) }
func (r *Schema) hasRelation(reference string, view bool) bool {
	schema, table, err := r.metadataTable(reference)
	if err != nil {
		return false
	}
	if schema == "" {
		schema, err = r.GetCurrentSchemaName()
		if err != nil {
			return false
		}
	}
	if view {
		views, err := r.GetViews(schema)
		if err != nil {
			return false
		}
		for _, value := range views {
			if strings.EqualFold(value.Name, table) {
				return true
			}
		}
	} else {
		tables, err := r.GetTables(schema)
		if err != nil {
			return false
		}
		for _, value := range tables {
			if strings.EqualFold(value.Name, table) {
				return true
			}
		}
	}
	return false
}
