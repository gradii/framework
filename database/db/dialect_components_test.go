package db

import (
	"context"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/processors"
	"testing"

	contractsquery "github.com/gradii/framework/contracts/database/query"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type suppliedInsertProcessor struct {
	*processors.BaseProcessor
	statements []string
}

func (p *suppliedInsertProcessor) ProcessInsertGetID(_ contractsquery.Connection, statement string, _ []any, _ string) (any, error) {
	p.statements = append(p.statements, statement)
	return int64(901), nil
}

func TestDBQueryCarriesSuppliedComponentsIntoFreshAndClonedQueries(t *testing.T) {
	grammar := querybuilder.NewBaseGrammar("app_")
	processor := &suppliedInsertProcessor{BaseProcessor: &processors.BaseProcessor{}}
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: processor, Dialect: baseTestDialect{}}
	query := NewQuery(context.Background(), nil, nil, grammar, "mysql", nil, "users", nil, components)
	for _, child := range []*Query{query, query.Clone().(*Query), query.NewQuery().From("users").(*Query)} {
		id, err := child.InsertGetID(map[string]any{"name": "alice"})
		require.NoError(t, err)
		assert.Equal(t, int64(901), id)
		assert.Same(t, processor, child.builder.Processor())
	}
	// The mysql literal rendering (backticks plus the escaped binding) that this test used to assert is
	// covered against the real driver dialect in tests/database/db.

	assert.Equal(t, []string{
		"insert into \"app_users\" (\"name\") values (?)",
		"insert into \"app_users\" (\"name\") values (?)",
		"insert into \"app_users\" (\"name\") values (?)",
	}, processor.statements)
}
