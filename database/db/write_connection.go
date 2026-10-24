package db

import (
	"context"
	"database/sql"
	"sync"

	"github.com/goravel/framework/contracts/database/db"
	"github.com/goravel/framework/errors"
	"github.com/jmoiron/sqlx"
)

type leasedConnectionState struct {
	mu           sync.Mutex
	connection   *sqlx.Conn
	closed       bool
	transactions []*transactionManager
}
type writeConnection struct {
	*Tx
	lease *leasedConnectionState
}

func (r *DB) AcquireWriteConnection() (db.WriteConnection, error) {
	base, ok := r.Tx.(*Tx)
	if !ok {
		return nil, errors.DatabaseConnectionIsNil
	}
	lease := &leasedConnectionState{}
	if !r.state.pretend() {
		if r.connection == nil {
			return nil, errors.DatabaseConnectionIsNil
		}
		// Version-dependent grammars must not borrow a second pool connection while this one is pinned.
		_, _ = r.connection.ServerVersion(r.ctx)
		pool := sqlx.NewDb(r.connection.Writer(), sqlxDriverName(r.DriverName())).Unsafe()
		pool.MapperFunc(NameMapper)
		connection, err := pool.Connx(r.ctx)
		if err != nil {
			return nil, err
		}
		lease.connection = connection
	}
	tx := *base
	tx.pinned = lease
	tx.txBuilder = nil
	tx.transactions, tx.record = nil, nil
	return &writeConnection{Tx: &tx, lease: lease}, nil
}
func (r *DB) WithWriteConnection(callback func(db.Tx) error) (err error) {
	connection, err := r.AcquireWriteConnection()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, connection.Close()) }()
	return callback(connection)
}
func (r *writeConnection) Close() error {
	r.lease.mu.Lock()
	if r.lease.closed {
		r.lease.mu.Unlock()
		return nil
	}
	r.lease.closed = true
	transactions := append([]*transactionManager(nil), r.lease.transactions...)
	r.lease.mu.Unlock()
	var failures []error
	for _, transaction := range transactions {
		if transaction.level() == 0 {
			continue
		}
		callbacks, err := transaction.rollback(context.Background(), 0, r.queryGrammar)
		if err != nil && !errors.Is(err, sql.ErrTxDone) {
			failures = append(failures, err)
		}
		if err := runTransactionCallbacks(callbacks); err != nil {
			failures = append(failures, err)
		}
	}
	if r.lease.connection != nil {
		if err := r.lease.connection.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

type connectionBuilder struct {
	*sqlx.Conn
	lease *leasedConnectionState
}

func (b *connectionBuilder) ready() error {
	b.lease.mu.Lock()
	defer b.lease.mu.Unlock()
	if b.lease.closed {
		return sql.ErrConnDone
	}
	return nil
}
func (b *connectionBuilder) Explain(statement string, args ...any) string {
	return Explain(statement, args...)
}
func (b *connectionBuilder) ExecContext(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	if err := b.ready(); err != nil {
		return nil, err
	}
	return b.Conn.ExecContext(ctx, statement, args...)
}
func (b *connectionBuilder) QueryxContext(ctx context.Context, statement string, args ...any) (*sqlx.Rows, error) {
	if err := b.ready(); err != nil {
		return nil, err
	}
	return b.Conn.QueryxContext(ctx, statement, args...)
}
func (b *connectionBuilder) GetContext(ctx context.Context, dest any, statement string, args ...any) error {
	if err := b.ready(); err != nil {
		return err
	}
	if target, ok := dest.(*map[string]any); ok {
		return getMap(ctx, b, target, statement, args...)
	}
	return b.Conn.GetContext(ctx, dest, statement, args...)
}
func (b *connectionBuilder) SelectContext(ctx context.Context, dest any, statement string, args ...any) error {
	if err := b.ready(); err != nil {
		return err
	}
	if target, ok := dest.(*[]map[string]any); ok {
		return selectMaps(ctx, b, target, statement, args...)
	}
	return b.Conn.SelectContext(ctx, dest, statement, args...)
}
func (r *Tx) beginPinnedTransaction() (db.Tx, error) {
	r.pinned.mu.Lock()
	defer r.pinned.mu.Unlock()
	if r.pinned.closed {
		return nil, sql.ErrConnDone
	}
	var builder db.TxBuilder
	if r.state.pretend() {
		builder = &pretendTxBuilder{}
	} else {
		tx, err := r.pinned.connection.BeginTxx(r.ctx, nil)
		if err != nil {
			return nil, err
		}
		builder = &TxBuilder{Tx: tx}
	}
	child := *r
	child.txBuilder = builder
	child.transactions, child.record = newTransactionManager(builder)
	r.pinned.transactions = append(r.pinned.transactions, child.transactions)
	return &child, nil
}
