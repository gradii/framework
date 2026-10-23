package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSqlServerUpdateJsonColumns(t *testing.T) {
	decimal := 456.789
	for _, test := range []struct {
		name    string
		column  string
		value   any
		wantSet string
		binding any
	}{
		{"string", "data->string", "updated", `[data] = json_modify([data], '$."string"', ?)`, "updated"},
		{"integer", "data->int", 789, `[data] = json_modify([data], '$."int"', ?)`, 789},
		{"pointer", "data->float", &decimal, `[data] = json_modify([data], '$."float"', ?)`, &decimal},
		{"boolean", "data->bool", false, `[data] = json_modify([data], '$."bool"', ?)`, false},
		{"array", "data->array", []string{"uvw", "xyz"}, `[data] = json_modify([data], '$."array"', json_query(?))`, `["uvw","xyz"]`},
		{"object", "data->nested", map[string]any{"name": "updated"}, `[data] = json_modify([data], '$."nested"', json_query(?))`, `{"name":"updated"}`},
		{"array element", "data->objects[0]->level", "changed", `[data] = json_modify([data], '$."objects"[0]."level"', ?)`, "changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := NewBuilder(NewSqlServerGrammar("")).From("users", "").Where("id", 9)
			statement, bindings := builder.ToUpdateSQL(map[string]any{test.column: test.value})
			require.NoError(t, builder.Error())
			assert.Equal(t, "update [users] set "+test.wantSet+" where [id] = ?", statement)
			assert.Equal(t, []any{test.binding, 9}, bindings)
		})
	}
}

func TestSqlServerUpdateJsonColumnsGroupsBindings(t *testing.T) {
	builder := NewBuilder(NewSqlServerGrammar("")).From("users", "").Where("id", 9)
	statement, bindings := builder.ToUpdateSQL(UpdateValues{
		{Column: "data->name", Value: "updated"},
		{Column: "label", Value: "other"},
		{Column: "data->nested->items", Value: []string{"a", "b"}},
		{Column: "meta->enabled", Value: false},
		{Column: "data->count", Value: Raw("1 + 1")},
	})
	require.NoError(t, builder.Error())
	assert.Equal(t, `update [users] set [data] = json_modify(json_modify(json_modify([data], '$."name"', ?), '$."nested"."items"', json_query(?)), '$."count"', 1 + 1), [label] = ?, [meta] = json_modify([meta], '$."enabled"', ?) where [id] = ?`, statement)
	assert.Equal(t, []any{"updated", `["a","b"]`, "other", false, 9}, bindings)
}
