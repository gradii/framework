package query

import "github.com/gradii/framework/database/query/core"

// Grammar compiles builder state into SQL. It is the Go counterpart of
// Illuminate\Database\Query\Grammars\Grammar composed with Illuminate\Database\Grammar.
//
// Every compile and wrap method is on the interface because Laravel's dialect grammars override
// them individually and Go embedding does not dispatch virtually. BaseGrammar holds a self
// reference so its shared implementations route through the concrete dialect.
type Grammar interface {
	SetEscaper(escape func(any, bool) string)
	SubstituteBindingsIntoRawSQL(sql string, bindings []any) string
	// --- wrapping and parameters (Illuminate\Database\Grammar) ---
	Wrap(value any) string
	WrapTable(table any) string
	GetValue(expression core.Expression) string
	Parameter(value any) string
	Parameterize(values []any) string
	Columnize(columns []any) string
	WrapValue(value string) string
	GetOperators() []string
	GetBitwiseOperators() []string
	GetDateFormat() string
	IsJsonSelector(value string) bool
	WrapJsonSelector(value string) string
	WrapJsonBooleanSelector(value string) string
	WrapJsonBooleanValue(value string) string
	PrepareBindingForJsonContains(value any) any
	PrepareWhereLikeBinding(value string, caseSensitive bool) string
	PrepareBindingsForUpdate(bindings map[string][]any, values core.UpdateValues) []any
	PrepareBindingsForDelete(bindings map[string][]any) []any

	// --- select ---
	SelectComponents() []string
	CompileSelect(b core.Query) string
	CompileComponents(b core.Query) []string
	CompileAggregate(b core.Query, aggregate core.Aggregate) string
	CompileColumns(b core.Query, columns []any) string
	CompileFrom(b core.Query, table any) string
	CompileIndexHint(b core.Query, hint *core.IndexHint) string
	CompileJoins(b core.Query, joins []core.Join) string
	CompileWheres(b core.Query) string
	CompileGroups(b core.Query, groups []any) string
	CompileHavings(b core.Query) string
	CompileHaving(b core.Query, having core.Having) string
	CompileOrders(b core.Query, orders []core.Order) string
	CompileLimit(b core.Query, limit *int) string
	CompileOffset(b core.Query, offset *int) string
	CompileLock(b core.Query, value any) string
	CompileRandom(seed any) string
	CompileRowNumber(partition string, orders string) string
	CompileGroupLimit(b core.Query) string
	CompileLegacyGroupLimit(b core.Query) string
	UseLegacyGroupLimit(b core.Query) bool
	CompileInsertOrIgnoreUsing(b core.Query, columns []any, sql string) string
	CompileJoinLateral(join core.Join, expression string) string
	CompileJsonValueCast(value string) string
	CompileUnions(b core.Query) string
	CompileUnionAggregate(b core.Query) string
	WrapUnion(sql string) string
	CompileExists(b core.Query) string

	// --- write ---
	CompileInsert(b core.Query, values []map[string]any) string
	CompileInsertUsing(b core.Query, columns []any, sql string) string
	CompileInsertGetId(b core.Query, values []map[string]any, sequence string) string
	CompileInsertOrIgnore(b core.Query, values []map[string]any) string
	CompileUpdate(b core.Query, values core.UpdateValues) string
	CompileUpdateWithJoins(b core.Query, table string, columns string, where string) string
	CompileUpdateWithoutJoins(b core.Query, table string, columns string, where string) string
	CompileUpdateColumns(b core.Query, values core.UpdateValues) string
	CompileUpsert(b core.Query, values []map[string]any, uniqueBy []string, update []core.UpsertUpdate) string
	CompileDelete(b core.Query) string
	CompileDeleteWithJoins(b core.Query, table string, where string) string
	CompileDeleteWithoutJoins(b core.Query, table string, where string) string
	CompileTruncate(b core.Query) []core.Statement

	// --- JSON, dialect specific ---
	CompileJsonContains(column string, value string) string
	CompileJsonContainsKey(column string) string
	CompileJsonLength(column string, operator string, value string) string
	CompileJsonOverlaps(column string, value string) string
	CompileJsonUpdateColumn(key string, value any) string

	// --- where dispatch and compilers ---
	CompileWhere(b core.Query, where core.Where) string
	WhereRaw(b core.Query, where core.Where) string
	WhereBasic(b core.Query, where core.Where) string
	WhereBitwise(b core.Query, where core.Where) string
	WhereLike(b core.Query, where core.Where) string
	WhereIn(b core.Query, where core.Where) string
	WhereNotIn(b core.Query, where core.Where) string
	WhereInRaw(b core.Query, where core.Where) string
	WhereNotInRaw(b core.Query, where core.Where) string
	WhereNull(b core.Query, where core.Where) string
	WhereNotNull(b core.Query, where core.Where) string
	WhereBetween(b core.Query, where core.Where) string
	WhereBetweenColumns(b core.Query, where core.Where) string
	WhereValueBetween(b core.Query, where core.Where) string
	WhereDate(b core.Query, where core.Where) string
	WhereTime(b core.Query, where core.Where) string
	WhereDay(b core.Query, where core.Where) string
	WhereMonth(b core.Query, where core.Where) string
	WhereYear(b core.Query, where core.Where) string
	WhereColumn(b core.Query, where core.Where) string
	WhereNested(b core.Query, where core.Where) string
	WhereSub(b core.Query, where core.Where) string
	WhereExists(b core.Query, where core.Where) string
	WhereNotExists(b core.Query, where core.Where) string
	WhereRowValues(b core.Query, where core.Where) string
	WhereJsonBoolean(b core.Query, where core.Where) string
	WhereJsonContains(b core.Query, where core.Where) string
	WhereJsonOverlaps(b core.Query, where core.Where) string
	WhereJsonContainsKey(b core.Query, where core.Where) string
	WhereJsonLength(b core.Query, where core.Where) string
	WhereFullText(b core.Query, where core.Where) string
	WhereExpression(b core.Query, where core.Where) string
}
