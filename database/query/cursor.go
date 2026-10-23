package query

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/goravel/framework/errors"
)

// CursorDirectionKey is the JSON key that carries the scan direction inside an encoded cursor. It
// is reserved: a query may not order by a column of this name.
const CursorDirectionKey = "_pointsToNextItems"

// MaxCursorPayloadSize caps both the encoded string and the decoded JSON of a cursor. A cursor is
// client-supplied input, so it is refused before it is parsed rather than after.
const MaxCursorPayloadSize = 16 * 1024

// Cursor is the decoded page anchor: one value per ORDER BY column, in ORDER BY order, plus the
// direction bit. It mirrors Illuminate\Pagination\Cursor and round-trips through Encode and
// DecodeCursor in the same wire format, so a cursor issued by Laravel can be consumed here and vice
// versa.
type Cursor struct {
	// Parameters holds the sort key of the anchor row, one entry per ORDER BY column, in ORDER BY
	// order. It is a slice, not a map: map iteration order is random, which would make every
	// encoding of the same cursor differ.
	Parameters CursorParameters
	// PointsNext reports whether the anchor row is the last row of the page just returned, so the
	// next fetch scans forward. False means scan backwards with every ORDER BY direction reversed.
	PointsNext bool
}

// CursorParameter is one ordered column/value pair of a cursor.
type CursorParameter struct {
	// Column is the ORDER BY column name as the query knows it, not the alias and not the SELECT
	// expression.
	Column string
	// Value is the sort key of the anchor row for Column. Scalars are expected; nested objects and
	// arrays are rejected by the codec.
	Value any
}

// CursorParameters is the ordered list of a cursor's column/value pairs. Duplicate columns are
// rejected at encode time; a later entry would silently shadow an earlier one.
type CursorParameters []CursorParameter

// NewCursor builds a cursor. Direction defaults to next at the call site; pass false to scan
// backwards.
func NewCursor(parameters CursorParameters, pointsNext bool) Cursor {
	return Cursor{Parameters: parameters, PointsNext: pointsNext}
}

// Parameter returns the value stored for column. It reports errors.QueryCursorParameterMissing when
// the cursor and the query's ORDER BY disagree, mirroring Laravel's UnexpectedValueException.
// Lookup is by first match, in slice order.
func (c Cursor) Parameter(column string) (any, error) {
	for _, parameter := range c.Parameters {
		if parameter.Column == column {
			return parameter.Value, nil
		}
	}

	return nil, errors.QueryCursorParameterMissing.Args(column)
}

// Values returns the values for columns in the caller's order, which is the ORDER BY order of the
// query being run, never the cursor's own order. It fails on the first column the cursor does not
// carry.
func (c Cursor) Values(columns []string) ([]any, error) {
	values := make([]any, 0, len(columns))
	for _, column := range columns {
		value, err := c.Parameter(column)
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

// PointsToPreviousItems is the inverse of PointsNext: true means the ORDER BY directions are
// reversed for this fetch and the returned rows must be reversed to read front-to-back.
func (c Cursor) PointsToPreviousItems() bool {
	return !c.PointsNext
}

// Has reports whether column carries a value in the cursor.
func (c Cursor) Has(column string) bool {
	_, ok := c.Parameters.value(column)

	return ok
}

// Encode renders the cursor in Laravel's wire format: unpadded base64url over compact JSON, with the
// parameters in slice order and the direction bit appended as _pointsToNextItems. Laravel escapes
// "/" as "\/" and Go does not; both are valid JSON, so either side decodes either form.
func (c Cursor) Encode() (string, error) {
	payload, err := c.marshalJSON()
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// MarshalJSON emits the same compact object Encode base64-wraps, so the cursor can be nested in a
// caller's own response without a second trip through base64.
func (c Cursor) MarshalJSON() ([]byte, error) {
	return c.marshalJSON()
}

func (c Cursor) marshalJSON() ([]byte, error) {
	seen := make(map[string]struct{}, len(c.Parameters))

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, parameter := range c.Parameters {
		if parameter.Column == CursorDirectionKey {
			return nil, errors.QueryCursorReservedParameter.Args(parameter.Column)
		}
		if _, ok := seen[parameter.Column]; ok {
			return nil, errors.QueryCursorDuplicateParameter.Args(parameter.Column)
		}
		seen[parameter.Column] = struct{}{}

		if !utf8.ValidString(parameter.Column) {
			return nil, errors.QueryCursorInvalidValue.Args(parameter.Column, "the column name is not valid UTF-8")
		}

		if i > 0 {
			buf.WriteByte(',')
		}

		key, err := json.Marshal(parameter.Column)
		if err != nil {
			return nil, errors.QueryCursorInvalidValue.Args(parameter.Column, err)
		}
		buf.Write(key)
		buf.WriteByte(':')

		value, err := marshalCursorValue(parameter.Column, parameter.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(value)
	}

	if len(c.Parameters) > 0 {
		buf.WriteByte(',')
	}
	fmt.Fprintf(&buf, `%q:%t}`, CursorDirectionKey, c.PointsNext)

	payload := buf.Bytes()
	if len(payload) > MaxCursorPayloadSize {
		return nil, errors.QueryCursorPayloadTooLarge.Args(MaxCursorPayloadSize)
	}

	return payload, nil
}

// marshalCursorValue encodes one parameter value and rejects anything that is not a JSON scalar.
// Nested objects and arrays are refused because they would make the ORDER BY comparison meaningless
// and because Laravel's decoder would read them back as arrays anyway.
func marshalCursorValue(column string, value any) ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, errors.QueryCursorInvalidValue.Args(column, err)
	}

	switch encoded[0] {
	case '{', '[':
		return nil, errors.QueryCursorNonScalarValue.Args(column, jsonKindName(encoded[0]))
	}

	return encoded, nil
}

// DecodeCursor parses an encoded cursor produced by Encode or by Laravel's Cursor::encode. It is
// strict where Laravel is lenient: every failure returns a named error rather than a nil cursor.
//
// Numbers are decoded as json.Number, so an id above 2^53 reaches the caller intact instead of
// being rounded through float64.
func DecodeCursor(encoded string) (Cursor, error) {
	// A cursor is client input: check its size before doing any work.
	if len(encoded) > encodedCursorLimit() {
		return Cursor{}, errors.QueryCursorPayloadTooLarge.Args(MaxCursorPayloadSize)
	}

	payload, err := decodeBase64(encoded)
	if err != nil {
		return Cursor{}, err
	}

	if len(payload) > MaxCursorPayloadSize {
		return Cursor{}, errors.QueryCursorPayloadTooLarge.Args(MaxCursorPayloadSize)
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil {
		return Cursor{}, errors.QueryCursorInvalid.Args(err)
	}
	if token != json.Delim('{') {
		return Cursor{}, errors.QueryCursorNotObject.Args(jsonTokenKindName(token))
	}

	var (
		parameters CursorParameters
		seen       = make(map[string]struct{})
		pointsNext bool
		hasPoints  bool
	)

	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return Cursor{}, errors.QueryCursorInvalid.Args(err)
		}
		column, ok := keyToken.(string)
		if !ok {
			return Cursor{}, errors.QueryCursorInvalid.Args("an object key is not a string")
		}

		valueToken, err := decoder.Token()
		if err != nil {
			return Cursor{}, errors.QueryCursorInvalid.Args(err)
		}

		if _, ok := valueToken.(json.Delim); ok {
			return Cursor{}, errors.QueryCursorNonScalarValue.Args(column, jsonTokenKindName(valueToken))
		}

		if column == CursorDirectionKey {
			direction, ok := valueToken.(bool)
			if !ok {
				return Cursor{}, errors.QueryCursorPointsToNextItemsInvalid.Args(jsonTokenKindName(valueToken))
			}
			pointsNext, hasPoints = direction, true

			continue
		}

		if _, ok := seen[column]; ok {
			return Cursor{}, errors.QueryCursorDuplicateParameter.Args(column)
		}
		seen[column] = struct{}{}

		parameters = append(parameters, CursorParameter{Column: column, Value: valueToken})
	}

	if _, err := decoder.Token(); err != nil { // consume the closing brace
		return Cursor{}, errors.QueryCursorInvalid.Args(err)
	}

	// Anything after the object means the payload was not a single cursor.
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return Cursor{}, errors.QueryCursorInvalid.Args(err)
		}

		return Cursor{}, errors.QueryCursorInvalid.Args(fmt.Sprintf("unexpected trailing token %s", jsonTokenKindName(token)))
	}

	if !hasPoints {
		return Cursor{}, errors.QueryCursorPointsToNextItemsMissing
	}

	return NewCursor(parameters, pointsNext), nil
}

// decodeBase64 accepts unpadded base64url first (the Laravel format), then padded base64url and
// standard base64 so a cursor that survived a form POST or a query-string round trip still decodes.
func decodeBase64(encoded string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.StdEncoding,
	}

	var err error
	for _, encoding := range encodings {
		var payload []byte
		payload, err = encoding.DecodeString(encoded)
		if err == nil {
			return payload, nil
		}
	}

	return nil, errors.QueryCursorInvalid.Args(err)
}

func encodedCursorLimit() int {
	return base64.RawURLEncoding.EncodedLen(MaxCursorPayloadSize) + 4
}

// jsonTokenKindName names a decoded JSON token for an error message.
func jsonTokenKindName(token any) string {
	switch value := token.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case json.Delim:
		switch value {
		case '[':
			return "an array"
		case '{':
			return "an object"
		default:
			return "the delimiter " + value.String()
		}
	default:
		return fmt.Sprintf("%T", value)
	}
}

// jsonKindName names the container an encoded JSON value starts with.
func jsonKindName(first byte) string {
	if first == '[' {
		return "an array"
	}

	return "an object"
}

// value is the unordered lookup helper behind Has. It reports the first match, in slice order.
func (p CursorParameters) value(column string) (any, bool) {
	for _, parameter := range p {
		if parameter.Column == column {
			return parameter.Value, true
		}
	}

	return nil, false
}

// cursorPredicateOrders returns the orders that take part in a keyset predicate: every order that
// carries a direction. A raw order (OrderRawType) names no column, so it contributes neither a
// comparison nor a bound parameter — it still appears in ORDER BY, which is why only the predicate
// build filters it and the order list itself is never rewritten here.
func cursorPredicateOrders(orders []Order) []Order {
	filtered := make([]Order, 0, len(orders))
	for _, order := range orders {
		if order.Type == OrderRawType {
			continue
		}

		filtered = append(filtered, order)
	}

	return filtered
}

// addCursorPredicate appends the keyset ("seek") predicate for cursor to the builder, consuming orders
// in order. It is the predicate seam of cursor pagination: orders is the builder's own order list (or,
// for a union query, its unionOrders), from which raw orders are dropped because they carry no column
// to compare.
//
// It returns errors.QueryOrderRequired when no direction-bearing order remains, mirroring Laravel's
// enforceOrderBy: cursor pagination is meaningless without an order, and no implicit order is invented.
// Every ORDER BY direction must already be final — a previous-page fetch flips each order direction
// before calling here — so each column's operator is read straight from its own order.
func (b *Builder) addCursorPredicate(cursor Cursor, orders []Order) error {
	orders = cursorPredicateOrders(orders)
	if len(orders) == 0 {
		return errors.QueryOrderRequired
	}

	return b.addCursorConditionsFrom(cursor, orders, "", nil, 0, b.State().Columns, b.State().Columns)
}

// addCursorPredicateFrom is addCursorPredicate with the original-column resolution split between the
// first order column and the deeper ones. A non-union builder passes its own columns for both; a union
// branch passes its own columns for the first column and the root builder's for the rest (see
// addCursorConditionsFrom).
func (b *Builder) addCursorPredicateFrom(cursor Cursor, orders []Order, firstColumns, deeperColumns []any) error {
	orders = cursorPredicateOrders(orders)
	if len(orders) == 0 {
		return errors.QueryOrderRequired
	}

	return b.addCursorConditionsFrom(cursor, orders, "", nil, 0, firstColumns, deeperColumns)
}

// addCursorConditionsFrom appends the nested lexicographic predicate for orders[i:] to b, mirroring
// Laravel's BuildsQueries::addCursorConditions. For orders c0..cn the emitted group is
//
//	(c0 OP0 v0 or (c0 = v0 and (c1 OP1 v1 or (... and (cn OPn vn)...))))
//
// where OP is ">" for an ascending column and "<" for a descending one, equality is always "=", and v
// is the cursor's value for that column. previousColumn is the column whose equality is emitted at
// this level (empty on the first call); previousOriginal is its already-resolved original column, or
// nil to resolve it here. Operators come from each column's own direction, so a reversed fetch — which
// flips directions per column upstream — yields per-column operators, never a blanket flip of the
// whole tuple.
//
// firstColumns and deeperColumns are the select lists the original column is resolved against: the
// first order column against firstColumns and every deeper one against deeperColumns. They differ only
// for a union branch, where Laravel resolves the branch's own first column (getOriginalColumnName for
// the union builder) but every deeper column against the ROOT builder's columns — the recursion is a
// closure bound to the root, so $this is the root at every level below the first. Passing the root's
// columns for the deeper levels reproduces that. For a non-union builder both are the builder's own
// columns.
func (b *Builder) addCursorConditionsFrom(cursor Cursor, orders []Order, previousColumn string, previousOriginal any, i int, firstColumns, deeperColumns []any) error {
	// The equality on the previous column belongs on this builder, ahead of the group for column i.
	if previousColumn != "" {
		original := previousOriginal
		if original == nil {
			original = originalCursorColumn(firstColumns, previousColumn, b.grammar)
		}

		value, err := cursor.Parameter(previousColumn)
		if err != nil {
			return err
		}

		b.cursorWhere(original, "=", value)
	}

	column := getValueString(orders[i].Column)
	columns := deeperColumns
	if i == 0 {
		columns = firstColumns
	}
	original := originalCursorColumn(columns, column, b.grammar)

	value, err := cursor.Parameter(column)
	if err != nil {
		return err
	}

	operator := ">"
	if orders[i].Direction == "desc" {
		operator = "<"
	}

	var nestedErr error
	b.WhereNested(func(group *Builder) *Builder {
		group.cursorWhere(original, operator, value)

		if i < len(orders)-1 {
			group.WhereNested(func(next *Builder) *Builder {
				nestedErr = next.addCursorConditionsFrom(cursor, orders, column, original, i+1, firstColumns, deeperColumns)

				return next
			}, BooleanOr)
		}

		return group
	}, BooleanAnd)

	return nestedErr
}

// cursorWhere adds one side of the keyset comparison. A resolved column that is itself a raw SQL
// expression — its string form contains a parenthesis, e.g. a select("(CONCAT(first, ' ', last)) as
// alias") expression — goes through WhereRaw: addWhere treats an Expression column as its own clause
// type and would drop both the operator and the value. Everything else is a plain wrapped comparison.
func (b *Builder) cursorWhere(column any, operator string, value any) {
	if text := getValueString(column); strings.ContainsAny(text, "()") {
		b.WhereRaw(text+" "+operator+" ?", []any{value}, BooleanAnd)

		return
	}

	b.Where(column, operator, value)
}

// CursorPaginate prepares b for one keyset ("seek") page fetch and returns the paginator skeleton the
// caller fills with rows. It mirrors the SQL-mutating half of Laravel's
// BuildsQueries::paginateUsingCursor: resolve the orders, reverse them for a backwards cursor, emit the
// cursor predicate (once per union branch), set the limit to perPage+1, and hand back a paginator whose
// Parameters name the ORDER BY columns.
//
// A nil cursor is the first page: no predicate and no reversal, but the order requirement and the
// perPage+1 limit still apply, exactly as Laravel calls ensureOrderForCursorPagination unconditionally.
// It mutates the builder — a caller that reuses one should clone it first — and does not execute: the
// returned paginator has no Items yet, because the row fetch and the paginator's derived booleans and
// cursors are the facade's job (it scans the rows and calls NewCursorPaginator).
//
// It returns errors.QueryOrderRequired when the builder carries no order at all, mirroring
// enforceOrderBy; no implicit order is invented.
func (b *Builder) CursorPaginate(perPage int, cursor *Cursor) (CursorPaginator, error) {
	orders := b.cursorOrders()
	if len(orders) == 0 {
		return CursorPaginator{}, errors.QueryOrderRequired
	}

	hasCursor := cursor != nil

	// A previous-page fetch reverses every ORDER BY direction in place; OrderBy appends, so the slice is
	// rebuilt and written back rather than re-added.
	if hasCursor && cursor.PointsToPreviousItems() {
		orders = flipCursorOrders(orders)
		b.setCursorOrders(orders)
	}

	if hasCursor {
		// Union branches flattened their own bindings into this builder's "union" bucket when they were
		// attached. Clear it and replay each branch's where bindings in branch order, so the placeholders
		// the compiled union SQL emits line up with the values the executor binds.
		if len(b.State().Unions) > 0 {
			if err := b.SetBindings([]any{}, "union"); err != nil {
				return CursorPaginator{}, err
			}
		}

		if err := b.addCursorPredicateFrom(*cursor, orders, b.State().Columns, b.State().Columns); err != nil {
			return CursorPaginator{}, err
		}

		for _, union := range b.State().Unions {
			if union.Query == nil {
				continue
			}

			// The branch's predicate compares its own original column for the first order column and the
			// root's for the deeper ones — see addCursorConditionsFrom.
			if err := union.Query.(*Builder).addCursorPredicateFrom(*cursor, orders, union.Query.State().Columns, b.State().Columns); err != nil {
				return CursorPaginator{}, err
			}

			// One snapshot of the branch's where bindings per branch: its pre-existing values followed by
			// the cursor values just appended.
			if err := b.AddBinding(union.Query.RawBindings("where"), "union"); err != nil {
				return CursorPaginator{}, err
			}
		}
	}

	// limit(perPage+1) is the has-more probe; with unions present Limit routes to the union limit.
	b.Limit(perPage + 1)

	var anchor Cursor
	if cursor != nil {
		anchor = *cursor
	}

	return NewCursorPaginator(nil, perPage, anchor, hasCursor, cursorParameterColumns(orders)), nil
}

// cursorOrders is the order list cursor pagination reads and reverses: the union orders when the
// builder has unions (Laravel orders unions through unionOrders), otherwise the plain orders.
func (b *Builder) cursorOrders() []Order {
	if len(b.State().Unions) > 0 {
		return b.State().UnionOrders
	}

	return b.State().Orders
}

// setCursorOrders writes a reversed order list back into the slot cursorOrders reads, so the ORDER BY
// the builder compiles is the reversed one.
func (b *Builder) setCursorOrders(orders []Order) {
	if len(b.State().Unions) > 0 {
		b.State().UnionOrders = orders

		return
	}

	b.State().Orders = orders
}

// flipCursorOrders reverses each order's direction in a fresh slice. A raw order carries no direction
// and is copied unchanged, mirroring Laravel's isset($order['direction']) guard in
// ensureOrderForCursorPagination.
func flipCursorOrders(orders []Order) []Order {
	flipped := make([]Order, len(orders))
	for i, order := range orders {
		switch order.Direction {
		case "asc":
			order.Direction = "desc"
		case "desc":
			order.Direction = "asc"
		}

		flipped[i] = order
	}

	return flipped
}

// cursorParameterColumns returns the ORDER BY column names the cursor carries, in ORDER BY order,
// dropping raw orders (they name no column to compare).
func cursorParameterColumns(orders []Order) []string {
	columns := make([]string, 0, len(orders))
	for _, order := range cursorPredicateOrders(orders) {
		columns = append(columns, getValueString(order.Column))
	}

	return columns
}
