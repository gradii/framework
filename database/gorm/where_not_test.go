package gorm

// WhereNot / OrWhereNot pins, measured with the oracle (php .claude/laravel-oracle/oracle.php
// against illuminate/database v12.55.1, sqlite). The ORM mirrors Eloquent\Builder::whereNot
// (Builder.php:407) — where($column, $operator, $value, $boolean.' not') — not the raw
// Query\Builder::whereNot (Query/Builder.php:1027). A column with its value rides the "and not"
// connector (`and not "b" = ?`), the callback form wraps its nested group in the negation
// (`and not ("b" = ? or "c" = ?)`), and an array first argument takes Eloquent's route: the array
// reaches the inner builder as where(array, 'and not') (Eloquent where() Builder.php:352 ->
// Query/Builder.php:823 -> addArrayOfWheres Query/Builder.php:928), which negates every column
// entry inside one nested group — `and not (not "b" = ? and not "c" = ?)`, or
// `or not (not "b" = ? or not "c" = ?)` under OrWhereNot (Eloquent Builder.php:420). The raw
// builder keeps Query\Builder::whereNot's own shape, `and not (("b" = ? and "c" = ?))`.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

func TestWhereNot_NegatesOneColumnValuePair(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("a", 1).WhereNot("b", 2).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" = ? and not "b" = ?`, statement)
	assert.Equal(t, []any{1, 2}, bindings)
}

func TestOrWhereNot_NegatesUnderTheOrConnector(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("a", 1).OrWhereNot("b", 2).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" = ? or not "b" = ?`, statement)
	assert.Equal(t, []any{1, 2}, bindings)
}

func TestWhereNot_CallbackRendersOneNegatedGroup(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("a", 1).
		WhereNot(func(query contractsorm.Query) contractsorm.Query {
			return query.Where("b", 2).OrWhere("c", 3)
		}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" = ? and not ("b" = ? or "c" = ?)`, statement)
	assert.Equal(t, []any{1, 2, 3}, bindings)
}

func TestWhereNot_ArrayNegatesEveryColumnInsideTheGroup(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("a", 1).
		WhereNot(map[string]any{"b": 2, "c": 3}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" = ? and not (not "b" = ? and not "c" = ?)`, statement)
	assert.Equal(t, []any{1, 2, 3}, bindings)
}

func TestOrWhereNot_ArrayNegatesEveryColumnUnderTheOrConnector(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("a", 1).
		OrWhereNot(map[string]any{"b": 2, "c": 3}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" = ? or not (not "b" = ? or not "c" = ?)`, statement)
	assert.Equal(t, []any{1, 2, 3}, bindings)
}

func TestWhereNot_OrderedPairArrayNegatesEachEntry(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("a", 1).
		WhereNot([]any{[]any{"foo", 1}, []any{"bar", "<", 2}}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" = ? and not (not "foo" = ? and not "bar" < ?)`, statement)
	assert.Equal(t, []any{1, 1, 2}, bindings)
}

func TestWhereNot_EmptyArrayAddsNoClause(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).Where("a", 1).
		WhereNot(map[string]any{}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where "a" = ?`, statement)
	assert.Equal(t, []any{1}, bindings)
}

// Both first-clause shapes: with no preceding where, compileSelect strips the "and "/"or "
// connector from the leading negated group, which still nests once — oracle-measured
// `select * from "users" where not (not "b" = ? and not "c" = ?)` and, for orWhereNot,
// `select * from "users" where not (not "b" = ? or not "c" = ?)`.
func TestWhereNot_ArrayAsFirstClauseStripsOnlyTheConnector(t *testing.T) {
	query, ok := compiledQuery(t, &relUser{}, Conditions{}).WhereNot(map[string]any{"b": 2, "c": 3}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where not (not "b" = ? and not "c" = ?)`, statement)
	assert.Equal(t, []any{2, 3}, bindings)

	query, ok = compiledQuery(t, &relUser{}, Conditions{}).OrWhereNot(map[string]any{"b": 2, "c": 3}).(*OrmBuilder)
	require.True(t, ok)

	statement, bindings, err = query.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where not (not "b" = ? or not "c" = ?)`, statement)
	assert.Equal(t, []any{2, 3}, bindings)
}
