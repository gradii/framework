package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Laravel keeps the DISTINCT columns separate from both the projection and its bindings. These run
// on the framework's own base grammar; the dialect spelling of DISTINCT ON — Postgres' `select
// distinct on (...)` — is pinned in the integration corpus (tests/database/query) against the real
// driver grammars.
func TestDistinctPreservesSelection(t *testing.T) {
	query := compiledQuery(t, &getColumnsUser{}, Conditions{})
	base := query.Select("name").SelectRaw("? as flag", 1).Where("id", 9).(*OrmBuilder)
	columns := []string{"name"}
	selected := base.clone().Distinct(columns...).(*OrmBuilder)
	// Writing through the caller's slice must not reach the query, which copies it.
	columns[0] = "id"
	all := selected.clone().Distinct().(*OrmBuilder)

	statement, bindings, err := selected.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select distinct "name", ? as flag from "users" where "id" = ?`, statement)
	assert.Equal(t, []any{1, 9}, bindings)

	statement, bindings, err = all.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select distinct "name", ? as flag from "users" where "id" = ?`, statement)
	assert.Equal(t, []any{1, 9}, bindings)

	statement, bindings, err = selected.compileAggregate("count", []any{"*"})
	require.NoError(t, err)
	assert.Equal(t, `select count(distinct "name") as aggregate from "users" where "id" = ?`, statement)
	assert.Equal(t, []any{9}, bindings)

	// Distinct is written to the clone, not the query it branched from.
	assert.False(t, base.query.DistinctFlag())
	assert.Len(t, base.query.Selects(), 2)
}

func TestDistinctPreservesRelationAggregate(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).WithCount("Books").Distinct("name").(*OrmBuilder)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, statement, `select distinct "rel_users".*`)
	assert.Contains(t, statement, `(select count(*) from "rel_books"`)
	assert.Contains(t, statement, `AS "books_count"`)
	assert.Empty(t, bindings)
}

func TestSelectPreservesDistinctColumns(t *testing.T) {
	query := compiledQuery(t, &getColumnsUser{}, Conditions{})
	// A later Select replaces the projection but must leave the DISTINCT columns in place.
	selected := query.SelectRaw("? as old", 1).Distinct("name").Select("id").(*OrmBuilder)

	statement, bindings, err := selected.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select distinct "id" from "users"`, statement)
	assert.Empty(t, bindings)
}
