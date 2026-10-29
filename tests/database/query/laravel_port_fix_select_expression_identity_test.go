package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Laravel v12.55.1 oracle: each selectRaw creates a new Expression, so identical SQL keeps both
// placeholders and the select bindings precede the where binding.
func TestSelectRawKeepsRepeatedExpressionsAndBindings(t *testing.T) {
	builder := NewBuilder(NewPostgresGrammar("")).From("users", "").
		SelectRaw("coalesce(a, ?)", []any{1}).
		SelectRaw("coalesce(a, ?)", []any{2}).
		Where("id", 9)

	assert.Equal(t, `select coalesce(a, ?), coalesce(a, ?) from "users" where "id" = ?`, builder.ToSQL())
	assert.Equal(t, []any{1, 2, 9}, builder.GetBindings())
	require.NoError(t, builder.Error())
}

// Laravel's strict comparison matches Expression objects by identity. A Go value expression has
// no object identity, so each occurrence represents a separate Laravel Expression.
func TestAddSelectComparesExpressionIdentity(t *testing.T) {
	t.Run("value expressions with equal SQL", func(t *testing.T) {
		builder := newTestBuilder().From("users", "").AddSelect(Raw("x"), Raw("x"))
		assert.Equal(t, `select x, x from "users"`, builder.ToSQL())
	})

	t.Run("same expression pointer", func(t *testing.T) {
		expression := &Expression{Value: "x"}
		builder := newTestBuilder().From("users", "").AddSelect(expression).AddSelect(expression)
		assert.Equal(t, `select x from "users"`, builder.ToSQL())
	})

	t.Run("distinct expression pointers with equal SQL", func(t *testing.T) {
		builder := newTestBuilder().From("users", "").AddSelect(&Expression{Value: "x"}, &Expression{Value: "x"})
		assert.Equal(t, `select x, x from "users"`, builder.ToSQL())
	})

	t.Run("uncomparable values are appended", func(t *testing.T) {
		for _, value := range []any{
			[]string{"x"},
			map[string]string{"column": "x"},
			func() {},
			struct{ Value any }{Value: []string{"x"}},
		} {
			builder := newTestBuilder()
			require.NotPanics(t, func() { builder.AddSelect(value, value) })
			assert.Len(t, builder.Selects(), 2)
		}
	})
}
