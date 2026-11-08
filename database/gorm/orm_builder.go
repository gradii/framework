package gorm

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/gradii/framework/contracts/config"
	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/contracts/log"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	"github.com/gradii/framework/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/database/utils"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
	"github.com/gradii/framework/support/deep"
	"github.com/gradii/framework/support/str"
	"github.com/spf13/cast"
)

type OrmBuilder struct {
	config       config.Config
	ctx          context.Context
	grammar      contractsdriver.Grammar
	queryGrammar querybuilder.Grammar
	components   databasedriver.QueryComponents
	connection   *databasedriver.Connection
	log          log.Log
	logger       contractslogger.Logger
	// writer is the connection's writer pool, and tx is the transaction the query runs inside when it
	// is bound to one. A query inside a transaction must run every statement on tx: the pool would
	// execute in auto-commit, so a rollback would roll nothing back and a locking read would take no
	// lock.
	writer          *sql.DB
	tx              *sql.Tx
	queries         map[string]*OrmBuilder
	modelToObserver []contractsorm.ModelToObserver
	conditions      Conditions
	// inverseChaperone carries the after-query write-back a relation read through Related(...)
	// performs: the parent the relation was resolved from and the related model's inverse
	// relation name. Get / First and the chunked page reads hand that parent to every result
	// model, the way Eloquent's Builder::get() runs the after-query callbacks chaperone()
	// registered on the relation's builder. nil for every query that is not a relation read.
	inverseChaperone  *inverseChaperoneState
	dbConfig          contractsdatabase.Config
	mutex             *sync.Mutex
	telemetryResolver contractstelemetry.Resolver
	query             *querybuilder.Builder
}

func NewOrmBuilder(
	ctx context.Context,
	config config.Config,
	dbConfig contractsdatabase.Config,
	writer *sql.DB,
	tx *sql.Tx,
	grammar contractsdriver.Grammar,
	log log.Log,
	modelToObserver []contractsorm.ModelToObserver,
	conditions *Conditions,
	telemetryResolver contractstelemetry.Resolver,
	supplied ...databasedriver.QueryComponents,
) *OrmBuilder {
	components, initErr := databasedriver.ResolveQueryComponents(nil, contractsdriver.GrammarOptions{Driver: dbConfig.Driver, Prefix: dbConfig.Prefix})
	if len(supplied) > 0 {
		components = supplied[0].WithContext(ctx)
		initErr = components.Validate()
	}
	if initErr != nil {
		// Keep a usable builder to carry the construction error through fluent methods.
		components.Grammar = querybuilder.NewBaseGrammar(dbConfig.Prefix)
		components.Processor = querybuilder.NewProcessor("")
	}
	builder := querybuilder.NewBuilder(components.Grammar, components.Processor)
	builder.SetDriver(dbConfig.Driver)
	builder.SetError(initErr)

	queryImpl := &OrmBuilder{
		config:            config,
		ctx:               ctx,
		dbConfig:          dbConfig,
		writer:            writer,
		tx:                tx,
		grammar:           grammar,
		queryGrammar:      components.Grammar,
		components:        components,
		log:               log,
		logger:            queryLogger(config, log),
		modelToObserver:   modelToObserver,
		queries:           make(map[string]*OrmBuilder),
		telemetryResolver: telemetryResolver,
		query:             builder,
	}

	if conditions != nil {
		queryImpl.conditions = *conditions
	}
	if initErr != nil {
		queryImpl.conditions.err = initErr
	}

	return queryImpl
}

// queryLogger returns the logger a compiled statement is traced through, so the query log
// (EnableQueryLog / GetQueryLog) captures exactly what the ORM ran. It is nil for the shape a query
// built without a config or a log carries — the one the package's own unit tests construct — in
// which case tracing is a no-op.
func queryLogger(config config.Config, log log.Log) contractslogger.Logger {
	if config == nil || log == nil {
		return nil
	}

	return db.NewLogger(config, log)
}

// trace reports a compiled statement the ORM ran through the connection's logger, with its bindings
// inlined, which is the form the query log captures. It is a no-op when the query carries no logger.
func (r *OrmBuilder) trace(begin *carbon.Carbon, statement string, bindings []any, rowsAffected int64, err error) {
	if r.logger == nil {
		return
	}

	r.logger.Trace(r.ctx, begin, db.Explain(statement, bindings...), rowsAffected, err)
}

func BuildQuery(ctx context.Context, config config.Config, connection string, log log.Log, modelToObserver []contractsorm.ModelToObserver, telemetryResolver contractstelemetry.Resolver) (*OrmBuilder, contractsdatabase.Config, error) {
	if connection == "" {
		connection = config.GetString("database.default")
	}
	driverCallback, exist := config.Get(fmt.Sprintf("database.connections.%s.via", connection)).(func() (contractsdriver.Driver, error))
	if !exist {
		return nil, contractsdatabase.Config{}, errors.DatabaseConfigNotFound
	}

	driver, err := driverCallback()
	if err != nil {
		return nil, contractsdatabase.Config{}, err
	}

	pool := driver.Pool()
	if len(pool.Writers) == 0 {
		return nil, contractsdatabase.Config{}, errors.DatabaseConfigNotFound
	}
	// The driver's source name selects its endpoint; ORM policy and model connection selection use
	// the requested logical name. Pool identity stays on the handle instead of leaking into Config.
	dbConfig := pool.Writers[0]
	dbConfig.Connection = connection
	dialect, err := databasedriver.ResolveDialect(driver)
	if err != nil {
		return nil, dbConfig, err
	}
	logger := db.NewLogger(config, log)
	connectionHandle, err := databasedriver.Open(config, logger, pool, connection, telemetryResolver, dialect)
	if err != nil {
		return nil, dbConfig, err
	}
	var writer *sql.DB
	options := contractsdriver.GrammarOptions{Driver: pool.Writers[0].Driver, Prefix: pool.Writers[0].Prefix}
	if connectionHandle != nil {
		writer = connectionHandle.Writer()
	}
	if connectionHandle != nil && connectionHandle.SupportsServerVersion() {
		options.ServerVersion = func() (string, error) { return connectionHandle.ServerVersion(ctx) }
		if versioned, ok := driver.(contractsdriver.DriverWithServerVersion); ok {
			versioned.SetServerVersion(options.ServerVersion)
		}
	}
	components, err := databasedriver.ResolveQueryComponents(dialect, options)
	if err != nil {
		return nil, dbConfig, err
	}
	if connectionHandle != nil && connectionHandle.SupportsServerVersion() {
		components.VersionSource = connectionHandle.ServerVersion
	}
	query := NewOrmBuilder(ctx, config, dbConfig, writer, nil, driver.Grammar(), log, modelToObserver, nil, telemetryResolver, components)
	query.connection = connectionHandle
	return query, dbConfig, nil
}

// ConnectionIsCurrent lets the ORM facade evict a cached query after its logical connection was
// purged through another facade. Queries constructed directly without a managed handle are valid.
func (r *OrmBuilder) ConnectionIsCurrent() bool {
	return r.connection == nil || r.connection.IsCurrent()
}

// DEPRECATED Use BeginTransaction instead.
func (r *OrmBuilder) Begin() (contractsorm.Query, error) {
	return r.BeginTransaction()
}

func (r *OrmBuilder) BeginTransaction() (contractsorm.Query, error) {
	if r.InTransaction() {
		return r, nil
	}
	// Discover capabilities before BEGIN leases the only connection in a one-connection pool.
	if _, versioned := r.components.Dialect.(contractsdriver.DialectWithServerVersion); versioned && r.components.VersionSource != nil {
		if _, err := r.components.VersionSource(r.ctx); err != nil {
			return nil, err
		}
	}

	options, err := databasedriver.TransactionOptions(r.config, r.dbConfig.Connection)
	if err != nil {
		return nil, err
	}

	tx, err := r.writer.BeginTx(r.ctx, options)
	if err != nil {
		return nil, err
	}

	return r.new(tx), nil
}

func (r *OrmBuilder) Commit() error {
	if r.tx == nil {
		return errors.OrmQueryTransactionNotStarted
	}

	return r.tx.Commit()
}

func (r *OrmBuilder) Context() context.Context {
	return r.ctx
}

func (r *OrmBuilder) Count() (int64, error) {
	query := buildSelectForCount(r)

	sql, bindings, err := query.compileAggregate("count", countColumns(query))
	if err != nil {
		return 0, err
	}

	var count int64
	if err := query.queryScalar(&count, sql, bindings); err != nil {
		return 0, err
	}

	return count, nil
}

// GetCountForPagination returns the count a paginator draws its page from, mirroring Laravel's
// Builder::getCountForPagination and the compiler the raw query carries for it
// (Builder::runPaginationCountQuery): the count statement is built on a clone — the query keeps its
// own clauses and bindings — and a grouped or having query is counted by wrapping its select in a
// derived table named "aggregate_table", so groups are counted, not rows. Count() keeps Laravel's
// count() semantics — a single aggregate over the grouped result — which is why the paginator
// reads its total here instead.
func (r *OrmBuilder) GetCountForPagination(columns ...string) (int64, error) {
	query := r.addGlobalScopes().buildConditions()

	builder, err := query.newBuilder()
	if err != nil {
		return 0, err
	}

	columnsAny := make([]any, len(columns))
	for i, column := range columns {
		columnsAny[i] = column
	}

	sql, bindings := builder.GetCountForPagination(columnsAny...)
	if err := builder.Error(); err != nil {
		return 0, err
	}

	var count int64
	if err := query.queryScalar(&count, sql, bindings); err != nil {
		return 0, err
	}

	return count, nil
}

func (r *OrmBuilder) Create(value any) error {
	query := r.dest(value).buildConditions()

	if hasStringColumns(query.query.Selects()) && len(query.conditions.omit) > 0 {
		return errors.OrmQuerySelectAndOmitsConflict
	}

	// The three shapes GORM distinguished — a plain create, and one narrowed by Select or by Omit — are
	// one insert here, because the column filter is applied to the record itself.
	return query.create(value)
}

// --- chunked reads ---------------------------------------------------------------------------

// maxChunkInt is the largest page size a chunked read accepts, the int-range guard the raw query's
// chunk loops apply as well.
var maxChunkInt = int(^uint(0) >> 1)

// Chunk reads the query in offset pages of size rows, mirroring Eloquent's BuildsQueries::chunk:
// the query is given the model's qualified key ascending order when it carries none (Eloquent's
// Builder::enforceOrderBy override; the raw query builder would refuse instead), the caller's
// offset and limit are honoured as the starting point and the ceiling, every page is scanned into
// dest — a non-nil pointer to a slice of models — and callback runs with the 1-based page number
// after the page's model events, where a callback returning errors.DatabaseChunkStopped stops the
// walk cleanly (Laravel's `return false`). The loop continues only while a full page came back.
func (r *OrmBuilder) Chunk(size uint64, dest any, callback func(page int) error) error {
	if size == 0 {
		return nil
	}
	if size > uint64(maxChunkInt) {
		return errors.QueryPaginationOverflow
	}

	query := r.orderedForChunk().addGlobalScopes()

	skip, _ := query.query.OffsetValue()
	remaining, limited := query.query.LimitValue()
	maxStep := maxChunkInt - int(size)

	for page, offset := 1, skip; ; page++ {
		limit := int(size)
		if limited && remaining < limit {
			limit = remaining
		}
		if limit <= 0 {
			return nil
		}

		pageQuery := query.clone()
		pageQuery.query.Offset(offset).Limit(limit)

		rows, err := pageQuery.pageInto(dest)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if limited {
			remaining -= len(rows)
			if remaining < 0 {
				remaining = 0
			}
		}

		if err := pageQuery.retrieved(dest); err != nil {
			return err
		}

		// Every chunked page is its own get() in Eloquent, so the relation read's inverse
		// write-back runs per page.
		if err := pageQuery.applyInverseChaperone(dest); err != nil {
			return err
		}

		if err := callback(page); err != nil {
			if errors.Is(err, errors.DatabaseChunkStopped) {
				return nil
			}

			return err
		}

		if len(rows) != int(size) {
			return nil
		}
		if offset > maxStep {
			return errors.QueryPaginationOverflow
		}
		offset += int(size)
	}
}

// ChunkById chunks by comparing ids, mirroring BuildsQueries::chunkById: each page is fetched with
// ForPageAfterId — the caller's offset applies to the first page only, the caller's limit caps
// every page size, and a zero-size chunk breaks before any query is issued — and the alias value
// of the page's last row drives the next page's keyset predicate. The column and alias default
// the way the relation overrides resolve them: a many-to-many read walks the related key
// qualified by its table, a through read the related model's qualified key, anything else the
// model's bare key name (Eloquent's Builder::defaultKeyName); column may be (column) or
// (column, alias).
func (r *OrmBuilder) ChunkById(size uint64, dest any, callback func(page int) error, column ...string) error {
	return r.chunkById(size, dest, callback, false, column...)
}

// ChunkByIdDesc is ChunkById in descending order, mirroring BuildsQueries::chunkByIdDesc: each
// page is fetched with ForPageBeforeId.
func (r *OrmBuilder) ChunkByIdDesc(size uint64, dest any, callback func(page int) error, column ...string) error {
	return r.chunkById(size, dest, callback, true, column...)
}

// chunkById is the shared keyset loop behind ChunkById and ChunkByIdDesc, mirroring
// BuildsQueries::orderedChunkById: a caller's offset and limit are honoured (the first page keeps
// the offset, later pages drop it, the limit caps the page size), and the loop continues only
// while a complete page came back. The id of the last row of each page is read under alias and
// drives the next page's keyset predicate.
func (r *OrmBuilder) chunkById(size uint64, dest any, callback func(page int) error, descending bool, column ...string) error {
	if size == 0 {
		return nil
	}
	if size > uint64(maxChunkInt) {
		return errors.QueryPaginationOverflow
	}

	query := r.addGlobalScopes()

	keyColumn, alias := query.chunkByIdColumn(column)

	skip, hasSkip := query.query.OffsetValue()
	remaining, hasLimit := query.query.LimitValue()

	var lastID any

	for page := 1; ; page++ {
		limit := int(size)
		if hasLimit && remaining < limit {
			limit = remaining
		}
		if limit <= 0 {
			return nil
		}

		pageQuery := query.clone()
		if hasSkip && skip > 0 && page > 1 {
			pageQuery.query.Offset(0)
		}
		if descending {
			pageQuery.query.ForPageBeforeId(limit, lastID, keyColumn)
		} else {
			pageQuery.query.ForPageAfterId(limit, lastID, keyColumn)
		}

		rows, err := pageQuery.pageInto(dest)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if hasLimit {
			remaining -= len(rows)
			if remaining < 0 {
				remaining = 0
			}
		}

		if err := pageQuery.retrieved(dest); err != nil {
			return err
		}

		// Every chunked page is its own get() in Eloquent, so the relation read's inverse
		// write-back runs per page.
		if err := pageQuery.applyInverseChaperone(dest); err != nil {
			return err
		}

		if err := callback(page); err != nil {
			if errors.Is(err, errors.DatabaseChunkStopped) {
				return nil
			}

			return err
		}

		lastValue := pageQuery.chunkAliasValue(rows[len(rows)-1], alias)
		if lastValue == nil {
			return errors.DatabaseChunkByIdColumnNotPresent.Args(alias)
		}
		lastID = lastValue

		if len(rows) != int(size) {
			return nil
		}
	}
}

// Each executes callback over every matching model one at a time, reading 1000 rows per statement
// and walking offset pages — the walk BuildsQueries::each, a chunk(1000) wrapper, performs. dest
// is a non-nil pointer to a model and holds the current row when the callback runs; index is the
// model's key within its page, the key Laravel hands each()'s callback.
func (r *OrmBuilder) Each(dest any, callback func(index int) error) error {
	page, model, err := newElementPage(dest)
	if err != nil {
		return err
	}

	return r.Chunk(1000, page.Interface(), func(int) error {
		models := page.Elem()
		for key := 0; key < models.Len(); key++ {
			model.Elem().Set(models.Index(key))

			if err := callback(key); err != nil {
				return err
			}
		}

		return nil
	})
}

// EachById executes callback over every matching model one at a time, walking the keyset like
// BuildsQueries::eachById. dest holds the current row and index is the model's 0-based ordinal
// across the whole iteration — Laravel's ((page-1)*size + key) arithmetic.
func (r *OrmBuilder) EachById(size uint64, dest any, callback func(index int) error, column ...string) error {
	page, model, err := newElementPage(dest)
	if err != nil {
		return err
	}

	return r.chunkById(size, page.Interface(), func(pageNumber int) error {
		models := page.Elem()
		for key := 0; key < models.Len(); key++ {
			model.Elem().Set(models.Index(key))

			if err := callback(((pageNumber - 1) * int(size)) + key); err != nil {
				return err
			}
		}

		return nil
	}, false, column...)
}

// Lazy walks the query in pages of chunkSize (default 1000) and hands every hydrated model to
// callback one at a time through dest (a non-nil pointer to a model), with index counting the
// models seen so far, mirroring BuildsQueries::lazy: the query is given the model's qualified key
// ascending order when it carries none, a chunk size below 1 is refused
// (errors.QueryInvalidChunkSize), and the walk ends once a page comes back shorter than
// chunkSize. Go has no lazy collection, so the walk runs immediately and reports its error; the
// page sequence is the one Laravel's LazyCollection would pull.
func (r *OrmBuilder) Lazy(dest any, callback func(index int) error, chunkSize ...uint64) error {
	size := uint64(1000)
	if len(chunkSize) > 0 {
		size = chunkSize[0]
	}
	if size == 0 || size > uint64(maxChunkInt) {
		return errors.QueryInvalidChunkSize
	}

	query := r.orderedForChunk().addGlobalScopes()

	page, model, err := newElementPage(dest)
	if err != nil {
		return err
	}

	seen := 0
	for pageNumber := 1; ; pageNumber++ {
		pageQuery := query.clone()
		pageQuery.query.ForPage(pageNumber, int(size))

		rows, err := pageQuery.pageInto(page.Interface())
		if err != nil {
			return err
		}
		if err := pageQuery.retrieved(page.Interface()); err != nil {
			return err
		}

		// Every lazy page is its own get() in Eloquent, so the inverse write-back runs per page.
		if err := pageQuery.applyInverseChaperone(page.Interface()); err != nil {
			return err
		}

		models := page.Elem()
		for key := 0; key < models.Len(); key++ {
			model.Elem().Set(models.Index(key))

			if err := callback(seen); err != nil {
				if errors.Is(err, errors.DatabaseChunkStopped) {
					return nil
				}

				return err
			}
			seen++
		}

		if len(rows) < int(size) {
			return nil
		}
	}
}

// LazyById is Lazy with chunkById's keyset, mirroring BuildsQueries::lazyById: each page is
// fetched with ForPageAfterId using the last model's id as the keyset, with the same column/alias
// defaults ChunkById resolves.
func (r *OrmBuilder) LazyById(chunkSize uint64, dest any, callback func(index int) error, column ...string) error {
	return r.lazyById(chunkSize, dest, callback, false, column...)
}

// LazyByIdDesc is LazyById in descending order, mirroring BuildsQueries::lazyByIdDesc: each page
// is fetched with ForPageBeforeId.
func (r *OrmBuilder) LazyByIdDesc(chunkSize uint64, dest any, callback func(index int) error, column ...string) error {
	return r.lazyById(chunkSize, dest, callback, true, column...)
}

// lazyById is the shared keyset walk behind LazyById and LazyByIdDesc, mirroring
// BuildsQueries::orderedLazyById: every model of a page is handed to the callback, the walk ends
// after a page that came back short of chunkSize, and the next keyset is read from the last row
// only when a full page came back — exactly where Laravel reads it. A chunk size below 1 is
// refused (errors.QueryInvalidChunkSize), mirroring orderedLazyById's InvalidArgumentException.
func (r *OrmBuilder) lazyById(size uint64, dest any, callback func(index int) error, descending bool, column ...string) error {
	if size == 0 || size > uint64(maxChunkInt) {
		return errors.QueryInvalidChunkSize
	}

	query := r.addGlobalScopes()

	keyColumn, alias := query.chunkByIdColumn(column)

	page, model, err := newElementPage(dest)
	if err != nil {
		return err
	}

	var lastID any
	seen := 0

	for {
		pageQuery := query.clone()
		if descending {
			pageQuery.query.ForPageBeforeId(int(size), lastID, keyColumn)
		} else {
			pageQuery.query.ForPageAfterId(int(size), lastID, keyColumn)
		}

		rows, err := pageQuery.pageInto(page.Interface())
		if err != nil {
			return err
		}
		if err := pageQuery.retrieved(page.Interface()); err != nil {
			return err
		}

		// Every lazy page is its own get() in Eloquent, so the inverse write-back runs per page.
		if err := pageQuery.applyInverseChaperone(page.Interface()); err != nil {
			return err
		}

		models := page.Elem()
		for key := 0; key < models.Len(); key++ {
			model.Elem().Set(models.Index(key))

			if err := callback(seen); err != nil {
				if errors.Is(err, errors.DatabaseChunkStopped) {
					return nil
				}

				return err
			}
			seen++
		}

		if len(rows) < int(size) {
			return nil
		}

		lastValue := pageQuery.chunkAliasValue(rows[len(rows)-1], alias)
		if lastValue == nil {
			return errors.DatabaseChunkByIdColumnNotPresent.Args(alias)
		}
		lastID = lastValue
	}
}

// orderedForChunk returns the query with Eloquent's enforced order for chunk()/lazy(): the
// Eloquent builder's override adds "order by <qualified key> asc" when the query carries no order
// at all, where the raw query builder refuses to chunk. A query with no model keeps the raw
// behaviour and reports QueryOrderRequired when its first page compiles.
func (r *OrmBuilder) orderedForChunk() *OrmBuilder {
	if len(r.query.State().Orders) > 0 || len(r.query.State().UnionOrders) > 0 {
		return r
	}
	// A query that already carries a condition error — a guarded relation read, say — keeps it:
	// the enforced order must not replace the error the walk is going to report.
	if r.conditions.err != nil {
		return r
	}

	column := r.qualifiedPrimaryKeyColumn()
	if column == "" {
		return r.withError(errors.QueryOrderRequired).(*OrmBuilder)
	}

	query := r.clone()
	query.query.OrderByRaw(r.queryGrammar.Wrap(column)+" asc", nil)

	return query
}

// chunkByIdColumn resolves the column and alias a chunkById-family call walks the keyset with,
// mirroring BuildsQueries::orderedChunkById and the relation overrides: a caller-supplied column
// is used verbatim, and a caller-supplied alias always wins. Without an alias the caller's column
// is read back under the column itself on the plain builder (`$alias ??= $column`, qualifier and
// all), while a many-to-many read and a through read read it back under the related model's key
// name — the alias their overrides default even when a column is passed
// (BelongsToMany::orderedChunkById's `$alias ??= $this->getRelatedKeyName()`,
// HasOneOrManyThrough::chunkById's `$alias ??= $this->getRelated()->getKeyName()`). With no caller
// column the column defaults the way the relation overrides resolve theirs: a many-to-many read
// walks the related key qualified by its table, a through read the related model's qualified key,
// and anything else the model's bare key name (Eloquent's Builder::defaultKeyName); a query with
// no model falls back to Query\Builder's "id".
func (r *OrmBuilder) chunkByIdColumn(column []string) (string, string) {
	keyColumn := ""
	if len(column) > 0 {
		keyColumn = column[0]
	}

	callerColumn := keyColumn != ""

	if keyColumn == "" {
		switch {
		case r.conditions.pivotRead != nil:
			keyColumn = r.conditions.pivotRead.relatedTable + "." + r.conditions.pivotRead.relatedKey
		case r.conditions.throughRead != nil:
			keyColumn = r.qualifiedPrimaryKeyColumn()
		default:
			if model := r.parentModel(); model != nil {
				if schema, err := parseSchema(model); err == nil && len(schema.PrimaryFields) > 0 {
					keyColumn = schema.PrimaryFields[0].DBName
				}
			}
		}
		if keyColumn == "" {
			keyColumn = "id"
		}
	}

	alias := ""
	switch {
	case len(column) > 1 && column[1] != "":
		alias = column[1]
	case !callerColumn:
		alias = unqualifyColumn(keyColumn)
	case r.conditions.pivotRead != nil:
		alias = r.conditions.pivotRead.relatedKey
	case r.conditions.throughRead != nil:
		alias = unqualifyColumn(r.qualifiedPrimaryKeyColumn())
		if alias == "" {
			alias = "id"
		}
	default:
		alias = keyColumn
	}

	return keyColumn, alias
}

// unqualifyColumn strips a "table." (or "column as alias") qualifier off a column name, deriving
// the bare key name a chunkById-family keyset alias defaults to.
func unqualifyColumn(column string) string {
	separator := "."
	if strings.Contains(strings.ToLower(column), " as ") {
		separator = " as "
	}

	if index := strings.LastIndex(strings.ToLower(column), separator); index != -1 {
		return column[index+len(separator):]
	}

	return column
}

// chunkAliasValue reads the keyset alias off a page's last row map the way Laravel reads it with
// data_get($model, $alias) on the hydrated model: a many-to-many read has already migrated every
// pivot_-prefixed attribute off that model (BelongsToMany::hydratePivotRelation and its
// migratePivotAttributes, which unset them), so a pivot_-prefixed alias is not present even when
// the statement selected it — Laravel's chunkById/lazyById then aborts naming the alias. Every
// other alias is read from the row as scanned.
func (r *OrmBuilder) chunkAliasValue(row map[string]any, alias string) any {
	if r.conditions.pivotRead != nil && strings.HasPrefix(alias, "pivot_") {
		return nil
	}

	return row[alias]
}

// applyPivotReadSelect rewrites a many-to-many relation read's select list the way
// BelongsToMany::shouldSelect installs it for get()/first()/sole()/find()/cursor() and — through
// prepareQueryBuilder — for chunk()/each()/lazy(): with no selection of the caller's own the
// related table's columns come first, and every pivot key follows aliased pivot_<column> —
// foreign pivot key, related pivot key, the morph type on a polymorphic relation
// (MorphToMany::aliasedPivotColumns), then the related model's declared Pivot-struct columns,
// Laravel's withPivot list, extended by the object layer's call-time WithPivot list (cfg.withPivotColumns) — so the joined pivot row cannot overwrite the related model's own
// columns on hydration, and the aliases are what hydratePivotRow migrates into the Pivot struct.
// (Oracle, illuminate/database v12.55.1, executed against sqlite: `$user->friends()->get()`
// selects `"users".*, "friends"."user_id" as "pivot_user_id", "friends"."friend_id" as
// "pivot_friend_id"`; a withPivot('friend_level_id') adds
// `"friends"."friend_level_id" as "pivot_friend_level_id"`; first(['email']) selects
// `"email", aliases`; `$post->tags()->get()` interleaves the morph type —
// `"taggables"."taggable_id" as "pivot_taggable_id", "taggables"."tag_id" as "pivot_tag_id",
// "taggables"."taggable_type" as "pivot_taggable_type", "taggables"."taxonomy" as
// "pivot_taxonomy"`.)
//
// A caller's own column list — get($columns)/first([$columns]) — keeps its columns and still gains
// the aliases, the addSelect of Laravel's get() path; the builder-forwarded reads (value, pluck,
// exists) drop them through withoutPivotReadSelect, the way those calls bypass shouldSelect.
func (r *OrmBuilder) applyPivotReadSelect(builder *querybuilder.Builder) {
	cfg := r.conditions.pivotRead
	if cfg == nil {
		return
	}

	if len(builder.Selects()) == 0 {
		builder.SelectRaw(r.ident(cfg.relatedTable, "*"), nil)
	}

	columns := []string{cfg.foreignPivotKey, cfg.relatedPivotKey, cfg.morphTypeColumn}
	if cfg.plan != nil {
		columns = append(columns, cfg.plan.extraColumns...)
	}
	columns = append(columns, cfg.withPivotColumns...)

	seen := make(map[string]bool, len(columns))
	for _, column := range columns {
		if column == "" || seen[column] {
			continue
		}
		seen[column] = true

		builder.SelectRaw(r.ident(cfg.pivotTable, column)+" as "+r.ident("pivot_"+column), nil)
	}
}

// compileChunkSelect renders one page of a chunked read: the plain select plus the pivot alias
// list when the query is a many-to-many relation read. A through read's select list is added by
// compileSelect's applyThroughReadSelect, the way HasOneOrManyThrough::prepareQueryBuilder adds
// it for get()/chunk() as well.
func (r *OrmBuilder) compileChunkSelect() (string, []any, error) {
	if raw := r.conditions.raw; raw != nil {
		return fmt.Sprint(raw.query), raw.args, nil
	}

	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	r.applyPivotReadSelect(builder)
	r.applyThroughReadSelect(builder)

	sql, bindings := builder.ToSelectSQL()
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// pageInto executes one compiled page and scans its models into dest, returning the page's rows
// as column-keyed maps — the form the keyset alias is read from — so the loop can size the page
// and pick its last id. dest must be a non-nil pointer to a slice of models (structs or struct
// pointers); it is reset to the page.
func (r *OrmBuilder) pageInto(dest any) ([]map[string]any, error) {
	destValue := reflect.ValueOf(dest)
	if !destValue.IsValid() || destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return nil, errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer to a slice")
	}

	target := reflect.Indirect(destValue)
	if target.Kind() != reflect.Slice {
		return nil, errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer to a slice")
	}

	elementType := target.Type().Elem()
	if elementType.Kind() != reflect.Struct && (elementType.Kind() != reflect.Pointer || elementType.Elem().Kind() != reflect.Struct) {
		return nil, errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "pointer to a slice of models")
	}

	statement, bindings, err := r.compileChunkSelect()
	if err != nil {
		return nil, err
	}

	rows, err := r.queryRows(statement, bindings)
	if err != nil {
		return nil, err
	}
	defer errors.Ignore(rows.Close)

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, 8)
	page := reflect.MakeSlice(target.Type(), 0, 0)

	for rows.Next() {
		row, rowErr := scanRowMap(rows, columns)
		if rowErr != nil {
			return nil, rowErr
		}
		items = append(items, row)

		if elementType.Kind() == reflect.Pointer {
			element := reflect.New(elementType.Elem())
			if err := scanRow(row, element.Interface()); err != nil {
				return nil, err
			}
			if err := r.hydratePivotRow(row, element); err != nil {
				return nil, err
			}

			page = reflect.Append(page, element)

			continue
		}

		element := reflect.New(elementType).Elem()
		if err := scanRow(row, element.Addr().Interface()); err != nil {
			return nil, err
		}
		if err := r.hydratePivotRow(row, element); err != nil {
			return nil, err
		}

		page = reflect.Append(page, element)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	destValue.Elem().Set(page)

	return items, nil
}

// newElementPage validates an element-walk destination — a non-nil pointer to a model — and
// returns a pointer to a fresh slice of that model type plus the destination itself, so a page
// can be scanned into the slice and each model copied into dest before its callback runs.
func newElementPage(dest any) (reflect.Value, reflect.Value, error) {
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Struct {
		return reflect.Value{}, reflect.Value{}, errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer to a model")
	}

	return reflect.New(reflect.SliceOf(target.Elem().Type())), target, nil
}

func (r *OrmBuilder) Cursor() chan contractsdb.Row {
	query := r.addGlobalScopes().buildConditions()

	cursorChan := make(chan contractsdb.Row)
	go func() {
		var err error

		defer func() {
			if err != nil {
				cursorChan <- &Row{query: r, err: err}
			}

			close(cursorChan)
		}()

		statement, bindings, compileErr := query.compileSelect()
		if compileErr != nil {
			err = compileErr

			return
		}

		rows, queryErr := query.queryRows(statement, bindings)
		if queryErr != nil {
			err = queryErr

			return
		}
		defer errors.Ignore(rows.Close)

		columns, columnsErr := rows.Columns()
		if columnsErr != nil {
			err = columnsErr

			return
		}

		for rows.Next() {
			// Each row is read as it arrives so a large result set is streamed rather than held.
			row, rowErr := scanRowMap(rows, columns)
			if rowErr != nil {
				err = rowErr

				return
			}

			cursorChan <- &Row{query: r, row: row}
		}

		err = rows.Err()
	}()
	return cursorChan
}

func (r *OrmBuilder) DB() (*sql.DB, error) {
	return r.writer, nil
}

func (r *OrmBuilder) Delete(dests ...any) (*contractsdb.Result, error) {
	var (
		dest  any
		query *OrmBuilder
	)

	if len(dests) > 0 {
		dest = dests[0]
		query = r.dest(dest).addGlobalScopes().buildConditions()
	} else {
		query = r.addGlobalScopes().buildConditions()
	}

	if err := query.deleting(dest); err != nil {
		return nil, err
	}

	// Model::delete touches the owning models before the row goes away
	// (`$this->touchOwners(); $this->performDeleteOnModel();`).
	if err := query.touchOwners(dest); err != nil {
		return nil, err
	}

	res, err := query.deleteOrSoftDelete()
	if err != nil {
		return nil, err
	}

	if err := query.deleted(dest); err != nil {
		return nil, err
	}

	return res, nil
}

func (r *OrmBuilder) Distinct(columns ...string) contractsorm.Query {
	cols := make([]any, len(columns))
	for i, column := range columns {
		cols[i] = column
	}
	r.query.Distinct(cols...)

	return r
}

func (r *OrmBuilder) Driver() string {
	return r.dbConfig.Driver
}

// Exec runs a statement the caller wrote. The statement is passed through untouched apart from the
// dialect's placeholder style, so a caller can use it for DDL and for anything the builder does not
// cover.
func (r *OrmBuilder) Exec(statement string, values ...any) (*contractsdb.Result, error) {
	query := r.buildConditions()

	return query.execStatement(statement, values)
}

func (r *OrmBuilder) Exists() (bool, error) {
	query := r.addGlobalScopes().buildConditions()

	// The statement selects rows and is limited to one, rather than selecting the literal 1: a
	// literal would be wrapped as an identifier, which some dialects reject.
	query.query.Limit(1)

	// exists() is forwarded to the Eloquent builder in Laravel, so neither relation read wears its
	// select rewrite: the through read keeps the builder's own select list rather than the
	// laravel_through_key alias, and the pivot read drops its alias list the same way (oracle:
	// exists() on a many-to-many relation compiles `select exists(select * from "users" inner join
	// "friends" ...)`).
	query = query.withoutThroughReadSelect().withoutPivotReadSelect()

	statement, bindings, err := query.compileSelect()
	if err != nil {
		return false, err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return false, err
	}
	defer errors.Ignore(rows.Close)

	return rows.Next(), nil
}

func (r *OrmBuilder) Find(dest any, conds ...any) error {
	query := r.dest(dest).addGlobalScopes().buildConditions()

	if err := filterFindConditions(conds...); err != nil {
		return err
	}

	// find() delegates an array of ids to findMany(), which reads every match through one whereKey
	// IN statement (HasOneOrManyThrough::find).
	if len(conds) == 1 && isSlice(conds[0]) && isSlicePointer(dest) {
		keys, _ := r.keyValues(conds[0])

		return r.FindMany(dest, keys)
	}

	query = query.withFindConditions(conds)

	statement, bindings, err := query.compileFind(conds)
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	count, err := query.scanRows(rows, dest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	// Find reports no row as an empty destination rather than as an error, and still runs the
	// retrieved hooks, which is what the behaviour this replaced did.
	if err := query.retrieved(dest); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	// A relation read (Related) applies the inverse write-back as its after-query step, run last
	// the way applyAfterQueryCallbacks runs after eagerLoadRelations.
	return query.applyInverseChaperone(dest)
}

// withFindConditions folds Find's inline conditions into the query's own, so they compile alongside
// everything else.
//
// A lone scalar addresses the model's primary key, which is the form the relation writers use; a
// column with its value is passed through as a single condition.
func (r *OrmBuilder) withFindConditions(conds []any) *OrmBuilder {
	if len(conds) == 0 {
		return r
	}

	// The condition lands on the query builder when it is written, in the shape applyWhereShape
	// gives the ORM's internal conditions (a raw fragment, a column with its value, or the
	// "col in ?" shorthand), so it sits after the clauses written before the call.
	add := func(column string, args []any) *OrmBuilder {
		applyWhereShape(r.query, column, args, core.BooleanAnd, false)

		return r
	}

	if column, ok := conds[0].(string); ok {
		if len(conds) > 1 {
			return add(column, conds[1:])
		}

		return r
	}

	// A list stands for several primary keys, which keep the IN form. The column is qualified with
	// the model's table, the way Laravel's find() delegates an array to findMany() and renders its
	// whereIn on the related model's qualified key name: a bare `id in (...)` is ambiguous on a
	// read that joins another table.
	if isSlice(conds[0]) {
		if column := r.qualifiedPrimaryKeyColumn(); column != "" {
			return add(column, []any{conds[0]})
		}

		return r
	}

	// A scalar stands for the primary key of whichever model is the destination. The key is qualified
	// with the model's table, the way GORM wrote it, so a statement that also joins another table
	// still compares the model's own id.
	if column := r.primaryKeyColumn(); column != "" {
		return add(column+" = ?", []any{conds[0]})
	}

	return r
}

// isScalarFindKey reports whether Find's conditions address a single primary key — the lone
// non-string scalar shape Laravel's find($id) takes, whereKey($id)->first() reading one row. A
// column with its value, a key list, a model or a map keeps the caller's shape.
func isScalarFindKey(conds []any) bool {
	if len(conds) != 1 {
		return false
	}

	switch conds[0].(type) {
	case string:
		return false
	}

	switch reflect.Indirect(reflect.ValueOf(conds[0])).Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		return false
	}

	return true
}

// FindOrFail retrieves the first row matching the given conditions, or, when the only condition is
// a slice of primary keys and dest points to a slice, every row whose key is in the list.
//
// The key-list form mirrors the findOrFail Laravel gives a through relation
// (HasOneOrManyThrough::findOrFail): the ids run through findMany's whereKey IN read, and when the
// rows found fall short of the unique ids requested the error names the related model and every
// requested id — "No query results for model [Model] 1, 2", the message ModelNotFoundException
// carries — wrapping errors.OrmRecordNotFound so callers matching the sentinel keep working (oracle:
// findOrFail([1, 2]) with post 1 present prints "No query results for model [HasManyThroughTestPost]
// 1, 2" — the whole request, not the difference — and findOrFail([]) returns an empty collection).
// An empty list reads nothing and reports no error.
//
// The single-condition form is unchanged: a miss reports the bare errors.OrmRecordNotFound.
func (r *OrmBuilder) FindOrFail(dest any, conds ...any) error {
	if len(conds) == 1 && isSlice(conds[0]) && isSlicePointer(dest) {
		return r.findOrFailKeyList(dest, conds[0])
	}

	query := r.dest(dest).addGlobalScopes().buildConditions()

	if err := filterFindConditions(conds...); err != nil {
		return err
	}

	query = query.withFindConditions(conds)

	statement, bindings, err := query.compileFind(conds)
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	count, err := query.scanRows(rows, dest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if count == 0 {
		return errors.OrmRecordNotFound
	}

	if err := query.retrieved(dest); err != nil {
		return err
	}

	// find() funnels through first(), so a found model carries the relation read's inverse
	// write-back too.
	return query.applyInverseChaperone(dest)
}

// FindOr retrieves the first row whose key matches id, or runs the callback when there is none,
// mirroring Laravel's Builder::findOr. It is Find's read with FirstOr's missing-row behaviour.
//
// When id is a slice of primary keys and dest points to a slice, the read is findMany's whereKey IN
// read and the callback answers only a short result — the semantics Laravel gives a relation's
// findOr (HasOneOrManyThrough::findOr): a complete key list returns the collection, a list with a
// missing id returns the callback's result, and an empty list returns the empty collection without
// calling the callback. The single-key form is unchanged: a missing key runs the callback.
func (r *OrmBuilder) FindOr(dest any, id any, callback func() error) error {
	if isSlice(id) && isSlicePointer(dest) {
		return r.findOrKeyList(dest, id, callback)
	}

	query := r.dest(dest).addGlobalScopes().buildConditions()

	if err := filterFindConditions(id); err != nil {
		return err
	}

	query = query.withFindConditions([]any{id})

	statement, bindings, err := query.compileFirst()
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	count, err := query.scanRows(rows, dest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if count == 0 {
		return callback()
	}

	if err := query.retrieved(dest); err != nil {
		return err
	}

	// findOr answers through find()'s first(), so the found path carries the inverse write-back
	// as well.
	return query.applyInverseChaperone(dest)
}

// findOrFailKeyList implements the key-list half of FindOrFail: every unique key must resolve.
func (r *OrmBuilder) findOrFailKeyList(dest any, id any) error {
	keys, _ := r.keyValues(id)

	found, err := r.readKeyList(dest, keys)
	if err != nil {
		return err
	}
	if found {
		return nil
	}

	return errors.OrmModelNotFound.Args(modelName(r.parentModel()), keyListString(keys))
}

// findOrKeyList implements the key-list half of FindOr: a short result is answered by the callback.
func (r *OrmBuilder) findOrKeyList(dest any, id any, callback func() error) error {
	keys, _ := r.keyValues(id)

	found, err := r.readKeyList(dest, keys)
	if err != nil {
		return err
	}
	if found {
		return nil
	}

	return callback()
}

// readKeyList reads the keys through findMany's whereKey IN statement into a fresh slice and reports
// whether every unique key was found. dest is written only when the read is complete, so a short
// result leaves the caller's slice alone the way the callback's result replaces the collection.
func (r *OrmBuilder) readKeyList(dest any, keys []any) (bool, error) {
	// Laravel's findMany([]) returns an empty collection without running a statement; there is
	// nothing to read and nothing missing.
	if len(keys) == 0 {
		return true, nil
	}

	query := r.dest(dest).addGlobalScopes().buildConditions()

	scoped := query.WhereKey(keys)
	listQuery, ok := scoped.(*OrmBuilder)
	if !ok {
		return false, errors.OrmQueryInvalidModel.Args(fmt.Sprintf("%T", scoped))
	}

	statement, bindings, err := listQuery.compileSelect()
	if err != nil {
		return false, err
	}

	rows, err := listQuery.queryRows(statement, bindings)
	if err != nil {
		return false, err
	}
	defer errors.Ignore(rows.Close)

	found := reflect.New(reflect.TypeOf(dest).Elem())
	count, err := listQuery.scanRows(rows, found.Interface())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if count != uniqueKeyCount(keys) {
		return false, nil
	}

	reflect.ValueOf(dest).Elem().Set(found.Elem())

	if err := listQuery.retrieved(dest); err != nil {
		return true, err
	}

	// findMany's whereKey IN read is a get() in Eloquent, so a relation read's key list carries
	// the same inverse write-back.
	return true, listQuery.applyInverseChaperone(dest)
}

// uniqueKeyCount counts the requested keys the way Laravel's array_unique does: duplicate ids add
// nothing to the count a complete read must reach.
func uniqueKeyCount(keys []any) int {
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		seen[fmt.Sprint(key)] = struct{}{}
	}

	return len(seen)
}

// keyListString joins the requested keys for the not-found message, Laravel's implode(', ', $ids).
func keyListString(keys []any) string {
	list := make([]string, len(keys))
	for index, key := range keys {
		list[index] = fmt.Sprint(key)
	}

	return strings.Join(list, ", ")
}

// isSlicePointer reports whether v is a non-nil pointer to a slice: the destination shape the
// FindOr/FindOrFail key-list forms read a collection into.
func isSlicePointer(v any) bool {
	if v == nil {
		return false
	}

	reflected := reflect.ValueOf(v)

	return reflected.Kind() == reflect.Pointer && !reflected.IsNil() && reflected.Elem().Kind() == reflect.Slice
}

func (r *OrmBuilder) First(dest any) error {
	_, err := r.firstRow(dest)

	return err
}

// firstRow runs the query as a single-row read, reporting whether a row was found and firing the
// retrieved hook when it was. A statement that matched nothing is not an error: First reads it as an
// empty destination, while the callers that care ask for the flag.
func (r *OrmBuilder) firstRow(dest any) (bool, error) {
	query := r.dest(dest).addGlobalScopes().buildConditions()

	statement, bindings, err := query.compileFirst()
	if err != nil {
		return false, err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return false, err
	}
	defer errors.Ignore(rows.Close)

	count, err := query.scanRows(rows, dest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	// The cursor closes before the retrieved hook runs, so the eager loads a scope's with() merged
	// — or the hook itself — read on a pool that is no longer waiting on this statement: a
	// single-connection pool would otherwise block on the row cursor First still holds.
	errors.Ignore(rows.Close)

	if count == 0 {
		return false, nil
	}

	if err := query.retrieved(dest); err != nil {
		return true, err
	}

	// First is limit(1)->get(), so the row a relation read returns carries the same after-query
	// write-back a Get does.
	if err := query.applyInverseChaperone(dest); err != nil {
		return true, err
	}

	return true, nil
}

func (r *OrmBuilder) FirstOr(dest any, callback func() error) error {
	found, err := r.firstRow(dest)
	if err != nil {
		return err
	}
	if !found {
		return callback()
	}

	return nil
}

func (r *OrmBuilder) FirstOrCreate(dest any, conds ...any) error {
	defer r.withZeroFor(conds...)()
	_, err := r.firstOrCreateWithCreated(dest, conds...)

	return err
}

func (r *OrmBuilder) FirstOrFail(dest any) error {
	found, err := r.firstRow(dest)
	if err != nil {
		return err
	}
	if !found {
		return errors.OrmRecordNotFound
	}

	return nil
}

// ValueOrFail retrieves a single column of the first row into dest, mirroring Laravel's
// Builder::valueOrFail: the read Value makes, firstOrFail([$column]) read back by the column's
// bare name, except that a miss reports the model-not-found error naming the model instead of
// leaving dest untouched (oracle: the found read is `select "name" from "users" limit 1`; a
// miss over whereKey('bar') throws ModelNotFoundException "No query results for model [Model].").
func (r *OrmBuilder) ValueOrFail(column string, dest any) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer")
	}

	scoped := r.singleColumnFirstQuery(column)

	statement, bindings, err := scoped.compileSelect()
	if err != nil {
		return err
	}

	// firstOrFail answers a read that matched nothing with ModelNotFoundException, whose id-less
	// message names the model and no ids — "No query results for model [Model]." — rendered by
	// errors.OrmModelNotFoundNoIds, which wraps OrmRecordNotFound. A row whose kept select list no
	// longer carries the column reads as null — the same answer value() gives (oracle: scope
	// selectRaw('name') + valueOrFail('email') runs `select name from "users" limit 1` and returns
	// null, no exception) — so dest is left alone and no error is reported.
	if err := scoped.queryColumnValue(dest, column, statement, bindings); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.OrmModelNotFoundNoIds.Args(modelName(r.parentModel()))
		}

		return err
	}

	return nil
}

// FirstOrNew looks a model up and, finding none, prepares one instead of storing it: the destination
// is filled from the query's own equalities, from the attributes and from the query's pending
// attributes (WithAttributes), the pending ones merging underneath the way newModelInstance()
// merges them, and nothing is written.
func (r *OrmBuilder) FirstOrNew(dest any, attributes any, values ...any) error {
	_, err := r.firstOrInit(dest, attributes, values, nil)

	return err
}

func (r *OrmBuilder) ForceDelete(dests ...any) (*contractsdb.Result, error) {
	var (
		dest  any
		query *OrmBuilder
	)

	if len(dests) > 0 {
		dest = dests[0]
		query = r.dest(dest).WithTrashed().(*OrmBuilder).addGlobalScopes().buildConditions()
	} else {
		query = r.WithTrashed().(*OrmBuilder).addGlobalScopes().buildConditions()
	}

	if err := query.forceDeleting(dest); err != nil {
		return nil, err
	}

	// Model::forceDelete runs the same touchOwners step as delete().
	if err := query.touchOwners(dest); err != nil {
		return nil, err
	}

	res, err := query.execDelete()
	if err != nil {
		return nil, err
	}

	if res.RowsAffected > 0 {
		if err := query.forceDeleted(dest); err != nil {
			return nil, err
		}
	}

	return res, nil
}

// Get retrieves every matching row into dest.
//
// A column list may be passed for this call only, mirroring Laravel's Builder::get($columns): the
// columns render into the statement this call runs — on the scoped copy it reads from, never on
// the receiver — and they are installed only when that copy carried no selection, so a select the
// caller pre-set or a global scope wrote survives verbatim and a statement compiled afterwards is
// unchanged.
func (r *OrmBuilder) Get(dest any, columns ...any) error {
	if len(columns) == 0 {
		return r.Find(dest)
	}

	// Eloquent applies the scopes before onceWithColumns runs: Builder::get calls applyScopes()
	// first, and the columns then go through the base builder's get(), whose onceWithColumns writes
	// them only when the scoped query carries no select list. A model whose global scope does
	// selectRaw('name') therefore compiles `select name from "users"` for get(['email']) — the
	// requested column is dropped — where installing it first would compile `select "users"."email",
	// name ...` and return the email (oracle, illuminate/database v12.55.1, executed against
	// sqlite: scope selectRaw('name') + get(['email']) compiles `select name from "users"` and the
	// rows carry only name; scope select('name') + get(['email']) compiles `select "name" from
	// "users"`). withScopesApplied hands back a private copy, so the install never reaches the
	// receiver and the scopes are marked applied for the Find below.
	query := r.dest(dest).addGlobalScopes().withScopesApplied()

	if !hasSelectColumns(query) {
		for _, col := range columnsToStrings(columns) {
			query.query.AddSelect(col)
		}
	}

	return query.Find(dest)
}

// columnsToStrings flattens a Get column argument the way Laravel's Arr::wrap does: a single column
// name stands alone, and a slice stands for several.
func columnsToStrings(columns []any) []string {
	result := make([]string, 0, len(columns))

	for _, column := range columns {
		switch value := column.(type) {
		case string:
			result = append(result, value)
		case []string:
			result = append(result, value...)
		case []any:
			for _, item := range value {
				result = append(result, fmt.Sprint(item))
			}
		default:
			result = append(result, fmt.Sprint(value))
		}
	}

	return result
}

// DEPRECATED Use GroupBy instead.
func (r *OrmBuilder) Group(column string) contractsorm.Query {
	return r.GroupBy(column)
}

func (r *OrmBuilder) GroupBy(column ...string) contractsorm.Query {
	if len(column) == 0 {
		return r
	}

	cols := make([]any, len(column))
	for i, c := range column {
		cols[i] = c
	}
	r.query.GroupBy(cols...)

	return r
}

func (r *OrmBuilder) Having(query any, args ...any) contractsorm.Query {
	if value, ok := query.(string); ok && containsPlaceholder(value) {
		r.query.HavingRaw(value, args, core.BooleanAnd)
	} else {
		r.query.Having(query, args...)
	}

	return r
}

// Join adds an inner join. With condition arguments it is Laravel's structured
// Builder::join($table, $first, $operator, $second): the columns compare in an "on" clause and the
// corpus's join('posts', 'users.id', '=', 'posts.user_id') compiles as `inner join "posts" on
// "users"."id" = "posts"."user_id"` (oracle); a single callback argument builds a nested on-clause
// the way Laravel's Closure form does. Without arguments the string is taken as a whole clause —
// the "table on condition" fragment form the relation writers and the legacy callers use, handled
// by the inner builder's raw join — which has no Laravel join() form (Laravel writes it as
// joinRaw), so the fragment passes through as written instead of being bound as a value.
func (r *OrmBuilder) Join(query string, args ...any) contractsorm.Query {
	if len(args) == 0 {
		r.query.JoinRaw(query, nil, "inner")

		return r
	}

	var first, operator, second any
	first = args[0]
	if len(args) > 1 {
		operator = args[1]
	}
	if len(args) > 2 {
		second = args[2]
	}

	r.query.Join(query, first, operator, second, "inner", false)

	return r
}

func (r *OrmBuilder) Limit(limit int) contractsorm.Query {
	r.query.Limit(limit)

	return r
}

// Take is an alias for Limit, mirroring Eloquent\Builder::take — the builder-level alias, always
// the plain limit. The relation objects override both Take and Limit with Eloquent's
// relation-level limit (HasOneOrMany::limit HasOneOrMany.php:555, HasOneOrManyThrough::limit
// HasOneOrManyThrough.php:772 — database/gorm/relations has.go / through.go), which keeps this
// plain limit for an existing parent and routes a non-existing parent through GroupLimit (oracle
// /tmp/followup-take-grouplimit/take_grouplimit.php H1 / H7). BelongsToMany::take
// (BelongsToMany.php:1492) is still the promoted plain limit on the many-to-many object.
func (r *OrmBuilder) Take(limit int) contractsorm.Query {
	return r.Limit(limit)
}

// OfMany configures the relation to read one row per parent through the aggregate join Laravel's
// ofMany installs, mirroring its string form: the column's aggregate is taken per parent and the
// related model's key joins the aggregate set with the same aggregate, so ties resolve to the
// extreme key. Column and aggregate default to "id" / "MAX". Only MIN and MAX are accepted,
// case-insensitively, exactly as Laravel's ofMany requires; any other aggregate makes the relation
// query report errors.OrmRelationInvalidOfManyAggregate when it is compiled.
func (r *OrmBuilder) OfMany(column, aggregate string) contractsorm.Query {
	if column == "" {
		column = "id"
	}
	if aggregate == "" {
		aggregate = "MAX"
	}
	return r.setOneOfMany([]oneOfManyColumn{{column: column, aggregate: aggregate}}, aggregate)
}

// OfManyColumns configures the relation the way ofMany's ordered column map does, e.g.
// ofMany(['published_at' => 'max', 'id' => 'max']): the pairs are taken in order with each later
// pair breaking ties in the earlier ones, and the related model's key is appended with MAX when
// the columns omit it. Every pair's aggregate must be MIN or MAX, as Laravel's ofMany requires.
func (r *OrmBuilder) OfManyColumns(columns ...contractsorm.OfManyColumn) contractsorm.Query {
	cols := make([]oneOfManyColumn, 0, len(columns))
	for _, col := range columns {
		aggregate := col.Aggregate
		if aggregate == "" {
			aggregate = "MAX"
		}
		cols = append(cols, oneOfManyColumn{column: col.Column, aggregate: aggregate})
	}
	return r.setOneOfMany(cols, "MAX")
}

// LatestOfMany is shorthand for ofMany with every column aggregated "MAX" — Laravel's
// latestOfMany($column), which defaults to "id". The related model's key is appended as MAX when
// the columns omit it.
func (r *OrmBuilder) LatestOfMany(column ...string) contractsorm.Query {
	if len(column) == 0 {
		column = []string{"id"}
	}
	columns := make([]contractsorm.OfManyColumn, 0, len(column))
	for _, col := range column {
		columns = append(columns, contractsorm.OfManyColumn{Column: col, Aggregate: "MAX"})
	}
	return r.OfManyColumns(columns...)
}

// OldestOfMany is shorthand for ofMany with every column aggregated "MIN" — Laravel's
// oldestOfMany($column), which defaults to "id". The related model's key is appended as MAX when
// the columns omit it, exactly as ofMany appends it.
func (r *OrmBuilder) OldestOfMany(column ...string) contractsorm.Query {
	if len(column) == 0 {
		column = []string{"id"}
	}
	columns := make([]contractsorm.OfManyColumn, 0, len(column))
	for _, col := range column {
		columns = append(columns, contractsorm.OfManyColumn{Column: col, Aggregate: "MIN"})
	}
	return r.OfManyColumns(columns...)
}

// setOneOfMany records a one-of-many configuration and the clause positions the builder stood at,
// so every path that reads the relation can rewrite the read into the aggregate join.
//
// Laravel's ofMany accepts only MIN and MAX (case-insensitively) and reports the first offending
// aggregate; the error is carried on the query, like every condition the ORM cannot accept, and it
// surfaces when the relation's statement is compiled. The message is Laravel's, verbatim (oracle:
// ofMany('id', 'count') throws InvalidArgumentException).
func (r *OrmBuilder) setOneOfMany(columns []oneOfManyColumn, keyAggregate string) contractsorm.Query {
	for _, column := range columns {
		if !strings.EqualFold(column.aggregate, "min") && !strings.EqualFold(column.aggregate, "max") {
			// Laravel throws before any configuration is installed, so a rejected aggregate never
			// leaves a one-of-many configuration on the query: any earlier configuration is
			// cleared, and the error surfaces when the statement is compiled.
			conditions := r.conditions
			conditions.oneOfMany = nil
			conditions.err = errors.OrmRelationInvalidOfManyAggregate.Args(column.aggregate)

			return r.setConditions(conditions)
		}
	}

	state := r.query.State()
	conditions := r.conditions
	conditions.oneOfMany = &oneOfManyConfig{
		columns:       columns,
		keyAggregate:  keyAggregate,
		whereMark:     len(state.Wheres),
		joinMark:      len(state.Joins),
		whereBindMark: len(state.Bindings["where"]),
		joinBindMark:  len(state.Bindings["join"]),
		// The removals are taken now, as newOneOfManySubQuery takes removedScopes() when ofMany
		// runs: only the ones written before OfMany reach the aggregate (oracle, illuminate/database
		// v12.55.1, real sqlite, /Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle.php O1-O5, O7, L1-L3 and
		// /tmp/r14v/probe_ofmany_orders.php: latestOfMany()->withoutGlobalScope('active') keeps
		// `"deleted_at" is null and "active" = ?` in the aggregate and drops "active" from the outer
		// query only).
		removals: scopeRemovalsOf(r),
	}
	return r.setConditions(conditions)
}

// Load lazily loads the named relation onto an already-populated model, the way Laravel's
// Model::load() does. The relation is eager-loaded against the model instance itself: the instance's
// primary key supplies the keys the related query is constrained by, so the model's own table is
// never re-read and a Load issued from an unconstrained query still attaches the receiver's own
// relations rather than whichever row the table happens to return first.
func (r *OrmBuilder) Load(model any, relation string, args ...any) error {
	if relation == "" {
		return errors.OrmQueryEmptyRelation
	}

	destType := reflect.TypeOf(model)
	if destType.Kind() != reflect.Pointer {
		return errors.OrmQueryModelNotPointer
	}

	if id := databaseorm.GetID(model); id == nil && !loadsWithoutParentKey(model, relation) {
		return errors.OrmQueryEmptyId
	}

	// Queue the relation the same way With does, then run the loaders directly against the model.
	// Going through With would return the contractsorm.Query interface, and the loaders live on the
	// concrete *Query, so the entries are queued here to avoid a type assertion on every call.
	entries, err := relations.ParseEagerLoad(append([]any{relation}, args...))
	if err != nil {
		return err
	}
	conditions := r.conditions
	for _, entry := range entries {
		conditions.eagerLoad = relations.MergeEagerLoadEntry(conditions.eagerLoad, entry)
	}

	return r.setConditions(conditions).applyEagerLoads(model)
}

// loadsWithoutParentKey reports whether relation — the head segment, for a dotted path — is one a
// Load can run without the parent's key. A MorphTo needs no parent key: its loader keys off the
// model's own morph type/id columns, and a missing type attaches the relation's nil/default
// without a query — what Eloquent's load('imageable') does for a brand-new model (oracle: zero
// statements, the relation reads null). A BelongsTo needs none either: it constrains the related
// read by the parent's foreign key, which a pivot struct — the model that reaches Load without a
// primary key — carries the same way any model does. Every other relation constrains the related
// read by the parent's key, which an empty id cannot supply.
func loadsWithoutParentKey(model any, relation string) bool {
	head, _, _ := strings.Cut(relation, ".")

	desc, err := relations.ResolveRelation(model, head)

	return err == nil && (desc.Kind == relKindMorphTo || desc.Kind == relKindBelongsTo)
}

func (r *OrmBuilder) LoadMissing(model any, relation string, args ...any) error {
	destType := reflect.TypeOf(model)
	if destType.Kind() != reflect.Pointer {
		return errors.OrmQueryModelNotPointer
	}

	t := reflect.TypeOf(model).Elem()
	v := reflect.ValueOf(model).Elem()
	for i := 0; i < t.NumField(); i++ {
		if !t.Field(i).IsExported() {
			continue
		}
		if t.Field(i).Name == relation {
			var id any
			if v.Field(i).Kind() == reflect.Pointer {
				if !v.Field(i).IsNil() {
					id = databaseorm.GetIDByReflect(v.Field(i).Type().Elem(), v.Field(i).Elem())
				}
			} else if v.Field(i).Kind() == reflect.Slice {
				if v.Field(i).Len() > 0 {
					return nil
				}
			} else {
				id = databaseorm.GetIDByReflect(v.Field(i).Type(), v.Field(i))
			}
			if cast.ToString(id) != "" {
				return nil
			}
		}
	}

	return r.Load(model, relation, args...)
}

func (r *OrmBuilder) LockForUpdate() contractsorm.Query {
	r.query.Lock(true)

	return r
}

// Make builds an unsaved model instance carrying the query's pending attributes and the attributes
// it is given, mirroring Builder::make: newModelInstance() merges $pendingAttributes underneath the
// attributes it is given (array_merge($this->pendingAttributes, $attributes)), so an attribute
// passed here wins over a pending one. Nothing is written.
func (r *OrmBuilder) Make(dest any, attributes ...any) error {
	if r.conditions.err != nil {
		return r.conditions.err
	}

	if err := r.applyPendingAttributes(dest); err != nil {
		return err
	}

	for _, attribute := range attributes {
		if err := r.assignValue(dest, attribute); err != nil {
			return err
		}
	}

	return nil
}

func (r *OrmBuilder) Model(value any) contractsorm.Query {
	if r.conditions.model != nil {
		return r
	}

	conditions := r.conditions
	conditions.model = value

	return r.setConditions(conditions)
}

func (r *OrmBuilder) Observe(model any, observer contractsorm.Observer) {
	mutex := r.mutex
	if mutex == nil {
		mutex = &sync.Mutex{}
		r.mutex = mutex
	}
	mutex.Lock()
	defer mutex.Unlock()
	r.modelToObserver = append(r.modelToObserver, contractsorm.ModelToObserver{
		Model:    model,
		Observer: observer,
	})
}

func (r *OrmBuilder) Offset(offset int) contractsorm.Query {
	r.query.Offset(offset)

	return r
}

// WithZero names columns a struct write carries even when the field holds its Go zero value. A Go
// struct cannot tell an unset field from one set to its zero value, so Create, Save and Update with a
// struct leave every zero field out by default; WithZero opts named columns back in. It does not narrow the write the
// way Select does: every non-zero field is still written. "*" names every column, Omit wins over
// it, and names resolve like Select and Omit (field name or column, optionally table-qualified).
// Calls accumulate and, like Omit and the other builder methods, stay on the query handle.
func (r *OrmBuilder) WithZero(columns ...string) contractsorm.Query {
	conditions := r.conditions
	conditions.withZero = append(append([]string(nil), conditions.withZero...), columns...)

	return r.setConditions(conditions)
}

// withZeroFor adds every key of the given column maps to the query's WithZero list for the length of
// one call, and returns the function that puts the handle's own list back; the Or* entry points run
// `defer r.withZeroFor(...)()`. A value a caller passes in an attribute or value map is an explicit
// assignment, which Eloquent writes even when it is null/false/0 (oracle v12.55.1, /tmp/r14/probe_a.php
// U4 and I2/I3; /tmp/r15v/probe_v.out P1); filling it into a struct would otherwise make it
// indistinguishable from an unset field. The keys belong to that one call: Eloquent's builder keeps
// no record of them, so a later write on the same builder carries only its own attributes
// (/tmp/r15v/probe_v.out P7b), and restoring the list keeps them from leaking into the next
// statement the handle runs. Non-map arguments add nothing.
func (r *OrmBuilder) withZeroFor(values ...any) (restore func()) {
	var columns []string
	for _, value := range values {
		if mapping, ok := asColumnMap(value); ok {
			for key := range mapping {
				columns = append(columns, key)
			}
		}
	}
	if len(columns) == 0 {
		return func() {}
	}

	own := r.conditions.withZero
	r.WithZero(columns...)

	return func() {
		conditions := r.conditions
		conditions.withZero = own
		r.setConditions(conditions)
	}
}

// writesZero reports whether a struct write carries column although its value is zero. The "*"
// wildcard does not reach the automatic timestamps: a zero created_at or updated_at stands for a
// timestamp the struct does not carry, so a wildcard write never clears a stored one, the rule
// saveAutoUpdateTime already applies to updated_at; naming the column explicitly writes it as NULL.
func (r *OrmBuilder) writesZero(schema *modelSchema, column string) bool {
	for _, name := range r.conditions.withZero {
		if name == "*" {
			if !automaticTimestampColumn(schema, column) {
				return true
			}

			continue
		}
		if resolveInsertColumn(schema, name) == column {
			return true
		}
	}

	return false
}

// automaticTimestampColumn reports whether column is one of the model's automatic timestamps.
func automaticTimestampColumn(schema *modelSchema, column string) bool {
	if schema == nil {
		return false
	}
	field, ok := schema.FieldsByDBName[column]

	return ok && (field.AutoCreateTime != 0 || field.AutoUpdateTime != 0)
}

func (r *OrmBuilder) Omit(columns ...string) contractsorm.Query {
	conditions := r.conditions
	conditions.omit = columns

	return r.setConditions(conditions)
}

// DEPRECATED: Use OrderByRaw instead
func (r *OrmBuilder) Order(value any) contractsorm.Query {
	return r.OrderByRaw(fmt.Sprintf("%s", value))
}

// OrderBy adds an "order by" clause. The direction must be asc or desc, as Laravel requires; any
// other direction is an error (errors.QueryOrderDirectionInvalid). A custom direction such as
// "DESC NULLS LAST" goes through OrderByRaw, the only entry point that renders its argument
// verbatim, so no caller-supplied string reaches the SQL unvalidated.
func (r *OrmBuilder) OrderBy(column string, direction ...string) contractsorm.Query {
	orderDirection := "asc"
	if len(direction) > 0 {
		orderDirection = strings.ToLower(direction[0])
	}
	if orderDirection != "asc" && orderDirection != "desc" {
		return r.withError(errors.QueryOrderDirectionInvalid).(*OrmBuilder)
	}

	return r.OrderByRaw(r.queryGrammar.Wrap(column) + " " + orderDirection)
}

func (r *OrmBuilder) OrderByDesc(column string) contractsorm.Query {
	return r.OrderByRaw(r.queryGrammar.Wrap(column) + " desc")
}

func (r *OrmBuilder) OrderByRaw(raw string) contractsorm.Query {
	r.query.OrderByRaw(raw, nil)

	return r
}

func (r *OrmBuilder) InRandomOrder() contractsorm.Query {
	return r.OrderByRaw(randomOrderFunction(r.dbConfig.Driver))
}

func (r *OrmBuilder) InTransaction() bool {
	return r.tx != nil
}

// OrWhere mirrors Eloquent's Builder::orWhere(), which is where() with the "or" connector.
func (r *OrmBuilder) OrWhere(query any, args ...any) contractsorm.Query {
	if callback, ok := query.(func(contractsorm.Query) contractsorm.Query); ok && len(args) == 0 {
		return r.addNestedWhereQuery(callback, core.BooleanOr)
	}

	r.query.OrWhere(query, args...)

	return r
}

// Paginate retrieves one page of rows, writing the total into total when it is not nil. It mirrors
// Laravel's Builder::paginate: a non-positive page and limit fall back to the defaults of page 1 and 15
// rows, a total already carried by the caller is an override that skips the count, and a zero total skips
// the page fetch (Laravel returns an empty collection when the count is zero). The count itself comes
// from GetCountForPagination, so a grouped query's total counts its groups.
func (r *OrmBuilder) Paginate(page, limit int, dest any, total *int64) error {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 15
	}

	offset := (page - 1) * limit

	// Resolve the total exactly as Laravel's Builder::paginate, and as database/db.Paginate does: a
	// non-zero total already carried by the caller stands for its $total argument and short-circuits
	// the count, while an unset (nil or zero) total is counted through GetCountForPagination -- the
	// count is written back only when the caller asked for it.
	resolvedTotal := int64(0)
	if total != nil {
		resolvedTotal = *total
	}
	if resolvedTotal == 0 {
		var (
			count int64
			err   error
		)
		if r.query.State().From == nil && r.conditions.model == nil {
			count, err = r.Model(dest).GetCountForPagination()
		} else {
			count, err = r.GetCountForPagination()
		}
		if err != nil {
			return err
		}

		resolvedTotal = count
		if total != nil {
			*total = count
		}
	}

	// The zero-count short-circuit is decided on the RESOLVED total, so a nil-total query whose count
	// is zero skips the page fetch, mirroring Laravel's `$results = $total ? ... : new Collection`.
	if resolvedTotal == 0 {
		return emptyPaginatedDest(dest)
	}

	return r.Offset(offset).Limit(limit).Find(dest)
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

// SimplePaginate retrieves one page of rows without counting the total, mirroring Laravel's
// Builder::simplePaginate (Eloquent Builder.php:1143): a non-positive page and limit fall back to the
// same defaults as Paginate — page 1 and 15 rows — the query runs with one probe row (limit+1), dest
// receives at most limit rows, and the returned bool reports whether the probe row was present, which
// is Laravel's hasMorePages(). No count statement runs, so the page carries no total.
//
// Oracle (v12.55.1, executed against sqlite; .claude/laravel-oracle/_simplepaginate_probe.php): a first
// page of 3 rows at perPage 2 runs `select * from "users" order by "id" asc limit 3 offset 0` as its
// only statement — paginate(2) runs a count first — trims its items to 2 and reports true; a last
// partial page runs `... limit 3 offset 2` and reports false; an exact page of 3 rows at perPage 3 runs
// `limit 4 offset 0` and reports false; a page beyond range runs `limit 3 offset 16` with 0 items and
// false; perPage 0 and null both fall back to 15 (`limit 16 offset 0`).
func (r *OrmBuilder) SimplePaginate(page, limit int, dest any) (bool, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 15
	}

	maxInt := int(^uint(0) >> 1)
	if limit == maxInt || page-1 > maxInt/limit {
		return false, errors.QueryPaginationOverflow
	}

	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Slice {
		return false, errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "pointer to slice")
	}

	if err := r.Offset((page - 1) * limit).Limit(limit + 1).Find(dest); err != nil {
		return false, err
	}

	rows := target.Elem()
	if rows.Len() > limit {
		rows.Set(rows.Slice(0, limit))

		return true, nil
	}

	return false, nil
}

// CursorPaginate fetches one keyset ("seek") page and scans it into dest, which must be a pointer to
// a slice: a *[]map[string]any for an untyped page, or a pointer to a slice of structs for a typed
// one. cursor is the encoded anchor — an empty string is the first page, and a non-empty malformed
// string is refused with errors.QueryCursorInvalid rather than silently restarting.
//
// The statement runs exactly once and is never counted. Unlike Paginate there is no total; the page
// carries only the rows and the cursors that reach its neighbours.
func (r *OrmBuilder) CursorPaginate(perPage int, cursor string, dest any) (contractsdb.CursorPaginator, error) {
	anchor, err := decodeCursorAnchor(cursor)
	if err != nil {
		return contractsdb.CursorPaginator{}, err
	}

	query := r.dest(dest).addGlobalScopes().buildConditions()

	builder, err := query.cursorBuilder()
	if err != nil {
		return contractsdb.CursorPaginator{}, err
	}

	page, err := builder.CursorPaginate(perPage, anchor)
	if err != nil {
		return contractsdb.CursorPaginator{}, err
	}

	statement, bindings := builder.ToSelectSQL()
	if err := builder.Error(); err != nil {
		return contractsdb.CursorPaginator{}, err
	}

	items, err := query.cursorRows(statement, bindings, dest)
	if err != nil {
		return contractsdb.CursorPaginator{}, err
	}

	// cursorPaginate() pages through get() in Eloquent, so a relation read's page carries the
	// inverse write-back too.
	if err := query.applyInverseChaperone(dest); err != nil {
		return contractsdb.CursorPaginator{}, err
	}

	return cursorPaginatorPage(querybuilder.NewCursorPaginator(items, page.PerPage, page.Cursor, page.HasCursor, page.Parameters)), nil
}

// cursorBuilder builds the query's statement with its orders expressed as structured orders, which
// the keyset predicate reads a column and a direction from. The ORM stores an order as a raw fragment
// ("`id` asc"), which carries neither, so each such fragment is parsed back into the column and
// direction it was written from; a fragment that is not a plain "column direction" pair is left as a
// raw order and contributes no predicate.
func (r *OrmBuilder) cursorBuilder() (*querybuilder.Builder, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return nil, err
	}

	// The builder stores an order as a core.Order descriptor, not as a bare fragment: a raw order keeps
	// its text in SQL, and an order written through OrderBy() keeps a column and a direction. The cursor
	// parser works on the fragment text, so the descriptor is flattened back into one here.
	stored := r.query.State().Orders
	orders := make([]any, 0, len(stored))
	for _, order := range stored {
		if order.SQL != nil {
			orders = append(orders, fmt.Sprint(order.SQL))

			continue
		}
		orders = append(orders, fmt.Sprintf("%v %s", order.Column, order.Direction))
	}
	columns, directions, raws := parseCursorOrders(orders)
	if len(columns) == 0 {
		return builder, nil
	}

	builder = builder.CloneWithout("orders")
	for i, column := range columns {
		builder.OrderBy(column, directions[i])
	}
	for _, raw := range raws {
		builder.OrderByRaw(raw, nil)
	}

	return builder, nil
}

// parseCursorOrders splits the ORM's raw ORDER BY fragments into the columns and directions of each,
// keeping any fragment that is not a plain "column direction" pair to be re-added verbatim. GORM
// wrote an order as "<wrapped column> <direction>", so the column is unquoted again: the keyset
// predicate and the minted cursors must use the same column names the rows carry.
func parseCursorOrders(orders []any) (columns []string, directions []string, raws []string) {
	for _, order := range orders {
		text := strings.TrimSpace(fmt.Sprint(order))
		if text == "" {
			continue
		}

		fields := strings.Fields(text)
		if len(fields) < 2 {
			raws = append(raws, text)

			continue
		}

		direction := strings.ToLower(fields[len(fields)-1])
		if direction != "asc" && direction != "desc" {
			raws = append(raws, text)

			continue
		}

		column := unwrapCursorColumn(strings.Join(fields[:len(fields)-1], " "))
		if column == "" || strings.ContainsAny(column, "()*") {
			raws = append(raws, text)

			continue
		}

		columns = append(columns, column)
		directions = append(directions, direction)
	}

	return columns, directions, raws
}

// unwrapCursorColumn strips the identifier quoting a wrapped column carries, so "`id`" and
// "`users`.`id`" become "id" and "users.id". It is the inverse of the grammar's Wrap for the plain
// identifiers the ORM writes into an order.
func unwrapCursorColumn(column string) string {
	return strings.NewReplacer("`", "", `"`, "", "[", "", "]", "").Replace(column)
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
func (r *OrmBuilder) cursorRows(statement string, bindings []any, dest any) ([]map[string]any, error) {
	rows, err := r.queryRows(statement, bindings)
	if err != nil {
		return nil, err
	}
	defer errors.Ignore(rows.Close)

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	items := []map[string]any{}
	for rows.Next() {
		row, err := scanRowMap(rows, columns)
		if err != nil {
			return nil, err
		}

		items = append(items, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

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
			if err := scanRow(item, element.Interface()); err != nil {
				return err
			}

			slice = reflect.Append(slice, element)

			continue
		}

		element := reflect.New(elementType).Elem()
		if err := scanRow(item, element.Addr().Interface()); err != nil {
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
func cursorPaginatorPage(page querybuilder.CursorPaginator) contractsdb.CursorPaginator {
	result := contractsdb.CursorPaginator{
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

func (r *OrmBuilder) Raw(sql string, values ...any) contractsorm.Query {
	conditions := r.conditions
	conditions.raw = &Select{query: sql, args: values}

	return r.setConditions(conditions)
}

func (r *OrmBuilder) Restore(dests ...any) (*contractsdb.Result, error) {
	var (
		dest  any
		query *OrmBuilder
	)

	if len(dests) > 0 {
		dest = dests[0]
		query = r.dest(dest).addGlobalScopes().buildConditions()
	} else {
		query = r.addGlobalScopes().buildConditions()
	}

	var deletedAtColumnName string
	if dest != nil {
		deletedAtColumnName = getDeletedAtColumn(dest)
	} else if query.conditions.model != nil {
		deletedAtColumnName = getDeletedAtColumn(query.conditions.model)
	}
	if deletedAtColumnName == "" {
		return nil, errors.OrmDeletedAtColumnNotFound
	}

	if err := r.restoring(dest); err != nil {
		return nil, err
	}

	// Clearing the column is an update scoped the way a delete is, by the destination's key, so the
	// row being restored is the one the call named.
	res, err := query.updateScoped(map[string]any{deletedAtColumnName: nil}, true)
	if err != nil {
		return nil, err
	}

	if err := r.restored(dest); err != nil {
		return nil, err
	}

	return res, nil
}

func (r *OrmBuilder) Rollback() error {
	if r.tx == nil {
		return errors.OrmQueryTransactionNotStarted
	}

	return r.tx.Rollback()
}

// Save persists a model: a model that carries no key is inserted, and a model that names a row is
// updated by its key. A Go struct has no attribute bag and the ORM keeps no record of the values a
// model was loaded with, so neither path compares against a previous state: the write carries the
// fields that hold a non-zero value, the automatic updated_at, and any column the query names through
// WithZero (or a Select list) even when it holds the zero value. Eloquent's equivalent is the set of
// attributes the model carries (oracle v12.55.1: an explicitly assigned null/false/0 is written).
func (r *OrmBuilder) Save(dest any) error {
	if hasStringColumns(r.query.Selects()) && len(r.conditions.omit) > 0 {
		return errors.OrmQuerySelectAndOmitsConflict
	}

	id := databaseorm.GetID(dest)
	update := id != nil

	var query *OrmBuilder
	if update {
		query = r.dest(dest).addGlobalScopes().buildConditions()
	} else {
		query = r.dest(dest).buildConditions()
	}

	var err error
	if dest, err = query.saving(dest); err != nil {
		return err
	}

	// wrote records whether a statement ran: finishSave touches the owners only then.
	var wrote bool
	if update {
		// Model::save runs performUpdate — and with it the updating and updated events — only
		// when the model has something to write (`$saved = $this->isDirty() ?
		// $this->performUpdate($query) : true`). With no attribute bag, "something to write" is a
		// non-empty Save column set; a keyed model with none (no non-zero field, no WithZero, no
		// automatic timestamp) runs no statement and fires saving and saved alone (oracle
		// v12.55.1, /tmp/r15v/probe_v.out P4: a clean save logs no statement and the events
		// ["saving","saved"]).
		assignments, err := query.saveAssignments(dest)
		if err != nil {
			return err
		}
		if len(assignments) > 0 {
			if dest, err = query.updating(dest); err != nil {
				return err
			}

			if wrote, err = query.save(dest); err != nil {
				return err
			}
			if wrote {
				if _, err := query.updated(dest); err != nil {
					return err
				}
			}
		}
	} else {
		if dest, err = query.creating(dest); err != nil {
			return err
		}

		if wrote, err = query.save(dest); err != nil {
			return err
		}
		// Like create(): an insert the empty-attribute guard skipped fires no created event.
		if wrote {
			if err := query.created(dest); err != nil {
				return err
			}
		}
	}

	if _, err := query.saved(dest); err != nil {
		return err
	}

	// finishSave touches the model's owners after a save that wrote, unless WithoutTouch asked
	// otherwise: `if ($this->isDirty() && ($options['touch'] ?? true))` — a save with nothing to
	// write leaves the owners alone (/tmp/r15v/probe_v.out P4: no owner update on the clean save,
	// one on the dirty save P4b).
	if wrote && !query.conditions.withoutTouch {
		if err := query.touchOwners(dest); err != nil {
			return err
		}
	}

	return nil
}

func (r *OrmBuilder) SaveQuietly(value any) error {
	return r.WithoutEvents().Save(value)
}

func (r *OrmBuilder) Scan(dest any) error {
	query := r.dest(dest).buildConditions()

	statement, bindings, err := query.compileSelect()
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	// Scanning a statement that matched nothing fills nothing, which is not an error here.
	if _, err := query.scanRows(rows, dest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	return nil
}

// Scopes applies one or more query scopes immediately, mirroring Eloquent's Builder::scopes(): the
// scopes run against this query at call time, not when a statement compiles, so a where chained
// after this call lands after the scope's clauses. Each scope's wheres are regrouped the way
// callScope groups them (applyScope). A return value whose concrete type is not an OrmBuilder is
// tolerated by keeping the receiver as the chain — the tolerance the deferred application always
// had.
func (r *OrmBuilder) Scopes(funcs ...func(contractsorm.Query) contractsorm.Query) contractsorm.Query {
	query := r.flushWheres()

	for _, apply := range funcs {
		query = query.applyScope(apply)
	}

	return query
}

// Select replaces the selected columns and their bindings, mirroring Laravel's select().
func (r *OrmBuilder) Select(columns ...string) contractsorm.Query {
	r.conditions.selectSubs = nil
	if len(columns) == 0 {
		columns = []string{"*"}
	}
	cols := make([]any, len(columns))
	for i, column := range columns {
		cols[i] = column
	}
	r.query.Select(cols...)

	return r
}

func (r *OrmBuilder) SelectRaw(query any, args ...any) contractsorm.Query {
	r.query.SelectRaw(fmt.Sprint(query), args)

	return r
}

func (r *OrmBuilder) WithContext(ctx context.Context) contractsorm.Query {
	query := *r
	query.ctx = ctx
	query.conditions = r.conditions.clone()
	query.query = r.query.Clone()
	query.components = r.components.WithContext(ctx)
	query.queryGrammar = query.components.Grammar
	query.query = query.query.RebindGrammar(query.queryGrammar, query.components.Processor, query.dbConfig.Driver)

	return &query
}

func (r *OrmBuilder) SharedLock() contractsorm.Query {
	r.query.Lock(false)

	return r
}

// UseWritePdo forces the query's reads to resolve to the write connection instead of the replica, so
// a read that must observe a write issued on the same connection is not served by a lagging replica.
// It mirrors Laravel's Builder::useWritePdo(), which stores the flag the connection call reads as the
// inverse of its $useReadPdo argument; in this port the executor's readPool honours it.
func (r *OrmBuilder) UseWritePdo() contractsorm.Query {
	conditions := r.conditions
	conditions.useWritePdo = true

	return r.setConditions(conditions)
}

func (r *OrmBuilder) Sum(column string, dest any) error {
	return r.aggregate("sum", column, dest)
}

func (r *OrmBuilder) Avg(column string, dest any) error {
	return r.aggregate("avg", column, dest)
}

func (r *OrmBuilder) Min(column string, dest any) error {
	return r.aggregate("min", column, dest)
}

func (r *OrmBuilder) Max(column string, dest any) error {
	return r.aggregate("max", column, dest)
}

// aggregate runs a single-column aggregate and scans its one value into dest, which GORM's own
// Select(...).Row().Scan did before.
func (r *OrmBuilder) aggregate(function string, column string, dest any) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer {
		return errors.DatabaseUnsupportedType.Args(destValue.Kind(), "pointer")
	}

	query := r.addGlobalScopes().buildConditions()

	statement, bindings, err := query.compileAggregate(function, []any{column})
	if err != nil {
		return err
	}

	return query.queryScalar(dest, statement, bindings)
}

// Table points the query at a table by name: the builder's from clause, the way Eloquent's
// Builder::from() sets it on the Query\Builder. A string name is stored bare and the framework
// grammar applies the connection prefix once when the statement is rendered; a
// querybuilder.Expression is stored as given and rendered verbatim, for a caller naming a table
// that is already qualified. The trailing arguments are accepted for the contract's signature and
// take no part in the statement, as they never did.
func (r *OrmBuilder) Table(name any, _ ...any) contractsorm.Query {
	r.query.From(name, "")

	return r
}

func (r *OrmBuilder) ToSql() contractsorm.ToSql {
	return NewToSql(r, r.log, false)
}

func (r *OrmBuilder) ToRawSql() contractsorm.ToSql {
	return NewToSql(r, r.log, true)
}

func (r *OrmBuilder) Update(column any, value ...any) (*contractsdb.Result, error) {
	query := r.addGlobalScopes().buildConditions()

	if _, ok := column.(string); !ok && len(value) > 0 {
		return nil, errors.OrmQueryInvalidParameter
	}

	// The model the query addresses is the framework's own state; reading it from the statement would
	// be reading the same value through GORM.
	var singleUpdate bool
	if model := query.conditions.model; model != nil {
		if id := databaseorm.GetID(model); id != nil {
			singleUpdate = true
		}
	}

	values := column
	if name, ok := column.(string); ok && len(value) > 0 {
		values = map[string]any{name: value[0]}
	}

	// The events run against the values, and take the place of them when an observer changed one.
	if singleUpdate {
		var err error
		if values, err = query.saving(values); err != nil {
			return nil, err
		}
		if values, err = query.updating(values); err != nil {
			return nil, err
		}
	}

	res, err := query.update(values)

	if singleUpdate && err == nil {
		if _, err := query.updated(values); err != nil {
			return nil, err
		}
		if _, err := query.saved(values); err != nil {
			return nil, err
		}

		// The single model-update branch is `$model->update(...)`'s save, so it runs finishSave's
		// touch step. Increment / Decrement mark the query through conditions.withoutTouch so the
		// statement keeps Eloquent's behaviour of not propagating touches.
		if !query.conditions.withoutTouch {
			if err := query.touchOwners(query.conditions.model); err != nil {
				return nil, err
			}
		}
	}

	return res, err
}

// UpdateOrCreate looks a model up, writing the values it was given either way: the found model is saved
// with them, and a model that was not found is created carrying them. A create that loses a unique
// constraint race reads the winning row back (createOrFirst) and that row is filled with the values
// and saved too — Laravel's wasRecentlyCreated() branch.
func (r *OrmBuilder) UpdateOrCreate(dest any, attributes any, values any) error {
	defer r.withZeroFor(attributes, values)()
	found, err := r.firstOrInit(dest, attributes, nil, nil)
	if err != nil {
		return err
	}

	if err := r.assignValue(dest, values); err != nil {
		return err
	}

	if found {
		return r.Save(dest)
	}

	readBack, err := r.createWithUniqueRetry(dest, attributes)
	if err != nil {
		return err
	}
	if readBack {
		if err := r.assignValue(dest, values); err != nil {
			return err
		}

		return r.Save(dest)
	}

	return nil
}

// firstOrInit looks a model up, and finding none describes one from the query's own equalities, from
// the query's pending attributes and from the attributes, with the assigned values applied either
// way. It reports whether the row was found.
//
// GORM expressed this as FirstOrInit plus Attrs and Assign; the rules are reproduced here so the three
// methods above share one lookup.
func (r *OrmBuilder) firstOrInit(dest any, find any, attrs []any, assigns []any) (bool, error) {
	query := r.dest(dest)

	// The fold appends its equalities to the builder's where list; the count before it runs marks
	// where they start, so assignWhereValues reads back the lookup alone.
	foldStart := len(query.query.State().Wheres)

	if find != nil {
		folded, err := query.foldFindCondition(find)
		if err != nil {
			return false, err
		}

		query = folded
	}

	query = query.addGlobalScopes()

	found, err := query.firstRow(dest)
	if err != nil {
		return false, err
	}

	if !found {
		// Laravel's firstOrNew routes a miss through newModelInstance(), which merges the query's
		// pending attributes underneath the attributes and values the instance is built from
		// (array_merge($pendingAttributes, array_merge($attributes, $values))); applying them first
		// reproduces that precedence — every assignment below wins over a pending attribute.
		if err := query.applyPendingAttributes(dest); err != nil {
			return false, err
		}
		if err := query.assignWhereValues(dest, foldStart); err != nil {
			return false, err
		}
		for _, attr := range attrs {
			if err := query.assignValue(dest, attr); err != nil {
				return false, err
			}
		}
	}

	for _, assign := range assigns {
		if err := query.assignValue(dest, assign); err != nil {
			return false, err
		}
	}

	return found, nil
}

// foldFindCondition adds the lookup the FirstOr* family was given to the query's where clause, in the
// shapes the callers use: a model, whose columns carrying a value are matched, a map, whose keys name
// the columns, a column with its value, and a lone scalar, which is the primary key.
func (r *OrmBuilder) foldFindCondition(find any) (*OrmBuilder, error) {
	if mapping, ok := asColumnMap(find); ok {
		for _, key := range slices.Sorted(maps.Keys(mapping)) {
			applyWhereShape(r.query, key, []any{mapping[key]}, core.BooleanAnd, false)
		}

		return r, nil
	}

	if pair, ok := find.([]any); ok && len(pair) == 2 {
		if column, ok := pair[0].(string); ok {
			return r.withFindConditions([]any{column, pair[1]}), nil
		}
	}

	reflected := reflect.Indirect(reflect.ValueOf(find))
	if !reflected.IsValid() || reflected.Kind() != reflect.Struct {
		return r.withFindConditions([]any{find}), nil
	}

	schema, err := parseSchema(find)
	if err != nil {
		return nil, err
	}

	for _, field := range schema.Fields {
		value, zero := field.ValueOf(r.ctx, reflected)
		if zero {
			continue
		}

		applyWhereShape(r.query, field.DBName, []any{value}, core.BooleanAnd, false)
	}

	return r, nil
}

// assignWhereValues copies the equalities the FirstOr* fold wrote onto a model, which is how a model
// built by FirstOrNew still carries the conditions it was looked up with. from is the builder's
// where count before the fold ran: only the clauses the fold appended are read back — a where the
// caller chained before the call is not copied, exactly as it was not when the fold kept its own
// list — and only the plain `column = value` ones, which the fold writes as Basic clauses; a
// fragment, an IN list or a column comparison takes another clause type and is skipped.
func (r *OrmBuilder) assignWhereValues(dest any, from int) error {
	wheres := r.query.State().Wheres
	if from > len(wheres) {
		return nil
	}

	for _, item := range wheres[from:] {
		column, ok := item.Column.(string)
		if !ok || item.Type != core.WhereBasic || item.Operator != "=" || isSlice(item.Value) {
			continue
		}

		if err := r.assignColumn(dest, column, item.Value); err != nil {
			return err
		}
	}

	return nil
}

// assignValue fills a model from the shapes the callers pass: a map, whose keys name the columns, or a
// model, whose columns carrying a value are copied.
func (r *OrmBuilder) assignValue(dest any, value any) error {
	if value == nil {
		return nil
	}

	if mapping, ok := asColumnMap(value); ok {
		for _, key := range slices.Sorted(maps.Keys(mapping)) {
			if err := r.assignColumn(dest, key, mapping[key]); err != nil {
				return err
			}
		}

		return nil
	}

	reflected := reflect.Indirect(reflect.ValueOf(value))
	if !reflected.IsValid() || reflected.Kind() != reflect.Struct {
		return nil
	}

	schema, err := parseSchema(dest)
	if err != nil {
		return err
	}

	for _, field := range schema.Fields {
		fieldValue, zero := field.ValueOf(r.ctx, reflected)
		if zero {
			continue
		}

		_ = field.Set(r.ctx, reflect.ValueOf(dest), fieldValue)
	}

	return nil
}

// assignColumn writes one column of a model, looked up by its Go field name or by its column name. A
// column the model does not carry is left alone.
func (r *OrmBuilder) assignColumn(dest any, name string, value any) error {
	schema, err := parseSchema(dest)
	if err != nil {
		return err
	}

	field, ok := schema.FieldsByName[name]
	if !ok {
		field, ok = schema.FieldsByDBName[name]
	}
	if !ok {
		return nil
	}

	reflected := reflect.ValueOf(dest)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return nil
	}

	return field.Set(r.ctx, reflected, value)
}

func (r *OrmBuilder) When(condition bool, callback func(query contractsorm.Query) contractsorm.Query, falseCallback ...func(query contractsorm.Query) contractsorm.Query) contractsorm.Query {
	if condition {
		return callback(r)
	}

	if len(falseCallback) > 0 {
		return falseCallback[0](r)
	}

	return r
}

// Where mirrors Eloquent's Builder::where() (Builder.php:352-365). A callback with no operator runs
// against a new query without relationships: its eager loads merge into this query and its wheres
// join this query as one nested group. Every other condition is handed to the inner database/query
// builder as written, which owns the operator and value grammar. Raw SQL has its own entry point in
// WhereRaw, the way Eloquent separates where() from whereRaw().
func (r *OrmBuilder) Where(query any, args ...any) contractsorm.Query {
	if callback, ok := query.(func(contractsorm.Query) contractsorm.Query); ok && len(args) == 0 {
		return r.addNestedWhereQuery(callback, core.BooleanAnd)
	}

	r.query.Where(query, args...)

	return r
}

// WhereRaw mirrors Eloquent's Builder::whereRaw: the fragment renders verbatim and the values bind
// to its placeholders in order.
func (r *OrmBuilder) WhereRaw(query string, args ...any) contractsorm.Query {
	r.query.WhereRaw(query, args, core.BooleanAnd)

	return r
}

// OrWhereRaw mirrors Eloquent's Builder::orWhereRaw.
func (r *OrmBuilder) OrWhereRaw(query string, args ...any) contractsorm.Query {
	r.query.WhereRaw(query, args, core.BooleanOr)

	return r
}

// WhereAll adds one condition per column, joined by "and" inside a single nested group; an IN or
// NOT IN against a slice expands to one WhereIn per column (Builder::whereAll).
func (r *OrmBuilder) WhereAll(columns []string, args ...any) contractsorm.Query {
	op, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		return r.withError(err).(*OrmBuilder)
	}

	if !applyInSliceGroup(r.query, columns, op, value, core.BooleanAnd, core.BooleanAnd) {
		r.query.WhereAll(toStrings(columns), op, value, core.BooleanAnd)
	}

	return r
}

// WhereAny adds one condition per column, joined by "or" inside a single nested group
// (Builder::whereAny).
func (r *OrmBuilder) WhereAny(columns []string, args ...any) contractsorm.Query {
	op, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		return r.withError(err).(*OrmBuilder)
	}

	if !applyInSliceGroup(r.query, columns, op, value, core.BooleanOr, core.BooleanAnd) {
		r.query.WhereAny(toStrings(columns), op, value, core.BooleanAnd)
	}

	return r
}

// WhereIn adds a "where column in (...)" clause by calling the inner query builder's WhereIn, which
// expands the slice to one placeholder per value inside the wrapped column (Builder::whereIn). The
// raw "col in ?" fragments the pivot and Find conditions queue compile through the descriptor path.
func (r *OrmBuilder) WhereIn(column string, values []any) contractsorm.Query {
	r.query.WhereIn(column, values, core.BooleanAnd, false)

	return r
}

// WhereJsonContains calls the inner query builder's WhereJsonContains, which prepares the JSON value
// through the grammar before it is bound (Builder::whereJsonContains).
func (r *OrmBuilder) WhereJsonContains(column string, value any) contractsorm.Query {
	r.query.WhereJsonContains(column, value, core.BooleanAnd, false)

	return r
}

// WhereJsonContainsKey calls the inner query builder's WhereJsonContainsKey, which renders the
// dialect's key-existence check (Builder::whereJsonContainsKey).
func (r *OrmBuilder) WhereJsonContainsKey(column string) contractsorm.Query {
	r.query.WhereJsonContainsKey(column, core.BooleanAnd, false)

	return r
}

// WhereJsonDoesntContain is WhereJsonContains with the negation set (Builder::whereJsonDoesntContain).
func (r *OrmBuilder) WhereJsonDoesntContain(column string, value any) contractsorm.Query {
	r.query.WhereJsonContains(column, value, core.BooleanAnd, true)

	return r
}

// WhereJsonDoesntContainKey is WhereJsonContainsKey with the negation set
// (Builder::whereJsonDoesntContainKey).
func (r *OrmBuilder) WhereJsonDoesntContainKey(column string) contractsorm.Query {
	r.query.WhereJsonContainsKey(column, core.BooleanAnd, true)

	return r
}

// WhereJsonLength calls the inner query builder's JSON-length comparison (Builder::whereJsonLength);
// OrWhereJsonLength is the same comparison with the "or" connector.
func (r *OrmBuilder) WhereJsonLength(column string, args ...any) contractsorm.Query {
	r.query.WhereJsonLength(column, args...)

	return r
}

func (r *OrmBuilder) OrWhereIn(column string, values []any) contractsorm.Query {
	r.query.WhereIn(column, values, core.BooleanOr, false)

	return r
}

func (r *OrmBuilder) OrWhereJsonContains(column string, value any) contractsorm.Query {
	r.query.WhereJsonContains(column, value, core.BooleanOr, false)

	return r
}

func (r *OrmBuilder) OrWhereJsonContainsKey(column string) contractsorm.Query {
	r.query.WhereJsonContainsKey(column, core.BooleanOr, false)

	return r
}

func (r *OrmBuilder) OrWhereJsonDoesntContain(column string, value any) contractsorm.Query {
	r.query.WhereJsonContains(column, value, core.BooleanOr, true)

	return r
}

func (r *OrmBuilder) OrWhereJsonDoesntContainKey(column string) contractsorm.Query {
	r.query.WhereJsonContainsKey(column, core.BooleanOr, true)

	return r
}

func (r *OrmBuilder) OrWhereJsonLength(column string, args ...any) contractsorm.Query {
	r.query.OrWhereJsonLength(column, args...)

	return r
}

// WhereNotIn mirrors Builder::whereNotIn: the same call with the negation set.
func (r *OrmBuilder) WhereNotIn(column string, values []any) contractsorm.Query {
	r.query.WhereIn(column, values, core.BooleanAnd, true)

	return r
}

func (r *OrmBuilder) OrWhereNotIn(column string, values []any) contractsorm.Query {
	r.query.WhereIn(column, values, core.BooleanOr, true)

	return r
}

// WhereBetween mirrors Builder::whereBetween: the inner builder wraps the column and binds the two
// bounds in order.
func (r *OrmBuilder) WhereBetween(column string, x, y any) contractsorm.Query {
	r.query.WhereBetween(column, []any{x, y}, core.BooleanAnd, false)

	return r
}

func (r *OrmBuilder) WhereNotBetween(column string, x, y any) contractsorm.Query {
	r.query.WhereBetween(column, []any{x, y}, core.BooleanAnd, true)

	return r
}

func (r *OrmBuilder) OrWhereBetween(column string, x, y any) contractsorm.Query {
	r.query.WhereBetween(column, []any{x, y}, core.BooleanOr, false)

	return r
}

func (r *OrmBuilder) OrWhereNotBetween(column string, x, y any) contractsorm.Query {
	r.query.WhereBetween(column, []any{x, y}, core.BooleanOr, true)

	return r
}

// WhereColumn compares two columns, mirroring Builder::whereColumn: one trailing column means
// equality, two mean the operator and then the column. The trailing arguments are normalised the
// way the raw query's WhereColumn normalises them.
func (r *OrmBuilder) WhereColumn(column1 string, column2 ...string) contractsorm.Query {
	operator, second, err := columnComparison(column2)
	if err != nil {
		return r.withError(err).(*OrmBuilder)
	}

	r.query.WhereColumn(column1, operator, second, core.BooleanAnd)

	return r
}

// OrWhereColumn compares two columns under the "or" connector, mirroring Builder::orWhereColumn.
func (r *OrmBuilder) OrWhereColumn(column1 string, column2 ...string) contractsorm.Query {
	operator, second, err := columnComparison(column2)
	if err != nil {
		return r.withError(err).(*OrmBuilder)
	}

	r.query.OrWhereColumn(column1, operator, second)

	return r
}

// columnComparison normalises the WhereColumn trailing arguments, the raw query's helper of the
// same name: one trailing column is an equality, two are operator then column.
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

func (r *OrmBuilder) OrWhereNull(column string) contractsorm.Query {
	r.query.WhereNull(column, core.BooleanOr, false)

	return r
}

// WhereNone adds one condition per column, joined by "or" inside one negated group: the group
// carries the "not" connector, as Builder::whereNone runs whereAny with it.
func (r *OrmBuilder) WhereNone(columns []string, args ...any) contractsorm.Query {
	op, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		return r.withError(err).(*OrmBuilder)
	}

	if !applyInSliceGroup(r.query, columns, op, value, core.BooleanOr, core.BooleanAndNot) {
		r.query.WhereNone(toStrings(columns), op, value, core.BooleanAnd)
	}

	return r
}

// isNotInOperator reports whether operator is NOT IN, ignoring case and surrounding space.
func isNotInOperator(operator any) bool {
	op, ok := operator.(string)
	if !ok {
		return false
	}

	return strings.ToLower(strings.TrimSpace(op)) == "not in"
}

// isInOperator reports whether operator is IN or NOT IN, ignoring case and surrounding space.
func isInOperator(operator any) bool {
	op, ok := operator.(string)
	if !ok {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(op)) {
	case "in", "not in":
		return true
	default:
		return false
	}
}

// sliceValues returns the elements of a slice or array value, or false for any other value.
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

// isArrayCondition reports whether a where's first argument is an array of wheres: a map of key/value
// pairs, or a list of [column, operator, value] entries — the two shapes the inner builder's
// isArrayCondition accepts. Mirrors the is_array($column) check Builder::where() makes before
// addArrayOfWheres, which is where Eloquent's array forms land.
func isArrayCondition(column any) bool {
	switch column.(type) {
	case map[string]any, []any:
		return true
	}

	return false
}

// WhereNot mirrors Eloquent's Builder::whereNot (Builder.php:407), which is
// where($column, $operator, $value, $boolean.' not') — not the inner Query\Builder::whereNot. A
// callback with no other arguments runs as one nested group carrying the "and not" connector; a
// column with its value (or with no value) is handed to the inner builder's whereNot as written; an
// array first argument takes Eloquent's route into Query\Builder::where()'s is_array branch, which
// expands it through addArrayOfWheres under the "and not" boolean: every column entry is negated
// inside one nested group — `and not (not "b" = ? and not "c" = ?)` — where the raw builder's own
// whereNot nests twice (`and not (("b" = ? and "c" = ?))`).
func (r *OrmBuilder) WhereNot(query any, args ...any) contractsorm.Query {
	if callback, ok := query.(func(contractsorm.Query) contractsorm.Query); ok && len(args) == 0 {
		return r.addNestedWhereQuery(callback, core.BooleanAndNot)
	}

	if isArrayCondition(query) {
		r.query.AddArrayOfWheres(query, core.BooleanAndNot)

		return r
	}

	r.query.WhereNot(query, args...)

	return r
}

// OrWhereNot mirrors Eloquent's Builder::orWhereNot (Builder.php:420): the same forms as WhereNot
// with the "or not" connector; the array form expands through addArrayOfWheres under the "or not"
// boolean, negating every column inside one group — `or not (not "b" = ? or not "c" = ?)`.
func (r *OrmBuilder) OrWhereNot(query any, args ...any) contractsorm.Query {
	if callback, ok := query.(func(contractsorm.Query) contractsorm.Query); ok && len(args) == 0 {
		return r.addNestedWhereQuery(callback, core.BooleanOrNot)
	}

	if isArrayCondition(query) {
		r.query.AddArrayOfWheres(query, core.BooleanOrNot)

		return r
	}

	r.query.OrWhereNot(query, args...)

	return r
}
func (r *OrmBuilder) WhereNotNull(column string) contractsorm.Query {
	r.query.WhereNull(column, core.BooleanAnd, true)

	return r
}

// WhereNull mirrors Builder::whereNull; WhereNotNull and OrWhereNull are the same call with the
// negation and the connector changed.
func (r *OrmBuilder) WhereNull(column string) contractsorm.Query {
	r.query.WhereNull(column, core.BooleanAnd, false)

	return r
}

func (r *OrmBuilder) WithoutEvents() contractsorm.Query {
	conditions := r.conditions
	conditions.withoutEvents = true

	return r.setConditions(conditions)
}

// WithoutTimestamps runs the callback with the query's automatic created_at / updated_at stamping
// suspended, mirroring Eloquent's HasTimestamps::withoutTimestamps: a write through the query while
// a scope is active leaves both columns alone — a value the caller supplied is still written — and
// the scope is re-entrant, each call restoring exactly its own suspension. The suspension is lifted
// before the callback's error propagates, the way Eloquent's try / finally restores its class list
// before an exception leaves the call.
//
// Eloquent scopes the suspension to the model class because a PHP process keeps one static list per
// class; the Go scope suspends the one query instead, so the query and the copies it hands out
// (Clone, WithContext) suspend together, while a write through another query handle is not
// suspended.
func (r *OrmBuilder) WithoutTimestamps(callback func() error) error {
	conditions := r.conditions
	conditions.withoutTimestamps++
	r.setConditions(conditions)

	defer func() {
		conditions := r.conditions
		conditions.withoutTimestamps--
		r.setConditions(conditions)
	}()

	return callback()
}

// UsesTimestamps reports whether writes through the query stamp the model's automatic timestamps,
// the query-level form of Eloquent's HasTimestamps::usesTimestamps: false while a WithoutTimestamps
// scope is active, and false when the query's model carries the per-instance flag (a
// databaseorm.Timestamps whose Disabled field names it ignored). With no model attached only the
// scope is consulted.
func (r *OrmBuilder) UsesTimestamps() bool {
	return r.usesTimestamps(r.parentModel())
}

// usesTimestamps folds Eloquent's usesTimestamps predicate for a write of model — the scope
// suspension first, then the model's own per-instance flag, read through the model's UsesTimestamps
// method the way Eloquent reads $this->timestamps && ! static::isIgnoringTimestamps(). A model that
// does not carry the flag reports nothing and stamps.
func (r *OrmBuilder) usesTimestamps(model any) bool {
	if r.conditions.withoutTimestamps > 0 {
		return false
	}

	value := reflect.ValueOf(model)
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return true
		}

		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return true
	}

	aware, ok := value.Interface().(contractsorm.ModelWithTimestamps)

	return !ok || aware.UsesTimestamps()
}

// WithoutGlobalScopes removes the named global scopes; called with no names it removes every scope.
//
// Inherent Go limitation: a variadic call cannot distinguish no arguments from an empty slice, so
// WithoutGlobalScopes() and a call spreading an empty list behave alike here and remove all scopes.
// Laravel distinguishes them — withoutGlobalScopes() (null) removes all, while
// withoutGlobalScopes([]) removes none (oracle against illuminate/database v12.55.1: the empty
// array leaves every scope applied). The empty-slice shape is not expressible through this
// signature.
func (r *OrmBuilder) WithoutGlobalScopes(names ...string) contractsorm.Query {
	conditions := r.conditions

	if len(names) == 0 {
		names = []string{"*"}
	}
	conditions.withoutGlobalScopes = append(conditions.withoutGlobalScopes, names...)

	return r.setConditions(conditions)
}

func (r *OrmBuilder) WithTrashed() contractsorm.Query {
	conditions := r.conditions
	conditions.withTrashed = true

	return r.setConditions(conditions)
}

func (r *OrmBuilder) OnlyTrashed() contractsorm.Query {
	conditions := r.conditions
	conditions.onlyTrashed = true

	// Laravel's macro removes SoftDeletingScope and writes one whereNotNull per call at call time.
	// The predicate names the model's table, which may only be known when the statement compiles,
	// so the call writes a deferred entry at its position instead (deferredWhere).
	r.setConditions(conditions).deferWhere(core.BooleanAnd, &deferredWhere{onlyTrashed: true})

	return r
}

func (r *OrmBuilder) addGlobalScopes() *OrmBuilder {
	// Scopes are registered once per statement: a query that already folded its queued scopes in
	// (withScopesApplied) must not queue them again, or the read that continues on it — get()'s
	// Find, for one — would apply every scope a second time.
	if r.conditions.scopesApplied {
		return r
	}

	if slices.Contains(r.conditions.withoutGlobalScopes, "*") {
		return r
	}

	var model any

	if r.conditions.model != nil {
		model = r.conditions.model
	} else if r.conditions.dest != nil {
		model = r.conditions.dest
	} else {
		return r
	}

	model, err := modelToStruct(model)
	if err != nil {
		return r
	}

	modelWithGlobalScopes, ok := model.(contractsorm.ModelWithGlobalScopes)
	if !ok {
		return r
	}

	modelGlobalScopes := modelWithGlobalScopes.GlobalScopes()
	if len(modelGlobalScopes) == 0 {
		return r
	}

	var globalScopes []func(contractsorm.Query) contractsorm.Query

	// The scopes keep the order the model declared them — Laravel's registration order, the order
	// its boot() added its scopes in — and removal names them by their identity.
	for _, globalScope := range modelGlobalScopes {
		if slices.Contains(r.conditions.withoutGlobalScopes, globalScope.Name) {
			continue
		}

		globalScopes = append(globalScopes, globalScope.Scope)
	}

	// Global scopes are deferred the way Laravel defers them: withGlobalScope() registers the scope
	// and applyScopes() applies it when a statement compiles, which is where withScopesApplied folds
	// it in. The caller's own Scopes() call applies immediately instead (Builder::scopes), so the
	// queue it fills is only ever the model's global scopes.
	query := r.clone()
	conditions := query.conditions
	conditions.scopes = deep.Append(conditions.scopes, globalScopes...)

	return query.setConditions(conditions)
}

// addNestedWhereQuery runs a where callback the way Eloquent's Builder::where() does: the callback
// receives a new query without relationships, its eager loads merge into this query (array_merge of
// getEagerLoads()), and its wheres join this query's clause as one nested group through the inner
// builder's AddNestedWhereQuery (Builder.php:352-365).
func (r *OrmBuilder) addNestedWhereQuery(callback func(contractsorm.Query) contractsorm.Query, boolean string) contractsorm.Query {
	child := r.newQueryWithoutRelationships()
	result := callback(child)
	nested := child
	if returned, ok := result.(*OrmBuilder); ok {
		nested = returned
	}

	// The child's eager loads merge the way Eloquent's where-closure branch array_merges
	// getEagerLoads(): the later write wins by relation key regardless of constraint.
	for _, entry := range nested.conditions.eagerLoad {
		r.conditions.eagerLoad = relations.MergeEagerLoadEntry(r.conditions.eagerLoad, entry)
	}

	// Anything the callback queued as a descriptor lands on the child's query first through
	// flushWheres; the declared methods already wrote straight to it. AddNestedWhereQuery adds
	// nothing when no where remains.
	nested.flushWheres()

	// An onlyTrashed or a whereHas / has / doesntHave the closure called is a deferred entry in the
	// closure's own where list, with its marker among the closure's bindings, exactly where Laravel's
	// macro and addHasWhere write their clauses onto the closure's builder. The group joins this
	// query like any other, so the entries travel inside it and render in place when the statement
	// compiles (resolveDeferredWheres).
	r.query.AddNestedWhereQuery(nested.query, boolean)

	return r
}

// newQueryWithoutRelationships builds the query Eloquent's Builder::where() hands a closure: the
// same model and connection, with none of this query's wheres or eager loads.
func (r *OrmBuilder) newQueryWithoutRelationships() *OrmBuilder {
	child := r.fresh()
	child.conditions = r.conditions.clone()
	child.conditions.eagerLoad = nil
	// Eloquent hands the closure a fresh builder, so the parent's trashed switches are not
	// inherited: a switch the closure does not set itself stays off for its nested group. The
	// parent's deferred entries live in the parent's where list, which the fresh child does not
	// carry, so the group renders only the entries the closure wrote (oracle, illuminate/database
	// v12.55.1, real sqlite, /Users/dog/.claude/jobs/013c27a0/tmp/r15b-verify-probes/inherit.php D1:
	// whereHas('posts')->where(fn => where('email', 'a')) compiles `where exists (...) and ("email" =
	// ?)`).
	child.conditions.withTrashed = false
	child.conditions.onlyTrashed = false

	return child
}

// addWhere writes one internally recorded condition — the pivot chain's fragments and column/value
// pairs — onto the query builder at call time, so it keeps its place among the clauses written
// around it, the way Eloquent appends every where to the underlying Query\Builder as it is called.
func (r *OrmBuilder) addWhere(where contractsdriver.Where) contractsorm.Query {
	boolean := core.BooleanAnd
	if where.Or {
		boolean = core.BooleanOr
	}

	r.applySingleWhere(r.query, where, boolean)

	return r
}

func (r *OrmBuilder) buildConditions() *OrmBuilder {
	// The query is no longer assembled into a GORM statement here. GORM used to build one and run
	// it; the compiled path builds from the query's conditions, so the only thing left to do is
	// rebind to the connection a model may declare and hand the query on.
	query, err := r.refreshConnection()
	if err != nil {
		return r.withError(err).(*OrmBuilder)
	}

	return query
}

func (r *OrmBuilder) clearConditions() {
	r.conditions = Conditions{}
}

func (r *OrmBuilder) create(dest any) error {
	// Laravel's create() goes through newModelInstance(), which merges $pendingAttributes into the
	// attributes before the model is saved, so the values WithAttributes recorded reach the row and
	// the saving/creating hooks see them.
	if err := r.applyPendingAttributes(dest); err != nil {
		return err
	}

	var err error
	if dest, err = r.saving(dest); err != nil {
		return err
	}
	if dest, err = r.creating(dest); err != nil {
		return err
	}

	inserted, err := r.execInsert(dest)
	if err != nil {
		return err
	}

	// Eloquent fires the created event only when performInsert wrote the row; a skipped empty
	// non-incrementing insert returns before it, while the saved event still runs.
	if inserted {
		if err := r.created(dest); err != nil {
			return err
		}
	}
	if _, err := r.saved(dest); err != nil {
		return err
	}

	// finishSave touches the model's owners after a successful save, the Go form of Eloquent's
	// `if ($this->isDirty() && ($options['touch'] ?? true)) { $this->touchOwners(); }`.
	if err := r.touchOwners(dest); err != nil {
		return err
	}

	return nil
}

func (r *OrmBuilder) created(dest any) error {
	if isSlice(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventCreated, r.conditions.model, dest)

	return err
}

func (r *OrmBuilder) creating(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventCreating, r.conditions.model, dest)
}

// event runs the handlers registered for an event and returns the destination as they left it.
//
// The destination can be replaced while the handlers run — an observer that changes an attribute on a
// value it cannot address gets a copy — so the value is returned rather than only recorded, which is
// how such a change reaches the statement being built.
func (r *OrmBuilder) event(event contractsorm.EventType, model, dest any) (any, error) {
	if r.conditions.withoutEvents {
		return dest, nil
	}

	instance := NewEvent(r, model, dest)
	if err := r.dispatch(event, model, dest, instance); err != nil {
		return instance.dest, err
	}

	return instance.dest, nil
}

// dispatch runs the handlers registered for one event.
func (r *OrmBuilder) dispatch(event contractsorm.EventType, model, dest any, instance *Event) error {
	if model != nil {
		if dispatchesEvents, exist := model.(contractsorm.DispatchesEvents); exist {
			if dispatchesEvent, exists := dispatchesEvents.DispatchesEvents()[event]; exists {
				return dispatchesEvent(instance)
			}

			return nil
		}

		if observer := r.getObserver(model); observer != nil {
			if observerEvent := getObserverEvent(event, observer); observerEvent != nil {
				return observerEvent(instance)
			}

			return nil
		}
	}

	if dest != nil {
		if dispatchesEvents, exist := dest.(contractsorm.DispatchesEvents); exist {
			if dispatchesEvent, exists := dispatchesEvents.DispatchesEvents()[event]; exists {
				return dispatchesEvent(instance)
			}
			return nil
		}

		if observer := r.getObserver(dest); observer != nil {
			if observerEvent := getObserverEvent(event, observer); observerEvent != nil {
				return observerEvent(instance)
			}
			return nil
		}
	}

	return nil
}

func (r *OrmBuilder) deleting(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventDeleting, r.conditions.model, dest)

	return err
}

func (r *OrmBuilder) deleted(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventDeleted, r.conditions.model, dest)

	return err
}

// withScopesApplied runs the scopes the query still carries queued and returns a query carrying what
// they added, with the queue cleared so applying them is not repeated. Only a model's global scopes
// are deferred this way — addGlobalScopes queues them the way withGlobalScope() registers them —
// because Laravel applies them at statement time (Builder::applyScopes, run by get()/toSql()); the
// caller's own Scopes() call is the immediate path (Builder::scopes) and never queues.
//
// Each scope starts from what the previous ones produced, which is how GORM chained them onto one
// statement.
func (r *OrmBuilder) withScopesApplied() *OrmBuilder {
	// Laravel's applyScopes() starts from "clone $this" and applies the scopes to the copy, so the
	// receiver keeps its own scope list and a second compile from the same query does not append the
	// scope's clauses a second time. This is the one place a clone is required rather than optional:
	// the queued scopes are the model's, not the query's, and they are applied per compiled
	// statement.
	query := r.clone()
	scopes := query.conditions.scopes
	query.conditions.scopes = nil

	// Anything already queued lands on the builder before the scopes run, and each scope's clauses
	// land before the next one's: the builder holds the single ordered where list, and a scope starts
	// from what the previous ones produced.
	query = query.flushWheres()

	// The built-in SoftDeletingScope is the model's first registered global scope (bootSoftDeletes
	// runs before any booted() registration), so applyScopes applies it before every user scope. The
	// deferred entries recorded so far belong to the where list it splits - Eloquent appended their
	// clauses at call time - so they are rendered in place first, on this copy's builder.
	if !query.conditions.softDeleteScopeApplied {
		query.conditions.softDeleteScopeApplied = true

		if err := query.resolveDeferredWheres(query.query, query.deferredWhereScope()); err != nil {
			query.conditions.err = err
		}

		if !query.scopesRemoveSoftDeletingScope(scopes) {
			if err := query.applySoftDelete(query.query); err != nil {
				query.conditions.err = err
			}
		}
	}

	for _, apply := range scopes {
		query = query.applyScope(apply)
	}

	// The scopes are now part of the query's clauses; mark the copy so a later addGlobalScopes on
	// it does not queue the model's scopes a second time (get($columns) runs its Find from this
	// copy).
	if len(scopes) > 0 {
		query.conditions.scopesApplied = true
	}

	// A scope may queue eager loads, and Laravel runs the eager-load pass from the builder
	// applyScopes returned ($builder = $this->applyScopes() in get()/first()). This receiver is
	// that builder here, so fold the scoped list back onto it before the scoped copy is dropped.
	r.conditions.eagerLoad = query.conditions.eagerLoad

	return query
}

// scopesRemoveSoftDeletingScope reports whether one of the queued scopes lifts the built-in
// soft-delete predicate (WithTrashed, or a removal of SoftDeletingScope / every scope). Laravel's
// applyScopes skips a scope a previously applied scope removed, and a model can register such a
// scope before bootSoftDeletes runs (the corpus's GlobalScopeCanBeRemovedByOtherGlobalScope). The
// port always applies the predicate first, so the queued scopes are dry-run on a throwaway copy to
// learn whether any of them removes it; the dry run's clauses are discarded.
func (r *OrmBuilder) scopesRemoveSoftDeletingScope(scopes []func(contractsorm.Query) contractsorm.Query) bool {
	if len(scopes) == 0 || !r.usesFrameworkSoftDelete() {
		return false
	}

	probe := r.clone()
	for _, apply := range scopes {
		probe = probe.applyScope(apply)
	}

	if probe.conditions.onlyTrashed {
		return false
	}

	return probe.conditions.withTrashed || probe.globalScopeRemoved(contractsorm.SoftDeletingScope)
}

// applyScope runs one scope the way Eloquent's Builder::callScope does: the wheres on the query
// before the scope ran and the wheres the scope added are each rebuilt as their own slice
// (Builder::addNewWheresWithinGroup), so the wheres a scope added — and the chain they were
// appended to — become nested groups exactly when an or-connector makes that necessary. A scope
// returning something other than an OrmBuilder is tolerated by keeping the receiver as the chain.
func (r *OrmBuilder) applyScope(apply func(contractsorm.Query) contractsorm.Query) *OrmBuilder {
	originalWhereCount := len(r.query.State().Wheres)

	scoped, ok := apply(r).(*OrmBuilder)
	if !ok {
		return r
	}

	// A scope may hand back a query that still carries queued scopes; the statement compiles from
	// the returned query, so they must not run a second time there.
	scoped.conditions.scopes = nil

	query := scoped.flushWheres()

	// An onlyTrashed or a whereHas the scope called is Laravel's macro / addHasWhere running inside
	// callScope: its clause lands on the query at call time, among the scope's own wheres and before
	// they are regrouped. The deferred entry the call wrote is already there, so it is regrouped with
	// the scope's slice and renders in place when the statement compiles (newBuilder). The
	// soft-delete step of withScopesApplied has already run by now - SoftDeletingScope is the first
	// applied scope, so the macro's withoutGlobalScope comes too late to drop the "is null". Oracle
	// (illuminate/database v12.55.1, real sqlite, /Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle.php
	// G1, first measured in /Users/dog/.claude/jobs/013c27a0/tmp/r15-probes/r15-r14-mutants/probe4.out): a
	// scope calling onlyTrashed() compiles `where "email" = ? and "sr_users"."deleted_at" is null and
	// "sr_users"."deleted_at" is not null`.
	// An onlyTrashed that will render nothing - the model has no framework soft-delete type - is
	// absent from the slice Laravel regroups (its clause is written at call time, and a model without
	// SoftDeletes writes none), so it must not decide the slice's connector: a scope running
	// OnlyTrashed().OrWhere("id", 6) after Where("id", 1) groups as `"id" = ? or ("id" = ?)`, the
	// shape of a scope adding orWhere alone (oracle, illuminate/database v12.55.1, real sqlite,
	// /Users/dog/.claude/jobs/013c27a0/tmp/r15d/oracle.php B2ref).
	query.dropInertOnlyTrashed(originalWhereCount)

	if len(query.query.State().Wheres) > originalWhereCount {
		query.addNewWheresWithinGroup(originalWhereCount)
	}

	return query
}

// dropInertOnlyTrashed removes, from the wheres at or after from, the onlyTrashed entries (and their
// binding markers) whose model is known and has no framework soft-delete type, which therefore
// render nothing. An entry whose model is not known yet is left for the compile-time resolver.
func (r *OrmBuilder) dropInertOnlyTrashed(from int) {
	state := r.query.State()
	if from >= len(state.Wheres) {
		return
	}

	inert := make(map[*deferredWhere]bool)
	kept := append([]core.Where(nil), state.Wheres[:from]...)
	for _, where := range state.Wheres[from:] {
		if payload, ok := where.Value.(*deferredWhere); ok && where.Type == core.WhereDeferredType && payload.onlyTrashed {
			model := payload.scopeFor(r.deferredWhereScope()).parent
			if model != nil {
				if _, soft := softDeleteColumn(model); !soft {
					inert[payload] = true

					continue
				}
			}
		}
		kept = append(kept, where)
	}
	if len(inert) == 0 {
		return
	}

	bindings := make([]any, 0, len(state.Bindings["where"]))
	for _, binding := range state.Bindings["where"] {
		if marker, ok := binding.(*deferredWhere); ok && inert[marker] {
			continue
		}
		bindings = append(bindings, binding)
	}

	state.Wheres = kept
	state.Bindings["where"] = bindings
}

// addNewWheresWithinGroup mirrors Eloquent's Builder::addNewWheresWithinGroup: the wheres that were
// on the query before a scope ran (originalWhereCount) and the wheres the scope added are each
// rebuilt as their own slice, so an or-chain one of them cannot leak into the other.
func (r *OrmBuilder) addNewWheresWithinGroup(originalWhereCount int) {
	addNewWheresWithinGroupIn(r.query, originalWhereCount)
}

// addNewWheresWithinGroupIn is the same regroup on a raw subquery builder. The existence-subquery
// paths append a callback's or-scope's wheres into a *querybuilder.Builder rather than an
// OrmBuilder, and Laravel regroups them the same way (callScope runs on whichever builder a scope
// was handed), so the raw form is the single implementation both sides share.
func addNewWheresWithinGroupIn(builder *querybuilder.Builder, originalWhereCount int) {
	allWheres := builder.State().Wheres

	builder.State().Wheres = nil

	groupWhereSliceForScopeIn(builder, allWheres[:originalWhereCount])
	groupWhereSliceForScopeIn(builder, allWheres[originalWhereCount:])
}

// groupWhereSliceForScopeIn mirrors Eloquent's Builder::groupWhereSliceForScope on a raw builder,
// shared with the existence-subquery paths so a callback's or-chain is grouped on the subquery
// builder exactly as callScope groups it on an Eloquent builder: a slice that carries any "or"
// connector becomes one nested where whose own connector is the slice's first, with its " not"
// stripped (Eloquent's str_replace) so a "and not" lead is not doubled; a slice without one is
// appended flat, so no unnecessary parentheses appear. The group is built the way Eloquent's
// createNestedWhere builds it — the wheres are assigned to the child directly — so the bindings
// stay in the parent's list, where the wheres already put them, in the order they were added.
func groupWhereSliceForScopeIn(builder *querybuilder.Builder, whereSlice []core.Where) {
	if !slices.ContainsFunc(whereSlice, func(where core.Where) bool {
		return strings.Contains(where.Boolean, "or")
	}) {
		builder.State().Wheres = append(builder.State().Wheres, whereSlice...)

		return
	}

	group := builder.ForNestedWhere()
	group.State().Wheres = append(group.State().Wheres, whereSlice...)

	builder.State().Wheres = append(builder.State().Wheres, core.Where{
		Type:    core.WhereNestedType,
		Query:   group,
		Boolean: strings.ReplaceAll(whereSlice[0].Boolean, " not", ""),
	})
}

// flushWheres marks the points where the ORM's internally recorded conditions had to be on the
// builder before the next clause was written: a deferred where entry's position, a scope's starting
// where count, a nested group's merge. Every such condition
// is now written onto the builder when it is called (addWhere, withFindConditions,
// foldFindCondition), so the builder already holds the single ordered where list — the way Eloquent
// keeps every where on the underlying Query\Builder — and there is nothing left to flush. The call
// sites keep the name as the record of that ordering requirement.
func (r *OrmBuilder) flushWheres() *OrmBuilder {
	return r
}

// withError returns a query carrying a condition that could not be accepted, for whichever statement
// is compiled from it to report.
func (r *OrmBuilder) withError(err error) contractsorm.Query {
	conditions := r.conditions
	conditions.err = err

	return r.setConditions(conditions)
}

func (r *OrmBuilder) dest(value any) *OrmBuilder {
	conditions := r.conditions
	conditions.dest = value

	return r.setConditions(conditions)
}

func (r *OrmBuilder) forceDeleting(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventForceDeleting, r.conditions.model, dest)

	return err
}

func (r *OrmBuilder) forceDeleted(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventForceDeleted, r.conditions.model, dest)

	return err
}

func (r *OrmBuilder) getModelConnection() string {
	var (
		model any
		err   error
	)

	if r.conditions.model != nil {
		model, err = modelToStruct(r.conditions.model)
	} else if r.conditions.dest != nil {
		model, err = modelToStruct(r.conditions.dest)
	}
	if err != nil {
		return ""
	}

	connectionModel, ok := model.(contractsorm.ModelWithConnection)
	if !ok {
		return ""
	}

	return connectionModel.Connection()
}

func (r *OrmBuilder) getObserver(dest any) contractsorm.Observer {
	destType := reflect.TypeOf(dest)
	if destType.Kind() == reflect.Pointer {
		destType = destType.Elem()
	}

	for _, observer := range r.modelToObserver {
		modelType := reflect.TypeOf(observer.Model)
		if modelType.Kind() == reflect.Pointer {
			modelType = modelType.Elem()
		}
		if destType.PkgPath() == modelType.PkgPath() && destType.Name() == modelType.Name() {
			return observer.Observer
		}
	}

	return nil
}

func (r *OrmBuilder) new(tx *sql.Tx) *OrmBuilder {
	query := r.fresh()
	query.tx = tx
	query.conditions = r.conditions.clone()
	query.query = r.query.Clone()

	return query
}

// fresh builds the same query with no state of its own: no conditions, no clauses. It is the starting
// point for a query that addresses a different table or exists only to carry one piece of state, such
// as the per-row eager load the cursor path builds.
func (r *OrmBuilder) fresh() *OrmBuilder {
	query := NewOrmBuilder(r.ctx, r.config, r.dbConfig, r.writer, r.tx, r.grammar, r.log, r.modelToObserver, nil, r.telemetryResolver, r.components)
	query.connection = r.connection
	return query
}

func (r *OrmBuilder) refreshConnection() (*OrmBuilder, error) {
	connection := r.getModelConnection()
	if connection == "" || connection == r.dbConfig.Connection {
		return r, nil
	}

	query, ok := r.queries[connection]
	if !ok || !query.ConnectionIsCurrent() {
		var err error
		query, _, err = BuildQuery(r.ctx, r.config, connection, r.log, r.modelToObserver, r.telemetryResolver)
		if err != nil {
			return nil, err
		}

		if r.queries == nil {
			r.queries = make(map[string]*OrmBuilder)
		}
		r.queries[connection] = query
	}

	// The clauses come from this query, the dialect from the connection they are being moved to.
	// Handing the receiver's builder over as-is compiles the statement with the source connection's
	// grammar and processor: a MySQL-built builder then renders backticks on Postgres and an insert
	// that expected no RETURNING loses its key.
	query = query.clone()
	query.conditions = r.conditions.clone()
	query.query = r.query.RebindGrammar(query.queryGrammar, query.components.Processor, query.dbConfig.Driver)

	return query, nil
}

func (r *OrmBuilder) restored(dest any) error {
	_, err := r.event(contractsorm.EventRestored, r.conditions.model, dest)

	return err
}

func (r *OrmBuilder) restoring(dest any) error {
	_, err := r.event(contractsorm.EventRestoring, r.conditions.model, dest)

	return err
}

func (r *OrmBuilder) retrieved(dest any) error {
	if err := r.applyEagerLoads(dest); err != nil {
		return err
	}
	if isSlice(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventRetrieved, r.conditions.model, dest)

	return err
}

// save writes a model, inserting it when it carries no key and updating the row it names when it
// does. The update carries the columns saveAssignments reads from the struct — the non-zero fields,
// the WithZero/Select-named zero ones and the save-time updated_at — and reports whether a statement
// ran, so Save can leave the updated event out for a set that turned out empty (performUpdate's
// `if (count($dirty) > 0)` guard).
//
// GORM also recreated the row when an update matched none, by upserting on the primary key. That is
// deliberately not carried over: it made saving a soft-deleted model resurrect it and overwrite every
// column from the value passed in, which is not what "updates value in a database" promises, and
// creating a row is what Create and FirstOrCreate are for. A save whose key matches nothing writes
// nothing.
func (r *OrmBuilder) save(value any) (bool, error) {
	if databaseorm.GetID(value) == nil {
		return r.execInsert(value)
	}

	assignments, err := r.saveAssignments(value)
	if err != nil {
		return false, err
	}
	// Nothing to set: performUpdate writes only a non-empty attribute set, and an empty assignment
	// list would compile to a statement no dialect accepts.
	if len(assignments) == 0 {
		return false, nil
	}

	statement, bindings, err := r.compileUpdateScoped(assignments, true)
	if err != nil {
		return false, err
	}

	_, err = r.execStatement(statement, bindings)
	if err != nil {
		return false, err
	}

	return true, r.assignSavedTimestamps(value, assignments)
}

func (r *OrmBuilder) saved(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventSaved, r.conditions.model, dest)
}

func (r *OrmBuilder) saving(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventSaving, r.conditions.model, dest)
}

// setConditions records the query's conditions and returns the query itself: a fluent call changes
// the receiver, the way Laravel's builder does, rather than producing a new query. Callers that need
// the original untouched clone() first.
func (r *OrmBuilder) setConditions(conditions Conditions) *OrmBuilder {
	r.conditions = conditions

	return r
}

// Clone returns an independent copy of the query: it shares the connection, grammar and observers,
// but owns its conditions and its builder, so clauses written to it never reach the receiver. It is
// what Orm.Query() hands a caller, mirroring Laravel's Connection::query(), which builds a new
// Query\Builder on every call.
func (r *OrmBuilder) Clone() contractsorm.Query {
	return r.clone()
}

// clone returns a query that shares the receiver's connection and scope but owns its conditions, so
// changing the copy leaves the original alone. It is the isolation primitive the execution paths use:
// dest(), the global-scope folding and the temporary clauses a compiled statement needs all start
// from a clone, which is what the copy-on-set semantics used to give them for free.
func (r *OrmBuilder) clone() *OrmBuilder {
	query := *r
	query.conditions = r.conditions.clone()
	query.query = r.query.Clone()

	return &query
}

func (r *OrmBuilder) updating(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventUpdating, r.conditions.model, dest)
}

func (r *OrmBuilder) updated(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventUpdated, r.conditions.model, dest)
}

// update compiles and runs an update, reporting how many rows it changed.
//
// GORM used to build and run this statement. It is compiled here instead, through the same grammar as
// the ORM's reads. The values still arrive as the statement's destination holds them, because that is
// where the updating event writes back an attribute an observer changed.
func (r *OrmBuilder) update(values any) (*contractsdb.Result, error) {
	return r.updateScoped(values, false)
}

// updateScoped compiles and runs an update, reporting how many rows it changed. A soft delete scopes
// the statement the way a delete does rather than the way an update does.
func (r *OrmBuilder) updateScoped(values any, byDest bool) (*contractsdb.Result, error) {
	if hasStringColumns(r.query.Selects()) && len(r.conditions.omit) > 0 {
		return nil, errors.OrmQuerySelectAndOmitsConflict
	}

	assignments, err := r.updateAssignments(values)
	if err != nil {
		return nil, err
	}

	// Nothing to set: an empty assignment list would compile to a statement no dialect accepts.
	if len(assignments) == 0 {
		return &contractsdb.Result{}, nil
	}

	// GORM wrote each SET value back onto the query's model while it built the statement, which is how
	// an attribute an observer changed through SetAttribute stayed readable on the model the caller
	// holds. The write-back belongs to the update path, where the values are a separate object from the
	// model; a soft delete is scoped by the model itself and already writes into it.
	if !byDest {
		if err := r.assignUpdatedValues(assignments); err != nil {
			return nil, err
		}
	}

	statement, bindings, err := r.compileUpdateScoped(assignments, byDest)
	if err != nil {
		return nil, err
	}

	return r.execStatement(statement, bindings)
}

// assignUpdatedValues copies an update's column/value pairs onto the query's model, so the caller
// reads the written values — including one an observer set through SetAttribute — from the model it
// passed to Model(), exactly as it did while GORM built the statement.
//
// The values go in through the framework's own schema, the same way assignInsertedKey writes a
// generated key back after an insert. Each pair is keyed by database column, because that is what
// updateAssignments resolved it to, so a Go field name or a raw column both land on the right field;
// a column the model does not declare is left alone.
func (r *OrmBuilder) assignUpdatedValues(assignments map[string]any) error {
	model := r.conditions.model
	if model == nil || len(assignments) == 0 {
		return nil
	}

	// Only a pointer leads back to the caller's model; a value with no address has no receiver to
	// write to.
	reflected := reflect.ValueOf(model)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return nil
	}

	schema, err := parseSchemaWithTable(model, r.declaredTable())
	if err != nil {
		return err
	}

	for column, value := range assignments {
		// SQL-side values can only be known after a read, not assigned to a Go
		// field while compiling the update.
		if querybuilder.IsExpression(value) {
			continue
		}
		if _, ok := value.(*querybuilder.Builder); ok {
			continue
		}
		field, ok := schema.FieldsByDBName[column]
		if !ok {
			continue
		}

		// Automatic timestamps must stay in sync with their SQL value. Other
		// columns retain their existing best-effort write-back for driver values
		// that database/sql accepts but the model cannot directly represent.
		if err := field.Set(r.ctx, reflected, value); err != nil && (field.AutoCreateTime != 0 || field.AutoUpdateTime != 0) {
			return err
		}
	}
	return nil
}

// countColumns returns the column a count reduces: the one column the query selects, or every row.
//
// Counting a single selected column is what the caller asked for by selecting it. With DISTINCT it
// becomes count(distinct column) rather than a leading DISTINCT over count(*), which would count every
// row instead.
func countColumns(query *OrmBuilder) []any {
	var stringColumns []string
	for _, item := range query.query.Selects() {
		if col, ok := item.(string); ok {
			stringColumns = append(stringColumns, col)
		}
	}
	if len(stringColumns) == 1 {
		return []any{stringColumns[0]}
	}

	return []any{"*"}
}

func buildSelectForCount(query *OrmBuilder) *OrmBuilder {

	// A single select column that carries a rename ("name as n") cannot be counted as it stands — the
	// count would come out count(name as n) — so the alias is trimmed back to its bare column first.
	// This shape appears when Paginate builds its count.
	// With several select columns there is nothing to trim: the count ignores them and reduces the
	// whole row with *.
	// For example: Select("name as n").Count() counts the name column, while Select("name", "age as a").Count() counts *.
	columns := selectStringColumns(query.query.Selects())
	if len(columns) == 1 && str.Of(columns[0]).Trim().Contains(" ") {
		query.query.SetSelects([]any{str.Of(columns[0]).Split(" ")[0]})
	}

	// The count a paginator runs never orders or bounds the rows it counts: Laravel builds it on a
	// clone without the orders, limit and offset (Builder::runPaginationCountQuery), so an ordered or
	// limited query still counts every matching row.
	query.query.CloneWithout("orders", "limit", "offset")

	return query.addGlobalScopes().buildConditions()
}

func filterFindConditions(conds ...any) error {
	if len(conds) > 0 {
		switch cond := conds[0].(type) {
		case string:
			if cond == "" {
				return errors.OrmMissingWhereClause
			}
		default:
			reflectValue := reflect.Indirect(reflect.ValueOf(cond))
			switch reflectValue.Kind() {
			case reflect.Slice, reflect.Array:
				if reflectValue.Len() == 0 {
					return errors.OrmMissingWhereClause
				}
			}
		}
	}

	return nil
}

// getDeletedAtColumn returns the database column of the model's soft-delete field, or "" when the
// model has none.
//
// It reports the column rather than the Go field name, because every caller feeds the result
// straight into SQL. The previous implementation returned the field name and relied on GORM to map
// it back to a column, which only worked while GORM built the statement.
func getDeletedAtColumn(model any) string {
	column, ok := softDeleteColumn(model)
	if !ok {
		return ""
	}

	return column
}

func getObserverEvent(event contractsorm.EventType, observer contractsorm.Observer) func(contractsorm.Event) error {
	switch event {
	case contractsorm.EventCreated:
		return observer.Created
	case contractsorm.EventCreating:
		if o, ok := observer.(contractsorm.ObserverWithCreating); ok {
			return o.Creating
		}
	case contractsorm.EventDeleted:
		return observer.Deleted
	case contractsorm.EventDeleting:
		if o, ok := observer.(contractsorm.ObserverWithDeleting); ok {
			return o.Deleting
		}
	case contractsorm.EventForceDeleted:
		return observer.ForceDeleted
	case contractsorm.EventForceDeleting:
		if o, ok := observer.(contractsorm.ObserverWithForceDeleting); ok {
			return o.ForceDeleting
		}
	case contractsorm.EventRestored:
		if o, ok := observer.(contractsorm.ObserverWithRestored); ok {
			return o.Restored
		}
	case contractsorm.EventRestoring:
		if o, ok := observer.(contractsorm.ObserverWithRestoring); ok {
			return o.Restoring
		}
	case contractsorm.EventRetrieved:
		if o, ok := observer.(contractsorm.ObserverWithRetrieved); ok {
			return o.Retrieved
		}
	case contractsorm.EventSaved:
		if o, ok := observer.(contractsorm.ObserverWithSaved); ok {
			return o.Saved
		}
	case contractsorm.EventSaving:
		if o, ok := observer.(contractsorm.ObserverWithSaving); ok {
			return o.Saving
		}
	case contractsorm.EventUpdated:
		return observer.Updated
	case contractsorm.EventUpdating:
		if o, ok := observer.(contractsorm.ObserverWithUpdating); ok {
			return o.Updating
		}
	}

	return nil
}

// modelToStruct normalizes a model value for database operations.
// It handles nil pointers by creating new instances, resolves interface types,
// unwraps slice/array/pointer types to their underlying struct type,
// validates that the final type is a struct (rejecting maps and other types),
// and returns a new instance of the struct type ready for database operations.
// Returns an error if the model type is invalid (map, primitive, etc.).
func modelToStruct(model any) (any, error) {
	if model == nil {
		return nil, errors.OrmQueryInvalidModel.Args("nil")
	}

	modelValue := reflect.ValueOf(model)
	if modelValue.Kind() == reflect.Pointer && modelValue.IsNil() {
		// If the model is a pointer and is nil, we will create a new instance of the model
		modelValue = reflect.New(modelValue.Type().Elem())
	}
	modelType := reflect.Indirect(modelValue).Type()

	if modelType.Kind() == reflect.Interface {
		modelType = reflect.Indirect(modelValue).Elem().Type()
	}

	for modelType.Kind() == reflect.Slice || modelType.Kind() == reflect.Array || modelType.Kind() == reflect.Pointer {
		modelType = modelType.Elem()
	}

	if modelType.Kind() == reflect.Map {
		return nil, errors.OrmQueryInvalidModel.Args("map")
	}

	if modelType.Kind() != reflect.Struct {
		if modelType.PkgPath() == "" {
			return nil, errors.OrmQueryInvalidModel.Args("")
		}
		return nil, errors.OrmQueryInvalidModel.Args(fmt.Sprintf(": %s.%s", modelType.PkgPath(), modelType.Name()))
	}

	newModel := reflect.New(modelType)

	return newModel.Interface(), nil
}

func isSlice(dest any) bool {
	if dest == nil {
		return false
	}
	destKind := reflect.Indirect(reflect.ValueOf(dest)).Type().Kind()

	return destKind == reflect.Slice || destKind == reflect.Array
}

func hasID(dest any) bool {
	return databaseorm.GetID(dest) != nil
}

// hasSelectColumns reports whether conditions has any selections (either column names or raw expressions).
func hasSelectColumns(r *OrmBuilder) bool {
	if r.query == nil {
		return len(r.conditions.selectSubs) > 0
	}
	return len(r.query.Selects()) > 0 || len(r.conditions.selectSubs) > 0
}
