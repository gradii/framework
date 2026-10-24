package query

type selectColumn struct {
	alias string
	value any
}

// PHP's array form supplies either a column list or aliases for query-valued columns.
func selectColumns(columns []any) []selectColumn {
	if len(columns) == 1 {
		if entries, ok := mapEntries(columns[0]); ok {
			result := make([]selectColumn, len(entries))
			for i, entry := range entries {
				result[i] = selectColumn{alias: entry.Column, value: entry.Value}
			}
			return result
		}
		if values, ok := arrayValues(columns[0]); ok {
			columns = values
		}
	}
	result := make([]selectColumn, len(columns))
	for i, column := range columns {
		result[i].value = column
	}
	return result
}
