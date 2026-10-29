package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	querybuilder "github.com/gradii/framework/database/query"
	mocksdb "github.com/gradii/framework/mocks/database/db"
	mockslogger "github.com/gradii/framework/mocks/database/logger"
)

// insertBatchRow carries a pointer column, the shape a schema with a nullable column has.
type insertBatchRow struct {
	ID     uint   `db:"id"`
	Name   string `db:"name"`
	Weight *int   `db:"weight"`
}

// A batch insert of structs maps each struct to its own columns: a field left at its zero value is
// omitted, so the first record can lack a column a later one carries. The statement's column list is
// taken from the first record (as Laravel's compileInsert does), so without normalisation the later
// record's weight would never reach the statement and would read back as nil. This is the shape of
// DBTestSuite's "multiple structs" case (tests/db_test.go).
func TestBuildInsertKeepsColumnOnlyALaterRecordCarries(t *testing.T) {
	weight := 1

	records, err := convertToSliceMap([]insertBatchRow{
		{Name: "row1"},
		{Name: "row2", Weight: &weight},
	})
	assert.NoError(t, err)

	query := newDialectTestQuery(context.Background(), mocksdb.NewBuilder(t), mocksdb.NewBuilder(t), querybuilder.NewBaseGrammar(""), querybuilder.DialectMySQL, mockslogger.NewLogger(t), "products", nil)

	sql, args, err := query.buildInsert(records)
	assert.NoError(t, err)
	assert.Equal(t, "insert into \"products\" (\"name\", \"weight\") values (?, ?), (?, ?)", sql)
	assert.Equal(t, []any{"row1", nil, "row2", &weight}, args)
}

// A batch whose records already share their columns must compile exactly as before, so the fix changes
// nothing for the common case.
func TestBuildInsertHomogeneousBatchIsUnchanged(t *testing.T) {
	query := newDialectTestQuery(context.Background(), mocksdb.NewBuilder(t), mocksdb.NewBuilder(t), querybuilder.NewBaseGrammar(""), querybuilder.DialectMySQL, mockslogger.NewLogger(t), "products", nil)

	sql, args, err := query.buildInsert([]map[string]any{
		{"name": "row1", "weight": 1},
		{"name": "row2", "weight": 2},
	})
	assert.NoError(t, err)
	assert.Equal(t, "insert into \"products\" (\"name\", \"weight\") values (?, ?), (?, ?)", sql)
	assert.Equal(t, []any{"row1", 1, "row2", 2}, args)
}
