package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormio "gorm.io/gorm"

	"github.com/goravel/framework/errors"
	basedatabase "github.com/goravel/framework/support/database"
)

// softDeleteUser carries the framework's own soft-delete type, which is what makes this package
// build the predicate and the delete rewrite instead of leaving them to GORM.
type softDeleteUser struct {
	ID        uint
	Name      string
	DeletedAt basedatabase.DeletedAt `gorm:"column:deleted_at"`
}

func (softDeleteUser) TableName() string { return "soft_users" }

// gormSoftDeleteUser still declares GORM's type, so GORM's own clause stays in charge of it.
type gormSoftDeleteUser struct {
	ID        uint
	DeletedAt gormio.DeletedAt `gorm:"column:deleted_at"`
}

func (gormSoftDeleteUser) TableName() string { return "gorm_soft_users" }

func softDeleteSQL(t *testing.T, q *Query, dest any) string {
	t.Helper()

	statement, _, err := q.dest(dest).compileSelect()
	require.NoError(t, err)

	return statement
}

func TestSoftDeletePredicate_AppliesToFrameworkType(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{})

	assert.Contains(t, softDeleteSQL(t, q, &softDeleteUser{}), `"soft_users"."deleted_at" IS NULL`)
}

func TestSoftDeletePredicate_SkippedWhenWithTrashed(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).WithTrashed().(*Query)

	assert.NotContains(t, softDeleteSQL(t, q, &softDeleteUser{}), "deleted_at")
}

// OnlyTrashed keeps a predicate, but inverts it: the default "IS NULL" is replaced by "IS NOT NULL",
// exactly as Laravel's SoftDeletes::onlyTrashed drops the scope and adds whereNotNull(deleted_at).
func TestSoftDeletePredicate_OnlyTrashedInvertsPredicate(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).OnlyTrashed().(*Query)

	sql := softDeleteSQL(t, q, &softDeleteUser{})
	assert.Contains(t, sql, `"soft_users"."deleted_at" IS NOT NULL`)
	assert.NotContains(t, sql, `"soft_users"."deleted_at" IS NULL`)
}

// OnlyTrashed wins over WithTrashed regardless of call order, matching Laravel where the whereNotNull
// macro's predicate survives the scope removal both ways round.
func TestSoftDeletePredicate_OnlyTrashedOverridesWithTrashed(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).WithTrashed().(*Query).OnlyTrashed().(*Query)

	assert.Contains(t, softDeleteSQL(t, q, &softDeleteUser{}), `"soft_users"."deleted_at" IS NOT NULL`)
}

func TestSoftDeletePredicate_LeavesGormTypeAlone(t *testing.T) {
	q := newRelQueryWith(t, &gormSoftDeleteUser{})

	// The compiled statement carries no predicate: the framework only adds one for its own
	// soft-delete type, and a model still declaring GORM's type is left to GORM.
	sql := softDeleteSQL(t, q, &gormSoftDeleteUser{})

	assert.NotContains(t, sql, "deleted_at")
}

// A model carrying the framework's own type has its delete rewritten into an update that stamps
// deleted_at, and the rewrite keeps the "not already deleted" predicate.
func TestSoftDeleteOrDelete_RewritesFrameworkTypeToUpdate(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).Where("id", 1).(*Query)

	column, ok := q.softDeleteColumn()
	require.True(t, ok, "a delete of this model must be rewritten")

	statement, bindings, err := q.compileSoftDeleteUpdate(map[string]any{column: q.softDeleteValue()})
	require.NoError(t, err)

	assert.Contains(t, statement, `update "soft_users" set "deleted_at" = ?`)
	// The predicate is a raw fragment, so it keeps the case it was written with.
	assert.Contains(t, statement, `"soft_users"."deleted_at" IS NULL`)
	assert.Contains(t, statement, `"id" = ?`)
	// The stamped value comes first because the set values precede the where bindings.
	assert.Len(t, bindings, 2)
	assert.Equal(t, 1, bindings[1])
}

// Asking for the trashed rows too removes the row instead of stamping it.
func TestSoftDeleteOrDelete_UnscopedStaysADelete(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).Where("id", 1).(*Query).WithTrashed().(*Query)

	_, ok := q.softDeleteColumn()
	assert.False(t, ok, "an unscoped delete must not be rewritten")

	statement, bindings, err := q.compileDelete()
	require.NoError(t, err)

	assert.Contains(t, statement, `delete from "soft_users"`)
	assert.NotContains(t, statement, "deleted_at")
	assert.Equal(t, []any{1}, bindings)
}

// A delete with nothing naming its rows is refused rather than compiled into a statement that would
// empty the table.
func TestCompileDelete_RefusesEveryRow(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).WithTrashed().(*Query)

	_, _, err := q.compileDelete()
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)
}

// A framework soft delete rewritten into an update is still refused when nothing names its rows: the
// "deleted_at IS NULL" predicate belongs to the model and is added to the compiled builder, not to the
// query's conditions, so it does not satisfy the missing-where guard on its own the way a global
// scope's where does.
func TestCompileSoftDeleteUpdate_RefusesEveryRow(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{})

	column, ok := q.softDeleteColumn()
	require.True(t, ok, "a delete of this model must be rewritten into an update")

	_, _, err := q.compileSoftDeleteUpdate(map[string]any{column: q.softDeleteValue()})
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)
}
