package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the corpus assertions that drive Builder::orWhereNone (Builder.php:2429):
//
//   - testOrWhereNone (tests/Database/DatabaseQueryBuilderTest.php:1687), the whole method.
//   - the "where()->orWhereNone([...keys], value)" block of testWhereWithArrayConditions
//     (tests/Database/DatabaseQueryBuilderTest.php:2887-2897); the rest of that method is already
//     ported by laravel_port_fix_b14_ordered_pair_list_form_of_addarrayofwheres_where_test.go, so
//     only the orWhereNone slice is (re)stated here.
//
// orWhereNone is whereNone with the connector flipped to "or": whereNone is whereAny with the group
// negated, so orWhereNone queues one nested, "not"-prefixed group whose inner clauses are joined by
// "or", repeated across every column. It only pushes descriptors that the shared Grammar compiles, so
// no dialect overrides the compiler and there are no dialectCase entries.
//
// The expectations are the corpus SQL verbatim over the base grammar (double quotes, "?" bindings),
// which is the grammar getBuilder() uses, and the binding tables pin the bindings the corpus also
// asserts (a builder with no bindings returns a nil slice, so an empty expectation is nil).

// orWhereNoneCase is a laravelCase plus the bindings the corpus asserts for it. It reuses laravelCase
// for the name/SQL/build triple and adds the binding expectation on top, since runLaravelCases
// compares SQL only.
type orWhereNoneCase struct {
	laravelCase
	bindings []any
}

func runOrWhereNoneCases(t *testing.T, cases []orWhereNoneCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := test.build(newTestBuilder())

			assert.Equal(t, test.want, builder.ToSQL())
			assert.Equal(t, test.bindings, builder.GetBindings())
		})
	}
}

// TestOrWhereNone ports Laravel's testOrWhereNone. The first two cases are the same statement built
// through the orWhereNone entry point and through whereNone(..., 'or'); the third is the two-argument
// "=" shorthand; the fourth passes a list of closures, each becoming its own nested group (hence the
// doubled parentheses the corpus asserts). The whole group is prefixed with "not".
func TestOrWhereNone(t *testing.T) {
	runOrWhereNoneCases(t, []orWhereNoneCase{
		{
			laravelCase{"or where none like", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereNone([]any{"last_name", "email"}, "like", "%Otwell%")
			}, `select * from "users" where "first_name" like ? or not ("last_name" like ? or "email" like ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			// whereNone with an explicit "or" connector is the same statement as orWhereNone.
			laravelCase{"where none with or connector", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					WhereNone([]any{"last_name", "email"}, "like", "%Otwell%", core.BooleanOr)
			}, `select * from "users" where "first_name" like ? or not ("last_name" like ? or "email" like ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			// The two-argument shorthand: the sole extra argument is the value, and "=" is implied.
			laravelCase{"or where none equality shorthand", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereNone([]any{"last_name", "email"}, "%Otwell%")
			}, `select * from "users" where "first_name" like ? or not ("last_name" = ? or "email" = ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			laravelCase{"or where none closures", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereNone([]any{
						func(q *Builder) *Builder { return q.Where("last_name", "like", "%Otwell%") },
						func(q *Builder) *Builder { return q.Where("email", "like", "%Otwell%") },
					})
			}, `select * from "users" where "first_name" like ? or not (("last_name" like ?) or ("email" like ?))`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
	})
}

// TestOrWhereNoneWithArrayConditions ports the where()->orWhereNone([...keys], value) block of
// Laravel's testWhereWithArrayConditions. The corpus builds the same statement twice; both are kept,
// matching the corpus's pair of assertions.
func TestOrWhereNoneWithArrayConditions(t *testing.T) {
	build := func(b *Builder) *Builder {
		return b.Select("*").From("users", "").
			Where("xxxx", "xxxx").
			OrWhereNone([]any{"foo", "bar"}, 2)
	}

	runOrWhereNoneCases(t, []orWhereNoneCase{
		{
			laravelCase{"or where none keys value", build,
				`select * from "users" where "xxxx" = ? or not ("foo" = ? or "bar" = ?)`},
			[]any{"xxxx", 2, 2},
		},
		{
			laravelCase{"or where none keys value repeated", build,
				`select * from "users" where "xxxx" = ? or not ("foo" = ? or "bar" = ?)`},
			[]any{"xxxx", 2, 2},
		},
	})
}
