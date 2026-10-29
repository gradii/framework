package processors

import (
	"testing"

	. "github.com/gradii/framework/database/query/processors"
	"github.com/stretchr/testify/assert"
)

func TestLaravelSchemaTableAndViewProcessing(t *testing.T) {
	processor := &BaseProcessor{}
	assert.Equal(t, []Row{{"name": "main", "path": nil, "default": true}}, processor.ProcessSchemas([]Row{{"name": "main", "default": 1}}))
	tables := processor.ProcessTables([]Row{{"name": "users", "schema": "public", "size": "42"}})
	assert.Equal(t, []Row{{"name": "users", "schema": "public", "schema_qualified_name": "public.users", "size": 42, "comment": nil, "collation": nil, "engine": nil}}, tables)
	assert.Equal(t, []Row{{"name": "users_view", "schema": nil, "schema_qualified_name": "users_view", "definition": "select * from users"}}, processor.ProcessViews([]Row{{"name": "users_view", "definition": "select * from users"}}))
}

func TestLaravelMetadataIndexesAndForeignKeys(t *testing.T) {
	sqlite := &SqliteProcessor{}
	indexes := sqlite.ProcessIndexes([]Row{
		{"name": "primary", "columns": "a,b", "primary": 1, "unique": 1},
		{"name": "sqlite_autoindex_users_1", "columns": "a,b", "primary": 1, "unique": 1},
	})
	assert.Len(t, indexes, 1)
	assert.Equal(t, "sqlite_autoindex_users_1", indexes[0]["name"])
	assert.Equal(t, []string{"a", "b"}, indexes[0]["columns"])
	assert.Nil(t, indexes[0]["type"])

	pg := &PostgresProcessor{}
	keys := pg.ProcessForeignKeys([]Row{{"name": "fk_users", "columns": "user_id,tenant_id", "foreign_schema": "public",
		"foreign_table": "users", "foreign_columns": "id,tenant_id", "on_update": "a", "on_delete": "c"}})
	assert.Equal(t, "no action", keys[0]["on_update"])
	assert.Equal(t, "cascade", keys[0]["on_delete"])
	assert.Equal(t, []string{"id", "tenant_id"}, keys[0]["foreign_columns"])

	types := pg.ProcessTypes([]Row{{"name": "state", "schema": "public", "type": "e", "category": "e", "implicit": false}})
	assert.Equal(t, []Row{{"name": "state", "schema": "public", "schema_qualified_name": "public.state", "type": "enum", "category": "enum", "implicit": false}}, types)
}
