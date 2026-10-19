package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports the corpus methods the port plan classifies as PORTABLE with area "pagination-gap"
// (see .claude/laravel-oracle/port_plan.md): testForPage, testForPageBeforeId and testForPageAfterId.
// The corpus is tests/Database/DatabaseQueryBuilderTest.php from laravel/framework 12.x
// (commit 0fc645c6737626a1b1636cd234192b0d78b76569), lines 2459-2507. Every chain there starts from
// getBuilder(), so the base grammar applies: "?" placeholders and double-quoted identifiers.

// TestForPage ports Laravel's testForPage (corpus L2459-2484): six assertions on
// select('*')->from('users')->forPage(page, perPage). The last three pin a page size of zero and rely
// on forPage not substituting a default for an explicit 0: Builder::forPage is
// offset(($page - 1) * $perPage)->limit($perPage), so a perPage of 0 keeps a limit of 0 and, times the
// page delta, still yields offset 0. ForPage honours that now (its variadic perPage defaults to 15
// only when omitted), so all six assertions hold.
func TestForPage(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"second page of fifteen", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPage(2, 15)
		}, `select * from "users" limit 15 offset 15`},
		{"zero page of fifteen", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPage(0, 15)
		}, `select * from "users" limit 15 offset 0`},
		{"negative page of fifteen", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPage(-2, 15)
		}, `select * from "users" limit 15 offset 0`},
		{"second page of zero", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPage(2, 0)
		}, `select * from "users" limit 0 offset 0`},
		{"zero page of zero", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPage(0, 0)
		}, `select * from "users" limit 0 offset 0`},
		{"negative page of zero", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPage(-2, 0)
		}, `select * from "users" limit 0 offset 0`},
	})
}

// TestForPageBeforeId ports Laravel's testForPageBeforeId (corpus L2486-2496). The corpus passes no
// column, so our empty column argument stands for Laravel's default 'id'; a nil last id becomes a
// not-null test and a 0 last id a "< ?" comparison, and in both shapes the order on the paged column
// is desc with no offset.
func TestForPageBeforeId(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"null last id", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPageBeforeId(15, nil, "")
		}, `select * from "users" where "id" is not null order by "id" desc limit 15`},
		{"zero last id", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPageBeforeId(15, 0, "")
		}, `select * from "users" where "id" < ? order by "id" desc limit 15`},
	})
}

// TestForPageAfterId ports Laravel's testForPageAfterId (corpus L2497-2507). Same two shapes as the
// "before" case, but the comparison is ">" and the order on the paged column is asc.
func TestForPageAfterId(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"null last id", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPageAfterId(15, nil, "")
		}, `select * from "users" where "id" is not null order by "id" asc limit 15`},
		{"zero last id", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").ForPageAfterId(15, 0, "")
		}, `select * from "users" where "id" > ? order by "id" asc limit 15`},
	})
}

// The five methods below are the "count-for-pagination" group. The port plan files them under
// NEEDS_DB because the corpus drives each through a mocked connection and processor, but what every
// one of them actually pins is the SQL text handed to Connection::select() (and, in the bindings
// case, that the original builder keeps its own bindings). The builder has no execution side here,
// so the ports assert the compiled count statement and its bindings; the mocked rows and the
// integer the processor extracts are out of reach and are deliberately not asserted.
//
// Entry point added for this group: Builder.GetCountForPagination(columns ...any) (string, []any),
// mirroring Builder::runPaginationCountQuery (the compiler half of Builder::getCountForPagination).
// Like Laravel it clones the query rather than mutating it, so the original builder's bindings
// survive (pinned by the first case).

// TestGetCountForPaginationWithBindings ports Laravel's testGetCountForPaginationWithBindings (corpus
// L2508-2524). A selectSub contributes a "select" binding; the count statement drops the column list
// and, with it, that binding, so the count SQL carries no bindings — while the original builder keeps
// its [4], because the count runs against a clone.
func TestGetCountForPaginationWithBindings(t *testing.T) {
	sub := NewBuilder(NewBaseGrammar("")).From("posts", "").Select("body").Where("id", 4)
	b := newTestBuilder().From("users", "").SelectSub(sub, "post")

	sql, bindings := b.GetCountForPagination()

	assert.Equal(t, `select count(*) as aggregate from "users"`, sql)
	assert.Empty(t, bindings)
	// The count ran on a clone; the source builder's select binding is intact.
	assert.Equal(t, []any{4}, b.GetBindings())
}

// TestGetCountForPaginationWithColumnAliases ports Laravel's testGetCountForPaginationWithColumnAliases
// (corpus L2525-2539). The count columns are passed explicitly and each " as alias" suffix is stripped
// before they are wrapped, so count("body", "teaser", "posts"."created") counts the underlying columns.
func TestGetCountForPaginationWithColumnAliases(t *testing.T) {
	columns := []any{"body as post_body", "teaser", "posts.created as published"}
	b := newTestBuilder().From("posts", "").Select(columns...)

	sql, bindings := b.GetCountForPagination(columns...)

	assert.Equal(t, `select count("body", "teaser", "posts"."created") as aggregate from "posts"`, sql)
	assert.Empty(t, bindings)
}

// TestGetCountForPaginationWithUnion ports Laravel's testGetCountForPaginationWithUnion (corpus
// L2540-2553). With unions present the count wraps the union as a derived table named "temp_table"
// and counts that.
func TestGetCountForPaginationWithUnion(t *testing.T) {
	other := NewBuilder(NewBaseGrammar("")).From("videos", "").Select("id")
	b := newTestBuilder().From("posts", "").Select("id").Union(other, false)

	sql, bindings := b.GetCountForPagination()

	assert.Equal(t, `select count(*) as aggregate from ((select "id" from "posts") union (select "id" from "videos")) as "temp_table"`, sql)
	assert.Empty(t, bindings)
}

// TestGetCountForPaginationWithUnionOrders ports Laravel's testGetCountForPaginationWithUnionOrders
// (corpus L2554-2567). An order on the union must not reach the count: the clone drops unionOrders.
func TestGetCountForPaginationWithUnionOrders(t *testing.T) {
	other := NewBuilder(NewBaseGrammar("")).From("videos", "").Select("id")
	b := newTestBuilder().From("posts", "").Select("id").Union(other, false).Latest("")

	sql, bindings := b.GetCountForPagination()

	assert.Equal(t, `select count(*) as aggregate from ((select "id" from "posts") union (select "id" from "videos")) as "temp_table"`, sql)
	assert.Empty(t, bindings)
}

// TestGetCountForPaginationWithUnionLimitAndOffset ports Laravel's
// testGetCountForPaginationWithUnionLimitAndOffset (corpus L2568-2581). A limit/offset on the union
// must not reach the count either: the clone drops unionLimit and unionOffset.
func TestGetCountForPaginationWithUnionLimitAndOffset(t *testing.T) {
	other := NewBuilder(NewBaseGrammar("")).From("videos", "").Select("id")
	b := newTestBuilder().From("posts", "").Select("id").Union(other, false).Limit(15).Offset(1)

	sql, bindings := b.GetCountForPagination()

	assert.Equal(t, `select count(*) as aggregate from ((select "id" from "posts") union (select "id" from "videos")) as "temp_table"`, sql)
	assert.Empty(t, bindings)
}
