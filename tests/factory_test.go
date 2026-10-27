package tests

import (
	"testing"

	"github.com/stretchr/testify/suite"

	ormcontract "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/factory"
)

type FactoryTestSuite struct {
	suite.Suite
	factory   *factory.FactoryImpl
	testQuery *TestQuery
	query     ormcontract.Query
}

func TestFactoryTestSuite(t *testing.T) {
	suite.Run(t, &FactoryTestSuite{})
}

func (s *FactoryTestSuite) SetupSuite() {
	s.testQuery = NewTestQueryBuilder().Postgres("", false)
	s.testQuery.CreateTable(TestTableUsers, TestTableAuthors)
}

// SetupTest takes a fresh query handle per test, so a fluent call in one test cannot reach the next.
func (s *FactoryTestSuite) SetupTest() {
	s.query = s.testQuery.Query()
	s.factory = factory.NewFactoryImpl(s.query)
}

func (s *FactoryTestSuite) TestTimes() {
	var user []User
	s.Nil(s.factory.Count(2).Make(&user))
	s.Require().True(len(user) == 2)
	s.True(len(user[0].Name) > 0)
	s.True(len(user[1].Name) > 0)
}

func (s *FactoryTestSuite) TestCreate() {
	var user []User
	s.Nil(s.factory.Create(&user))
	s.Require().True(len(user) == 1)
	s.True(user[0].ID > 0)
	s.True(len(user[0].Name) > 0)
	s.True(len(user[0].Avatar) > 0)
	s.NotEmpty(user[0].CreatedAt.String())
	s.NotEmpty(user[0].UpdatedAt.String())

	var user1 User
	s.Nil(s.factory.Create(&user1))
	s.NotNil(user1)
	s.True(user1.ID > 0)
	s.True(len(user1.Avatar) > 0)
	s.NotEmpty(user1.CreatedAt.String())
	s.NotEmpty(user1.UpdatedAt.String())

	var user2 User
	s.Nil(s.factory.Create(&user2, map[string]any{
		"Avatar": "avatar",
	}))
	s.NotNil(user2)
	s.True(user2.ID > 0)
	s.Equal("avatar", user2.Avatar)
	s.NotEmpty(user2.CreatedAt.String())
	s.NotEmpty(user2.UpdatedAt.String())

	var user3 []User
	s.Nil(s.factory.Count(2).Create(&user3))
	s.Require().True(len(user3) == 2)
	s.True(user3[0].ID > 0)
	s.True(user3[1].ID > 0)
	s.True(len(user3[0].Name) > 0)
	s.True(len(user3[1].Name) > 0)
}

func (s *FactoryTestSuite) TestCreateQuietly() {
	var user []User
	s.Nil(s.factory.CreateQuietly(&user))
	s.Require().True(len(user) == 1)
	s.True(user[0].ID > 0)
	s.True(len(user[0].Name) > 0)
	s.True(len(user[0].Avatar) > 0)
	s.NotEmpty(user[0].CreatedAt.String())
	s.NotEmpty(user[0].UpdatedAt.String())

	var user1 User
	s.Nil(s.factory.CreateQuietly(&user1))
	s.NotNil(user1)
	s.True(user1.ID > 0)
	s.True(len(user1.Avatar) > 0)
	s.NotEmpty(user1.CreatedAt.String())
	s.NotEmpty(user1.UpdatedAt.String())

	var user2 User
	s.Nil(s.factory.CreateQuietly(&user2, map[string]any{
		"Avatar": "avatar",
	}))
	s.NotNil(user2)
	s.True(user2.ID > 0)
	s.Equal("avatar", user2.Avatar)
	s.NotEmpty(user2.CreatedAt.String())
	s.NotEmpty(user2.UpdatedAt.String())

	var user3 []User
	s.Nil(s.factory.Count(2).CreateQuietly(&user3))
	s.Require().True(len(user3) == 2)
	s.True(user3[0].ID > 0)
	s.True(user3[1].ID > 0)
	s.True(len(user3[0].Name) > 0)
	s.True(len(user3[1].Name) > 0)
}

func (s *FactoryTestSuite) TestMake() {
	var user User
	s.Nil(s.factory.Make(&user))
	s.True(user.ID == 0)
	s.True(len(user.Name) > 0)
	s.True(len(user.Avatar) > 0)
	s.NotEmpty(user.CreatedAt.String())
	s.NotEmpty(user.UpdatedAt.String())

	var user1 User
	s.Nil(s.factory.Make(&user1, map[string]any{
		"Avatar": "avatar",
	}))
	s.True(user1.ID == 0)
	s.True(len(user1.Name) > 0)
	s.Equal("avatar", user1.Avatar)
	s.NotEmpty(user1.CreatedAt.String())
	s.NotEmpty(user1.UpdatedAt.String())

	var users []User
	s.Nil(s.factory.Make(&users))
	s.Require().True(len(users) == 1)
	s.True(users[0].ID == 0)
	s.True(len(users[0].Name) > 0)
	s.True(len(users[0].Avatar) > 0)
	s.NotEmpty(users[0].CreatedAt.String())
	s.NotEmpty(users[0].UpdatedAt.String())

	var author Author
	s.Nil(s.factory.Make(&author))
	s.True(author.ID == 1)
	s.True(len(author.Name) > 0)
	s.True(author.BookID == 2)
	s.NotEmpty(author.CreatedAt.String())
	s.NotEmpty(author.UpdatedAt.String())
}
