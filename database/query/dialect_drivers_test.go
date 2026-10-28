package query

import (
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/database/query/processors"
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
)

// Keep the framework's dialect regression corpus here while exercising the implementations
// supplied by the external drivers. These adapters are compiled only into the tests.
var (
	NewMySqlGrammar     = mysql.NewQueryGrammar
	NewMariaDbGrammar   = mysql.NewMariaDBQueryGrammar
	NewPostgresGrammar  = postgres.NewQueryGrammar
	NewSqliteGrammar    = sqlite.NewQueryGrammar
	NewSqlServerGrammar = sqlserver.NewQueryGrammar
)

type MariaDbGrammar = mysql.MariaDBQueryGrammar
type MySqlProcessor = mysql.QueryProcessor
type MariaDbProcessor = mysql.MariaDBQueryProcessor
type PostgresProcessor = postgres.QueryProcessor
type SqliteProcessor = sqlite.QueryProcessor
type SqlServerProcessor = sqlserver.QueryProcessor

func testDialect(driver string) contractsdriver.Dialect {
	if core.NormalizeDriver(driver) == "mariadb" {
		return mysql.NewDialect("MariaDB")
	}
	switch CanonicalDialect(driver) {
	case DialectMySQL:
		return mysql.NewDialect()
	case DialectPostgres:
		return postgres.NewDialect()
	case DialectSqlite:
		return sqlite.NewDialect()
	case DialectSqlServer:
		return sqlserver.NewDialect()
	default:
		return nil
	}
}

func newTestGrammar(driver, prefix string) (Grammar, bool) {
	if dialect := testDialect(driver); dialect != nil {
		return dialect.NewQueryGrammar(contractsdriver.GrammarOptions{Driver: driver, Prefix: prefix}), true
	}
	return NewBaseGrammar(prefix), false
}

func newTestProcessor(driver string) Processor {
	if dialect := testDialect(driver); dialect != nil {
		return dialect.NewProcessor()
	}
	return &processors.BaseProcessor{}
}
