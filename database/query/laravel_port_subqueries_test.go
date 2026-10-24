package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the "subqueries" area of Laravel's tests/Database/DatabaseQueryBuilderTest.php: exactly
// the cases the port plan marks PORTABLE with area subqueries. Each case rebuilds the corpus chain with
// our Builder and asserts the exact SQL string, and where the corpus also asserts bindings, those are
// asserted too. Our ToSQL renders "?" placeholders and double-quoted identifiers for every dialect, so
// the Postgres cases keep "?" rather than "$N".
//
// Corpus methods ported here:
//   testSubSelectWhereIns, testFullSubSelects, testSubqueriesBindings, testSubSelect,
//   testSubSelectResetBindings.
//
// testSubSelect's third block is the exception expectation for selectSub(['foo'], 'sub'): Laravel raises
// InvalidArgumentException, which in Go is the declared error errSubqueryTypeInvalid recorded on the
// builder and surfaced through Builder.Error(). The corpus asserts no SQL for that block, so none is
// asserted here; only the recorded error.

// TestSubSelectWhereIns ports Laravel's testSubSelectWhereIns.
func TestSubSelectWhereIns(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"where in sub", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", func(q *Builder) *Builder {
				return q.Select("id").From("users", "").Where("age", ">", 25).Limit(3)
			}, core.BooleanAnd, false)
		}, `select * from "users" where "id" in (select "id" from "users" where "age" > ? limit 3)`},
		{"where not in sub", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotIn("id", func(q *Builder) *Builder {
				return q.Select("id").From("users", "").Where("age", ">", 25).Limit(3)
			}, core.BooleanAnd)
		}, `select * from "users" where "id" not in (select "id" from "users" where "age" > ? limit 3)`},
	})

	// assertEquals([25], $builder->getBindings()) for both blocks.
	assert.Equal(t, []any{25}, newTestBuilder().Select("*").From("users", "").
		WhereIn("id", func(q *Builder) *Builder {
			return q.Select("id").From("users", "").Where("age", ">", 25).Limit(3)
		}, core.BooleanAnd, false).GetBindings())
	assert.Equal(t, []any{25}, newTestBuilder().Select("*").From("users", "").
		WhereNotIn("id", func(q *Builder) *Builder {
			return q.Select("id").From("users", "").Where("age", ">", 25).Limit(3)
		}, core.BooleanAnd).GetBindings())
}

// TestFullSubSelects ports Laravel's testFullSubSelects.
func TestFullSubSelects(t *testing.T) {
	builder := newTestBuilder().Select("*").From("users", "")
	builder.Where("email", "=", "foo").OrWhere("id", "=", func(q *Builder) *Builder {
		return q.Select(Raw("max(id)")).From("users", "").Where("email", "=", "bar")
	})

	assert.Equal(t, `select * from "users" where "email" = ? or "id" = (select max(id) from "users" where "email" = ?)`,
		builder.ToSQL())
	assert.Equal(t, []any{"foo", "bar"}, builder.GetBindings())
}

// TestSubqueriesBindings ports Laravel's testSubqueriesBindings.
func TestSubqueriesBindings(t *testing.T) {
	// The corpus builds the two union operands with getBuilder(), which is our base grammar.
	second := newTestBuilder().Select("*").From("users", "").OrderByRaw("id = ?", []any{2})
	third := newTestBuilder().Select("*").From("users", "").Where("id", 3).GroupBy("id").Having("id", "!=", 4)

	builder := newTestBuilder().Select("*").From("users", "").GroupBy("a").Having("a", "=", 1).
		Union(second, false).Union(third, false)

	// assertEquals([1, 2, 3, 4], $builder->getBindings()): having(1), then the union operands' order(2),
	// where(3) and having(4) bindings, flattened in BindingTypes order.
	assert.Equal(t, []any{1, 2, 3, 4}, builder.GetBindings())

	// The second block nests a subquery value in a where, ordering and grouping inside it.
	builder = newTestBuilder().Select("*").From("users", "").Where("email", "=", func(q *Builder) *Builder {
		return q.Select(Raw("max(id)")).From("users", "").Where("email", "=", "bar").
			OrderByRaw("email like ?", []any{"%.com"}).GroupBy("id").Having("id", "=", 4)
	}).OrWhere("id", "=", "foo").GroupBy("id").Having("id", "=", 5)

	// assertEquals(['bar', 4, '%.com', 'foo', 5], ...): the subquery's own bindings flatten as
	// where('bar') then having(4) then order('%.com'), followed by the outer where and having.
	assert.Equal(t, []any{"bar", 4, "%.com", "foo", 5}, builder.GetBindings())
}

// TestSubSelect ports Laravel's testSubSelect, whose corpus builder is getPostgresBuilder().
func TestSubSelect(t *testing.T) {
	const wantSQL = `select "foo", "bar", (select "baz" from "two" where "subkey" = ?) as "sub" from "one" where "key" = ?`

	runDialectCases(t, []dialectCase{
		{"sub select from a callback", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.From("one", "").Select("foo", "bar").Where("key", "=", "val").SelectSub(func(q *Builder) *Builder {
				return q.From("two", "").Select("baz").Where("subkey", "=", "subval")
			}, "sub")
		}, wantSQL},
		{"sub select from a builder", NewPostgresGrammar(""), func(b *Builder) *Builder {
			sub := NewBuilder(NewPostgresGrammar("")).From("two", "").Select("baz").Where("subkey", "=", "subval")

			return b.From("one", "").Select("foo", "bar").Where("key", "=", "val").SelectSub(sub, "sub")
		}, wantSQL},
	})

	// assertEquals(['subval', 'val'], $builder->getBindings()) for both forms: select bindings come first
	// in BindingTypes order, ahead of the where binding.
	assert.Equal(t, []any{"subval", "val"}, NewBuilder(NewPostgresGrammar("")).
		From("one", "").Select("foo", "bar").Where("key", "=", "val").
		SelectSub(func(q *Builder) *Builder {
			return q.From("two", "").Select("baz").Where("subkey", "=", "subval")
		}, "sub").GetBindings())
	assert.Equal(t, []any{"subval", "val"}, NewBuilder(NewPostgresGrammar("")).
		From("one", "").Select("foo", "bar").Where("key", "=", "val").
		SelectSub(NewBuilder(NewPostgresGrammar("")).From("two", "").Select("baz").Where("subkey", "=", "subval"), "sub").
		GetBindings())

	// The third block expects InvalidArgumentException::class for selectSub(['foo'], 'sub').
	assert.Error(t, NewBuilder(NewPostgresGrammar("")).From("one", "").SelectSub([]any{"foo"}, "sub").Error())
}

// TestSubSelectResetBindings ports Laravel's testSubSelectResetBindings.
func TestSubSelectResetBindings(t *testing.T) {
	builder := NewBuilder(NewPostgresGrammar("")).From("one", "").SelectSub(func(q *Builder) *Builder {
		return q.From("two", "").Select("baz").Where("subkey", "=", "subval")
	}, "sub")

	assert.Equal(t, `select (select "baz" from "two" where "subkey" = ?) as "sub" from "one"`, builder.ToSQL())
	assert.Equal(t, []any{"subval"}, builder.GetBindings())

	// select('*') clears the columns and the select bindings, dropping the subquery's placeholder.
	builder.Select("*")

	assert.Equal(t, `select * from "one"`, builder.ToSQL())
	assert.Empty(t, builder.GetBindings())
}
