package gorm

import (
	"context"
	"testing"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prefixContractModel declares its own table, so the declared-name branch of tableNameFor is the one
// under test.
type prefixContractModel struct {
	ID uint
}

func (prefixContractModel) TableName() string { return "prefix_contract" }

// TestTablePrefixAppliedOnce pins the contract that a table name held in the ORM is bare and the
// connection prefix is applied exactly once, at render time.
//
// Before the fix tableNameFor and Table() prefixed the name themselves while the framework grammar's
// WrapTable prefixed it again, so every statement addressed "goravel_goravel_<table>" and failed with
// "table does not exist" (Postgres SQLSTATE 42P01, SQLite "no such table").
func TestTablePrefixAppliedOnce(t *testing.T) {
	dbConfig := contractsdatabase.Config{Driver: "sqlite", Prefix: "goravel_"}

	// A model that declares its table: the FROM clause carries the prefix once.
	q := newDialectQuery(context.Background(), nil, dbConfig, nil, nil, nil, nil, nil, &Conditions{model: &prefixContractModel{}}, nil)
	statement, _, err := q.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, statement, `from "goravel_prefix_contract"`)
	assert.NotContains(t, statement, "goravel_goravel_")

	// A column qualified by the same table: ident applies the prefix to the leading segment once.
	qualified, _, err := q.Where("prefix_contract.id", 1).(*OrmBuilder).compileSelect()
	require.NoError(t, err)
	assert.Contains(t, qualified, `"goravel_prefix_contract"."id"`)
	assert.NotContains(t, qualified, "goravel_goravel_")

	// A caller-named table through Table() must be prefixed once as well.
	named := newDialectQuery(context.Background(), nil, dbConfig, nil, nil, nil, nil, nil, nil, nil)
	namedStatement, _, err := named.Table("products").(*OrmBuilder).compileSelect()
	require.NoError(t, err)
	assert.Contains(t, namedStatement, `from "goravel_products"`)
	assert.NotContains(t, namedStatement, "goravel_goravel_")
}

// TestTableAcceptsAnAlreadyQualifiedName pins the entry point a caller holding a catalog name needs.
//
// A string names a bare table and the connection prefix is applied when the statement is rendered.
// An expression names a table that is already qualified — a database view's catalog name, for
// instance — and is rendered verbatim, exactly as Laravel's Grammar::wrapTable() returns an
// Expression's value untouched, which is what Connection::withoutTablePrefix achieves by blanking
// the prefix around the callback. Stripping the prefix by hand, which the
// db:show command used to do, cannot reproduce it: a catalog name that does not begin with the
// prefix went unstripped and was then prefixed anyway.
func TestTableAcceptsAnAlreadyQualifiedName(t *testing.T) {
	dbConfig := contractsdatabase.Config{Driver: "sqlite", Prefix: "goravel_"}

	// A catalog name that does not start with the prefix still renders as it was given.
	qualified := newDialectQuery(context.Background(), nil, dbConfig, nil, nil, nil, nil, nil, nil, nil).Table(querybuilder.Raw("external_view")).(*OrmBuilder)
	statement, _, err := qualified.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, statement, "from external_view")
	assert.NotContains(t, statement, "goravel_external_view")

	// A catalog name that does carry the prefix is not prefixed a second time.
	prefixed := newDialectQuery(context.Background(), nil, dbConfig, nil, nil, nil, nil, nil, nil, nil).Table(querybuilder.Raw("goravel_view")).(*OrmBuilder)
	prefixedStatement, _, err := prefixed.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, prefixedStatement, "from goravel_view")
	assert.NotContains(t, prefixedStatement, "goravel_goravel_view")

	// A string is still a bare name: the prefix applies exactly once, as before.
	bare := newDialectQuery(context.Background(), nil, dbConfig, nil, nil, nil, nil, nil, nil, nil).Table("products").(*OrmBuilder)
	bareStatement, _, err := bare.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, bareStatement, `from "goravel_products"`)

	// A write through the expression reads the table back for schema resolution.
	assert.Equal(t, "external_view", qualified.declaredTable())
	assert.Equal(t, "products", bare.declaredTable())
}
