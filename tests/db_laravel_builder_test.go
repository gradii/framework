package tests

import (
	"github.com/goravel/framework/contracts/database/db"
	"github.com/goravel/framework/errors"
	"github.com/spf13/cast"
)

func (s *DBTestSuite) TestLaravelPublicBuilderWritesAndPagination() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			connection := query.DB()
			s.Require().NoError(connection.Statement("drop table if exists port_builder_records"))
			s.Require().NoError(connection.Statement("create table port_builder_records (id bigint primary key, name varchar(255), votes integer)"))
			defer func() { s.NoError(connection.Statement("drop table if exists port_builder_records")) }()
			table := connection.Table("port_builder_records")
			_, err := table.Clone().Insert([]map[string]any{
				{"id": 1, "name": "first", "votes": 1},
				{"id": 2, "name": "second", "votes": 2},
				{"id": 3, "name": "second", "votes": 3},
			})
			s.Require().NoError(err)
			_, err = table.Clone().Upsert([]map[string]any{
				{"id": 1, "name": "updated", "votes": 5},
				{"id": 4, "name": "fourth", "votes": 4},
			}, []string{"id"}, []string{"name", "votes"})
			s.Require().NoError(err)
			_, err = table.Clone().Where("id", 1).IncrementEach(map[string]any{"votes": 2}, map[string]any{"name": "final"})
			s.Require().NoError(err)
			_, err = table.Clone().Where("id", 1).DecrementEach(map[string]any{"votes": 1})
			s.Require().NoError(err)
			var sole map[string]any
			s.Require().NoError(table.Clone().Where("id", 1).Sole(&sole))
			s.Equal(6, cast.ToInt(sole["votes"]))
			s.Equal("final", cast.ToString(sole["name"]))
			s.ErrorIs(table.Clone().Where("id", -1).Sole(&sole), errors.QueryRecordsNotFound)
			s.Error(table.Clone().Sole(&sole))

			var rows []map[string]any
			more, err := table.Clone().OrderBy("id").SimplePaginate(1, 2, &rows)
			s.Require().NoError(err)
			s.True(more)
			s.Len(rows, 2)
			s.Equal(1, cast.ToInt(rows[0]["id"]))
			more, err = table.Clone().OrderBy("id").SimplePaginate(2, 2, &rows)
			s.Require().NoError(err)
			s.False(more)
			s.Len(rows, 2)
			s.Equal(3, cast.ToInt(rows[0]["id"]))

			var total int64
			s.Require().NoError(table.Clone().Select("name").GroupBy("name").OrderBy("name").Paginate(1, 2, &rows, &total))
			s.Equal(int64(3), total)
			s.Len(rows, 2)

			var ids []int
			for row, err := range table.Clone().OrderBy("id").Lazy(2) {
				s.Require().NoError(err)
				var value map[string]any
				s.Require().NoError(row.Scan(&value))
				ids = append(ids, cast.ToInt(value["id"]))
			}
			s.Equal([]int{1, 2, 3, 4}, ids)

			sub := table.NewQuery().From("port_builder_records").Select("id").Where("votes", ">=", 3)
			s.Require().NoError(table.Clone().Select("port_builder_records.id").JoinSub(sub, "matched", "matched.id", "=", "port_builder_records.id").Get(&rows))
			s.Len(rows, 3)

			s.Require().NoError(table.Clone().Truncate())
			count, err := table.Clone().Count()
			s.Require().NoError(err)
			s.Zero(count)
		})
	}
}

func (s *DBTestSuite) TestLaravelInsertUsingAndCallbacks() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			connection := query.DB()
			s.Require().NoError(connection.Statement("drop table if exists port_builder_copy"))
			s.Require().NoError(connection.Statement("create table port_builder_copy (id bigint primary key, name varchar(255))"))
			defer func() { s.NoError(connection.Statement("drop table if exists port_builder_copy")) }()
			_, err := connection.Table("products").Insert([]map[string]any{{"name": "a"}, {"name": "b"}})
			s.Require().NoError(err)
			table := connection.Table("port_builder_copy")
			result, err := table.Clone().InsertUsing([]string{"id", "name"}, func(q db.Query) db.Query {
				return q.From("products").Select("id", "name")
			})
			s.Require().NoError(err)
			s.Equal(int64(2), result.RowsAffected)
			before, after := 0, 0
			var rows []map[string]any
			err = table.Clone().BeforeQuery(func(q db.Query) db.Query {
				before++
				return q.Where("name", "a")
			}).AfterQuery(func(rows any) any { after++; return rows }).Get(&rows)
			s.Require().NoError(err)
			s.Equal(1, before)
			s.Equal(1, after)
			s.Len(rows, 1)
		})
	}
}
