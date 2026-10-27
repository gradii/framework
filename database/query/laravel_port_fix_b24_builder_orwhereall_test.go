package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the corpus assertions that drive Builder::orWhereAll (Builder.php:2365):
//
//   - testOrWhereAll (tests/Database/DatabaseQueryBuilderTest.php:1588), the whole method.
//   - the "where()->orWhereAll([...keys], value)" block of testWhereWithArrayConditions
//     (tests/Database/DatabaseQueryBuilderTest.php:2863-2871); the rest of that method is already
//     ported by laravel_port_fix_b14_ordered_pair_list_form_of_addarrayofwheres_where_test.go, so
//     only the orWhereAll slice is (re)stated here.
//
// orWhereAll is whereAll with the connector flipped to "or": it queues one nested group whose inner
// clauses are joined by "and", repeated across every column. It only pushes descriptors that the
// shared Grammar compiles, so no dialect overrides the compiler and there are no dialectCase entries.
//
// The expectations are the corpus SQL verbatim over the base grammar (double quotes, "?" bindings),
// which is the grammar getBuilder() uses, and the binding tables pin the bindings the corpus also
// asserts (a builder with no bindings returns a nil slice, so an empty expectation is nil).

// orWhereAllCase is a laravelCase plus the bindings the corpus asserts for it. It reuses laravelCase
// for the name/SQL/build triple and adds the binding expectation on top, since runLaravelCases
// compares SQL only.
type orWhereAllCase struct {
	laravelCase
	bindings []any
}

func runOrWhereAllCases(t *testing.T, cases []orWhereAllCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := test.build(newTestBuilder())

			assert.Equal(t, test.want, builder.ToSQL())
			assert.Equal(t, test.bindings, builder.GetBindings())
		})
	}
}

// TestOrWhereAll ports Laravel's testOrWhereAll. The first two cases are the same statement built
// through the orWhereAll entry point and through whereAll(..., 'or'); the third is the two-argument
// "=" shorthand; the fourth passes a list of closures, each becoming its own nested group (hence the
// doubled parentheses the corpus asserts).
func TestOrWhereAll(t *testing.T) {
	runOrWhereAllCases(t, []orWhereAllCase{
		{
			laravelCase{"or where all like", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereAll([]any{"last_name", "email"}, "like", "%Otwell%")
			}, `select * from "users" where "first_name" like ? or ("last_name" like ? and "email" like ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			// whereAll with an explicit "or" connector is the same statement as orWhereAll.
			laravelCase{"where all with or connector", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					WhereAll([]any{"last_name", "email"}, "like", "%Otwell%", core.BooleanOr)
			}, `select * from "users" where "first_name" like ? or ("last_name" like ? and "email" like ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			// The two-argument shorthand: the sole extra argument is the value, and "=" is implied.
			laravelCase{"or where all equality shorthand", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereAll([]any{"last_name", "email"}, "%Otwell%")
			}, `select * from "users" where "first_name" like ? or ("last_name" = ? and "email" = ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
		{
			laravelCase{"or where all closures", func(b *Builder) *Builder {
				return b.Select("*").From("users", "").
					Where("first_name", "like", "%Taylor%").
					OrWhereAll([]any{
						func(q *Builder) *Builder { return q.Where("last_name", "like", "%Otwell%") },
						func(q *Builder) *Builder { return q.Where("email", "like", "%Otwell%") },
					})
			}, `select * from "users" where "first_name" like ? or (("last_name" like ?) and ("email" like ?))`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"},
		},
	})
}

// TestOrWhereAllWithArrayConditions ports the where()->orWhereAll([...keys], value) block of
// Laravel's testWhereWithArrayConditions. The corpus builds the same statement twice; both are kept,
// matching the corpus's pair of assertions.
func TestOrWhereAllWithArrayConditions(t *testing.T) {
	build := func(b *Builder) *Builder {
		return b.Select("*").From("users", "").
			Where("xxxx", "xxxx").
			OrWhereAll([]any{"foo", "bar"}, 2)
	}

	runOrWhereAllCases(t, []orWhereAllCase{
		{
			laravelCase{"or where all keys value", build,
				`select * from "users" where "xxxx" = ? or ("foo" = ? and "bar" = ?)`},
			[]any{"xxxx", 2, 2},
		},
		{
			laravelCase{"or where all keys value repeated", build,
				`select * from "users" where "xxxx" = ? or ("foo" = ? and "bar" = ?)`},
			[]any{"xxxx", 2, 2},
		},
	})
}
