package schema

import (
	"context"
	"sync"
	"time"

	"github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/contracts/database/driver"
	contractsschema "github.com/gradii/framework/contracts/database/schema"
	"github.com/gradii/framework/errors"
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
	if grammar, ok := r.grammar.(driver.SchemaOperationsGrammar); ok {
		statements, err := grammar.CompileSchemaOperation(driver.SchemaOperation{Kind: "foreignKeys", Enabled: enabled})
		if err != nil {
			return "", err
		}
		if len(statements) > 0 {
			return statements[0].SQL, nil
		}
		return "", nil
	}
	return "", errors.SchemaFeatureNotSupported.Args(r.driverName, "foreign key constraint control")
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
	disable, err := r.foreignKeySQL(false)
	if err != nil {
		return errors.Join(err, close())
	}
	query := ""
	if grammar, ok := r.grammar.(driver.SchemaOperationsGrammar); ok {
		query = grammar.ForeignKeyStateQuery()
	}
	if query != "" {
		value, err := connection.Scalar(query)
		if err != nil {
			_ = close()
			return err
		}
		if r.foreignKeysOutsideTransaction() && connection.TransactionLevel() > 0 && metadataBool(value) {
			_ = close()
			return errors.SchemaForeignKeysInTransaction
		}
		if !metadataBool(value) {
			restore = disable
		}
	}
	r.constraints.mu.Lock()
	if r.constraints.connection != nil {
		r.constraints.mu.Unlock()
		return close()
	}
	r.constraints.connection, r.constraints.restore, r.constraints.close = connection, restore, closeConnection
	r.constraints.mu.Unlock()
	if err := connection.Statement(disable); err != nil {
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
	if grammar, ok := r.grammar.(driver.SchemaDatabaseFiles); ok {
		return grammar.CreateDatabase(name)
	}
	return r.executeSchemaOperation(driver.SchemaOperation{Kind: "createDatabase", Name: name, Charset: r.charset})
}

func (r *Schema) DropDatabaseIfExists(name string) error {
	if grammar, ok := r.grammar.(driver.SchemaDatabaseFiles); ok {
		return grammar.DropDatabaseIfExists(name)
	}
	return r.executeSchemaOperation(driver.SchemaOperation{Kind: "dropDatabase", Name: name})
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
	var policy driver.SchemaExecutionPolicy
	if planner, ok := r.grammar.(driver.SchemaBlueprintPlanner); ok {
		policy = planner.SchemaExecutionPolicy(blueprint)
	}
	return r.withSchemaExecutionPolicy(policy, func(scoped *Schema) error {
		return scoped.runDDL(blueprint, scoped.executionConnection())
	})
}

func (r *Schema) foreignKeysOutsideTransaction() bool {
	if planner, ok := r.grammar.(driver.SchemaOperationPlanner); ok {
		return planner.SchemaOperationPolicy(driver.SchemaOperation{Kind: "foreignKeys"}).ForeignKeysOutsideTransaction
	}
	return false
}

// Session changes surround the transaction; compilation and catalog reads happen
// inside it so a rebuild observes the same connection and schema state it alters.
func (r *Schema) withSchemaExecutionPolicy(policy driver.SchemaExecutionPolicy, callback func(*Schema) error) error {
	execute := func(scoped *Schema) error {
		executor := scoped.executionConnection()
		if executor == nil {
			return callback(scoped)
		}
		if !policy.Transactional || executor.TransactionLevel() > 0 {
			return callback(scoped)
		}
		return executor.Transaction(func(tx db.Tx) error {
			transactional := *scoped
			transactional.executor = tx
			return callback(&transactional)
		})
	}
	if !policy.DisableForeignKeys {
		return execute(r)
	}
	executor := r.executionConnection()
	if executor != nil && executor.TransactionLevel() > 0 && policy.ForeignKeysOutsideTransaction {
		grammar, ok := r.grammar.(driver.SchemaOperationsGrammar)
		if !ok || grammar.ForeignKeyStateQuery() == "" {
			return errors.SchemaForeignKeysInTransaction
		}
		enabled, err := executor.Scalar(grammar.ForeignKeyStateQuery())
		if err != nil {
			return err
		}
		if metadataBool(enabled) {
			return errors.SchemaForeignKeysInTransaction
		}
		return execute(r)
	}
	return r.WithoutForeignKeyConstraints(func(scoped contractsschema.Schema) error { return execute(scoped.(*Schema)) })
}

func (r *Schema) executeSchemaOperation(operation driver.SchemaOperation) error {
	grammar, ok := r.grammar.(driver.SchemaOperationsGrammar)
	if !ok {
		return errors.SchemaFeatureNotSupported.Args(r.driverName, operation.Kind)
	}
	statements, err := grammar.CompileSchemaOperation(operation)
	if err != nil {
		return err
	}
	if len(statements) == 0 {
		return nil
	}
	var policy driver.SchemaExecutionPolicy
	if planner, ok := r.grammar.(driver.SchemaOperationPlanner); ok {
		policy = planner.SchemaOperationPolicy(operation)
	}
	return r.withSchemaExecutionPolicy(policy, func(scoped *Schema) error { return scoped.executeSchemaStatements(statements) })
}

func (r *Schema) runDDL(blueprint *Blueprint, connection db.Tx) error {
	scoped := *r
	scoped.executor = connection
	prepared := *blueprint
	prepared.schema = &scoped
	statements, err := prepared.CompileStatements(r.grammar)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if statement.SQL == "" {
			continue
		}
		if err := connection.Statement(statement.SQL, statement.Bindings...); err != nil {
			return err
		}
	}
	return nil
}

func (r *Schema) executeSchemaStatements(statements []driver.SchemaStatement) error {
	for _, statement := range statements {
		if statement.SQL == "" {
			continue
		}
		if connection := r.executionConnection(); connection != nil {
			if err := connection.Statement(statement.SQL, statement.Bindings...); err != nil {
				return err
			}
		} else if r.orm != nil {
			if _, err := r.orm.Query().UseWritePdo().Exec(statement.SQL, statement.Bindings...); err != nil {
				return err
			}
		} else {
			return errors.DatabaseConnectionIsNil
		}
	}
	return nil
}
