package query

import "github.com/goravel/framework/database/query/processors"

type Processor = processors.Processor

// NewProcessor selects the connection's post-processor, mirroring Laravel's getDefaultPostProcessor.
func NewProcessor(driver string) Processor { return processors.New(driver) }

// Processor returns the post-processor supplied by the connection.
func (b *Builder) Processor() Processor { return b.processor }

// InsertGetID mirrors Builder::insertGetId: compile with the grammar, then execute through the
// processor. ToInsertGetIdSQL remains available for callers that only need to preview the SQL.
func (b *Builder) InsertGetID(connection processors.Connection, values any, sequence string) (any, error) {
	statement, bindings := b.ToInsertGetIdSQL(values, sequence)
	if err := b.Error(); err != nil {
		return nil, err
	}

	return b.processor.ProcessInsertGetID(connection, statement, bindings, sequence)
}
