package db

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"

	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
	basedatabase "github.com/goravel/framework/support/database"
	"github.com/goravel/framework/support/str"
)

type Row struct {
	err error
	row map[string]any
}

func NewRow(row map[string]any, err error) *Row {
	return &Row{row: row, err: err}
}

func (r *Row) Err() error {
	return r.err
}

func (r *Row) Scan(value any) error {
	if r.err != nil {
		return r.err
	}

	msConfig := &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			ToFieldTypeHookFunc(), ToStringHookFunc(), ToTimeHookFunc(), ToDeletedAtHookFunc(), ToScannerHookFunc(), ToSliceHookFunc(), ToMapHookFunc(),
		),
		Squash: true,
		Result: value,
		MatchName: func(mapKey, fieldName string) bool {
			return str.Of(mapKey).Studly().String() == fieldName || mapKey == str.Of(fieldName).Snake().String() || strings.EqualFold(mapKey, fieldName)
		},
	}

	decoder, err := mapstructure.NewDecoder(msConfig)
	if err != nil {
		return err
	}

	return decoder.Decode(r.row)
}

// ToFieldTypeHookFunc converts a value a database driver returned into the type
// declared by the destination field. A driver hands back the database's native
// representation — SQLite reports booleans as integers, PostgreSQL reports some
// string columns as bool, MySQL reports decimals as []byte — which rarely matches
// the field the row is scanned into. The decoder refuses such a pair and the whole
// row is dropped, so this hook performs the conversions GORM used to accept. A
// pair that cannot be coerced is returned untouched, so the decoder still reports
// it as an error.
//
// The coercion stays narrow, so a mismatch is never hidden:
//   - a string field only accepts text, the string a driver returns or the bytes
//     it carries text in; a numeric or boolean source is left for the decoder to
//     reject instead of being stringified into "42" or "true";
//   - a float source only fills an integer field when it is integral and in range;
//     a fractional, NaN or out-of-range value is refused rather than truncated;
//   - the bytes of a byte slice column fill a byte slice field unchanged, while a
//     byte/uint8 field is a number and parses the bytes as a decimal, so binary
//     bytes remain a genuine mismatch and stay a hard error.
func ToFieldTypeHookFunc() mapstructure.DecodeHookFunc {
	scannerType := reflect.TypeOf((*interface{ Scan(any) error })(nil)).Elem()

	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f == nil || t == nil || data == nil || f == t {
			return data, nil
		}

		// A type with its own Scan method handles its own driver values.
		if t.Implements(scannerType) || reflect.PointerTo(t).Implements(scannerType) {
			return data, nil
		}

		source, ok := driverScalar(data)
		if !ok {
			return data, nil
		}

		switch t.Kind() {
		case reflect.Bool:
			if value, ok := scalarToBool(source); ok {
				return value, nil
			}
		case reflect.String:
			if value, ok := scalarToString(source); ok {
				return value, nil
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if value, ok := scalarToInt(source, t.Bits()); ok {
				return value, nil
			}
			if isFloatSource(source) {
				return nil, errors.OrmRowScanValueNotRepresentable.Args(data, source.Type().String(), t.String())
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if value, ok := scalarToUint(source, t.Bits()); ok {
				return value, nil
			}
			if isFloatSource(source) {
				return nil, errors.OrmRowScanValueNotRepresentable.Args(data, source.Type().String(), t.String())
			}
		case reflect.Float32, reflect.Float64:
			if value, ok := scalarToFloat(source, t.Bits()); ok {
				return value, nil
			}
		}

		return data, nil
	}
}

// driverScalar normalises a driver value to a scalar reflect.Value. A byte slice
// is treated as the string a driver uses to carry a text, json or decimal column.
func driverScalar(data any) (reflect.Value, bool) {
	value := reflect.ValueOf(data)
	switch value.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return value, true
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			bytes := make([]byte, value.Len())
			reflect.Copy(reflect.ValueOf(bytes), value)

			return reflect.ValueOf(string(bytes)), true
		}
	}

	return reflect.Value{}, false
}

func scalarToBool(value reflect.Value) (bool, bool) {
	switch value.Kind() {
	case reflect.Bool:
		return value.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() != 0, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint() != 0, true
	case reflect.Float32, reflect.Float64:
		return value.Float() != 0, true
	case reflect.String:
		parsed, err := strconv.ParseBool(value.String())
		if err == nil {
			return parsed, true
		}
	}

	return false, false
}

// scalarToString returns the text a driver source carries. Only a string source
// is text: a driver never reports a text column as a number or a bool, so
// stringifying one would hide a genuine type mismatch from the caller.
func scalarToString(value reflect.Value) (string, bool) {
	if value.Kind() == reflect.String {
		return value.String(), true
	}

	return "", false
}

func scalarToInt(value reflect.Value, bitSize int) (int64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		converted := value.Float()
		if !isIntegralFloat(converted) || converted < math.MinInt64 || converted >= math.MaxInt64 {
			return 0, false
		}

		return int64(converted), true
	case reflect.String:
		parsed, err := strconv.ParseInt(value.String(), 10, bitSize)
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

func scalarToUint(value reflect.Value, bitSize int) (uint64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		converted := value.Int()
		if converted < 0 {
			return 0, false
		}

		return uint64(converted), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), true
	case reflect.Float32, reflect.Float64:
		converted := value.Float()
		if !isIntegralFloat(converted) || converted < 0 || converted >= math.MaxUint64 {
			return 0, false
		}

		return uint64(converted), true
	case reflect.String:
		parsed, err := strconv.ParseUint(value.String(), 10, bitSize)
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

func scalarToFloat(value reflect.Value, bitSize int) (float64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	case reflect.String:
		parsed, err := strconv.ParseFloat(value.String(), bitSize)
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

// isFloatSource reports whether a decode source is a floating point driver value.
func isFloatSource(value reflect.Value) bool {
	return value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64
}

// isIntegralFloat reports whether a floating point value can be narrowed to an
// integer without losing information.
func isIntegralFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value == math.Trunc(value)
}

// isTextSource reports whether a decode source carries text: a string, or the raw
// bytes a driver returns for a text, json or decimal column.
func isTextSource(f reflect.Type) bool {
	if f == nil {
		return false
	}
	if f.Kind() == reflect.String {
		return true
	}

	return f.Kind() == reflect.Slice && f.Elem().Kind() == reflect.Uint8
}

// textSource returns a decode source as a string.
func textSource(data any) (string, bool) {
	if str, ok := data.(string); ok {
		return str, true
	}

	if value, ok := driverScalar(data); ok && value.Kind() == reflect.String {
		return value.String(), true
	}

	return "", false
}

// ToStringHookFunc is a hook function that converts []uint8 to string.
// Mysql returns []uint8 for String type when scanning the rows.
func ToStringHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if t != reflect.TypeOf("") {
			return data, nil
		}

		dataSlice, ok := data.([]uint8)
		if ok {
			return string(dataSlice), nil
		}

		return data, nil
	}
}

func ToTimeHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if t != reflect.TypeOf(time.Time{}) {
			return data, nil
		}

		switch f.Kind() {
		case reflect.String:
			return time.Parse(time.RFC3339, data.(string))
		case reflect.Float64:
			return time.Unix(0, int64(data.(float64))*int64(time.Millisecond)), nil
		case reflect.Int64:
			return time.Unix(0, data.(int64)*int64(time.Millisecond)), nil
		default:
			return data, nil
		}
	}
}

func ToDeletedAtHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if !isDeletedAtType(t) {
			return data, nil
		}

		var value time.Time
		switch typed := data.(type) {
		case time.Time:
			value = typed
		case string:
			value = carbon.Parse(typed).StdTime()
		default:
			return data, nil
		}

		return basedatabase.DeletedAt{Time: value, Valid: true}, nil
	}
}

// isDeletedAtType reports whether a decode target is a soft-delete field.
func isDeletedAtType(t reflect.Type) bool {
	return t == reflect.TypeOf(basedatabase.DeletedAt{})
}

// ToScannerHookFunc is a hook function that handles types with custom Scan methods (sql.Scanner interface).
// This includes carbon types and other custom types implementing the Scan method.
func ToScannerHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		// Skip types that are handled by other specific hooks
		if t == reflect.TypeOf(time.Time{}) || isDeletedAtType(t) {
			return data, nil
		}

		// Skip if source and target are the same type
		if f == t {
			return data, nil
		}

		// Only process database types (string, []byte, []uint8, time.Time)
		if f.Kind() != reflect.String && f != reflect.TypeOf([]byte(nil)) && f != reflect.TypeOf([]uint8(nil)) && f != reflect.TypeOf(time.Time{}) {
			return data, nil
		}

		// Check if the target type implements a Scan method
		scannerType := reflect.TypeOf((*interface{ Scan(any) error })(nil)).Elem()

		// Create a pointer to the target type to check for Scan method
		targetPtr := reflect.PointerTo(t)
		if !targetPtr.Implements(scannerType) {
			return data, nil
		}

		// Handle nil or empty data
		if data == nil {
			return reflect.Zero(t).Interface(), nil
		}
		if str, ok := data.(string); ok && str == "" {
			return reflect.Zero(t).Interface(), nil
		}

		// Create a new instance of the target type
		result := reflect.New(t)
		scanner := result.Interface().(interface{ Scan(any) error })

		// Convert string to []byte if needed (common for JSON fields from database)
		scanData := data
		if str, ok := data.(string); ok {
			scanData = []byte(str)
		}

		// Call the Scan method with the data
		if err := scanner.Scan(scanData); err != nil {
			return nil, err
		}

		return result.Elem().Interface(), nil
	}
}

// ToSliceHookFunc is a hook function that converts JSON string to slice.
func ToSliceHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if t.Kind() != reflect.Slice || !isTextSource(f) {
			return data, nil
		}

		// A byte slice target keeps the raw driver bytes: decoding it as JSON
		// would corrupt a binary value.
		if f.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return data, nil
		}

		str, ok := textSource(data)
		if !ok {
			return data, nil
		}

		// Return empty slice for empty string
		if str == "" {
			return reflect.MakeSlice(t, 0, 0).Interface(), nil
		}

		result := reflect.New(t).Interface()
		if err := json.Unmarshal([]byte(str), result); err != nil {
			return nil, err
		}

		return reflect.ValueOf(result).Elem().Interface(), nil
	}
}

// ToMapHookFunc is a hook function that converts JSON string to map.
func ToMapHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if t.Kind() != reflect.Map || !isTextSource(f) {
			return data, nil
		}

		str, ok := textSource(data)
		if !ok {
			return data, nil
		}

		// Return empty map for empty string
		if str == "" {
			return reflect.MakeMap(t).Interface(), nil
		}

		result := reflect.MakeMap(t).Interface()
		if err := json.Unmarshal([]byte(str), &result); err != nil {
			return nil, err
		}

		return result, nil
	}
}
