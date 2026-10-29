package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
)

// This file ports the "leftover and unnamed" corpus rows: rows no blocker entry in the port plan
// named, plus rows the plan left without a missing list. Each test below is one row of
// tests/Database/DatabaseQueryBuilderTest.php (laravel/framework 12.x, commit
// 0fc645c6737626a1b1636cd234192b0d78b76569) and asserts that row's SQL verbatim, with our "?"
// placeholders and the dialect's identifier quoting.
//
// The two lateral rows are the ones this item owns: Builder::leftJoinLateral() is
// joinLateral($query, $as, 'left'), and the compilation half (JoinLateralClause plus the per-dialect
// compileJoinLateral) already landed, so only the sugar method was missing.

// TestLeftJoinLateral ports Laravel's testLeftJoinLateral (corpus L3488-3503). MySQL spells a lateral
// join "left join lateral ... on true"; a non-queryable argument is refused, which Laravel raises as
// an InvalidArgumentException and the port records as errSubqueryTypeInvalid.
func TestLeftJoinLateral(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"mysql left join lateral", NewMySqlGrammar(""), func(b *Builder) *Builder {
			sub := NewBuilder(NewMySqlGrammar("")).From("contacts", "").
				WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)

			return b.From("users", "").LeftJoinLateral(sub, "sub")
		}, "select * from `users` left join lateral (select * from `contacts` where `contracts`.`user_id` = `users`.`id`) as `sub` on true"},
	})

	builder := NewBuilder(NewBaseGrammar("")).From("users", "").LeftJoinLateral([]any{"foo"}, "sub")
	_ = builder.ToSQL()

	assert.ErrorIs(t, builder.Error(), errors.QuerySubqueryTypeInvalid)
}

// TestLeftJoinLateralSqlServer ports Laravel's testLeftJoinLateralSqlServer (corpus L3504-3512). SQL
// Server has no lateral keyword: a left lateral join is spelled "outer apply" and carries no on clause.
func TestLeftJoinLateralSqlServer(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"sqlserver left join lateral becomes outer apply", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").LeftJoinLateral(func(q *Builder) *Builder {
				return q.From("contacts", "").WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)
			}, "sub")
		}, "select * from [users] outer apply (select * from [contacts] where [contracts].[user_id] = [users].[id]) as [sub]"},
	})
}

// TestRawHavings ports Laravel's testRawHavings (corpus L2409-2424). A raw having carries no binding
// and keeps its SQL; the "or" form is the same HavingRaw descriptor with the or boolean. The case is
// also ported through the named OrHavingRaw entry point in
// laravel_port_fix_blocked_by_port_test.go; this file keeps the boolean-form route it was written with.
func TestRawHavings(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"raw having only", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").HavingRaw("user_foo < user_bar", nil, core.BooleanAnd)
		}, `select * from "users" having user_foo < user_bar`},
		{"having then raw or having", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Having("baz", "=", 1).
				HavingRaw("user_foo < user_bar", nil, core.BooleanOr)
		}, `select * from "users" having "baz" = ? or user_foo < user_bar`},
		{"having between then raw or having", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				HavingBetween("last_login_date", []any{"2018-11-16", "2018-12-16"}, core.BooleanAnd, false).
				HavingRaw("user_foo < user_bar", nil, core.BooleanOr)
		}, `select * from "users" having "last_login_date" between ? and ? or user_foo < user_bar`},
	})
}

// TestWhereRowValues ports Laravel's testWhereRowValues (corpus L6490-6505): the tuple comparison,
// its or form, and a raw value that is inlined and records no binding.
func TestWhereRowValues(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"row values", func(b *Builder) *Builder {
			return b.Select("*").From("orders", "").
				WhereRowValues([]any{"last_update", "order_number"}, "<", []any{1, 2})
		}, `select * from "orders" where ("last_update", "order_number") < (?, ?)`},
		{"or row values after a where", func(b *Builder) *Builder {
			return b.Select("*").From("orders", "").Where("company_id", 1).
				OrWhereRowValues([]any{"last_update", "order_number"}, "<", []any{1, 2})
		}, `select * from "orders" where "company_id" = ? or ("last_update", "order_number") < (?, ?)`},
	})

	builder := newTestBuilder().Select("*").From("orders", "").
		WhereRowValues([]any{"last_update", "order_number"}, "<", []any{1, Raw("2")})

	assert.Equal(t, `select * from "orders" where ("last_update", "order_number") < (?, 2)`, builder.ToSQL())
	assert.Equal(t, []any{1}, builder.GetBindings())
}

// TestWhereExists ports Laravel's testWhereExists (corpus L2988-3020). The subquery is built by the
// callback and compared through a raw column reference; the not and or forms are the same WhereExists
// descriptor with its not and boolean arguments set.
func TestWhereExists(t *testing.T) {
	sub := func(q *Builder) *Builder {
		return q.Select("*").From("products", "").Where("products.id", "=", Raw(`"orders"."id"`))
	}

	runLaravelCases(t, []laravelCase{
		{"where exists", func(b *Builder) *Builder {
			return b.Select("*").From("orders", "").WhereExists(sub, core.BooleanAnd, false)
		}, `select * from "orders" where exists (select * from "products" where "products"."id" = "orders"."id")`},
		{"where not exists", func(b *Builder) *Builder {
			return b.Select("*").From("orders", "").WhereExists(sub, core.BooleanAnd, true)
		}, `select * from "orders" where not exists (select * from "products" where "products"."id" = "orders"."id")`},
		{"or where exists", func(b *Builder) *Builder {
			return b.Select("*").From("orders", "").Where("id", "=", 1).WhereExists(sub, core.BooleanOr, false)
		}, `select * from "orders" where "id" = ? or exists (select * from "products" where "products"."id" = "orders"."id")`},
		{"or where not exists", func(b *Builder) *Builder {
			return b.Select("*").From("orders", "").Where("id", "=", 1).WhereExists(sub, core.BooleanOr, true)
		}, `select * from "orders" where "id" = ? or not exists (select * from "products" where "products"."id" = "orders"."id")`},
	})
}

// TestWhereJsonOverlapsMySql ports Laravel's testWhereJsonOverlapsMySql (corpus L6533-6550): the plain
// column, a qualified column with a JSON path, and the or form with a raw value.
func TestWhereJsonOverlapsMySql(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereJsonOverlaps("options", []any{"en", "fr"}, core.BooleanAnd, false)
	assert.Equal(t, "select * from `users` where json_overlaps(`options`, ?)", builder.ToSQL())
	assert.Equal(t, []any{`["en","fr"]`}, builder.GetBindings())

	builder = NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereJsonOverlaps("users.options->languages", []any{"en", "fr"}, core.BooleanAnd, false)
	assert.Equal(t, "select * from `users` where json_overlaps(`users`.`options`, ?, '$.\"languages\"')", builder.ToSQL())
	assert.Equal(t, []any{`["en","fr"]`}, builder.GetBindings())

	builder = NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("id", "=", 1).
		WhereJsonOverlaps("options->languages", Raw(`'["en", "fr"]'`), core.BooleanOr, false)
	assert.Equal(t, "select * from `users` where `id` = ? or json_overlaps(`options`, '[\"en\", \"fr\"]', '$.\"languages\"')", builder.ToSQL())
	assert.Equal(t, []any{1}, builder.GetBindings())
}

// TestWhereJsonDoesntOverlapMySql ports Laravel's testWhereJsonDoesntOverlapMySql (corpus L6613-6626):
// the negated form, whose "not " prefix the base whereJsonOverlaps compiler adds.
func TestWhereJsonDoesntOverlapMySql(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereJsonOverlaps("options->languages", []any{"en", "fr"}, core.BooleanAnd, true)
	assert.Equal(t, "select * from `users` where not json_overlaps(`options`, ?, '$.\"languages\"')", builder.ToSQL())
	assert.Equal(t, []any{`["en","fr"]`}, builder.GetBindings())

	builder = NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("id", "=", 1).
		WhereJsonOverlaps("options->languages", Raw(`'["en", "fr"]'`), core.BooleanOr, true)
	assert.Equal(t, "select * from `users` where `id` = ? or not json_overlaps(`options`, '[\"en\", \"fr\"]', '$.\"languages\"')", builder.ToSQL())
	assert.Equal(t, []any{1}, builder.GetBindings())
}

// TestMySqlWrappingJsonWithBoolean ports Laravel's testMySqlWrappingJsonWithBoolean (corpus
// L4920-4929): a bool against a JSON selector becomes a raw true literal, and the second half proves
// the same happens for a raw selector column, whose "->" is what the check looks for.
func TestMySqlWrappingJsonWithBoolean(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("items->available", "=", true)
	assert.Equal(t, "select * from `users` where json_extract(`items`, '$.\"available\"') = true", builder.ToSQL())

	// The corpus' second half -- where(new Raw("items->'$.available'"), '=', true) compiling to
	// `items->'$.available' = true` -- is NOT ported. Our addWhere() short-circuits any Expression
	// column into a bare whereExpression descriptor before the JSON-boolean branch is reached, so we
	// compile `items->'$.available'` with no operator and no value. Laravel takes that bare-expression
	// branch only for a ConditionExpression, not for a Query\\Expression used as a column. Recorded as a
	// finding rather than worked around here: it is a non-test change outside this item.
}

// TestSqlServerLimitsAndOffsets ports Laravel's testSqlServerLimitsAndOffsets (corpus L5048-5086). The
// corpus' final three cases pass the non-numeric strings 'foo' and 'bar'; our Limit/Offset take an int
// and cannot carry them, so they are not asserted here. Every numeric case is.
func TestSqlServerLimitsAndOffsets(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"limit alone becomes top", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Limit(10)
		}, "select top 10 * from [users]"},
		{"offset with an order", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Offset(10).OrderBy("email", "desc")
		}, "select * from [users] order by [email] desc offset 10 rows"},
		{"offset without an order gets a placeholder", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Offset(10).Limit(10)
		}, "select * from [users] order by (SELECT 0) offset 10 rows fetch next 10 rows only"},
		{"offset and limit with an order", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Offset(11).Limit(10).OrderBy("email", "desc")
		}, "select * from [users] order by [email] desc offset 11 rows fetch next 10 rows only"},
	})

	builder := NewBuilder(NewSqlServerGrammar("")).Select("*").From("users", "").Where("email", "emailBinding").
		OrderBy(func(q *Builder) *Builder {
			return q.Select("created_at").From("logins", "").
				Where("users.name", "nameBinding").
				WhereColumn("user_id", "=", "users.id", core.BooleanAnd).
				Limit(1)
		}, "asc").Offset(10).Limit(10)

	assert.Equal(t, "select * from [users] where [email] = ? order by (select top 1 [created_at] from [logins] where [users].[name] = ? and [user_id] = [users].[id]) asc offset 10 rows fetch next 10 rows only", builder.ToSQL())
	assert.Equal(t, []any{"emailBinding", "nameBinding"}, builder.GetBindings())
}

// TestUpdateFromMethodWithJoinsOnPostgres ports Laravel's testUpdateFromMethodWithJoinsOnPostgres
// (corpus L4301-4333). Laravel executes updateFrom() against its connection, so the grammar is called
// directly here: compileUpdateFrom for the SQL and prepareBindingsForUpdateFrom for the bindings, whose
// order is the set values first, then the base where, then the join constraints.
func TestUpdateFromMethodWithJoinsOnPostgres(t *testing.T) {
	values := core.UpdateValues{{Column: "email", Value: "foo"}, {Column: "name", Value: "bar"}}
	grammar := NewPostgresGrammar("")

	t.Run("a plain join condition", func(t *testing.T) {
		builder := NewBuilder(grammar).From("users", "").
			Join("orders", "users.id", "=", "orders.user_id", "inner", false).
			Where("users.id", "=", 1)

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? from "orders" where "users"."id" = ? and "users"."id" = "orders"."user_id"`,
			grammar.CompileUpdateFrom(builder, values))
		assert.Equal(t, []any{"foo", "bar", 1}, grammar.PrepareBindingsForUpdateFrom(builder.Bindings(), values))
	})

	t.Run("a join callback condition", func(t *testing.T) {
		builder := NewBuilder(grammar).From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).
					Where("users.id", "=", 1)
			}, nil, "", "inner", false)

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? from "orders" where "users"."id" = "orders"."user_id" and "users"."id" = ?`,
			grammar.CompileUpdateFrom(builder, values))
		assert.Equal(t, []any{"foo", "bar", 1}, grammar.PrepareBindingsForUpdateFrom(builder.Bindings(), values))
	})

	t.Run("a base where before the join conditions", func(t *testing.T) {
		builder := NewBuilder(grammar).From("users", "").
			Join("orders", func(join *Builder) *Builder {
				return join.WhereColumn("users.id", "=", "orders.user_id", core.BooleanAnd).
					Where("users.id", "=", 1)
			}, nil, "", "inner", false).
			Where("name", "baz")

		assert.Equal(t, `update "users" set "email" = ?, "name" = ? from "orders" where "name" = ? and "users"."id" = "orders"."user_id" and "users"."id" = ?`,
			grammar.CompileUpdateFrom(builder, values))
		assert.Equal(t, []any{"foo", "bar", "baz", 1}, grammar.PrepareBindingsForUpdateFrom(builder.Bindings(), values))
	})
}

// TestTruncateMethodWithPrefixAndSchema ports the base-grammar half of Laravel's
// testTruncateMethodWithPrefixAndSchema (corpus L4530-4544): a schema-qualified table takes the prefix
// on its table segment only, so the statement is `truncate table "my_schema"."prefix_users"`.
//
// The corpus' second half asserts SQLite's compileTruncate with a mocked connection feeding
// parseSchemaAndTable; our SQLite compileTruncate takes no connection and does not split the schema off
// the table, so that half is not expressible here (see the item notes).
func TestTruncateMethodWithPrefixAndSchema(t *testing.T) {
	statements := NewBuilder(NewBaseGrammar("prefix_")).From("my_schema.users", "").ToTruncateSQL()

	assert.Len(t, statements, 1)
	assert.Equal(t, `truncate table "my_schema"."prefix_users"`, statements[0].SQL)
	assert.Empty(t, statements[0].Bindings)
}
