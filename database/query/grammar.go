package query

// Grammar compiles builder state into SQL. It is the Go counterpart of
// Illuminate\Database\Query\Grammars\Grammar composed with Illuminate\Database\Grammar.
//
// Every compile and wrap method is on the interface because Laravel's dialect grammars override
// them individually and Go embedding does not dispatch virtually. BaseGrammar holds a self
// reference so its shared implementations route through the concrete dialect.
type Grammar interface {
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
	CompileSelect(b *Builder) string
	CompileComponents(b *Builder) []string
	CompileAggregate(b *Builder, aggregate Aggregate) string
	CompileColumns(b *Builder, columns []any) string
	CompileFrom(b *Builder, table any) string
	CompileIndexHint(b *Builder, hint *IndexHint) string
	CompileJoins(b *Builder, joins []*JoinClause) string
	CompileWheres(b *Builder) string
	CompileGroups(b *Builder, groups []any) string
	CompileHavings(b *Builder) string
	CompileHaving(b *Builder, having Having) string
	CompileOrders(b *Builder, orders []Order) string
	CompileLimit(b *Builder, limit *int) string
	CompileOffset(b *Builder, offset *int) string
	CompileLock(b *Builder, value any) string
	CompileRandom(seed any) string
	CompileRowNumber(partition string, orders string) string
	CompileGroupLimit(b *Builder) string
	CompileLegacyGroupLimit(b *Builder) string
	UseLegacyGroupLimit(b *Builder) bool
	CompileInsertOrIgnoreUsing(b *Builder, columns []any, sql string) string
	CompileJoinLateral(join *JoinClause, expression string) string
	CompileJsonValueCast(value string) string
	CompileUnions(b *Builder) string
	CompileUnionAggregate(b *Builder) string
	WrapUnion(sql string) string
	CompileExists(b *Builder) string

	// --- write ---
	CompileInsert(b *Builder, values []map[string]any) string
	CompileInsertUsing(b *Builder, columns []any, sql string) string
	CompileInsertGetId(b *Builder, values []map[string]any, sequence string) string
	CompileInsertOrIgnore(b *Builder, values []map[string]any) string
	CompileUpdate(b *Builder, values UpdateValues) string
	CompileUpdateWithJoins(b *Builder, table string, columns string, where string) string
	CompileUpdateWithoutJoins(b *Builder, table string, columns string, where string) string
	CompileUpdateColumns(b *Builder, values UpdateValues) string
	CompileUpsert(b *Builder, values []map[string]any, uniqueBy []string, update []UpsertUpdate) string
	CompileDelete(b *Builder) string
	CompileDeleteWithJoins(b *Builder, table string, where string) string
	CompileDeleteWithoutJoins(b *Builder, table string, where string) string
	CompileTruncate(b *Builder) []Statement

	// --- JSON, dialect specific ---
	CompileJsonContains(column string, value string) string
	CompileJsonContainsKey(column string) string
	CompileJsonLength(column string, operator string, value string) string
	CompileJsonOverlaps(column string, value string) string
	CompileJsonUpdateColumn(key string, value any) string

	// --- where dispatch and compilers ---
	CompileWhere(b *Builder, where Where) string
	WhereRaw(b *Builder, where Where) string
	WhereBasic(b *Builder, where Where) string
	WhereBitwise(b *Builder, where Where) string
	WhereLike(b *Builder, where Where) string
	WhereIn(b *Builder, where Where) string
	WhereNotIn(b *Builder, where Where) string
	WhereInRaw(b *Builder, where Where) string
	WhereNotInRaw(b *Builder, where Where) string
	WhereNull(b *Builder, where Where) string
	WhereNotNull(b *Builder, where Where) string
	WhereBetween(b *Builder, where Where) string
	WhereBetweenColumns(b *Builder, where Where) string
	WhereValueBetween(b *Builder, where Where) string
	WhereDate(b *Builder, where Where) string
	WhereTime(b *Builder, where Where) string
	WhereDay(b *Builder, where Where) string
	WhereMonth(b *Builder, where Where) string
	WhereYear(b *Builder, where Where) string
	WhereColumn(b *Builder, where Where) string
	WhereNested(b *Builder, where Where) string
	WhereSub(b *Builder, where Where) string
	WhereExists(b *Builder, where Where) string
	WhereNotExists(b *Builder, where Where) string
	WhereRowValues(b *Builder, where Where) string
	WhereJsonBoolean(b *Builder, where Where) string
	WhereJsonContains(b *Builder, where Where) string
	WhereJsonOverlaps(b *Builder, where Where) string
	WhereJsonContainsKey(b *Builder, where Where) string
	WhereJsonLength(b *Builder, where Where) string
	WhereFullText(b *Builder, where Where) string
	WhereExpression(b *Builder, where Where) string
}
