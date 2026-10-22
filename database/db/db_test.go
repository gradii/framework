package db

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsdb "github.com/goravel/framework/contracts/database/db"
	"github.com/goravel/framework/errors"
	mocksdb "github.com/goravel/framework/mocks/database/db"
	mocksdriver "github.com/goravel/framework/mocks/database/driver"
	mockslogger "github.com/goravel/framework/mocks/database/logger"
	"github.com/goravel/framework/support/carbon"
	instrumentationdatabase "github.com/goravel/framework/telemetry/instrumentation/database"
)

func TestTxSelectPassesParameterizedSQL(t *testing.T) {
	ctx := context.Background()
	now := carbon.Now()
	carbon.SetTestNow(now)
	defer carbon.ClearTestNow()

	parameterizedSQL := "SELECT * FROM users WHERE name = ?"
	explainedSQL := `SELECT * FROM users WHERE name = "John"`

	t.Run("slice destination uses SelectContext with placeholder", func(t *testing.T) {
		mockBuilder := mocksdb.NewTxBuilder(t)
		mockLogger := mockslogger.NewLogger(t)
		tx := &Tx{ctx: ctx, logger: mockLogger, txBuilder: mockBuilder}

		var users []TestUser
		mockBuilder.EXPECT().Explain(parameterizedSQL, "John").Return(explainedSQL).Once()
		mockBuilder.EXPECT().SelectContext(ctx, &users, parameterizedSQL, "John").Return(nil).Once()
		mockLogger.EXPECT().Trace(ctx, now, explainedSQL, int64(0), nil).Once()

		assert.NoError(t, tx.Select(&users, parameterizedSQL, "John"))
	})

	t.Run("struct destination uses GetContext with placeholder", func(t *testing.T) {
		mockBuilder := mocksdb.NewTxBuilder(t)
		mockLogger := mockslogger.NewLogger(t)
		tx := &Tx{ctx: ctx, logger: mockLogger, txBuilder: mockBuilder}

		var user TestUser
		mockBuilder.EXPECT().Explain(parameterizedSQL, "John").Return(explainedSQL).Once()
		mockBuilder.EXPECT().GetContext(ctx, &user, parameterizedSQL, "John").Return(nil).Once()
		mockLogger.EXPECT().Trace(ctx, now, explainedSQL, int64(1), nil).Once()

		assert.NoError(t, tx.Select(&user, parameterizedSQL, "John"))
	})
}

func TestNewTx_UsesSharedInstrument(t *testing.T) {
	pool := contractsdatabase.Pool{Writers: []contractsdatabase.Config{{Driver: "postgres", Connection: "primary"}}}
	instrument := instrumentationdatabase.NewInstrument(pool, "primary", nil)

	driver := mocksdriver.NewDriver(t)
	driver.EXPECT().Pool().Return(pool).Once()
	driver.EXPECT().Grammar().Return(nil).Once()

	mockTxBuilder := mocksdb.NewTxBuilder(t)
	tx := NewTx(context.Background(), driver, mockslogger.NewLogger(t), nil, mockTxBuilder, &[]TxLog{}, instrument)

	assert.Equal(t, instrument, tx.instrument)
	assert.NotEqual(t, contractsdb.TxBuilder(mockTxBuilder), tx.txBuilder)
}

func TestConnection_ConcurrentSafe(t *testing.T) {
	child := &DB{}
	cache := &queriesCache{m: map[string]contractsdb.DB{
		"test": child,
	}}
	child.queries = cache

	db := &DB{queries: cache}

	var wg sync.WaitGroup
	n := 100
	results := make([]contractsdb.DB, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = db.Connection("test")
		}(i)
	}
	wg.Wait()

	for i, result := range results {
		require.NotNil(t, result, "goroutine %d got nil", i)
		assert.Same(t, child, result, "goroutine %d got unexpected result", i)
		resultDB, ok := result.(*DB)
		require.True(t, ok, "goroutine %d: result is not *DB", i)
		assert.Same(t, cache, resultDB.queries, "goroutine %d: child queries not aliased", i)
	}
}

func TestTx_UnconfiguredConnection(t *testing.T) {
	t.Run("readBuilder reports the declared error instead of dereferencing a nil connection", func(t *testing.T) {
		tx := &Tx{ctx: context.Background()}

		builder, err := tx.readBuilder()

		assert.Nil(t, builder)
		assert.ErrorIs(t, err, errors.DatabaseConnectionIsNil)
	})

	t.Run("writeBuilder reports the declared error instead of dereferencing a nil connection", func(t *testing.T) {
		tx := &Tx{ctx: context.Background()}

		builder, err := tx.writeBuilder()

		assert.Nil(t, builder)
		assert.ErrorIs(t, err, errors.DatabaseConnectionIsNil)
	})

	t.Run("Table surfaces the declared error through the logger rather than dereferencing the connection", func(t *testing.T) {
		mockLogger := mockslogger.NewLogger(t)
		tx := &Tx{ctx: context.Background(), logger: mockLogger}
		mockLogger.EXPECT().Panicf(context.Background(), errors.DatabaseConnectionIsNil.Error()).Return().Once()

		assert.Nil(t, tx.Table("users"))
	})
}

func TestDB_BeginTransactionUnconfiguredConnection(t *testing.T) {
	// A connection opened without a database name has no pool to build a transaction
	// against; BeginTransaction must report the declared error rather than dereference it.
	db := &DB{ctx: context.Background()}

	tx, err := db.BeginTransaction()

	assert.Nil(t, tx)
	assert.ErrorIs(t, err, errors.DatabaseConnectionIsNil)
}

func TestDB_TransactionUnconfiguredConnection(t *testing.T) {
	db := &DB{ctx: context.Background()}
	called := false

	err := db.Transaction(func(tx contractsdb.Tx) error {
		called = true
		return nil
	})

	assert.ErrorIs(t, err, errors.DatabaseConnectionIsNil)
	assert.False(t, called, "callback must not run when the transaction cannot start")
}

func TestNewTx_UnrecognisedDriverWarning(t *testing.T) {
	ctx := context.Background()

	t.Run("unrecognised driver logs warning when logger is not nil", func(t *testing.T) {
		mockLogger := mockslogger.NewLogger(t)
		mockDriver := mocksdriver.NewDriver(t)
		mockGrammar := mocksdriver.NewGrammar(t)
		mockPool := contractsdatabase.Pool{
			Writers: []contractsdatabase.Config{
				{Driver: "unknown_test_driver", Prefix: ""},
			},
		}

		mockDriver.EXPECT().Pool().Return(mockPool).Once()
		mockDriver.EXPECT().Grammar().Return(mockGrammar).Once()
		mockLogger.EXPECT().Warningf(ctx, "%s", mock.AnythingOfType("*errors.errorString")).Once()

		tx := NewTx(ctx, mockDriver, mockLogger, nil, nil, &[]TxLog{}, nil)

		assert.NotNil(t, tx)
		assert.NotNil(t, tx.queryGrammar)
	})

	t.Run("unrecognised driver is silent when logger is nil", func(t *testing.T) {
		mockDriver := mocksdriver.NewDriver(t)
		mockGrammar := mocksdriver.NewGrammar(t)
		mockPool := contractsdatabase.Pool{
			Writers: []contractsdatabase.Config{
				{Driver: "unknown_test_driver_2", Prefix: ""},
			},
		}

		mockDriver.EXPECT().Pool().Return(mockPool).Once()
		mockDriver.EXPECT().Grammar().Return(mockGrammar).Once()

		tx := NewTx(ctx, mockDriver, nil, nil, nil, &[]TxLog{}, nil)

		assert.NotNil(t, tx)
		assert.NotNil(t, tx.queryGrammar)
	})

	t.Run("recognised driver does not log warning", func(t *testing.T) {
		mockLogger := mockslogger.NewLogger(t)
		mockDriver := mocksdriver.NewDriver(t)
		mockGrammar := mocksdriver.NewGrammar(t)
		mockPool := contractsdatabase.Pool{
			Writers: []contractsdatabase.Config{
				{Driver: "mysql", Prefix: ""},
			},
		}

		mockDriver.EXPECT().Pool().Return(mockPool).Once()
		mockDriver.EXPECT().Grammar().Return(mockGrammar).Once()

		tx := NewTx(ctx, mockDriver, mockLogger, nil, nil, &[]TxLog{}, nil)

		assert.NotNil(t, tx)
		assert.NotNil(t, tx.queryGrammar)
	})
}
