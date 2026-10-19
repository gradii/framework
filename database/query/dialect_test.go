package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewGrammar(t *testing.T) {
	tests := []struct {
		driver string
		quoted string
		known  bool
	}{
		{"mysql", "`users`", true},
		{"postgres", `"users"`, true},
		{"sqlite", `"users"`, true},
		{"sqlserver", "[users]", true},
		{"unknown", `"users"`, false},
	}

	for _, test := range tests {
		t.Run(test.driver, func(t *testing.T) {
			grammar, known := NewGrammar(test.driver, "")
			assert.Equal(t, test.known, known)
			assert.Equal(t, test.quoted, grammar.WrapTable("users"))
		})
	}
}

func TestReplacePlaceholders(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		sql    string
		want   string
	}{
		{"mysql keeps question marks", "mysql", `where "id" = ? and "name" = ?`, `where "id" = ? and "name" = ?`},
		{"postgres numbers them", "postgres", `where "id" = ? and "name" = ?`, `where "id" = $1 and "name" = $2`},
		{"sqlite keeps question marks", "sqlite", `where "id" = ?`, `where "id" = ?`},
		{"postgres unescapes an adjacent doubled mark", "postgres", `where "data" ?? and "id" = ?`, `where "data" ? and "id" = $1`},
		{"sqlserver numbers them", "sqlserver", `where "id" = ? and "name" = ?`, `where "id" = @p1 and "name" = @p2`},
		{"sqlserver unescapes an adjacent doubled mark", "sqlserver", `where "data" ?? and "id" = ?`, `where "data" ? and "id" = @p1`},
		{"postgres leaves a mark inside a string literal", "postgres", `where "note" = '?' and "id" = ?`, `where "note" = '?' and "id" = $1`},
		{"sqlserver leaves a mark inside a string literal", "sqlserver", `where "note" = '?' and "id" = ?`, `where "note" = '?' and "id" = @p1`},
		{"postgres leaves a mark inside an escaped quote", "postgres", `where "note" = 'it''s ?' and "id" = ?`, `where "note" = 'it''s ?' and "id" = $1`},
		{"postgres leaves a mark inside a quoted identifier", "postgres", `where "we?rd" = ?`, `where "we?rd" = $1`},
		{"no placeholders", "postgres", `select * from "users"`, `select * from "users"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, ReplacePlaceholders(test.sql, test.driver))
		})
	}
}

// The four driver modules report their names capitalised, and SQL Server's carries a space, so this
// is what a connection's database.Config.Driver holds in an application. The dialect lookup has to
// accept those names, not only the lowercase short form.
func TestNewGrammarWithDriverModuleNames(t *testing.T) {
	tests := []struct {
		driver string
		quoted string
	}{
		{"PostgreSQL", `"users"`},
		{"MySQL", "`users`"},
		{"SQLite", `"users"`},
		{"SQL Server", "[users]"},
	}

	for _, test := range tests {
		t.Run(test.driver, func(t *testing.T) {
			grammar, known := NewGrammar(test.driver, "")
			assert.True(t, known)
			assert.Equal(t, test.quoted, grammar.WrapTable("users"))
		})
	}
}

func TestCanonicalDialect(t *testing.T) {
	tests := map[string]string{
		"postgres":    DialectPostgres,
		"PostgreSQL":  DialectPostgres,
		"postgresql":  DialectPostgres,
		"pgx":         DialectPostgres,
		"MySQL":       DialectMySQL,
		"mariadb":     DialectMySQL,
		"SQLite":      DialectSqlite,
		"SQL Server":  DialectSqlServer,
		"sqlserver":   DialectSqlServer,
		"mssql":       DialectSqlServer,
		" sql_server": DialectSqlServer,
		"unknown":     "",
		"":            "",
	}

	for driver, want := range tests {
		assert.Equal(t, want, CanonicalDialect(driver), driver)
	}
}

func TestReplacePlaceholdersWithDriverModuleNames(t *testing.T) {
	assert.Equal(t, `where "id" = $1`, ReplacePlaceholders(`where "id" = ?`, "PostgreSQL"))
	assert.Equal(t, `where "id" = ?`, ReplacePlaceholders(`where "id" = ?`, "MySQL"))
	assert.Equal(t, `where "id" = @p1`, ReplacePlaceholders(`where "id" = ?`, "SQL Server"))
}
