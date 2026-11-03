package gorm

// GetCountForPagination pins the statement the paginator's count runs, measured against Laravel's
// Builder::getCountForPagination (oracle: .claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1). With no groups the count drops the select list, the orders and
// the limit/offset — and the select bindings — while a grouped query wraps its select in a derived
// table named "aggregate_table", the select list (sub-selects and their bindings included) intact.

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCountForPagination_DropsColumnsOrdersAndLimits(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)
	query = query.Select("name").Where("id", 9).OrderBy("id").Limit(5).(*OrmBuilder)

	recorder.returnRows("select count(*)", []string{"aggregate"}, []driver.Value{int64(7)})

	count, err := query.GetCountForPagination()
	require.NoError(t, err)

	assert.Equal(t, int64(7), count)
	assert.Equal(t, `select count(*) as aggregate from "users" where "id" = ?`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(9)}, recorder.last(t).args)
}

func TestGetCountForPagination_WrapsGroupsAndKeepsSelectBindings(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)
	query = query.Select("name").SelectRaw("? as flag", 1).GroupBy("name").(*OrmBuilder)

	recorder.returnRows("select count(*)", []string{"aggregate"}, []driver.Value{int64(3)})

	count, err := query.GetCountForPagination()
	require.NoError(t, err)

	assert.Equal(t, int64(3), count)
	assert.Equal(t, `select count(*) as aggregate from (select "name", ? as flag from "users" group by "name") as "aggregate_table"`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, recorder.last(t).args)
}
