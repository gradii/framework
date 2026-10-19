package query

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var (
	jsonPathArrayKeys = regexp.MustCompile(`(\[[^\]]+\])+$`)
	jsonPathKey       = regexp.MustCompile(`\[([^\]]+)\]`)
)

// PostgresGrammar mirrors Illuminate\Database\Query\Grammars\PostgresGrammar. Only the methods
// whose SQL differs from the base are declared here.
type PostgresGrammar struct {
	*BaseGrammar
}

// NewPostgresGrammar creates the Postgres grammar.
func NewPostgresGrammar(prefix string) *PostgresGrammar {
	g := &PostgresGrammar{BaseGrammar: NewBaseGrammar(prefix)}
	g.BaseGrammar.Attach(g)

	return g
}

// postgresOperators mirrors PostgresGrammar::$operators: the clause operators this dialect accepts.
// It carries the ?-family and the Postgres-specific comparisons that the base Builder list lacks.
var postgresOperators = []string{
	"=", "<", ">", "<=", ">=", "<>", "!=",
	"like", "not like", "between", "ilike", "not ilike",
	"~", "&", "|", "#", "<<", ">>", "<<=", ">>=",
	"&&", "@>", "<@", "?", "?|", "?&", "||", "-", "@?", "@@", "#-",
	"is distinct from", "is not distinct from",
}

// GetOperators mirrors PostgresGrammar::getOperators(), which merges the dialect's own $operators
// with any custom operators. We keep no custom-operator registry, so it returns the dialect list;
// without it a ? or # operator is treated as the "=" shorthand and shifted into the value slot.
func (g *PostgresGrammar) GetOperators() []string {
	return append([]string{}, postgresOperators...)
}

// GetBitwiseOperators mirrors PostgresGrammar::$bitwiseOperators, which adds "#" and the shift
// spellings to the base Builder set so those clauses compile through whereBitwise().
func (g *PostgresGrammar) GetBitwiseOperators() []string {
	return []string{"~", "&", "|", "#", "<<", ">>", "<<=", ">>="}
}

// WhereBasic mirrors PostgresGrammar::whereBasic(): a like comparison casts the column to text.
func (g *PostgresGrammar) WhereBasic(b *Builder, where Where) string {
	if strings.Contains(strings.ToLower(where.Operator), "like") {
		return g.Wrap(where.Column) + "::text " + where.Operator + " " + g.Parameter(where.Value)
	}

	return g.BaseGrammar.WhereBasic(b, where)
}

// WhereDate mirrors PostgresGrammar::whereDate().
func (g *PostgresGrammar) WhereDate(b *Builder, where Where) string {
	column := g.Wrap(where.Column)
	if g.IsJsonSelector(asString(where.Column)) {
		column = "(" + column + ")"
	}

	return column + "::date " + where.Operator + " " + g.Parameter(where.Value)
}

// dateBasedWhere mirrors PostgresGrammar::dateBasedWhere().
func (g *PostgresGrammar) dateBasedWhere(typ string, where Where) string {
	return "extract(" + typ + " from " + g.Wrap(where.Column) + ") " + where.Operator + " " + g.Parameter(where.Value)
}

// WhereTime mirrors PostgresGrammar::whereTime(): the column is cast to time rather than read out
// with extract(), and a JSON selector is parenthesised before the cast the way the date clause is.
func (g *PostgresGrammar) WhereTime(b *Builder, where Where) string {
	column := g.Wrap(where.Column)
	if g.IsJsonSelector(asString(where.Column)) {
		column = "(" + column + ")"
	}

	return column + "::time " + where.Operator + " " + g.Parameter(where.Value)
}

// WhereDay mirrors PostgresGrammar::whereDay().
func (g *PostgresGrammar) WhereDay(b *Builder, where Where) string {
	return g.dateBasedWhere("day", where)
}

// WhereMonth mirrors PostgresGrammar::whereMonth().
func (g *PostgresGrammar) WhereMonth(b *Builder, where Where) string {
	return g.dateBasedWhere("month", where)
}

// WhereYear mirrors PostgresGrammar::whereYear().
func (g *PostgresGrammar) WhereYear(b *Builder, where Where) string {
	return g.dateBasedWhere("year", where)
}

// CompileColumns mirrors PostgresGrammar::compileColumns(), which supports distinct on.
func (g *PostgresGrammar) CompileColumns(b *Builder, columns []any) string {
	if b.aggregate != nil {
		return ""
	}

	switch {
	case len(b.distinctColumns) > 0:
		return "select distinct on (" + g.Columnize(b.distinctColumns) + ") " + g.Columnize(columns)
	case b.distinct:
		return "select distinct " + g.Columnize(columns)
	default:
		return "select " + g.Columnize(columns)
	}
}

// CompileInsertGetId mirrors PostgresGrammar::compileInsertGetId(): insert then returning.
func (g *PostgresGrammar) CompileInsertGetId(b *Builder, values []map[string]any, sequence string) string {
	if sequence == "" {
		sequence = "id"
	}

	return g.CompileInsert(b, values) + " returning " + g.Wrap(sequence)
}

// CompileUpsert mirrors PostgresGrammar::compileUpsert(): on conflict do update.
func (g *PostgresGrammar) CompileUpsert(b *Builder, values []map[string]any, uniqueBy []string, update []UpsertUpdate) string {
	sql := g.CompileInsert(b, values)
	sql += " on conflict (" + g.Columnize(anySlice(uniqueBy)) + ") do update set "

	parts := make([]string, 0, len(update))
	for _, item := range update {
		if item.Bound {
			parts = append(parts, g.Wrap(item.Column)+" = "+g.Parameter(item.Value))
			continue
		}
		parts = append(parts, g.Wrap(item.Column)+" = "+g.WrapValue("excluded")+"."+g.Wrap(item.Column))
	}

	return sql + strings.Join(parts, ", ")
}

// postgresCascadeTruncate mirrors PostgresGrammar::$cascadeTruncate. Laravel's flag is a static
// property, so it is a package-level toggle here rather than a grammar field: one value governs every
// PostgresGrammar in the process, exactly as the static does.
var postgresCascadeTruncate = true

// CascadeOnTruncate mirrors PostgresGrammar::cascadeOnTruncate(): it turns the "cascade" keyword a
// truncate statement appends on or off. The default is on, matching Laravel. The flag is process-wide,
// so a caller that flips it should restore the previous value.
func CascadeOnTruncate(value bool) {
	postgresCascadeTruncate = value
}

// CompileTruncate mirrors PostgresGrammar::compileTruncate(): restart identity, then the cascade
// keyword unless CascadeOnTruncate(false) disabled it.
func (g *PostgresGrammar) CompileTruncate(b *Builder) []Statement {
	sql := "truncate " + g.WrapTable(b.from) + " restart identity"
	if postgresCascadeTruncate {
		sql += " cascade"
	}

	return []Statement{{SQL: sql}}
}

// CompileLock mirrors PostgresGrammar::compileLock().
func (g *PostgresGrammar) CompileLock(b *Builder, value any) string {
	if s, ok := value.(string); ok {
		return s
	}

	// Postgres spells a shared lock "for share" rather than MySQL's "lock in share mode".
	if locked, ok := value.(bool); ok && !locked {
		return "for share"
	}

	return "for update"
}

// CompileInsertOrIgnore mirrors PostgresGrammar::compileInsertOrIgnore(): the conflict clause is what
// ignores the row, so the statement is an ordinary insert with one appended.
func (g *PostgresGrammar) CompileInsertOrIgnore(b *Builder, values []map[string]any) string {
	return g.CompileInsert(b, values) + " on conflict do nothing"
}

// CompileInsertOrIgnoreUsing mirrors PostgresGrammar::compileInsertOrIgnoreUsing().
func (g *PostgresGrammar) CompileInsertOrIgnoreUsing(b *Builder, columns []any, sql string) string {
	return g.CompileInsertUsing(b, columns, sql) + " on conflict do nothing"
}

// PrepareBindingsForUpdate mirrors PostgresGrammar::prepareBindingsForUpdate(): array values and
// non-expression JSON selectors are encoded before binding. The assignments are grouped first so the
// bindings of a column's several JSON selectors travel together in the order the merged jsonb_set
// chain consumes them, keeping the placeholders aligned with the bindings exactly as
// CompileUpdateColumns emits them.
func (g *PostgresGrammar) PrepareBindingsForUpdate(bindings map[string][]any, values UpdateValues) []any {
	merged := g.groupJsonColumnsForUpdate(values)
	out := make([]any, 0, len(merged))

	for _, value := range merged {
		out = g.appendUpdateBindings(out, value)
	}

	for _, typ := range BindingTypes {
		if typ == "select" {
			continue
		}
		out = append(out, bindings[typ]...)
	}

	return out
}

// appendUpdateBindings appends one (possibly merged) assignment's binding contribution. A JSON group
// contributes one binding per selector, in path order, each prepared by the rule a single selector's
// value takes.
func (g *PostgresGrammar) appendUpdateBindings(out []any, value UpdateValue) []any {
	if group, ok := value.Value.(postgresJsonUpdate); ok {
		for _, selector := range group.values {
			out = g.appendUpdateBinding(out, value.Column, selector)
		}

		return out
	}

	return g.appendUpdateBinding(out, value.Column, value.Value)
}

// appendUpdateBinding applies the per-value rule: an array value and a non-expression JSON-selector
// value are json-encoded; anything else is appended as-is, so a sub-select splices in its own bindings.
func (g *PostgresGrammar) appendUpdateBinding(out []any, column string, value any) []any {
	if isArrayValue(value) || (g.IsJsonSelector(column) && !IsExpression(value)) {
		return append(out, encodeJSON(value))
	}

	return g.AppendUpdateValueBinding(out, value)
}

// WrapJsonSelector mirrors PostgresGrammar::wrapJsonSelector(): native -> and ->> operators.
func (g *PostgresGrammar) WrapJsonSelector(value string) string {
	segments := strings.Split(value, "->")
	field := g.wrap.Column(segments[0])

	attributes := g.WrapJsonPathAttributes(segments[1:], "'")
	if len(attributes) == 0 {
		return field
	}

	last := attributes[len(attributes)-1]
	rest := attributes[:len(attributes)-1]

	if len(rest) > 0 {
		return field + "->" + strings.Join(rest, "->") + "->>" + last
	}

	return field + "->>" + last
}

// WrapJsonPathAttributes mirrors PostgresGrammar::wrapJsonPathAttributes().
func (g *PostgresGrammar) WrapJsonPathAttributes(path []string, quote string) []string {
	var out []string

	for _, attribute := range path {
		for _, key := range g.ParseJsonPathArrayKeys(attribute) {
			if _, err := strconv.Atoi(key); err == nil {
				out = append(out, key)
				continue
			}
			out = append(out, quote+key+quote)
		}
	}

	return out
}

// ParseJsonPathArrayKeys mirrors PostgresGrammar::parseJsonPathArrayKeys().
func (g *PostgresGrammar) ParseJsonPathArrayKeys(attribute string) []string {
	parts := jsonPathArrayKeys.FindString(attribute)
	if parts == "" {
		return []string{attribute}
	}

	out := make([]string, 0, 2)
	if key := strings.TrimSuffix(attribute, parts); key != "" {
		out = append(out, key)
	}

	for _, match := range jsonPathKey.FindAllStringSubmatch(parts, -1) {
		if len(match) > 1 && match[1] != "" {
			out = append(out, match[1])
		}
	}

	return out
}

// CompileJsonContains mirrors PostgresGrammar::compileJsonContains().
func (g *PostgresGrammar) CompileJsonContains(column string, value string) string {
	wrapped := strings.ReplaceAll(g.Wrap(column), "->>", "->")

	return "(" + wrapped + ")::jsonb @> " + value
}

// CompileJsonLength mirrors PostgresGrammar::compileJsonLength().
func (g *PostgresGrammar) CompileJsonLength(column string, operator string, value string) string {
	wrapped := strings.ReplaceAll(g.Wrap(column), "->>", "->")

	return "jsonb_array_length((" + wrapped + ")::jsonb) " + operator + " " + value
}

// CompileJsonContainsKey mirrors PostgresGrammar::compileJsonContainsKey().
func (g *PostgresGrammar) CompileJsonContainsKey(column string) string {
	segments := strings.Split(column, "->")
	last := segments[len(segments)-1]
	segments = segments[:len(segments)-1]

	index := ""
	if _, err := strconv.Atoi(last); err == nil {
		index = last
	} else if match := regexp.MustCompile(`\[(-?[0-9]+)\]$`).FindStringSubmatch(last); match != nil {
		segments = append(segments, strings.TrimSuffix(last, match[0]))
		index = match[1]
	}

	wrapped := strings.ReplaceAll(g.Wrap(strings.Join(segments, "->")), "->>", "->")

	if index != "" {
		n, _ := strconv.Atoi(index)
		if n < 0 {
			n = -n
		} else {
			n++
		}
		return "case when jsonb_typeof((" + wrapped + ")::jsonb) = 'array' then jsonb_array_length((" +
			wrapped + ")::jsonb) >= " + strconv.Itoa(n) + " else false end"
	}

	return "coalesce((" + wrapped + ")::jsonb ?? '" + strings.ReplaceAll(last, "'", "''") + "', false)"
}

// CompileJsonOverlaps mirrors PostgresGrammar::compileJsonOverlaps().
func (g *PostgresGrammar) CompileJsonOverlaps(column string, value string) string {
	wrapped := strings.ReplaceAll(g.Wrap(column), "->>", "->")

	return "(" + wrapped + ")::jsonb ?| array[" + value + "]"
}

// CompileJsonUpdateColumn mirrors PostgresGrammar::compileJsonUpdateColumn().
func (g *PostgresGrammar) CompileJsonUpdateColumn(key string, value any) string {
	segments := strings.Split(key, "->")
	field := g.Wrap(segments[0])
	path := "'{" + strings.Join(g.WrapJsonPathAttributes(segments[1:], `"`), ",") + "}'"

	return field + " = jsonb_set(" + field + "::jsonb, " + path + ", " + g.Parameter(value) + ")"
}

// CompileRandom mirrors PostgresGrammar, which keeps the base RANDOM().
func (g *PostgresGrammar) CompileRandom(seed any) string { return "RANDOM()" }

// encodeJSON renders a value as JSON, mirroring PHP's json_encode for the shapes an update value can
// take: strings are quoted, booleans and null keep their spelling, and slices and maps recurse. It is
// what the grammars bind for a json-encoded update value: SQLite and Postgres array values, and every
// non-expression Postgres json-selector value, scalar or not.
func encodeJSON(value any) any {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return jsonString(v)
	case []byte:
		return jsonString(string(v))
	case []any:
		return encodeJSONArray(v)
	case map[string]any:
		return encodeJSONObject(v)
	}

	// Dereference pointers one level so a value carried as *float64/*string/... is encoded as the
	// scalar it points at, not as its Go address. PHP has no such pointer layer: json_encode() of
	// the value a pointer stands for is the faithful rendering.
	if rv := reflect.ValueOf(value); rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "null"
		}
		return encodeJSON(rv.Elem().Interface())
	}

	if isArrayValue(value) {
		return encodeJSONArray(toAnySlice(value))
	}

	return asString(value)
}

// encodeJSONArray renders a slice as a JSON array.
func encodeJSONArray(values []any) string {
	parts := make([]string, len(values))
	for i, item := range values {
		parts[i] = asString(encodeJSON(item))
	}

	return "[" + strings.Join(parts, ",") + "]"
}

// encodeJSONObject renders a map as a JSON object with its keys sorted, the order a Go map can give
// deterministically.
func encodeJSONObject(values map[string]any) string {
	keys := sortedKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, jsonString(key)+":"+asString(encodeJSON(values[key])))
	}

	return "{" + strings.Join(parts, ",") + "}"
}

// jsonString quotes a string for JSON, escaping backslashes and double quotes.
func jsonString(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

// validFullTextLanguages mirrors PostgresGrammar::validFullTextLanguages().
var validFullTextLanguages = []string{
	"simple", "arabic", "danish", "dutch", "english", "finnish", "french", "german",
	"hungarian", "indonesian", "irish", "italian", "lithuanian", "nepali", "norwegian",
	"portuguese", "romanian", "russian", "spanish", "swedish", "tamil", "turkish",
}

// WhereLike mirrors PostgresGrammar::whereLike(): the default match is case insensitive.
func (g *PostgresGrammar) WhereLike(b *Builder, where Where) string {
	operator := ""
	if where.Not {
		operator = "not "
	}
	if where.CaseSensitive {
		operator += "like"
	} else {
		operator += "ilike"
	}

	basic := where
	basic.Operator = operator

	return g.WhereBasic(b, basic)
}

// WhereBitwise mirrors PostgresGrammar::whereBitwise().
func (g *PostgresGrammar) WhereBitwise(b *Builder, where Where) string {
	operator := strings.ReplaceAll(where.Operator, "?", "??")

	return "(" + g.Wrap(where.Column) + " " + operator + " " + g.Parameter(where.Value) + ")::bool"
}

// WhereFullText mirrors PostgresGrammar::whereFullText().
func (g *PostgresGrammar) WhereFullText(b *Builder, where Where) string {
	language := optionValue(where.Options, "language", "english")
	if !containsString(validFullTextLanguages, language) {
		language = "english"
	}

	columns := make([]string, 0, len(where.Columns))
	for _, column := range where.Columns {
		columns = append(columns, "to_tsvector('"+language+"', "+g.Wrap(column)+")")
	}

	mode := "plainto_tsquery"
	switch optionValue(where.Options, "mode", "") {
	case "phrase":
		mode = "phraseto_tsquery"
	case "websearch":
		mode = "websearch_to_tsquery"
	case "raw":
		mode = "to_tsquery"
	}

	return "(" + strings.Join(columns, " || ") + ") @@ " + mode + "('" + language + "', " + g.Parameter(where.Value) + ")"
}

// CompileHaving mirrors PostgresGrammar::compileHaving().
func (g *PostgresGrammar) CompileHaving(b *Builder, having Having) string {
	if having.Type == HavingBitType {
		return "(" + g.Wrap(having.Column) + " " + having.Operator + " " + g.Parameter(having.Value) + ")::bool"
	}

	return g.BaseGrammar.CompileHaving(b, having)
}

// CompileUpdateColumns mirrors PostgresGrammar::compileUpdateColumns(): the key is reduced to its
// last dot-segment before wrapping (so a qualified update column assigns to the bare column), and JSON
// selectors use jsonb_set. The isJsonSelector test still runs against the full key, exactly as Laravel
// does, but the reduced column is what compileJsonUpdateColumn receives.
//
// Postgres rejects more than one assignment to the same column ("multiple assignments to same
// column"), so a column's JSON selectors are fused into one nested jsonb_set chain at the first
// selector's position: jsonb_set(jsonb_set(col::jsonb, path1, ?), path2, ?). Laravel leaves them as
// separate assignments, which only survives on a driver that permits the duplicate. Every other
// assignment keeps its position and its value.
func (g *PostgresGrammar) CompileUpdateColumns(b *Builder, values UpdateValues) string {
	merged := g.groupJsonColumnsForUpdate(values)
	parts := make([]string, 0, len(merged))

	for _, value := range merged {
		if group, ok := value.Value.(postgresJsonUpdate); ok {
			parts = append(parts, g.compileJsonUpdateGroup(value.Column, group))
			continue
		}

		parts = append(parts, g.Wrap(lastDotSegment(value.Column))+" = "+g.Parameter(value.Value))
	}

	return strings.Join(parts, ", ")
}

// groupJsonColumnsForUpdate folds a column's several JSON selectors into a single assignment, kept in
// place at the first selector, so the compiled statement assigns the column once. A group carries the
// selector paths and values in order; a non-selector assignment is passed through untouched. The
// grouping is deterministic, so the compile and prepareBindingsForUpdate paths derive the same
// sequence and keep the placeholders aligned with the bindings.
func (g *PostgresGrammar) groupJsonColumnsForUpdate(values UpdateValues) UpdateValues {
	merged := make(UpdateValues, 0, len(values))
	index := make(map[string]int, len(values))

	for _, value := range values {
		if !g.IsJsonSelector(value.Column) {
			merged = append(merged, value)
			continue
		}

		segments := strings.Split(lastDotSegment(value.Column), "->")
		field := segments[0]
		path := "'{" + strings.Join(g.WrapJsonPathAttributes(segments[1:], `"`), ",") + "}'"

		if i, ok := index[field]; ok {
			group := merged[i].Value.(postgresJsonUpdate)
			group.paths = append(group.paths, path)
			group.values = append(group.values, value.Value)
			merged[i].Value = group
			continue
		}

		index[field] = len(merged)
		merged = append(merged, UpdateValue{
			Column: lastDotSegment(value.Column),
			Value:  postgresJsonUpdate{paths: []string{path}, values: []any{value.Value}},
		})
	}

	return merged
}

// compileJsonUpdateGroup renders a column's JSON selectors as one assignment whose jsonb_set calls
// nest, so the column is assigned exactly once. A single selector renders identically to
// compileJsonUpdateColumn.
func (g *PostgresGrammar) compileJsonUpdateGroup(column string, group postgresJsonUpdate) string {
	field := g.Wrap(strings.Split(column, "->")[0])

	expression := field + "::jsonb"
	for i, path := range group.paths {
		expression = "jsonb_set(" + expression + ", " + path + ", " + g.Parameter(group.values[i]) + ")"
	}

	return field + " = " + expression
}

// postgresJsonUpdate is one column's JSON selectors fused into a single chained jsonb_set assignment:
// the rendered path literals and their values, parallel and in selector order.
type postgresJsonUpdate struct {
	paths  []string
	values []any
}

// CompileUpdate mirrors PostgresGrammar::compileUpdate(): joins or a limit take the ctid rewrite.
func (g *PostgresGrammar) CompileUpdate(b *Builder, values UpdateValues) string {
	if len(b.joins) > 0 || b.limit != nil {
		return g.compileUpdateWithJoinsOrLimit(b, values)
	}

	return g.BaseGrammar.CompileUpdate(b, values)
}

// compileUpdateWithJoinsOrLimit mirrors PostgresGrammar::compileUpdateWithJoinsOrLimit().
func (g *PostgresGrammar) compileUpdateWithJoinsOrLimit(b *Builder, values UpdateValues) string {
	table := g.WrapTable(b.from)
	columns := g.CompileUpdateColumns(b, values)
	alias := aliasOf(b.from)

	inner := b.Clone()
	inner.columns = []any{alias + ".ctid"}
	selectSQL := g.CompileSelect(inner)

	return "update " + table + " set " + columns + " where " + g.Wrap("ctid") + " in (" + selectSQL + ")"
}

// CompileJoinLateral mirrors PostgresGrammar::compileJoinLateral(): Postgres supports lateral joins
// and spells them the same way MySQL does.
func (g *PostgresGrammar) CompileJoinLateral(join *JoinClause, expression string) string {
	return strings.TrimSpace(join.Type + " join lateral " + expression + " on true")
}

// CompileUpdateFrom mirrors PostgresGrammar::compileUpdateFrom().
func (g *PostgresGrammar) CompileUpdateFrom(b *Builder, values UpdateValues) string {
	table := g.WrapTable(b.from)
	columns := g.CompileUpdateColumns(b, values)

	from := ""
	if len(b.joins) > 0 {
		tables := make([]string, 0, len(b.joins))
		for _, join := range b.joins {
			tables = append(tables, g.WrapTable(join.Table))
		}
		if len(tables) > 0 {
			from = " from " + strings.Join(tables, ", ")
		}
	}

	where := g.CompileUpdateWheres(b)

	return strings.TrimSpace("update " + table + " set " + columns + from + " " + where)
}

// CompileUpdateWheres mirrors PostgresGrammar::compileUpdateWheres().
func (g *PostgresGrammar) CompileUpdateWheres(b *Builder) string {
	baseWheres := g.CompileWheres(b)
	if len(b.joins) == 0 {
		return baseWheres
	}

	joinWheres := g.CompileUpdateJoinWheres(b)
	if strings.TrimSpace(baseWheres) == "" {
		return "where " + removeLeadingBoolean(joinWheres)
	}

	return baseWheres + " " + joinWheres
}

// CompileUpdateJoinWheres mirrors PostgresGrammar::compileUpdateJoinWheres().
func (g *PostgresGrammar) CompileUpdateJoinWheres(b *Builder) string {
	var parts []string

	for _, join := range b.joins {
		for _, where := range join.wheres {
			parts = append(parts, where.Boolean+" "+g.CompileWhere(b, where))
		}
	}

	return strings.Join(parts, " ")
}

// PrepareBindingsForUpdateFrom mirrors PostgresGrammar::prepareBindingsForUpdateFrom().
func (g *PostgresGrammar) PrepareBindingsForUpdateFrom(bindings map[string][]any, values UpdateValues) []any {
	merged := g.groupJsonColumnsForUpdate(values)
	out := make([]any, 0, len(merged))

	for _, value := range merged {
		out = g.appendUpdateBindings(out, value)
	}
	out = append(out, bindings["where"]...)
	for _, typ := range BindingTypes {
		if typ == "select" || typ == "where" {
			continue
		}
		out = append(out, bindings[typ]...)
	}

	return out
}

// CompileDelete mirrors PostgresGrammar::compileDelete(): joins or a limit take the ctid rewrite.
func (g *PostgresGrammar) CompileDelete(b *Builder) string {
	if len(b.joins) > 0 || b.limit != nil {
		table := g.WrapTable(b.from)
		alias := aliasOf(b.from)

		inner := b.Clone()
		inner.columns = []any{alias + ".ctid"}
		selectSQL := g.CompileSelect(inner)

		return "delete from " + table + " where " + g.Wrap("ctid") + " in (" + selectSQL + ")"
	}

	return g.BaseGrammar.CompileDelete(b)
}

// WrapJsonBooleanSelector mirrors PostgresGrammar::wrapJsonBooleanSelector().
func (g *PostgresGrammar) WrapJsonBooleanSelector(value string) string {
	return "(" + strings.ReplaceAll(g.WrapJsonSelector(value), "->>", "->") + ")::jsonb"
}

// WrapJsonBooleanValue mirrors PostgresGrammar::wrapJsonBooleanValue().
func (g *PostgresGrammar) WrapJsonBooleanValue(value string) string { return "'" + value + "'::jsonb" }

// optionValue reads a fulltext option. Go carries options as key or key:value strings.
func optionValue(options []string, key string, fallback string) string {
	for _, option := range options {
		if option == key {
			return key
		}
		if strings.HasPrefix(option, key+":") {
			return strings.TrimPrefix(option, key+":")
		}
	}

	return fallback
}

// containsString reports whether a slice contains a value.
func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}

	return false
}
