package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// TestSqliteWhereJsonContainsList pins the SQLite list form of whereJsonContains. Laravel's SQLite
// grammar compiles one json_each predicate with a single "is ?" parameter, which cannot bind a list:
// the binding path flattens a list value into one binding per element, so the statement would carry
// several arguments against the one placeholder and fail at execution with
// "expected 1 arguments, got 2". The list is expanded into one predicate per element, ANDed together,
// each taking its own binding - the shape the pre-port Sqlite grammar produced. A scalar value
// compiles exactly as the base grammar does.
//
// The negated form is the negation of the whole conjunction ("does not contain all"), matching the
// single "not json_contains(column, list)" MySQL renders and the "not column @> list" Postgres
// renders, so all three dialects agree for the same call. The oracle confirms MySQL/Postgres compile
// the list to one predicate; SQLite cannot bind it, so the expansion must preserve their meaning.
func TestSqliteWhereJsonContainsList(t *testing.T) {
	grammar := NewSqliteGrammar("")

	t.Run("list expands to one predicate per element", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("json_data", "").
			WhereJsonContains("data->array", []string{"abc", "def"}, core.BooleanAnd, false)

		assert.Equal(t, `select * from "json_data" where exists (select 1 from json_each("data", '$."array"') where "json_each"."value" is ?) and exists (select 1 from json_each("data", '$."array"') where "json_each"."value" is ?)`, builder.ToSQL())
		assert.Equal(t, []any{"abc", "def"}, builder.GetBindings())
	})

	t.Run("does not contain negates the whole conjunction", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("json_data", "").
			WhereJsonDoesntContain("data->array", []any{"abc", "def"}, core.BooleanAnd)

		assert.Equal(t, `select * from "json_data" where not (exists (select 1 from json_each("data", '$."array"') where "json_each"."value" is ?) and exists (select 1 from json_each("data", '$."array"') where "json_each"."value" is ?))`, builder.ToSQL())
		assert.Equal(t, []any{"abc", "def"}, builder.GetBindings())
	})

	t.Run("scalar keeps the base shape", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("users", "").
			WhereJsonContains("options", "en", core.BooleanAnd, false)

		assert.Equal(t, `select * from "users" where exists (select 1 from json_each("options") where "json_each"."value" is ?)`, builder.ToSQL())
		assert.Equal(t, []any{"en"}, builder.GetBindings())
	})

	t.Run("empty list matches nothing", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("json_data", "").
			WhereJsonContains("data->array", []string{}, core.BooleanAnd, false)

		assert.Equal(t, `select * from "json_data" where 0 = 1`, builder.ToSQL())
		assert.Empty(t, builder.GetBindings())
	})

	t.Run("empty list does not contain matches everything", func(t *testing.T) {
		builder := NewBuilder(grammar).Select("*").From("json_data", "").
			WhereJsonDoesntContain("data->array", []string{}, core.BooleanAnd)

		assert.Equal(t, `select * from "json_data" where 1 = 1`, builder.ToSQL())
		assert.Empty(t, builder.GetBindings())
	})
}
