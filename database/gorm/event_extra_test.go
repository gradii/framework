package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEvent_Context_NilCtx verifies Context returns the underlying query's context (nil when
// unset).
func TestEvent_Context_NilCtx(t *testing.T) {
	// A query built for compilation alone carries no context, so neither does the event.
	q := &OrmBuilder{}
	e := NewEvent(q, &testEventModel, nil)
	assert.Nil(t, e.Context())
}

// TestEvent_Context_ReturnsCtx verifies Context returns the configured context.
func TestEvent_Context_ReturnsCtx(t *testing.T) {
	type ctxKey int
	const k ctxKey = 1
	ctx := context.WithValue(context.Background(), k, "value")
	q := &OrmBuilder{ctx: ctx}
	e := NewEvent(q, &testEventModel, nil)
	assert.Equal(t, "value", e.Context().Value(k))
}

// TestEvent_IsClean_InverseOfIsDirty verifies IsClean returns !IsDirty.
func TestEvent_IsClean_InverseOfIsDirty(t *testing.T) {
	// When dest is empty/nil, IsDirty returns false, so IsClean returns true.
	q := compiledQuery(t, &testEventModel, Conditions{})
	e := NewEvent(q, &testEventModel, nil)
	assert.True(t, e.IsClean())
	assert.False(t, e.IsDirty())
}

// TestEvent_ValidColumnFollowsTheQueryFilter pins that an observer sees the column filter the query
// itself carries — the same one the compiled statement writes by.
func TestEvent_ValidColumnFollowsTheQueryFilter(t *testing.T) {
	selecting := compiledQuery(t, &testEventModel, Conditions{})
	selecting.query.Select("Name")

	event := NewEvent(selecting, &testEventModel, &TestEventModel{})

	assert.True(t, event.validColumn("Name"), "a named column is one the observer may see")
	assert.False(t, event.validColumn("Avatar"), "a column the filter leaves out is not")

	omitting := compiledQuery(t, &testEventModel, Conditions{omit: []string{"Avatar"}})

	omitted := NewEvent(omitting, &testEventModel, &TestEventModel{})

	assert.True(t, omitted.validColumn("Name"), "omitting one column leaves the others writable")
	assert.False(t, omitted.validColumn("Avatar"), "an omitted column is not")
}
