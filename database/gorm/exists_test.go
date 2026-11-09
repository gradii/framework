package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exists mirrors Query\Builder::exists: it runs the grammar's compileExists over the query and leaves
// the query as it was. Every statement below is the oracle's (illuminate/database v12.55.1,
// /Users/dog/.claude/jobs/013c27a0/tmp/ofa/exists_probe.php -> exists_probe.out); the inner select
// keeps the caller's own limit and order, which the former `limit 1` rewrite overwrote.

type existsRow struct {
	ID   uint
	Name string
}

func (existsRow) TableName() string { return "t" }

func TestExists_CompilesLaravelExistsStatement(t *testing.T) {
	for _, tt := range []struct {
		name  string
		build func(q *OrmBuilder) *OrmBuilder
		sql   string
		value int64
		want  bool
	}{
		{"plain", func(q *OrmBuilder) *OrmBuilder { return q.Where("id", ">", 0).(*OrmBuilder) },
			`select exists(select * from "t" where "id" > ?) as "exists"`, 1, true},
		{"limit0", func(q *OrmBuilder) *OrmBuilder { return q.Where("id", ">", 0).Limit(0).(*OrmBuilder) },
			`select exists(select * from "t" where "id" > ? limit 0) as "exists"`, 0, false},
		{"order_limit", func(q *OrmBuilder) *OrmBuilder {
			return q.Where("id", ">", 0).OrderBy("name").Limit(2).(*OrmBuilder)
		}, `select exists(select * from "t" where "id" > ? order by "name" asc limit 2) as "exists"`, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, &existsRow{})
			recorder.returnRows(`select exists(`, []string{"exists"}, []driver.Value{tt.value})
			query := tt.build(q)

			exists, err := query.Exists()
			require.NoError(t, err)
			assert.Equal(t, tt.want, exists)

			statement := recorder.last(t)
			assert.Equal(t, tt.sql, statement.sql)
			assert.Equal(t, []driver.Value{int64(0)}, statement.args)
		})
	}
}

// TestExists_NoRowIsFalse covers the dialect form that returns no row when nothing matches (SQL
// Server's `select top 1 1 [exists] ...`): no row answers false, as Laravel's `isset($results[0])`.
func TestExists_NoRowIsFalse(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &existsRow{})

	exists, err := q.Where("id", ">", 0).Exists()
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestExists_LeavesTheQueryAsItWas pins that exists() compiles from a copy: the query compiles the
// same select before and after, so a later Get is neither limited nor reordered (oracle
// exists_probe.out ROWS: `$q->exists(); $q->get()` reads all three rows on every engine).
func TestExists_LeavesTheQueryAsItWas(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &existsRow{})
	recorder.returnRows(`select exists(`, []string{"exists"}, []driver.Value{int64(1)})
	query := q.Where("id", ">", 0).OrderBy("name").(*OrmBuilder)

	before, beforeBindings, err := query.compileSelect()
	require.NoError(t, err)

	_, err = query.Exists()
	require.NoError(t, err)

	after, afterBindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "t" where "id" > ? order by "name" asc`, after)
	assert.Equal(t, before, after)
	assert.Equal(t, beforeBindings, afterBindings)
}
