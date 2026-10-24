package db

import (
	"context"
	"database/sql"
	"reflect"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/database/db"
	dbdriver "github.com/goravel/framework/database/driver"
	"github.com/jmoiron/sqlx"
)

type queryDurationHandler struct {
	duration time.Duration
	callback func(db.QueryExecuted)
}
type connectionHooks struct {
	beforeTransactions []func(db.Tx) error
	mu                 sync.RWMutex
	before             []func(context.Context, string, []any) error
	listeners          []func(db.QueryExecuted)
	durations          []queryDurationHandler
}
type connectionState struct {
	mu             sync.Mutex
	name           string
	driver         string
	hooks          *connectionHooks
	sticky         bool
	modified       bool
	forceWriter    bool
	pretending     bool
	logging        bool
	queryLog       []db.QueryExecuted
	total          time.Duration
	fired          map[int]bool
	useUpsertAlias bool
	serverVersion  func(context.Context) (string, error)
}

func newConnectionState(name, driver string) *connectionState {
	return &connectionState{name: name, driver: driver, hooks: &connectionHooks{}, fired: make(map[int]bool)}
}
func (s *connectionState) newContext() *connectionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &connectionState{name: s.name, driver: s.driver, hooks: s.hooks, sticky: s.sticky,
		useUpsertAlias: s.useUpsertAlias, serverVersion: s.serverVersion, fired: make(map[int]bool)}
}
func (s *connectionState) useWriter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forceWriter || (s.sticky && s.modified)
}
func (s *connectionState) pretend() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pretending
}
func copyBindings(bindings []any) []any {
	result := append([]any(nil), bindings...)
	for i, value := range result {
		if value, ok := value.([]byte); ok {
			result[i] = append([]byte(nil), value...)
		}
	}
	return result
}
func (s *connectionState) beforeQuery(ctx context.Context, statement string, bindings []any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.hooks.mu.RLock()
	callbacks := append([]func(context.Context, string, []any) error(nil), s.hooks.before...)
	s.hooks.mu.RUnlock()
	for _, callback := range callbacks {
		if err := callback(ctx, statement, copyBindings(bindings)); err != nil {
			return err
		}
	}
	return nil
}
func (s *connectionState) completed(statement string, bindings []any, begin time.Time, modified bool) {
	event := db.QueryExecuted{SQL: statement, Bindings: copyBindings(bindings), Duration: time.Since(begin), ConnectionName: s.name}
	s.hooks.mu.RLock()
	listeners := append([]func(db.QueryExecuted){}, s.hooks.listeners...)
	handlers := append([]queryDurationHandler(nil), s.hooks.durations...)
	s.hooks.mu.RUnlock()
	s.mu.Lock()
	s.total += event.Duration
	if modified && !s.pretending {
		s.modified = true
	}
	if s.logging || s.pretending {
		s.queryLog = append(s.queryLog, event)
	}
	var callbacks []func(db.QueryExecuted)
	for i, handler := range handlers {
		if !s.fired[i] && s.total > handler.duration {
			s.fired[i] = true
			callbacks = append(callbacks, handler.callback)
		}
	}
	s.mu.Unlock()
	for _, callback := range listeners {
		callback(event)
	}
	for _, callback := range callbacks {
		callback(event)
	}
}

type observedBuilder struct {
	db.CommonBuilder
	state  *connectionState
	read   bool
	writer func() (db.CommonBuilder, error)
}

func observeBuilder(builder db.CommonBuilder, state *connectionState, read bool, writer func() (db.CommonBuilder, error)) db.CommonBuilder {
	if state == nil {
		return builder
	}
	return &observedBuilder{CommonBuilder: builder, state: state, read: read, writer: writer}
}
func (b *observedBuilder) target() (db.CommonBuilder, error) {
	if b.read && b.state.useWriter() && b.writer != nil {
		return b.writer()
	}
	return b.CommonBuilder, nil
}
func (b *observedBuilder) Explain(statement string, args ...any) string {
	if b.CommonBuilder == nil {
		return Explain(statement, args...)
	}
	return b.CommonBuilder.Explain(statement, args...)
}

type queryOperationKey struct{}

type queryOperation struct {
	kind    int
	context context.Context
}

const (
	affectingOperation = iota
	statementOperation
	returningWriteOperation
)

func operationContext(ctx context.Context, builder db.CommonBuilder, operation int) context.Context {
	if _, ok := builder.(*observedBuilder); ok {
		return context.WithValue(ctx, queryOperationKey{}, queryOperation{kind: operation, context: ctx})
	}
	return ctx
}

func queryContext(ctx context.Context) (context.Context, int) {
	if operation, ok := ctx.Value(queryOperationKey{}).(queryOperation); ok {
		return operation.context, operation.kind
	}
	return ctx, affectingOperation
}

type pretendResult struct{}

func (pretendResult) LastInsertId() (int64, error) { return 0, nil }
func (pretendResult) RowsAffected() (int64, error) { return 0, nil }

func (b *observedBuilder) ExecContext(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	ctx, operation := queryContext(ctx)
	if err := b.state.beforeQuery(ctx, statement, args); err != nil {
		return nil, err
	}
	begin := time.Now()
	if b.state.pretend() {
		b.state.completed(statement, args, begin, false)
		return pretendResult{}, nil
	}
	target, err := b.target()
	if err != nil {
		return nil, err
	}
	result, err := target.ExecContext(ctx, statement, args...)
	if err == nil {
		count, _ := result.RowsAffected()
		b.state.completed(statement, args, begin, count > 0 || operation == statementOperation)
	}
	return result, err
}
func (b *observedBuilder) SelectContext(ctx context.Context, dest any, statement string, args ...any) error {
	ctx, operation := queryContext(ctx)
	if err := b.state.beforeQuery(ctx, statement, args); err != nil {
		return err
	}
	begin := time.Now()
	if b.state.pretend() {
		clearDestination(dest)
		b.state.completed(statement, args, begin, false)
		return nil
	}
	target, err := b.target()
	if err != nil {
		return err
	}
	err = target.SelectContext(ctx, dest, statement, args...)
	if b.retryRead(ctx, err, operation) {
		clearDestination(dest)
		err = target.SelectContext(ctx, dest, statement, args...)
	}
	if err == nil {
		b.state.completed(statement, args, begin, operation == returningWriteOperation)
	}
	return err
}
func (b *observedBuilder) GetContext(ctx context.Context, dest any, statement string, args ...any) error {
	ctx, operation := queryContext(ctx)
	if err := b.state.beforeQuery(ctx, statement, args); err != nil {
		return err
	}
	begin := time.Now()
	if b.state.pretend() {
		clearDestination(dest)
		b.state.completed(statement, args, begin, false)
		return nil
	}
	target, err := b.target()
	if err != nil {
		return err
	}
	err = target.GetContext(ctx, dest, statement, args...)
	if b.retryRead(ctx, err, operation) {
		clearDestination(dest)
		err = target.GetContext(ctx, dest, statement, args...)
	}
	if err == nil || err == sql.ErrNoRows {
		b.state.completed(statement, args, begin, operation == returningWriteOperation)
	}
	return err
}
func (b *observedBuilder) QueryxContext(ctx context.Context, statement string, args ...any) (*sqlx.Rows, error) {
	ctx, operation := queryContext(ctx)
	if err := b.state.beforeQuery(ctx, statement, args); err != nil {
		return nil, err
	}
	begin := time.Now()
	if b.state.pretend() {
		b.state.completed(statement, args, begin, false)
		return nil, nil
	}
	target, err := b.target()
	if err != nil {
		return nil, err
	}
	rows, err := target.QueryxContext(ctx, statement, args...)
	if b.retryRead(ctx, err, operation) {
		if rows != nil {
			_ = rows.Close()
		}
		rows, err = target.QueryxContext(ctx, statement, args...)
	}
	if err == nil {
		b.state.completed(statement, args, begin, operation == returningWriteOperation)
	}
	return rows, err
}

// database/sql replaces failed physical connections within its pool. Retry a read once
// before any rows escape to the caller; transactions, pinned sessions and writes cannot replay.
func (b *observedBuilder) retryRead(ctx context.Context, err error, operation int) bool {
	return b.read && operation != returningWriteOperation && ctx.Err() == nil && dbdriver.CausedByLostConnection(err)
}
func clearDestination(dest any) {
	value := reflect.ValueOf(dest)
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() && value.Elem().CanSet() {
		value.Elem().SetZero()
	}
}
