package processors

import (
	"strconv"
	"strings"

	contractsquery "github.com/gradii/framework/contracts/database/query"
)

// Compatibility aliases preserve the existing processor API.
type Row = contractsquery.Row
type Connection = contractsquery.Connection
type Processor = contractsquery.Processor

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
