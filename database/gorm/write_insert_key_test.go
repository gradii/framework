package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormio "gorm.io/gorm"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	querybuilder "github.com/goravel/framework/database/query"
)

// insertKeyEntity carries a string primary key, the shape a UUID or ULID model has.
type insertKeyEntity struct {
	ID   string `gorm:"primaryKey;type:char(26)"`
	Name string
}

func (insertKeyEntity) TableName() string { return "insert_key_entities" }

// A key the developer has not assigned must not reach the row. Eloquent carries the primary key column
// only when the key attribute is set, so an unset key is left for the database to fill in. The old
// path dropped the column only for integer keys, so a string key went out as an empty string.
func TestInsertRecordsOmitsAnUnsetStringPrimaryKey(t *testing.T) {
	query := compiledQuery(t, &insertKeyEntity{}, Conditions{})

	records, err := query.insertRecords(&insertKeyEntity{Name: "unset-key"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		_, present := records[0]["id"]
		assert.False(t, present, "an unset string primary key must not be inserted")
		assert.Equal(t, "unset-key", records[0]["name"])
	}
}

// A key the developer did assign stays in the row, whatever its type.
func TestInsertRecordsKeepsASetStringPrimaryKey(t *testing.T) {
	query := compiledQuery(t, &insertKeyEntity{}, Conditions{})

	records, err := query.insertRecords(&insertKeyEntity{ID: "01AN4Z07BY79KA1307SR9X4MV3", Name: "set-key"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "01AN4Z07BY79KA1307SR9X4MV3", records[0]["id"])
	}
}

// A string primary key is read back as a string. The key read-back used to scan the returning value
// into a fixed int64, so on PostgreSQL a ULID key failed to scan ("converting driver.Value type
// string ... to a int64") and, where it did not error, the integer was coerced into a one-byte
// string. The value now goes into a target built from the primary key column's own Go type.
func TestInsertRowReadsAStringKeyByItsColumnType(t *testing.T) {
	query, recordDriver := newPostgresKeyQuery(t, insertKeyEntity{})
	recordDriver.returnRows("returning", []string{"id"}, []driver.Value{"01AN4Z07BY79KA1307SR9X4MV3"})

	key, err := query.insertRow(map[string]any{"id": "01AN4Z07BY79KA1307SR9X4MV3", "name": "ulid"})
	require.NoError(t, err)
	assert.Equal(t, "01AN4Z07BY79KA1307SR9X4MV3", key, "a string key must come back as a string")

	// The write-back preserves the key on the receiver rather than overwriting it with a coerced
	// number, which is the half the integration test TestUlidColumn pins against a real database.
	entity := insertKeyEntity{Name: "ulid"}
	query.assignInsertedKeys(&entity, []any{key})
	assert.Equal(t, "01AN4Z07BY79KA1307SR9X4MV3", entity.ID)
}

// An integer primary key still comes back as the model's own integer type, so the fix did not turn
// every key into a string.
func TestInsertRowReadsAnIntegerKeyByItsColumnType(t *testing.T) {
	query, recordDriver := newPostgresKeyQuery(t, relUser{})
	recordDriver.returnRows("returning", []string{"id"}, []driver.Value{int64(42)})

	key, err := query.insertRow(map[string]any{"name": "user"})
	require.NoError(t, err)
	assert.Equal(t, uint(42), key)
}

func TestInsertRowIgnoresSQLKeywordsInValues(t *testing.T) {
	query, recorder := newPostgresKeyQuery(t, &insertKeyEntity{})
	query = rebindGrammar(query, "MySQL", querybuilder.NewMySqlGrammar(""))

	key, err := query.insertRow(map[string]any{
		"id":   "01AN4Z07BY79KA1307SR9X4MV3",
		"name": querybuilder.Raw("' returning output inserted. '"),
	})
	require.NoError(t, err)
	assert.Nil(t, key)
	assert.Zero(t, recorder.count(), "a MySQL insert must not attempt to read a returned row")
}

// isIntegerKeyType is what the non-returning path consults before trusting the driver's LastInsertId,
// so only an auto-increment integer key is read back. A string key (a ULID or UUID) never is.
func TestIsIntegerKeyType(t *testing.T) {
	assert.True(t, isIntegerKeyType(reflect.TypeOf(0)))
	assert.True(t, isIntegerKeyType(reflect.TypeOf(uint(0))))
	assert.True(t, isIntegerKeyType(reflect.TypeOf(new(uint64))))
	assert.False(t, isIntegerKeyType(reflect.TypeOf("")))
	assert.False(t, isIntegerKeyType(reflect.TypeOf(true)))
	assert.False(t, isIntegerKeyType(nil))
}

// newPostgresKeyQuery builds a query that compiles through the PostgreSQL grammar, whose insert
// carries the returning clause, over a recording pool, so the key read-back can be driven with no
// database behind it.
func newPostgresKeyQuery(t *testing.T, model any) (*Query, *recordingSQLDriver) {
	t.Helper()

	recordDriver := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recordDriver})
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gormio.Open(stubDialector{}, &gormio.Config{ConnPool: sqlDB, DisableAutomaticPing: true})
	require.NoError(t, err)

	conditions := Conditions{model: model}

	return NewQuery(context.Background(), nil, contractsdatabase.Config{Driver: "PostgreSQL"}, db, nil, nil, nil, &conditions, nil), recordDriver
}
