package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the Arr::flatten slice of Laravel's tests/Database/DatabaseQueryBuilderTest.php for
// the binding path. Laravel flattens nested values before they become bindings: whereBetween and
// havingBetween call Arr::flatten() on the bounds (Builder.php:1402, :2640), whereIntegerInRaw calls
// Arr::flatten() before casting each value to an int (Builder.php:1294), and getBindings() is
// Arr::flatten($this->bindings), so a binding that is a nested list still expands into individual
// bindings. whereIn additionally refuses a nested list whose one-level flatten would change the value
// count (Builder.php:1228).
//
// The SQL and binding expectations were confirmed against the oracle at
// .claude/laravel-oracle/oracle.php (illuminate/database v12.55.1); the oracle agrees with the corpus.
//
// testWhereIntegerInRaw's cast half — ['1a', 2, Bar::FOO] -> in (1, 2, 5) — is not ported here: it
// needs PHP's (int) cast for numeric-prefixed strings and a BackedEnum value, which is work item b15
// (PHP (int) cast semantics in toInt), not this one. The nested-list flatten that test also needs is
// covered by TestWhereIntegerInRawFlattenNested using integer values, isolating it from the cast.

// flattenCase is a laravelCase plus the bindings the corpus asserts for it. An empty binding
// expectation is nil, because a builder with no bindings returns a nil slice.
type flattenCase struct {
	laravelCase
	bindings []any
}

func runFlattenCases(t *testing.T, cases []flattenCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := test.build(newTestBuilder())

			assert.Equal(t, test.want, builder.ToSQL())
			assert.Equal(t, test.bindings, builder.GetBindings())
		})
	}
}

// TestWhereBetweens ports Laravel's testWhereBetweens (corpus L1026-1067). The bounds are flattened
// before binding, so a nested list such as [[1, 2, 3]] contributes 1 and 2, not one array binding.
func TestWhereBetweens(t *testing.T) {
	const between = `select * from "users" where "id" between ? and ?`

	runFlattenCases(t, []flattenCase{
		{laravelCase{"flat bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereBetween("id", []any{1, 2}, core.BooleanAnd, false)
		}, between}, []any{1, 2}},
		{laravelCase{"nested bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereBetween("id", []any{[]any{1, 2, 3}}, core.BooleanAnd, false)
		}, between}, []any{1, 2}},
		{laravelCase{"two nested bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereBetween("id", []any{[]any{1}, []any{2, 3}}, core.BooleanAnd, false)
		}, between}, []any{1, 2}},
		{laravelCase{"where not between", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereBetween("id", []any{1, 2}, core.BooleanAnd, true)
		}, `select * from "users" where "id" not between ? and ?`}, []any{1, 2}},
		{laravelCase{"raw bounds are inlined", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereBetween("id", []any{Raw("1"), Raw("2")}, core.BooleanAnd, false)
		}, `select * from "users" where "id" between 1 and 2`}, nil},
		{laravelCase{"list value behaves like a collection", func(b *Builder) *Builder {
			// The corpus passes collect([1, 2]); a Go slice is the equivalent value list.
			return b.Select("*").From("users", "").WhereBetween("id", []any{1, 2}, core.BooleanAnd, false)
		}, between}, []any{1, 2}},
	})
}

// TestOrWhereBetween ports Laravel's testOrWhereBetween (corpus L1072-1103).
func TestOrWhereBetween(t *testing.T) {
	const want = `select * from "users" where "id" = ? or "id" between ? and ?`

	runFlattenCases(t, []flattenCase{
		{laravelCase{"flat bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereBetween("id", []any{3, 5})
		}, want}, []any{1, 3, 5}},
		{laravelCase{"nested bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereBetween("id", []any{[]any{3, 4, 5}})
		}, want}, []any{1, 3, 4}},
		{laravelCase{"single nested pair", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereBetween("id", []any{[]any{3, 5}})
		}, want}, []any{1, 3, 5}},
		{laravelCase{"two nested bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereBetween("id", []any{[]any{4}, []any{6, 8}})
		}, want}, []any{1, 4, 6}},
		{laravelCase{"raw bounds are inlined", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereBetween("id", []any{Raw("3"), Raw("4")})
		}, `select * from "users" where "id" = ? or "id" between 3 and 4`}, []any{1}},
	})
}

// TestOrWhereNotBetween ports Laravel's testOrWhereNotBetween (corpus L1105-1136).
func TestOrWhereNotBetween(t *testing.T) {
	const want = `select * from "users" where "id" = ? or "id" not between ? and ?`

	runFlattenCases(t, []flattenCase{
		{laravelCase{"flat bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).WhereNotBetween("id", []any{3, 5}, core.BooleanOr)
		}, want}, []any{1, 3, 5}},
		{laravelCase{"nested bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).WhereNotBetween("id", []any{[]any{3, 4, 5}}, core.BooleanOr)
		}, want}, []any{1, 3, 4}},
		{laravelCase{"single nested pair", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).WhereNotBetween("id", []any{[]any{3, 5}}, core.BooleanOr)
		}, want}, []any{1, 3, 5}},
		{laravelCase{"two nested bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).WhereNotBetween("id", []any{[]any{4}, []any{6, 8}}, core.BooleanOr)
		}, want}, []any{1, 4, 6}},
		{laravelCase{"raw bounds are inlined", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).WhereNotBetween("id", []any{Raw("3"), Raw("4")}, core.BooleanOr)
		}, `select * from "users" where "id" = ? or "id" not between 3 and 4`}, []any{1}},
	})
}

// TestBasicWhereIns ports Laravel's testBasicWhereIns (corpus L1312-1343). The nested single-value
// arrays pass the whereIn guard (their one-level flatten keeps the count) and are flattened back into
// individual bindings by getBindings.
func TestBasicWhereIns(t *testing.T) {
	const want = `select * from "users" where "id" in (?, ?, ?)`

	runFlattenCases(t, []flattenCase{
		{laravelCase{"flat list", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", []any{1, 2, 3}, core.BooleanAnd, false)
		}, want}, []any{1, 2, 3}},
		{laravelCase{"associative list keeps order", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", []any{45582, 2, 3}, core.BooleanAnd, false)
		}, want}, []any{45582, 2, 3}},
		{laravelCase{"nested single-value arrays", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", []any{[]any{45582}, []any{2}, []any{3}}, core.BooleanAnd, false)
		}, want}, []any{45582, 2, 3}},
		{laravelCase{"or where in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereIn("id", []any{1, 2, 3})
		}, `select * from "users" where "id" = ? or "id" in (?, ?, ?)`}, []any{1, 1, 2, 3}},
	})
}

// TestWhereInNestedArraysRejected ports the guard from Laravel's testBasicWhereInsException
// (corpus L1345-1357): whereIn refuses a nested list whose one-level flatten changes the value count.
// Laravel throws InvalidArgumentException; the port records errNestedArraysNotAllowed.
func TestWhereInNestedArraysRejected(t *testing.T) {
	builder := newTestBuilder().Select("*").From("users", "").WhereIn("id", []any{
		[]any{1, 1}, []any{2}, []any{3},
	}, core.BooleanAnd, false)

	_ = builder.ToSQL()

	assert.ErrorIs(t, builder.Error(), errNestedArraysNotAllowed)
}

// TestHavingBetweens ports Laravel's testHavingBetweens (corpus L2279-2290).
func TestHavingBetweens(t *testing.T) {
	const want = `select * from "users" having "id" between ? and ?`

	runFlattenCases(t, []flattenCase{
		{laravelCase{"three bounds take the first two", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").HavingBetween("id", []any{1, 2, 3}, core.BooleanAnd, false)
		}, want}, []any{1, 2}},
		{laravelCase{"two nested bounds", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").HavingBetween("id", []any{[]any{1, 2}, []any{3, 4}}, core.BooleanAnd, false)
		}, want}, []any{1, 2}},
	})
}

// TestWhereIntegerInRawFlattenNested covers the Arr::flatten half of Laravel's testWhereIntegerInRaw
// (corpus L1410-1427) with integer values, so it isolates the flatten from the (int)-cast behaviour
// that work item b15 owns. Laravel flattens first, then casts each value, then inlines them.
func TestWhereIntegerInRawFlattenNested(t *testing.T) {
	const want = `select * from "users" where "id" in (1, 2, 3)`

	runFlattenCases(t, []flattenCase{
		{laravelCase{"flat integers", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{1, 2, 3}, core.BooleanAnd, false)
		}, want}, nil},
		{laravelCase{"nested single-value arrays", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{[]any{1}, []any{2}, []any{3}}, core.BooleanAnd, false)
		}, want}, nil},
		{laravelCase{"deeply nested arrays", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{[]any{[]any{1, 2}}, []any{3}}, core.BooleanAnd, false)
		}, want}, nil},
	})
}
