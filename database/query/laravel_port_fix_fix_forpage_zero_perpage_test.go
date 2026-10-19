package query

import (
	"testing"
)

// This file covers WORK ITEM 4 of the Laravel query-builder port's fix batch: ForPage must honour an
// explicit perPage of 0 the way Laravel does. Laravel's Builder::forPage($page, $perPage = 15)
// (Database/Query/Builder.php:2846) is offset(($page - 1) * $perPage)->limit($perPage): the 15 is a
// parameter default applied only when the caller omits perPage, never a substitution for a zero that
// was passed. ForPage used to replace a perPage of 0 with 15 - the divergence recorded in
// laravel_port_gap_pagination_gap_test.go - which made the three zero-page-size assertions of the
// corpus case unreachable. It now takes perPage ...int, defaulting to 15 when omitted and otherwise
// using the argument verbatim.
//
// The corpus case is tests/Database/DatabaseQueryBuilderTest.php testForPage (L2459-2484,
// laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569). The oracle
// (.claude/laravel-oracle/oracle.php, illuminate/database v12.55.1) prints forPage(2,0), forPage(0,0)
// and forPage(-2,0) each as "limit 0 offset 0", and forPage(2) as "limit 15 offset 15" - matching the
// corpus. The SQL is asserted verbatim, with our "?" placeholders and the base grammar's
// double-quoted identifiers, reusing laravelCase/runLaravelCases.

// TestLaravelPortFixForPageZeroPerPage pins the perPage-0 half of the corpus testForPage and the Go
// parameter default that stands in for Laravel's $perPage = 15.
func TestLaravelPortFixForPageZeroPerPage(t *testing.T) {
	// An explicit perPage of 0 is kept: limit 0, and the offset stays 0 because (page - 1) * 0 is 0.
	runLaravelCases(t, []laravelCase{
		{"forPage(2, 0)", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").ForPage(2, 0)
		}, `select * from "users" limit 0 offset 0`},
		{"forPage(0, 0)", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").ForPage(0, 0)
		}, `select * from "users" limit 0 offset 0`},
		{"forPage(-2, 0)", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").ForPage(-2, 0)
		}, `select * from "users" limit 0 offset 0`},
	})

	// The parameter default is a separate behaviour from a zero that was passed: an omitted perPage is
	// 15, so ForPage(2) and ForPage(1) match forPage(2, 15) and forPage(1, 15).
	runLaravelCases(t, []laravelCase{
		{"forPage(2) defaults to fifteen", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").ForPage(2)
		}, `select * from "users" limit 15 offset 15`},
		{"forPage(1) defaults to fifteen", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").ForPage(1)
		}, `select * from "users" limit 15 offset 0`},
	})
}
