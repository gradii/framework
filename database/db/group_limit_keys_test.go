package db

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// TestGetStripsGroupLimitBookkeepingColumns pins Query\Builder::withoutGroupLimitKeys()
// (illuminate/database v12.55.1, Query/Builder.php:3516): when a group limit is set, get() removes
// the rewrite's bookkeeping columns from the materialized rows before the after-query callbacks
// run — the numbering column, plus the two @laravel_group session-variable columns keyed by the
// grammar-wrapped last segment of the group-limit column. Oracle: /tmp/strip-laravel-row/p17b.php
// P17b.1 (get() rows exclude laravel_row) and P17b.7 (the double-quote key spellings removed).
func (s *QueryTestSuite) TestGetStripsGroupLimitBookkeepingColumns() {
	var rows []map[string]any

	s.mockReadBuilder.EXPECT().SelectContext(mock.Anything, &rows, mock.Anything).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*[]map[string]any)) = []map[string]any{{
				"id":                                int64(1),
				"team_id":                           int64(7),
				"laravel_row":                       int64(1),
				`@laravel_group := "team_id"`:       int64(1),
				`@laravel_group := "pivot_team_id"`: int64(1),
			}}
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(mock.Anything).Return("").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, mock.Anything, int64(1), nil).Return().Once()

	s.NoError(s.query.Clone().GroupLimit(2, "gl_posts.team_id").Get(&rows))

	s.Equal([]map[string]any{{"id": int64(1), "team_id": int64(7)}}, rows)
}

// TestFirstStripsGroupLimitBookkeepingColumns: first() is take(1)->get()->first(), so a single-map
// destination loses the bookkeeping columns as well. Oracle: /tmp/strip-laravel-row/p17b.php
// P17b.4.
func (s *QueryTestSuite) TestFirstStripsGroupLimitBookkeepingColumns() {
	row := map[string]any{}

	s.mockReadBuilder.EXPECT().GetContext(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*map[string]any)) = map[string]any{"id": int64(1), "laravel_row": int64(1)}
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(mock.Anything).Return("").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, mock.Anything, int64(1), nil).Return().Once()

	s.NoError(s.query.Clone().GroupLimit(2, "team_id").First(&row))

	s.Equal(map[string]any{"id": int64(1)}, row)
}

// TestCursorPaginateStripsGroupLimitBookkeepingColumns: cursorPaginate() routes through get() in
// Laravel, so the keyset page's map rows are stripped too. Oracle: /tmp/strip-laravel-row/p17b.php
// P17b.1 (the same get() step).
func (s *QueryTestSuite) TestCursorPaginateStripsGroupLimitBookkeepingColumns() {
	var items []map[string]any

	s.mockReadBuilder.EXPECT().SelectContext(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*[]map[string]any)) = []map[string]any{{"id": int64(1), "laravel_row": int64(1)}}
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(mock.Anything).Return("").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, mock.Anything, int64(1), nil).Return().Once()

	_, err := s.query.Clone().OrderBy("id").GroupLimit(2, "team_id").CursorPaginate(2, "", &items)
	s.NoError(err)

	s.Equal([]map[string]any{{"id": int64(1)}}, items)
}

// TestGetKeepsMapKeysWithoutAGroupLimit pins Laravel's isset($this->groupLimit) guard: the strip
// runs only for a group-limited read, so a column that happens to be named laravel_row survives an
// ordinary select.
func (s *QueryTestSuite) TestGetKeepsMapKeysWithoutAGroupLimit() {
	var rows []map[string]any

	s.mockReadBuilder.EXPECT().SelectContext(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*[]map[string]any)) = []map[string]any{{"id": int64(1), "laravel_row": int64(1)}}
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(mock.Anything).Return("").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, mock.Anything, int64(1), nil).Return().Once()

	s.NoError(s.query.Clone().Get(&rows))

	s.Equal([]map[string]any{{"id": int64(1), "laravel_row": int64(1)}}, rows)
}
