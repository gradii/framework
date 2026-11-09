package tests

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/sqlserver"
)

// TestExistsRows checks exists() against real rows on every available driver, SQL Server included
// when GRADII_TEST_SQLSERVER_DSN names one (its grammar's form, `select top 1 1 [exists] ...`, returns
// no row when nothing matches). The expectations are Laravel's own answers for the same table on
// sqlite, MySQL 9.7, PostgreSQL 18.3 and SQL Server 2022 (illuminate/database v12.55.1,
// /Users/dog/.claude/jobs/013c27a0/tmp/ofa/exists_probe.php -> exists_probe.out ROWS, and
// exists_sqlsrv_probe.php -> exists_sqlsrv_probe.out): a query keeps
// its own limit inside the exists, and exists() leaves the query as it was, so a later Get reads
// every row instead of the one row the former `limit 1` rewrite left behind.
func TestExistsRows(t *testing.T) {
	queries := NewTestQueryBuilder().All("", false)
	if queries[sqlserver.Name] == nil && os.Getenv("GRADII_TEST_SQLSERVER_DSN") != "" {
		queries[sqlserver.Name] = NewTestQueryBuilder().Sqlserver("", false)
	}

	for name, fixture := range queries {
		t.Run(name, func(t *testing.T) {
			fixture.CreateTable(TestTableUsers)
			for _, user := range []string{"exists_rows_a", "exists_rows_b", "exists_rows_c"} {
				require.NoError(t, fixture.Query().Create(&User{Name: user}))
			}

			exists, err := fixture.Query().Model(&User{}).Where("name", "like", "exists_rows_%").Exists()
			require.NoError(t, err)
			assert.True(t, exists, "plain")

			// The inner select keeps a limit of zero, so nothing matches - except on SQL Server, whose
			// compileExists replaces the limit with `top 1` (Laravel answers true there too: real
			// SQL Server 2022, /Users/dog/.claude/jobs/013c27a0/tmp/ofa/exists_sqlsrv_probe.php).
			exists, err = fixture.Query().Model(&User{}).Where("name", "like", "exists_rows_%").Limit(0).Exists()
			require.NoError(t, err)
			assert.Equal(t, name == "SQL Server", exists, "limit 0")

			exists, err = fixture.Query().Model(&User{}).Where("name", "like", "exists_rows_%").OrderBy("name").Limit(2).Exists()
			require.NoError(t, err)
			assert.True(t, exists, "order and limit")

			exists, err = fixture.Query().Model(&User{}).Where("name", "missing_exists_rows_user").Exists()
			require.NoError(t, err)
			assert.False(t, exists, "no match")

			query := fixture.Query().Model(&User{}).Where("name", "like", "exists_rows_%")
			exists, err = query.Exists()
			require.NoError(t, err)
			require.True(t, exists)
			var users []User
			require.NoError(t, query.Get(&users))
			assert.Len(t, users, 3, "exists() must not leave a limit on the query")
		})
	}
}
