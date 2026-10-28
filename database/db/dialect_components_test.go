package db

import (
	"context"
	"testing"

	contractsquery "github.com/gradii/framework/contracts/database/query"
	databasedriver "github.com/gradii/framework/database/driver"
	mysqldialect "github.com/gradii/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type suppliedInsertProcessor struct {
	*mysqldialect.QueryProcessor
	statements []string
}

func (p *suppliedInsertProcessor) ProcessInsertGetID(_ contractsquery.Connection, statement string, _ []any, _ string) (any, error) {
	p.statements = append(p.statements, statement)
	return int64(901), nil
}

func TestDBQueryCarriesSuppliedComponentsIntoFreshAndClonedQueries(t *testing.T) {
	grammar := mysqldialect.NewQueryGrammar("app_")
	processor := &suppliedInsertProcessor{QueryProcessor: mysqldialect.NewQueryProcessor()}
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: processor, Dialect: mysqldialect.NewDialect()}
	query := NewQuery(context.Background(), nil, nil, grammar, "mysql", nil, "users", nil, components)
	for _, child := range []*Query{query, query.Clone().(*Query), query.NewQuery().From("users").(*Query)} {
		id, err := child.InsertGetID(map[string]any{"name": "alice"})
		require.NoError(t, err)
		assert.Equal(t, int64(901), id)
		assert.Same(t, processor, child.builder.Processor())
		assert.Equal(t, "select * from `app_users` where `name` = 'a\\\\b''c'", child.Where("name", `a\b'c`).ToRawSql().Get())
	}
	assert.Equal(t, []string{
		"insert into `app_users` (`name`) values (?)",
		"insert into `app_users` (`name`) values (?)",
		"insert into `app_users` (`name`) values (?)",
	}, processor.statements)
}
