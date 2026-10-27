package db

import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"

	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/str"
)

// NameMapper maps a Go field name to its database column for sqlx, which scans rows into structs.
// Explicit db tags override this mapper, including fields of embedded structs.
var NameMapper = func(s string) string {
	return str.Of(s).Snake().String()
}

// Builder executes statements against a database pool. sqlx supplies the struct scanning and the
// transaction plumbing; the SQL itself is produced by database/query.
type Builder struct {
	*sqlx.DB
}

// sqlxDriverName reduces a connection's driver name to the string sqlx keys its bind-type lookup on.
//
// The four driver modules report display names ("PostgreSQL", "SQL Server") that sqlx does not know,
// so a recognised driver is replaced by its canonical dialect. SQLite is the exception: this
// package's canonical name for it is "sqlite", but sqlx keys its question-mark bind type on
// "sqlite3", so that dialect is handed over under sqlx's own name. A name the canonicaliser does not
// map is passed through unchanged: sqlx understands names of its own (for example "cloudsqlpostgres",
// "nrmysql", "godror" or "cockroach"), and an application may teach it more with sqlx.BindDriver, so
// collapsing an unrecognised name to "" would silently downgrade a working bind type to UNKNOWN.
func sqlxDriverName(driver string) string {
	switch canonical := querybuilder.CanonicalDialect(driver); canonical {
	case "":
		return driver
	case querybuilder.DialectSqlite:
		// sqlx spells SQLite "sqlite3"; handing it "sqlite" resolves to UNKNOWN.
		return "sqlite3"
	default:
		return canonical
	}
}

// NewBuilder wraps a pool for statement execution. driver is the connection's driver name; a
// recognised driver is reduced to the dialect string sqlx keys its bind-type lookup on.
func NewBuilder(pool *sql.DB, driver string) (*Builder, error) {
	if pool == nil {
		return nil, errors.DatabaseConnectionIsNil
	}

	// Unsafe mode lets sqlx scan rows into structs that do not map every column, which is the
	// normal case while a migration is adding columns.
	dbx := sqlx.NewDb(pool, sqlxDriverName(driver)).Unsafe()
	dbx.MapperFunc(NameMapper)

	return &Builder{DB: dbx}, nil
}

// Explain renders a statement with its arguments interpolated, which is what ToRawSql reports.
func (r *Builder) Explain(sql string, args ...any) string {
	return interpolate(sql, args)
}

// Explain writes the arguments into a statement's placeholders. It is the same rendering the builders
// use for the raw form of a query, exposed so a caller holding only compiled SQL can report it that way
// too.
func Explain(statement string, args ...any) string {
	return interpolate(statement, args)
}

// TxBuilder executes statements inside a transaction.
type TxBuilder struct {
	*sqlx.Tx
}

// NewTxBuilder opens a transaction on the given pool.
func NewTxBuilder(pool *sql.DB, driver string, options *sql.TxOptions) (*TxBuilder, error) {
	return NewTxBuilderWithContext(context.Background(), pool, driver, options)
}

func NewTxBuilderWithContext(ctx context.Context, pool *sql.DB, driver string, options *sql.TxOptions) (*TxBuilder, error) {
	if pool == nil {
		return nil, errors.DatabaseConnectionIsNil
	}

	dbx := sqlx.NewDb(pool, sqlxDriverName(driver))
	dbx.MapperFunc(NameMapper)

	tx, err := dbx.BeginTxx(ctx, options)
	if err != nil {
		return nil, err
	}

	return &TxBuilder{Tx: tx}, nil
}

// Explain renders a statement with its arguments interpolated, which is what ToRawSql reports.
func (r *TxBuilder) Explain(sql string, args ...any) string {
	return interpolate(sql, args)
}
