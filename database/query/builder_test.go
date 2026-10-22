package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBeforeQueryMultipleCallbacksAppend verifies that registering multiple beforeQuery callbacks
// appends them (rather than replacing), stores all of them, and runs them in registration order.
func TestBeforeQueryMultipleCallbacksAppend(t *testing.T) {
	b := newTestBuilder()

	// Register two callbacks
	b.BeforeQuery(func(q *Builder) *Builder { return q.Where("first", 1) })
	b.BeforeQuery(func(q *Builder) *Builder { return q.Where("second", 2) })

	// Both should be stored
	assert.Len(t, b.beforeQueryCallbacks, 2, "expected two callbacks to be stored")

	// Compile to trigger ApplyBeforeQueryCallbacks
	sql := b.From("users", "").ToSQL()

	// Both callbacks should have run, in order
	assert.Equal(t, `select * from "users" where "first" = ? and "second" = ?`, sql)
	assert.Equal(t, []any{1, 2}, b.GetBindings())
}

// TestApplyBeforeQueryCallbacksDrainsQueue verifies that after applying callbacks once, recompiling
// the builder does not re-run them.
func TestApplyBeforeQueryCallbacksDrainsQueue(t *testing.T) {
	b := newTestBuilder().From("users", "")

	// Register a callback that adds a where clause
	b.BeforeQuery(func(q *Builder) *Builder { return q.Where("id", 1) })

	// First compile: callback runs and adds the where
	sql1 := b.ToSQL()
	assert.Equal(t, `select * from "users" where "id" = ?`, sql1)
	assert.Equal(t, []any{1}, b.GetBindings())

	// Queue should be drained
	assert.Len(t, b.beforeQueryCallbacks, 0, "callbacks should be drained after first compile")

	// Second compile: callback should NOT run again (no duplicate where clause)
	sql2 := b.ToSQL()
	assert.Equal(t, `select * from "users" where "id" = ?`, sql2, "second compile should not re-run callbacks")
	assert.Equal(t, []any{1}, b.GetBindings())
}
