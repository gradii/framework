package core

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// SortedKeys returns a map's keys in ascending order. Laravel ksort()s insert and update records so
// column and placeholder order line up; Go maps are unordered, so sorting is mandatory.
func SortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// LastDotSegment mirrors PHP's last(explode('.', $key)): the key after its final dot, or the key
// itself when it has none. Grammars use it to strip a table qualifier from an update column, so a
// qualified key such as "users.email" assigns to the bare column.
func LastDotSegment(value string) string {
	if index := strings.LastIndex(value, "."); index >= 0 {
		return value[index+1:]
	}
	return value
}

// StringsToAny converts a string slice to []any.
func StringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// FirstAndLast returns the first and last element, mirroring array_first and array_last.
func FirstAndLast(values []any) (any, any) {
	if len(values) == 0 {
		return nil, nil
	}
	return values[0], values[len(values)-1]
}

// JoinAny renders values comma separated, used by the raw in clauses.
func JoinAny(values []any) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprint(value)
	}
	return strings.Join(parts, ", ")
}

// AsString renders a value as a string without panicking on a nil or non-string column.
func AsString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

// GetValueString mirrors PHP's "(string) $this->getValue($value)": an Expression contributes its raw
// SQL, anything else its string form. MySqlGrammar::whereNull()/whereNotNull() stringify the column
// this way before sniffing it for a JSON selector, so a raw "items->id" still takes the json_extract
// path rather than being wrapped as a plain identifier.
func GetValueString(value any) string {
	if IsExpression(value) {
		return ExpressionValue(value)
	}

	return AsString(value)
}

// IsArrayValue reports whether a value is a PHP array in Laravel's is_array() sense: a Go slice or
// map. A []byte is a PHP binary string rather than an array, so it is left out. It decides which
// update values the grammars json-encode before binding.
func IsArrayValue(value any) bool {
	switch value.(type) {
	case []any, map[string]any:
		return true
	case []byte:
		return false
	}

	if value == nil {
		return false
	}

	kind := reflect.ValueOf(value).Kind()

	return kind == reflect.Slice || kind == reflect.Array
}

// ToAnySlice normalises any slice or array to []any. Go callers pass []int, []string and other
// concrete slice types where Laravel would pass a plain array, so the builder must accept them all.
func ToAnySlice(value any) []any {
	switch v := value.(type) {
	case nil:
		return nil
	case []any:
		return v
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = rv.Index(i).Interface()
		}
		return out
	default:
		return []any{value}
	}
}
