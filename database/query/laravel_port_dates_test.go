package query

import (
	"testing"
	stdtime "time"

	"github.com/gradii/framework/support/carbon"
	"github.com/stretchr/testify/assert"
)

// This file ports the "dates" area of Laravel's tests/Database/DatabaseQueryBuilderTest.php: exactly the
// cases the port plan marks PORTABLE with area dates — the whereDate / whereDay / whereMonth / whereYear /
// whereTime family, per dialect. Each case rebuilds the corpus chain with our Builder and asserts the
// exact SQL string; where the corpus also asserts getBindings(), the binding list is asserted inline.
//
// It reuses laravelCase/runLaravelCases and dialectCase/runDialectCases from laravel_port_test.go and
// newTestBuilder from grammar_base_test.go. The corpus builds dialect cases with getMySqlBuilder() /
// getPostgresBuilder() / getSQLiteBuilder() / getSqlServerBuilder() and no prefix, so those use
// NewMySqlGrammar("") and friends; the one case on the corpus getBuilder() uses the base grammar through
// newTestBuilder(). Our ToSQL renders "?" placeholders and double-quoted identifiers for every dialect,
// so the expected strings keep the corpus's own identifier quoting (a backtick pair is literal, and only
// the Go raw-string form is chosen to match each quoting).

// TestDateBasedWheresAcceptsTwoArguments ports Laravel's testDateBasedWheresAcceptsTwoArguments: the
// two-argument form leaves the operator to default ("="), which PrepareValueAndOperator supplies.
func TestDateBasedWheresAcceptsTwoArguments(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"whereDate", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", 1)
		}, "select * from `users` where date(`created_at`) = ?"},
		{"whereDay", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", 1)
		}, "select * from `users` where day(`created_at`) = ?"},
		{"whereMonth", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", 1)
		}, "select * from `users` where month(`created_at`) = ?"},
		{"whereYear", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", 1)
		}, "select * from `users` where year(`created_at`) = ?"},
	})
}

// TestDateBasedWheresExpressionIsNotBound ports Laravel's testDateBasedWheresExpressionIsNotBound: a Raw
// value contributes no binding, while a plain where() value still does. The corpus asserts only
// getBindings() for this method (on the base grammar, via getBuilder()); the SQL strings asserted here are
// our base-grammar rendering of the same chains, which the bindings depend on.
func TestDateBasedWheresExpressionIsNotBound(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"whereDate raw then plain where", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", Raw("NOW()")).Where("admin", true)
		}, `select * from "users" where date("created_at") = NOW() and "admin" = ?`},
		{"whereDay raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", Raw("NOW()"))
		}, `select * from "users" where day("created_at") = NOW()`},
		{"whereMonth raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", Raw("NOW()"))
		}, `select * from "users" where month("created_at") = NOW()`},
		{"whereYear raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", Raw("NOW()"))
		}, `select * from "users" where year("created_at") = NOW()`},
	})

	// assertEquals([true], $builder->getBindings()): the Raw is not bound, the plain where() value is.
	assert.Equal(t, []any{true}, newTestBuilder().Select("*").From("users", "").
		WhereDate("created_at", Raw("NOW()")).Where("admin", true).GetBindings())

	// assertEquals([], $builder->getBindings()) for each of the day/month/year Raw forms.
	assert.Empty(t, newTestBuilder().Select("*").From("users", "").WhereDay("created_at", Raw("NOW()")).GetBindings())
	assert.Empty(t, newTestBuilder().Select("*").From("users", "").WhereMonth("created_at", Raw("NOW()")).GetBindings())
	assert.Empty(t, newTestBuilder().Select("*").From("users", "").WhereYear("created_at", Raw("NOW()")).GetBindings())
}

// TestWhereDateMySql ports Laravel's testWhereDateMySql.
func TestWhereDateMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", "=", "2015-12-21")
		}, "select * from `users` where date(`created_at`) = ?"},
		{"raw value", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", "=", Raw("NOW()"))
		}, "select * from `users` where date(`created_at`) = NOW()"},
	})

	assert.Equal(t, []any{"2015-12-21"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereDate("created_at", "=", "2015-12-21").GetBindings())
}

// TestWhereDayMySql ports Laravel's testWhereDayMySql.
func TestWhereDayMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, "select * from `users` where day(`created_at`) = ?"},
	})

	assert.Equal(t, []any{"01"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1).GetBindings())
}

// TestWhereMonthMySql ports Laravel's testWhereMonthMySql.
func TestWhereMonthMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5)
		}, "select * from `users` where month(`created_at`) = ?"},
	})

	assert.Equal(t, []any{"05"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5).GetBindings())
}

// TestWhereMonthSqlite ports Laravel's testWhereMonthSqlite.
func TestWhereMonthSqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5)
		}, `select * from "users" where strftime('%m', "created_at") = cast(? as text)`},
	})

	assert.Equal(t, []any{"05"}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5).GetBindings())
}

// TestWhereTimeMySql ports Laravel's testWhereTimeMySql.
func TestWhereTimeMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", ">=", "22:00")
		}, "select * from `users` where time(`created_at`) >= ?"},
	})

	assert.Equal(t, []any{"22:00"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", ">=", "22:00").GetBindings())
}

// TestWhereTimeOperatorOptionalMySql ports Laravel's testWhereTimeOperatorOptionalMySql.
func TestWhereTimeOperatorOptionalMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"default operator", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "22:00")
		}, "select * from `users` where time(`created_at`) = ?"},
	})

	assert.Equal(t, []any{"22:00"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "22:00").GetBindings())
}

// TestWhereTimeSqlServer ports Laravel's testWhereTimeSqlServer.
func TestWhereTimeSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "22:00")
		}, "select * from [users] where cast([created_at] as time) = ?"},
		{"raw value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", Raw("NOW()"))
		}, "select * from [users] where cast([created_at] as time) = NOW()"},
	})

	assert.Equal(t, []any{"22:00"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "22:00").GetBindings())
	// assertEquals([], $builder->getBindings()) for the Raw form.
	assert.Empty(t, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", Raw("NOW()")).GetBindings())
}

// TestWhereDatePostgres ports Laravel's testWhereDatePostgres. The date comparison casts the column;
// a JSON selector is parenthesised with the ->> extraction before the cast.
func TestWhereDatePostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", "=", "2015-12-21")
		}, `select * from "users" where "created_at"::date = ?`},
		{"raw value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", Raw("NOW()"))
		}, `select * from "users" where "created_at"::date = NOW()`},
		{"json selector raw value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("result->created_at", Raw("NOW()"))
		}, `select * from "users" where ("result"->>'created_at')::date = NOW()`},
	})

	assert.Equal(t, []any{"2015-12-21"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereDate("created_at", "=", "2015-12-21").GetBindings())
}

// TestWhereDayPostgres ports Laravel's testWhereDayPostgres.
func TestWhereDayPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, `select * from "users" where extract(day from "created_at") = ?`},
	})

	assert.Equal(t, []any{"01"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1).GetBindings())
}

// TestWhereMonthPostgres ports Laravel's testWhereMonthPostgres.
func TestWhereMonthPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5)
		}, `select * from "users" where extract(month from "created_at") = ?`},
	})

	assert.Equal(t, []any{"05"}, NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5).GetBindings())
}

// TestWhereDateSqlite ports Laravel's testWhereDateSqlite: the date is compared against strftime's
// formatted column, with the bound value cast to text.
func TestWhereDateSqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", "=", "2015-12-21")
		}, `select * from "users" where strftime('%Y-%m-%d', "created_at") = cast(? as text)`},
		{"raw value", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", Raw("NOW()"))
		}, `select * from "users" where strftime('%Y-%m-%d', "created_at") = cast(NOW() as text)`},
	})

	assert.Equal(t, []any{"2015-12-21"}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereDate("created_at", "=", "2015-12-21").GetBindings())
}

// TestWhereDaySqlite ports Laravel's testWhereDaySqlite.
func TestWhereDaySqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, `select * from "users" where strftime('%d', "created_at") = cast(? as text)`},
	})

	assert.Equal(t, []any{"01"}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1).GetBindings())
}

// TestWhereYearSqlite ports Laravel's testWhereYearSqlite.
func TestWhereYearSqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", "=", 2014)
		}, `select * from "users" where strftime('%Y', "created_at") = cast(? as text)`},
	})

	assert.Equal(t, []any{2014}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereYear("created_at", "=", 2014).GetBindings())
}

// TestWhereTimeSqlite ports Laravel's testWhereTimeSqlite.
func TestWhereTimeSqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", ">=", "22:00")
		}, `select * from "users" where strftime('%H:%M:%S', "created_at") >= cast(? as text)`},
	})

	assert.Equal(t, []any{"22:00"}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", ">=", "22:00").GetBindings())
}

// TestWhereTimeOperatorOptionalSqlite ports Laravel's testWhereTimeOperatorOptionalSqlite: the omitted
// operator defaults to "=".
func TestWhereTimeOperatorOptionalSqlite(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"default operator", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "22:00")
		}, `select * from "users" where strftime('%H:%M:%S', "created_at") = cast(? as text)`},
	})

	assert.Equal(t, []any{"22:00"}, NewBuilder(NewSqliteGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "22:00").GetBindings())
}

// TestWhereDateSqlServer ports Laravel's testWhereDateSqlServer.
func TestWhereDateSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", "=", "2015-12-21")
		}, "select * from [users] where cast([created_at] as date) = ?"},
		{"raw value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDate("created_at", Raw("NOW()"))
		}, "select * from [users] where cast([created_at] as date) = NOW()"},
	})

	assert.Equal(t, []any{"2015-12-21"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereDate("created_at", "=", "2015-12-21").GetBindings())
}

// TestWhereDaySqlServer ports Laravel's testWhereDaySqlServer.
func TestWhereDaySqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereDay("created_at", "=", 1)
		}, "select * from [users] where day([created_at]) = ?"},
	})

	assert.Equal(t, []any{"01"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereDay("created_at", "=", 1).GetBindings())
}

// TestWhereMonthSqlServer ports Laravel's testWhereMonthSqlServer.
func TestWhereMonthSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereMonth("created_at", "=", 5)
		}, "select * from [users] where month([created_at]) = ?"},
	})

	assert.Equal(t, []any{"05"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereMonth("created_at", "=", 5).GetBindings())
}

// TestWhereYearSqlServer ports Laravel's testWhereYearSqlServer.
func TestWhereYearSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereYear("created_at", "=", 2014)
		}, "select * from [users] where year([created_at]) = ?"},
	})

	assert.Equal(t, []any{2014}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereYear("created_at", "=", 2014).GetBindings())
}

// TestDateBasedWhereFormatsDateTimeObjectPerPart pins the DateTimeInterface half of the date-part
// binding: Laravel formats the object with the PART's own pattern before binding - whereDate with
// "Y-m-d", whereTime with "H:i:s", whereYear with "Y" - while whereYear leaves a non-object int or
// string as given. Oracle bindings for a 2024-05-06 12:34:56 object: ["2024-05-06", "12:34:56",
// "2024"]; whereYear(2024) binds [2024] and whereYear("2024") binds ["2024"].
func TestDateBasedWhereFormatsDateTimeObjectPerPart(t *testing.T) {
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
			build := func(fn func(*Builder) *Builder) *Builder {
				return fn(NewBuilder(dialect.grammar).Select("*").From("users", ""))
			}

			assert.Equal(t, []any{"2024-05-06"}, build(func(b *Builder) *Builder {
				return b.WhereDate("created_at", "=", instant)
			}).GetBindings())
			assert.Equal(t, []any{"12:34:56"}, build(func(b *Builder) *Builder {
				return b.WhereTime("created_at", "=", instant)
			}).GetBindings())
			assert.Equal(t, []any{"2024"}, build(func(b *Builder) *Builder {
				return b.WhereYear("created_at", "=", instant)
			}).GetBindings())

			// A Carbon object formats the same way.
			assert.Equal(t, []any{"2024-05-06"}, build(func(b *Builder) *Builder {
				return b.WhereDate("created_at", "=", carbon.Parse("2024-05-06 12:34:56"))
			}).GetBindings())

			// A non-object year keeps its Go type: the int stays an int, the string stays a string.
			assert.Equal(t, []any{2024}, build(func(b *Builder) *Builder {
				return b.WhereYear("created_at", "=", 2024)
			}).GetBindings())
			assert.Equal(t, []any{"2024"}, build(func(b *Builder) *Builder {
				return b.WhereYear("created_at", "=", "2024")
			}).GetBindings())
		})
	}
}
