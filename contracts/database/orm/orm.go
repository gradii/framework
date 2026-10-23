package orm

import (
	"context"
	"database/sql"

	"github.com/goravel/framework/contracts/database"
	"github.com/goravel/framework/contracts/database/db"
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
	// Related returns a Query pre-scoped to the related rows for the given parent and relation
	// name. parent must be a non-nil pointer to a struct. The returned Query is a fresh chain —
	// call any of Where / OrderBy / Get / First / Count / etc. on it.
	//
	// Per-kind shape:
	//   - HasOne / HasMany:                     Query().Model(related).Where("<id_col>", parent.<local_key>)
	//   - BelongsTo:                            Query().Model(related).Where("<owner_key>", parent.<fk_col>)
	//   - MorphOne / MorphMany:                 HasMany shape + Where("<type_col>", desc.morphValue)
	//   - MorphTo:                              type resolved from morph map; Query().Model(<resolved>).Where("<owner_key>", parent.<id_col>)
	//   - Many2Many / MorphToMany / MorphedByMany: Query().Table(related).Joins("INNER JOIN <pivot> ON ...").Where("<pivot>.<parent_fk>", parent.<pk>)
	//   - HasOneThrough / HasManyThrough:       Query().Table(related).Joins("INNER JOIN <through> ON ...").Where("<through>.<first_key>", parent.<local_key>)
	//
	// Mirrors fedaco's model.NewRelation('foo') for the read path. Write operations live on
	// RelationWriter (see Orm.Relation) — they are not chained off this Query.
	Related(parent any, relation string) Query
	// Relation returns a RelationWriter bound to (parent, name) for FK-safe write operations.
	// All write methods (Save / Create / UpdateOrCreate / Attach / Sync / Detach / Toggle /
	// Associate / Dissociate / etc.) are reached via this builder rather than as flat methods on
	// Orm — the (parent, name) pair binds once.
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

type Query interface {
	// QueryWithRelations exposes the QueriesRelationships surface (Has / WhereHas / WithCount /
	// HasMorph / etc.). Embedding it into Query lets users chain relationship queries with the
	// rest of the builder: q.Where(...).Has("Books", ">=", 3).Get(&users).
	QueryWithRelations
	// Related returns a Query pre-scoped to the related rows for parent.name. Mirrors Orm.Related
	// but lives on Query so it can be used inside a Transaction callback. parent must be a
	// non-nil pointer to a struct.
	Related(parent any, name string) Query
	// Relation returns a RelationWriter bound to (parent, name) for FK-safe write operations.
	// Mirrors Orm.Relation but lives on Query so writes inside a Transaction callback honor the
	// transaction.
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
	// Create inserts new record into the database.
	Create(value any) error
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
	// FirstOrFail finds the first record that matches the given conditions or throws an error.
	FirstOrFail(dest any) error
	// FirstOrNew finds the first record that matches the given conditions or
	// return a new instance of the model initialized with those attributes.
	FirstOrNew(dest any, attributes any, values ...any) error
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
	// Limit the number of records returned.
	Limit(limit int) Query
	// Load loads a relationship for the model. args may be a callback or other
	// shapes accepted by With (e.g. "Books:id,name" column pruning is supported by
	// embedding the column list in relation; a callback may be passed via args).
	Load(dest any, relation string, args ...any) error
	// LoadMissing loads a relationship for the model that is not already loaded.
	// args follow the same shapes as Load.
	LoadMissing(dest any, relation string, args ...any) error
	// LockForUpdate locks the selected rows in the table for updating.
	LockForUpdate() Query
	// Model sets the model instance to be queried.
	Model(value any) Query
	// OfMany configures a HasOne or MorphOne relation to return the row whose value of column
	// matches the given SQL aggregate ("MAX" / "MIN") within each parent. Intended for use
	// inside a With(...) callback so the rewrite is local to a single relation:
	//
	//	q.With("LatestImage", func(q orm.Query) orm.Query {
	//	    return q.OfMany("created_at", "MAX")
	//	})
	OfMany(column, aggregate string) Query
	// LatestOfMany is shorthand for OfMany(column, "MAX") with column defaulting to "id" when
	// empty. Mirrors fedaco's latestOfMany().
	LatestOfMany(column ...string) Query
	// OldestOfMany is shorthand for OfMany(column, "MIN") with column defaulting to "id" when
	// empty. Mirrors fedaco's oldestOfMany().
	OldestOfMany(column ...string) Query
	// Offset specifies the number of records to skip before starting to return the records.
	Offset(offset int) Query
	// Omit specifies columns that should be omitted from the query.
	Omit(columns ...string) Query
	// Order specifies the order in which the results should be returned.
	// DEPRECATED Use OrderByRaw instead.
	Order(value any) Query
	// OrderBy specifies the order should be ascending.
	OrderBy(column string, direction ...string) Query
	// OrderByDesc specifies the order should be descending.
	OrderByDesc(column string) Query
	// OrderByRaw specifies the order should be raw.
	OrderByRaw(raw string) Query
	// OrWhere add an "or where" clause to the query.
	OrWhere(query any, args ...any) Query
	// OrWhereBetween adds an "or where column between x and y" clause to the query.
	OrWhereBetween(column string, x, y any) Query
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
	// OrWhereNotBetween adds an "or where column not between x and y" clause to the query.
	OrWhereNotBetween(column string, x, y any) Query
	// OrWhereNotIn adds an "or where column not in" clause to the query.
	OrWhereNotIn(column string, values []any) Query
	// OrWhereNull adds a "or where column is null" clause to the query.
	OrWhereNull(column string) Query
	// Paginate the given query into a simple paginator.
	Paginate(page, limit int, dest any, total *int64) error
	// Pluck retrieves a single column from the database.
	Pluck(column string, dest any) error
	// Raw creates a raw query.
	Raw(sql string, values ...any) Query
	// Restore restores a soft deleted model.
	Restore(model ...any) (*db.Result, error)
	// Rollback rolls back the changes in a transaction.
	Rollback() error
	// Save updates value in a database
	Save(value any) error
	// SaveQuietly updates value in a database without firing events
	SaveQuietly(value any) error
	// Scan scans the query result and populates the destination object.
	Scan(dest any) error
	// Scopes applies one or more query scopes.
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
	// UseWritePdo forces the query's reads to run on the write connection rather than the reader
	// replica. Mirrors Laravel's Builder::useWritePdo.
	UseWritePdo() Query
	// When executes the callback if the condition is true, otherwise the falseCallback if it is given.
	When(condition bool, callback func(query Query) Query, falseCallback ...func(query Query) Query) Query
	// Where add a "where" clause to the query.
	Where(query any, args ...any) Query
	// WhereAll adds a "where all columns match" clause to the query.
	WhereAll(columns []string, args ...any) Query
	// WhereAny adds a "where any of columns match" clause to the query.
	WhereAny(columns []string, args ...any) Query
	// WhereBetween adds a "where column between x and y" clause to the query.
	WhereBetween(column string, x, y any) Query
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
	// WhereNotBetween adds a "where column not between x and y" clause to the query.
	WhereNotBetween(column string, x, y any) Query
	// WhereNotIn adds a "where column not in" clause to the query.
	WhereNotIn(column string, values []any) Query
	// WhereNotNull adds a "where column is not null" clause to the query.
	WhereNotNull(column string) Query
	// WhereNull adds a "where column is null" clause to the query.
	WhereNull(column string) Query
	// WithoutEvents disables event firing for the query.
	WithoutEvents() Query
	// WithoutGlobalScopes disables all global scopes for the query.
	WithoutGlobalScopes(names ...string) Query
	// Without removes the given relations from the eager-load list set by With.
	// Mirrors fedaco's without().
	Without(relations ...string) Query
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

type QueryWithObserver interface {
	Observe(model any, observer Observer)
}

type ModelWithConnection interface {
	// Connection gets the connection name for the model.
	Connection() string
}

type ModelWithGlobalScopes interface {
	GlobalScopes() map[string]func(Query) Query
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
