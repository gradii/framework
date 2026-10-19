package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the Conditionable::when() family that the query Builder consumes:
// Illuminate\Support\Traits\Conditionable::when(), reached through
// Illuminate\Database\Concerns\BuildsQueries (which `use Conditionable`). when() is control flow, not
// a SQL clause: it runs the callback only when the condition is (or resolves to) truthy, and the
// optional default callback only when it is falsy. The SQL the corpus asserts is therefore just the
// callback that ran, chained onto the builder's own where().
//
// Laravel proves the condition by capturing it inside the callback; the cases here do the same, which
// also exercises our value-passing and the `?? $this` fallback when the callback returns nothing.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php, testWhenCallback (L175-190),
// testWhenCallbackWithReturn (L192-207), testWhenCallbackWithDefault (L209-231).

// TestWhenCallback ports Laravel's testWhenCallback. Its callback returns nothing — PHP null — so
// when()'s `?? $this` keeps the chain on the builder; the Go callback returns nil to mean the same.
// The falsy branch never calls the callback, so the captured condition is only ever true.
func TestWhenCallback(t *testing.T) {
	var captured any
	callback := func(q *Builder, condition any) *Builder {
		captured = condition
		q.Where("id", "=", 1)

		return nil // PHP's "no return": when() must fall back to the receiver.
	}

	runLaravelCases(t, []laravelCase{
		{"truthy runs callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").When(true, callback).Where("email", "foo")
		}, `select * from "users" where "id" = ? and "email" = ?`},
		{"falsy skips callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").When(false, callback).Where("email", "foo")
		}, `select * from "users" where "email" = ?`},
	})

	// Only the truthy call ran the callback, and it received the raw condition.
	assert.Equal(t, true, captured)
}

// TestWhenCallbackWithReturn ports Laravel's testWhenCallbackWithReturn: the callback returns the
// builder it was handed, which when() must return unchanged (Laravel's `return $callback(...) ?? $this`
// takes the non-null result rather than $this).
func TestWhenCallbackWithReturn(t *testing.T) {
	var captured any
	callback := func(q *Builder, condition any) *Builder {
		captured = condition

		return q.Where("id", "=", 1)
	}

	runLaravelCases(t, []laravelCase{
		{"truthy runs callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").When(true, callback).Where("email", "foo")
		}, `select * from "users" where "id" = ? and "email" = ?`},
		{"falsy skips callback", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").When(false, callback).Where("email", "foo")
		}, `select * from "users" where "email" = ?`},
	})

	assert.Equal(t, true, captured)
}

// TestWhenCallbackWithDefault ports Laravel's testWhenCallbackWithDefault: the truthy value runs the
// callback, the falsy value runs the default, and each receives the original value ('truthy' / 0) as
// its second argument. The two branches emit the same SQL shape, so the bindings are what tell them
// apart — Laravel asserts [1, 'foo'] then [2, 'foo'], and so do we.
func TestWhenCallbackWithDefault(t *testing.T) {
	callback := func(q *Builder, condition any) *Builder {
		assert.Equal(t, "truthy", condition)
		q.Where("id", "=", 1)

		return nil
	}
	fallback := func(q *Builder, condition any) *Builder {
		assert.Equal(t, 0, condition)
		q.Where("id", "=", 2)

		return nil
	}

	truthy := newTestBuilder().From("users", "").Select("*").When("truthy", callback, fallback).Where("email", "foo")
	assert.Equal(t, `select * from "users" where "id" = ? and "email" = ?`, truthy.ToSQL())
	assert.Equal(t, []any{1, "foo"}, truthy.GetBindings())

	falsy := newTestBuilder().From("users", "").Select("*").When(0, callback, fallback).Where("email", "foo")
	assert.Equal(t, `select * from "users" where "id" = ? and "email" = ?`, falsy.ToSQL())
	assert.Equal(t, []any{2, "foo"}, falsy.GetBindings())
}

// TestWhenPhpTruthiness pins the loose boolean cast when()'s `if ($value)` relies on, since the
// difference from Go's own semantics ("0" is falsy in PHP, truthy as a Go string) is exactly the kind
// of thing a port gets wrong. The table is what the oracle prints for these values.
func TestWhenPhpTruthiness(t *testing.T) {
	falsy := []any{nil, false, 0, 0.0, "", "0", []any{}, map[string]any{}}
	for _, value := range falsy {
		assert.Falsef(t, truthy(value), "expected %#v to be falsy", value)
	}

	truthyValues := []any{true, 1, -1, 0.5, "a", "0.0", []any{1}, map[string]any{"a": 1}, struct{}{}, &struct{}{}}
	for _, value := range truthyValues {
		assert.Truef(t, truthy(value), "expected %#v to be truthy", value)
	}
}

// TestWhenResolvesClosureValue ports the `$value instanceof Closure ? $value($this) : $value` arm of
// when(): a func(*Builder) any is called with the builder and its result decides the branch.
func TestWhenResolvesClosureValue(t *testing.T) {
	b := newTestBuilder().From("users", "").Select("*").
		When(func(*Builder) any { return true }, func(q *Builder, _ any) *Builder {
			return q.Where("id", "=", 1)
		}).
		Where("email", "foo")

	assert.Equal(t, `select * from "users" where "id" = ? and "email" = ?`, b.ToSQL())
	assert.Equal(t, []any{1, "foo"}, b.GetBindings())
}
