package processors

import (
	"regexp"
	"strings"
)

// SqliteProcessor inherits the base insert-ID processing, as in Laravel.
type SqliteProcessor struct{ BaseProcessor }

// ProcessColumns ports SQLiteProcessor::processColumns. The optional CREATE TABLE statement
// supplies collations and generated expressions that SQLite's column pragma does not expose.
func (p *SqliteProcessor) ProcessColumns(results []Row, sql ...string) []Row {
	primaryCount := 0
	for _, result := range results {
		primaryCount += columnInt(result["primary"])
	}
	statement := ""
	if len(sql) > 0 {
		statement = sql[0]
	}
	columns := make([]Row, 0, len(results))
	for _, result := range results {
		typ := strings.ToLower(columnString(result["type"]))
		typeName, _, _ := strings.Cut(typ, "(")
		extra := columnInt(result["extra"])
		generated := extra == 2 || extra == 3
		var collation, expression, generation any
		if statement != "" {
			name := regexp.QuoteMeta(columnString(result["name"]))
			collate := regexp.MustCompile(`(?i)\b` + name + `\b[^,(]+(?:\([^()]+\)[^,]*)?(?:(?:default|check|as)\s*(?:\(.*?\))?[^,]*)*collate\s+["'` + "`" + `]?(\w+)`)
			if match := collate.FindStringSubmatch(statement); match != nil {
				collation = strings.ToLower(match[1])
			}
			if generated {
				pattern := regexp.MustCompile(`(?i)\b` + name + `\b[^,]+\s+as\s+\(((?:[^()]+|\((?:[^()]+|\([^()]*\))*\))*)\)`)
				if match := pattern.FindStringSubmatch(statement); match != nil {
					expression = match[1]
				}
			}
		}
		if generated {
			kind := "virtual"
			if extra == 3 {
				kind = "stored"
			}
			generation = Row{"type": kind, "expression": expression}
		}
		columns = append(columns, Row{
			"name": result["name"], "type_name": typeName, "type": typ,
			"collation": collation, "nullable": columnBool(result["nullable"]), "default": result["default"],
			"auto_increment": primaryCount == 1 && columnBool(result["primary"]) && typ == "integer",
			"comment":        nil, "generation": generation,
		})
	}
	return columns
}
