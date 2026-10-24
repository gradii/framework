package db

import (
	"strconv"
	"strings"
	"time"

	dbdriver "github.com/goravel/framework/database/driver"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/database/query/grammars"
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

// explainWithDriver uses the same scalar escaping as DB.Escape, then substitutes the
// placeholders already prepared for database/sql. Expressions prevent a second quoting pass.
func explainWithDriver(statement string, bindings []any, driver string) (string, error) {
	escaped := make([]any, len(bindings))
	for i, value := range bindings {
		switch scalar := value.(type) {
		case bool:
			value = 0
			if scalar {
				value = 1
			}
		case time.Time:
			format := "2006-01-02 15:04:05"
			if querybuilder.CanonicalDialect(driver) == querybuilder.DialectSqlServer {
				format += ".000"
			}
			value = scalar.Format(format)
		}
		_, binary := value.([]byte)
		literal, err := dbdriver.Escape(driver, value, binary)
		if err != nil {
			return "", err
		}
		escaped[i] = querybuilder.Raw(literal)
	}
	return interpolate(statement, escaped), nil
}

// interpolate replaces each placeholder with a literal rendering of the argument.
//
// The grammars emit "?" for every dialect, and that is the form database/gorm and Raw() pass, so a
// "?" is rendered positionally. Postgres and SQL Server are the dialects whose placeholders are
// rewritten before execution: the db facade runs and traces querybuilder.ReplacePlaceholders' "$1",
// "$2" … form for postgres and its "@p1", "@p2" … form for SQL Server. Neither form was recognised
// here, so the facade's query log recorded "values ($1)" or left every "@pN" binding in place while
// the ORM's recorded the same statement inlined. A statement carrying either numbered form is
// therefore rendered by each placeholder's own number, and its "?" characters are left alone,
// because once ReplacePlaceholders has run a surviving "?" is an escaped literal (an operator such
// as postgres's "?"), not a binding.
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

	if prefix := numberedPlaceholderPrefix(expr); prefix != "" {
		return interpolateNumbered(expr, args, prefix)
	}

	var out strings.Builder

	index := 0
	var quote byte
	for i := 0; i < len(expr); i++ {
		char := expr[i]

		if quote != 0 {
			out.WriteByte(char)
			if i+1 < len(expr) && (char == '\\' || char == quote && expr[i+1] == quote) {
				i++
				out.WriteByte(expr[i])
				continue
			}
			if char == quote {
				quote = 0
			}
			continue
		}

		if char == '\'' || char == '"' || char == '`' {
			quote = char
			out.WriteByte(char)
			continue
		}

		if char == '?' && i+1 < len(expr) && expr[i+1] == '?' {
			out.WriteString("??")
			i++
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

// numberedPlaceholderPrefix reports the numbered placeholder form expr carries: "$" for the postgres
// form querybuilder.ReplacePlaceholders produces, "@p" for the SQL Server form it produces for
// go-mssqldb (whose ordinal parameters are named "@p1", "@p2" and so on), or "" for the "?" form. A
// marker inside a quoted literal is literal text rather than a placeholder, so quoting is tracked
// and those are ignored; otherwise a "?"-form statement embedding, say, 'costs $5' would be mistaken
// for the numbered form and lose every binding.
func numberedPlaceholderPrefix(expr string) string {
	var quote byte
	for i := 0; i < len(expr); i++ {
		char := expr[i]

		if quote != 0 {
			if i+1 < len(expr) && (char == '\\' || char == quote && expr[i+1] == quote) {
				i++
				continue
			}
			if char == quote {
				quote = 0
			}
			continue
		}

		if char == '\'' || char == '"' || char == '`' {
			quote = char
			continue
		}

		if char == '$' && i+1 < len(expr) && isDigit(expr[i+1]) {
			return "$"
		}

		if char == '@' && i+2 < len(expr) && expr[i+1] == 'p' && isDigit(expr[i+2]) {
			return "@p"
		}
	}

	return ""
}

// isDigit reports whether char is an ASCII decimal digit.
func isDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

// interpolateNumbered renders the prefix form's placeholders ("$<n>" or "@p<n>") by their own
// number, which is the order ReplacePlaceholders numbered them in. A marker that is not followed by
// a usable number is left as it stands, and a placeholder inside a quoted literal is literal text
// rather than a binding, so quoting is tracked and those are left intact instead of being
// overwritten and having their quotes doubled.
func interpolateNumbered(expr string, args []any, prefix string) string {
	var out strings.Builder

	var quote byte
	for i := 0; i < len(expr); i++ {
		char := expr[i]

		if quote != 0 {
			out.WriteByte(char)
			if i+1 < len(expr) && (char == '\\' || char == quote && expr[i+1] == quote) {
				i++
				out.WriteByte(expr[i])
				continue
			}
			if char == quote {
				quote = 0
			}
			continue
		}

		if char == '\'' || char == '"' || char == '`' {
			quote = char
			out.WriteByte(char)
			continue
		}

		if !strings.HasPrefix(expr[i:], prefix) {
			out.WriteByte(char)
			continue
		}

		end := i + len(prefix)
		for end < len(expr) && isDigit(expr[end]) {
			end++
		}

		if end == i+len(prefix) {
			// The prefix is not followed by a number, so it is not a placeholder: write it
			// through as text rather than consuming a binding.
			out.WriteString(prefix)
			i = end - 1
			continue
		}

		number, err := strconv.Atoi(expr[i+len(prefix) : end])
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

// renderLiteral shares the SQL preview value formatter with query grammars.
func renderLiteral(value any) string { return grammars.RenderLiteral(value) }
