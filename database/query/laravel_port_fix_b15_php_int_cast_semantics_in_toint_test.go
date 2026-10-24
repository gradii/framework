package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the whereIntegerInRaw family of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php — testWhereIntegerInRaw, testOrWhereIntegerInRaw,
// testWhereIntegerNotInRaw and testOrWhereIntegerNotInRaw (corpus L1410-1452). What these pin is
// PHP's (int) cast, which Builder::whereIntegerInRaw applies to every value after Arr::flatten and
// before the descriptor is stored (Builder.php:1294):
//
//	$value = (int) ($value instanceof BackedEnum ? $value->value : $value);
//
// so the string "1a" becomes 1 rather than failing to parse, and a backed enum contributes its
// integer value (Fixtures/Enums/Bar::FOO === 5). Our WhereIntegerInRaw carries the same cast in
// toInt; the Go cases pass the enum's integer value 5 directly, since Go has no BackedEnum.
//
// The nested form of testWhereIntegerInRaw also exercises Arr::flatten: PHP's associative inner
// arrays collapse to their values (the 'any' => '3' entry still contributes 3, proving keys are
// ignored), which a nested Go slice reproduces in the same value order. That flatten half is covered
// on its own, with integers, by TestWhereIntegerInRawFlattenNested in
// laravel_port_fix_b5_one_level_arr_flatten_in_the_binding_path_cleanb_test.go; here it is combined
// with the cast, which is what b5 left for this item.
//
// The generic builder is used (double quotes, "?" bindings), matching getBuilder() in the corpus.
// The expectations were confirmed against the oracle at .claude/laravel-oracle/oracle.php.
// It reuses laravelCase/runLaravelCases from laravel_port_test.go.

// TestWhereIntegerInRaw ports Laravel's testWhereIntegerInRaw (corpus L1410-1427).
func TestWhereIntegerInRaw(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"integer prefixed strings and enum", func(b *Builder) *Builder {
			// Corpus: ['1a', 2, Bar::FOO] -> in (1, 2, 5).
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{"1a", 2, 5}, core.BooleanAnd, false)
		}, `select * from "users" where "id" in (1, 2, 5)`},
		{"flattened nested arrays", func(b *Builder) *Builder {
			// Corpus: [['id' => '1a'], ['id' => 2], ['any' => '3'], ['id' => Bar::FOO]] -> in (1, 2, 3, 5).
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{
				[]any{"1a"}, []any{2}, []any{"3"}, []any{5},
			}, core.BooleanAnd, false)
		}, `select * from "users" where "id" in (1, 2, 3, 5)`},
	})

	// The values are inlined, so whereIntegerInRaw contributes no bindings.
	assert.Equal(t, []any(nil), newTestBuilder().Select("*").From("users", "").
		WhereIntegerInRaw("id", []any{"1a", 2, 5}, core.BooleanAnd, false).GetBindings())
}

// TestOrWhereIntegerInRaw ports Laravel's testOrWhereIntegerInRaw (corpus L1430-1436).
func TestOrWhereIntegerInRaw(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"or", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).
				WhereIntegerInRaw("id", []any{"1a", 2}, core.BooleanOr, false)
		}, `select * from "users" where "id" = ? or "id" in (1, 2)`},
	})

	// Only the leading basic where binds.
	assert.Equal(t, []any{1}, newTestBuilder().Select("*").From("users", "").Where("id", "=", 1).
		WhereIntegerInRaw("id", []any{"1a", 2}, core.BooleanOr, false).GetBindings())
}

// TestWhereIntegerNotInRaw ports Laravel's testWhereIntegerNotInRaw (corpus L1438-1444).
func TestWhereIntegerNotInRaw(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"not in raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{"1a", 2}, core.BooleanAnd, true)
		}, `select * from "users" where "id" not in (1, 2)`},
	})

	assert.Equal(t, []any(nil), newTestBuilder().Select("*").From("users", "").
		WhereIntegerInRaw("id", []any{"1a", 2}, core.BooleanAnd, true).GetBindings())
}

// TestOrWhereIntegerNotInRaw ports Laravel's testOrWhereIntegerNotInRaw (corpus L1446-1452).
func TestOrWhereIntegerNotInRaw(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"or not in raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).
				WhereIntegerInRaw("id", []any{"1a", 2}, core.BooleanOr, true)
		}, `select * from "users" where "id" = ? or "id" not in (1, 2)`},
	})

	assert.Equal(t, []any{1}, newTestBuilder().Select("*").From("users", "").Where("id", "=", 1).
		WhereIntegerInRaw("id", []any{"1a", 2}, core.BooleanOr, true).GetBindings())
}

// TestToIntPhpCast pins the PHP (int) cast semantics of the shared toInt helper directly, covering
// the edges the corpus values do not: truncation toward zero, an exponent, a bare sign and a string
// with no leading number.
func TestToIntPhpCast(t *testing.T) {
	assert.Equal(t, 1, toInt("1a"))
	assert.Equal(t, 3, toInt("3"))
	assert.Equal(t, 1, toInt("1.9"))
	assert.Equal(t, -1, toInt("-1.9"))
	assert.Equal(t, 1000, toInt("1e3"))
	assert.Equal(t, 0, toInt("abc"))
	assert.Equal(t, 0, toInt(""))
	assert.Equal(t, 0, toInt("-"))
	assert.Equal(t, 12, toInt(" 12abc"))
	assert.Equal(t, 0, toInt("0x1A"))
	assert.Equal(t, 5, toInt(5))
}
