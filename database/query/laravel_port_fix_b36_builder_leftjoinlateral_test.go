package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports Laravel's tests/Database/DatabaseQueryBuilderTest.php:testMySqlSoundsLikeOperator.
//
// MySqlGrammar declares the clause operator "sounds like" in $operators, which Builder::invalidOperator()
// reads back through Grammar::getOperators(). Our BaseGrammar.GetOperators returned nil and MySqlGrammar
// did not override it, so an unknown operator fell through to the "=" shorthand and was shifted into the
// value slot: where('name', 'sounds like', 'John Doe') compiled to `name` = ? with "sounds like" bound as
// the value. MySqlGrammar now overrides GetOperators to return ["sounds like"], matching Laravel, and the
// operator survives into the clause. Only MySQL declares it; the other dialects keep Laravel's behaviour
// of refusing it and treating it as the "=" shorthand.
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go, and the expectation is the corpus SQL
// verbatim, with our "?" placeholder and MySQL identifier quoting.

// TestMySqlSoundsLikeOperator ports Laravel's testMySqlSoundsLikeOperator.
func TestMySqlSoundsLikeOperator(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"sounds like operator survives on mysql", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("name", "sounds like", "John Doe")
		}, "select * from `users` where `name` sounds like ?"},
	})

	// The operator is clause syntax, not a value: the only binding is the right-hand operand.
	assert.Equal(t, []any{"John Doe"}, NewBuilder(NewMySqlGrammar("")).
		Select("*").From("users", "").Where("name", "sounds like", "John Doe").GetBindings())
}
