package processors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ports DatabaseProcessorTest::testInsertGetIdProcessing, including PDO's numeric string result.
func TestDatabaseProcessor_InsertGetIdProcessing(t *testing.T) {
	connection := &processorConnection{id: "1"}
	id, err := (&BaseProcessor{}).ProcessInsertGetID(connection, "sql", []any{"foo"}, "id")
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)
	assert.Equal(t, []string{"insert", "last-id"}, connection.calls)
	assert.Equal(t, "sql", connection.statement)
	assert.Equal(t, []any{"foo"}, connection.values)
	assert.Equal(t, "id", connection.sequence)
}

func TestDatabaseMySqlProcessor_ProcessColumns(t *testing.T) {
	assertLaravelColumnFixture(t, &MySqlProcessor{}, "mysql")
}

func TestDatabaseMariaDbProcessor_ProcessColumns(t *testing.T) {
	assertLaravelColumnFixture(t, &MariaDbProcessor{}, "mariadb")
}

func TestDatabasePostgresProcessor_ProcessColumns(t *testing.T) {
	assertLaravelColumnFixture(t, &PostgresProcessor{}, "postgres")
}

func TestDatabaseSQLiteProcessor_ProcessColumns(t *testing.T) {
	assertLaravelColumnFixture(t, &SqliteProcessor{}, "sqlite")
}

// Fixtures preserve the upstream rows and expectations. PHP array and stdClass result rows both
// map to Go's Row representation; the processor does not depend on PDO's fetch mode.
func assertLaravelColumnFixture(t *testing.T, processor Processor, dialect string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", dialect+"_columns.json"))
	require.NoError(t, err)
	var fixture struct {
		Source   string
		Listing  []Row
		Expected []Row
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	if dialect == "mariadb" {
		// MariaDB inherits MySQL's empty-comment normalization. PHPUnit's assertEquals permits
		// the fixture's empty string to equal null; Go's strict comparison uses the returned null.
		fixture.Expected[1]["comment"] = nil
	}
	assert.Equal(t, fixture.Expected, processor.ProcessColumns(fixture.Listing), fixture.Source)
}
