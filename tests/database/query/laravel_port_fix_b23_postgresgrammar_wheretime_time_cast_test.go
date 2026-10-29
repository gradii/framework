package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
)

// This file ports the Postgres "where time" cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php: testWhereTimePostgres,
// testWhereTimeOperatorOptionalPostgres and testOrWhereTimePostgres.
//
// They belong together because they all exercise one grammar method:
// PostgresGrammar::whereTime() (Query/Grammars/PostgresGrammar.php:128-138). Postgres does not call a
// function named after the part it compares; it casts the column, so whereTime() renders
// "<column>::time" and parenthesises the column first when it is a JSON selector. Only the day, month
// and year clauses fall back to the base dateBasedWhere() and its extract() form.
//
// whereTime owns no builder logic of its own: Builder::whereTime() (and orWhereTime(), which delegates
// to it with the "or" boolean) append a 'Time' descriptor through addDateBasedWhere(), and
// PostgresGrammar::whereTime() compiles it. Each case below rebuilds the corpus chain through our
// Builder, over the Postgres grammar and its double-quote identifier quoting, and asserts the exact SQL
// the corpus asserts, with the binding list asserted inline the way the corpus does.
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go.

// TestWhereTimePostgres ports Laravel's testWhereTimePostgres.
func TestWhereTimePostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"plain column", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", ">=", "22:00")
		}, `select * from "users" where "created_at"::time >= ?`},
		// A JSON selector is parenthesised before the cast, so the ->> extraction binds tighter.
		{"json selector", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("result->created_at", ">=", "22:00")
		}, `select * from "users" where ("result"->>'created_at')::time >= ?`},
	})

	assert.Equal(t, []any{"22:00"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", ">=", "22:00").GetBindings())
	assert.Equal(t, []any{"22:00"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereTime("result->created_at", ">=", "22:00").GetBindings())
}

// TestWhereTimeOperatorOptionalPostgres ports Laravel's testWhereTimeOperatorOptionalPostgres: the
// omitted operator becomes "=" and the lone argument is the value.
func TestWhereTimeOperatorOptionalPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"operator optional, bound value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "22:00")
		}, `select * from "users" where "created_at"::time = ?`},
		// A raw value is not parameterised: the cast is still applied around the column.
		{"operator optional, raw value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", Raw("NOW()"))
		}, `select * from "users" where "created_at"::time = NOW()`},
	})

	assert.Equal(t, []any{"22:00"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "22:00").GetBindings())
	// assertEquals([], $builder->getBindings()) for the Raw form.
	assert.Empty(t, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", Raw("NOW()")).GetBindings())
}

// TestOrWhereTimePostgres ports Laravel's testOrWhereTimePostgres: orWhereTime() delegates to
// whereTime() with the "or" boolean, so both clauses go through the same cast.
func TestOrWhereTimePostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", ">=", "22:00")
		}, `select * from "users" where "created_at"::time <= ? or "created_at"::time >= ?`},
	})

	assert.Equal(t, []any{"10:00", "22:00"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", ">=", "22:00").GetBindings())
}

// TestPostgresWhereTimeLeavesExtractToTheDateParts is our own companion to the corpus cases: after the
// ::time cast for whereTime(), the day, month and year clauses still render through
// PostgresGrammar::dateBasedWhere()'s extract(...) form, which is the one place Postgres does read the
// part out of the timestamp. It keeps the neighbouring date-part expectations honest about the split.
func TestPostgresWhereTimeLeavesExtractToTheDateParts(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"postgres day still extracts", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, `select * from "users" where extract(day from "created_at") = ?`},
		{"postgres month still extracts", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 1)
		}, `select * from "users" where extract(month from "created_at") = ?`},
		{"postgres year still extracts", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", "=", 2024)
		}, `select * from "users" where extract(year from "created_at") = ?`},
	})
}
