package query

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/goravel/framework/database/schema"
)

// SelectComponents is Laravel's $selectComponents order. The grammar compiles in this order.
var SelectComponents = []string{
	"aggregate", "columns", "from", "indexHint", "joins", "wheres",
	"groups", "havings", "orders", "limit", "offset", "lock",
}

var leadingBoolean = regexp.MustCompile(`(?i)^(and|or) `)

// BaseGrammar implements the shared parts of Laravel's query grammar. Dialects embed it and
// override only the methods whose SQL differs. Go embedding does not dispatch virtually, so
// BaseGrammar keeps a self reference to the concrete dialect and routes shared logic through it.
type BaseGrammar struct {
	self   Grammar
	wrap   *schema.Wrap
	prefix string
}

// NewBaseGrammar creates the shared grammar. Call Attach with the concrete dialect afterwards.
func NewBaseGrammar(prefix string) *BaseGrammar {
	g := &BaseGrammar{wrap: schema.NewWrap(prefix), prefix: prefix}
	// The base quotes with double quotes, mirroring Grammar::wrapValue(). Dialects replace this
	// with their own wrapper, for example backticks for MySQL and brackets for SQL Server.
	g.wrap.SetValueWrapper(func(value string) string {
		if value == "*" {
			return value
		}
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	})
	return g
}

// Attach wires the concrete dialect so shared implementations reach its overrides.
func (g *BaseGrammar) Attach(self Grammar) { g.self = self }

// dialect returns the concrete grammar, or the base when Attach was not called.
func (g *BaseGrammar) dialect() Grammar {
	if g.self != nil {
		return g.self
	}
	return g
}

// SetValueWrapper sets the dialect's identifier quoting.
func (g *BaseGrammar) SetValueWrapper(wrapper func(string) string) { g.wrap.SetValueWrapper(wrapper) }

// Prefix returns the table prefix.
func (g *BaseGrammar) Prefix() string { return g.prefix }

// --- wrapping and parameters ---

// Wrap quotes a column or value. Mirrors Grammar::wrap().
func (g *BaseGrammar) Wrap(value any) string {
	if IsExpression(value) {
		return ExpressionValue(value)
	}

	if s, ok := value.(string); ok {
		if g.dialect().IsJsonSelector(s) {
			return g.dialect().WrapJsonSelector(s)
		}
		return g.wrap.Column(s)
	}

	return fmt.Sprint(value)
}

// WrapTable quotes a table. Mirrors Grammar::wrapTable().
func (g *BaseGrammar) WrapTable(table any) string {
	if IsExpression(table) {
		return ExpressionValue(table)
	}

	if s, ok := table.(string); ok {
		return g.wrap.Table(s)
	}

	return fmt.Sprint(table)
}

// GetValue returns an expression's raw SQL. Mirrors Grammar::getValue().
func (g *BaseGrammar) GetValue(expression Expression) string { return expression.Value }

// Parameter returns the placeholder for a value. Mirrors Grammar::parameter().
func (g *BaseGrammar) Parameter(value any) string {
	if IsExpression(value) {
		return ExpressionValue(value)
	}

	return "?"
}

// Parameterize joins placeholders with a comma. Mirrors Grammar::parameterize().
func (g *BaseGrammar) Parameterize(values []any) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = g.dialect().Parameter(value)
	}

	return strings.Join(parts, ", ")
}

// Columnize joins wrapped columns with a comma. Mirrors Grammar::columnize().
func (g *BaseGrammar) Columnize(columns []any) string {
	parts := make([]string, len(columns))
	for i, column := range columns {
		parts[i] = g.dialect().Wrap(column)
	}

	return strings.Join(parts, ", ")
}

// WrapValue quotes a single identifier. Mirrors Grammar::wrapValue().
func (g *BaseGrammar) WrapValue(value string) string { return g.wrap.Value(value) }

// GetOperators returns dialect specific operators. The base adds none.
func (g *BaseGrammar) GetOperators() []string { return nil }

// GetBitwiseOperators returns dialect specific bitwise operators. The base adds none.
func (g *BaseGrammar) GetBitwiseOperators() []string { return nil }

// GetDateFormat returns the dialect date format. Mirrors Grammar::getDateFormat().
func (g *BaseGrammar) GetDateFormat() string { return "Y-m-d H:i:s" }

// IsJsonSelector reports whether a value is a JSON selector. Mirrors Grammar::isJsonSelector().
func (g *BaseGrammar) IsJsonSelector(value string) bool { return strings.Contains(value, "->") }

// PrepareBindingForJsonContains normalises a JSON contains binding. The base json-encodes the value,
// matching Grammar::prepareBindingForJsonContains(); SQLite and SQL Server override to pass scalars
// through unchanged.
func (g *BaseGrammar) PrepareBindingForJsonContains(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}

	return string(encoded)
}

// PrepareWhereLikeBinding normalises a like binding. The base is an identity.
func (g *BaseGrammar) PrepareWhereLikeBinding(value string, caseSensitive bool) string { return value }

// PrepareBindingsForUpdate mirrors Grammar::prepareBindingsForUpdate().
func (g *BaseGrammar) PrepareBindingsForUpdate(bindings map[string][]any, values UpdateValues) []any {
	out := append([]any{}, bindings["join"]...)

	for _, value := range values {
		out = g.AppendUpdateValueBinding(out, value.Value)
	}

	for _, typ := range BindingTypes {
		if typ == "select" || typ == "join" {
			continue
		}
		out = append(out, bindings[typ]...)
	}

	return out
}

// AppendUpdateValueBinding appends one assignment's binding contribution to out. A subquery value
// contributes the bindings of its sub-select, spliced in at the assignment's position, which is what
// Grammar::prepareBindingsForUpdate()'s Arr::flatten does to the bindings closure Builder::update()
// stores for a query value.
func (g *BaseGrammar) AppendUpdateValueBinding(out []any, value any) []any {
	if sub, ok := value.(subQueryBindings); ok {
		return append(out, sub.values...)
	}

	return append(out, value)
}

// PrepareBindingsForDelete mirrors Grammar::prepareBindingsForDelete().
func (g *BaseGrammar) PrepareBindingsForDelete(bindings map[string][]any) []any {
	var out []any
	for _, typ := range BindingTypes {
		if typ == "select" {
			continue
		}
		out = append(out, bindings[typ]...)
	}

	return out
}

// --- select ---

// SelectComponents returns the clause order this dialect compiles in.
// Mirrors Grammar::$selectComponents.
func (g *BaseGrammar) SelectComponents() []string { return SelectComponents }

// CompileSelect compiles a select statement. Mirrors Grammar::compileSelect().
func (g *BaseGrammar) CompileSelect(b *Builder) string {
	d := g.dialect()

	if (len(b.unions) > 0 || len(b.havings) > 0) && b.aggregate != nil {
		return d.CompileUnionAggregate(b)
	}

	// A "group limit" compiles to a different shape entirely: Laravel short-circuits to
	// compileGroupLimit before touching the normal component list. The Go builder mirrors
	// Laravel's isset($query->groupLimit) with the groupLimitSet presence flag.
	if b.groupLimitSet {
		if b.columns == nil {
			b.columns = []any{"*"}
		}

		return d.CompileGroupLimit(b)
	}

	original := b.columns
	if b.columns == nil {
		b.columns = []any{"*"}
	}

	sql := strings.TrimSpace(strings.Join(d.CompileComponents(b), " "))

	if len(b.unions) > 0 {
		sql = d.WrapUnion(sql) + " " + d.CompileUnions(b)
	}

	b.columns = original

	return sql
}

// CompileComponents compiles each set component in Laravel's fixed order.
// Mirrors Grammar::compileComponents().
func (g *BaseGrammar) CompileComponents(b *Builder) []string {
	d := g.dialect()
	order := d.SelectComponents()
	out := make([]string, 0, len(order))

	for _, component := range order {
		var compiled string

		switch component {
		case "aggregate":
			if b.aggregate == nil {
				continue
			}
			compiled = d.CompileAggregate(b, *b.aggregate)
		case "columns":
			if b.columns == nil {
				continue
			}
			compiled = d.CompileColumns(b, b.columns)
		case "from":
			if b.from == nil {
				continue
			}
			compiled = d.CompileFrom(b, b.from)
		case "indexHint":
			if b.indexHint == nil {
				continue
			}
			compiled = d.CompileIndexHint(b, b.indexHint)
		case "joins":
			if len(b.joins) == 0 {
				continue
			}
			compiled = d.CompileJoins(b, b.joins)
		case "wheres":
			if len(b.wheres) == 0 {
				continue
			}
			compiled = d.CompileWheres(b)
		case "groups":
			if len(b.groups) == 0 {
				continue
			}
			compiled = d.CompileGroups(b, b.groups)
		case "havings":
			if len(b.havings) == 0 {
				continue
			}
			compiled = d.CompileHavings(b)
		case "orders":
			if len(b.orders) == 0 {
				continue
			}
			compiled = d.CompileOrders(b, b.orders)
		case "limit":
			if b.limit == nil {
				continue
			}
			compiled = d.CompileLimit(b, b.limit)
		case "offset":
			if b.offset == nil {
				continue
			}
			compiled = d.CompileOffset(b, b.offset)
		case "lock":
			if b.lock == nil {
				continue
			}
			compiled = d.CompileLock(b, b.lock)
		}

		if compiled != "" {
			out = append(out, compiled)
		}
	}

	return out
}

// CompileAggregate mirrors Grammar::compileAggregate().
func (g *BaseGrammar) CompileAggregate(b *Builder, aggregate Aggregate) string {
	column := g.dialect().Columnize(aggregate.Columns)

	if len(b.distinctColumns) > 0 {
		column = "distinct " + g.dialect().Columnize(b.distinctColumns)
	} else if b.distinct && column != "*" {
		column = "distinct " + column
	}

	return "select " + aggregate.Function + "(" + column + ") as aggregate"
}

// CompileColumns mirrors Grammar::compileColumns().
func (g *BaseGrammar) CompileColumns(b *Builder, columns []any) string {
	if b.aggregate != nil {
		return ""
	}

	selectClause := "select "
	if b.distinct {
		selectClause = "select distinct "
	}

	return selectClause + g.dialect().Columnize(columns)
}

// CompileFrom mirrors Grammar::compileFrom().
func (g *BaseGrammar) CompileFrom(b *Builder, table any) string {
	return "from " + g.dialect().WrapTable(table)
}

// CompileIndexHint is dialect specific. The base compiles nothing.
func (g *BaseGrammar) CompileIndexHint(b *Builder, hint *IndexHint) string { return "" }

// CompileJoins mirrors Grammar::compileJoins().
func (g *BaseGrammar) CompileJoins(b *Builder, joins []*JoinClause) string {
	d := g.dialect()
	parts := make([]string, 0, len(joins))

	for _, join := range joins {
		table := d.WrapTable(join.Table)

		if len(join.joins) > 0 {
			table = "(" + table + " " + d.CompileJoins(join.Builder, join.joins) + ")"
		}

		if join.isLateral {
			compiled := d.CompileJoinLateral(join, table)
			if compiled == "" {
				// The dialect refused, as Grammar::compileJoinLateral throws a RuntimeException there.
				// The port records the refusal on the query instead of panicking.
				b.setError(errLateralJoinNotSupported)
				continue
			}

			parts = append(parts, compiled)
			continue
		}

		parts = append(parts, strings.TrimSpace(join.Type+" join "+table+" "+d.CompileWheres(join.Builder)))
	}

	return strings.Join(parts, " ")
}

// CompileGroups mirrors Grammar::compileGroups().
func (g *BaseGrammar) CompileGroups(b *Builder, groups []any) string {
	return "group by " + g.dialect().Columnize(groups)
}

// CompileHavings mirrors Grammar::compileHavings().
func (g *BaseGrammar) CompileHavings(b *Builder) string {
	d := g.dialect()
	parts := make([]string, 0, len(b.havings))

	for _, having := range b.havings {
		parts = append(parts, having.Boolean+" "+d.CompileHaving(b, having))
	}

	return "having " + removeLeadingBoolean(strings.Join(parts, " "))
}

// CompileHaving mirrors Grammar::compileHaving().
func (g *BaseGrammar) CompileHaving(b *Builder, having Having) string {
	switch having.Type {
	case HavingRawType:
		return fmt.Sprint(having.SQL)
	case HavingBetweenType:
		return g.compileHavingBetween(having)
	case HavingNullType:
		return g.dialect().Wrap(having.Column) + " is null"
	case HavingNotNullType:
		return g.dialect().Wrap(having.Column) + " is not null"
	case HavingExpressionType:
		if expr, ok := having.Column.(Expression); ok {
			return g.dialect().GetValue(expr)
		}
		return fmt.Sprint(having.Column)
	case HavingNestedType:
		if having.Query == nil {
			return ""
		}
		return "(" + strings.TrimPrefix(g.dialect().CompileHavings(having.Query), "having ") + ")"
	default:
		return g.dialect().Wrap(having.Column) + " " + having.Operator + " " + g.dialect().Parameter(having.Value)
	}
}

func (g *BaseGrammar) compileHavingBetween(having Having) string {
	between := "between"
	if having.Not {
		between = "not between"
	}

	min, max := firstAndLast(having.Values)

	return g.dialect().Wrap(having.Column) + " " + between + " " + g.dialect().Parameter(min) + " and " + g.dialect().Parameter(max)
}

// CompileOrders mirrors Grammar::compileOrders().
func (g *BaseGrammar) CompileOrders(b *Builder, orders []Order) string {
	if len(orders) == 0 {
		return ""
	}

	parts := make([]string, 0, len(orders))
	for _, order := range orders {
		if order.Type == OrderRawType {
			parts = append(parts, fmt.Sprint(order.SQL))
			continue
		}
		parts = append(parts, g.dialect().Wrap(order.Column)+" "+order.Direction)
	}

	return "order by " + strings.Join(parts, ", ")
}

// CompileLimit mirrors Grammar::compileLimit().
func (g *BaseGrammar) CompileLimit(b *Builder, limit *int) string {
	if limit == nil {
		return ""
	}

	return "limit " + strconv.Itoa(*limit)
}

// CompileOffset mirrors Grammar::compileOffset().
func (g *BaseGrammar) CompileOffset(b *Builder, offset *int) string {
	if offset == nil {
		return ""
	}

	return "offset " + strconv.Itoa(*offset)
}

// CompileLock mirrors Grammar::compileLock().
func (g *BaseGrammar) CompileLock(b *Builder, value any) string {
	if s, ok := value.(string); ok {
		return s
	}

	// The boolean shortcuts — lockForUpdate() and sharedLock() — resolve here, which is what Laravel's
	// base grammar does; a dialect that spells the clauses differently overrides this.
	if locked, ok := value.(bool); ok {
		if locked {
			return "for update"
		}

		return "lock in share mode"
	}

	return ""
}

// CompileRandom mirrors Grammar::compileRandom().
func (g *BaseGrammar) CompileRandom(seed any) string { return "RANDOM()" }

// CompileRowNumber mirrors Grammar::compileRowNumber(), used by the group limit rewrite.
func (g *BaseGrammar) CompileRowNumber(partition string, orders string) string {
	over := strings.TrimSpace("partition by " + g.dialect().Wrap(partition) + " " + orders)

	return ", row_number() over (" + over + ") as " + g.dialect().Wrap("laravel_row")
}

// CompileGroupLimit mirrors Grammar::compileGroupLimit().
func (g *BaseGrammar) CompileGroupLimit(b *Builder) string {
	d := g.dialect()

	selectBindings := append(append([]any{}, b.bindings["select"]...), b.bindings["order"]...)
	_ = b.SetBindings(selectBindings, "select")
	_ = b.SetBindings(nil, "order")

	limit := b.groupLimit
	offset := b.offset
	if offset != nil {
		limit += *offset
		b.offset = nil
	}

	components := d.CompileComponents(b)

	orders := d.CompileOrders(b, b.orders)
	columns := ""
	for i, component := range components {
		if strings.HasPrefix(component, "select ") {
			columns = component
			components = append(components[:i], components[i+1:]...)
			break
		}
	}

	sql := strings.Join(append([]string{columns + d.CompileRowNumber(b.groupLimitColumn, orders)}, components...), " ")

	table := d.Wrap("laravel_table")
	row := d.Wrap("laravel_row")

	sql = "select * from (" + sql + ") as " + table + " where " + row + " <= " + strconv.Itoa(limit)
	if offset != nil {
		sql += " and " + row + " > " + strconv.Itoa(*offset)
	}

	return sql + " order by " + row
}

// CompileLegacyGroupLimit is only implemented by MySQL.
func (g *BaseGrammar) CompileLegacyGroupLimit(b *Builder) string {
	return g.dialect().CompileGroupLimit(b)
}

// UseLegacyGroupLimit reports whether the dialect needs the legacy group limit rewrite.
func (g *BaseGrammar) UseLegacyGroupLimit(b *Builder) bool { return false }

// CompileInsertOrIgnoreUsing mirrors Grammar::compileInsertOrIgnoreUsing(). The base refuses.
func (g *BaseGrammar) CompileInsertOrIgnoreUsing(b *Builder, columns []any, sql string) string {
	b.setError(errInsertIgnoreNotSupported)
	return ""
}

// CompileJoinLateral mirrors Grammar::compileJoinLateral(). The base does not support lateral joins
// and compiles nothing; CompileJoins records errLateralJoinNotSupported, mirroring the
// RuntimeException the base throws.
func (g *BaseGrammar) CompileJoinLateral(join *JoinClause, expression string) string {
	return ""
}

// CompileJsonValueCast mirrors Grammar::compileJsonValueCast(). The base is an identity.
func (g *BaseGrammar) CompileJsonValueCast(value string) string { return value }

// WrapUnion mirrors Grammar::wrapUnion().
func (g *BaseGrammar) WrapUnion(sql string) string { return "(" + sql + ")" }

// CompileUnions mirrors Grammar::compileUnions().
func (g *BaseGrammar) CompileUnions(b *Builder) string {
	d := g.dialect()
	var out strings.Builder

	for _, union := range b.unions {
		if union.All {
			out.WriteString(" union all ")
		} else {
			out.WriteString(" union ")
		}

		if union.Query != nil {
			out.WriteString(d.WrapUnion(union.Query.ToSQL()))
		}
	}

	if len(b.unionOrders) > 0 {
		out.WriteString(" " + d.CompileOrders(b, b.unionOrders))
	}
	if b.unionLimit != nil {
		out.WriteString(" " + d.CompileLimit(b, b.unionLimit))
	}
	if b.unionOffset != nil {
		out.WriteString(" " + d.CompileOffset(b, b.unionOffset))
	}

	return strings.TrimLeft(out.String(), " ")
}

// CompileUnionAggregate mirrors Grammar::compileUnionAggregate().
func (g *BaseGrammar) CompileUnionAggregate(b *Builder) string {
	sql := g.dialect().CompileAggregate(b, *b.aggregate)
	b.aggregate = nil

	return sql + " from (" + g.dialect().CompileSelect(b) + ") as " + g.dialect().WrapTable("temp_table")
}

// CompileExists mirrors Grammar::compileExists().
func (g *BaseGrammar) CompileExists(b *Builder) string {
	return "select exists(" + g.dialect().CompileSelect(b) + ") as " + g.dialect().Wrap("exists")
}

// --- JSON, dialect specific. The base refuses, exactly as Laravel does, by returning an empty
// fragment; the where compiler records the capability error.

func (g *BaseGrammar) WrapJsonSelector(value string) string { return "" }

func (g *BaseGrammar) WrapJsonBooleanSelector(value string) string {
	return g.dialect().WrapJsonSelector(value)
}

func (g *BaseGrammar) WrapJsonBooleanValue(value string) string { return value }

func (g *BaseGrammar) CompileJsonContains(column string, value string) string { return "" }

func (g *BaseGrammar) CompileJsonContainsKey(column string) string { return "" }

func (g *BaseGrammar) CompileJsonLength(column string, operator string, value string) string {
	return ""
}

func (g *BaseGrammar) CompileJsonOverlaps(column string, value string) string { return "" }

func (g *BaseGrammar) CompileJsonUpdateColumn(key string, value any) string {
	return g.dialect().Wrap(key) + " = " + g.dialect().Parameter(value)
}
