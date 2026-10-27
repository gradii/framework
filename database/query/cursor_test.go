package query

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

// cursorB64 encodes raw JSON as the unpadded base64url string the codec transports, so a test can
// feed DecodeCursor an arbitrary payload without going through Encode first.
func cursorB64(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func cursorMustDecode(t *testing.T, encoded string) Cursor {
	t.Helper()

	cursor, err := DecodeCursor(encoded)
	require.NoError(t, err)

	return cursor
}

// TestCursorEncodeMatchesLaravelWireFormat pins Encode byte-for-byte against strings produced by
// Illuminate\Pagination\Cursor::encode() (the vendored v12.55.1, run through the oracle). The
// expected values are the "encode:" lines that PHP emitted, so a drift in key order, the direction
// bit, or the base64 flavour fails here rather than at an integration boundary.
func TestCursorEncodeMatchesLaravelWireFormat(t *testing.T) {
	tests := []struct {
		name       string
		parameters CursorParameters
		pointsNext bool
		want       string
	}{
		{
			name:       "single int forward",
			parameters: CursorParameters{{Column: "id", Value: json.Number("1")}},
			pointsNext: true,
			want:       "eyJpZCI6MSwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		},
		{
			name:       "single int backward",
			parameters: CursorParameters{{Column: "id", Value: json.Number("1")}},
			pointsNext: false,
			want:       "eyJpZCI6MSwiX3BvaW50c1RvTmV4dEl0ZW1zIjpmYWxzZX0",
		},
		{
			name: "multiple parameters keep order",
			parameters: CursorParameters{
				{Column: "created_at", Value: "2023-01-01 00:00:00"},
				{Column: "id", Value: json.Number("42")},
			},
			pointsNext: true,
			want:       "eyJjcmVhdGVkX2F0IjoiMjAyMy0wMS0wMSAwMDowMDowMCIsImlkIjo0MiwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		},
		{
			name:       "string value",
			parameters: CursorParameters{{Column: "name", Value: "Abc"}},
			pointsNext: true,
			want:       "eyJuYW1lIjoiQWJjIiwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		},
		{
			name:       "integer above 2^53 is not rounded",
			parameters: CursorParameters{{Column: "id", Value: json.Number("9007199254740993")}},
			pointsNext: true,
			want:       "eyJpZCI6OTAwNzE5OTI1NDc0MDk5MywiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		},
		{
			name:       "null value",
			parameters: CursorParameters{{Column: "x", Value: nil}},
			pointsNext: true,
			want:       "eyJ4IjpudWxsLCJfcG9pbnRzVG9OZXh0SXRlbXMiOnRydWV9",
		},
		{
			name:       "bool value",
			parameters: CursorParameters{{Column: "active", Value: true}},
			pointsNext: true,
			want:       "eyJhY3RpdmUiOnRydWUsIl9wb2ludHNUb05leHRJdGVtcyI6dHJ1ZX0",
		},
		{
			name:       "float value",
			parameters: CursorParameters{{Column: "score", Value: json.Number("1.5")}},
			pointsNext: true,
			want:       "eyJzY29yZSI6MS41LCJfcG9pbnRzVG9OZXh0SXRlbXMiOnRydWV9",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := NewCursor(test.parameters, test.pointsNext).Encode()
			require.NoError(t, err)
			assert.Equal(t, test.want, encoded)
		})
	}
}

// TestCursorEncodeIntAndJSONNumberAgree checks that a native int and a json.Number carrying the same
// value marshal to identical bytes, so callers need not know which the decoder would hand back.
func TestCursorEncodeIntAndJSONNumberAgree(t *testing.T) {
	fromInt, err := NewCursor(CursorParameters{{Column: "id", Value: 1}}, true).Encode()
	require.NoError(t, err)

	fromNumber, err := NewCursor(CursorParameters{{Column: "id", Value: json.Number("1")}}, true).Encode()
	require.NoError(t, err)

	assert.Equal(t, fromNumber, fromInt)
}

// TestCursorDecodeLaravelCursor decodes the raw strings Laravel::encode() produced (the "encode:"
// lines above) and asserts the reconstructed parameters and direction.
func TestCursorDecodeLaravelCursor(t *testing.T) {
	tests := []struct {
		name       string
		encoded    string
		wantParams CursorParameters
		wantNext   bool
	}{
		{
			name:       "single int forward",
			encoded:    "eyJpZCI6MSwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
			wantParams: CursorParameters{{Column: "id", Value: json.Number("1")}},
			wantNext:   true,
		},
		{
			name:       "multiple parameters keep order",
			encoded:    "eyJjcmVhdGVkX2F0IjoiMjAyMy0wMS0wMSAwMDowMDowMCIsImlkIjo0MiwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
			wantParams: CursorParameters{{Column: "created_at", Value: "2023-01-01 00:00:00"}, {Column: "id", Value: json.Number("42")}},
			wantNext:   true,
		},
		{
			name:       "null value",
			encoded:    "eyJ4IjpudWxsLCJfcG9pbnRzVG9OZXh0SXRlbXMiOnRydWV9",
			wantParams: CursorParameters{{Column: "x", Value: nil}},
			wantNext:   true,
		},
		{
			name:       "bool value",
			encoded:    "eyJhY3RpdmUiOnRydWUsIl9wb2ludHNUb05leHRJdGVtcyI6dHJ1ZX0",
			wantParams: CursorParameters{{Column: "active", Value: true}},
			wantNext:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cursor := cursorMustDecode(t, test.encoded)
			assert.Equal(t, test.wantParams, cursor.Parameters)
			assert.Equal(t, test.wantNext, cursor.PointsNext)
		})
	}
}

// TestCursorDecodeDirectionFirst shows the direction bit may appear before the parameters, since
// Laravel builds the object with array_merge and Go reads keys in document order.
func TestCursorDecodeDirectionFirst(t *testing.T) {
	cursor := cursorMustDecode(t, cursorB64(`{"_pointsToNextItems":false,"id":1}`))

	assert.Equal(t, CursorParameters{{Column: "id", Value: json.Number("1")}}, cursor.Parameters)
	assert.False(t, cursor.PointsNext)
}

// TestCursorRoundTrip re-encodes decoded cursors and requires the exact input back: values carried
// as json.Number survive verbatim and slice order is stable.
func TestCursorRoundTrip(t *testing.T) {
	encoded := []string{
		"eyJpZCI6MSwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		"eyJpZCI6MSwiX3BvaW50c1RvTmV4dEl0ZW1zIjpmYWxzZX0",
		"eyJjcmVhdGVkX2F0IjoiMjAyMy0wMS0wMSAwMDowMDowMCIsImlkIjo0MiwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		"eyJuYW1lIjoiQWJjIiwiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		"eyJpZCI6OTAwNzE5OTI1NDc0MDk5MywiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ",
		"eyJ4IjpudWxsLCJfcG9pbnRzVG9OZXh0SXRlbXMiOnRydWV9",
		"eyJhY3RpdmUiOnRydWUsIl9wb2ludHNUb05leHRJdGVtcyI6dHJ1ZX0",
		"eyJzY29yZSI6MS41LCJfcG9pbnRzVG9OZXh0SXRlbXMiOnRydWV9",
	}

	for _, want := range encoded {
		got, err := cursorMustDecode(t, want).Encode()
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

// TestCursorDecodePreservesLargeInteger is the precision guard: an id above 2^53 must survive as its
// exact digits, not the nearest float64.
func TestCursorDecodePreservesLargeInteger(t *testing.T) {
	cursor := cursorMustDecode(t, "eyJpZCI6OTAwNzE5OTI1NDc0MDk5MywiX3BvaW50c1RvTmV4dEl0ZW1zIjp0cnVlfQ")

	value, err := cursor.Parameter("id")
	require.NoError(t, err)

	number, ok := value.(json.Number)
	require.True(t, ok, "expected json.Number, got %T", value)
	assert.Equal(t, "9007199254740993", number.String())
	assert.NotEqual(t, "9007199254740992", number.String(), "value was rounded through float64")
}

// TestCursorParameterOrderPreserved asserts the encoded object lists keys in slice order, not
// sorted, which is the whole reason CursorParameters is a slice rather than a map.
func TestCursorParameterOrderPreserved(t *testing.T) {
	cursor := NewCursor(CursorParameters{
		{Column: "z", Value: "1"},
		{Column: "a", Value: "2"},
		{Column: "m", Value: "3"},
	}, true)

	payload, err := cursor.marshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `{"z":"1","a":"2","m":"3","_pointsToNextItems":true}`, string(payload))

	decoded := cursorMustDecode(t, cursorB64(string(payload)))
	assert.Equal(t, cursor.Parameters, decoded.Parameters)
}

// TestCursorMarshalJSON checks the exported JSON projection equals the bytes Encode base64-wraps.
func TestCursorMarshalJSON(t *testing.T) {
	cursor := NewCursor(CursorParameters{{Column: "id", Value: 7}}, false)

	marshaled, err := json.Marshal(cursor)
	require.NoError(t, err)
	assert.Equal(t, `{"id":7,"_pointsToNextItems":false}`, string(marshaled))

	encoded, err := cursor.Encode()
	require.NoError(t, err)
	assert.Equal(t, cursorB64(string(marshaled)), encoded)
}

// TestCursorParameterAndValues covers the lookup methods and their missing-column error.
func TestCursorParameterAndValues(t *testing.T) {
	cursor := NewCursor(CursorParameters{
		{Column: "created_at", Value: "ts"},
		{Column: "id", Value: 5},
	}, true)

	value, err := cursor.Parameter("id")
	require.NoError(t, err)
	assert.Equal(t, 5, value)

	values, err := cursor.Values([]string{"id", "created_at"})
	require.NoError(t, err)
	assert.Equal(t, []any{5, "ts"}, values)

	_, err = cursor.Parameter("missing")
	assert.ErrorIs(t, err, errors.QueryCursorParameterMissing)
	assert.Contains(t, err.Error(), "unable to find parameter [missing]")

	_, err = cursor.Values([]string{"id", "missing"})
	assert.ErrorIs(t, err, errors.QueryCursorParameterMissing)
}

// TestCursorPointsToPreviousItems pins the direction accessors.
func TestCursorPointsToPreviousItems(t *testing.T) {
	assert.True(t, NewCursor(nil, true).PointsNext)
	assert.False(t, NewCursor(nil, true).PointsToPreviousItems())
	assert.True(t, NewCursor(nil, false).PointsToPreviousItems())
}

// TestCursorHas covers the membership helper.
func TestCursorHas(t *testing.T) {
	cursor := NewCursor(CursorParameters{{Column: "id", Value: 1}}, true)

	assert.True(t, cursor.Has("id"))
	assert.False(t, cursor.Has("missing"))
	// Has matches exactly, the same way Parameter and Values do. Has has no Laravel counterpart and every
	// Laravel lookup is an exact key match, so a fold-insensitive lookup here would let Has("ID") answer
	// true for a cursor that Parameter("ID") then refuses with QueryCursorParameterMissing.
	assert.False(t, cursor.Has("ID"))
}

// TestDecodeCursorAcceptsPaddedBase64 shows a cursor that survived URL/padding round trips still
// decodes: the raw flavour is tried first, then the padded ones.
func TestDecodeCursorAcceptsPaddedBase64(t *testing.T) {
	raw := `{"id":1,"_pointsToNextItems":true}`
	padded := base64.URLEncoding.EncodeToString([]byte(raw))
	require.Contains(t, padded, "=")

	cursor := cursorMustDecode(t, padded)
	assert.Equal(t, CursorParameters{{Column: "id", Value: json.Number("1")}}, cursor.Parameters)
}

// TestDecodeCursorRejections exercises every failure path of the decoder, each with its own error.
func TestDecodeCursorRejections(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		want    error
	}{
		{
			name:    "not base64",
			encoded: "!!!not base64!!!",
			want:    errors.QueryCursorInvalid,
		},
		{
			name:    "empty string",
			encoded: "",
			want:    errors.QueryCursorInvalid,
		},
		{
			name:    "valid base64 but not JSON",
			encoded: cursorB64("not json"),
			want:    errors.QueryCursorInvalid,
		},
		{
			name:    "top level array",
			encoded: cursorB64(`[1,2]`),
			want:    errors.QueryCursorNotObject,
		},
		{
			name:    "top level number",
			encoded: cursorB64(`123`),
			want:    errors.QueryCursorNotObject,
		},
		{
			name:    "top level string",
			encoded: cursorB64(`"hello"`),
			want:    errors.QueryCursorNotObject,
		},
		{
			name:    "top level null",
			encoded: cursorB64(`null`),
			want:    errors.QueryCursorNotObject,
		},
		{
			name:    "missing direction bit",
			encoded: cursorB64(`{"id":1}`),
			want:    errors.QueryCursorPointsToNextItemsMissing,
		},
		{
			name:    "direction bit not boolean",
			encoded: cursorB64(`{"id":1,"_pointsToNextItems":"yes"}`),
			want:    errors.QueryCursorPointsToNextItemsInvalid,
		},
		{
			name:    "direction bit is null",
			encoded: cursorB64(`{"id":1,"_pointsToNextItems":null}`),
			want:    errors.QueryCursorPointsToNextItemsInvalid,
		},
		{
			name:    "object value",
			encoded: cursorB64(`{"id":{"nested":1},"_pointsToNextItems":true}`),
			want:    errors.QueryCursorNonScalarValue,
		},
		{
			name:    "array value",
			encoded: cursorB64(`{"id":[1,2],"_pointsToNextItems":true}`),
			want:    errors.QueryCursorNonScalarValue,
		},
		{
			name:    "duplicate parameter",
			encoded: cursorB64(`{"id":1,"id":2,"_pointsToNextItems":true}`),
			want:    errors.QueryCursorDuplicateParameter,
		},
		{
			name:    "trailing garbage",
			encoded: cursorB64(`{"id":1,"_pointsToNextItems":true}extra`),
			want:    errors.QueryCursorInvalid,
		},
		{
			name:    "trailing object",
			encoded: cursorB64(`{"id":1,"_pointsToNextItems":true}{}`),
			want:    errors.QueryCursorInvalid,
		},
		{
			name:    "payload larger than the cap",
			encoded: strings.Repeat("A", encodedCursorLimit()+4),
			want:    errors.QueryCursorPayloadTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeCursor(test.encoded)
			assert.ErrorIs(t, err, test.want)
		})
	}
}

// TestDecodeCursorRejectsOversizedDecodedPayload builds a payload one byte over the cap whose
// base64 fits under the encoded-length guard, so the decode-side size check itself fires.
func TestDecodeCursorRejectsOversizedDecodedPayload(t *testing.T) {
	overhead := len(`{"a":""}`) // braces, key, and quotes around the value
	value := strings.Repeat("a", MaxCursorPayloadSize+1-overhead)
	raw := `{"a":"` + value + `"}`

	require.Equal(t, MaxCursorPayloadSize+1, len(raw))
	require.LessOrEqual(t, len(cursorB64(raw)), encodedCursorLimit())

	_, err := DecodeCursor(cursorB64(raw))
	assert.ErrorIs(t, err, errors.QueryCursorPayloadTooLarge)
}

// TestCursorEncodeRejections covers every failure path of the encoder.
func TestCursorEncodeRejections(t *testing.T) {
	longValue := strings.Repeat("a", MaxCursorPayloadSize+16)

	tests := []struct {
		name   string
		cursor Cursor
		want   error
	}{
		{
			name:   "reserved direction key",
			cursor: NewCursor(CursorParameters{{Column: CursorDirectionKey, Value: true}}, true),
			want:   errors.QueryCursorReservedParameter,
		},
		{
			name: "duplicate column",
			cursor: NewCursor(CursorParameters{
				{Column: "id", Value: 1},
				{Column: "id", Value: 2},
			}, true),
			want: errors.QueryCursorDuplicateParameter,
		},
		{
			name:   "object value",
			cursor: NewCursor(CursorParameters{{Column: "id", Value: map[string]any{"a": 1}}}, true),
			want:   errors.QueryCursorNonScalarValue,
		},
		{
			name:   "slice value",
			cursor: NewCursor(CursorParameters{{Column: "id", Value: []int{1, 2}}}, true),
			want:   errors.QueryCursorNonScalarValue,
		},
		{
			name:   "invalid UTF-8 column",
			cursor: NewCursor(CursorParameters{{Column: string([]byte{0xff, 0xfe}), Value: 1}}, true),
			want:   errors.QueryCursorInvalidValue,
		},
		{
			name:   "unmarshalable value",
			cursor: NewCursor(CursorParameters{{Column: "id", Value: func() {}}}, true),
			want:   errors.QueryCursorInvalidValue,
		},
		{
			name:   "payload over the cap",
			cursor: NewCursor(CursorParameters{{Column: "id", Value: longValue}}, true),
			want:   errors.QueryCursorPayloadTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.cursor.Encode()
			assert.ErrorIs(t, err, test.want)
		})
	}
}
