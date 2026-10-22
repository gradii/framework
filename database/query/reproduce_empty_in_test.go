package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEmptyInSliceConsistency reproduces the defect: an empty IN slice compiles to "column IN ()"
// on the WhereAny path but "0 = 1" on the WhereIn path.
func TestEmptyInSliceConsistency(t *testing.T) {
	t.Run("empty IN via WhereIn produces 0 = 1", func(t *testing.T) {
		b := NewBuilder(NewBaseGrammar(""))
		b.From("users", "").WhereIn("name", []any{}, BooleanAnd, false)

		sql := b.ToSQL()
		bindings := b.GetBindings()
		assert.Contains(t, sql, "0 = 1", "WhereIn with empty slice should produce '0 = 1'")
		assert.Nil(t, bindings, "Empty IN should bind nothing")
	})

	t.Run("empty NOT IN via WhereNotIn produces 1 = 1", func(t *testing.T) {
		b := NewBuilder(NewBaseGrammar(""))
		b.From("users", "").WhereNotIn("name", []any{}, BooleanAnd)

		sql := b.ToSQL()
		bindings := b.GetBindings()
		assert.Contains(t, sql, "1 = 1", "WhereNotIn with empty slice should produce '1 = 1'")
		assert.Nil(t, bindings, "Empty NOT IN should bind nothing")
	})

	t.Run("empty IN via WhereAny produces malformed column IN ()", func(t *testing.T) {
		b := NewBuilder(NewBaseGrammar(""))
		b.From("users", "").WhereAny([]any{"name"}, "IN", []any{}, BooleanAnd)

		sql := b.ToSQL()
		bindings := b.GetBindings()
		t.Logf("SQL: %s", sql)
		t.Logf("Bindings: %v", bindings)

		// This should produce "0 = 1" like WhereIn, but instead produces malformed "name IN ()"
	})

	t.Run("empty NOT IN via WhereAny produces malformed column NOT IN ()", func(t *testing.T) {
		b := NewBuilder(NewBaseGrammar(""))
		b.From("users", "").WhereAny([]any{"name"}, "NOT IN", []any{}, BooleanAnd)

		sql := b.ToSQL()
		bindings := b.GetBindings()
		t.Logf("SQL: %s", sql)
		t.Logf("Bindings: %v", bindings)

		// This should produce "1 = 1" like WhereNotIn, but instead produces malformed "name NOT IN ()"
	})
}
