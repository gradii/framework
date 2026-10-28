package grammars

import (
	"encoding/json"
	"github.com/gradii/framework/database/query/core"
	"reflect"
	"strings"
)

// EncodeJSON renders a value as JSON, mirroring PHP's json_encode for the shapes an update value can
// take: strings are quoted, booleans and null keep their spelling, and slices and maps recurse. It is
// what the grammars bind for a json-encoded update value: SQLite and Postgres array values, and every
// non-expression Postgres json-selector value, scalar or not.
func EncodeJSON(value any) any {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return jsonString(v)
	case []byte:
		return jsonString(string(v))
	case []any:
		return encodeJSONArray(v)
	case map[string]any:
		return encodeJSONObject(v)
	}

	// Dereference pointers one level so a value carried as *float64/*string/... is encoded as the
	// scalar it points at, not as its Go address. PHP has no such pointer layer: json_encode() of
	// the value a pointer stands for is the faithful rendering.
	if rv := reflect.ValueOf(value); rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "null"
		}
		return EncodeJSON(rv.Elem().Interface())
	}

	if core.IsArrayValue(value) {
		return encodeJSONArray(core.ToAnySlice(value))
	}

	return core.AsString(value)
}

// encodeJSONArray renders a slice as a JSON array.
func encodeJSONArray(values []any) string {
	parts := make([]string, len(values))
	for i, item := range values {
		parts[i] = core.AsString(EncodeJSON(item))
	}

	return "[" + strings.Join(parts, ",") + "]"
}

// encodeJSONObject renders a map as a JSON object with its keys sorted, the order a Go map can give
// deterministically.
func encodeJSONObject(values map[string]any) string {
	keys := core.SortedKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, jsonString(key)+":"+core.AsString(EncodeJSON(values[key])))
	}

	return "{" + strings.Join(parts, ",") + "}"
}

// jsonString quotes a string for JSON, escaping backslashes and double quotes.
func jsonString(value string) string {
	var encoded strings.Builder
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return strings.TrimSuffix(encoded.String(), "\n")
}
