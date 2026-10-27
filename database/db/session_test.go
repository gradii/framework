package db

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/gradii/framework/contracts/database"
	"github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/errors"
	mocksdb "github.com/gradii/framework/mocks/database/db"
	mocksdriver "github.com/gradii/framework/mocks/database/driver"
	mockslogger "github.com/gradii/framework/mocks/database/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestLostConnectionReadRetriesWithoutRepeatingHooks(t *testing.T) {
	ctx := context.Background()
	state := newConnectionState("primary", "mysql")
	connection := &DB{state: state}
	before, after := 0, 0
	connection.BeforeExecuting(func(context.Context, string, []any) error { before++; return nil })
	connection.Listen(func(db.QueryExecuted) { after++ })
	builder := mocksdb.NewBuilder(t)
	observed := &observedBuilder{CommonBuilder: builder, state: state, read: true}
	value := 3
	builder.EXPECT().GetContext(ctx, &value, "select value").Return(io.EOF).Once()
	builder.EXPECT().GetContext(ctx, &value, "select value").Run(func(_ context.Context, dest any, _ string, _ ...any) {
		assert.Zero(t, *dest.(*int))
		*dest.(*int) = 17
	}).Return(nil).Once()
	require.NoError(t, observed.GetContext(ctx, &value, "select value"))
	assert.Equal(t, 17, value)
	assert.Equal(t, 1, before)
	assert.Equal(t, 1, after)
}

func TestLostConnectionDoesNotReplayWritesOrTransactionalReads(t *testing.T) {
	ctx := context.Background()
	builder := mocksdb.NewBuilder(t)
	observed := &observedBuilder{CommonBuilder: builder, state: newConnectionState("primary", "mysql")}
	builder.EXPECT().ExecContext(ctx, "update users set votes = votes + 1").Return(nil, io.EOF).Once()
	_, err := observed.ExecContext(ctx, "update users set votes = votes + 1")
	require.ErrorIs(t, err, io.EOF)
	var value int
	builder.EXPECT().GetContext(ctx, &value, "select value").Return(io.EOF).Once()
	require.ErrorIs(t, observed.GetContext(ctx, &value, "select value"), io.EOF)
	observed.read = true
	builder.EXPECT().GetContext(ctx, &value, "insert returning id").Return(io.EOF).Once()
	require.ErrorIs(t, observed.GetContext(operationContext(ctx, observed, returningWriteOperation), &value, "insert returning id"), io.EOF)
}

func TestConnectionPretendUsesAnIsolatedSession(t *testing.T) {
	driver := mocksdriver.NewDriver(t)
	driver.EXPECT().Pool().Return(database.Pool{Writers: []database.Config{{Driver: "mysql", Connection: "primary"}}})
	driver.EXPECT().Grammar().Return(nil)
	logger := mockslogger.NewLogger(t)
	logger.On("Trace", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	connection, err := NewDB(context.Background(), nil, driver, logger, nil, nil)
	require.NoError(t, err)
	var observed []db.QueryExecuted
	connection.Listen(func(event db.QueryExecuted) { observed = append(observed, event) })
	events, err := connection.Pretend(func(db db.DB) error {
		_, err := db.Table("users").Insert(map[string]any{"name": "Alice"})
		if err != nil {
			return err
		}
		var rows []map[string]any
		return db.Table("users").Where("name", "Alice").Get(&rows)
	})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "insert into `users` (`name`) values (?)", events[0].SQL)
	assert.Equal(t, []any{"Alice"}, events[0].Bindings)
	assert.Equal(t, "select * from `users` where `name` = ?", events[1].SQL)
	assert.Len(t, observed, 2)
	assert.False(t, connection.state.pretend())
	assert.False(t, connection.HasModifiedRecords())
	assert.Empty(t, connection.GetQueryLog())
	assert.Zero(t, connection.TotalQueryDuration())
}

func TestPretendResolvesServerCapabilitiesBeforeTheCallback(t *testing.T) {
	driver := mocksdriver.NewDriver(t)
	driver.EXPECT().Pool().Return(database.Pool{Writers: []database.Config{{Driver: "mysql", Connection: "primary"}}})
	driver.EXPECT().Grammar().Return(nil)
	logger := mockslogger.NewLogger(t)
	logger.On("Trace", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	connection, err := NewDB(context.Background(), nil, driver, logger, nil, nil)
	require.NoError(t, err)
	inside, calls := false, 0
	connection.state.serverVersion = func(context.Context) (string, error) {
		assert.False(t, inside)
		calls++
		return "5.7.44", nil
	}
	events, err := connection.Pretend(func(child db.DB) error {
		inside = true
		version, err := child.ServerVersion()
		require.NoError(t, err)
		assert.Equal(t, "5.7.44", version)
		var rows []map[string]any
		return child.Table("users").GroupLimit(2, "team_id").Get(&rows)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	require.Len(t, events, 1)
	assert.Contains(t, events[0].SQL, "@laravel_row")
}

func TestConnectionStickyAndExplicitWriterRouting(t *testing.T) {
	state := newConnectionState("primary", "mysql")
	state.sticky = true
	reader := mocksdb.NewBuilder(t)
	writer := mocksdb.NewBuilder(t)
	read := &observedBuilder{CommonBuilder: reader, state: state, read: true, writer: func() (db.CommonBuilder, error) { return writer, nil }}
	write := &observedBuilder{CommonBuilder: writer, state: state}
	ctx := context.Background()
	var value int
	reader.EXPECT().GetContext(ctx, &value, "select value").Return(nil).Once()
	require.NoError(t, read.GetContext(ctx, &value, "select value"))
	result := &MockResult{}
	result.On("RowsAffected").Return(int64(1), nil)
	writer.EXPECT().ExecContext(ctx, "update values", 1).Return(result, nil).Once()
	_, err := write.ExecContext(ctx, "update values", 1)
	require.NoError(t, err)
	writer.EXPECT().GetContext(ctx, &value, "select value").Return(nil).Once()
	require.NoError(t, read.GetContext(ctx, &value, "select value"))
	child := state.newContext()
	assert.False(t, child.useWriter())
	assert.True(t, state.useWriter())
}

func TestConnectionBeforeHookStopsExecutionAndDurationFiresOnce(t *testing.T) {
	state := newConnectionState("primary", "mysql")
	connection := &DB{state: state}
	builder := mocksdb.NewBuilder(t)
	observed := &observedBuilder{CommonBuilder: builder, state: state}
	ctx := context.Background()
	var rows []int
	builder.EXPECT().SelectContext(ctx, &rows, "select value").Return(nil).Times(3)
	calls := 0
	connection.WhenQueryingForLongerThan(-time.Nanosecond, func(event db.QueryExecuted) { calls++ })
	require.NoError(t, observed.SelectContext(ctx, &rows, "select value"))
	require.NoError(t, observed.SelectContext(ctx, &rows, "select value"))
	assert.Equal(t, 1, calls)
	connection.AllowQueryDurationHandlersToRunAgain()
	require.NoError(t, observed.SelectContext(ctx, &rows, "select value"))
	assert.Equal(t, 2, calls)
	connection.BeforeExecuting(func(context.Context, string, []any) error { return errors.QueryOrderRequired })
	assert.ErrorIs(t, observed.SelectContext(ctx, &rows, "never executed"), errors.QueryOrderRequired)
}

func TestConnectionStateConcurrentAccounting(t *testing.T) {
	state := newConnectionState("primary", "mysql")
	state.logging = true
	const count = 32
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func(value int) { defer group.Done(); state.completed("select ?", []any{value}, time.Now(), false) }(i)
	}
	group.Wait()
	connection := &DB{state: state}
	assert.Len(t, connection.GetQueryLog(), count)
	assert.False(t, connection.HasModifiedRecords())
}
