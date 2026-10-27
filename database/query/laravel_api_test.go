package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
)

func TestLaravelAfterQueryKeepsFalseLikeResultsAndCallbacks(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar(""))
	calls := 0
	builder.AfterQuery(func(value any) any { calls++; return false }).
		AfterQuery(func(value any) any { return 0 }).
		AfterQuery(func(value any) any { return "0" }).
		AfterQuery(func(value any) any { return []any{} }).
		AfterQuery(func(value any) any { return value.(int) + 1 })
	assert.Equal(t, 3, builder.ApplyAfterQueryCallbacks(2))
	clone := builder.Clone().AfterQuery(func(value any) any { return value.(int) * 2 })
	assert.Equal(t, 6, clone.ApplyAfterQueryCallbacks(2))
	assert.Equal(t, 3, builder.ApplyAfterQueryCallbacks(2))
	assert.Equal(t, 3, calls)
}

func TestLaravelSelectAliasesAndArrays(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).From("users", "")
	subquery := builder.NewQuery().From("posts", "").Select("name").Where("published", true)
	builder.AddSelect(map[string]any{"latest": subquery})
	assert.Equal(t, "select `users`.*, (select `name` from `posts` where `published` = ?) as `latest` from `users`", builder.ToSQL())
	assert.Equal(t, []any{true}, builder.GetBindings())
	builder.Select([]string{"id", "name"})
	assert.Equal(t, "select `id`, `name` from `users`", builder.ToSQL())
	assert.Empty(t, builder.GetBindings())
	builder.Select(map[string]any{"latest": subquery})
	assert.Equal(t, "select (select `name` from `posts` where `published` = ?) as `latest` from `users`", builder.ToSQL())
}

func TestLaravelDecrementEachValidationAndExtra(t *testing.T) {
	builder := NewBuilder(NewMySqlGrammar("")).From("users", "").Where("id", 7)
	builder.DecrementEach(core.UpdateValues{{Column: "votes", Value: "1.5"}, {Column: "balance", Value: 2}}, core.UpdateValue{Column: "balance", Value: 10})
	sql, bindings := builder.ToUpdateSQL(builder.UpdateValues())
	assert.Equal(t, "update `users` set `votes` = `votes` - 1.5, `balance` = ? where `id` = ?", sql)
	assert.Equal(t, []any{10, 7}, bindings)
	invalid := builder.NewQuery().DecrementEach([]any{"not numeric"})
	assert.Equal(t, errors.QueryDecrementAmountNonNumeric.Args(0), invalid.Error())
	invalid = builder.NewQuery().DecrementEach([]any{1})
	assert.Equal(t, errors.QueryDecrementEachNonAssociative, invalid.Error())
}
