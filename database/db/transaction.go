package db

import (
	"context"
	"database/sql"
	"strconv"
	"sync"

	"github.com/jmoiron/sqlx"

	"github.com/gradii/framework/contracts/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
)

const (
	transactionPending = iota
	transactionCommitted
	transactionRolledBack
)

type transactionRecord struct {
	level             int
	parent            *transactionRecord
	status            int
	commitCallbacks   []func() error
	rollbackCallbacks []func() error
}
type transactionManager struct {
	mu        sync.Mutex
	builder   db.TxBuilder
	current   *transactionRecord
	committed []*transactionRecord
	closed    bool
	didCommit bool
	failed    error
}

func newTransactionManager(builder db.TxBuilder) (*transactionManager, *transactionRecord) {
	record := &transactionRecord{level: 1}
	return &transactionManager{builder: builder, current: record}, record
}
func (m *transactionManager) ready(record *transactionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || record == nil || record.status != transactionPending {
		return sql.ErrTxDone
	}
	return m.failed
}
func (m *transactionManager) level() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return 0
	}
	return m.current.level
}
func savepointSQL(grammar querybuilder.Grammar, name string, rollback bool) string {
	if rollback {
		if compiler, ok := grammar.(interface{ CompileSavepointRollBack(string) string }); ok {
			return compiler.CompileSavepointRollBack(name)
		}
		return "ROLLBACK TO SAVEPOINT " + name
	}
	if compiler, ok := grammar.(interface{ CompileSavepoint(string) string }); ok {
		return compiler.CompileSavepoint(name)
	}
	return "SAVEPOINT " + name
}
func (m *transactionManager) begin(ctx context.Context, parent *transactionRecord, grammar querybuilder.Grammar) (*transactionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || parent.status != transactionPending {
		return nil, sql.ErrTxDone
	}
	if m.failed != nil {
		return nil, m.failed
	}
	if m.current != parent {
		return nil, errors.DatabaseTransactionOrder
	}
	level := parent.level + 1
	if _, err := m.builder.ExecContext(ctx, savepointSQL(grammar, "trans"+strconv.Itoa(level), false)); err != nil {
		return nil, err
	}
	record := &transactionRecord{level: level, parent: parent}
	m.current = record
	return record, nil
}

// Committed children are staged before their parent, matching Laravel's transaction manager.
func (m *transactionManager) commit(record *transactionRecord) (bool, []func() error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || record.status != transactionPending {
		return false, nil, sql.ErrTxDone
	}
	if m.failed != nil {
		return false, nil, m.failed
	}
	if m.current != record {
		return false, nil, errors.DatabaseTransactionOrder
	}
	if record.level == 1 {
		if err := m.builder.Commit(); err != nil {
			m.discard(0)
			m.closed = true
			return false, nil, err
		}
	}
	record.status = transactionCommitted
	m.committed = append(m.committed, record)
	m.current = record.parent
	if record.level != 1 {
		return false, nil, nil
	}
	m.closed, m.didCommit = true, true
	var callbacks []func() error
	for _, transaction := range m.committed {
		callbacks = append(callbacks, transaction.commitCallbacks...)
	}
	m.committed = nil
	return true, callbacks, nil
}

// discard removes committed descendants but only runs rollback callbacks on the active chain.
// A child that already committed does not gain a rollback callback when its parent rolls back.
func (m *transactionManager) discard(level int) []func() error {
	var callbacks []func() error
	var removed *transactionRecord
	for m.current != nil && m.current.level > level {
		record := m.current
		callbacks = append(callbacks, record.rollbackCallbacks...)
		record.status = transactionRolledBack
		removed = record
		m.current = record.parent
	}
	if removed != nil {
		kept := m.committed[:0]
		for _, record := range m.committed {
			discard := false
			for ancestor := record; ancestor != nil; ancestor = ancestor.parent {
				if ancestor == removed {
					discard = true
					break
				}
			}
			if discard {
				record.status = transactionRolledBack
			} else {
				kept = append(kept, record)
			}
		}
		m.committed = kept
	}
	return callbacks
}
func (m *transactionManager) rollback(ctx context.Context, level int, grammar querybuilder.Grammar) ([]func() error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, sql.ErrTxDone
	}
	if level < 0 || m.current == nil || level >= m.current.level {
		return nil, nil
	}
	var err error
	if level == 0 {
		err = m.builder.Rollback()
		if ctx.Err() != nil && errors.Is(err, sql.ErrTxDone) {
			err = nil
		}
		m.closed = true
	} else {
		_, err = m.builder.ExecContext(ctx, savepointSQL(grammar, "trans"+strconv.Itoa(level+1), true))
	}
	if err != nil {
		if level == 0 {
			m.discard(0)
		}
		return nil, err
	}
	return m.discard(level), nil
}
func (m *transactionManager) abortNested(level int, failure error) []func() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = failure
	return m.discard(level)
}
func runTransactionCallbacks(callbacks []func() error) error {
	for _, callback := range callbacks {
		if err := callback(); err != nil {
			return errors.Join(errors.DatabaseTransactionCallbackFailed.Args(err), err)
		}
	}
	return nil
}

func (r *Tx) BeginTransaction() (db.Tx, error) {
	if err := r.beforeStartingTransaction(); err != nil {
		return nil, err
	}
	if r.transactions == nil && r.pinned != nil {
		return r.beginPinnedTransaction()
	}
	if r.transactions == nil {
		return nil, errors.DatabaseTransactionNotStarted
	}
	record, err := r.transactions.begin(r.ctx, r.record, r.queryGrammar)
	if err != nil {
		return nil, err
	}
	child := *r
	child.record = record
	return &child, nil
}
func (r *Tx) Transaction(callback func(db.Tx) error, attempts ...int) error {
	return runTransaction(r.BeginTransaction, callback, attempts)
}
func (r *Tx) TransactionLevel() int {
	if r.transactions != nil {
		return r.transactions.level()
	}
	if r.txBuilder != nil {
		return 1
	}
	return 0
}
func (r *Tx) AfterCommit(callback func() error) error {
	if r.transactions == nil {
		return callback()
	}
	r.transactions.mu.Lock()
	record := r.transactions.current
	if record == nil {
		r.transactions.mu.Unlock()
		return callback()
	}
	record.commitCallbacks = append(record.commitCallbacks, callback)
	r.transactions.mu.Unlock()
	return nil
}
func (r *Tx) AfterRollback(callback func() error) error {
	if r.transactions == nil {
		return nil
	}
	r.transactions.mu.Lock()
	defer r.transactions.mu.Unlock()
	if record := r.transactions.current; record != nil {
		record.rollbackCallbacks = append(record.rollbackCallbacks, callback)
	}
	return nil
}
func (r *Tx) AfterRollBack(callback func() error) error { return r.AfterRollback(callback) }
func (r *Tx) RollbackTo(level int) error {
	if r.transactions == nil {
		if level == 0 {
			return r.Rollback()
		}
		return errors.DatabaseTransactionNotStarted
	}
	callbacks, err := r.transactions.rollback(r.ctx, level, r.queryGrammar)
	if err != nil {
		return err
	}
	return runTransactionCallbacks(callbacks)
}
func transactionPanic(callback func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.DatabaseTransactionPanic.Args(recovered)
		}
	}()
	return callback()
}
func runTransaction(begin func() (db.Tx, error), callback func(db.Tx) error, attempts []int) error {
	max := 1
	if len(attempts) > 0 {
		max = attempts[0]
	}
	for attempt := 0; attempt < max; attempt++ {
		tx, err := begin()
		if err != nil {
			return err
		}
		concrete, _ := tx.(*Tx)
		err = transactionPanic(func() error { return callback(tx) })
		if err != nil {
			if concrete != nil && concrete.transactions != nil && concrete.record.level > 1 && databasedriver.CausedByConcurrencyError(err) {
				callbacks := concrete.transactions.abortNested(concrete.record.level-1, err)
				return errors.Join(err, runTransactionCallbacks(callbacks))
			}
			if rollbackErr := transactionPanic(tx.Rollback); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				return errors.Join(err, rollbackErr)
			}
			if attempt+1 < max && retryableTransactionError(err) {
				continue
			}
			return err
		}
		err = transactionPanic(tx.Commit)
		if err == nil {
			return nil
		}
		committed, closed := false, false
		if concrete != nil && concrete.transactions != nil {
			concrete.transactions.mu.Lock()
			committed, closed = concrete.transactions.didCommit, concrete.transactions.closed
			concrete.transactions.mu.Unlock()
		}
		if committed {
			return err
		}
		if !closed {
			if rollbackErr := transactionPanic(tx.Rollback); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				return errors.Join(err, rollbackErr)
			}
		}
		if attempt+1 < max && retryableTransactionError(err) {
			continue
		}
		return err
	}
	return nil
}

func retryableTransactionError(err error) bool {
	return !errors.Is(err, errors.DatabaseTransactionCallbackFailed) && !errors.Is(err, errors.DatabaseTransactionPanic) && databasedriver.CausedByConcurrencyError(err)
}

// Each query checks the logical savepoint scope as well as database/sql's root transaction.
type transactionQueryBuilder struct {
	db.CommonBuilder
	manager *transactionManager
	record  *transactionRecord
}

func (b *transactionQueryBuilder) ExecContext(ctx context.Context, sql string, args ...any) (sql.Result, error) {
	if err := b.manager.ready(b.record); err != nil {
		return nil, err
	}
	return b.CommonBuilder.ExecContext(ctx, sql, args...)
}
func (b *transactionQueryBuilder) GetContext(ctx context.Context, dest any, sql string, args ...any) error {
	if err := b.manager.ready(b.record); err != nil {
		return err
	}
	return b.CommonBuilder.GetContext(ctx, dest, sql, args...)
}
func (b *transactionQueryBuilder) SelectContext(ctx context.Context, dest any, sql string, args ...any) error {
	if err := b.manager.ready(b.record); err != nil {
		return err
	}
	return b.CommonBuilder.SelectContext(ctx, dest, sql, args...)
}

func (b *transactionQueryBuilder) QueryxContext(ctx context.Context, statement string, args ...any) (*sqlx.Rows, error) {
	if err := b.manager.ready(b.record); err != nil {
		return nil, err
	}
	return b.CommonBuilder.QueryxContext(ctx, statement, args...)
}

func (r *Tx) transactionalBuilder() db.CommonBuilder {
	var builder db.CommonBuilder = r.txBuilder
	if r.transactions != nil {
		builder = &transactionQueryBuilder{CommonBuilder: builder, manager: r.transactions, record: r.record}
	}
	return observeBuilder(builder, r.state, false, nil)
}

func (r *Tx) BeforeStartingTransaction(callback func(db.Tx) error) {
	if r.state == nil {
		return
	}
	r.state.hooks.mu.Lock()
	defer r.state.hooks.mu.Unlock()
	r.state.hooks.beforeTransactions = append(r.state.hooks.beforeTransactions, callback)
}

func (r *Tx) beforeStartingTransaction() error {
	if r.state == nil {
		return nil
	}
	r.state.hooks.mu.RLock()
	callbacks := append([]func(db.Tx) error{}, r.state.hooks.beforeTransactions...)
	r.state.hooks.mu.RUnlock()
	for _, callback := range callbacks {
		if err := callback(r); err != nil {
			return err
		}
	}
	return nil
}

type pretendTxBuilder struct{}

func (*pretendTxBuilder) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return pretendResult{}, nil
}
func (*pretendTxBuilder) GetContext(context.Context, any, string, ...any) error    { return nil }
func (*pretendTxBuilder) SelectContext(context.Context, any, string, ...any) error { return nil }
func (*pretendTxBuilder) QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error) {
	return nil, nil
}
func (*pretendTxBuilder) Explain(statement string, values ...any) string {
	return Explain(statement, values...)
}
func (*pretendTxBuilder) Commit() error   { return nil }
func (*pretendTxBuilder) Rollback() error { return nil }
