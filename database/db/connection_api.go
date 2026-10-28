package db

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"time"

	"github.com/gradii/framework/contracts/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

func (r *DB) Listen(callback func(db.QueryExecuted)) {
	r.state.hooks.mu.Lock()
	defer r.state.hooks.mu.Unlock()
	r.state.hooks.listeners = append(r.state.hooks.listeners, callback)
}
func (r *DB) BeforeExecuting(callback func(context.Context, string, []any) error) {
	r.state.hooks.mu.Lock()
	defer r.state.hooks.mu.Unlock()
	r.state.hooks.before = append(r.state.hooks.before, callback)
}
func (r *DB) WhenQueryingForLongerThan(duration time.Duration, callback func(db.QueryExecuted)) {
	r.state.hooks.mu.Lock()
	defer r.state.hooks.mu.Unlock()
	r.state.hooks.durations = append(r.state.hooks.durations, queryDurationHandler{duration: duration, callback: callback})
}
func (r *DB) TotalQueryDuration() time.Duration {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return r.state.total
}
func (r *DB) ResetTotalQueryDuration() {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.total = 0
}
func (r *DB) AllowQueryDurationHandlersToRunAgain() {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.fired = make(map[int]bool)
}
func (r *DB) Sticky(enabled bool) db.DB {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.sticky = enabled
	return r
}
func (r *DB) UseWriteConnectionWhenReading(enabled bool) db.DB {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.forceWriter = enabled
	return r
}
func (r *DB) HasModifiedRecords() bool {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return r.state.modified
}
func (r *DB) RecordsHaveBeenModified(modified ...bool) {
	value := true
	if len(modified) > 0 {
		value = modified[0]
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if !r.state.modified {
		r.state.modified = value
	}
}
func (r *DB) ForgetRecordModificationState() {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.modified = false
}
func (r *DB) EnableQueryLog()  { r.state.mu.Lock(); defer r.state.mu.Unlock(); r.state.logging = true }
func (r *DB) DisableQueryLog() { r.state.mu.Lock(); defer r.state.mu.Unlock(); r.state.logging = false }
func (r *DB) FlushQueryLog()   { r.state.mu.Lock(); defer r.state.mu.Unlock(); r.state.queryLog = nil }
func (r *DB) GetQueryLog() []db.QueryExecuted {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	events := append([]db.QueryExecuted(nil), r.state.queryLog...)
	for i := range events {
		events[i].Bindings = copyBindings(events[i].Bindings)
	}
	return events
}

// Pretend receives an isolated session sharing the connection configuration and hooks.
// Queries issued through the callback's handle are recorded without reaching the driver.
func (r *DB) Pretend(callback func(db.DB) error) ([]db.QueryExecuted, error) {
	child := r.WithContext(r.ctx).(*DB)
	// Resolve version-dependent compilation before entering the no-execution scope.
	if child.state.serverVersion != nil {
		version, err := child.state.serverVersion(r.ctx)
		child.state.serverVersion = func(context.Context) (string, error) { return version, err }
	}
	child.state.pretending = true
	child.state.logging = true
	err := callback(child)
	return child.GetQueryLog(), err
}
func (r *DB) Name() string       { return r.state.name }
func (r *DB) DriverName() string { return r.state.driver }
func (r *DB) ServerVersion() (string, error) {
	r.state.mu.Lock()
	resolve := r.state.serverVersion
	r.state.mu.Unlock()
	if resolve == nil {
		return "", errors.DatabaseConnectionIsNil
	}
	return resolve(r.ctx)
}
func (r *DB) Escape(value any, binary ...bool) (string, error) {
	isBinary := len(binary) > 0 && binary[0]
	dialect, err := databasedriver.ResolveDialect(r.driver)
	if err != nil {
		return "", err
	}
	return databasedriver.EscapeWithDialect(dialect, r.DriverName(), value, isBinary)
}
func (r *DB) Purge(connection ...string) error {
	name := firstString(connection, r.Name())
	err := databasedriver.PurgeConnection(name)
	r.queries.mu.Lock()
	delete(r.queries.m, name)
	r.queries.mu.Unlock()
	return err
}
func (r *DB) Disconnect(connection ...string) error { return r.Purge(connection...) }
func (r *DB) Reconnect(connection ...string) (db.DB, error) {
	name := firstString(connection, r.Name())
	if err := r.Purge(name); err != nil {
		return nil, err
	}
	fresh, err := BuildDB(r.ctx, r.config, r.logger.Log(), name, r.telemetryResolver)
	if err != nil {
		return nil, err
	}
	fresh.queries = r.queries
	fresh.state.hooks = r.state.hooks
	r.queries.mu.Lock()
	r.queries.m[name] = fresh
	r.queries.mu.Unlock()
	return fresh, nil
}

func (r *Tx) Query() db.Query {
	query := r.Table("").(*Query)
	query.allowTableless = true
	return query
}
func (r *Tx) executionBuilder(writer bool) (db.CommonBuilder, error) {
	if r.txBuilder != nil {
		return r.transactionalBuilder(), nil
	}
	if writer {
		return r.writeBuilder()
	}
	return r.readBuilder()
}
func (r *Tx) SelectOne(dest any, statement string, args ...any) error {
	builder, err := r.executionBuilder(false)
	if err != nil {
		return err
	}
	statement = querybuilder.ReplacePlaceholders(statement, r.driverName)
	begin := carbon.Now()
	err = builder.GetContext(r.ctx, dest, statement, args...)
	count := int64(1)
	if errors.Is(err, sql.ErrNoRows) {
		clearDestination(dest)
		err = nil
		count = 0
	}
	if err != nil {
		count = -1
	}
	r.logger.Trace(r.ctx, begin, builder.Explain(statement, args...), count, err)
	return err
}
func (r *Tx) Scalar(statement string, args ...any) (any, error) {
	var row map[string]any
	if err := r.SelectOne(&row, statement, args...); err != nil {
		return nil, err
	}
	if len(row) > 1 {
		return nil, errors.DatabaseMultipleColumnsSelected
	}
	for _, value := range row {
		return value, nil
	}
	return nil, nil
}
func (r *Tx) SelectFromWriteConnection(dest any, statement string, args ...any) error {
	builder, err := r.executionBuilder(true)
	if err != nil {
		return err
	}
	statement = querybuilder.ReplacePlaceholders(statement, r.driverName)
	begin := carbon.Now()
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer")
	}
	rows := int64(1)
	if target.Elem().Kind() == reflect.Slice {
		err = builder.SelectContext(r.ctx, dest, statement, args...)
		rows = int64(target.Elem().Len())
	} else {
		err = builder.GetContext(r.ctx, dest, statement, args...)
	}
	if err != nil {
		rows = -1
	}
	r.logger.Trace(r.ctx, begin, builder.Explain(statement, args...), rows, err)
	return err
}
func (r *Tx) SelectResultSets(statement string, args ...any) (result [][]map[string]any, err error) {
	builder, err := r.executionBuilder(false)
	if err != nil {
		return nil, err
	}
	statement = querybuilder.ReplacePlaceholders(statement, r.driverName)
	begin := carbon.Now()
	rows, err := builder.QueryxContext(r.ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	result = [][]map[string]any{}
	count := int64(0)
	if rows != nil {
		defer func() { err = errors.Join(err, rows.Close()) }()
		for {
			set := []map[string]any{}
			for rows.Next() {
				row := map[string]any{}
				if err := rows.MapScan(row); err != nil {
					return nil, err
				}
				copyMapBytes(row)
				set = append(set, row)
				count++
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
			result = append(result, set)
			if !rows.NextResultSet() {
				break
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	r.logger.Trace(r.ctx, begin, builder.Explain(statement, args...), count, nil)
	return result, nil
}
func (r *Tx) AffectingStatement(statement string, args ...any) (*db.Result, error) {
	return r.exec(statement, args...)
}
func (r *Tx) Unprepared(statement string) error { return r.Statement(statement) }
