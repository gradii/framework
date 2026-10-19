package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	querybuilder "github.com/goravel/framework/database/query"
	mockslogger "github.com/goravel/framework/mocks/database/logger"
)

// pdoRecordingDriver is a database/sql driver that records the statements it is asked to run, so a
// test can tell which of the two pools a read executed on without a database server behind it.
type pdoRecordingDriver struct {
	mu   sync.Mutex
	seen []string
}

func (d *pdoRecordingDriver) Open(string) (driver.Conn, error) {
	return &pdoRecordingConn{driver: d}, nil
}

func (d *pdoRecordingDriver) record(query string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.seen = append(d.seen, query)
}

func (d *pdoRecordingDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.seen)
}

type pdoRecordingConn struct{ driver *pdoRecordingDriver }

func (c *pdoRecordingConn) Prepare(query string) (driver.Stmt, error) {
	return &pdoRecordingStmt{driver: c.driver, query: query}, nil
}
func (c *pdoRecordingConn) Close() error              { return nil }
func (c *pdoRecordingConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

type pdoRecordingStmt struct {
	driver *pdoRecordingDriver
	query  string
}

func (s *pdoRecordingStmt) Close() error  { return nil }
func (s *pdoRecordingStmt) NumInput() int { return -1 }
func (s *pdoRecordingStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (s *pdoRecordingStmt) Query([]driver.Value) (driver.Rows, error) {
	s.driver.record(s.query)

	return pdoEmptyRows{}, nil
}

type pdoEmptyRows struct{}

func (pdoEmptyRows) Columns() []string         { return nil }
func (pdoEmptyRows) Close() error              { return nil }
func (pdoEmptyRows) Next([]driver.Value) error { return io.EOF }

type pdoRecordingConnector struct{ driver *pdoRecordingDriver }

func (c pdoRecordingConnector) Connect(context.Context) (driver.Conn, error) {
	return &pdoRecordingConn{driver: c.driver}, nil
}
func (c pdoRecordingConnector) Driver() driver.Driver { return c.driver }

func TestLockUsesWriteConnection(t *testing.T) {
	read := &pdoRecordingDriver{}
	write := &pdoRecordingDriver{}
	readPool := sql.OpenDB(pdoRecordingConnector{driver: read})
	writePool := sql.OpenDB(pdoRecordingConnector{driver: write})
	t.Cleanup(func() {
		_ = readPool.Close()
		_ = writePool.Close()
	})

	readBuilder, err := NewBuilder(readPool, "mysql")
	require.NoError(t, err)
	writeBuilder, err := NewBuilder(writePool, "mysql")
	require.NoError(t, err)

	logger := mockslogger.NewLogger(t)
	logger.EXPECT().Trace(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	query := NewQuery(context.Background(), readBuilder, writeBuilder, querybuilder.NewMySqlGrammar(""), querybuilder.DialectMySQL, logger, "users", nil)

	var first []TestUser
	require.NoError(t, query.LockForUpdate().Get(&first))
	assert.Equal(t, 0, read.count())
	assert.Equal(t, 1, write.count())

	var second []TestUser
	require.NoError(t, query.SharedLock().Get(&second))
	assert.Equal(t, 0, read.count())
	assert.Equal(t, 2, write.count())
}

// TestUseWritePdoUsesWriteConnection ports Laravel's testBasicSelectUseWritePdo at the db facade:
// a plain read runs on the reader pool, while UseWritePdo() forces the same read onto the writer.
func TestUseWritePdoUsesWriteConnection(t *testing.T) {
	read := &pdoRecordingDriver{}
	write := &pdoRecordingDriver{}
	readPool := sql.OpenDB(pdoRecordingConnector{driver: read})
	writePool := sql.OpenDB(pdoRecordingConnector{driver: write})
	t.Cleanup(func() {
		_ = readPool.Close()
		_ = writePool.Close()
	})

	readBuilder, err := NewBuilder(readPool, "mysql")
	require.NoError(t, err)
	writeBuilder, err := NewBuilder(writePool, "mysql")
	require.NoError(t, err)

	logger := mockslogger.NewLogger(t)
	logger.EXPECT().Trace(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	query := NewQuery(context.Background(), readBuilder, writeBuilder, querybuilder.NewMySqlGrammar(""), querybuilder.DialectMySQL, logger, "users", nil)

	// A plain get() reads through the reader.
	var plain []TestUser
	require.NoError(t, query.Get(&plain))
	assert.Equal(t, 1, read.count())
	assert.Equal(t, 0, write.count())

	// UseWritePdo()->get() forces the writer.
	var forced []TestUser
	require.NoError(t, query.UseWritePdo().Get(&forced))
	assert.Equal(t, 1, read.count())
	assert.Equal(t, 1, write.count())
}
