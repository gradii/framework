package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

func TestCompileSeams(t *testing.T) {
	t.Run("select", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("id", 1).ToSelectSQL()
		assert.Equal(t, `select * from "users" where "id" = ?`, sql)
		assert.Equal(t, []any{1}, bindings)
	})

	t.Run("insert", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").ToInsertSQL([]map[string]any{{"name": "x"}})
		assert.Equal(t, `insert into "users" ("name") values (?)`, sql)
		assert.Equal(t, []any{"x"}, bindings)
	})

	t.Run("insert bindings follow the column order", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			ToInsertSQL([]map[string]any{{"name": "x", "id": 1}, {"name": "y", "id": 2}})
		assert.Equal(t, `insert into "users" ("id", "name") values (?, ?), (?, ?)`, sql)
		assert.Equal(t, []any{1, "x", 2, "y"}, bindings)
	})

	t.Run("update puts set values before the where clause", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("id", 1).
			ToUpdateSQL(map[string]any{"name": "y"})
		assert.Equal(t, `update "users" set "name" = ? where "id" = ?`, sql)
		assert.Equal(t, []any{"y", 1}, bindings)
	})

	t.Run("delete", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("id", 1).ToDeleteSQL()
		assert.Equal(t, `delete from "users" where "id" = ?`, sql)
		assert.Equal(t, []any{1}, bindings)
	})

	t.Run("exists", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("id", 1).ToExistsSQL()
		assert.Equal(t, `select exists(select * from "users" where "id" = ?) as "exists"`, sql)
		assert.Equal(t, []any{1}, bindings)
	})

	t.Run("aggregate leaves the builder unchanged", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Where("id", 1)
		sql, bindings := b.ToAggregateSQL(core.Aggregate{Function: "count", Columns: []any{"*"}})
		assert.Equal(t, `select count(*) as aggregate from "users" where "id" = ?`, sql)
		assert.Equal(t, []any{1}, bindings)
		assert.Nil(t, b.State().Aggregate)
	})

	t.Run("truncate", func(t *testing.T) {
		statements := newTestBuilder().From("users", "").ToTruncateSQL()
		assert.Len(t, statements, 1)
		assert.Equal(t, `truncate table "users"`, statements[0].SQL)
	})
}

func TestTableSource(t *testing.T) {
	b := newTestBuilder()
	assert.Nil(t, b.TableSource())

	b.From("users", "")
	assert.Equal(t, "users", b.TableSource())

	b.From("users", "u")
	assert.Equal(t, "users as u", b.TableSource())

	b.FromRaw("(select 1) as t", nil)
	assert.Equal(t, "(select 1) as t", ExpressionValue(b.TableSource()))
}
