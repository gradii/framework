package query

import "strings"

// ToSelectSQL compiles a select statement and its bindings.
func (b *Builder) ToSelectSQL() (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileSelect(b), b.GetBindings()
}

// ToInsertSQL compiles an insert statement and its bindings.
func (b *Builder) ToInsertSQL(values []map[string]any) (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileInsert(b, values), CleanBindings(insertBindings(values, nil))
}

// ToInsertGetIdSQL compiles an insert that returns the generated id. The values may be an ordered
// InsertValues, which fixes the column order Laravel keeps for a single insertGetId record, or a
// []map[string]any, whose columns are ordered by name (the historic map-shaped behaviour).
func (b *Builder) ToInsertGetIdSQL(values any, sequence string) (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	records, columns := insertGetIdRecords(values)
	if len(columns) > 0 {
		b.insertColumns = columns
		defer func() { b.insertColumns = nil }()
	}

	return b.grammar.CompileInsertGetId(b, records, sequence), CleanBindings(insertBindings(records, columns))
}

// insertGetIdRecords normalises the argument ToInsertGetIdSQL accepts into an ordered []map[string]any
// plus the column order CompileInsert should use. An InsertValues (an ordered record) fixes the column
// order; a []map[string]any leaves it nil, so CompileInsert and insertBindings fall back to sorting.
func insertGetIdRecords(values any) ([]map[string]any, []string) {
	switch v := values.(type) {
	case nil:
		return nil, nil
	case InsertValues:
		if len(v) == 0 {
			return nil, nil
		}

		record := make(map[string]any, len(v))
		columns := make([]string, 0, len(v))
		for _, pair := range v {
			record[pair.Column] = pair.Value
			columns = append(columns, pair.Column)
		}

		return []map[string]any{record}, columns
	case []map[string]any:
		return v, nil
	default:
		return nil, nil
	}
}

// ToInsertOrIgnoreSQL compiles an insert that ignores conflicts.
func (b *Builder) ToInsertOrIgnoreSQL(values []map[string]any) (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileInsertOrIgnore(b, values), CleanBindings(insertBindings(values, nil))
}

// ToUpsertSQL compiles an upsert statement and its bindings.
func (b *Builder) ToUpsertSQL(values []map[string]any, uniqueBy []string, update []UpsertUpdate) (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	bindings := insertBindings(values, nil)
	for _, item := range update {
		if item.Bound {
			bindings = append(bindings, item.Value)
		}
	}

	return b.grammar.CompileUpsert(b, values, uniqueBy, update), CleanBindings(bindings)
}

// ToUpdateSQL compiles an update statement and its bindings. The values may be an ordered UpdateValues
// (Laravel's update array, whose insertion order fixes the SET list) or a plain map[string]any, which is
// ordered by column name. The binding order matches the SQL: set values first, then the where clause,
// exactly as Grammar::prepareBindingsForUpdate produces.
func (b *Builder) ToUpdateSQL(values any) (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	ordered := updateValuesFrom(values)
	compiled, contributions := b.resolveUpdateValues(ordered)

	return b.grammar.CompileUpdate(b, compiled), CleanBindings(b.grammar.PrepareBindingsForUpdate(b.bindings, contributions))
}

// resolveUpdateValues splits the update assignments so a query-valued assignment compiles as a
// parenthesised subquery while its bindings travel as a binding contribution. Mirrors the mapping
// Builder::update() performs before compileUpdate()/prepareBindingsForUpdate(): a value that is a
// Builder becomes Expression("(subSql)") carrying the sub-select's bindings, and every other value is
// carried unchanged. Only a *Builder is resolved, matching Laravel's `instanceof Builder` guard.
func (b *Builder) resolveUpdateValues(ordered UpdateValues) (UpdateValues, UpdateValues) {
	compiled := make(UpdateValues, len(ordered))
	contributions := make(UpdateValues, len(ordered))

	for i, value := range ordered {
		compiled[i] = value
		contributions[i] = value

		if query, ok := value.Value.(*Builder); ok {
			sql, bindings := b.parseSub(query)
			compiled[i] = UpdateValue{Column: value.Column, Value: Expression{Value: "(" + sql + ")"}}
			contributions[i] = UpdateValue{Column: value.Column, Value: subQueryBindings{values: bindings}}
		}
	}

	return compiled, contributions
}

// ToDeleteSQL compiles a delete statement and its bindings.
func (b *Builder) ToDeleteSQL() (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileDelete(b), b.grammar.PrepareBindingsForDelete(b.bindings)
}

// ToExistsSQL compiles an exists statement and its bindings.
func (b *Builder) ToExistsSQL() (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileExists(b), b.GetBindings()
}

// ToAggregateSQL compiles an aggregate select and its bindings. The aggregate is applied to a copy
// so the builder itself is left unchanged.
//
// The copy mirrors what Laravel's aggregate() does before it compiles:
//
//	$this->cloneWithout($this->unions || $this->havings ? [] : ['columns'])
//	     ->cloneWithoutBindings($this->unions || $this->havings ? [] : ['select'])
//	     ->setAggregate($function, $columns)
//	     ->get($columns);
//
// Without unions or havings the select list is dropped and, with it, the select bindings: the
// compiled aggregate selects no columns of its own (compileColumns is skipped whenever an aggregate
// is set), so a subselect that contributed a "select" binding no longer has a placeholder in the
// SQL and must not hand its value to the executor. With unions or havings the select list survives
// inside the derived table, so its bindings must survive with it.
//
// setAggregate() also drops the orders and their bindings when the query has no groups, which is
// why an aggregate over an ordered query compiles without an order by clause.
func (b *Builder) ToAggregateSQL(aggregate Aggregate) (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	clone := b.Clone()
	if len(clone.unions) == 0 && len(clone.havings) == 0 {
		clone = clone.CloneWithout("columns").CloneWithoutBindings("select")
	}

	clone.aggregate = &aggregate

	if len(clone.groups) == 0 {
		clone.orders = nil
		clone.bindings["order"] = []any{}
	}

	clone.ApplyBeforeQueryCallbacks()

	return clone.grammar.CompileSelect(clone), clone.GetBindings()
}

// GetCountForPagination compiles the count statement a paginator runs to learn how many rows a page
// is drawn from. It mirrors the compiler half of Builder::getCountForPagination, i.e.
// Builder::runPaginationCountQuery: the count is built on a clone, so the receiver's own clauses and
// bindings survive untouched, and it returns the compiled SQL and its bindings rather than an int
// because this builder has no execution side.
//
// With neither groups nor havings the count runs directly against the table, dropping the select
// list, the orders and the limit/offset (and the matching select and order bindings) so the statement
// is a plain "select count(...) as aggregate from ...". With unions the count wraps the union in a
// derived table named "temp_table", dropping only the union orders, limit and offset so the union
// itself survives. A grouped or having query is counted by wrapping the grouped select as a derived
// table named "aggregate_table" and counting its rows, which is what makes grouped pagination count
// groups rather than rows.
func (b *Builder) GetCountForPagination(columns ...any) (string, []any) {
	if b.grammar == nil {
		return "", nil
	}

	if len(columns) == 0 {
		columns = []any{"*"}
	}

	if len(b.groups) > 0 || len(b.havings) > 0 {
		clone := b.CloneWithout("orders", "limit", "offset").CloneWithoutBindings("order")

		if clone.columns == nil && len(b.joins) > 0 {
			clone.Select(asString(clone.from) + ".*")
		}

		query := b.NewQuery()
		query.From(Expression{Value: "(" + clone.ToSQL() + ") as " + b.grammar.Wrap("aggregate_table")}, "")
		query.MergeBindings(clone)
		query.SetAggregate("count", b.withoutSelectAliases(columns))

		return query.ToSQL(), query.GetBindings()
	}

	var without, withoutBindings []string
	if len(b.unions) > 0 {
		without = []string{"unionOrders", "unionLimit", "unionOffset"}
		withoutBindings = []string{"unionOrder"}
	} else {
		without = []string{"columns", "orders", "limit", "offset"}
		withoutBindings = []string{"select", "order"}
	}

	clone := b.CloneWithout(without...).CloneWithoutBindings(withoutBindings...)
	clone.SetAggregate("count", b.withoutSelectAliases(columns))

	return clone.ToSQL(), clone.GetBindings()
}

// withoutSelectAliases strips a trailing " as <alias>" from each string column so a count can wrap
// the underlying column. Mirrors Builder::withoutSelectAliases().
func (b *Builder) withoutSelectAliases(columns []any) []any {
	out := make([]any, len(columns))
	for i, column := range columns {
		if s, ok := column.(string); ok {
			if pos := strings.Index(strings.ToLower(s), " as "); pos != -1 {
				out[i] = s[:pos]
				continue
			}
		}
		out[i] = column
	}

	return out
}

// ToTruncateSQL compiles the truncate statements. SQLite needs two.
func (b *Builder) ToTruncateSQL() []Statement {
	if b.grammar == nil {
		return nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileTruncate(b)
}

// insertBindings returns the values of every record in the column order CompileInsert uses. A nil
// columns means a map-shaped insert, whose columns come from the first record's sorted keys (Go maps
// are unordered); an ordered insertGetId passes its recorded columns so the bindings match its SQL.
func insertBindings(values []map[string]any, columns []string) []any {
	if len(values) == 0 {
		return nil
	}

	if columns == nil {
		columns = sortedKeys(values[0])
	}

	out := make([]any, 0, len(values)*len(columns))

	for _, record := range values {
		for _, column := range columns {
			out = append(out, record[column])
		}
	}

	return out
}

// ToInsertUsingSQL compiles the insert-using statement InsertUsing recorded and returns it with its
// bindings. It mirrors what Laravel hands to affectingStatement —
// grammar.compileInsertUsing(columns, sql) with cleanBindings(bindings). No dialect overrides
// compileInsertUsing: MySql, SQLite and Postgres each build their ignore variant on top of it in
// their own entry points rather than here.
func (b *Builder) ToInsertUsingSQL() (string, []any) {
	if b.grammar == nil || !b.insertUsingSet {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileInsertUsing(b, b.insertUsingColumns, b.insertUsingSQL), b.insertUsingBindings
}

// ToInsertOrIgnoreUsingSQL compiles the insert-or-ignore-using statement InsertOrIgnoreUsing recorded
// and returns it with its bindings. It mirrors what Laravel hands to affectingStatement —
// grammar.compileInsertOrIgnoreUsing(columns, sql) with cleanBindings(bindings) — so the base and
// SqlServer grammars refuse and record errInsertIgnoreNotSupported, surfaced through Builder.Error().
func (b *Builder) ToInsertOrIgnoreUsingSQL() (string, []any) {
	if b.grammar == nil || !b.insertUsingSet {
		return "", nil
	}

	b.ApplyBeforeQueryCallbacks()

	return b.grammar.CompileInsertOrIgnoreUsing(b, b.insertUsingColumns, b.insertUsingSQL), b.insertUsingBindings
}
