package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
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

// compileInsertForDriver compiles a single-row insert for a model over the dialect the driver name
// selects, so the key-reporting clause can be inspected with no database behind it.
func compileInsertForDriver(t *testing.T, driver string, model any) string {
	t.Helper()

	conditions := Conditions{model: model}
	query := newDialectQuery(context.Background(), nil, contractsdatabase.Config{Driver: driver},
		stubWriter(t), nil, nil, nil, nil, &conditions, nil)

	sql, _, err := query.compileInsertAll([]map[string]any{{"name": "x"}})
	require.NoError(t, err)

	return sql
}

// The key-reporting clause names the model's own primary key column. It used to hardcode `id`, so a
// non-`id` primary key compiled `output inserted.[id]` and SQL Server failed with "Invalid column
// name 'id'"; PostgreSQL's `returning` carried the same defect. The pre-fix plain insert succeeded
// with key 0 only because it never asked for the column back.
func TestCompileInsertReportsTheModelsPrimaryKeyColumn(t *testing.T) {
	t.Run("sql server output names the primary key column", func(t *testing.T) {
		sql := compileInsertForDriver(t, "SqlServer", &insertKeyColumnEntity{})

		assert.Equal(t, "insert into [insert_key_column_entities] ([name]) output inserted.[user_id] values (?)", sql)
		assert.NotContains(t, sql, "inserted.[id]")
	})

	t.Run("postgres returning names the primary key column", func(t *testing.T) {
		sql := compileInsertForDriver(t, "PostgreSQL", &insertKeyColumnEntity{})

		assert.Equal(t, `insert into "insert_key_column_entities" ("name") values (?) returning "user_id"`, sql)
	})

	t.Run("a conventional id primary key is unchanged", func(t *testing.T) {
		assert.Equal(t,
			"insert into [insert_default_key_entities] ([name]) output inserted.[id] values (?)",
			compileInsertForDriver(t, "SqlServer", &insertDefaultKeyEntity{}))
		assert.Equal(t,
			`insert into "insert_default_key_entities" ("name") values (?) returning "id"`,
			compileInsertForDriver(t, "PostgreSQL", &insertDefaultKeyEntity{}))
	})

	t.Run("a query naming no model keeps the default key", func(t *testing.T) {
		conditions := Conditions{}
		query := newDialectQuery(context.Background(), nil, contractsdatabase.Config{Driver: "PostgreSQL"},
			stubWriter(t), nil, nil, nil, nil, &conditions, nil)

		sql, _, err := query.compileInsertAll([]map[string]any{{"name": "x"}})
		require.NoError(t, err)
		// No model, so no key column is known and the Laravel default stands.
		assert.Contains(t, sql, `returning "id"`)
	})
}
