package db

import (
	"context"
	"testing"

	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsquery "github.com/gradii/framework/contracts/database/query"
	"github.com/gradii/framework/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/gradii/framework/database/query/processors"
	mocksdb "github.com/gradii/framework/mocks/database/db"
	mockslogger "github.com/gradii/framework/mocks/database/logger"
	"github.com/gradii/framework/support/carbon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// dialectResult is a database/sql.Result with a scripted row count.
type dialectResult struct{ rowsAffected int64 }

func (r dialectResult) LastInsertId() (int64, error) { return 0, nil }

func (r dialectResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

// TestDialectCompiledJSONWheres pins the JSON where clauses against the real MySQL dialect. The base
// grammar refuses JSON, so these rows cannot be asserted on the framework grammar in database/db.
func TestDialectCompiledJSONWheres(t *testing.T) {
	for _, test := range []struct {
		name  string
		build func(contractsdb.Query) contractsdb.Query
		want  string
	}{
		{"where json contains", func(q contractsdb.Query) contractsdb.Query { return q.WhereJsonContains("data", "x") }, "select * from `users` where json_contains(`data`, ?)"},
		{"where json contains key", func(q contractsdb.Query) contractsdb.Query { return q.WhereJsonContainsKey("data") }, "select * from `users` where ifnull(json_contains_path(`data`, 'one'), 0)"},
		{"where json length", func(q contractsdb.Query) contractsdb.Query { return q.WhereJsonLength("data", 2) }, "select * from `users` where json_length(`data`) = ?"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.build(dialectQuery(t, "mysql", "users")).ToSql().Get())
		})
	}
}

// TestDialectToRawSql pins that the facade renders a bound statement as a raw literal string with the
// dialect's quoting and escaping.
func TestDialectToRawSql(t *testing.T) {
	query := dialectQuery(t, "mysql", "users")
	assert.Equal(t, "select * from `users` where `name` = 'John'", query.Clone().Where("name", "John").ToRawSql().Get())
}

// TestDialectInsertGetIDSqlServer pins SQL Server's insert path: the generated key is read with an
// OUTPUT clause through the writer's GetContext, not with LastInsertId.
func TestDialectInsertGetIDSqlServer(t *testing.T) {
	ctx := context.Background()
	now := carbon.Now()
	carbon.SetTestNow(now)
	write := mocksdb.NewBuilder(t)
	logger := mockslogger.NewLogger(t)
	const statement = "insert into [users] ([age], [name]) output inserted.[id] values (@p1, @p2)"
	const explained = "insert into [users] ([age], [name]) output inserted.[id] values (25, 'John')"

	write.EXPECT().GetContext(mock.Anything, mock.Anything, statement, 25, "John").Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*(dest.(*int64)) = 17
	}).Return(nil).Once()
	write.EXPECT().Explain(statement, 25, "John").Return(explained).Once()
	logger.EXPECT().Trace(ctx, now, explained, int64(1), nil).Return().Once()

	query := dialectQueryWith(t, "sqlserver", "users", contractsdriver.GrammarOptions{}, nil, write, logger, nil)
	id, err := query.InsertGetID(map[string]any{"name": "John", "age": 25})
	require.NoError(t, err)
	assert.Equal(t, int64(17), id)
}

// TestDialectUpsertStatement pins MySQL's upsert: "on duplicate key update" with the named columns.
func TestDialectUpsertStatement(t *testing.T) {
	ctx := context.Background()
	now := carbon.Now()
	carbon.SetTestNow(now)
	write := mocksdb.NewBuilder(t)
	logger := mockslogger.NewLogger(t)
	const statement = "insert into `users` (`email`, `name`) values (?, ?) on duplicate key update `name` = ?"

	write.EXPECT().ExecContext(ctx, statement, "foo", "Alice", "Bob").Return(dialectResult{rowsAffected: 2}, nil).Once()
	write.EXPECT().Explain(statement, "foo", "Alice", "Bob").Return(statement).Once()
	logger.EXPECT().Trace(ctx, now, statement, int64(2), nil).Return().Once()

	query := dialectQueryWith(t, "mysql", "users", contractsdriver.GrammarOptions{}, nil, write, logger, nil)
	got, err := query.Upsert(map[string]any{"name": "Alice", "email": "foo"}, []string{"email"}, map[string]any{"name": "Bob"})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.RowsAffected)

	// Empty input short-circuits before the grammar compiles anything.
	empty, err := query.Upsert([]map[string]any{}, []string{"id"})
	require.NoError(t, err)
	require.Zero(t, empty.RowsAffected)
	empty, err = query.InsertOrIgnore(map[string]any{})
	require.NoError(t, err)
	require.Zero(t, empty.RowsAffected)
}

// recordingInsertProcessor captures the INSERT statements the connection routes through the
// processor that was supplied with the components.
type recordingInsertProcessor struct {
	*processors.BaseProcessor
	statements []string
}

func (p *recordingInsertProcessor) ProcessInsertGetID(_ contractsquery.Connection, statement string, _ []any, _ string) (any, error) {
	p.statements = append(p.statements, statement)
	return int64(901), nil
}

// TestDBQueryCarriesSuppliedComponentsIntoFreshAndClonedQueries pins that a query created by the
// facade keeps the components its connection supplied - grammar, processor and dialect - across a
// clone and a fresh nested query, so the dialect's INSERT shape and literal escaping both survive.
func TestDBQueryCarriesSuppliedComponentsIntoFreshAndClonedQueries(t *testing.T) {
	processor := &recordingInsertProcessor{BaseProcessor: &processors.BaseProcessor{}}
	resolved := dialect("mysql")
	options := contractsdriver.GrammarOptions{Driver: "mysql", Prefix: "app_"}
	grammar := resolved.NewQueryGrammar(options)
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: processor, Dialect: resolved, Options: options}
	query := db.NewQuery(context.Background(), nil, nil, grammar, "mysql", nil, "users", nil, components)

	for _, child := range []*db.Query{query, query.Clone().(*db.Query), query.NewQuery().From("users").(*db.Query)} {
		id, err := child.InsertGetID(map[string]any{"name": "alice"})
		require.NoError(t, err)
		assert.Equal(t, int64(901), id)
		assert.Equal(t, "select * from `app_users` where `name` = 'a\\\\b''c'", child.Where("name", `a\b'c`).ToRawSql().Get())
	}
	assert.Equal(t, []string{
		"insert into `app_users` (`name`) values (?)",
		"insert into `app_users` (`name`) values (?)",
		"insert into `app_users` (`name`) values (?)",
	}, processor.statements)
}

// TestDialectUpdateOrInsertUsesLimit pins the update path of UpdateOrInsert against MySQL: an
// existing row is updated with the "limit 1" clause MySQL's grammar adds to updates, which the base
// grammar does not render.
func TestDialectUpdateOrInsertUsesLimit(t *testing.T) {
	ctx := context.Background()
	now := carbon.Now()
	carbon.SetTestNow(now)
	read := mocksdb.NewBuilder(t)
	write := mocksdb.NewBuilder(t)
	logger := mockslogger.NewLogger(t)
	const exists = "select exists(select * from `users` where `id` = ?) as `exists`"
	const statement = "update `users` set `phone` = ? where `id` = ? limit 1"
	const explained = "update `users` set `phone` = '123' where `id` = 1 limit 1"

	read.EXPECT().GetContext(mock.Anything, mock.Anything, exists, 1).Run(func(_ context.Context, dest any, _ string, _ ...any) {
		*(dest.(*bool)) = true
	}).Return(nil).Once()
	read.EXPECT().Explain(exists, 1).Return(exists).Once()
	logger.EXPECT().Trace(ctx, now, exists, int64(-1), nil).Return().Once()

	write.EXPECT().ExecContext(mock.Anything, statement, "123", 1).Return(dialectResult{rowsAffected: 1}, nil).Once()
	write.EXPECT().Explain(statement, "123", 1).Return(explained).Once()
	logger.EXPECT().Trace(ctx, now, explained, int64(1), nil).Return().Once()

	query := dialectQueryWith(t, "mysql", "users", contractsdriver.GrammarOptions{}, read, write, logger, nil)
	got, err := query.UpdateOrInsert(map[string]any{"id": 1}, map[string]any{"phone": "123"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.RowsAffected)
}
