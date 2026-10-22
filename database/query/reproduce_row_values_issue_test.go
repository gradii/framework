package query

import (
	"testing"
)

// TestWhereRowValuesRequiresExplicitBoolean reproduces the finding: WhereRowValues
// requires an explicit boolean parameter, unlike other Where methods which have
// variadic defaults.
func TestWhereRowValuesRequiresExplicitBoolean(t *testing.T) {
	// Current signature requires all 4 parameters
	b := NewBuilder(NewBaseGrammar(""))
	b.From("users", "").WhereRowValues([]any{"a", "b"}, "=", []any{1, 2}, BooleanAnd)

	// Compare with Where which uses variadic args:
	// Where(column, args...) can be called as Where("col", "=", "val") and boolean defaults to "and"
	b2 := NewBuilder(NewBaseGrammar(""))
	b2.From("users", "").Where("name", "=", "test")

	// The finding: WhereRowValues doesn't follow the variadic-default convention
}

// TestOrWhereRowValuesDoesNotExist reproduces the finding: OrWhereRowValues is missing
func TestOrWhereRowValuesDoesNotExist(t *testing.T) {
	b := NewBuilder(NewBaseGrammar(""))

	// Current approach: explicit BooleanOr
	b.From("users", "").WhereRowValues([]any{"a", "b"}, "=", []any{1, 2}, BooleanOr)

	// Laravel has orWhereRowValues($columns, $operator, $values) which calls
	// whereRowValues with 'or' boolean. We don't have that method.
}
