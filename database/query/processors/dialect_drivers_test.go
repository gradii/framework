package processors_test

import (
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
)

// Preserve the original processor corpus while testing each driver's actual processor.
type MySqlProcessor = mysql.QueryProcessor
type MariaDbProcessor = mysql.MariaDBQueryProcessor
type PostgresProcessor = postgres.QueryProcessor
type SqliteProcessor = sqlite.QueryProcessor
type SqlServerProcessor = sqlserver.QueryProcessor
