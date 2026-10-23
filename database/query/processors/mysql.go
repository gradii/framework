package processors

// MySqlProcessor uses the ID recorded by the insert operation, matching MySqlProcessor in Laravel.
// Go's sql.Result supplies the connection-local ID that Laravel's MySqlConnection records.
type MySqlProcessor struct{ BaseProcessor }

// ProcessColumns ports MySqlProcessor::processColumns, including generated-column metadata.
func (p *MySqlProcessor) ProcessColumns(results []Row, sql ...string) []Row {
	columns := make([]Row, 0, len(results))
	for _, result := range results {
		var generation any
		if columnBool(result["expression"]) {
			var kind any
			switch result["extra"] {
			case "STORED GENERATED":
				kind = "stored"
			case "VIRTUAL GENERATED":
				kind = "virtual"
			}
			generation = Row{"type": kind, "expression": result["expression"]}
		}
		columns = append(columns, Row{
			"name": result["name"], "type_name": result["type_name"], "type": result["type"],
			"collation": result["collation"], "nullable": result["nullable"] == "YES",
			"default": result["default"], "auto_increment": result["extra"] == "auto_increment",
			"comment": nullableComment(result["comment"]), "generation": generation,
		})
	}
	return columns
}
