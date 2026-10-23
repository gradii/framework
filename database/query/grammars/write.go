package grammars

import "strings"

// CompileInsert mirrors Grammar::compileInsert().
func (g *BaseGrammar) CompileInsert(b Query, values []map[string]any) string {
	return g.compileInsert(b, values, "")
}

// compileInsert places a dialect's OUTPUT clause structurally, without searching the rendered SQL.
func (g *BaseGrammar) compileInsert(b Query, values []map[string]any, output string) string {
	table := g.dialect().WrapTable(b.State().From)

	if len(values) == 0 {
		return "insert into " + table + output + " default values"
	}

	// Laravel keys the column list off the first record and ksort()s every record so the column
	// order and the placeholder order agree. Go maps are unordered, so the order is fixed here and
	// reused for every record.
	columns := SortedKeys(values[0])
	if len(b.State().InsertColumns) > 0 {
		columns = b.State().InsertColumns
	}
	columnList := g.dialect().Columnize(anySlice(columns))

	rows := make([]string, 0, len(values))
	for _, record := range values {
		placeholders := make([]any, 0, len(columns))
		for _, column := range columns {
			placeholders = append(placeholders, record[column])
		}
		rows = append(rows, "("+g.dialect().Parameterize(placeholders)+")")
	}

	return "insert into " + table + " (" + columnList + ")" + output + " values " + strings.Join(rows, ", ")
}

// CompileInsertUsing mirrors Grammar::compileInsertUsing().
func (g *BaseGrammar) CompileInsertUsing(b Query, columns []any, sql string) string {
	table := g.dialect().WrapTable(b.State().From)

	if len(columns) == 0 || (len(columns) == 1 && columns[0] == "*") {
		return "insert into " + table + " " + sql
	}

	return "insert into " + table + " (" + g.dialect().Columnize(columns) + ") " + sql
}

// CompileInsertGetId mirrors Grammar::compileInsertGetId(). Dialects that need a returning clause
// override this.
func (g *BaseGrammar) CompileInsertGetId(b Query, values []map[string]any, sequence string) string {
	return g.dialect().CompileInsert(b, values)
}

// CompileInsertOrIgnore mirrors Grammar::compileInsertOrIgnore(). The base refuses.
func (g *BaseGrammar) CompileInsertOrIgnore(b Query, values []map[string]any) string {
	b.SetError(errInsertIgnoreNotSupported)
	return ""
}

// CompileUpdate mirrors Grammar::compileUpdate().
func (g *BaseGrammar) CompileUpdate(b Query, values UpdateValues) string {
	table := g.dialect().WrapTable(b.State().From)
	columns := g.dialect().CompileUpdateColumns(b, values)
	where := g.dialect().CompileWheres(b)

	if len(b.State().Joins) > 0 {
		return strings.TrimSpace(g.dialect().CompileUpdateWithJoins(b, table, columns, where))
	}

	return strings.TrimSpace(g.dialect().CompileUpdateWithoutJoins(b, table, columns, where))
}

// CompileUpdateWithJoins mirrors Grammar::compileUpdateWithJoins().
func (g *BaseGrammar) CompileUpdateWithJoins(b Query, table string, columns string, where string) string {
	joins := g.dialect().CompileJoins(b, b.State().Joins)

	return "update " + table + " " + joins + " set " + columns + " " + where
}

// CompileUpdateWithoutJoins mirrors Grammar::compileUpdateWithoutJoins().
func (g *BaseGrammar) CompileUpdateWithoutJoins(b Query, table string, columns string, where string) string {
	return "update " + table + " set " + columns + " " + where
}

// CompileUpdateColumns mirrors Grammar::compileUpdateColumns().
func (g *BaseGrammar) CompileUpdateColumns(b Query, values UpdateValues) string {
	parts := make([]string, 0, len(values))

	for _, value := range values {
		parts = append(parts, g.dialect().Wrap(value.Column)+" = "+g.dialect().Parameter(value.Value))
	}

	return strings.Join(parts, ", ")
}

// CompileUpsert mirrors Grammar::compileUpsert(). The base refuses.
func (g *BaseGrammar) CompileUpsert(b Query, values []map[string]any, uniqueBy []string, update []UpsertUpdate) string {
	b.SetError(errUpsertNotSupported)
	return ""
}

// CompileDelete mirrors Grammar::compileDelete().
func (g *BaseGrammar) CompileDelete(b Query) string {
	table := g.dialect().WrapTable(b.State().From)
	where := g.dialect().CompileWheres(b)

	if len(b.State().Joins) > 0 {
		return strings.TrimSpace(g.dialect().CompileDeleteWithJoins(b, table, where))
	}

	return strings.TrimSpace(g.dialect().CompileDeleteWithoutJoins(b, table, where))
}

// CompileDeleteWithJoins mirrors Grammar::compileDeleteWithJoins().
func (g *BaseGrammar) CompileDeleteWithJoins(b Query, table string, where string) string {
	parts := strings.Split(table, " as ")
	alias := parts[len(parts)-1]
	joins := g.dialect().CompileJoins(b, b.State().Joins)

	return "delete " + alias + " from " + table + " " + joins + " " + where
}

// CompileDeleteWithoutJoins mirrors Grammar::compileDeleteWithoutJoins().
func (g *BaseGrammar) CompileDeleteWithoutJoins(b Query, table string, where string) string {
	return "delete from " + table + " " + where
}

// CompileTruncate mirrors Grammar::compileTruncate().
func (g *BaseGrammar) CompileTruncate(b Query) []Statement {
	return []Statement{{SQL: "truncate table " + g.dialect().WrapTable(b.State().From)}}
}
