package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/errors"
)

// This file ports the incrementEach argument-validation cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php. Builder::incrementEach() validates every entry before it
// builds any update: the amount must be numeric and the key a column name. The port records the same
// failure on the builder, surfaced through Error(), the way the other argument guards do.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. Laravel asserts an InvalidArgumentException and its exact
// message; the two messages are declared verbatim as errors.QueryIncrementAmountNonNumeric and
// errors.QueryIncrementEachNonAssociative, so the ported cases assert the same text. Confirmed by the
// oracle at .claude/laravel-oracle/oracle.php (illuminate/database v12.55.1), which throws
// "Non-numeric value passed as increment amount for column: 'col'." and "Non-associative array passed to
// incrementEach method." respectively.

// TestIncrementManyArgumentValidation1 ports Laravel's testIncrementManyArgumentValidation1 (corpus
// L2943-2950): a non-numeric amount is rejected, naming the column. Laravel's ['col' => 'a'] is a
// map[string]any in Go.
func TestIncrementManyArgumentValidation1(t *testing.T) {
	builder := newTestBuilder().From("users", "")

	builder.IncrementEach(map[string]any{"col": "a"})

	assert.EqualError(t, builder.Error(), "Non-numeric value passed as increment amount for column: 'col'.")
	assert.ErrorIs(t, builder.Error(), errors.QueryIncrementAmountNonNumeric)
}

// TestIncrementManyArgumentValidation2 ports Laravel's testIncrementManyArgumentValidation2 (corpus
// L2951-2958): a non-associative array is rejected. Laravel's [11 => 11] has an integer key, which a Go
// slice reproduces.
func TestIncrementManyArgumentValidation2(t *testing.T) {
	builder := newTestBuilder().From("users", "")

	builder.IncrementEach([]any{11})

	assert.EqualError(t, builder.Error(), "Non-associative array passed to incrementEach method.")
	assert.ErrorIs(t, builder.Error(), errors.QueryIncrementEachNonAssociative)
}

// TestIncrementEachBuildsRawIncrement is not a corpus case: it exercises the entry point's success path,
// which Laravel would only observe by executing update() against a connection. Every column becomes the
// raw expression <wrapped column> + <amount> in the recorded descriptor, which ToUpdateSQL compiles; the
// raw expression contributes no binding, so only the where value is bound.
func TestIncrementEachBuildsRawIncrement(t *testing.T) {
	builder := newTestBuilder().From("users", "").Where("id", 1)

	builder.IncrementEach(map[string]any{"a": 2})

	sql, bindings := builder.ToUpdateSQL(builder.UpdateValues())

	assert.Equal(t, `update "users" set "a" = "a" + 2 where "id" = ?`, sql)
	assert.Equal(t, []any{1}, bindings)
	assert.NoError(t, builder.Error())
}
