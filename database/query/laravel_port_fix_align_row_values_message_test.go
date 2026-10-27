package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/errors"
)

// This file pins the message Laravel's testWhereRowValuesArityMismatch asserts. The corpus case
// (tests/Database/DatabaseQueryBuilderTest.php L6506-6514, laravel/framework 12.x) expects
// InvalidArgumentException with the message "The number of columns must match the number of values"
// when one column meets two values. Builder.php:1993 throws that string verbatim:
//
//	if (count($columns) !== count($values)) {
//	    throw new InvalidArgumentException('The number of columns must match the number of values');
//	}
//
// The oracle (.claude/laravel-oracle/oracle.php, illuminate/database v12.55.1) prints the same class
// and message for all four dialects, since the check runs in the Builder before any grammar is
// reached:
//
//	mysql   InvalidArgumentException  The number of columns must match the number of values
//	pgsql   InvalidArgumentException  The number of columns must match the number of values
//	sqlite  InvalidArgumentException  The number of columns must match the number of values
//	sqlsrv  InvalidArgumentException  The number of columns must match the number of values
//
// errors.QueryRowValuesCountMismatch now carries exactly that text. We keep the package's deliberate
// deviation of recording the error on the builder (surfaced through Error()) rather than throwing;
// only the message has to match the corpus.

// TestWhereRowValuesArityMismatchMessage pins the text, and pins it case-sensitively: the earlier
// declared message lowercased the first letter, which the corpus's exact-message assertion rejects.
func TestWhereRowValuesArityMismatchMessage(t *testing.T) {
	builder := newTestBuilder().Select("*").From("orders", "").
		WhereRowValues([]any{"last_update"}, "<", []any{1, 2})

	assert.Error(t, builder.Error())
	assert.ErrorIs(t, builder.Error(), errors.QueryRowValuesCountMismatch)
	assert.EqualError(t, builder.Error(), "The number of columns must match the number of values")

	// The guard records nothing on the builder, so no where clause and no binding reach the SQL.
	assert.Equal(t, `select * from "orders"`, builder.ToSQL())
	assert.Empty(t, builder.GetBindings())
}

// TestWhereRowValuesArityMismatchMessagePerDialect runs the same arity check against every grammar:
// the check lives in the Builder, so no dialect can reach a different message, and no dialect
// overrides the compiler for it. Each builder must still render the bare select with no clause.
func TestWhereRowValuesArityMismatchMessagePerDialect(t *testing.T) {
	dialects := []struct {
		name    string
		grammar Grammar
		want    string
	}{
		{"mysql", NewMySqlGrammar(""), "select * from `orders`"},
		{"postgres", NewPostgresGrammar(""), `select * from "orders"`},
		{"sqlite", NewSqliteGrammar(""), `select * from "orders"`},
		{"sqlserver", NewSqlServerGrammar(""), "select * from [orders]"},
	}

	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			builder := NewBuilder(dialect.grammar).Select("*").From("orders", "").
				WhereRowValues([]any{"last_update"}, "<", []any{1, 2})

			assert.EqualError(t, builder.Error(), "The number of columns must match the number of values")
			assert.Equal(t, dialect.want, builder.ToSQL())
			assert.Empty(t, builder.GetBindings())
		})
	}
}

// TestWhereRowValuesArityMismatchMatchingArityStillCompiles guards the other side: the fixed message
// must not change how a well-formed tuple renders. The SQL is corpus L6492 (testWhereRowValues), with
// our ? placeholders and double-quoted identifiers under the generic grammar.
func TestWhereRowValuesArityMismatchMatchingArityStillCompiles(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"where row values", func(b *Builder) *Builder {
			return b.Select("*").From("orders", "").WhereRowValues([]any{"last_update", "order_number"}, "<", []any{1, 2})
		}, `select * from "orders" where ("last_update", "order_number") < (?, ?)`},
	})
}
