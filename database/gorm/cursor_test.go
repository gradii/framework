package gorm

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
)

// newCursorQuery builds a query over a model whose compiled statements are recorded by the in-package
// stub driver, so a facade test can assert the SQL CursorPaginate emits without a live database. The
// connection names MySQL, which is the grammar the identifiers below are pinned against.
func newCursorQuery(t *testing.T, conditions Conditions) (*Query, *recordingSQLDriver) {
	t.Helper()

	recorder := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })

	return newDialectQuery(context.Background(), nil, contractsdatabase.Config{Driver: "mysql"}, sqlDB, nil, nil, nil, nil, &conditions, nil, mysqlTestComponents()), recorder
}

// TestCursorPaginateFirstPage pins the statement a first page compiles to: the query's ORDER BY, and
// the perPage+1 probe limit, with no keyset predicate and no cursor.
func TestCursorPaginateFirstPage(t *testing.T) {
	query, recorder := newCursorQuery(t, Conditions{model: &relUser{}})
	query = query.OrderBy("id").(*Query)

	var items []map[string]any

	page, err := query.CursorPaginate(15, "", &items)
	require.NoError(t, err)

	assert.Equal(t, "select * from `rel_users` order by `id` asc limit 16", recorder.last(t).sql)
	assert.Equal(t, 15, page.PerPage)
	assert.False(t, page.HasMorePages)
	assert.Nil(t, page.NextCursor)
	assert.Nil(t, page.PreviousCursor)
	assert.Empty(t, page.Items)
}

// TestCursorPaginateWithCursor pins the keyset predicate the ORM compiles for an encoded cursor: the
// single comparison for a one-column order, ahead of the ORDER BY and the probe limit.
func TestCursorPaginateWithCursor(t *testing.T) {
	query, recorder := newCursorQuery(t, Conditions{model: &relUser{}})
	query = query.OrderBy("id").(*Query)

	encoded, err := querybuilder.NewCursor(querybuilder.CursorParameters{{Column: "id", Value: 5}}, true).Encode()
	require.NoError(t, err)

	var items []map[string]any

	if _, err := query.CursorPaginate(15, encoded, &items); err != nil {
		require.NoError(t, err)
	}

	// The ORM stores orders as raw fragments, which the facade converts back into columns and
	// directions so the predicate can be built; the emitted ORDER BY must be unchanged by that.
	assert.Equal(t, "select * from `rel_users` where (`id` > ?) order by `id` asc limit 16", recorder.last(t).sql)
}

// TestCursorPaginateRejectsAMalformedCursor pins that a non-empty cursor which cannot be decoded is
// refused rather than silently restarting at the first page.
func TestCursorPaginateRejectsAMalformedCursor(t *testing.T) {
	query, _ := newCursorQuery(t, Conditions{model: &relUser{}})
	query = query.OrderBy("id").(*Query)

	var items []map[string]any

	_, err := query.CursorPaginate(15, "not-a-cursor", &items)
	assert.ErrorIs(t, err, errors.QueryCursorInvalid)
}

// TestCursorPaginateRequiresAnOrder pins that a query with no ORDER BY cannot be keyset-paginated.
func TestCursorPaginateRequiresAnOrder(t *testing.T) {
	query, _ := newCursorQuery(t, Conditions{model: &relUser{}})

	var items []map[string]any

	_, err := query.CursorPaginate(15, "", &items)
	assert.ErrorIs(t, err, errors.QueryOrderRequired)
}
