package gorm

import (
	"database/sql"

	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/support/carbon"
)

// insertConnection keeps model key conversion, transaction routing, and tracing in the ORM adapter.
type insertConnection struct {
	query     *Query
	pool      statementPool
	result    sql.Result
	statement string
	values    []any
	begin     *carbon.Carbon
}

func (c *insertConnection) Insert(statement string, values []any, sequence string) error {
	c.statement, c.values, c.begin = statement, values, carbon.Now()
	result, err := c.pool.ExecContext(c.query.ctx, querybuilder.ReplacePlaceholders(statement, c.query.dbConfig.Driver), values...)
	if err != nil {
		c.query.trace(c.begin, statement, values, -1, err)
		return err
	}
	c.result = result
	return nil
}

func (c *insertConnection) LastInsertID(sequence string) (any, error) {
	// UUID and ULID keys supplied by the caller must not be replaced with a numeric row ID.
	if field := c.query.primaryKeyField(); field != nil && !isIntegerKeyType(field.FieldType) {
		c.query.trace(c.begin, c.statement, c.values, 1, nil)
		return nil, nil
	}
	id, err := c.result.LastInsertId()
	rows := int64(1)
	if err != nil {
		rows = -1
	}
	c.query.trace(c.begin, c.statement, c.values, rows, err)
	if err != nil {
		return nil, err
	}
	return id, nil
}

func (c *insertConnection) SelectFromWriteConnection(statement string, values []any, sequence string) (any, error) {
	return c.query.readReturnedKey(statement, values)
}
