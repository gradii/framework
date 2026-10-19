package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the PORTABLE wheres rows of the Laravel port plan: testBasicWhereNot and
// testWheresWithArrayValue. Both build the chain the corpus builds and pin the corpus SQL verbatim on
// the base grammar, so every identifier is double quoted and every value is a "?" placeholder.

// TestBasicWhereNot ports Laravel's testBasicWhereNot (corpus L335-341). whereNot carries its
// negation in the connector ("and not"), and CompileWheres strips the leading connector from the
// first clause, so the negated equality compiles to `not "name" = ?` and not `not "name" <> ?`.
func TestBasicWhereNot(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"whereNot twice", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNot("name", "foo").WhereNot("name", "<>", "bar")
		}, `select * from "users" where not "name" = ? and not "name" <> ?`},
	})

	// The corpus asserts the bindings too ('foo', 'bar'); runLaravelCases only compares SQL.
	b := newTestBuilder().Select("*").From("users", "").WhereNot("name", "foo").WhereNot("name", "<>", "bar")

	assert.Equal(t, []any{"foo", "bar"}, b.GetBindings())
}

// TestWheresWithArrayValue ports Laravel's testWheresWithArrayValue (corpus L343-367). A where whose
// value is a list renders a single placeholder and binds only the first element; the corpus asserts
// that for a list of one, a list of two, a list nested one level deeper, and the != / <> operators.
// laravelCase carries the SQL expectation, so each row embeds it and adds the binding expectation.
func TestWheresWithArrayValue(t *testing.T) {
	cases := []struct {
		laravelCase
		bindings []any
	}{
		{laravelCase{"single element list", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", []any{12})
		}, `select * from "users" where "id" = ?`}, []any{12}},
		{laravelCase{"two element list", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", []any{12, 30})
		}, `select * from "users" where "id" = ?`}, []any{12}},
		{laravelCase{"not equal two element list", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "!=", []any{12, 30})
		}, `select * from "users" where "id" != ?`}, []any{12}},
		{laravelCase{"not equal operator two element list", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "<>", []any{12, 30})
		}, `select * from "users" where "id" <> ?`}, []any{12}},
		// PHP's [[12, 30]]: the value is flattened into its first element until a scalar is reached.
		{laravelCase{"nested list", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", []any{[]any{12, 30}})
		}, `select * from "users" where "id" = ?`}, []any{12}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			b := test.build(newTestBuilder())

			assert.Equal(t, test.want, b.ToSQL())
			assert.Equal(t, test.bindings, b.GetBindings())
		})
	}
}
