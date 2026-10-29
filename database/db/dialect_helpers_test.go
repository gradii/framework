package db

import (
	"context"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	contractsquery "github.com/gradii/framework/contracts/database/query"
	databasedriver "github.com/gradii/framework/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
)

// baseTestDialect is the framework's own dialect: the base grammar and the base processor,
// which is what a connection runs on when its driver supplies no dialect of its own.
type baseTestDialect struct{ contractsdriver.Dialect }

func (baseTestDialect) Name() string { return "base" }

func (baseTestDialect) NewQueryGrammar(options contractsdriver.GrammarOptions) contractsquery.Grammar {
	return querybuilder.NewBaseGrammar(options.Prefix)
}

func (baseTestDialect) NewProcessor() contractsquery.Processor { return querybuilder.NewProcessor("") }

// SQL-only tests supply the components directly instead of opening a server connection.
func newDialectTestQuery(ctx context.Context, read, write contractsdb.CommonBuilder, grammar querybuilder.Grammar, name string, logger contractslogger.Logger, table string, logs *[]TxLog) *Query {
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: querybuilder.NewProcessor(name)}
	return NewQuery(ctx, read, write, grammar, name, logger, table, logs, components)
}

type mysqlTestDriver struct{ contractsdriver.Driver }

func (mysqlTestDriver) Dialect() contractsdriver.Dialect { return baseTestDialect{} }
