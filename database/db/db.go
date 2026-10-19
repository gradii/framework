package db

import (
	"context"
	databasesql "database/sql"
	"fmt"
	"reflect"
	"sync"

	"github.com/goravel/framework/contracts/config"
	contractsdb "github.com/goravel/framework/contracts/database/db"
	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractslogger "github.com/goravel/framework/contracts/database/logger"
	"github.com/goravel/framework/contracts/log"
	contractstelemetry "github.com/goravel/framework/contracts/telemetry"
	databasedriver "github.com/goravel/framework/database/driver"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
	instrumentationdatabase "github.com/goravel/framework/telemetry/instrumentation/database"
)

type queriesCache struct {
	mu sync.RWMutex
	m  map[string]contractsdb.DB
}

type DB struct {
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

	return &DB{
		Tx:                NewTx(ctx, driver, logger, connection, nil, &[]TxLog{}, instrument),
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
	conn, err := databasedriver.Open(config, logger, pool, connection, telemetryResolver)
	if err != nil {
		return nil, err
	}

	return NewDB(ctx, config, driver, logger, conn, telemetryResolver)
}

func (r *DB) BeginTransaction() (contractsdb.Tx, error) {
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

	driverName := r.driver.Pool().Writers[0].Driver
	txBuilder, err := NewTxBuilder(r.connection.Writer(), driverName)
	if err != nil {
		return nil, err
	}

	return NewTx(r.ctx, r.driver, r.logger, nil, txBuilder, &[]TxLog{}, r.instrument), nil
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

	return db
}

func (r *DB) Transaction(callback func(tx contractsdb.Tx) error) (err error) {
	tx, err := r.BeginTransaction()
	if err != nil {
		return err
	}

	defer func() {
		if re := recover(); re != nil {
			err = fmt.Errorf("panic: %v", re)
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
		}
	}()

	if err = callback(tx); err != nil {
		if err := tx.Rollback(); err != nil {
			return err
		}

		return err
	}

	return tx.Commit()
}

func (r *DB) WithContext(ctx context.Context) contractsdb.DB {
	db, err := NewDB(ctx, r.config, r.driver, r.logger, r.connection, r.telemetryResolver)
	if err != nil {
		r.logger.Panicf(r.ctx, err.Error())
		return nil
	}
	return db
}

type Tx struct {
	ctx          context.Context
	grammar      contractsdriver.Grammar
	queryGrammar querybuilder.Grammar
	instrument   *instrumentationdatabase.Instrument
	logger       contractslogger.Logger
	txBuilder    contractsdb.TxBuilder
	connection   *databasedriver.Connection
	txLogs       *[]TxLog
	driverName   string
}

func NewTx(
	ctx context.Context,
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
	// An unrecognised driver is deliberately allowed (a module may bring its own database/sql
	// name); it warns once, naming the driver and the recognised set, rather than failing.
	queryGrammar, known := querybuilder.NewGrammar(poolWriter.Driver, poolWriter.Prefix)
	if !known && logger != nil {
		if err := querybuilder.UnrecognisedDriverWarning(poolWriter.Driver); err != nil {
			logger.Warningf(ctx, "%s", err)
		}
	}

	return &Tx{
		ctx:          ctx,
		driverName:   poolWriter.Driver,
		queryGrammar: queryGrammar,
		connection:   connection,
		grammar:      driver.Grammar(),
		instrument:   instrument,
		logger:       logger,
		txBuilder:    txBuilder,
		txLogs:       txLogs,
	}
}

func (r *Tx) Commit() error {
	if r.txBuilder == nil {
		return errors.DatabaseTransactionNotStarted
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
	return r.exec(sql, args...)
}

func (r *Tx) Rollback() error {
	if r.txBuilder == nil {
		return errors.DatabaseTransactionNotStarted
	}

	return r.txBuilder.Rollback()
}

func (r *Tx) Select(dest any, sql string, args ...any) error {
	var (
		builder contractsdb.CommonBuilder
		realSql string
		err     error
	)

	if r.txBuilder != nil {
		builder = r.txBuilder
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
	_, err := r.exec(sql, args...)

	return err
}

func (r *Tx) Table(name string) contractsdb.Query {
	ctx := instrumentationdatabase.ContextWithTable(r.ctx, name)

	if r.txBuilder != nil {
		return NewQuery(ctx, r.txBuilder, r.txBuilder, r.queryGrammar, r.driverName, r.logger, name, r.txLogs)
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

	return NewQuery(ctx, readBuilder, writeBuilder, r.queryGrammar, r.driverName, r.logger, name, nil)
}

func (r *Tx) Update(sql string, args ...any) (*contractsdb.Result, error) {
	return r.exec(sql, args...)
}

func (r *Tx) exec(sql string, args ...any) (*contractsdb.Result, error) {
	var (
		builder contractsdb.CommonBuilder
		realSql string
		result  databasesql.Result
		err     error
	)

	if r.txBuilder != nil {
		builder = r.txBuilder
	} else {
		builder, err = r.writeBuilder()
		if err != nil {
			return nil, err
		}
	}

	realSql = builder.Explain(sql, args...)
	result, err = builder.ExecContext(r.ctx, sql, args...)
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
func (r *Tx) readBuilder() (contractsdb.Builder, error) {
	if r.connection == nil {
		return nil, errors.DatabaseConnectionIsNil
	}

	builder, err := NewBuilder(r.connection.Reader(), r.driverName)
	if err != nil {
		return nil, err
	}

	if r.instrument != nil {
		return instrumentationdatabase.WrapBuilder(builder, r.instrument), nil
	}

	return builder, nil
}

// writeBuilder builds the handle for statements that change rows. Inserts, updates and deletes
// stay on the writer pool, and so does a read that has to observe one of them, such as an
// inserted id being read back. A connection opened without a database name has no pool to build
// against, so it is reported as the declared error rather than dereferenced.
func (r *Tx) writeBuilder() (contractsdb.Builder, error) {
	if r.connection == nil {
		return nil, errors.DatabaseConnectionIsNil
	}

	builder, err := NewBuilder(r.connection.Writer(), r.driverName)
	if err != nil {
		return nil, err
	}

	if r.instrument != nil {
		return instrumentationdatabase.WrapBuilder(builder, r.instrument), nil
	}

	return builder, nil
}
