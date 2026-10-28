package db_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	sqlitedriver "github.com/gradii/sqlite"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/database/db"
	"github.com/gradii/framework/database/orm"
)

func TestQueryMapsDBTagsToStructs(t *testing.T) {
	type User struct {
		ID   string `db:"id"`
		Name string `db:"display_name" json:"name"`
	}
	for _, transaction := range []bool{false, true} {
		name := "connection"
		if transaction {
			name = "transaction"
		}
		t.Run(name, func(t *testing.T) {
			query := structMappingQuery(t, transaction, []string{"id", "display_name"}, [][]driver.Value{
				{int64(1), []byte("Alice")},
				{int64(2), "Bob"},
			})

			var users []User
			require.NoError(t, query.Get(&users))
			assert.Equal(t, []User{{ID: "1", Name: "Alice"}, {ID: "2", Name: "Bob"}}, users)

			var user User
			require.NoError(t, query.Where("id", 1).First(&user))
			assert.Equal(t, User{ID: "1", Name: "Alice"}, user)
		})
	}
}

func TestQueryMapsEmbeddedModels(t *testing.T) {
	type User struct {
		orm.Model
		orm.SoftDeletes
		Name string `db:"name"`
	}
	createdAt := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	deletedAt := createdAt.Add(time.Hour)
	for _, transaction := range []bool{false, true} {
		name := "connection"
		if transaction {
			name = "transaction"
		}
		t.Run(name, func(t *testing.T) {
			query := structMappingQuery(t, transaction,
				[]string{"id", "name", "created_at", "updated_at", "deleted_at"},
				[][]driver.Value{
					{int64(1), "Alice", createdAt, createdAt, nil},
					{int64(2), "Bob", nil, nil, deletedAt},
				})

			var users []User
			require.NoError(t, query.Get(&users))
			require.Len(t, users, 2)
			assert.Equal(t, uint(1), users[0].ID)
			assert.Equal(t, "Alice", users[0].Name)
			require.NotNil(t, users[0].CreatedAt)
			require.NotNil(t, users[0].UpdatedAt)
			assert.True(t, createdAt.Equal(users[0].CreatedAt.StdTime()))
			assert.True(t, createdAt.Equal(users[0].UpdatedAt.StdTime()))
			assert.False(t, users[0].DeletedAt.Valid)
			assert.Equal(t, uint(2), users[1].ID)
			assert.Equal(t, "Bob", users[1].Name)
			assert.Nil(t, users[1].CreatedAt)
			assert.Nil(t, users[1].UpdatedAt)
			assert.True(t, users[1].DeletedAt.Valid)
			assert.Equal(t, deletedAt, users[1].DeletedAt.Time)

			var user User
			require.NoError(t, query.Where("id", 1).First(&user))
			assert.Equal(t, users[0], user)
		})
	}
}

func TestQueryMapsEmbeddedPointers(t *testing.T) {
	type User struct {
		*orm.Model
		*orm.SoftDeletes
		Name string `db:"name"`
	}
	query := structMappingQuery(t, false,
		[]string{"id", "name", "created_at", "updated_at", "deleted_at"},
		[][]driver.Value{{int64(1), "Alice", nil, nil, nil}})

	var users []*User
	require.NoError(t, query.Get(&users))
	require.Len(t, users, 1)
	require.NotNil(t, users[0])
	require.NotNil(t, users[0].Model)
	require.NotNil(t, users[0].SoftDeletes)
	assert.Equal(t, uint(1), users[0].ID)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Nil(t, users[0].CreatedAt)
	assert.Nil(t, users[0].UpdatedAt)
	assert.False(t, users[0].DeletedAt.Valid)
}

func TestCursorPaginationMapsDBTags(t *testing.T) {
	type User struct {
		Key  uint   `db:"id"`
		Name string `db:"display_name"`
	}
	query := structMappingQuery(t, false, []string{"id", "display_name"}, [][]driver.Value{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})

	var users []User
	_, err := query.OrderBy("id").CursorPaginate(2, "", &users)
	require.NoError(t, err)
	assert.Equal(t, []User{{Key: 1, Name: "Alice"}, {Key: 2, Name: "Bob"}}, users)
}

func TestRowScanMapsDBTags(t *testing.T) {
	type Identity struct {
		ID uint `db:"user_id"`
	}
	type User struct {
		Identity
		Name    string `db:"display_name"`
		Ignored string `db:"-"`
	}
	user := User{Ignored: "keep"}
	err := db.NewRow(map[string]any{
		"user_id":      int64(1),
		"display_name": []byte("Alice"),
		"ignored":      "database value",
	}, nil).Scan(&user)
	require.NoError(t, err)
	assert.Equal(t, User{Identity: Identity{ID: 1}, Name: "Alice", Ignored: "keep"}, user)
}

// These fixtures exercise database/sql and sqlx scanning without a database server.
func structMappingQuery(t *testing.T, transaction bool, columns []string, values [][]driver.Value) *db.Query {
	t.Helper()
	pool := sql.OpenDB(&structMappingConnector{columns: columns, values: values})
	t.Cleanup(func() { assert.NoError(t, pool.Close()) })
	var builder contractsdb.CommonBuilder
	if transaction {
		tx, err := db.NewTxBuilder(pool, "sqlite", nil)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, tx.Rollback()) })
		builder = tx
	} else {
		var err error
		builder, err = db.NewBuilder(pool, "sqlite")
		require.NoError(t, err)
	}
	logs := []db.TxLog{}
	return db.NewQuery(context.Background(), builder, builder, sqlitedriver.NewQueryGrammar(""), "sqlite", nil, "users", &logs)
}

type structMappingConnector struct {
	columns []string
	values  [][]driver.Value
}

func (c *structMappingConnector) Connect(context.Context) (driver.Conn, error) {
	return structMappingConn{c}, nil
}
func (c *structMappingConnector) Driver() driver.Driver { return structMappingDriver{c} }

type structMappingDriver struct{ connector *structMappingConnector }

func (d structMappingDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type structMappingConn struct{ connector *structMappingConnector }

func (c structMappingConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c structMappingConn) Close() error                        { return nil }
func (c structMappingConn) Begin() (driver.Tx, error)           { return structMappingTx{}, nil }
func (c structMappingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &structMappingRows{columns: c.connector.columns, values: c.connector.values}, nil
}

type structMappingTx struct{}

func (structMappingTx) Commit() error   { return nil }
func (structMappingTx) Rollback() error { return nil }

type structMappingRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *structMappingRows) Columns() []string { return r.columns }
func (r *structMappingRows) Close() error      { return nil }
func (r *structMappingRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}
