package schema

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/database/query/processors"
	"github.com/gradii/framework/errors"
	"github.com/spf13/cast"
)

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
func (r *Schema) metadataResult(kind, reference string, schemas []string) (driver.SchemaMetadataResult, error) {
	var namespace, table string
	if reference != "" {
		var err error
		namespace, table, err = r.metadataTable(reference)
		if err != nil {
			return driver.SchemaMetadataResult{}, err
		}
	}
	query := driver.SchemaMetadataQuery{Kind: kind, Schema: namespace, Table: table, Schemas: schemas}
	if resolver, ok := r.grammar.(driver.SchemaMetadataResolver); ok {
		return resolver.ResolveSchemaMetadata(query, schemaCatalog{r})
	}
	grammar, ok := r.grammar.(driver.SchemaMetadataGrammar)
	if !ok {
		return driver.SchemaMetadataResult{}, errors.SchemaMetadataNotSupported.Args(kind, r.driverName)
	}
	statement, err := grammar.CompileSchemaMetadata(query)
	if err != nil {
		return driver.SchemaMetadataResult{}, err
	}
	if statement.SQL == "" {
		return driver.SchemaMetadataResult{}, nil
	}
	rows, err := r.selectMetadata(statement.SQL, statement.Bindings...)
	return driver.SchemaMetadataResult{Rows: rows}, err
}
func (r *Schema) metadataRows(kind, reference string, schemas []string) ([]processors.Row, error) {
	result, err := r.metadataResult(kind, reference, schemas)
	return result.Rows, err
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
	if grammar, ok := r.grammar.(driver.SchemaCurrentSchemas); ok {
		info := driver.SchemaConnectionInfo{Database: r.databaseName, Username: r.username, Schema: r.schema, Prefix: r.prefix}
		if r.config != nil {
			info.SearchPath = r.config.Get("database.connections." + r.GetConnection() + ".search_path")
		}
		return grammar.ResolveCurrentSchemas(info, schemaCatalog{r})
	}
	if grammar, ok := r.grammar.(driver.SchemaMetadataGrammar); ok {
		return grammar.CurrentSchemas(r.databaseName), nil
	}
	return nil, nil
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
	metadata, err := r.metadataResult("columns", table, nil)
	if err != nil {
		return nil, err
	}
	raw := metadata.Rows
	rows := r.queryProcessor.ProcessColumns(raw, metadata.ColumnSQL...)
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
		legacy := driver.Column{Name: column.Name, Type: column.Type, TypeName: column.TypeName, Default: cast.ToString(column.Default), DefaultValue: column.Default, DefaultKnown: true,
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
	rows, err := r.metadataRows("tables", "", schemas)
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

func (r *Schema) GetViews(schemas ...string) ([]driver.View, error) {
	if r.queryProcessor == nil {
		return r.legacyGetViews()
	}
	rows, err := r.metadataRows("views", "", schemas)
	if err != nil {
		return nil, err
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
	rows, err := r.metadataRows("hasTable", reference, nil)
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
