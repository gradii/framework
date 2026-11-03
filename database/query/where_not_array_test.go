package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file pins whereNot / orWhereNot with an array first argument on the raw builder, measured
// with the oracle (php .claude/laravel-oracle/oracle.php against illuminate/database v12.55.1,
// sqlite). Query\Builder::whereNot (Query/Builder.php:1027) wraps whereNested(..., $boolean.' not')
// around a re-entered where(), which expands the array through Builder::addArrayOfWheres
// (Query/Builder.php:928); the negation therefore wraps the whole group and the parentheses double:
// `and not (("b" = ? and "c" = ?))`, or `or not (("b" = ? or "c" = ?))` under orWhereNot, and an
// empty array adds no clause at all. The ORM's Eloquent level routes arrays through Eloquent's
// whereNot instead and negates per column; that shape lives in database/gorm/where_not_test.go.
func TestWhereNotArrayConditionsKeepTheQueryBuilderShape(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"where not array", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("a", 1).WhereNot(map[string]any{"b": 2, "c": 3})
		}, `select * from "users" where "a" = ? and not (("b" = ? and "c" = ?))`},
		{"or where not array", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("a", 1).OrWhereNot(map[string]any{"b": 2, "c": 3})
		}, `select * from "users" where "a" = ? or not (("b" = ? or "c" = ?))`},
		{"where not empty array", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("a", 1).WhereNot(map[string]any{})
		}, `select * from "users" where "a" = ?`},
	})

	// The bindings the oracle reports for the same chains.
	assert.Equal(t, []any{1, 2, 3},
		newTestBuilder().Select("*").From("users", "").Where("a", 1).WhereNot(map[string]any{"b": 2, "c": 3}).GetBindings())
	assert.Equal(t, []any{1, 2, 3},
		newTestBuilder().Select("*").From("users", "").Where("a", 1).OrWhereNot(map[string]any{"b": 2, "c": 3}).GetBindings())
	assert.Equal(t, []any{1},
		newTestBuilder().Select("*").From("users", "").Where("a", 1).WhereNot(map[string]any{}).GetBindings())
}
