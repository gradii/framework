package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormio "gorm.io/gorm"
	gormschema "gorm.io/gorm/schema"

	contractsdatabase "github.com/goravel/framework/contracts/database"
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
	instance, err := gormio.Open(stubDialector{}, &gormio.Config{
		NamingStrategy: gormschema.NamingStrategy{TablePrefix: "goravel_"},
	})
	require.NoError(t, err)

	dbConfig := contractsdatabase.Config{Driver: "sqlite", Prefix: "goravel_"}

	// A model that declares its table: the FROM clause carries the prefix once.
	q := NewQuery(context.Background(), nil, dbConfig, instance, nil, nil, nil, &Conditions{model: &prefixContractModel{}}, nil)
	statement, _, err := q.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, statement, `from "goravel_prefix_contract"`)
	assert.NotContains(t, statement, "goravel_goravel_")

	// A column qualified by the same table: ident applies the prefix to the leading segment once.
	qualified, _, err := q.Where("prefix_contract.id", 1).(*Query).compileSelect()
	require.NoError(t, err)
	assert.Contains(t, qualified, `"goravel_prefix_contract"."id"`)
	assert.NotContains(t, qualified, "goravel_goravel_")

	// A caller-named table through Table() must be prefixed once as well.
	named := NewQuery(context.Background(), nil, dbConfig, instance, nil, nil, nil, nil, nil)
	namedStatement, _, err := named.Table("products").(*Query).compileSelect()
	require.NoError(t, err)
	assert.Contains(t, namedStatement, `from "goravel_products"`)
	assert.NotContains(t, namedStatement, "goravel_goravel_")
}
