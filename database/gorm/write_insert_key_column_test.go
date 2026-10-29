package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// insertKeyColumnEntity carries a primary key whose column is not `id`, the shape a model with
// `gorm:"primaryKey;column:user_id"` has.
type insertKeyColumnEntity struct {
	ID   uint `gorm:"primaryKey;column:user_id"`
	Name string
}

func (insertKeyColumnEntity) TableName() string { return "insert_key_column_entities" }

// insertDefaultKeyEntity keeps the conventional `id` primary key, so the fix does not disturb it.
type insertDefaultKeyEntity struct {
	ID   uint `gorm:"primaryKey"`
	Name string
}

func (insertDefaultKeyEntity) TableName() string { return "insert_default_key_entities" }

// soleKeyColumn resolves the primary key column the insert key-reporting clause names, which is the
// model's own key column rather than an assumed `id`.
func soleKeyColumn(t *testing.T, model any) string {
	t.Helper()

	return compiledQuery(t, model, Conditions{}).solePrimaryKeyColumn()
}

// The key-reporting clause names the model's own primary key column. It used to hardcode `id`, so a
// non-`id` primary key compiled `output inserted.[id]` and SQL Server failed with "Invalid column
// name 'id'"; PostgreSQL's `returning` carried the same defect. The pre-fix plain insert succeeded
// with key 0 only because it never asked for the column back.
//
// The dialects that render the clause — `output inserted.<column>` on SQL Server and
// `returning <column>` on PostgreSQL — are exercised in the integration corpus (tests/database/gorm)
// against the real driver grammars; here the column the framework resolves is pinned on its own side.
func TestCompileInsertReportsTheModelsPrimaryKeyColumn(t *testing.T) {
	t.Run("a non-id primary key names its own column", func(t *testing.T) {
		assert.Equal(t, "user_id", soleKeyColumn(t, &insertKeyColumnEntity{}))
	})

	t.Run("a conventional id primary key is unchanged", func(t *testing.T) {
		assert.Equal(t, "id", soleKeyColumn(t, &insertDefaultKeyEntity{}))
	})

	t.Run("a query naming no model keeps the default key", func(t *testing.T) {
		// No model, so no key column is known; the grammar falls back to Laravel's "id".
		assert.Equal(t, "", soleKeyColumn(t, nil))
	})
}
