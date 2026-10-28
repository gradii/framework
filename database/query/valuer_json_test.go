package query

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/gradii/framework/database/types"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type boundJSONList []string

func (boundJSONList) Value() (driver.Value, error) { return `{"from":"Value"}`, nil }

type failedJSONValue struct{}

func (failedJSONValue) Value() (driver.Value, error) { return nil, assert.AnError }

func TestJSONValuersRemainAtomicForWholeColumns(t *testing.T) {
	for _, name := range []string{"mysql", "postgres", "sqlite", "sqlserver"} {
		t.Run(name, func(t *testing.T) {
			g, _ := newTestGrammar(name, "")
			for _, value := range []any{boundJSONList{"must not encode me"}, types.JSON(`{"n":1}`), types.JSONMap{"n": 1}} {
				b := NewBuilder(g).From("items", "")
				_, args := b.ToUpdateSQL(map[string]any{"data": value})
				require.NoError(t, b.Error())
				require.Len(t, args, 1)
				require.IsType(t, value, args[0])
				_, ok := args[0].(driver.Valuer)
				require.True(t, ok)
			}
		})
	}
}

func TestJSONPathUsesValuerDocumentOnce(t *testing.T) {
	for _, name := range []string{"mysql", "postgres", "sqlite", "sqlserver"} {
		t.Run(name, func(t *testing.T) {
			g, _ := newTestGrammar(name, "")
			b := NewBuilder(g).From("items", "")
			_, args := b.ToUpdateSQL(map[string]any{"data->nested": boundJSONList{"ignored"}})
			require.NoError(t, b.Error())
			require.Len(t, args, 1)
			expected := `{"from":"Value"}`
			if name == "sqlite" {
				expected = `{"nested":{"from":"Value"}}`
			}
			require.JSONEq(t, expected, args[0].(string))
			failed := NewBuilder(g).From("items", "")
			statement, args := failed.ToUpdateSQL(map[string]any{"data->nested": failedJSONValue{}})
			require.Empty(t, statement)
			require.Empty(t, args)
			require.ErrorIs(t, failed.Error(), assert.AnError)
		})
	}
}

func TestSQLServerJSONContainsRejectsCollectionsWithoutPanic(t *testing.T) {
	type object map[string]any
	for _, value := range []any{map[string]any{"active": true}, object{"active": true}, []any{map[string]any{"x": 1}}, [][]string{{"a"}}, []any{[]any{true}}, []any{Expression{Value: "1"}}} {
		b := NewBuilder(NewSqlServerGrammar("")).From("items", "")
		require.NotPanics(t, func() { b.WhereJsonContains("data", value, "and", false) })
		require.ErrorIs(t, b.Error(), errors.QueryJsonContainsValueNotSupported)
		require.Empty(t, b.State().Wheres)
		require.Empty(t, b.GetBindings())
	}
	input := []any{true, false, "writer", 3}
	b := NewBuilder(NewSqlServerGrammar("")).From("items", "").WhereJsonContains("data", input, "and", true)
	statement, args := b.ToSelectSQL()
	require.NoError(t, b.Error())
	require.Equal(t, []any{true, false, "writer", 3}, input)
	require.Equal(t, []any{"true", "false", "writer", 3}, args)
	require.Equal(t, len(args), strings.Count(statement, "?"))
	require.Contains(t, statement, "not (")
	for _, name := range []string{"mysql", "postgres"} {
		g, _ := newTestGrammar(name, "")
		other := NewBuilder(g).From("items", "").WhereJsonContains("data", types.JSON(`[true,false]`), "and", false)
		_, bindings := other.ToSelectSQL()
		require.NoError(t, other.Error())
		require.JSONEq(t, `[true,false]`, bindings[0].(string))
	}
}
