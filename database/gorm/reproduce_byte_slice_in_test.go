package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	contractsdriver "github.com/goravel/framework/contracts/database/driver"
)

// TestByteSliceBindsAsValue verifies that []byte is treated as a single value
// rather than expanded into an IN list.
//
// This reproduces the fault described in backlog item
// "treat-a-byte-slice-as-a-value-on-the-remaining-in-routes":
// Before fix: Where("note", []byte("ab")) rendered as `where "note" in (?, ?)`
//
//	with bindings [97, 98] (two separate bytes)
//
// After fix:  Where("note", []byte("ab")) renders as `where "note" = ?`
//
//	with binding []byte("ab") (one byte slice value)
func TestByteSliceBindsAsValue(t *testing.T) {
	// Test the database/gorm/sql.go applySingleWhere path (line 470)
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "note", Args: []any{[]byte("ab")}}},
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	// The SQL should use equality (=), not IN
	assert.Contains(t, sql, `"note" = ?`)
	assert.NotContains(t, sql, `"note" in`)

	// The binding should be a single []byte value, not two separate int values
	assert.Len(t, bindings, 1, "should bind exactly one value")
	bytesVal, ok := bindings[0].([]byte)
	assert.True(t, ok, "binding should be []byte type, got %T", bindings[0])
	assert.Equal(t, []byte("ab"), bytesVal)
}
