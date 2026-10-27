package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

// These tests pin Builder.CursorPaginate against Laravel's own union cursor-pagination corpus tests
// (tests/Database/DatabaseQueryBuilderTest.php, methods testCursorPaginateWithUnion*). Each case
// rebuilds the corpus chain, calls CursorPaginate, and asserts the compiled SQL and the exact "where"
// / "union" binding split the corpus asserts. The split is the load-bearing part: the root builder's
// own predicate values land in "where"; every union branch contributes its pre-existing values
// followed by its cursor values, once, to "union".

const cursorTs = "2023-01-01 00:00:00"

// unionCursorRoot builds the corpus root builder for the union cases: videos aliased start_time as
// created_at, tagged 'video'.
func unionCursorRoot() *Builder {
	return newTestBuilder().
		Select("id", "start_time as created_at").
		SelectRaw("'video' as type", nil).
		From("videos", "")
}

// unionCursorNews builds the first union branch: news with a plain created_at, tagged 'news'.
func unionCursorNews() *Builder {
	return newTestBuilder().
		Select("id", "created_at").
		SelectRaw("'news' as type", nil).
		From("news", "")
}

// TestCursorPaginateWithUnionWheres ports testCursorPaginateWithUnionWheres (6138): a single ordered
// column, one union branch. The root compares its own original column (start_time) while the branch
// compares created_at; the cursor parameter name is created_at for both.
func TestCursorPaginateWithUnionWheres(t *testing.T) {
	b := unionCursorRoot()
	b.Union(unionCursorNews(), false)
	b.OrderBy("created_at", "asc")

	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true)

	page, err := b.CursorPaginate(16, &cursor)
	require.NoError(t, err)
	require.Equal(t, []string{"created_at"}, page.Parameters)

	assert.Equal(t, `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" > ?)) union (select "id", "created_at", 'news' as type from "news" where ("created_at" > ?)) order by "created_at" asc limit 17`, b.ToSQL())
	assert.Equal(t, []any{cursorTs}, b.RawBindings("where"))
	assert.Equal(t, []any{cursorTs}, b.RawBindings("union"))
}

// TestCursorPaginateWithMultipleUnionsAndMultipleWheres ports
// testCursorPaginateWithMultipleUnionsAndMultipleWheres (6185): two branches, each with a pre-existing
// where. The pre-existing branch value is replayed once, ahead of that branch's cursor values.
func TestCursorPaginateWithMultipleUnionsAndMultipleWheres(t *testing.T) {
	b := unionCursorRoot()

	news := unionCursorNews().Where("extra", "first")
	podcasts := newTestBuilder().
		Select("id", "created_at").
		SelectRaw("'podcast' as type", nil).
		From("podcasts", "").
		Where("extra", "second")

	b.Union(news, false)
	b.Union(podcasts, false)
	b.OrderBy("created_at", "asc")

	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true)

	_, err := b.CursorPaginate(16, &cursor)
	require.NoError(t, err)

	assert.Equal(t, `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" > ?)) union (select "id", "created_at", 'news' as type from "news" where "extra" = ? and ("created_at" > ?)) union (select "id", "created_at", 'podcast' as type from "podcasts" where "extra" = ? and ("created_at" > ?)) order by "created_at" asc limit 17`, b.ToSQL())
	assert.Equal(t, []any{cursorTs}, b.RawBindings("where"))
	assert.Equal(t, []any{"first", cursorTs, "second", cursorTs}, b.RawBindings("union"))
}

// TestCursorPaginateWithUnionMultipleWheresMultipleOrders ports
// testCursorPaginateWithUnionMultipleWheresMultipleOrders (6234): three ordered columns and two
// branches, each with a pre-existing where. It also pins the alias-resolution quirk: every branch's
// FIRST order column resolves against the branch's own select list, but the deeper columns resolve
// against the ROOT's — so the branches compare "start_time" for created_at even though their own
// select list does not alias it (Laravel's recursion is bound to the root at every level below the
// first).
func TestCursorPaginateWithUnionMultipleWheresMultipleOrders(t *testing.T) {
	b := newTestBuilder().
		Select("id", "start_time as created_at", "type").
		From("videos", "").
		Where("extra", "first")

	news := newTestBuilder().
		Select("id", "created_at", "type").
		From("news", "").
		Where("extra", "second")
	podcasts := newTestBuilder().
		Select("id", "created_at", "type").
		From("podcasts", "").
		Where("extra", "third")

	b.Union(news, false)
	b.Union(podcasts, false)
	b.OrderBy("id", "asc")
	b.OrderByDesc("created_at")
	b.OrderBy("type", "asc")

	cursor := NewCursor(CursorParameters{
		{Column: "id", Value: 1},
		{Column: "created_at", Value: cursorTs},
		{Column: "type", Value: "news"},
	}, true)

	page, err := b.CursorPaginate(16, &cursor)
	require.NoError(t, err)
	require.Equal(t, []string{"id", "created_at", "type"}, page.Parameters)

	assert.Equal(t, `(select "id", "start_time" as "created_at", "type" from "videos" where "extra" = ? and ("id" > ? or ("id" = ? and ("start_time" < ? or ("start_time" = ? and ("type" > ?)))))) union (select "id", "created_at", "type" from "news" where "extra" = ? and ("id" > ? or ("id" = ? and ("start_time" < ? or ("start_time" = ? and ("type" > ?)))))) union (select "id", "created_at", "type" from "podcasts" where "extra" = ? and ("id" > ? or ("id" = ? and ("start_time" < ? or ("start_time" = ? and ("type" > ?)))))) order by "id" asc, "created_at" desc, "type" asc limit 17`, b.ToSQL())
	assert.Equal(t, []any{"first", 1, 1, cursorTs, cursorTs, "news"}, b.RawBindings("where"))
	assert.Equal(t, []any{"second", 1, 1, cursorTs, cursorTs, "news", "third", 1, 1, cursorTs, cursorTs, "news"}, b.RawBindings("union"))
}

// TestCursorPaginateWithUnionWheresWithRawOrderExpression ports
// testCursorPaginateWithUnionWheresWithRawOrderExpression (6284): a raw ORDER BY fragment takes part in
// ORDER BY but contributes no comparison and no parameter, so the cursor compares only the following
// real column. Each branch also keeps its own pre-existing where.
func TestCursorPaginateWithUnionWheresWithRawOrderExpression(t *testing.T) {
	b := newTestBuilder().
		Select("id", "is_published", "start_time as created_at").
		SelectRaw("'video' as type", nil).
		Where("is_published", true).
		From("videos", "")

	news := newTestBuilder().
		Select("id", "is_published", "created_at").
		SelectRaw("'news' as type", nil).
		Where("is_published", true).
		From("news", "")

	b.Union(news, false)
	b.OrderByRaw(`case when (id = 3 and type="news" then 0 else 1 end)`, nil)
	b.OrderBy("created_at", "asc")

	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true)

	page, err := b.CursorPaginate(16, &cursor)
	require.NoError(t, err)
	require.Equal(t, []string{"created_at"}, page.Parameters)

	assert.Equal(t, `(select "id", "is_published", "start_time" as "created_at", 'video' as type from "videos" where "is_published" = ? and ("start_time" > ?)) union (select "id", "is_published", "created_at", 'news' as type from "news" where "is_published" = ? and ("created_at" > ?)) order by case when (id = 3 and type="news" then 0 else 1 end), "created_at" asc limit 17`, b.ToSQL())
	assert.Equal(t, []any{true, cursorTs}, b.RawBindings("where"))
	assert.Equal(t, []any{true, cursorTs}, b.RawBindings("union"))
}

// TestCursorPaginateWithUnionWheresReverseOrder ports testCursorPaginateWithUnionWheresReverseOrder
// (6331): a previous-page cursor reverses every ORDER BY direction, which flips the comparison operator
// from ">" to "<" while the ORDER BY itself descends.
func TestCursorPaginateWithUnionWheresReverseOrder(t *testing.T) {
	b := unionCursorRoot()
	b.Union(unionCursorNews(), false)
	b.OrderBy("created_at", "asc")

	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, false)

	_, err := b.CursorPaginate(16, &cursor)
	require.NoError(t, err)

	assert.Equal(t, `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" < ?)) union (select "id", "created_at", 'news' as type from "news" where ("created_at" < ?)) order by "created_at" desc limit 17`, b.ToSQL())
	assert.Equal(t, []any{cursorTs}, b.RawBindings("where"))
	assert.Equal(t, []any{cursorTs}, b.RawBindings("union"))
}

// TestCursorPaginateWithUnionWheresMultipleOrders ports testCursorPaginateWithUnionWheresMultipleOrders
// (6378): two ordered columns with mixed directions; each column's operator follows its own direction
// and the equality between columns is always "=".
func TestCursorPaginateWithUnionWheresMultipleOrders(t *testing.T) {
	b := unionCursorRoot()
	b.Union(unionCursorNews(), false)
	b.OrderByDesc("created_at")
	b.OrderBy("id", "asc")

	cursor := NewCursor(CursorParameters{
		{Column: "created_at", Value: cursorTs},
		{Column: "id", Value: 1},
	}, true)

	page, err := b.CursorPaginate(16, &cursor)
	require.NoError(t, err)
	require.Equal(t, []string{"created_at", "id"}, page.Parameters)

	assert.Equal(t, `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" < ? or ("start_time" = ? and ("id" > ?)))) union (select "id", "created_at", 'news' as type from "news" where ("created_at" < ? or ("created_at" = ? and ("id" > ?)))) order by "created_at" desc, "id" asc limit 17`, b.ToSQL())
	assert.Equal(t, []any{cursorTs, cursorTs, 1}, b.RawBindings("where"))
	assert.Equal(t, []any{cursorTs, cursorTs, 1}, b.RawBindings("union"))
}

// TestCursorPaginateWithUnionWheresAndAliassedOrderColumns ports
// testCursorPaginateWithUnionWheresAndAliassedOrderColumns (6425): two branches alias the ordered
// column differently (start_time, created_at, init_at); a single order column means each branch
// resolves its own original column.
func TestCursorPaginateWithUnionWheresAndAliassedOrderColumns(t *testing.T) {
	b := unionCursorRoot()

	news := newTestBuilder().
		Select("id", "created_at").
		SelectRaw("'news' as type", nil).
		From("news", "")
	podcasts := newTestBuilder().
		Select("id", "init_at as created_at").
		SelectRaw("'podcast' as type", nil).
		From("podcasts", "")

	b.Union(news, false)
	b.Union(podcasts, false)
	b.OrderBy("created_at", "asc")

	cursor := NewCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true)

	_, err := b.CursorPaginate(16, &cursor)
	require.NoError(t, err)

	assert.Equal(t, `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" > ?)) union (select "id", "created_at", 'news' as type from "news" where ("created_at" > ?)) union (select "id", "init_at" as "created_at", 'podcast' as type from "podcasts" where ("init_at" > ?)) order by "created_at" asc limit 17`, b.ToSQL())
	assert.Equal(t, []any{cursorTs}, b.RawBindings("where"))
	assert.Equal(t, []any{cursorTs, cursorTs}, b.RawBindings("union"))
}

// TestCursorPaginateWithoutOrder pins that an unordered query is refused, mirroring enforceOrderBy:
// cursor pagination is meaningless without an order, and no implicit order is invented. This holds for
// the first page too, because the order requirement does not depend on the cursor.
func TestCursorPaginateWithoutOrder(t *testing.T) {
	b := newTestBuilder().From("foobar", "")

	_, err := b.CursorPaginate(16, nil)
	assert.ErrorIs(t, err, errors.QueryOrderRequired)

	cursor := NewCursor(CursorParameters{{Column: "test", Value: "bar"}}, true)
	_, err = b.CursorPaginate(16, &cursor)
	assert.ErrorIs(t, err, errors.QueryOrderRequired)
}

// TestCursorPaginateFirstPage pins the nil-cursor path: no predicate and no reversal, but the
// perPage+1 probe limit still applies (16 -> 17).
func TestCursorPaginateFirstPage(t *testing.T) {
	b := newTestBuilder().From("foobar", "").OrderBy("test", "asc")

	page, err := b.CursorPaginate(16, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"test"}, page.Parameters)
	require.False(t, page.HasCursor)

	assert.Equal(t, `select * from "foobar" order by "test" asc limit 17`, b.ToSQL())
	assert.Empty(t, b.RawBindings("where"))
}
