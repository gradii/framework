package db

import (
	"context"
	"testing"

	"github.com/gradii/framework/contracts/database/db"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	mocksdb "github.com/gradii/framework/mocks/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTransactionTest(t *testing.T) (*Tx, *mocksdb.TxBuilder) {
	t.Helper()
	builder := mocksdb.NewTxBuilder(t)
	manager, record := newTransactionManager(builder)
	return &Tx{ctx: context.Background(), txBuilder: builder, transactions: manager, record: record,
		state: newConnectionState("test", "mysql"), queryGrammar: querybuilder.NewMySqlGrammar(""), txLogs: &[]TxLog{}}, builder
}

func TestTransactionCommitCallbacksFollowLaravelStagingOrder(t *testing.T) {
	root, builder := newTransactionTest(t)
	builder.EXPECT().ExecContext(root.ctx, "SAVEPOINT trans2").Return(nil, nil).Once()
	builder.EXPECT().Commit().Return(nil).Once()
	var called []string
	require.NoError(t, root.AfterCommit(func() error { called = append(called, "outer before"); return nil }))
	child, err := root.BeginTransaction()
	require.NoError(t, err)
	assert.Equal(t, 2, child.TransactionLevel())
	require.NoError(t, child.AfterCommit(func() error { called = append(called, "inner"); return nil }))
	require.NoError(t, child.Commit())
	assert.Empty(t, called)
	assert.Equal(t, 1, root.TransactionLevel())
	require.NoError(t, root.AfterCommit(func() error { called = append(called, "outer after"); return nil }))
	require.NoError(t, root.Commit())
	assert.Equal(t, []string{"inner", "outer before", "outer after"}, called)
	assert.Zero(t, root.TransactionLevel())
}

func TestTransactionRollbackRemovesCommittedDescendants(t *testing.T) {
	root, builder := newTransactionTest(t)
	builder.EXPECT().ExecContext(root.ctx, "SAVEPOINT trans2").Return(nil, nil).Twice()
	builder.EXPECT().ExecContext(root.ctx, "SAVEPOINT trans3").Return(nil, nil).Once()
	builder.EXPECT().ExecContext(root.ctx, "ROLLBACK TO SAVEPOINT trans2").Return(nil, nil).Once()
	builder.EXPECT().Commit().Return(nil).Once()
	var called []string
	first, err := root.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, first.AfterCommit(func() error { called = append(called, "first committed"); return nil }))
	require.NoError(t, first.Commit())
	second, err := root.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, second.AfterRollback(func() error { called = append(called, "second rolled back"); return nil }))
	grandchild, err := second.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, grandchild.AfterCommit(func() error { called = append(called, "must not commit"); return nil }))
	require.NoError(t, grandchild.AfterRollback(func() error { called = append(called, "must not roll back"); return nil }))
	require.NoError(t, grandchild.Commit())
	require.NoError(t, second.Rollback())
	require.NoError(t, root.AfterCommit(func() error { called = append(called, "root"); return nil }))
	require.NoError(t, root.Commit())
	assert.Equal(t, []string{"second rolled back", "first committed", "root"}, called)
}

func TestTransactionRollbackCallbacksRunFromActiveChildToParent(t *testing.T) {
	root, builder := newTransactionTest(t)
	builder.EXPECT().ExecContext(root.ctx, "SAVEPOINT trans2").Return(nil, nil).Once()
	builder.EXPECT().Rollback().Return(nil).Once()
	var called []string
	require.NoError(t, root.AfterRollback(func() error { called = append(called, "root"); return nil }))
	child, err := root.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, child.AfterRollback(func() error { called = append(called, "child"); return nil }))
	require.NoError(t, root.RollbackTo(0))
	assert.Equal(t, []string{"child", "root"}, called)
	assert.Zero(t, child.TransactionLevel())
}

func TestTransactionRetriesTheWholeCallback(t *testing.T) {
	attempts := 0
	begin := func() (db.Tx, error) {
		root, builder := newTransactionTest(t)
		attempts++
		if attempts == 1 {
			builder.EXPECT().Rollback().Return(nil).Once()
		} else {
			builder.EXPECT().Commit().Return(nil).Once()
		}
		return root, nil
	}
	calls := 0
	err := runTransaction(begin, func(tx db.Tx) error {
		calls++
		if calls == 1 {
			return errors.DatabaseDeadlock.Args("retry")
		}
		return nil
	}, []int{3})
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 2, calls)
}

func TestTransactionDoesNotRetryAnAfterCommitFailure(t *testing.T) {
	attempts := 0
	begin := func() (db.Tx, error) {
		root, builder := newTransactionTest(t)
		builder.EXPECT().Commit().Return(nil).Once()
		attempts++
		return root, nil
	}
	err := runTransaction(begin, func(tx db.Tx) error {
		return tx.AfterCommit(func() error { return errors.DatabaseDeadlock.Args("callback") })
	}, []int{3})
	assert.ErrorIs(t, err, errors.DatabaseTransactionCallbackFailed)
	assert.Equal(t, 1, attempts)
}

func TestTransactionRollsBackPanicAndDoesNotRetryIt(t *testing.T) {
	attempts := 0
	begin := func() (db.Tx, error) {
		root, builder := newTransactionTest(t)
		builder.EXPECT().Rollback().Return(nil).Once()
		attempts++
		return root, nil
	}
	err := runTransaction(begin, func(db.Tx) error { panic("deadlock detected in application") }, []int{3})
	assert.ErrorIs(t, err, errors.DatabaseTransactionPanic)
	assert.Equal(t, 1, attempts)
}

func TestTransactionCallbacksOutsideATransaction(t *testing.T) {
	tx := &Tx{}
	calls := 0
	require.NoError(t, tx.AfterCommit(func() error { calls++; return nil }))
	require.NoError(t, tx.AfterRollback(func() error { calls++; return nil }))
	assert.Equal(t, 1, calls)
}
