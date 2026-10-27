package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the where-json area of Laravel's tests/Database/DatabaseQueryBuilderTest.php
// (laravel/framework 12.x, commit 0fc645c6737626a1b1636cd234192b0d78b76569). Every case rebuilds the
// corpus chain with our Builder and pins the SQL Laravel asserts for it, per dialect, because the
// JSON spelling is what differs between them. The base grammar refuses JSON selectors, so no case
// here uses it.
//
// Two notes on the port itself:
//   - Laravel's orderBy() defaults the direction to "asc". Our OrderBy takes the direction
//     explicitly, so the cases that port an orderBy() pass "asc" and assert the "asc" Laravel's
//     default already produced.
//   - ToSQL renders "?" placeholders and double-quoted identifiers for every dialect, Postgres
//     included; nothing here translates "?" to "$N".

// TestJsonWhereNullMysql ports Laravel's testJsonWhereNullMysql.
func TestJsonWhereNullMysql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"json where null", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNull("items->id", core.BooleanAnd, false)
		}, "select * from `users` where (json_extract(`items`, '$.\"id\"') is null OR json_type(json_extract(`items`, '$.\"id\"')) = 'NULL')"},
	})
}

// TestJsonWhereNotNullMysql ports Laravel's testJsonWhereNotNullMysql.
func TestJsonWhereNotNullMysql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"json where not null", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotNull("items->id", core.BooleanAnd)
		}, "select * from `users` where (json_extract(`items`, '$.\"id\"') is not null AND json_type(json_extract(`items`, '$.\"id\"')) != 'NULL')"},
	})
}

// TestMySqlWrappingJsonWithString ports Laravel's testMySqlWrappingJsonWithString, which also
// inspects the single where binding.
func TestMySqlWrappingJsonWithString(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").Where("items->sku", "=", "foo-bar")

	assert.Equal(t, "select * from `users` where json_unquote(json_extract(`items`, '$.\"sku\"')) = ?", builder.ToSQL())
	assert.Equal(t, []any{"foo-bar"}, builder.RawBindings("where"))
}

// TestMySqlWrappingJsonWithInteger ports Laravel's testMySqlWrappingJsonWithInteger.
func TestMySqlWrappingJsonWithInteger(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"integer value", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price", "=", 1)
		}, "select * from `users` where json_unquote(json_extract(`items`, '$.\"price\"')) = ?"},
	})
}

// TestMySqlWrappingJsonWithDouble ports Laravel's testMySqlWrappingJsonWithDouble.
func TestMySqlWrappingJsonWithDouble(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"double value", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price", "=", 1.5)
		}, "select * from `users` where json_unquote(json_extract(`items`, '$.\"price\"')) = ?"},
	})
}

// TestMySqlWrappingJsonWithBooleanAndIntegerThatLooksLikeOne ports Laravel's
// testMySqlWrappingJsonWithBooleanAndIntegerThatLooksLikeOne. A bool against a JSON selector becomes
// a raw true/false literal; the int 0 stays a bound value, which is the point of the case.
func TestMySqlWrappingJsonWithBooleanAndIntegerThatLooksLikeOne(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		Where("items->available", "=", true).
		Where("items->active", "=", false).
		Where("items->number_available", "=", 0)

	assert.Equal(t, "select * from `users` where json_extract(`items`, '$.\"available\"') = true and json_extract(`items`, '$.\"active\"') = false and json_unquote(json_extract(`items`, '$.\"number_available\"')) = ?", builder.ToSQL())
	assert.Equal(t, []any{0}, builder.GetBindings())
}

// TestJsonPathEscaping ports Laravel's testJsonPathEscaping. The corpus writes the same selector four
// ways in PHP double-quoted strings; PHP's escaping rules collapse two pairs onto the same bytes, and
// the path renderer drops the backslashes in either case, so all four assert one SQL string.
func TestJsonPathEscaping(t *testing.T) {
	columns := []string{
		"json->'))#",     // PHP "json->'))#"
		"json->\\'))#",   // PHP "json->\'))#"
		"json->\\'))#",   // PHP "json->\\'))#"
		"json->\\\\'))#", // PHP "json->\\\'))#"
	}

	for _, column := range columns {
		builder := NewBuilder(NewMySqlGrammar("")).Select(column)

		assert.Equal(t, "select json_unquote(json_extract(`json`, '$.\"''))#\"'))", builder.ToSQL(), "%q", column)
	}
}

// TestMySqlWrappingJson ports Laravel's testMySqlWrappingJson.
func TestMySqlWrappingJson(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"where raw", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereRaw("items->'$.\"price\"' = 1", nil, core.BooleanAnd)
		}, "select * from `users` where items->'$.\"price\"' = 1"},
		{"select, where and order by a json column", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("items->price").From("users", "").Where("users.items->price", "=", 1).OrderBy("items->price", "asc")
		}, "select json_unquote(json_extract(`items`, '$.\"price\"')) from `users` where json_unquote(json_extract(`users`.`items`, '$.\"price\"')) = ? order by json_unquote(json_extract(`items`, '$.\"price\"')) asc"},
		{"nested path", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1)
		}, "select * from `users` where json_unquote(json_extract(`items`, '$.\"price\".\"in_usd\"')) = ?"},
		{"nested path and a sibling path", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1).Where("items->age", "=", 2)
		}, "select * from `users` where json_unquote(json_extract(`items`, '$.\"price\".\"in_usd\"')) = ? and json_unquote(json_extract(`items`, '$.\"age\"')) = ?"},
	})
}

// TestPostgresWrappingJson ports Laravel's testPostgresWrappingJson. The last assertion is the
// boolean shape: the selector is cast to jsonb and the literal follows as jsonb.
func TestPostgresWrappingJson(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"select, where and order by a json column", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("items->price").From("users", "").Where("users.items->price", "=", 1).OrderBy("items->price", "asc")
		}, `select "items"->>'price' from "users" where "users"."items"->>'price' = ? order by "items"->>'price' asc`},
		{"nested path", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1)
		}, `select * from "users" where "items"->'price'->>'in_usd' = ?`},
		{"nested path and a sibling path", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1).Where("items->age", "=", 2)
		}, `select * from "users" where "items"->'price'->>'in_usd' = ? and "items"->>'age' = ?`},
		{"array index in the path", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->prices->0", "=", 1).Where("items->age", "=", 2)
		}, `select * from "users" where "items"->'prices'->>0 = ? and "items"->>'age' = ?`},
		{"boolean value", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->available", "=", true)
		}, `select * from "users" where ("items"->'available')::jsonb = 'true'::jsonb`},
	})
}

// TestSqlServerWrappingJson ports Laravel's testSqlServerWrappingJson. SQL Server reads a selector
// with json_value() and takes the boolean literal as a quoted string.
func TestSqlServerWrappingJson(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"select, where and order by a json column", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("items->price").From("users", "").Where("users.items->price", "=", 1).OrderBy("items->price", "asc")
		}, `select json_value([items], '$."price"') from [users] where json_value([users].[items], '$."price"') = ? order by json_value([items], '$."price"') asc`},
		{"nested path", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1)
		}, `select * from [users] where json_value([items], '$."price"."in_usd"') = ?`},
		{"nested path and a sibling path", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1).Where("items->age", "=", 2)
		}, `select * from [users] where json_value([items], '$."price"."in_usd"') = ? and json_value([items], '$."age"') = ?`},
		{"boolean value", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->available", "=", true)
		}, `select * from [users] where json_value([items], '$."available"') = 'true'`},
	})
}

// TestSqliteWrappingJson ports Laravel's testSqliteWrappingJson. SQLite keeps the boolean literal
// unquoted because it has no separate boolean type.
func TestSqliteWrappingJson(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"select, where and order by a json column", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("items->price").From("users", "").Where("users.items->price", "=", 1).OrderBy("items->price", "asc")
		}, `select json_extract("items", '$."price"') from "users" where json_extract("users"."items", '$."price"') = ? order by json_extract("items", '$."price"') asc`},
		{"nested path", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1)
		}, `select * from "users" where json_extract("items", '$."price"."in_usd"') = ?`},
		{"nested path and a sibling path", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->price->in_usd", "=", 1).Where("items->age", "=", 2)
		}, `select * from "users" where json_extract("items", '$."price"."in_usd"') = ? and json_extract("items", '$."age"') = ?`},
		{"boolean value", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("items->available", "=", true)
		}, `select * from "users" where json_extract("items", '$."available"') = true`},
	})
}

// TestWhereJsonContainsSqlite ports Laravel's testWhereJsonContainsSqlite, including the bindings the
// corpus inspects (`is ?` is an equality, which is SQLite's spelling of json_contains).
func TestWhereJsonContainsSqlite(t *testing.T) {
	grammar := NewSqliteGrammar("")

	builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonContains("options", "en", core.BooleanAnd, false)

	assert.Equal(t, `select * from "users" where exists (select 1 from json_each("options") where "json_each"."value" is ?)`, builder.ToSQL())
	assert.Equal(t, []any{"en"}, builder.GetBindings())

	builder = NewBuilder(grammar).Select("*").From("users", "").WhereJsonContains("users.options->language", "en", core.BooleanAnd, false)

	assert.Equal(t, `select * from "users" where exists (select 1 from json_each("users"."options", '$."language"') where "json_each"."value" is ?)`, builder.ToSQL())
	assert.Equal(t, []any{"en"}, builder.GetBindings())
}

// TestWhereJsonLengthMySql ports Laravel's testWhereJsonLengthMySql.
func TestWhereJsonLengthMySql(t *testing.T) {
	grammar := NewMySqlGrammar("")

	t.Run("length", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("options", 0)

		assert.Equal(t, "select * from `users` where json_length(`options`) = ?", builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("length of a path on a qualified column", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("users.options->languages", ">", 0)

		assert.Equal(t, "select * from `users` where json_length(`users`.`options`, '$.\"languages\"') > ?", builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("or a length against a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", Raw("0"))

		assert.Equal(t, "select * from `users` where `id` = ? or json_length(`options`, '$.\"languages\"') = 0", builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})

	t.Run("or a length above a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", ">", Raw("0"))

		assert.Equal(t, "select * from `users` where `id` = ? or json_length(`options`, '$.\"languages\"') > 0", builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})
}

// TestWhereJsonLengthPostgres ports Laravel's testWhereJsonLengthPostgres.
func TestWhereJsonLengthPostgres(t *testing.T) {
	grammar := NewPostgresGrammar("")

	t.Run("length", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("options", 0)

		assert.Equal(t, `select * from "users" where jsonb_array_length(("options")::jsonb) = ?`, builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("length of a path on a qualified column", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("users.options->languages", ">", 0)

		assert.Equal(t, `select * from "users" where jsonb_array_length(("users"."options"->'languages')::jsonb) > ?`, builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("or a length against a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", Raw("0"))

		assert.Equal(t, `select * from "users" where "id" = ? or jsonb_array_length(("options"->'languages')::jsonb) = 0`, builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})

	t.Run("or a length above a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", ">", Raw("0"))

		assert.Equal(t, `select * from "users" where "id" = ? or jsonb_array_length(("options"->'languages')::jsonb) > 0`, builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})
}

// TestWhereJsonLengthSqlite ports Laravel's testWhereJsonLengthSqlite.
func TestWhereJsonLengthSqlite(t *testing.T) {
	grammar := NewSqliteGrammar("")

	t.Run("length", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("options", 0)

		assert.Equal(t, `select * from "users" where json_array_length("options") = ?`, builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("length of a path on a qualified column", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("users.options->languages", ">", 0)

		assert.Equal(t, `select * from "users" where json_array_length("users"."options", '$."languages"') > ?`, builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("or a length against a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", Raw("0"))

		assert.Equal(t, `select * from "users" where "id" = ? or json_array_length("options", '$."languages"') = 0`, builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})

	t.Run("or a length above a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", ">", Raw("0"))

		assert.Equal(t, `select * from "users" where "id" = ? or json_array_length("options", '$."languages"') > 0`, builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})
}

// TestWhereJsonLengthSqlServer ports Laravel's testWhereJsonLengthSqlServer.
func TestWhereJsonLengthSqlServer(t *testing.T) {
	grammar := NewSqlServerGrammar("")

	t.Run("length", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("options", 0)

		assert.Equal(t, `select * from [users] where (select count(*) from openjson([options])) = ?`, builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("length of a path on a qualified column", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").WhereJsonLength("users.options->languages", ">", 0)

		assert.Equal(t, `select * from [users] where (select count(*) from openjson([users].[options], '$."languages"')) > ?`, builder.ToSQL())
		assert.Equal(t, []any{0}, builder.GetBindings())
	})

	t.Run("or a length against a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", Raw("0"))

		assert.Equal(t, `select * from [users] where [id] = ? or (select count(*) from openjson([options], '$."languages"')) = 0`, builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})

	t.Run("or a length above a raw value", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").Where("id", "=", 1).OrWhereJsonLength("options->languages", ">", Raw("0"))

		assert.Equal(t, `select * from [users] where [id] = ? or (select count(*) from openjson([options], '$."languages"')) > 0`, builder.ToSQL())
		assert.Equal(t, []any{1}, builder.GetBindings())
	})
}
