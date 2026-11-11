package orm

import (
	"context"
	"database/sql"

	"github.com/gradii/framework/contracts/database"
	"github.com/gradii/framework/contracts/database/db"
)

type Orm interface {
	// Config gets the database config.
	Config() database.Config
	// Connection gets an Orm instance from the connection pool.
	Connection(name string) Orm
	// DB gets the underlying database connection.
	DB() (*sql.DB, error)
	// Factory gets a new factory instance for the given model name.
	Factory() Factory
	// DatabaseName gets the current database name.
	DatabaseName() string
	// Name gets the current connection name.
	Name() string
	// Related returns the relation builder object pre-scoped to the related rows for the given
	// parent and relation name. parent must be a non-nil pointer to a struct. The returned
	// RelationQuery is a fresh chain — call any of Where / OrderBy / Get / First / Count / etc.
	// on it — and carries the relation semantics: GetResults / Is / IsNot / RawUpdate and,
	// through the per-kind interface (a Go narrowing assertion, e.g. rel.(HasManyRelation)), the
	// write families (Make / Save / Create / Attach / Sync / ...). See RelationQuery.
	//
	// Per-kind shape:
	//   - HasOne / HasMany:                     Query().Model(related).Where("<id_col>", parent.<local_key>)
	//   - BelongsTo:                            Query().Model(related).Where("<owner_key>", parent.<fk_col>)
	//   - MorphOne / MorphMany:                 HasMany shape + Where("<type_col>", desc.morphValue)
	//   - MorphTo:                              type resolved from morph map; Query().Model(<resolved>).Where("<owner_key>", parent.<id_col>)
	//   - Many2Many / MorphToMany / MorphedByMany: Query().Table(related).Joins("INNER JOIN <pivot> ON ...").Where("<pivot>.<parent_fk>", parent.<pk>)
	//   - HasOneThrough / HasManyThrough:       Query().Table(related).Joins("INNER JOIN <through> ON ...").Where("<through>.<first_key>", parent.<local_key>)
	//
	// When the relation declares an Inverse (HasOne / HasMany / MorphOne / MorphMany), a model-row
	// read through the returned Query — Get / First, a key-list Find / FindOrFail, a chunked page
	// or a cursor page — stores the parent instance on each result model's inverse relation: the
	// write-back Eloquent's chaperone after-query callback performs on every execution of the
	// relation query. The callback sees the actual result set, so a widened query links every row
	// it returns, not only rows whose foreign key matches the parent.
	//
	// Mirrors fedaco's model.NewRelation('foo'). RelationWriter (see Orm.Relation) remains only
	// as the deprecated flat compatibility entry for the write path.
	Related(parent any, relation string) RelationQuery
	// Relation returns a RelationWriter bound to (parent, name) for FK-safe write operations.
	// All write methods (Save / Create / UpdateOrCreate / Attach / Sync / Detach / Toggle /
	// Associate / Dissociate / etc.) are reached via this builder rather than as flat methods on
	// Orm — the (parent, name) pair binds once.
	//
	// Deprecated: use Related(parent, relation) instead, Relation will be removed in a future
	// major version. The relation builder object Related returns — RelationQuery and its per-kind
	// write interfaces — carries the same write operations.
	Relation(parent any, name string) RelationWriter

	// Observe registers an observer with the Orm.
	Observe(model any, observer Observer)
	// Query gets a new query builder instance.
	Query() Query
	// Fresh resets the Orm instance.
	Fresh()
	// SetQuery sets the query builder instance.
	SetQuery(query Query)
	// Transaction runs a callback wrapped in a database transaction.
	Transaction(txFunc func(tx Query) error) error
	// WithContext sets the context to be used by the Orm.
	WithContext(ctx context.Context) Orm
}

// OfManyColumn is one column/aggregate pair of an OfManyColumns configuration — the Go form of one
// entry in Laravel's ofMany(['published_at' => 'max', 'id' => 'max']): the column the aggregate is
// taken over and the aggregate itself ("MAX" or "MIN").
type OfManyColumn struct {
	Column    string
	Aggregate string
}

type Query interface {
	// QueryWithRelations exposes the QueriesRelationships surface (Has / WhereHas / WithCount /
	// HasMorph / etc.). Embedding it into Query lets users chain relationship queries with the
	// rest of the builder: q.Where(...).Has("Books", ">=", 3).Get(&users).
	QueryWithRelations
	// Related returns the relation builder object pre-scoped to the related rows for parent.name.
	// Mirrors Orm.Related but lives on Query so it can be used inside a Transaction callback.
	// parent must be a non-nil pointer to a struct. See RelationQuery for the object surface.
	Related(parent any, name string) RelationQuery
	// Relation returns a RelationWriter bound to (parent, name) for FK-safe write operations.
	// Mirrors Orm.Relation but lives on Query so writes inside a Transaction callback honor the
	// transaction.
	//
	// Deprecated: use Related(parent, name) instead, Relation will be removed in a future major
	// version. The relation builder object Related returns — RelationQuery and its per-kind write
	// interfaces — carries the same write operations.
	Relation(parent any, name string) RelationWriter
	// Begin begins a new transaction
	// DEPRECATED Use BeginTransaction instead.
	Begin() (Query, error)
	// BeginTransaction begins a new transaction
	BeginTransaction() (Query, error)
	// Commit commits the changes in a transaction.
	Commit() error
	// Context gets the context used by the query.
	Context() context.Context
	// Count retrieve the "count" result of the query.
	Count() (int64, error)
	// GetCountForPagination gets the count a paginator draws its page from, mirroring Laravel's
	// Builder::getCountForPagination (Builder::runPaginationCountQuery): the count is built on a
	// clone, so the query keeps its own clauses and bindings, and a grouped or having query is
	// counted by wrapping its select in a derived table named "aggregate_table" — the count counts
	// groups, not rows, unlike Count(), which keeps Laravel's count() semantics of one aggregate
	// over the grouped result. Paginate feeds its total from this method.
	GetCountForPagination(columns ...string) (int64, error)
	// Create inserts new record into the database.
	Create(value any) error
	// Chunk reads the query in pages of size rows, scanning every page into dest — a non-nil
	// pointer to a slice of models — and calling callback with the 1-based page number after the
	// page was scanned, mirroring Eloquent's BuildsQueries::chunk: a query without an order is
	// ordered by the model's qualified key ascending first, the caller's offset and limit are
	// honoured, and a callback returning errors.DatabaseChunkStopped stops the walk cleanly — the
	// Go form of Laravel's `return false`.
	Chunk(size uint64, dest any, callback func(page int) error) error
	// ChunkById chunks by comparing ids, mirroring BuildsQueries::chunkById: each page is fetched
	// with ForPageAfterId using the last row's id as the keyset, so the walk pays no growing
	// offset. On a relation read the key column defaults to the related model's qualified key and
	// the alias to its bare key name, the way BelongsToMany / HasOneOrManyThrough::chunkById
	// override the builder defaults for a joined read; elsewhere the column is the model's bare
	// key name (Eloquent's Builder::defaultKeyName). column may be (column) or (column, alias).
	ChunkById(size uint64, dest any, callback func(page int) error, column ...string) error
	// ChunkByIdDesc is ChunkById in descending order, mirroring BuildsQueries::chunkByIdDesc.
	ChunkByIdDesc(size uint64, dest any, callback func(page int) error, column ...string) error
	// Cursor returns a cursor, use scan to iterate over the returned rows.
	Cursor() chan db.Row
	// CursorPaginate fetches a single keyset ("seek") page and scans it into dest, returning the
	// page and its cursors. It never counts, so the page carries no total, and the name is distinct
	// from Cursor, which streams rows.
	CursorPaginate(perPage int, cursor string, dest any) (db.CursorPaginator, error)
	// DB gets the underlying database connection.
	DB() (*sql.DB, error)
	// Delete deletes records matching given conditions, if the conditions are empty will delete all records.
	Delete(value ...any) (*db.Result, error)
	// Distinct preserves the selected fields and returns distinct results. Optional columns
	// specify DISTINCT ON in PostgreSQL and the distinct columns for aggregate queries.
	Distinct(columns ...string) Query
	// Driver gets the driver for the query.
	Driver() string
	// Each executes the callback over every matching model one at a time, reading 1000 rows per
	// statement like BuildsQueries::each: dest — a non-nil pointer to a model — holds the current
	// row, index is the model's key within its page, and a callback returning
	// errors.DatabaseChunkStopped stops the walk cleanly (Laravel's `return false`).
	Each(dest any, callback func(index int) error) error
	// EachById executes the callback over every matching model one at a time, walking the keyset
	// like BuildsQueries::eachById. dest holds the current row and index is the model's 0-based
	// ordinal across the whole iteration — Laravel's ((page-1)*size + key) arithmetic.
	EachById(size uint64, dest any, callback func(index int) error, column ...string) error
	// Exec executes raw sql
	Exec(sql string, values ...any) (*db.Result, error)
	// Exists returns true if matching records exist; otherwise, it returns false.
	Exists() (bool, error)
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
	// FirstOrCreate finds the first record that matches the given attributes
	// or create a new one with those attributes if none was found.
	FirstOrCreate(dest any, conds ...any) error
	// CreateOrFirst attempts to create a record with the attributes (and values) without reading
	// first, mirroring Eloquent's Builder::createOrFirst: when the insert trips a unique
	// constraint the row matching the attributes is read back — on the write connection — and
	// returned instead of the error. The insert runs inside a savepoint when the query is bound
	// to a transaction.
	CreateOrFirst(dest any, conds ...any) error
	// IncrementOrCreate finds the first record that matches the given attributes, or creates one
	// when none matches, and steps a record that already existed, mirroring Eloquent's
	// Builder::incrementOrCreate: the lookup and the create run through FirstOrCreate — the
	// counted column's default rides along as its create-only values — and only a record that was
	// found is incremented the way Model::increment does it: the column moves on the model and the
	// extras fill before the updating event, the scope-less update keyed on the model writes the
	// raw `<column> + <step>` expression, the extras and the refreshed updated-at stamp, the
	// updated event runs, and the incremented column re-baselines in the model's original
	// attributes. args carries Laravel's optional tail positionally — a column name (a string,
	// default "count"), the create default and the step (numbers, default 1 each), and the extra
	// values (a map) — because a Go signature cannot declare defaults.
	IncrementOrCreate(dest any, attributes any, args ...any) error
	// FirstOrFail finds the first record that matches the given conditions or throws an error.
	FirstOrFail(dest any) error
	// FirstOrNew finds the first record that matches the given conditions or return a new instance
	// of the model initialized with those attributes and with the query's pending attributes
	// (WithAttributes), which merge underneath them. Nothing is written, so a zero value in the maps
	// reaches a later Save only when that Save names its column through WithZero.
	FirstOrNew(dest any, attributes any, values ...any) error
	// Fresh reads model's row back from the database into dest — a new instance — mirroring
	// Eloquent's Model::fresh: the read matches on the model's primary key (a pivot struct without
	// one declares its keys through PivotKeys instead), global scopes are skipped, the read runs on
	// the write connection, with names relations to eager-load on the fresh instance, and a model
	// with no key — or one the database no longer holds — leaves dest zero-valued, Laravel's null.
	// A model that declares neither a primary key nor PivotKeys is refused with a named error.
	Fresh(model any, dest any, with ...string) error
	// Refresh re-reads model's row into model itself, mirroring Eloquent's Model::refresh (which
	// takes no $with): the same key match and scope-skipping read as Fresh, applied in place
	// through firstOrFail — a missing row reports errors.OrmModelNotFoundNoIds, firstOrFail's
	// id-less message — and every relation the model already carries loaded is re-loaded, the Go
	// form of refresh()'s reload of its
	// non-pivot relations. A model with no key reads nothing and is returned unchanged.
	Refresh(model any) error
	// FindMany retrieves the rows whose primary key is one of ids into dest, mirroring Laravel's
	// Builder::findMany: an empty list reads nothing and leaves dest empty.
	FindMany(dest any, ids []any, columns ...any) error
	// ForceDelete forces delete records matching given conditions.
	ForceDelete(value ...any) (*db.Result, error)
	// Get retrieves all rows from the database. A column list may be given for this call only,
	// mirroring Laravel's Builder::get($columns); the query's own selection is left untouched.
	Get(dest any, columns ...any) error
	// Group specifies the group method on the query.
	// DEPRECATED Use GroupBy instead.
	Group(column string) Query
	// GroupBy specifies the group method on the query.
	GroupBy(column ...string) Query
	// Having specifying HAVING conditions for the query.
	Having(query any, args ...any) Query
	// InRandomOrder specifies the order randomly.
	InRandomOrder() Query
	// InTransaction checks if the query is in a transaction.
	InTransaction() bool
	// Join specifying JOIN conditions for the query.
	Join(query string, args ...any) Query
	// Lazy walks the query in pages of chunkSize (default 1000) and hands each hydrated model to
	// callback through dest — a non-nil pointer to a model — with index counting the models seen
	// so far, mirroring BuildsQueries::lazy: a query without an order is ordered by the model's
	// qualified key ascending, a chunk size below 1 is refused (errors.QueryInvalidChunkSize), and
	// errors.DatabaseChunkStopped stops the walk cleanly. Go has no lazy collection, so the walk
	// runs immediately and reports its error; the page sequence is the one Laravel's LazyCollection
	// would pull.
	Lazy(dest any, callback func(index int) error, chunkSize ...uint64) error
	// LazyById is Lazy with chunkById's keyset, mirroring BuildsQueries::lazyById: each page is
	// fetched with forPageAfterId using the last model's id as the keyset, with the same
	// column/alias defaults as ChunkById.
	LazyById(chunkSize uint64, dest any, callback func(index int) error, column ...string) error
	// LazyByIdDesc is LazyById in descending order, mirroring BuildsQueries::lazyByIdDesc.
	LazyByIdDesc(chunkSize uint64, dest any, callback func(index int) error, column ...string) error
	// Limit the number of records returned.
	Limit(limit int) Query
	// Take is an alias of Limit, mirroring Eloquent's Builder::take. On the has-family and
	// through relation objects both Take and Limit are Eloquent's relation-level limit
	// (HasOneOrMany::limit HasOneOrMany.php:555, HasOneOrManyThrough::limit
	// HasOneOrManyThrough.php:772): the plain limit for an existing parent, the per-parent group
	// limit — the row_number() over (partition by <key>) derived table — for a parent that does
	// not exist, returning the relation object (oracle
	// /tmp/followup-take-grouplimit/take_grouplimit.php H1 / H7 / T1 / T5). The many-to-many
	// object carries the same branch (BelongsToMany::take BelongsToMany.php:1492 / BelongsToMany
	// ::limit :1503), partitioned by the qualified foreign pivot key; oracle
	// /tmp/eager-m2m-limit/probe.php B2 / B4.
	Take(limit int) Query
	// Load loads a relationship for the model. args may be a callback or other
	// shapes accepted by With (e.g. "Books:id,name" column pruning is supported by
	// embedding the column list in relation; a callback may be passed via args).
	Load(dest any, relation string, args ...any) error
	// LoadMissing loads a relationship for the model that is not already loaded.
	// args follow the same shapes as Load.
	LoadMissing(dest any, relation string, args ...any) error
	// LockForUpdate locks the selected rows in the table for updating.
	LockForUpdate() Query
	// Make builds an unsaved model instance carrying the query's pending attributes (recorded by
	// WithAttributes) and the attributes it is given, mirroring Laravel's Builder::make: the
	// pending attributes merge underneath, so an attribute passed here wins; nothing is written.
	Make(dest any, attributes ...any) error
	// Model sets the model instance to be queried.
	Model(value any) Query
	// OfMany configures a HasOne, MorphOne or HasOneThrough relation to return one row per parent
	// through the aggregate join Laravel's ofMany installs, mirroring its string form: the
	// column's aggregate is taken per parent and the related model's key joins the aggregate set
	// with the same aggregate, so ties resolve to the extreme key. Column and aggregate default to
	// "id" / "MAX". Intended for use inside a relation's OnQuery or a With(...) callback so the
	// rewrite is local to a single relation:
	//
	//	q.With("LatestImage", func(q orm.Query) orm.Query {
	//	    return q.OfMany("created_at", "MAX")
	//	})
	//
	// constraint is Laravel's ofMany closure: each one runs against every aggregate subquery,
	// in order, so its conditions decide which row is the extreme one. A condition chained after
	// OfMany stays on the relation query and filters the chosen row instead, as in Laravel:
	//
	//	q.OfMany("id", "MAX", func(sub orm.Query) orm.Query {
	//	    return sub.Where("state", "published") // the latest published row
	//	}).Where("state", "published")              // the latest row, if it is published
	//
	// Laravel takes the closure on the array form only; the string form taking it too is a Go
	// convenience for a single column.
	OfMany(column, aggregate string, constraint ...func(Query) Query) Query
	// OfManyColumns configures the relation the way ofMany's ordered column map does, e.g.
	// ofMany(['published_at' => 'max', 'id' => 'max']): the pairs are taken in order with each
	// later pair breaking ties in the earlier ones, and the related model's key is appended with
	// MAX when the columns omit it. constraint is ofMany's closure, as for OfMany.
	OfManyColumns(columns []OfManyColumn, constraint ...func(Query) Query) Query
	// LatestOfMany is shorthand for ofMany with every column aggregated "MAX" — Laravel's
	// latestOfMany($column), which defaults to "id". The related model's key is appended as MAX
	// when the columns omit it. Mirrors fedaco's latestOfMany().
	LatestOfMany(column ...string) Query
	// OldestOfMany is shorthand for ofMany with every column aggregated "MIN" — Laravel's
	// oldestOfMany($column), which defaults to "id". The related model's key is appended as MAX
	// when the columns omit it, exactly as ofMany appends it. Mirrors fedaco's oldestOfMany().
	OldestOfMany(column ...string) Query
	// Offset specifies the number of records to skip before starting to return the records.
	Offset(offset int) Query
	// Omit specifies columns that should be omitted from the query.
	Omit(columns ...string) Query
	// WithZero names columns a struct write (Create, Save, Update with a struct, and the relation
	// writes, which carry the list to the child they persist) carries even when the field holds its
	// Go zero value. A Go struct cannot tell an unset field from one set to zero, so struct writes
	// leave zero fields out by default; WithZero opts the named columns in without narrowing the
	// write the way Select does. "*" names every column but the automatic timestamps and a sole
	// primary key, which join only when named; Omit wins over it. The attribute and value
	// maps FirstOrCreate, CreateOrFirst, UpdateOrCreate and IncrementOrCreate are given are added to
	// the list for that call only, since a map value is an explicit assignment.
	WithZero(columns ...string) Query
	// Order specifies the order in which the results should be returned.
	// DEPRECATED Use OrderByRaw instead.
	Order(value any) Query
	// OrderBy specifies the order should be ascending, or the given direction. The direction must be asc
	// or desc, as Laravel requires; a custom direction such as "DESC NULLS LAST" goes through OrderByRaw.
	OrderBy(column string, direction ...string) Query
	// OrderByDesc specifies the order should be descending.
	OrderByDesc(column string) Query
	// OrderByRaw specifies the order should be raw.
	OrderByRaw(raw string) Query
	// Latest orders by the given column descending, defaulting to the model's created-at column,
	// mirroring Laravel's Builder::latest.
	Latest(column ...string) Query
	// Oldest orders by the given column ascending, defaulting to the model's created-at column,
	// mirroring Laravel's Builder::oldest.
	Oldest(column ...string) Query
	// OrWhere add an "or where" clause to the query.
	OrWhere(query any, args ...any) Query
	// OrWhereBetween adds an "or where column between x and y" clause to the query.
	OrWhereBetween(column string, x, y any) Query
	// OrWhereColumn adds an "or where column" comparison clause to the query, mirroring
	// Builder::orWhereColumn. One trailing column means equality with that column; two mean the
	// operator and then the column.
	OrWhereColumn(column1 string, column2 ...string) Query
	// OrWhereIn adds an "or where column in" clause to the query.
	OrWhereIn(column string, values []any) Query
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
	// OrWhereNot adds a negated "or where" clause, mirroring Eloquent's Builder::orWhereNot
	// (Builder.php:420). A callback with no other arguments runs as one nested group
	// carrying the "or not" connector; a column with its value is handed to the underlying
	// query builder, which renders "or not col = ?"; an array first argument expands through
	// the inner builder's addArrayOfWheres under the "or not" boolean —
	// `or not (not "b" = ? or not "c" = ?)`.
	OrWhereNot(query any, args ...any) Query
	// OrWhereNotBetween adds an "or where column not between x and y" clause to the query.
	OrWhereNotBetween(column string, x, y any) Query
	// OrWhereNotIn adds an "or where column not in" clause to the query.
	OrWhereNotIn(column string, values []any) Query
	// OrWhereNull adds a "or where column is null" clause to the query.
	OrWhereNull(column string) Query
	// Paginate the given query into a simple paginator.
	Paginate(page, limit int, dest any, total *int64) error
	// SimplePaginate retrieves one page of rows without counting the total, mirroring Laravel's
	// Builder::simplePaginate (Eloquent Builder.php:1143): page and limit follow the same
	// conventions as Paginate — a non-positive page falls back to 1 and a non-positive limit to
	// 15 — the query runs with one probe row (limit+1), dest — a pointer to a slice — receives at
	// most limit rows with the probe row trimmed away, and the returned bool reports whether the
	// probe row was present, which is Laravel's hasMorePages(). No count query runs, so the page
	// carries no total; relation queries inherit the method through their embedded Query surface.
	SimplePaginate(page, limit int, dest any) (bool, error)
	// Pluck retrieves a single column from the database, mirroring Laravel's pluck($column, $key):
	// dest is a pointer to a slice that receives the values, or — when a key column is given — a
	// pointer to a map keyed by that column's values.
	Pluck(column string, dest any, key ...string) error
	// Sole retrieves the one row the query matches, mirroring Laravel's Builder::sole: no row is an
	// error, and so is more than one.
	Sole(dest any) error
	// Raw creates a raw query.
	Raw(sql string, values ...any) Query
	// Restore restores a soft deleted model.
	Restore(model ...any) (*db.Result, error)
	// Rollback rolls back the changes in a transaction.
	Rollback() error
	// Save persists a model: a model that carries no key inserts, and a model with a key updates that
	// row. Either way the write carries the non-zero fields and the automatic timestamps; a zero field
	// is written only when WithZero (or Select) names it. No previously loaded state is consulted. A
	// keyed model with nothing to write runs no statement: only the saving and saved events fire and
	// no owner is touched.
	Save(value any) error
	// SaveQuietly updates value in a database without firing events
	SaveQuietly(value any) error
	// Scan scans the query result and populates the destination object.
	Scan(dest any) error
	// Scopes applies one or more query scopes immediately — Eloquent's Builder::scopes(), not the
	// deferred global-scope registration — so clauses chained after the call follow the scope's.
	Scopes(funcs ...func(Query) Query) Query
	// Select specifies fields that should be retrieved from the database.
	Select(columns ...string) Query
	// SelectRaw specifies a raw SQL query for selecting fields.
	SelectRaw(query any, args ...any) Query
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
	// Increment adds amount (default 1) to the column of every matching row, refreshing the model's
	// updated-at column and writing extra, mirroring Laravel's Builder::increment.
	Increment(column string, args ...any) (*db.Result, error)
	// Decrement subtracts amount (default 1) from the column of every matching row, refreshing the
	// model's updated-at column and writing extra, mirroring Laravel's Builder::decrement.
	Decrement(column string, args ...any) (*db.Result, error)
	// Touch sets the model's updated-at column (or the named column) to now on every matching row,
	// mirroring Laravel's Builder::touch. A model without an updated-at column touches nothing.
	Touch(column ...string) (*db.Result, error)
	// Table specifies the table for the query.
	//
	// A string is a bare table name, and the connection prefix is applied to it when the statement
	// is rendered. Pass a query.Raw expression to name a table that is already qualified — for
	// example a catalog name that must be addressed verbatim — and it is rendered as given, with no
	// prefix added. That mirrors Laravel's withoutTablePrefix(), which the query builder itself does
	// not expose.
	Table(name any, args ...any) Query
	// ToSql returns the query as a SQL string.
	ToSql() ToSql
	// ToRawSql returns the query as a raw SQL string.
	ToRawSql() ToSql
	// Update updates records with the given column and values
	Update(column any, value ...any) (*db.Result, error)
	// UpdateOrCreate finds the first record that matches the given attributes
	// or create a new one with those attributes if none was found.
	UpdateOrCreate(dest any, attributes any, values any) error
	// Upsert inserts the rows, updating the listed columns of a row whose uniqueBy columns already
	// exist. It mirrors Laravel's Builder::upsert on a model: the model's timestamps are stamped,
	// the created-at column is left alone on update, and the updated-at column is always refreshed.
	// With no update columns every inserted column is updated; an empty update list inserts only.
	Upsert(values any, uniqueBy []string, update ...any) (*db.Result, error)
	// UsesTimestamps reports whether writes through the query stamp the model's automatic timestamps,
	// the query-level form of Eloquent's HasTimestamps::usesTimestamps: false while a
	// WithoutTimestamps scope is active, and false when the query's model carries the per-instance
	// flag (a databaseorm.Timestamps whose Disabled field names it ignored, read through the model's
	// own UsesTimestamps method). A query with no model attached reports the scope alone.
	UsesTimestamps() bool
	// Value retrieves a single column of the first row into dest, mirroring Laravel's
	// Builder::value.
	Value(column string, dest any) error
	// ValueOrFail retrieves a single column of the first row into dest, mirroring Laravel's
	// Builder::valueOrFail: a read that matches no row reports the model-not-found error naming
	// the model instead of leaving dest untouched.
	ValueOrFail(column string, dest any) error
	// UseWritePdo forces the query's reads to run on the write connection rather than the reader
	// replica. Mirrors Laravel's Builder::useWritePdo.
	UseWritePdo() Query
	// When executes the callback if the condition is true, otherwise the falseCallback if it is given.
	When(condition bool, callback func(query Query) Query, falseCallback ...func(query Query) Query) Query
	// Where adds a "where" clause to the query, mirroring Eloquent's Builder::where. A callback
	// with no other arguments runs against a new query without relationships: its eager loads
	// merge into this query and its wheres join this query as one nested group. Every other
	// condition is applied to the underlying query builder as it is written; raw SQL goes through
	// WhereRaw, the way Eloquent separates where() from whereRaw().
	Where(query any, args ...any) Query
	// WhereRaw adds a raw where clause, mirroring Eloquent's Builder::whereRaw: the fragment renders
	// verbatim and the values bind to its placeholders in order.
	WhereRaw(query string, args ...any) Query
	// OrWhereRaw adds a raw where clause with the "or" connector, mirroring Builder::orWhereRaw.
	OrWhereRaw(query string, args ...any) Query
	// WhereAll adds a "where all columns match" clause to the query.
	WhereAll(columns []string, args ...any) Query
	// WhereAny adds a "where any of columns match" clause to the query.
	WhereAny(columns []string, args ...any) Query
	// WhereBetween adds a "where column between x and y" clause to the query.
	WhereBetween(column string, x, y any) Query
	// WhereColumn adds a "where" clause comparing two columns to the query, mirroring
	// Builder::whereColumn. One trailing column means equality with that column; two mean the
	// operator and then the column.
	WhereColumn(column1 string, column2 ...string) Query
	// WhereIn adds a "where column in" clause to the query.
	WhereIn(column string, values []any) Query
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
	// WhereNone adds a "where none of columns match" clause to the query.
	WhereNone(columns []string, args ...any) Query
	// WhereNot adds a negated where clause, mirroring Eloquent's Builder::whereNot
	// (Builder.php:407), which is where($column, $operator, $value, $boolean.' not'). A callback
	// with no other arguments runs as one nested group carrying the "and not" connector; a
	// column with its value (or with no value) is handed to the underlying query builder, which
	// renders "and not col = ?" — the negation rides the connector; an array first argument
	// expands through the inner builder's addArrayOfWheres under the "and not" boolean,
	// negating every column inside one group — `and not (not "b" = ? and not "c" = ?)` —
	// where the raw Query\Builder::whereNot nests twice (`and not (("b" = ? and "c" = ?))`).
	WhereNot(query any, args ...any) Query
	// WhereNotBetween adds a "where column not between x and y" clause to the query.
	WhereNotBetween(column string, x, y any) Query
	// WhereNotIn adds a "where column not in" clause to the query.
	WhereNotIn(column string, values []any) Query
	// WhereNotNull adds a "where column is not null" clause to the query.
	WhereNotNull(column string) Query
	// WhereNull adds a "where column is null" clause to the query.
	WhereNull(column string) Query
	// WhereKey constrains the query to the row (or rows) with the given primary key, mirroring
	// Laravel's Builder::whereKey. A slice stands for several keys; a model stands for its own key.
	WhereKey(id any) Query
	// WhereKeyNot excludes the row (or rows) with the given primary key, mirroring Laravel's
	// Builder::whereKeyNot.
	WhereKeyNot(id any) Query
	// Except excludes the given models by primary key, mirroring Laravel's Builder::except.
	Except(models any) Query
	// WithAttributes adds the attributes as where equalities and remembers them for every model the
	// query creates afterwards, mirroring Laravel's Builder::withAttributes. Pass a map, or a column
	// with its value. asConditions false records the attributes without adding the where clauses.
	WithAttributes(attributes any, args ...any) Query
	// WithoutEvents disables event firing for the query.
	WithoutEvents() Query
	// WithoutGlobalScopes disables all global scopes for the query.
	//
	// RemoveScope(name) is the builder-local form of the same intent and the one callers should
	// reach for. WithoutGlobalScopes belongs to the model-declared global-scope API and goes away
	// with it (see .claude/plans/remove-custom-global-scopes.md); the framework's own no-scope
	// operations still use it until they are migrated.
	WithoutGlobalScopes(names ...string) Query
	// ApplyScope mounts a named scope on this query — Laravel's withGlobalScope($name, $scope) — and
	// returns the query. The scope runs when a statement is prepared from the query, not at call
	// time (Scopes() is the immediate form), on the copy that statement compiles from, in mount
	// order, after the built-in soft-delete predicates. Mounting the same name again replaces its
	// callback and keeps its position; a mount after RemoveScope is a fresh mount, appended at the
	// end, and clears that name's removal record. A nil callback mounts nothing.
	//
	// The scope belongs to this query alone: a query built elsewhere never inherits it, and Clone()
	// copies the mount rather than sharing it.
	ApplyScope(name string, scope func(Query) Query) Query
	// RemoveScope removes the scope mounted under name from this query and records the removal, so
	// the scopes the framework mounts by itself — the built-in soft-delete predicates, mounted per
	// model when the statement is prepared — are dropped too. Laravel's withoutGlobalScope($name)
	// has the same two effects.
	//
	// The removal is recorded even when nothing is mounted under that name yet (so it governs a
	// mount that happens later), and only a new ApplyScope for the same name clears it. Clauses a
	// scope already wrote are not rewritten: the removal applies from the point it is expressed, so
	// OnlyTrashed's "is not null" stays when the soft-delete scope is removed afterwards.
	//
	// The built-in scopes answer to the names in contracts/database/orm/scope.go.
	RemoveScope(name string) Query
	// Without removes the given relations from the eager-load list set by With.
	// Mirrors fedaco's without().
	Without(relations ...string) Query
	// WithoutTimestamps runs the callback with the query's automatic created_at / updated_at
	// stamping suspended, mirroring Eloquent's HasTimestamps::withoutTimestamps for the query-level
	// Go surface: while a scope is active, writes through the query write neither column — a value
	// the caller supplied is still written — nested scopes stay suspended with each restoring
	// exactly its own, and the suspension is lifted before the callback's error (or panic)
	// propagates. The callback's error is returned unchanged.
	WithoutTimestamps(callback func() error) error
	// WithTrashed allows soft deleted models to be included in the results.
	WithTrashed() Query
	// OnlyTrashed restricts the results to the soft deleted models, mirroring Laravel's
	// SoftDeletes::onlyTrashed: the automatic "not deleted" predicate is dropped and replaced by a
	// "deleted_at is not null" one.
	OnlyTrashed() Query
	// With eagerly loads the given relationships using Goravel's own loader (does not
	// delegate to GORM Preload). Accepts the union of fedaco's with(...) shapes:
	//
	//   q.With("Books")
	//   q.With("Books", cb)                                        // string + callback
	//   q.With("Books", "Roles", "Address")                        // multiple strings
	//   q.With("Books:id,name")                                    // column pruning
	//   q.With(map[string]orm.RelationCallback{"Books": cb})       // map of name -> callback
	//   q.With([]any{"Books", map[string]orm.RelationCallback{"Roles": cb}})
	//   q.With("Books.Author")                                     // nested
	//   q.With("Books.Author", cb)                                 // nested + callback
	//
	// Supports HasOne, HasMany, BelongsTo, BelongsToMany, MorphOne, MorphMany,
	// HasOneThrough and HasManyThrough.
	With(args ...any) Query
	// WithOnly clears the eager-load list set by With, then adds the given
	// relations. Mirrors fedaco's withOnly().
	WithOnly(args ...any) Query
}

type QueryWithContext interface {
	WithContext(ctx context.Context) Query
}

// QueryWithClone is the optional capability a Query implements when it can hand out an independent
// copy. Orm.Query() uses it to return a new builder per call, which is what the Query() contract
// promises and what Laravel's Connection::query() does. A Query without it is returned as stored.
type QueryWithClone interface {
	Clone() Query
}

type QueryWithObserver interface {
	Observe(model any, observer Observer)
}

type ModelWithConnection interface {
	// Connection gets the connection name for the model.
	Connection() string
}

// ModelWithTimestamps is implemented by a model that carries the per-instance timestamps flag — the
// Go form of Eloquent's HasTimestamps::$timestamps. Models embedding databaseorm.Timestamps answer
// through its promoted UsesTimestamps method, and the ORM reads the capability through this
// interface when it decides whether a write stamps. A model that declares its own timestamp fields
// without the embed reports nothing and keeps stamping.
type ModelWithTimestamps interface {
	// UsesTimestamps reports whether the model stamps its automatic timestamps.
	UsesTimestamps() bool
}

// GlobalScope is one global scope a model declares. Name identifies the scope — WithoutGlobalScopes
// takes the same names — and Scope constrains the query it is applied to.
type GlobalScope struct {
	Name  string
	Scope func(Query) Query
}

// ModelWithGlobalScopes is implemented by a model that carries global scopes. The scopes apply in
// the declared order, which is Laravel's registration order — the order a boot() method added its
// scopes in. The order is observable when several scopes add wheres: a later scope's clauses follow
// the earlier ones', the way Laravel appends them.
type ModelWithGlobalScopes interface {
	GlobalScopes() []GlobalScope
}

// PivotKeyed is implemented by a pivot struct that names the pivot-table columns identifying one
// pivot row — AsPivot's (foreignKey, relatedKey) pair, plus the morph type on a MorphPivot, the
// order setKeysForSelectQuery writes them. Fresh/Refresh match on these columns because a pivot
// row carries no primary key of its own; the values come from the struct's current fields.
type PivotKeyed interface {
	PivotKeys() []string
}

type ToSql interface {
	Count() string
	Create(value any) string
	Delete(value ...any) string
	Find(dest any, conds ...any) string
	First(dest any) string
	ForceDelete(value ...any) string
	Get(dest any) string
	Pluck(column string, dest any) string
	Save(value any) string
	Sum(column string, dest any) string
	Update(column any, value ...any) string
}

// ModelWithTableName is implemented by every model, which must declare the table it is stored in.
//
// Declaring a table is required, not optional: the ORM resolves a model's table through this method
// and nothing else. The framework never derives a table name from the struct name and never
// pluralises, so `type User struct{ ... }` addresses no table until TableName() says which one it
// is. The method is the single source every part of the framework consults when it needs a model's
// table - reads, writes, relations and the query builder alike - so there is exactly one place to
// change it.
//
// A model that declares no table is a configuration error rather than something to guess around:
// instead of falling back to a naming strategy, the ORM fails with a named error that reports the
// offending type and names the TableName() method to add.
//
// `make:model` writes this method into every model it generates.
type ModelWithTableName interface {
	// TableName gets the table name for the model, without the connection prefix.
	TableName() string
}
