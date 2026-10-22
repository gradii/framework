package tests

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	ormcontract "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/database/gorm"
	mockslog "github.com/goravel/framework/mocks/log"
)

type ToSqlTestSuite struct {
	suite.Suite
	mockLog *mockslog.Log
	query   ormcontract.Query
}

func TestToSqlTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, &ToSqlTestSuite{})
}

func (s *ToSqlTestSuite) SetupSuite() {
	postgresTestQuery := NewTestQueryBuilder().Postgres("", false)
	postgresTestQuery.CreateTable(TestTableUsers)
	s.query = postgresTestQuery.Query()
}

func (s *ToSqlTestSuite) SetupTest() {
	s.mockLog = mockslog.NewLog(s.T())
}

func (s *ToSqlTestSuite) TestCount() {
	toSql := gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Distinct().Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Distinct("name").Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(distinct \"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Distinct("name", "avatar").Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Select("name", "avatar").Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Select("name as n").Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(\"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Select("name as n").Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(\"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&User{}).Select("name").Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(\"name\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Count())

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Count())

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = $1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Count())

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	s.Equal("select count(*) as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Count())
}

func (s *ToSqlTestSuite) TestCreate() {
	user := User{Name: "to_sql_create"}

	toSql := gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, false)
	s.Equal("insert into \"users\" (\"avatar\", \"bio\", \"created_at\", \"deleted_at\", \"name\", \"ratio\", \"updated_at\") values ($1, $2, $3, $4, $5, $6, $7) returning \"id\"", toSql.Create(&user))

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Contains(toSql.Create(&user), "insert into \"users\" (\"avatar\", \"bio\", \"created_at\", \"deleted_at\", \"name\", \"ratio\", \"updated_at\") values (")
	s.Contains(toSql.Create(&user), "NULL, 'to_sql_create', 0,")

	// global scopes
	globalScope := GlobalScope{Name: "to_sql_create", Avatar: "avatar"}

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, false)
	sql := toSql.Create(&globalScope)
	s.Equal(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values ($1, $2, $3, $4, $5) returning \"id\"")

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, true)
	sql = toSql.Create(&globalScope)
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "NULL, 'to_sql_create',")
	s.NotContains(sql, "WHERE")

	// Without global scopes
	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	sql = toSql.Create(&globalScope)
	s.Equal(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values ($1, $2, $3, $4, $5) returning \"id\"")

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	sql = toSql.Create(&globalScope)
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "NULL, 'to_sql_create',")
	s.NotContains(sql, "WHERE")
}

func (s *ToSqlTestSuite) TestDelete() {
	toSql := gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("update \"users\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" IS NULL", toSql.Delete(&User{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("delete from \"roles\" where \"id\" = $1", toSql.Delete(&Role{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	sql := toSql.Delete(&User{})
	s.Contains(sql, "update \"users\" set \"deleted_at\" = ")
	s.Contains(sql, "where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL")

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("delete from \"roles\" where \"id\" = 1", toSql.Delete(&Role{}))

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	sql = toSql.Delete(&GlobalScope{})
	s.Equal("update \"global_scopes\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"name\" = $5 and \"global_scopes\".\"deleted_at\" IS NULL", sql)

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	sql = toSql.Delete(&GlobalScope{})
	s.Contains(sql, "update \"global_scopes\" set \"deleted_at\" = '")
	s.Contains(sql, "where \"id\" = 1")
	s.Contains(sql, "\"name\" = 'name_scope'")
	s.Contains(sql, "\"avatar\" = 'avatar_scope'")
	s.Contains(sql, "\"global_scopes\".\"deleted_at\" IS NULL")

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	sql = toSql.Delete(&GlobalScope{})
	s.Equal("update \"global_scopes\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL", sql)

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	sql = toSql.Delete(&GlobalScope{})
	s.Contains(sql, "update \"global_scopes\" set \"deleted_at\" = '")
	s.Contains(sql, "' where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL")

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	sql = toSql.Delete(&GlobalScope{})
	s.Equal("update \"global_scopes\" set \"deleted_at\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"global_scopes\".\"deleted_at\" IS NULL", sql)

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	sql = toSql.Delete(&GlobalScope{})
	s.Contains(sql, "update \"global_scopes\" set \"deleted_at\" = '")
	s.Contains(sql, "' where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL")
}

func (s *ToSqlTestSuite) TestFind() {
	toSql := gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" IS NULL", toSql.Find(&User{}))

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"users\" where \"users\".\"id\" = $1 and \"users\".\"deleted_at\" IS NULL", toSql.Find(&User{}, 1))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Find(&User{}))

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"users\" where \"users\".\"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Find(&User{}, 1))

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Find(&GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Find(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Find(&GlobalScope{}))
}

func (s *ToSqlTestSuite) TestFirst() {
	toSql := gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" IS NULL order by \"users\".\"id\" limit 1", toSql.First(&User{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL order by \"users\".\"id\" limit 1", toSql.First(&User{}))

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" IS NULL order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"global_scopes\".\"deleted_at\" IS NULL order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" IS NULL order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL order by \"global_scopes\".\"id\" limit 1", toSql.First(&GlobalScope{}))
}

func (s *ToSqlTestSuite) TestForceDelete() {
	toSql := gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("delete from \"users\" where \"id\" = $1", toSql.ForceDelete(&User{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("delete from \"roles\" where \"id\" = $1", toSql.ForceDelete(&Role{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("delete from \"users\" where \"id\" = 1", toSql.ForceDelete(&User{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("delete from \"roles\" where \"id\" = 1", toSql.ForceDelete(&Role{}))

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("delete from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("delete from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope'", toSql.ForceDelete(&GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("delete from \"global_scopes\" where \"id\" = $1", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	s.Equal("delete from \"global_scopes\" where \"id\" = 1", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("delete from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2", toSql.ForceDelete(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	s.Equal("delete from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope'", toSql.ForceDelete(&GlobalScope{}))
}

func (s *ToSqlTestSuite) TestGet() {
	toSql := gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" IS NULL", toSql.Get([]User{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Get([]User{}))

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Get([]GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("select * from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Get([]GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	s.Equal("select * from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Get([]GlobalScope{}))
}

func (s *ToSqlTestSuite) TestInvalidModel() {
	s.mockLog.EXPECT().Errorf("failed to get sql: %v", mock.MatchedBy(func(err error) bool {
		return s.EqualError(err, "string: Table not set, please add a TableName() method returning the table name")
	})).Once()

	toSql := gorm.NewToSql(s.query.Model("invalid").Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Empty(toSql.Get([]User{}))
}

func (s *ToSqlTestSuite) TestPluck() {
	toSql := gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select \"id\" from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" IS NULL", toSql.Pluck("id", User{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select \"id\" from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Pluck("id", User{}))

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Pluck("id", GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = $1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Pluck("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	s.Equal("select \"id\" from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Pluck("id", GlobalScope{}))
}

func (s *ToSqlTestSuite) TestSave() {
	toSql := gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, false)
	s.Equal("insert into \"users\" (\"avatar\", \"bio\", \"created_at\", \"deleted_at\", \"name\", \"ratio\", \"updated_at\") values ($1, $2, $3, $4, $5, $6, $7) returning \"id\"", toSql.Save(&User{}))

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, false)
	s.Equal("update \"users\" set \"avatar\" = $1, \"bio\" = $2, \"created_at\" = $3, \"deleted_at\" = $4, \"name\" = $5, \"ratio\" = $6, \"updated_at\" = $7 where \"users\".\"deleted_at\" IS NULL and \"id\" = $8", toSql.Save(&User{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	sql := toSql.Save(&User{Name: "to_sql_save"})
	s.Contains(sql, "insert into \"users\" (\"avatar\", \"bio\", \"created_at\", \"deleted_at\", \"name\", \"ratio\", \"updated_at\") values (")
	s.Contains(sql, "NULL, 'to_sql_save',")

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, true)
	sql = toSql.Save(&User{Model: Model{ID: 2}, Name: "to_sql_save"})
	s.Contains(sql, "update \"users\" set \"avatar\" = ")
	s.Contains(sql, "\"updated_at\" = ")
	s.Contains(sql, "\"deleted_at\" = ")
	s.Contains(sql, "\"name\" = 'to_sql_save'")
	s.Contains(sql, "\"bio\" = ")
	s.Contains(sql, "\"avatar\" = ")
	s.Contains(sql, "where \"users\".\"deleted_at\" IS NULL and \"id\" = 2")

	// global scopes
	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, false)
	s.Equal("insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values ($1, $2, $3, $4, $5) returning \"id\"", toSql.Save(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"avatar\" = $1, \"created_at\" = $2, \"deleted_at\" = $3, \"name\" = $4, \"updated_at\" = $5 where \"avatar\" = $6 and \"name\" = $7 and \"global_scopes\".\"deleted_at\" IS NULL and \"id\" = $8", toSql.Save(&GlobalScope{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "NULL, 'to_sql_save',")

	toSql = gorm.NewToSql(s.query.(*gorm.Query), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Model: Model{ID: 2}, Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "update \"global_scopes\" set \"avatar\" = 'avatar', \"created_at\" = NULL, \"deleted_at\" = NULL")
	s.Contains(sql, "\"name\" = 'to_sql_save'")
	s.Contains(sql, "\"avatar\" = 'avatar'")
	s.Contains(sql, "\"deleted_at\" = NULL")
	s.Contains(sql, "\"name\" = 'name_scope'")
	s.Contains(sql, "\"avatar\" = 'avatar_scope'")
	s.Contains(sql, "\"global_scopes\".\"deleted_at\" IS NULL and \"id\" = 2")

	// Without global scopes
	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values ($1, $2, $3, $4, $5) returning \"id\"", toSql.Save(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"avatar\" = $1, \"created_at\" = $2, \"deleted_at\" = $3, \"name\" = $4, \"updated_at\" = $5 where \"global_scopes\".\"deleted_at\" IS NULL and \"id\" = $6", toSql.Save(&GlobalScope{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "NULL, 'to_sql_save',")

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Model: Model{ID: 2}, Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "update \"global_scopes\" set \"avatar\" = 'avatar', \"created_at\" = NULL, \"deleted_at\" = NULL")
	s.Contains(sql, "\"name\" = 'to_sql_save', \"updated_at\" = NULL where \"global_scopes\".\"deleted_at\" IS NULL and \"id\" = 2")

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values ($1, $2, $3, $4, $5) returning \"id\"", toSql.Save(&GlobalScope{}))

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"avatar\" = $1, \"created_at\" = $2, \"deleted_at\" = $3, \"name\" = $4, \"updated_at\" = $5 where \"avatar\" = $6 and \"global_scopes\".\"deleted_at\" IS NULL and \"id\" = $7", toSql.Save(&GlobalScope{Model: Model{ID: 2}}))

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "insert into \"global_scopes\" (\"avatar\", \"created_at\", \"deleted_at\", \"name\", \"updated_at\") values (")
	s.Contains(sql, "NULL, 'to_sql_save',")

	toSql = gorm.NewToSql(s.query.WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	sql = toSql.Save(&GlobalScope{Model: Model{ID: 2}, Name: "to_sql_save", Avatar: "avatar"})
	s.Contains(sql, "update \"global_scopes\" set \"avatar\" = 'avatar', \"created_at\" = NULL, \"deleted_at\" = NULL")
	s.Contains(sql, "\"name\" = 'to_sql_save', \"updated_at\" = NULL where \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL and \"id\" = 2")
}

func (s *ToSqlTestSuite) TestSum() {
	toSql := gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"users\" where \"id\" = $1 and \"users\".\"deleted_at\" IS NULL", toSql.Sum("id", User{}))

	toSql = gorm.NewToSql(s.query.Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"users\" where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL", toSql.Sum("id", User{}))

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"name\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Sum("id", GlobalScope{}))

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = $1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = $1 and \"avatar\" = $2 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Sum("id", GlobalScope{}))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	s.Equal("select sum(\"id\") as aggregate from \"global_scopes\" where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Sum("id", GlobalScope{}))
}

func (s *ToSqlTestSuite) TestUpdate() {
	toSql := gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("update \"users\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" IS NULL", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	sql := toSql.Update("name", "goravel")
	s.Contains(sql, "update \"users\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL")

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Empty(toSql.Update(0, "goravel"))

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	s.Empty(toSql.Update(0, "goravel"))

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("update \"users\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" IS NULL", toSql.Update(map[string]any{
		"name": "goravel",
	}))

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	sql = toSql.Update(map[string]any{
		"name": "goravel",
	})
	s.Contains(sql, "update \"users\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL")

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("update \"users\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"users\".\"deleted_at\" IS NULL", toSql.Update(User{
		Name: "goravel",
	}))

	toSql = gorm.NewToSql(s.query.Model(&User{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	sql = toSql.Update(User{
		Name: "goravel",
	})
	s.Contains(sql, "update \"users\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"users\".\"deleted_at\" IS NULL")

	// global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"name\" = $5 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).(*gorm.Query), s.mockLog, true)
	sql = toSql.Update("name", "goravel")
	s.Contains(sql, "update \"global_scopes\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"name\" = 'name_scope' and \"global_scopes\".\"deleted_at\" IS NULL")

	// Without global scopes
	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes().(*gorm.Query), s.mockLog, true)
	sql = toSql.Update("name", "goravel")
	s.Contains(sql, "update \"global_scopes\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"global_scopes\".\"deleted_at\" IS NULL")

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, false)
	s.Equal("update \"global_scopes\" set \"name\" = $1, \"updated_at\" = $2 where \"id\" = $3 and \"avatar\" = $4 and \"global_scopes\".\"deleted_at\" IS NULL", toSql.Update("name", "goravel"))

	toSql = gorm.NewToSql(s.query.Model(&GlobalScope{}).Where("id", 1).WithoutGlobalScopes("name").(*gorm.Query), s.mockLog, true)
	sql = toSql.Update("name", "goravel")
	s.Contains(sql, "update \"global_scopes\" set \"name\" = 'goravel', \"updated_at\" = ")
	s.Contains(sql, "' where \"id\" = 1 and \"avatar\" = 'avatar_scope' and \"global_scopes\".\"deleted_at\" IS NULL")
}

func (s *ToSqlTestSuite) TestEmptyInSliceBug() {
	// Test 1: Where with IN operator and empty slice
	// Expected: should produce "0 = 1" (matches nothing)
	toSql1 := gorm.NewToSql(s.query.Model(&User{}).Where("name", "IN", []any{}).(*gorm.Query), s.mockLog, true)
	sql1 := toSql1.Get([]User{})
	s.T().Logf("Test 1 - Where(name, IN, []any{}):\nSQL: %s\n", sql1)
	s.NotContains(sql1, "IN ()", "Should not produce malformed 'IN ()'")

	// Test 2: WhereAny with IN operator and empty slice
	// Expected: should also produce "0 = 1" or similar, NOT "name IN ()"
	toSql2 := gorm.NewToSql(s.query.Model(&User{}).WhereAny([]string{"name"}, "IN", []any{}).(*gorm.Query), s.mockLog, true)
	sql2 := toSql2.Get([]User{})
	s.T().Logf("Test 2 - WhereAny([name], IN, []any{}):\nSQL: %s\n", sql2)
	s.NotContains(sql2, "IN ()", "Should not produce malformed 'IN ()'")

	// Test 3: WhereAny with NOT IN and empty slice
	toSql3 := gorm.NewToSql(s.query.Model(&User{}).WhereAny([]string{"name"}, "NOT IN", []any{}).(*gorm.Query), s.mockLog, true)
	sql3 := toSql3.Get([]User{})
	s.T().Logf("Test 3 - WhereAny([name], NOT IN, []any{}):\nSQL: %s\n", sql3)
	s.NotContains(sql3, "NOT IN ()", "Should not produce malformed 'NOT IN ()'")
}
