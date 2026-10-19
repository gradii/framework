package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file ports group (a) of the cursor-pagination acceptance target in
// .claude/laravel-oracle/cursor_pagination_brief.md (section 6): the 16 corpus methods of
// tests/Database/DatabaseQueryBuilderTest.php (laravel/framework 12.x) whose SQL and bindings the brief
// pins with line numbers. Each case rebuilds the corpus chain, runs Builder.CursorPaginate, and asserts
// the compiled SQL plus the exact "where" and "union" binding lists the corpus asserts: 15 SQL, 15
// where-binding and 7 union-binding assertions across the 16 methods. The corpus is authority here — a
// case that disagrees is an implementation bug, not an expectation to loosen.
//
// Group (b) of the same table is deliberately NOT ported: the whole-paginator
// assertEquals(new CursorPaginator(...), $result) calls compare a CursorPaginator against another built
// by the same constructor from the same inputs inside the mocked get(), so every derived field
// (hasMore, the perPage slice, the backwards reversal, the minted cursors and their URLs) is
// tautological and verifies nothing. Only the constructor arguments carry information, and the parts
// that port (perPage, cursor parameters and direction, and parameters = the ORDER BY column list) are
// covered by the package's own cursor and paginator tests. Group (c) is scaffolding for the same reason
// the critique gives: "select *" is a mocked-get artifact, the bindings['where']/bindings['union']
// keying is PHP storage, and Mockery, the static resolvers and Container::makeWith never reach SQL.
//
// Following the package's ported-test conventions the cases run over newTestBuilder() (the base
// grammar: double-quoted identifiers, "?" bindings), which reproduces Laravel's getBuilder() grammar.

// cursorLaravelCase is one ported cursor-pagination case: the perPage and cursor the corpus builds, the
// chain it feeds them to, and the SQL and binding lists it asserts. wantUnion is asserted only when set
// (the seven union methods); the rest leave it nil.
type cursorLaravelCase struct {
	perPage   int
	cursor    *Cursor
	build     func(*Builder) *Builder
	wantSQL   string
	wantWhere []any
	wantUnion []any
}

// runCursorLaravelCase builds the case chain over newTestBuilder, runs CursorPaginate, and asserts the
// SQL and the where (and, when present, union) bindings.
func runCursorLaravelCase(t *testing.T, c cursorLaravelCase) {
	t.Helper()

	b := c.build(newTestBuilder())

	_, err := b.CursorPaginate(c.perPage, c.cursor)
	require.NoError(t, err)

	assert.Equal(t, c.wantSQL, b.ToSQL())
	assert.Equal(t, c.wantWhere, b.RawBindings("where"))
	if c.wantUnion != nil {
		assert.Equal(t, c.wantUnion, b.RawBindings("union"))
	}
}

// TestCursorPaginate ports testCursorPaginate (corpus 5792; SQL 5811, where 5812): one ordered column,
// one cursor value. The comparison is ">" for an ascending column and the limit is perPage+1 (17).
func TestLaravelPortCursorPaginate(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor:  newTestCursor(CursorParameters{{Column: "test", Value: "bar"}}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").OrderBy("test", "asc")
		},
		wantSQL:   `select * from "foobar" where ("test" > ?) order by "test" asc limit 17`,
		wantWhere: []any{"bar"},
	})
}

// TestCursorPaginateMultipleOrderColumns ports testCursorPaginateMultipleOrderColumns (corpus 5830; SQL
// 5849, where 5851): two ascending columns produce the nested lexicographic group, and the first value
// is bound twice because it appears in both the comparison and the equality.
func TestLaravelPortCursorPaginateMultipleOrderColumns(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor: newTestCursor(CursorParameters{
			{Column: "test", Value: "bar"},
			{Column: "another", Value: "foo"},
		}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").OrderBy("test", "asc").OrderBy("another", "asc")
		},
		wantSQL:   `select * from "foobar" where ("test" > ? or ("test" = ? and ("another" > ?))) order by "test" asc, "another" asc limit 17`,
		wantWhere: []any{"bar", "bar", "foo"},
	})
}

// TestCursorPaginateWithDefaultArguments ports testCursorPaginateWithDefaultArguments (corpus 5869; SQL
// 5887, where 5888): the corpus takes the default perPage of 15, so the probe limit is 16. (The default
// cursorName "cursor" is asserted only by group (b) and is carried by the paginator, not the builder.)
func TestLaravelPortCursorPaginateWithDefaultArguments(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 15,
		cursor:  newTestCursor(CursorParameters{{Column: "test", Value: "bar"}}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").OrderBy("test", "asc")
		},
		wantSQL:   `select * from "foobar" where ("test" > ?) order by "test" asc limit 16`,
		wantWhere: []any{"bar"},
	})
}

// TestCursorPaginateWhenNoResults ports testCursorPaginateWhenNoResults (corpus 5910): the corpus runs
// with no cursor (its resolver returns null) and asserts no SQL — the mocked get() returns an empty
// collection. It is the first-page path: no predicate and no reversal, but the order requirement and
// the perPage+1 probe limit still apply.
func TestLaravelPortCursorPaginateWhenNoResults(t *testing.T) {
	b := newTestBuilder().From("foobar", "").OrderBy("test", "asc")

	page, err := b.CursorPaginate(15, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"test"}, page.Parameters)
	assert.False(t, page.HasCursor)
	assert.Empty(t, page.Items)
}

// TestCursorPaginateWithSpecificColumns ports testCursorPaginateWithSpecificColumns (corpus 5938; SQL
// 5957, where 5958): the $columns argument never reaches SQL in the corpus (the mocked get() does not
// apply it), so the select stays "*"; the integer cursor value binds through unchanged as 2.
func TestLaravelPortCursorPaginateWithSpecificColumns(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor:  newTestCursor(CursorParameters{{Column: "id", Value: 2}}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").OrderBy("id", "asc")
		},
		wantSQL:   `select * from "foobar" where ("id" > ?) order by "id" asc limit 17`,
		wantWhere: []any{2},
	})
}

// TestCursorPaginateWithMixedOrders ports testCursorPaginateWithMixedOrders (corpus 5976; SQL 5996,
// where 5997): each column's operator follows its own direction — asc is ">", desc is "<" — while the
// equality between columns is always "=".
func TestLaravelPortCursorPaginateWithMixedOrders(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor: newTestCursor(CursorParameters{
			{Column: "foo", Value: 1},
			{Column: "bar", Value: 2},
			{Column: "baz", Value: 3},
		}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").OrderBy("foo", "asc").OrderByDesc("bar").OrderBy("baz", "asc")
		},
		wantSQL:   `select * from "foobar" where ("foo" > ? or ("foo" = ? and ("bar" < ? or ("bar" = ? and ("baz" > ?))))) order by "foo" asc, "bar" desc, "baz" asc limit 17`,
		wantWhere: []any{1, 1, 2, 2, 3},
	})
}

// TestCursorPaginateWithDynamicColumnInSelectRaw ports testCursorPaginateWithDynamicColumnInSelectRaw
// (corpus 6015; SQL 6033, where 6034): the order names an alias from the select list, so the comparison
// targets the original expression. The expression already carries its own parentheses, and the nested
// group adds another pair, giving "((...)) > ?".
func TestLaravelPortCursorPaginateWithDynamicColumnInSelectRaw(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 15,
		cursor:  newTestCursor(CursorParameters{{Column: "test", Value: "bar"}}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").
				Select("*").
				SelectRaw("(CONCAT(firstname, ' ', lastname)) as test", nil).
				OrderBy("test", "asc")
		},
		wantSQL:   `select *, (CONCAT(firstname, ' ', lastname)) as test from "foobar" where ((CONCAT(firstname, ' ', lastname)) > ?) order by "test" asc limit 16`,
		wantWhere: []any{"bar"},
	})
}

// TestCursorPaginateWithDynamicColumnWithCastInSelectRaw ports
// testCursorPaginateWithDynamicColumnWithCastInSelectRaw (corpus 6056; SQL 6074, where 6075): same
// alias-resolution shape as the previous case, with a CAST wrapper around the expression.
func TestLaravelPortCursorPaginateWithDynamicColumnWithCastInSelectRaw(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 15,
		cursor:  newTestCursor(CursorParameters{{Column: "test", Value: "bar"}}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").
				Select("*").
				SelectRaw("(CAST(CONCAT(firstname, ' ', lastname) as VARCHAR)) as test", nil).
				OrderBy("test", "asc")
		},
		wantSQL:   `select *, (CAST(CONCAT(firstname, ' ', lastname) as VARCHAR)) as test from "foobar" where ((CAST(CONCAT(firstname, ' ', lastname) as VARCHAR)) > ?) order by "test" asc limit 16`,
		wantWhere: []any{"bar"},
	})
}

// TestCursorPaginateWithDynamicColumnInSelectSub ports testCursorPaginateWithDynamicColumnInSelectSub
// (corpus 6097; SQL 6115, where 6116): the alias is introduced by selectSub, whose subquery is a bare
// expression here, so the SELECT list quotes the alias ("test") while the cursor comparison still
// resolves to the original expression.
func TestLaravelPortCursorPaginateWithDynamicColumnInSelectSub(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 15,
		cursor:  newTestCursor(CursorParameters{{Column: "test", Value: "bar"}}, true),
		build: func(b *Builder) *Builder {
			return b.From("foobar", "").
				Select("*").
				SelectSub("CONCAT(firstname, ' ', lastname)", "test").
				OrderBy("test", "asc")
		},
		wantSQL:   `select *, (CONCAT(firstname, ' ', lastname)) as "test" from "foobar" where ((CONCAT(firstname, ' ', lastname)) > ?) order by "test" asc limit 16`,
		wantWhere: []any{"bar"},
	})
}

// TestCursorPaginateWithUnionWheres ports testCursorPaginateWithUnionWheres (corpus 6138; SQL 6165,
// where 6166, union 6167): one ordered column and one union branch. The root compares its own original
// column (start_time) while the branch compares created_at, and the bindings split one value to "where"
// and one to "union".
func TestLaravelPortCursorPaginateWithUnionWheres(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor:  newTestCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true),
		build: func(b *Builder) *Builder {
			news := newTestBuilder().
				Select("id", "created_at").
				SelectRaw("'news' as type", nil).
				From("news", "")

			b.Select("id", "start_time as created_at").
				SelectRaw("'video' as type", nil).
				From("videos", "")
			b.Union(news, false)

			return b.OrderBy("created_at", "asc")
		},
		wantSQL:   `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" > ?)) union (select "id", "created_at", 'news' as type from "news" where ("created_at" > ?)) order by "created_at" asc limit 17`,
		wantWhere: []any{cursorTs},
		wantUnion: []any{cursorTs},
	})
}

// TestCursorPaginateWithMultipleUnionsAndMultipleWheres ports
// testCursorPaginateWithMultipleUnionsAndMultipleWheres (corpus 6185; SQL 6214, where 6215, union 6216):
// two branches, each with a pre-existing where. The pre-existing branch value is replayed once, ahead of
// that branch's cursor value.
func TestLaravelPortCursorPaginateWithMultipleUnionsAndMultipleWheres(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor:  newTestCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true),
		build: func(b *Builder) *Builder {
			news := newTestBuilder().
				Select("id", "created_at").
				SelectRaw("'news' as type", nil).
				From("news", "").
				Where("extra", "first")
			podcasts := newTestBuilder().
				Select("id", "created_at").
				SelectRaw("'podcast' as type", nil).
				From("podcasts", "").
				Where("extra", "second")

			b.Select("id", "start_time as created_at").
				SelectRaw("'video' as type", nil).
				From("videos", "")
			b.Union(news, false)
			b.Union(podcasts, false)

			return b.OrderBy("created_at", "asc")
		},
		wantSQL:   `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" > ?)) union (select "id", "created_at", 'news' as type from "news" where "extra" = ? and ("created_at" > ?)) union (select "id", "created_at", 'podcast' as type from "podcasts" where "extra" = ? and ("created_at" > ?)) order by "created_at" asc limit 17`,
		wantWhere: []any{cursorTs},
		wantUnion: []any{"first", cursorTs, "second", cursorTs},
	})
}

// TestCursorPaginateWithUnionMultipleWheresMultipleOrders ports
// testCursorPaginateWithUnionMultipleWheresMultipleOrders (corpus 6234; SQL 6264, where 6265, union
// 6266): three ordered columns and two branches, each with a pre-existing where. It also pins the
// alias-resolution quirk the corpus encodes: every branch's FIRST order column resolves against the
// branch's own select list, but the deeper columns resolve against the ROOT's — so both branches compare
// "start_time" for created_at even though their own select list does not alias it.
func TestLaravelPortCursorPaginateWithUnionMultipleWheresMultipleOrders(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor: newTestCursor(CursorParameters{
			{Column: "id", Value: 1},
			{Column: "created_at", Value: cursorTs},
			{Column: "type", Value: "news"},
		}, true),
		build: func(b *Builder) *Builder {
			news := newTestBuilder().
				Select("id", "created_at", "type").
				From("news", "").
				Where("extra", "second")
			podcasts := newTestBuilder().
				Select("id", "created_at", "type").
				From("podcasts", "").
				Where("extra", "third")

			b.Select("id", "start_time as created_at", "type").
				From("videos", "").
				Where("extra", "first")
			b.Union(news, false)
			b.Union(podcasts, false)

			return b.OrderBy("id", "asc").OrderByDesc("created_at").OrderBy("type", "asc")
		},
		wantSQL:   `(select "id", "start_time" as "created_at", "type" from "videos" where "extra" = ? and ("id" > ? or ("id" = ? and ("start_time" < ? or ("start_time" = ? and ("type" > ?)))))) union (select "id", "created_at", "type" from "news" where "extra" = ? and ("id" > ? or ("id" = ? and ("start_time" < ? or ("start_time" = ? and ("type" > ?)))))) union (select "id", "created_at", "type" from "podcasts" where "extra" = ? and ("id" > ? or ("id" = ? and ("start_time" < ? or ("start_time" = ? and ("type" > ?)))))) order by "id" asc, "created_at" desc, "type" asc limit 17`,
		wantWhere: []any{"first", 1, 1, cursorTs, cursorTs, "news"},
		wantUnion: []any{"second", 1, 1, cursorTs, cursorTs, "news", "third", 1, 1, cursorTs, cursorTs, "news"},
	})
}

// TestCursorPaginateWithUnionWheresWithRawOrderExpression ports
// testCursorPaginateWithUnionWheresWithRawOrderExpression (corpus 6284; SQL 6311, where 6312, union
// 6313): a raw ORDER BY fragment takes part in ORDER BY but contributes no comparison and no parameter,
// so the cursor compares only the following real column. Each branch keeps its own pre-existing where,
// and a bool binding passes through unchanged.
func TestLaravelPortCursorPaginateWithUnionWheresWithRawOrderExpression(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor:  newTestCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true),
		build: func(b *Builder) *Builder {
			news := newTestBuilder().
				Select("id", "is_published", "created_at").
				SelectRaw("'news' as type", nil).
				From("news", "").
				Where("is_published", true)

			b.Select("id", "is_published", "start_time as created_at").
				SelectRaw("'video' as type", nil).
				From("videos", "").
				Where("is_published", true)
			b.Union(news, false)

			return b.OrderByRaw(`case when (id = 3 and type="news" then 0 else 1 end)`, nil).
				OrderBy("created_at", "asc")
		},
		wantSQL:   `(select "id", "is_published", "start_time" as "created_at", 'video' as type from "videos" where "is_published" = ? and ("start_time" > ?)) union (select "id", "is_published", "created_at", 'news' as type from "news" where "is_published" = ? and ("created_at" > ?)) order by case when (id = 3 and type="news" then 0 else 1 end), "created_at" asc limit 17`,
		wantWhere: []any{true, cursorTs},
		wantUnion: []any{true, cursorTs},
	})
}

// TestCursorPaginateWithUnionWheresReverseOrder ports testCursorPaginateWithUnionWheresReverseOrder
// (corpus 6331; SQL 6358, where 6359, union 6360): a previous-page cursor reverses every ORDER BY
// direction, flipping the comparison operator from ">" to "<" while the ORDER BY itself descends.
func TestLaravelPortCursorPaginateWithUnionWheresReverseOrder(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor:  newTestCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, false),
		build: func(b *Builder) *Builder {
			news := newTestBuilder().
				Select("id", "created_at").
				SelectRaw("'news' as type", nil).
				From("news", "")

			b.Select("id", "start_time as created_at").
				SelectRaw("'video' as type", nil).
				From("videos", "")
			b.Union(news, false)

			return b.OrderBy("created_at", "asc")
		},
		wantSQL:   `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" < ?)) union (select "id", "created_at", 'news' as type from "news" where ("created_at" < ?)) order by "created_at" desc limit 17`,
		wantWhere: []any{cursorTs},
		wantUnion: []any{cursorTs},
	})
}

// TestCursorPaginateWithUnionWheresMultipleOrders ports testCursorPaginateWithUnionWheresMultipleOrders
// (corpus 6378; SQL 6405, where 6406, union 6407): two ordered columns with mixed directions. The
// reversal is per column, never a blanket flip of the tuple: created_at stays "desc" (so "<") and id
// stays "asc" (so ">").
func TestLaravelPortCursorPaginateWithUnionWheresMultipleOrders(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor: newTestCursor(CursorParameters{
			{Column: "created_at", Value: cursorTs},
			{Column: "id", Value: 1},
		}, true),
		build: func(b *Builder) *Builder {
			news := newTestBuilder().
				Select("id", "created_at").
				SelectRaw("'news' as type", nil).
				From("news", "")

			b.Select("id", "start_time as created_at").
				SelectRaw("'video' as type", nil).
				From("videos", "")
			b.Union(news, false)

			return b.OrderByDesc("created_at").OrderBy("id", "asc")
		},
		wantSQL:   `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" < ? or ("start_time" = ? and ("id" > ?)))) union (select "id", "created_at", 'news' as type from "news" where ("created_at" < ? or ("created_at" = ? and ("id" > ?)))) order by "created_at" desc, "id" asc limit 17`,
		wantWhere: []any{cursorTs, cursorTs, 1},
		wantUnion: []any{cursorTs, cursorTs, 1},
	})
}

// TestCursorPaginateWithUnionWheresAndAliassedOrderColumns ports
// testCursorPaginateWithUnionWheresAndAliassedOrderColumns (corpus 6425; SQL 6454, where 6455, union
// 6456): two branches alias the ordered column differently (start_time, created_at, init_at); a single
// order column means each branch resolves its own original column.
func TestLaravelPortCursorPaginateWithUnionWheresAndAliassedOrderColumns(t *testing.T) {
	runCursorLaravelCase(t, cursorLaravelCase{
		perPage: 16,
		cursor:  newTestCursor(CursorParameters{{Column: "created_at", Value: cursorTs}}, true),
		build: func(b *Builder) *Builder {
			news := newTestBuilder().
				Select("id", "created_at").
				SelectRaw("'news' as type", nil).
				From("news", "")
			podcasts := newTestBuilder().
				Select("id", "init_at as created_at").
				SelectRaw("'podcast' as type", nil).
				From("podcasts", "")

			b.Select("id", "start_time as created_at").
				SelectRaw("'video' as type", nil).
				From("videos", "")
			b.Union(news, false)
			b.Union(podcasts, false)

			return b.OrderBy("created_at", "asc")
		},
		wantSQL:   `(select "id", "start_time" as "created_at", 'video' as type from "videos" where ("start_time" > ?)) union (select "id", "created_at", 'news' as type from "news" where ("created_at" > ?)) union (select "id", "init_at" as "created_at", 'podcast' as type from "podcasts" where ("init_at" > ?)) order by "created_at" asc limit 17`,
		wantWhere: []any{cursorTs},
		wantUnion: []any{cursorTs, cursorTs},
	})
}

// newTestCursor builds a *Cursor for a case literal, since the builder entry point takes a pointer and
// NewCursor returns the value type.
func newTestCursor(parameters CursorParameters, pointsNext bool) *Cursor {
	cursor := NewCursor(parameters, pointsNext)

	return &cursor
}
