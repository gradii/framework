package tests

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/database/db"
	"github.com/goravel/framework/errors"
	"github.com/goravel/mysql"
	"github.com/goravel/postgres"
	"github.com/goravel/sqlserver"
	"github.com/spf13/cast"
)

func (s *DBTestSuite) TestLaravelSingleConnectionTransactionCompilesVersionedQueries() {
	query := NewTestQueryBuilder().Mysql("", false)
	connection := query.DB()
	pool, err := query.Query().DB()
	s.Require().NoError(err)
	pool.SetMaxOpenConns(1)
	defer pool.SetMaxOpenConns(100)
	s.Require().NoError(connection.Statement("create table versioned_transaction (id integer primary key, team_id integer)"))
	defer func() { s.NoError(connection.Statement("drop table versioned_transaction")) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = connection.WithContext(ctx).Transaction(func(tx db.Tx) error {
		var rows []map[string]any
		return tx.Table("versioned_transaction").GroupLimit(1, "team_id").Get(&rows)
	})
	s.Require().NoError(err)
	s.Zero(pool.Stats().InUse)
}

func (s *DBTestSuite) TestLaravelAggregatesPreserveNullAndReturnZeroSum() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			table := query.DB().Table("products")
			sum := int64(9)
			s.Require().NoError(table.Sum("weight", &sum))
			s.Zero(sum)
			var average sql.NullFloat64
			s.Require().NoError(table.Avg("weight", &average))
			s.False(average.Valid)
			_, err := table.Insert([]map[string]any{{"name": "one", "weight": 1}, {"name": "two", "weight": 2}})
			s.Require().NoError(err)
			s.Require().NoError(table.Sum("weight", &sum))
			s.Equal(int64(3), sum)
			s.Require().NoError(table.Avg("weight", &average))
			s.True(average.Valid)
			// SQL Server averages integers using integer division, as Laravel's SQL does.
			if driver == sqlserver.Name {
				s.Equal(float64(1), average.Float64)
			} else {
				s.Equal(1.5, average.Float64)
			}
			s.True(table.GetLimit() == nil)
			s.Empty(table.GetColumns())
		})
	}
}

func (s *DBTestSuite) TestLaravelPublicParametersAndWritePreview() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			connection := query.DB()
			table := connection.Table("products")
			id, err := table.InsertGetID(map[string]any{"name": "Alpha"}, "id")
			s.Require().NoError(err)
			_, err = table.Insert(map[string]any{"name": "alpha"})
			s.Require().NoError(err)
			var rows []map[string]any
			err = table.Clone().WhereIn("id", func(child db.Query) {
				child.From("products").Select("id").Where("id", id)
			}).WhereExists(func(child db.Query) {
				child.From("products as matched").WhereColumn("matched.id", "products.id")
			}).Get(&rows)
			s.Require().NoError(err)
			s.Len(rows, 1)
			s.Equal("Alpha", cast.ToString(rows[0]["name"]))
			err = table.Clone().WhereLike("name", "Alpha", true).Get(&rows)
			if driver == sqlserver.Name {
				s.Error(err)
			} else {
				s.Require().NoError(err)
				s.Len(rows, 1)
			}
			s.Require().NoError(table.Clone().WhereLike("name", "Alpha").InRandomOrder(7).Get(&rows))
			s.Len(rows, 2)

			write := table.Clone().Where("id", id)
			callbacks := 0
			write.BeforeQuery(func(q db.Query) db.Query { callbacks++; return q })
			preview := write.ToSql().Update("name", "updated")
			s.Require().NoError(table.Clone().Where("id", id).Get(&rows))
			s.Len(rows, 1)
			s.Equal("Alpha", cast.ToString(rows[0]["name"]))
			connection.FlushQueryLog()
			connection.EnableQueryLog()
			_, err = write.Update("name", "updated")
			connection.DisableQueryLog()
			s.Require().NoError(err)
			events := connection.GetQueryLog()
			s.Require().Len(events, 1)
			s.Equal(preview, events[0].SQL)
			s.Equal([]any{"updated", id}, events[0].Bindings)
			s.Equal(1, callbacks)

			if driver == mysql.Name && os.Getenv("GORAVEL_TEST_MARIADB") == "1" {
				version, err := connection.ServerVersion()
				s.Require().NoError(err)
				s.Contains(strings.ToLower(version), "mariadb")
				err = table.Clone().JoinLateral("select 1 as n", "lateral_query").Get(&rows)
				s.ErrorIs(err, errors.QueryLateralJoinNotSupported)
			}
		})
	}
}

func (s *DBTestSuite) TestLaravelInsertGetIDCustomPrimaryKey() {
	query := s.queries[postgres.Name]
	if query == nil {
		s.T().Skip("PostgreSQL is unavailable")
	}
	connection := query.DB()
	s.Require().NoError(connection.Statement("create table port_custom_id (record_id bigserial primary key, name varchar(255))"))
	defer func() { s.NoError(connection.Statement("drop table port_custom_id")) }()
	id, err := connection.Table("port_custom_id").InsertGetID(map[string]any{"name": "custom"}, "record_id")
	s.Require().NoError(err)
	s.Positive(id)
	var row map[string]any
	s.Require().NoError(connection.Table("port_custom_id").Where("record_id", id).First(&row))
	s.Equal("custom", row["name"])
}
