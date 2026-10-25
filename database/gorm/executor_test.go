package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormio "gorm.io/gorm"

	querybuilder "github.com/goravel/framework/database/query"
)

// This file pins where a compiled statement runs: on the transaction's own *sql.Tx when the query is
// bound to one, and on the connection's base pool otherwise.
//
// The distinction matters because gorm's (*DB).DB() — the handle the pool helpers used to read —
// unwraps a *sql.Tx back to the *sql.DB underneath it. A statement run through that base pool
// executes in auto-commit, so a Rollback rolls nothing back and a FOR UPDATE takes no lock. The
// integration module proves the end-to-end semantics (TestTransactionError, TestLockForUpdate);
// these tests pin the resolution itself, with no database behind it.

// stubTxDriver is a minimal database/sql driver whose connections can begin a transaction, so a test
// can hold a real *sql.Tx with no database behind it.
type stubTxDriver struct{}

func (stubTxDriver) Open(string) (driver.Conn, error) { return stubTxConn{}, nil }

type stubTxConnector struct{}

func (stubTxConnector) Connect(context.Context) (driver.Conn, error) { return stubTxConn{}, nil }
func (stubTxConnector) Driver() driver.Driver                        { return stubTxDriver{} }

type stubTxConn struct{}

func (stubTxConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (stubTxConn) Close() error                        { return nil }
func (stubTxConn) Begin() (driver.Tx, error)           { return stubTx{}, nil }

type stubTx struct{}

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

// TestQueryPoolResolvesTheTransactionHandle pins that a query bound to a transaction resolves its
// pool to that transaction's *sql.Tx. Before the fix queryPool returned (*gorm.DB).DB(), which for a
// transaction is the base *sql.DB, so every compiled write ran in auto-commit and no Rollback could
// undo it.
func TestQueryPoolResolvesTheTransactionHandle(t *testing.T) {
	sqlDB := sql.OpenDB(stubTxConnector{})
	t.Cleanup(func() { _ = sqlDB.Close() })

	base, err := gormio.Open(stubDialector{}, &gormio.Config{ConnPool: sqlDB, DisableAutomaticPing: true})
	require.NoError(t, err)

	// Outside a transaction both pools are the base *sql.DB.
	plain := &Query{instance: base, builder: querybuilder.NewBuilder(nil)}
	assert.False(t, plain.inTransaction())

	writer, err := plain.queryPool()
	require.NoError(t, err)
	assert.Same(t, sqlDB, writer)

	// GORM swaps the statement's ConnPool for the *sql.Tx when Begin runs; that is the handle a
	// compiled statement must run on.
	tx := base.Begin()
	require.NoError(t, tx.Error)

	inTx := &Query{instance: tx, builder: querybuilder.NewBuilder(nil)}
	assert.True(t, inTx.inTransaction())

	pool, err := inTx.queryPool()
	require.NoError(t, err)

	resolved, ok := pool.(*sql.Tx)
	require.True(t, ok, "a statement issued inside a transaction must run on the transaction's *sql.Tx")
	assert.Same(t, tx.Statement.ConnPool, resolved)
	assert.NotSame(t, sqlDB, resolved)

	// A read inside the transaction runs on the transaction too, so it sees its uncommitted writes.
	read, err := inTx.readPool()
	require.NoError(t, err)
	assert.Same(t, resolved, read)
}
