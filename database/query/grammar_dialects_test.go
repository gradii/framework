package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPostgresGrammar(t *testing.T) {
	g := NewPostgresGrammar("")

	t.Run("bare", func(t *testing.T) {
		assert.Equal(t, `select * from "users"`, NewBuilder(g).From("users", "").ToSQL())
	})

	t.Run("where", func(t *testing.T) {
		assert.Equal(t, `select * from "users" where "id" = ?`,
			NewBuilder(g).From("users", "").Where("id", 1).ToSQL())
	})

	t.Run("distinct on", func(t *testing.T) {
		assert.Equal(t, `select distinct on ("name") "name" from "users"`,
			NewBuilder(g).From("users", "").Distinct([]any{"name"}).Select("name").ToSQL())
	})

	t.Run("ilike is the default like", func(t *testing.T) {
		// Postgres whereBasic casts the column to text whenever the operator contains "like",
		// which includes ilike.
		assert.Equal(t, `select * from "users" where "name"::text ilike ?`,
			NewBuilder(g).From("users", "").WhereLike("name", "%x%", false, BooleanAnd, false).ToSQL())
	})

	t.Run("json selector uses native operators", func(t *testing.T) {
		assert.Equal(t, `select * from "users" where "data"->>'a' = ?`,
			NewBuilder(g).From("users", "").Where("data->a", "x").ToSQL())
	})

	t.Run("where date casts", func(t *testing.T) {
		assert.Equal(t, `select * from "users" where "created_at"::date = ?`,
			NewBuilder(g).From("users", "").WhereDate("created_at", "2024-01-01").ToSQL())
	})

	t.Run("insert get id returns", func(t *testing.T) {
		b := NewBuilder(g).From("users", "")
		assert.Equal(t, `insert into "users" ("name") values (?) returning "id"`,
			g.CompileInsertGetId(b, []map[string]any{{"name": "x"}}, ""))
	})

	t.Run("truncate restarts identity and cascades", func(t *testing.T) {
		// PostgresGrammar::compileTruncate appends " cascade" by default (static $cascadeTruncate).
		b := NewBuilder(g).From("users", "")
		statements := g.CompileTruncate(b)
		assert.Len(t, statements, 1)
		assert.Equal(t, `truncate "users" restart identity cascade`, statements[0].SQL)
	})

	t.Run("upsert on conflict", func(t *testing.T) {
		b := NewBuilder(g).From("users", "")
		got := g.CompileUpsert(b, []map[string]any{{"id": 1, "name": "x"}}, []string{"id"},
			[]UpsertUpdate{{Column: "name"}})
		assert.Equal(t, `insert into "users" ("id", "name") values (?, ?) on conflict ("id") do update set "name" = "excluded"."name"`, got)
	})
}

func TestMySqlGrammar(t *testing.T) {
	g := NewMySqlGrammar("")

	t.Run("bare uses backticks", func(t *testing.T) {
		assert.Equal(t, "select * from `users`", NewBuilder(g).From("users", "").ToSQL())
	})

	t.Run("case sensitive like is like binary", func(t *testing.T) {
		assert.Equal(t, "select * from `users` where `name` like binary ?",
			NewBuilder(g).From("users", "").WhereLike("name", "%x%", true, BooleanAnd, false).ToSQL())
	})

	t.Run("lock for update", func(t *testing.T) {
		assert.Equal(t, "select * from `users` for update",
			NewBuilder(g).From("users", "").LockForUpdate().ToSQL())
	})

	t.Run("random order", func(t *testing.T) {
		assert.Equal(t, "select * from `users` order by RAND()",
			NewBuilder(g).From("users", "").InRandomOrder("").ToSQL())
	})

	t.Run("json selector unwraps", func(t *testing.T) {
		assert.Equal(t, "select * from `users` where json_unquote(json_extract(`data`, '$.\"a\"')) = ?",
			NewBuilder(g).From("users", "").Where("data->a", "x").ToSQL())
	})

	t.Run("insert or ignore", func(t *testing.T) {
		b := NewBuilder(g).From("users", "")
		assert.Equal(t, "insert ignore into `users` (`name`) values (?)",
			g.CompileInsertOrIgnore(b, []map[string]any{{"name": "x"}}))
	})

	t.Run("upsert on duplicate key", func(t *testing.T) {
		b := NewBuilder(g).From("users", "")
		got := g.CompileUpsert(b, []map[string]any{{"id": 1, "name": "x"}}, []string{"id"},
			[]UpsertUpdate{{Column: "name"}})
		assert.Equal(t, "insert into `users` (`id`, `name`) values (?, ?) on duplicate key update `name` = values(`name`)", got)
	})
}

func TestSqliteGrammar(t *testing.T) {
	g := NewSqliteGrammar("")

	t.Run("bare", func(t *testing.T) {
		assert.Equal(t, `select * from "users"`, NewBuilder(g).From("users", "").ToSQL())
	})

	t.Run("where date uses strftime", func(t *testing.T) {
		assert.Equal(t, `select * from "users" where strftime('%Y-%m-%d', "created_at") = cast(? as text)`,
			NewBuilder(g).From("users", "").WhereDate("created_at", "2024-01-01").ToSQL())
	})

	t.Run("json selector extracts", func(t *testing.T) {
		assert.Equal(t, `select * from "users" where json_extract("data", '$."a"') = ?`,
			NewBuilder(g).From("users", "").Where("data->a", "x").ToSQL())
	})

	t.Run("case sensitive like uses glob", func(t *testing.T) {
		assert.Equal(t, `select * from "users" where "name" glob ?`,
			NewBuilder(g).From("users", "").WhereLike("name", "%x%", true, BooleanAnd, false).ToSQL())
	})

	t.Run("insert or ignore", func(t *testing.T) {
		b := NewBuilder(g).From("users", "")
		assert.Equal(t, `insert or ignore into "users" ("name") values (?)`,
			g.CompileInsertOrIgnore(b, []map[string]any{{"name": "x"}}))
	})

	t.Run("truncate resets the sequence", func(t *testing.T) {
		b := NewBuilder(g).From("users", "")
		statements := g.CompileTruncate(b)
		assert.Len(t, statements, 2)
		assert.Equal(t, "delete from sqlite_sequence where name = ?", statements[0].SQL)
		assert.Equal(t, `delete from "users"`, statements[1].SQL)
	})
}

func TestSqlServerGrammar(t *testing.T) {
	g := NewSqlServerGrammar("")

	t.Run("bare uses brackets", func(t *testing.T) {
		assert.Equal(t, "select * from [users]", NewBuilder(g).From("users", "").ToSQL())
	})

	t.Run("limit without offset becomes top", func(t *testing.T) {
		assert.Equal(t, "select top 10 * from [users]",
			NewBuilder(g).From("users", "").Limit(10).ToSQL())
	})

	t.Run("random order uses newid", func(t *testing.T) {
		assert.Equal(t, "select * from [users] order by NEWID()",
			NewBuilder(g).From("users", "").InRandomOrder("").ToSQL())
	})

	t.Run("json selector uses json_value", func(t *testing.T) {
		assert.Equal(t, "select * from [users] where json_value([data], '$.\"a\"') = ?",
			NewBuilder(g).From("users", "").Where("data->a", "x").ToSQL())
	})

	t.Run("insert ID uses output", func(t *testing.T) {
		b := NewBuilder(g).From("users", "")
		assert.Equal(t, "insert into [users] ([name]) output inserted.[id] values (?)",
			g.CompileInsertGetId(b, []map[string]any{{"name": "x"}}, ""))
	})
}

func TestSqlServerOffsetPrecedesLimit(t *testing.T) {
	// SQL Server needs OFFSET before FETCH, so its component order differs from every other dialect.
	b := NewBuilder(NewSqlServerGrammar("")).
		From("users", "").
		Select("users.id").
		OrderBy("users.created_at", "desc").
		Offset(5).
		Limit(10)

	assert.Equal(t, "select [users].[id] from [users] order by [users].[created_at] desc offset 5 rows fetch next 10 rows only", b.ToSQL())
}
