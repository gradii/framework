package db

import (
	"context"
	databasesql "database/sql"
	"fmt"
	"reflect"
	"sync"

	"github.com/gradii/framework/contracts/config"
	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	"github.com/gradii/framework/contracts/log"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	databasedriver "github.com/gradii/framework/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
	instrumentationdatabase "github.com/gradii/framework/telemetry/instrumentation/database"
)

type queriesCache struct {
	mu sync.RWMutex
	m  map[string]contractsdb.DB
}

type DB struct {
	state *connectionState
	contractsdb.Tx
	config            config.Config
	ctx               context.Context
	connection        *databasedriver.Connection
	driver            contractsdriver.Driver
	instrument        *instrumentationdatabase.Instrument
	logger            contractslogger.Logger
	queries           *queriesCache
	telemetryResolver contractstelemetry.Resolver
}

func NewDB(ctx context.Context, config config.Config, driver contractsdriver.Driver, logger contractslogger.Logger, connection *databasedriver.Connection, telemetryResolver contractstelemetry.Resolver) (*DB, error) {
	var instrument *instrumentationdatabase.Instrument
	if connection != nil {
		instrument = connection.Instrument()
	}

	if versioned, ok := driver.(contractsdriver.DriverWithServerVersion); ok && connection != nil && connection.SupportsServerVersion() {
		versioned.SetServerVersion(func() (string, error) { return connection.ServerVersion(ctx) })
	}
	tx := NewTx(ctx, config, driver, logger, connection, nil, &[]TxLog{}, instrument)
	if tx.initErr != nil {
		return nil, tx.initErr
	}
	if config != nil && connection != nil {
		tx.state.sticky = config.GetBool("database.connections."+connection.Name()+".sticky", false)
		tx.state.useUpsertAlias = config.GetBool("database.connections."+connection.Name()+".use_upsert_alias", false)
	}
	if connection != nil {
		tx.state.serverVersion = connection.ServerVersion
	}
	return &DB{
		Tx:                tx,
		state:             tx.state,
		ctx:               ctx,
		config:            config,
		driver:            driver,
		connection:        connection,
		instrument:        instrument,
		logger:            logger,
		queries:           &queriesCache{m: make(map[string]contractsdb.DB)},
		telemetryResolver: telemetryResolver,
	}, nil
}

func BuildDB(ctx context.Context, config config.Config, log log.Log, connection string, telemetryResolver contractstelemetry.Resolver) (*DB, error) {
	driverCallback, exist := config.Get(fmt.Sprintf("database.connections.%s.via", connection)).(func() (contractsdriver.Driver, error))
	if !exist {
		return nil, errors.DatabaseConfigNotFound
	}

	driver, err := driverCallback()
	if err != nil {
		return nil, err
	}

	pool := driver.Pool()
	logger := NewLogger(config, log)
	dialect, err := databasedriver.ResolveDialect(driver)
	if err != nil {
		return nil, err
	}
	conn, err := databasedriver.Open(config, logger, pool, connection, telemetryResolver, dialect)
	if err != nil {
		return nil, err
	}

	return NewDB(ctx, config, driver, logger, conn, telemetryResolver)
}

func (r *DB) BeginTransaction() (contractsdb.Tx, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if r.Tx != nil {
		if concrete, ok := r.Tx.(*Tx); ok {
			if err := concrete.beforeStartingTransaction(); err != nil {
				return nil, err
			}
		}
	}
	if r.state.pretend() {
		tx := NewTx(r.ctx, r.config, r.driver, r.logger, nil, &pretendTxBuilder{}, &[]TxLog{}, nil)
		tx.state = r.state
		return tx, nil
	}
	if r.connection == nil {
		return nil, errors.DatabaseConnectionIsNil
	}

	// The driver and its first writer are assumed below. A *DB is only ever built by NewDB, whose
	// NewTx already indexes driver.Pool().Writers[0], and BuildDB dereferences driver before that,
	// so a nil driver or an empty Writers slice cannot reach this method through a construction
	// path. Report the same declared error rather than panicking should a hand-assembled DB do so.
	if r.driver == nil || len(r.driver.Pool().Writers) == 0 {
		return nil, errors.DatabaseConnectionIsNil
	}

	// A version-dependent query must not borrow a second connection after BEGIN
	// has pinned the only writer in a pool configured with max_open_conns = 1.
	_, _ = r.ServerVersion()

	driverName := r.driver.Pool().Writers[0].Driver
	options, err := databasedriver.TransactionOptions(r.config, r.Name())
	if err != nil {
		return nil, err
	}
	txBuilder, err := NewTxBuilderWithContext(r.ctx, r.connection.Writer(), driverName, options)
	if err != nil {
		return nil, err
	}

	tx := NewTx(r.ctx, r.config, r.driver, r.logger, nil, txBuilder, &[]TxLog{}, r.instrument)
	tx.state = r.state
	return tx, nil
}

func (r *DB) Connection(name string) contractsdb.DB {
	if name == "" {
		name = r.config.GetString("database.default")
	}

	r.queries.mu.RLock()
	if db, ok := r.queries.m[name]; ok {
		r.queries.mu.RUnlock()
		return db
	}
	r.queries.mu.RUnlock()

	r.queries.mu.Lock()
	defer r.queries.mu.Unlock()
	if db, ok := r.queries.m[name]; ok {
		return db
	}

	db, err := BuildDB(r.ctx, r.config, r.logger.Log(), name, r.telemetryResolver)
	if err != nil {
		r.logger.Panicf(r.ctx, err.Error())
		return nil
	}

	r.queries.m[name] = db
	db.queries = r.queries
	if r.state != nil {
		db.state.hooks = r.state.hooks
	}

	return db
}

func (r *DB) Transaction(callback func(contractsdb.Tx) error, attempts ...int) error {
	return runTransaction(r.BeginTransaction, callback, attempts)
}

func (r *DB) WithContext(ctx context.Context) contractsdb.DB {
	db, err := NewDB(ctx, r.config, r.driver, r.logger, r.connection, r.telemetryResolver)
	if err != nil {
		r.logger.Panicf(r.ctx, err.Error())
		return nil
	}
	if r.state != nil {
		db.state = r.state.newContext()
		db.Tx.(*Tx).state = db.state
	}
	return db
}

type Tx struct {
	pinned       *leasedConnectionState
	transactions *transactionManager
	record       *transactionRecord
	state        *connectionState
	ctx          context.Context
	config       config.Config
	driver       contractsdriver.Driver
	grammar      contractsdriver.Grammar
	queryGrammar querybuilder.Grammar
	components   databasedriver.QueryComponents
	initErr      error
	instrument   *instrumentationdatabase.Instrument
	logger       contractslogger.Logger
	txBuilder    contractsdb.TxBuilder
	connection   *databasedriver.Connection
	txLogs       *[]TxLog
	driverName   string
}

func NewTx(
	ctx context.Context,
	config config.Config,
	driver contractsdriver.Driver,
	logger contractslogger.Logger,
	connection *databasedriver.Connection,
	txBuilder contractsdb.TxBuilder,
	txLogs *[]TxLog,
	instrument *instrumentationdatabase.Instrument,
) *Tx {
	if txBuilder != nil && instrument != nil {
		txBuilder = instrumentationdatabase.WrapTxBuilder(txBuilder, instrument)
	}

	poolWriter := driver.Pool().Writers[0]
	dialect, initErr := databasedriver.ResolveDialect(driver)
	options := contractsdriver.GrammarOptions{Driver: poolWriter.Driver, Prefix: poolWriter.Prefix}
	if connection != nil && connection.SupportsServerVersion() {
		options.ServerVersion = func() (string, error) { return connection.ServerVersion(ctx) }
	}
	components, componentErr := databasedriver.ResolveQueryComponents(dialect, options)
	if connection != nil && connection.SupportsServerVersion() {
		components.VersionSource = connection.ServerVersion
	}
	if initErr == nil {
		initErr = componentErr
	}
	if initErr == nil && dialect == nil && logger != nil {
		if err := querybuilder.UnrecognisedDriverWarning(poolWriter.Driver); err != nil {
			logger.Warningf(ctx, "%s", err)
		}
	}

	tx := &Tx{
		state:        newConnectionState(poolWriter.Connection, poolWriter.Driver),
		ctx:          ctx,
		config:       config,
		driver:       driver,
		driverName:   poolWriter.Driver,
		queryGrammar: components.Grammar,
		components:   components,
		initErr:      initErr,
		connection:   connection,
		grammar:      driver.Grammar(),
		instrument:   instrument,
		logger:       logger,
		txBuilder:    txBuilder,
		txLogs:       txLogs,
	}
	if txBuilder != nil {
		tx.transactions, tx.record = newTransactionManager(txBuilder)
	}
	return tx
}

func (r *Tx) Commit() error {
	if r.txBuilder == nil {
		return errors.DatabaseTransactionNotStarted
	}
	if r.transactions != nil {
		committed, callbacks, err := r.transactions.commit(r.record)
		if err != nil {
			return err
		}
		if committed && r.txLogs != nil {
			for _, log := range *r.txLogs {
				r.logger.Trace(log.ctx, log.begin, log.sql, log.rowsAffected, log.err)
			}
			*r.txLogs = nil
		}
		return runTransactionCallbacks(callbacks)
	}

	if err := r.txBuilder.Commit(); err != nil {
		return err
	}

	for _, log := range *r.txLogs {
		r.logger.Trace(log.ctx, log.begin, log.sql, log.rowsAffected, log.err)
	}

	return nil
}

func (r *Tx) Delete(sql string, args ...any) (*contractsdb.Result, error) {
	return r.exec(sql, args...)
}

func (r *Tx) Insert(sql string, args ...any) (*contractsdb.Result, error) {
	return r.execOperation(sql, statementOperation, args...)
}

func (r *Tx) Rollback() error {
	if r.txBuilder == nil {
		return errors.DatabaseTransactionNotStarted
	}
	if r.transactions != nil {
		return r.RollbackTo(r.record.level - 1)
	}

	return r.txBuilder.Rollback()
}

func (r *Tx) Select(dest any, sql string, args ...any) error {
	sql = querybuilder.ReplacePlaceholders(sql, r.driverName)
	var (
		builder contractsdb.CommonBuilder
		realSql string
		err     error
	)

	if r.txBuilder != nil {
		builder = r.transactionalBuilder()
	} else {
		builder, err = r.readBuilder()
		if err != nil {
			return err
		}
	}

	realSql = builder.Explain(sql, args...)

	destValue := reflect.Indirect(reflect.ValueOf(dest))

	rowsAffected := int64(1)
	if destValue.Kind() == reflect.Slice {
		if err = builder.SelectContext(r.ctx, dest, sql, args...); err != nil {
			r.logger.Trace(r.ctx, carbon.Now(), realSql, -1, err)

			return err
		}

		rowsAffected = int64(destValue.Len())
	} else {
		if err = builder.GetContext(r.ctx, dest, sql, args...); err != nil {
			r.logger.Trace(r.ctx, carbon.Now(), realSql, -1, err)

			return err
		}
	}

	r.logger.Trace(r.ctx, carbon.Now(), realSql, rowsAffected, nil)

	return nil
}

func (r *Tx) Statement(sql string, args ...any) error {
	_, err := r.execOperation(sql, statementOperation, args...)

	return err
}

func (r *Tx) StatementContext(ctx context.Context, statement string, args ...any) error {
	scoped := *r
	scoped.ctx = ctx
	return scoped.Statement(statement, args...)
}

func (r *Tx) Table(name string) contractsdb.Query {
	if r.initErr != nil {
		q := NewQuery(r.ctx, nil, nil, querybuilder.NewBaseGrammar(""), r.driverName, r.logger, name, nil)
		q.err = r.initErr
		q.builder.SetError(r.initErr)
		return q
	}
	ctx := instrumentationdatabase.ContextWithTable(r.ctx, name)

	if r.txBuilder != nil {
		builder := r.transactionalBuilder()
		return NewQuery(ctx, builder, builder, r.queryGrammar, r.driverName, r.logger, name, r.txLogs, r.components)
	}

	readBuilder, err := r.readBuilder()
	if err != nil {
		r.logger.Panicf(r.ctx, err.Error())
		return nil
	}

	writeBuilder, err := r.writeBuilder()
	if err != nil {
		r.logger.Panicf(r.ctx, err.Error())
		return nil
	}

	return NewQuery(ctx, readBuilder, writeBuilder, r.queryGrammar, r.driverName, r.logger, name, nil, r.components)
}

func (r *Tx) Update(sql string, args ...any) (*contractsdb.Result, error) {
	return r.exec(sql, args...)
}

func (r *Tx) exec(sql string, args ...any) (*contractsdb.Result, error) {
	return r.execOperation(sql, affectingOperation, args...)
}

func (r *Tx) execOperation(sql string, operation int, args ...any) (*contractsdb.Result, error) {
	sql = querybuilder.ReplacePlaceholders(sql, r.driverName)
	var (
		builder contractsdb.CommonBuilder
		realSql string
		result  databasesql.Result
		err     error
	)

	if r.txBuilder != nil {
		builder = r.transactionalBuilder()
	} else {
		builder, err = r.writeBuilder()
		if err != nil {
			return nil, err
		}
	}

	realSql = builder.Explain(sql, args...)
	result, err = builder.ExecContext(operationContext(r.ctx, builder, operation), sql, args...)
	if err != nil {
		r.logger.Trace(r.ctx, carbon.Now(), realSql, -1, err)
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		r.logger.Trace(r.ctx, carbon.Now(), realSql, -1, err)
		return nil, err
	}

	r.logger.Trace(r.ctx, carbon.Now(), realSql, rowsAffected, nil)

	return &contractsdb.Result{RowsAffected: rowsAffected}, nil
}

// readBuilder builds the handle for statements that only read rows. It executes on the
// connection's reader pool, so a read sees the replica rather than the writer. A connection
// opened without a database name has no pool to build against, so it is reported as the
// declared error rather than dereferenced.
func (r *Tx) readBuilder() (contractsdb.CommonBuilder, error) {
	if r.state.pretend() {
		return observeBuilder(nil, r.state, true, nil), nil
	}
	builder, err := r.rawBuilder(false)
	if err != nil {
		return nil, err
	}
	return observeBuilder(builder, r.state, r.pinned == nil, func() (contractsdb.CommonBuilder, error) { return r.rawBuilder(true) }), nil
}

func (r *Tx) writeBuilder() (contractsdb.CommonBuilder, error) {
	if r.state.pretend() {
		return observeBuilder(nil, r.state, false, nil), nil
	}
	builder, err := r.rawBuilder(true)
	if err != nil {
		return nil, err
	}
	return observeBuilder(builder, r.state, false, nil), nil
}

func (r *Tx) rawBuilder(writer bool) (contractsdb.CommonBuilder, error) {
	if r.pinned != nil {
		return &connectionBuilder{Conn: r.pinned.connection, lease: r.pinned}, nil
	}
	if r.connection == nil {
		return nil, errors.DatabaseConnectionIsNil
	}
	pool := r.connection.Reader()
	if writer {
		pool = r.connection.Writer()
	}
	builder, err := NewBuilder(pool, r.driverName)
	if err != nil {
		return nil, err
	}
	if r.instrument != nil {
		return instrumentationdatabase.WrapBuilder(builder, r.instrument), nil
	}
	return builder, nil
}
