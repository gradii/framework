package relations_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// detachUser / detachRole are the many-to-many fixtures for the detach edge-shape pins below.
type detachUser struct {
	ID    uint          `gorm:"column:id;primaryKey"`
	Roles []*detachRole `gorm:"-"`
}

func (detachUser) TableName() string { return "detach_users" }

func (detachUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{Related: &detachRole{}, Table: "detach_role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id"},
	}
}

type detachRole struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (detachRole) TableName() string { return "detach_roles" }

// TestDetachRelation_EmptyIDsIsANoOp pins Laravel's detach([]): parseIds reads the explicitly
// empty list and the method returns 0 before any statement (vendor v12.55.1,
// InteractsWithPivotTable.php:530-535). Oracle v12.55.1, executed
// (/tmp/lalign-cleanups/detach_empty.php): "detach([]) return => 0", "detach([]) queries => []";
// detachOrFail([]) likewise "=> 0" with no queries.
func TestDetachRelation_EmptyIDsIsANoOp(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	removed, err := builder.DetachRelation(&detachUser{ID: 7}, "Roles", []any{})
	require.NoError(t, err)
	assert.EqualValues(t, 0, removed)
	assert.Empty(t, recorder.take(), "an explicitly empty id list must not reach the database")
}

// TestDetachRelation_NilIDsDetachesEveryRow: a nil list is Laravel's detach(null) and deletes
// every pivot row of the parent — oracle: "detach(null) return => 3", one delete carrying only
// the parent constraint (no in-list).
func TestDetachRelation_NilIDsDetachesEveryRow(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	_, err := builder.DetachRelation(&detachUser{ID: 7}, "Roles", nil)
	require.NoError(t, err)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.True(t, strings.HasPrefix(statements[0].query, `delete from "detach_role_user" where `), statements[0].query)
	assert.NotContains(t, statements[0].query, " in (")
}

// TestDetachRelation_IDListConstrainsWithIn: a non-empty list adds the whereIn on the related
// pivot key — oracle: "detach([2]) return => 1", `... and "de_post_tag"."tag_id" in (?) || [1,2]`.
func TestDetachRelation_IDListConstrainsWithIn(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	_, err := builder.DetachRelation(&detachUser{ID: 7}, "Roles", []any{1, 2})
	require.NoError(t, err)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Contains(t, statements[0].query, `"role_id" in (`)
}

// TestDetachRelation_SingleEmptySliceArgIsANoOp pins the second Go spelling of Laravel's
// detach([]): Detach([]any{}) hands the variadic one empty list — read as the empty list it is
// (0, no statement). An empty []byte is an id value, not an empty list (the guard's []byte
// exclusion), so that shape proceeds to the guarded delete.
func TestDetachRelation_SingleEmptySliceArgIsANoOp(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	removed, err := builder.DetachRelation(&detachUser{ID: 7}, "Roles", []any{[]any{}})
	require.NoError(t, err)
	assert.EqualValues(t, 0, removed)
	assert.Empty(t, recorder.take(), "a single empty-slice argument must not reach the database")

	removed, err = builder.DetachRelation(&detachUser{ID: 7}, "Roles", []any{[]byte{}})
	require.NoError(t, err)
	assert.EqualValues(t, 0, removed)

	statements := recorder.take()
	require.Len(t, statements, 1, "an empty []byte reads as an id value, so the delete runs")
	assert.Contains(t, statements[0].query, `"role_id" in (`)
}
