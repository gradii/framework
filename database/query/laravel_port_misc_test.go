package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
)

// This file ports the corpus methods the port plan classifies as PORTABLE with area "misc":
// prepareValueAndOperator, binding order and the binding merge helpers, the leading-boolean where
// forms, and the table-valued function spelling SqlServer gives a FROM table.
//
// It reuses laravelCase/runLaravelCases and dialectCase/runDialectCases from laravel_port_test.go
// and newTestBuilder from grammar_base_test.go.

// TestPrepareValueAndOperator ports Laravel's testPrepareValueAndOperator. The corpus asserts the
// returned pair, not SQL: prepareValueAndOperator('>', '20') keeps both operands, and the
// useDefault form rotates them to ('20', '=').
func TestPrepareValueAndOperator(t *testing.T) {
	value, operator, err := newTestBuilder().PrepareValueAndOperator(">", "20", false)
	assert.NoError(t, err)
	assert.Equal(t, ">", value)
	assert.Equal(t, "20", operator)

	value, operator, err = newTestBuilder().PrepareValueAndOperator(">", "20", true)
	assert.NoError(t, err)
	assert.Equal(t, "20", value)
	assert.Equal(t, "=", operator)
}

// TestPrepareValueAndOperatorExpectException ports Laravel's
// testPrepareValueAndOperatorExpectException: a nil value with the "like" operator is an illegal
// combination and raises.
func TestPrepareValueAndOperatorExpectException(t *testing.T) {
	value, operator, err := newTestBuilder().PrepareValueAndOperator(nil, "like", false)

	assert.Error(t, err)
	assert.Nil(t, value)
	assert.Nil(t, operator)
	// Corpus text: expectExceptionMessage('Illegal operator and value combination.'); the oracle
	// prints 'InvalidArgumentException Illegal operator and value combination.'. Our declared error is
	// errors.QueryIllegalOperatorAndValue, whose message was aligned with Laravel - capital I and
	// trailing full stop included (errors/list.go) - so the assertion below is the corpus string.
	assert.Equal(t, errors.QueryIllegalOperatorAndValue, err)
	assert.Equal(t, "Illegal operator and value combination.", err.Error())
}

// TestBindingOrder ports Laravel's testBindingOrder. Bindings are collected per component and
// flattened in Laravel's component order, so the join binding lands before the where, having and
// order ones even when the chain queues those components in the opposite order.
func TestBindingOrder(t *testing.T) {
	const expectedSQL = `select * from "users" inner join "othertable" on "bar" = ? where "registered" = ? group by "city" having "population" > ? order by match ("foo") against(?)`
	expectedBindings := []any{"foo", 1, 3, "bar"}

	join := func(join *Builder) *Builder { return join.Where("bar", "=", "foo") }

	forward := newTestBuilder().Select("*").From("users", "").
		Join("othertable", join, "", "", "inner", false).
		Where("registered", 1).GroupBy("city").Having("population", ">", 3).
		OrderByRaw(`match ("foo") against(?)`, []any{"bar"})

	assert.Equal(t, expectedSQL, forward.ToSQL())
	assert.Equal(t, expectedBindings, forward.GetBindings())

	// Order of statements reversed: the rendered SQL and the flattened bindings are unchanged.
	reversed := newTestBuilder().Select("*").From("users", "").
		OrderByRaw(`match ("foo") against(?)`, []any{"bar"}).
		Having("population", ">", 3).GroupBy("city").Where("registered", 1).
		Join("othertable", join, "", "", "inner", false)

	assert.Equal(t, expectedSQL, reversed.ToSQL())
	assert.Equal(t, expectedBindings, reversed.GetBindings())
}

// TestAddBindingWithArrayMergesBindings ports Laravel's testAddBindingWithArrayMergesBindings:
// adding a slice appends its elements to the component's bindings instead of nesting it.
func TestAddBindingWithArrayMergesBindings(t *testing.T) {
	builder := newTestBuilder()

	assert.NoError(t, builder.AddBinding([]any{"foo", "bar"}, "where"))
	assert.NoError(t, builder.AddBinding([]any{"baz"}, "where"))
	assert.Equal(t, []any{"foo", "bar", "baz"}, builder.GetBindings())
}

// TestAddBindingWithArrayMergesBindingsInCorrectOrder ports Laravel's
// testAddBindingWithArrayMergesBindingsInCorrectOrder: 'having' is filled first, but flattening
// reads 'where' first, so the result is still foo, bar, baz.
func TestAddBindingWithArrayMergesBindingsInCorrectOrder(t *testing.T) {
	builder := newTestBuilder()

	assert.NoError(t, builder.AddBinding([]any{"bar", "baz"}, "having"))
	assert.NoError(t, builder.AddBinding([]any{"foo"}, "where"))
	assert.Equal(t, []any{"foo", "bar", "baz"}, builder.GetBindings())
}

// TestMergeBuilders ports Laravel's testMergeBuilders: mergeBindings appends the other builder's
// every component onto this one's.
func TestMergeBuilders(t *testing.T) {
	builder := newTestBuilder()
	assert.NoError(t, builder.AddBinding([]any{"foo", "bar"}, "where"))

	other := newTestBuilder()
	assert.NoError(t, other.AddBinding([]any{"baz"}, "where"))

	builder.MergeBindings(other)
	assert.Equal(t, []any{"foo", "bar", "baz"}, builder.GetBindings())
}

// TestMergeBuildersBindingOrder ports Laravel's testMergeBuildersBindingOrder: the merge keeps each
// component contiguous, so where's foo and bar precede having's baz.
func TestMergeBuildersBindingOrder(t *testing.T) {
	builder := newTestBuilder()
	assert.NoError(t, builder.AddBinding("foo", "where"))
	assert.NoError(t, builder.AddBinding("baz", "having"))

	other := newTestBuilder()
	assert.NoError(t, other.AddBinding("bar", "where"))

	builder.MergeBindings(other)
	assert.Equal(t, []any{"foo", "bar", "baz"}, builder.GetBindings())
}

// TestUppercaseLeadingBooleansAreRemoved ports Laravel's testUppercaseLeadingBooleansAreRemoved.
// The corpus passes 'AND' as a fourth argument to where(); our Where is variadic and takes only the
// operator and value, so the extra argument is dropped here where Laravel keeps it as the clause
// boolean and compileWheres strips it. The oracle renders the same SQL for all three spellings.
func TestUppercaseLeadingBooleansAreRemoved(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"uppercase leading boolean", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("name", "=", "Taylor", "AND")
		}, `select * from "users" where "name" = ?`},
	})
}

// TestLowercaseLeadingBooleansAreRemoved ports Laravel's testLowercaseLeadingBooleansAreRemoved.
func TestLowercaseLeadingBooleansAreRemoved(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"lowercase leading boolean", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("name", "=", "Taylor", "and")
		}, `select * from "users" where "name" = ?`},
	})
}

// TestCaseInsensitiveLeadingBooleansAreRemoved ports Laravel's
// testCaseInsensitiveLeadingBooleansAreRemoved.
func TestCaseInsensitiveLeadingBooleansAreRemoved(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"mixed-case leading boolean", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("name", "=", "Taylor", "And")
		}, `select * from "users" where "name" = ?`},
	})
}

// TestTableValuedFunctionAsTableInSqlServer ports Laravel's
// testTableValuedFunctionAsTableInSqlServer: SqlServer wraps the table name up to the argument
// list, leaving the parentheses outside the brackets.
func TestTableValuedFunctionAsTableInSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"empty argument list", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users()", "")
		}, `select * from [users]()`},
		{"literal arguments", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users(1,2)", "")
		}, `select * from [users](1,2)`},
	})
}

// TestTapCallback ports Laravel's testTapCallback. tap() runs the callback against the builder and
// returns the same receiver, so the where the callback queues survives on the chain and the
// following where('email', ...) is appended after it.
func TestTapCallback(t *testing.T) {
	builder := newTestBuilder().Select("*").From("users", "")
	builder.Tap(func(q *Builder) {
		q.Where("id", "=", 1)
	}).Where("email", "=", "foo")

	assert.Equal(t, `select * from "users" where "id" = ? and "email" = ?`, builder.ToSQL())
}

// TestPipeCallback ports Laravel's testPipeCallback. pipe() returns the callback's result, falling
// back to the receiver when that result is null (a PHP void callback returns null; the Go callback
// spells both as nil). The callback's side effects land on the receiver: the corpus reads them back
// off $query->wheres (0, then 1), which here is re-expressed through the compiled SQL, since the
// builder exposes no wheres slice.
func TestPipeCallback(t *testing.T) {
	builder := newTestBuilder()

	assert.Equal(t, 5, builder.Pipe(func(q *Builder) any { return 5 }))

	// Explicit null return -> the receiver.
	assert.Same(t, builder, builder.Pipe(func(q *Builder) any { return nil }))

	// PHP's empty-body callback returns null, so pipe falls back to the receiver; the Go spelling
	// above (nil) covers the same case.

	// No where queued yet.
	assert.Equal(t, `select *`, builder.ToSQL())

	// The callback's where lands on the receiver and is returned: pipe yields the receiver.
	assert.Same(t, builder, builder.Pipe(func(q *Builder) any { return q.Where("foo", "bar") }))
	assert.Equal(t, `select * where "foo" = ?`, builder.ToSQL())
}

// TestMergeWheresCanMergeWheresAndBindings ports Laravel's
// testMergeWheresCanMergeWheresAndBindings: mergeWheres appends the incoming where clauses onto this
// builder's and appends the incoming where bindings in order, discarding the incoming binding keys
// (the corpus passes [12 => 'foo', 13 => 'bar'] and expects the values alone). Laravel holds wheres
// as a plain array of mixed values, so the corpus seeds it with the strings 'foo' and 'wheres'; here
// a where is a Where descriptor, so those two stand in as marker clauses and the assertion is on
// their order, not their rendering.
func TestMergeWheresCanMergeWheresAndBindings(t *testing.T) {
	builder := newTestBuilder()
	builder.State().Wheres = []core.Where{{Column: "foo"}}
	builder.MergeWheres([]core.Where{{Column: "wheres"}}, []any{"foo", "bar"})

	assert.Equal(t, []core.Where{{Column: "foo"}, {Column: "wheres"}}, builder.State().Wheres)
	assert.Equal(t, []any{"foo", "bar"}, builder.GetBindings())
}

// Direct sibling grammar cases live in grammars/laravel_port_test.go.
