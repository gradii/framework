package db

import (
	"context"
	"testing"

	"github.com/gradii/framework/contracts/database/db"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// previewQuery builds a SQL-only query over the framework's own base grammar. The previews below
// assert framework behaviour; the per-dialect SQL this test used to pin - backticks, "like binary",
// RAND(seed), binary literals, insert ignore, upsert - needs the driver's grammar and escaper and is
// asserted against the real dialects in tests/database/db.
func previewQuery() *Query {
	return newDialectTestQuery(context.Background(), nil, nil, querybuilder.NewBaseGrammar(""), "", nil, "users", nil)
}

func TestPublicQueryOptionalArguments(t *testing.T) {
	query := previewQuery()
	other := query.NewQuery().From("ignored")
	query.WhereIn("id", func(child db.Query) db.Query {
		child.From("posts").Select("user_id").Where("active", true)
		return other
	}).WhereExists(func(child db.Query) {
		child.From("profiles").WhereColumn("profiles.user_id", "users.id")
	}).WhereLike("name", "J%").InRandomOrder(17)
	assert.Equal(t, "select * from \"users\" where \"id\" in (select \"user_id\" from \"posts\" where \"active\" = ?) and exists (select * from \"profiles\" where \"profiles\".\"user_id\" = \"users\".\"id\") and \"name\" like ? order by RANDOM()", query.ToSql().Get())
	assert.Equal(t, []any{true, "J%"}, query.GetBindings())

	// A case-sensitive like is a dialect capability the base grammar does not carry, so asking for it
	// records an error instead of compiling SQL.
	unsupported := previewQuery()
	unsupported.WhereLike("name", "J%", true)
	_, _, err := unsupported.buildSelect()
	require.Error(t, err)
}

// TestPreviewReportsUnsupportedUpdateFrom pins that updateFrom, which only PostgreSQL implements,
// surfaces as a builder error instead of SQL.
func TestPreviewReportsUnsupportedUpdateFrom(t *testing.T) {
	query := previewQuery()
	assert.Empty(t, query.ToSql().UpdateFrom(map[string]any{"name": "Alice"}))
	require.ErrorIs(t, query.builder.Error(), errors.QueryUpdateFromNotSupported)
}

func TestPluckPreviewConsumesCallbackAndRestoresColumns(t *testing.T) {
	query := previewQuery()
	calls := 0
	query.BeforeQuery(func(q db.Query) db.Query { calls++; return q.Where("active", true) })
	assert.Equal(t, "select \"name\" from \"users\" where \"active\" = ?", query.ToSql().Pluck("name", nil))
	assert.Equal(t, "select * from \"users\" where \"active\" = ?", query.ToSql().Get())
	assert.Equal(t, 1, calls)
	var rows []map[string]any
	assert.ErrorIs(t, query.Paginate(int(^uint(0)>>1), 2, &rows, nil), errors.QueryPaginationOverflow)
}
