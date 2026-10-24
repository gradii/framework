package grammars

import (
	"strings"

	"github.com/goravel/framework/database/query/core"
)

// SQL Server JSON updates extend Laravel's grammar to preserve the framework's JSON write API.
// JSON_VALUE can read a selector, but an assignment must target the column and use JSON_MODIFY.
// Multiple paths in one column are nested because SQL Server rejects duplicate SET assignments.
func (g *SqlServerGrammar) CompileUpdateColumns(b core.Query, values core.UpdateValues) string {
	merged := g.groupJsonColumnsForUpdate(values)
	parts := make([]string, 0, len(merged))
	for _, value := range merged {
		if group, ok := value.Value.(sqlServerJsonUpdate); ok {
			parts = append(parts, g.compileJsonUpdateGroup(value.Column, group))
			continue
		}
		parts = append(parts, g.Wrap(value.Column)+" = "+g.Parameter(value.Value))
	}

	return strings.Join(parts, ", ")
}

// CompileJsonUpdateColumn renders a single JSON path assignment.
func (g *SqlServerGrammar) CompileJsonUpdateColumn(key string, value any) string {
	column, _, _ := strings.Cut(key, "->")
	return g.compileJsonUpdateGroup(column, sqlServerJsonUpdate{{Column: key, Value: value}})
}

// groupJsonColumnsForUpdate keeps each JSON column at its first selector's position. Both SQL
// compilation and binding preparation use this order, including when ordinary assignments intervene.
func (g *SqlServerGrammar) groupJsonColumnsForUpdate(values core.UpdateValues) core.UpdateValues {
	merged := make(core.UpdateValues, 0, len(values))
	positions := make(map[string]int)
	for _, value := range values {
		if !g.IsJsonSelector(value.Column) {
			merged = append(merged, value)
			continue
		}

		column, _, _ := strings.Cut(value.Column, "->")
		if index, ok := positions[column]; ok {
			merged[index].Value = append(merged[index].Value.(sqlServerJsonUpdate), value)
			continue
		}
		positions[column] = len(merged)
		merged = append(merged, core.UpdateValue{Column: column, Value: sqlServerJsonUpdate{value}})
	}

	return merged
}

func (g *SqlServerGrammar) compileJsonUpdateGroup(column string, group sqlServerJsonUpdate) string {
	field := g.Wrap(column)
	expression := field
	for _, selector := range group {
		_, path := g.wrap.JsonFieldAndPath(selector.Column)
		value := g.Parameter(selector.Value)
		if core.IsArrayValue(selector.Value) {
			// JSON_QUERY prevents SQL Server from storing the encoded document as a quoted string.
			value = g.CompileJsonValueCast(value)
		}
		expression = "json_modify(" + expression + path + ", " + value + ")"
	}

	return field + " = " + expression
}

type sqlServerJsonUpdate []core.UpdateValue
