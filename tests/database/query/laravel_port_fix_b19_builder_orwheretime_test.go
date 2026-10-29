package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
)

// This file ports the "or where time" cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// testOrWhereTimeMySql and testOrWhereTimeSqlServer.
//
// orWhereTime has no grammar of its own: Laravel's Builder::orWhereTime() delegates to whereTime() with
// the "or" boolean, and each dialect's whereTime() override then compiles it. The corpus therefore only
// differs from whereTime in the joining boolean, and every case rebuilds the chain through our Builder
// and asserts the exact SQL the corpus asserts, over the dialect and identifier quoting the corpus uses.
//
// testOrWhereTimePostgres was deliberately NOT ported here: when this file was written our
// PostgresGrammar::whereTime() still emitted extract(time from ...), so the case was deferred to the
// sibling work item b23-postgresgrammar-wheretime-time-cast, which has since landed the ::time cast
// and now owns that case (see laravel_port_fix_b23_postgresgrammar_wheretime_time_cast_test.go).
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go.

// TestOrWhereTimeMySql ports Laravel's testOrWhereTimeMySql.
func TestOrWhereTimeMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", ">=", "22:00")
		}, "select * from `users` where time(`created_at`) <= ? or time(`created_at`) >= ?"},
	})

	assert.Equal(t, []any{"10:00", "22:00"}, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", ">=", "22:00").GetBindings())
}

// TestOrWhereTimeSqlServer ports Laravel's testOrWhereTimeSqlServer.
func TestOrWhereTimeSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bound values", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", ">=", "22:00")
		}, "select * from [users] where cast([created_at] as time) <= ? or cast([created_at] as time) >= ?"},
		{"raw value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", Raw("NOW()"))
		}, "select * from [users] where cast([created_at] as time) <= ? or cast([created_at] as time) = NOW()"},
	})

	assert.Equal(t, []any{"10:00", "22:00"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", ">=", "22:00").GetBindings())
	// assertEquals([0 => '10:00'], $builder->getBindings()) for the Raw form.
	assert.Equal(t, []any{"10:00"}, NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").
		WhereTime("created_at", "<=", "10:00").OrWhereTime("created_at", Raw("NOW()")).GetBindings())
}
