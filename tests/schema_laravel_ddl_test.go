package tests

import (
	"context"
	"strings"

	"github.com/goravel/mysql"
	"github.com/goravel/sqlite"
	"github.com/goravel/sqlserver"
	contractsschema "github.com/gradii/framework/contracts/database/schema"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
	"github.com/spf13/cast"
)

func (s *SchemaSuite) TestLaravelMariaDBSpatialAndVector() {
	if !isMariaDBTest(mysql.Name) {
		s.T().Skip("requires the MariaDB test instance")
	}
	query := s.driverToTestQuery[mysql.Name]
	schema := newSchema(query, s.driverToTestQuery)
	s.Require().NoError(schema.Create("maria_features", func(table contractsschema.Blueprint) {
		table.ID()
		table.Geometry("position", "point", 4326)
		table.Vector("embedding", 3)
		table.SpatialIndex("position")
	}))
	defer func() { s.NoError(schema.DropIfExists("maria_features")) }()
	_, err := query.DB().Table("maria_features").Insert(map[string]any{
		"position":  querybuilder.Raw("ST_GeomFromText('POINT(1 2)', 4326)"),
		"embedding": querybuilder.Raw("VEC_FromText('[1,2,3]')"),
	})
	s.Require().NoError(err)
	columns, err := schema.GetColumnsMetadata("maria_features")
	s.Require().NoError(err)
	s.Require().Len(columns, 3)
	s.Equal("point", columns[1].TypeName)
	s.Equal("vector(3)", columns[2].Type)
	s.True(schema.HasIndex("maria_features", []string{"position"}))
	count, err := query.DB().Table("maria_features").Count()
	s.Require().NoError(err)
	s.Equal(int64(1), count)
}

func (s *SchemaSuite) TestSQLiteRebuildUnicodeAndMissingColumns() {
	query := s.driverToTestQuery[sqlite.Name]
	if query == nil {
		s.T().Skip("SQLite is unavailable")
	}
	schema := newSchema(query, s.driverToTestQuery)
	connection := query.DB()
	s.Require().NoError(connection.Statement("create table " + s.prefix + "unicode_columns (id integer primary key, 价格 integer not null, 名称 text unique, stale integer)"))
	defer func() { s.NoError(schema.DropIfExists("unicode_columns")) }()
	_, err := connection.Table("unicode_columns").Insert(map[string]any{"id": 1, "价格": 7, "名称": "保留"})
	s.Require().NoError(err)
	s.Require().NoError(schema.Table("unicode_columns", func(table contractsschema.Blueprint) {
		table.RenameColumn("价格", "金额")
		table.BigInteger("价格").Change()
		table.DropColumn("stale")
	}))
	var row map[string]any
	s.Require().NoError(connection.Table("unicode_columns").First(&row))
	s.Equal(7, cast.ToInt(row["金额"]))
	s.Equal("保留", row["名称"])
	for _, modify := range []func(contractsschema.Blueprint){
		func(table contractsschema.Blueprint) { table.Integer("missing").Change() },
		func(table contractsschema.Blueprint) { table.DropColumn("missing") },
	} {
		s.ErrorContains(schema.Table("unicode_columns", modify), errors.SchemaColumnNotFound.Args("missing", "unicode_columns").Error())
	}
	s.ErrorContains(schema.Table("unicode_columns", func(table contractsschema.Blueprint) {
		table.DropForeignByName("unknown")
	}), errors.SchemaDropForeignByNameNotSupported.Error())
	s.True(schema.HasColumn("unicode_columns", "金额"))
	_, err = connection.Table("unicode_columns").Insert(map[string]any{"id": 2, "金额": 1, "名称": "保留"})
	s.Error(err)
}

func (s *SchemaSuite) TestSQLiteConstraintScopeRestoresAfterCancellation() {
	query := s.driverToTestQuery[sqlite.Name]
	if query == nil {
		s.T().Skip("SQLite is unavailable")
	}
	connection := query.DB()
	pool, err := query.Query().DB()
	s.Require().NoError(err)
	pool.SetMaxOpenConns(1)
	defer pool.SetMaxOpenConns(100)
	s.Require().NoError(connection.Statement("pragma foreign_keys = on"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	schema := newSchema(query, s.driverToTestQuery, connection.WithContext(ctx))
	err = schema.WithoutForeignKeyConstraints(func(scoped contractsschema.Schema) error {
		if err := scoped.WithWriteConnection(func(pinned contractsschema.Schema) error {
			s.Same(scoped.DB(), pinned.DB())
			return pinned.WithoutForeignKeyConstraints(func(nested contractsschema.Schema) error {
				value, err := nested.DB().Scalar("pragma foreign_keys")
				s.Equal(0, cast.ToInt(value))
				return err
			})
		}); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	s.ErrorIs(err, context.Canceled)
	value, err := connection.Scalar("pragma foreign_keys")
	s.Require().NoError(err)
	s.Equal(1, cast.ToInt(value))
	s.Zero(pool.Stats().InUse)
}

func (s *SchemaSuite) TestLaravelAdvancedDDL() {
	for name, query := range s.driverToTestQuery {
		s.Run(name, func() {
			schema := newSchema(query, s.driverToTestQuery)
			table := "advanced_ddl"
			err := schema.Create(table, func(blueprint contractsschema.Blueprint) {
				blueprint.ID()
				blueprint.Integer("price")
				if name == sqlserver.Name {
					blueprint.Computed("doubled", querybuilder.Raw("price * 2")).Persisted()
				} else {
					blueprint.Integer("doubled").StoredAs(querybuilder.Raw("price * 2"))
				}
				blueprint.Binary("payload", 16).Nullable()
				blueprint.String("note").Default("It's ordinary")
			})
			s.Require().NoError(err)
			defer func() { s.NoError(schema.DropIfExists(table)) }()
			_, err = query.DB().Table(table).Insert(map[string]any{"price": 3})
			s.Require().NoError(err)
			var rows []map[string]any
			s.Require().NoError(query.DB().Table(table).Get(&rows))
			s.Require().Len(rows, 1)
			s.Equal(6, cast.ToInt(rows[0]["doubled"]))
			s.Equal("It's ordinary", cast.ToString(rows[0]["note"]))
			columns, err := schema.GetColumnsMetadata(table)
			s.Require().NoError(err)
			found := false
			for _, column := range columns {
				if column.Name == "doubled" {
					found = true
					s.Require().NotNil(column.Generation)
					s.Equal("stored", column.Generation.Type)
				}
			}
			s.True(found)
			called := false
			s.Require().NoError(schema.WhenTableDoesntHaveColumn(table, "extra", func(blueprint contractsschema.Blueprint) {
				called = true
				blueprint.String("extra").Nullable().Default("added")
			}))
			s.True(called)
			s.True(schema.HasColumn(table, "extra"))
		})
	}
}

func (s *SchemaSuite) TestSQLiteRebuildPreservesDataConstraintsAndConnectionState() {
	query := s.driverToTestQuery[sqlite.Name]
	if query == nil {
		s.T().Skip("SQLite is unavailable")
	}
	schema := newSchema(query, s.driverToTestQuery)
	connection := query.DB()
	pool, err := query.Query().DB()
	s.Require().NoError(err)
	pool.SetMaxOpenConns(1)
	defer pool.SetMaxOpenConns(100)
	parent, child, audit := s.prefix+"rebuild_parent", s.prefix+"rebuild_child", s.prefix+"rebuild_audit"
	s.Require().NoError(connection.Statement("pragma foreign_keys = on"))
	s.Require().NoError(connection.Statement("create table " + parent + " (id integer primary key, name text not null unique, price integer not null check (price >= 0), payload text default 'primary key, keep text')"))
	defer func() { s.NoError(connection.Statement("drop table if exists " + parent)) }()
	s.Require().NoError(connection.Statement("create table " + child + " (id integer primary key, parent_id integer references " + parent + " (id) on delete cascade)"))
	defer func() { s.NoError(connection.Statement("drop table if exists " + child)) }()
	s.Require().NoError(connection.Statement("create table " + audit + " (value integer, literal text)"))
	defer func() { s.NoError(connection.Statement("drop table if exists " + audit)) }()
	s.Require().NoError(connection.Statement("create index rebuild_price_index on " + parent + " (price)"))
	s.Require().NoError(connection.Statement("create trigger rebuild_price_trigger after update of price on " + parent + " begin insert into " + audit + " (value, literal) values (new.price, 'price'); end"))
	_, err = connection.Table("rebuild_parent").Insert(map[string]any{"id": 1, "name": "one", "price": 5})
	s.Require().NoError(err)
	_, err = connection.Table("rebuild_child").Insert(map[string]any{"id": 1, "parent_id": 1})
	s.Require().NoError(err)

	s.Require().NoError(schema.Table("rebuild_parent", func(blueprint contractsschema.Blueprint) {
		blueprint.BigInteger("price").Change()
		blueprint.String("extra").Default("added")
	}))
	var parentRow map[string]any
	s.Require().NoError(connection.Table("rebuild_parent").Where("id", 1).First(&parentRow))
	s.Equal("added", cast.ToString(parentRow["extra"]))
	s.Equal("primary key, keep text", cast.ToString(parentRow["payload"]))
	count, err := connection.Table("rebuild_child").Count()
	s.Require().NoError(err)
	s.Equal(int64(1), count)
	state, err := connection.Scalar("pragma foreign_keys")
	s.Require().NoError(err)
	s.Equal(1, cast.ToInt(state))
	_, err = connection.Table("rebuild_parent").Insert(map[string]any{"id": 2, "name": "one", "price": 1})
	s.Error(err)
	_, err = connection.Table("rebuild_parent").Where("id", 1).Update("price", -1)
	s.Error(err)

	s.Require().NoError(schema.Table("rebuild_parent", func(blueprint contractsschema.Blueprint) {
		blueprint.RenameColumn("price", "amount")
		blueprint.BigInteger("price").Change()
		blueprint.DropColumn("payload")
	}))
	s.True(schema.HasColumn("rebuild_parent", "amount"))
	s.False(schema.HasColumn("rebuild_parent", "payload"))
	_, err = connection.Table("rebuild_parent").Where("id", 1).Update("amount", 7)
	s.Require().NoError(err)
	var auditRows []map[string]any
	s.Require().NoError(connection.Table("rebuild_audit").Get(&auditRows))
	s.Require().Len(auditRows, 1)
	s.Equal(7, cast.ToInt(auditRows[0]["value"]))
	s.Equal("price", cast.ToString(auditRows[0]["literal"]))
	indexes, err := schema.GetIndexes("rebuild_parent")
	s.Require().NoError(err)
	found := false
	for _, index := range indexes {
		if index.Name == "rebuild_price_index" {
			found = true
			s.Equal([]string{"amount"}, index.Columns)
		}
	}
	s.True(found)

	err = schema.Table("rebuild_parent", func(blueprint contractsschema.Blueprint) {
		blueprint.BigInteger("amount").Change()
		blueprint.Integer("required_without_default")
	})
	s.Error(err)
	s.False(schema.HasColumn("rebuild_parent", "required_without_default"))
	state, err = connection.Scalar("pragma foreign_keys")
	s.Require().NoError(err)
	s.Equal(1, cast.ToInt(state))
	s.Require().NoError(connection.Table("rebuild_parent").Where("id", 1).First(&parentRow))
	s.Equal(7, cast.ToInt(parentRow["amount"]))
	s.Require().NoError(schema.Table("rebuild_child", func(blueprint contractsschema.Blueprint) { blueprint.DropForeign("parent_id") }))
	keys, err := schema.GetForeignKeys("rebuild_child")
	s.Require().NoError(err)
	s.Empty(keys)
}

func (s *SchemaSuite) TestLaravelScopedTemporaryTable() {
	for name, query := range s.driverToTestQuery {
		s.Run(name, func() {
			schema := newSchema(query, s.driverToTestQuery)
			s.Require().NoError(schema.WithWriteConnection(func(scoped contractsschema.Schema) error {
				if err := scoped.Create("temporary_scope", func(blueprint contractsschema.Blueprint) { blueprint.Temporary(); blueprint.Integer("value") }); err != nil {
					return err
				}
				table := s.prefix + "temporary_scope"
				if name == sqlserver.Name {
					table = "#" + table
				}
				// FromRaw selects the already-prefixed temporary table on this pinned connection.
				query := scoped.DB().Query().FromRaw(table)
				if _, err := query.Insert(map[string]any{"value": 1}); err != nil {
					return err
				}
				var rows []map[string]any
				if err := query.Get(&rows); err != nil {
					return err
				}
				s.Require().Len(rows, 1)
				s.Equal(1, cast.ToInt(rows[0]["value"]))
				return scoped.Sql("drop table " + strings.ReplaceAll(table, "'", "''"))
			}))
		})
	}
}
