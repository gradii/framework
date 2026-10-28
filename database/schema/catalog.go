package schema

import "github.com/gradii/framework/contracts/database/driver"

// schemaCatalog keeps all catalog reads on the active writer or schema transaction.
type schemaCatalog struct{ schema *Schema }

func (c schemaCatalog) Select(statement driver.SchemaStatement) ([]map[string]any, error) {
	return c.schema.selectMetadata(statement.SQL, statement.Bindings...)
}
func (c schemaCatalog) Columns(table string) ([]driver.ColumnMetadata, error) {
	return c.schema.GetColumnsMetadata(table)
}
func (c schemaCatalog) ResolveTable(table string) (string, string, error) {
	return c.schema.metadataTable(table)
}
