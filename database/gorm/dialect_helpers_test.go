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
	mysqldialect "github.com/gradii/mysql"
	postgresdriver "github.com/gradii/postgres"
	sqlitedriver "github.com/gradii/sqlite"
	sqlserverdriver "github.com/gradii/sqlserver"
)

// Recording pools exercise query execution without requiring server-version SQL.
func mysqlTestComponents() databasedriver.QueryComponents {
	dialect := mysqldialect.NewDialect()
	return databasedriver.QueryComponents{
		Grammar:   dialect.NewQueryGrammar(contractsdriver.GrammarOptions{}),
		Processor: dialect.NewProcessor(), Dialect: dialect,
	}
}

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

func newDialectQuery(ctx context.Context, config config.Config, dbConfig contractsdatabase.Config, writer *sql.DB, tx *sql.Tx, grammar contractsdriver.Grammar, log log.Log, observers []contractsorm.ModelToObserver, conditions *Conditions, resolver contractstelemetry.Resolver, supplied ...databasedriver.QueryComponents) *Query {
	if len(supplied) == 0 {
		if dialect := testDialect(dbConfig.Driver); dialect != nil {
			components, err := databasedriver.ResolveQueryComponents(dialect, contractsdriver.GrammarOptions{Driver: dbConfig.Driver, Prefix: dbConfig.Prefix})
			if err != nil {
				panic(err)
			}
			supplied = append(supplied, components)
		}
	}
	return NewQuery(ctx, config, dbConfig, writer, tx, grammar, log, observers, conditions, resolver, supplied...)
}
