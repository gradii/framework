package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/errors"
)

// This file pins the message Laravel's testPrepareValueAndOperatorExpectException asserts. The corpus
// case (tests/Database/DatabaseQueryBuilderTest.php L5152-5159, laravel/framework 12.x @
// 0fc645c6737626a1b1636cd234192b0d78b76569) expects InvalidArgumentException with the message
// "Illegal operator and value combination." - a capital I and a trailing full stop. Builder.php:956
// throws that string verbatim, and the oracle (.claude/laravel-oracle/oracle.php, illuminate/database
// v12.55.1) prints 'InvalidArgumentException: Illegal operator and value combination.' for all four
// dialects:
//
//   mysql   prepareValueAndOperator(null,like)  ERROR InvalidArgumentException: Illegal operator and value combination.
//
// The declared error errors.QueryIllegalOperatorAndValue now carries exactly that text, so a build
// that reaches the guard reports the corpus message rather than the earlier lowercased variant.
//
// The direct-call half of the corpus case lives in laravel_port_misc_test.go
// (TestPrepareValueAndOperatorExpectException); this file covers the same guard reached through the
// where pipeline, which is the path an application actually takes.

// TestPrepareValueAndOperatorIllegalMessageThroughWhere drives the guard through Where, the entry an
// application uses: a nil value with the "like" operator is an illegal combination, the builder
// records the error, and its text is Laravel's own.
func TestPrepareValueAndOperatorIllegalMessageThroughWhere(t *testing.T) {
	builder := newTestBuilder().From("users", "")

	// Where("col", "like", nil) supplies an explicit operator (two arguments), so useDefault is false
	// and the guard runs; a nil value with an operator other than =, <=>, <> or != is refused.
	builder.Where("foo", "like", nil)

	assert.ErrorIs(t, builder.Error(), errors.QueryIllegalOperatorAndValue)
	assert.EqualError(t, builder.Error(), "Illegal operator and value combination.")
}
