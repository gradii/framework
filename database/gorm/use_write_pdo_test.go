package gorm

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	databasedriver "github.com/goravel/framework/database/driver"
	mocksconfig "github.com/goravel/framework/mocks/config"
)

// TestSelectWithLockUsesWritePdo ports Laravel's testSelectWithLockUsesWritePdo: a select that
// carries a lock must run on the write connection even though a plain select runs on the replica.
// Laravel implements it inside Builder::lock(), which calls useWritePdo() for any non-null lock, so
// both lockForUpdate() and sharedLock() force the writer. Routing is observed the same way
// TestBasicSelectUseWritePdo observes it: the recording pools report which one saw the statement.
func TestSelectWithLockUsesWritePdo(t *testing.T) {
	t.Cleanup(databasedriver.CloseConnections)

	writer := &recordingSQLDriver{}
	reader := &recordingSQLDriver{}
	writerPool := sql.OpenDB(recordingConnector{driver: writer})
	readerPool := sql.OpenDB(recordingConnector{driver: reader})
	require.NotSame(t, writerPool, readerPool)

	config := mocksconfig.NewConfig(t)
	config.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Times(2)
	config.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)

	pool := contractsdatabase.Pool{
		Writers: []contractsdatabase.Config{{Driver: "mysql", Database: "app", Dialector: poolRecordingDialector{pool: writerPool}}},
		Readers: []contractsdatabase.Config{{Driver: "mysql", Database: "replica", Dialector: poolRecordingDialector{pool: readerPool}}},
	}

	conn, err := databasedriver.Open(config, discardQueryLogger{}, pool, "lock_write_pdo", nil)
	require.NoError(t, err)
	require.NotNil(t, conn)

	query := NewQuery(context.Background(), config,
		contractsdatabase.Config{Driver: "mysql", Connection: "lock_write_pdo"},
		conn.Gorm(), nil, nil, nil, &Conditions{model: &getColumnsUser{}}, nil)

	// A plain get() reads through the replica, so the reader pool is the one exercised.
	var plain []getColumnsUser
	assert.NoError(t, query.Get(&plain))
	assert.Equal(t, 1, reader.count())
	assert.Equal(t, 0, writer.count())

	// lockForUpdate() forces the write connection.
	var first []getColumnsUser
	assert.NoError(t, query.LockForUpdate().Get(&first))
	assert.Equal(t, 1, writer.count())
	assert.Equal(t, 1, reader.count())

	// sharedLock() forces it too: Laravel's lock(false) is still a non-null lock.
	var second []getColumnsUser
	assert.NoError(t, query.SharedLock().Get(&second))
	assert.Equal(t, 2, writer.count())
	assert.Equal(t, 1, reader.count())

	// useWritePdo() forces it for a query that carries no lock.
	var third []getColumnsUser
	assert.NoError(t, query.UseWritePdo().Get(&third))
	assert.Equal(t, 3, writer.count())
	assert.Equal(t, 1, reader.count())
}
