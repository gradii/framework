package db

import (
	"context"
	querybuilder "github.com/gradii/framework/database/query"
	"testing"

	"github.com/gradii/framework/contracts/database/db"
	"github.com/stretchr/testify/assert"
)

func TestLaravelFluentClausesMutateTheSameBuilder(t *testing.T) {
	query := newDialectTestQuery(context.Background(), nil, nil, querybuilder.NewBaseGrammar(""), "mysql", nil, "users", nil)
	assert.Same(t, query, query.Where("active", true))
	assert.Same(t, query, query.OrWhereColumn("first_name", "=", "last_name"))
	assert.Same(t, query, query.OrderBy("id"))
	assert.Same(t, query, query.Limit(5))
	assert.Equal(t, "select * from \"users\" where \"active\" = ? or \"first_name\" = \"last_name\" order by \"id\" asc limit 5", query.ToSql().Get())
	assert.Equal(t, []any{true}, query.GetBindings())

	clone := query.Clone()
	assert.NotSame(t, query, clone)
	clone.Where("tenant_id", 7)
	assert.Equal(t, []any{true}, query.GetBindings())
	assert.Equal(t, []any{true, 7}, clone.GetBindings())
	assert.Same(t, query, query.Select("id"))
	assert.Equal(t, []string{"id"}, query.GetColumns())
}

func TestLaravelNestedCallbacksKeepTheProvidedBuilder(t *testing.T) {
	query := newDialectTestQuery(context.Background(), nil, nil, querybuilder.NewBaseGrammar(""), "mysql", nil, "users", nil)
	other := query.NewQuery().From("ignored").Where("wrong", 9)
	query.WhereNested(func(child db.Query) db.Query {
		child.Where("active", true)
		return other
	})
	assert.Equal(t, "select * from \"users\" where (\"active\" = ?)", query.ToSql().Get())
	assert.Equal(t, []any{true}, query.GetBindings())
	query.Join("posts", func(join db.JoinClause) db.JoinClause {
		assert.Same(t, join, join.On("posts.user_id", "users.id"))
		assert.Same(t, join, join.Where("posts.visible", true))
		return join
	})
	assert.Equal(t, []any{true, true}, query.GetBindings())
}

func TestLaravelNewQueryStartsWithFreshClauses(t *testing.T) {
	query := newDialectTestQuery(context.Background(), nil, nil, querybuilder.NewBaseGrammar(""), "mysql", nil, "users", nil)
	query.Where("active", true).Limit(3).UseWritePdo()
	fresh := query.NewQuery().From("posts")
	assert.NotSame(t, query, fresh)
	assert.Empty(t, fresh.GetBindings())
	assert.Nil(t, fresh.GetLimit())
	assert.Equal(t, "select * from \"posts\"", fresh.ToSql().Get())
	assert.Equal(t, []any{true}, query.GetBindings())
}
