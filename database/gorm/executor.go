package gorm

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	rowscan "github.com/gradii/framework/database/internal/scan"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file executes SQL the framework compiles itself.
//
// The ORM used to hand every statement to GORM, which both built and ran it. Compiling now happens
// in this package, so execution needs its own path: a statement goes to the connection's pool, and
// rows are decoded using the ORM's field-name mapping.

// The pool a statement ran on, reported on its span. The names are the ones the read/write
// resolver used to report through its context value, so a dashboard does not have to change.
const (
	resolverModeRead  = "read"
	resolverModeWrite = "write"
)

// statementPool is what a compiled statement runs on. A connection's writer or reader is a
// *sql.DB; a query bound to a transaction has to run on that transaction's *sql.Tx instead, so both
// are used through the small interface of methods they share.
type statementPool interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// transaction returns the *sql.Tx a compiled statement has to run on when the query is bound to a
// transaction, and whether it is.
//
// The handle is the query's own tx field and nothing else. A statement run through the connection's
// base pool instead executes in auto-commit, so a Rollback rolls nothing back and a FOR UPDATE
// takes no lock; the transaction would exist on paper only. Every path that spawns a query carries
// tx across, which is what keeps a statement inside its transaction.
func (r *Query) transaction() (*sql.Tx, bool) {
	return r.tx, r.tx != nil
}

// queryPool returns the pool statements execute through: the transaction's own pool when the query
// is bound to a transaction, and the writer pool otherwise.
func (r *Query) queryPool() (statementPool, error) {
	if tx, ok := r.transaction(); ok {
		return tx, nil
	}

	if r.writer == nil {
		return nil, errors.DatabaseConnectionIsNil
	}

	return r.writer, nil
}

// readPool returns the pool compiled reads execute through: the replica the connection was
// configured with, or the writer when it has no replica. A read inside a transaction runs on the
// transaction's own pool, so it sees the transaction's uncommitted writes.
//
// Routing is deliberately NOT sticky. Outside a transaction a compiled read never consults the
// writer, not even immediately after a write on the same connection: on an asynchronous replica a
// Find issued right after a Create or Save can therefore return stale or missing rows. Laravel's
// "sticky" option, which sends reads back to the writer for the rest of the request that wrote, is
// intentionally not implemented here. The integration tests TestDbReadWriteSeparate and
// TestOrmReadWriteSeparate pin this behaviour, and the operator-facing documentation records it as a
// deliberate deviation; implementing stickiness means changing those tests at the same time.
//
// UseWritePdo overrides the routing above: a read on a query that asked for the write connection
// resolves to the writer pool, which is Laravel's useWritePdo behaviour applied at the same point.
// A read that carries a lock routes there too: Laravel's Builder::lock() calls useWritePdo() for
// any non-null lock, so both lockForUpdate() and sharedLock() force the writer. That is also the
// only correct routing, since a FOR UPDATE (or a shared lock) sent to a replica locks nothing the
// writer can see.
func (r *Query) readPool() (statementPool, error) {
	if r.conditions.useWritePdo || r.builder.State().Lock != nil {
		return r.queryPool()
	}

	if r.inTransaction() {
		return r.queryPool()
	}

	if reader := databasedriver.Reader(r.dbConfig.Connection); reader != nil {
		return reader, nil
	}

	return r.queryPool()
}

// inTransaction reports whether the query executes inside a transaction. GORM swaps the
// statement's connection pool for the transaction's *sql.Tx when Begin runs, which is what this
// looks for.
func (r *Query) inTransaction() bool {
	_, ok := r.transaction()

	return ok
}

// startStatementSpan opens the telemetry span for one statement and returns the context to run it in
// plus the function that closes the span. When telemetry is off — or the connection was never opened,
// as in the package's own unit tests — the context is the query's own and the closer does nothing, so
// callers need no branch of their own.
func (r *Query) startStatementSpan(resolverMode string) (context.Context, func(statement, table string, rows int64, err error)) {
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	instrument := databasedriver.Instrument(r.dbConfig.Connection)
	if !instrument.Active() {
		return ctx, func(string, string, int64, error) {}
	}

	ctx, span := instrument.StartSpan(ctx, "db")
	start := time.Now()

	return ctx, func(statement, table string, rows int64, err error) {
		instrument.EndSpan(ctx, span, start, statement, table, rows, err, resolverMode)
	}
}

// statementTable names the table a statement addresses, for the span and the query log: the table the
// caller named on the query, or the model's own when they named none.
func (r *Query) statementTable() string {
	if table := r.declaredTable(); table != "" {
		return table
	}

	model := r.parentModel()
	if model == nil {
		return ""
	}

	table, err := tableNameFor(model)
	if err != nil {
		return ""
	}

	return table
}

// poolResolverMode reports which pool a statement ran on, for the span: a replica when the pool is the
// connection's reader, the writer otherwise. A transaction always runs on the writer.
func (r *Query) poolResolverMode(pool statementPool) string {
	if reader := databasedriver.Reader(r.dbConfig.Connection); reader != nil && statementPool(reader) == pool {
		return resolverModeRead
	}

	return resolverModeWrite
}

// queryRows runs a compiled read and returns the result set. Callers own the rows.
func (r *Query) queryRows(statement string, bindings []any) (*sql.Rows, error) {
	pool, err := r.readPool()
	if err != nil {
		return nil, err
	}

	ctx, end := r.startStatementSpan(resolverModeRead)
	begin := carbon.Now()
	rows, err := pool.QueryContext(ctx, querybuilder.ReplacePlaceholders(statement, r.dbConfig.Driver), bindings...)
	r.trace(begin, statement, bindings, -1, err)
	end(statement, r.statementTable(), -1, err)

	return rows, err
}

// queryScalar runs a compiled read that yields one row, scanning its single column.
func (r *Query) queryScalar(dest any, statement string, bindings []any) error {
	pool, err := r.readPool()
	if err != nil {
		return err
	}

	return r.scanScalar(pool, dest, statement, bindings)
}

// writerScalar runs a single-row statement that has to observe a write which just went through
// the writer — reading back an inserted key, for instance — so it never runs on the reader pool.
func (r *Query) writerScalar(dest any, statement string, bindings []any) error {
	pool, err := r.queryPool()
	if err != nil {
		return err
	}

	return r.scanScalar(pool, dest, statement, bindings)
}

// scanScalar runs a statement expected to yield one row on the given pool and scans its single
// column into dest.
func (r *Query) scanScalar(pool statementPool, dest any, statement string, bindings []any) error {
	ctx, end := r.startStatementSpan(r.poolResolverMode(pool))
	begin := carbon.Now()
	rows, err := pool.QueryContext(ctx, querybuilder.ReplacePlaceholders(statement, r.dbConfig.Driver), bindings...)
	if err != nil {
		r.trace(begin, statement, bindings, -1, err)
		end(statement, r.statementTable(), -1, err)

		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			r.trace(begin, statement, bindings, -1, err)
			end(statement, r.statementTable(), -1, err)

			return err
		}

		r.trace(begin, statement, bindings, 0, nil)
		end(statement, r.statementTable(), 0, nil)

		return sql.ErrNoRows
	}

	if err := rows.Scan(dest); err != nil {
		r.trace(begin, statement, bindings, -1, err)
		end(statement, r.statementTable(), -1, err)

		return err
	}

	r.trace(begin, statement, bindings, 1, nil)
	end(statement, r.statementTable(), 1, nil)

	return nil
}

// execStatement runs a compiled statement that affects rows and reports how many it changed.
func (r *Query) execStatement(statement string, bindings []any) (*contractsdb.Result, error) {
	// A statement the caller wrote still has to report a condition the query could not accept.
	if r.conditions.err != nil {
		return nil, r.conditions.err
	}

	pool, err := r.queryPool()
	if err != nil {
		return nil, err
	}

	ctx, end := r.startStatementSpan(resolverModeWrite)
	begin := carbon.Now()
	result, err := pool.ExecContext(ctx, querybuilder.ReplacePlaceholders(statement, r.dbConfig.Driver), bindings...)
	if err != nil {
		r.trace(begin, statement, bindings, -1, err)
		end(statement, r.statementTable(), -1, err)

		return nil, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		r.trace(begin, statement, bindings, -1, err)
		end(statement, r.statementTable(), -1, err)

		return nil, err
	}

	r.trace(begin, statement, bindings, affected, nil)
	end(statement, r.statementTable(), affected, nil)

	return &contractsdb.Result{RowsAffected: affected}, nil
}

// scanRowMap reads the current row of a result set into a column-keyed map, using the column names
// the caller already fetched. A map is what the row scanner and the cursor both start from.
func scanRowMap(rows *sql.Rows, columns []string) (map[string]any, error) {
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}

	row := make(map[string]any, len(columns))
	for index, column := range columns {
		row[column] = values[index]
	}

	return row, nil
}

// scanRows decodes every row into dest, which is a pointer either to a struct or to a slice of
// structs, and reports how many rows it decoded. Columns are matched to fields by name, so a model
// needs no per-field tags for this path to fill it.
func (r *Query) scanRows(rows *sql.Rows, dest any) (int, error) {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return 0, errors.DatabaseUnsupportedType.Args(destValue.Kind(), "pointer")
	}

	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	target := reflect.Indirect(destValue)
	intoSlice := target.Kind() == reflect.Slice
	count := 0

	if intoSlice {
		// Reset the caller's slice before appending the returned rows into it. Rows are appended
		// below, so a destination that already holds rows from an earlier read would otherwise keep
		// them and the caller would see N + returned; the path this replaced reset it as well.
		target = target.Slice(0, 0)
	}

	for rows.Next() {
		count++

		if !intoSlice {
			return 1, rowscan.Row(rows, columns, dest, rowMapper)
		}

		// A slice of values is filled in place; a slice of pointers is given a value per row, because
		// the row scanner needs somewhere to write and the pointer the caller declared is nil.
		elementType := target.Type().Elem()
		if elementType.Kind() == reflect.Pointer {
			element := reflect.New(elementType.Elem())
			if err := rowscan.Row(rows, columns, element.Interface(), rowMapper); err != nil {
				return 0, err
			}

			target = reflect.Append(target, element)

			continue
		}

		element := reflect.New(elementType).Elem()
		if err := rowscan.Row(rows, columns, element.Addr().Interface(), rowMapper); err != nil {
			return 0, err
		}
		target = reflect.Append(target, element)
	}

	if err := rows.Err(); err != nil {
		return 0, err
	}

	if intoSlice {
		destValue.Elem().Set(target)

		return count, nil
	}

	// A single-row read that matched nothing reports ErrNoRows rather than succeeding silently, so
	// callers can keep the "no row is not an error" handling they had before.
	if count == 0 {
		return 0, sql.ErrNoRows
	}

	return count, nil
}
