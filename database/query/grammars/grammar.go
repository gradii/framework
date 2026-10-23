package grammars

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
	GetValue(expression Expression) string
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
	PrepareBindingsForUpdate(bindings map[string][]any, values UpdateValues) []any
	PrepareBindingsForDelete(bindings map[string][]any) []any

	// --- select ---
	SelectComponents() []string
	CompileSelect(b Query) string
	CompileComponents(b Query) []string
	CompileAggregate(b Query, aggregate Aggregate) string
	CompileColumns(b Query, columns []any) string
	CompileFrom(b Query, table any) string
	CompileIndexHint(b Query, hint *IndexHint) string
	CompileJoins(b Query, joins []Join) string
	CompileWheres(b Query) string
	CompileGroups(b Query, groups []any) string
	CompileHavings(b Query) string
	CompileHaving(b Query, having Having) string
	CompileOrders(b Query, orders []Order) string
	CompileLimit(b Query, limit *int) string
	CompileOffset(b Query, offset *int) string
	CompileLock(b Query, value any) string
	CompileRandom(seed any) string
	CompileRowNumber(partition string, orders string) string
	CompileGroupLimit(b Query) string
	CompileLegacyGroupLimit(b Query) string
	UseLegacyGroupLimit(b Query) bool
	CompileInsertOrIgnoreUsing(b Query, columns []any, sql string) string
	CompileJoinLateral(join Join, expression string) string
	CompileJsonValueCast(value string) string
	CompileUnions(b Query) string
	CompileUnionAggregate(b Query) string
	WrapUnion(sql string) string
	CompileExists(b Query) string

	// --- write ---
	CompileInsert(b Query, values []map[string]any) string
	CompileInsertUsing(b Query, columns []any, sql string) string
	CompileInsertGetId(b Query, values []map[string]any, sequence string) string
	CompileInsertOrIgnore(b Query, values []map[string]any) string
	CompileUpdate(b Query, values UpdateValues) string
	CompileUpdateWithJoins(b Query, table string, columns string, where string) string
	CompileUpdateWithoutJoins(b Query, table string, columns string, where string) string
	CompileUpdateColumns(b Query, values UpdateValues) string
	CompileUpsert(b Query, values []map[string]any, uniqueBy []string, update []UpsertUpdate) string
	CompileDelete(b Query) string
	CompileDeleteWithJoins(b Query, table string, where string) string
	CompileDeleteWithoutJoins(b Query, table string, where string) string
	CompileTruncate(b Query) []Statement

	// --- JSON, dialect specific ---
	CompileJsonContains(column string, value string) string
	CompileJsonContainsKey(column string) string
	CompileJsonLength(column string, operator string, value string) string
	CompileJsonOverlaps(column string, value string) string
	CompileJsonUpdateColumn(key string, value any) string

	// --- where dispatch and compilers ---
	CompileWhere(b Query, where Where) string
	WhereRaw(b Query, where Where) string
	WhereBasic(b Query, where Where) string
	WhereBitwise(b Query, where Where) string
	WhereLike(b Query, where Where) string
	WhereIn(b Query, where Where) string
	WhereNotIn(b Query, where Where) string
	WhereInRaw(b Query, where Where) string
	WhereNotInRaw(b Query, where Where) string
	WhereNull(b Query, where Where) string
	WhereNotNull(b Query, where Where) string
	WhereBetween(b Query, where Where) string
	WhereBetweenColumns(b Query, where Where) string
	WhereValueBetween(b Query, where Where) string
	WhereDate(b Query, where Where) string
	WhereTime(b Query, where Where) string
	WhereDay(b Query, where Where) string
	WhereMonth(b Query, where Where) string
	WhereYear(b Query, where Where) string
	WhereColumn(b Query, where Where) string
	WhereNested(b Query, where Where) string
	WhereSub(b Query, where Where) string
	WhereExists(b Query, where Where) string
	WhereNotExists(b Query, where Where) string
	WhereRowValues(b Query, where Where) string
	WhereJsonBoolean(b Query, where Where) string
	WhereJsonContains(b Query, where Where) string
	WhereJsonOverlaps(b Query, where Where) string
	WhereJsonContainsKey(b Query, where Where) string
	WhereJsonLength(b Query, where Where) string
	WhereFullText(b Query, where Where) string
	WhereExpression(b Query, where Where) string
}
