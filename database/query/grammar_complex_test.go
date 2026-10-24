package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// complexQuery builds a query that exercises select, join, where, where in, where null, group by,
// having, order by, limit and offset at once, so the whole component pipeline is covered.
func complexQuery(g Grammar) *Builder {
	return NewBuilder(g).
		From("users", "").
		Select("users.id", "users.name").
		LeftJoin("posts", "posts.user_id", "=", "users.id").
		Where("users.active", true).
		WhereIn("users.role", []any{"admin", "editor"}, core.BooleanAnd, false).
		WhereNull("users.deleted_at", core.BooleanAnd, false).
		GroupBy("users.id", "users.name").
		HavingRaw("count(*) > ?", []any{1}, core.BooleanAnd).
		OrderBy("users.created_at", "desc").
		Limit(10).
		Offset(5)
}

func TestComplexQuery(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		got := complexQuery(NewPostgresGrammar("")).ToSQL()
		assert.Equal(t, `select "users"."id", "users"."name" from "users" left join "posts" on "posts"."user_id" = "users"."id" where "users"."active" = ? and "users"."role" in (?, ?) and "users"."deleted_at" is null group by "users"."id", "users"."name" having count(*) > ? order by "users"."created_at" desc limit 10 offset 5`, got)
	})

	t.Run("mysql", func(t *testing.T) {
		got := complexQuery(NewMySqlGrammar("")).ToSQL()
		assert.Equal(t, "select `users`.`id`, `users`.`name` from `users` left join `posts` on `posts`.`user_id` = `users`.`id` where `users`.`active` = ? and `users`.`role` in (?, ?) and `users`.`deleted_at` is null group by `users`.`id`, `users`.`name` having count(*) > ? order by `users`.`created_at` desc limit 10 offset 5", got)
	})

	t.Run("sqlite", func(t *testing.T) {
		got := complexQuery(NewSqliteGrammar("")).ToSQL()
		assert.Equal(t, `select "users"."id", "users"."name" from "users" left join "posts" on "posts"."user_id" = "users"."id" where "users"."active" = ? and "users"."role" in (?, ?) and "users"."deleted_at" is null group by "users"."id", "users"."name" having count(*) > ? order by "users"."created_at" desc limit 10 offset 5`, got)
	})

	t.Run("sqlserver keeps the clauses in place", func(t *testing.T) {
		// SQL Server expresses a bare limit as top and needs an order by before offset, so the
		// limit and offset are checked separately from the shared query.
		b := NewBuilder(NewSqlServerGrammar("")).
			From("users", "").
			Select("users.id").
			LeftJoin("posts", "posts.user_id", "=", "users.id").
			Where("users.active", true).
			GroupBy("users.id").
			HavingRaw("count(*) > ?", []any{1}, core.BooleanAnd).
			OrderBy("users.created_at", "desc").
			Limit(10)

		assert.Equal(t, "select top 10 [users].[id] from [users] left join [posts] on [posts].[user_id] = [users].[id] where [users].[active] = ? group by [users].[id] having count(*) > ? order by [users].[created_at] desc", b.ToSQL())
	})

	t.Run("bindings line up with the placeholders", func(t *testing.T) {
		b := complexQuery(NewPostgresGrammar(""))
		_, bindings := b.ToSelectSQL()

		assert.Equal(t, []any{true, "admin", "editor", 1}, bindings)
	})
}

func TestUnionsAndAggregate(t *testing.T) {
	t.Run("union all", func(t *testing.T) {
		b := NewBuilder(NewPostgresGrammar("")).From("users", "").Select("name")
		b.UnionAll(func(q *Builder) *Builder {
			return q.From("archived_users", "").Select("name")
		})

		assert.Equal(t, `(select "name" from "users") union all (select "name" from "archived_users")`, b.ToSQL())
	})

	t.Run("union aggregate wraps in a derived table", func(t *testing.T) {
		b := NewBuilder(NewPostgresGrammar("")).From("users", "").Select("name")
		b.UnionAll(func(q *Builder) *Builder {
			return q.From("archived_users", "").Select("name")
		})
		b.State().Aggregate = &core.Aggregate{Function: "count", Columns: []any{"*"}}

		sql, _ := b.ToSelectSQL()
		assert.Contains(t, sql, "select count(*) as aggregate from (")
		assert.Contains(t, sql, `) as "temp_table"`)
	})
}
