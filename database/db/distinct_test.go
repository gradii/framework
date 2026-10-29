package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	querybuilder "github.com/gradii/framework/database/query"
)

// TestDistinctPreservesSelection pins the base-grammar SQL and the builder invariants behind
// Distinct: the columns are captured on a clone without touching the receiver, and the receiver's
// distinct flag and its own selection survive. The per-dialect rendering - PostgreSQL's
// "distinct on (...)", MySQL's backticks - needs the driver grammar and is asserted against the real
// dialects in tests/database/db.
func TestDistinctPreservesSelection(t *testing.T) {
	query := newDialectTestQuery(context.Background(), nil, nil, querybuilder.NewBaseGrammar(""), "", nil, "users", nil)
	base := query.Select("name").(*Query)
	base.builder.SelectRaw("? as flag", []any{1}).Where("id", 9)
	columns := []string{"name"}
	selected := base.Clone().Distinct(columns...).(*Query)
	columns[0] = "id"
	all := selected.Clone().Distinct().(*Query)

	statement, bindings, err := selected.buildSelect()
	require.NoError(t, err)
	assert.Equal(t, `select distinct "name", ? as flag from "users" where "id" = ?`, statement)
	assert.Equal(t, []any{1, 9}, bindings)

	statement, bindings, err = all.buildSelect()
	require.NoError(t, err)
	assert.Equal(t, `select distinct "name", ? as flag from "users" where "id" = ?`, statement)
	assert.Equal(t, []any{1, 9}, bindings)

	count := selected.clone()
	require.NoError(t, buildSelectForCount(count))
	statement, bindings, err = count.buildSelect()
	require.NoError(t, err)
	assert.Equal(t, `select count(distinct "name") as aggregate from "users" where "id" = ?`, statement)
	assert.Equal(t, []any{9}, bindings)

	assert.False(t, base.builder.DistinctFlag())
	assert.Len(t, base.builder.Selects(), 2)
}
