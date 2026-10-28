package db

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"sync"

	"github.com/gradii/framework/contracts/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/gradii/framework/errors"
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
	return r.close(false)
}

// CanDiscard reports whether this handle owns a removable physical connection.
func (r *Tx) CanDiscard() bool { return r.pinned != nil }

// Discard rolls back work and removes a leased physical connection from its pool.
// It is optional for db.Tx implementations and used when session cleanup fails.
func (r *Tx) Discard() error {
	if r.pinned == nil {
		return errors.SchemaSessionDiscardNotSupported
	}
	return (&writeConnection{Tx: r, lease: r.pinned}).close(true)
}

func (r *writeConnection) close(discard bool) error {
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
		if discard {
			if err := r.lease.connection.Raw(func(any) error { return sqldriver.ErrBadConn }); err != nil && !errors.Is(err, sqldriver.ErrBadConn) && !errors.Is(err, sql.ErrConnDone) {
				failures = append(failures, err)
			}
		}
		if err := r.lease.connection.Close(); err != nil {
			if !discard || !errors.Is(err, sql.ErrConnDone) {
				failures = append(failures, err)
			}
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
		options, err := databasedriver.TransactionOptions(r.config, r.state.name)
		if err != nil {
			return nil, err
		}
		tx, err := r.pinned.connection.BeginTxx(r.ctx, options)
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
