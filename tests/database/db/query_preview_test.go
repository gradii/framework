package db

import (
	"testing"
	"time"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	"github.com/stretchr/testify/assert"
)

// TestDialectQueryOptionalArguments pins the optional arguments of the public query clauses against
// the real MySQL dialect: the in-subquery callback, exists, the case-sensitive like and the random
// ordering all render the SQL Laravel's MySQL grammar produces.
func TestDialectQueryOptionalArguments(t *testing.T) {
	query := dialectQuery(t, "mysql", "users")
	other := query.NewQuery().From("ignored")
	query.WhereIn("id", func(child contractsdb.Query) contractsdb.Query {
		child.From("posts").Select("user_id").Where("active", true)
		return other
	}).WhereExists(func(child contractsdb.Query) {
		child.From("profiles").WhereColumn("profiles.user_id", "users.id")
	}).WhereLike("name", "J%", true).InRandomOrder(17)
	assert.Equal(t, "select * from `users` where `id` in (select `user_id` from `posts` where `active` = ?) and exists (select * from `profiles` where `profiles`.`user_id` = `users`.`id`) and `name` like binary ? order by RAND(17)", query.ToSql().Get())
	assert.Equal(t, []any{true, "J%"}, query.GetBindings())

	// SQL Server carries no case-sensitive like, so the facade records the capability error and
	// renders no SQL.
	unsupported := dialectQuery(t, "sqlserver", "users")
	unsupported.WhereLike("name", "J%", true)
	assert.Empty(t, unsupported.ToSql().Get())
}

// TestDialectRawSQLUsesConnectionEscaping pins that ToRawSql renders literals with the connection's
// dialect: binary columns, escaped strings and dates all come from the dialect's escaper.
func TestDialectRawSQLUsesConnectionEscaping(t *testing.T) {
	for _, test := range []struct{ driver, binary string }{
		{"mysql", "x'00ff'"}, {"postgres", "'\\x00ff'::bytea"},
		{"sqlite", "x'00ff'"}, {"sqlserver", "0x00ff"},
	} {
		t.Run(test.driver, func(t *testing.T) {
			// A fresh query is tableless, matching the facade's raw select path.
			raw := dialectQuery(t, test.driver, "users").NewQuery()
			raw.SelectRaw("?, ?, ?", []byte{0, 255}, "O'Reilly?", true)
			assert.Equal(t, "select "+test.binary+", 'O''Reilly?', 1", raw.ToRawSql().Get())
		})
	}

	query := dialectQuery(t, "postgres", "users")
	query.WhereRaw("document ?? ?", []any{"a?'b"}).Where("id", 7)
	assert.Equal(t, `select * from "users" where document ? 'a?''b' and "id" = 7`, query.ToRawSql().Get())

	query = dialectQuery(t, "mysql", "users")
	query.SelectRaw(`'it\'s ?' as literal`).Where("name", `a\b'c`)
	assert.Equal(t, "select 'it\\'s ?' as literal from `users` where `name` = 'a\\\\b''c'", query.ToRawSql().Get())
	assert.Empty(t, dialectQuery(t, "mysql", "users").Where("name", "invalid\x00text").ToRawSql().Get())

	query = dialectQuery(t, "sqlserver", "users")
	query.Where("created_at", time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.UTC))
	assert.Contains(t, query.ToRawSql().Get(), "'2026-01-02 03:04:05.123'")
}

// TestDialectWriteSQLPreviews pins the write statements the dialect grammars compile: insert ignore,
// returning, insert-using, upsert and the increment/decrement expressions.
func TestDialectWriteSQLPreviews(t *testing.T) {
	assert.Equal(t, "insert ignore into `users` (`name`) values ('O''Reilly')", dialectQuery(t, "mysql", "users").ToRawSql().InsertOrIgnore(map[string]any{"name": "O'Reilly"}))
	assert.Equal(t, `insert into "users" ("name") values ($1) returning "user_id"`, dialectQuery(t, "postgres", "users").ToSql().InsertGetID(map[string]any{"name": "Alice"}, "user_id"))
	assert.Empty(t, dialectQuery(t, "postgres", "users").ToSql().InsertGetID(nil))
	assert.Equal(t, "insert into `users` (`name`) select `name` from `archived` where `id` > ?", dialectQuery(t, "mysql", "users").ToSql().InsertUsing([]string{"name"}, func(q contractsdb.Query) {
		q.From("archived").Select("name").Where("id", ">", 2)
	}))
	assert.Equal(t, "insert ignore into `users` (`name`) select 'Alice'", dialectQuery(t, "mysql", "users").ToRawSql().InsertOrIgnoreUsing([]string{"name"}, "select 'Alice'"))
	assert.Equal(t, "insert into `users` (`id`, `name`) values (?, ?) on duplicate key update `name` = values(`name`)", dialectQuery(t, "mysql", "users").ToSql().Upsert(map[string]any{"id": 1, "name": "Alice"}, []string{"id"}, []string{"name"}))
	assert.Equal(t, "insert into `users` (`id`) values (?)", dialectQuery(t, "mysql", "users").ToSql().Upsert(map[string]any{"id": 1}, []string{"id"}, []string{}))
	assert.Equal(t, "update `users` set `votes` = `votes` + 2, `name` = 'Alice' where `id` = 1", dialectQuery(t, "mysql", "users").Where("id", 1).ToRawSql().IncrementEach(map[string]any{"votes": 2}, map[string]any{"name": "Alice"}))
	assert.Equal(t, "update `users` set `votes` = `votes` - 2", dialectQuery(t, "mysql", "users").ToSql().DecrementEach(map[string]any{"votes": 2}))
	assert.Equal(t, `update "users" set "name" = $1`, dialectQuery(t, "postgres", "users").ToSql().UpdateFrom(map[string]any{"name": "Alice"}))
	assert.Len(t, dialectQuery(t, "sqlite", "users").ToRawSql().Truncate(), 2)
	assert.Empty(t, dialectQuery(t, "mysql", "users").ToSql().UpdateFrom(map[string]any{"name": "Alice"}))
}
