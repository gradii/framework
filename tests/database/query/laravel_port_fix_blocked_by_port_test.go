package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// testRawHavings through the named entry point.
//
// The corpus (DatabaseQueryBuilderTest::testRawHavings, L2409-2424) builds the "or" half of the test
// with Builder::orHavingRaw(), which Laravel declares at Query/Builder.php:2669 as
// havingRaw($sql, $bindings, 'or'). The port had only the boolean form of HavingRaw, so the case was
// recorded as blocked; clauses.go now carries OrHavingRaw and these cases run through it. The boolean
// form's own port (TestRawHavings, in laravel_port_fix_leftover_and_unnamed_test.go) is left as it is:
// it asserts the same three SQL strings by the route that was available then.
//
// Expected SQL, bindings and identifier quoting were measured with the oracle on
// illuminate/database v12.55.1 across mysql, pgsql, sqlite and sqlsrv; the corpus text agrees with it.

// TestRawHavingsOrHavingRaw ports testRawHavings using the orHavingRaw entry point: a raw having with
// no bindings, then one appended with "or" after a having and after a havingBetween.
func TestRawHavingsOrHavingRaw(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"raw having only", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").HavingRaw("user_foo < user_bar", nil, core.BooleanAnd)
		}, `select * from "users" having user_foo < user_bar`},
		{"having then orHavingRaw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("baz", "=", 1).OrHavingRaw("user_foo < user_bar", nil)
		}, `select * from "users" having "baz" = ? or user_foo < user_bar`},
		{"havingBetween then orHavingRaw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				HavingBetween("last_login_date", []any{"2018-11-16", "2018-12-16"}, core.BooleanAnd, false).
				OrHavingRaw("user_foo < user_bar", nil)
		}, `select * from "users" having "last_login_date" between ? and ? or user_foo < user_bar`},
	})
}

// TestRawHavingsOrHavingRawBindings pins the binding list and its order: orHavingRaw takes the place
// of havingRaw($sql, $bindings, 'or'), so its bindings land in the having component in call order and
// a raw fragment with no bindings contributes none.
func TestRawHavingsOrHavingRawBindings(t *testing.T) {
	noBindings := newTestBuilder().Select("*").From("users", "").HavingRaw("user_foo < user_bar", nil, core.BooleanAnd)
	assert.Empty(t, noBindings.GetBindings())

	afterHaving := newTestBuilder().Select("*").From("users", "").Having("baz", "=", 1).OrHavingRaw("user_foo < user_bar", nil)
	assert.Equal(t, []any{1}, afterHaving.GetBindings())

	afterBetween := newTestBuilder().Select("*").From("users", "").
		HavingBetween("last_login_date", []any{"2018-11-16", "2018-12-16"}, core.BooleanAnd, false).
		OrHavingRaw("user_foo < user_bar", nil)
	assert.Equal(t, []any{"2018-11-16", "2018-12-16"}, afterBetween.GetBindings())

	// The bindings of the raw fragment are appended after the earlier having's, in call order.
	withBindings := newTestBuilder().Select("*").From("users", "").
		Having("baz", "=", 1).
		OrHavingRaw("a > ?", []any{2})
	assert.Equal(t, `select * from "users" having "baz" = ? or a > ?`, withBindings.ToSQL())
	assert.Equal(t, []any{1, 2}, withBindings.GetBindings())

	// orHavingRaw is havingRaw with the or boolean, so the two agree on the descriptor they push.
	orForm := newTestBuilder().Select("*").From("users", "").Having("baz", "=", 1).OrHavingRaw("user_foo < user_bar", nil)
	booleanForm := newTestBuilder().Select("*").From("users", "").Having("baz", "=", 1).
		HavingRaw("user_foo < user_bar", nil, core.BooleanOr)
	assert.Equal(t, booleanForm.ToSQL(), orForm.ToSQL())
	assert.Equal(t, booleanForm.GetBindings(), orForm.GetBindings())
}

// TestRawHavingsOrHavingRawDialects ports the same three corpus strings on every dialect. Laravel
// carries no per-dialect override for a raw having: only the identifiers around the fragment are
// wrapped, and the fragments themselves are inlined verbatim in each dialect's quoting.
func TestRawHavingsOrHavingRawDialects(t *testing.T) {
	dialects := []struct {
		name    string
		grammar Grammar
		only    string
		after   string
		between string
	}{
		{"mysql", NewMySqlGrammar(""),
			"select * from `users` having user_foo < user_bar",
			"select * from `users` having `baz` = ? or user_foo < user_bar",
			"select * from `users` having `last_login_date` between ? and ? or user_foo < user_bar"},
		{"postgres", NewPostgresGrammar(""),
			`select * from "users" having user_foo < user_bar`,
			`select * from "users" having "baz" = ? or user_foo < user_bar`,
			`select * from "users" having "last_login_date" between ? and ? or user_foo < user_bar`},
		{"sqlite", NewSqliteGrammar(""),
			`select * from "users" having user_foo < user_bar`,
			`select * from "users" having "baz" = ? or user_foo < user_bar`,
			`select * from "users" having "last_login_date" between ? and ? or user_foo < user_bar`},
		{"sqlserver", NewSqlServerGrammar(""),
			"select * from [users] having user_foo < user_bar",
			"select * from [users] having [baz] = ? or user_foo < user_bar",
			"select * from [users] having [last_login_date] between ? and ? or user_foo < user_bar"},
	}

	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			assert.Equal(t, dialect.only, NewBuilder(dialect.grammar).Select("*").From("users", "").
				HavingRaw("user_foo < user_bar", nil, core.BooleanAnd).ToSQL())
			assert.Equal(t, dialect.after, NewBuilder(dialect.grammar).Select("*").From("users", "").
				Having("baz", "=", 1).OrHavingRaw("user_foo < user_bar", nil).ToSQL())
			assert.Equal(t, dialect.between, NewBuilder(dialect.grammar).Select("*").From("users", "").
				HavingBetween("last_login_date", []any{"2018-11-16", "2018-12-16"}, core.BooleanAnd, false).
				OrHavingRaw("user_foo < user_bar", nil).ToSQL())
		})
	}
}
