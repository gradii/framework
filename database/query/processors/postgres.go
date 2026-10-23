package processors

import "strings"

// PostgresProcessor reads the row produced by RETURNING on the write connection.
// This ports PostgresProcessor::processInsertGetId without inferring the result shape from SQL text.
type PostgresProcessor struct{ BaseProcessor }

func (p *PostgresProcessor) ProcessInsertGetID(connection Connection, statement string, values []any, sequence string) (any, error) {
	if sequence == "" {
		sequence = "id"
	}

	return connection.SelectFromWriteConnection(statement, values, sequence)
}

// ProcessColumns ports PostgresProcessor::processColumns.
func (p *PostgresProcessor) ProcessColumns(results []Row, sql ...string) []Row {
	columns := make([]Row, 0, len(results))
	for _, result := range results {
		defaultValue := result["default"]
		var generation any
		if columnBool(result["generated"]) {
			var kind any
			switch result["generated"] {
			case "s":
				kind = "stored"
			case "v":
				kind = "virtual"
			}
			generation = Row{"type": kind, "expression": defaultValue}
			defaultValue = nil
		}
		columns = append(columns, Row{
			"name": result["name"], "type_name": result["type_name"], "type": result["type"],
			"collation": result["collation"], "nullable": columnBool(result["nullable"]),
			"default": defaultValue, "auto_increment": strings.HasPrefix(columnString(result["default"]), "nextval("),
			"comment": result["comment"], "generation": generation,
		})
	}
	return columns
}
