package db

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDialectGroupLimitUsesLegacyMySQLSQL pins that a MySQL connection older than 8.0.11 compiles the
// group limit through Laravel's legacy rewrite, which numbers rows with the @laravel_row session
// variable. The modern rewrite (row_number() over (...)) is what the base grammar produces and is
// pinned in database/db.
func TestDialectGroupLimitUsesLegacyMySQLSQL(t *testing.T) {
	options := contractsdriver.GrammarOptions{Driver: "mysql", ServerVersion: func() (string, error) { return "5.7.44", nil }}
	query := dialectQueryWith(t, "mysql", "users", options, nil, nil, nil, nil)

	sql := query.GroupLimit(2, "team_id").ToSql().Get()
	assert.Contains(t, sql, "@laravel_row")

	modern := dialectQueryWith(t, "mysql", "users", contractsdriver.GrammarOptions{Driver: "mysql", ServerVersion: func() (string, error) { return "8.0.11", nil }}, nil, nil, nil, nil)
	assert.Contains(t, modern.GroupLimit(2, "team_id").ToSql().Get(), "row_number() over")
}

// groupLimitStubQuery builds a query over a stub pool that answers every statement with the given
// canned result set, on the driver's real dialect. The stub stands in for the server's rows, so a
// test pins what the framework does with the columns a group-limited read returns, not the SQL it
// sends.
func groupLimitStubQuery(t *testing.T, name string, options contractsdriver.GrammarOptions, columns []string, values [][]driver.Value) *db.Query {
	t.Helper()

	pool := sql.OpenDB(&structMappingConnector{columns: columns, values: values})
	t.Cleanup(func() { assert.NoError(t, pool.Close()) })

	builder, err := db.NewBuilder(pool, name)
	require.NoError(t, err)

	logs := []db.TxLog{}

	return dialectQueryWith(t, name, "gl_posts", options, builder, builder, nil, &logs)
}

// TestGroupLimitStripsBookkeepingColumnsFromMapDestinations pins Query\Builder::withoutGroupLimitKeys()
// (illuminate/database v12.55.1, Query/Builder.php:3516) on the legacy MySQL rewrite: the server's
// result set carries `laravel_row` plus the unaliased `@laravel_group := `team_id` columns (MySQL
// names them after their expressions), and a group-limited get() removes all of them from a map
// destination. Oracle: /tmp/strip-laravel-row/p17b.php P17b.2 (a raw select keeps laravel_row) and
// P17b.6 (the MySQL-grammar key set the method removes).
func TestGroupLimitStripsBookkeepingColumnsFromMapDestinations(t *testing.T) {
	options := contractsdriver.GrammarOptions{Driver: "mysql", ServerVersion: func() (string, error) { return "5.7.44", nil }}
	query := groupLimitStubQuery(t, "mysql", options,
		[]string{"id", "laravel_row", "@laravel_group := `team_id`", "@laravel_group := `pivot_team_id`"},
		[][]driver.Value{{int64(1), int64(1), int64(1), int64(1)}})

	var rows []map[string]any
	require.NoError(t, query.GroupLimit(2, "gl_posts.team_id").Get(&rows))

	require.Len(t, rows, 1)
	assert.Equal(t, map[string]any{"id": int64(1)}, rows[0])
}

// TestGroupLimitLeavesStructDestinationsUnchanged: the row scanner fills declared fields only, so
// the rewrite's bookkeeping columns never reach a struct destination and the strip has nothing to
// remove.
func TestGroupLimitLeavesStructDestinationsUnchanged(t *testing.T) {
	type item struct {
		ID     int64 `db:"id"`
		TeamID int64 `db:"team_id"`
	}

	query := groupLimitStubQuery(t, "sqlite", contractsdriver.GrammarOptions{Driver: "sqlite"},
		[]string{"id", "team_id", "laravel_row"},
		[][]driver.Value{{int64(1), int64(7), int64(1)}})

	var items []item
	require.NoError(t, query.GroupLimit(2, "team_id").Get(&items))

	assert.Equal(t, []item{{ID: 1, TeamID: 7}}, items)
}
