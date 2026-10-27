package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/support/carbon"
)

// This file ports the "where future" cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// testWhereFuture and testPassingArrayToWhereFuture.
//
// whereFuture / orWhereFuture own no grammar. Laravel's BuildsWhereDateClauses concern implements both by
// delegating to wherePastOrFuture(), which pushes one plain "Basic" descriptor per column (value =
// Carbon::now(), operator ">") and binds that value once per column. Unlike the whereToday family the
// value is the full timestamp, not a formatted "Y-m-d", and the column is not wrapped in date(), so each
// clause compiles like any other basic where: `"col" > ?`. No dialect overrides the compiler.
//
// Laravel freezes the clock with Carbon::setTestNow; the port freezes goravel's clock the same way
// through carbon.SetTestNow (freezeNow, shared with the where-past port) and restores it afterwards. The
// corpus also asserts the bindings equal Carbon::create(<the frozen now>), which the port mirrors with a
// full-slice comparison.
//
// It reuses laravelCase/runLaravelCases from laravel_port_test.go.

// TestWhereFuture ports Laravel's testWhereFuture.
func TestWhereFuture(t *testing.T) {
	freezeNow(t, "2022-04-22 21:01:23.123456")

	testDate := carbon.Parse("2022-04-22 21:01:23.123456")

	runLaravelCases(t, []laravelCase{
		{"whereFuture", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereFuture("published_at")
		}, `select * from "posts" where "published_at" > ?`},
		{"where id = 1 orWhereFuture", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWhereFuture("published_at")
		}, `select * from "posts" where "id" = ? or "published_at" > ?`},
	})

	assert.Equal(t, []any{testDate}, newTestBuilder().Select("*").From("posts", "").
		WhereFuture("published_at").GetBindings())
	assert.Equal(t, []any{1, testDate}, newTestBuilder().Select("*").From("posts", "").
		Where("id", "=", 1).OrWhereFuture("published_at").GetBindings())
}

// TestPassingArrayToWhereFuture ports Laravel's testPassingArrayToWhereFuture: one clause is added per
// entry of the column array, all joined by the same boolean and all bound to the same instant.
func TestPassingArrayToWhereFuture(t *testing.T) {
	freezeNow(t, "2022-04-22 01:23:45.123456")

	testDate := carbon.Parse("2022-04-22 01:23:45.123456")

	runLaravelCases(t, []laravelCase{
		{"whereFuture array", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").WhereFuture([]any{"published_at", "held_at"})
		}, `select * from "posts" where "published_at" > ? and "held_at" > ?`},
		{"orWhereFuture array", func(b *Builder) *Builder {
			return b.Select("*").From("posts", "").Where("id", "=", 1).OrWhereFuture([]any{"published_at", "held_at"})
		}, `select * from "posts" where "id" = ? or "published_at" > ? or "held_at" > ?`},
	})

	assert.Equal(t, []any{testDate, testDate}, newTestBuilder().Select("*").From("posts", "").
		WhereFuture([]any{"published_at", "held_at"}).GetBindings())
	assert.Equal(t, []any{1, testDate, testDate}, newTestBuilder().Select("*").From("posts", "").
		Where("id", "=", 1).OrWhereFuture([]any{"published_at", "held_at"}).GetBindings())
}
