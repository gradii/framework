package db

import (
	"context"
	"testing"

	"github.com/goravel/framework/contracts/database/db"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPublicLaravelClausesMutateAndExplicitCloneIsolates(t *testing.T) {
	var query db.Query = NewQuery(context.Background(), nil, nil, querybuilder.NewMySqlGrammar(""), "mysql", nil, "users", nil)
	snapshot := query.Clone()
	subquery := query.NewQuery().From("posts").Select("user_id").Where("published", true)
	joined := query.Select("users.id").JoinSub(subquery, "p", "users.id", "=", "p.user_id").
		WhereDate("created_at", ">=", "2026-01-01").GroupBy("users.id").HavingRaw("count(*) > ?", 2)
	assert.Equal(t, "select `users`.`id` from `users` inner join (select `user_id` from `posts` where `published` = ?) as `p` on `users`.`id` = `p`.`user_id` where date(`created_at`) >= ? group by `users`.`id` having count(*) > ?", joined.ToSql().Get())
	assert.Equal(t, []any{true, "2026-01-01", 2}, joined.GetBindings())
	assert.Same(t, query, joined)
	assert.Equal(t, "select * from `users`", snapshot.ToSql().Get())
	assert.Empty(t, snapshot.GetBindings())
	query = query.NewQuery().From("users")

	joined = query.Join("posts", func(join db.JoinClause) db.JoinClause {
		return join.On("users.id", "=", "posts.user_id").Where("posts.state", "active")
	}).Where("users.active", true)
	assert.Equal(t, "select * from `users` inner join `posts` on `users`.`id` = `posts`.`user_id` and `posts`.`state` = ? where `users`.`active` = ?", joined.ToSql().Get())
	assert.Equal(t, []any{"active", true}, joined.GetBindings())
}

func TestPublicBeforeQueryAndSubqueryCallbacks(t *testing.T) {
	query := NewQuery(context.Background(), nil, nil, querybuilder.NewMySqlGrammar(""), "mysql", nil, "users", nil)
	calls := 0
	callback := query.BeforeQuery(func(q db.Query) db.Query { calls++; return q.Where("active", true) })
	assert.Equal(t, "select * from `users` where `active` = ?", callback.ToSql().Get())
	assert.Equal(t, "select * from `users` where `active` = ?", callback.ToSql().Get())
	assert.Equal(t, 1, calls)
	subquery := query.NewQuery().From("users").WhereNotExists(func(q db.Query) db.Query {
		return q.From("posts").WhereColumn("posts.user_id", "users.id").Where("published", true)
	})
	assert.Equal(t, "select * from `users` where not exists (select * from `posts` where `posts`.`user_id` = `users`.`id` and `published` = ?)", subquery.ToSql().Get())
	assert.Equal(t, []any{true}, subquery.GetBindings())
}

func (s *QueryTestSuite) TestSimplePaginateProbesWithoutCount() {
	users := []TestUser{}
	const statement = "select * from `users` order by `id` asc limit 3 offset 2"
	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, &users, statement).Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*dest.(*[]TestUser) = []TestUser{{ID: 3}, {ID: 4}, {ID: 5}}
	}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(statement).Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(3), nil).Once()
	more, err := s.query.OrderBy("id").SimplePaginate(2, 2, &users)
	s.NoError(err)
	s.True(more)
	s.Equal([]TestUser{{ID: 3}, {ID: 4}}, users)
	s.Equal(3, *s.query.GetLimit())
	s.Equal(2, *s.query.GetOffset())
}

func (s *QueryTestSuite) TestSimplePaginateEmptyAndInvalidDest() {
	var users []TestUser
	const statement = "select * from `users` limit 16 offset 0"
	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, &users, statement).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(statement).Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(0), nil).Once()
	more, err := s.query.SimplePaginate(0, 0, &users)
	s.NoError(err)
	s.False(more)
	_, err = s.query.SimplePaginate(1, 2, nil)
	s.Error(err)
	_, err = s.query.SimplePaginate(int(^uint(0)>>1), 2, &users)
	s.ErrorIs(err, errors.QueryPaginationOverflow)
}

func (s *QueryTestSuite) TestInsertUsingExecutesWriterAndCleansBindings() {
	const statement = "insert into `users` (`name`) select `name` from `archived_users` where `active` = ?"
	result := &MockResult{}
	result.On("RowsAffected").Return(int64(2), nil).Once()
	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, statement, true).Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain(statement, true).Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(2), nil).Once()
	got, err := s.query.InsertUsing([]string{"name"}, func(q db.Query) db.Query {
		return q.From("archived_users").Select("name").Where("active", true)
	})
	s.NoError(err)
	s.Equal(int64(2), got.RowsAffected)
}

func (s *QueryTestSuite) TestUpsertDefaultAndExplicitValues() {
	const statement = "insert into `users` (`email`, `name`) values (?, ?) on duplicate key update `name` = ?"
	result := &MockResult{}
	result.On("RowsAffected").Return(int64(2), nil).Once()
	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, statement, "foo", "Alice", "Bob").Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain(statement, "foo", "Alice", "Bob").Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(2), nil).Once()
	got, err := s.query.Upsert(map[string]any{"name": "Alice", "email": "foo"}, []string{"email"}, map[string]any{"name": "Bob"})
	s.NoError(err)
	s.Equal(int64(2), got.RowsAffected)
	empty, err := s.query.Upsert([]map[string]any{}, []string{"id"})
	s.NoError(err)
	s.Zero(empty.RowsAffected)
	empty, err = s.query.InsertOrIgnore(map[string]any{})
	s.NoError(err)
	s.Zero(empty.RowsAffected)
}

func (s *QueryTestSuite) TestAfterQueryTransformsRowsWithoutASecondSelect() {
	var values []TestUser
	const statement = "select * from `users`"
	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, &values, statement).Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*dest.(*[]TestUser) = []TestUser{{ID: 1}, {ID: 2}}
	}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(statement).Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(2), nil).Once()
	err := s.query.AfterQuery(func(result any) any { return result.([]TestUser)[:1] }).Get(&values)
	s.NoError(err)
	s.Equal([]TestUser{{ID: 1}}, values)
}

func (s *QueryTestSuite) TestLazyStopsBeforeFetchingTheNextChunk() {
	const statement = "select * from `users` order by `id` asc limit 2 offset 0"
	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, mock.Anything, statement).Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*dest.(*[]map[string]any) = []map[string]any{{"id": 1}, {"id": 2}}
	}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(statement).Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(2), nil).Once()
	seen := 0
	for row, err := range s.query.OrderBy("id").Lazy(2) {
		require.NoError(s.T(), err)
		require.NotNil(s.T(), row)
		seen++
		break
	}
	s.Equal(1, seen)
}
