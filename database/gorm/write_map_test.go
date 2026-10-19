package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A map handed to Create is keyed the way the caller wrote it -- a Go field name such as "Name" --
// while the statement needs the column ("name"). Only the struct path produced a column-keyed
// record, so a map's Go field name was sent as a column the table does not have and every dialect
// rejected the statement. The keys must resolve through the same mapping Select and Omit use, and a
// name the model does not declare must survive as written so a raw column still reaches the statement.
func TestInsertRecordsResolvesMapKeysToColumns(t *testing.T) {
	query := compiledQuery(t, &SchemaOuter{}, Conditions{})

	records, err := query.insertRecords(map[string]any{
		"Name":     "mapped",
		"ID":       7,
		"raw_expr": 1,
	})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "mapped", records[0]["name"], "a Go field name must resolve to its column")
		assert.Equal(t, 7, records[0]["id"])
		assert.Equal(t, 1, records[0]["raw_expr"], "an unknown key is left as written")
		_, hasGoName := records[0]["Name"]
		assert.False(t, hasGoName, "a Go field name must not survive as a column")
	}
}

// A slice of maps goes through the same resolution, and a table-qualified key names the model's own
// column.
func TestInsertRecordsResolvesMapKeysInSlicesAndQualifiedNames(t *testing.T) {
	query := compiledQuery(t, &SchemaOuter{}, Conditions{})

	records, err := query.insertRecords([]map[string]any{
		{"Name": "one", "schema_outers.ID": 1},
		{"Name": "two"},
	})
	assert.NoError(t, err)
	if assert.Len(t, records, 2) {
		assert.Equal(t, "one", records[0]["name"])
		assert.Equal(t, 1, records[0]["id"], "a qualified key must unwrap to its column")
		assert.Equal(t, "two", records[1]["name"])
	}
}

// A map with no model to resolve against is taken as written, which is what Table(...).Create with
// raw column keys relies on.
func TestInsertRecordsKeepsKeysWhenNoModelNamesColumns(t *testing.T) {
	query := compiledQuery(t, nil, Conditions{})

	records, err := query.insertRecords(map[string]any{"name": "raw", "avatar": "raw_avatar"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "raw", records[0]["name"])
		assert.Equal(t, "raw_avatar", records[0]["avatar"])
	}
}
