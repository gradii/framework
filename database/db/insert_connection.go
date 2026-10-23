package db

import (
	"database/sql"

	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/support/carbon"
)

// insertConnection adapts the writer and tracing to a query processor for one insert operation.
type insertConnection struct {
	query     *Query
	result    sql.Result
	statement string
	values    []any
	begin     *carbon.Carbon
}

func (c *insertConnection) Insert(statement string, values []any, sequence string) error {
	c.statement = querybuilder.ReplacePlaceholders(statement, c.query.driver)
	c.values = values
	c.begin = carbon.Now()
	result, err := c.query.writeBuilder.ExecContext(c.query.ctx, c.statement, values...)
	if err != nil {
		c.query.trace(c.query.writeBuilder, c.statement, values, c.begin, -1, err)
		return err
	}
	c.result = result
	return nil
}

func (c *insertConnection) LastInsertID(sequence string) (any, error) {
	id, err := c.result.LastInsertId()
	rows := int64(1)
	if err != nil {
		rows = -1
	}
	c.query.trace(c.query.writeBuilder, c.statement, c.values, c.begin, rows, err)
	if err != nil {
		return nil, err
	}
	return id, nil
}

func (c *insertConnection) SelectFromWriteConnection(statement string, values []any, sequence string) (any, error) {
	statement = querybuilder.ReplacePlaceholders(statement, c.query.driver)
	begin := carbon.Now()
	var id int64
	if err := c.query.writeBuilder.GetContext(c.query.ctx, &id, statement, values...); err != nil {
		c.query.trace(c.query.writeBuilder, statement, values, begin, -1, err)
		return nil, err
	}
	c.query.trace(c.query.writeBuilder, statement, values, begin, 1, nil)
	return id, nil
}
