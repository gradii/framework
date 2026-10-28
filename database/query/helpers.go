package query

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
)

var (
	errRowValuesCountMismatch      = errors.QueryRowValuesCountMismatch
	errSubqueryTypeInvalid         = errors.QuerySubqueryTypeInvalid
	errOrderDirectionInvalid       = errors.QueryOrderDirectionInvalid
	errIncrementAmountNonNumeric   = errors.QueryIncrementAmountNonNumeric
	errIncrementEachNonAssociative = errors.QueryIncrementEachNonAssociative

	errJsonSelectorNotSupported      = errors.QueryJsonSelectorNotSupported
	errJsonContainsNotSupported      = errors.QueryJsonContainsNotSupported
	errJsonContainsKeyNotSupported   = errors.QueryJsonContainsKeyNotSupported
	errJsonLengthNotSupported        = errors.QueryJsonLengthNotSupported
	errJsonOverlapsNotSupported      = errors.QueryJsonOverlapsNotSupported
	errFullTextNotSupported          = errors.QueryFullTextNotSupported
	errUpsertNotSupported            = errors.QueryUpsertNotSupported
	errInsertIgnoreNotSupported      = errors.QueryInsertIgnoreNotSupported
	errLateralJoinNotSupported       = errors.QueryLateralJoinNotSupported
	errCaseSensitiveLikeNotSupported = errors.QueryCaseSensitiveLikeNotSupported
	errNestedArraysNotAllowed        = errors.QueryNestedArraysNotAllowed
)

// mapEntries normalises an associative map argument into ordered column/value pairs, or reports false
// when the value is not a string-keyed map. It backs IncrementEach's columns argument: PHP's
// incrementEach() takes an array whose string keys name columns, and a Go map is the closest
// representation, but only map[string]any was recognised, so a map[string]int (the natural literal for
// numeric amounts) fell through to the non-associative guard. Any map with string keys is accepted, in
// ascending key order (Go maps carry no insertion order); a map with integer keys reports false, since
// PHP would treat it as a non-associative array.
func mapEntries(value any) (core.UpdateValues, bool) {
	if value == nil {
		return nil, false
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}

	keys := rv.MapKeys()
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	entries := make(core.UpdateValues, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, core.UpdateValue{Column: key.String(), Value: rv.MapIndex(key).Interface()})
	}

	return entries, true
}

// wrapValues normalises a single value or a list into a slice.
// Mirrors Arr::wrap().
func wrapValues(value any) []any {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []any:
		return v
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out
	default:
		return []any{value}
	}
}

// flatten mirrors Arr::flatten(): nested slices are collapsed to the given depth. depth 1 flattens a
// single level (Laravel's Arr::flatten($values, 1)); any other depth flattens fully, which is the
// default Arr::flatten($values) with the INF depth Laravel's getBindings() relies on. A []byte is a
// scalar — a PHP binary string — so it is left intact rather than exploded into individual bytes.
func flatten(values []any, depth int) []any {
	out := make([]any, 0, len(values))

	for _, value := range values {
		if value == nil {
			out = append(out, nil)

			continue
		}

		// A SQL Valuer (including named slices) is one parameter. Binary strings likewise retain
		// their bytes instead of being recursively expanded like an ordinary Go value list.
		if !core.IsArrayValue(value) {
			out = append(out, value)

			continue
		}

		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			out = append(out, value)

			continue
		}

		inner := core.ToAnySlice(value)
		if depth == 1 {
			out = append(out, inner...)
		} else {
			out = append(out, flatten(inner, depth-1)...)
		}
	}

	return out
}

// toInt mirrors PHP's (int) cast, which whereIntegerInRaw (Builder.php:1294) and whereJsonLength
// (Builder.php:2215) apply to every value before it is inlined or bound. A numeric-prefixed string
// yields its leading number ("1a" -> 1, "1.9" -> 1), matching PHP; strconv.Atoi would instead fail on
// such a string and yield 0. A string with no leading number yields 0.
func toInt(value any) any {
	switch v := value.(type) {
	case nil:
		return 0
	case bool:
		if v {
			return 1
		}
		return 0
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case string:
		return phpIntCast(v)
	case []byte:
		return phpIntCast(string(v))
	default:
		return 0
	}
}

// phpIntCast returns the integer PHP's (int) cast would produce for the string s: PHP parses the
// leading numeric portion (after optional whitespace and a sign) as a number and truncates it toward
// zero, so "1a" is 1, "1.9" is 1, "1e3" is 1000 and "abc" is 0.
func phpIntCast(s string) int {
	s = strings.TrimLeft(s, " \t\n\r\v\f")

	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}

	mantissa := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	// A sign or a lone "." with no digits is not a number.
	if i == mantissa {
		return 0
	}

	// An exponent belongs to the number only when it is well formed.
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}

			i = j
		}
	}

	parsed, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}

	return int(parsed)
}

// updateValuesFrom normalises the argument ToUpdateSQL accepts into an ordered UpdateValues. An
// UpdateValues (or []UpdateValue) is used as given; a map[string]any is ordered by column name, which
// keeps the behaviour callers had before the ordered form existed. Laravel's Builder::update() takes an
// array and keeps its insertion order, which is what an UpdateValues carries.
func updateValuesFrom(values any) core.UpdateValues {
	switch v := values.(type) {
	case nil:
		return nil
	case core.UpdateValues:
		return v
	case []core.UpdateValue:
		return core.UpdateValues(v)
	case map[string]any:
		keys := core.SortedKeys(v)
		ordered := make(core.UpdateValues, 0, len(keys))
		for _, key := range keys {
			ordered = append(ordered, core.UpdateValue{Column: key, Value: v[key]})
		}
		return ordered
	default:
		return nil
	}
}

// truthy mirrors PHP's loose boolean cast, which is what Conditionable::when()'s `if ($value)` uses to
// decide whether the callback runs. PHP treats false, 0, 0.0, "", "0", an empty array and null as
// falsy and everything else — including any object — as truthy; note that the string "0" is falsy.
// Values reach Go as any, so the dynamic type decides. A struct, pointer, func or channel is treated
// as a non-null object, hence truthy.
func truthy(value any) bool {
	if value == nil {
		return false
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Bool:
		return reflected.Bool()
	case reflect.String:
		s := reflected.String()
		return s != "" && s != "0"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return reflected.Float() != 0
	case reflect.Slice, reflect.Map, reflect.Array:
		return reflected.Len() > 0
	default:
		return true
	}
}

// isNumericValue mirrors PHP's is_numeric(): any integer or float, and a numeric string (an optional
// sign, digits, an optional decimal part and an optional exponent, with surrounding whitespace
// allowed). A []byte is a PHP binary string, not a number.
func isNumericValue(value any) bool {
	switch v := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	case string:
		return isNumericString(v)
	default:
		return false
	}
}

// isNumericString reports whether s parses as a PHP numeric string. PHP accepts a leading sign, a
// decimal point and an exponent, which is the shape strconv.ParseFloat matches; a surrounding space is
// trimmed first because PHP allows it while ParseFloat does not.
func isNumericString(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}

	_, err := strconv.ParseFloat(trimmed, 64)

	return err == nil
}

// amountString renders an increment amount the way PHP interpolates it into the raw expression: an
// integer keeps its digits and a numeric string keeps its text.
func amountString(amount any) string {
	return fmt.Sprint(amount)
}

// mergeUpdateValues mirrors array_merge() for string keys: an extra assignment to a column already
// present keeps that column's position and replaces its value, and a new column is appended.
func mergeUpdateValues(base core.UpdateValues, extra []core.UpdateValue) core.UpdateValues {
	for _, item := range extra {
		replaced := false
		for i := range base {
			if base[i].Column == item.Column {
				base[i].Value = item.Value
				replaced = true

				break
			}
		}
		if !replaced {
			base = append(base, item)
		}
	}

	return base
}

// arrayValues returns the elements of a Go slice or array as []any and reports whether value was one.
// It is how the builder recognises an argument standing for PHP's non-associative array: a list, whose
// keys are integer indices rather than column names. A []byte is a PHP binary string, so it is not a
// list.
func arrayValues(value any) ([]any, bool) {
	if value == nil {
		return nil, false
	}

	if _, ok := value.([]byte); ok {
		return nil, false
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}

	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}

	return out, true
}

// laravelSnake mirrors Illuminate\Support\Str::snake($value) with the default "_" delimiter, which the
// dynamic where path uses to turn a whereXxx method segment into a column name. Laravel inserts a
// delimiter before every ASCII uppercase letter that has a preceding character and lowercases the
// result, with no special handling for consecutive capitals ("userID" -> "user_i_d"); the framework's
// support/str.Snake applies "smart acronym" splitting instead ("userID" -> "user_id"), so the port keeps
// its own copy. The trigger is ASCII-only because Laravel matches "/(.)(?=[A-Z])/", which does not fire
// on a non-ASCII capital ("FooÉ" -> "fooé"); a whereXxx segment is a method name, so it carries no
// whitespace for PHP's ucwords step to act on.
func laravelSnake(value string) string {
	if isLowercaseLetters(value) {
		return value
	}

	var out strings.Builder
	for i, r := range value {
		if i > 0 && r >= 'A' && r <= 'Z' {
			out.WriteRune('_')
		}
		out.WriteRune(r)
	}

	return strings.ToLower(out.String())
}

// isLowercaseLetters mirrors PHP's ctype_lower() for the letters-only input of a method segment: true
// only when every rune is an ASCII lowercase letter, so an empty string is false as it is in PHP 8.
func isLowercaseLetters(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// originalCursorColumn resolves the original select expression behind a cursor order parameter. When
// an order names an alias introduced by the select list ("start_time as created_at"), the keyset
// predicate must compare the underlying expression ("start_time") rather than the alias, so this
// scans the select list for an "<expr> as <alias>" entry whose alias matches the parameter — by its
// bare name or by its grammar-wrapped form — and returns the substring before " as ". A parameter
// with no matching alias is returned unchanged.
//
// Laravel finds the alias with strripos(), which is case-insensitive, so the " as " search runs over a
// lowercased copy while the returned slice comes from the original string. aliasOf returns the half
// AFTER " as " (the alias itself), so it must not be reused here.
func originalCursorColumn(columns []any, parameter string, grammar Grammar) string {
	if len(columns) == 0 {
		return parameter
	}

	wrapped := parameter
	if grammar != nil {
		wrapped = grammar.Wrap(parameter)
	}

	for _, column := range columns {
		value := core.GetValueString(column)

		position := strings.LastIndex(strings.ToLower(value), " as ")
		if position < 0 {
			continue
		}

		original := value[:position]
		alias := value[position+4:]
		if parameter == alias || wrapped == alias {
			return original
		}
	}

	return parameter
}
