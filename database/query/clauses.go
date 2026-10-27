package query

import (
	"reflect"
	"strings"

	"github.com/gradii/framework/database/query/core"
)

// Select sets the selected columns. Mirrors Builder::select().
//
// Laravel also accepts a string-keyed array to alias a subselect; Go callers use SelectSub for
// that, which is a deliberate deviation because a variadic Go call cannot carry keys.
func (b *Builder) Select(columns ...any) *Builder {
	b.State().Columns = []any{}
	b.State().Bindings["select"] = []any{}

	if len(columns) == 0 {
		columns = []any{"*"}
	}
	for _, column := range selectColumns(columns) {
		if column.alias != "" && IsQueryable(column.value) {
			b.SelectSub(column.value, column.alias)
		} else {
			b.State().Columns = append(b.State().Columns, column.value)
		}
	}

	return b
}

// SelectSub adds a subselect aliased as `as`. Mirrors Builder::selectSub().
func (b *Builder) SelectSub(query any, as string) *Builder {
	sql, bindings := b.createSub(query)

	return b.SelectRaw("("+sql+") as "+b.grammar.Wrap(as), bindings)
}

// SelectRaw adds a raw select expression. Mirrors Builder::selectRaw().
func (b *Builder) SelectRaw(expression string, bindings []any) *Builder {
	b.AddSelect(Expression{Value: expression})

	if len(bindings) > 0 {
		_ = b.AddBinding(bindings, "select")
	}

	return b
}

// AddSelect appends columns without clearing the list. Mirrors Builder::addSelect().
func (b *Builder) AddSelect(columns ...any) *Builder {
	for _, entry := range selectColumns(columns) {
		column := entry.value
		if entry.alias != "" && IsQueryable(column) {
			if b.State().Columns == nil {
				b.Select(core.AsString(b.State().From) + ".*")
			}
			b.SelectSub(column, entry.alias)
			continue
		}
		duplicate := false
		// A value Expression represents a fresh Laravel object. Comparable columns use strict
		// equality: strings match by value and *Expression matches only the same pointer.
		_, valueExpression := column.(Expression)
		if !valueExpression && (column == nil || reflect.ValueOf(column).Comparable()) {
			for _, existing := range b.State().Columns {
				if (existing == nil || reflect.ValueOf(existing).Comparable()) && existing == column {
					duplicate = true
					break
				}
			}
		}
		if duplicate {
			continue
		}

		b.State().Columns = append(b.State().Columns, column)
	}

	return b
}

// Distinct sets distinct, or distinct-on columns. Mirrors Builder::distinct().
func (b *Builder) Distinct(columns ...any) *Builder {
	b.State().DistinctColumns = nil
	if len(columns) == 0 {
		b.State().Distinct = true
		return b
	}

	if len(columns) == 1 {
		switch value := columns[0].(type) {
		case bool:
			b.State().Distinct = value
			return b
		case []any:
			b.State().Distinct = true
			b.State().DistinctColumns = value
			return b
		}
	}

	b.State().Distinct = true
	b.State().DistinctColumns = columns

	return b
}

// From sets the table, or a subquery when table is queryable. Mirrors Builder::from().
func (b *Builder) From(table any, as string) *Builder {
	if IsQueryable(table) {
		return b.FromSub(table, as)
	}

	if as != "" {
		b.State().From = aliased(table, as)
	} else {
		b.State().From = table
	}

	return b
}

// FromSub sets a subquery as the table. Mirrors Builder::fromSub().
func (b *Builder) FromSub(query any, as string) *Builder {
	sql, bindings := b.createSub(query)

	return b.FromRaw("("+sql+") as "+b.grammar.WrapTable(as), bindings)
}

// FromRaw sets a raw table expression. Mirrors Builder::fromRaw().
func (b *Builder) FromRaw(expression string, bindings []any) *Builder {
	b.State().From = Expression{Value: expression}
	_ = b.AddBinding(bindings, "from")

	return b
}

// Join adds a join. Mirrors Builder::join().
func (b *Builder) Join(table any, first any, operator any, second any, typ string, where bool) *Builder {
	join := NewJoinClause(b, typ, table)

	if callback, ok := first.(func(*Builder) *Builder); ok {
		callback(join.Builder)
	} else if first != nil {
		if where {
			join.addWhere(first, []any{operator, second}, core.BooleanAnd)
		} else {
			join.On(first, operator, second, core.BooleanAnd)
		}
	}

	b.State().Joins = append(b.State().Joins, join)
	_ = b.AddBinding(join.GetBindings(), "join")

	return b
}

// JoinWhere adds a join with the condition in the where clause. Mirrors Builder::joinWhere().
func (b *Builder) JoinWhere(table any, first any, operator any, second any, typ string) *Builder {
	return b.Join(table, first, operator, second, typ, true)
}

// JoinSub adds a subquery join. Mirrors Builder::joinSub().
func (b *Builder) JoinSub(query any, as string, first any, operator any, second any, typ string, where bool) *Builder {
	sql, bindings := b.createSub(query)
	expression := Expression{Value: "(" + sql + ") as " + b.grammar.WrapTable(as)}

	_ = b.AddBinding(bindings, "join")

	return b.Join(expression, first, operator, second, typ, where)
}

// JoinLateral adds a lateral join. Mirrors Builder::joinLateral(): the subquery is resolved through
// the same createSub path as JoinSub, its bindings go to the join component, and the clause is stored
// as a lateral JoinClause that compileJoins routes through CompileJoinLateral.
func (b *Builder) JoinLateral(query any, as string, typ string) *Builder {
	sql, bindings := b.createSub(query)
	expression := Expression{Value: "(" + sql + ") as " + b.grammar.WrapTable(as)}

	_ = b.AddBinding(bindings, "join")

	b.State().Joins = append(b.State().Joins, NewJoinLateralClause(b, typ, expression))

	return b
}

// LeftJoinLateral adds a lateral left join. Mirrors Builder::leftJoinLateral(), which is
// joinLateral($query, $as, 'left').
func (b *Builder) LeftJoinLateral(query any, as string) *Builder {
	return b.JoinLateral(query, as, "left")
}

// LeftJoin adds a left join. Mirrors Builder::leftJoin().
func (b *Builder) LeftJoin(table any, first any, operator any, second any) *Builder {
	return b.Join(table, first, operator, second, "left", false)
}

// LeftJoinSub adds a left subquery join. Mirrors Builder::leftJoinSub().
func (b *Builder) LeftJoinSub(query any, as string, first any, operator any, second any) *Builder {
	return b.JoinSub(query, as, first, operator, second, "left", false)
}

// RightJoin adds a right join. Mirrors Builder::rightJoin().
func (b *Builder) RightJoin(table any, first any, operator any, second any) *Builder {
	return b.Join(table, first, operator, second, "right", false)
}

// RightJoinSub adds a right subquery join. Mirrors Builder::rightJoinSub().
func (b *Builder) RightJoinSub(query any, as string, first any, operator any, second any) *Builder {
	return b.JoinSub(query, as, first, operator, second, "right", false)
}

// CrossJoin adds a cross join. Mirrors Builder::crossJoin().
func (b *Builder) CrossJoin(table any, first any, operator any, second any) *Builder {
	if first != nil {
		return b.Join(table, first, operator, second, "cross", false)
	}

	b.State().Joins = append(b.State().Joins, NewJoinClause(b, "cross", table))

	return b
}

// CrossJoinSub adds a subquery cross join. Mirrors Builder::crossJoinSub(): the subquery is resolved
// through createSub, its bindings go to the join component, and the clause is stored as a cross join
// whose table is the wrapped subquery expression.
func (b *Builder) CrossJoinSub(query any, as string) *Builder {
	sql, bindings := b.createSub(query)
	expression := Expression{Value: "(" + sql + ") as " + b.grammar.WrapTable(as)}

	_ = b.AddBinding(bindings, "join")

	b.State().Joins = append(b.State().Joins, NewJoinClause(b, "cross", expression))

	return b
}

// GroupBy appends grouping columns. Mirrors Builder::groupBy().
func (b *Builder) GroupBy(groups ...any) *Builder {
	for _, group := range groups {
		b.State().Groups = append(b.State().Groups, wrapValues(group)...)
	}

	return b
}

// GroupByRaw appends a raw grouping expression. Mirrors Builder::groupByRaw().
func (b *Builder) GroupByRaw(sql string, bindings []any) *Builder {
	b.State().Groups = append(b.State().Groups, Expression{Value: sql})
	_ = b.AddBinding(bindings, "groupBy")

	return b
}

// Having adds a having clause. Mirrors Builder::having().
func (b *Builder) Having(column any, args ...any) *Builder {
	return b.addHaving(column, args, core.BooleanAnd)
}

// OrHaving adds a having clause with "or". Mirrors Builder::orHaving().
func (b *Builder) OrHaving(column any, args ...any) *Builder {
	return b.addHaving(column, args, core.BooleanOr)
}

func (b *Builder) addHaving(column any, args []any, boolean string) *Builder {
	if expr, ok := column.(Expression); ok {
		b.State().Havings = append(b.State().Havings, core.Having{Type: core.HavingExpressionType, Column: expr, Boolean: boolean})
		return b
	}

	var operator, value any
	switch len(args) {
	case 1:
		operator = args[0]
	default:
		if len(args) >= 2 {
			operator, value = args[0], args[1]
		}
	}

	value, operator, err := b.PrepareValueAndOperator(value, operator, len(args) == 1)
	if err != nil {
		b.setError(err)
		return b
	}

	if callback, ok := column.(func(*Builder) *Builder); ok && operator == nil {
		return b.HavingNested(callback, boolean)
	}

	if b.InvalidOperator(operator) {
		value, operator = operator, "="
	}

	op, _ := operator.(string)

	// A bitwise operator becomes its own having type, exactly as Builder::having() marks it. The base
	// grammar renders it as a plain comparison; the two dialects that define it cast or compare it.
	havingType := core.HavingBasic
	if b.IsBitwiseOperator(op) {
		havingType = core.HavingBitType
	}

	b.State().Havings = append(b.State().Havings, core.Having{Type: havingType, Column: column, Operator: op, Value: value, Boolean: boolean})

	if !IsExpression(value) {
		_ = b.AddBinding(FlattenValue(value), "having")
	}

	return b
}

// HavingRaw adds a raw having clause. Mirrors Builder::havingRaw().
func (b *Builder) HavingRaw(sql string, bindings []any, boolean string) *Builder {
	b.State().Havings = append(b.State().Havings, core.Having{Type: core.HavingRawType, SQL: sql, Boolean: boolean})
	_ = b.AddBinding(bindings, "having")

	return b
}

// OrHavingRaw adds a raw having clause joined with "or". Mirrors Builder::orHavingRaw(), which is
// havingRaw() with the or boolean.
func (b *Builder) OrHavingRaw(sql string, bindings []any) *Builder {
	return b.HavingRaw(sql, bindings, core.BooleanOr)
}

// HavingNested nests having clauses. Mirrors Builder::havingNested().
func (b *Builder) HavingNested(callback func(*Builder) *Builder, boolean string) *Builder {
	query := b.ForNestedWhere()
	callback(query)

	return b.AddNestedHavingQuery(query, boolean)
}

// AddNestedHavingQuery adds a builder as a nested having. Mirrors Builder::addNestedHavingQuery().
func (b *Builder) AddNestedHavingQuery(query *Builder, boolean string) *Builder {
	if len(query.State().Havings) > 0 {
		b.State().Havings = append(b.State().Havings, core.Having{Type: core.HavingNestedType, Query: query, Boolean: boolean})
		_ = b.AddBinding(query.State().Bindings["having"], "having")
	}

	return b
}

// HavingBetween adds a having between clause. Mirrors Builder::havingBetween().
func (b *Builder) HavingBetween(column any, values []any, boolean string, not bool) *Builder {
	b.State().Havings = append(b.State().Havings, core.Having{Type: core.HavingBetweenType, Column: column, Values: values, Boolean: boolean, Not: not})

	// Laravel resolves the bounds with Arr::flatten() before binding, exactly like whereBetween.
	bounds := CleanBindings(flatten(values, 0))
	if len(bounds) > 2 {
		bounds = bounds[:2]
	}
	_ = b.AddBinding(bounds, "having")

	return b
}

// HavingNull adds a having null clause. Mirrors Builder::havingNull().
func (b *Builder) HavingNull(columns any, boolean string, not bool) *Builder {
	typ := core.HavingNullType
	if not {
		typ = core.HavingNotNullType
	}

	for _, column := range wrapValues(columns) {
		b.State().Havings = append(b.State().Havings, core.Having{Type: typ, Column: column, Boolean: boolean})
	}

	return b
}

// HavingNotNull adds a having not null clause. Mirrors Builder::havingNotNull().
func (b *Builder) HavingNotNull(columns any, boolean string) *Builder {
	return b.HavingNull(columns, boolean, true)
}

// OrderBy adds an order clause. Mirrors Builder::orderBy().
func (b *Builder) OrderBy(column any, direction string) *Builder {
	if IsQueryable(column) {
		sql, bindings := b.createSub(column)
		column = Expression{Value: "(" + sql + ")"}

		if len(b.State().Unions) > 0 {
			_ = b.AddBinding(bindings, "unionOrder")
		} else {
			_ = b.AddBinding(bindings, "order")
		}
	}

	direction = strings.ToLower(direction)
	if direction != "asc" && direction != "desc" {
		b.setError(errOrderDirectionInvalid)
		return b
	}

	order := core.Order{Column: column, Direction: direction}
	if len(b.State().Unions) > 0 {
		b.State().UnionOrders = append(b.State().UnionOrders, order)
	} else {
		b.State().Orders = append(b.State().Orders, order)
	}

	return b
}

// OrderByDesc adds a descending order clause. Mirrors Builder::orderByDesc().
func (b *Builder) OrderByDesc(column any) *Builder { return b.OrderBy(column, "desc") }

// OrderByRaw adds a raw order clause. Mirrors Builder::orderByRaw().
func (b *Builder) OrderByRaw(sql string, bindings []any) *Builder {
	order := core.Order{Type: core.OrderRawType, SQL: sql}
	if len(b.State().Unions) > 0 {
		b.State().UnionOrders = append(b.State().UnionOrders, order)
		_ = b.AddBinding(bindings, "unionOrder")
	} else {
		b.State().Orders = append(b.State().Orders, order)
		_ = b.AddBinding(bindings, "order")
	}

	return b
}

// Latest orders by a column descending. Mirrors Builder::latest().
func (b *Builder) Latest(column string) *Builder {
	if column == "" {
		column = "created_at"
	}

	return b.OrderBy(column, "desc")
}

// Oldest orders by a column ascending. Mirrors Builder::oldest().
func (b *Builder) Oldest(column string) *Builder {
	if column == "" {
		column = "created_at"
	}

	return b.OrderBy(column, "asc")
}

// InRandomOrder orders randomly. Mirrors Builder::inRandomOrder().
func (b *Builder) InRandomOrder(seed any) *Builder {
	if b.grammar == nil {
		return b
	}

	return b.OrderByRaw(b.grammar.CompileRandom(seed), nil)
}

// Reorder clears ordering then optionally adds one. Mirrors Builder::reorder().
func (b *Builder) Reorder(column any, direction string) *Builder {
	b.State().Orders = nil
	b.State().UnionOrders = nil
	b.State().Bindings["order"] = []any{}
	b.State().Bindings["unionOrder"] = []any{}

	if column != nil {
		return b.OrderBy(column, direction)
	}

	return b
}

// RemoveExistingOrdersFor drops orders on one column. Mirrors Builder::removeExistingOrdersFor().
func (b *Builder) RemoveExistingOrdersFor(column string) []core.Order {
	kept := make([]core.Order, 0, len(b.State().Orders))
	for _, order := range b.State().Orders {
		if existing, ok := order.Column.(string); ok && existing == column {
			continue
		}
		kept = append(kept, order)
	}

	return kept
}

// Limit sets the limit, or the union limit while unions exist. Mirrors Builder::limit().
func (b *Builder) Limit(value int) *Builder {
	if value < 0 {
		return b
	}

	if len(b.State().Unions) > 0 {
		b.State().UnionLimit = &value
	} else {
		b.State().Limit = &value
	}

	return b
}

// Offset sets the offset, or the union offset while unions exist. Mirrors Builder::offset().
func (b *Builder) Offset(value int) *Builder {
	if value < 0 {
		value = 0
	}

	if len(b.State().Unions) > 0 {
		b.State().UnionOffset = &value
	} else {
		b.State().Offset = &value
	}

	return b
}

// Take is an alias for Limit. Mirrors Builder::take().
func (b *Builder) Take(value int) *Builder { return b.Limit(value) }

// Skip is an alias for Offset. Mirrors Builder::skip().
func (b *Builder) Skip(value int) *Builder { return b.Offset(value) }

// LimitValue reports the limit set on the builder, mirroring Builder::getLimit(). The bool is false
// when no limit was set, so callers can tell "unlimited" from a limit of zero.
func (b *Builder) LimitValue() (int, bool) {
	if b.State().Limit == nil {
		return 0, false
	}

	return *b.State().Limit, true
}

// OffsetValue reports the offset set on the builder, mirroring Builder::getOffset(). The bool is
// false when no offset was set.
func (b *Builder) OffsetValue() (int, bool) {
	if b.State().Offset == nil {
		return 0, false
	}

	return *b.State().Offset, true
}

// ForPage sets offset and limit for a page. Mirrors Builder::forPage(), whose signature is
// forPage($page, $perPage = 15): the 15 is the default applied only when the caller omits perPage,
// never a substitute for a zero that was passed. A perPage of 0 therefore keeps a limit of 0 and,
// multiplied into the offset, still yields an offset of 0. Laravel's body is
// offset(($page - 1) * $perPage)->limit($perPage) with no zero substitution; the variadic perPage
// stands in for the PHP parameter default.
func (b *Builder) ForPage(page int, perPage ...int) *Builder {
	size := 15
	if len(perPage) > 0 {
		size = perPage[0]
	}

	return b.Offset((page - 1) * size).Limit(size)
}

// ForPageBeforeId constrains to the page before an id. Mirrors Builder::forPageBeforeId(), whose
// signature is forPageBeforeId($perPage = 15, $lastId = 0, $column = 'id'). As with ForPage, the 15
// is a default for an omitted argument, never a substitute for a zero that was passed: Laravel's
// body is limit($perPage) with no zero substitution, so an explicit 0 keeps a limit of 0. Go cannot
// default a leading parameter, so callers that want the framework default pass 15 themselves.
func (b *Builder) ForPageBeforeId(perPage int, lastID any, column string) *Builder {
	if column == "" {
		column = "id"
	}

	b.State().Orders = b.RemoveExistingOrdersFor(column)

	if lastID == nil {
		b.WhereNotNull(column, core.BooleanAnd)
	} else {
		b.Where(column, "<", lastID)
	}

	return b.OrderBy(column, "desc").Limit(perPage)
}

// ForPageAfterId constrains to the page after an id. Mirrors Builder::forPageAfterId(), whose
// signature is forPageAfterId($perPage = 15, $lastId = 0, $column = 'id'). The 15 defaults an omitted
// argument only, never a passed zero; see ForPageBeforeId for why callers supply the default.
func (b *Builder) ForPageAfterId(perPage int, lastID any, column string) *Builder {
	if column == "" {
		column = "id"
	}

	b.State().Orders = b.RemoveExistingOrdersFor(column)

	if lastID == nil {
		b.WhereNotNull(column, core.BooleanAnd)
	} else {
		b.Where(column, ">", lastID)
	}

	return b.OrderBy(column, "asc").Limit(perPage)
}

// Union adds a union clause. Mirrors Builder::union().
func (b *Builder) Union(query any, all bool) *Builder {
	var union *Builder
	if callback, ok := query.(func(*Builder) *Builder); ok {
		union = b.NewQuery()
		callback(union)
	} else if existing, ok := query.(*Builder); ok {
		union = existing
	}

	b.State().Unions = append(b.State().Unions, core.Union{Query: union, All: all})

	if union != nil {
		_ = b.AddBinding(union.GetBindings(), "union")
	}

	return b
}

// UnionAll adds a union all clause. Mirrors Builder::unionAll().
func (b *Builder) UnionAll(query any) *Builder { return b.Union(query, true) }

// Lock sets the lock value. Mirrors Builder::lock().
func (b *Builder) Lock(value any) *Builder {
	b.State().Lock = value

	return b
}

// LockForUpdate locks for update. Mirrors Builder::lockForUpdate().
func (b *Builder) LockForUpdate() *Builder { return b.Lock(true) }

// SharedLock takes a shared lock. Mirrors Builder::sharedLock().
func (b *Builder) SharedLock() *Builder { return b.Lock(false) }

// aliased renders "table as alias" for strings and expressions.
func aliased(table any, as string) any {
	switch value := table.(type) {
	case string:
		return value + " as " + as
	case Expression:
		return Expression{Value: value.Value + " as " + as}
	default:
		return table
	}
}

// joinTypeModifiers are the words that may precede the JOIN keyword in a raw join clause. They are
// folded into the clause's type, which compileJoins emits as "<type> join <table>".
var joinTypeModifiers = map[string]bool{
	"inner": true, "left": true, "right": true, "full": true, "cross": true,
	"outer": true, "natural": true,
}

// JoinRaw adds a join whose target and condition are a raw expression. This is the Go form of the
// single string Laravel accepts when a join is passed as one argument, and it is what the existing
// database/db Join(query string, args ...any) maps onto.
//
// The expression may be either a bare "<table> on <condition>" fragment, which is rendered with the
// caller's type ("inner join posts on ..."), or a whole join clause that carries its own keyword
// ("left join addresses ua on ..."), the string form GORM's Joins accepted. In the second case the
// leading keyword becomes the clause's type, so the default is not prepended on top of it — the
// defect that rendered "inner join left join addresses ua on ...".
func (b *Builder) JoinRaw(expression string, bindings []any, typ string) *Builder {
	if clauseType, rest, ok := splitJoinClause(expression); ok {
		typ, expression = clauseType, rest
	}

	if typ == "" {
		typ = "inner"
	}

	join := NewJoinClause(b, typ, Expression{Value: expression})
	_ = join.AddBinding(bindings, "join")

	b.State().Joins = append(b.State().Joins, join)
	_ = b.AddBinding(join.GetBindings(), "join")

	return b
}

// splitJoinClause splits an optional leading join-type keyword off a raw join expression. ok is
// false when the expression does not open with a join keyword, which leaves the caller's type in
// force. When ok is true, typ is the modifier chain without the trailing "join" ("left",
// "left outer", or "" for a bare "join", which the caller defaults to inner) and rest is the
// expression with the keyword and the whitespace that follows it removed.
func splitJoinClause(expression string) (typ string, rest string, ok bool) {
	trimmed := strings.TrimLeft(expression, " \t\r\n")
	fields := strings.Fields(trimmed)

	i := 0
	for i < len(fields) && joinTypeModifiers[strings.ToLower(fields[i])] {
		i++
	}

	if i >= len(fields) || !strings.EqualFold(fields[i], "join") {
		return "", expression, false
	}

	typ = strings.ToLower(strings.Join(fields[:i], " "))

	// The modifiers never contain the "join" substring, so the first occurrence marks the keyword.
	lower := strings.ToLower(trimmed)
	rest = strings.TrimSpace(trimmed[strings.Index(lower, "join")+len("join"):])

	return typ, rest, true
}

// TableSource returns the table the query targets: a string, an Expression, or nil when unset.
// database/db uses it to preserve the "table is required" error and to inspect the from clause.
func (b *Builder) TableSource() any { return b.State().From }

// InsertUsing mirrors Builder::insertUsing(): it applies the queued before-query callbacks at its
// head — exactly as Laravel does — then resolves the subquery into a statement. createSub's argument
// guard rejects anything that is not a builder, a callback or a string, and the descriptor
// ToInsertUsingSQL compiles is recorded on the builder. Laravel runs the compiled statement against
// its connection; the builder here holds none, so the statement is left for the caller to execute.
func (b *Builder) InsertUsing(columns []any, query any) *Builder {
	b.ApplyBeforeQueryCallbacks()

	errBefore := b.err
	sql, bindings := b.createSub(query)
	if b.err != errBefore {
		// createSub rejected the subquery; Laravel throws before the grammar is reached.
		return b
	}

	b.insertUsingSet = true
	b.insertUsingColumns = columns
	b.insertUsingSQL = sql
	b.insertUsingBindings = CleanBindings(bindings)

	return b
}

// InsertOrIgnoreUsing mirrors Builder::insertOrIgnoreUsing(): it resolves the subquery into a
// statement — createSub's argument guard rejects anything that is not a builder, a callback or a
// string — and records the descriptor ToInsertOrIgnoreUsingSQL compiles. Laravel runs the compiled
// statement against its connection; the builder here holds none, so the statement is left on the
// builder for the caller to execute.
func (b *Builder) InsertOrIgnoreUsing(columns []any, query any) *Builder {
	errBefore := b.err
	sql, bindings := b.createSub(query)
	if b.err != errBefore {
		// createSub rejected the subquery; Laravel throws before the grammar is reached.
		return b
	}

	b.insertUsingSet = true
	b.insertUsingColumns = columns
	b.insertUsingSQL = sql
	b.insertUsingBindings = CleanBindings(bindings)

	return b
}

// IncrementEach mirrors Builder::incrementEach(): every column's amount must be numeric and every key a
// column name, and the column is then rewritten to the raw expression `<wrapped column> + <amount>` and
// merged with extra. The merged assignments become the ordered update descriptor the caller compiles
// with ToUpdateSQL. Laravel hands the array to update(), which executes against its connection; the
// builder here holds none, so the descriptor is left on the builder. A Go slice stands for PHP's
// non-associative array, which Laravel rejects because its keys are integer indices, not column names.
func (b *Builder) IncrementEach(columns any, extra ...core.UpdateValue) *Builder {
	entries, ok := b.incrementEntries(columns)
	if !ok {
		return b
	}

	// Rewrite into a fresh slice: an UpdateValues argument shares its backing array with the caller.
	rewritten := make(core.UpdateValues, len(entries))
	for i := range entries {
		rewritten[i] = core.UpdateValue{
			Column: entries[i].Column,
			Value:  Raw(b.grammar.Wrap(entries[i].Column) + " + " + amountString(entries[i].Value)),
		}
	}

	b.updateValues = mergeUpdateValues(rewritten, extra)

	return b
}

// incrementEntries normalises IncrementEach's columns argument into the ordered column/amount pairs it
// validates. A false ok means a failure was recorded on the builder and the caller must stop. A
// map[string]any and an UpdateValues are associative; any other list is the non-associative array
// Laravel rejects.
func (b *Builder) incrementEntries(columns any) (core.UpdateValues, bool) {
	switch value := columns.(type) {
	case core.UpdateValues:
		return b.validateIncrementEntries(value)
	case []core.UpdateValue:
		return b.validateIncrementEntries(core.UpdateValues(value))
	default:
		if entries, ok := mapEntries(value); ok {
			return b.validateIncrementEntries(entries)
		}

		list, isList := arrayValues(columns)
		if !isList {
			b.setError(errIncrementEachNonAssociative)

			return nil, false
		}

		// A list's keys are integer indices, so Laravel's per-entry guards — the amount first, then the
		// key — decide the failure on the first entry: a non-numeric amount is named by its index, and a
		// numeric one falls to the non-associative guard. An empty list has no entries, so it passes.
		if len(list) > 0 {
			if !isNumericValue(list[0]) {
				b.setError(errIncrementAmountNonNumeric.Args(0))
			} else {
				b.setError(errIncrementEachNonAssociative)
			}

			return nil, false
		}

		return core.UpdateValues{}, true
	}
}

// validateIncrementEntries rejects a non-numeric amount before a non-string column, exactly as
// Builder::incrementEach() orders the two guards, and is otherwise an identity.
func (b *Builder) validateIncrementEntries(entries core.UpdateValues) (core.UpdateValues, bool) {
	for i := range entries {
		if !isNumericValue(entries[i].Value) {
			b.setError(errIncrementAmountNonNumeric.Args(entries[i].Column))

			return nil, false
		}
	}

	return entries, true
}
