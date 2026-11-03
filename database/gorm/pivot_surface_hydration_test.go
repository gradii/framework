package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkerrors "github.com/gradii/framework/errors"
)

// This file drives the pivot-accessor surface's hydration half (item lalign-pivot-surface)
// through a real relation read on the recording transport: the object's As() re-targets which
// related-model field the read hydrates (and the aliased SELECT carries the named field's
// columns), WithPivot entries without a declared field are selected but skipped, and an accessor
// naming a non-struct field errors the read. Oracle: /tmp/lalign-probes/pivot_surface.out
// (illuminate/database v12.55.1, sqlite).

// --- fixtures ---

type phUser struct {
	ID    uint      `gorm:"column:id;primaryKey"`
	Roles []*phRole `gorm:"-"`
}

func (phUser) TableName() string { return "ph_users" }

func (phUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{
			Related:         &phRole{},
			Table:           "ph_user_roles",
			ForeignPivotKey: "user_id",
			RelatedPivotKey: "role_id",
			PivotField:      "UserPivot",
		},
	}
}

type phRole struct {
	ID         uint         `gorm:"column:id;primaryKey"`
	Name       string       `gorm:"column:name"`
	UserPivot  phUserPivot  `gorm:"-"`
	GroupPivot phGroupPivot `gorm:"-"`
	NotAPivot  string       `gorm:"column:not_a_pivot"`
}

func (phRole) TableName() string { return "ph_roles" }

type phUserPivot struct {
	UserID uint   `gorm:"column:user_id"`
	Level  string `gorm:"column:level"`
}

type phGroupPivot struct {
	RoleID uint   `gorm:"column:role_id"`
	Kind   string `gorm:"column:kind"`
}

// The pivot structs carry a table name because the descriptor build parses them to resolve their
// db-tagged columns (resolvePivotTimestamps -> parseSchema) and parseSchema requires a resolvable
// table — the same convention the existing pivot fixtures follow (prFriendPivot).
func (phUserPivot) TableName() string { return "ph_user_roles" }

func (phGroupPivot) TableName() string { return "ph_user_roles" }

// --- tests -------------------------------------------------------------------

func TestPivotSurfaceHydration_AsRetargetsTheHydratedField(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &phUser{})
	recorder.returnRows(`from "ph_roles"`,
		[]string{"id", "name", "pivot_user_id", "pivot_role_id", "pivot_kind"},
		[]driver.Value{int64(2), "admin", int64(1), int64(2), "vip"})

	relation := q.Related(&phUser{ID: 1}, "Roles").(contractsorm.BelongsToManyRelation).As("GroupPivot")

	roles := []phRole{}
	require.NoError(t, relation.Get(&roles))

	// Oracle P5: as('subscription') landed the pivot under the new accessor ($role->subscription,
	// $role->pivot null). The Go form lands it in the field the accessor names — GroupPivot holds
	// the row, UserPivot stays empty.
	require.Len(t, roles, 1)
	assert.Equal(t, uint(2), roles[0].GroupPivot.RoleID)
	assert.Equal(t, "vip", roles[0].GroupPivot.Kind)
	assert.Equal(t, uint(0), roles[0].UserPivot.UserID)
	assert.Equal(t, "", roles[0].UserPivot.Level)

	// The statement is the retargeted read: the named field's columns replace the declared
	// struct's (kind appended; role_id deduplicated against the fixed key alias).
	assert.Equal(t, `select "ph_roles".*, "ph_user_roles"."user_id" as "pivot_user_id", "ph_user_roles"."role_id" as "pivot_role_id", "ph_user_roles"."kind" as "pivot_kind" from "ph_roles" inner join "ph_user_roles" ON "ph_user_roles"."role_id" = "ph_roles"."id" where "ph_user_roles"."user_id" = ?`, recorder.last(t).sql)
}

func TestPivotSurfaceHydration_WithPivotWithoutADeclaredFieldSkipsTheColumn(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &phUser{})
	recorder.returnRows(`from "ph_roles"`,
		[]string{"id", "name", "pivot_user_id", "pivot_role_id", "pivot_level", "pivot_note"},
		[]driver.Value{int64(2), "admin", int64(1), int64(2), "gold", "free text"})

	relation := q.Related(&phUser{ID: 1}, "Roles").(contractsorm.BelongsToManyRelation).WithPivot("note")

	roles := []phRole{}
	require.NoError(t, relation.Get(&roles))

	// Oracle P2: the selected level hydrates (gold); the call-time note has no declared struct
	// field, so it is selected but not surfaced — the documented Go divergence from PHP's dynamic
	// pivot attributes — and the read must not error. The note alias appends after the declared
	// columns, the oracle's position.
	require.Len(t, roles, 1)
	assert.Equal(t, "gold", roles[0].UserPivot.Level)
	assert.Equal(t, `select "ph_roles".*, "ph_user_roles"."user_id" as "pivot_user_id", "ph_user_roles"."role_id" as "pivot_role_id", "ph_user_roles"."level" as "pivot_level", "ph_user_roles"."note" as "pivot_note" from "ph_roles" inner join "ph_user_roles" ON "ph_user_roles"."role_id" = "ph_roles"."id" where "ph_user_roles"."user_id" = ?`, recorder.last(t).sql)
}

func TestPivotSurfaceHydration_AsMissingFieldHydratesNothing(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &phUser{})
	recorder.returnRows(`from "ph_roles"`,
		[]string{"id", "name", "pivot_user_id", "pivot_role_id"},
		[]driver.Value{int64(2), "admin", int64(1), int64(2)})

	relation := q.Related(&phUser{ID: 1}, "Roles").(contractsorm.BelongsToManyRelation).As("Subscription")

	roles := []phRole{}
	require.NoError(t, relation.Get(&roles))

	// The read runs; no field by the new name exists, so nothing hydrates (the PivotField
	// convention's "no field by this name, no Pivot hydration") — and the declared select list is
	// untouched: the level alias stays, the way Laravel's frozen alias list never changes.
	require.Len(t, roles, 1)
	assert.Equal(t, uint(0), roles[0].UserPivot.UserID)
	assert.Equal(t, uint(0), roles[0].GroupPivot.RoleID)
	assert.Equal(t, `select "ph_roles".*, "ph_user_roles"."user_id" as "pivot_user_id", "ph_user_roles"."role_id" as "pivot_role_id", "ph_user_roles"."level" as "pivot_level" from "ph_roles" inner join "ph_user_roles" ON "ph_user_roles"."role_id" = "ph_roles"."id" where "ph_user_roles"."user_id" = ?`, recorder.last(t).sql)
}

func TestPivotSurfaceHydration_AsNonStructFieldErrorsTheRead(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &phUser{})
	relation := q.Related(&phUser{ID: 1}, "Roles").(contractsorm.BelongsToManyRelation).As("NotAPivot")

	roles := []phRole{}
	err := relation.Get(&roles)

	require.ErrorIs(t, err, frameworkerrors.OrmRelationPivotFieldNotStruct)
}
