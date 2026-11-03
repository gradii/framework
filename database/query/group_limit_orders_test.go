package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGroupLimitMovesTheOrderIntoTheWindow pins Grammar::compileGroupLimit's handling of an order
// by: the orders compile into the row_number() window — `over (partition by X order by ...)` —
// and are unset from the component list, so the derived table carries no order of its own and the
// outer statement orders by "laravel_row" alone. Oracle (illuminate/database v12.55.1, sqlite,
// /tmp/followup-take-grouplimit/take_grouplimit.php H12):
//
//	select * from (select *, row_number() over (partition by "gl_posts"."user_id" order by "title" desc)
//	as "laravel_row" from "gl_posts" where ...) as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"
func TestGroupLimitMovesTheOrderIntoTheWindow(t *testing.T) {
	builder := NewBuilder(NewBaseGrammar("")).From("gl_posts", "").OrderBy("title", "desc").GroupLimit(2, "gl_posts.user_id")

	assert.Equal(t, `select * from (select *, row_number() over (partition by "gl_posts"."user_id" order by "title" desc) as "laravel_row" from "gl_posts") as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`, builder.ToSQL())
}
