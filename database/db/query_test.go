package db

import (
	"context"
	databasesql "database/sql"
	"database/sql/driver"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/gradii/framework/contracts/database/db"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	mocksdb "github.com/gradii/framework/mocks/database/db"
	mockslogger "github.com/gradii/framework/mocks/database/logger"
	"github.com/gradii/framework/support/carbon"
)

// TestUser is the shared fixture. Name and Age are excluded from the database mapping, which is
// what several tests rely on.
type TestUser struct {
	ID    uint `db:"id"`
	Phone string
	Email string
	Name  string `db:"-"`
	Age   int    `db:"-"`
}

func TestSelectResetsColumnsAndBindings(t *testing.T) {
	query := newDialectTestQuery(context.Background(), nil, nil, querybuilder.NewBaseGrammar(""), "", nil, "users", nil)
	query.builder.SelectRaw("? as old", []any{1}).Where("id", 9)

	snapshot := query.Clone().(*Query)
	selected := query.Select("name").(*Query)
	assert.Same(t, query, selected)
	assert.Equal(t, `select "name" from "users" where "id" = ?`, selected.builder.ToSQL())
	assert.Equal(t, []any{9}, selected.builder.GetBindings())
	assert.Equal(t, `select ? as old from "users" where "id" = ?`, snapshot.builder.ToSQL())
	assert.Equal(t, []any{1, 9}, snapshot.builder.GetBindings())
}

// MockResult is a database/sql.Result whose behaviour the tests script.
type MockResult struct {
	mock.Mock
}

func (m *MockResult) LastInsertId() (int64, error) {
	arguments := m.Called()

	return arguments.Get(0).(int64), arguments.Error(1)
}

func (m *MockResult) RowsAffected() (int64, error) {
	arguments := m.Called()

	return arguments.Get(0).(int64), arguments.Error(1)
}

type QueryTestSuite struct {
	suite.Suite
	ctx              context.Context
	grammar          querybuilder.Grammar
	mockLogger       *mockslogger.Logger
	mockReadBuilder  *mocksdb.Builder
	mockWriteBuilder *mocksdb.Builder
	now              *carbon.Carbon
	query            *Query
}

func TestQueryTestSuite(t *testing.T) {
	suite.Run(t, &QueryTestSuite{})
}

func (s *QueryTestSuite) SetupTest() {
	s.ctx = context.Background()
	s.grammar = querybuilder.NewBaseGrammar("")
	s.mockLogger = mockslogger.NewLogger(s.T())
	s.mockReadBuilder = mocksdb.NewBuilder(s.T())
	s.mockWriteBuilder = mocksdb.NewBuilder(s.T())
	s.now = carbon.Now()
	carbon.SetTestNow(s.now)

	s.query = newDialectTestQuery(s.ctx, s.mockReadBuilder, s.mockWriteBuilder, s.grammar, querybuilder.DialectMySQL, s.mockLogger, "users", nil)
}

// TestWhereAnyGroupConnector pins the connector that joins WhereAny's OR-group to the clauses
// before it, for every grammar the facade can run on. Laravel's whereAny($columns, $operator,
// $value, $boolean = 'and') puts the "or" inside the column loop and joins the group with "and", so
// a preceding clause must survive; the port was re-joining the whole group with "or", which dropped
// that clause. WhereAll/WhereNone already used "and" and are pinned here for contrast.
//
// The connector lives in the shared whereNested/compileWheres path, so it is dialect-independent by
// construction - but the unit test that guards it was built on a single MySQL grammar, so a
// dialect-specific regression in the group's wrap, negation or placeholder rendering would have
// gone unnoticed until the integration suite. Each expectation below is the phrase Laravel's own
// grammar produces for that dialect (illuminate/database v12.55.1); the placeholders are the only
// per-dialect difference: `?` on MySQL/SQLite, `$n` on PostgreSQL, `@pn` on SQL Server.
func (s *QueryTestSuite) TestWhereAnyGroupConnector() {
	tests := []struct {
		name     string
		grammar  querybuilder.Grammar
		driver   string
		where    string
		all      string
		none     string
		adjacent string
	}{
		{
			name:     "mysql",
			grammar:  querybuilder.NewBaseGrammar(""),
			driver:   querybuilder.DialectMySQL,
			where:    "select * from \"users\" where \"name\" = ? and (\"email\" = ? or \"phone\" = ?)",
			all:      "select * from \"users\" where \"name\" = ? and (\"email\" = ? and \"phone\" = ?)",
			none:     "select * from \"users\" where \"name\" = ? and not (\"email\" = ? or \"phone\" = ?)",
			adjacent: "select * from \"users\" where (\"weight\" >= ?) and (\"height\" <= ?)",
		},
		{
			name:     "postgres",
			grammar:  querybuilder.NewBaseGrammar(""),
			driver:   querybuilder.DialectPostgres,
			where:    `select * from "users" where "name" = $1 and ("email" = $2 or "phone" = $3)`,
			all:      `select * from "users" where "name" = $1 and ("email" = $2 and "phone" = $3)`,
			none:     `select * from "users" where "name" = $1 and not ("email" = $2 or "phone" = $3)`,
			adjacent: `select * from "users" where ("weight" >= $1) and ("height" <= $2)`,
		},
		{
			name:     "sqlite",
			grammar:  querybuilder.NewBaseGrammar(""),
			driver:   querybuilder.DialectSqlite,
			where:    `select * from "users" where "name" = ? and ("email" = ? or "phone" = ?)`,
			all:      `select * from "users" where "name" = ? and ("email" = ? and "phone" = ?)`,
			none:     `select * from "users" where "name" = ? and not ("email" = ? or "phone" = ?)`,
			adjacent: `select * from "users" where ("weight" >= ?) and ("height" <= ?)`,
		},
		{
			name:     "sqlserver",
			grammar:  querybuilder.NewBaseGrammar(""),
			driver:   querybuilder.DialectSqlServer,
			where:    "select * from \"users\" where \"name\" = @p1 and (\"email\" = @p2 or \"phone\" = @p3)",
			all:      "select * from \"users\" where \"name\" = @p1 and (\"email\" = @p2 and \"phone\" = @p3)",
			none:     "select * from \"users\" where \"name\" = @p1 and not (\"email\" = @p2 or \"phone\" = @p3)",
			adjacent: "select * from \"users\" where (\"weight\" >= @p1) and (\"height\" <= @p2)",
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			// A fresh query per case: a builder carries the accumulated wheres, so reusing s.query
			// would chain the cases together.
			newQuery := func() *Query {
				return newDialectTestQuery(s.ctx, nil, nil, test.grammar, test.driver, nil, "users", nil)
			}

			s.Equal(test.where, newQuery().Where("name", "John").WhereAny([]string{"email", "phone"}, "=", "x").ToSql().Get())
			s.Equal(test.all, newQuery().Where("name", "John").WhereAll([]string{"email", "phone"}, "=", "x").ToSql().Get())
			s.Equal(test.none, newQuery().Where("name", "John").WhereNone([]string{"email", "phone"}, "=", "x").ToSql().Get())
			s.Equal(test.adjacent, newQuery().WhereAny([]string{"weight"}, ">=", 200).WhereAny([]string{"height"}, "<=", 150).ToSql().Get())
		})
	}
}

// TestCompiledSQL asserts the SQL every public method produces. The grammars are covered by
// database/query's own tests, so this table is the facade's contract: each method must reach the
// builder that produces the Laravel-shaped clause.

func (s *QueryTestSuite) TestCompiledSQL() {
	tests := []struct {
		name  string
		build func(db.Query) db.Query
		want  string
	}{
		{"bare", func(q db.Query) db.Query { return q }, "select * from \"users\""},
		{"where", func(q db.Query) db.Query { return q.Where("name", "John") }, "select * from \"users\" where \"name\" = ?"},
		{"where operator", func(q db.Query) db.Query { return q.Where("age", ">", 18) }, "select * from \"users\" where \"age\" > ?"},
		{"or where", func(q db.Query) db.Query { return q.Where("name", "John").OrWhere("name", "Jane") }, "select * from \"users\" where \"name\" = ? or \"name\" = ?"},
		{"where not", func(q db.Query) db.Query { return q.Where("name", "John").WhereNot("name", "Jane") }, "select * from \"users\" where \"name\" = ? and not \"name\" = ?"},
		{"or where not", func(q db.Query) db.Query { return q.Where("name", "John").OrWhereNot("name", "Jane") }, "select * from \"users\" where \"name\" = ? or not \"name\" = ?"},
		{"where raw", func(q db.Query) db.Query { return q.WhereRaw("age > ?", []any{18}) }, "select * from \"users\" where age > ?"},
		{"where string with placeholder", func(q db.Query) db.Query { return q.Where("age > ?", 18) }, "select * from \"users\" where age > ?"},
		{"where slice means in", func(q db.Query) db.Query { return q.Where("age", []int{25, 30}) }, "select * from \"users\" where \"age\" in (?, ?)"},
		{"where in", func(q db.Query) db.Query { return q.WhereIn("age", []any{25, 30}) }, "select * from \"users\" where \"age\" in (?, ?)"},
		{"where not in", func(q db.Query) db.Query { return q.WhereNotIn("age", []any{25, 30}) }, "select * from \"users\" where \"age\" not in (?, ?)"},
		{"where null", func(q db.Query) db.Query { return q.WhereNull("name") }, "select * from \"users\" where \"name\" is null"},
		{"where not null", func(q db.Query) db.Query { return q.WhereNotNull("name") }, "select * from \"users\" where \"name\" is not null"},
		{"where between", func(q db.Query) db.Query { return q.WhereBetween("age", 18, 30) }, "select * from \"users\" where \"age\" between ? and ?"},
		{"where not between", func(q db.Query) db.Query { return q.WhereNotBetween("age", 18, 30) }, "select * from \"users\" where \"age\" not between ? and ?"},
		{"where column", func(q db.Query) db.Query { return q.WhereColumn("a", "b") }, "select * from \"users\" where \"a\" = \"b\""},
		{"where column operator", func(q db.Query) db.Query { return q.WhereColumn("a", "<", "b") }, "select * from \"users\" where \"a\" < \"b\""},
		{"where column arity", func(q db.Query) db.Query { return q.WhereColumn("a", "b", "c") }, "select * from \"users\" where \"a\" = \"b\""},
		{"where like", func(q db.Query) db.Query { return q.WhereLike("name", "%John%") }, "select * from \"users\" where \"name\" like ?"},
		{"where not like", func(q db.Query) db.Query { return q.WhereNotLike("name", "%John%") }, "select * from \"users\" where \"name\" not like ?"},
		{"where any", func(q db.Query) db.Query { return q.WhereAny([]string{"name", "email"}, "=", "John") }, "select * from \"users\" where (\"name\" = ? or \"email\" = ?)"},
		{"where all", func(q db.Query) db.Query { return q.WhereAll([]string{"name", "email"}, "=", "John") }, "select * from \"users\" where (\"name\" = ? and \"email\" = ?)"},
		{"where none", func(q db.Query) db.Query { return q.WhereNone([]string{"name", "email"}, "=", "John") }, "select * from \"users\" where not (\"name\" = ? or \"email\" = ?)"},
		{"select", func(q db.Query) db.Query { return q.Select("id", "name") }, "select \"id\", \"name\" from \"users\""},
		{"select preserves duplicates", func(q db.Query) db.Query { return q.Select("name", "name") }, "select \"name\", \"name\" from \"users\""},
		{"select replaces columns", func(q db.Query) db.Query { return q.Select("id").Select("name") }, "select \"name\" from \"users\""},
		{"select preserves explicit wildcard", func(q db.Query) db.Query { return q.Select("*", "name") }, "select *, \"name\" from \"users\""},
		{"select defaults to wildcard", func(q db.Query) db.Query { return q.Select("id").Select() }, "select * from \"users\""},
		{"distinct", func(q db.Query) db.Query { return q.Distinct() }, "select distinct * from \"users\""},
		{"distinct columns", func(q db.Query) db.Query { return q.Distinct("name") }, "select distinct * from \"users\""},
		{"distinct two columns", func(q db.Query) db.Query { return q.Distinct("name", "age") }, "select distinct * from \"users\""},
		{"group by", func(q db.Query) db.Query { return q.GroupBy("name") }, "select * from \"users\" group by \"name\""},
		{"having", func(q db.Query) db.Query { return q.GroupBy("name").Having("name", "John") }, "select * from \"users\" group by \"name\" having \"name\" = ?"},
		{"order by", func(q db.Query) db.Query { return q.OrderBy("name") }, "select * from \"users\" order by \"name\" asc"},
		{"order by desc", func(q db.Query) db.Query { return q.OrderByDesc("name") }, "select * from \"users\" order by \"name\" desc"},
		{"order by raw", func(q db.Query) db.Query { return q.OrderByRaw("age DESC, id ASC") }, "select * from \"users\" order by age DESC, id ASC"},
		{"latest", func(q db.Query) db.Query { return q.Latest() }, "select * from \"users\" order by \"created_at\" desc"},
		{"latest column", func(q db.Query) db.Query { return q.Latest("name") }, "select * from \"users\" order by \"name\" desc"},
		{"random order", func(q db.Query) db.Query { return q.InRandomOrder() }, "select * from \"users\" order by RANDOM()"},
		{"limit", func(q db.Query) db.Query { return q.Limit(10) }, "select * from \"users\" limit 10"},
		{"offset", func(q db.Query) db.Query { return q.Offset(5) }, "select * from \"users\" offset 5"},
		{"lock for update", func(q db.Query) db.Query { return q.LockForUpdate() }, "select * from \"users\" for update"},
		{"shared lock", func(q db.Query) db.Query { return q.SharedLock() }, "select * from \"users\" lock in share mode"},
		{"join", func(q db.Query) db.Query { return q.Join("posts as p ON users.id = p.user_id", 1) }, "select * from \"users\" inner join posts as p ON users.id = p.user_id"},
		{"left join", func(q db.Query) db.Query { return q.LeftJoin("posts as p ON users.id = p.user_id") }, "select * from \"users\" left join posts as p ON users.id = p.user_id"},
		{"right join", func(q db.Query) db.Query { return q.RightJoin("posts as p ON users.id = p.user_id") }, "select * from \"users\" right join posts as p ON users.id = p.user_id"},
		{"cross join", func(q db.Query) db.Query { return q.CrossJoin("posts as p") }, "select * from \"users\" cross join posts as p"},
		{"count", func(q db.Query) db.Query { return q }, "select count(*) as aggregate from \"users\""},
		{"count one column", func(q db.Query) db.Query { return q.Select("name") }, "select count(\"name\") as aggregate from \"users\""},
		{"count renamed column", func(q db.Query) db.Query { return q.Select("name as n") }, "select count(\"name\") as aggregate from \"users\""},
		{"count distinct column", func(q db.Query) db.Query { return q.Distinct("name") }, "select count(distinct \"name\") as aggregate from \"users\""},
		{"count several columns", func(q db.Query) db.Query { return q.Select("name", "age") }, "select count(*) as aggregate from \"users\""},
		{"value", func(q db.Query) db.Query { return q }, "select \"name\" from \"users\" limit 1"},
		{"pluck", func(q db.Query) db.Query { return q }, "select \"name\" from \"users\""},
		{"sum", func(q db.Query) db.Query { return q }, "select sum(\"age\") as aggregate from \"users\""},
		{"delete", func(q db.Query) db.Query { return q.Where("name", "John") }, "delete from \"users\" where \"name\" = ?"},
		{"update", func(q db.Query) db.Query { return q.Where("id", 1) }, "update \"users\" set \"name\" = ? where \"id\" = ?"},
		{"increment", func(q db.Query) db.Query { return q.Where("name", "John") }, "update \"users\" set \"age\" = \"age\" + 1 where \"name\" = ?"},
		{"decrement", func(q db.Query) db.Query { return q.Where("name", "John") }, "update \"users\" set \"age\" = \"age\" - 1 where \"name\" = ?"},
		{"insert", func(q db.Query) db.Query { return q }, "insert into \"users\" (\"name\") values (?)"},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			var got string

			switch test.name {
			case "count", "count one column", "count renamed column", "count distinct column", "count several columns":
				got = test.build(s.query.Clone()).ToSql().Count()
			case "value":
				// db.ToSql has no Value method, so the select is assembled the same way Value does.
				got = test.build(s.query.Clone()).Select("name").Limit(1).ToSql().Get()
			case "pluck":
				var names []string
				got = test.build(s.query.Clone()).ToSql().Pluck("name", &names)
			case "sum":
				// db.ToSql has no Sum method either; the aggregate is set as Sum does.
				sumQuery := test.build(s.query.Clone()).(*Query)
				sumQuery.builder.SetAggregate("sum", []any{"age"})
				got = sumQuery.ToSql().Get()
			case "delete":
				got = test.build(s.query.Clone()).ToSql().Delete()
			case "update":
				got = test.build(s.query.Clone()).ToSql().Update(map[string]any{"name": "John"})
			case "increment":
				inc := test.build(s.query.Clone()).(*Query)
				got = inc.ToSql().Update(map[string]any{
					"age": querybuilder.Raw(inc.grammar.Wrap("age") + " + 1"),
				})
			case "decrement":
				dec := test.build(s.query.Clone()).(*Query)
				got = dec.ToSql().Update(map[string]any{
					"age": querybuilder.Raw(dec.grammar.Wrap("age") + " - 1"),
				})
			case "insert":
				got = test.build(s.query.Clone()).ToSql().Insert(map[string]any{"name": "John"})
			default:
				got = test.build(s.query.Clone()).ToSql().Get()
			}

			assert.Equal(s.T(), test.want, got)
		})
	}
}

func (s *QueryTestSuite) TestWhereExistsSQL() {
	sub := newDialectTestQuery(s.ctx, s.mockReadBuilder, s.mockWriteBuilder, s.grammar, querybuilder.DialectMySQL, s.mockLogger, "agents", nil).Where("age", 25)

	got := s.query.Clone().Where("name", "John").WhereExists(func() db.Query { return sub }).ToSql().Get()

	assert.Equal(s.T(), "select * from \"users\" where \"name\" = ? and exists (select * from \"agents\" where \"age\" = ?)", got)
}

func (s *QueryTestSuite) TestNestedWhereSQL() {
	got := s.query.Clone().Where("name", "John").Where(func(q db.Query) db.Query {
		return q.WhereIn("age", []any{25, 30}).Where("name", "Tom")
	}).ToSql().Get()

	assert.Equal(s.T(), "select * from \"users\" where \"name\" = ? and (\"age\" in (?, ?) and \"name\" = ?)", got)
}

// --- execution ---------------------------------------------------------------

func (s *QueryTestSuite) TestGet() {
	var users []TestUser

	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, &users, "select * from \"users\" where \"name\" = ?", "John").Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select * from \"users\" where \"name\" = ?", "John").Return("select * from \"users\" where \"name\" = \"John\"").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select * from \"users\" where \"name\" = \"John\"", int64(0), nil).Return().Once()

	s.NoError(s.query.Clone().Where("name", "John").Get(&users))
}

func (s *QueryTestSuite) TestFirst() {
	var user TestUser

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, &user, "select * from \"users\" where \"name\" = ? limit 1", "John").Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select * from \"users\" where \"name\" = ? limit 1", "John").Return("select * from \"users\" where \"name\" = \"John\" limit 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select * from \"users\" where \"name\" = \"John\" limit 1", int64(1), nil).Return().Once()

	s.NoError(s.query.Clone().Where("name", "John").First(&user))
}

func (s *QueryTestSuite) TestFirstReportsNoRowsAsEmpty() {
	var user TestUser

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, &user, "select * from \"users\" where \"name\" = ? limit 1", "John").Return(databasesql.ErrNoRows).Once()
	s.mockReadBuilder.EXPECT().Explain("select * from \"users\" where \"name\" = ? limit 1", "John").Return("select * from \"users\" where \"name\" = \"John\" limit 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select * from \"users\" where \"name\" = \"John\" limit 1", int64(0), nil).Return().Once()

	s.NoError(s.query.Clone().Where("name", "John").First(&user))
}

func (s *QueryTestSuite) TestFirstOr() {
	var user TestUser

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, &user, "select * from \"users\" where \"name\" = ? limit 1", "John").Return(databasesql.ErrNoRows).Once()
	s.mockReadBuilder.EXPECT().Explain("select * from \"users\" where \"name\" = ? limit 1", "John").Return("select * from \"users\" where \"name\" = \"John\" limit 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select * from \"users\" where \"name\" = \"John\" limit 1", int64(0), nil).Return().Once()

	called := false
	err := s.query.Clone().Where("name", "John").FirstOr(&user, func() error {
		called = true
		return nil
	})

	s.NoError(err)
	s.True(called)
}

func (s *QueryTestSuite) TestFindShorthand() {
	var user TestUser

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, &user, "select * from \"users\" where \"id\" = ? limit 1", 1).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select * from \"users\" where \"id\" = ? limit 1", 1).Return("select * from \"users\" where \"id\" = 1 limit 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select * from \"users\" where \"id\" = 1 limit 1", int64(1), nil).Return().Once()

	s.NoError(s.query.Clone().Find(&user, 1))
}

func (s *QueryTestSuite) TestFindWithSlice() {
	var users []TestUser

	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, &users, "select * from \"users\" where \"id\" in (?, ?)", 1, 2).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select * from \"users\" where \"id\" in (?, ?)", 1, 2).Return("select * from \"users\" where \"id\" in (1, 2)").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select * from \"users\" where \"id\" in (1, 2)", int64(0), nil).Return().Once()

	s.NoError(s.query.Clone().Find(&users, []int{1, 2}))
}

func (s *QueryTestSuite) TestFindRejectsTooManyConditions() {
	var users []TestUser

	s.Equal(errors.DatabaseInvalidArgumentNumber.Args(3, "1 or 2"), s.query.Clone().Find(&users, 1, 2, 3))
}

func (s *QueryTestSuite) TestCountAndExists() {
	var count int64

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, &count, "select count(*) as aggregate from \"users\" where \"name\" = ?", "John").Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*(dest.(*int64)) = 1
	}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select count(*) as aggregate from \"users\" where \"name\" = ?", "John").Return("select count(*) as aggregate from \"users\" where \"name\" = \"John\"").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select count(*) as aggregate from \"users\" where \"name\" = \"John\"", int64(-1), nil).Return().Once()

	got, err := s.query.Clone().Where("name", "John").Count()
	s.NoError(err)
	s.Equal(int64(1), got)

	s.expectExists(false, " where \"name\" = ?", "John")

	exists, err := s.query.Clone().Where("name", "John").Exists()
	s.NoError(err)
	s.False(exists)
}

func (s *QueryTestSuite) TestCountDistinctWithoutColumnsCountsAllRows() {
	s.expectCount(3)
	count, err := s.query.Clone().Distinct().Count()

	s.NoError(err)
	s.Equal(int64(3), count)
}

func (s *QueryTestSuite) TestInsert() {
	result := &MockResult{}
	result.On("RowsAffected").Return(int64(1), nil)

	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "insert into \"users\" (\"name\") values (?)", "John").Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("insert into \"users\" (\"name\") values (?)", "John").Return("insert into \"users\" (\"name\") values (\"John\")").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "insert into \"users\" (\"name\") values (\"John\")", int64(1), nil).Return().Once()

	got, err := s.query.Clone().Insert(map[string]any{"name": "John"})
	s.NoError(err)
	s.Equal(int64(1), got.RowsAffected)
}

func (s *QueryTestSuite) TestInsertEmpty() {
	got, err := s.query.Clone().Insert(nil)

	s.NoError(err)
	s.Equal(int64(0), got.RowsAffected)
}

func (s *QueryTestSuite) TestInsertGetID() {
	result := &MockResult{}
	result.On("LastInsertId").Return(int64(1), nil)

	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "insert into \"users\" (\"age\", \"name\") values (?, ?)", 25, "John").Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("insert into \"users\" (\"age\", \"name\") values (?, ?)", 25, "John").Return("insert into \"users\" (\"age\", \"name\") values (25, \"John\")").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "insert into \"users\" (\"age\", \"name\") values (25, \"John\")", int64(1), nil).Return().Once()

	id, err := s.query.Clone().InsertGetID(map[string]any{"name": "John", "age": 25})
	s.NoError(err)
	s.Equal(int64(1), id)
}

func (s *QueryTestSuite) TestInsertGetIDIgnoresSQLKeywordsInValues() {
	const statement = "insert into \"users\" (\"name\") values (' returning output inserted. ')"
	result := &MockResult{}
	result.On("LastInsertId").Return(int64(17), nil)
	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, statement).Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain(statement).Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(1), nil).Return().Once()

	id, err := s.query.Clone().InsertGetID(map[string]any{"name": querybuilder.Raw("' returning output inserted. '")})
	s.Require().NoError(err)
	s.Equal(int64(17), id)
}

func (s *QueryTestSuite) TestUpdate() {
	result := &MockResult{}
	result.On("RowsAffected").Return(int64(1), nil)

	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "update \"users\" set \"name\" = ? where \"id\" = ?", "John", 1).Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("update \"users\" set \"name\" = ? where \"id\" = ?", "John", 1).Return("update \"users\" set \"name\" = \"John\" where \"id\" = 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "update \"users\" set \"name\" = \"John\" where \"id\" = 1", int64(1), nil).Return().Once()

	got, err := s.query.Clone().Where("id", 1).Update("name", "John")
	s.NoError(err)
	s.Equal(int64(1), got.RowsAffected)
}

func (s *QueryTestSuite) TestDelete() {
	result := &MockResult{}
	result.On("RowsAffected").Return(int64(1), nil)

	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "delete from \"users\" where \"name\" = ?", "John").Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("delete from \"users\" where \"name\" = ?", "John").Return("delete from \"users\" where \"name\" = \"John\"").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "delete from \"users\" where \"name\" = \"John\"", int64(1), nil).Return().Once()

	got, err := s.query.Clone().Where("name", "John").Delete()
	s.NoError(err)
	s.Equal(int64(1), got.RowsAffected)
}

func (s *QueryTestSuite) TestIncrementAndDecrement() {
	result := &MockResult{}
	result.On("RowsAffected").Return(int64(1), nil).Twice()

	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "update \"users\" set \"age\" = \"age\" + 1 where \"name\" = ?", "John").Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("update \"users\" set \"age\" = \"age\" + 1 where \"name\" = ?", "John").Return("update \"users\" set \"age\" = \"age\" + 1 where \"name\" = \"John\"").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "update \"users\" set \"age\" = \"age\" + 1 where \"name\" = \"John\"", int64(1), nil).Return().Once()

	s.NoError(s.query.Clone().Where("name", "John").Increment("age"))

	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "update \"users\" set \"age\" = \"age\" - 1 where \"name\" = ?", "John").Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("update \"users\" set \"age\" = \"age\" - 1 where \"name\" = ?", "John").Return("update \"users\" set \"age\" = \"age\" - 1 where \"name\" = \"John\"").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "update \"users\" set \"age\" = \"age\" - 1 where \"name\" = \"John\"", int64(1), nil).Return().Once()

	s.NoError(s.query.Clone().Where("name", "John").Decrement("age"))
}

func (s *QueryTestSuite) TestUpdateOrInsertUpdatesExistingRow() {
	s.expectExists(true, " where \"id\" = ?", 1)

	result := &MockResult{}
	result.On("RowsAffected").Return(int64(1), nil)

	// The base grammar renders no LIMIT on an update; MySQL's "limit 1" is asserted in tests/database/db.
	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "update \"users\" set \"phone\" = ? where \"id\" = ?", "123", 1).Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("update \"users\" set \"phone\" = ? where \"id\" = ?", "123", 1).Return("update \"users\" set \"phone\" = \"123\" where \"id\" = 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "update \"users\" set \"phone\" = \"123\" where \"id\" = 1", int64(1), nil).Return().Once()

	got, err := s.query.Clone().UpdateOrInsert(map[string]any{"id": 1}, map[string]any{"phone": "123"})
	s.NoError(err)
	s.Equal(int64(1), got.RowsAffected)
}

func (s *QueryTestSuite) TestUpdateOrInsertInsertsMissingRow() {
	s.expectExists(false, " where \"id\" = ?", 1)

	result := &MockResult{}
	result.On("RowsAffected").Return(int64(1), nil)

	s.mockWriteBuilder.EXPECT().ExecContext(s.ctx, "insert into \"users\" (\"id\", \"phone\") values (?, ?)", 1, "123").Return(result, nil).Once()
	s.mockWriteBuilder.EXPECT().Explain("insert into \"users\" (\"id\", \"phone\") values (?, ?)", 1, "123").Return("insert into \"users\" (\"id\", \"phone\") values (1, \"123\")").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "insert into \"users\" (\"id\", \"phone\") values (1, \"123\")", int64(1), nil).Return().Once()

	got, err := s.query.Clone().UpdateOrInsert(map[string]any{"id": 1}, map[string]any{"phone": "123"})
	s.NoError(err)
	s.Equal(int64(1), got.RowsAffected)
}

// TestUpdateOrInsertEmptyValuesSkipsUpdate mirrors Laravel's
// testUpdateOrInsertMethodWorksWithEmptyUpdateValues: when the row exists and no update values are
// supplied, UpdateOrInsert must not run an update at all, yet still report success. The missing
// write-builder expectation is the assertion - any update or insert call would fail the mock.
func (s *QueryTestSuite) TestUpdateOrInsertEmptyValuesSkipsUpdate() {
	s.expectExists(true, " where \"email\" = ?", "foo")

	got, err := s.query.Clone().UpdateOrInsert(map[string]any{"email": "foo"}, nil)
	s.NoError(err)
	s.Equal(int64(0), got.RowsAffected)
}

func (s *QueryTestSuite) TestPaginate() {
	var users []TestUser
	var total int64

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, &total, "select count(*) as aggregate from \"users\" where \"name\" = ?", "John").Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*(dest.(*int64)) = 2
	}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select count(*) as aggregate from \"users\" where \"name\" = ?", "John").Return("select count(*) as aggregate from \"users\" where \"name\" = \"John\"").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select count(*) as aggregate from \"users\" where \"name\" = \"John\"", int64(-1), nil).Return().Once()

	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, &users, "select * from \"users\" where \"name\" = ? limit 10 offset 0", "John").Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select * from \"users\" where \"name\" = ? limit 10 offset 0", "John").Return("select * from \"users\" where \"name\" = \"John\" limit 10 offset 0").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select * from \"users\" where \"name\" = \"John\" limit 10 offset 0", int64(0), nil).Return().Once()

	s.NoError(s.query.Clone().Where("name", "John").Paginate(1, 10, &users, &total))
	s.Equal(int64(2), total)
}

// TestPaginateNilTotalSkipsFetchOnZeroCount pins Laravel's guarantee that paginate() skips
// forPage()/get() entirely when the resolved total is zero -- even when the caller supplied no total
// out-parameter. Laravel decides on the resolved count (Query\Builder::paginate():
// `$results = $total ? $this->forPage($page, $perPage)->get($columns) : new Collection`), not on
// whether a total was passed in, so a nil-total page whose count is zero must never run the page
// query. The old guard `total != nil && *total == 0` can never fire for a nil total, so the page
// query ran anyway; there is no SelectContext expectation below, so any fetch is an unexpected mock
// call and fails the test.
func (s *QueryTestSuite) TestPaginateNilTotalSkipsFetchOnZeroCount() {
	var users []TestUser

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, mock.Anything, "select count(*) as aggregate from \"users\" where \"name\" = ?", "John").Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*(dest.(*int64)) = 0
	}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select count(*) as aggregate from \"users\" where \"name\" = ?", "John").Return("select count(*) as aggregate from \"users\" where \"name\" = \"John\"").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select count(*) as aggregate from \"users\" where \"name\" = \"John\"", int64(-1), nil).Return().Once()

	s.NoError(s.query.Clone().Where("name", "John").Paginate(1, 10, &users, nil))
	s.Empty(users)
}

func (s *QueryTestSuite) TestWhen() {
	applied := false

	got := s.query.Clone().When(true, func(q db.Query) db.Query {
		applied = true
		return q.Where("name", "John")
	})
	s.True(applied)
	s.Equal("select * from \"users\" where \"name\" = ?", got.ToSql().Get())

	got = s.query.Clone().When(false, func(q db.Query) db.Query {
		s.Fail("the true callback must not run")
		return q
	}, func(q db.Query) db.Query {
		return q.Where("name", "Jane")
	})
	s.Equal("select * from \"users\" where \"name\" = ?", got.ToSql().Get())
}

func (s *QueryTestSuite) TestMissingTableIsRejected() {
	query := newDialectTestQuery(s.ctx, s.mockReadBuilder, s.mockWriteBuilder, s.grammar, querybuilder.DialectMySQL, s.mockLogger, "", nil)

	s.Equal(errors.DatabaseTableIsRequired, query.Where("id", 1).Get(&[]TestUser{}))
}

// TestCursorPaginate asserts the facade emits the keyset statement for a first page and projects the
// scanned rows onto the contract's paginator. The stub builder records the SQL but fills nothing, so
// the page carries the probe limit (perPage+1) and no rows.
func (s *QueryTestSuite) TestCursorPaginate() {
	var (
		items   []map[string]any
		gotSQL  string
		gotArgs []any
	)

	s.mockReadBuilder.EXPECT().SelectContext(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ any, sql string, args ...any) {
			gotSQL = sql
			gotArgs = args
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(mock.Anything).Return("select * from \"users\" order by \"id\" asc limit 16").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, mock.Anything, int64(0), nil).Return().Once()

	page, err := s.query.Clone().OrderBy("id").CursorPaginate(15, "", &items)
	s.Require().NoError(err)

	s.Equal("select * from \"users\" order by \"id\" asc limit 16", gotSQL)
	s.Empty(gotArgs)
	s.Equal(15, page.PerPage)
	s.False(page.HasMorePages)
	s.Nil(page.NextCursor)
	s.Nil(page.PreviousCursor)
	s.Empty(page.Items)
}

// TestCursorPaginateWithCursor asserts the keyset predicate the encoded cursor produces: the one
// comparison for a single ordered column, bound to the cursor's value, with the perPage+1 probe.
func (s *QueryTestSuite) TestCursorPaginateWithCursor() {
	var (
		items  []map[string]any
		gotSQL string
	)

	encoded, err := querybuilder.NewCursor(querybuilder.CursorParameters{{Column: "id", Value: 5}}, true).Encode()
	s.Require().NoError(err)

	s.mockReadBuilder.EXPECT().SelectContext(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ any, sql string, _ ...any) { gotSQL = sql }).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(mock.Anything, mock.Anything).Return("").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, mock.Anything, int64(0), nil).Return().Once()

	page, err := s.query.Clone().OrderBy("id").CursorPaginate(15, encoded, &items)
	s.Require().NoError(err)

	s.Equal("select * from \"users\" where (\"id\" > ?) order by \"id\" asc limit 16", gotSQL)
	s.Equal(15, page.PerPage)
	s.False(page.HasMorePages)
}

// TestCursorPaginateRejectsAMalformedCursor pins that a non-empty cursor which cannot be decoded is
// refused rather than silently restarting at the first page.
func (s *QueryTestSuite) TestCursorPaginateRejectsAMalformedCursor() {
	var items []map[string]any

	_, err := s.query.Clone().OrderBy("id").CursorPaginate(15, "not-a-cursor", &items)
	s.ErrorIs(err, errors.QueryCursorInvalid)
}

// TestCursorPaginateRequiresAnOrder pins the keyset contract: with no ORDER BY there is no column to
// seek on, and errors.QueryOrderRequired is returned rather than an implicit ordering being invented.
func (s *QueryTestSuite) TestCursorPaginateRequiresAnOrder() {
	var items []map[string]any

	_, err := s.query.Clone().CursorPaginate(15, "", &items)
	s.ErrorIs(err, errors.QueryOrderRequired)
}

// TestImplode mirrors Laravel's DatabaseQueryBuilderTest::testImplode: implode concatenates the
// values of a single plucked column, with no glue by default and the given glue otherwise. The
// corpus asserts only the concatenated result; the SQL is pinned here through the mocked builder
// (MySQL grammar, so backtick quoting and our ? placeholder).
func (s *QueryTestSuite) TestImplode() {
	// Test without glue.
	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, mock.Anything, "select \"foo\" from \"users\" where \"id\" = ?", 1).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*[]map[string]any)) = []map[string]any{{"foo": "bar"}, {"foo": "baz"}}
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select \"foo\" from \"users\" where \"id\" = ?", 1).Return("select \"foo\" from \"users\" where \"id\" = 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select \"foo\" from \"users\" where \"id\" = 1", int64(2), nil).Return().Once()

	var withoutGlue string
	s.NoError(s.query.Clone().Where("id", 1).Implode("foo", "", &withoutGlue))
	s.Equal("barbaz", withoutGlue)

	// Test with glue.
	s.mockReadBuilder.EXPECT().SelectContext(s.ctx, mock.Anything, "select \"foo\" from \"users\" where \"id\" = ?", 1).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*[]map[string]any)) = []map[string]any{{"foo": "bar"}, {"foo": "baz"}}
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain("select \"foo\" from \"users\" where \"id\" = ?", 1).Return("select \"foo\" from \"users\" where \"id\" = 1").Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, "select \"foo\" from \"users\" where \"id\" = 1", int64(2), nil).Return().Once()

	var withGlue string
	s.NoError(s.query.Clone().Where("id", 1).Implode("foo", ",", &withGlue))
	s.Equal("bar,baz", withGlue)
}

// TestRawValue mirrors Laravel's DatabaseQueryBuilderTest::testRawValueMethodReturnsSingleColumn
// (corpus 3640): a raw expression is selected and the single value of the first row is returned, not
// a wrapped identifier. Laravel's corpus builder is the base grammar, so its expected SQL quotes the
// table with double quotes ("users"); this file's suite pins the MySQL grammar, so the table is
// backtick-quoted while the raw expression's own text (UPPER("foo")) is passed through verbatim.
func (s *QueryTestSuite) TestRawValue() {
	const (
		wantSQL = "select UPPER(\"foo\") from \"users\" where \"id\" = ? limit 1"
		explain = "select UPPER(\"foo\") from \"users\" where \"id\" = 1"
	)

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, mock.Anything, wantSQL, 1).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*string)) = "BAR"
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(wantSQL, 1).Return(explain).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, explain, int64(1), nil).Return().Once()

	var value string
	s.NoError(s.query.Clone().Where("id", 1).RawValue("UPPER(\"foo\")", &value))
	s.Equal("BAR", value)
}

// TestExistsOr and TestDoesntExistOr mirror Laravel's DatabaseQueryBuilderTest::testDoesntExistsOr
// and ::testExistsOr (corpus 3734 / 3718). The corpus method names and bodies are swapped: the body
// named testExistsOr exercises doesntExistOr and vice versa, so the two Go methods below are named for
// the behaviour they actually assert. existsOr returns true when the query matches a row and otherwise
// returns the callback's value; doesntExistOr mirrors it (true when nothing matches, else the
// callback's value). The callback never runs on the true branch. The SQL exercised is the
// grammar-compiled EXISTS query, pinned here through the mocked builder (MySQL grammar).
func (s *QueryTestSuite) TestExistsOr() {
	// No rows match, so the callback's value is returned.
	s.expectExists(false, "")
	value, err := s.query.Clone().ExistsOr(func() any { return 123 })
	s.NoError(err)
	s.Equal(123, value)

	// A row matches, so true is returned and the callback is not run.
	s.expectExists(true, "")
	ran := false
	value, err = s.query.Clone().ExistsOr(func() any {
		ran = true

		return 123
	})
	s.NoError(err)
	s.Equal(true, value)
	s.False(ran)
}

func (s *QueryTestSuite) TestDoesntExistOr() {
	// A row matches, so the callback's value is returned.
	s.expectExists(true, "")
	value, err := s.query.Clone().DoesntExistOr(func() any { return 123 })
	s.NoError(err)
	s.Equal(123, value)

	// No rows match, so true is returned and the callback is not run.
	s.expectExists(false, "")
	ran := false
	value, err = s.query.Clone().DoesntExistOr(func() any {
		ran = true

		return 123
	})
	s.NoError(err)
	s.Equal(true, value)
	s.False(ran)
}

// expectCount scripts the count aggregate statement for the "users"
// table with no additional clauses.
func (s *QueryTestSuite) expectCount(count int64) {
	const sql = "select count(*) as aggregate from \"users\""

	s.mockReadBuilder.EXPECT().GetContext(s.ctx, mock.Anything, sql).
		Run(func(_ context.Context, dest any, _ string, _ ...any) {
			*(dest.(*int64)) = count
		}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(sql).Return(sql).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, sql, int64(-1), nil).Return().Once()
}

// chunkStubConnector serves canned result sets without a database server so the Get-driven Chunk
// loop can be exercised end to end through the real Builder. Every statement the query issues is
// answered with the next page; once the pages run out the connection reports an empty result, which
// is exactly the trailing fetch the loop must treat as its terminator. The shared counters record how
// many statements actually reached the connection and the SQL of each, so a test can pin both the
// number of fetches and the exact offset/limit they carry.
type chunkStubConnector struct {
	columns []string
	pages   [][][]driver.Value
	served  *int
	queries *[]string
}

func (c chunkStubConnector) Connect(context.Context) (driver.Conn, error) {
	return chunkStubConn(c), nil
}

func (c chunkStubConnector) Driver() driver.Driver { return chunkStubDriver{} }

type chunkStubDriver struct{}

func (chunkStubDriver) Open(string) (driver.Conn, error) { return nil, driver.ErrBadConn }

type chunkStubConn struct {
	columns []string
	pages   [][][]driver.Value
	served  *int
	queries *[]string
}

func (c chunkStubConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrBadConn }
func (c chunkStubConn) Close() error                        { return nil }
func (c chunkStubConn) Begin() (driver.Tx, error)           { return nil, driver.ErrBadConn }

func (c chunkStubConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	*c.queries = append(*c.queries, query)

	index := *c.served
	*c.served = index + 1

	if index >= len(c.pages) {
		return &chunkStubRows{columns: c.columns}, nil
	}

	return &chunkStubRows{columns: c.columns, data: c.pages[index]}, nil
}

type chunkStubRows struct {
	columns []string
	data    [][]driver.Value
	index   int
}

func (r *chunkStubRows) Columns() []string { return r.columns }
func (r *chunkStubRows) Close() error      { return nil }

func (r *chunkStubRows) Next(dest []driver.Value) error {
	if r.index >= len(r.data) {
		return io.EOF
	}

	copy(dest, r.data[r.index])
	r.index++

	return nil
}

// newChunkQuery builds a Query over a stub pool that answers successive fetches with the given pages,
// then empty. It returns the query plus pointers to the number of statements issued and the SQL each
// carried, so a test can assert how many fetches a chunk made and against which offset/limit.
func (s *QueryTestSuite) newChunkQuery(pages [][][]driver.Value) (db.Query, *int, *[]string) {
	served := new(int)
	queries := new([]string)

	pool := databasesql.OpenDB(chunkStubConnector{
		columns: []string{"id"},
		pages:   pages,
		served:  served,
		queries: queries,
	})
	s.T().Cleanup(func() { _ = pool.Close() })

	builder, err := NewBuilder(pool, "mysql")
	s.Require().NoError(err)

	s.mockLogger.EXPECT().Trace(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return().Maybe()

	return newDialectTestQuery(s.ctx, builder, builder, s.grammar, querybuilder.DialectMySQL, s.mockLogger, "users", nil).OrderBy("id").(*Query), served, queries
}

// TestChunkStopsOnTheEmptyChunk pins the empty-chunk terminator over an exact-multiple dataset:
// chunk(2) over four rows issues three fetches at offsets 0, 2 and 4, and passes only the two full
// pages to the callback, stopping when the trailing fetch comes back empty
// (Laravel's testChunkWithLastChunkComplete).
func (s *QueryTestSuite) TestChunkStopsOnTheEmptyChunk() {
	query, _, queries := s.newChunkQuery([][][]driver.Value{
		{{int64(1)}, {int64(2)}},
		{{int64(3)}, {int64(4)}},
	})

	var sizes []int
	s.NoError(query.Chunk(2, func(rows []db.Row) error {
		sizes = append(sizes, len(rows))

		return nil
	}))

	s.Equal([]int{2, 2}, sizes)
	s.Equal([]string{
		"select * from \"users\" order by \"id\" asc limit 2 offset 0",
		"select * from \"users\" order by \"id\" asc limit 2 offset 2",
		"select * from \"users\" order by \"id\" asc limit 2 offset 4",
	}, *queries)
}

// TestChunkWithZeroSizeDoesNoWork pins Laravel's testChunkWithCountZero: chunk(0) short-circuits
// before any offset/limit/get, so no statement reaches the connection and the callback never fires.
func (s *QueryTestSuite) TestChunkWithZeroSizeDoesNoWork() {
	query, served, _ := s.newChunkQuery([][][]driver.Value{{{int64(1)}}})

	called := false
	s.NoError(query.Chunk(0, func(rows []db.Row) error {
		called = true

		return nil
	}))

	s.False(called)
	s.Equal(0, *served)
}

// TestChunkHonoursCallbackError pins the error path of Laravel's testChunkCanBeStoppedByReturningFalse:
// a callback error that is not the stop sentinel aborts the loop and is returned verbatim, before the
// next fetch is issued.
func (s *QueryTestSuite) TestChunkHonoursCallbackError() {
	query, served, _ := s.newChunkQuery([][][]driver.Value{
		{{int64(1)}, {int64(2)}},
		{{int64(3)}},
	})

	calls := 0
	err := query.Chunk(2, func(rows []db.Row) error {
		calls++

		return errors.DatabaseDataIsEmpty
	})

	s.Equal(errors.DatabaseDataIsEmpty, err)
	s.Equal(1, calls)
	// One fetch only: the error stops the loop before a second statement is built.
	s.Equal(1, *served)
}

// TestChunkStopsCleanlyOnChunkStopped pins the clean-stop path: returning
// errors.DatabaseChunkStopped (Go's analogue of Laravel's `return false`) halts the loop and makes
// Chunk return nil, with no second fetch.
func (s *QueryTestSuite) TestChunkStopsCleanlyOnChunkStopped() {
	query, served, _ := s.newChunkQuery([][][]driver.Value{{{int64(1)}, {int64(2)}}})

	calls := 0
	err := query.Chunk(2, func(rows []db.Row) error {
		calls++

		return errors.DatabaseChunkStopped
	})

	s.NoError(err)
	s.Equal(1, calls)
	s.Equal(1, *served)
}

func (s *QueryTestSuite) expectExists(exists bool, where string, values ...any) {
	statement := "select exists(select * from \"users\"" + where + ") as \"exists\""
	s.mockReadBuilder.EXPECT().GetContext(s.ctx, mock.Anything, statement, values...).Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*dest.(*bool) = exists
	}).Return(nil).Once()
	s.mockReadBuilder.EXPECT().Explain(statement, values...).Return(statement).Once()
	s.mockLogger.EXPECT().Trace(s.ctx, s.now, statement, int64(-1), nil).Return().Once()
}
