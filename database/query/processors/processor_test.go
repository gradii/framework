package processors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/goravel/framework/errors"
)

type processorConnection struct {
	calls     []string
	statement string
	values    []any
	sequence  string
	id        any
	insertErr error
	idErr     error
	selectErr error
}

func (c *processorConnection) Insert(statement string, values []any, sequence string) error {
	c.calls = append(c.calls, "insert")
	c.statement, c.values, c.sequence = statement, values, sequence
	return c.insertErr
}

func (c *processorConnection) LastInsertID(sequence string) (any, error) {
	c.calls = append(c.calls, "last-id")
	c.sequence = sequence
	return c.id, c.idErr
}

func (c *processorConnection) SelectFromWriteConnection(statement string, values []any, sequence string) (any, error) {
	c.calls = append(c.calls, "select-writer")
	c.statement, c.values, c.sequence = statement, values, sequence
	return c.id, c.selectErr
}

func TestInsertIDProcessingUsesTheDialect(t *testing.T) {
	for _, test := range []struct {
		name      string
		processor Processor
		calls     []string
	}{
		{"base", &BaseProcessor{}, []string{"insert", "last-id"}},
		{"mysql", &MySqlProcessor{}, []string{"insert", "last-id"}},
		{"mariadb", &MariaDbProcessor{}, []string{"insert", "last-id"}},
		{"sqlite", &SqliteProcessor{}, []string{"insert", "last-id"}},
		{"postgres", &PostgresProcessor{}, []string{"select-writer"}},
		{"sqlserver", &SqlServerProcessor{}, []string{"select-writer"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, statement := range []string{
				"insert into users (name) values (' returning output inserted. ')",
				"INSERT INTO users (name) VALUES (?)\nRETURNING\nuser_id",
			} {
				connection := &processorConnection{id: int64(73)}
				values := []any{"alice"}
				id, err := test.processor.ProcessInsertGetID(connection, statement, values, "user_id")
				require.NoError(t, err)
				assert.Equal(t, int64(73), id)
				assert.Equal(t, test.calls, connection.calls)
				assert.Equal(t, statement, connection.statement)
				assert.Equal(t, values, connection.values)
				assert.Equal(t, "user_id", connection.sequence)
			}
		})
	}
}

func TestInsertIDProcessingPropagatesErrors(t *testing.T) {
	err := errors.DatabaseTableIsRequired
	connection := &processorConnection{insertErr: err}
	_, got := (&BaseProcessor{}).ProcessInsertGetID(connection, "insert", nil, "")
	assert.ErrorIs(t, got, err)
	assert.Equal(t, []string{"insert"}, connection.calls)

	connection = &processorConnection{idErr: err}
	_, got = (&MySqlProcessor{}).ProcessInsertGetID(connection, "insert", nil, "")
	assert.ErrorIs(t, got, err)
	assert.Equal(t, []string{"insert", "last-id"}, connection.calls)

	for _, processor := range []Processor{&PostgresProcessor{}, &SqlServerProcessor{}} {
		connection = &processorConnection{selectErr: err}
		_, got = processor.ProcessInsertGetID(connection, "insert", nil, "")
		assert.ErrorIs(t, got, err)
		assert.Equal(t, []string{"select-writer"}, connection.calls)
		assert.Equal(t, "id", connection.sequence)
	}
}

func TestReturnedStringKeyKeepsItsType(t *testing.T) {
	for _, processor := range []Processor{&PostgresProcessor{}, &SqlServerProcessor{}} {
		connection := &processorConnection{id: "01AN4Z07BY79KA1307SR9X4MV3"}
		id, err := processor.ProcessInsertGetID(connection, "insert", nil, "")
		require.NoError(t, err)
		assert.Equal(t, connection.id, id)
		assert.Equal(t, "id", connection.sequence)
	}
}
