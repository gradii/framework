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
