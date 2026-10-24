package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
)

type DB interface {
	AcquireWriteConnection() (WriteConnection, error)
	WithWriteConnection(callback func(Tx) error) error
	Listen(callback func(QueryExecuted))
	BeforeExecuting(callback func(context.Context, string, []any) error)
	WhenQueryingForLongerThan(duration time.Duration, callback func(QueryExecuted))
	TotalQueryDuration() time.Duration
	ResetTotalQueryDuration()
	AllowQueryDurationHandlersToRunAgain()
	Sticky(enabled bool) DB
	UseWriteConnectionWhenReading(enabled bool) DB
	HasModifiedRecords() bool
	RecordsHaveBeenModified(modified ...bool)
	ForgetRecordModificationState()
	Pretend(callback func(DB) error) ([]QueryExecuted, error)
	EnableQueryLog()
	DisableQueryLog()
	GetQueryLog() []QueryExecuted
	FlushQueryLog()
	Purge(connection ...string) error
	Disconnect(connection ...string) error
	Reconnect(connection ...string) (DB, error)
	Name() string
	DriverName() string
	ServerVersion() (string, error)
	Escape(value any, binary ...bool) (string, error)
	Tx
	// BeginTransaction begins a transaction.
	BeginTransaction() (Tx, error)
	// Connection gets a DB session for the named connection.
	Connection(name string) DB
	// Transaction runs a callback wrapped in a database transaction.
	Transaction(txFunc func(tx Tx) error, attempts ...int) error
	// WithContext creates a DB session for the context with isolated request state.
	WithContext(ctx context.Context) DB
}

type Tx interface {
	BeginTransaction() (Tx, error)
	Transaction(callback func(Tx) error, attempts ...int) error
	TransactionLevel() int
	RollbackTo(level int) error
	AfterCommit(callback func() error) error
	AfterRollback(callback func() error) error
	AfterRollBack(callback func() error) error
	BeforeStartingTransaction(callback func(Tx) error)
	Query() Query
	SelectOne(dest any, sql string, args ...any) error
	Scalar(sql string, args ...any) (any, error)
	SelectFromWriteConnection(dest any, sql string, args ...any) error
	SelectResultSets(sql string, args ...any) ([][]map[string]any, error)
	AffectingStatement(sql string, args ...any) (*Result, error)
	Unprepared(sql string) error
	// Commit commits the changes in a transaction.
	Commit() error
	// Delete executes a delete query.
	Delete(sql string, args ...any) (*Result, error)
	// Insert executes a insert query.
	Insert(sql string, args ...any) (*Result, error)
	// Rollback rolls back the changes in a transaction.
	Rollback() error
	// Select executes a select query.
	Select(dest any, sql string, args ...any) error
	// Statement executes a raw sql.
	Statement(sql string, args ...any) error
	// StatementContext executes on the same connection with an explicit context.
	StatementContext(ctx context.Context, sql string, args ...any) error
	// Table specifies the table for the query.
	Table(name string) Query
	// Update executes a update query.
	Update(sql string, args ...any) (*Result, error)
}

type Query interface {
	GetCountForPagination(columns ...string) (int64, error)
	SelectRaw(sql string, bindings ...any) Query
	SelectSub(query any, as string) Query
	AddSelect(columns ...any) Query
	From(table any, alias ...string) Query
	FromSub(query any, as string) Query
	FromRaw(sql string, bindings ...any) Query
	UseIndex(index string) Query
	ForceIndex(index string) Query
	IgnoreIndex(index string) Query
	JoinLateral(query any, as string) Query
	LeftJoinLateral(query any, as string) Query
	CrossJoinSub(query any, as string) Query
	Union(query any) Query
	UnionAll(query any) Query
	GroupByRaw(sql string, bindings ...any) Query
	OrHaving(column any, args ...any) Query
	HavingRaw(sql string, bindings ...any) Query
	OrHavingRaw(sql string, bindings ...any) Query
	HavingNull(columns ...string) Query
	HavingNotNull(columns ...string) Query
	OrHavingNull(columns ...string) Query
	OrHavingNotNull(columns ...string) Query
	HavingBetween(column string, lower, upper any) Query
	HavingNotBetween(column string, lower, upper any) Query
	Reorder(column ...string) Query
	ReorderDesc(column string) Query
	Oldest(column ...string) Query
	Skip(offset int) Query
	Take(limit int) Query
	GroupLimit(limit int, column string) Query
	ForPage(page int, perPage ...int) Query
	ForPageBeforeId(perPage int, lastID any, column ...string) Query
	ForPageAfterId(perPage int, lastID any, column ...string) Query
	Lock(value any) Query
	WhereBetweenColumns(column, lower, upper string) Query
	OrWhereBetweenColumns(column, lower, upper string) Query
	WhereNotBetweenColumns(column, lower, upper string) Query
	OrWhereNotBetweenColumns(column, lower, upper string) Query
	WhereValueBetween(value any, lower, upper string) Query
	OrWhereValueBetween(value any, lower, upper string) Query
	WhereValueNotBetween(value any, lower, upper string) Query
	OrWhereValueNotBetween(value any, lower, upper string) Query
	WhereIntegerInRaw(column string, values []any) Query
	OrWhereIntegerInRaw(column string, values []any) Query
	WhereIntegerNotInRaw(column string, values []any) Query
	OrWhereIntegerNotInRaw(column string, values []any) Query
	WhereRowValues(columns []string, operator string, values []any) Query
	OrWhereRowValues(columns []string, operator string, values []any) Query
	WhereJsonOverlaps(column string, value any) Query
	OrWhereJsonOverlaps(column string, value any) Query
	WhereJsonDoesntOverlap(column string, value any) Query
	OrWhereJsonDoesntOverlap(column string, value any) Query
	WhereFullText(columns []string, value string, options ...string) Query
	OrWhereFullText(columns []string, value string, options ...string) Query
	OrWhereAll(columns []string, args ...any) Query
	OrWhereAny(columns []string, args ...any) Query
	OrWhereNone(columns []string, args ...any) Query
	DynamicWhere(method string, parameters ...any) Query
	WhereDate(column string, args ...any) Query
	OrWhereDate(column string, args ...any) Query
	WhereTime(column string, args ...any) Query
	OrWhereTime(column string, args ...any) Query
	WhereDay(column string, args ...any) Query
	OrWhereDay(column string, args ...any) Query
	WhereMonth(column string, args ...any) Query
	OrWhereMonth(column string, args ...any) Query
	WhereYear(column string, args ...any) Query
	OrWhereYear(column string, args ...any) Query
	WherePast(columns ...string) Query
	OrWherePast(columns ...string) Query
	WhereNowOrPast(columns ...string) Query
	OrWhereNowOrPast(columns ...string) Query
	WhereFuture(columns ...string) Query
	OrWhereFuture(columns ...string) Query
	WhereNowOrFuture(columns ...string) Query
	OrWhereNowOrFuture(columns ...string) Query
	WhereBeforeToday(columns ...string) Query
	OrWhereBeforeToday(columns ...string) Query
	WhereTodayOrBefore(columns ...string) Query
	OrWhereTodayOrBefore(columns ...string) Query
	WhereAfterToday(columns ...string) Query
	OrWhereAfterToday(columns ...string) Query
	WhereTodayOrAfter(columns ...string) Query
	OrWhereTodayOrAfter(columns ...string) Query
	WhereToday(columns ...string) Query
	OrWhereToday(columns ...string) Query
	OrWhereExists(query any) Query
	WhereNotExists(query any) Query
	OrWhereNotExists(query any) Query
	JoinWhere(table string, column string, operator string, value any) Query
	LeftJoinWhere(table string, column string, operator string, value any) Query
	RightJoinWhere(table string, column string, operator string, value any) Query
	JoinSub(query any, as string, first any, args ...any) Query
	LeftJoinSub(query any, as string, first any, args ...any) Query
	RightJoinSub(query any, as string, first any, args ...any) Query
	WhereNested(callback func(Query) Query) Query
	HavingNested(callback func(Query) Query) Query
	BeforeQuery(callback func(Query) Query) Query
	AfterQuery(callback func(any) any) Query
	Tap(callback func(Query)) Query
	Pipe(callback func(Query) any) any
	Unless(condition bool, callback func(Query) Query, otherwise ...func(Query) Query) Query
	NewQuery() Query
	Clone() Query
	CloneWithout(components ...string) Query
	CloneWithoutBindings(bindings ...string) Query
	GetBindings() []any
	GetRawBindings() map[string][]any
	SetBindings(values []any, component string) Query
	AddBinding(value any, component string) Query
	GetColumns() []string
	GetLimit() *int
	GetOffset() *int
	SimplePaginate(page, limit int, dest any) (hasMore bool, err error)
	Sole(dest any) error
	SoleValue(column string, dest any) error
	ChunkMap(size uint64, callback func(Row) (any, error)) ([]any, error)
	EachById(size uint64, callback func(Row) error, column ...string) error
	Lazy(chunkSize ...uint64) RowIterator
	LazyById(chunkSize uint64, column ...string) RowIterator
	LazyByIdDesc(chunkSize uint64, column ...string) RowIterator
	Explain(dest any) error
	InsertOrIgnore(data any) (*Result, error)
	InsertUsing(columns []string, query any) (*Result, error)
	InsertOrIgnoreUsing(columns []string, query any) (*Result, error)
	UpdateFrom(values any) (*Result, error)
	Upsert(data any, uniqueBy []string, update ...any) (*Result, error)
	IncrementEach(columns any, extra ...map[string]any) (*Result, error)
	DecrementEach(columns any, extra ...map[string]any) (*Result, error)
	Truncate() error
	// Chunk chunks the query into smaller chunks.
	Chunk(size uint64, callback func(rows []Row) error) error
	// ChunkById chunks the query by comparing ids, streaming larger result sets without the offset
	// cost. The optional column is the key column (default "id"); an optional second value names the
	// result-key alias the id is read from (default the column itself).
	ChunkById(size uint64, callback func(rows []Row) error, column ...string) error
	// ChunkByIdDesc is ChunkById in descending id order.
	ChunkByIdDesc(size uint64, callback func(rows []Row) error, column ...string) error
	// Count retrieves the "count" result of the query.
	Count() (int64, error)
	// CrossJoin specifies CROSS JOIN conditions for the query.
	CrossJoin(query string, args ...any) Query
	// Cursor returns a cursor, use scan to iterate over the returned rows.
	Cursor() chan Row
	// CursorPaginate fetches a single keyset ("seek") page and scans it into dest, returning the page
	// together with the cursors that reach its neighbours. It never counts, so the page carries no
	// total. The method name is deliberately distinct from Cursor, which streams rows.
	CursorPaginate(perPage int, cursor string, dest any) (CursorPaginator, error)
	// Decrement decrements the given column's values by the given amounts.
	Decrement(column string, value ...uint64) error
	// Delete deletes records from the database.
	Delete() (*Result, error)
	// DoesntExist determines if no rows exist for the current query.
	DoesntExist() (bool, error)
	// DoesntExistOr reports true if no rows exist; otherwise it returns the callback's value.
	DoesntExistOr(callback func() any) (any, error)
	// Distinct preserves the selected fields and returns distinct results. Optional columns
	// specify DISTINCT ON in PostgreSQL and the distinct columns for aggregate queries.
	Distinct(columns ...string) Query
	// Each executes the query and passes each row to the callback.
	Each(callback func(row Row) error) error
	// Exists returns true if matching records exist; otherwise, it returns false.
	Exists() (bool, error)
	// ExistsOr reports true if matching records exist; otherwise it returns the callback's value.
	ExistsOr(callback func() any) (any, error)
	// Find finds records that match given conditions.
	Find(dest any, conds ...any) error
	// FindOr finds the first record matching the given id, or runs the callback when there is none.
	FindOr(dest any, id any, callback func() error) error
	// FindOrFail finds records that match given conditions or throws an error.
	FindOrFail(dest any, conds ...any) error
	// First finds record that match given conditions.
	First(dest any) error
	// FirstOr finds the first record that matches the given conditions or
	// execute the callback and return its result if no record is found.
	FirstOr(dest any, callback func() error) error
	// FirstOrFail finds the first record that matches the given conditions or throws an error.
	FirstOrFail(dest any) error
	// Get retrieves all rows from the database.
	Get(dest any) error
	// GroupBy specifies the group method on the query.
	GroupBy(column ...string) Query
	// Having specifies HAVING conditions for the query.
	Having(query any, args ...any) Query
	// Increment increments a column's value by a given amount.
	Increment(column string, value ...uint64) error
	// Implode concatenates the values of a single column into a string separated by glue.
	Implode(column string, glue string, dest any) error
	// InRandomOrder specifies the order randomly.
	InRandomOrder(seed ...any) Query
	// Insert a new record into the database.
	Insert(data any) (*Result, error)
	// InsertGetID returns the inserted ID. PostgreSQL accepts an optional primary key column.
	InsertGetID(data any, sequence ...string) (int64, error)
	// Join specifies JOIN conditions for the query.
	Join(query string, args ...any) Query
	// Latest retrieves the latest record from the database, default column is "created_at"
	Latest(column ...string) Query
	// LeftJoin specifies LEFT JOIN conditions for the query.
	LeftJoin(query string, args ...any) Query
	// Limit the number of records returned.
	Limit(limit uint64) Query
	// LockForUpdate locks the selected rows in the table for updating.
	LockForUpdate() Query
	// Offset specifies the number of records to skip before starting to return the records.
	Offset(offset uint64) Query
	// OrderBy specifies the order should be ascending.
	OrderBy(column any, directions ...string) Query
	// OrderByDesc specifies the order should be descending.
	OrderByDesc(column any) Query
	// OrderByRaw specifies the order should be raw.
	OrderByRaw(raw string, bindings ...any) Query
	// OrWhere adds an "or where" clause to the query.
	OrWhere(query any, args ...any) Query
	// OrWhereBetween adds an "or where column between x and y" clause to the query.
	OrWhereBetween(column string, x, y any) Query
	// OrWhereColumn adds an "or where column" clause to the query.
	OrWhereColumn(column1 string, column2 ...string) Query
	// OrWhereIn adds an "or where column in" clause to the query.
	OrWhereIn(column string, values any) Query
	// OrWhereJsonContains adds an "or where JSON contains" clause to the query.
	OrWhereJsonContains(column string, value any) Query
	// OrWhereJsonContainsKey add a clause that determines if a JSON path exists to the query.
	OrWhereJsonContainsKey(column string) Query
	// OrWhereJsonDoesntContain add an "or where JSON not contains" clause to the query.
	OrWhereJsonDoesntContain(column string, value any) Query
	// OrWhereJsonDoesntContainKey add a clause that determines if a JSON path does not exist to the query.
	OrWhereJsonDoesntContainKey(column string) Query
	// OrWhereJsonLength add an "or where JSON length" clause to the query. Mirrors
	// Laravel's Builder::orWhereJsonLength, so args is either (length) for an
	// equality check or (operator, length) for a comparison.
	OrWhereJsonLength(column string, args ...any) Query
	// OrWhereLike adds an "or where column like" clause to the query.
	OrWhereLike(column string, value string, caseSensitive ...bool) Query
	// OrWhereNot adds an "or where not" clause to the query.
	OrWhereNot(query any, args ...any) Query
	// OrWhereNotBetween adds an "or where column not between x and y" clause to the query.
	OrWhereNotBetween(column string, x, y any) Query
	// OrWhereNotIn adds an "or where column not in" clause to the query.
	OrWhereNotIn(column string, values any) Query
	// OrWhereNotLike adds an "or where column not like" clause to the query.
	OrWhereNotLike(column string, value string, caseSensitive ...bool) Query
	// OrWhereNotNull adds an "or where column is not null" clause to the query.
	OrWhereNotNull(column string) Query
	// OrWhereNull adds an "or where column is null" clause to the query.
	OrWhereNull(column string) Query
	// OrWhereRaw adds a raw "or where" clause to the query.
	OrWhereRaw(raw string, args []any) Query
	// Paginate the given query into a simple paginator.
	Paginate(page, limit int, dest any, total *int64) error
	// Pluck retrieves a single column from the database.
	Pluck(column string, dest any) error
	// RawValue gets a single raw expression value from the first result of a query.
	RawValue(expression string, dest any) error
	// RightJoin specifies RIGHT JOIN conditions for the query.
	RightJoin(query string, args ...any) Query
	// Select specifies fields that should be retrieved from the database.
	Select(columns ...string) Query
	// SharedLock locks the selected rows in the table.
	SharedLock() Query
	// Sum calculates the sum of a column's values and populates the destination object.
	Sum(column string, dest any) error
	// Avg calculates the average of a column's values.
	Avg(column string, dest any) error
	// Min calculates the minimum value of a column.
	Min(column string, dest any) error
	// Max calculates the maximum value of a column.
	Max(column string, dest any) error
	// ToSql returns the query as a SQL string.
	ToSql() ToSql
	// ToRawSql returns the query as a raw SQL string.
	ToRawSql() ToSql
	// Update records with the given column and values
	Update(column any, value ...any) (*Result, error)
	// UpdateOrInsert finds the first record that matches the given attributes
	// or create a new one with those attributes if none was found.
	UpdateOrInsert(attributes any, values any) (*Result, error)
	// UseWritePdo forces the query's reads to run on the write connection rather than the reader
	// replica. Mirrors Laravel's Builder::useWritePdo. A read that carries a lock always runs on
	// the write connection, so this only matters for a plain read that has to observe a write
	// issued on the same connection.
	UseWritePdo() Query
	// Value gets a single column's value from the first result of a query.
	Value(column string, dest any) error
	// When executes the callback if the condition is true.
	When(condition bool, callback func(query Query) Query, falseCallback ...func(query Query) Query) Query
	// Where adds a "where" clause to the query.
	Where(query any, args ...any) Query
	// WhereAll adds a "where all columns match" clause to the query.
	WhereAll(columns []string, args ...any) Query
	// WhereAny adds a "where any of columns match" clause to the query.
	WhereAny(columns []string, args ...any) Query
	// WhereBetween adds a "where column between x and y" clause to the query.
	WhereBetween(column string, x, y any) Query
	// WhereColumn adds a "where" clause comparing two columns to the query.
	WhereColumn(column1 string, column2 ...string) Query
	// WhereExists adds an exists clause to the query.
	WhereExists(query any) Query
	// WhereIn adds a "where column in" clause to the query.
	WhereIn(column string, values any) Query
	// WhereJsonContains add a "where JSON contains" clause to the query.
	WhereJsonContains(column string, value any) Query
	// WhereJsonContainsKey add a clause that determines if a JSON path exists to the query.
	WhereJsonContainsKey(column string) Query
	// WhereJsonDoesntContain add a "where JSON not contains" clause to the query.
	WhereJsonDoesntContain(column string, value any) Query
	// WhereJsonDoesntContainKey add a clause that determines if a JSON path does not exist to the query.
	WhereJsonDoesntContainKey(column string) Query
	// WhereJsonLength add a "where JSON length" clause to the query. Mirrors
	// Laravel's Builder::whereJsonLength, so args is either (length) for an
	// equality check or (operator, length) for a comparison.
	WhereJsonLength(column string, args ...any) Query
	// WhereLike adds a "where like" clause to the query.
	WhereLike(column string, value string, caseSensitive ...bool) Query
	// WhereNone adds a "where none of columns match" clause to the query.
	WhereNone(columns []string, args ...any) Query
	// WhereNot adds a basic "where not" clause to the query.
	WhereNot(query any, args ...any) Query
	// WhereNotBetween adds a "where column not between x and y" clause to the query.
	WhereNotBetween(column string, x, y any) Query
	// WhereNotIn adds a "where column not in" clause to the query.
	WhereNotIn(column string, values any) Query
	// WhereNotLike adds a "where not like" clause to the query.
	WhereNotLike(column string, value string, caseSensitive ...bool) Query
	// WhereNotNull adds a "where column is not null" clause to the query.
	WhereNotNull(column string) Query
	// WhereNull adds a "where column is null" clause to the query.
	WhereNull(column string) Query
	// WhereRaw adds a raw where clause to the query.
	WhereRaw(raw string, args []any) Query
}

// CursorPaginator is one keyset ("seek") page: the rows fetched, the page size, and the encoded
// cursors that reach the neighbouring pages. It mirrors database/query's CursorPaginator, but is
// declared here because a contract package cannot import the query builder: database/query pulls in
// database/schema, which imports contracts/database/orm, which imports this package, so naming
// query.CursorPaginator from a contract would be an import cycle. Each facade projects its paginator
// onto this value at the edge.
type CursorPaginator struct {
	// Items is the page, already trimmed to PerPage, in display order.
	Items []map[string]any
	// PerPage is the page size actually used.
	PerPage int
	// NextCursor is the encoded forward cursor, or nil when there is no next page.
	NextCursor *string
	// PreviousCursor is the encoded backward cursor, or nil when there is none.
	PreviousCursor *string
	// HasMorePages reports whether another page exists in the fetch direction.
	HasMorePages bool
}

// ToMap renders the page as data, per_page, next_cursor and prev_cursor. There is no total (this path
// never counts), no path and no URLs: link assembly belongs to the caller's router.
func (p CursorPaginator) ToMap() map[string]any {
	var nextCursor, previousCursor any
	if p.NextCursor != nil {
		nextCursor = *p.NextCursor
	}
	if p.PreviousCursor != nil {
		previousCursor = *p.PreviousCursor
	}

	return map[string]any{
		"data":        p.Items,
		"per_page":    p.PerPage,
		"next_cursor": nextCursor,
		"prev_cursor": previousCursor,
	}
}

type Result struct {
	RowsAffected int64
}

// SyncResult reports the per-id outcome of a Sync / SyncWithoutDetaching / Toggle operation on
// a many-to-many (or polymorphic many-to-many) pivot table. Each slice carries the related ids
// in the order they were processed.
type SyncResult struct {
	Attached []any
	Detached []any
	Updated  []any
}

type Builder interface {
	CommonBuilder
	Beginx() (*sqlx.Tx, error)
}

type TxBuilder interface {
	CommonBuilder
	Commit() error
	Rollback() error
}

type CommonBuilder interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Explain(sql string, args ...any) string
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error)
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
}

type ToSql interface {
	Count() string
	Delete() string
	First() string
	Get() string
	Insert(data any) string
	InsertGetID(data any, sequence ...string) string
	InsertOrIgnore(data any) string
	InsertUsing(columns []string, query any) string
	InsertOrIgnoreUsing(columns []string, query any) string
	Pluck(column string, dest any) string
	Update(column any, value ...any) string
	UpdateFrom(values any) string
	Upsert(data any, uniqueBy []string, update ...any) string
	IncrementEach(columns any, extra ...map[string]any) string
	DecrementEach(columns any, extra ...map[string]any) string
	// Truncate returns all statements, including SQLite's sequence reset.
	Truncate() []string
}

type Row interface {
	Err() error
	Scan(value any) error
}
