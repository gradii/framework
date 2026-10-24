package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the array-first-argument cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php that drive Builder::addArrayOfWheres's ordered-pair
// list form: testArrayWhereColumn (L1483), testOrWheresHaveConsistentResults (L2590),
// testWhereWithArrayConditions (L2621) and testWhereNotWithArrayConditions (L2959).
//
// Laravel's addArrayOfWheres (Builder.php:928) walks a PHP array in insertion order: a numeric key
// whose value is an array is splatted into the target method as ($column, $operator, $value,
// boolean: $boolean), and any other entry is a key/value equality pair. where() (is_array($column))
// and whereColumn() (is_array($first)) both route an array first argument here, and whereNot() /
// orWhereNot() wrap it in a negation (whereNested(..., $boolean.' not') re-entering where()).
//
// Go has no ordered map, so the corpus's numeric form is expressed as a []any of [column, operator,
// value] pairs -- exactly the ordered list this item adds -- while the corpus's associative form
// (['foo' => 1, 'bar' => 2]) is ported as the ordered-pair list carrying the same entries in the
// corpus's order, because a map[string]any cannot reproduce PHP's insertion order (our map form is
// walked in sorted-key order instead, which is a documented deviation).
//
// The expectations are the corpus SQL verbatim over the base grammar (double quotes, "?" bindings),
// which is the grammar getBuilder() uses, and the binding tables pin the bindings the corpus also
// asserts (a builder with no bindings returns a nil slice, so an empty expectation is nil).
//
// No dialect overrides the compiler for this behaviour: addArrayOfWheres only pushes descriptors that
// the shared Grammar compiles, so there are no dialectCase entries here.

// arrayWhereCase is a laravelCase plus the bindings the corpus asserts for it. runArrayWhereCases
// pins the SQL through runLaravelCases and then the bindings.
type arrayWhereCase struct {
	laravelCase
	bindings []any
}

func runArrayWhereCases(t *testing.T, cases []arrayWhereCase) {
	t.Helper()

	sqlCases := make([]laravelCase, len(cases))
	for i, c := range cases {
		sqlCases[i] = c.laravelCase
	}

	runLaravelCases(t, sqlCases)

	for _, c := range cases {
		t.Run(c.name+": bindings", func(t *testing.T) {
			assert.Equal(t, c.bindings, c.build(newTestBuilder()).GetBindings())
		})
	}
}

// TestArrayWhereColumn ports Laravel's testArrayWhereColumn. whereColumn() reads an array first
// argument, and the two-entry pair is the "=" shorthand while the three-entry pair carries its
// operator.
func TestArrayWhereColumn(t *testing.T) {
	runArrayWhereCases(t, []arrayWhereCase{
		{
			laravelCase{"where column pairs", func(b *Builder) *Builder {
				conditions := []any{
					[]any{"first_name", "last_name"},
					[]any{"updated_at", ">", "created_at"},
				}

				return b.Select("*").From("users", "").WhereColumn(conditions, nil, nil, core.BooleanAnd)
			}, `select * from "users" where ("first_name" = "last_name" and "updated_at" > "created_at")`},
			nil,
		},
	})
}

// TestOrWheresHaveConsistentResults ports Laravel's testOrWheresHaveConsistentResults. The corpus
// builds the same SQL twice per group, once from the associative form and once from the numeric form;
// both are ported here as the ordered-pair list, in the corpus's entry order, so the two builders in
// each group still stand as the pair of "consistent results".
func TestOrWheresHaveConsistentResults(t *testing.T) {
	orWhere := func(b *Builder) *Builder {
		return b.Select("*").From("users", "").Where("xxxx", "xxxx").
			OrWhere([]any{[]any{"foo", 1}, []any{"bar", 2}})
	}
	orWhereColumn := func(b *Builder) *Builder {
		return b.Select("*").From("users", "").Where("xxxx", "xxxx").
			OrWhereColumn([]any{[]any{"foo", "_foo"}, []any{"bar", "_bar"}}, nil, nil)
	}

	runArrayWhereCases(t, []arrayWhereCase{
		{
			laravelCase{"or where numeric", orWhere,
				`select * from "users" where "xxxx" = ? or ("foo" = ? or "bar" = ?)`},
			[]any{"xxxx", 1, 2},
		},
		{
			laravelCase{"or where numeric repeated", orWhere,
				`select * from "users" where "xxxx" = ? or ("foo" = ? or "bar" = ?)`},
			[]any{"xxxx", 1, 2},
		},
		{
			laravelCase{"or where column numeric", orWhereColumn,
				`select * from "users" where "xxxx" = ? or ("foo" = "_foo" or "bar" = "_bar")`},
			[]any{"xxxx"},
		},
		{
			laravelCase{"or where column numeric repeated", orWhereColumn,
				`select * from "users" where "xxxx" = ? or ("foo" = "_foo" or "bar" = "_bar")`},
			[]any{"xxxx"},
		},
	})
}

// TestWhereWithArrayConditions ports Laravel's testWhereWithArrayConditions, which mixes
// where(key, value), where(key, <, value), whereNot(key, value), whereNot(key, <, value) and
// whereColumn(col1, col2) array clauses, each with the default and the "or" boolean. An "or" first
// clause is our OrWhere / OrWhereNot entry point, and whereNot(list, boolean: 'or') is our
// OrWhereNot(list), which nests under "or not" with the inner clauses joined by "or".
func TestWhereWithArrayConditions(t *testing.T) {
	pairs := []any{[]any{"foo", 1}, []any{"bar", 2}}
	comparisons := []any{[]any{"foo", 1}, []any{"bar", "<", 2}}
	columns := []any{[]any{"foo", "_foo"}, []any{"bar", "_bar"}}

	runArrayWhereCases(t, []arrayWhereCase{
		// where(key, value)
		{
			laravelCase{"where pairs and", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").Where(pairs)
			}, `select * from "users" where ("foo" = ? and "bar" = ?)`},
			[]any{1, 2},
		},
		{
			laravelCase{"where pairs or", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").OrWhere(pairs)
			}, `select * from "users" where ("foo" = ? or "bar" = ?)`},
			[]any{1, 2},
		},
		{
			laravelCase{"where pairs and explicit", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").Where(pairs)
			}, `select * from "users" where ("foo" = ? and "bar" = ?)`},
			[]any{1, 2},
		},

		// where(key, <, value)
		{
			laravelCase{"where comparison and", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").Where(comparisons)
			}, `select * from "users" where ("foo" = ? and "bar" < ?)`},
			[]any{1, 2},
		},
		{
			laravelCase{"where comparison or", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").OrWhere(comparisons)
			}, `select * from "users" where ("foo" = ? or "bar" < ?)`},
			[]any{1, 2},
		},
		{
			laravelCase{"where comparison and explicit", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").Where(comparisons)
			}, `select * from "users" where ("foo" = ? and "bar" < ?)`},
			[]any{1, 2},
		},

		// whereNot(key, value)
		{
			laravelCase{"where not pairs", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereNot(pairs)
			}, `select * from "users" where not (("foo" = ? and "bar" = ?))`},
			[]any{1, 2},
		},
		{
			laravelCase{"where not pairs or", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").OrWhereNot(pairs)
			}, `select * from "users" where not (("foo" = ? or "bar" = ?))`},
			[]any{1, 2},
		},
		{
			laravelCase{"where not pairs and explicit", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereNot(pairs)
			}, `select * from "users" where not (("foo" = ? and "bar" = ?))`},
			[]any{1, 2},
		},

		// whereNot(key, <, value)
		{
			laravelCase{"where not comparison", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereNot(comparisons)
			}, `select * from "users" where not (("foo" = ? and "bar" < ?))`},
			[]any{1, 2},
		},
		{
			laravelCase{"where not comparison or", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").OrWhereNot(comparisons)
			}, `select * from "users" where not (("foo" = ? or "bar" < ?))`},
			[]any{1, 2},
		},
		{
			laravelCase{"where not comparison and explicit", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereNot(comparisons)
			}, `select * from "users" where not (("foo" = ? and "bar" < ?))`},
			[]any{1, 2},
		},

		// whereColumn(col1, col2)
		{
			laravelCase{"where column comparison and", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereColumn(columns, nil, nil, core.BooleanAnd)
			}, `select * from "users" where ("foo" = "_foo" and "bar" = "_bar")`},
			nil,
		},
		{
			laravelCase{"where column comparison or", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereColumn(columns, nil, nil, core.BooleanOr)
			}, `select * from "users" where ("foo" = "_foo" or "bar" = "_bar")`},
			nil,
		},
		{
			laravelCase{"where column comparison and explicit", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereColumn(columns, nil, nil, core.BooleanAnd)
			}, `select * from "users" where ("foo" = "_foo" and "bar" = "_bar")`},
			nil,
		},
	})
}

// TestWhereNotWithArrayConditions ports Laravel's testWhereNotWithArrayConditions. whereNot() with an
// array first argument wraps the whole nested group in "not", so the rendered clause carries the
// doubled parentheses the corpus asserts.
func TestWhereNotWithArrayConditions(t *testing.T) {
	pairs := []any{[]any{"foo", 1}, []any{"bar", 2}}

	runArrayWhereCases(t, []arrayWhereCase{
		{
			laravelCase{"where not pairs", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereNot(pairs)
			}, `select * from "users" where not (("foo" = ? and "bar" = ?))`},
			[]any{1, 2},
		},
		{
			// The corpus's map form is ported as the ordered-pair list: a Go map cannot preserve
			// its insertion order.
			laravelCase{"where not pairs associative", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereNot([]any{[]any{"foo", 1}, []any{"bar", 2}})
			}, `select * from "users" where not (("foo" = ? and "bar" = ?))`},
			[]any{1, 2},
		},
		{
			laravelCase{"where not comparison", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").WhereNot([]any{[]any{"foo", 1}, []any{"bar", "<", 2}})
			}, `select * from "users" where not (("foo" = ? and "bar" < ?))`},
			[]any{1, 2},
		},
	})
}
