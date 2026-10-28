package tests

import (
	"github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/sqlserver"
	"strings"
)

func (s *SchemaSuite) TestLaravelGeneratedColumnMetadata() {
	for name, query := range s.driverToTestQuery {
		s.Run(name, func() {
			schema := newSchema(query, s.driverToTestQuery)
			table := s.prefix + "laravel_metadata"
			generated := "doubled integer generated always as (price * 2) stored"
			if name == sqlserver.Name {
				generated = "doubled as (price * 2) persisted"
			}
			s.Require().NoError(query.DB().Statement("create table " + table + " (id integer primary key, price integer not null, " + generated + ", note varchar(255) null)"))
			defer func() { s.NoError(query.DB().Statement("drop table " + table)) }()
			columns, err := schema.GetColumnsMetadata("laravel_metadata")
			s.Require().NoError(err)
			s.Require().Len(columns, 4)
			var doubled, note driver.ColumnMetadata
			for _, column := range columns {
				if column.Name == "doubled" {
					doubled = column
				}
				if column.Name == "note" {
					note = column
				}
			}
			s.Require().NotNil(doubled.Generation)
			s.Equal("stored", doubled.Generation.Type)
			s.Require().NotNil(doubled.Generation.Expression)
			s.Contains(strings.ToLower(*doubled.Generation.Expression), "price")
			s.True(note.Nullable)
			if isMariaDBTest(name) {
				s.Equal("NULL", doubled.Default)
				s.Equal("NULL", note.Default)
			} else {
				s.Nil(doubled.Default)
				s.Nil(note.Default)
			}
			s.True(schema.HasColumn("laravel_metadata", "NOTE"))
			s.True(schema.HasColumns("laravel_metadata", []string{"ID", "NOTE"}))

			legacy, err := schema.GetColumns("laravel_metadata")
			s.Require().NoError(err)
			s.Require().Len(legacy, 4)
			s.NotNil(legacy[2].Generation)

			schemas, err := schema.GetSchemas()
			s.Require().NoError(err)
			current := ""
			for _, value := range schemas {
				if value.Default {
					current = value.Name
				}
			}
			s.Require().NotEmpty(current)
			tables, err := schema.GetTables(current)
			s.Require().NoError(err)
			found := false
			for _, value := range tables {
				if value.Name == table {
					found = true
					s.Equal(current+"."+table, value.SchemaQualifiedName)
				}
			}
			s.True(found)
			s.Contains(schema.GetTableListing(current), current+"."+table)
			s.Contains(schema.GetTableListing(current, false), table)
			s.True(schema.HasTable("laravel_metadata"))
		})
	}
}

func (s *SchemaSuite) TestLaravelIndexAndForeignKeyMetadata() {
	for name, query := range s.driverToTestQuery {
		s.Run(name, func() {
			schema := newSchema(query, s.driverToTestQuery)
			parent, child := s.prefix+"metadata_parent", s.prefix+"metadata_child"
			s.Require().NoError(query.DB().Statement("create table " + parent + " (id integer primary key)"))
			defer func() { s.NoError(query.DB().Statement("drop table " + parent)) }()
			s.Require().NoError(query.DB().Statement("create table " + child + " (id integer primary key, parent_id integer, constraint metadata_parent_fk foreign key (parent_id) references " + parent + " (id) on delete cascade)"))
			defer func() { s.NoError(query.DB().Statement("drop table " + child)) }()
			s.Require().NoError(query.DB().Statement("create unique index metadata_parent_unique on " + child + " (parent_id, id)"))
			indexes, err := schema.GetIndexes("metadata_child")
			s.Require().NoError(err)
			found := false
			for _, index := range indexes {
				if index.Name == "metadata_parent_unique" {
					found = true
					s.True(index.Unique)
					s.Equal([]string{"parent_id", "id"}, index.Columns)
				}
			}
			s.True(found)
			s.True(schema.HasIndex("metadata_child", []string{"parent_id", "id"}, "unique"))
			keys, err := schema.GetForeignKeys("metadata_child")
			s.Require().NoError(err)
			s.Require().Len(keys, 1)
			s.Equal([]string{"parent_id"}, keys[0].Columns)
			s.Equal([]string{"id"}, keys[0].ForeignColumns)
			s.Equal(parent, keys[0].ForeignTable)
			s.Equal("cascade", keys[0].OnDelete)
		})
	}
}
