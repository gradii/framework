package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the "unions" area of Laravel's tests/Database/DatabaseQueryBuilderTest.php: exactly the
// cases the port plan marks PORTABLE with area unions. Each case rebuilds the corpus chain with our
// Builder and asserts the exact SQL string; where the corpus also asserts bindings, those are asserted
// too. Our ToSQL renders "?" placeholders and double-quoted identifiers for every dialect, so the MySQL,
// SQLite and SQL Server expectations keep their own identifier quoting verbatim.
//
// Corpus methods ported here:
//   testUnions, testUnionAlls, testMultipleUnions, testMultipleUnionAlls, testUnionOrderBys,
//   testUnionLimitsAndOffsets, testUnionWithJoin, testMySqlUnionOrderBys, testMySqlUnionLimitsAndOffsets.
//
// Not reproduced, because they are PHP-only and have no query-package counterpart (reported, not
// approximated):
//   - The EloquentBuilder variants appended to testUnions and testUnionAlls: they only swap the union
//     operand for an EloquentBuilder, which the corpus uses to prove union() accepts it; the SQL asserted
//     is identical to the base case already covered.
//
// orderBy('a') is written OrderBy("a", "asc"): the corpus leaves the direction unset and Laravel defaults
// it to asc, whereas our OrderBy takes the direction explicitly.

// TestUnions ports Laravel's testUnions.
func TestUnions(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		other := newTestBuilder().Select("*").From("users", "").Where("id", 2)

		b := newTestBuilder().Select("*").From("users", "").Where("id", 1)
		b.Union(other, false)

		assert.Equal(t, `(select * from "users" where "id" = ?) union (select * from "users" where "id" = ?)`, b.ToSQL())
		assert.Equal(t, []any{1, 2}, b.GetBindings())
	})

	t.Run("mysql", func(t *testing.T) {
		other := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("id", 2)

		b := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("id", 1)
		b.Union(other, false)

		assert.Equal(t, "(select * from `users` where `id` = ?) union (select * from `users` where `id` = ?)", b.ToSQL())
		assert.Equal(t, []any{1, 2}, b.GetBindings())
	})

	t.Run("mysql order by and limit", func(t *testing.T) {
		union := NewBuilder(NewMySqlGrammar("")).Select("a").From("t2", "").Where("a", 11).Where("b", 2)

		b := NewBuilder(NewMySqlGrammar("")).Select("a").From("t1", "").Where("a", 10).Where("b", 1)
		b.Union(union, false).OrderBy("a", "asc").Limit(10)

		assert.Equal(t, "(select `a` from `t1` where `a` = ? and `b` = ?) union (select `a` from `t2` where `a` = ? and `b` = ?) order by `a` asc limit 10", b.ToSQL())
		assert.Equal(t, []any{10, 1, 11, 2}, b.GetBindings())
	})

	t.Run("postgres", func(t *testing.T) {
		other := NewBuilder(NewPostgresGrammar("")).Select("name").From("users", "").Where("id", 2)

		b := NewBuilder(NewPostgresGrammar("")).Select("name").From("users", "").Where("id", 1)
		b.Union(other, false)

		assert.Equal(t, `(select "name" from "users" where "id" = ?) union (select "name" from "users" where "id" = ?)`, b.ToSQL())
		assert.Equal(t, []any{1, 2}, b.GetBindings())
	})

	t.Run("sqlite", func(t *testing.T) {
		other := NewBuilder(NewSqliteGrammar("")).Select("name").From("users", "").Where("id", 2)

		b := NewBuilder(NewSqliteGrammar("")).Select("name").From("users", "").Where("id", 1)
		b.Union(other, false)

		assert.Equal(t, `select * from (select "name" from "users" where "id" = ?) union select * from (select "name" from "users" where "id" = ?)`, b.ToSQL())
		assert.Equal(t, []any{1, 2}, b.GetBindings())
	})

	t.Run("sqlserver", func(t *testing.T) {
		other := NewBuilder(NewSqlServerGrammar("")).Select("name").From("users", "").Where("id", 2)

		b := NewBuilder(NewSqlServerGrammar("")).Select("name").From("users", "").Where("id", 1)
		b.Union(other, false)

		assert.Equal(t, "select * from (select [name] from [users] where [id] = ?) as [temp_table] union select * from (select [name] from [users] where [id] = ?) as [temp_table]", b.ToSQL())
		assert.Equal(t, []any{1, 2}, b.GetBindings())
	})
}

// TestUnionAlls ports Laravel's testUnionAlls.
func TestUnionAlls(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		other := newTestBuilder().Select("*").From("users", "").Where("id", 2)

		b := newTestBuilder().Select("*").From("users", "").Where("id", 1)
		b.UnionAll(other)

		assert.Equal(t, `(select * from "users" where "id" = ?) union all (select * from "users" where "id" = ?)`, b.ToSQL())
		assert.Equal(t, []any{1, 2}, b.GetBindings())
	})

	t.Run("postgres", func(t *testing.T) {
		// The corpus passes a base-grammar builder as the union of a Postgres builder; the subquery is
		// compiled in place, so the base grammar's quoting is what appears in the union member.
		other := newTestBuilder().Select("*").From("users", "").Where("id", 2)

		b := NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").Where("id", 1)
		b.UnionAll(other)

		assert.Equal(t, `(select * from "users" where "id" = ?) union all (select * from "users" where "id" = ?)`, b.ToSQL())
		assert.Equal(t, []any{1, 2}, b.GetBindings())
	})
}

// TestMultipleUnions ports Laravel's testMultipleUnions.
func TestMultipleUnions(t *testing.T) {
	b := newTestBuilder().Select("*").From("users", "").Where("id", 1)
	b.Union(newTestBuilder().Select("*").From("users", "").Where("id", 2), false)
	b.Union(newTestBuilder().Select("*").From("users", "").Where("id", 3), false)

	assert.Equal(t, `(select * from "users" where "id" = ?) union (select * from "users" where "id" = ?) union (select * from "users" where "id" = ?)`, b.ToSQL())
	assert.Equal(t, []any{1, 2, 3}, b.GetBindings())
}

// TestMultipleUnionAlls ports Laravel's testMultipleUnionAlls.
func TestMultipleUnionAlls(t *testing.T) {
	b := newTestBuilder().Select("*").From("users", "").Where("id", 1)
	b.UnionAll(newTestBuilder().Select("*").From("users", "").Where("id", 2))
	b.UnionAll(newTestBuilder().Select("*").From("users", "").Where("id", 3))

	assert.Equal(t, `(select * from "users" where "id" = ?) union all (select * from "users" where "id" = ?) union all (select * from "users" where "id" = ?)`, b.ToSQL())
	assert.Equal(t, []any{1, 2, 3}, b.GetBindings())
}

// TestUnionOrderBys ports Laravel's testUnionOrderBys.
func TestUnionOrderBys(t *testing.T) {
	b := newTestBuilder().Select("*").From("users", "").Where("id", 1)
	b.Union(newTestBuilder().Select("*").From("users", "").Where("id", 2), false)
	b.OrderBy("id", "desc")

	assert.Equal(t, `(select * from "users" where "id" = ?) union (select * from "users" where "id" = ?) order by "id" desc`, b.ToSQL())
	assert.Equal(t, []any{1, 2}, b.GetBindings())
}

// TestUnionLimitsAndOffsets ports Laravel's testUnionLimitsAndOffsets.
func TestUnionLimitsAndOffsets(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		b := newTestBuilder().Select("*").From("users", "")
		b.Union(newTestBuilder().Select("*").From("dogs", ""), false)
		b.Offset(5).Limit(10)

		assert.Equal(t, `(select * from "users") union (select * from "dogs") limit 10 offset 5`, b.ToSQL())
	})

	t.Run("postgres", func(t *testing.T) {
		b := NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "")
		b.Union(newTestBuilder().Select("*").From("dogs", ""), false)
		b.Offset(5).Limit(10)

		assert.Equal(t, `(select * from "users") union (select * from "dogs") limit 10 offset 5`, b.ToSQL())
	})

	t.Run("postgres with per-select limits", func(t *testing.T) {
		b := NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").Limit(11)
		b.Union(newTestBuilder().Select("*").From("dogs", "").Limit(22), false)
		b.Offset(5).Limit(10)

		assert.Equal(t, `(select * from "users" limit 11) union (select * from "dogs" limit 22) limit 10 offset 5`, b.ToSQL())
	})
}

// TestUnionWithJoin ports Laravel's testUnionWithJoin. The corpus join callback is a JoinClause; its
// on(col, op, col) is our WhereColumn with BooleanAnd, matching how the joins area expresses on().
func TestUnionWithJoin(t *testing.T) {
	b := newTestBuilder().Select("*").From("users", "")
	b.Union(newTestBuilder().Select("*").From("dogs", "").Join("breeds", func(j *Builder) *Builder {
		return j.WhereColumn("dogs.breed_id", "=", "breeds.id", BooleanAnd).Where("breeds.is_native", 1)
	}, "", "", "inner", false), false)

	assert.Equal(t, `(select * from "users") union (select * from "dogs" inner join "breeds" on "dogs"."breed_id" = "breeds"."id" and "breeds"."is_native" = ?)`, b.ToSQL())
	assert.Equal(t, []any{1}, b.GetBindings())
}

// TestMySqlUnionOrderBys ports Laravel's testMySqlUnionOrderBys.
func TestMySqlUnionOrderBys(t *testing.T) {
	b := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("id", 1)
	b.Union(NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("id", 2), false)
	b.OrderBy("id", "desc")

	assert.Equal(t, "(select * from `users` where `id` = ?) union (select * from `users` where `id` = ?) order by `id` desc", b.ToSQL())
	assert.Equal(t, []any{1, 2}, b.GetBindings())
}

// TestMySqlUnionLimitsAndOffsets ports Laravel's testMySqlUnionLimitsAndOffsets.
func TestMySqlUnionLimitsAndOffsets(t *testing.T) {
	b := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "")
	b.Union(NewBuilder(NewMySqlGrammar("")).Select("*").From("dogs", ""), false)
	b.Offset(5).Limit(10)

	assert.Equal(t, "(select * from `users`) union (select * from `dogs`) limit 10 offset 5", b.ToSQL())
}
