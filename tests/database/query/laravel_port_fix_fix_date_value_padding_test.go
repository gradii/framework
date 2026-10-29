package query

import (
	"testing"
	stdtime "time"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/support/carbon"
	"github.com/stretchr/testify/assert"
)

// This file ports the zero-padding Laravel applies to the value of whereDay / whereMonth and their
// "or" variants, and pins it at the binding level as the corpus's loose assertEquals does not.
//
// Laravel's Builder::whereDay() formats a DateTimeInterface with 'd' and then runs
// sprintf('%02d', $value) over any non-expression value; Builder::whereMonth() does the same with
// 'm'. Builder::whereDate() ('Y-m-d'), whereTime() ('H:i:s') and whereYear() ('Y') format an object
// but perform no padding, so whereYear(2024) still binds the int 2024. The padding lives in the
// Builder, not in a grammar, so it is dialect-independent: all four dialects bind the same strings.
//
// Our four entry points (WhereDay, OrWhereDay, WhereMonth, OrWhereMonth) share whereDatePart in
// where.go, where the normalisation now lives; the expectations below were measured against
// illuminate/database v12.55.1 with the oracle, for mysql, pgsql, sqlite and sqlsrv.
//
// The corpus methods this file ports are testWhereDayMySql, testWhereMonthMySql, testWhereDaySqlite,
// testWhereDaySqlServer and testWhereMonthSqlServer. Their pre-padding ported copies live in
// laravel_port_dates_test.go; the cases here repeat the corpus SQL and add the binding's value AND
// type, because the corpus's assertEquals(1, '01') is loose in PHP (1 == '01') and would pass on
// either.
//
// It reuses laravelCase/runLaravelCases and dialectCase/runDialectCases from laravel_port_test.go.

// TestFixDateValuePaddingCorpusDialectCases re-runs the five corpus cases with their SQL and asserts
// the padded binding on each dialect the corpus uses.
func TestFixDateValuePaddingCorpusDialectCases(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"testWhereDayMySql", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, "select * from `users` where day(`created_at`) = ?"},
		{"testWhereMonthMySql", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5)
		}, "select * from `users` where month(`created_at`) = ?"},
		{"testWhereDaySqlite", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, `select * from "users" where strftime('%d', "created_at") = cast(? as text)`},
		{"testWhereDaySqlServer", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, "select * from [users] where day([created_at]) = ?"},
		{"testWhereMonthSqlServer", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5)
		}, "select * from [users] where month([created_at]) = ?"},
	})
}

// TestFixDateValuePaddingBindsTwoDigitStrings pins the binding for every entry point that routes
// through whereDatePart, on all four dialects: the value is a two-digit STRING, so the assertion is
// on []any of strings rather than on anything the corpus's loose comparison would accept.
func TestFixDateValuePaddingBindsTwoDigitStrings(t *testing.T) {
	dialects := []struct {
		name    string
		grammar Grammar
	}{
		{"mysql", NewMySqlGrammar("")},
		{"postgres", NewPostgresGrammar("")},
		{"sqlite", NewSqliteGrammar("")},
		{"sqlserver", NewSqlServerGrammar("")},
	}

	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			build := func(fn func(*Builder) *Builder) []any {
				return fn(NewBuilder(dialect.grammar)).Select("*").From("users", "").GetBindings()
			}

			// whereDay / whereMonth on the and-form.
			assert.Equal(t, []any{"01"}, build(func(b *Builder) *Builder {
				return b.WhereDay("created_at", "=", 1)
			}))
			assert.Equal(t, []any{"05"}, build(func(b *Builder) *Builder {
				return b.WhereMonth("created_at", "=", 5)
			}))

			// orWhereDay / orWhereMonth: the or-variants share the helper, so they pad too.
			assert.Equal(t, []any{"01"}, build(func(b *Builder) *Builder {
				return b.OrWhereDay("created_at", "=", 1)
			}))
			assert.Equal(t, []any{"05"}, build(func(b *Builder) *Builder {
				return b.OrWhereMonth("created_at", "=", 5)
			}))

			// The two-argument chain the corpus builds: Where("id", 1) followed by the two-argument
			// date form. Laravel binds [1, "01"] here, not [1, 1]; the orWhereDay/orWhereMonth port
			// tests pin the same shape.
			assert.Equal(t, []any{1, "01"}, build(func(b *Builder) *Builder {
				return b.Where("id", 1).OrWhereDay("created_at", 1)
			}))
			assert.Equal(t, []any{1, "05"}, build(func(b *Builder) *Builder {
				return b.Where("id", 1).OrWhereMonth("created_at", 5)
			}))
		})
	}
}

// TestFixDateValuePaddingUsesSprintfSemantics pins the cast Laravel's sprintf('%02d', $value)
// performs, oracle-verified: an already-padded string survives, "5abc" becomes 5, a float truncates,
// and a non-numeric string or nil becomes 0. Unlike the corpus, every value here is bound as a
// two-digit string.
func TestFixDateValuePaddingUsesSprintfSemantics(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"int nine", 9, "09"},
		{"int ten", 10, "10"},
		{"int one hundred twenty three", 123, "123"},
		{"already padded string", "05", "05"},
		{"leading digits", "5abc", "05"},
		{"leading whitespace", " 5", "05"},
		{"float truncates", 5.9, "05"},
		{"bool true", true, "01"},
		{"non-numeric string", "abc", "00"},
		{"empty string", "", "00"},
		{"nil", nil, "00"},
		{"one-element array", []any{9}, "09"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, []any{test.want}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
				WhereMonth("created_at", "=", test.value).GetBindings())
		})
	}
}

// TestFixDateValuePaddingFormatsDatesPerPart pins the DateTimeInterface half: Laravel formats the
// object with the PART's pattern first ('d' for day, 'm' for month), then pads. A 2024-05-06 value
// therefore binds "06" for whereDay and "05" for whereMonth, on every dialect.
func TestFixDateValuePaddingFormatsDatesPerPart(t *testing.T) {
	instant := stdtime.Date(2024, 5, 6, 12, 34, 56, 0, stdtime.UTC)

	dialects := []struct {
		name    string
		grammar Grammar
	}{
		{"mysql", NewMySqlGrammar("")},
		{"postgres", NewPostgresGrammar("")},
		{"sqlite", NewSqliteGrammar("")},
		{"sqlserver", NewSqlServerGrammar("")},
	}

	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			assert.Equal(t, []any{"06"}, NewBuilder(dialect.grammar).Select("*").From("users", "").
				WhereDay("created_at", "=", instant).GetBindings())
			assert.Equal(t, []any{"05"}, NewBuilder(dialect.grammar).Select("*").From("users", "").
				WhereMonth("created_at", "=", instant).GetBindings())
			assert.Equal(t, []any{"06"}, NewBuilder(dialect.grammar).Select("*").From("users", "").
				OrWhereDay("created_at", "=", carbon.Parse("2024-05-06 12:34:56")).GetBindings())
			assert.Equal(t, []any{"05"}, NewBuilder(dialect.grammar).Select("*").From("users", "").
				OrWhereMonth("created_at", "=", carbon.Parse("2024-05-06 12:34:56")).GetBindings())
		})
	}
}

// TestFixDateValuePaddingLeavesExpressionsAndOtherPartsAlone guards the blast radius: a Raw value is
// neither formatted nor padded (Laravel skips sprintf for an ExpressionContract), and whereDate /
// whereTime / whereYear keep binding non-object values as given - an int year stays an int, so
// whereYear(2024) binds [2024] and not "2024" or a padded "0024".
func TestFixDateValuePaddingLeavesExpressionsAndOtherPartsAlone(t *testing.T) {
	raw := Raw("NOW()")
	instant := stdtime.Date(2024, 5, 6, 12, 34, 56, 0, stdtime.UTC)

	build := func(fn func(*Builder) *Builder) *Builder {
		return fn(NewBuilder(NewMySqlGrammar("")).Select("*").From("users", ""))
	}

	// An expression records no binding and is emitted verbatim.
	assert.Empty(t, build(func(b *Builder) *Builder { return b.WhereDay("created_at", "=", raw) }).GetBindings())
	assert.Empty(t, build(func(b *Builder) *Builder { return b.WhereMonth("created_at", "=", raw) }).GetBindings())
	assert.Equal(t, "select * from `users` where day(`created_at`) = NOW()",
		build(func(b *Builder) *Builder { return b.WhereDay("created_at", "=", raw) }).ToSQL())
	assert.Equal(t, "select * from `users` where month(`created_at`) = NOW()",
		build(func(b *Builder) *Builder { return b.WhereMonth("created_at", "=", raw) }).ToSQL())

	// whereYear keeps the int: Laravel binds 2024, not "2024" and not "0024".
	assert.Equal(t, []any{2024}, build(func(b *Builder) *Builder {
		return b.WhereYear("created_at", "=", 2024)
	}).GetBindings())

	// whereDate and whereTime bind the string values they are given, unpadded.
	assert.Equal(t, []any{"2015-12-21"}, build(func(b *Builder) *Builder {
		return b.WhereDate("created_at", "=", "2015-12-21")
	}).GetBindings())
	assert.Equal(t, []any{"22:00"}, build(func(b *Builder) *Builder {
		return b.WhereTime("created_at", ">=", "22:00")
	}).GetBindings())

	// whereYear keeps the int and the string as given: Laravel binds [2024] for the int and ["2024"]
	// for the string, and the string is not padded either.
	assert.Equal(t, []any{"2024"}, build(func(b *Builder) *Builder {
		return b.WhereYear("created_at", "=", "2024")
	}).GetBindings())

	// whereDate / whereTime / whereYear format an object with the part's pattern ('Y-m-d', 'H:i:s',
	// 'Y'), matching Laravel - oracle rows: "2024-05-06", "12:34:56", "2024".
	assert.Equal(t, []any{"2024-05-06"}, build(func(b *Builder) *Builder {
		return b.WhereDate("created_at", "=", instant)
	}).GetBindings())
	assert.Equal(t, []any{"12:34:56"}, build(func(b *Builder) *Builder {
		return b.WhereTime("created_at", "=", instant)
	}).GetBindings())
	assert.Equal(t, []any{"2024"}, build(func(b *Builder) *Builder {
		return b.WhereYear("created_at", "=", instant)
	}).GetBindings())
}

// TestFixDateValuePaddingChangesTheCompiledSQLNotOnlyTheBinding states the user-visible effect the
// item's notes measured on SQLite: the column side is strftime('%m', ...) / strftime('%d', ...), which
// yields two-character strings, so the unpadded int could never match while "05" does. This asserts
// the SQL is unchanged by the fix (only the binding moved) and that the binding is the padded string
// the comparison needs.
func TestFixDateValuePaddingChangesTheCompiledSQLNotOnlyTheBinding(t *testing.T) {
	builder := NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5)

	assert.Equal(t, `select * from "users" where strftime('%m', "created_at") = cast(? as text)`, builder.ToSQL())
	assert.Equal(t, []any{"05"}, builder.GetBindings())

	builder = NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1)

	assert.Equal(t, `select * from "users" where strftime('%d', "created_at") = cast(? as text)`, builder.ToSQL())
	assert.Equal(t, []any{"01"}, builder.GetBindings())
}
