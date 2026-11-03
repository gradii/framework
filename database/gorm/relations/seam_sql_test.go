package relations_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasegorm "github.com/gradii/framework/database/gorm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/support/carbon"
)

// The S2 seam widening, step 1: the row primitives (W4-W7) and the single-row read are pinned
// byte for byte against the statements the legacy Tier-2 workers run today — the same real
// *gorm.OrmBuilder on the recording transport runs the legacy shell first and the primitive
// second, and the two recordings must be equal (query text and bindings, the call-time
// timestamps masked). This is the evidence the later port step relies on: a worker rebuilt over
// (Builder, RelationInfo) assembles the same query state the gorm original does.

// seamSQLUser / seamSQLRole: a many-to-many with pivot timestamps and a pivot scope, so every
// primitive exercises the stamps and the applyOnPivotQueryToQuery replay.
type seamSQLUser struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
	Roles     []*seamSQLRole   `gorm:"-"`
}

func (seamSQLUser) TableName() string { return "seam_users" }

func (seamSQLUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{
			Related:         &seamSQLRole{},
			Table:           "seam_role_user",
			ForeignPivotKey: "user_id",
			RelatedPivotKey: "role_id",
			PivotTimestamps: true,
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("active", 1)
			},
		},
	}
}

type seamSQLRole struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (seamSQLRole) TableName() string { return "seam_roles" }

func seamRelationInfo(t *testing.T, builder relations.Builder) relations.RelationInfo {
	t.Helper()

	info, err := builder.RelationInfoOf(&seamSQLUser{ID: 7}, "Roles")
	require.NoError(t, err)

	return info
}

// maskTimes replaces the time.Time bindings — the time.Now() stamps the pivot writers take at
// call time — with a marker, so two statements compiled moments apart compare equal.
func maskTimes(statements []recordedStatement) []recordedStatement {
	out := make([]recordedStatement, len(statements))
	for i, statement := range statements {
		args := make([]any, len(statement.args))
		for j, arg := range statement.args {
			if _, ok := arg.(time.Time); ok {
				args[j] = "<time>"
			} else {
				args[j] = arg
			}
		}
		out[i] = recordedStatement{query: statement.query, args: args}
	}

	return out
}

// TestSeam_DeleteRows_IsByteIdenticalToDetach: DetachRelation's doDetach versus DeleteRows fed
// the same three predicates (parent equality, related in-list, the pivot scope).
func TestSeam_DeleteRows_IsByteIdenticalToDetach(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	var seam relations.Builder = builder
	info := seamRelationInfo(t, seam)

	_, err := builder.DetachRelation(&seamSQLUser{ID: 7}, "Roles", []any{1, 2})
	require.NoError(t, err)
	legacy := recorder.take()
	require.Len(t, legacy, 1)

	wheres := []relations.RowWhere{
		{Query: seam.Ident(info.PivotTable, info.PivotParentRef.ForeignColumn) + " = ?", Args: []any{uint(7)}},
		{Query: info.PivotTable + "." + info.PivotRelatedRef.ForeignColumn, Args: []any{[]any{1, 2}}},
	}
	_, err = seam.DeleteRows(info.PivotTable, wheres, info.OnPivotQuery)
	require.NoError(t, err)
	ported := recorder.take()
	require.Len(t, ported, 1)

	assert.Equal(t, legacy, ported)
	assert.Contains(t, ported[0].query, `delete from "seam_role_user" where`)
	assert.Contains(t, ported[0].query, `"seam_role_user"."role_id" in (`)
	assert.Contains(t, ported[0].query, `"seam_role_user"."active" = ?`, "the pivot scope replays last")
}

// TestSeam_UpdateRows_IsByteIdenticalToUpdateExistingPivot: doUpdateExistingPivot versus
// UpdateRows with the same predicates and the same value map (attrs plus the updated-at stamp).
func TestSeam_UpdateRows_IsByteIdenticalToUpdateExistingPivot(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	var seam relations.Builder = builder
	info := seamRelationInfo(t, seam)

	_, err := builder.UpdateExistingPivotRelation(&seamSQLUser{ID: 7}, "Roles", 2, map[string]any{"note": "x"})
	require.NoError(t, err)
	legacy := recorder.take()
	require.Len(t, legacy, 1)

	wheres := []relations.RowWhere{
		{Query: seam.Ident(info.PivotTable, info.PivotParentRef.ForeignColumn) + " = ?", Args: []any{uint(7)}},
		{Query: seam.Ident(info.PivotTable, info.PivotRelatedRef.ForeignColumn) + " = ?", Args: []any{2}},
	}
	values := map[string]any{"note": "x", info.PivotUpdatedAtColumn: time.Now()}
	_, err = seam.UpdateRows(info.PivotTable, wheres, info.OnPivotQuery, values)
	require.NoError(t, err)
	ported := recorder.take()
	require.Len(t, ported, 1)

	assert.Equal(t, maskTimes(legacy), maskTimes(ported))
	assert.Contains(t, ported[0].query, `update "seam_role_user" set "note" = ?, "updated_at" = ? where`)
	assert.Contains(t, ported[0].query, `"seam_role_user"."active" = ?`)
}

// TestSeam_SelectRowsAndInsertRows_AreByteIdenticalToAttach: AttachRelation reads the existing
// ids (existingPivotIDs) and inserts the new rows (insertPivotRows via basePivotRow); SelectRows
// and InsertRows reproduce both statements.
func TestSeam_SelectRowsAndInsertRows_AreByteIdenticalToAttach(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	var seam relations.Builder = builder
	info := seamRelationInfo(t, seam)

	require.NoError(t, builder.AttachRelation(&seamSQLUser{ID: 7}, "Roles", []any{3}))
	legacy := recorder.take()
	require.Len(t, legacy, 2, "attach reads the existing ids, then inserts")

	wheres := []relations.RowWhere{
		{Query: seam.Ident(info.PivotTable, info.PivotParentRef.ForeignColumn) + " = ?", Args: []any{uint(7)}},
		{Query: info.PivotTable + "." + info.PivotRelatedRef.ForeignColumn, Args: []any{[]any{3}}},
	}
	rows, err := seam.SelectRows(info.PivotTable, []string{info.PivotRelatedRef.ForeignColumn}, wheres, info.OnPivotQuery)
	require.NoError(t, err)
	assert.Empty(t, rows, "the recording transport answers every read with no rows")

	now := time.Now()
	require.NoError(t, seam.InsertRows(info.PivotTable, []map[string]any{{
		info.PivotParentRef.ForeignColumn:  uint(7),
		info.PivotRelatedRef.ForeignColumn: 3,
		info.PivotCreatedAtColumn:          now,
		info.PivotUpdatedAtColumn:          now,
	}}))
	ported := recorder.take()
	require.Len(t, ported, 2)

	assert.Equal(t, maskTimes(legacy), maskTimes(ported))
	assert.Contains(t, ported[0].query, `from "seam_role_user" where`)
	assert.Contains(t, ported[0].query, `"seam_role_user"."active" = ?`, "the read replays the pivot scope")
	assert.Contains(t, ported[1].query, `insert into "seam_role_user" (`)
	assert.NotContains(t, ported[1].query, "active", "the insert replays no pivot scope — Laravel's attach does not either")
}

// TestSeam_SelectRows_IsByteIdenticalToSyncRead: an empty sync runs exactly allPivotIDs — the
// parent-only read, no in-list — which SelectRows reproduces.
func TestSeam_SelectRows_IsByteIdenticalToSyncRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	var seam relations.Builder = builder
	info := seamRelationInfo(t, seam)

	_, err := builder.SyncRelation(&seamSQLUser{ID: 7}, "Roles", []any{})
	require.NoError(t, err)
	legacy := recorder.take()
	require.Len(t, legacy, 1, "nothing current, nothing wanted: the read alone")

	wheres := []relations.RowWhere{
		{Query: seam.Ident(info.PivotTable, info.PivotParentRef.ForeignColumn) + " = ?", Args: []any{uint(7)}},
	}
	_, err = seam.SelectRows(info.PivotTable, []string{info.PivotRelatedRef.ForeignColumn}, wheres, info.OnPivotQuery)
	require.NoError(t, err)
	ported := recorder.take()
	require.Len(t, ported, 1)

	assert.Equal(t, legacy, ported)
	assert.NotContains(t, ported[0].query, " in (")
}

// TestSeam_UpdateRows_ParentTouchShape pins the statement touchIfTouching compiles for the
// parent bump — the fresh builder bound to the parent table, one primary-key equality, no scope —
// the shape the touch worker will hand UpdateRows once ported (the legacy worker only runs after
// a write reports affected rows, which the recording transport never does).
func TestSeam_UpdateRows_ParentTouchShape(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	var seam relations.Builder = builder

	affected, err := seam.UpdateRows("seam_users", []relations.RowWhere{
		{Query: seam.Ident("id") + " = ?", Args: []any{uint(7)}},
	}, nil, map[string]any{"updated_at": time.Now()})
	require.NoError(t, err)
	assert.EqualValues(t, 0, affected)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `update "seam_users" set "updated_at" = ? where "id" = ?`, statements[0].query)
	assert.Len(t, statements[0].args, 2)
}

// TestSeam_FirstFound_ReportsTheMiss: the single-row read behind the find-then-write family
// reports false (not an error) when the statement matches nothing.
func TestSeam_FirstFound_ReportsTheMiss(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	seam, ok := relations.BuilderOf(builder.Model(&seamSQLRole{}).Where("id", 1))
	require.True(t, ok)

	var role seamSQLRole
	found, err := seam.FirstFound(&role)
	require.NoError(t, err)
	assert.False(t, found)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Contains(t, statements[0].query, `from "seam_roles" where`)
	assert.Contains(t, statements[0].query, "limit 1")
}

// TestSeam_BuilderOf_UnwrapsARelationObjectToItsOrmBuilder: the (*OrmBuilder) casts the workers
// make on a relation query re-home onto BuilderOf, which reaches the engine builder behind the
// relation object.
func TestSeam_BuilderOf_UnwrapsARelationObjectToItsOrmBuilder(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	rel := builder.Related(&seamSQLUser{ID: 7}, "Roles")
	seam, ok := relations.BuilderOf(rel)
	require.True(t, ok)
	_, isOrmBuilder := seam.(*databasegorm.OrmBuilder)
	assert.True(t, isOrmBuilder, "%T", seam)
	assert.False(t, seam == relations.Builder(builder), "the relation read runs on its own fresh builder")
}
