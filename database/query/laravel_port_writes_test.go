package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the writes area of Laravel's tests/Database/QueryBuilderTest.php: insert,
// insertGetId, insertOrIgnore and update. Laravel asserts these through a connection or processor
// spy, and what the spy receives is precisely the compiled statement, which is what the cases here
// compare together with the bindings the statement carries.
//
// The port is literal about SQL shape: a clause that renders differently from Laravel is meant to
// show up here rather than be smoothed over by the Go API.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. The line ranges name the corpus method's slice.

// TestInsertMethod ports Laravel's testInsertMethod (corpus L3831-3838).
func TestInsertMethod(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").ToInsertSQL([]map[string]any{{"email": "foo"}})

	assert.Equal(t, `insert into "users" ("email") values (?)`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestMySqlInsertOrIgnoreMethod ports Laravel's testMySqlInsertOrIgnoreMethod (corpus L3884-3891).
func TestMySqlInsertOrIgnoreMethod(t *testing.T) {
	sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").
		ToInsertOrIgnoreSQL([]map[string]any{{"email": "foo"}})

	assert.Equal(t, "insert ignore into `users` (`email`) values (?)", sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestPostgresInsertOrIgnoreMethod ports Laravel's testPostgresInsertOrIgnoreMethod (corpus
// L3892-3899): Postgres spells the ignore as a conflict clause rather than a different verb.
func TestPostgresInsertOrIgnoreMethod(t *testing.T) {
	sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
		ToInsertOrIgnoreSQL([]map[string]any{{"email": "foo"}})

	assert.Equal(t, `insert into "users" ("email") values (?) on conflict do nothing`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestSqliteInsertOrIgnoreMethod ports Laravel's testSQLiteInsertOrIgnoreMethod (corpus L3900-3907).
func TestSqliteInsertOrIgnoreMethod(t *testing.T) {
	sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").
		ToInsertOrIgnoreSQL([]map[string]any{{"email": "foo"}})

	assert.Equal(t, `insert or ignore into "users" ("email") values (?)`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestSqlServerInsertOrIgnoreMethod ports Laravel's testSqlServerInsertOrIgnoreMethod (corpus
// L3908-3915), which expects a RuntimeException saying 'does not support'. SQL Server inherits the
// base refusal; the corpus asserts the message, not a SQL string, so the statement is empty here too.
func TestSqlServerInsertOrIgnoreMethod(t *testing.T) {
	builder := NewBuilder(NewSqlServerGrammar("")).From("users", "")

	sql, _ := builder.ToInsertOrIgnoreSQL([]map[string]any{{"email": "foo"}})

	assert.Empty(t, sql)
	assert.ErrorContains(t, builder.Error(), "does not support")
}

// TestInsertGetIdMethod ports Laravel's testInsertGetIdMethod (corpus L4047-4054). The base grammar
// ignores the sequence argument, which is what the corpus's spy receives.
func TestInsertGetIdMethod(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").
		ToInsertGetIdSQL([]map[string]any{{"email": "foo"}}, "id")

	assert.Equal(t, `insert into "users" ("email") values (?)`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestInsertGetIdWithEmptyValues ports Laravel's testInsertGetIdWithEmptyValues (corpus L4063-4081).
// Each dialect spells an insert of no values its own way. SQL Server uses OUTPUT for go-mssqldb,
// whose generated-key API differs from the PDO API Laravel's SQL Server processor uses.
func TestInsertGetIdWithEmptyValues(t *testing.T) {
	t.Run("mysql", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").ToInsertGetIdSQL(nil, "")

		assert.Equal(t, "insert into `users` () values ()", sql)
		assert.Empty(t, bindings)
	})

	t.Run("postgres", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").ToInsertGetIdSQL(nil, "")

		assert.Equal(t, `insert into "users" default values returning "id"`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("sqlite", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").ToInsertGetIdSQL(nil, "")

		assert.Equal(t, `insert into "users" default values`, sql)
		assert.Empty(t, bindings)
	})

	t.Run("sqlserver", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users", "").ToInsertGetIdSQL(nil, "")

		assert.Equal(t, "insert into [users] output inserted.[id] default values", sql)
		assert.Empty(t, bindings)
	})
}

// TestInsertMethodRespectsRawBindings ports Laravel's testInsertMethodRespectsRawBindings (corpus
// L4082-4089): an expression value renders inline and leaves no binding behind, so the statement
// carries the empty binding list the corpus asserts.
func TestInsertMethodRespectsRawBindings(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").
		ToInsertSQL([]map[string]any{{"email": Raw("CURRENT TIMESTAMP")}})

	assert.Equal(t, `insert into "users" ("email") values (CURRENT TIMESTAMP)`, sql)
	assert.Empty(t, bindings)
}

// TestMultipleInsertsWithExpressionValues ports Laravel's testMultipleInsertsWithExpressionValues
// (corpus L4090-4097): one column, one record per expression, and no bindings at all.
func TestMultipleInsertsWithExpressionValues(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").ToInsertSQL([]map[string]any{
		{"email": Raw("UPPER('Foo')")},
		{"email": Raw("LOWER('Foo')")},
	})

	assert.Equal(t, `insert into "users" ("email") values (UPPER('Foo')), (LOWER('Foo'))`, sql)
	assert.Empty(t, bindings)
}

// TestUpdateMethod ports Laravel's testUpdateMethod (corpus L4098-4110).
func TestUpdateMethod(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("id", 1).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "id" = ?`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("mysql appends order and limit", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("id", 1).
			OrderBy("foo", "desc").Limit(5).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, "update `users` set `email` = ?, `name` = ? where `id` = ? order by `foo` desc limit 5", sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})
}

// TestUpdateMethodWithJoins ports Laravel's testUpdateMethodWithJoins (corpus L4175-4190). The
// closure form puts its condition inside the join, so it lands in the on clause and its binding is
// queued with the join rather than with the where clause.
func TestUpdateMethodWithJoins(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			Join("orders", "users.id", "=", "orders.user_id", "inner", false).
			Where("users.id", 1).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" inner join "orders" on "users"."id" = "orders"."user_id" set "email" = ?, "name" = ? where "users"."id" = ?`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("base closure join", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).Where("users.id", 1)
			}, "", "", "inner", false).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" inner join "orders" on "users"."id" = "orders"."user_id" and "users"."id" = ? set "email" = ?, "name" = ?`, sql)
		assert.Equal(t, []any{1, "foo", "bar"}, bindings)
	})
}

// TestUpdateMethodWithJoinsOnSqlServer ports Laravel's testUpdateMethodWithJoinsOnSqlServer (corpus
// L4191-4206): SQL Server keeps the join out of the update target and names the table again in from.
func TestUpdateMethodWithJoinsOnSqlServer(t *testing.T) {
	t.Run("sqlserver", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users", "").
			Join("orders", "users.id", "=", "orders.user_id", "inner", false).
			Where("users.id", 1).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, "update [users] set [email] = ?, [name] = ? from [users] inner join [orders] on [users].[id] = [orders].[user_id] where [users].[id] = ?", sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("sqlserver closure join", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).Where("users.id", 1)
			}, "", "", "inner", false).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, "update [users] set [email] = ?, [name] = ? from [users] inner join [orders] on [users].[id] = [orders].[user_id] and [users].[id] = ?", sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})
}

// TestUpdateMethodWithJoinsOnMySql ports Laravel's testUpdateMethodWithJoinsOnMySql (corpus
// L4207-4222): MySQL keeps the join in the update statement itself.
func TestUpdateMethodWithJoinsOnMySql(t *testing.T) {
	t.Run("mysql", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").
			Join("orders", "users.id", "=", "orders.user_id", "inner", false).
			Where("users.id", 1).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, "update `users` inner join `orders` on `users`.`id` = `orders`.`user_id` set `email` = ?, `name` = ? where `users`.`id` = ?", sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("mysql closure join", func(t *testing.T) {
		sql, bindings := NewBuilder(NewMySqlGrammar("")).From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).Where("users.id", 1)
			}, "", "", "inner", false).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, "update `users` inner join `orders` on `users`.`id` = `orders`.`user_id` and `users`.`id` = ? set `email` = ?, `name` = ?", sql)
		assert.Equal(t, []any{1, "foo", "bar"}, bindings)
	})
}

// TestUpdateMethodWithJoinsOnSQLite ports Laravel's testUpdateMethodWithJoinsOnSQLite (corpus
// L4223-4248). SQLite cannot update through a join or a limit, so both are rewritten as a rowid
// membership test, and the alias of the target is used to reach the rowid.
func TestUpdateMethodWithJoinsOnSQLite(t *testing.T) {
	t.Run("sqlite with limit and order", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").Where("users.id", ">", 1).
			Limit(3).Oldest("id").
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "rowid" in (select "users"."rowid" from "users" where "users"."id" > ? order by "id" asc limit 3)`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("sqlite with a join", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").
			Join("orders", "users.id", "=", "orders.user_id", "inner", false).
			Where("users.id", 1).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "rowid" in (select "users"."rowid" from "users" inner join "orders" on "users"."id" = "orders"."user_id" where "users"."id" = ?)`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("sqlite with a closure join", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).Where("users.id", 1)
			}, "", "", "inner", false).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "rowid" in (select "users"."rowid" from "users" inner join "orders" on "users"."id" = "orders"."user_id" and "users"."id" = ?)`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("sqlite with aliases", func(t *testing.T) {
		sql, bindings := NewBuilder(NewSqliteGrammar("")).From("users as u", "").
			Join("orders as o", "u.id", "=", "o.user_id", "inner", false).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" as "u" set "email" = ?, "name" = ? where "rowid" in (select "u"."rowid" from "users" as "u" inner join "orders" as "o" on "u"."id" = "o"."user_id")`, sql)
		assert.Equal(t, []any{"foo", "bar"}, bindings)
	})
}

// TestUpdateMethodWithJoinsAndAliasesOnSqlServer ports Laravel's
// testUpdateMethodWithJoinsAndAliasesOnSqlServer (corpus L4249-4256): the update target is the alias.
func TestUpdateMethodWithJoinsAndAliasesOnSqlServer(t *testing.T) {
	sql, bindings := NewBuilder(NewSqlServerGrammar("")).From("users as u", "").
		Join("orders", "u.id", "=", "orders.user_id", "inner", false).
		Where("u.id", 1).
		ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

	assert.Equal(t, "update [u] set [email] = ?, [name] = ? from [users] as [u] inner join [orders] on [u].[id] = [orders].[user_id] where [u].[id] = ?", sql)
	assert.Equal(t, []any{"foo", "bar", 1}, bindings)
}

// TestUpdateMethodWithJoinsOnPostgres ports Laravel's testUpdateMethodWithJoinsOnPostgres (corpus
// L4275-4300). Postgres cannot update through a join either, so it rewrites through ctid, the same
// way it does for a limit.
func TestUpdateMethodWithJoinsOnPostgres(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
			Join("orders", "users.id", "=", "orders.user_id", "inner", false).
			Where("users.id", 1).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "ctid" in (select "users"."ctid" from "users" inner join "orders" on "users"."id" = "orders"."user_id" where "users"."id" = ?)`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("postgres closure join", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).Where("users.id", 1)
			}, "", "", "inner", false).
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "ctid" in (select "users"."ctid" from "users" inner join "orders" on "users"."id" = "orders"."user_id" and "users"."id" = ?)`, sql)
		assert.Equal(t, []any{"foo", "bar", 1}, bindings)
	})

	t.Run("postgres closure join with an outer where", func(t *testing.T) {
		sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).Where("users.id", 1)
			}, "", "", "inner", false).
			Where("name", "baz").
			ToUpdateSQL(map[string]any{"email": "foo", "name": "bar"})

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? where "ctid" in (select "users"."ctid" from "users" inner join "orders" on "users"."id" = "orders"."user_id" and "users"."id" = ? where "name" = ?)`, sql)
		assert.Equal(t, []any{"foo", "bar", 1, "baz"}, bindings)
	})
}

// TestUpdateMethodRespectsRaw ports Laravel's testUpdateMethodRespectsRaw (corpus L4327-4334): the
// expression renders inline and contributes no binding, while the rest keep theirs.
func TestUpdateMethodRespectsRaw(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").Where("id", 1).
		ToUpdateSQL(map[string]any{"email": Raw("foo"), "name": "bar"})

	assert.Equal(t, `update "users" set "email" = foo, "name" = ? where "id" = ?`, sql)
	assert.Equal(t, []any{"bar", 1}, bindings)
}

// TestPostgresInsertGetId ports Laravel's testPostgresInsertGetId (corpus L4667-4674): Postgres is
// the dialect whose insertGetId carries the returning clause in the statement.
func TestPostgresInsertGetId(t *testing.T) {
	sql, bindings := NewBuilder(NewPostgresGrammar("")).From("users", "").
		ToInsertGetIdSQL([]map[string]any{{"email": "foo"}}, "id")

	assert.Equal(t, `insert into "users" ("email") values (?) returning "id"`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestInsertGetIdMethodRemovesExpressions ports Laravel's testInsertGetIdMethodRemovesExpressions
// (corpus L4055-4062): a record mixing a bound value and an expression renders the expression inline
// and keeps only the bound value as a binding. Unlike insert(), Laravel's insertGetId() does not
// ksort() its single record, so the column list is the record's insertion order — ("email", "bar") —
// not the sorted order. A Go map cannot carry that order, so the corpus's associative record is
// expressed as an ordered InsertValues, the entry point ToInsertGetIdSQL now accepts.
func TestInsertGetIdMethodRemovesExpressions(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").
		ToInsertGetIdSQL(core.InsertValues{
			{Column: "email", Value: "foo"},
			{Column: "bar", Value: Raw("bar")},
		}, "id")

	assert.Equal(t, `insert into "users" ("email", "bar") values (?, bar)`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestInsertGetIdMapFallbackKeepsSorting documents the fallback: a caller that still passes a
// []map[string]any gets the historic sorted order — the ksort an insert performs — so existing call
// sites are unaffected by the ordered entry point.
func TestInsertGetIdMapFallbackKeepsSorting(t *testing.T) {
	sql, bindings := newTestBuilder().From("users", "").
		ToInsertGetIdSQL([]map[string]any{{"email": "foo", "bar": Raw("bar")}}, "id")

	assert.Equal(t, `insert into "users" ("bar", "email") values (bar, ?)`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestUpdateMethodWorksWithQueryAsValue ports Laravel's testUpdateMethodWorksWithQueryAsValue
// (corpus L4335-4342): an update value that is a query builder is compiled into the SET list as a
// parenthesised subquery, and the sub-select's own bindings ride with the set values — ahead of the
// outer where bindings, exactly as Grammar::prepareBindingsForUpdate places them. Laravel resolves
// the value in Builder::update() (parseSub -> Expression("(...)") plus the sub's bindings); the Go
// builder holds no connection, so the same resolution happens in ToUpdateSQL.
func TestUpdateMethodWorksWithQueryAsValue(t *testing.T) {
	sub := newTestBuilder().From("transactions", "").
		SelectRaw("sum(credits)", nil).
		WhereColumn("transactions.user_id", "=", "users.id", core.BooleanAnd).
		Where("type", "foo")

	sql, bindings := newTestBuilder().From("users", "").Where("id", 1).
		ToUpdateSQL(map[string]any{"credits": sub})

	assert.Equal(t, `update "users" set "credits" = (select sum(credits) from "transactions" where "transactions"."user_id" = "users"."id" and "type" = ?) where "id" = ?`, sql)
	assert.Equal(t, []any{"foo", 1}, bindings)
}
