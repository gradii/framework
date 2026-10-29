package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RebindGrammar moves a builder's clauses onto another dialect. Clauses are recorded
// dialect-neutral and rendered only at compile time, so a query that changes connection has to keep
// them and swap the grammar with them: a builder carried over unchanged renders the source
// dialect's quoting, and its processor keeps the source dialect's key handling.
func TestRebindGrammarKeepsClausesAndSwapsTheDialect(t *testing.T) {
	source := NewBuilder(NewMySqlGrammar(""), newTestProcessor("mysql"))
	source.SetDriver("mysql")
	source.From("users", "")
	source.Select("name")
	source.Where("id", 1)
	source.OrderBy("id", "asc")
	source.Limit(5)

	const onMySQL = "select `name` from `users` where `id` = ? order by `id` asc limit 5"
	require.Equal(t, onMySQL, source.ToSQL())

	rebound := source.RebindGrammar(NewPostgresGrammar(""), newTestProcessor("postgres"), "postgres")

	assert.Equal(t, `select "name" from "users" where "id" = ? order by "id" asc limit 5`, rebound.ToSQL())
	assert.Equal(t, []any{1}, rebound.GetBindings(), "the where binding must move with its clause")

	// Rebinding hands back a copy: the receiver keeps its clauses and its dialect.
	assert.Equal(t, onMySQL, source.ToSQL())
	assert.Equal(t, "mysql", source.Driver())
	assert.Equal(t, "postgres", rebound.Driver())
}

// A clause written after rebinding lands on the copy, and the copy's own dialect renders it.
func TestRebindGrammarCopiesRatherThanAliases(t *testing.T) {
	source := NewBuilder(NewBaseGrammar(""))
	source.From("users", "")

	rebound := source.RebindGrammar(NewBaseGrammar(""), newTestProcessor("sqlite"), "sqlite")
	rebound.Where("id", 1)

	assert.Equal(t, `select * from "users" where "id" = ?`, rebound.ToSQL())
	assert.Equal(t, `select * from "users"`, source.ToSQL(), "the clause must not reach the receiver")
	assert.Empty(t, source.GetBindings())
}

// A nil processor leaves the source's in place, so a caller that only needs another grammar does not
// have to name one.
func TestRebindGrammarKeepsTheProcessorWhenNoneIsGiven(t *testing.T) {
	processor := newTestProcessor("mysql")

	source := NewBuilder(NewMySqlGrammar(""), processor)
	source.From("users", "")

	rebound := source.RebindGrammar(NewPostgresGrammar(""), nil, "postgres")

	assert.Equal(t, `select * from "users"`, rebound.ToSQL())
	assert.Same(t, processor, rebound.Processor(), "an omitted processor is the one already bound")
}

// The processor follows the dialect a query is rebound to, which is what carries the insert-key
// strategy with it: MySQL reads LastInsertId, Postgres reads the RETURNING row.
func TestRebindGrammarSwapsTheProcessor(t *testing.T) {
	source := NewBuilder(NewMySqlGrammar(""), newTestProcessor("mysql"))
	postgres := newTestProcessor("postgres")

	rebound := source.RebindGrammar(NewPostgresGrammar(""), postgres, "postgres")

	assert.Same(t, postgres, rebound.Processor())
	assert.NotSame(t, source.Processor(), rebound.Processor())
}
