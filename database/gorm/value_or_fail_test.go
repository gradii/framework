package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

// valueOrFail() is value() with the miss turned into an error: the read selects the column alone
// and limits to one row (oracle: `select "name" from "users" limit 1`).
func TestValueOrFailReadsTheColumnOfTheFirstRow(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)
	recorder.returnRows(`select "name"`, []string{"name"}, []driver.Value{"Jane"})

	var name string
	require.NoError(t, query.Where("id", 1).(*OrmBuilder).ValueOrFail("name", &name))

	assert.Equal(t, "Jane", name)
	assert.Equal(t, `select "name" from "users" where "id" = ? limit 1`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, recorder.last(t).args)

	// The column is installed for this read only: the query's own selection is still the wildcard.
	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "users" where "id" = ?`, statement)
}

// A read that matches no row reports the model-not-found error: Laravel's valueOrFail throws
// ModelNotFoundException, whose id-less message names the model — "No query results for model
// [class]." — wrapping errors.OrmRecordNotFound. dest keeps the value it had.
func TestValueOrFailReportsTheModelNotFoundErrorOnAMiss(t *testing.T) {
	query, _ := newGetColumnsQuery(t)

	name := "unchanged"
	err := query.Where("id", 999).(*OrmBuilder).ValueOrFail("name", &name)

	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
	assert.EqualError(t, err, "No query results for model [gorm.getColumnsUser].")
	assert.Equal(t, "unchanged", name)
}

// TestValueOrFailKeepsASelectionTheReadFoundMatchesTheOracle pins the same onceWithColumns guard
// for valueOrFail: the scopes apply before the read narrows the select, so a scope-written or
// pre-set list survives and is read back by the column's bare name — a kept list that does not
// carry the column answers null, no error, and dest keeps its value; a kept multi-column list that
// does carry it reads its value (oracle, illuminate/database v12.55.1, executed against sqlite,
// /tmp/pluck-verify/oracle_scope_repin.php + oracle_scope_repin2.php: scope selectRaw('name') +
// valueOrFail('email') runs `select name from "users" limit 1` and returns null;
// select('name')->valueOrFail('email') runs `select "name" from "users" limit 1` and returns null;
// select('id','email')->valueOrFail('email') keeps `select "id", "email" from "users" limit 1` and
// returns "taylorotwell@gmail.com").
func TestValueOrFailKeepsASelectionTheReadFoundMatchesTheOracle(t *testing.T) {
	t.Run("selectRaw scope", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeRawUser{}, "sel", pluckSelectRawScope)
		recorder.returnRows(`select name from "users"`, []string{"name"}, []driver.Value{"Taylor"})

		email := "unchanged"
		require.NoError(t, query.ValueOrFail("users.email", &email))

		assert.Equal(t, `select name from "users" limit 1`, recorder.last(t).sql)
		assert.Equal(t, "unchanged", email)
	})

	t.Run("a pre-set select list", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select "name"`, []string{"name"}, []driver.Value{"Taylor"})

		email := "unchanged"
		require.NoError(t, query.Select("name").(*OrmBuilder).ValueOrFail("email", &email))

		assert.Equal(t, `select "name" from "users" limit 1`, recorder.last(t).sql)
		assert.Equal(t, "unchanged", email)
	})

	t.Run("a kept multi-column list reads the requested column back by name", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select "id", "email"`, []string{"id", "email"},
			[]driver.Value{int64(1), "taylorotwell@gmail.com"})

		var email string
		require.NoError(t, query.Select("id", "email").(*OrmBuilder).ValueOrFail("email", &email))

		assert.Equal(t, `select "id", "email" from "users" limit 1`, recorder.last(t).sql)
		assert.Equal(t, "taylorotwell@gmail.com", email)
	})
}

// TestValueOrFailScopeProvidedLimitMatchesTheOracle pins the same limit ordering for valueOrFail:
// the scope's limit overrides the one-row limit (oracle, illuminate/database v12.55.1, executed
// against sqlite, /tmp/pluck-verify/zz_limit_vof_probe.php: a scope doing
// selectRaw('name')->limit(5) then valueOrFail('users.name') compiles `select name from "users"
// limit 5` and answers "Taylor"; a scope doing limit(0) then valueOrFail('users.email') compiles
// `select "users"."email" from "users" limit 0` and throws ModelNotFoundException).
func TestValueOrFailScopeProvidedLimitMatchesTheOracle(t *testing.T) {
	t.Run("a scope limit overrides the one-row limit", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeLimitUser{}, "sel", pluckLimitScope)
		recorder.returnRows(`from "users"`, []string{"name"}, []driver.Value{"Taylor"})

		var name string
		require.NoError(t, query.ValueOrFail("users.name", &name))

		assert.Equal(t, `select name from "users" limit 5`, recorder.last(t).sql)
		assert.Equal(t, "Taylor", name)
	})

	t.Run("a limit(0) scope reports the model-not-found error", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeZeroLimitUser{}, "zero", pluckZeroLimitScope)

		email := "unchanged"
		err := query.ValueOrFail("users.email", &email)

		assert.Equal(t, `select "users"."email" from "users" limit 0`, recorder.last(t).sql)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.OrmRecordNotFound)
		assert.EqualError(t, err, "No query results for model [gorm.pluckScopeZeroLimitUser].")
		assert.Equal(t, "unchanged", email)
	})
}
