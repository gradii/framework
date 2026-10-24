package core

import (
	"strconv"
	"strings"
)

// Dialect names, matching the values carried in database.Config.Driver.
const (
	DialectMySQL     = "mysql"
	DialectPostgres  = "postgres"
	DialectSqlite    = "sqlite"
	DialectSqlServer = "sqlserver"
)

// NormalizeDriver reduces a driver name to the lowercased, separator-free form
// used by dialect identification and grammar selection.
func NormalizeDriver(driver string) string {
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
	switch NormalizeDriver(driver) {
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
