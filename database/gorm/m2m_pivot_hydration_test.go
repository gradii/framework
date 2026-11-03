package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/database/gorm/relations"
)

// Per-parent pivot hydration. The eager many-to-many loader reads the related rows through the
// joined relation query — one row per pivot row, the pivot columns aliased pivot_<column> — and
// groups them per parent by the pivot parent key's alias, mirroring BelongsToMany::match's
// `$model->pivot->getAttribute($this->foreignPivotKey)`. Each joined row is scanned into its own
// related instance, so the same related row attached to two parents yields two instances carrying
// their own pivot attributes (the bug PR #1463 flagged: keying pivot data by related id only let one
// parent's values overwrite another's).
func TestLoadMany2Many_PerParentPivot(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &pivotUser{})
	desc, err := relations.ResolveRelation(&pivotUser{}, "Roles")
	assert.NoError(t, err)
	if err != nil {
		return
	}

	parentCol := desc.PivotParentRef.ForeignColumn
	relatedCol := desc.PivotRelatedRef.ForeignColumn

	// Both parents attach role 99 with different pivot attributes: two joined rows.
	recorder.returnRows(`from "`+desc.RelatedTable+`"`,
		[]string{"id", "name", "pivot_" + parentCol, "pivot_" + relatedCol, "pivot_user_id", "pivot_role_id", "pivot_priority", "pivot_notes"},
		[]driver.Value{int64(99), "admin", int64(1), int64(99), int64(1), int64(99), "high", "one"},
		[]driver.Value{int64(99), "admin", int64(2), int64(99), int64(2), int64(99), "low", "two"},
	)

	u1 := &pivotUser{ID: 1}
	u2 := &pivotUser{ID: 2}
	parents := []reflect.Value{reflect.ValueOf(u1).Elem(), reflect.ValueOf(u2).Elem()}

	assert.NoError(t, q.loadMany2Many(parents, &pivotUser{}, desc, eagerLoadEntry{Relation: "Roles"}, nil))

	if !assert.Len(t, u1.Roles, 1) || !assert.Len(t, u2.Roles, 1) {
		return
	}
	assert.NotSame(t, u1.Roles[0], u2.Roles[0], "each joined row hydrates its own instance")
	assert.Equal(t, "high", u1.Roles[0].Pivot.Priority)
	assert.Equal(t, "one", u1.Roles[0].Pivot.Notes)
	assert.Equal(t, "low", u2.Roles[0].Pivot.Priority)
	assert.Equal(t, "two", u2.Roles[0].Pivot.Notes)
}

// Without a Pivot field the loader still returns one instance per joined row — the hydration simply
// writes nothing.
func TestLoadMany2Many_NoPivotField_DistinctInstances(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})
	desc, err := relations.ResolveRelation(&relUser{}, "Roles")
	assert.NoError(t, err)
	if err != nil {
		return
	}

	parentCol := desc.PivotParentRef.ForeignColumn
	recorder.returnRows(`from "rel_roles"`,
		[]string{"id", "name", "pivot_" + parentCol},
		[]driver.Value{int64(99), "admin", int64(1)},
		[]driver.Value{int64(99), "admin", int64(2)},
	)

	u1 := &relUser{ID: 1}
	u2 := &relUser{ID: 2}
	parents := []reflect.Value{reflect.ValueOf(u1).Elem(), reflect.ValueOf(u2).Elem()}

	assert.NoError(t, q.loadMany2Many(parents, &relUser{}, desc, eagerLoadEntry{Relation: "Roles"}, nil))

	if !assert.Len(t, u1.Roles, 1) || !assert.Len(t, u2.Roles, 1) {
		return
	}
	assert.NotSame(t, u1.Roles[0], u2.Roles[0], "each joined row is its own instance")
}
