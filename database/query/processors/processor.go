package processors

import (
	"strconv"
	"strings"
)

// Row is a database metadata row. PHP array and stdClass rows share this representation in Go.
type Row = map[string]any

// Connection is the execution surface used by Laravel's query processors. An adapter belongs to
// one insert operation, so its last insert ID cannot be confused with another pooled connection.
type Connection interface {
	Insert(statement string, values []any, sequence string) error
	LastInsertID(sequence string) (any, error)
	SelectFromWriteConnection(statement string, values []any, sequence string) (any, error)
}

// Processor handles execution and result processing after the grammar has compiled the statement.
type Processor interface {
	ProcessSelect(results any) any
	ProcessSchemas(results []Row) []Row
	ProcessTables(results []Row) []Row
	ProcessViews(results []Row) []Row
	ProcessTypes(results []Row) []Row
	ProcessIndexes(results []Row) []Row
	ProcessForeignKeys(results []Row) []Row
	ProcessInsertGetID(connection Connection, statement string, values []any, sequence string) (any, error)
	ProcessColumns(results []Row, sql ...string) []Row
}

// BaseProcessor ports Illuminate\Database\Query\Processors\Processor's insert-ID path.
type BaseProcessor struct{}

func (p *BaseProcessor) ProcessInsertGetID(connection Connection, statement string, values []any, sequence string) (any, error) {
	if err := connection.Insert(statement, values, sequence); err != nil {
		return nil, err
	}

	id, err := connection.LastInsertID(sequence)
	if err == nil {
		if value, ok := id.(string); ok {
			if integer, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64); parseErr == nil {
				id = integer
			}
		}
	}
	return id, err
}

// ProcessColumns ports the base processor's identity transformation.
func (p *BaseProcessor) ProcessColumns(results []Row, sql ...string) []Row { return results }
