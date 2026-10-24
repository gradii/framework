package grammars

import (
	"strconv"
	"strings"
	"sync"

	"github.com/goravel/framework/errors"
)

// Dialect names, matching the values carried in database.Config.Driver.
const (
	DialectMySQL     = "mysql"
	DialectPostgres  = "postgres"
	DialectSqlite    = "sqlite"
	DialectSqlServer = "sqlserver"
)

// normalizeDriver reduces a driver name to the lowercased, separator-free form both CanonicalDialect
// and NewGrammar match on, so the two agree on what a driver name denotes.
func normalizeDriver(driver string) string {
	normalized := strings.ToLower(strings.TrimSpace(driver))
	return strings.NewReplacer(" ", "", "_", "", "-", "").Replace(normalized)
}

// CanonicalDialect maps a driver name onto the dialect string this package switches on.
//
// The four driver modules report their names capitalised and, for SQL Server, with a space
// ("PostgreSQL", "MySQL", "SQLite", "SQL Server"), while an application's config may carry either
// that name or the lowercase form. Matching therefore happens on a trimmed, lowercased name with
// the separators removed and the common aliases folded in. An unknown driver yields "".
func CanonicalDialect(driver string) string {
	switch normalizeDriver(driver) {
	case "mysql", "mariadb":
		return DialectMySQL
	case "postgres", "postgresql", "pgsql", "pgx", "pq":
		return DialectPostgres
	case "sqlite", "sqlite3":
		return DialectSqlite
	case "sqlserver", "mssql", "sqlsrv":
		return DialectSqlServer
	default:
		return ""
	}
}

// NewGrammar returns the grammar for a driver name. The second result reports whether the driver
// is recognised; an unrecognised driver yields the base grammar and false.
//
// The dialect is identified by the connection's driver string rather than by the driver module,
// because the four driver modules are separate repositories and must not change for this port.
func NewGrammar(driver string, prefix string) (Grammar, bool) {
	// MariaDB ships its own grammar in Laravel (MariaDbGrammar), which extends the MySQL grammar but
	// refuses lateral joins. CanonicalDialect deliberately folds "mariadb" onto DialectMySQL for the
	// quoting, placeholder and locking consumers, so the grammar is selected from the driver name here.
	if normalizeDriver(driver) == "mariadb" {
		return NewMariaDbGrammar(prefix), true
	}

	switch CanonicalDialect(driver) {
	case DialectMySQL:
		return NewMySqlGrammar(prefix), true
	case DialectPostgres:
		return NewPostgresGrammar(prefix), true
	case DialectSqlite:
		return NewSqliteGrammar(prefix), true
	case DialectSqlServer:
		return NewSqlServerGrammar(prefix), true
	default:
		return NewBaseGrammar(prefix), false
	}
}

// unrecognisedDrivers remembers the driver names already reported through
// UnrecognisedDriverWarning, so the warning is logged once per driver per process.
var unrecognisedDrivers sync.Map

// UnrecognisedDriverWarning returns the declared error to log for a driver NewGrammar does not
// recognise, and nil for a recognised driver or one already reported. Callers log the error as a
// warning rather than failing: the framework deliberately lets a connection carry a database/sql
// driver name with no dedicated grammar, because a driver module may bring its own, and such a
// connection runs on the base grammar. The warning names the driver and the recognised set, so a
// mistyped driver is signalled at configuration time instead of surfacing as base-grammar SQL at
// the database. It reports each driver at most once per process: the construction paths run per
// transaction and per query clone, where a repeating warning would be noise.
func UnrecognisedDriverWarning(driver string) error {
	if CanonicalDialect(driver) != "" {
		return nil
	}

	if _, reported := unrecognisedDrivers.LoadOrStore(driver, struct{}{}); reported {
		return nil
	}

	return errors.QueryGrammarDriverNotRecognised.Args(driver)
}

// ReplacePlaceholders rewrites the grammar's "?" placeholders into the dialect's own form.
//
// The grammars emit "?" for every dialect, exactly as Laravel does. Postgres needs "$1", "$2" and
// so on, which is what the existing squirrel based builder produced through sq.Dollar. SQL Server
// needs "@p1", "@p2" and so on, because go-mssqldb "uses native "@" parameter placeholder names
// and does no pre-processing" and names an ordinal parameter "@pN" (its doc.go). MySQL and SQLite
// take "?" as it stands and are returned unchanged. A doubled "??" is an escaped literal question
// mark, used by operators such as "?", and is left as a single "?".
//
// A "?" inside a single- or double-quoted literal is part of that literal's text rather than a
// binding, so quoting is tracked and such a mark is left exactly as it stands: rewriting it would
// both corrupt the literal and shift every following placeholder's number. This is the same rule
// database/db's Raw interpolation follows, so a statement is read the same way by both renderers.
func ReplacePlaceholders(sql string, driver string) string {
	var prefix string
	switch CanonicalDialect(driver) {
	case DialectPostgres:
		prefix = "$"
	case DialectSqlServer:
		prefix = "@p"
	default:
		return sql
	}

	var out strings.Builder
	out.Grow(len(sql) + 8)

	index := 0
	var quote byte
	for i := 0; i < len(sql); i++ {
		char := sql[i]

		if quote != 0 {
			// Inside a quoted literal every byte is text, including a "?"; copying it verbatim
			// keeps the literal intact and the placeholder count unchanged.
			out.WriteByte(char)
			if i+1 < len(sql) && (char == '\\' || char == quote && sql[i+1] == quote) {
				i++
				out.WriteByte(sql[i])
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

		if char != '?' {
			out.WriteByte(char)
			continue
		}

		if i+1 < len(sql) && sql[i+1] == '?' {
			out.WriteByte('?')
			i++
			continue
		}

		index++
		out.WriteString(prefix)
		out.WriteString(strconv.Itoa(index))
	}

	return out.String()
}
