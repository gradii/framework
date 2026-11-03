package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
)

// softDeleteUser carries the framework's own soft-delete type, which is what makes this package
// build the predicate and the delete rewrite instead of leaving them to GORM.
type softDeleteUser struct {
	ID        uint
	Name      string
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at"`
}

func (softDeleteUser) TableName() string { return "soft_users" }

func softDeleteSQL(t *testing.T, q *OrmBuilder, dest any) string {
	t.Helper()

	statement, _, err := q.dest(dest).compileSelect()
	require.NoError(t, err)

	return statement
}

func TestSoftDeletePredicate_AppliesToFrameworkType(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{})

	assert.Contains(t, softDeleteSQL(t, q, &softDeleteUser{}), `"soft_users"."deleted_at" is null`)
}

func TestSoftDeletePredicate_SkippedWhenWithTrashed(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).WithTrashed().(*OrmBuilder)

	assert.NotContains(t, softDeleteSQL(t, q, &softDeleteUser{}), "deleted_at")
}

// OnlyTrashed keeps a predicate, but inverts it: the default "is null" is replaced by "is not null",
// exactly as Laravel's SoftDeletes::onlyTrashed drops the scope and adds whereNotNull(deleted_at).
func TestSoftDeletePredicate_OnlyTrashedInvertsPredicate(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).OnlyTrashed().(*OrmBuilder)

	sql := softDeleteSQL(t, q, &softDeleteUser{})
	assert.Contains(t, sql, `"soft_users"."deleted_at" is not null`)
	assert.NotContains(t, sql, `"soft_users"."deleted_at" is null`)
}

// OnlyTrashed wins over WithTrashed regardless of call order, matching Laravel where the whereNotNull
// macro's predicate survives the scope removal both ways round.
func TestSoftDeletePredicate_OnlyTrashedOverridesWithTrashed(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).WithTrashed().(*OrmBuilder).OnlyTrashed().(*OrmBuilder)

	assert.Contains(t, softDeleteSQL(t, q, &softDeleteUser{}), `"soft_users"."deleted_at" is not null`)
}

// A model carrying the framework's own type has its delete rewritten into an update that stamps
// deleted_at, and the rewrite keeps the "not already deleted" predicate.
func TestSoftDeleteOrDelete_RewritesFrameworkTypeToUpdate(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).Where("id", 1).(*OrmBuilder)

	column, ok := q.softDeleteColumn()
	require.True(t, ok, "a delete of this model must be rewritten")

	statement, bindings, err := q.compileSoftDeleteUpdate(map[string]any{column: q.softDeleteValue()})
	require.NoError(t, err)

	assert.Contains(t, statement, `update "soft_users" set "deleted_at" = ?`)
	// The predicate is a structured where, so the grammar renders its lowercase "is null".
	assert.Contains(t, statement, `"soft_users"."deleted_at" is null`)
	assert.Contains(t, statement, `"id" = ?`)
	// The stamped value comes first because the set values precede the where bindings.
	assert.Len(t, bindings, 2)
	assert.Equal(t, 1, bindings[1])
}

// Asking for the trashed rows too removes the row instead of stamping it.
func TestSoftDeleteOrDelete_UnscopedStaysADelete(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{}).Where("id", 1).(*OrmBuilder).WithTrashed().(*OrmBuilder)

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
	q := newRelQueryWith(t, &softDeleteUser{}).WithTrashed().(*OrmBuilder)

	_, _, err := q.compileDelete()
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)
}

// A framework soft delete rewritten into an update compiles when nothing but the model's own
// soft-delete predicate names its rows: the predicate is a WHERE the compiled statement carries, and
// the missing-where guard reads the compiled where clause, so `Model(&user{}).Delete()` on a
// soft-delete model soft-deletes the live rows the way Laravel's `User::query()->delete()` does
// (oracle, sqlite: `update "users" set "deleted_at" = ?, "updated_at" = ? where "users"."deleted_at" is null`).
func TestCompileSoftDeleteUpdate_SoftDeleteWhereSatisfiesGuard(t *testing.T) {
	q := newRelQueryWith(t, &softDeleteUser{})

	column, ok := q.softDeleteColumn()
	require.True(t, ok, "a delete of this model must be rewritten into an update")

	statement, bindings, err := q.compileSoftDeleteUpdate(map[string]any{column: q.softDeleteValue()})
	require.NoError(t, err)

	assert.Equal(t, `update "soft_users" set "deleted_at" = ? where "soft_users"."deleted_at" is null`, statement)
	assert.Len(t, bindings, 1)
}
