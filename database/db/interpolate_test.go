package db

import (
	"testing"

	"github.com/stretchr/testify/assert"

	querybuilder "github.com/goravel/framework/database/query"
)

// TestInterpolateNumberedPlaceholders pins the fix for the db facade's query log: the postgres form
// the facade executes and traces is "$1", "$2" …, which interpolate left untouched, so GetQueryLog
// recorded "values ($1)" instead of the literal the statement ran with.
func TestInterpolateNumberedPlaceholders(t *testing.T) {
	assert.Equal(t,
		`insert into "products" ("name") values ('query_log_product')`,
		Explain(`insert into "products" ("name") values ($1)`, "query_log_product"))

	assert.Equal(t,
		`select * from "products" where "name" = 'query_log_product'`,
		Explain(`select * from "products" where "name" = $1`, "query_log_product"))

	// The placeholders are rendered by their own number, not by order of appearance.
	assert.Equal(t,
		`insert into "users" ("name", "age") values ('John', 25)`,
		Explain(`insert into "users" ("name", "age") values ($1, $2)`, "John", 25))
}

// TestInterpolateNumberedFormLeavesQuestionMarkOperator pins that once the numbered form is present a
// surviving "?" is a literal escape (the postgres "?" operator), not a binding.
func TestInterpolateNumberedFormLeavesQuestionMarkOperator(t *testing.T) {
	assert.Equal(t,
		`select * from "t" where "data" ? 'key' and "id" = 42`,
		Explain(`select * from "t" where "data" ? 'key' and "id" = $1`, 42))
}

// TestInterpolateNumberedFormLeavesUnusableDollarIntact covers a "$" that is not a placeholder: a
// number out of range or a bare "$" is written through rather than consuming a binding.
func TestInterpolateNumberedFormLeavesUnusableDollarIntact(t *testing.T) {
	assert.Equal(t, "select * from t where a = $9", Explain("select * from t where a = $9", 1))
	assert.Equal(t, "select * from t where a = $", Explain("select * from t where a = $", 1))
}

// TestInterpolateQuestionMarkFormUnchanged pins that the "?" form the ORM and every non-postgres
// dialect still use is rendered exactly as before.
func TestInterpolateQuestionMarkFormUnchanged(t *testing.T) {
	// With no bindings there is nothing to inline, so a "?" statement is returned untouched.
	assert.Equal(t, "select * from `users` where `name` = ?", Explain("select * from `users` where `name` = ?"))
	assert.Equal(t,
		"select * from `users` where `name` = 'John' and `id` = 1",
		Explain("select * from `users` where `name` = ? and `id` = ?", "John", 1))
}

// TestInterpolateQuestionFormIgnoresQuotedDollar pins that a "$<n>" inside a quoted literal, in a
// "?"-form statement, is not mistaken for the postgres numbered form: the statement still
// interpolates its "?" bindings and the literal is left exactly as it stands.
func TestInterpolateQuestionFormIgnoresQuotedDollar(t *testing.T) {
	assert.Equal(t,
		`select * from t where note = '$5' and id = 7`,
		Explain(`select * from t where note = '$5' and id = ?`, 7))

	// A double-quoted region is treated the same way.
	assert.Equal(t,
		`select * from t where "a$2" = 'x' and id = 7`,
		Explain(`select * from t where "a$2" = 'x' and id = ?`, 7))
}

// TestInterpolateNumberedFormIgnoresQuotedDollar pins that the numbered form leaves a "$<n>" inside a
// quoted literal intact rather than overwriting it and doubling its quotes.
func TestInterpolateNumberedFormIgnoresQuotedDollar(t *testing.T) {
	assert.Equal(t,
		`insert into t (a, b, c) values ('x', 'y', '$3')`,
		Explain(`insert into t (a, b, c) values ($1, $2, '$3')`, "x", "y", "z"))
}

// TestInterpolateOrdinaryStatementsUnchanged guards that quote tracking does not alter the rendering
// of ordinary statements: the interpolated form is display-only and must match the literals the
// compiled statement runs with.
func TestInterpolateOrdinaryStatementsUnchanged(t *testing.T) {
	assert.Equal(t,
		`select * from "products" where "name" = 'John' and "id" = 1`,
		Explain(`select * from "products" where "name" = ? and "id" = ?`, "John", 1))

	assert.Equal(t,
		`insert into "products" ("name") values ('John')`,
		Explain(`insert into "products" ("name") values ($1)`, "John"))
}

// TestInterpolateRendersExprAliasVerbatim pins that db.Expr is the same type as the builder's
// Expression, so an expression argument interpolates verbatim. While Expr was a distinct struct
// embedding Expression it failed renderLiteral's Expression case and reached fmt.Sprint, which
// printed the struct as `{{"data"::jsonb}}` and produced a malformed statement.
func TestInterpolateRendersExprAliasVerbatim(t *testing.T) {
	assert.Equal(t,
		`jsonb_set("data"::jsonb,'{"string"}','"updated_first"')`,
		Explain(`jsonb_set(?,?,?)`, Expr{Value: `"data"::jsonb`}, `{"string"}`, `"updated_first"`))
}

// TestExprIsBuilderExpression pins the alias contract directly: a db.Expr is recognised as an
// expression by the builder's type switches, not merely re-rendered, which a distinct embedding
// struct could not be.
func TestExprIsBuilderExpression(t *testing.T) {
	assert.True(t, querybuilder.IsExpression(Expr{Value: "1"}))
	assert.True(t, querybuilder.IsExpression(&Expr{Value: "1"}))
}

// TestRawInterpolatesOnlyOutsideQuotedLiterals pins what database/db.Raw does TODAY with an argument
// whose "?" sits inside a quoted literal.
//
// Raw is an EXECUTED path, not display-only: it bakes interpolate(expr, args) into the fragment it
// returns (expression.go:30-36), and that fragment is compiled into the statement the connection
// runs. The quote tracking the display path needs therefore also governs Raw, and the contract that
// follows is easy to state but easy to forget, so it is pinned here rather than left to be
// discovered when a caller first passes an argument for a quoted "?":
//
//   - a "?" inside a single- or double-quoted literal is literal text, so an argument supplied for
//     it is NOT consumed and does not appear anywhere in the fragment;
//   - the argument list is not re-indexed: each argument binds to the next real "?" outside a
//     literal, so an argument intended for a quoted "?" instead shifts onto the following binding.
//
// This is the same rule interpolate applies on the display path (builder Explain / ToRawSql), so the
// executed fragment and the logged statement agree. A caller that needs a parameterised literal
// should bind it (WhereRaw and friends) rather than pass it to Raw.
func TestRawInterpolatesOnlyOutsideQuotedLiterals(t *testing.T) {
	// The only "?" sits inside a string literal, so there is no binding to consume the argument: it
	// is dropped and the literal is left exactly as written.
	assert.Equal(t, "select * from t where note = '?'",
		Raw("select * from t where note = '?'", "ARG").(querybuilder.Expression).Value)

	// The quoted "?" is skipped, so the two arguments land on the two real placeholders in order.
	assert.Equal(t, "select '?' = 'a' and x = 'b'",
		Raw("select '?' = ? and x = ?", "a", "b").(querybuilder.Expression).Value)

	// With no arguments the fragment is returned verbatim, quoted "?" included.
	assert.Equal(t, "select * from t where note = '?'",
		Raw("select * from t where note = '?'").(querybuilder.Expression).Value)
}

// TestRawQuotedLiteralScopeLimitBacktickAndBackslash records a known scope limit of the interpolator
// rather than a behaviour to rely on: the quote state machine models only "'" and '"'. It does not
// model backtick-quoted identifiers, nor a MySQL backslash-escaped quote, so a "?" inside either is
// treated as a binding.
//
//   - "select 'it\'s ?'" (MySQL's backslash escape) closes the literal early at the escaped quote, so
//     the following "?" is interpolated and the literal is corrupted;
//   - "select `a?b` = ?" (a backtick identifier) tracks no quote at all, so the "?" inside the
//     identifier consumes the argument and the real trailing binding is left as a bare "?".
//
// The framework's own grammars escape by doubling (” and ""), which the machine handles correctly,
// so this limit is only reachable from a hand-written fragment on a dialect that uses backslash
// escapes or backtick identifiers. If the interpolator is ever extended to model them, this test must
// change with it.
func TestRawQuotedLiteralScopeLimitBacktickAndBackslash(t *testing.T) {
	// MySQL backslash-escaped quote: the "?" inside the literal is interpolated with the argument.
	assert.Equal(t, `select 'it\'s 'x''`,
		Raw(`select 'it\'s ?'`, "x").(querybuilder.Expression).Value)

	// Backtick-quoted identifier: the "?" inside the identifier takes the argument, and the real
	// trailing "?" is not bound.
	assert.Equal(t, "select `a'x'b` = ?",
		Raw("select `a?b` = ?", "x").(querybuilder.Expression).Value)
}
