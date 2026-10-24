package core

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// RenderLiteral renders a value as a SQL literal.
//
// The recognised kinds mirror GORM's own statement explainer, which the ToRawSql surface used to
// delegate to: strings and byte slices are single quoted, numbers and booleans are printed bare,
// and anything implementing sql/driver.Valuer is rendered through its own driver value.
func RenderLiteral(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case string:
		return quoteLiteral(v)
	case []byte:
		return quoteLiteral(string(v))
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case time.Time:
		return quoteLiteral(v.Format("2006-01-02 15:04:05.999"))
	case Expression:
		// A nested raw expression (for example the "data"::jsonb fragment and the jsonb_set
		// wrapper a JSON-path update builds) must be rendered verbatim. Without this it fell
		// through to the fmt.Sprint fallback, which printed the struct as {value} and produced a
		// malformed statement.
		return v.Value
	case *Expression:
		if v == nil {
			return "NULL"
		}

		return v.Value
	}

	// Types such as carbon.DateTime carry their database value through driver.Valuer. Rendering
	// that value keeps timestamps and other custom scalars readable in ToRawSql output.
	//
	// A typed nil must render as NULL rather than have Value() called on it: a nil *carbon.DateTime
	// reached this path through a model field and the call panicked inside the implementation
	// ("value method carbon.LayoutType.Value called using nil *LayoutType pointer") instead of
	// returning a value.
	if valuer, ok := value.(driver.Valuer); ok {
		if isNilValue(value) {
			return "NULL"
		}

		// GORM renders whatever Value() returned even when it also returned an error, so keep the
		// value rather than discarding it; a (nil, err) pair still renders as NULL through the
		// recursion below.
		driverValue, _ := valuer.Value()

		return RenderLiteral(driverValue)
	}

	// A nil pointer that carries no driver.Valuer is still nil: without this the fallback would
	// print "<nil>" into the statement, which is how a nil *string field reached ToRawSql output.
	if isNilValue(value) {
		return "NULL"
	}

	// A non-nil pointer with no driver.Valuer holds its value one level down. GORM dereferences it
	// before printing, so do the same rather than emitting the Go address the fallback would print.
	if reflected := reflect.ValueOf(value); reflected.Kind() == reflect.Pointer {
		return RenderLiteral(reflected.Elem().Interface())
	}

	return fmt.Sprint(value)
}

// isNilValue reports whether value is a nil pointer or interface, which is the shape a typed nil
// takes when it is carried behind an interface. Only those kinds short-circuit to NULL: a nil map,
// slice, channel or function still carries a driver value, and GORM calls Value() on such a
// driver.Valuer rather than rendering NULL.
func isNilValue(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Interface, reflect.Pointer:
		return reflected.IsNil()
	default:
		return false
	}
}

// quoteLiteral wraps a value in single quotes, doubling any embedded quote.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
