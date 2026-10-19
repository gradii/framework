package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/support/carbon"
)

// This file ports the "where past" cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// testWherePast and testWherePastUsesArray.
//
// wherePast / orWherePast own no grammar. Laravel's BuildsWhereDateClauses concern implements both by
// delegating to wherePastOrFuture(), which pushes one plain "Basic" descriptor per column (value =
// Carbon::now(), operator "<") and binds that value once per column. Unlike the whereToday family the
// value is the full timestamp, not a formatted "Y-m-d", and the column is not wrapped in date(), so each
// clause compiles like any other basic where: `"col" < ?`. No dialect overrides the compiler.
//
// Laravel freezes the clock with Carbon::setTestNow; the port freezes goravel's clock the same way
// through carbon.SetTestNow and restores it afterwards. The corpus also asserts the bindings equal
// Carbon::create(<the frozen now>), which the port mirrors with a full-slice comparison: goravel's
// carbon.Now() under the same frozen clock is deeply equal to the parsed value (verified), and the
// microsecond component is preserved.
//
// It reuses laravelCase/runLaravelCases from laravel_port_test.go.

// freezeNow pins the clock the way the corpus's Carbon::setTestNow does, so wherePast() resolves to the
// corpus instant for the duration of the test.
func freezeNow(t *testing.T, at string) {
	t.Helper()

	carbon.SetTestNow(carbon.Parse(at))
	t.Cleanup(carbon.ClearTestNow)
}

// TestWherePast ports Laravel's testWherePast.
func TestWherePast(t *testing.T) {
	freezeNow(t, "2022-04-20 23:45:06.123456")

	testDate := carbon.Parse("2022-04-20 23:45:06.123456")

	runLaravelCases(t, []laravelCase{
		{"wherePast", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WherePast("published_at")
		}, `select * from "posts" where "published_at" < ?`},
		{"where id = 1 orWherePast", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWherePast("published_at")
		}, `select * from "posts" where "id" = ? or "published_at" < ?`},
	})

	assert.Equal(t, []any{testDate}, newTestBuilder().Select("*").From("posts", "").
		WherePast("published_at").GetBindings())
	assert.Equal(t, []any{1, testDate}, newTestBuilder().Select("*").From("posts", "").
		Where("id", "=", 1).OrWherePast("published_at").GetBindings())
}

// TestWherePastUsesArray ports Laravel's testWherePastUsesArray: one clause is added per entry of the
// column array, all joined by the same boolean and all bound to the same instant.
func TestWherePastUsesArray(t *testing.T) {
	freezeNow(t, "2022-04-20 12:34:56.123456")

	testDate := carbon.Parse("2022-04-20 12:34:56.123456")

	runLaravelCases(t, []laravelCase{
		{"wherePast array", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WherePast([]any{"published_at", "held_at"})
		}, `select * from "posts" where "published_at" < ? and "held_at" < ?`},
		{"orWherePast array", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWherePast([]any{"published_at", "held_at"})
		}, `select * from "posts" where "id" = ? or "published_at" < ? or "held_at" < ?`},
	})

	assert.Equal(t, []any{testDate, testDate}, newTestBuilder().Select("*").From("posts", "").
		WherePast([]any{"published_at", "held_at"}).GetBindings())
	assert.Equal(t, []any{1, testDate, testDate}, newTestBuilder().Select("*").From("posts", "").
		Where("id", "=", 1).OrWherePast([]any{"published_at", "held_at"}).GetBindings())
}
