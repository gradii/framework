package schema

import (
	"github.com/gradii/framework/contracts/database"
	"github.com/gradii/framework/contracts/database/driver"
	contractsquery "github.com/gradii/framework/contracts/database/query"
	"github.com/gradii/framework/database/query/processors"
	"github.com/gradii/framework/errors"
	mocksdb "github.com/gradii/framework/mocks/database/db"
	mocksorm "github.com/gradii/framework/mocks/database/orm"
	"github.com/stretchr/testify/require"
	"testing"
)

type boundSchemaGrammar struct{ driver.Grammar }

func (boundSchemaGrammar) GetAttributeCommands() []string { return nil }
func (boundSchemaGrammar) CompileSchemaCommand(driver.Blueprint, *driver.Command) ([]driver.SchemaStatement, bool, error) {
	return []driver.SchemaStatement{{SQL: "select ?", Bindings: []any{"schema value"}}}, true, nil
}
func (boundSchemaGrammar) CompileSchemaStartingValues(driver.Blueprint) ([]driver.SchemaStatement, error) {
	return nil, nil
}
func TestSchemaCompilerPreservesBindings(t *testing.T) {
	blueprint := NewBlueprint(nil, "", "items")
	blueprint.Create()
	statements, err := blueprint.CompileStatements(boundSchemaGrammar{})
	require.NoError(t, err)
	require.Equal(t, []driver.SchemaStatement{{SQL: "select ?", Bindings: []any{"schema value"}}}, statements)
	_, err = blueprint.ToSql(boundSchemaGrammar{})
	require.ErrorIs(t, err, errors.SchemaBindingsRequireExecution)
	query := mocksorm.NewQuery(t)
	query.EXPECT().Exec("select ?", "schema value").Return(nil, nil).Once()
	require.NoError(t, blueprint.Build(query, boundSchemaGrammar{}))
}
func TestColumnDescriptionPreservesOptionalValues(t *testing.T) {
	blueprint := NewBlueprint(nil, "p_", "items")
	blueprint.Temporary()
	blueprint.InnoDb()
	blueprint.Charset("utf8mb4")
	column := blueprint.Binary("digest", 16).Fixed().Nullable(false).StartingValue(0).VirtualAs(nil)
	description := column.(driver.ColumnDescriber).DescribeColumn()
	require.NotNil(t, description.Nullable)
	require.False(t, *description.Nullable)
	require.NotNil(t, description.StartingValue)
	require.Zero(t, *description.StartingValue)
	require.True(t, description.VirtualSet)
	require.Nil(t, description.VirtualAs)
	require.True(t, description.Fixed)
	require.Equal(t, 16, *description.Length)
	*description.Nullable = true
	require.False(t, column.GetNullable())
	table := blueprint.DescribeBlueprint()
	require.True(t, table.Temporary)
	require.Equal(t, "InnoDB", table.Engine)
	require.Equal(t, "utf8mb4", table.Charset)
	require.Equal(t, "p_", table.Prefix)
}

type schemaVersionGrammar struct {
	driver.Grammar
	resolve func() (string, error)
}

func (r *schemaVersionGrammar) SetVersionResolver(resolve func() (string, error)) {
	r.resolve = resolve
}

type injectedSchemaDriver struct {
	driver.Driver
	grammar driver.Grammar
	dialect driver.Dialect
}

func (r injectedSchemaDriver) Grammar() driver.Grammar     { return r.grammar }
func (r injectedSchemaDriver) Dialect() driver.Dialect     { return r.dialect }
func (r injectedSchemaDriver) Processor() driver.Processor { return nil }
func (r injectedSchemaDriver) Pool() database.Pool {
	return database.Pool{Writers: []database.Config{{Driver: "custom", Database: "main", Prefix: "p_"}}}
}

type injectedSchemaDialect struct {
	driver.Dialect
	processor contractsquery.Processor
}

func (r *injectedSchemaDialect) NewProcessor() contractsquery.Processor { return r.processor }

func TestSchemaReceivesConnectionVersionAndDialectProcessor(t *testing.T) {
	grammar := &schemaVersionGrammar{}
	processor := &processors.BaseProcessor{}
	source := injectedSchemaDriver{grammar: grammar, dialect: &injectedSchemaDialect{processor: processor}}
	connection := mocksdb.NewDB(t)
	connection.EXPECT().ServerVersion().Return("5.7.44", nil).Once()
	schema, err := NewSchema(nil, nil, nil, source, nil, connection)
	require.NoError(t, err)
	require.Same(t, processor, schema.queryProcessor)
	require.NotNil(t, grammar.resolve)
	version, err := grammar.resolve()
	require.NoError(t, err)
	require.Equal(t, "5.7.44", version)
}
func TestSchemaRejectsMissingDialectComponents(t *testing.T) {
	var missingDialect *injectedSchemaDialect
	var missingProcessor *processors.BaseProcessor
	var missingGrammar *schemaVersionGrammar
	cases := []injectedSchemaDriver{
		{grammar: &schemaVersionGrammar{}, dialect: missingDialect},
		{grammar: &schemaVersionGrammar{}, dialect: &injectedSchemaDialect{processor: missingProcessor}},
		{grammar: missingGrammar, dialect: &injectedSchemaDialect{processor: &processors.BaseProcessor{}}},
	}
	for _, source := range cases {
		_, err := NewSchema(nil, nil, nil, source, nil)
		require.ErrorIs(t, err, errors.DatabaseDialectComponentsMissing)
	}
}
