package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the corpus methods the port plan classifies as PORTABLE with area "misc-gap"
// (see .claude/laravel-oracle/port_plan.md): testProvidingNullWithOperatorsBuildsCorrectly and
// testCloneWithoutBindings. The corpus is tests/Database/DatabaseQueryBuilderTest.php from
// laravel/framework 12.x (commit 0fc645c6737626a1b1636cd234192b0d78b76569, 12.43.1), corpus lines
// 5160-5183 and 7070-7083. Every chain in both methods starts from getBuilder(), so the base grammar
// applies: "?" placeholders and double-quoted identifiers.

// TestProvidingNullWithOperatorsBuildsCorrectly ports Laravel's
// testProvidingNullWithOperatorsBuildsCorrectly (corpus L5160-5183). Each of the five chains is the
// same statement with a different way of spelling the null comparison: the two-argument shorthand,
// '=', '!=', '<>' and '<=>'. A null operand collapses the basic where into an is-null or is-not-null
// check, so the operator never reaches the SQL; '=' and '<=>' are the null-safe spellings, '!=' and
// '<>' the negating ones.
func TestProvidingNullWithOperatorsBuildsCorrectly(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"where shorthand null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Where("foo", nil)
		}, `select * from "users" where "foo" is null`},
		{"where equals null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Where("foo", "=", nil)
		}, `select * from "users" where "foo" is null`},
		{"where not equals null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Where("foo", "!=", nil)
		}, `select * from "users" where "foo" is not null`},
		{"where diamond null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Where("foo", "<>", nil)
		}, `select * from "users" where "foo" is not null`},
		{"where spaceship null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Where("foo", "<=>", nil)
		}, `select * from "users" where "foo" is null`},
	})
}

// TestCloneWithoutBindings ports Laravel's testCloneWithoutBindings (corpus L7070-7083): the builder
// is cloned with its wheres removed and then with the where bindings emptied, and the corpus asserts
// both halves -- the original keeps its clause and its binding, the clone keeps only the order clause
// and binds nothing. The two assertions on getBindings() are part of the case, so they are asserted
// here too once the SQL assertions have been checked.
func TestCloneWithoutBindings(t *testing.T) {
	builder := newTestBuilder().From("users", "").Select("*").Where("email", "foo").OrderBy("email", "asc")
	clone := builder.CloneWithout("wheres").CloneWithoutBindings("where")

	// The original is untouched: it still carries the where clause and its binding.
	assert.Equal(t, `select * from "users" where "email" = ? order by "email" asc`, builder.ToSQL())
	assert.Equal(t, []any{"foo"}, builder.GetBindings())

	// The clone drops the where clause with its bindings and keeps the order clause.
	assert.Equal(t, `select * from "users" order by "email" asc`, clone.ToSQL())
	assert.Empty(t, clone.GetBindings())
}
