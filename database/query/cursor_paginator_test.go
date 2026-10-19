package query

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/goravel/framework/errors"
)

// cursorRoundTrip encodes cursor, decodes it back and asserts the parameters and direction survived.
// Values are compared by their string form because a decoded number comes back as json.Number while
// the minted one is a native int; the two encode identically.
func cursorRoundTrip(t *testing.T, cursor Cursor) {
	t.Helper()

	encoded, err := cursor.Encode()
	require.NoError(t, err)

	decoded, err := DecodeCursor(encoded)
	require.NoError(t, err)

	assert.Equal(t, cursor.PointsNext, decoded.PointsNext)
	require.Len(t, decoded.Parameters, len(cursor.Parameters))
	for i, parameter := range cursor.Parameters {
		assert.Equal(t, parameter.Column, decoded.Parameters[i].Column)
		assert.Equal(t, fmt.Sprint(parameter.Value), fmt.Sprint(decoded.Parameters[i].Value))
	}
}

// rows builds a page of single-column rows from ids, in the order given.
func cursorRows(ids ...any) []map[string]any {
	rows := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]any{"id": id})
	}

	return rows
}

// TestCursorPaginatorFirstPageWithMore covers a first page whose probe row survives: the page is
// trimmed, there is a next cursor and no previous one.
func TestCursorPaginatorFirstPageWithMore(t *testing.T) {
	items := cursorRows(1, 2, 3)
	page := NewCursorPaginator(items, 2, Cursor{}, false, []string{"id"})

	assert.Equal(t, 2, page.PerPage)
	assert.Equal(t, cursorRows(1, 2), page.Items, "the probe row must be dropped")
	assert.True(t, page.HasMorePages())
	assert.True(t, page.OnFirstPage())
	assert.False(t, page.OnLastPage())
	assert.True(t, page.HasPages())

	_, hasPrevious := page.PreviousCursor()
	assert.False(t, hasPrevious, "a first page has no previous cursor")

	next, hasNext := page.NextCursor()
	require.True(t, hasNext)
	assert.Equal(t, CursorParameters{{Column: "id", Value: 2}}, next.Parameters)
	assert.True(t, next.PointsNext)

	_, ok, err := page.NextCursorEncoded()
	require.NoError(t, err)
	require.True(t, ok)

	cursorRoundTrip(t, next)
}

// TestCursorPaginatorFirstPageWithoutMore covers a first page that is also the last.
func TestCursorPaginatorFirstPageWithoutMore(t *testing.T) {
	page := NewCursorPaginator(cursorRows(1), 2, Cursor{}, false, []string{"id"})

	assert.True(t, page.OnFirstPage())
	assert.True(t, page.OnLastPage())
	assert.False(t, page.HasMorePages())
	assert.False(t, page.HasPages())

	_, hasNext := page.NextCursor()
	_, hasPrevious := page.PreviousCursor()
	assert.False(t, hasNext)
	assert.False(t, hasPrevious)
}

// TestCursorPaginatorLastPage covers a forward page that has run out of rows: no next cursor, a
// previous cursor, and not the first page.
func TestCursorPaginatorLastPage(t *testing.T) {
	cursor := NewCursor(CursorParameters{{Column: "id", Value: 5}}, true)
	page := NewCursorPaginator(cursorRows(6), 2, cursor, true, []string{"id"})

	assert.False(t, page.OnFirstPage())
	assert.True(t, page.OnLastPage())
	assert.False(t, page.HasMorePages())
	assert.True(t, page.HasPages())

	_, hasNext := page.NextCursor()
	assert.False(t, hasNext)

	previous, hasPrevious := page.PreviousCursor()
	require.True(t, hasPrevious)
	assert.Equal(t, CursorParameters{{Column: "id", Value: 6}}, previous.Parameters)
	assert.False(t, previous.PointsNext)
}

// TestCursorPaginatorBackwardsPage covers a previous-page fetch: the scan order arrives reversed and
// must be flipped so the page reads front-to-back, and both cursors are produced.
func TestCursorPaginatorBackwardsPage(t *testing.T) {
	cursor := NewCursor(CursorParameters{{Column: "id", Value: 5}}, false)
	// Scan order is descending (3, 2, 1); the probe row (1) is dropped before the flip.
	page := NewCursorPaginator(cursorRows(3, 2, 1), 2, cursor, true, []string{"id"})

	assert.Equal(t, cursorRows(2, 3), page.Items, "a backwards page must be reversed into display order")
	assert.False(t, page.OnFirstPage())
	assert.False(t, page.OnLastPage())
	assert.True(t, page.HasMorePages())
	assert.True(t, page.HasPages())

	next, hasNext := page.NextCursor()
	require.True(t, hasNext)
	assert.Equal(t, CursorParameters{{Column: "id", Value: 3}}, next.Parameters)
	assert.True(t, next.PointsNext)

	previous, hasPrevious := page.PreviousCursor()
	require.True(t, hasPrevious)
	assert.Equal(t, CursorParameters{{Column: "id", Value: 2}}, previous.Parameters)
	assert.False(t, previous.PointsNext)
}

// TestCursorPaginatorBackwardsPageAtStart covers a backwards page that is also the first page: no
// previous cursor, but a next cursor back towards where the rows came from.
func TestCursorPaginatorBackwardsPageAtStart(t *testing.T) {
	cursor := NewCursor(CursorParameters{{Column: "id", Value: 5}}, false)
	page := NewCursorPaginator(cursorRows(2, 1), 2, cursor, true, []string{"id"})

	assert.Equal(t, cursorRows(1, 2), page.Items)
	assert.True(t, page.OnFirstPage())
	assert.False(t, page.OnLastPage())
	assert.True(t, page.HasMorePages())

	_, hasNext := page.NextCursor()
	_, hasPrevious := page.PreviousCursor()
	assert.True(t, hasNext)
	assert.False(t, hasPrevious)
}

// TestCursorPaginatorEmptyPage covers an empty first page and an empty backwards page.
func TestCursorPaginatorEmptyPage(t *testing.T) {
	page := NewCursorPaginator(nil, 15, Cursor{}, false, []string{"id"})

	assert.Empty(t, page.Items)
	assert.True(t, page.OnFirstPage())
	assert.True(t, page.OnLastPage())
	assert.False(t, page.HasMorePages())
	assert.False(t, page.HasPages())

	_, hasNext := page.NextCursor()
	_, hasPrevious := page.PreviousCursor()
	assert.False(t, hasNext)
	assert.False(t, hasPrevious)

	// An empty backwards page: the backwards HasMorePages branch is unconditional, so the page still
	// claims a next page, but there is no row to anchor a cursor to.
	empty := NewCursorPaginator([]map[string]any{}, 15, NewCursor(nil, false), true, []string{"id"})
	assert.Equal(t, cursorRows(), empty.Items)
	assert.True(t, empty.HasMorePages())
	_, hasNext = empty.NextCursor()
	_, hasPrevious = empty.PreviousCursor()
	assert.False(t, hasNext)
	assert.False(t, hasPrevious)
}

// TestCursorPaginatorMultiColumnParameters covers a cursor minted over several ORDER BY columns, one
// value per column in parameter order.
func TestCursorPaginatorMultiColumnParameters(t *testing.T) {
	items := []map[string]any{
		{"created_at": "2023-01-01 00:00:00", "id": 2},
		{"created_at": "2023-01-01 00:00:01", "id": 1},
		{"created_at": "2023-01-01 00:00:02", "id": 3},
	}

	page := NewCursorPaginator(items, 2, Cursor{}, false, []string{"created_at", "id"})

	next, ok := page.NextCursor()
	require.True(t, ok)
	assert.Equal(t, CursorParameters{
		{Column: "created_at", Value: "2023-01-01 00:00:01"},
		{Column: "id", Value: 1},
	}, next.Parameters)

	_, ok, err := page.NextCursorEncoded()
	require.NoError(t, err)
	require.True(t, ok)

	cursorRoundTrip(t, next)
}

// TestCursorPaginatorDottedColumnFallback covers the exact-key miss followed by the after-last-dot
// lookup, so an order on "users.id" finds the row's "id".
func TestCursorPaginatorDottedColumnFallback(t *testing.T) {
	page := NewCursorPaginator(cursorRows(1, 2, 3), 2, Cursor{}, false, []string{"users.id"})

	next, ok := page.NextCursor()
	require.True(t, ok)
	assert.Equal(t, CursorParameters{{Column: "users.id", Value: 2}}, next.Parameters)
}

// TestCursorPaginatorMissingColumnError covers the named error raised when a row lacks an ordered
// column: it surfaces through the Encoded accessor rather than becoming a silent nil.
func TestCursorPaginatorMissingColumnError(t *testing.T) {
	page := NewCursorPaginator(cursorRows(1, 2, 3), 2, Cursor{}, false, []string{"missing"})

	_, hasNext := page.NextCursor()
	assert.False(t, hasNext)

	_, ok, err := page.NextCursorEncoded()
	assert.False(t, ok)
	assert.ErrorIs(t, err, errors.QueryCursorColumnNotFound)
	assert.Contains(t, err.Error(), "missing")
}

// TestCursorPaginatorPerPageClamp covers the perPage floor: a non-positive perPage becomes 1.
func TestCursorPaginatorPerPageClamp(t *testing.T) {
	page := NewCursorPaginator(cursorRows(1, 2), 0, Cursor{}, false, []string{"id"})

	assert.Equal(t, 1, page.PerPage)
	assert.Equal(t, cursorRows(1), page.Items)
	assert.True(t, page.HasMorePages())
}

// TestCursorPaginatorCursorNameDefault covers the cursorName default and that it is overridable.
func TestCursorPaginatorCursorNameDefault(t *testing.T) {
	page := NewCursorPaginator(cursorRows(1), 1, Cursor{}, false, []string{"id"})
	assert.Equal(t, "cursor", page.CursorName)

	page.CursorName = "page"
	assert.Equal(t, "page", page.CursorName)
}

// TestCursorPaginatorToMap covers the four-key response shape and that absent cursors are null.
func TestCursorPaginatorToMap(t *testing.T) {
	page := NewCursorPaginator(cursorRows(1, 2, 3), 2, Cursor{}, false, []string{"id"})
	payload := page.ToMap()

	assert.Equal(t, cursorRows(1, 2), payload["data"])
	assert.Equal(t, 2, payload["per_page"])

	encoded, _, err := page.NextCursorEncoded()
	require.NoError(t, err)
	assert.Equal(t, encoded, payload["next_cursor"])
	assert.Nil(t, payload["prev_cursor"])

	_, hasTotal := payload["total"]
	assert.False(t, hasTotal, "ToMap must not emit a total")
	assert.Len(t, payload, 4)
}

// TestCursorPaginatorToMapNullCursors covers ToMap on a page with neither cursor.
func TestCursorPaginatorToMapNullCursors(t *testing.T) {
	page := NewCursorPaginator(cursorRows(1), 2, Cursor{}, false, []string{"id"})
	payload := page.ToMap()

	assert.Nil(t, payload["next_cursor"])
	assert.Nil(t, payload["prev_cursor"])
}
