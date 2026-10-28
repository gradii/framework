package db

import (
	"context"
	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	databasedriver "github.com/gradii/framework/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
	mysqldialect "github.com/gradii/mysql"
	postgresdriver "github.com/gradii/postgres"
	sqlitedriver "github.com/gradii/sqlite"
	sqlserverdriver "github.com/gradii/sqlserver"
)

// SQL-only tests explicitly supply the external dialect without opening a server connection.
func newDialectTestQuery(ctx context.Context, read, write contractsdb.CommonBuilder, grammar querybuilder.Grammar, name string, logger contractslogger.Logger, table string, logs *[]TxLog) *Query {
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: querybuilder.NewProcessor(name)}
	if dialect := testDialect(name); dialect != nil {
		components.Dialect = dialect
		components.Processor = components.Dialect.NewProcessor()
	}
	return NewQuery(ctx, read, write, grammar, name, logger, table, logs, components)
}

type mysqlTestDriver struct{ contractsdriver.Driver }

func (mysqlTestDriver) Dialect() contractsdriver.Dialect { return mysqldialect.NewDialect() }

func testDialect(name string) contractsdriver.Dialect {
	switch querybuilder.CanonicalDialect(name) {
	case querybuilder.DialectMySQL:
		return mysqldialect.NewDialect(name)
	case querybuilder.DialectPostgres:
		return postgresdriver.NewDialect()
	case querybuilder.DialectSqlite:
		return sqlitedriver.NewDialect()
	case querybuilder.DialectSqlServer:
		return sqlserverdriver.NewDialect()
	default:
		return nil
	}
}
