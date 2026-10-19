package query

import (
	"regexp"
	"strconv"
	"strings"
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
	g.BaseGrammar.Attach(g)
	g.BaseGrammar.SetValueWrapper(func(value string) string {
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
func (g *SqlServerGrammar) CompileSelect(b *Builder) string {
	if b.offset != nil && len(b.orders) == 0 {
		b.orders = append(b.orders, Order{Type: OrderRawType, SQL: "(SELECT 0)"})
	}

	return g.BaseGrammar.CompileSelect(b)
}

// CompileColumns mirrors SqlServerGrammar::compileColumns(): a limit without an offset becomes top.
func (g *SqlServerGrammar) CompileColumns(b *Builder, columns []any) string {
	if b.aggregate != nil {
		return ""
	}

	selectClause := "select "
	if b.distinct {
		selectClause = "select distinct "
	}

	if b.limit != nil && *b.limit > 0 && (b.offset == nil || *b.offset <= 0) {
		selectClause += "top " + strconv.Itoa(*b.limit) + " "
	}

	return selectClause + g.Columnize(columns)
}

// CompileFrom mirrors SqlServerGrammar::compileFrom(): the lock is expressed on the from clause.
func (g *SqlServerGrammar) CompileFrom(b *Builder, table any) string {
	from := g.BaseGrammar.CompileFrom(b, table)

	if s, ok := b.lock.(string); ok {
		return from + " " + s
	}

	if b.lock != nil {
		updlock := ""
		if locked, ok := b.lock.(bool); ok && locked {
			updlock = "updlock,"
		}
		return from + " with(rowlock," + updlock + "holdlock)"
	}

	return from
}

// CompileIndexHint mirrors SqlServerGrammar::compileIndexHint().
func (g *SqlServerGrammar) CompileIndexHint(b *Builder, hint *IndexHint) string {
	if hint.Type == "force" {
		return "with (index(" + hint.Index + "))"
	}

	return ""
}

// WhereBitwise mirrors SqlServerGrammar::whereBitwise().
func (g *SqlServerGrammar) WhereBitwise(b *Builder, where Where) string {
	operator := strings.ReplaceAll(where.Operator, "?", "??")

	return "(" + g.Wrap(where.Column) + " " + operator + " " + g.Parameter(where.Value) + ") != 0"
}

// WhereDate mirrors SqlServerGrammar::whereDate().
func (g *SqlServerGrammar) WhereDate(b *Builder, where Where) string {
	return "cast(" + g.Wrap(where.Column) + " as date) " + where.Operator + " " + g.Parameter(where.Value)
}

// WhereTime mirrors SqlServerGrammar::whereTime().
func (g *SqlServerGrammar) WhereTime(b *Builder, where Where) string {
	return "cast(" + g.Wrap(where.Column) + " as time) " + where.Operator + " " + g.Parameter(where.Value)
}

// CompileHaving mirrors SqlServerGrammar::compileHaving().
func (g *SqlServerGrammar) CompileHaving(b *Builder, having Having) string {
	if having.Type == HavingBitType {
		return "(" + g.Wrap(having.Column) + " " + having.Operator + " " + g.Parameter(having.Value) + ") != 0"
	}

	return g.BaseGrammar.CompileHaving(b, having)
}

// CompileDeleteWithoutJoins mirrors SqlServerGrammar::compileDeleteWithoutJoins().
func (g *SqlServerGrammar) CompileDeleteWithoutJoins(b *Builder, table string, where string) string {
	sql := g.BaseGrammar.CompileDeleteWithoutJoins(b, table, where)

	if b.limit != nil && *b.limit > 0 && (b.offset == nil || *b.offset <= 0) {
		return strings.Replace(sql, "delete", "delete top ("+strconv.Itoa(*b.limit)+")", 1)
	}

	return sql
}

// CompileRandom mirrors SqlServerGrammar::compileRandom().
func (g *SqlServerGrammar) CompileRandom(seed any) string { return "NEWID()" }

// CompileLimit mirrors SqlServerGrammar::compileLimit().
func (g *SqlServerGrammar) CompileLimit(b *Builder, limit *int) string {
	if limit == nil || *limit == 0 {
		return ""
	}
	if b.offset != nil && *b.offset > 0 {
		return "fetch next " + strconv.Itoa(*limit) + " rows only"
	}

	return ""
}

// CompileOffset mirrors SqlServerGrammar::compileOffset().
func (g *SqlServerGrammar) CompileOffset(b *Builder, offset *int) string {
	if offset == nil || *offset == 0 {
		return ""
	}

	return "offset " + strconv.Itoa(*offset) + " rows"
}

// CompileLock mirrors SqlServerGrammar::compileLock().
func (g *SqlServerGrammar) CompileLock(b *Builder, value any) string { return "" }

// WrapUnion mirrors SqlServerGrammar::wrapUnion().
func (g *SqlServerGrammar) WrapUnion(sql string) string {
	return "select * from (" + sql + ") as " + g.WrapTable("temp_table")
}

// CompileExists mirrors SqlServerGrammar::compileExists().
func (g *SqlServerGrammar) CompileExists(b *Builder) string {
	inner := b.Clone()
	inner.columns = []any{}
	inner.AddSelect(Expression{Value: "1 [exists]"})
	inner.Limit(1)

	return g.CompileSelect(inner)
}

// CompileUpdateWithJoins mirrors SqlServerGrammar::compileUpdateWithJoins().
func (g *SqlServerGrammar) CompileUpdateWithJoins(b *Builder, table string, columns string, where string) string {
	alias := aliasOf(table)
	joins := g.CompileJoins(b, b.joins)

	return "update " + alias + " set " + columns + " from " + table + " " + joins + " " + where
}

// CompileUpsert mirrors SqlServerGrammar::compileUpsert(): a merge statement.
func (g *SqlServerGrammar) CompileUpsert(b *Builder, values []map[string]any, uniqueBy []string, update []UpsertUpdate) string {
	columns := sortedKeys(values[0])

	parameters := make([]string, 0, len(values))
	for _, record := range values {
		placeholders := make([]any, 0, len(columns))
		for _, column := range columns {
			placeholders = append(placeholders, record[column])
		}
		parameters = append(parameters, "("+g.Parameterize(placeholders)+")")
	}

	sql := "merge " + g.WrapTable(b.from) + " using (values " + strings.Join(parameters, ", ") + ") " +
		g.WrapTable("laravel_source") + " (" + g.Columnize(anySlice(columns)) + ") "

	on := make([]string, 0, len(uniqueBy))
	for _, column := range uniqueBy {
		on = append(on, g.Wrap("laravel_source."+column)+" = "+g.Wrap(asString(b.from)+"."+column))
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

	columnList := g.Columnize(anySlice(columns))
	sql += "when not matched then insert (" + columnList + ") values (" + columnList + ");"

	return sql
}

// PrepareBindingsForUpdate mirrors SqlServerGrammar::prepareBindingsForUpdate().
func (g *SqlServerGrammar) PrepareBindingsForUpdate(bindings map[string][]any, values UpdateValues) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = g.AppendUpdateValueBinding(out, value.Value)
	}

	for _, typ := range BindingTypes {
		if typ == "select" {
			continue
		}
		out = append(out, bindings[typ]...)
	}

	return out
}

// CompileJoinLateral mirrors SqlServerGrammar::compileJoinLateral().
func (g *SqlServerGrammar) CompileJoinLateral(join *JoinClause, expression string) string {
	typ := "cross"
	if join.Type == "left" {
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
	if IsExpression(table) {
		return ExpressionValue(table)
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

// CompileInsertGetIdOutput compiles an insert that reports the generated key through an OUTPUT clause.
//
// It is the SQL Server counterpart of the returning clause PostgresGrammar::compileInsertGetId appends,
// and exists because go-mssqldb's LastInsertId returns -1 by design ("LastInsertId is not supported.
// Please use the OUTPUT clause or add `select ID = convert(bigint, SCOPE_IDENTITY())`"). Laravel keeps
// the compiled insert plain and reads the key in its SQL Server processor, but a following
// SCOPE_IDENTITY() statement would run on a pooled connection that may differ from the insert's, so the
// key is carried by the insert itself. SQL Server places OUTPUT between the column list and the source
// of the rows: before VALUES, or before `default values` when no column is named.
func (g *SqlServerGrammar) CompileInsertGetIdOutput(b *Builder, values []map[string]any, sequence string) string {
	if sequence == "" {
		sequence = "id"
	}

	clause := " output inserted." + g.Wrap(sequence)

	insert := g.CompileInsert(b, values)

	if index := strings.Index(insert, " values "); index != -1 {
		return insert[:index] + clause + insert[index:]
	}

	if strings.HasSuffix(insert, " default values") {
		return strings.TrimSuffix(insert, " default values") + clause + " default values"
	}

	return insert
}
