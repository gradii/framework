package db

import (
	"context"
	databasesql "database/sql"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/contracts/database/logger"
	databasedriver "github.com/gradii/framework/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/database/utils"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
	"github.com/gradii/framework/support/str"
	"github.com/jmoiron/sqlx"
)

// Query is the DB facade query builder. All SQL is produced by database/query, a port of
// Illuminate\Database's builder and its grammars; this type adapts that builder to the framework's
// db.Query contract and executes through CommonBuilder, keeping the tracing and transaction
// behaviour unchanged.
type Query struct {
	originalReadBuilder db.CommonBuilder
	allowTableless      bool
	ctx                 context.Context
	err                 error
	grammar             querybuilder.Grammar
	driver              string
	logger              logger.Logger
	readBuilder         db.CommonBuilder
	writeBuilder        db.CommonBuilder
	txLogs              *[]TxLog
	builder             *querybuilder.Builder
	components          databasedriver.QueryComponents
}

// NewQuery creates a query bound to a table. grammar selects the dialect and driver is its name,
// used to convert placeholders at execution time.
func NewQuery(ctx context.Context, readBuilder db.CommonBuilder, writeBuilder db.CommonBuilder, grammar querybuilder.Grammar, driver string, logger logger.Logger, table string, txLogs *[]TxLog, supplied ...databasedriver.QueryComponents) *Query {
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: querybuilder.NewProcessor(driver)}
	if len(supplied) > 0 {
		components = supplied[0]
	}
	initErr := components.Validate()
	if initErr != nil {
		components.Grammar = querybuilder.NewBaseGrammar("")
		components.Processor = querybuilder.NewProcessor("")
	}
	builder := querybuilder.NewBuilder(components.Grammar, components.Processor)
	builder.SetDriver(driver)
	builder.SetError(initErr)
	if observed, ok := writeBuilder.(*observedBuilder); ok {
		observed.state.mu.Lock()
		builder.SetUseUpsertAlias(observed.state.useUpsertAlias)
		version := observed.state.serverVersion
		if version != nil {
			builder.State().ServerVersion = func() (string, error) { return version(ctx) }
		}
		observed.state.mu.Unlock()
	}
	if table != "" {
		builder.From(table, "")
	}

	return &Query{
		ctx:                 ctx,
		err:                 initErr,
		components:          components,
		grammar:             grammar,
		driver:              driver,
		logger:              logger,
		readBuilder:         readBuilder,
		originalReadBuilder: readBuilder,
		writeBuilder:        writeBuilder,
		txLogs:              txLogs,
		builder:             builder,
	}
}

// clone is reserved for explicit Clone operations and the isolated paths cloned by Laravel,
// such as aggregate queries and keyset chunks. Fluent clauses mutate the receiver.
func (r *Query) clone() *Query {
	c := *r
	c.builder = r.builder.Clone()

	return &c
}

// --- where -------------------------------------------------------------------

// Where adds a where clause. Mirrors db.Query::Where.
func (r *Query) Where(query any, args ...any) db.Query {
	return r.applyWhere(query, args, core.BooleanAnd)
}

// OrWhere adds an or where clause.
func (r *Query) OrWhere(query any, args ...any) db.Query {
	return r.applyWhere(query, args, core.BooleanOr)
}

// WhereNot adds a negated where clause.
func (r *Query) WhereNot(query any, args ...any) db.Query {
	return r.applyWhere(query, args, core.BooleanAndNot)
}

// OrWhereNot adds a negated or where clause.
func (r *Query) OrWhereNot(query any, args ...any) db.Query {
	return r.applyWhere(query, args, core.BooleanOrNot)
}

// applyWhere translates the DB facade's where shorthands onto the builder.
//
// Goravel's Where accepts more shapes than Laravel's, and the facade must keep accepting them:
// a raw string with placeholders, a string plus a slice meaning IN, and a callback that builds a
// nested group. Laravel's own shapes pass straight through.
func (r *Query) applyWhere(query any, args []any, boolean string) *Query {
	switch value := query.(type) {
	case string:
		trimmed := strings.TrimSpace(value)

		switch {
		case len(args) == 0:
			if trimmed != "" {
				r.builder.WhereRaw(value, nil, boolean)
			}
		case strings.Contains(value, "?"):
			r.builder.WhereRaw(value, args, boolean)
		case strings.Contains(trimmed, " "):
			r.builder.WhereRaw(value, args, boolean)
		case len(args) == 1 && isSlice(args[0]) && !isByteSlice(args[0]):
			r.builder.WhereIn(value, args[0], boolean, false)
		default:
			r.addSimpleWhere(value, args, boolean)
		}
	case map[string]any:
		// A map has always expanded into flat equality clauses here. Laravel groups an array
		// condition in parentheses instead, which the ORM layer follows.
		columns := make([]string, 0, len(value))
		for column := range value {
			columns = append(columns, column)
		}
		sort.Strings(columns)

		for _, column := range columns {
			r.addSimpleWhere(column, []any{value[column]}, boolean)
		}
	case func(db.Query) db.Query:
		r.builder.WhereNested(func(b *querybuilder.Builder) *querybuilder.Builder {
			return r.nested(value, b)
		}, boolean)
	default:
		r.addSimpleWhere(query, args, boolean)
	}

	return r
}

// addSimpleWhere routes to the builder method that matches the connector.
func (r *Query) addSimpleWhere(query any, args []any, boolean string) {
	switch boolean {
	case core.BooleanOr:
		r.builder.OrWhere(query, args...)
	case core.BooleanAndNot:
		r.builder.WhereNot(query, args...)
	case core.BooleanOrNot:
		r.builder.OrWhereNot(query, args...)
	default:
		r.builder.Where(query, args...)
	}
}

// nested runs a facade callback against a child builder and returns the builder it produced.
func (r *Query) nested(callback func(db.Query) db.Query, b *querybuilder.Builder) *querybuilder.Builder {
	child := &Query{
		ctx:          r.ctx,
		components:   r.components,
		grammar:      r.grammar,
		driver:       r.driver,
		logger:       r.logger,
		readBuilder:  r.readBuilder,
		writeBuilder: r.writeBuilder,
		txLogs:       r.txLogs,
		builder:      b,
	}

	callback(child)
	if child.err != nil {
		b.SetError(child.err)
	}
	return b
}

// isSlice reports whether a value is a slice or array, which the facade treats as an IN list.
func isSlice(value any) bool {
	if value == nil {
		return false
	}

	kind := reflect.ValueOf(value).Kind()

	return kind == reflect.Slice || kind == reflect.Array
}

// isByteSlice reports whether the value is a byte slice or byte array (after unwrapping
// pointers/interfaces). A byte slice is ONE binary value, not a list to expand into IN (?,...).
func isByteSlice(value any) bool {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return false
	}
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return false
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return false
	}

	return reflected.Type().Elem().Kind() == reflect.Uint8
}

// WhereRaw adds a raw where clause.
func (r *Query) WhereRaw(raw string, args []any) db.Query {
	r.builder.WhereRaw(raw, args, core.BooleanAnd)

	return r
}

// OrWhereRaw adds a raw or where clause.
func (r *Query) OrWhereRaw(raw string, args []any) db.Query {
	r.builder.WhereRaw(raw, args, core.BooleanOr)

	return r
}

// WhereIn adds an in clause.
func (r *Query) WhereIn(column string, values any) db.Query {
	r.builder.WhereIn(column, r.subquery(values), core.BooleanAnd, false)

	return r
}

// OrWhereIn adds an or in clause.
func (r *Query) OrWhereIn(column string, values any) db.Query {
	r.builder.WhereIn(column, r.subquery(values), core.BooleanOr, false)

	return r
}

// WhereNotIn adds a not in clause.
func (r *Query) WhereNotIn(column string, values any) db.Query {
	r.builder.WhereNotIn(column, r.subquery(values), core.BooleanAnd)

	return r
}

// OrWhereNotIn adds an or not in clause.
func (r *Query) OrWhereNotIn(column string, values any) db.Query {
	r.builder.WhereNotIn(column, r.subquery(values), core.BooleanOr)

	return r
}

// WhereBetween adds a between clause.
func (r *Query) WhereBetween(column string, x, y any) db.Query {
	r.builder.WhereBetween(column, []any{x, y}, core.BooleanAnd, false)

	return r
}

// OrWhereBetween adds an or between clause.
func (r *Query) OrWhereBetween(column string, x, y any) db.Query {
	r.builder.WhereBetween(column, []any{x, y}, core.BooleanOr, false)

	return r
}

// WhereNotBetween adds a not between clause.
func (r *Query) WhereNotBetween(column string, x, y any) db.Query {
	r.builder.WhereBetween(column, []any{x, y}, core.BooleanAnd, true)

	return r
}

// OrWhereNotBetween adds an or not between clause.
func (r *Query) OrWhereNotBetween(column string, x, y any) db.Query {
	r.builder.WhereBetween(column, []any{x, y}, core.BooleanOr, true)

	return r
}

// WhereNull adds an is null clause.
func (r *Query) WhereNull(column string) db.Query {
	r.builder.WhereNull(column, core.BooleanAnd, false)

	return r
}

// OrWhereNull adds an or is null clause.
func (r *Query) OrWhereNull(column string) db.Query {
	r.builder.WhereNull(column, core.BooleanOr, false)

	return r
}

// WhereNotNull adds an is not null clause.
func (r *Query) WhereNotNull(column string) db.Query {
	r.builder.WhereNotNull(column, core.BooleanAnd)

	return r
}

// OrWhereNotNull adds an or is not null clause.
func (r *Query) OrWhereNotNull(column string) db.Query {
	r.builder.WhereNotNull(column, core.BooleanOr)

	return r
}

// WhereColumn compares two columns. One trailing column means equality, two mean operator then
// column.
func (r *Query) WhereColumn(column1 string, column2 ...string) db.Query {
	operator, second, err := columnComparison(column2)
	if err != nil {
		r.err = err
		return r
	}
	r.builder.WhereColumn(column1, operator, second, core.BooleanAnd)

	return r
}

// OrWhereColumn compares two columns with or.
func (r *Query) OrWhereColumn(column1 string, column2 ...string) db.Query {
	operator, second, err := columnComparison(column2)
	if err != nil {
		r.err = err
		return r
	}
	r.builder.WhereColumn(column1, operator, second, core.BooleanOr)

	return r
}

// WhereLike adds a like clause.
func (r *Query) WhereLike(column string, value string, caseSensitive ...bool) db.Query {
	r.builder.WhereLike(column, value, len(caseSensitive) > 0 && caseSensitive[0], core.BooleanAnd, false)

	return r
}

// OrWhereLike adds an or like clause.
func (r *Query) OrWhereLike(column string, value string, caseSensitive ...bool) db.Query {
	r.builder.WhereLike(column, value, len(caseSensitive) > 0 && caseSensitive[0], core.BooleanOr, false)

	return r
}

// WhereNotLike adds a not like clause.
func (r *Query) WhereNotLike(column string, value string, caseSensitive ...bool) db.Query {
	r.builder.WhereLike(column, value, len(caseSensitive) > 0 && caseSensitive[0], core.BooleanAnd, true)

	return r
}

// OrWhereNotLike adds an or not like clause.
func (r *Query) OrWhereNotLike(column string, value string, caseSensitive ...bool) db.Query {
	r.builder.WhereLike(column, value, len(caseSensitive) > 0 && caseSensitive[0], core.BooleanOr, true)

	return r
}

// WhereExists adds an exists clause.
func (r *Query) WhereExists(query any) db.Query {
	r.builder.WhereExists(r.subquery(query), core.BooleanAnd, false)

	return r
}

// WhereJsonContains adds a JSON contains clause.
func (r *Query) WhereJsonContains(column string, value any) db.Query {
	r.builder.WhereJsonContains(column, value, core.BooleanAnd, false)

	return r
}

// OrWhereJsonContains adds an or JSON contains clause.
func (r *Query) OrWhereJsonContains(column string, value any) db.Query {
	r.builder.WhereJsonContains(column, value, core.BooleanOr, false)

	return r
}

// WhereJsonDoesntContain adds a negated JSON contains clause.
func (r *Query) WhereJsonDoesntContain(column string, value any) db.Query {
	r.builder.WhereJsonContains(column, value, core.BooleanAnd, true)

	return r
}

// OrWhereJsonDoesntContain adds a negated or JSON contains clause.
func (r *Query) OrWhereJsonDoesntContain(column string, value any) db.Query {
	r.builder.WhereJsonContains(column, value, core.BooleanOr, true)

	return r
}

// WhereJsonContainsKey adds a JSON key existence clause.
func (r *Query) WhereJsonContainsKey(column string) db.Query {
	r.builder.WhereJsonContainsKey(column, core.BooleanAnd, false)

	return r
}

// OrWhereJsonContainsKey adds an or JSON key existence clause.
func (r *Query) OrWhereJsonContainsKey(column string) db.Query {
	r.builder.WhereJsonContainsKey(column, core.BooleanOr, false)

	return r
}

// WhereJsonDoesntContainKey adds a negated JSON key existence clause.
func (r *Query) WhereJsonDoesntContainKey(column string) db.Query {
	r.builder.WhereJsonContainsKey(column, core.BooleanAnd, true)

	return r
}

// OrWhereJsonDoesntContainKey adds a negated or JSON key existence clause.
func (r *Query) OrWhereJsonDoesntContainKey(column string) db.Query {
	r.builder.WhereJsonContainsKey(column, core.BooleanOr, true)

	return r
}

// WhereJsonLength compares a JSON array length. Mirrors Laravel's
// Builder::whereJsonLength: args is either a single length (equality) or an
// operator and a length.
func (r *Query) WhereJsonLength(column string, args ...any) db.Query {
	r.builder.WhereJsonLength(column, args...)

	return r
}

// OrWhereJsonLength compares a JSON array length with or. Mirrors Laravel's
// Builder::orWhereJsonLength: args is either a single length (equality) or an
// operator and a length.
func (r *Query) OrWhereJsonLength(column string, args ...any) db.Query {
	r.builder.OrWhereJsonLength(column, args...)

	return r
}

// WhereAll repeats one condition across columns joined by and.
func (r *Query) WhereAll(columns []string, args ...any) db.Query {
	operator, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		r.err = err
		return r
	}
	if r.expandInGroup(columns, operator, value, core.BooleanAnd, core.BooleanAnd) {
		return r
	}

	r.builder.WhereAll(toStrings(columns), operator, value, core.BooleanAnd)

	return r
}

// WhereAny repeats one condition across columns joined by or, and joins that group to the
// clauses before it with and - the group connector Laravel's whereAny defaults to; the or lives
// inside the group.
func (r *Query) WhereAny(columns []string, args ...any) db.Query {
	operator, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		r.err = err
		return r
	}
	if r.expandInGroup(columns, operator, value, core.BooleanOr, core.BooleanAnd) {
		return r
	}

	r.builder.WhereAny(toStrings(columns), operator, value, core.BooleanAnd)

	return r
}

// WhereNone negates a condition repeated across columns.
func (r *Query) WhereNone(columns []string, args ...any) db.Query {
	operator, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		r.err = err
		return r
	}
	if r.expandInGroup(columns, operator, value, core.BooleanOr, core.BooleanAnd+" not") {
		return r
	}

	r.builder.WhereNone(toStrings(columns), operator, value, core.BooleanAnd)

	return r
}

// --- select and clauses ------------------------------------------------------

// Select replaces the selected columns and their bindings, mirroring Laravel's select().
func (r *Query) Select(columns ...string) db.Query {

	if len(columns) == 0 {
		columns = []string{"*"}
	}
	selects := make([]any, 0, len(columns))
	for _, column := range columns {
		selects = append(selects, column)
	}
	r.builder.Select(selects...)

	return r
}

// Distinct preserves the select list and sets the distinct columns, mirroring Laravel's distinct().
func (r *Query) Distinct(columns ...string) db.Query {

	distinctColumns := make([]any, len(columns))
	for i, column := range columns {
		distinctColumns[i] = column
	}
	r.builder.Distinct(distinctColumns...)

	return r
}

// GroupBy appends grouping columns.
func (r *Query) GroupBy(column ...string) db.Query {
	r.builder.GroupBy(toStrings(column)...)

	return r
}

// Having adds a having clause.
//
// As with applyWhere, a string that carries a binding placeholder is a raw fragment rather than a
// column name, so it is routed to HavingRaw; anything else keeps the builder's Laravel-faithful
// Having, which takes a column, an operator and a value (or a raw expression).
func (r *Query) Having(query any, args ...any) db.Query {
	r.applyHaving(query, args)

	return r
}

// applyHaving routes the DB facade's Having shorthands onto the builder. Laravel's Having takes a
// column, an operator and a value (or a raw expression); Goravel additionally accepts the
// string-with-placeholder form GORM allowed, which is a raw fragment and must keep its placeholders
// and bindings intact. This mirrors applyWhere's placeholder handling for Where.
func (r *Query) applyHaving(query any, args []any) {
	if value, ok := query.(string); ok && strings.Contains(value, "?") {
		r.builder.HavingRaw(value, args, core.BooleanAnd)

		return
	}

	r.builder.Having(query, args...)
}

// OrderBy adds an order clause.
func (r *Query) OrderBy(column any, directions ...string) db.Query {
	direction := "asc"
	if len(directions) > 0 {
		direction = directions[0]
	}

	r.builder.OrderBy(r.subquery(column), direction)

	return r
}

// OrderByDesc adds a descending order clause.
func (r *Query) OrderByDesc(column any) db.Query {
	r.builder.OrderByDesc(r.subquery(column))

	return r
}

// OrderByRaw adds a raw order clause.
func (r *Query) OrderByRaw(raw string, bindings ...any) db.Query {
	r.builder.OrderByRaw(raw, bindings)

	return r
}

// Latest orders by created_at descending, or by the given column.
func (r *Query) Latest(column ...string) db.Query {
	col := ""
	if len(column) > 0 {
		col = column[0]
	}

	r.builder.Latest(col)

	return r
}

// InRandomOrder orders randomly.
func (r *Query) InRandomOrder(seed ...any) db.Query {
	var value any = ""
	if len(seed) > 0 {
		value = seed[0]
	}
	r.builder.InRandomOrder(value)

	return r
}

// Limit sets the limit.
func (r *Query) Limit(limit uint64) db.Query {
	r.builder.Limit(int(limit))

	return r
}

// Offset sets the offset.
func (r *Query) Offset(offset uint64) db.Query {
	r.builder.Offset(int(offset))

	return r
}

// LockForUpdate locks the selected rows for update.
func (r *Query) LockForUpdate() db.Query {
	r.builder.LockForUpdate()
	r.readBuilder = r.writeBuilder

	return r
}

// SharedLock takes a shared lock on the selected rows.
func (r *Query) SharedLock() db.Query {
	r.builder.SharedLock()
	r.readBuilder = r.writeBuilder

	return r
}

// UseWritePdo forces the query's reads to run on the write connection instead of the reader
// replica, so a read that has to observe a write issued on the same connection is not answered by
// a lagging replica. Mirrors Laravel's Builder::useWritePdo, whose flag reaches the connection as
// the inverse of its $useReadPdo argument. Here the read routing is the builder a read executes
// through, so the write builder replaces the read one for this query and its clones.
func (r *Query) UseWritePdo() db.Query {
	r.builder.State().UseWritePdo = true
	r.readBuilder = r.writeBuilder

	return r
}

// Join adds an inner join from raw SQL. query is a "<table> on <condition>" fragment, or a whole
// join clause whose leading keyword ("left join ...") then overrides the default inner type.
func (r *Query) Join(query string, args ...any) db.Query {
	return r.join(query, args, "inner")
}

// LeftJoin adds a left join from raw SQL. A query that carries its own leading join keyword
// overrides the default left type.
func (r *Query) LeftJoin(query string, args ...any) db.Query {
	return r.join(query, args, "left")
}

// RightJoin adds a right join from raw SQL. A query that carries its own leading join keyword
// overrides the default right type.
func (r *Query) RightJoin(query string, args ...any) db.Query {
	return r.join(query, args, "right")
}

// CrossJoin adds a cross join from raw SQL. A query that carries its own leading join keyword
// overrides the default cross type.
func (r *Query) CrossJoin(query string, args ...any) db.Query {
	return r.join(query, args, "cross")
}

// When applies a callback when the condition holds.
func (r *Query) When(condition bool, callback func(query db.Query) db.Query, falseCallback ...func(query db.Query) db.Query) db.Query {
	if condition {
		return callback(r)
	}

	if len(falseCallback) > 0 {
		return falseCallback[0](r)
	}

	return r
}

// --- compilation -------------------------------------------------------------

func (r *Query) buildSelect() (string, []any, error) {
	if r.err != nil {
		return "", nil, r.err
	}
	if r.builder.TableSource() == nil && !r.allowTableless {
		return "", nil, errors.DatabaseTableIsRequired
	}

	// An aggregate compiles through ToAggregateSQL, which mirrors Builder::aggregate(): with neither
	// unions nor havings it drops the select list and, with it, the select bindings a subselect
	// contributed, so the executor is never handed a value whose placeholder the aggregate SQL no
	// longer carries. ToSelectSQL compiles the same SQL but keeps those stale bindings.
	var (
		sql  string
		args []any
	)
	if aggregate := r.builder.Aggregate(); aggregate != nil {
		sql, args = r.builder.ToAggregateSQL(*aggregate)
	} else {
		sql, args = r.builder.ToSelectSQL()
	}
	if err := r.builder.Error(); err != nil {
		return "", nil, err
	}
	r.resolveReader()

	return querybuilder.ReplacePlaceholders(sql, r.driver), args, nil
}

// uniformInsertRecords gives every record in a batch the same columns, the union across the batch,
// filling a column a record does not carry with nil.
//
// A record built from a struct omits a field left at its zero value, so a batch built from a slice of
// structs can differ column by column: the first record may carry a timestamp it set while a later one
// carries a pointer column the first left nil. The grammar keys the whole statement off one record —
// CompileInsert and insertBindings both take the columns from the first record, as Laravel's
// compileInsert does — so a column only a later record carries never reaches the statement and its value
// is silently dropped, which shows up as a nil on read-back. Giving every record the union keeps the
// column and inserts NULL for the rows that lack it, the value an absent field already stands for. A
// single record is returned untouched, so a one-row insert stays byte for byte what it was.
func uniformInsertRecords(records []map[string]any) []map[string]any {
	if len(records) < 2 {
		return records
	}

	columns := make(map[string]struct{})
	for _, record := range records {
		for column := range record {
			columns[column] = struct{}{}
		}
	}

	headers := make([]string, 0, len(columns))
	for column := range columns {
		headers = append(headers, column)
	}
	sort.Strings(headers)

	uniform := make([]map[string]any, len(records))
	for index, record := range records {
		row := make(map[string]any, len(headers))
		for _, column := range headers {
			row[column] = record[column]
		}
		uniform[index] = row
	}

	return uniform
}

func (r *Query) buildInsert(values []map[string]any) (string, []any, error) {
	if r.err != nil {
		return "", nil, r.err
	}
	if r.builder.TableSource() == nil {
		return "", nil, errors.DatabaseTableIsRequired
	}

	sql, args := r.builder.ToInsertSQL(uniformInsertRecords(values))
	if err := r.builder.Error(); err != nil {
		return "", nil, err
	}

	return querybuilder.ReplacePlaceholders(sql, r.driver), args, nil
}

func (r *Query) buildUpdate(values map[string]any) (string, []any, error) {
	if r.err != nil {
		return "", nil, r.err
	}
	if r.builder.TableSource() == nil {
		return "", nil, errors.DatabaseTableIsRequired
	}

	sql, args := r.builder.ToUpdateSQL(r.subquery(values))
	if err := r.builder.Error(); err != nil {
		return "", nil, err
	}

	return querybuilder.ReplacePlaceholders(sql, r.driver), args, nil
}

func (r *Query) buildDelete() (string, []any, error) {
	if r.err != nil {
		return "", nil, r.err
	}
	if r.builder.TableSource() == nil {
		return "", nil, errors.DatabaseTableIsRequired
	}

	sql, args := r.builder.ToDeleteSQL()
	if err := r.builder.Error(); err != nil {
		return "", nil, err
	}

	return querybuilder.ReplacePlaceholders(sql, r.driver), args, nil
}

// buildSelectForCount rewrites the select list into a count aggregate. Several columns or none
// count all rows, while one column counts that column. The grammar applies explicit DISTINCT columns.
//
// The count a paginator runs never orders or bounds the rows it counts: Laravel builds it on a clone
// without the orders, limit and offset (Builder::runPaginationCountQuery), so an ordered or limited
// query still counts every matching row. The orders and the bound are dropped here, together with the
// order bindings that would otherwise outnumber the placeholders left in the compiled SQL.
func buildSelectForCount(q *Query) error {
	selects := q.builder.Selects()

	columns := []any{"*"}
	if len(selects) == 1 {
		column := asString(selects[0])
		if str.Of(column).Trim().Contains(" ") {
			column = str.Of(column).Split(" ")[0]
		}
		columns = []any{column}
	}

	q.builder = q.builder.CloneWithout("orders", "limit", "offset").CloneWithoutBindings("order")
	q.builder.SetAggregate("count", columns)

	return nil
}

// --- reads -------------------------------------------------------------------

// Count returns the number of matching rows.
func (r *Query) Count() (int64, error) {
	q := r.clone()
	if err := buildSelectForCount(q); err != nil {
		return 0, err
	}

	sql, args, err := q.buildSelect()
	if err != nil {
		return 0, err
	}

	var count int64
	now := carbon.Now()
	if err := q.readBuilder.GetContext(q.ctx, &count, sql, args...); err != nil {
		q.trace(q.readBuilder, sql, args, now, -1, err)

		return 0, err
	}

	q.trace(q.readBuilder, sql, args, now, -1, nil)

	return count, nil
}

// Exists executes the grammar's existence query, preserving limit and offset semantics.
func (r *Query) Exists() (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	if r.builder.TableSource() == nil && !r.allowTableless {
		return false, errors.DatabaseTableIsRequired
	}
	statement, bindings := r.builder.ToExistsSQL()
	if err := r.builder.Error(); err != nil {
		return false, err
	}
	statement = querybuilder.ReplacePlaceholders(statement, r.driver)
	r.resolveReader()
	var exists bool
	begin := carbon.Now()
	err := r.readBuilder.GetContext(r.ctx, &exists, statement, bindings...)
	if errors.Is(err, databasesql.ErrNoRows) {
		err = nil
	}
	r.trace(r.readBuilder, statement, bindings, begin, -1, err)
	return exists, err
}

func (r *Query) DoesntExist() (bool, error) { exists, err := r.Exists(); return !exists, err }
func (r *Query) ExistsOr(callback func() any) (any, error) {
	exists, err := r.Exists()
	if err != nil {
		return nil, err
	}
	if exists {
		return true, nil
	}
	return callback(), nil
}
func (r *Query) DoesntExistOr(callback func() any) (any, error) {
	exists, err := r.Exists()
	if err != nil {
		return nil, err
	}
	if !exists {
		return true, nil
	}
	return callback(), nil
}

// Get retrieves every matching row into dest.
func (r *Query) Get(dest any) error {
	sql, args, err := r.buildSelect()
	if err != nil {
		return err
	}

	now := carbon.Now()
	if err := r.readBuilder.SelectContext(r.ctx, dest, sql, args...); err != nil {
		r.trace(r.readBuilder, sql, args, now, -1, err)

		return err
	}

	rowsAffected := int64(-1)
	if destValue := reflect.Indirect(reflect.ValueOf(dest)); destValue.Kind() == reflect.Slice {
		rowsAffected = int64(destValue.Len())
	}

	r.trace(r.readBuilder, sql, args, now, rowsAffected, nil)

	return r.applyAfterQuery(dest)
}

// First follows Laravel's limit(1)->get()->first() data semantics.
func (r *Query) First(dest any) error { return r.Limit(1).(*Query).firstResult(dest, nil) }
func (r *Query) FirstOr(dest any, callback func() error) error {
	return r.Limit(1).(*Query).firstResult(dest, callback)
}
func (r *Query) FirstOrFail(dest any) error {
	return r.Limit(1).(*Query).firstResult(dest, func() error { return databasesql.ErrNoRows })
}
func (r *Query) firstResult(dest any, missing func() error) error {
	if r.builder.HasAfterQueryCallbacks() {
		return r.firstTransformed(dest, missing)
	}
	statement, bindings, err := r.buildSelect()
	if err != nil {
		return err
	}
	begin := carbon.Now()
	err = r.readBuilder.GetContext(r.ctx, dest, statement, bindings...)
	if errors.Is(err, databasesql.ErrNoRows) {
		r.trace(r.readBuilder, statement, bindings, begin, 0, nil)
		clearDestination(dest)
		if missing != nil {
			return missing()
		}
		return nil
	}
	if err != nil {
		r.trace(r.readBuilder, statement, bindings, begin, -1, err)
		return err
	}
	r.trace(r.readBuilder, statement, bindings, begin, 1, nil)
	return r.applyAfterQuery(dest)
}

// Find retrieves rows matching the given conditions.
func (r *Query) Find(dest any, conds ...any) error {
	q, err := r.findQuery(conds)
	if err != nil {
		return err
	}

	if reflect.Indirect(reflect.ValueOf(dest)).Kind() == reflect.Slice {
		return q.Get(dest)
	}

	return q.First(dest)
}

// FindOrFail retrieves rows matching the given conditions or fails.
func (r *Query) FindOrFail(dest any, conds ...any) error {
	q, err := r.findQuery(conds)
	if err != nil {
		return err
	}

	if reflect.Indirect(reflect.ValueOf(dest)).Kind() == reflect.Slice {
		return q.Get(dest)
	}

	return q.FirstOrFail(dest)
}

// FindOr retrieves the first row matching the given id, or runs the callback when there is none,
// mirroring Laravel's Builder::findOr.
func (r *Query) FindOr(dest any, id any, callback func() error) error {
	q, err := r.findQuery([]any{id})
	if err != nil {
		return err
	}

	return q.FirstOr(dest, callback)
}

// Value retrieves a single column of the first row.
func (r *Query) Value(column string, dest any) error {
	return r.onceWithColumns([]any{column}, func() error { return r.First(dest) })
}

// RawValue retrieves a single raw expression value from the first row. It mirrors Laravel's
// Builder::rawValue: selectRaw($expression)->first(). Unlike Value, the expression is not wrapped as
// an identifier, so it can carry an arbitrary SQL fragment such as UPPER("foo").
func (r *Query) RawValue(expression string, dest any) error {
	return r.SelectRaw(expression).First(dest)
}

// Pluck retrieves a single column of every row.
func (r *Query) Pluck(column string, dest any) error {
	return r.onceWithColumns([]any{column}, func() error { return r.Get(dest) })
}

// Implode concatenates the values of a single column into a string separated by glue. It mirrors
// Laravel's Builder::implode, which plucks the column and joins the plucked values.
func (r *Query) Implode(column string, glue string, dest any) error {
	var rows []map[string]any
	if err := r.Pluck(column, &rows); err != nil {
		return err
	}

	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() || destValue.Elem().Kind() != reflect.String {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer to string")
	}

	key := stripTableForPluck(column)
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if value, ok := row[key]; ok && value != nil {
			values = append(values, fmt.Sprintf("%v", value))
		}
	}

	destValue.Elem().SetString(strings.Join(values, glue))

	return nil
}

// stripTableForPluck strips the table name or alias from a column identifier, mirroring Laravel's
// Builder::stripTableForPluck: a plucked result set is keyed by the bare column name only.
func stripTableForPluck(column string) string {
	lower := strings.ToLower(column)
	separator := "."
	if strings.Contains(lower, " as ") {
		separator = " as "
	}

	if index := strings.LastIndex(lower, separator); index != -1 {
		return column[index+len(separator):]
	}

	return column
}

// Sum aggregates a column with SUM.
func (r *Query) Sum(column string, dest any) error { return r.aggregate("sum", column, dest) }

// Avg aggregates a column with AVG.
func (r *Query) Avg(column string, dest any) error { return r.aggregate("avg", column, dest) }

// Min aggregates a column with MIN.
func (r *Query) Min(column string, dest any) error { return r.aggregate("min", column, dest) }

// Max aggregates a column with MAX.
func (r *Query) Max(column string, dest any) error { return r.aggregate("max", column, dest) }

func (r *Query) aggregate(function string, column string, dest any) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer {
		return errors.DatabaseUnsupportedType.Args(destValue.Kind(), "pointer")
	}

	q := r.clone()
	q.builder.SetAggregate(function, []any{column})

	var rows []map[string]any
	if err := q.Get(&rows); err != nil {
		return err
	}
	var value any
	if len(rows) > 0 {
		value = rows[0]["aggregate"]
	}
	if value == nil && function == "sum" {
		value = int64(0)
	}
	return scanAggregate(value, dest)
}

// Paginate retrieves one page of rows, writing the total into total when it is not nil. It mirrors
// Laravel's Builder::paginate: a non-positive page and limit fall back to the defaults of page 1 and 15
// rows, a total already carried by the caller is an override that skips the count, and a zero total skips
// the page fetch (Laravel returns an empty collection when the count is zero).
func (r *Query) Paginate(page, limit int, dest any, total *int64) error {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 15
	}

	if page-1 > int(^uint(0)>>1)/limit {
		return errors.QueryPaginationOverflow
	}
	offset := (page - 1) * limit

	// Resolve the total exactly as Laravel's Builder::paginate does: a non-zero total already carried
	// by the caller stands for its $total argument and short-circuits getCountForPagination(); only a
	// zero (unset) total is counted, and the count is written back when the caller asked for it.
	resolvedTotal := int64(0)
	if total != nil {
		resolvedTotal = *total
	}
	if resolvedTotal == 0 {
		count, err := r.GetCountForPagination()
		if err != nil {
			return err
		}

		resolvedTotal = count
		if total != nil {
			*total = count
		}
	}

	// The zero-count short-circuit is decided on the RESOLVED total, not on whether an out-parameter
	// was supplied, so a nil-total query whose count is zero still skips forPage()/get() as Laravel's
	// `$results = $total ? ... : new Collection` does.
	if resolvedTotal == 0 {
		return emptyPaginatedDest(dest)
	}

	return r.Offset(uint64(offset)).Limit(uint64(limit)).Get(dest)
}

// emptyPaginatedDest clears a slice destination when the count is zero, mirroring Laravel's paginate(), which
// skips forPage()/get() and returns an empty collection in that case. A destination that is not a pointer to
// a slice is left untouched.
func emptyPaginatedDest(dest any) error {
	value := reflect.ValueOf(dest)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil
	}

	elem := value.Elem()
	if elem.Kind() != reflect.Slice {
		return nil
	}

	elem.Set(reflect.MakeSlice(elem.Type(), 0, 0))

	return nil
}

// CursorPaginate fetches one keyset ("seek") page and scans it into dest, which must be a pointer to
// a slice: a *[]map[string]any for an untyped page, or a pointer to a slice of structs for a typed
// one. cursor is the encoded anchor — an empty string is the first page, and a non-empty malformed
// string is refused with errors.QueryCursorInvalid rather than silently restarting at the first page.
//
// It mirrors Paginate but never counts: the page carries no total. The builder entry does the SQL
// work — the keyset predicate, the per-column order flip for a backwards cursor and the perPage+1
// probe limit; this method executes that statement once and projects the scanned rows onto the
// paginator. The name is deliberately distinct from Cursor, which streams rows.
func (r *Query) CursorPaginate(perPage int, cursor string, dest any) (db.CursorPaginator, error) {
	anchor, err := decodeCursorAnchor(cursor)
	if err != nil {
		return db.CursorPaginator{}, err
	}

	// The builder entry mutates the builder (it reverses the order in place and overwrites the
	// limit), and applies those clauses to this builder like Laravel.

	page, err := r.builder.CursorPaginate(perPage, anchor)
	if err != nil {
		return db.CursorPaginator{}, err
	}

	sql, args, err := r.buildSelect()
	if err != nil {
		return db.CursorPaginator{}, err
	}

	items, err := r.cursorRows(sql, args, dest)
	if err != nil {
		return db.CursorPaginator{}, err
	}

	return cursorPaginatorPage(querybuilder.NewCursorPaginator(items, page.PerPage, page.Cursor, page.HasCursor, page.Parameters)), nil
}

// decodeCursorAnchor turns the facade's encoded cursor string into the builder entry's anchor: an
// empty string is the first page (a nil cursor), and any other string must decode.
func decodeCursorAnchor(cursor string) (*querybuilder.Cursor, error) {
	if cursor == "" {
		return nil, nil
	}

	decoded, err := querybuilder.DecodeCursor(cursor)
	if err != nil {
		return nil, err
	}

	return &decoded, nil
}

// cursorRows runs the compiled statement once, collecting the page as column-keyed maps and decoding
// the same rows into dest.
func (r *Query) cursorRows(sql string, args []any, dest any) ([]map[string]any, error) {
	items := []map[string]any{}

	now := carbon.Now()
	if err := r.readBuilder.SelectContext(r.ctx, &items, sql, args...); err != nil {
		r.trace(r.readBuilder, sql, args, now, -1, err)

		return nil, err
	}

	r.trace(r.readBuilder, sql, args, now, int64(len(items)), nil)

	if err := assignCursorRows(dest, items); err != nil {
		return nil, err
	}

	return items, nil
}

// assignCursorRows fills the caller's destination from the scanned page. A *[]map[string]any receives
// the rows as they are; any other pointer to a slice has each row decoded into its element type by
// the framework's row scanner, so a typed page and the paginator's own map-keyed rows come from the
// same single execution.
func assignCursorRows(dest any, items []map[string]any) error {
	if dest == nil {
		return errors.DatabaseUnsupportedType.Args("nil", "pointer to slice")
	}

	if typed, ok := dest.(*[]map[string]any); ok {
		*typed = items

		return nil
	}

	target := reflect.ValueOf(dest)
	if target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Slice {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer to slice")
	}

	elementType := target.Elem().Type().Elem()
	slice := reflect.MakeSlice(target.Elem().Type(), 0, len(items))
	for _, item := range items {
		if elementType.Kind() == reflect.Pointer {
			element := reflect.New(elementType.Elem())
			if err := NewRow(item, nil).Scan(element.Interface()); err != nil {
				return err
			}

			slice = reflect.Append(slice, element)

			continue
		}

		element := reflect.New(elementType).Elem()
		if err := NewRow(item, nil).Scan(element.Addr().Interface()); err != nil {
			return err
		}

		slice = reflect.Append(slice, element)
	}

	target.Elem().Set(slice)

	return nil
}

// cursorPaginatorPage projects the query builder's paginator onto the contract's page value. A cursor
// end that is absent, or that failed to mint, stays nil, and the ToMap of the returned page then
// reports it as null.
func cursorPaginatorPage(page querybuilder.CursorPaginator) db.CursorPaginator {
	result := db.CursorPaginator{
		Items:        page.Items,
		PerPage:      page.PerPage,
		HasMorePages: page.HasMorePages(),
	}

	if encoded, ok, err := page.NextCursorEncoded(); ok && err == nil {
		result.NextCursor = &encoded
	}
	if encoded, ok, err := page.PreviousCursorEncoded(); ok && err == nil {
		result.PreviousCursor = &encoded
	}

	return result
}

// Cursor streams the matching rows.
func (r *Query) Cursor() chan db.Row {
	ch := make(chan db.Row)

	go func() {
		var (
			args  []any
			count int64 = -1
			err   error
			now   = carbon.Now()
			rows  *sqlx.Rows
			sql   string
		)

		defer func() {
			if err != nil {
				ch <- NewRow(nil, err)
			}

			if len(sql) > 0 {
				r.trace(r.readBuilder, sql, args, now, count, err)
			}

			close(ch)
		}()

		if sql, args, err = r.buildSelect(); err != nil {
			return
		}

		if rows, err = r.readBuilder.QueryxContext(r.ctx, sql, args...); err != nil {
			return
		}
		if rows == nil {
			count = 0
			return
		}
		defer errors.Ignore(rows.Close)

		for rows.Next() {
			row := make(map[string]any)
			if err = rows.MapScan(row); err != nil {
				return
			}

			ch <- NewRow(row, nil)
			count++
		}

		if err = rows.Err(); err != nil {
			return
		}
	}()

	return ch
}

// Chunk follows BuildsQueries::chunk, including the caller's starting offset and limit.
func (r *Query) Chunk(size uint64, callback func(rows []db.Row) error) error {
	if len(r.builder.State().Orders) == 0 && len(r.builder.State().UnionOrders) == 0 {
		return errors.QueryOrderRequired
	}
	if size == 0 {
		return nil
	}
	max := int(^uint(0) >> 1)
	if size > uint64(max) {
		return errors.QueryPaginationOverflow
	}
	skip, _ := r.builder.OffsetValue()
	remaining, limited := r.builder.LimitValue()
	for offset := skip; ; {
		limit := int(size)
		if limited && remaining < limit {
			limit = remaining
		}
		if limit <= 0 {
			return nil
		}
		r.builder.Offset(offset).Limit(limit)
		_, rows, err := r.collectChunk()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if limited {
			remaining -= len(rows)
		}
		if err := callback(rows); err != nil {
			if errors.Is(err, errors.DatabaseChunkStopped) {
				return nil
			}
			return err
		}
		if len(rows) != int(size) {
			return nil
		}
		if offset > max-int(size) {
			return errors.QueryPaginationOverflow
		}
		offset += int(size)
	}
}

// ChunkById chunks the query by comparing ids, mirroring Laravel's BuildsQueries::chunkById. Each
// page is fetched with ForPageAfterId (ascending) using the last id of the previous page as the
// keyset, so a large result set is walked without the growing offset Chunk pays. The optional column
// is the key column (default "id"); an optional second value names the result-key alias the id is
// read from (default the column itself). A callback returning errors.DatabaseChunkStopped halts the
// loop cleanly, mirroring Laravel's `return false`.
func (r *Query) ChunkById(size uint64, callback func(rows []db.Row) error, column ...string) error {
	return r.chunkById(size, callback, false, column...)
}

// ChunkByIdDesc is ChunkById in descending id order, mirroring Laravel's
// BuildsQueries::chunkByIdDesc: each page is fetched with ForPageBeforeId.
func (r *Query) ChunkByIdDesc(size uint64, callback func(rows []db.Row) error, column ...string) error {
	return r.chunkById(size, callback, true, column...)
}

// chunkById is the shared keyset loop behind ChunkById and ChunkByIdDesc. It mirrors
// BuildsQueries::orderedChunkById: a caller's offset and limit are honoured (the first page keeps the
// offset, later pages drop it, and the limit caps the page size), a zero-size chunk breaks before any
// query is issued, and the loop continues only while a complete page came back. The id of the last
// row of each page is read from the result under alias and drives the next page's keyset predicate.
func (r *Query) chunkById(size uint64, callback func(rows []db.Row) error, descending bool, column ...string) error {
	keyColumn := "id"
	if len(column) > 0 && column[0] != "" {
		keyColumn = column[0]
	}
	alias := keyColumn
	if len(column) > 1 && column[1] != "" {
		alias = column[1]
	}

	var lastID any

	skip, hasSkip := r.builder.OffsetValue()
	remaining, hasRemaining := r.builder.LimitValue()
	page := 1

	for {
		q := r.clone()
		if hasSkip && skip > 0 && page > 1 {
			q.builder.Offset(0)
		}

		limit := int(size)
		if hasRemaining && remaining < limit {
			limit = remaining
		}
		if limit == 0 {
			break
		}

		if descending {
			q.builder.ForPageBeforeId(limit, lastID, keyColumn)
		} else {
			q.builder.ForPageAfterId(limit, lastID, keyColumn)
		}

		items, rows, err := q.collectChunk()
		if err != nil {
			return err
		}

		count := len(rows)
		if count == 0 {
			break
		}

		if hasRemaining {
			remaining -= count
			if remaining < 0 {
				remaining = 0
			}
		}

		if err := callback(rows); err != nil {
			if errors.Is(err, errors.DatabaseChunkStopped) {
				return nil
			}

			return err
		}

		lastValue := items[count-1][alias]
		if lastValue == nil {
			return errors.DatabaseChunkByIdColumnNotPresent.Args(alias)
		}
		lastID = lastValue

		page++

		if count != int(size) {
			break
		}
	}

	return nil
}

// collectChunk runs the built statement as one keyset page, returning the rows as column-keyed maps
// (for reading the keyset id) alongside the db.Row facade values handed to the callback.
func (r *Query) collectChunk() ([]map[string]any, []db.Row, error) {
	var items []map[string]any
	if err := r.Get(&items); err != nil {
		return nil, nil, err
	}
	rows := make([]db.Row, len(items))
	for i, item := range items {
		rows[i] = NewRow(item, nil)
	}
	return items, rows, nil
}

// Each iterates the results one row at a time.
func (r *Query) Each(callback func(row db.Row) error) error {
	return r.Chunk(1, func(rows []db.Row) error {
		for _, row := range rows {
			if err := callback(row); err != nil {
				return err
			}
		}

		return nil
	})
}

// --- writes ------------------------------------------------------------------

// Insert inserts one or many rows.
func (r *Query) Insert(data any) (*db.Result, error) {
	if emptyBatch(data) {
		return &db.Result{}, nil
	}
	mapData, err := convertToSliceMap(data)
	if err != nil {
		return nil, err
	}
	if len(mapData) == 0 {
		return nil, errors.DatabaseDataIsEmpty
	}

	sql, args, err := r.buildInsert(mapData)
	if err != nil {
		return nil, err
	}

	now := carbon.Now()
	result, err := r.writeBuilder.ExecContext(r.ctx, sql, args...)
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return nil, err
	}

	r.trace(r.writeBuilder, sql, args, now, rowsAffected, nil)

	return &db.Result{RowsAffected: rowsAffected}, nil
}

// InsertGetID inserts one row and returns its generated id.
func (r *Query) InsertGetID(data any, sequence ...string) (int64, error) {
	mapData, err := convertToMap(data)
	if err != nil {
		return 0, err
	}
	if len(mapData) == 0 {
		return 0, errors.DatabaseUnsupportedType.Args("nil", "struct, map[string]any").SetModule("DB")
	}
	if r.err != nil {
		return 0, r.err
	}
	if r.builder.TableSource() == nil {
		return 0, errors.DatabaseTableIsRequired
	}

	id, err := r.builder.InsertGetID(&insertConnection{query: r}, []map[string]any{mapData}, firstString(sequence, ""))
	if err != nil {
		return 0, err
	}
	return id.(int64), nil
}

// Update updates rows with the given column and value, or with a map of columns.
func (r *Query) Update(column any, value ...any) (*db.Result, error) {
	columnStr, ok := column.(string)
	if ok {
		if len(value) != 1 {
			return nil, errors.DatabaseInvalidArgumentNumber.Args(len(value), "1")
		}

		return r.Update(map[string]any{columnStr: value[0]})
	}

	mapData, err := convertToMap(column)
	if err != nil {
		return nil, err
	}

	sql, args, err := r.buildUpdate(mapData)
	if err != nil {
		return nil, err
	}

	now := carbon.Now()
	result, err := r.writeBuilder.ExecContext(r.ctx, sql, args...)
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return nil, err
	}

	r.trace(r.writeBuilder, sql, args, now, rowsAffected, nil)

	return &db.Result{RowsAffected: rowsAffected}, nil
}

// UpdateOrInsert updates the first matching row, or inserts a new one.
func (r *Query) UpdateOrInsert(attributes any, values any) (*db.Result, error) {
	where, err := convertToMap(attributes)
	if err != nil {
		return nil, err
	}
	query := r.Where(where)
	exists, err := query.Exists()
	if err != nil {
		return nil, err
	}
	switch callback := values.(type) {
	case func(bool) any:
		values = callback(exists)
	case func(bool) map[string]any:
		values = callback(exists)
	case func(bool) (map[string]any, error):
		values, err = callback(exists)
		if err != nil {
			return nil, err
		}
	}
	assignments, err := convertToMap(values)
	if err != nil {
		return nil, err
	}
	if exists {
		if len(assignments) == 0 {
			return &db.Result{}, nil
		}
		return query.Limit(1).Update(assignments)
	}
	insert := make(map[string]any, len(where)+len(assignments))
	for key, value := range where {
		insert[key] = value
	}
	for key, value := range assignments {
		insert[key] = value
	}
	return r.Insert(insert)
}

// Increment adds to a numeric column.
func (r *Query) Increment(column string, value ...uint64) error {
	amount := uint64(1)
	if len(value) > 0 {
		amount = value[0]
	}

	_, err := r.Update(map[string]any{
		column: querybuilder.Raw(r.grammar.Wrap(column) + " + " + strconv.FormatUint(amount, 10)),
	})

	return err
}

// Decrement subtracts from a numeric column.
func (r *Query) Decrement(column string, value ...uint64) error {
	amount := uint64(1)
	if len(value) > 0 {
		amount = value[0]
	}

	_, err := r.Update(map[string]any{
		column: querybuilder.Raw(r.grammar.Wrap(column) + " - " + strconv.FormatUint(amount, 10)),
	})

	return err
}

// Delete deletes every matching row.
func (r *Query) Delete() (*db.Result, error) {
	sql, args, err := r.buildDelete()
	if err != nil {
		return nil, err
	}

	now := carbon.Now()
	result, err := r.writeBuilder.ExecContext(r.ctx, sql, args...)
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return nil, err
	}

	r.trace(r.writeBuilder, sql, args, now, rowsAffected, nil)

	return &db.Result{RowsAffected: rowsAffected}, nil
}

// ToSql returns the query as a parameterised SQL string.
func (r *Query) ToSql() db.ToSql { return NewToSql(r, false) }

// ToRawSql returns the query as a SQL string with the bindings inlined.
func (r *Query) ToRawSql() db.ToSql { return NewToSql(r, true) }

// --- internals ---------------------------------------------------------------

// findQuery applies the Find shorthand: one condition is an id, two are column and value.
func (r *Query) findQuery(conds []any) (db.Query, error) {
	var q db.Query

	switch {
	case len(conds) > 2:
		return nil, errors.DatabaseInvalidArgumentNumber.Args(len(conds), "1 or 2")
	case len(conds) == 1:
		q = r.Where("id", conds...)
	case len(conds) == 2:
		q = r.Where(conds[0], conds[1])
	default:
		q = r
	}

	return q, nil
}

func (r *Query) trace(builder db.CommonBuilder, sql string, args []any, now *carbon.Carbon, rowsAffected int64, err error) {
	if r.txLogs != nil {
		*r.txLogs = append(*r.txLogs, TxLog{
			ctx:          r.ctx,
			begin:        now,
			sql:          builder.Explain(sql, args...),
			rowsAffected: rowsAffected,
			err:          err,
		})
	} else {
		r.logger.Trace(r.ctx, now, builder.Explain(sql, args...), rowsAffected, err)
	}
}

// columnComparison normalises the WhereColumn trailing arguments.
func columnComparison(column2 []string) (string, string, error) {
	switch len(column2) {
	case 1:
		return "=", column2[0], nil
	case 2:
		return column2[0], column2[1], nil
	default:
		return "", "", errors.DatabaseInvalidArgumentNumber.Args(len(column2), "1 or 2")
	}
}

// toStrings converts a string slice to []any for the builder.
func toStrings(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}

	return out
}

// asString renders a value as a string.
func asString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}

	return fmt.Sprint(value)
}

// expandInGroup renders a WhereAny / WhereAll / WhereNone whose operator is IN or NOT IN against a
// slice as one IN clause per column, the shape the sibling WhereIn entry point already gives and
// the ORM's WhereAny now produces. innerBoolean is the connector between the columns and
// groupBoolean the connector that queues the whole group (whereNone passes "and not" to negate it).
//
// Without this the operator reached the builder's where() path, where the invalid-operator shortcut
// swapped it to "=" and bound the literal string "IN", so the group silently matched nothing and the
// DB facade disagreed with the ORM about the same call. Laravel's own whereAny performs that same
// swap, so this is a deliberate divergence from Laravel, taken to keep the two facades consistent.
//
// It reports false when the condition is not an IN / NOT IN against a slice (a scalar, a subquery,
// a nil, or any other operator), so the caller falls back to the ordinary path unchanged.
func (r *Query) expandInGroup(columns []string, operator, value any, innerBoolean, groupBoolean string) bool {
	not, ok := inOperator(operator)
	if !ok {
		return false
	}

	values, ok := sliceValues(value)
	if !ok {
		return false
	}

	r.builder.WhereNested(func(b *querybuilder.Builder) *querybuilder.Builder {
		for _, column := range columns {
			b.WhereIn(column, values, innerBoolean, not)
		}

		return b
	}, groupBoolean)

	return true
}

// inOperator reports whether operator is IN or NOT IN, ignoring case and surrounding space, and
// returns whether it is the negated (NOT IN) form.
func inOperator(operator any) (not bool, ok bool) {
	op, isString := operator.(string)
	if !isString {
		return false, false
	}

	switch strings.ToLower(strings.TrimSpace(op)) {
	case "in":
		return false, true
	case "not in":
		return true, true
	default:
		return false, false
	}
}

// sliceValues returns the elements of a slice or array value, dereferencing a pointer or interface
// first, or false for anything else. Mirrors the ORM WhereAny family's own slice detection.
func sliceValues(value any) ([]any, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil, false
	}

	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return nil, false
		}

		reflected = reflected.Elem()
	}

	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return nil, false
	}

	values := make([]any, reflected.Len())
	for i := range values {
		values[i] = reflected.Index(i).Interface()
	}

	return values, true
}
