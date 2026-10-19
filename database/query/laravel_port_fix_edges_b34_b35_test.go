package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/errors"
)

// This file locks two of the low-severity semantic edges recorded by the Part 1 report and closed here.
//
// b34: Builder::incrementEach() takes a PHP array whose string keys name columns. The port recognised
// only map[string]any, so a map[string]int — the natural Go literal for numeric amounts such as
// ['a' => 2] — fell through to the non-associative guard. Any string-keyed map is now accepted
// (ascending key order); an integer-keyed map is still rejected, matching PHP, whose integer keys make
// the array non-associative. Oracle (illuminate/database v12.55.1): ['a'=>2] is a valid associative
// array; [11=>11] throws "Non-associative array passed to incrementEach method.".
//
// b35: Builder::dynamicWhere() snake-cases each method segment with Str::snake(), whose delimiter
// trigger is the ASCII lookahead "/(.)(?=[A-Z])/". A non-ASCII capital does not fire it, so oracle
// Str::snake('FooÉ') === 'fooé'; the previous unicode.IsUpper check produced 'foo_é'.

// TestIncrementEachAcceptsStringKeyedMaps covers the b34 map[string]int (and other string-keyed map)
// gap: such a map is associative and its values become the raw increment amounts.
func TestIncrementEachAcceptsStringKeyedMaps(t *testing.T) {
	builder := newTestBuilder().From("users", "").Where("id", 1)
	builder.IncrementEach(map[string]int{"a": 2})

	assert.NoError(t, builder.Error())
	sql, _ := builder.ToUpdateSQL(builder.UpdateValues())
	assert.Equal(t, `update "users" set "a" = "a" + 2 where "id" = ?`, sql)

	floatBuilder := newTestBuilder().From("users", "")
	floatBuilder.IncrementEach(map[string]float64{"n": 1.5})

	assert.NoError(t, floatBuilder.Error())
	sql, _ = floatBuilder.ToUpdateSQL(floatBuilder.UpdateValues())
	assert.Equal(t, `update "users" set "n" = "n" + 1.5`, sql)
}

// TestIncrementEachRejectsIntegerKeyedMap pins the b34 integer-key representation edge: a map with
// integer keys stands for PHP's non-associative array and is rejected, exactly as [11 => 11] is.
func TestIncrementEachRejectsIntegerKeyedMap(t *testing.T) {
	builder := newTestBuilder().From("users", "")
	builder.IncrementEach(map[int]int{11: 11})

	assert.EqualError(t, builder.Error(), "Non-associative array passed to incrementEach method.")
	assert.ErrorIs(t, builder.Error(), errors.QueryIncrementEachNonAssociative)
}

// TestDynamicWhereNonAsciiSegment pins the b35 non-ASCII laravelSnake edge: an ASCII-uppercase run gets
// the delimiter, a non-ASCII capital does not ("FooÉ" -> "fooé", not "foo_é").
func TestDynamicWhereNonAsciiAndAsciiSegments(t *testing.T) {
	ascii := newTestBuilder().From("users", "").DynamicWhere("whereFooBar", []any{1})
	assert.Equal(t, `select * from "users" where "foo_bar" = ?`, ascii.ToSQL())

	nonAscii := newTestBuilder().From("users", "").DynamicWhere("whereFooÉ", []any{1})
	assert.Equal(t, `select * from "users" where "fooé" = ?`, nonAscii.ToSQL())
}
