package grammars_test

import (
	"strings"
	"testing"

	"github.com/gradii/framework/database/query/core"
	. "github.com/gradii/framework/database/query/grammars"
	"github.com/stretchr/testify/assert"
)

// The upstream mocks only use the builder's public from property for these grammar cases.
type grammarTestQuery struct {
	core.Query
	state core.State
}

func (q *grammarTestQuery) State() *core.State { return &q.state }

// DatabaseQueryGrammarTest::testWhereRawReturnsStringWhenExpressionPassed.
func TestDatabaseQueryGrammar_WhereRawReturnsStringWhenExpressionPassed(t *testing.T) {
	grammar := NewBaseGrammar("")
	assert.Equal(t, `select * from "users"`, grammar.WhereRaw(nil, core.Where{SQL: core.Raw(`select * from "users"`)}))
}

// DatabaseQueryGrammarTest::testWhereRawReturnsStringWhenStringPassed.
func TestDatabaseQueryGrammar_WhereRawReturnsStringWhenStringPassed(t *testing.T) {
	grammar := NewBaseGrammar("")
	assert.Equal(t, `select * from "users"`, grammar.WhereRaw(nil, core.Where{SQL: `select * from "users"`}))
}

// DatabaseQueryGrammarTest::testCompileOrdersAcceptsExpression.
func TestDatabaseQueryGrammar_CompileOrdersAcceptsExpression(t *testing.T) {
	grammar := NewBaseGrammar("")
	orders := []core.Order{{SQL: core.Raw(`length("name") desc`)}}
	assert.Equal(t, `order by length("name") desc`, strings.ToLower(grammar.CompileOrders(nil, orders)))
}

// DatabaseQueryGrammarTest::testCompileOrdersAcceptsExpressionWithPlaceholders.
func TestDatabaseQueryGrammar_CompileOrdersAcceptsExpressionWithPlaceholders(t *testing.T) {
	grammar := NewBaseGrammar("")
	orders := []core.Order{{SQL: core.Raw("field(status, ?, ?) asc")}}
	assert.Equal(t, "order by field(status, ?, ?) asc", strings.ToLower(grammar.CompileOrders(nil, orders)))
}

func TestDatabaseMySqlQueryGrammar_ToRawSql(t *testing.T) {
	assertLaravelRawSQL(t, NewMySqlGrammar(""),
		`select * from "users" where 'Hello\'World?' IS NOT NULL AND "email" = ?`,
		`select * from "users" where 'Hello\'World?' IS NOT NULL AND "email" = 'foo'`)
}

func TestDatabaseMariaDbQueryGrammar_ToRawSql(t *testing.T) {
	assertLaravelRawSQL(t, NewMariaDbGrammar(""),
		`select * from "users" where 'Hello\'World?' IS NOT NULL AND "email" = ?`,
		`select * from "users" where 'Hello\'World?' IS NOT NULL AND "email" = 'foo'`)
}

func TestDatabaseSQLiteQueryGrammar_ToRawSql(t *testing.T) {
	assertLaravelRawSQL(t, NewSqliteGrammar(""),
		`select * from "users" where 'Hello''World?' IS NOT NULL AND "email" = ?`,
		`select * from "users" where 'Hello''World?' IS NOT NULL AND "email" = 'foo'`)
}

func TestDatabaseSqlServerQueryGrammar_ToRawSql(t *testing.T) {
	assertLaravelRawSQL(t, NewSqlServerGrammar(""),
		`select * from [users] where 'Hello''World?' IS NOT NULL AND [email] = ?`,
		`select * from [users] where 'Hello''World?' IS NOT NULL AND [email] = 'foo'`)
}

func TestDatabasePostgresQueryGrammar_ToRawSql(t *testing.T) {
	assertLaravelRawSQL(t, NewPostgresGrammar(""),
		`select * from "users" where '{}' ?? 'Hello\'\'World?' AND "email" = ?`,
		`select * from "users" where '{}' ? 'Hello\'\'World?' AND "email" = 'foo'`)
}

// Each dialect's Database*QueryGrammarTest::testToRawSql mocks Connection::escape('foo', false).
func assertLaravelRawSQL(t *testing.T, grammar Grammar, statement, want string) {
	t.Helper()
	calls := 0
	grammar.SetEscaper(func(value any, binary bool) string {
		calls++
		assert.Equal(t, "foo", value)
		assert.False(t, binary)
		return "'foo'"
	})
	assert.Equal(t, want, grammar.SubstituteBindingsIntoRawSQL(statement, []any{"foo"}))
	assert.Equal(t, 1, calls)
}

// DatabasePostgresQueryGrammarTest::testCustomOperators.
func TestDatabasePostgresQueryGrammar_CustomOperators(t *testing.T) {
	CustomOperators([]any{"@@@", "@>", ""})
	CustomOperators([]any{"@@>", 1})
	operators := NewPostgresGrammar("").GetOperators()
	assert.Contains(t, operators, "@@@")
	assert.Contains(t, operators, "@@>")
	assert.NotContains(t, operators, "")
	seen := make(map[string]bool)
	for _, operator := range operators {
		assert.False(t, seen[operator], "duplicate operator %q", operator)
		seen[operator] = true
	}
}

// DatabasePostgresQueryGrammarTest::testCompileTruncate.
func TestDatabasePostgresQueryGrammar_CompileTruncate(t *testing.T) {
	grammar := NewPostgresGrammar("")
	query := &grammarTestQuery{state: core.State{From: "users"}}
	defer CascadeOnTruncate(strings.HasSuffix(grammar.CompileTruncate(query)[0].SQL, " cascade"))
	CascadeOnTruncate(true)
	assert.Equal(t, []core.Statement{{SQL: `truncate "users" restart identity cascade`}}, grammar.CompileTruncate(query))
	CascadeOnTruncate(false)
	assert.Equal(t, []core.Statement{{SQL: `truncate "users" restart identity`}}, grammar.CompileTruncate(query))
	CascadeOnTruncate(true)
	assert.Equal(t, []core.Statement{{SQL: `truncate "users" restart identity cascade`}}, grammar.CompileTruncate(query))
}
