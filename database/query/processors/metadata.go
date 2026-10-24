package processors

import "strings"

// New selects the processor without importing a concrete query or schema builder.
func New(driver string) Processor {
	name := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(driver)))
	switch name {
	case "mysql":
		return &MySqlProcessor{}
	case "mariadb":
		return &MariaDbProcessor{}
	case "postgres", "postgresql", "pgsql", "pgx", "pq":
		return &PostgresProcessor{}
	case "sqlite", "sqlite3":
		return &SqliteProcessor{}
	case "sqlserver", "mssql", "sqlsrv":
		return &SqlServerProcessor{}
	default:
		return &BaseProcessor{}
	}
}

func (p *BaseProcessor) ProcessSelect(results any) any          { return results }
func (p *BaseProcessor) ProcessTypes(results []Row) []Row       { return results }
func (p *BaseProcessor) ProcessIndexes(results []Row) []Row     { return results }
func (p *BaseProcessor) ProcessForeignKeys(results []Row) []Row { return results }
func (p *BaseProcessor) ProcessSchemas(results []Row) []Row {
	schemas := make([]Row, 0, len(results))
	for _, result := range results {
		schemas = append(schemas, Row{"name": result["name"], "path": result["path"], "default": columnBool(result["default"])})
	}
	return schemas
}
func qualifiedName(result Row) string {
	if result["schema"] != nil {
		return columnString(result["schema"]) + "." + columnString(result["name"])
	}
	return columnString(result["name"])
}
func (p *BaseProcessor) ProcessTables(results []Row) []Row {
	tables := make([]Row, 0, len(results))
	for _, result := range results {
		var size any
		if result["size"] != nil {
			size = columnInt(result["size"])
		}
		tables = append(tables, Row{"name": result["name"], "schema": result["schema"], "schema_qualified_name": qualifiedName(result),
			"size": size, "comment": result["comment"], "collation": result["collation"], "engine": result["engine"]})
	}
	return tables
}
func (p *BaseProcessor) ProcessViews(results []Row) []Row {
	views := make([]Row, 0, len(results))
	for _, result := range results {
		views = append(views, Row{"name": result["name"], "schema": result["schema"], "schema_qualified_name": qualifiedName(result), "definition": result["definition"]})
	}
	return views
}
func metadataColumns(value any) []string {
	if !columnBool(value) {
		return []string{}
	}
	if values, ok := value.([]string); ok {
		return append([]string(nil), values...)
	}
	return strings.Split(columnString(value), ",")
}
func processIndexes(results []Row, dialect string) []Row {
	indexes := make([]Row, 0, len(results))
	primaryCount := 0
	for _, result := range results {
		name := strings.ToLower(columnString(result["name"]))
		primary := columnBool(result["primary"])
		if dialect == "mysql" {
			primary = name == "primary"
		}
		if primary {
			primaryCount++
		}
		var kind any = strings.ToLower(columnString(result["type"]))
		if dialect == "sqlite" {
			kind = nil
		}
		indexes = append(indexes, Row{"name": name, "columns": metadataColumns(result["columns"]), "type": kind,
			"unique": columnBool(result["unique"]), "primary": primary})
	}
	if dialect == "sqlite" && primaryCount > 1 {
		kept := indexes[:0]
		for _, index := range indexes {
			if index["name"] != "primary" {
				kept = append(kept, index)
			}
		}
		indexes = kept
	}
	return indexes
}
func processForeignKeys(results []Row, dialect string) []Row {
	keys := make([]Row, 0, len(results))
	for _, result := range results {
		name := result["name"]
		if dialect == "sqlite" {
			name = nil
		}
		action := func(value any) any {
			action := strings.ToLower(columnString(value))
			if dialect == "postgres" {
				return map[string]any{"a": "no action", "r": "restrict", "c": "cascade", "n": "set null", "d": "set default"}[action]
			}
			if dialect == "sqlserver" {
				action = strings.ReplaceAll(action, "_", " ")
			}
			return action
		}
		keys = append(keys, Row{"name": name, "columns": metadataColumns(result["columns"]), "foreign_schema": result["foreign_schema"],
			"foreign_table": result["foreign_table"], "foreign_columns": metadataColumns(result["foreign_columns"]),
			"on_update": action(result["on_update"]), "on_delete": action(result["on_delete"])})
	}
	return keys
}
func (p *MySqlProcessor) ProcessIndexes(rows []Row) []Row    { return processIndexes(rows, "mysql") }
func (p *PostgresProcessor) ProcessIndexes(rows []Row) []Row { return processIndexes(rows, "postgres") }
func (p *SqliteProcessor) ProcessIndexes(rows []Row) []Row   { return processIndexes(rows, "sqlite") }
func (p *SqlServerProcessor) ProcessIndexes(rows []Row) []Row {
	return processIndexes(rows, "sqlserver")
}
func (p *MySqlProcessor) ProcessForeignKeys(rows []Row) []Row {
	return processForeignKeys(rows, "mysql")
}
func (p *PostgresProcessor) ProcessForeignKeys(rows []Row) []Row {
	return processForeignKeys(rows, "postgres")
}
func (p *SqliteProcessor) ProcessForeignKeys(rows []Row) []Row {
	return processForeignKeys(rows, "sqlite")
}
func (p *SqlServerProcessor) ProcessForeignKeys(rows []Row) []Row {
	return processForeignKeys(rows, "sqlserver")
}

func (p *PostgresProcessor) ProcessTypes(results []Row) []Row {
	types := map[string]any{"b": "base", "c": "composite", "d": "domain", "e": "enum", "p": "pseudo", "r": "range", "m": "multirange"}
	categories := map[string]any{"a": "array", "b": "boolean", "c": "composite", "d": "date_time", "e": "enum", "g": "geometric",
		"i": "network_address", "n": "numeric", "p": "pseudo", "r": "range", "s": "string", "t": "timespan", "u": "user_defined",
		"v": "bit_string", "x": "unknown", "z": "internal_use"}
	result := make([]Row, 0, len(results))
	for _, row := range results {
		result = append(result, Row{"name": row["name"], "schema": row["schema"], "schema_qualified_name": qualifiedName(row),
			"implicit": columnBool(row["implicit"]), "type": types[strings.ToLower(columnString(row["type"]))],
			"category": categories[strings.ToLower(columnString(row["category"]))]})
	}
	return result
}
