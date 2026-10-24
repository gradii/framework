package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the "havings" area of Laravel's tests/Database/QueryBuilderTest.php
// (laravel/framework 12.x, commit 0fc645c6737626a1b1636cd234192b0d78b76569).
//
// Every case here is a PORTABLE row of .claude/laravel-oracle/port_plan.md whose area is
// "havings": testHavings, testNestedHavings, testNestedHavingBindings, testHavingNull,
// testHavingNotNull, testHavingExpression and testHavingShortcut. The plan's non-PORTABLE
// havings rows (testHavingBetweens, testRawHavings, testHavingFollowedBySelectGet) are left
// to the areas that own their missing entry points or their database dependency.
//
// The corpus builds on getBuilder(), so the base grammar is used and the SQL keeps "?" for
// every binding and double quotes for every identifier, Postgres included.

// TestHavings ports Laravel's testHavings.
func TestHavings(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"having basic", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("email", ">", 1)
		}, `select * from "users" having "email" > ?`},
		{"or having twice", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				OrHaving("email", "=", "test@example.com").
				OrHaving("email", "=", "test2@example.com")
		}, `select * from "users" having "email" = ? or "email" = ?`},
		{"having with group by", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").GroupBy("email").Having("email", ">", 1)
		}, `select * from "users" group by "email" having "email" > ?`},
		{"having an aliased column", func(b *Builder) *Builder {
			// The corpus selects "email as foo_email" and then groups on the alias; the
			// having clause refers to the alias too, which the grammar passes through.
			return b.Select("email as foo_email").From("users", "").Having("foo_email", ">", 1)
		}, `select "email" as "foo_email" from "users" having "foo_email" > ?`},
		{"having a raw value", func(b *Builder) *Builder {
			// A raw value records no binding, so the SQL inlines it: `having "total" > 3`.
			return b.Select("category", Raw(`count(*) as "total"`)).From("item", "").
				Where("department", "=", "popular").
				GroupBy("category").
				Having("total", ">", Raw("3"))
		}, `select "category", count(*) as "total" from "item" where "department" = ? group by "category" having "total" > 3`},
		{"having a bound value", func(b *Builder) *Builder {
			return b.Select("category", Raw(`count(*) as "total"`)).From("item", "").
				Where("department", "=", "popular").
				GroupBy("category").
				Having("total", ">", 3)
		}, `select "category", count(*) as "total" from "item" where "department" = ? group by "category" having "total" > ?`},
	})
}

// TestNestedHavings ports Laravel's testNestedHavings, which also pins the binding order:
// the outer having's value first, then the nested clause's values in the order they were added.
func TestNestedHavings(t *testing.T) {
	builder := newTestBuilder().
		Select("*").
		From("users", "").
		Having("email", "=", "foo").
		OrHaving(func(q *Builder) *Builder {
			return q.Having("name", "=", "bar").Having("age", "=", 25)
		})

	assert.Equal(t, `select * from "users" having "email" = ? or ("name" = ? and "age" = ?)`, builder.ToSQL())
	assert.Equal(t, []any{"foo", "bar", 25}, builder.GetBindings())
}

// TestNestedHavingBindings ports Laravel's testNestedHavingBindings. The nested builder's
// selectRaw binding ("ignore") must be dropped: addNestedHavingQuery merges only the child's
// "having" bindings, exactly as Laravel does.
func TestNestedHavingBindings(t *testing.T) {
	builder := newTestBuilder().
		Having("email", "=", "foo").
		Having(func(q *Builder) *Builder {
			q.SelectRaw("?", []any{"ignore"}).Having("name", "=", "bar")

			return q
		})

	assert.Equal(t, []any{"foo", "bar"}, builder.GetBindings())
}

// TestHavingNull ports Laravel's testHavingNull.
//
// The corpus spells the "or" forms as orHavingNull(col); that method has no port here, and
// HavingNull(col, BooleanOr, false) is the same descriptor Laravel's orHavingNull() queues.
// The corpus repeats its final assertion verbatim (same chain twice), so it appears once.
func TestHavingNull(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"having null", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").HavingNull("email", core.BooleanAnd, false)
		}, `select * from "users" having "email" is null`},
		{"having null twice", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				HavingNull("email", core.BooleanAnd, false).
				HavingNull("phone", core.BooleanAnd, false)
		}, `select * from "users" having "email" is null and "phone" is null`},
		{"or having null twice", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				HavingNull("email", core.BooleanOr, false).
				HavingNull("phone", core.BooleanOr, false)
		}, `select * from "users" having "email" is null or "phone" is null`},
		{"having null with group by", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").GroupBy("email").HavingNull("email", core.BooleanAnd, false)
		}, `select * from "users" group by "email" having "email" is null`},
		{"having null on an alias", func(b *Builder) *Builder {
			return b.Select("email as foo_email").From("users", "").HavingNull("foo_email", core.BooleanAnd, false)
		}, `select "email" as "foo_email" from "users" having "foo_email" is null`},
		{"having null on a raw aggregate", func(b *Builder) *Builder {
			return b.Select("category", Raw(`count(*) as "total"`)).From("item", "").
				Where("department", "=", "popular").
				GroupBy("category").
				HavingNull("total", core.BooleanAnd, false)
		}, `select "category", count(*) as "total" from "item" where "department" = ? group by "category" having "total" is null`},
	})
}

// TestHavingNotNull ports Laravel's testHavingNotNull, the "not" mirror of the previous test.
// The corpus again spells the "or" forms as orHavingNotNull(col); HavingNull with not=true and
// BooleanOr is the same descriptor, and the corpus repeats its final assertion verbatim.
func TestHavingNotNull(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"having not null", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").HavingNotNull("email", core.BooleanAnd)
		}, `select * from "users" having "email" is not null`},
		{"having not null twice", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				HavingNotNull("email", core.BooleanAnd).
				HavingNotNull("phone", core.BooleanAnd)
		}, `select * from "users" having "email" is not null and "phone" is not null`},
		{"or having not null twice", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				HavingNotNull("email", core.BooleanOr).
				HavingNotNull("phone", core.BooleanOr)
		}, `select * from "users" having "email" is not null or "phone" is not null`},
		{"having not null with group by", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").GroupBy("email").HavingNotNull("email", core.BooleanAnd)
		}, `select * from "users" group by "email" having "email" is not null`},
		{"having not null on an alias", func(b *Builder) *Builder {
			return b.Select("email as foo_email").From("users", "").HavingNotNull("foo_email", core.BooleanAnd)
		}, `select "email" as "foo_email" from "users" having "foo_email" is not null`},
		{"having not null on a raw aggregate", func(b *Builder) *Builder {
			return b.Select("category", Raw(`count(*) as "total"`)).From("item", "").
				Where("department", "=", "popular").
				GroupBy("category").
				HavingNotNull("total", core.BooleanAnd)
		}, `select "category", count(*) as "total" from "item" where "department" = ? group by "category" having "total" is not null`},
	})
}

// TestHavingExpressionHavings ports Laravel's testHavingExpression, where the corpus passes a
// ConditionExpression anonymous class and expects the raw fragment plus no binding.
//
// The name carries the area suffix because TestHavingExpression in laravel_port_test.go is
// already taken; that test pins a different chain (a group by with count(*) > 1), so the
// corpus shape is asserted here on its own. Our Having(Expression) queues exactly the
// HavingExpression descriptor Laravel's ConditionExpression branch queues.
func TestHavingExpressionHavings(t *testing.T) {
	builder := newTestBuilder().Select("*").From("users", "").Having(Raw("1 = 1"))

	assert.Equal(t, `select * from "users" having 1 = 1`, builder.ToSQL())
	// The corpus asserts an empty binding list; GetBindings returns a nil slice when no
	// binding was ever recorded, which assert.Empty treats as the same empty list.
	assert.Empty(t, builder.GetBindings())
}

// TestHavingShortcut ports Laravel's testHavingShortcut, where a single argument is the value
// and the operator defaults to "=".
func TestHavingShortcut(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"having with a shortcut operator", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("email", 1).OrHaving("email", 2)
		}, `select * from "users" having "email" = ? or "email" = ?`},
	})
}
