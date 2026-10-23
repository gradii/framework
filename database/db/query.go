package db

import (
	"context"
	databasesql "database/sql"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/goravel/framework/contracts/database/db"
	"github.com/goravel/framework/contracts/database/logger"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/database/utils"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/support/str"
)

// Query is the DB facade query builder. All SQL is produced by database/query, a port of
// Illuminate\Database's builder and its grammars; this type adapts that builder to the framework's
// db.Query contract and executes through CommonBuilder, keeping the tracing and transaction
// behaviour unchanged.
type Query struct {
	ctx          context.Context
	err          error
	grammar      querybuilder.Grammar
	driver       string
	logger       logger.Logger
	readBuilder  db.CommonBuilder
	writeBuilder db.CommonBuilder
	txLogs       *[]TxLog
	builder      *querybuilder.Builder
}

// NewQuery creates a query bound to a table. grammar selects the dialect and driver is its name,
// used to convert placeholders at execution time.
func NewQuery(ctx context.Context, readBuilder db.CommonBuilder, writeBuilder db.CommonBuilder, grammar querybuilder.Grammar, driver string, logger logger.Logger, table string, txLogs *[]TxLog) *Query {
	builder := querybuilder.NewBuilder(grammar)
	builder.SetDriver(driver)
	if table != "" {
		builder.From(table, "")
	}

	return &Query{
		ctx:          ctx,
		grammar:      grammar,
		driver:       driver,
		logger:       logger,
		readBuilder:  readBuilder,
		writeBuilder: writeBuilder,
		txLogs:       txLogs,
		builder:      builder,
	}
}

// clone returns a copy sharing the execution fields but with its own builder, preserving the
// immutable chaining the DB facade has always had.
func (r *Query) clone() *Query {
	c := *r
	c.builder = r.builder.Clone()

	return &c
}

// --- where -------------------------------------------------------------------

// Where adds a where clause. Mirrors db.Query::Where.
func (r *Query) Where(query any, args ...any) db.Query {
	return r.clone().applyWhere(query, args, querybuilder.BooleanAnd)
}

// OrWhere adds an or where clause.
func (r *Query) OrWhere(query any, args ...any) db.Query {
	return r.clone().applyWhere(query, args, querybuilder.BooleanOr)
}

// WhereNot adds a negated where clause.
func (r *Query) WhereNot(query any, args ...any) db.Query {
	return r.clone().applyWhere(query, args, querybuilder.BooleanAndNot)
}

// OrWhereNot adds a negated or where clause.
func (r *Query) OrWhereNot(query any, args ...any) db.Query {
	return r.clone().applyWhere(query, args, querybuilder.BooleanOrNot)
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
	case querybuilder.BooleanOr:
		r.builder.OrWhere(query, args...)
	case querybuilder.BooleanAndNot:
		r.builder.WhereNot(query, args...)
	case querybuilder.BooleanOrNot:
		r.builder.OrWhereNot(query, args...)
	default:
		r.builder.Where(query, args...)
	}
}

// nested runs a facade callback against a child builder and returns the builder it produced.
func (r *Query) nested(callback func(db.Query) db.Query, b *querybuilder.Builder) *querybuilder.Builder {
	child := &Query{
		ctx:          r.ctx,
		grammar:      r.grammar,
		driver:       r.driver,
		logger:       r.logger,
		readBuilder:  r.readBuilder,
		writeBuilder: r.writeBuilder,
		txLogs:       r.txLogs,
		builder:      b,
	}

	if result, ok := callback(child).(*Query); ok {
		return result.builder
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
	q := r.clone()
	q.builder.WhereRaw(raw, args, querybuilder.BooleanAnd)

	return q
}

// OrWhereRaw adds a raw or where clause.
func (r *Query) OrWhereRaw(raw string, args []any) db.Query {
	q := r.clone()
	q.builder.WhereRaw(raw, args, querybuilder.BooleanOr)

	return q
}

// WhereIn adds an in clause.
func (r *Query) WhereIn(column string, values []any) db.Query {
	q := r.clone()
	q.builder.WhereIn(column, values, querybuilder.BooleanAnd, false)

	return q
}

// OrWhereIn adds an or in clause.
func (r *Query) OrWhereIn(column string, values []any) db.Query {
	q := r.clone()
	q.builder.WhereIn(column, values, querybuilder.BooleanOr, false)

	return q
}

// WhereNotIn adds a not in clause.
func (r *Query) WhereNotIn(column string, values []any) db.Query {
	q := r.clone()
	q.builder.WhereNotIn(column, values, querybuilder.BooleanAnd)

	return q
}

// OrWhereNotIn adds an or not in clause.
func (r *Query) OrWhereNotIn(column string, values []any) db.Query {
	q := r.clone()
	q.builder.WhereNotIn(column, values, querybuilder.BooleanOr)

	return q
}

// WhereBetween adds a between clause.
func (r *Query) WhereBetween(column string, x, y any) db.Query {
	q := r.clone()
	q.builder.WhereBetween(column, []any{x, y}, querybuilder.BooleanAnd, false)

	return q
}

// OrWhereBetween adds an or between clause.
func (r *Query) OrWhereBetween(column string, x, y any) db.Query {
	q := r.clone()
	q.builder.WhereBetween(column, []any{x, y}, querybuilder.BooleanOr, false)

	return q
}

// WhereNotBetween adds a not between clause.
func (r *Query) WhereNotBetween(column string, x, y any) db.Query {
	q := r.clone()
	q.builder.WhereBetween(column, []any{x, y}, querybuilder.BooleanAnd, true)

	return q
}

// OrWhereNotBetween adds an or not between clause.
func (r *Query) OrWhereNotBetween(column string, x, y any) db.Query {
	q := r.clone()
	q.builder.WhereBetween(column, []any{x, y}, querybuilder.BooleanOr, true)

	return q
}

// WhereNull adds an is null clause.
func (r *Query) WhereNull(column string) db.Query {
	q := r.clone()
	q.builder.WhereNull(column, querybuilder.BooleanAnd, false)

	return q
}

// OrWhereNull adds an or is null clause.
func (r *Query) OrWhereNull(column string) db.Query {
	q := r.clone()
	q.builder.WhereNull(column, querybuilder.BooleanOr, false)

	return q
}

// WhereNotNull adds an is not null clause.
func (r *Query) WhereNotNull(column string) db.Query {
	q := r.clone()
	q.builder.WhereNotNull(column, querybuilder.BooleanAnd)

	return q
}

// OrWhereNotNull adds an or is not null clause.
func (r *Query) OrWhereNotNull(column string) db.Query {
	q := r.clone()
	q.builder.WhereNotNull(column, querybuilder.BooleanOr)

	return q
}

// WhereColumn compares two columns. One trailing column means equality, two mean operator then
// column.
func (r *Query) WhereColumn(column1 string, column2 ...string) db.Query {
	q := r.clone()
	operator, second, err := columnComparison(column2)
	if err != nil {
		q.err = err
		return q
	}
	q.builder.WhereColumn(column1, operator, second, querybuilder.BooleanAnd)

	return q
}

// OrWhereColumn compares two columns with or.
func (r *Query) OrWhereColumn(column1 string, column2 ...string) db.Query {
	q := r.clone()
	operator, second, err := columnComparison(column2)
	if err != nil {
		q.err = err
		return q
	}
	q.builder.WhereColumn(column1, operator, second, querybuilder.BooleanOr)

	return q
}

// WhereLike adds a like clause.
func (r *Query) WhereLike(column string, value string) db.Query {
	q := r.clone()
	q.builder.WhereLike(column, value, false, querybuilder.BooleanAnd, false)

	return q
}

// OrWhereLike adds an or like clause.
func (r *Query) OrWhereLike(column string, value string) db.Query {
	q := r.clone()
	q.builder.WhereLike(column, value, false, querybuilder.BooleanOr, false)

	return q
}

// WhereNotLike adds a not like clause.
func (r *Query) WhereNotLike(column string, value string) db.Query {
	q := r.clone()
	q.builder.WhereLike(column, value, false, querybuilder.BooleanAnd, true)

	return q
}

// OrWhereNotLike adds an or not like clause.
func (r *Query) OrWhereNotLike(column string, value string) db.Query {
	q := r.clone()
	q.builder.WhereLike(column, value, false, querybuilder.BooleanOr, true)

	return q
}

// WhereExists adds an exists clause.
func (r *Query) WhereExists(query func() db.Query) db.Query {
	q := r.clone()
	if sub, ok := query().(*Query); ok {
		q.builder.WhereExists(sub.builder, querybuilder.BooleanAnd, false)
	}

	return q
}

// WhereJsonContains adds a JSON contains clause.
func (r *Query) WhereJsonContains(column string, value any) db.Query {
	q := r.clone()
	q.builder.WhereJsonContains(column, value, querybuilder.BooleanAnd, false)

	return q
}

// OrWhereJsonContains adds an or JSON contains clause.
func (r *Query) OrWhereJsonContains(column string, value any) db.Query {
	q := r.clone()
	q.builder.WhereJsonContains(column, value, querybuilder.BooleanOr, false)

	return q
}

// WhereJsonDoesntContain adds a negated JSON contains clause.
func (r *Query) WhereJsonDoesntContain(column string, value any) db.Query {
	q := r.clone()
	q.builder.WhereJsonContains(column, value, querybuilder.BooleanAnd, true)

	return q
}

// OrWhereJsonDoesntContain adds a negated or JSON contains clause.
func (r *Query) OrWhereJsonDoesntContain(column string, value any) db.Query {
	q := r.clone()
	q.builder.WhereJsonContains(column, value, querybuilder.BooleanOr, true)

	return q
}

// WhereJsonContainsKey adds a JSON key existence clause.
func (r *Query) WhereJsonContainsKey(column string) db.Query {
	q := r.clone()
	q.builder.WhereJsonContainsKey(column, querybuilder.BooleanAnd, false)

	return q
}

// OrWhereJsonContainsKey adds an or JSON key existence clause.
func (r *Query) OrWhereJsonContainsKey(column string) db.Query {
	q := r.clone()
	q.builder.WhereJsonContainsKey(column, querybuilder.BooleanOr, false)

	return q
}

// WhereJsonDoesntContainKey adds a negated JSON key existence clause.
func (r *Query) WhereJsonDoesntContainKey(column string) db.Query {
	q := r.clone()
	q.builder.WhereJsonContainsKey(column, querybuilder.BooleanAnd, true)

	return q
}

// OrWhereJsonDoesntContainKey adds a negated or JSON key existence clause.
func (r *Query) OrWhereJsonDoesntContainKey(column string) db.Query {
	q := r.clone()
	q.builder.WhereJsonContainsKey(column, querybuilder.BooleanOr, true)

	return q
}

// WhereJsonLength compares a JSON array length. Mirrors Laravel's
// Builder::whereJsonLength: args is either a single length (equality) or an
// operator and a length.
func (r *Query) WhereJsonLength(column string, args ...any) db.Query {
	q := r.clone()
	q.builder.WhereJsonLength(column, args...)

	return q
}

// OrWhereJsonLength compares a JSON array length with or. Mirrors Laravel's
// Builder::orWhereJsonLength: args is either a single length (equality) or an
// operator and a length.
func (r *Query) OrWhereJsonLength(column string, args ...any) db.Query {
	q := r.clone()
	q.builder.OrWhereJsonLength(column, args...)

	return q
}

// WhereAll repeats one condition across columns joined by and.
func (r *Query) WhereAll(columns []string, args ...any) db.Query {
	operator, value, err := utils.PrepareWhereOperatorAndValue(args...)
	q := r.clone()
	if err != nil {
		q.err = err
		return q
	}
	if q.expandInGroup(columns, operator, value, querybuilder.BooleanAnd, querybuilder.BooleanAnd) {
		return q
	}

	q.builder.WhereAll(toStrings(columns), operator, value, querybuilder.BooleanAnd)

	return q
}

// WhereAny repeats one condition across columns joined by or, and joins that group to the
// clauses before it with and - the group connector Laravel's whereAny defaults to; the or lives
// inside the group.
func (r *Query) WhereAny(columns []string, args ...any) db.Query {
	operator, value, err := utils.PrepareWhereOperatorAndValue(args...)
	q := r.clone()
	if err != nil {
		q.err = err
		return q
	}
	if q.expandInGroup(columns, operator, value, querybuilder.BooleanOr, querybuilder.BooleanAnd) {
		return q
	}

	q.builder.WhereAny(toStrings(columns), operator, value, querybuilder.BooleanAnd)

	return q
}

// WhereNone negates a condition repeated across columns.
func (r *Query) WhereNone(columns []string, args ...any) db.Query {
	operator, value, err := utils.PrepareWhereOperatorAndValue(args...)
	q := r.clone()
	if err != nil {
		q.err = err
		return q
	}
	if q.expandInGroup(columns, operator, value, querybuilder.BooleanOr, querybuilder.BooleanAnd+" not") {
		return q
	}

	q.builder.WhereNone(toStrings(columns), operator, value, querybuilder.BooleanAnd)

	return q
}

// --- select and clauses ------------------------------------------------------

// Select replaces the selected columns and their bindings, mirroring Laravel's select().
func (r *Query) Select(columns ...string) db.Query {
	q := r.clone()

	if len(columns) == 0 {
		columns = []string{"*"}
	}
	selects := make([]any, 0, len(columns))
	for _, column := range columns {
		selects = append(selects, column)
	}
	q.builder.Select(selects...)

	return q
}

// Distinct preserves the select list and sets the distinct columns, mirroring Laravel's distinct().
func (r *Query) Distinct(columns ...string) db.Query {
	q := r.clone()

	distinctColumns := make([]any, len(columns))
	for i, column := range columns {
		distinctColumns[i] = column
	}
	q.builder.Distinct(distinctColumns...)

	return q
}

// GroupBy appends grouping columns.
func (r *Query) GroupBy(column ...string) db.Query {
	q := r.clone()
	q.builder.GroupBy(toStrings(column)...)

	return q
}

// Having adds a having clause.
//
// As with applyWhere, a string that carries a binding placeholder is a raw fragment rather than a
// column name, so it is routed to HavingRaw; anything else keeps the builder's Laravel-faithful
// Having, which takes a column, an operator and a value (or a raw expression).
func (r *Query) Having(query any, args ...any) db.Query {
	q := r.clone()
	q.applyHaving(query, args)

	return q
}

// applyHaving routes the DB facade's Having shorthands onto the builder. Laravel's Having takes a
// column, an operator and a value (or a raw expression); Goravel additionally accepts the
// string-with-placeholder form GORM allowed, which is a raw fragment and must keep its placeholders
// and bindings intact. This mirrors applyWhere's placeholder handling for Where.
func (r *Query) applyHaving(query any, args []any) {
	if value, ok := query.(string); ok && strings.Contains(value, "?") {
		r.builder.HavingRaw(value, args, querybuilder.BooleanAnd)

		return
	}

	r.builder.Having(query, args...)
}

// OrderBy adds an order clause.
func (r *Query) OrderBy(column string, directions ...string) db.Query {
	direction := "asc"
	if len(directions) > 0 {
		direction = directions[0]
	}

	q := r.clone()
	q.builder.OrderBy(column, direction)

	return q
}

// OrderByDesc adds a descending order clause.
func (r *Query) OrderByDesc(column string) db.Query {
	q := r.clone()
	q.builder.OrderByDesc(column)

	return q
}

// OrderByRaw adds a raw order clause.
func (r *Query) OrderByRaw(raw string) db.Query {
	q := r.clone()
	q.builder.OrderByRaw(raw, nil)

	return q
}

// Latest orders by created_at descending, or by the given column.
func (r *Query) Latest(column ...string) db.Query {
	col := ""
	if len(column) > 0 {
		col = column[0]
	}

	q := r.clone()
	q.builder.Latest(col)

	return q
}

// InRandomOrder orders randomly.
func (r *Query) InRandomOrder() db.Query {
	q := r.clone()
	q.builder.InRandomOrder("")

	return q
}

// Limit sets the limit.
func (r *Query) Limit(limit uint64) db.Query {
	q := r.clone()
	q.builder.Limit(int(limit))

	return q
}

// Offset sets the offset.
func (r *Query) Offset(offset uint64) db.Query {
	q := r.clone()
	q.builder.Offset(int(offset))

	return q
}

// LockForUpdate locks the selected rows for update.
func (r *Query) LockForUpdate() db.Query {
	q := r.clone()
	q.builder.LockForUpdate()
	q.readBuilder = q.writeBuilder

	return q
}

// SharedLock takes a shared lock on the selected rows.
func (r *Query) SharedLock() db.Query {
	q := r.clone()
	q.builder.SharedLock()
	q.readBuilder = q.writeBuilder

	return q
}

// UseWritePdo forces the query's reads to run on the write connection instead of the reader
// replica, so a read that has to observe a write issued on the same connection is not answered by
// a lagging replica. Mirrors Laravel's Builder::useWritePdo, whose flag reaches the connection as
// the inverse of its $useReadPdo argument. Here the read routing is the builder a read executes
// through, so the write builder replaces the read one for this query and its clones.
func (r *Query) UseWritePdo() db.Query {
	q := r.clone()
	q.readBuilder = q.writeBuilder

	return q
}

// Join adds an inner join from raw SQL. query is a "<table> on <condition>" fragment, or a whole
// join clause whose leading keyword ("left join ...") then overrides the default inner type.
func (r *Query) Join(query string, args ...any) db.Query {
	q := r.clone()
	q.builder.JoinRaw(query, args, "inner")

	return q
}

// LeftJoin adds a left join from raw SQL. A query that carries its own leading join keyword
// overrides the default left type.
func (r *Query) LeftJoin(query string, args ...any) db.Query {
	q := r.clone()
	q.builder.JoinRaw(query, args, "left")

	return q
}

// RightJoin adds a right join from raw SQL. A query that carries its own leading join keyword
// overrides the default right type.
func (r *Query) RightJoin(query string, args ...any) db.Query {
	q := r.clone()
	q.builder.JoinRaw(query, args, "right")

	return q
}

// CrossJoin adds a cross join from raw SQL. A query that carries its own leading join keyword
// overrides the default cross type.
func (r *Query) CrossJoin(query string, args ...any) db.Query {
	q := r.clone()
	q.builder.JoinRaw(query, args, "cross")

	return q
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
	if r.builder.TableSource() == nil {
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

func (r *Query) buildInsertGetID(values []map[string]any) (string, []any, error) {
	if r.err != nil {
		return "", nil, r.err
	}
	if r.builder.TableSource() == nil {
		return "", nil, errors.DatabaseTableIsRequired
	}

	sql, args := r.builder.ToInsertGetIdSQL(values, "")
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

	sql, args := r.builder.ToUpdateSQL(values)
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

// Exists reports whether any row matches.
func (r *Query) Exists() (bool, error) {
	count, err := r.Count()
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// DoesntExist reports whether no row matches.
func (r *Query) DoesntExist() (bool, error) {
	count, err := r.Count()
	if err != nil {
		return false, err
	}

	return count == 0, nil
}

// ExistsOr reports true when a row matches; otherwise it returns the callback's value. Mirrors
// Builder::existsOr(): the callback runs only on the miss branch.
func (r *Query) ExistsOr(callback func() any) (any, error) {
	count, err := r.Count()
	if err != nil {
		return nil, err
	}

	if count > 0 {
		return true, nil
	}

	return callback(), nil
}

// DoesntExistOr reports true when no row matches; otherwise it returns the callback's value. Mirrors
// Builder::doesntExistOr(): the callback runs only on the hit branch.
func (r *Query) DoesntExistOr(callback func() any) (any, error) {
	count, err := r.Count()
	if err != nil {
		return nil, err
	}

	if count == 0 {
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

	return nil
}

// First retrieves the first matching row into dest.
func (r *Query) First(dest any) error {
	sql, args, err := r.buildSelect()
	if err != nil {
		return err
	}

	now := carbon.Now()
	if err := r.readBuilder.GetContext(r.ctx, dest, sql, args...); err != nil {
		if errors.Is(err, databasesql.ErrNoRows) {
			r.trace(r.readBuilder, sql, args, now, 0, nil)

			return nil
		}

		r.trace(r.readBuilder, sql, args, now, -1, err)

		return err
	}

	r.trace(r.readBuilder, sql, args, now, 1, nil)

	return nil
}

// FirstOr retrieves the first matching row, or runs the callback when there is none.
func (r *Query) FirstOr(dest any, callback func() error) error {
	sql, args, err := r.buildSelect()
	if err != nil {
		return err
	}

	now := carbon.Now()
	if err := r.readBuilder.GetContext(r.ctx, dest, sql, args...); err != nil {
		if errors.Is(err, databasesql.ErrNoRows) {
			r.trace(r.readBuilder, sql, args, now, 0, nil)

			return callback()
		}

		r.trace(r.readBuilder, sql, args, now, -1, err)

		return err
	}

	r.trace(r.readBuilder, sql, args, now, 1, nil)

	return nil
}

// FirstOrFail retrieves the first matching row or returns the driver error.
func (r *Query) FirstOrFail(dest any) error {
	sql, args, err := r.buildSelect()
	if err != nil {
		return err
	}

	now := carbon.Now()
	if err := r.readBuilder.GetContext(r.ctx, dest, sql, args...); err != nil {
		r.trace(r.readBuilder, sql, args, now, -1, err)

		return err
	}

	r.trace(r.readBuilder, sql, args, now, 1, nil)

	return nil
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
	return r.Select(column).Limit(1).First(dest)
}

// RawValue retrieves a single raw expression value from the first row. It mirrors Laravel's
// Builder::rawValue: selectRaw($expression)->first(). Unlike Value, the expression is not wrapped as
// an identifier, so it can carry an arbitrary SQL fragment such as UPPER("foo").
func (r *Query) RawValue(expression string, dest any) error {
	q := r.clone()
	q.builder.SetSelects([]any{querybuilder.Raw(expression)})

	return q.Limit(1).First(dest)
}

// Pluck retrieves a single column of every row.
func (r *Query) Pluck(column string, dest any) error {
	q := r.clone()
	q.builder.SetSelects([]any{column})

	return q.Get(dest)
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

	return q.First(dest)
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

	offset := (page - 1) * limit

	// Resolve the total exactly as Laravel's Builder::paginate does: a non-zero total already carried
	// by the caller stands for its $total argument and short-circuits getCountForPagination(); only a
	// zero (unset) total is counted, and the count is written back when the caller asked for it.
	resolvedTotal := int64(0)
	if total != nil {
		resolvedTotal = *total
	}
	if resolvedTotal == 0 {
		count, err := r.Count()
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
	// limit), so it runs on a clone, preserving the facade's immutable chaining.
	q := r.clone()

	page, err := q.builder.CursorPaginate(perPage, anchor)
	if err != nil {
		return db.CursorPaginator{}, err
	}

	sql, args, err := q.buildSelect()
	if err != nil {
		return db.CursorPaginator{}, err
	}

	items, err := q.cursorRows(sql, args, dest)
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

// Chunk iterates the results in slices of the given size. A callback returning
// errors.DatabaseChunkStopped halts the loop cleanly (mirroring Laravel's `return false`, which
// makes chunk return true), so the next chunk is never fetched; any other error aborts and is
// returned. A size of zero short-circuits before any query is issued, mirroring Laravel's
// `if ($limit == 0) break;` guard.
func (r *Query) Chunk(size uint64, callback func(rows []db.Row) error) error {
	if size == 0 {
		return nil
	}

	offset := uint64(0)

	for {
		rows := r.clone().Offset(offset).Limit(size).Cursor()

		var destSlice []db.Row
		for row := range rows {
			if err := row.Err(); err != nil {
				return err
			}
			destSlice = append(destSlice, row)
		}

		if len(destSlice) == 0 {
			break
		}

		if err := callback(destSlice); err != nil {
			if errors.Is(err, errors.DatabaseChunkStopped) {
				return nil
			}

			return err
		}

		if len(destSlice) < int(size) {
			break
		}

		offset += size
	}

	return nil
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
	var (
		items []map[string]any
		rows  []db.Row
	)

	for row := range r.Cursor() {
		if err := row.Err(); err != nil {
			return nil, nil, err
		}

		rows = append(rows, row)

		concrete, _ := row.(*Row)
		if concrete == nil {
			items = append(items, nil)

			continue
		}

		items = append(items, concrete.row)
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
func (r *Query) InsertGetID(data any) (int64, error) {
	mapData, err := convertToMap(data)
	if err != nil {
		return 0, err
	}
	if len(mapData) == 0 {
		return 0, errors.DatabaseUnsupportedType.Args("nil", "struct, map[string]any").SetModule("DB")
	}

	sql, args, err := r.buildInsertGetID([]map[string]any{mapData})
	if err != nil {
		return 0, err
	}

	now := carbon.Now()

	// Postgres and SQL Server return the generated id from the insert statement itself, and their
	// drivers do not support LastInsertId, so the row is read back instead.
	if strings.Contains(strings.ToLower(sql), " returning ") {
		var id int64
		if err := r.writeBuilder.GetContext(r.ctx, &id, sql, args...); err != nil {
			r.trace(r.writeBuilder, sql, args, now, -1, err)

			return 0, err
		}

		r.trace(r.writeBuilder, sql, args, now, 1, nil)

		return id, nil
	}

	result, err := r.writeBuilder.ExecContext(r.ctx, sql, args...)
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		r.trace(r.writeBuilder, sql, args, now, -1, err)

		return 0, err
	}

	r.trace(r.writeBuilder, sql, args, now, 1, nil)

	return id, nil
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
	mapAttributes, err := convertToMap(attributes)
	if err != nil {
		return nil, err
	}

	mapValues, err := convertToMap(values)
	if err != nil {
		return nil, err
	}

	exist, err := r.clone().Where(mapAttributes).Exists()
	if err != nil {
		return nil, err
	}

	if exist {
		// Laravel returns true without touching the row when no update values are given
		// (Builder::updateOrInsert's `if (empty($values))` branch).
		if len(mapValues) == 0 {
			return &db.Result{RowsAffected: 0}, nil
		}

		return r.Where(mapAttributes).Update(values)
	}

	for key, value := range mapValues {
		mapAttributes[key] = value
	}

	return r.Insert(mapAttributes)
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
		q = r.clone()
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
