package query

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/database/types"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countedJSONValue struct {
	Calls     *int
	ValueText driver.Value
	Internal  func()
}

func (v countedJSONValue) Value() (driver.Value, error) {
	*v.Calls++
	return v.ValueText, nil
}

func TestJSONPathPreparesTypedCollectionsAndKeepsOrderedInput(t *testing.T) {
	type labels map[string]string
	type flags []bool
	for _, name := range []string{"mysql", "mariadb", "postgres", "sqlite", "sqlserver"} {
		t.Run(name, func(t *testing.T) {
			grammar, _ := newTestGrammar(name, "")
			for _, value := range []any{labels{"label": "雪"}, flags{true, false}, &labels{"label": "雪"}} {
				builder := NewBuilder(grammar).From("items", "")
				statement, bindings := builder.ToUpdateSQL(core.UpdateValues{{Column: "data->value", Value: value}})
				require.NoError(t, builder.Error())
				require.Len(t, bindings, 1)
				encoded, err := json.Marshal(value)
				require.NoError(t, err)
				expected := string(encoded)
				if name == "sqlite" {
					expected = `{"value":` + expected + `}`
				}
				assert.JSONEq(t, expected, bindings[0].(string))
				if name == "mysql" {
					assert.Contains(t, statement, "cast(? as json)")
				}
				if name == "mariadb" {
					assert.Contains(t, statement, "json_query(?, '$')")
				}
			}
			calls := 0
			value := countedJSONValue{Calls: &calls, ValueText: []byte(`{"label":"from Value"}`), Internal: func() {}}
			input := core.UpdateValues{{Column: "data->value", Value: value}}
			builder := NewBuilder(grammar).From("items", "")
			_, _ = builder.ToUpdateSQL(input)
			require.NoError(t, builder.Error())
			assert.Equal(t, 1, calls)
			assert.IsType(t, value, input[0].Value, "compilation must not replace the caller's Valuer")
		})
	}
}

func TestJSONPathNumberDocumentsKeepTheirNumericType(t *testing.T) {
	for _, name := range []string{"mysql", "mariadb", "postgres", "sqlite", "sqlserver"} {
		t.Run(name, func(t *testing.T) {
			grammar, _ := newTestGrammar(name, "")
			builder := NewBuilder(grammar).From("items", "")
			statement, bindings := builder.ToUpdateSQL(map[string]any{"data->n": types.JSON(`9007199254740993`)})
			require.NoError(t, builder.Error())
			require.Len(t, bindings, 1)
			switch name {
			case "mysql", "mariadb":
				assert.Contains(t, statement, "json_extract(?, '$')")
				assert.Equal(t, json.Number("9007199254740993"), bindings[0])
			case "sqlite":
				assert.JSONEq(t, `{"n":9007199254740993}`, bindings[0].(string))
			case "postgres":
				assert.Equal(t, "9007199254740993", bindings[0])
			case "sqlserver":
				assert.Contains(t, statement, "cast(? as decimal(38, 0))")
				assert.Equal(t, "9007199254740993", bindings[0])
			}
		})
	}
}

func TestExplicitJSONOperationsAcceptPlainSQLTextValuers(t *testing.T) {
	for _, name := range []string{"mysql", "postgres", "sqlite", "sqlserver"} {
		grammar, _ := newTestGrammar(name, "")
		for _, value := range []any{sql.NullString{String: "plain text", Valid: true}, []byte("plain text")} {
			builder := NewBuilder(grammar).From("items", "")
			_, bindings := builder.ToUpdateSQL(map[string]any{"data->label": value})
			require.NoError(t, builder.Error())
			switch name {
			case "postgres":
				assert.Equal(t, []any{`"plain text"`}, bindings)
			case "sqlite":
				assert.Equal(t, []any{`{"label":"plain text"}`}, bindings)
			default:
				assert.Equal(t, []any{"plain text"}, bindings)
			}
		}
		for _, value := range []any{types.JSON(`{invalid`), failedJSONValue{}} {
			builder := NewBuilder(grammar).From("items", "")
			statement, bindings := builder.ToUpdateSQL(map[string]any{"data->label": value})
			require.Error(t, builder.Error())
			assert.Empty(t, statement)
			assert.Empty(t, bindings)
		}
	}
}

func TestJSONContainsValuersFollowMembershipShapes(t *testing.T) {
	for _, name := range []string{"sqlite", "sqlserver"} {
		t.Run(name, func(t *testing.T) {
			grammar, _ := newTestGrammar(name, "")
			for _, value := range []any{types.JSON(`{"x":1}`), types.JSON(`[[1]]`), types.JSON(`[{}]`), map[int]string{1: "x"}} {
				builder := NewBuilder(grammar).From("items", "").WhereJsonContains("data", value, "and", false)
				require.ErrorIs(t, builder.Error(), errors.QueryJsonContainsValueNotSupported)
				assert.Empty(t, builder.State().Wheres)
				assert.Empty(t, builder.GetBindings())
			}
			for _, value := range []any{math.Inf(1), math.NaN(), json.Number("not a number"), failedJSONValue{}} {
				builder := NewBuilder(grammar).From("items", "").WhereJsonContains("data", value, "and", false)
				require.Error(t, builder.Error())
				assert.Empty(t, builder.State().Wheres)
			}
			for _, text := range []driver.Value{"writer", `"writer"`, []byte(`"writer"`)} {
				calls := 0
				value := countedJSONValue{Calls: &calls, ValueText: text, Internal: func() {}}
				builder := NewBuilder(grammar).From("items", "").WhereJsonContains("data", []any{value}, "and", false)
				statement, bindings := builder.ToSelectSQL()
				require.NoError(t, builder.Error())
				assert.Equal(t, 1, calls)
				assert.Equal(t, []any{"writer"}, bindings)
				assert.Equal(t, 1, strings.Count(statement, "?"))
			}
			builder := NewBuilder(grammar).From("items", "").WhereJsonContains("data", sql.NullString{String: "writer", Valid: true}, "and", false)
			require.NoError(t, builder.Error())
			assert.Equal(t, []any{"writer"}, builder.GetBindings())
			builder = NewBuilder(grammar).From("items", "").WhereJsonContains("data", types.JSON(`[true,"writer",3]`), "and", true)
			statement, bindings := builder.ToSelectSQL()
			require.NoError(t, builder.Error())
			assert.Contains(t, statement, "not (")
			assert.Equal(t, len(bindings), strings.Count(statement, "?"))
			if name == "sqlite" {
				assert.Equal(t, []any{true, "writer", int64(3)}, bindings)
			} else {
				assert.Equal(t, []any{"true", "writer", json.Number("3")}, bindings)
			}
		})
	}
}

func TestJSONValuerBindingsAreNotFlattenedByOrdinaryWhere(t *testing.T) {
	for _, value := range []any{boundJSONList{"x", "y"}, types.JSON(`{"n":1}`)} {
		builder := newTestBuilder().From("items", "").Where("data", value)
		statement, bindings := builder.ToSelectSQL()
		require.NoError(t, builder.Error())
		assert.Equal(t, 1, strings.Count(statement, "?"))
		require.Len(t, bindings, 1)
		assert.Equal(t, value, bindings[0])
	}
}

func TestJSONPathGroupingDoesNotMutateAnAssignedObject(t *testing.T) {
	object := map[string]any{"original": true}
	builder := NewBuilder(NewSqliteGrammar("")).From("items", "")
	_, bindings := builder.ToUpdateSQL(core.UpdateValues{
		{Column: "data->object", Value: object},
		{Column: "data->object->added", Value: "new"},
	})
	require.NoError(t, builder.Error())
	assert.Equal(t, map[string]any{"original": true}, object)
	assert.Equal(t, []any{`{"object":{"added":"new","original":true}}`}, bindings)

	var nilObject map[string]any
	builder = NewBuilder(NewSqliteGrammar("")).From("items", "")
	_, bindings = builder.ToUpdateSQL(core.UpdateValues{
		{Column: "data->object", Value: nilObject},
		{Column: "data->object->added", Value: "new"},
	})
	require.NoError(t, builder.Error())
	assert.Nil(t, nilObject)
	assert.Equal(t, []any{`{"object":{"added":"new"}}`}, bindings)
}

func TestJSONOverlapAndUpdateFromShareValuerPreparation(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).From("items", "").WhereJsonOverlaps("data", boundJSONList{"ignored"}, "and", false)
	_, bindings := builder.ToSelectSQL()
	require.NoError(t, builder.Error())
	assert.Equal(t, []any{`{"from":"Value"}`}, bindings)
	builder = NewBuilder(NewPostgresGrammar("")).From("items", "")
	_, bindings = builder.ToUpdateFromSQL(map[string]any{"data->nested": boundJSONList{"ignored"}})
	require.NoError(t, builder.Error())
	assert.Equal(t, []any{`{"from":"Value"}`}, bindings)
	for _, name := range []string{"postgres", "sqlite"} {
		grammar, _ := newTestGrammar(name, "")
		subquery := NewBuilder(grammar).From("source", "").Select("value").Where("id", 17)
		builder := NewBuilder(grammar).From("items", "")
		statement, bindings := builder.ToUpdateSQL(core.UpdateValues{{Column: "data->nested", Value: subquery}})
		require.NoError(t, builder.Error())
		assert.Contains(t, statement, "select")
		assert.Equal(t, []any{17}, bindings)
	}
}
