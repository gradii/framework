package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the Conditionable::unless() family that the query Builder consumes:
// Illuminate\Support\Traits\Conditionable::unless(), reached through
// Illuminate\Database\Concerns\BuildsQueries (which `use Conditionable`). unless() is the negation of
// when(): it runs the callback when the condition is (or resolves to) falsy, and the optional default
// callback when it is truthy. It is control flow, not a SQL clause, so the SQL the corpus asserts is
// just the callback that ran, chained onto the builder's own where().
//
// Laravel proves the condition by capturing it inside the callback; the cases here do the same, which
// also exercises our value-passing and the `?? $this` fallback when the callback returns nothing.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php, testUnlessCallback (L234-249),
// testUnlessCallbackWithReturn (L251-266), testUnlessCallbackWithDefault (L268-289).

// TestUnlessCallback ports Laravel's testUnlessCallback. Its callback returns nothing — PHP null — so
// unless()'s `?? $this` keeps the chain on the builder; the Go callback returns nil to mean the same.
// The truthy branch never calls the callback, so the captured condition is only ever false.
func TestUnlessCallback(t *testing.T) {
	var captured any
	callback := func(q *Builder, condition any) *Builder {
		captured = condition
		q.Where("id", "=", 1)

		return nil // PHP's "no return": unless() must fall back to the receiver.
	}

	runLaravelCases(t, []laravelCase{
		{"falsy runs callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Unless(false, callback).Where("email", "foo")
		}, `select * from "users" where "id" = ? and "email" = ?`},
		{"truthy skips callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Unless(true, callback).Where("email", "foo")
		}, `select * from "users" where "email" = ?`},
	})

	// Only the falsy call ran the callback, and it received the raw condition.
	assert.Equal(t, false, captured)
}

// TestUnlessCallbackWithReturn ports Laravel's testUnlessCallbackWithReturn: the callback returns the
// builder it was handed, which unless() must return unchanged (Laravel's `return $callback(...) ?? $this`
// takes the non-null result rather than $this).
func TestUnlessCallbackWithReturn(t *testing.T) {
	var captured any
	callback := func(q *Builder, condition any) *Builder {
		captured = condition

		return q.Where("id", "=", 1)
	}

	runLaravelCases(t, []laravelCase{
		{"falsy runs callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Unless(false, callback).Where("email", "foo")
		}, `select * from "users" where "id" = ? and "email" = ?`},
		{"truthy skips callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Unless(true, callback).Where("email", "foo")
		}, `select * from "users" where "email" = ?`},
	})

	assert.Equal(t, false, captured)
}

// TestUnlessCallbackWithDefault ports Laravel's testUnlessCallbackWithDefault: the falsy value runs the
// callback, the truthy value runs the default, and each receives the original value (0 / 'truthy') as
// its second argument. The two branches emit the same SQL shape, so the bindings are what tell them
// apart — Laravel asserts [1, 'foo'] then [2, 'foo'], and so do we.
func TestUnlessCallbackWithDefault(t *testing.T) {
	callback := func(q *Builder, condition any) *Builder {
		assert.Equal(t, 0, condition)
		q.Where("id", "=", 1)

		return nil
	}
	fallback := func(q *Builder, condition any) *Builder {
		assert.Equal(t, "truthy", condition)
		q.Where("id", "=", 2)

		return nil
	}

	falsy := newTestBuilder().From("users", "").Select("*").Unless(0, callback, fallback).Where("email", "foo")
	assert.Equal(t, `select * from "users" where "id" = ? and "email" = ?`, falsy.ToSQL())
	assert.Equal(t, []any{1, "foo"}, falsy.GetBindings())

	truthy := newTestBuilder().From("users", "").Select("*").Unless("truthy", callback, fallback).Where("email", "foo")
	assert.Equal(t, `select * from "users" where "id" = ? and "email" = ?`, truthy.ToSQL())
	assert.Equal(t, []any{2, "foo"}, truthy.GetBindings())
}

// TestUnlessPhpTruthiness pins the loose boolean cast unless()'s `if (! $value)` relies on — the mirror
// of TestWhenPhpTruthiness, since unless() negates the same test. The table is what the oracle prints.
func TestUnlessPhpTruthiness(t *testing.T) {
	falsy := []any{nil, false, 0, 0.0, "", "0", []any{}, map[string]any{}}
	for _, value := range falsy {
		assert.Falsef(t, truthy(value), "expected %#v to be falsy", value)
	}

	truthyValues := []any{true, 1, -1, 0.5, "a", "0.0", []any{1}, map[string]any{"a": 1}, struct{}{}, &struct{}{}}
	for _, value := range truthyValues {
		assert.Truef(t, truthy(value), "expected %#v to be truthy", value)
	}
}

// TestUnlessResolvesClosureValue ports the `$value instanceof Closure ? $value($this) : $value` arm of
// unless(): a func(*Builder) any is called with the builder and its result decides the branch. The
// resolver returns false, so unless() runs the callback.
func TestUnlessResolvesClosureValue(t *testing.T) {
	b := newTestBuilder().From("users", "").Select("*").
		Unless(func(*Builder) any { return false }, func(q *Builder, _ any) *Builder {
			return q.Where("id", "=", 1)
		}).
		Where("email", "foo")

	assert.Equal(t, `select * from "users" where "id" = ? and "email" = ?`, b.ToSQL())
	assert.Equal(t, []any{1, "foo"}, b.GetBindings())
}
