package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Where hands every condition to the inner query builder as written, mirroring Builder::where():
// a string that looks like a fragment is a column and binds like any other value. Pinned against
// the Laravel oracle (v12.55.1, pgsql): select * from "users" where "name = ?" = ?.
func TestWhere_FragmentStringIsAColumn(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).Where("name = ?", "x").(*OrmBuilder)
	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where "name = ?" = ?`, sql)
	assert.Equal(t, []any{"x"}, bindings)
}

// A single-argument Where is Laravel's whereNull shorthand applied to the string as the column
// (oracle: select * from "users" where "1 = 0" is null).
func TestWhere_SingleArgumentIsANullTest(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).Where("1 = 0").(*OrmBuilder)
	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where "1 = 0" is null`, sql)
	assert.Empty(t, bindings)
}

// WhereRaw is the raw entry point, mirroring Builder::whereRaw (oracle: ... where name = ?).
func TestWhereRaw_RendersTheFragmentVerbatim(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).WhereRaw("name = ?", "x").(*OrmBuilder)
	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where name = ?`, sql)
	assert.Equal(t, []any{"x"}, bindings)
}

// OrWhereRaw keeps the "or" connector, mirroring Builder::orWhereRaw.
func TestOrWhereRaw_KeepsTheOrConnector(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).Where("id", 1).OrWhereRaw("name = ?", "x").(*OrmBuilder)
	query = query.dest(&[]relUser{}).addGlobalScopes().buildConditions()

	sql, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where "id" = ? or name = ?`, sql)
	assert.Equal(t, []any{1, "x"}, bindings)
}
