package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/database/query/processors"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type insertIDProcessorSpy struct {
	processors.BaseProcessor
	calls     int
	statement string
	values    []any
	sequence  string
}

func (p *insertIDProcessorSpy) ProcessInsertGetID(connection processors.Connection, statement string, values []any, sequence string) (any, error) {
	p.calls++
	p.statement, p.values, p.sequence = statement, values, sequence
	return int64(73), nil
}

func TestInsertGetIDCompilesBeforeProcessing(t *testing.T) {
	processor := &insertIDProcessorSpy{}
	base := NewBuilder(NewMySqlGrammar(""), processor)
	for _, builder := range []*Builder{base.NewQuery(), base.Clone()} {
		builder.From("original", "").BeforeQuery(func(b *Builder) *Builder {
			return b.From("users", "")
		})
		id, err := builder.InsertGetID(nil, core.InsertValues{{Column: "name", Value: "alice"}}, "user_id")
		require.NoError(t, err)
		assert.Equal(t, int64(73), id)
		assert.Equal(t, "insert into `users` (`name`) values (?)", processor.statement)
		assert.Equal(t, []any{"alice"}, processor.values)
		assert.Equal(t, "user_id", processor.sequence)
	}
	assert.Equal(t, 2, processor.calls)
}

func TestInsertGetIDRejectsBuilderErrorsBeforeProcessing(t *testing.T) {
	processor := &insertIDProcessorSpy{}
	builder := NewBuilder(NewMySqlGrammar(""), processor).From("users", "")
	builder.WhereIn("id", nil, core.BooleanAnd, false)
	_, err := builder.InsertGetID(nil, []map[string]any{{"name": "alice"}}, "")
	assert.ErrorIs(t, err, errors.QueryNilValueListNotAllowed)
	assert.Zero(t, processor.calls)
}

func TestNewProcessorSelectsTheConnectionDialect(t *testing.T) {
	for _, test := range []struct {
		driver string
		want   Processor
	}{
		{"MySQL", &processors.MySqlProcessor{}},
		{"Maria-DB", &processors.MariaDbProcessor{}},
		{"pgx", &processors.PostgresProcessor{}},
		{"SQLite3", &processors.SqliteProcessor{}},
		{"SQL Server", &processors.SqlServerProcessor{}},
		{"custom", &processors.BaseProcessor{}},
	} {
		t.Run(test.driver, func(t *testing.T) {
			assert.IsType(t, test.want, NewProcessor(test.driver))
		})
	}
}

func TestSqlServerInsertIDClauseDoesNotParseSQLText(t *testing.T) {
	builder := NewBuilder(NewSqlServerGrammar("")).From("table values archive", "")
	statement, bindings := builder.ToInsertGetIdSQL(core.InsertValues{
		{Column: "name", Value: Raw("' returning output inserted. '")},
		{Column: "value", Value: 7},
	}, "user_id")
	assert.Equal(t, "insert into [table values archive] ([name], [value]) output inserted.[user_id] values (' returning output inserted. ', ?)", statement)
	assert.Equal(t, []any{7}, bindings)
}
