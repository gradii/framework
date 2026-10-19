package query

import (
	"strings"

	"github.com/goravel/framework/errors"
)

// DefaultCursorName is the query-parameter name a cursor travels under, mirroring Laravel's
// CursorPaginator::$cursorName default.
const DefaultCursorName = "cursor"

// CursorPaginator is the result of one keyset page fetch. It holds no SQL, no database handle and no
// request state: everything it needs was decided before it was constructed, and everything it
// exposes is derived from Items, PerPage and the anchor cursor. It mirrors Illuminate\Pagination\
// CursorPaginator, minus URLs, views and the Arrayable/Jsonable idiom adapters.
//
// Items is the page as it should be displayed: at most PerPage rows, in display order. For a
// backwards fetch the scan order is reversed here so the caller never sees it.
type CursorPaginator struct {
	// Items is the page, already trimmed to PerPage and, for a backwards fetch, reversed so it reads
	// front-to-back.
	Items []map[string]any
	// PerPage is the page size actually used, clamped to at least 1.
	PerPage int
	// Cursor is the anchor the page was fetched from. The zero value is the first page.
	Cursor Cursor
	// HasCursor distinguishes an explicit cursor (even an empty one) from the first page.
	HasCursor bool
	// CursorName is the query-parameter name the encoded cursors travel under.
	CursorName string
	// Parameters is the ORDER BY column list, in ORDER BY order. It is the key set a minted cursor
	// carries, never the SELECT list.
	Parameters []string

	// hasMore records whether the probe row survived: len(items) > PerPage before truncation.
	hasMore bool
	// nextCursor is the minted forward anchor (last displayed row), or nil when there is no next page.
	nextCursor *Cursor
	// previousCursor is the minted backward anchor (first displayed row), or nil when there is none.
	previousCursor *Cursor
	// err is the first cursor-minting failure (a row lacked an ordered column). It is surfaced by the
	// Encoded accessors so a missing column is never silently downgraded to "no cursor".
	err error
}

// NewCursorPaginator builds the page result. It clamps perPage to >= 1, records whether the probe
// row indicates more (hasMore = len(items) > perPage), truncates the probe row BEFORE any reversal
// (the dropped row is the extra row in the scan direction), and, when a real cursor
// points to previous items, reverses the page so it reads front-to-back. The next cursor is minted from the last
// displayed row and the previous cursor from the first, each only when its end-of-page rule says it
// exists.
func NewCursorPaginator(items []map[string]any, perPage int, cursor Cursor, hasCursor bool, parameters []string) CursorPaginator {
	if perPage < 1 {
		perPage = 1
	}

	hasMore := len(items) > perPage
	if hasMore {
		items = items[:perPage]
	}

	// Reverse after truncation: the probe is the (perPage+1)-th row of the scan, so it must be
	// dropped before the page is flipped. The reversal is guarded by HasCursor, exactly as Laravel
	// guards it with `$this->cursor && ...`: the zero-valued cursor carries PointsNext == false, so
	// an unguarded check would flip the first page too.
	if hasCursor && cursor.PointsToPreviousItems() {
		reverseRowMaps(items)
	}

	page := CursorPaginator{
		Items:      items,
		PerPage:    perPage,
		Cursor:     cursor,
		HasCursor:  hasCursor,
		CursorName: DefaultCursorName,
		Parameters: parameters,
		hasMore:    hasMore,
	}

	if len(items) == 0 {
		return page
	}

	if !page.nextAbsent() {
		if minted, err := page.mintCursor(items[len(items)-1], true); err != nil {
			page.err = err
		} else {
			page.nextCursor = &minted
		}
	}

	if !page.previousAbsent() {
		if minted, err := page.mintCursor(items[0], false); err != nil {
			if page.err == nil {
				page.err = err
			}
		} else {
			page.previousCursor = &minted
		}
	}

	return page
}

// HasMorePages reports whether another page exists in the fetch direction. A backwards page always
// has a next page (it is travelling back towards where it came from), which is why the last branch
// does not consult hasMore.
func (p CursorPaginator) HasMorePages() bool {
	switch {
	case !p.HasCursor:
		return p.hasMore
	case p.Cursor.PointsNext:
		return p.hasMore
	default:
		return true
	}
}

// OnFirstPage reports whether the page is the first in the result set.
func (p CursorPaginator) OnFirstPage() bool {
	return !p.HasCursor || (!p.Cursor.PointsNext && !p.hasMore)
}

// OnLastPage reports whether the page is the last in the result set.
func (p CursorPaginator) OnLastPage() bool {
	return !p.HasMorePages()
}

// HasPages reports whether more than one page could be navigated to.
func (p CursorPaginator) HasPages() bool {
	return !p.OnFirstPage() || p.HasMorePages()
}

// NextCursor returns the forward cursor over the last displayed row, and whether it exists.
func (p CursorPaginator) NextCursor() (Cursor, bool) {
	if p.nextCursor == nil {
		return Cursor{}, false
	}

	return *p.nextCursor, true
}

// PreviousCursor returns the backward cursor over the first displayed row, and whether it exists.
func (p CursorPaginator) PreviousCursor() (Cursor, bool) {
	if p.previousCursor == nil {
		return Cursor{}, false
	}

	return *p.previousCursor, true
}

// NextCursorEncoded returns the encoded forward cursor and whether it exists. A cursor-minting
// failure (an ordered column absent from the row) is returned here rather than hidden behind the
// boolean.
func (p CursorPaginator) NextCursorEncoded() (string, bool, error) {
	if p.err != nil {
		return "", false, p.err
	}

	cursor, ok := p.NextCursor()
	if !ok {
		return "", false, nil
	}

	encoded, err := cursor.Encode()
	if err != nil {
		return "", false, err
	}

	return encoded, true, nil
}

// PreviousCursorEncoded returns the encoded backward cursor and whether it exists. See
// NextCursorEncoded for the error contract.
func (p CursorPaginator) PreviousCursorEncoded() (string, bool, error) {
	if p.err != nil {
		return "", false, p.err
	}

	cursor, ok := p.PreviousCursor()
	if !ok {
		return "", false, nil
	}

	encoded, err := cursor.Encode()
	if err != nil {
		return "", false, err
	}

	return encoded, true, nil
}

// ToMap renders the page as Laravel's cursor-pagination JSON shape: data, per_page, next_cursor and
// prev_cursor only. There is no total (this path never counts), no path and no URLs (those are
// transport, and the caller owns its router). A cursor end that does not exist, or that failed to
// mint, is emitted as nil.
func (p CursorPaginator) ToMap() map[string]any {
	var nextCursor, previousCursor any

	if encoded, ok, err := p.NextCursorEncoded(); ok && err == nil {
		nextCursor = encoded
	}
	if encoded, ok, err := p.PreviousCursorEncoded(); ok && err == nil {
		previousCursor = encoded
	}

	return map[string]any{
		"data":        p.Items,
		"per_page":    p.PerPage,
		"next_cursor": nextCursor,
		"prev_cursor": previousCursor,
	}
}

// nextAbsent is the complement of NextCursor's presence rule, read straight from the brief's truth
// table: no cursor when the first page is also the last, when a forward cursor has already reached
// the end, or when there are no items to anchor to.
func (p CursorPaginator) nextAbsent() bool {
	if len(p.Items) == 0 {
		return true
	}

	if !p.HasCursor {
		return !p.hasMore
	}

	return p.Cursor.PointsNext && !p.hasMore
}

// previousAbsent is the complement of PreviousCursor's presence rule: no cursor on the first page,
// and none on a backwards page that has run out of rows (that page is the first page).
func (p CursorPaginator) previousAbsent() bool {
	if len(p.Items) == 0 {
		return true
	}

	if !p.HasCursor {
		return true
	}

	return !p.Cursor.PointsNext && !p.hasMore
}

// mintCursor reads one value per ordered column off row, in parameter order, and builds a cursor
// pointing in the requested direction. A column the row does not carry is a named error.
func (p CursorPaginator) mintCursor(row map[string]any, pointsNext bool) (Cursor, error) {
	parameters := make(CursorParameters, 0, len(p.Parameters))
	for _, column := range p.Parameters {
		value, err := cursorColumnValue(row, column)
		if err != nil {
			return Cursor{}, err
		}

		parameters = append(parameters, CursorParameter{Column: column, Value: value})
	}

	return NewCursor(parameters, pointsNext), nil
}

// cursorColumnValue reads column off row by exact key, then by the segment after the last '.', so an
// order on "users.id" finds the row's "id" column. It returns errors.QueryCursorColumnNotFound when
// neither key is present, so a missing column is never bound as a silent nil.
func cursorColumnValue(row map[string]any, column string) (any, error) {
	if value, ok := row[column]; ok {
		return value, nil
	}

	if index := strings.LastIndex(column, "."); index >= 0 {
		if value, ok := row[column[index+1:]]; ok {
			return value, nil
		}
	}

	return nil, errors.QueryCursorColumnNotFound.Args(column)
}

// reverseRowMaps reverses items in place.
func reverseRowMaps(items []map[string]any) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}
