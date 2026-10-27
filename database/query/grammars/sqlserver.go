package grammars

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/gradii/framework/database/query/core"
)

var tableValuedFunction = regexp.MustCompile(`^(.+?)(\(.*?\))]$`)

// SqlServerGrammar mirrors Illuminate\Database\Query\Grammars\SqlServerGrammar. Only the methods
// whose SQL differs from the base are declared here. SQL Server quotes with brackets.
type SqlServerGrammar struct {
	*BaseGrammar
}

// NewSqlServerGrammar creates the SQL Server grammar.
func NewSqlServerGrammar(prefix string) *SqlServerGrammar {
	g := &SqlServerGrammar{BaseGrammar: NewBaseGrammar(prefix)}
	g.Attach(g)
	g.SetValueWrapper(func(value string) string {
		if value == "*" {
			return value
		}
		return "[" + strings.ReplaceAll(value, "]", "]]") + "]"
	})

	return g
}

// sqlServerSelectComponents mirrors SqlServerGrammar::$selectComponents: offset comes before limit.
var sqlServerSelectComponents = []string{
	"aggregate", "columns", "from", "indexHint", "joins", "wheres",
	"groups", "havings", "orders", "offset", "limit", "lock",
}

// SelectComponents mirrors SqlServerGrammar::$selectComponents.
func (g *SqlServerGrammar) SelectComponents() []string { return sqlServerSelectComponents }

// GetOperators mirrors SqlServerGrammar::$operators, which adds these beyond the base set.
func (g *SqlServerGrammar) GetOperators() []string {
	return []string{"!<", "!>", "&=", "|=", "^=", "ilike"}
}

// CompileSelect mirrors SqlServerGrammar::compileSelect(): offset needs an order by.
func (g *SqlServerGrammar) CompileSelect(b core.Query) string {
	if b.State().Offset != nil && len(b.State().Orders) == 0 {
		b.State().Orders = append(b.State().Orders, core.Order{Type: core.OrderRawType, SQL: "(SELECT 0)"})
	}

	return g.BaseGrammar.CompileSelect(b)
}

// CompileColumns mirrors SqlServerGrammar::compileColumns(): a limit without an offset becomes top.
func (g *SqlServerGrammar) CompileColumns(b core.Query, columns []any) string {
	if b.State().Aggregate != nil {
		return ""
	}

	selectClause := "select "
	if b.State().Distinct {
		selectClause = "select distinct "
	}

	if b.State().Limit != nil && *b.State().Limit > 0 && (b.State().Offset == nil || *b.State().Offset <= 0) {
		selectClause += "top " + strconv.Itoa(*b.State().Limit) + " "
	}

	return selectClause + g.Columnize(columns)
}

// CompileFrom mirrors SqlServerGrammar::compileFrom(): the lock is expressed on the from clause.
func (g *SqlServerGrammar) CompileFrom(b core.Query, table any) string {
	from := g.BaseGrammar.CompileFrom(b, table)

	if s, ok := b.State().Lock.(string); ok {
		return from + " " + s
	}

	if b.State().Lock != nil {
		updlock := ""
		if locked, ok := b.State().Lock.(bool); ok && locked {
			updlock = "updlock,"
		}
		return from + " with(rowlock," + updlock + "holdlock)"
	}

	return from
}

// CompileIndexHint mirrors SqlServerGrammar::compileIndexHint().
func (g *SqlServerGrammar) CompileIndexHint(b core.Query, hint *core.IndexHint) string {
	if hint.Type == "force" {
		return "with (index(" + hint.Index + "))"
	}

	return ""
}

// WhereBitwise mirrors SqlServerGrammar::whereBitwise().
func (g *SqlServerGrammar) WhereBitwise(b core.Query, where core.Where) string {
	operator := strings.ReplaceAll(where.Operator, "?", "??")

	return "(" + g.Wrap(where.Column) + " " + operator + " " + g.Parameter(where.Value) + ") != 0"
}

// WhereDate mirrors SqlServerGrammar::whereDate().
func (g *SqlServerGrammar) WhereDate(b core.Query, where core.Where) string {
	return "cast(" + g.Wrap(where.Column) + " as date) " + where.Operator + " " + g.Parameter(where.Value)
}

// WhereTime mirrors SqlServerGrammar::whereTime().
func (g *SqlServerGrammar) WhereTime(b core.Query, where core.Where) string {
	return "cast(" + g.Wrap(where.Column) + " as time) " + where.Operator + " " + g.Parameter(where.Value)
}

// CompileHaving mirrors SqlServerGrammar::compileHaving().
func (g *SqlServerGrammar) CompileHaving(b core.Query, having core.Having) string {
	if having.Type == core.HavingBitType {
		return "(" + g.Wrap(having.Column) + " " + having.Operator + " " + g.Parameter(having.Value) + ") != 0"
	}

	return g.BaseGrammar.CompileHaving(b, having)
}

// CompileDeleteWithoutJoins mirrors SqlServerGrammar::compileDeleteWithoutJoins().
func (g *SqlServerGrammar) CompileDeleteWithoutJoins(b core.Query, table string, where string) string {
	sql := g.BaseGrammar.CompileDeleteWithoutJoins(b, table, where)

	if b.State().Limit != nil && *b.State().Limit > 0 && (b.State().Offset == nil || *b.State().Offset <= 0) {
		return strings.Replace(sql, "delete", "delete top ("+strconv.Itoa(*b.State().Limit)+")", 1)
	}

	return sql
}

// CompileRandom mirrors SqlServerGrammar::compileRandom().
func (g *SqlServerGrammar) CompileRandom(seed any) string { return "NEWID()" }

// CompileLimit mirrors SqlServerGrammar::compileLimit().
func (g *SqlServerGrammar) CompileLimit(b core.Query, limit *int) string {
	if limit == nil || *limit == 0 {
		return ""
	}
	if b.State().Offset != nil && *b.State().Offset > 0 {
		return "fetch next " + strconv.Itoa(*limit) + " rows only"
	}

	return ""
}

// CompileOffset mirrors SqlServerGrammar::compileOffset().
func (g *SqlServerGrammar) CompileOffset(b core.Query, offset *int) string {
	if offset == nil || *offset == 0 {
		return ""
	}

	return "offset " + strconv.Itoa(*offset) + " rows"
}

// CompileLock mirrors SqlServerGrammar::compileLock().
func (g *SqlServerGrammar) CompileLock(b core.Query, value any) string { return "" }

// WrapUnion mirrors SqlServerGrammar::wrapUnion().
func (g *SqlServerGrammar) WrapUnion(sql string) string {
	return "select * from (" + sql + ") as " + g.WrapTable("temp_table")
}

// CompileExists mirrors SqlServerGrammar::compileExists().
func (g *SqlServerGrammar) CompileExists(b core.Query) string {
	inner := b.CloneForGrammar()
	inner.State().Columns = []any{core.Expression{Value: "1 [exists]"}}
	limit := 1
	inner.State().Limit = &limit

	return g.CompileSelect(inner)
}

// CompileUpdateWithJoins mirrors SqlServerGrammar::compileUpdateWithJoins().
func (g *SqlServerGrammar) CompileUpdateWithJoins(b core.Query, table string, columns string, where string) string {
	alias := aliasOf(table)
	joins := g.CompileJoins(b, b.State().Joins)

	return "update " + alias + " set " + columns + " from " + table + " " + joins + " " + where
}

// CompileUpsert mirrors SqlServerGrammar::compileUpsert(): a merge statement.
func (g *SqlServerGrammar) CompileUpsert(b core.Query, values []map[string]any, uniqueBy []string, update []core.UpsertUpdate) string {
	columns := core.SortedKeys(values[0])

	parameters := make([]string, 0, len(values))
	for _, record := range values {
		placeholders := make([]any, 0, len(columns))
		for _, column := range columns {
			placeholders = append(placeholders, record[column])
		}
		parameters = append(parameters, "("+g.Parameterize(placeholders)+")")
	}

	sql := "merge " + g.WrapTable(b.State().From) + " using (values " + strings.Join(parameters, ", ") + ") " +
		g.WrapTable("laravel_source") + " (" + g.Columnize(core.StringsToAny(columns)) + ") "

	on := make([]string, 0, len(uniqueBy))
	for _, column := range uniqueBy {
		on = append(on, g.Wrap("laravel_source."+column)+" = "+g.Wrap(core.AsString(b.State().From)+"."+column))
	}
	sql += "on " + strings.Join(on, " and ") + " "

	if len(update) > 0 {
		sets := make([]string, 0, len(update))
		for _, item := range update {
			if item.Bound {
				sets = append(sets, g.Wrap(item.Column)+" = "+g.Parameter(item.Value))
				continue
			}
			sets = append(sets, g.Wrap(item.Column)+" = "+g.Wrap("laravel_source."+item.Column))
		}
		sql += "when matched then update set " + strings.Join(sets, ", ") + " "
	}

	columnList := g.Columnize(core.StringsToAny(columns))
	sql += "when not matched then insert (" + columnList + ") values (" + columnList + ");"

	return sql
}

// PrepareBindingsForUpdate keeps Laravel's SQL Server binding order. JSON updates additionally
// group their bindings with the JSON_MODIFY assignment and encode array and object values.
func (g *SqlServerGrammar) PrepareBindingsForUpdate(bindings map[string][]any, values core.UpdateValues) []any {
	out := make([]any, 0, len(values))
	for _, value := range g.groupJsonColumnsForUpdate(values) {
		if group, ok := value.Value.(sqlServerJsonUpdate); ok {
			for _, selector := range group {
				binding := selector.Value
				if core.IsArrayValue(binding) {
					binding = encodeJSON(binding)
				}
				out = g.AppendUpdateValueBinding(out, binding)
			}
			continue
		}
		out = g.AppendUpdateValueBinding(out, value.Value)
	}

	for _, typ := range core.BindingTypes {
		if typ == "select" {
			continue
		}
		out = append(out, bindings[typ]...)
	}

	return out
}

// CompileJoinLateral mirrors SqlServerGrammar::compileJoinLateral().
func (g *SqlServerGrammar) CompileJoinLateral(join core.Join, expression string) string {
	typ := "cross"
	if join.JoinType() == "left" {
		typ = "outer"
	}

	return strings.TrimSpace(typ + " apply " + expression)
}

// CompileSavepoint mirrors SqlServerGrammar::compileSavepoint().
func (g *SqlServerGrammar) CompileSavepoint(name string) string { return "SAVE TRANSACTION " + name }

// CompileSavepointRollBack mirrors SqlServerGrammar::compileSavepointRollBack().
func (g *SqlServerGrammar) CompileSavepointRollBack(name string) string {
	return "ROLLBACK TRANSACTION " + name
}

// GetDateFormat mirrors SqlServerGrammar::getDateFormat().
func (g *SqlServerGrammar) GetDateFormat() string { return "Y-m-d H:i:s.v" }

// WrapTable mirrors SqlServerGrammar::wrapTable().
func (g *SqlServerGrammar) WrapTable(table any) string {
	if core.IsExpression(table) {
		return core.ExpressionValue(table)
	}

	return g.WrapTableValuedFunction(g.BaseGrammar.WrapTable(table))
}

// WrapTableValuedFunction mirrors SqlServerGrammar::wrapTableValuedFunction().
func (g *SqlServerGrammar) WrapTableValuedFunction(table string) string {
	match := tableValuedFunction.FindStringSubmatch(table)
	if match == nil {
		return table
	}

	return match[1] + "]" + match[2]
}

// WrapJsonSelector mirrors SqlServerGrammar::wrapJsonSelector().
func (g *SqlServerGrammar) WrapJsonSelector(value string) string {
	field, path := g.wrap.JsonFieldAndPath(value)

	return "json_value(" + field + path + ")"
}

// WrapJsonBooleanValue mirrors SqlServerGrammar::wrapJsonBooleanValue().
func (g *SqlServerGrammar) WrapJsonBooleanValue(value string) string { return "'" + value + "'" }

// CompileJsonContains mirrors SqlServerGrammar::compileJsonContains().
func (g *SqlServerGrammar) CompileJsonContains(column string, value string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return value + " in (select [value] from openjson(" + field + path + "))"
}

// CompileJsonContainsKey mirrors SqlServerGrammar::compileJsonContainsKey().
func (g *SqlServerGrammar) CompileJsonContainsKey(column string) string {
	segments := strings.Split(column, "->")
	last := segments[len(segments)-1]
	segments = segments[:len(segments)-1]

	key := ""
	if match := regexp.MustCompile(`\[([0-9]+)\]$`).FindStringSubmatch(last); match != nil {
		segments = append(segments, strings.TrimSuffix(last, match[0]))
		key = match[1]
	} else {
		key = "'" + strings.ReplaceAll(last, "'", "''") + "'"
	}

	field, path := g.wrap.JsonFieldAndPath(strings.Join(segments, "->"))

	return key + " in (select [key] from openjson(" + field + path + "))"
}

// CompileJsonLength mirrors SqlServerGrammar::compileJsonLength().
func (g *SqlServerGrammar) CompileJsonLength(column string, operator string, value string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "(select count(*) from openjson(" + field + path + ")) " + operator + " " + value
}

// CompileJsonValueCast mirrors SqlServerGrammar::compileJsonValueCast().
func (g *SqlServerGrammar) CompileJsonValueCast(value string) string {
	return "json_query(" + value + ")"
}

// PrepareBindingForJsonContains mirrors SqlServerGrammar::prepareBindingForJsonContains().
func (g *SqlServerGrammar) PrepareBindingForJsonContains(value any) any {
	if b, ok := value.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}

	return value
}

// CompileRowNumber mirrors SqlServerGrammar::compileRowNumber(): an order by is required.
func (g *SqlServerGrammar) CompileRowNumber(partition string, orders string) string {
	if strings.TrimSpace(orders) == "" {
		orders = "order by (select 0)"
	}

	return g.BaseGrammar.CompileRowNumber(partition, orders)
}

// CompileInsertGetId returns the generated key in the INSERT result. Laravel's SQL Server
// processor uses PDO's identity API; go-mssqldb requires an OUTPUT clause on the same statement.
func (g *SqlServerGrammar) CompileInsertGetId(b core.Query, values []map[string]any, sequence string) string {
	if sequence == "" {
		sequence = "id"
	}
	return g.compileInsert(b, values, " output inserted."+g.Wrap(sequence))
}
