package tests

import (
	"context"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	databaseorm "github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/database/schema"
	"github.com/goravel/framework/testing/utils"
)

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

func newSchema(testQuery *TestQuery, connectionToTestQuery map[string]*TestQuery) *schema.Schema {
	queries := make(map[string]contractsorm.Query)
	dbConfigs := make(map[string]contractsdatabase.Config)
	for connection, testQuery := range connectionToTestQuery {
		queries[connection] = testQuery.Query()
		dbConfigs[connection] = testQuery.Driver().Pool().Writers[0]
	}

	log := utils.NewTestLog()
	dbConfig := testQuery.Driver().Pool().Writers[0]
	orm := databaseorm.NewOrm(context.Background(), testQuery.Config(), dbConfig.Connection, dbConfig, testQuery.Query(), databaseorm.NewQueriesCache(queries, dbConfigs), log, nil, nil, nil)

	schema, err := schema.NewSchema(testQuery.Config(), log, orm, testQuery.Driver(), nil)
	if err != nil {
		log.Panic(err.Error())
	}

	return schema
}
