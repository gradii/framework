package processors

import "strings"

// New selects the processor without importing a concrete query or schema builder.
func New(_ string) Processor { return &BaseProcessor{} }

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

// MetadataColumns normalizes catalog column lists for external processors.
func MetadataColumns(value any) []string { return metadataColumns(value) }
