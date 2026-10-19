package query

import (
	"strings"
)

// SqliteGrammar mirrors Illuminate\Database\Query\Grammars\SQLiteGrammar. Only the methods whose SQL
// differs from the base are declared here. SQLite quotes with double quotes, like the base.
type SqliteGrammar struct {
	*BaseGrammar
}

// NewSqliteGrammar creates the SQLite grammar.
func NewSqliteGrammar(prefix string) *SqliteGrammar {
	g := &SqliteGrammar{BaseGrammar: NewBaseGrammar(prefix)}
	g.BaseGrammar.Attach(g)

	return g
}

// WhereBasic mirrors SQLiteGrammar::whereBasic(): the null-safe equality operator becomes IS.
func (g *SqliteGrammar) WhereBasic(b *Builder, where Where) string {
	if where.Operator == "<=>" {
		return g.Wrap(where.Column) + " IS " + g.Parameter(where.Value)
	}

	return g.BaseGrammar.WhereBasic(b, where)
}

// WhereLike mirrors SQLiteGrammar::whereLike(): a case sensitive match uses glob.
func (g *SqliteGrammar) WhereLike(b *Builder, where Where) string {
	if !where.CaseSensitive {
		return g.BaseGrammar.WhereLike(b, where)
	}

	operator := "glob"
	if where.Not {
		operator = "not glob"
	}

	basic := where
	basic.Operator = operator

	return g.WhereBasic(b, basic)
}

// PrepareWhereLikeBinding mirrors SQLiteGrammar::prepareWhereLikeBinding().
func (g *SqliteGrammar) PrepareWhereLikeBinding(value string, caseSensitive bool) string {
	if !caseSensitive {
		return value
	}

	replacer := strings.NewReplacer("*", "[*]", "?", "[?]", "%", "*", "_", "?")

	return replacer.Replace(value)
}

// dateBasedWhere mirrors SQLiteGrammar::dateBasedWhere().
func (g *SqliteGrammar) dateBasedWhere(format string, where Where) string {
	return "strftime('" + format + "', " + g.Wrap(where.Column) + ") " + where.Operator +
		" cast(" + g.Parameter(where.Value) + " as text)"
}

// WhereDate mirrors SQLiteGrammar::whereDate().
func (g *SqliteGrammar) WhereDate(b *Builder, where Where) string {
	return g.dateBasedWhere("%Y-%m-%d", where)
}

// WhereDay mirrors SQLiteGrammar::whereDay().
func (g *SqliteGrammar) WhereDay(b *Builder, where Where) string {
	return g.dateBasedWhere("%d", where)
}

// WhereMonth mirrors SQLiteGrammar::whereMonth().
func (g *SqliteGrammar) WhereMonth(b *Builder, where Where) string {
	return g.dateBasedWhere("%m", where)
}

// WhereYear mirrors SQLiteGrammar::whereYear().
func (g *SqliteGrammar) WhereYear(b *Builder, where Where) string {
	return g.dateBasedWhere("%Y", where)
}

// WhereTime mirrors SQLiteGrammar::whereTime().
func (g *SqliteGrammar) WhereTime(b *Builder, where Where) string {
	return g.dateBasedWhere("%H:%M:%S", where)
}

// CompileIndexHint mirrors SQLiteGrammar::compileIndexHint().
func (g *SqliteGrammar) CompileIndexHint(b *Builder, hint *IndexHint) string {
	if hint.Type == "force" {
		return "indexed by " + hint.Index
	}

	return ""
}

// CompileInsertOrIgnore mirrors SQLiteGrammar::compileInsertOrIgnore().
func (g *SqliteGrammar) CompileInsertOrIgnore(b *Builder, values []map[string]any) string {
	return strings.Replace(g.CompileInsert(b, values), "insert", "insert or ignore", 1)
}

// CompileInsertOrIgnoreUsing mirrors SQLiteGrammar::compileInsertOrIgnoreUsing().
func (g *SqliteGrammar) CompileInsertOrIgnoreUsing(b *Builder, columns []any, sql string) string {
	return strings.Replace(g.CompileInsertUsing(b, columns, sql), "insert", "insert or ignore", 1)
}

// CompileUpdate mirrors SQLiteGrammar::compileUpdate(): joins or a limit take the rowid rewrite.
func (g *SqliteGrammar) CompileUpdate(b *Builder, values UpdateValues) string {
	if len(b.joins) > 0 || b.limit != nil {
		return g.compileUpdateWithJoinsOrLimit(b, values)
	}

	return g.BaseGrammar.CompileUpdate(b, values)
}

// compileUpdateWithJoinsOrLimit mirrors SQLiteGrammar::compileUpdateWithJoinsOrLimit().
func (g *SqliteGrammar) compileUpdateWithJoinsOrLimit(b *Builder, values UpdateValues) string {
	table := g.WrapTable(b.from)
	columns := g.CompileUpdateColumns(b, values)
	alias := aliasOf(b.from)

	inner := b.Clone()
	inner.columns = []any{alias + ".rowid"}
	selectSQL := g.CompileSelect(inner)

	return "update " + table + " set " + columns + " where " + g.Wrap("rowid") + " in (" + selectSQL + ")"
}

// CompileUpdateColumns mirrors SQLiteGrammar::compileUpdateColumns(): the JSON selectors of one column
// are folded into a single json_patch() assignment appended last, and the patch names the wrapped column
// rather than the extraction inside it. Every other assignment keeps its order and its value.
func (g *SqliteGrammar) CompileUpdateColumns(b *Builder, values UpdateValues) string {
	groups := g.groupJsonColumnsForUpdate(values)
	merged := g.mergeJsonGroups(values, groups)

	isGroup := make(map[string]bool, len(groups))
	for _, group := range groups {
		isGroup[group.Column] = true
	}

	parts := make([]string, 0, len(merged))
	for _, value := range merged {
		column := lastDotSegment(value.Column)

		compiled := g.Parameter(value.Value)
		if isGroup[value.Column] {
			compiled = g.CompileJsonPatch(column, value.Value)
		}

		parts = append(parts, g.Wrap(column)+" = "+compiled)
	}

	return strings.Join(parts, ", ")
}

// CompileJsonPatch mirrors SQLiteGrammar::compileJsonPatch().
func (g *SqliteGrammar) CompileJsonPatch(column string, value any) string {
	return "json_patch(ifnull(" + g.Wrap(column) + ", json('{}')), json(" + g.Parameter(value) + "))"
}

// CompileDelete mirrors SQLiteGrammar::compileDelete(): joins or a limit take the rowid rewrite.
func (g *SqliteGrammar) CompileDelete(b *Builder) string {
	if len(b.joins) > 0 || b.limit != nil {
		table := g.WrapTable(b.from)
		alias := aliasOf(b.from)

		inner := b.Clone()
		inner.columns = []any{alias + ".rowid"}
		selectSQL := g.CompileSelect(inner)

		return "delete from " + table + " where " + g.Wrap("rowid") + " in (" + selectSQL + ")"
	}

	return g.BaseGrammar.CompileDelete(b)
}

// CompileUpsert mirrors SQLiteGrammar::compileUpsert(): on conflict do update with excluded.
func (g *SqliteGrammar) CompileUpsert(b *Builder, values []map[string]any, uniqueBy []string, update []UpsertUpdate) string {
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

// CompileTruncate mirrors SQLiteGrammar::compileTruncate(): the sequence table is reset too.
//
// Laravel asks the connection's schema builder for parseSchemaAndTable. That method only splits the
// reference on ".", so the same split is done here: a schema-qualified table keeps its schema on the
// sqlite_sequence delete, and only the bare table name is prefixed in the binding.
func (g *SqliteGrammar) CompileTruncate(b *Builder) []Statement {
	reference := asString(b.from)
	schema := ""
	table := reference
	if index := strings.LastIndex(reference, "."); index >= 0 {
		schema = reference[:index]
		table = reference[index+1:]
	}

	// Mirrors `$schema = $schema ? $this->wrapValue($schema).'.' : ''`: sqlite_sequence itself is
	// never prefixed, whatever prefix the connection carries.
	sequence := "delete from "
	if schema != "" {
		sequence += g.WrapValue(schema) + "."
	}

	return []Statement{
		{SQL: sequence + "sqlite_sequence where name = ?", Bindings: []any{g.Prefix() + table}},
		{SQL: "delete from " + g.WrapTable(b.from)},
	}
}

// CompileLock mirrors SQLiteGrammar::compileLock().
func (g *SqliteGrammar) CompileLock(b *Builder, value any) string { return "" }

// WrapUnion mirrors SQLiteGrammar::wrapUnion().
func (g *SqliteGrammar) WrapUnion(sql string) string { return "select * from (" + sql + ")" }

// PrepareBindingForJsonContains mirrors SQLiteGrammar::prepareBindingForJsonContains().
func (g *SqliteGrammar) PrepareBindingForJsonContains(value any) any { return value }

// WrapJsonSelector mirrors SQLiteGrammar::wrapJsonSelector().
func (g *SqliteGrammar) WrapJsonSelector(value string) string {
	field, path := g.wrap.JsonFieldAndPath(value)

	return "json_extract(" + field + path + ")"
}

// CompileJsonContains mirrors SQLiteGrammar::compileJsonContains().
func (g *SqliteGrammar) CompileJsonContains(column string, value string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "exists (select 1 from json_each(" + field + path + ") where " + g.Wrap("json_each.value") + " is " + value + ")"
}

// WhereJsonContains mirrors SQLiteGrammar::whereJsonContains().
//
// Laravel's SQLite grammar compiles a single "json_each" predicate with one "is ?" parameter, which
// cannot bind a list: the binding path flattens a list value into one binding per element, so the
// statement would carry several arguments against the one placeholder and fail with "expected 1
// arguments, got 2". A list is therefore expanded into one predicate per element, ANDed together,
// each taking its own binding - the shape the pre-port Sqlite grammar produced. A scalar value, and a
// raw expression, compile exactly as the base grammar does.
//
// The negated (WhereJsonDoesntContain) form negates that whole conjunction rather than each
// predicate, so a row "does not contain" the list when it is missing at least one element. That is
// the meaning MySQL's "not json_contains(column, list)" and Postgres' "not column @> list" carry, and
// it makes the clause the exact negation of its positive form. Negating each predicate instead would
// read "contains none" and quietly diverge from the other two dialects for the same call.
func (g *SqliteGrammar) WhereJsonContains(b *Builder, where Where) string {
	if IsExpression(where.Value) || !isArrayValue(where.Value) {
		return g.BaseGrammar.WhereJsonContains(b, where)
	}

	values := toAnySlice(where.Value)
	if len(values) == 0 {
		// An empty list cannot be ANDed into a predicate, and the base grammar's single
		// "json_each.value is ?" placeholder would be left without a binding: the empty slice
		// is bound as one argument that the binding path then flattens to none, so the statement
		// fails at execution with "expected 1 arguments, got 0". Settle it the way whereIn's
		// empty list is settled: the positive form matches nothing ("0 = 1") and its exact
		// negation matches everything ("1 = 1"), both taking no binding.
		if where.Not {
			return "1 = 1"
		}

		return "0 = 1"
	}

	parts := make([]string, 0, len(values))
	for _, value := range values {
		compiled := g.CompileJsonContains(asString(where.Column), g.Parameter(value))
		if compiled == "" {
			b.setError(errJsonContainsNotSupported)

			return ""
		}

		parts = append(parts, compiled)
	}

	joined := strings.Join(parts, " and ")
	if where.Not {
		return "not (" + joined + ")"
	}

	return joined
}

// CompileJsonContainsKey mirrors SQLiteGrammar::compileJsonContainsKey().
func (g *SqliteGrammar) CompileJsonContainsKey(column string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "json_type(" + field + path + ") is not null"
}

// CompileJsonLength mirrors SQLiteGrammar::compileJsonLength().
func (g *SqliteGrammar) CompileJsonLength(column string, operator string, value string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "json_array_length(" + field + path + ") " + operator + " " + value
}

// PrepareBindingsForUpdate mirrors SQLiteGrammar::prepareBindingsForUpdate(): the JSON selectors are
// regrouped into one nested document per column, every array value is json-encoded, and the set values
// precede the bindings of every clause except select.
func (g *SqliteGrammar) PrepareBindingsForUpdate(bindings map[string][]any, values UpdateValues) []any {
	groups := g.groupJsonColumnsForUpdate(values)
	merged := g.mergeJsonGroups(values, groups)

	out := make([]any, 0, len(merged)+len(bindings))
	for _, value := range merged {
		if isArrayValue(value.Value) {
			out = append(out, encodeJSON(value.Value))
			continue
		}
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

// mergeJsonGroups mirrors the reject(json selectors)->merge($jsonGroups) step both
// SQLiteGrammar::compileUpdateColumns() and SQLiteGrammar::prepareBindingsForUpdate() perform: the
// non-selector assignments keep their order first, then each group is appended, and Collection::merge()
// overwrites in place a plain column that shares a group's name (so a group wins over it).
func (g *SqliteGrammar) mergeJsonGroups(values UpdateValues, groups []UpdateValue) []UpdateValue {
	merged := make([]UpdateValue, 0, len(values)+len(groups))
	index := make(map[string]int, len(values)+len(groups))

	for _, value := range values {
		if g.IsJsonSelector(value.Column) {
			continue
		}

		index[value.Column] = len(merged)
		merged = append(merged, value)
	}

	for _, group := range groups {
		if i, ok := index[group.Column]; ok {
			merged[i] = group
			continue
		}

		index[group.Column] = len(merged)
		merged = append(merged, group)
	}

	return merged
}

// groupJsonColumnsForUpdate mirrors SQLiteGrammar::groupJsonColumnsForUpdate(): each JSON selector is
// folded into a nested document keyed by the column it belongs to, in first-encounter order.
func (g *SqliteGrammar) groupJsonColumnsForUpdate(values UpdateValues) []UpdateValue {
	var groups []UpdateValue
	index := make(map[string]int)

	for _, value := range values {
		if !g.IsJsonSelector(value.Column) {
			continue
		}

		path := sqliteJsonGroupPath(value.Column)
		if i, ok := index[path[0]]; ok {
			groups[i].Value = setNestedGroup(groups[i].Value.(map[string]any), path[1:], value.Value)
			continue
		}

		index[path[0]] = len(groups)
		groups = append(groups, UpdateValue{Column: path[0], Value: setNestedGroup(map[string]any{}, path[1:], value.Value)})
	}

	return groups
}

// sqliteJsonGroupPath builds the grouping path the way SQLiteGrammar::groupJsonColumnsForUpdate() does:
// str_replace('->', '.', Str::after($key, '.')), split on '.'.
func sqliteJsonGroupPath(column string) []string {
	after := column
	if i := strings.Index(column, "."); i >= 0 {
		after = column[i+1:]
	}

	return strings.Split(strings.ReplaceAll(after, "->", "."), ".")
}

// setNestedGroup mirrors Arr::set() for the update grouping: the trailing path segment receives the
// value and intermediate segments become nested maps.
func setNestedGroup(target map[string]any, segments []string, value any) map[string]any {
	last := len(segments) - 1

	current := target
	for i := 0; i < last; i++ {
		next, ok := current[segments[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segments[i]] = next
		}
		current = next
	}

	if last >= 0 {
		current[segments[last]] = value
	}

	return target
}

// CompileGroupLimit mirrors SQLiteGrammar::compileGroupLimit(): modern SQLite uses the base rewrite.
func (g *SqliteGrammar) CompileGroupLimit(b *Builder) string {
	return g.BaseGrammar.CompileGroupLimit(b)
}

// aliasOf returns the alias of a "table as alias" source, or the table itself.
func aliasOf(from any) string {
	value := asString(from)
	if index := strings.LastIndex(strings.ToLower(value), " as "); index >= 0 {
		return strings.TrimSpace(value[index+4:])
	}

	return value
}
