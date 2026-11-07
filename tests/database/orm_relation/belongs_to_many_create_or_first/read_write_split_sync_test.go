package belongs_to_many_create_or_first

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkdb "github.com/gradii/framework/database/db"
)

// This file pins R15-4 (round 15b): InteractsWithPivotTable::sync returns before any statement
// when the record list is empty and the call is not detaching (vendor v12.55.1,
// Concerns/InteractsWithPivotTable.php:106, `if (empty($records) && ! $detaching) return $changes;`).
// A detaching sync still reads the current pivot rows (:113) to know what to detach, and toggle has
// no such check — it plucks the current related ids whatever it was given (:34).
//
// The read/write-split fixture makes the read observable: the replica has no pivot_table, so any
// current-ids read fails with "no such table", while a call that never reads succeeds.
//
// ORACLE (fresh, executed: /tmp/r15b/sync_empty.php — illuminate/database v12.55.1, Capsule with
// distinct 'read'/'write' sqlite files, the pivot table dropped on the read file):
//
//	A1 syncWithoutDetaching([])              result={"attached":[],"detached":[],"updated":[]} log=[]
//	A2 sync([],false)                        result={"attached":[],"detached":[],"updated":[]} log=[]
//	A3 syncWithPivotValues([],[x],false)     result={"attached":[],"detached":[],"updated":[]} log=[]
//	A7 syncWithoutDetachingOrFail([])        result={"attached":[],"detached":[],"updated":[]} log=[]
//	A4 sync([]) detaching                    THROW QueryException ... no such table: pivot_table
//	A5 toggle([])                            THROW QueryException ... no such table: pivot_table
//	A6 syncWithPivotValues([],[x]) detaching THROW QueryException ... no such table: pivot_table
//
// NON-VACUITY: the detaching and toggle legs below go through the same fixture and must fail on the
// replica, so the empty log of the non-detaching legs is the short-circuit, not a read routed to the
// writer. Removing the early return (database/gorm/relations/pivot_workers.go, SyncCore and
// SyncCoreWithPivot) turns the non-detaching legs red on their require.NoError.
func TestBelongsToManySync_EmptyWithoutDetachingRunsNoStatement(t *testing.T) {
	dir := t.TempDir()
	writeDsn := "file:" + filepath.ToSlash(filepath.Join(dir, "write.db")) + "?_pragma=foreign_keys(1)"
	readDsn := "file:" + filepath.ToSlash(filepath.Join(dir, "read.db")) + "?_pragma=foreign_keys(1)"

	split := newSplitRelationQuery(t, writeDsn, readDsn)

	belongsToManyCreateOrFirstSchema(t, split.query)
	for _, statement := range belongsToManyCreateOrFirstSchemaStatements() {
		_, err := split.reader.ExecContext(context.Background(), statement)
		require.NoError(t, err, statement)
	}

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, split.query).Create(&seed))
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	seeder, ok := cloneRelationQuery(t, split.query).Related(source, "Related").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")
	require.NoError(t, seeder.Attach([]any{uint64(456)}))

	// The replica loses its pivot table: a current-ids read routed there now fails.
	_, err := split.reader.ExecContext(context.Background(), `drop table pivot_table`)
	require.NoError(t, err)

	ctx := frameworkdb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, split.query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")
	related := func() contractsorm.BelongsToManyRelation {
		relation, ok := withContext.WithContext(ctx).Related(source, "Related").(contractsorm.BelongsToManyRelation)
		require.True(t, ok, "Related must return a many-to-many relation object")

		return relation
	}

	// A1/A2: syncWithoutDetaching([]) returns the empty change set before any statement.
	result, err := related().SyncWithoutDetaching([]any{})
	require.NoError(t, err, "an empty non-detaching sync must not read the pivot table")
	require.NotNil(t, result)
	assert.Empty(t, result.Attached)
	assert.Empty(t, result.Detached)
	assert.Empty(t, result.Updated)

	// A3: the per-id-values form (Laravel syncWithPivotValues([], ..., false) is the same sync call).
	result, err = related().SyncWithoutDetachingWithPivot(map[any]map[string]any{})
	require.NoError(t, err, "an empty non-detaching sync with pivot values must not read the pivot table")
	require.NotNil(t, result)
	assert.Empty(t, result.Attached)
	assert.Empty(t, result.Detached)
	assert.Empty(t, result.Updated)

	// A7: the transaction form short-circuits too.
	result, err = related().SyncWithoutDetachingOrFail([]any{})
	require.NoError(t, err, "an empty non-detaching syncOrFail must not read the pivot table")
	require.NotNil(t, result)
	assert.Empty(t, result.Attached)

	assert.Empty(t, frameworkdb.GetQueryLog(ctx), "the empty non-detaching syncs run no statement")

	// A4/A6: a detaching sync([]) still reads the current pivot rows — on the replica, which fails.
	_, err = related().Sync([]any{})
	require.Error(t, err, "sync([]) detaching reads the current pivot rows")
	assert.Contains(t, err.Error(), "no such table")
	_, err = related().SyncWithPivotValues([]any{}, map[string]any{"x": 1})
	require.Error(t, err, "syncWithPivotValues([]) detaching reads the current pivot rows")
	assert.Contains(t, err.Error(), "no such table")

	// A5: toggle([]) has no empty check and plucks the current ids.
	_, err = related().Toggle([]any{})
	require.Error(t, err, "toggle([]) plucks the current ids")
	assert.Contains(t, err.Error(), "no such table")
	// The per-id-values toggle runs through SyncCoreWithPivot, whose guard must spare toggle too.
	_, err = related().ToggleWithPivot(map[any]map[string]any{})
	require.Error(t, err, "toggleWithPivot([]) plucks the current ids")
	assert.Contains(t, err.Error(), "no such table")

	// The writer's pivot row is untouched throughout.
	assert.Equal(t, int64(1), countRows(t, split.writer, "pivot_table"))

	// Control: with the replica's pivot table restored, a non-empty syncWithoutDetaching reads the
	// current ids there and attaches on the writer, and the same log records both statements — so
	// the empty log above is the short-circuit, not a log that records nothing.
	_, err = split.reader.ExecContext(context.Background(), `create table pivot_table (source_id integer not null, related_id integer not null)`)
	require.NoError(t, err)
	result, err = related().SyncWithoutDetaching([]any{uint64(789)})
	require.NoError(t, err)
	assert.Equal(t, []any{uint64(789)}, result.Attached)
	// The log keeps failed statements too: the four failing reads above (two detaching syncs, two
	// toggle plucks) precede the control's read and insert.
	logs := frameworkdb.GetQueryLog(ctx)
	require.Len(t, logs, 6)
	assert.Contains(t, logs[0].Query, `select * from "pivot_table"`)
	assert.Contains(t, logs[1].Query, `select * from "pivot_table"`)
	assert.Contains(t, logs[2].Query, `select "related_id" from "pivot_table"`)
	assert.Contains(t, logs[3].Query, `select "related_id" from "pivot_table"`)
	assert.Contains(t, logs[4].Query, `select * from "pivot_table"`)
	assert.Contains(t, logs[5].Query, `insert into "pivot_table"`)
}
