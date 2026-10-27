package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/errors"
)

// This file pins Builder::invalidOperatorAndValue()'s case-sensitive membership test. Laravel tests
// the operator with in_array($operator, $this->operators), which is case-sensitive, so an uppercase
// "LIKE" with a null value is not an illegal combination; it reaches whereNull and renders
// "is not null". The oracle (illuminate/database v12.55.1) prints, for where('name', $op, null):
//
//   mysql   LIKE        select * from `users` where `name` is not null
//   mysql   Like        select * from `users` where `name` is not null
//   mysql   ILIKE       select * from `users` where `name` is not null
//   mysql   LIKE BINARY select * from `users` where `name` is not null
//   mysql   like        ERROR InvalidArgumentException: Illegal operator and value combination.
//
// InvalidOperator lowercases separately, so every one of these spellings still passes operator
// validation; only the null-value guard distinguishes them.

func TestInvalidOperatorAndValueOperatorCaseIsSignificant(t *testing.T) {
	for _, op := range []string{"LIKE", "Like", "ILIKE", "LIKE BINARY", "Is", "Rlike"} {
		t.Run(op, func(t *testing.T) {
			builder := newTestBuilder().From("users", "").Where("name", op, nil)

			assert.NoError(t, builder.Error())
			assert.Equal(t, `select * from "users" where "name" is not null`, builder.ToSQL())
		})
	}
}

func TestInvalidOperatorAndValueLowercaseStillRefused(t *testing.T) {
	for _, op := range []string{"like", "ilike", "like binary", "is", "rlike"} {
		t.Run(op, func(t *testing.T) {
			builder := newTestBuilder().From("users", "").Where("name", op, nil)

			assert.ErrorIs(t, builder.Error(), errors.QueryIllegalOperatorAndValue)
		})
	}
}

func TestInvalidOperatorAndValueEqualityOperatorsStillPass(t *testing.T) {
	// Laravel dispatches a null value to whereNull($column, $boolean, $operator !== "="), so only
	// "=" and "<=>" render "is null"; "<>" and "!=" render "is not null".
	tests := map[string]string{
		"=":   `select * from "users" where "name" is null`,
		"<=>": `select * from "users" where "name" is null`,
		"<>":  `select * from "users" where "name" is not null`,
		"!=":  `select * from "users" where "name" is not null`,
	}
	for op, want := range tests {
		t.Run(op, func(t *testing.T) {
			builder := newTestBuilder().From("users", "").Where("name", op, nil)

			assert.NoError(t, builder.Error())
			assert.Equal(t, want, builder.ToSQL())
		})
	}
}
