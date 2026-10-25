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

	"github.com/goravel/framework/contracts/config"
	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsdb "github.com/goravel/framework/contracts/database/db"
	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractslogger "github.com/goravel/framework/contracts/database/logger"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/contracts/log"
	contractstelemetry "github.com/goravel/framework/contracts/telemetry"
	"github.com/goravel/framework/database/db"
	databasedriver "github.com/goravel/framework/database/driver"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/database/query/core"
	"github.com/goravel/framework/database/utils"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/support/database"
	"github.com/goravel/framework/support/deep"
	"github.com/goravel/framework/support/str"
	"github.com/spf13/cast"
	gormio "gorm.io/gorm"
)

type Query struct {
	config            config.Config
	ctx               context.Context
	grammar           contractsdriver.Grammar
	queryGrammar      querybuilder.Grammar
	log               log.Log
	logger            contractslogger.Logger
	instance          *gormio.DB
	queries           map[string]*Query
	modelToObserver   []contractsorm.ModelToObserver
	conditions        Conditions
	dbConfig          contractsdatabase.Config
	mutex             *sync.Mutex
	telemetryResolver contractstelemetry.Resolver
}

func NewQuery(
	ctx context.Context,
	config config.Config,
	dbConfig contractsdatabase.Config,
	instance *gormio.DB,
	grammar contractsdriver.Grammar,
	log log.Log,
	modelToObserver []contractsorm.ModelToObserver,
	conditions *Conditions,
	telemetryResolver contractstelemetry.Resolver,
) *Query {
	// The framework's query grammar, derived from the connection, renders the identifiers of a
	// compiled statement — its columns, tables and relation subqueries — so their quoting matches the
	// dialect. Statements are compiled with it and run over the database/sql pool; GORM no longer
	// builds or runs them.
	// An unrecognised driver is deliberately allowed (a module may bring its own database/sql
	// name); it warns once, naming the driver and the recognised set, rather than failing.
	// When log is nil, the warning is silently lost: a nil logger cannot report anything, and
	// the framework allows NewQuery to be called with nil log for testing or when logging is
	// disabled. The base grammar still works, so silence is correct behaviour rather than an error.
	queryGrammar, known := querybuilder.NewGrammar(dbConfig.Driver, dbConfig.Prefix)
	if !known && log != nil {
		if err := querybuilder.UnrecognisedDriverWarning(dbConfig.Driver); err != nil {
			log.Warningf("%s", err)
		}
	}

	queryImpl := &Query{
		config:            config,
		ctx:               ctx,
		dbConfig:          dbConfig,
		instance:          instance,
		grammar:           grammar,
		queryGrammar:      queryGrammar,
		log:               log,
		logger:            queryLogger(config, log),
		modelToObserver:   modelToObserver,
		queries:           make(map[string]*Query),
		telemetryResolver: telemetryResolver,
	}

	if conditions != nil {
		queryImpl.conditions = *conditions
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
func (r *Query) trace(begin *carbon.Carbon, statement string, bindings []any, rowsAffected int64, err error) {
	if r.logger == nil {
		return
	}

	r.logger.Trace(r.ctx, begin, db.Explain(statement, bindings...), rowsAffected, err)
}

func BuildQuery(ctx context.Context, config config.Config, connection string, log log.Log, modelToObserver []contractsorm.ModelToObserver, telemetryResolver contractstelemetry.Resolver) (*Query, contractsdatabase.Config, error) {
	driverCallback, exist := config.Get(fmt.Sprintf("database.connections.%s.via", connection)).(func() (contractsdriver.Driver, error))
	if !exist {
		return nil, contractsdatabase.Config{}, errors.DatabaseConfigNotFound
	}

	driver, err := driverCallback()
	if err != nil {
		return nil, contractsdatabase.Config{}, err
	}

	pool := driver.Pool()
	logger := db.NewLogger(config, log)
	gorm, _, err := databasedriver.BuildGorm(config, logger, pool, connection, telemetryResolver)
	if err != nil {
		return nil, pool.Writers[0], err
	}

	return NewQuery(ctx, config, pool.Writers[0], gorm, driver.Grammar(), log, modelToObserver, nil, telemetryResolver), pool.Writers[0], nil
}

// DEPRECATED Use BeginTransaction instead.
func (r *Query) Begin() (contractsorm.Query, error) {
	return r.BeginTransaction()
}

func (r *Query) BeginTransaction() (contractsorm.Query, error) {
	if r.InTransaction() {
		return r, nil
	}

	tx := r.instance.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	return r.new(tx), nil
}

func (r *Query) Commit() error {
	return r.instance.Commit().Error
}

func (r *Query) Context() context.Context {
	return r.ctx
}

func (r *Query) Count() (int64, error) {
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

func (r *Query) Create(value any) error {
	query := r.dest(value).buildConditions()

	if hasStringColumns(query.conditions.selectItems) && len(query.conditions.omit) > 0 {
		return errors.OrmQuerySelectAndOmitsConflict
	}

	// The three shapes GORM distinguished — a plain create, and one narrowed by Select or by Omit — are
	// one insert here, because the column filter is applied to the record itself.
	return query.create(value)
}

func (r *Query) Cursor() chan contractsdb.Row {
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

func (r *Query) DB() (*sql.DB, error) {
	return r.instance.DB()
}

func (r *Query) Delete(dests ...any) (*contractsdb.Result, error) {
	var (
		dest  any
		query *Query
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

	res, err := query.deleteOrSoftDelete()
	if err != nil {
		return nil, err
	}

	if err := query.deleted(dest); err != nil {
		return nil, err
	}

	return res, nil
}

func (r *Query) Distinct(columns ...string) contractsorm.Query {
	conditions := r.conditions
	conditions.distinct = true
	conditions.distinctColumns = make([]any, len(columns))
	for i, column := range columns {
		conditions.distinctColumns[i] = column
	}

	return r.setConditions(conditions)
}

func (r *Query) Driver() string {
	return r.dbConfig.Driver
}

// Exec runs a statement the caller wrote. The statement is passed through untouched apart from the
// dialect's placeholder style, so a caller can use it for DDL and for anything the builder does not
// cover.
func (r *Query) Exec(statement string, values ...any) (*contractsdb.Result, error) {
	query := r.buildConditions()

	return query.execStatement(statement, values)
}

func (r *Query) Exists() (bool, error) {
	query := r.addGlobalScopes().buildConditions()

	// The statement selects rows and is limited to one, rather than selecting the literal 1: a
	// literal would be wrapped as an identifier, which some dialects reject.
	conditions := query.conditions
	limit := 1
	conditions.limit = &limit

	statement, bindings, err := query.setConditions(conditions).compileSelect()
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

func (r *Query) Find(dest any, conds ...any) error {
	query := r.dest(dest).addGlobalScopes().buildConditions()

	if err := filterFindConditions(conds...); err != nil {
		return err
	}

	query = query.withFindConditions(conds)

	statement, bindings, err := query.compileSelect()
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	if _, err := query.scanRows(rows, dest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	// Find reports no row as an empty destination rather than as an error, and still runs the
	// retrieved hooks, which is what the behaviour this replaced did.
	return query.retrieved(dest)
}

// withFindConditions folds Find's inline conditions into the query's own, so they compile alongside
// everything else.
//
// A lone scalar addresses the model's primary key, which is the form the relation writers use; a
// column with its value is passed through as a single condition.
func (r *Query) withFindConditions(conds []any) *Query {
	if len(conds) == 0 {
		return r
	}

	add := func(column string, args []any) *Query {
		conditions := r.conditions
		conditions.where = append(conditions.where, contractsdriver.Where{Query: column, Args: args})

		return r.setConditions(conditions)
	}

	if column, ok := conds[0].(string); ok {
		if len(conds) > 1 {
			return add(column, conds[1:])
		}

		return r
	}

	// A list stands for several primary keys, which keep the IN form built from the bare column.
	if isSlice(conds[0]) {
		if column := r.primaryKeyColumnName(); column != "" {
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

func (r *Query) FindOrFail(dest any, conds ...any) error {
	query := r.dest(dest).addGlobalScopes().buildConditions()

	if err := filterFindConditions(conds...); err != nil {
		return err
	}

	query = query.withFindConditions(conds)

	statement, bindings, err := query.compileSelect()
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

	return query.retrieved(dest)
}

// FindOr retrieves the first row whose key matches id, or runs the callback when there is none,
// mirroring Laravel's Builder::findOr. It is Find's read with FirstOr's missing-row behaviour.
func (r *Query) FindOr(dest any, id any, callback func() error) error {
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

	return query.retrieved(dest)
}

func (r *Query) First(dest any) error {
	_, err := r.firstRow(dest)

	return err
}

// firstRow runs the query as a single-row read, reporting whether a row was found and firing the
// retrieved hook when it was. A statement that matched nothing is not an error: First reads it as an
// empty destination, while the callers that care ask for the flag.
func (r *Query) firstRow(dest any) (bool, error) {
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
	if count == 0 {
		return false, nil
	}

	return true, query.retrieved(dest)
}

func (r *Query) FirstOr(dest any, callback func() error) error {
	found, err := r.firstRow(dest)
	if err != nil {
		return err
	}
	if !found {
		return callback()
	}

	return nil
}

func (r *Query) FirstOrCreate(dest any, conds ...any) error {
	if len(conds) == 0 {
		return errors.OrmQueryConditionRequired
	}

	found, err := r.firstOrInit(dest, conds[0], conds[1:], nil)
	if err != nil {
		return err
	}
	if found {
		// firstOrInit has already run the retrieved hooks.
		return nil
	}

	return r.Create(dest)
}

func (r *Query) FirstOrFail(dest any) error {
	found, err := r.firstRow(dest)
	if err != nil {
		return err
	}
	if !found {
		return errors.OrmRecordNotFound
	}

	return nil
}

// FirstOrNew looks a model up and, finding none, prepares one instead of storing it: the destination
// is filled from the query's own equalities and from the attributes, and nothing is written.
func (r *Query) FirstOrNew(dest any, attributes any, values ...any) error {
	_, err := r.firstOrInit(dest, attributes, values, nil)

	return err
}

func (r *Query) ForceDelete(dests ...any) (*contractsdb.Result, error) {
	var (
		dest  any
		query *Query
	)

	if len(dests) > 0 {
		dest = dests[0]
		query = r.dest(dest).WithTrashed().(*Query).addGlobalScopes().buildConditions()
	} else {
		query = r.WithTrashed().(*Query).addGlobalScopes().buildConditions()
	}

	if err := query.forceDeleting(dest); err != nil {
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
// columns render into this statement's select list, but the query keeps the selection it already
// had — and it only takes the columns when it had none — so a statement compiled afterwards is
// unchanged.
func (r *Query) Get(dest any, columns ...any) error {
	if len(columns) == 0 {
		return r.Find(dest)
	}

	// onceWithColumns temporarily installs the columns, runs the query, then restores the original
	// selection. This is how Laravel's get($columns) works: it mutates $this->columns in place,
	// executes, then writes the original back.
	original := r.conditions.selectItems
	defer func() { r.conditions.selectItems = original }()

	if !hasSelectColumns(r.conditions) {
		for _, col := range columnsToStrings(columns) {
			r.conditions.selectItems = append(r.conditions.selectItems, col)
		}
	}

	return r.Find(dest)
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
func (r *Query) Group(column string) contractsorm.Query {
	return r.GroupBy(column)
}

func (r *Query) GroupBy(column ...string) contractsorm.Query {
	if len(column) == 0 {
		return r
	}

	conditions := r.conditions
	conditions.groupBy = column

	return r.setConditions(conditions)
}

func (r *Query) Having(query any, args ...any) contractsorm.Query {
	conditions := r.conditions

	// A string that carries a binding placeholder is a raw fragment rather than a column name. The
	// builder must keep accepting the shape GORM did, so such a string is routed to HavingRaw, exactly
	// as applyWhere routes a string containing "?" to WhereRaw. The conditions are compiled later, so
	// the raw fragment is carried as a nested callback that runs against the child builder then.
	if value, ok := query.(string); ok && containsPlaceholder(value) {
		conditions.having = &contractsdriver.Having{
			Query: func(builder *querybuilder.Builder) *querybuilder.Builder {
				return builder.HavingRaw(value, args, core.BooleanAnd)
			},
		}

		return r.setConditions(conditions)
	}

	conditions.having = &contractsdriver.Having{
		Query: query,
		Args:  args,
	}

	return r.setConditions(conditions)
}

func (r *Query) Join(query string, args ...any) contractsorm.Query {
	conditions := r.conditions
	conditions.join = deep.Append(conditions.join, contractsdriver.Join{
		Query: query,
		Args:  args,
	})

	return r.setConditions(conditions)
}

func (r *Query) Limit(limit int) contractsorm.Query {
	conditions := r.conditions
	conditions.limit = &limit

	return r.setConditions(conditions)
}

func (r *Query) OfMany(column, aggregate string) contractsorm.Query {
	if column == "" {
		column = "id"
	}
	if aggregate == "" {
		aggregate = "MAX"
	}
	conditions := r.conditions
	conditions.oneOfMany = &oneOfManyConfig{column: column, aggregate: aggregate}
	return r.setConditions(conditions)
}

func (r *Query) LatestOfMany(column ...string) contractsorm.Query {
	col := ""
	if len(column) > 0 {
		col = column[0]
	}
	return r.OfMany(col, "MAX")
}

func (r *Query) OldestOfMany(column ...string) contractsorm.Query {
	col := ""
	if len(column) > 0 {
		col = column[0]
	}
	return r.OfMany(col, "MIN")
}

// Load lazily loads the named relation onto an already-populated model, the way Laravel's
// Model::load() does. The relation is eager-loaded against the model instance itself: the instance's
// primary key supplies the keys the related query is constrained by, so the model's own table is
// never re-read and a Load issued from an unconstrained query still attaches the receiver's own
// relations rather than whichever row the table happens to return first.
func (r *Query) Load(model any, relation string, args ...any) error {
	if relation == "" {
		return errors.OrmQueryEmptyRelation
	}

	destType := reflect.TypeOf(model)
	if destType.Kind() != reflect.Pointer {
		return errors.OrmQueryModelNotPointer
	}

	if id := database.GetID(model); id == nil {
		return errors.OrmQueryEmptyId
	}

	// Queue the relation the same way With does, then run the loaders directly against the model.
	// Going through With would return the contractsorm.Query interface, and the loaders live on the
	// concrete *Query, so the entries are queued here to avoid a type assertion on every call.
	entries, err := parseEagerLoad(append([]any{relation}, args...))
	if err != nil {
		return err
	}
	conditions := r.conditions
	for _, entry := range entries {
		conditions.eagerLoad = upsertEagerLoadEntry(conditions.eagerLoad, entry, entry.callback != nil || entry.columns != nil)
	}

	return r.setConditions(conditions).applyEagerLoads(model)
}

func (r *Query) LoadMissing(model any, relation string, args ...any) error {
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
					id = database.GetIDByReflect(v.Field(i).Type().Elem(), v.Field(i).Elem())
				}
			} else if v.Field(i).Kind() == reflect.Slice {
				if v.Field(i).Len() > 0 {
					return nil
				}
			} else {
				id = database.GetIDByReflect(v.Field(i).Type(), v.Field(i))
			}
			if cast.ToString(id) != "" {
				return nil
			}
		}
	}

	return r.Load(model, relation, args...)
}

func (r *Query) LockForUpdate() contractsorm.Query {
	conditions := r.conditions
	conditions.lockForUpdate = true

	return r.setConditions(conditions)
}

func (r *Query) Model(value any) contractsorm.Query {
	if r.conditions.model != nil {
		return r
	}

	conditions := r.conditions
	conditions.model = value

	return r.setConditions(conditions)
}

func (r *Query) Observe(model any, observer contractsorm.Observer) {
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

func (r *Query) Offset(offset int) contractsorm.Query {
	conditions := r.conditions
	conditions.offset = &offset

	return r.setConditions(conditions)
}

func (r *Query) Omit(columns ...string) contractsorm.Query {
	conditions := r.conditions
	conditions.omit = columns

	return r.setConditions(conditions)
}

// DEPRECATED: Use OrderByRaw instead
func (r *Query) Order(value any) contractsorm.Query {
	return r.OrderByRaw(fmt.Sprintf("%s", value))
}

func (r *Query) OrderBy(column string, direction ...string) contractsorm.Query {
	orderDirection := "asc"
	if len(direction) > 0 {
		orderDirection = strings.ToLower(direction[0])
	}

	return r.OrderByRaw(r.queryGrammar.Wrap(column) + " " + orderDirection)
}

func (r *Query) OrderByDesc(column string) contractsorm.Query {
	return r.OrderByRaw(r.queryGrammar.Wrap(column) + " desc")
}

func (r *Query) OrderByRaw(raw string) contractsorm.Query {
	var rawAny any = raw

	conditions := r.conditions
	conditions.order = deep.Append(r.conditions.order, rawAny)

	return r.setConditions(conditions)
}

func (r *Query) Instance() *gormio.DB {
	return r.instance
}

func (r *Query) InRandomOrder() contractsorm.Query {
	return r.OrderByRaw(randomOrderFunction(r.dbConfig.Driver))
}

func (r *Query) InTransaction() bool {
	committer, ok := r.Instance().Statement.ConnPool.(gormio.TxCommitter)

	return ok && committer != nil
}

func (r *Query) OrWhere(query any, args ...any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Query: query,
		Args:  args,
		Or:    true,
	})
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

	// Resolve the total exactly as Laravel's Builder::paginate, and as database/db.Paginate does: a
	// non-zero total already carried by the caller stands for its $total argument and short-circuits
	// the count, while an unset (nil or zero) total is counted -- the count is written back only when
	// the caller asked for it.
	resolvedTotal := int64(0)
	if total != nil {
		resolvedTotal = *total
	}
	if resolvedTotal == 0 {
		var (
			count int64
			err   error
		)
		if r.conditions.table == nil && r.conditions.model == nil {
			count, err = r.Model(dest).Count()
		} else {
			count, err = r.Count()
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

// CursorPaginate fetches one keyset ("seek") page and scans it into dest, which must be a pointer to
// a slice: a *[]map[string]any for an untyped page, or a pointer to a slice of structs for a typed
// one. cursor is the encoded anchor — an empty string is the first page, and a non-empty malformed
// string is refused with errors.QueryCursorInvalid rather than silently restarting.
//
// The statement runs exactly once and is never counted. Unlike Paginate there is no total; the page
// carries only the rows and the cursors that reach its neighbours.
func (r *Query) CursorPaginate(perPage int, cursor string, dest any) (contractsdb.CursorPaginator, error) {
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

	return cursorPaginatorPage(querybuilder.NewCursorPaginator(items, page.PerPage, page.Cursor, page.HasCursor, page.Parameters)), nil
}

// cursorBuilder builds the query's statement with its orders expressed as structured orders, which
// the keyset predicate reads a column and a direction from. The ORM stores an order as a raw fragment
// ("`id` asc"), which carries neither, so each such fragment is parsed back into the column and
// direction it was written from; a fragment that is not a plain "column direction" pair is left as a
// raw order and contributes no predicate.
func (r *Query) cursorBuilder() (*querybuilder.Builder, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return nil, err
	}

	columns, directions, raws := parseCursorOrders(r.conditions.order)
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
func (r *Query) cursorRows(statement string, bindings []any, dest any) ([]map[string]any, error) {
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

func (r *Query) Pluck(column string, dest any) error {
	query := r.addGlobalScopes().buildConditions()

	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Pointer || destValue.IsNil() || destValue.Elem().Kind() != reflect.Slice {
		return errors.DatabaseUnsupportedType.Args(reflect.TypeOf(dest).String(), "pointer to slice")
	}

	conditions := query.conditions
	conditions.selectItems = []any{column}
	conditions.raw = nil

	statement, bindings, err := query.setConditions(conditions).compileSelect()
	if err != nil {
		return err
	}

	rows, err := query.queryRows(statement, bindings)
	if err != nil {
		return err
	}
	defer errors.Ignore(rows.Close)

	// The scanned value is converted to the caller's element type, because a driver may report a
	// column as a different but equivalent Go type than the destination holds.
	target := destValue.Elem()
	elementType := target.Type().Elem()

	for rows.Next() {
		var value any
		if err := rows.Scan(&value); err != nil {
			return err
		}

		element := reflect.New(elementType).Elem()
		if converted := castKey(value, elementType); converted != nil {
			convertedValue := reflect.ValueOf(converted)
			switch {
			case convertedValue.Type().AssignableTo(elementType):
				element.Set(convertedValue)
			case convertedValue.Type().ConvertibleTo(elementType):
				element.Set(convertedValue.Convert(elementType))
			}
		}

		target = reflect.Append(target, element)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	destValue.Elem().Set(target)

	return nil
}

func (r *Query) Raw(sql string, values ...any) contractsorm.Query {
	conditions := r.conditions
	conditions.raw = &Select{query: sql, args: values}

	return r.setConditions(conditions)
}

func (r *Query) Restore(dests ...any) (*contractsdb.Result, error) {
	var (
		dest  any
		query *Query
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

func (r *Query) Rollback() error {
	return r.instance.Rollback().Error
}

func (r *Query) Save(dest any) error {
	if hasStringColumns(r.conditions.selectItems) && len(r.conditions.omit) > 0 {
		return errors.OrmQuerySelectAndOmitsConflict
	}

	id := database.GetID(dest)
	update := id != nil

	var query *Query
	if update {
		query = r.dest(dest).addGlobalScopes().buildConditions()
	} else {
		query = r.dest(dest).buildConditions()
	}

	var err error
	if dest, err = query.saving(dest); err != nil {
		return err
	}
	if update {
		if dest, err = query.updating(dest); err != nil {
			return err
		}
	} else {
		if dest, err = query.creating(dest); err != nil {
			return err
		}
	}

	// The three shapes GORM distinguished — a plain save, and one narrowed by Select or by Omit — are
	// one statement here, because the column filter carries that difference.
	if err := query.save(dest); err != nil {
		return err
	}

	if update {
		if _, err := query.updated(dest); err != nil {
			return err
		}
	} else {
		if err := query.created(dest); err != nil {
			return err
		}
	}
	if _, err := query.saved(dest); err != nil {
		return err
	}

	return nil
}

func (r *Query) SaveQuietly(value any) error {
	return r.WithoutEvents().Save(value)
}

func (r *Query) Scan(dest any) error {
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

func (r *Query) Scopes(funcs ...func(contractsorm.Query) contractsorm.Query) contractsorm.Query {
	conditions := r.conditions
	conditions.scopes = deep.Append(r.conditions.scopes, funcs...)

	return r.setConditions(conditions)
}

// Select replaces the selected columns and their bindings, mirroring Laravel's select().
func (r *Query) Select(columns ...string) contractsorm.Query {
	conditions := r.conditions
	conditions.selectItems = nil
	conditions.selectSubs = nil
	if len(columns) == 0 {
		columns = []string{"*"}
	}
	for _, column := range columns {
		conditions.selectItems = deep.Append[any](conditions.selectItems, column)
	}

	return r.setConditions(conditions)
}

func (r *Query) SelectRaw(query any, args ...any) contractsorm.Query {
	conditions := r.conditions
	conditions.selectItems = deep.Append[any](conditions.selectItems, &Select{
		query: query,
		args:  args,
	})

	return r.setConditions(conditions)
}

func (r *Query) WithContext(ctx context.Context) contractsorm.Query {
	instance := r.instance.WithContext(ctx)

	return NewQuery(ctx, r.config, r.dbConfig, instance, r.grammar, r.log, r.modelToObserver, nil, r.telemetryResolver)
}

func (r *Query) SharedLock() contractsorm.Query {
	conditions := r.conditions
	conditions.sharedLock = true

	return r.setConditions(conditions)
}

// UseWritePdo forces the query's reads to resolve to the write connection instead of the replica, so
// a read that must observe a write issued on the same connection is not served by a lagging replica.
// It mirrors Laravel's Builder::useWritePdo(), which stores the flag the connection call reads as the
// inverse of its $useReadPdo argument; in this port the executor's readPool honours it.
func (r *Query) UseWritePdo() contractsorm.Query {
	conditions := r.conditions
	conditions.useWritePdo = true

	return r.setConditions(conditions)
}

func (r *Query) Sum(column string, dest any) error {
	return r.aggregate("sum", column, dest)
}

func (r *Query) Avg(column string, dest any) error {
	return r.aggregate("avg", column, dest)
}

func (r *Query) Min(column string, dest any) error {
	return r.aggregate("min", column, dest)
}

func (r *Query) Max(column string, dest any) error {
	return r.aggregate("max", column, dest)
}

// aggregate runs a single-column aggregate and scans its one value into dest, which GORM's own
// Select(...).Row().Scan did before.
func (r *Query) aggregate(function string, column string, dest any) error {
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

// Table points the query at a table by name. A string name is stored bare and the framework grammar
// applies the connection prefix once when the statement is rendered; a querybuilder.Expression is
// stored as given and rendered verbatim, for a caller naming a table that is already qualified.
func (r *Query) Table(name any, args ...any) contractsorm.Query {
	conditions := r.conditions
	conditions.table = &Table{
		name: name,
		args: args,
	}

	return r.setConditions(conditions)
}

func (r *Query) ToSql() contractsorm.ToSql {
	return NewToSql(r, r.log, false)
}

func (r *Query) ToRawSql() contractsorm.ToSql {
	return NewToSql(r, r.log, true)
}

func (r *Query) Update(column any, value ...any) (*contractsdb.Result, error) {
	query := r.addGlobalScopes().buildConditions()

	if _, ok := column.(string); !ok && len(value) > 0 {
		return nil, errors.OrmQueryInvalidParameter
	}

	// The model the query addresses is the framework's own state; reading it from the statement would
	// be reading the same value through GORM.
	var singleUpdate bool
	if model := query.conditions.model; model != nil {
		if id := database.GetID(model); id != nil {
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
	}

	return res, err
}

// UpdateOrCreate looks a model up, writing the values it was given either way: the found model is saved
// with them, and a model that was not found is created carrying them.
func (r *Query) UpdateOrCreate(dest any, attributes any, values any) error {
	found, err := r.firstOrInit(dest, attributes, nil, []any{values})
	if err != nil {
		return err
	}
	if found {
		return r.Save(dest)
	}

	return r.Create(dest)
}

// firstOrInit looks a model up, and finding none describes one from the query's own equalities and from
// the attributes, with the assigned values applied either way. It reports whether the row was found.
//
// GORM expressed this as FirstOrInit plus Attrs and Assign; the rules are reproduced here so the three
// methods above share one lookup.
func (r *Query) firstOrInit(dest any, find any, attrs []any, assigns []any) (bool, error) {
	query := r.dest(dest)

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
		if err := query.assignWhereValues(dest); err != nil {
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
func (r *Query) foldFindCondition(find any) (*Query, error) {
	if mapping, ok := asColumnMap(find); ok {
		conditions := r.conditions
		for _, key := range slices.Sorted(maps.Keys(mapping)) {
			conditions.where = append(conditions.where, contractsdriver.Where{Query: key, Args: []any{mapping[key]}})
		}

		return r.setConditions(conditions), nil
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

	schema, err := parseSchema(r.instance, find)
	if err != nil {
		return nil, err
	}

	conditions := r.conditions
	for _, field := range schema.Fields {
		value, zero := field.ValueOf(r.ctx, reflected)
		if zero {
			continue
		}

		conditions.where = append(conditions.where, contractsdriver.Where{Query: field.DBName, Args: []any{value}})
	}

	return r.setConditions(conditions), nil
}

// assignWhereValues copies the query's own equalities onto a model, which is how a model built by
// FirstOrNew still carries the conditions it was looked up with.
func (r *Query) assignWhereValues(dest any) error {
	for _, item := range r.conditions.where {
		column, ok := item.Query.(string)
		if !ok || item.Type != contractsdriver.WhereTypeBase || len(item.Args) != 1 || containsPlaceholder(column) || isSlice(item.Args[0]) {
			continue
		}

		if err := r.assignColumn(dest, column, item.Args[0]); err != nil {
			return err
		}
	}

	return nil
}

// assignValue fills a model from the shapes the callers pass: a map, whose keys name the columns, or a
// model, whose columns carrying a value are copied.
func (r *Query) assignValue(dest any, value any) error {
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

	schema, err := parseSchema(r.instance, dest)
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
func (r *Query) assignColumn(dest any, name string, value any) error {
	schema, err := parseSchema(r.instance, dest)
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

func (r *Query) When(condition bool, callback func(query contractsorm.Query) contractsorm.Query, falseCallback ...func(query contractsorm.Query) contractsorm.Query) contractsorm.Query {
	if condition {
		return callback(r)
	}

	if len(falseCallback) > 0 {
		return falseCallback[0](r)
	}

	return r
}

func (r *Query) Where(query any, args ...any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Query: query,
		Args:  args,
	})
}

func (r *Query) WhereAll(columns []string, args ...any) contractsorm.Query {
	op, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		return r.withError(err).(*Query)
	}

	var conditions []string
	var conditionArgs []any
	for _, column := range columns {
		fragment, fragmentArgs := whereFragment(column, op, value)
		conditions = append(conditions, fragment)
		conditionArgs = append(conditionArgs, fragmentArgs...)
	}

	query := strings.Join(conditions, " AND ")
	r = r.addWhere(contractsdriver.Where{
		Query: query,
		Args:  conditionArgs,
	}).(*Query)

	return r
}

func (r *Query) WhereAny(columns []string, args ...any) contractsorm.Query {
	op, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		return r.withError(err).(*Query)
	}

	var conditions []string
	var conditionArgs []any
	for _, column := range columns {
		fragment, fragmentArgs := whereFragment(column, op, value)
		conditions = append(conditions, fragment)
		conditionArgs = append(conditionArgs, fragmentArgs...)
	}

	query := fmt.Sprintf("(%s)", strings.Join(conditions, " OR "))
	r = r.addWhere(contractsdriver.Where{
		Query: query,
		Args:  conditionArgs,
	}).(*Query)

	return r
}

// WhereIn adds a "where column in (...)" clause. The column and the value list are added as one
// condition carrying a slice argument, not as a raw "IN ?" fragment: the compiler reads a slice
// argument as the IN list and expands it to one placeholder per value (Laravel binds whereIn the
// same way). A raw fragment would bind the whole slice to a single placeholder.
func (r *Query) WhereIn(column string, values []any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{Query: column, Args: []any{values}})
}

func (r *Query) WhereJsonContains(column string, value any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContains,
		Query: column,
		Args:  []any{value},
	})
}

func (r *Query) WhereJsonContainsKey(column string) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContainsKey,
		Query: column,
	})
}

func (r *Query) WhereJsonDoesntContain(column string, value any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContains,
		Query: column,
		Args:  []any{value},
		IsNot: true,
	})
}

func (r *Query) WhereJsonDoesntContainKey(column string) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContainsKey,
		Query: column,
		IsNot: true,
	})
}

func (r *Query) WhereJsonLength(column string, args ...any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonLength,
		Query: column,
		Args:  args,
	})
}

func (r *Query) OrWhereIn(column string, values []any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{Query: column, Args: []any{values}, Or: true})
}

func (r *Query) OrWhereJsonContains(column string, value any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContains,
		Query: column,
		Args:  []any{value},
		Or:    true,
	})
}

func (r *Query) OrWhereJsonContainsKey(column string) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContainsKey,
		Query: column,
		Or:    true,
	})
}

func (r *Query) OrWhereJsonDoesntContain(column string, value any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContains,
		Query: column,
		Args:  []any{value},
		IsNot: true,
		Or:    true,
	})
}

func (r *Query) OrWhereJsonDoesntContainKey(column string) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonContainsKey,
		Query: column,
		IsNot: true,
		Or:    true,
	})
}

func (r *Query) OrWhereJsonLength(column string, args ...any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{
		Type:  contractsdriver.WhereTypeJsonLength,
		Query: column,
		Args:  args,
		Or:    true,
	})
}

func (r *Query) WhereNotIn(column string, values []any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{Query: column, Args: []any{values}, IsNot: true})
}

func (r *Query) OrWhereNotIn(column string, values []any) contractsorm.Query {
	return r.addWhere(contractsdriver.Where{Query: column, Args: []any{values}, Or: true, IsNot: true})
}

func (r *Query) WhereBetween(column string, x, y any) contractsorm.Query {
	return r.Where(fmt.Sprintf("%s BETWEEN ? AND ?", column), x, y)
}

func (r *Query) WhereNotBetween(column string, x, y any) contractsorm.Query {
	return r.Where(fmt.Sprintf("%s NOT BETWEEN ? AND ?", column), x, y)
}

func (r *Query) OrWhereBetween(column string, x, y any) contractsorm.Query {
	return r.OrWhere(fmt.Sprintf("%s BETWEEN ? AND ?", column), x, y)
}

func (r *Query) OrWhereNotBetween(column string, x, y any) contractsorm.Query {
	return r.OrWhere(fmt.Sprintf("%s NOT BETWEEN ? AND ?", column), x, y)
}

func (r *Query) OrWhereNull(column string) contractsorm.Query {
	return r.OrWhere(fmt.Sprintf("%s IS NULL", column))
}

func (r *Query) WhereNone(columns []string, args ...any) contractsorm.Query {
	op, value, err := utils.PrepareWhereOperatorAndValue(args...)
	if err != nil {
		return r.withError(err).(*Query)
	}

	var conditions []string
	var conditionArgs []any
	for _, column := range columns {
		if op == "=" {
			conditions = append(conditions, fmt.Sprintf("%s <> ?", column))
			conditionArgs = append(conditionArgs, value)

			continue
		}

		fragment, fragmentArgs := whereFragment(column, op, value)
		conditions = append(conditions, fmt.Sprintf("NOT (%s)", fragment))
		conditionArgs = append(conditionArgs, fragmentArgs...)
	}

	query := strings.Join(conditions, " AND ")
	r = r.addWhere(contractsdriver.Where{
		Query: query,
		Args:  conditionArgs,
	}).(*Query)

	return r
}

// whereFragment renders one raw "<column> <operator> ?" condition and the bindings it needs.
//
// An IN or NOT IN operator whose value is a slice expands to one placeholder and one binding per
// element: the expansion gorm's clause builder used to apply here, and the same shape the compiler
// gives the sibling WhereIn entry point. Without it the whole slice is bound to the single
// placeholder, so the list is never expanded and the statement fails at every driver.
//
// An EMPTY slice has no placeholder to expand to: joining zero placeholders would emit the
// malformed "column IN ()" that every driver rejects, so it renders the constants the compiler
// already gives the sibling WhereIn / WhereNotIn - "0 = 1" for an empty IN (matches nothing) and
// "1 = 1" for an empty NOT IN (matches everything), both matching Laravel and binding nothing.
func whereFragment(column string, operator any, value any) (string, []any) {
	if isInOperator(operator) {
		if values, ok := sliceValues(value); ok {
			if len(values) == 0 {
				if isNotInOperator(operator) {
					return "1 = 1", nil
				}

				return "0 = 1", nil
			}

			placeholders := make([]string, len(values))
			for i := range placeholders {
				placeholders[i] = "?"
			}

			return fmt.Sprintf("%s %v (%s)", column, operator, strings.Join(placeholders, ", ")), values
		}
	}

	return fmt.Sprintf("%s %v ?", column, operator), []any{value}
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

func (r *Query) WhereNotNull(column string) contractsorm.Query {
	return r.Where(fmt.Sprintf("%s IS NOT NULL", column))
}

func (r *Query) WhereNull(column string) contractsorm.Query {
	return r.Where(fmt.Sprintf("%s IS NULL", column))
}

func (r *Query) WithoutEvents() contractsorm.Query {
	conditions := r.conditions
	conditions.withoutEvents = true

	return r.setConditions(conditions)
}

func (r *Query) WithoutGlobalScopes(names ...string) contractsorm.Query {
	conditions := r.conditions

	if len(names) == 0 {
		names = []string{"*"}
	}
	conditions.withoutGlobalScopes = append(conditions.withoutGlobalScopes, names...)

	return r.setConditions(conditions)
}

func (r *Query) WithTrashed() contractsorm.Query {
	conditions := r.conditions
	conditions.withTrashed = true

	return r.setConditions(conditions)
}

func (r *Query) OnlyTrashed() contractsorm.Query {
	conditions := r.conditions
	conditions.onlyTrashed = true

	return r.setConditions(conditions)
}

func (r *Query) addGlobalScopes() *Query {
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

	nameToGlobalScopes := modelWithGlobalScopes.GlobalScopes()
	if len(nameToGlobalScopes) == 0 {
		return r
	}

	var globalScopes []func(contractsorm.Query) contractsorm.Query

	names := slices.Sorted(maps.Keys(nameToGlobalScopes))
	for _, name := range names {
		if slices.Contains(r.conditions.withoutGlobalScopes, name) {
			continue
		}

		globalScopes = append(globalScopes, nameToGlobalScopes[name])
	}

	return r.Scopes(globalScopes...).(*Query)
}

func (r *Query) addWhere(where contractsdriver.Where) contractsorm.Query {
	conditions := r.conditions
	conditions.where = deep.Append(conditions.where, where)

	return r.setConditions(conditions)
}

func (r *Query) buildConditions() *Query {
	// The query is no longer assembled into a GORM statement here. GORM used to build one and run
	// it; the compiled path builds from the query's conditions, so the only thing left to do is
	// rebind to the connection a model may declare and hand the query on.
	query, err := r.refreshConnection()
	if err != nil {
		return r.withError(err).(*Query)
	}

	return query
}

func (r *Query) clearConditions() {
	r.conditions = Conditions{}
}

func (r *Query) create(dest any) error {
	var err error
	if dest, err = r.saving(dest); err != nil {
		return err
	}
	if dest, err = r.creating(dest); err != nil {
		return err
	}

	if err := r.execInsert(dest); err != nil {
		return err
	}

	if err := r.created(dest); err != nil {
		return err
	}
	if _, err := r.saved(dest); err != nil {
		return err
	}

	return nil
}

func (r *Query) created(dest any) error {
	if isSlice(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventCreated, r.conditions.model, dest)

	return err
}

func (r *Query) creating(dest any) (any, error) {
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
func (r *Query) event(event contractsorm.EventType, model, dest any) (any, error) {
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
func (r *Query) dispatch(event contractsorm.EventType, model, dest any, instance *Event) error {
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

func (r *Query) deleting(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventDeleting, r.conditions.model, dest)

	return err
}

func (r *Query) deleted(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventDeleted, r.conditions.model, dest)

	return err
}

// withScopesApplied runs the query's scopes and returns a query carrying what they added, with the
// scopes cleared so applying them is not repeated.
//
// Each scope starts from what the previous ones produced, which is how GORM chained them onto one
// statement.
func (r *Query) withScopesApplied() *Query {
	scopes := r.conditions.scopes
	r.conditions.scopes = nil

	for _, apply := range scopes {
		scoped, ok := apply(r).(*Query)
		if !ok {
			continue
		}

		scoped.conditions.scopes = nil
		r = scoped
	}

	return r
}

// withError returns a query carrying a condition that could not be accepted, for whichever statement
// is compiled from it to report.
func (r *Query) withError(err error) contractsorm.Query {
	conditions := r.conditions
	conditions.err = err

	return r.setConditions(conditions)
}

func (r *Query) dest(value any) *Query {
	conditions := r.conditions
	conditions.dest = value

	return r.setConditions(conditions)
}

func (r *Query) forceDeleting(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventForceDeleting, r.conditions.model, dest)

	return err
}

func (r *Query) forceDeleted(dest any) error {
	if !hasID(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventForceDeleted, r.conditions.model, dest)

	return err
}

func (r *Query) getModelConnection() string {
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

func (r *Query) getObserver(dest any) contractsorm.Observer {
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

func (r *Query) new(db *gormio.DB) *Query {
	query := NewQuery(r.ctx, r.config, r.dbConfig, db, r.grammar, r.log, r.modelToObserver, nil, r.telemetryResolver)
	query.conditions = r.conditions.clone()

	return query
}

func (r *Query) refreshConnection() (*Query, error) {
	connection := r.getModelConnection()
	if connection == "" || connection == r.dbConfig.Connection {
		return r, nil
	}

	query, ok := r.queries[connection]
	if !ok {
		var err error
		query, _, err = BuildQuery(r.ctx, r.config, connection, r.log, r.modelToObserver, r.telemetryResolver)
		if err != nil {
			return nil, err
		}

		if r.queries == nil {
			r.queries = make(map[string]*Query)
		}
		r.queries[connection] = query
	}

	query.conditions = r.conditions

	return query, nil
}

func (r *Query) restored(dest any) error {
	_, err := r.event(contractsorm.EventRestored, r.conditions.model, dest)

	return err
}

func (r *Query) restoring(dest any) error {
	_, err := r.event(contractsorm.EventRestoring, r.conditions.model, dest)

	return err
}

func (r *Query) retrieved(dest any) error {
	if err := r.applyEagerLoads(dest); err != nil {
		return err
	}
	if isSlice(dest) {
		return nil
	}

	_, err := r.event(contractsorm.EventRetrieved, r.conditions.model, dest)

	return err
}

// save writes a model, inserting it when it carries no key and overwriting the row it names when it
// does.
//
// GORM also recreated the row when an update matched none, by upserting on the primary key. That is
// deliberately not carried over: it made saving a soft-deleted model resurrect it and overwrite every
// column from the value passed in, which is not what "updates value in a database" promises, and
// creating a row is what Create and FirstOrCreate are for. A save whose key matches nothing writes
// nothing.
func (r *Query) save(value any) error {
	if database.GetID(value) == nil {
		return r.execInsert(value)
	}

	assignments, err := r.saveAssignments(value)
	if err != nil {
		return err
	}
	if len(assignments) == 0 {
		return nil
	}

	statement, bindings, err := r.compileUpdateScoped(assignments, true)
	if err != nil {
		return err
	}

	_, err = r.execStatement(statement, bindings)

	return err
}

func (r *Query) saved(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventSaved, r.conditions.model, dest)
}

func (r *Query) saving(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventSaving, r.conditions.model, dest)
}

// setConditions records the query's conditions and returns the query itself: a fluent call changes
// the receiver, the way Laravel's builder does, rather than producing a new query. Callers that need
// the original untouched clone() first.
func (r *Query) setConditions(conditions Conditions) *Query {
	r.conditions = conditions

	return r
}

// clone returns a query that shares the receiver's connection and scope but owns its conditions, so
// changing the copy leaves the original alone. It is the isolation primitive the execution paths use:
// dest(), the global-scope folding and the temporary clauses a compiled statement needs all start
// from a clone, which is what the copy-on-set semantics used to give them for free.
func (r *Query) clone() *Query {
	query := *r
	query.conditions = r.conditions.clone()

	return &query
}

func (r *Query) updating(dest any) (any, error) {
	if isSlice(dest) {
		return dest, nil
	}

	return r.event(contractsorm.EventUpdating, r.conditions.model, dest)
}

func (r *Query) updated(dest any) (any, error) {
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
func (r *Query) update(values any) (*contractsdb.Result, error) {
	return r.updateScoped(values, false)
}

// updateScoped compiles and runs an update, reporting how many rows it changed. A soft delete scopes
// the statement the way a delete does rather than the way an update does.
func (r *Query) updateScoped(values any, byDest bool) (*contractsdb.Result, error) {
	if hasStringColumns(r.conditions.selectItems) && len(r.conditions.omit) > 0 {
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
		r.assignUpdatedValues(assignments)
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
func (r *Query) assignUpdatedValues(assignments map[string]any) {
	model := r.conditions.model
	if model == nil || len(assignments) == 0 {
		return
	}

	// Only a pointer leads back to the caller's model; a value with no address has no receiver to
	// write to.
	reflected := reflect.ValueOf(model)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return
	}

	schema, err := parseSchema(r.instance, model)
	if err != nil {
		return
	}

	for column, value := range assignments {
		field, ok := schema.FieldsByDBName[column]
		if !ok {
			continue
		}

		_ = field.Set(r.ctx, reflected, value)
	}
}

// countColumns returns the column a count reduces: the one column the query selects, or every row.
//
// Counting a single selected column is what the caller asked for by selecting it. With DISTINCT it
// becomes count(distinct column) rather than a leading DISTINCT over count(*), which would count every
// row instead.
func countColumns(query *Query) []any {
	// Count the string columns in selectItems
	var stringColumns []string
	for _, item := range query.conditions.selectItems {
		if col, ok := item.(string); ok {
			stringColumns = append(stringColumns, col)
		}
	}
	if len(stringColumns) == 1 {
		return []any{stringColumns[0]}
	}

	return []any{"*"}
}

func buildSelectForCount(query *Query) *Query {
	conditions := query.conditions

	// A single select column that carries a rename ("name as n") cannot be counted as it stands — the
	// count would come out count(name as n) — so the alias is trimmed back to its bare column first.
	// This shape appears when Paginate builds its count.
	// With several select columns there is nothing to trim: the count ignores them and reduces the
	// whole row with *.
	// For example: Select("name as n").Count() counts the name column, while Select("name", "age as a").Count() counts *.
	columns := selectStringColumns(conditions.selectItems)
	if len(columns) == 1 && str.Of(columns[0]).Trim().Contains(" ") {
		conditions.selectItems = []any{str.Of(columns[0]).Split(" ")[0]}
	}

	// The count a paginator runs never orders or bounds the rows it counts: Laravel builds it on a
	// clone without the orders, limit and offset (Builder::runPaginationCountQuery), so an ordered or
	// limited query still counts every matching row.
	conditions.order = nil
	conditions.limit = nil
	conditions.offset = nil

	return query.setConditions(conditions).addGlobalScopes().buildConditions()
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
	return database.GetID(dest) != nil
}

// hasSelectColumns reports whether conditions has any selections (either column names or raw expressions).
func hasSelectColumns(conditions Conditions) bool {
	return len(conditions.selectItems) > 0
}
