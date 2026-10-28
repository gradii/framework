package tests

import (
	"os"
	"strings"

	"github.com/gradii/framework/contracts/config"
	contractsdb "github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/database/schema"
	mocksfoundation "github.com/gradii/framework/mocks/foundation"
	"github.com/gradii/framework/testing/utils"
	"github.com/gradii/mysql"
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

var ormFactory func(foundation.Application) (any, error)

// ormConfig resolves the fixture's connection aliases without depending on the internal ORM cache.
type ormConfig struct {
	config.Config
	connection string
	queries    map[string]*TestQuery
}

func (r ormConfig) Get(path string, defaultValue ...any) any {
	if query, mapped := r.connectionConfig(path); query != nil {
		if strings.HasSuffix(path, ".via") {
			return func() (driver.Driver, error) { return query.Driver(), nil }
		}
		return query.Config().Get(mapped, defaultValue...)
	}
	return r.Config.Get(path, defaultValue...)
}

func (r ormConfig) connectionConfig(path string) (*TestQuery, string) {
	rest, ok := strings.CutPrefix(path, "database.connections.")
	if !ok {
		return nil, path
	}
	name, field, ok := strings.Cut(rest, ".")
	if !ok {
		return nil, path
	}
	for alias, query := range r.queries {
		actual := query.Driver().Pool().Writers[0].Connection
		if name == alias || name == actual {
			return query, "database.connections." + actual + "." + field
		}
	}
	return nil, path
}

func (r ormConfig) GetString(path string, defaultValue ...string) string {
	if path == "database.default" {
		return r.connection
	}
	if query, mapped := r.connectionConfig(path); query != nil {
		return query.Config().GetString(mapped, defaultValue...)
	}
	return r.Config.GetString(path, defaultValue...)
}

func (r ormConfig) GetBool(path string, defaultValue ...bool) bool {
	if path == "telemetry.instrumentation.database.enabled" {
		return false
	}
	if query, mapped := r.connectionConfig(path); query != nil {
		return query.Config().GetBool(mapped, defaultValue...)
	}
	return r.Config.GetBool(path, defaultValue...)
}

func newOrm(testQuery *TestQuery, queries map[string]*TestQuery) contractsorm.Orm {
	app := &mocksfoundation.Application{}
	app.EXPECT().MakeConfig().Return(ormConfig{
		Config: testQuery.Config(), connection: testQuery.Driver().Pool().Writers[0].Connection, queries: queries,
	})
	app.EXPECT().MakeLog().Return(utils.NewTestLog())
	app.EXPECT().MakeTelemetry().Return(nil)
	instance, err := ormFactory(app)
	if err != nil {
		panic(err)
	}
	orm := instance.(contractsorm.Orm)
	for connection := range queries {
		orm.Connection(connection)
	}
	return orm
}

func newSchema(testQuery *TestQuery, connectionToTestQuery map[string]*TestQuery, connections ...contractsdb.DB) *schema.Schema {
	log := utils.NewTestLog()
	orm := newOrm(testQuery, connectionToTestQuery)

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
