package tests

import (
	"context"
	"os"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsdb "github.com/goravel/framework/contracts/database/db"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	databaseorm "github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/database/schema"
	"github.com/goravel/framework/testing/utils"
	"github.com/goravel/mysql"
)

func isMariaDBTest(driver string) bool {
	return driver == mysql.Name && os.Getenv("GORAVEL_TEST_MARIADB") == "1"
}

// Expected catalog values differ between MySQL and MariaDB. Keep actual metadata
// untouched so the tests preserve Laravel's observable driver-specific results.
func mysqlMetadataExpected(value, field string, nullable ...bool) string {
	if !isMariaDBTest(mysql.Name) {
		return value
	}
	switch field {
	case "Type":
		if mapped, ok := map[string]string{
			"bigint": "bigint(20)", "bigint unsigned": "bigint(20) unsigned",
			"int": "int(11)", "int unsigned": "int(10) unsigned",
			"mediumint": "mediumint(9)", "mediumint unsigned": "mediumint(8) unsigned",
			"smallint": "smallint(6)", "smallint unsigned": "smallint(5) unsigned",
			"tinyint": "tinyint(4)", "tinyint unsigned": "tinyint(3) unsigned", "json": "longtext",
		}[value]; ok {
			return mapped
		}
	case "TypeName":
		if value == "json" {
			return "longtext"
		}
	case "Collation":
		if value == "utf8mb4_0900_ai_ci" {
			return "utf8mb4_uca1400_ai_ci"
		}
	case "JsonCollation":
		return "utf8mb4_bin"
	case "Default":
		switch value {
		case "":
			if len(nullable) > 0 && nullable[0] {
				return "NULL"
			}
		case "goravel":
			return "'goravel'"
		case "CURRENT_TIMESTAMP":
			return "current_timestamp()"
		}
	}
	return value
}

func defaultForeignRule(driver string) string {
	if isMariaDBTest(driver) {
		return "restrict"
	}
	return "no action"
}

const (
	testDatabase = "goravel"
	testUsername = "goravel"
	testPassword = "Framework!123"
	testSchema   = "goravel"

	// testSqlserverImageTag pins the SQL Server image the tests start. The
	// driver defaults to `:latest`, which resolves to SQL Server 2025 now, and
	// its SQLPAL layer aborts on kernels older than 5.10 (the CNB runners run
	// 5.4) with `ASSERT ... sqlpalstubs.cpp Line=887`, so the container exits
	// before it ever accepts a connection. 2022 is the newest release that
	// still starts on those runners.
	testSqlserverImageTag = "2022-latest"
)

func newSchema(testQuery *TestQuery, connectionToTestQuery map[string]*TestQuery, connections ...contractsdb.DB) *schema.Schema {
	queries := make(map[string]contractsorm.Query)
	dbConfigs := make(map[string]contractsdatabase.Config)
	for connection, testQuery := range connectionToTestQuery {
		queries[connection] = testQuery.Query()
		dbConfigs[connection] = testQuery.Driver().Pool().Writers[0]
	}

	log := utils.NewTestLog()
	dbConfig := testQuery.Driver().Pool().Writers[0]
	orm := databaseorm.NewOrm(context.Background(), testQuery.Config(), dbConfig.Connection, dbConfig, testQuery.Query(), databaseorm.NewQueriesCache(queries, dbConfigs), log, nil, nil, nil)

	connection := testQuery.DB()
	if len(connections) > 0 {
		connection = connections[0]
	}
	schema, err := schema.NewSchema(testQuery.Config(), log, orm, testQuery.Driver(), nil, connection)
	if err != nil {
		log.Panic(err.Error())
	}

	return schema
}
