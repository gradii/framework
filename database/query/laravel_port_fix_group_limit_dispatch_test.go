package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGroupLimitDispatch pins that compileSelect short-circuits to compileGroupLimit when a group
// limit is set on the builder. Previously the entry point and the dispatch were both missing, so
// the group-limit machinery was unreachable: the builder fields and CompileGroupLimit were only
// touched by a direct-call test. Laravel's Grammar::compileSelect dispatches on isset($groupLimit);
// the oracle confirms `(new QueryBuilder)->from('users')->select('id')->groupBy('id')
// ->groupLimit(1,'id')->toSql()` renders the derived-table rewrite.
func TestGroupLimitDispatch(t *testing.T) {
	expected := `select * from (select "id", row_number() over (partition by "id") as "laravel_row" from "users" group by "id") as "laravel_table" where "laravel_row" <= 1 order by "laravel_row"`

	t.Run("the public entry point reaches the rewrite through ToSQL", func(t *testing.T) {
		builder := NewBuilder(NewBaseGrammar("")).From("users", "").Select("id").GroupBy("id").GroupLimit(1, "id")

		assert.Equal(t, expected, builder.ToSQL())
	})

	t.Run("a defaulted select list is added by the dispatch", func(t *testing.T) {
		builder := NewBuilder(NewBaseGrammar("")).From("users", "").GroupBy("id").GroupLimit(1, "id")

		assert.Contains(t, builder.ToSQL(), `select *, row_number() over (partition by "id")`)
	})

	t.Run("offset folds into the row-number bound", func(t *testing.T) {
		builder := NewBuilder(NewBaseGrammar("")).From("users", "").Select("id").GroupBy("id").GroupLimit(2, "id").Offset(1)

		assert.Contains(t, builder.ToSQL(), `where "laravel_row" <= 3 and "laravel_row" > 1`)
	})

	t.Run("a negative value leaves the builder without a group limit", func(t *testing.T) {
		builder := NewBuilder(NewBaseGrammar("")).From("users", "").Select("id").GroupBy("id").GroupLimit(-1, "id")

		assert.Equal(t, `select "id" from "users" group by "id"`, builder.ToSQL())
	})

	t.Run("the dialect override is the one dispatched", func(t *testing.T) {
		for _, grammar := range []Grammar{NewMySqlGrammar(""), NewSqliteGrammar("")} {
			sql := NewBuilder(grammar).From("users", "").Select("id").GroupBy("id").GroupLimit(1, "id").ToSQL()
			assert.Contains(t, sql, "row_number() over (partition by")
			assert.Contains(t, sql, "laravel_table")
			assert.Contains(t, sql, "order by")
		}
	})
}
