package db

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	querybuilder "github.com/goravel/framework/database/query"
)

// Expr is a raw SQL fragment. It is an alias for the query builder's Expression, so the two names
// denote the same type: callers of this package need not import the builder directly, and a value
// built under either name is accepted everywhere an Expression is - by the builder's IsExpression
// and ExpressionValue type switches and by renderLiteral's interpolation switch alike.
//
// It used to be a distinct struct embedding querybuilder.Expression, but Go embedding is not type
// identity: a db.Expr then failed every type switch that names querybuilder.Expression, so a raw
// fragment passed to Raw fell through to the fmt.Sprint fallback and rendered as the struct's
// "{value}" text, producing a malformed statement.
type Expr = querybuilder.Expression

// Raw creates a raw SQL fragment.
//
// Laravel's DB::raw() takes only a string, with bindings supplied separately by whereRaw and
// friends. Goravel's Raw also accepts arguments, so they are rendered into the fragment here; a
// caller that needs parameterisation should use WhereRaw with its bindings instead.
func Raw(expr string, args ...any) any {
	if len(args) == 0 {
		return querybuilder.Raw(expr)
	}

	return querybuilder.Raw(interpolate(expr, args))
}

// interpolate replaces each placeholder with a literal rendering of the argument.
//
// The grammars emit "?" for every dialect, and that is the form database/gorm and Raw() pass, so a
// "?" is rendered positionally. Postgres is the one dialect whose placeholders are rewritten before
// execution: the db facade runs and traces querybuilder.ReplacePlaceholders' "$1", "$2" … form, which
// this did not recognise, so the facade's query log recorded "values ($1)" while the ORM's recorded
// the same statement inlined. A statement carrying numbered placeholders is therefore rendered in
// that form — by each placeholder's own number — and its "?" characters are left alone, because once
// ReplacePlaceholders has run a surviving "?" is an escaped literal (an operator such as postgres's
// "?"), not a binding.
//
// A placeholder inside a single- or double-quoted literal is part of the literal's text, not a
// binding, so the scan tracks quoting and leaves it untouched. This is the same rule
// querybuilder.ReplacePlaceholders follows when it rewrites "?" into "$n" (see
// skip-quoted-literals-in-placeholder-rewrite), so a statement is read the same way by both
// renderers.
func interpolate(expr string, args []any) string {
	if len(args) == 0 {
		return expr
	}

	if hasNumberedPlaceholder(expr) {
		return interpolateNumbered(expr, args)
	}

	var out strings.Builder

	index := 0
	var quote byte
	for i := 0; i < len(expr); i++ {
		char := expr[i]

		if quote != 0 {
			out.WriteByte(char)
			if char == quote {
				quote = 0
			}
			continue
		}

		if char == '\'' || char == '"' {
			quote = char
			out.WriteByte(char)
			continue
		}

		if char == '?' && index < len(args) {
			out.WriteString(renderLiteral(args[index]))
			index++
			continue
		}

		out.WriteByte(char)
	}

	return out.String()
}

// hasNumberedPlaceholder reports whether expr carries a "$<digits>" placeholder, the postgres form
// querybuilder.ReplacePlaceholders produces. A "$<digits>" inside a quoted literal is literal text
// rather than a placeholder, so quoting is tracked and those are ignored; otherwise a "?"-form
// statement embedding, say, 'costs $5' would be mistaken for the numbered form and lose every
// binding.
func hasNumberedPlaceholder(expr string) bool {
	var quote byte
	for i := 0; i+1 < len(expr); i++ {
		char := expr[i]

		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}

		if char == '\'' || char == '"' {
			quote = char
			continue
		}

		if char == '$' && expr[i+1] >= '0' && expr[i+1] <= '9' {
			return true
		}
	}

	return false
}

// interpolateNumbered renders "$<n>" placeholders by their own number, which is the order
// ReplacePlaceholders numbered them in. A "$" that is not followed by a usable number is left as it
// stands, and a "$<n>" inside a quoted literal is literal text rather than a placeholder, so quoting
// is tracked and those are left intact instead of being overwritten and having their quotes doubled.
func interpolateNumbered(expr string, args []any) string {
	var out strings.Builder

	var quote byte
	for i := 0; i < len(expr); i++ {
		char := expr[i]

		if quote != 0 {
			out.WriteByte(char)
			if char == quote {
				quote = 0
			}
			continue
		}

		if char == '\'' || char == '"' {
			quote = char
			out.WriteByte(char)
			continue
		}

		if char != '$' {
			out.WriteByte(char)
			continue
		}

		end := i + 1
		for end < len(expr) && expr[end] >= '0' && expr[end] <= '9' {
			end++
		}

		number, err := strconv.Atoi(expr[i+1 : end])
		if err != nil || number < 1 || number > len(args) {
			out.WriteString(expr[i:end])
			i = end - 1
			continue
		}

		out.WriteString(renderLiteral(args[number-1]))
		i = end - 1
	}

	return out.String()
}

// renderLiteral renders a value as a SQL literal.
//
// The recognised kinds mirror GORM's own statement explainer, which the ToRawSql surface used to
// delegate to: strings and byte slices are single quoted, numbers and booleans are printed bare,
// and anything implementing sql/driver.Valuer is rendered through its own driver value.
func renderLiteral(value any) string {
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
	case querybuilder.Expression:
		// A nested raw expression (for example the "data"::jsonb fragment and the jsonb_set
		// wrapper a JSON-path update builds) must be rendered verbatim. Without this it fell
		// through to the fmt.Sprint fallback, which printed the struct as {value} and produced a
		// malformed statement.
		return v.Value
	case *querybuilder.Expression:
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

		return renderLiteral(driverValue)
	}

	// A nil pointer that carries no driver.Valuer is still nil: without this the fallback would
	// print "<nil>" into the statement, which is how a nil *string field reached ToRawSql output.
	if isNilValue(value) {
		return "NULL"
	}

	// A non-nil pointer with no driver.Valuer holds its value one level down. GORM dereferences it
	// before printing, so do the same rather than emitting the Go address the fallback would print.
	if reflected := reflect.ValueOf(value); reflected.Kind() == reflect.Pointer {
		return renderLiteral(reflected.Elem().Interface())
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
