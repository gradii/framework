package db

import (
	"context"
	"testing"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractslogger "github.com/gradii/framework/contracts/database/logger"
	"github.com/gradii/framework/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
	"github.com/stretchr/testify/require"
)

// These helpers drive the framework's query facade with a real driver dialect. The dialect supplies
// the grammar, the result processor and the value escaper, so ToSql and ToRawSql render the SQL and
// the literal escaping a live connection would use, without a database server.

// dialect resolves a canonical driver name to the external module's dialect.
func dialect(driver string) contractsdriver.Dialect {
	switch driver {
	case "mysql":
		return mysql.NewDialect()
	case "postgres":
		return postgres.NewDialect()
	case "sqlite":
		return sqlite.NewDialect()
	case "sqlserver":
		return sqlserver.NewDialect()
	default:
		return nil
	}
}

// dialectQuery builds a SQL-only query for the driver's dialect.
func dialectQuery(t *testing.T, driver, table string) *db.Query {
	t.Helper()

	return dialectQueryWith(t, driver, table, contractsdriver.GrammarOptions{Driver: driver}, nil, nil, nil, nil)
}

// dialectQueryWith builds a query with the components a connection resolves, plus optional builders
// and logger, so a test can drive execution through mocks or preview the SQL through ToSql.
func dialectQueryWith(t *testing.T, driver, table string, options contractsdriver.GrammarOptions, read, write contractsdb.CommonBuilder, logger contractslogger.Logger, logs *[]db.TxLog) *db.Query {
	t.Helper()

	resolved := dialect(driver)
	require.NotNil(t, resolved, "no dialect for driver %q", driver)
	options.Driver = driver
	grammar := resolved.NewQueryGrammar(options)
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: resolved.NewProcessor(), Dialect: resolved, Options: options}

	return db.NewQuery(context.Background(), read, write, grammar, driver, logger, table, logs, components)
}
