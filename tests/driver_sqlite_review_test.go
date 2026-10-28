package tests

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	frameworkconfig "github.com/gradii/framework/config"
	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsschema "github.com/gradii/framework/contracts/database/schema"
	frameworkdb "github.com/gradii/framework/database/db"
	frameworkschema "github.com/gradii/framework/database/schema"
	frameworkerrors "github.com/gradii/framework/errors"
	"github.com/gradii/framework/testing/utils"
	"github.com/gradii/sqlite"
	"github.com/stretchr/testify/require"
)

func newRebuildDatabase(t *testing.T, ctx context.Context) (*frameworkschema.Schema, contractsdb.DB) {
	t.Helper()
	name := fmt.Sprintf("sqlite_rebuild_%d", time.Now().UnixNano())
	config := frameworkconfig.NewApplication("")
	config.Add("database.default", name)
	config.Add("database.pool", map[string]any{"max_open_conns": 1, "max_idle_conns": 1})
	config.Add("database.connections."+name, map[string]any{
		"driver": sqlite.Name, "dsn": "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "rebuild.db")),
		"via": func() (contractsdriver.Driver, error) { return sqlite.NewSqlite(config, nil, name), nil },
	})
	log := utils.NewTestLog()
	db, err := frameworkdb.BuildDB(ctx, config, log, name, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Purge()) })
	schema, err := frameworkschema.NewSchema(config, log, nil, sqlite.NewSqlite(config, nil, name), nil, db)
	require.NoError(t, err)
	require.NoError(t, schema.EnableForeignKeyConstraints())
	return schema, db
}

func TestRebuildPreservesForeignTargets(t *testing.T) {
	for _, inline := range []bool{false, true} {
		t.Run(fmt.Sprint("inline=", inline), func(t *testing.T) {
			s, db := newRebuildDatabase(t, context.Background())
			require.NoError(t, s.Sql("create table parent (id integer primary key)"))
			definition := "id integer primary key, parent_id integer, self_id integer, extra text, constraint external_fk foreign key (parent_id) references parent(id) on delete cascade deferrable initially deferred, constraint self_fk foreign key (self_id) references child(id) on delete set null"
			if inline {
				definition = "id integer primary key, parent_id integer constraint external_fk references parent(id) on delete cascade deferrable initially deferred, self_id integer constraint self_fk references child(id) on delete set null, extra text"
			}
			require.NoError(t, s.Sql("create table child ("+definition+")"))
			require.NoError(t, s.Sql("insert into parent values (1)"))
			require.NoError(t, s.Sql("insert into child values (1,1,null,'x'), (2,1,1,'x')"))
			require.NoError(t, s.Table("child", func(b contractsschema.Blueprint) {
				b.RenameColumn("id", "child_id")
				b.DropColumn("extra")
			}))
			require.NoError(t, s.Sql("insert into child values (3,1,2)"))
			require.Error(t, s.Sql("insert into child values (4,999,null)"))
			require.Error(t, s.Sql("insert into child values (4,1,999)"))
			require.NoError(t, s.Sql("delete from child where child_id=1"))
			value, err := db.Scalar("select self_id from child where child_id=2")
			require.NoError(t, err)
			require.Nil(t, value)
			ddl, err := db.Scalar("select sql from sqlite_master where name='child'")
			require.NoError(t, err)
			require.Contains(t, ddl, "deferrable initially deferred")
			require.Contains(t, ddl, "external_fk")
			require.NoError(t, s.Sql("delete from parent where id=1"))
			count, err := db.Scalar("select count(*) from child")
			require.NoError(t, err)
			require.EqualValues(t, 0, count)
		})
	}
}

func TestRebuildSequenceHighWatermark(t *testing.T) {
	for _, attached := range []bool{false, true} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("attached=%t/empty=%t", attached, empty), func(t *testing.T) {
				s, _ := newRebuildDatabase(t, context.Background())
				require.NoError(t, s.WithWriteConnection(func(scoped contractsschema.Schema) error {
					namespace := "main"
					if attached {
						namespace = "aux"
						require.NoError(t, scoped.Sql("attach database ? as aux", filepath.Join(t.TempDir(), "attached.db")))
					}
					table := namespace + ".items"
					require.NoError(t, scoped.Sql("create table "+table+" (id integer primary key autoincrement, label text, extra text)"))
					require.NoError(t, scoped.Sql("insert into "+table+"(id,label) values (10,'keep'),(100,'delete')"))
					require.NoError(t, scoped.Sql("delete from "+table+" where id=100"))
					if empty {
						require.NoError(t, scoped.Sql("delete from "+table))
					}
					require.NoError(t, scoped.Table(table, func(b contractsschema.Blueprint) {
						b.DropColumn("extra")
						b.Rename("renamed")
					}))
					table = namespace + ".renamed"
					for _, want := range []int{101, 102} {
						require.NoError(t, scoped.Table(table, func(b contractsschema.Blueprint) { b.Text("label").Nullable().Change() }))
						require.NoError(t, scoped.Sql("insert into "+table+"(label) values ('new')"))
						value, err := scoped.DB().Scalar("select max(id) from " + table)
						require.NoError(t, err)
						require.EqualValues(t, want, value)
					}
					return nil
				}))
			})
		}
	}
}

func TestRebuildRenameIndexPreservesDefinitionAndOrder(t *testing.T) {
	for _, renameFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(renameFirst), func(t *testing.T) {
			s, db := newRebuildDatabase(t, context.Background())
			require.NoError(t, s.Sql("create table items (id integer primary key, label text, enabled integer, extra text)"))
			require.NoError(t, s.Sql("create unique index old_index on items(lower(label)) where enabled=1"))
			require.NoError(t, s.Table("items", func(b contractsschema.Blueprint) {
				if renameFirst {
					b.RenameIndex("old_index", "middle_index")
				}
				b.Text("label").Nullable().Change()
				if !renameFirst {
					b.RenameIndex("old_index", "middle_index")
				}
				b.RenameIndex("middle_index", "new_index")
				b.Index("enabled").Name("added_index")
				b.RenameIndex("added_index", "added_renamed")
			}))
			require.False(t, s.HasIndex("items", "old_index"))
			require.False(t, s.HasIndex("items", "middle_index"))
			require.True(t, s.HasIndex("items", "new_index"))
			require.True(t, s.HasIndex("items", "added_renamed"))
			definition, err := db.Scalar("select sql from sqlite_master where name='new_index'")
			require.NoError(t, err)
			require.Contains(t, definition, "lower(label)")
			require.Contains(t, definition, "where enabled=1")
			require.NoError(t, s.Sql("insert into items(label,enabled) values ('Name',1),('Name',0),('name',0)"))
			require.Error(t, s.Sql("insert into items(label,enabled) values ('name',1)"))
			require.NoError(t, s.Table("items", func(b contractsschema.Blueprint) {
				b.DropColumn("extra")
				b.RenameIndex("new_index", "final_index")
				b.DropIndexByName("final_index")
			}))
			require.False(t, s.HasIndex("items", "final_index"))
		})
	}
}

func TestRebuildViewsAndPragmaRestoration(t *testing.T) {
	for _, original := range []int{0, 1} {
		t.Run(fmt.Sprint(original), func(t *testing.T) {
			s, db := newRebuildDatabase(t, context.Background())
			require.NoError(t, s.Sql(fmt.Sprintf("pragma legacy_alter_table=%d", original)))
			require.NoError(t, s.Sql("create table items (id integer primary key, label text, extra text)"))
			require.NoError(t, s.Sql("insert into items values (1,'kept','extra')"))
			require.NoError(t, s.Sql("create view labels as select id,label from items"))
			require.NoError(t, s.Sql("create view nested_labels as select * from labels"))
			require.NoError(t, s.Sql("create trigger insert_label instead of insert on labels begin insert into items(id,label) values(new.id,new.label); end"))
			require.NoError(t, s.Table("items", func(b contractsschema.Blueprint) { b.Text("label").Nullable().Change() }))
			require.NoError(t, s.Sql("insert into labels values (2,'through view')"))
			count, err := db.Scalar("select count(*) from nested_labels")
			require.NoError(t, err)
			require.EqualValues(t, 2, count)
			require.Error(t, s.Table("items", func(b contractsschema.Blueprint) { b.DropColumn("label") }))
			require.True(t, s.HasColumn("items", "label"))
			count, err = db.Scalar("select count(*) from nested_labels")
			require.NoError(t, err)
			require.EqualValues(t, 2, count)
			value, err := db.Scalar("pragma legacy_alter_table")
			require.NoError(t, err)
			require.EqualValues(t, original, value)
			require.False(t, s.HasTable("__temp__items"))
			value, err = db.Scalar("pragma foreign_keys")
			require.NoError(t, err)
			require.EqualValues(t, 1, value)
		})
	}
}

func TestRebuildCancellationRestoresSession(t *testing.T) {
	for _, original := range []int{0, 1} {
		t.Run(fmt.Sprint(original), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, db := newRebuildDatabase(t, ctx)
			require.NoError(t, s.Sql(fmt.Sprintf("pragma legacy_alter_table=%d", original)))
			require.NoError(t, s.Sql("create table items (id integer primary key, label text)"))
			require.NoError(t, s.Sql("insert into items values (1,'kept')"))
			require.NoError(t, s.Sql("create temp table cancellation_session_marker (id integer)"))
			db.BeforeExecuting(func(_ context.Context, sql string, _ []any) error {
				if strings.HasPrefix(sql, `insert into "main"."__temp__items"`) {
					cancel()
					return context.Canceled
				}
				return nil
			})
			err := s.Table("items", func(b contractsschema.Blueprint) { b.Text("label").Nullable().Change() })
			require.Error(t, err)
			fresh := db.WithContext(context.Background())
			value, err := fresh.Scalar("pragma legacy_alter_table")
			require.NoError(t, err)
			marker, markerErr := fresh.Scalar("select count(*) from temp.sqlite_master where name='cancellation_session_marker'")
			require.NoError(t, markerErr)
			if marker == int64(1) {
				require.EqualValues(t, original, value)
			} else {
				// database/sql may discard a connection when it cancels a transaction.
				require.EqualValues(t, 0, marker)
				require.EqualValues(t, 0, value)
			}
			value, err = fresh.Scalar("select count(*) from items where label='kept'")
			require.NoError(t, err)
			require.EqualValues(t, 1, value)
			value, err = fresh.Scalar("select count(*) from sqlite_master where name='__temp__items'")
			require.NoError(t, err)
			require.EqualValues(t, 0, value)
		})
	}
}

func TestRebuildCleanupFailureDiscardsConnectionAndRollsBack(t *testing.T) {
	s, db := newRebuildDatabase(t, context.Background())
	require.NoError(t, s.Sql("create table items (id integer primary key, label text not null)"))
	require.NoError(t, s.Sql("create temp table leased_session_marker (id integer)"))
	db.BeforeExecuting(func(_ context.Context, sql string, _ []any) error {
		if sql == "pragma legacy_alter_table = OFF" {
			return frameworkerrors.SchemaSessionStateUnknown
		}
		return nil
	})
	err := s.Table("items", func(b contractsschema.Blueprint) { b.Text("label").Nullable().Change() })
	require.Error(t, err)
	value, err := db.Scalar("pragma legacy_alter_table")
	require.NoError(t, err)
	require.EqualValues(t, 0, value)
	value, err = db.Scalar("select count(*) from temp.sqlite_master where name='leased_session_marker'")
	require.NoError(t, err)
	require.EqualValues(t, 0, value, "the dirty physical connection was not returned to the pool")
	value, err = db.Scalar(`select "notnull" from pragma_table_info('items') where name='label'`)
	require.NoError(t, err)
	require.EqualValues(t, 1, value, "cleanup failure rolls back the schema change")
}

type rebuildTransactionDB struct {
	contractsdb.DB
	active contractsdb.Tx
}

func (c rebuildTransactionDB) WithWriteConnection(callback func(contractsdb.Tx) error) error {
	return callback(c.active)
}

func TestRebuildFailureUsesSavepointInsideExistingTransaction(t *testing.T) {
	s, db := newRebuildDatabase(t, context.Background())
	require.NoError(t, s.Sql("create table items (id integer primary key, label text)"))
	require.NoError(t, s.Sql("create view labels as select label from items"))
	require.NoError(t, db.WithWriteConnection(func(session contractsdb.Tx) error {
		require.NoError(t, session.Statement("pragma foreign_keys=OFF"))
		defer func() { require.NoError(t, session.Statement("pragma foreign_keys=ON")) }()
		return session.Transaction(func(tx contractsdb.Tx) error {
			config := frameworkconfig.NewApplication("")
			config.Add("database.connections."+db.Name(), map[string]any{"driver": sqlite.Name, "dsn": ":memory:"})
			transactional, err := frameworkschema.NewSchema(config, utils.NewTestLog(), nil, sqlite.NewSqlite(config, nil, db.Name()), nil, rebuildTransactionDB{DB: db, active: tx})
			require.NoError(t, err)
			require.NoError(t, tx.Statement("insert into items values (1,'before savepoint')"))
			require.Error(t, transactional.Table("items", func(b contractsschema.Blueprint) { b.DropColumn("label") }))
			value, err := tx.Scalar("pragma legacy_alter_table")
			require.NoError(t, err)
			require.EqualValues(t, 0, value)
			require.NoError(t, tx.Statement("insert into items values (2,'after rollback')"))
			return nil
		})
	}))
	count, err := db.Scalar("select count(*) from labels")
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.False(t, s.HasTable("__temp__items"))
}

func TestRebuildRejectsUnleasedTransactionBeforeAnySessionSQL(t *testing.T) {
	s, db := newRebuildDatabase(t, context.Background())
	require.NoError(t, s.Sql("create table items (id integer primary key, label text)"))
	require.NoError(t, s.Sql("pragma foreign_keys=OFF"))
	active, err := db.BeginTransaction()
	require.NoError(t, err)
	defer active.Rollback()
	require.NoError(t, active.Statement("insert into items values (1,'before rejected rebuild')"))
	config := frameworkconfig.NewApplication("")
	config.Add("database.connections."+db.Name(), map[string]any{"driver": sqlite.Name, "dsn": ":memory:"})
	transactional, err := frameworkschema.NewSchema(config, utils.NewTestLog(), nil, sqlite.NewSqlite(config, nil, db.Name()), nil, rebuildTransactionDB{DB: db, active: active})
	require.NoError(t, err)
	var statements []string
	db.BeforeExecuting(func(_ context.Context, sql string, _ []any) error {
		statements = append(statements, sql)
		return nil
	})
	err = transactional.Table("items", func(b contractsschema.Blueprint) { b.Text("label").Nullable().Change() })
	require.ErrorIs(t, err, frameworkerrors.SchemaSessionDiscardNotSupported)
	require.Empty(t, statements, "reject before even querying a pragma or opening a savepoint")
	require.NoError(t, active.Statement("insert into items values (2,'outer transaction still usable')"))
	require.NoError(t, active.Commit())
	count, err := db.Scalar("select count(*) from items")
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	value, err := db.Scalar("pragma legacy_alter_table")
	require.NoError(t, err)
	require.EqualValues(t, 0, value)
}

func TestRebuildAttachedViewsAndSelfReferenceTableRename(t *testing.T) {
	s, _ := newRebuildDatabase(t, context.Background())
	require.NoError(t, s.WithWriteConnection(func(scoped contractsschema.Schema) error {
		require.NoError(t, scoped.Sql("attach database ? as aux", filepath.Join(t.TempDir(), "attached.db")))
		require.NoError(t, scoped.Sql("create table aux.items (id integer primary key, label text, parent_id integer references items(id), extra text)"))
		require.NoError(t, scoped.Sql("insert into aux.items values (1,'root',null,null)"))
		require.NoError(t, scoped.Sql("create view aux.labels as select label from items"))
		require.NoError(t, scoped.Sql("create temp view temporary_labels as select * from aux.labels"))
		require.NoError(t, scoped.Table("aux.items", func(b contractsschema.Blueprint) {
			b.Text("label").Nullable().Change()
		}))
		value, err := scoped.DB().Scalar("select label from temporary_labels")
		require.NoError(t, err)
		require.Equal(t, "root", value)
		// Views are deliberately not rewritten when a rename invalidates them.
		require.Error(t, scoped.Table("aux.items", func(b contractsschema.Blueprint) {
			b.DropColumn("extra")
			b.Rename("renamed")
		}))
		require.True(t, scoped.HasTable("aux.items"))
		require.NoError(t, scoped.Sql("drop view temporary_labels"))
		require.NoError(t, scoped.Sql("drop view aux.labels"))
		require.NoError(t, scoped.Table("aux.items", func(b contractsschema.Blueprint) {
			b.DropColumn("extra")
			b.RenameColumn("id", "item_id")
			b.Rename("renamed")
		}))
		require.NoError(t, scoped.Sql("insert into aux.renamed values (2,'child',1)"))
		require.Error(t, scoped.Sql("insert into aux.renamed values (3,'invalid',999)"))
		return nil
	}))
}
