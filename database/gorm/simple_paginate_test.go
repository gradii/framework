package gorm

// SimplePaginate runs one probe read (limit+1) and never counts. The statements and boundaries below
// mirror the executed oracle probe .claude/laravel-oracle/_simplepaginate_probe.php (illuminate/database
// v12.55.1, real sqlite): page 1 of 3 rows at perPage 2 -> `select * from "users" order by "id" asc limit
// 3 offset 0` as the only statement, items trimmed to 2, hasMorePages true; page 2 -> `... limit 3 offset
// 2`, one item, false; an exact page of 3 rows at perPage 3 -> `... limit 4 offset 0`, three items, false;
// a page beyond range -> `... limit 3 offset 16`, zero items, false; perPage null and 0 both fall back to
// 15 (`... limit 16 offset 0`); paginate(2) by contrast runs its count as a first statement.

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

// TestSimplePaginateFirstPageProbesWithoutCount pins the first page: the limit+1 probe read, the trim of
// the probe row, hasMore true — and that the probe read is the query's ONLY statement, which is the
// no-count guarantee (paginate() runs two).
func TestSimplePaginateFirstPageProbesWithoutCount(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)
	query = query.OrderBy("id").(*OrmBuilder)
	recorder.returnRows(`select * from "users"`, []string{"id", "name"},
		[]driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"}, []driver.Value{int64(3), "c"})

	var users []getColumnsUser

	more, err := query.SimplePaginate(1, 2, &users)
	require.NoError(t, err)

	assert.True(t, more)
	assert.Equal(t, []getColumnsUser{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}, users)
	assert.Equal(t, `select * from "users" order by "id" asc limit 3 offset 0`, recorder.last(t).sql)
	assert.Empty(t, recorder.last(t).args, "the probe read binds nothing")
	assert.Equal(t, 1, recorder.count(), "simplePaginate runs no count statement")
}

// TestSimplePaginateBoundaries pins the four boundary rows the probe exercised: a last partial page, an
// exact page (no probe row exists), a page beyond the table, and an empty result set.
func TestSimplePaginateBoundaries(t *testing.T) {
	t.Run("last partial page", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		query = query.OrderBy("id").(*OrmBuilder)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"}, []driver.Value{int64(3), "c"})

		var users []getColumnsUser

		more, err := query.SimplePaginate(2, 2, &users)
		require.NoError(t, err)

		assert.False(t, more)
		assert.Equal(t, []getColumnsUser{{ID: 3, Name: "c"}}, users)
		assert.Equal(t, `select * from "users" order by "id" asc limit 3 offset 2`, recorder.last(t).sql)
		assert.Equal(t, 1, recorder.count())
	})

	t.Run("exactly one page", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		query = query.OrderBy("id").(*OrmBuilder)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"},
			[]driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})

		var users []getColumnsUser

		more, err := query.SimplePaginate(1, 2, &users)
		require.NoError(t, err)

		assert.False(t, more, "an exact page has no probe row to trim")
		assert.Len(t, users, 2)
		// The probe read is still limit+1; the extra row simply does not exist.
		assert.Equal(t, `select * from "users" order by "id" asc limit 3 offset 0`, recorder.last(t).sql)
	})

	t.Run("page beyond range", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		query = query.OrderBy("id").(*OrmBuilder)

		var users []getColumnsUser

		more, err := query.SimplePaginate(9, 2, &users)
		require.NoError(t, err)

		assert.False(t, more)
		assert.Empty(t, users)
		assert.Equal(t, `select * from "users" order by "id" asc limit 3 offset 16`, recorder.last(t).sql)
		assert.Equal(t, 1, recorder.count())
	})

	t.Run("empty result set", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		query = query.OrderBy("id").(*OrmBuilder)

		var users []getColumnsUser

		more, err := query.SimplePaginate(1, 2, &users)
		require.NoError(t, err)

		assert.False(t, more)
		assert.Empty(t, users)
		assert.Equal(t, `select * from "users" order by "id" asc limit 3 offset 0`, recorder.last(t).sql)
	})
}

// TestSimplePaginateDefaultsMatchPaginate pins the Paginate conventions on the simple page: page <= 0
// becomes 1, limit <= 0 becomes 15, and the probe read renders as limit 16 offset 0 — the shape Eloquent
// produces for perPage null and 0 (both fall back to Model::$perPage = 15 in the oracle probe).
func TestSimplePaginateDefaultsMatchPaginate(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)
	query = query.OrderBy("id").(*OrmBuilder)

	var users []getColumnsUser

	more, err := query.SimplePaginate(0, 0, &users)
	require.NoError(t, err)

	assert.False(t, more)
	assert.Empty(t, users)
	assert.Equal(t, `select * from "users" order by "id" asc limit 16 offset 0`, recorder.last(t).sql)
	assert.Equal(t, 1, recorder.count(), "the defaulted call still runs no count statement")
}

// TestSimplePaginateOverflowAndInvalidDest pins the guard rails: the offset arithmetic is refused before
// it overflows, and dest must be a pointer to a slice — the trim needs one, so a nil or scalar
// destination is refused with the same error the database/db SimplePaginate reports.
func TestSimplePaginateOverflowAndInvalidDest(t *testing.T) {
	query, _ := newGetColumnsQuery(t)

	var users []getColumnsUser

	_, err := query.SimplePaginate(int(^uint(0)>>1), 2, &users)
	assert.ErrorIs(t, err, errors.QueryPaginationOverflow)

	_, err = query.SimplePaginate(1, 2, nil)
	assert.ErrorIs(t, err, errors.DatabaseUnsupportedType)

	var user getColumnsUser
	_, err = query.SimplePaginate(1, 2, &user)
	assert.ErrorIs(t, err, errors.DatabaseUnsupportedType)
}
