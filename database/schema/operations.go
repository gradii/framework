package schema

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/database/db"
	contractsschema "github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/errors"
)

type schemaConstraintScope struct {
	mu         sync.Mutex
	connection db.Tx
	close      func() error
	restore    string
}

func (r *Schema) DB() db.Tx { return r.executionConnection() }

func (r *Schema) WithWriteConnection(callback func(contractsschema.Schema) error) error {
	if r.executor != nil {
		return callback(r)
	}
	r.constraints.mu.Lock()
	pinned := r.constraints.connection != nil
	r.constraints.mu.Unlock()
	if pinned {
		return callback(r)
	}
	if r.db == nil {
		return errors.DatabaseConnectionIsNil
	}
	return r.db.WithWriteConnection(func(connection db.Tx) error {
		scoped := *r
		scoped.executor = connection
		scoped.constraints = &schemaConstraintScope{}
		return callback(&scoped)
	})
}

func (r *Schema) executionConnection() db.Tx {
	if r.executor != nil {
		return r.executor
	}
	if r.constraints != nil {
		r.constraints.mu.Lock()
		connection := r.constraints.connection
		r.constraints.mu.Unlock()
		if connection != nil {
			return connection
		}
	}
	return r.db
}
func (r *Schema) foreignKeySQL(enabled bool) (string, error) {
	switch r.metadataDialect() {
	case "mysql":
		if enabled {
			return "SET FOREIGN_KEY_CHECKS=1", nil
		}
		return "SET FOREIGN_KEY_CHECKS=0", nil
	case "sqlite":
		if enabled {
			return "PRAGMA foreign_keys = ON", nil
		}
		return "PRAGMA foreign_keys = OFF", nil
	case "postgres":
		if enabled {
			return "SET CONSTRAINTS ALL IMMEDIATE", nil
		}
		return "SET CONSTRAINTS ALL DEFERRED", nil
	case "sqlserver":
		if enabled {
			return "EXEC sp_msforeachtable 'ALTER TABLE ? WITH CHECK CHECK CONSTRAINT all'", nil
		}
		return "EXEC sp_msforeachtable 'ALTER TABLE ? NOCHECK CONSTRAINT all'", nil
	default:
		return "", errors.SchemaFeatureNotSupported.Args(r.driverName, "foreign key constraint control")
	}
}
func (r *Schema) DisableForeignKeyConstraints() error {
	if r.db == nil {
		return errors.DatabaseConnectionIsNil
	}
	r.constraints.mu.Lock()
	existing := r.constraints.connection
	r.constraints.mu.Unlock()
	if existing != nil {
		return nil
	}
	connection := r.executor
	var closeConnection func() error
	if connection == nil {
		lease, err := r.db.AcquireWriteConnection()
		if err != nil {
			return err
		}
		connection, closeConnection = lease, lease.Close
	}
	close := func() error {
		if closeConnection != nil {
			return closeConnection()
		}
		return nil
	}
	restore, err := r.foreignKeySQL(true)
	if err != nil {
		_ = close()
		return err
	}
	switch r.metadataDialect() {
	case "mysql", "sqlite":
		query := "select @@foreign_key_checks"
		if r.metadataDialect() == "sqlite" {
			query = "pragma foreign_keys"
		}
		value, err := connection.Scalar(query)
		if err != nil {
			_ = close()
			return err
		}
		if r.metadataDialect() == "sqlite" && connection.TransactionLevel() > 0 && metadataBool(value) {
			_ = close()
			return errors.SchemaSQLiteForeignKeysInTransaction
		}
		if !metadataBool(value) {
			restore, _ = r.foreignKeySQL(false)
		}
	}
	r.constraints.mu.Lock()
	if r.constraints.connection != nil {
		r.constraints.mu.Unlock()
		return close()
	}
	r.constraints.connection, r.constraints.restore, r.constraints.close = connection, restore, closeConnection
	r.constraints.mu.Unlock()
	statement, _ := r.foreignKeySQL(false)
	if err := connection.Statement(statement); err != nil {
		r.constraints.mu.Lock()
		r.constraints.connection = nil
		r.constraints.mu.Unlock()
		return errors.Join(err, close())
	}
	return nil
}
func (r *Schema) restoreForeignKeyConstraints(original bool) error {
	r.constraints.mu.Lock()
	connection, restore, close := r.constraints.connection, r.constraints.restore, r.constraints.close
	r.constraints.mu.Unlock()
	if !original {
		var err error
		restore, err = r.foreignKeySQL(true)
		if err != nil {
			return err
		}
	}
	if connection == nil {
		if original {
			return nil
		}
		return r.Sql(restore)
	}
	// Restoring a leased session must still run when the request has been cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := connection.StatementContext(ctx, restore)
	r.constraints.mu.Lock()
	if r.constraints.connection == connection {
		r.constraints.connection = nil
	}
	r.constraints.mu.Unlock()
	if close != nil {
		return errors.Join(err, close())
	}
	return err
}
func (r *Schema) EnableForeignKeyConstraints() error { return r.restoreForeignKeyConstraints(false) }

// The callback's schema owns the pinned connection and restores its previous setting on exit.
func (r *Schema) WithoutForeignKeyConstraints(callback func(contractsschema.Schema) error) (err error) {
	r.constraints.mu.Lock()
	existing := r.constraints.connection
	r.constraints.mu.Unlock()
	if existing != nil {
		return callback(r)
	}
	scoped := *r
	scoped.constraints = &schemaConstraintScope{}
	if err := scoped.DisableForeignKeyConstraints(); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, scoped.restoreForeignKeyConstraints(true)) }()
	return callback(&scoped)
}
func (r *Schema) CreateDatabase(name string) error {
	if r.metadataDialect() == "sqlite" {
		file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o666)
		if err != nil {
			return err
		}
		return file.Close()
	}
	statement := "create database " + r.ddlWrap().Value(name)
	if r.metadataDialect() == "mysql" && r.charset != "" {
		statement += " default character set " + r.ddlWrap().Value(r.charset)
	}
	if r.metadataDialect() == "postgres" && r.charset != "" {
		statement += " encoding " + r.ddlWrap().Value(r.charset)
	}
	return r.Sql(statement)
}
func (r *Schema) DropDatabaseIfExists(name string) error {
	if r.metadataDialect() == "sqlite" {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return r.Sql("drop database if exists " + r.ddlWrap().Value(name))
}
func (r *Schema) WhenTableHasColumn(table, column string, callback func(contractsschema.Blueprint)) error {
	if r.HasColumn(table, column) {
		return r.Table(table, callback)
	}
	return nil
}
func (r *Schema) WhenTableDoesntHaveColumn(table, column string, callback func(contractsschema.Blueprint)) error {
	if !r.HasColumn(table, column) {
		return r.Table(table, callback)
	}
	return nil
}
func (r *Schema) WhenTableHasIndex(table string, index any, callback func(contractsschema.Blueprint), indexType ...string) error {
	if r.HasIndex(table, index, indexType...) {
		return r.Table(table, callback)
	}
	return nil
}
func (r *Schema) WhenTableDoesntHaveIndex(table string, index any, callback func(contractsschema.Blueprint), indexType ...string) error {
	if !r.HasIndex(table, index, indexType...) {
		return r.Table(table, callback)
	}
	return nil
}

func (r *Schema) buildNative(blueprint *Blueprint) error {
	executor := r.executionConnection()
	run := func(connection db.Tx) error { return r.runDDL(blueprint, connection) }
	if r.metadataDialect() == "sqlite" && blueprint.sqliteRebuild() {
		if executor.TransactionLevel() > 0 {
			value, err := executor.Scalar("pragma foreign_keys")
			if err != nil {
				return err
			}
			if metadataBool(value) {
				return errors.SchemaSQLiteForeignKeysInTransaction
			}
			return run(executor)
		}
		return r.WithoutForeignKeyConstraints(func(schema contractsschema.Schema) error {
			scoped := schema.(*Schema)
			return scoped.executionConnection().Transaction(func(tx db.Tx) error { return scoped.runDDL(blueprint, tx) })
		})
	}
	if executor.TransactionLevel() > 0 {
		return run(executor)
	}
	switch r.metadataDialect() {
	case "postgres", "sqlserver":
		return executor.Transaction(run)
	default:
		return run(executor)
	}
}

func (r *Schema) runDDL(blueprint *Blueprint, connection db.Tx) error {
	scoped := *r
	scoped.executor = connection
	prepared := *blueprint
	prepared.schema = &scoped
	statements, err := prepared.ToSql(r.grammar)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if statement == "" {
			continue
		}
		if err := connection.Statement(statement); err != nil {
			return err
		}
	}
	return nil
}
