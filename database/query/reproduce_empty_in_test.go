package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// TestEmptyInSliceConsistency tests that empty IN slices produce consistent SQL across all paths.
func TestEmptyInSliceConsistency(t *testing.T) {
	t.Run("WhereIn with empty slice produces 0 = 1", func(t *testing.T) {
		b := newTestBuilder()
		b.From("users", "").WhereIn("name", []any{}, core.BooleanAnd, false)

		sql, bindings := b.ToSQL(), b.GetBindings()
		t.Logf("SQL: %s", sql)
		t.Logf("Bindings: %v", bindings)
		t.Logf("Wheres: %+v", b.State().Wheres)

		assert.Contains(t, sql, "0 = 1", "WhereIn with empty slice should produce '0 = 1'")
		assert.Nil(t, bindings, "Empty IN should bind nothing")
	})

	t.Run("WhereNotIn with empty slice produces 1 = 1", func(t *testing.T) {
		b := newTestBuilder()
		b.From("users", "").WhereNotIn("name", []any{}, core.BooleanAnd)

		sql, bindings := b.ToSQL(), b.GetBindings()
		t.Logf("SQL: %s", sql)
		t.Logf("Bindings: %v", bindings)
		t.Logf("Wheres: %+v", b.State().Wheres)

		assert.Contains(t, sql, "1 = 1", "WhereNotIn with empty slice should produce '1 = 1'")
		assert.Nil(t, bindings, "Empty NOT IN should bind nothing")
	})

	t.Run("WhereAny with empty IN slice via string operator", func(t *testing.T) {
		b := newTestBuilder()
		b.From("users", "").WhereAny([]any{"name", "email"}, "IN", []any{}, core.BooleanAnd)

		sql, bindings := b.ToSQL(), b.GetBindings()
		t.Logf("SQL: %s", sql)
		t.Logf("Bindings: %v", bindings)
		t.Logf("Wheres: %+v", b.State().Wheres)
		if len(b.State().Wheres) > 0 && b.State().Wheres[0].Query != nil {
			t.Logf("Nested wheres: %+v", b.State().Wheres[0].Query.State().Wheres)
		}

		// The bug: this might produce malformed "name IN ()" instead of "0 = 1"
		assert.NotContains(t, sql, "in ()", "WhereAny with empty IN should not produce malformed 'in ()'")
		assert.NotContains(t, sql, "IN ()", "WhereAny with empty IN should not produce malformed 'IN ()'")
	})

	t.Run("WhereAny with empty IN slice via uppercase IN", func(t *testing.T) {
		b := newTestBuilder()
		b.From("users", "").WhereAny([]any{"name", "email"}, "IN", []any{}, core.BooleanAnd)

		sql, bindings := b.ToSQL(), b.GetBindings()
		t.Logf("SQL: %s", sql)
		t.Logf("Bindings: %v", bindings)
		t.Logf("Wheres: %+v", b.State().Wheres)

		// The bug: this might produce malformed "name IN ()" instead of "0 = 1"
		assert.NotContains(t, sql, "in ()", "WhereAny with empty IN should not produce malformed 'in ()'")
		assert.NotContains(t, sql, "IN ()", "WhereAny with empty IN should not produce malformed 'IN ()'")
	})

	t.Run("WhereAny with empty NOT IN slice", func(t *testing.T) {
		b := newTestBuilder()
		b.From("users", "").WhereAny([]any{"name", "email"}, "NOT IN", []any{}, core.BooleanAnd)

		sql, bindings := b.ToSQL(), b.GetBindings()
		t.Logf("SQL: %s", sql)
		t.Logf("Bindings: %v", bindings)
		t.Logf("Wheres: %+v", b.State().Wheres)

		// The bug: this might produce malformed "name NOT IN ()" instead of "1 = 1"
		assert.NotContains(t, sql, "not in ()", "WhereAny with empty NOT IN should not produce malformed 'not in ()'")
		assert.NotContains(t, sql, "NOT IN ()", "WhereAny with empty NOT IN should not produce malformed 'NOT IN ()'")
	})
}
