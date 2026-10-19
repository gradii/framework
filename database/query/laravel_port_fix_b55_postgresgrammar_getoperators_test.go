package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports two Laravel tests/Database/DatabaseQueryBuilderTest.php cases whose clause SQL
// depends on the operators a grammar declares.
//
// testFromQuestionMarkOperatorOnPostgres (Laravel 6972-6986): PostgresGrammar carries its own
// $operators list, which includes the ?-family. Builder::invalidOperator() reads a grammar's
// operators back through Grammar::getOperators(); without the override a "?" is treated as the "="
// shorthand and shifted into the value slot, and the clause never reaches whereBasic to have its "?"
// escaped to "??" (the rewrite that already lives at grammar_where.go). PostgresGrammar.GetOperators
// now returns that dialect list, so the operator survives and whereBasic escapes it.
//
// testBitwiseOperators (Laravel 5095-5128): an operator Builder::isBitwiseOperator() recognises is
// stored as a Bitwise clause rather than a Basic one. The base grammar renders it exactly like a
// basic comparison; Postgres wraps it and casts to ::bool; SQL Server wraps it and compares to zero.
// Postgres adds "#" and the shift spellings through its own $bitwiseOperators, which
// PostgresGrammar.GetBitwiseOperators now reports; "&" is in the base Builder set, so SQL Server
// needs no override.
//
// The corpus SQL is asserted verbatim, with our "?" placeholder and each dialect's identifier
// quoting, reusing laravelCase/runLaravelCases (generic grammar) and dialectCase/runDialectCases
// (named dialect).

// TestBitwiseOperators ports Laravel's testBitwiseOperators.
func TestBitwiseOperators(t *testing.T) {
	// The generic grammar has no bitwise where of its own, so the clause renders like a basic one.
	runLaravelCases(t, []laravelCase{
		{"where bitwise", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("bar", "&", 1)
		}, `select * from "users" where "bar" & ?`},
		{"having bitwise", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("bar", "&", 1)
		}, `select * from "users" having "bar" & ?`},
	})

	runDialectCases(t, []dialectCase{
		{"postgres where hash", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("bar", "#", 1)
		}, `select * from "users" where ("bar" # ?)::bool`},
		{"postgres where shift", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("range", ">>", "[2022-01-08 00:00:00,2022-01-09 00:00:00)")
		}, `select * from "users" where ("range" >> ?)::bool`},
		{"sqlserver where", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("bar", "&", 1)
		}, "select * from [users] where ([bar] & ?) != 0"},
		{"postgres having hash", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("bar", "#", 1)
		}, `select * from "users" having ("bar" # ?)::bool`},
		{"postgres having shift", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("range", ">>", "[2022-01-08 00:00:00,2022-01-09 00:00:00)")
		}, `select * from "users" having ("range" >> ?)::bool`},
		{"sqlserver having", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("bar", "&", 1)
		}, "select * from [users] having ([bar] & ?) != 0"},
	})

	// The right-hand operand is what binds, not the operator: the clause kept its operator instead of
	// shifting it into the value slot.
	assert.Equal(t, []any{1}, NewBuilder(NewPostgresGrammar("")).
		Select("*").From("users", "").Where("bar", "#", 1).GetBindings())
}

// TestFromQuestionMarkOperatorOnPostgres ports Laravel's testFromQuestionMarkOperatorOnPostgres.
func TestFromQuestionMarkOperatorOnPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"question mark", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("roles", "?", "superuser")
		}, `select * from "users" where "roles" ?? ?`},
		{"question mark or", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("roles", "?|", "superuser")
		}, `select * from "users" where "roles" ??| ?`},
		{"question mark and", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("roles", "?&", "superuser")
		}, `select * from "users" where "roles" ??& ?`},
	})

	// Each ?-family operator is clause syntax, so the only binding is the operand. Had the operator
	// not been accepted it would have been shifted into the value slot and bound instead.
	assert.Equal(t, []any{"superuser"}, NewBuilder(NewPostgresGrammar("")).
		Select("*").From("users", "").Where("roles", "?", "superuser").GetBindings())
}
