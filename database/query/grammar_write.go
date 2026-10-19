package query

import "strings"

// CompileInsert mirrors Grammar::compileInsert().
func (g *BaseGrammar) CompileInsert(b *Builder, values []map[string]any) string {
	table := g.dialect().WrapTable(b.from)

	if len(values) == 0 {
		return "insert into " + table + " default values"
	}

	// Laravel keys the column list off the first record and ksort()s every record so the column
	// order and the placeholder order agree. Go maps are unordered, so the order is fixed here and
	// reused for every record.
	columns := sortedKeys(values[0])
	if len(b.insertColumns) > 0 {
		columns = b.insertColumns
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

	return "insert into " + table + " (" + columnList + ") values " + strings.Join(rows, ", ")
}

// CompileInsertUsing mirrors Grammar::compileInsertUsing().
func (g *BaseGrammar) CompileInsertUsing(b *Builder, columns []any, sql string) string {
	table := g.dialect().WrapTable(b.from)

	if len(columns) == 0 || (len(columns) == 1 && columns[0] == "*") {
		return "insert into " + table + " " + sql
	}

	return "insert into " + table + " (" + g.dialect().Columnize(columns) + ") " + sql
}

// CompileInsertGetId mirrors Grammar::compileInsertGetId(). Dialects that need a returning clause
// override this.
func (g *BaseGrammar) CompileInsertGetId(b *Builder, values []map[string]any, sequence string) string {
	return g.dialect().CompileInsert(b, values)
}

// CompileInsertOrIgnore mirrors Grammar::compileInsertOrIgnore(). The base refuses.
func (g *BaseGrammar) CompileInsertOrIgnore(b *Builder, values []map[string]any) string {
	b.setError(errInsertIgnoreNotSupported)
	return ""
}

// CompileUpdate mirrors Grammar::compileUpdate().
func (g *BaseGrammar) CompileUpdate(b *Builder, values UpdateValues) string {
	table := g.dialect().WrapTable(b.from)
	columns := g.dialect().CompileUpdateColumns(b, values)
	where := g.dialect().CompileWheres(b)

	if len(b.joins) > 0 {
		return strings.TrimSpace(g.dialect().CompileUpdateWithJoins(b, table, columns, where))
	}

	return strings.TrimSpace(g.dialect().CompileUpdateWithoutJoins(b, table, columns, where))
}

// CompileUpdateWithJoins mirrors Grammar::compileUpdateWithJoins().
func (g *BaseGrammar) CompileUpdateWithJoins(b *Builder, table string, columns string, where string) string {
	joins := g.dialect().CompileJoins(b, b.joins)

	return "update " + table + " " + joins + " set " + columns + " " + where
}

// CompileUpdateWithoutJoins mirrors Grammar::compileUpdateWithoutJoins().
func (g *BaseGrammar) CompileUpdateWithoutJoins(b *Builder, table string, columns string, where string) string {
	return "update " + table + " set " + columns + " " + where
}

// CompileUpdateColumns mirrors Grammar::compileUpdateColumns().
func (g *BaseGrammar) CompileUpdateColumns(b *Builder, values UpdateValues) string {
	parts := make([]string, 0, len(values))

	for _, value := range values {
		parts = append(parts, g.dialect().Wrap(value.Column)+" = "+g.dialect().Parameter(value.Value))
	}

	return strings.Join(parts, ", ")
}

// CompileUpsert mirrors Grammar::compileUpsert(). The base refuses.
func (g *BaseGrammar) CompileUpsert(b *Builder, values []map[string]any, uniqueBy []string, update []UpsertUpdate) string {
	b.setError(errUpsertNotSupported)
	return ""
}

// CompileDelete mirrors Grammar::compileDelete().
func (g *BaseGrammar) CompileDelete(b *Builder) string {
	table := g.dialect().WrapTable(b.from)
	where := g.dialect().CompileWheres(b)

	if len(b.joins) > 0 {
		return strings.TrimSpace(g.dialect().CompileDeleteWithJoins(b, table, where))
	}

	return strings.TrimSpace(g.dialect().CompileDeleteWithoutJoins(b, table, where))
}

// CompileDeleteWithJoins mirrors Grammar::compileDeleteWithJoins().
func (g *BaseGrammar) CompileDeleteWithJoins(b *Builder, table string, where string) string {
	parts := strings.Split(table, " as ")
	alias := parts[len(parts)-1]
	joins := g.dialect().CompileJoins(b, b.joins)

	return "delete " + alias + " from " + table + " " + joins + " " + where
}

// CompileDeleteWithoutJoins mirrors Grammar::compileDeleteWithoutJoins().
func (g *BaseGrammar) CompileDeleteWithoutJoins(b *Builder, table string, where string) string {
	return "delete from " + table + " " + where
}

// CompileTruncate mirrors Grammar::compileTruncate().
func (g *BaseGrammar) CompileTruncate(b *Builder) []Statement {
	return []Statement{{SQL: "truncate table " + g.dialect().WrapTable(b.from)}}
}
