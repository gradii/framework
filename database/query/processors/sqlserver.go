package processors

import "fmt"

// SqlServerProcessor owns SQL Server's insert-ID retrieval, as in Laravel. go-mssqldb cannot use
// PDO's lastInsertId API, so the grammar emits OUTPUT and this processor reads its row on the writer.
type SqlServerProcessor struct{ BaseProcessor }

func (p *SqlServerProcessor) ProcessInsertGetID(connection Connection, statement string, values []any, sequence string) (any, error) {
	if sequence == "" {
		sequence = "id"
	}

	return connection.SelectFromWriteConnection(statement, values, sequence)
}

// ProcessColumns ports SqlServerProcessor::processColumns.
func (p *SqlServerProcessor) ProcessColumns(results []Row, sql ...string) []Row {
	columns := make([]Row, 0, len(results))
	for _, result := range results {
		typ := columnString(result["type_name"])
		switch typ {
		case "binary", "varbinary", "char", "varchar", "nchar", "nvarchar":
			if columnInt(result["length"]) == -1 {
				typ += "(max)"
			} else {
				typ = fmt.Sprintf("%s(%d)", typ, columnInt(result["length"]))
			}
		case "decimal", "numeric":
			typ = fmt.Sprintf("%s(%d,%d)", typ, columnInt(result["precision"]), columnInt(result["places"]))
		case "float", "datetime2", "datetimeoffset", "time":
			typ = fmt.Sprintf("%s(%d)", typ, columnInt(result["places"]))
		}
		var generation any
		if columnBool(result["expression"]) {
			kind := "virtual"
			if columnBool(result["persisted"]) {
				kind = "stored"
			}
			generation = Row{"type": kind, "expression": result["expression"]}
		}
		columns = append(columns, Row{
			"name": result["name"], "type_name": result["type_name"], "type": typ,
			"collation": result["collation"], "nullable": columnBool(result["nullable"]), "default": result["default"],
			"auto_increment": columnBool(result["autoincrement"]),
			"comment":        nullableComment(result["comment"]), "generation": generation,
		})
	}
	return columns
}
