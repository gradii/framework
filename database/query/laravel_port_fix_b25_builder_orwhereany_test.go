package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the corpus assertions that drive Builder::orWhereAny (Builder.php:2402):
//
//   - testOrWhereAny (tests/Database/DatabaseQueryBuilderTest.php:1635), the whole method.
//   - the "where()->orWhereAny([...keys], value)" block of testWhereWithArrayConditions
//     (tests/Database/DatabaseQueryBuilderTest.php:2875-2884); the rest of that method is already
//     ported by laravel_port_fix_b14_ordered_pair_list_form_of_addarrayofwheres_where_test.go, so
//     only the orWhereAny slice is (re)stated here.
//
// orWhereAny is whereAny with the connector flipped to "or": it queues one nested group whose inner
// clauses are joined by "or", repeated across every column. It only pushes descriptors that the
// shared Grammar compiles, so no dialect overrides the compiler and there are no dialectCase entries.
//
// The expectations are the corpus SQL verbatim over the base grammar (double quotes, "?" bindings),
// which is the grammar getBuilder() uses, and the binding tables pin the bindings the corpus also
// asserts (a builder with no bindings returns a nil slice, so an empty expectation is nil).

// orWhereAnyCase is a laravelCase plus the bindings the corpus asserts for it. It reuses laravelCase
// for the name/SQL/build triple and adds the binding expectation on top, since runLaravelCases
// compares SQL only.
type orWhereAnyCase struct {
	laravelCase
	bindings []any
}

func runOrWhereAnyCases(t *testing.T, cases []orWhereAnyCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := test.build(newTestBuilder())

			assert.Equal(t, test.want, builder.ToSQL())
			assert.Equal(t, test.bindings, builder.GetBindings())
		})
	}
}

// TestOrWhereAny ports Laravel's testOrWhereAny. The first two cases are the same statement built
// through the orWhereAny entry point and through whereAny(..., 'or'); the third is the two-argument
// "=" shorthand; the fourth passes a list of closures, each becoming its own nested group (hence the
// doubled parentheses the corpus asserts).
func TestOrWhereAny(t *testing.T) {
	runOrWhereAnyCases(t, []orWhereAnyCase{
		{
			laravelCase{"or where any like", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereAny([]any{"last_name", "email"}, "like", "%Otwell%")
			}, `select * from "users" where "first_name" like ? or ("last_name" like ? or "email" like ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			// whereAny with an explicit "or" connector is the same statement as orWhereAny.
			laravelCase{"where any with or connector", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					WhereAny([]any{"last_name", "email"}, "like", "%Otwell%", BooleanOr)
			}, `select * from "users" where "first_name" like ? or ("last_name" like ? or "email" like ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			// The two-argument shorthand: the sole extra argument is the value, and "=" is implied.
			laravelCase{"or where any equality shorthand", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereAny([]any{"last_name", "email"}, "%Otwell%")
			}, `select * from "users" where "first_name" like ? or ("last_name" = ? or "email" = ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			laravelCase{"or where any closures", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereAny([]any{
						func(q *Builder) *Builder { return q.Where("last_name", "like", "%Otwell%") },
						func(q *Builder) *Builder { return q.Where("email", "like", "%Otwell%") },
					})
			}, `select * from "users" where "first_name" like ? or (("last_name" like ?) or ("email" like ?))`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
	})
}

// TestOrWhereAnyWithArrayConditions ports the where()->orWhereAny([...keys], value) block of
// Laravel's testWhereWithArrayConditions. The corpus builds the same statement twice; both are kept,
// matching the corpus's pair of assertions.
func TestOrWhereAnyWithArrayConditions(t *testing.T) {
	build := func(b *Builder) *Builder {
		return b.Select("*").From("users", "").
			Where("xxxx", "xxxx").
			OrWhereAny([]any{"foo", "bar"}, 2)
	}

	runOrWhereAnyCases(t, []orWhereAnyCase{
		{
			laravelCase{"or where any keys value", build,
				`select * from "users" where "xxxx" = ? or ("foo" = ? or "bar" = ?)`},
			[]any{"xxxx", 2, 2},
		},
		{
			laravelCase{"or where any keys value repeated", build,
				`select * from "users" where "xxxx" = ? or ("foo" = ? or "bar" = ?)`},
			[]any{"xxxx", 2, 2},
		},
	})
}
