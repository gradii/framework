package query

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
