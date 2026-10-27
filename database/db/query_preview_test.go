package db

import (
	"context"
	"testing"
	"time"

	"github.com/gradii/framework/contracts/database/db"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func previewQuery(driver string) *Query {
	grammar, _ := querybuilder.NewGrammar(driver, "")
	return NewQuery(context.Background(), nil, nil, grammar, driver, nil, "users", nil)
}

func TestPublicQueryOptionalArguments(t *testing.T) {
	query := previewQuery("mysql")
	other := query.NewQuery().From("ignored")
	query.WhereIn("id", func(child db.Query) db.Query {
		child.From("posts").Select("user_id").Where("active", true)
		return other
	}).WhereExists(func(child db.Query) {
		child.From("profiles").WhereColumn("profiles.user_id", "users.id")
	}).WhereLike("name", "J%", true).InRandomOrder(17)
	assert.Equal(t, "select * from `users` where `id` in (select `user_id` from `posts` where `active` = ?) and exists (select * from `profiles` where `profiles`.`user_id` = `users`.`id`) and `name` like binary ? order by RAND(17)", query.ToSql().Get())
	assert.Equal(t, []any{true, "J%"}, query.GetBindings())

	unsupported := previewQuery("sqlserver")
	unsupported.WhereLike("name", "J%", true)
	_, _, err := unsupported.buildSelect()
	require.Error(t, err)
}

func TestRawSQLUsesConnectionEscapingAndPreparedBindings(t *testing.T) {
	for _, test := range []struct{ driver, binary string }{
		{"mysql", "x'00ff'"}, {"postgres", "'\\x00ff'::bytea"},
		{"sqlite", "x'00ff'"}, {"sqlserver", "0x00ff"},
	} {
		t.Run(test.driver, func(t *testing.T) {
			query := previewQuery(test.driver)
			query.allowTableless = true
			query.builder.From(nil, "")
			query.SelectRaw("?, ?, ?", []byte{0, 255}, "O'Reilly?", true)
			assert.Equal(t, "select "+test.binary+", 'O''Reilly?', 1", query.ToRawSql().Get())
		})
	}
	query := previewQuery("postgres")
	query.WhereRaw("document ?? ?", []any{"a?'b"}).Where("id", 7)
	assert.Equal(t, `select * from "users" where document ? 'a?''b' and "id" = 7`, query.ToRawSql().Get())
	query = previewQuery("mysql")
	query.SelectRaw(`'it\'s ?' as literal`).Where("name", `a\b'c`)
	assert.Equal(t, "select 'it\\'s ?' as literal from `users` where `name` = 'a\\\\b''c'", query.ToRawSql().Get())
	assert.Empty(t, previewQuery("mysql").Where("name", "invalid\x00text").ToRawSql().Get())
	query = previewQuery("sqlserver")
	query.Where("created_at", time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.UTC))
	assert.Contains(t, query.ToRawSql().Get(), "'2026-01-02 03:04:05.123'")
}

func TestPublicWriteSQLPreviews(t *testing.T) {
	assert.Equal(t, "insert ignore into `users` (`name`) values ('O''Reilly')", previewQuery("mysql").ToRawSql().InsertOrIgnore(map[string]any{"name": "O'Reilly"}))
	assert.Equal(t, `insert into "users" ("name") values ($1) returning "user_id"`, previewQuery("postgres").ToSql().InsertGetID(map[string]any{"name": "Alice"}, "user_id"))
	assert.Empty(t, previewQuery("postgres").ToSql().InsertGetID(nil))
	assert.Equal(t, "insert into `users` (`name`) select `name` from `archived` where `id` > ?", previewQuery("mysql").ToSql().InsertUsing([]string{"name"}, func(q db.Query) {
		q.From("archived").Select("name").Where("id", ">", 2)
	}))
	assert.Equal(t, "insert ignore into `users` (`name`) select 'Alice'", previewQuery("mysql").ToRawSql().InsertOrIgnoreUsing([]string{"name"}, "select 'Alice'"))
	assert.Equal(t, "insert into `users` (`id`, `name`) values (?, ?) on duplicate key update `name` = values(`name`)", previewQuery("mysql").ToSql().Upsert(map[string]any{"id": 1, "name": "Alice"}, []string{"id"}, []string{"name"}))
	assert.Equal(t, "insert into `users` (`id`) values (?)", previewQuery("mysql").ToSql().Upsert(map[string]any{"id": 1}, []string{"id"}, []string{}))
	assert.Equal(t, "update `users` set `votes` = `votes` + 2, `name` = 'Alice' where `id` = 1", previewQuery("mysql").Where("id", 1).ToRawSql().IncrementEach(map[string]any{"votes": 2}, map[string]any{"name": "Alice"}))
	assert.Equal(t, "update `users` set `votes` = `votes` - 2", previewQuery("mysql").ToSql().DecrementEach(map[string]any{"votes": 2}))
	assert.Equal(t, `update "users" set "name" = $1`, previewQuery("postgres").ToSql().UpdateFrom(map[string]any{"name": "Alice"}))
	assert.Len(t, previewQuery("sqlite").ToRawSql().Truncate(), 2)
	unsupported := previewQuery("mysql")
	assert.Empty(t, unsupported.ToSql().UpdateFrom(map[string]any{"name": "Alice"}))
	require.ErrorIs(t, unsupported.builder.Error(), errors.QueryUpdateFromNotSupported)
}

func TestPluckPreviewConsumesCallbackAndRestoresColumns(t *testing.T) {
	query := previewQuery("mysql")
	calls := 0
	query.BeforeQuery(func(q db.Query) db.Query { calls++; return q.Where("active", true) })
	assert.Equal(t, "select `name` from `users` where `active` = ?", query.ToSql().Pluck("name", nil))
	assert.Equal(t, "select * from `users` where `active` = ?", query.ToSql().Get())
	assert.Equal(t, 1, calls)
	var rows []map[string]any
	assert.ErrorIs(t, query.Paginate(int(^uint(0)>>1), 2, &rows, nil), errors.QueryPaginationOverflow)
}
