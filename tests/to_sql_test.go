package tests

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/gradii/framework/database/gorm"
	mockslog "github.com/gradii/framework/mocks/log"
)

type ToSqlTestSuite struct {
	suite.Suite
	mockLog   *mockslog.Log
	testQuery *TestQuery
}

func TestToSqlTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, &ToSqlTestSuite{})
}

func (s *ToSqlTestSuite) SetupSuite() {
	s.testQuery = NewTestQueryBuilder().Postgres("", false)
	s.testQuery.CreateTable(TestTableUsers)
}

func (s *ToSqlTestSuite) SetupTest() {
	s.mockLog = mockslog.NewLog(s.T())
}

func (s *ToSqlTestSuite) TestCount() {
	toSql := gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Distinct().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Distinct("name").Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(distinct \"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Distinct("name", "avatar").Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(distinct \"name\", \"avatar\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Select("name", "avatar").Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Select("name as n").Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(\"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Select("name as n").Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(\"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Select("name").Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(\"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Count())

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Count())

	// Without global scopes. Removing every scope drops the model's SoftDeletingScope too, so the
	// soft-delete predicate is gone - Laravel's withoutGlobalScopes() removes every registered scope
	// (oracle, illuminate/database v12.55.1, /tmp/r12/scope_removal_probe.php §B: `select * from
	// "sr_users"`). A removal of only a named user scope keeps it - the "name" block below.
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = $1", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = 1", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" is null", toSql.Count())

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Count())
}

func (s *ToSqlTestSuite) TestCreate() {
	user := User{Name: "to_sql_create"}

	toSql := gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("insert into \"users\" (\"created_at\", \"name\", \"updated_at\") values ($1, $2, $3) returning \"id\"", toSql.Create(&user))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Contains(toSql.Create(&user), "insert into \"users\" (\"created_at\", \"name\", \"updated_at\") values (")
	s.Contains(toSql.Create(&user), "'to_sql_create',")

	// global scopes
	globalScope := GlobalScope{Name: "to_sql_create", Avatar: "avatar"}

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, false)
	sql := toSql.Create(&globalScope)
	s.Equal(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"name\", \"updated_at\") values ($1, $2, $3, $4) returning \"id\"")

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Create(&globalScope)
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "'to_sql_create',")
	s.NotContains(sql, "WHERE")

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	sql = toSql.Create(&globalScope)
	s.Equal(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"name\", \"updated_at\") values ($1, $2, $3, $4) returning \"id\"")

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Create(&globalScope)
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "'to_sql_create',")
	s.NotContains(sql, "WHERE")
}

func (s *ToSqlTestSuite) TestDelete() {
	toSql := gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"users\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" is null", toSql.Delete(&User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("delete from \"roles\" where \"id\" = $1", toSql.Delete(&Role{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	sql := toSql.Delete(&User{})
	s.Contains(sql, "update \"users\" set \"deleted_at\" = ")
	s.Contains(sql, "where \"id\" = 1 and \"users\".\"deleted_at\" is null")

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("delete from \"roles\" where \"id\" = 1", toSql.Delete(&Role{}))

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	sql = toSql.Delete(&GlobalScope{})
	s.Equal("update \"global_scopes\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"name\" = $5 and \"global_scopes\".\"deleted_at\" is null", sql)

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Delete(&GlobalScope{})
	s.Contains(sql, "update \"global_scopes\" set \"deleted_at\" = '")
	s.Contains(sql, "where \"id\" = 1")
	s.Contains(sql, "\"name\" = 'name_scope'")
	s.Contains(sql, "\"avatar\" = 'avatar_scope'")
	s.Contains(sql, "\"global_scopes\".\"deleted_at\" is null")

	// Without global scopes. The soft-delete rewrite compiles from the same removal-aware predicate:
	// with every scope removed there is no `deleted_at is null` left (oracle: a mass delete on a
	// SoftDeletes model is an update whose where has no predicate once withoutGlobalScopes() ran -
	// /tmp/r12/scope_removal_probe4.php §D1).
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	sql = toSql.Delete(&GlobalScope{})
	s.Equal("update \"global_scopes\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3", sql)

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Delete(&GlobalScope{})
	s.Contains(sql, "update \"global_scopes\" set \"deleted_at\" = '")
	s.Contains(sql, "' where \"id\" = 1")

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	sql = toSql.Delete(&GlobalScope{})
	s.Equal("update \"global_scopes\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"global_scopes\".\"deleted_at\" is null", sql)

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Delete(&GlobalScope{})
	s.Contains(sql, "update \"global_scopes\" set \"deleted_at\" = '")
	s.Contains(sql, "' where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null")
}

func (s *ToSqlTestSuite) TestFind() {
	toSql := gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" is null", toSql.Find(&User{}))

	// The lone scalar is find($id) — whereKey($id)->first() — so the read is one row: oracle
	// (sqlite, illuminate/database v12.55.1) `select * from "users" where "users"."id" = ? limit 1`.
	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"users\" where \"users\".\"id\" = $1 and \"users\".\"deleted_at\" is null limit 1", toSql.Find(&User{}, 1))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Find(&User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"users\" where \"users\".\"id\" = 1 and \"users\".\"deleted_at\" is null limit 1", toSql.Find(&User{}, 1))

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" is null", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Find(&GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" is null", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Find(&GlobalScope{}))
}

func (s *ToSqlTestSuite) TestFirst() {
	toSql := gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" is null order by \"users\".\"id\" limit 1", toSql.First(&User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null order by \"users\".\"id\" limit 1", toSql.First(&User{}))

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" is null order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" is null order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" is null order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))
}

func (s *ToSqlTestSuite) TestForceDelete() {
	toSql := gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("delete from \"users\" where \"id\" = $1", toSql.ForceDelete(&User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("delete from \"roles\" where \"id\" = $1", toSql.ForceDelete(&Role{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("delete from \"users\" where \"id\" = 1", toSql.ForceDelete(&User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("delete from \"roles\" where \"id\" = 1", toSql.ForceDelete(&Role{}))

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("delete from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("delete from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope'", toSql.ForceDelete(&GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("delete from \"global_scopes\" where \"id\" = $1", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("delete from \"global_scopes\" where \"id\" = 1", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("delete from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("delete from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope'", toSql.ForceDelete(&GlobalScope{}))
}

func (s *ToSqlTestSuite) TestGet() {
	toSql := gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" is null", toSql.Get([]User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Get([]User{}))

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" is null", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Get([]GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" is null", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Get([]GlobalScope{}))
}

func (s *ToSqlTestSuite) TestInvalidModel() {
	s.mockLog.EXPECT().Errorf("failed to get sql: %v", mock.MatchedBy(func(err error) bool {
		return s.EqualError(err, "string: Table not set, please add a TableName() method returning the table name")
	})).Once()

	toSql := gorm.NewToSql(s.testQuery.Query().Model("invalid").Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Empty(toSql.Get([]User{}))
}

func (s *ToSqlTestSuite) TestPluck() {
	toSql := gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select \"id\" from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" is null", toSql.Pluck("id", User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select \"id\" from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Pluck("id", User{}))

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" is null", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Pluck("id", GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = $1", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = 1", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" is null", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Pluck("id", GlobalScope{}))
}

func (s *ToSqlTestSuite) TestSave() {
	toSql := gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("insert into \"users\" (\"created_at\", \"updated_at\") values ($1, $2) returning \"id\"", toSql.Save(&User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"users\" set \"avatar\" = $1, \"bio\" = $2, \"created_at\" = $3, \"deleted_at\" = $4, \"name\" = $5, \"ratio\" = $6, \"updated_at\" = $7 where \"users\".\"deleted_at\" is null and \"id\" = $8", toSql.Save(&User{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	sql := toSql.Save(&User{Name: "to_sql_save"})
	s.Contains(sql, "insert into \"users\" (\"created_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "'to_sql_save',")

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Save(&User{Model: Model{ID: 2}, Name: "to_sql_save"})
	s.Contains(sql, "update \"users\" set \"avatar\" = ")
	s.Contains(sql, "\"updated_at\" = ")
	s.Contains(sql, "\"deleted_at\" = ")
	s.Contains(sql, "\"name\" = 'to_sql_save'")
	s.Contains(sql, "\"bio\" = ")
	s.Contains(sql, "\"avatar\" = ")
	s.Contains(sql, "where \"users\".\"deleted_at\" is null and \"id\" = 2")

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("insert into \"global_scopes\" (\"created_at\", \"updated_at\") values ($1, $2) returning \"id\"", toSql.Save(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"avatar\" = $1, \"created_at\" = $2, \"deleted_at\" = $3, \"name\" = $4, \"updated_at\" = $5 where \"avatar\" = $6 and \"name\" = $7 and \"global_scopes\".\"deleted_at\" is null and \"id\" = $8", toSql.Save(&GlobalScope{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "'to_sql_save',")

	toSql = gorm.NewToSql(s.testQuery.Query().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Model: Model{ID: 2}, Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "update \"global_scopes\" set \"avatar\" = 'avatar', \"created_at\" = NULL, \"deleted_at\" = NULL")
	s.Contains(sql, "\"name\" = 'to_sql_save'")
	s.Contains(sql, "\"avatar\" = 'avatar'")
	s.Contains(sql, "\"deleted_at\" = NULL")
	s.Contains(sql, "\"name\" = 'name_scope'")
	s.Contains(sql, "\"avatar\" = 'avatar_scope'")
	s.Contains(sql, "\"global_scopes\".\"deleted_at\" is null and \"id\" = 2")

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("insert into \"global_scopes\" (\"created_at\", \"updated_at\") values ($1, $2) returning \"id\"", toSql.Save(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"avatar\" = $1, \"created_at\" = $2, \"deleted_at\" = $3, \"name\" = $4, \"updated_at\" = $5 where \"id\" = $6", toSql.Save(&GlobalScope{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "'to_sql_save',")

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Model: Model{ID: 2}, Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "update \"global_scopes\" set \"avatar\" = 'avatar', \"created_at\" = NULL, \"deleted_at\" = NULL")
	s.Regexp(`"name" = 'to_sql_save', "updated_at" = '\d{4}-\d{2}-\d{2}[^']+' where "id" = 2`, sql)

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("insert into \"global_scopes\" (\"created_at\", \"updated_at\") values ($1, $2) returning \"id\"", toSql.Save(&GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"avatar\" = $1, \"created_at\" = $2, \"deleted_at\" = $3, \"name\" = $4, \"updated_at\" = $5 where \"avatar\" = $6 and \"global_scopes\".\"deleted_at\" is null and \"id\" = $7", toSql.Save(&GlobalScope{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "'to_sql_save',")

	toSql = gorm.NewToSql(s.testQuery.Query().WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Model: Model{ID: 2}, Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "update \"global_scopes\" set \"avatar\" = 'avatar', \"created_at\" = NULL, \"deleted_at\" = NULL")
	s.Regexp(`"name" = 'to_sql_save', "updated_at" = '\d{4}-\d{2}-\d{2}[^']+' where "avatar" = 'avatar_scope' and "global_scopes"\."deleted_at" is null and "id" = 2`, sql)
}

func (s *ToSqlTestSuite) TestSum() {
	toSql := gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" is null", toSql.Sum("id", User{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" is null", toSql.Sum("id", User{}))

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" is null", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Sum("id", GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = $1", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = 1", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" is null", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null", toSql.Sum("id", GlobalScope{}))
}

func (s *ToSqlTestSuite) TestUpdate() {
	toSql := gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"users\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" is null", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	sql := toSql.Update("name", "goravel")
	s.Contains(sql, "update \"users\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"users\".\"deleted_at\" is null")

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Empty(toSql.Update(0, "goravel"))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	s.Empty(toSql.Update(0, "goravel"))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"users\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" is null", toSql.Update(map[string]any{
		"name": "goravel",
	}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Update(map[string]any{
		"name": "goravel",
	})
	s.Contains(sql, "update \"users\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"users\".\"deleted_at\" is null")

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"users\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" is null", toSql.Update(User{
		Name: "goravel",
	}))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Update(User{
		Name: "goravel",
	})
	s.Contains(sql, "update \"users\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"users\".\"deleted_at\" is null")

	// global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"name\" = $5 and \"global_scopes\".\"deleted_at\" is null", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Update("name", "goravel")
	s.Contains(sql, "update \"global_scopes\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" is null")

	// Without global scopes
	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Update("name", "goravel")
	s.Contains(sql, "update \"global_scopes\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1")

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"global_scopes\".\"deleted_at\" is null", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.testQuery.Query().Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.OrmBuilder), s.mockLog, true)
	sql = toSql.Update("name", "goravel")
	s.Contains(sql, "update \"global_scopes\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" is null")
}

func (s *ToSqlTestSuite) TestEmptyInSliceBug() {
	// Test 1: Where with IN operator and empty slice
	// Expected: should produce "0 = 1" (matches nothing)
	toSql1 := gorm.NewToSql(s.testQuery.Query().Model(&User{}).Where("name", "IN", []any{}).(*gorm.OrmBuilder), s.mockLog, true)
	sql1 := toSql1.Get([]User{})
	s.T().Logf("Test 1 - Where(name, IN, []any{}):\nSQL: %s\n", sql1)
	s.NotContains(sql1, "in ()", "Should not produce malformed 'in ()'")

	// Test 2: WhereAny with IN operator and empty slice
	// Expected: should also produce "0 = 1" or similar, NOT "name in ()"
	toSql2 := gorm.NewToSql(s.testQuery.Query().Model(&User{}).WhereAny([]string{"name"}, "IN", []any{}).(*gorm.OrmBuilder), s.mockLog, true)
	sql2 := toSql2.Get([]User{})
	s.T().Logf("Test 2 - WhereAny([name], IN, []any{}):\nSQL: %s\n", sql2)
	s.NotContains(sql2, "in ()", "Should not produce malformed 'in ()'")

	// Test 3: WhereAny with NOT IN and empty slice
	toSql3 := gorm.NewToSql(s.testQuery.Query().Model(&User{}).WhereAny([]string{"name"}, "NOT IN", []any{}).(*gorm.OrmBuilder), s.mockLog, true)
	sql3 := toSql3.Get([]User{})
	s.T().Logf("Test 3 - WhereAny([name], NOT IN, []any{}):\nSQL: %s\n", sql3)
	s.NotContains(sql3, "not in ()", "Should not produce malformed 'not in ()'")
}
