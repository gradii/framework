package gorm

// WhereColumn / OrWhereColumn pin the comparison SQL both forms compile, measured with the oracle
// (php .claude/laravel-oracle/oracle.php against illuminate/database v12.55.1): whereColumn('a',
// 'b') renders `where "a" = "b"`, the operator form `where "a" != "b"` and orWhereColumn joins
// under "or" — mysql, pgsql, sqlite and sqlsrv alike, modulo identifier quoting. One trailing
// column means equality; two mean operator then column; anything else is refused the way the raw
// query refuses it.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

func TestWhereColumn_EqualityForm(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).WhereColumn("friend_id", "user_id").(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "friend_id" = "user_id"`, statement)
	assert.Empty(t, bindings)
}

func TestWhereColumn_OperatorForm(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).WhereColumn("a", "!=", "b").(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" != "b"`, statement)
	assert.Empty(t, bindings)
}

func TestOrWhereColumn_JoinsUnderOr(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("x", 1).OrWhereColumn("a", "b").(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "x" = ? or "a" = "b"`, statement)
	assert.Equal(t, []any{1}, bindings)
}

func TestWhereColumn_RefusesMoreThanTwoTrailingArguments(t *testing.T) {
	// Two trailing arguments are the operator form; a third has no meaning and is refused.
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).WhereColumn("a", "b", "c", "d").(*OrmBuilder)
	require.True(t, ok)

	_, _, err := query.compileSelect()
	assert.ErrorIs(t, err, errors.DatabaseInvalidArgumentNumber)
}
