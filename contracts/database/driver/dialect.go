package driver

import (
	sqldriver "database/sql/driver"

	"github.com/gradii/framework/contracts/database"
	"github.com/gradii/framework/contracts/database/query"
)

// GrammarOptions carries connection-local compilation settings. ServerVersion uses the
// managed writer pool; constructing a grammar must not open a separate connection.
type GrammarOptions struct {
	Prefix        string
	Driver        string
	ServerVersion func() (string, error)
}

// Dialect supplies the database-specific connection and query components. The framework
// retains ownership of SQL pools, query state, execution and transactions.
type Dialect interface {
	Name() string
	NewConnector(database.Config) (sqldriver.Connector, error)
	NewQueryGrammar(GrammarOptions) query.Grammar
	NewProcessor() query.Processor
}

// DriverWithDialect is implemented by drivers which supply their query components.
// Older drivers can continue to use the framework's remaining built-in dialects.
type DriverWithDialect interface {
	Dialect() Dialect
}

// DriverWithServerVersion receives version discovery backed by the managed writer.
// Schema grammars use this to compile version-dependent DDL without their own pools.
type DriverWithServerVersion interface {
	SetServerVersion(func() (string, error))
}

// DialectWithServerVersion supplies the SQL used for connection-local version discovery.
type DialectWithServerVersion interface {
	ServerVersionSQL() string
}

// DialectWithEscaper supplies database-specific literal rendering for SQL previews.
// Executed statements continue to use bound parameters.
type DialectWithEscaper interface {
	Escape(value any, binary bool) (string, error)
}
