package tests

import (
	"fmt"
	"os"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsschema "github.com/gradii/framework/contracts/database/schema"
	"github.com/gradii/framework/errors"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlserver"
	"github.com/spf13/cast"
)

func (s *SchemaSuite) reviewDriverQuery(name string) *TestQuery {
	query := s.driverToTestQuery[name]
	if query == nil && name == sqlserver.Name && os.Getenv("GRADII_TEST_SQLSERVER_DSN") != "" {
		query = NewTestQueryBuilder().Sqlserver(s.prefix, true)
		s.driverToTestQuery[name] = query
	}
	return query
}

type reviewSchemaTransactionDB struct {
	contractsdb.DB
	active contractsdb.Tx
}

func (c reviewSchemaTransactionDB) TransactionLevel() int { return c.active.TransactionLevel() }
func (c reviewSchemaTransactionDB) Transaction(callback func(contractsdb.Tx) error, attempts ...int) error {
	return c.active.Transaction(callback, attempts...)
}
func (c reviewSchemaTransactionDB) WithWriteConnection(callback func(contractsdb.Tx) error) error {
	return callback(c.active)
}

// These cases run in the independent integration module used by CNB, in
// addition to the focused native regression tests maintained by each driver.
func (s *SchemaSuite) TestDriverReviewEnumChanges() {
	for _, name := range []string{postgres.Name, sqlserver.Name} {
		query := s.reviewDriverQuery(name)
		if query == nil {
			continue
		}
		s.Run(name, func() {
			schema := newSchema(query, s.driverToTestQuery)
			db := query.DB()
			table := "review_enum"
			physical := s.prefix + table
			defer func() { s.NoError(schema.DropIfExists(table)) }()
			s.Require().NoError(schema.Create(table, func(b contractsschema.Blueprint) {
				b.Integer("id")
				b.Enum("state", []any{"open", "closed", ""}).Nullable().Default("open")
				b.Index("state").Name("review_enum_state_idx")
			}))
			length := "char_length"
			enumType := "varchar(255)"
			if name == sqlserver.Name {
				length = "len"
				enumType = "nvarchar(255)"
			}
			s.Require().NoError(db.Statement("alter table " + physical + " add constraint review_extra_length check (" + length + "(state) < 20)"))
			_, err := db.Table(table).Insert(map[string]any{"id": 1, "state": "open"})
			s.Require().NoError(err)
			s.Require().NoError(schema.Table(table, func(b contractsschema.Blueprint) {
				b.Enum("state", []any{"open", "closed", "", "new", "中文", "O'Reilly"}).Nullable().Default("new").Change()
			}))
			for i, value := range []string{"new", "中文", "O'Reilly", ""} {
				_, err := db.Table(table).Insert(map[string]any{"id": i + 2, "state": value})
				s.Require().NoError(err)
			}
			s.Error(schema.Table(table, func(b contractsschema.Blueprint) {
				b.Enum("state", []any{"closed"}).Nullable().Default("closed").Change()
			}))
			_, err = db.Table(table).Insert(map[string]any{"id": 20})
			s.Require().NoError(err)
			var row map[string]any
			s.Require().NoError(db.Table(table).Where("id", 20).First(&row))
			s.Equal("new", row["state"])
			// Catching a failed schema change inside an outer transaction must not
			// commit the dropped enum constraint or discard the caller's writes.
			_, err = db.ServerVersion()
			s.Require().NoError(err)
			tx, err := db.BeginTransaction()
			s.Require().NoError(err)
			defer tx.Rollback()
			_, err = tx.Table(table).Insert(map[string]any{"id": 30, "state": "open"})
			s.Require().NoError(err)
			nested := newSchema(query, s.driverToTestQuery, reviewSchemaTransactionDB{DB: db, active: tx})
			s.Error(nested.Table(table, func(b contractsschema.Blueprint) {
				b.Enum("state", []any{"closed"}).Nullable().Change()
			}))
			_, err = tx.Table(table).Insert(map[string]any{"id": 31, "state": "new"})
			s.Require().NoError(err)
			s.Require().NoError(tx.Commit())
			count, err := db.Table(table).WhereIn("id", []any{30, 31}).Count()
			s.Require().NoError(err)
			s.EqualValues(2, count)
			_, err = db.Table(table).Insert(map[string]any{"id": 21, "state": "invalid"})
			s.Error(err)
			s.True(schema.HasIndex(table, "review_enum_state_idx"))
			longValue := "this_label_is_longer_than_twenty"
			s.Require().NoError(schema.Table(table, func(b contractsschema.Blueprint) {
				b.Enum("state", []any{"open", "closed", "", "new", "中文", "O'Reilly", longValue}).Nullable().Change()
			}))
			_, err = db.Table(table).Insert(map[string]any{"id": 22, "state": longValue})
			s.ErrorContains(err, "review_extra_length")

			legacy := "review_enum_legacy"
			defer func() { s.NoError(schema.DropIfExists(legacy)) }()
			s.Require().NoError(db.Statement("create table " + s.prefix + legacy + " (state " + enumType + " check (state in ('a', 'b')) not null)"))
			s.Require().NoError(schema.Table(legacy, func(b contractsschema.Blueprint) { b.Enum("state", []any{"a", "b", "c"}).Change() }))
			_, err = db.Table(legacy).Insert(map[string]any{"state": "c"})
			s.Require().NoError(err)

			custom := "review_enum_custom"
			defer func() { s.NoError(schema.DropIfExists(custom)) }()
			s.Require().NoError(db.Statement("create table " + s.prefix + custom + " (state " + enumType + " not null, constraint review_custom_membership check (state in ('a', 'b')))"))
			err = schema.Table(custom, func(b contractsschema.Blueprint) {
				b.String("must_not_add")
				b.Enum("state", []any{"a", "b", "c"}).Change()
			})
			s.ErrorIs(err, errors.SchemaEnumConstraintAmbiguous)
			s.False(schema.HasColumn(custom, "must_not_add"))
			_, err = db.Table(custom).Insert(map[string]any{"state": "a"})
			s.Require().NoError(err)
			_, err = db.Table(custom).Insert(map[string]any{"state": "c"})
			s.ErrorContains(err, "review_custom_membership")
		})
	}
}

func (s *SchemaSuite) TestDriverReviewPostgresGeneratedRemoval() {
	query := s.driverToTestQuery[postgres.Name]
	if query == nil {
		s.T().Skip("PostgreSQL is unavailable")
	}
	version, err := query.DB().ServerVersion()
	s.Require().NoError(err)
	var major int
	_, err = fmt.Sscanf(version, "%d", &major)
	s.Require().NoError(err)
	if major < 13 {
		s.T().Skip("DROP EXPRESSION requires PostgreSQL 13")
	}
	schema := newSchema(query, s.driverToTestQuery)
	table := "review_generated"
	defer func() { s.NoError(schema.DropIfExists(table)) }()
	s.Require().NoError(schema.Create(table, func(b contractsschema.Blueprint) {
		b.Integer("source")
		b.Integer("doubled").StoredAs("source * 2")
		b.Integer("optional").StoredAs("source * 3").Nullable()
	}))
	_, err = query.DB().Table(table).Insert(map[string]any{"source": 3})
	s.Require().NoError(err)
	s.Require().NoError(schema.Table(table, func(b contractsschema.Blueprint) {
		b.Integer("doubled").StoredAs(nil).Default(7).Change()
		b.Integer("optional").StoredAs(nil).Nullable().Change()
	}))
	var row map[string]any
	s.Require().NoError(query.DB().Table(table).Where("source", 3).First(&row))
	s.Equal(6, cast.ToInt(row["doubled"]))
	s.Equal(9, cast.ToInt(row["optional"]))
	_, err = query.DB().Table(table).Insert(map[string]any{"source": 2})
	s.Require().NoError(err)
	row = nil
	s.Require().NoError(query.DB().Table(table).Where("source", 2).First(&row))
	s.Equal(7, cast.ToInt(row["doubled"]))
	s.Nil(row["optional"])
}

func (s *SchemaSuite) TestDriverReviewSqlserverModifiers() {
	query := s.reviewDriverQuery(sqlserver.Name)
	if query == nil {
		s.T().Skip("SQL Server is unavailable")
	}
	schema := newSchema(query, s.driverToTestQuery)
	db := query.DB()
	table := "review_defaults"
	defer func() { s.NoError(schema.DropIfExists(table)) }()
	s.Require().NoError(schema.Create(table, func(b contractsschema.Blueprint) {
		b.Integer("id")
		b.Timestamp("created_at", 4)
		b.String("label", 80).Default("中文🌏 O'Reilly")
	}))
	for i := 0; i < 2; i++ {
		s.Require().NoError(schema.Table(table, func(b contractsschema.Blueprint) { b.Timestamp("created_at", 4).UseCurrent().Change() }))
		_, err := db.Table(table).Insert(map[string]any{"id": i})
		s.Require().NoError(err)
	}
	var row map[string]any
	s.Require().NoError(db.Table(table).Where("id", 1).First(&row))
	s.Equal("中文🌏 O'Reilly", row["label"])
	s.NotNil(row["created_at"])
	s.Require().NoError(schema.Table(table, func(b contractsschema.Blueprint) {
		b.String("label", 100).Default("変更🌍 'quoted'").Change()
		b.String("added", 100).Default("追加🌍")
	}))
	_, err := db.Table(table).Insert(map[string]any{"id": 2})
	s.Require().NoError(err)
	row = nil
	s.Require().NoError(db.Table(table).Where("id", 2).First(&row))
	s.Equal("変更🌍 'quoted'", row["label"])
	s.Equal("追加🌍", row["added"])

	computed := "review_computed"
	defer func() { s.NoError(schema.DropIfExists(computed)) }()
	s.Require().NoError(schema.Create(computed, func(b contractsschema.Blueprint) {
		b.Integer("source").Nullable()
		b.Computed("required_value", "[source] + 1").Persisted().Nullable(false)
	}))
	_, err = db.Table(computed).Insert(map[string]any{"source": 3})
	s.Require().NoError(err)
	_, err = db.Table(computed).Insert(map[string]any{"source": nil})
	s.Error(err)
	err = schema.Create("review_computed_invalid", func(b contractsschema.Blueprint) {
		b.Integer("source")
		b.Computed("v", "[source] + 1").Nullable(false)
	})
	s.ErrorIs(err, errors.SchemaComputedNotNullRequiresPersisted)
	s.False(schema.HasTable("review_computed_invalid"))
}
