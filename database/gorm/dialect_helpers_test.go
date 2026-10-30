package gorm

import (
	"context"
	"database/sql"

	"github.com/gradii/framework/contracts/config"
	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/contracts/log"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	databasedriver "github.com/gradii/framework/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
)

// baseTestDialect is the framework's own dialect: the base grammar and the base processor, which
// is what a connection runs on when its driver supplies no dialect of its own.
type baseTestDialect struct{ contractsdriver.Dialect }

func (baseTestDialect) Name() string { return "base" }

func (baseTestDialect) NewQueryGrammar(options contractsdriver.GrammarOptions) querybuilder.Grammar {
	return querybuilder.NewBaseGrammar(options.Prefix)
}

func (baseTestDialect) NewProcessor() querybuilder.Processor { return querybuilder.NewProcessor("") }

// Recording pools exercise query execution without requiring server-version SQL.
func mysqlTestComponents() databasedriver.QueryComponents {
	dialect := baseTestDialect{}
	return databasedriver.QueryComponents{
		Grammar:   dialect.NewQueryGrammar(contractsdriver.GrammarOptions{}),
		Processor: dialect.NewProcessor(), Dialect: dialect,
	}
}

func testDialect(name string) contractsdriver.Dialect {
	if querybuilder.CanonicalDialect(name) == "" {
		return nil
	}
	return baseTestDialect{}
}

func newDialectQuery(ctx context.Context, config config.Config, dbConfig contractsdatabase.Config, writer *sql.DB, tx *sql.Tx, grammar contractsdriver.Grammar, log log.Log, observers []contractsorm.ModelToObserver, conditions *Conditions, resolver contractstelemetry.Resolver, supplied ...databasedriver.QueryComponents) *OrmBuilder {
	if len(supplied) == 0 {
		if dialect := testDialect(dbConfig.Driver); dialect != nil {
			components, err := databasedriver.ResolveQueryComponents(dialect, contractsdriver.GrammarOptions{Driver: dbConfig.Driver, Prefix: dbConfig.Prefix})
			if err != nil {
				panic(err)
			}
			supplied = append(supplied, components)
		}
	}
	return NewOrmBuilder(ctx, config, dbConfig, writer, tx, grammar, log, observers, conditions, resolver, supplied...)
}
