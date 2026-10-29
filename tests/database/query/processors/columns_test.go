package processors

import (
	"testing"

	. "github.com/gradii/framework/database/query/processors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedColumnMetadata(t *testing.T) {
	mysql := (&MySqlProcessor{}).ProcessColumns([]Row{{
		"name": "total", "type_name": "int", "type": "int", "nullable": "NO",
		"extra": "STORED GENERATED", "expression": "price * quantity",
	}})
	assert.Equal(t, Row{"type": "stored", "expression": "price * quantity"}, mysql[0]["generation"])

	postgres := (&PostgresProcessor{}).ProcessColumns([]Row{{
		"name": "total", "type_name": "int4", "type": "integer", "nullable": false,
		"generated": "v", "default": "price * quantity",
	}})
	assert.Nil(t, postgres[0]["default"])
	assert.Equal(t, Row{"type": "virtual", "expression": "price * quantity"}, postgres[0]["generation"])
}

func TestSqliteColumnDefinitionsAndCompositeKeys(t *testing.T) {
	columns := (&SqliteProcessor{}).ProcessColumns([]Row{
		{"name": "name", "type": "varchar(100)", "nullable": 1, "primary": 0, "extra": 0},
		{"name": "with/slash", "type": "INTEGER", "nullable": 0, "primary": 0, "extra": 3},
	}, `create table items (name varchar(100) collate NOCASE, "with/slash" integer generated always as ((price + tax) * quantity) stored)`)
	require.Len(t, columns, 2)
	assert.Equal(t, "nocase", columns[0]["collation"])
	assert.Equal(t, Row{"type": "stored", "expression": "(price + tax) * quantity"}, columns[1]["generation"])

	keys := (&SqliteProcessor{}).ProcessColumns([]Row{
		{"name": "first", "type": "INTEGER", "primary": "1"},
		{"name": "second", "type": "INTEGER", "primary": "2"},
	})
	assert.Equal(t, false, keys[0]["auto_increment"])
	assert.Equal(t, false, keys[1]["auto_increment"])
}

func TestSqlServerColumnMetadata(t *testing.T) {
	columns := (&SqlServerProcessor{}).ProcessColumns([]Row{
		{"name": "text", "type_name": "nvarchar", "length": -1, "nullable": 1, "comment": ""},
		{"name": "amount", "type_name": "decimal", "precision": 8, "places": 2, "nullable": 0},
		{"name": "total", "type_name": "float", "precision": 53, "places": 0, "expression": "price * quantity", "persisted": 1},
		{"name": "created_at", "type_name": "datetime2", "precision": 23, "places": 3},
	})
	assert.Equal(t, "nvarchar(max)", columns[0]["type"])
	assert.Equal(t, true, columns[0]["nullable"])
	assert.Nil(t, columns[0]["comment"])
	assert.Equal(t, "decimal(8,2)", columns[1]["type"])
	assert.Equal(t, "float(53)", columns[2]["type"])
	assert.Equal(t, Row{"type": "stored", "expression": "price * quantity"}, columns[2]["generation"])
	assert.Equal(t, "datetime2(23)", columns[3]["type"])
}
