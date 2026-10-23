package query

import (
	"strings"

	"github.com/goravel/framework/database/query/processors"
)

type Processor = processors.Processor

// NewProcessor selects the connection's post-processor, mirroring Laravel's getDefaultPostProcessor.
func NewProcessor(driver string) Processor {
	normalized := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(driver)))
	if normalized == "mariadb" {
		return &processors.MariaDbProcessor{}
	}

	switch CanonicalDialect(driver) {
	case DialectMySQL:
		return &processors.MySqlProcessor{}
	case DialectPostgres:
		return &processors.PostgresProcessor{}
	case DialectSqlite:
		return &processors.SqliteProcessor{}
	case DialectSqlServer:
		return &processors.SqlServerProcessor{}
	default:
		return &processors.BaseProcessor{}
	}
}

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
