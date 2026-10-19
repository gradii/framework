package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// builderStubConnector lets the builder constructors run without a real server. Connect hands back a
// connection whose Begin succeeds, so the transaction builder can be exercised too.
type builderStubConnector struct{}

func (builderStubConnector) Connect(context.Context) (driver.Conn, error) {
	return builderStubConn{}, nil
}
func (builderStubConnector) Driver() driver.Driver { return builderStubDriver{} }

type builderStubDriver struct{}

func (builderStubDriver) Open(string) (driver.Conn, error) { return builderStubConn{}, nil }

type builderStubConn struct{}

func (builderStubConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrBadConn }
func (builderStubConn) Close() error                        { return nil }
func (builderStubConn) Begin() (driver.Tx, error)           { return builderStubTx{}, nil }

type builderStubTx struct{}

func (builderStubTx) Commit() error   { return nil }
func (builderStubTx) Rollback() error { return nil }

// sqlxBindCases are the four recognised drivers, named as the driver modules report them (those
// display names are the strings that produced the original defect), paired with the bind type sqlx
// must resolve for each.
var sqlxBindCases = []struct {
	driver   string
	expected int
}{
	{"PostgreSQL", sqlx.DOLLAR},
	{"SQL Server", sqlx.AT},
	{"MySQL", sqlx.QUESTION},
	{"SQLite", sqlx.QUESTION},
}

// TestNewBuilder_UsesCanonicalDialectForBinding pins the driver string handed to sqlx. sqlx's
// BindType is an exact map lookup, so the module display names ("PostgreSQL", "SQL Server")
// resolve to UNKNOWN while the canonical dialect keys resolve to the dialect's bind style.
//
// The assertion is on the resolved bind type, not on a Rebind("?") output: Rebind returns the query
// unchanged for both UNKNOWN and QUESTION, so asserting its output would still pass if SQLite were
// resolved to UNKNOWN.
func TestNewBuilder_UsesCanonicalDialectForBinding(t *testing.T) {
	pool := sql.OpenDB(builderStubConnector{})
	defer pool.Close()

	for _, tc := range sqlxBindCases {
		builder, err := NewBuilder(pool, tc.driver)
		require.NoError(t, err, "driver %q", tc.driver)
		assert.Equal(t, tc.expected, sqlx.BindType(builder.DriverName()), "driver %q", tc.driver)
	}
}

// TestNewTxBuilder_UsesCanonicalDialectForBinding covers the transaction builder, which shares the
// same driver string with sqlx.
func TestNewTxBuilder_UsesCanonicalDialectForBinding(t *testing.T) {
	pool := sql.OpenDB(builderStubConnector{})
	defer pool.Close()

	for _, tc := range sqlxBindCases {
		tx, err := NewTxBuilder(pool, tc.driver)
		require.NoError(t, err, "driver %q", tc.driver)
		assert.Equal(t, tc.expected, sqlx.BindType(tx.DriverName()), "driver %q", tc.driver)
		require.NoError(t, tx.Rollback())
	}
}
