package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reverseCursorOrders flips every order's direction, mirroring the per-column reversal
// ensureOrderForCursorPagination performs before the predicate is built. A reverse test can apply it
// to b.State().Orders to state the flip explicitly instead of leaning on the separate reversal helper, and
// the ORDER BY it leaves behind is exactly what a previous-page fetch emits.
func reverseCursorOrders(orders []core.Order) []core.Order {
	flipped := make([]core.Order, len(orders))
	for i, order := range orders {
		direction := "asc"
		if order.Direction == "asc" {
			direction = "desc"
		}

		flipped[i] = core.Order{Column: order.Column, Direction: direction}
	}

	return flipped
}

// TestAddCursorPredicateSingleParameter pins the one-column case: a single comparison, wrapped in the
// nested group's parentheses, with one bound value.
func TestAddCursorPredicateSingleParameter(t *testing.T) {
	b := newTestBuilder().From("foobar", "").OrderBy("test", "asc")
	cursor := NewCursor(CursorParameters{{Column: "test", Value: "bar"}}, true)

	require.NoError(t, b.addCursorPredicate(cursor, b.State().Orders))

	sql, bindings := b.ToSelectSQL()
	assert.Equal(t, `select * from "foobar" where ("test" > ?) order by "test" asc`, sql)
	assert.Equal(t, []any{"bar"}, bindings)
}

// TestAddCursorPredicateMultipleAscending pins the lexicographic shape and its binding order for two
// ascending columns: the equality on the first column is emitted before the comparison on the second,
// so the first value is bound twice.
func TestAddCursorPredicateMultipleAscending(t *testing.T) {
	b := newTestBuilder().From("foobar", "").OrderBy("test", "asc").OrderBy("another", "asc")
	cursor := NewCursor(CursorParameters{{Column: "test", Value: "bar"}, {Column: "another", Value: "foo"}}, true)

	require.NoError(t, b.addCursorPredicate(cursor, b.State().Orders))

	sql, bindings := b.ToSelectSQL()
	assert.Equal(t, `select * from "foobar" where ("test" > ? or ("test" = ? and ("another" > ?))) order by "test" asc, "another" asc`, sql)
	assert.Equal(t, []any{"bar", "bar", "foo"}, bindings)
}

// TestAddCursorPredicateMixedDirection pins that each column's comparison operator follows its own
// direction: asc is ">", desc is "<", and the equality between columns is always "=".
func TestAddCursorPredicateMixedDirection(t *testing.T) {
	b := newTestBuilder().From("foobar", "").
		OrderBy("foo", "asc").
		OrderBy("bar", "desc").
		OrderBy("baz", "asc")
	cursor := NewCursor(CursorParameters{
		{Column: "foo", Value: 1},
		{Column: "bar", Value: 2},
		{Column: "baz", Value: 3},
	}, true)

	require.NoError(t, b.addCursorPredicate(cursor, b.State().Orders))

	sql, bindings := b.ToSelectSQL()
	assert.Equal(t, `select * from "foobar" where ("foo" > ? or ("foo" = ? and ("bar" < ? or ("bar" = ? and ("baz" > ?))))) order by "foo" asc, "bar" desc, "baz" asc`, sql)
	assert.Equal(t, []any{1, 1, 2, 2, 3}, bindings)
}

// TestAddCursorPredicateReverseSingle pins a previous-page fetch on one column: the direction flip
// upstream turns asc into desc, so the operator becomes "<" and ORDER BY descends. The predicate
// builder derives the operator from the already-flipped order, never by flipping the comparison.
func TestAddCursorPredicateReverseSingle(t *testing.T) {
	b := newTestBuilder().From("foobar", "").OrderBy("created_at", "asc")
	b.State().Orders = reverseCursorOrders(b.State().Orders) // ensureOrder flips in place before the predicate is built

	ts := "2023-01-01 00:00:00"
	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: ts}}, false)

	require.NoError(t, b.addCursorPredicate(cursor, b.State().Orders))

	sql, bindings := b.ToSelectSQL()
	assert.Equal(t, `select * from "foobar" where ("created_at" < ?) order by "created_at" desc`, sql)
	assert.Equal(t, []any{ts}, bindings)
}

// TestAddCursorPredicateReverseMultiple pins that a reverse flip is per column, not a blanket flip of
// the tuple: the descending first column keeps "<" while the ascending second column takes ">", even
// though both started from a mix that the flip swapped individually.
func TestAddCursorPredicateReverseMultiple(t *testing.T) {
	b := newTestBuilder().From("foobar", "").OrderBy("created_at", "asc").OrderBy("id", "desc")
	b.State().Orders = reverseCursorOrders(b.State().Orders) // -> created_at desc, id asc

	ts := "2023-01-01 00:00:00"
	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: ts}, {Column: "id", Value: 1}}, false)

	require.NoError(t, b.addCursorPredicate(cursor, b.State().Orders))

	sql, bindings := b.ToSelectSQL()
	assert.Equal(t, `select * from "foobar" where ("created_at" < ? or ("created_at" = ? and ("id" > ?))) order by "created_at" desc, "id" asc`, sql)
	assert.Equal(t, []any{ts, ts, 1}, bindings)
}

// TestAddCursorPredicateAliasedOrderColumn pins the alias resolution: order by an alias introduced by
// selectRaw ("... as test"), and the comparison must use the original select expression, not the
// alias. The original already carries its own parentheses, so the fragment is a raw "(expr) op ?".
func TestAddCursorPredicateAliasedOrderColumn(t *testing.T) {
	b := newTestBuilder().From("foobar", "").
		Select("*").
		SelectRaw("(CONCAT(firstname, ' ', lastname)) as test", nil).
		OrderBy("test", "asc")
	cursor := NewCursor(CursorParameters{{Column: "test", Value: "bar"}}, true)

	require.NoError(t, b.addCursorPredicate(cursor, b.State().Orders))

	sql, bindings := b.ToSelectSQL()
	assert.Equal(t, `select *, (CONCAT(firstname, ' ', lastname)) as test from "foobar" where ((CONCAT(firstname, ' ', lastname)) > ?) order by "test" asc`, sql)
	assert.Equal(t, []any{"bar"}, bindings)
}

// TestAddCursorPredicateRawOrderExcluded pins that a raw ORDER BY contributes no comparison and no
// parameter: the raw fragment stays in ORDER BY but only the following real column is compared.
func TestAddCursorPredicateRawOrderExcluded(t *testing.T) {
	b := newTestBuilder().From("foobar", "").
		OrderByRaw("case when (id = 3) then 0 else 1 end", nil).
		OrderBy("created_at", "asc")

	ts := "2023-01-01 00:00:00"
	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: ts}}, true)

	require.NoError(t, b.addCursorPredicate(cursor, b.State().Orders))

	sql, bindings := b.ToSelectSQL()
	assert.Equal(t, `select * from "foobar" where ("created_at" > ?) order by case when (id = 3) then 0 else 1 end, "created_at" asc`, sql)
	assert.Equal(t, []any{ts}, bindings)
}

// TestAddCursorPredicateNoOrder pins that an unordered query is refused rather than silently paginated
// without a keyset.
func TestAddCursorPredicateNoOrder(t *testing.T) {
	b := newTestBuilder().From("foobar", "")
	cursor := NewCursor(CursorParameters{{Column: "test", Value: "bar"}}, true)

	err := b.addCursorPredicate(cursor, b.State().Orders)

	assert.ErrorIs(t, err, errors.QueryOrderRequired)
}

// TestAddCursorPredicateMissingParameter pins that a cursor which does not carry every ordered column
// surfaces errors.QueryCursorParameterMissing instead of binding a nil.
func TestAddCursorPredicateMissingParameter(t *testing.T) {
	b := newTestBuilder().From("foobar", "").OrderBy("test", "asc").OrderBy("another", "asc")
	cursor := NewCursor(CursorParameters{{Column: "test", Value: "bar"}}, true)

	err := b.addCursorPredicate(cursor, b.State().Orders)

	assert.ErrorIs(t, err, errors.QueryCursorParameterMissing)
}
