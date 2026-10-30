package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedriver "github.com/gradii/framework/database/driver"
	databasegorm "github.com/gradii/framework/database/gorm"
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlserver"
)

// This file keeps the ORM-level SQL the framework root can no longer compile. The root now runs on the
// base grammar alone (it no longer depends on the driver modules), so a statement a driver compiles
// differently — a "returning" clause, a JSON operator, a version-dependent json_value — is pinned here
// through the ORM's public API against the real driver grammars. Each case mirrors a root test that used
// the ORM, moved when the drivers left the framework's module.
//
// No database server is needed: statements are previewed with the ORM's ToSql, and the generated-key
// read-back is driven through a recording connection pool.

// dialectUser is the shape a plain table row has, used where a model is only needed to name a table.
type dialectUser struct {
	ID     uint
	Name   string
	Avatar string
}

func (dialectUser) TableName() string { return "users" }

// keyColumnEntity carries a primary key whose column is not `id`, the shape a model with
// `gorm:"primaryKey;column:user_id"` has.
type keyColumnEntity struct {
	ID   uint `gorm:"primaryKey;column:user_id"`
	Name string
}

func (keyColumnEntity) TableName() string { return "insert_key_column_entities" }

// defaultKeyEntity keeps the conventional `id` primary key.
type defaultKeyEntity struct {
	ID   uint `gorm:"primaryKey"`
	Name string
}

func (defaultKeyEntity) TableName() string { return "insert_default_key_entities" }

// stringKeyEntity carries a string primary key, the shape a UUID or ULID model has.
type stringKeyEntity struct {
	ID   string `gorm:"primaryKey;type:char(26)"`
	Name string
}

func (stringKeyEntity) TableName() string { return "insert_key_entities" }

// intKeyEntity carries an auto-increment integer primary key.
type intKeyEntity struct {
	ID   uint `gorm:"primaryKey"`
	Name string
}

func (intKeyEntity) TableName() string { return "int_key_entities" }

// newDialectQuery builds an ORM query over a model on a real driver dialect, optionally backed by a
// recording pool. A nil writer is enough when the statement is only previewed.
func newDialectQuery(t *testing.T, dialect contractsdriver.Dialect, driverName string, writer *sql.DB, model any) *databasegorm.OrmBuilder {
	t.Helper()

	components, err := databasedriver.ResolveQueryComponents(dialect, contractsdriver.GrammarOptions{Driver: driverName})
	require.NoError(t, err)

	conditions := databasegorm.Conditions{}
	query := databasegorm.NewOrmBuilder(context.Background(), nil, contractsdatabase.Config{Driver: driverName},
		writer, nil, nil, nil, nil, &conditions, nil, components)

	return query.Model(model).(*databasegorm.OrmBuilder)
}

// newRecordingDialectQuery builds the same query over a recording pool, so the statements the ORM
// executes can be inspected with no database behind it.
func newRecordingDialectQuery(t *testing.T, dialect contractsdriver.Dialect, driverName string, model any) (*databasegorm.OrmBuilder, *dialectRecorder) {
	t.Helper()

	recorder := &dialectRecorder{}
	sqlDB := sql.OpenDB(dialectConnector{recorder: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })

	return newDialectQuery(t, dialect, driverName, sqlDB, model), recorder
}

// TestInsertReportsTheModelsPrimaryKeyColumn mirrors database/gorm's
// TestCompileInsertReportsTheModelsPrimaryKeyColumn: the key-reporting clause names the model's own
// primary key column. It used to hardcode `id`, so a non-`id` key compiled `output inserted.[id]` and
// SQL Server failed with "Invalid column name 'id'"; PostgreSQL's `returning` carried the same defect.
func TestInsertReportsTheModelsPrimaryKeyColumn(t *testing.T) {
	t.Run("postgres returning names the primary key column", func(t *testing.T) {
		query := newDialectQuery(t, postgres.NewDialect(), postgres.Name, nil, &keyColumnEntity{})
		statement := databasegorm.NewToSql(query, nil, false).Create(&keyColumnEntity{})

		assert.Equal(t, `insert into "insert_key_column_entities" ("name") values ($1) returning "user_id"`, statement)
		assert.NotContains(t, statement, `returning "id"`)
	})

	t.Run("sql server output names the primary key column", func(t *testing.T) {
		query := newDialectQuery(t, sqlserver.NewDialect(), sqlserver.Name, nil, &keyColumnEntity{})
		statement := databasegorm.NewToSql(query, nil, false).Create(&keyColumnEntity{})

		assert.Equal(t, `insert into [insert_key_column_entities] ([name]) output inserted.[user_id] values (@p1)`, statement)
		assert.NotContains(t, statement, "inserted.[id]")
	})

	t.Run("a conventional id primary key is unchanged", func(t *testing.T) {
		postgresQuery := newDialectQuery(t, postgres.NewDialect(), postgres.Name, nil, &defaultKeyEntity{})
		assert.Equal(t, `insert into "insert_default_key_entities" ("name") values ($1) returning "id"`,
			databasegorm.NewToSql(postgresQuery, nil, false).Create(&defaultKeyEntity{}))

		sqlserverQuery := newDialectQuery(t, sqlserver.NewDialect(), sqlserver.Name, nil, &defaultKeyEntity{})
		assert.Equal(t, `insert into [insert_default_key_entities] ([name]) output inserted.[id] values (@p1)`,
			databasegorm.NewToSql(sqlserverQuery, nil, false).Create(&defaultKeyEntity{}))
	})

	t.Run("a query naming no model keeps the default key", func(t *testing.T) {
		// No model, so no key column is known and Laravel's default "id" stands.
		query := newDialectQuery(t, postgres.NewDialect(), postgres.Name, nil, &defaultKeyEntity{}).Table("insert_default_key_entities").(*databasegorm.OrmBuilder)
		statement := databasegorm.NewToSql(query, nil, false).Create(map[string]any{"name": "x"})

		assert.Contains(t, statement, `returning "id"`)
	})
}

// TestCreateReadsTheGeneratedKeyByItsColumnType mirrors database/gorm's
// TestInsertRowReadsAStringKeyByItsColumnType / TestInsertRowReadsAnIntegerKeyByItsColumnType: the key
// read-back builds its scan target from the primary key column's own Go type, so a ULID stays a string
// and an integer key stays the model's integer type. Both read the key through the dialect's `returning`
// clause, which the base grammar does not emit.
func TestCreateReadsTheGeneratedKeyByItsColumnType(t *testing.T) {
	t.Run("a string key comes back as a string", func(t *testing.T) {
		query, recorder := newRecordingDialectQuery(t, postgres.NewDialect(), postgres.Name, &stringKeyEntity{})
		recorder.returnRows("returning", []string{"id"}, []driver.Value{"01AN4Z07BY79KA1307SR9X4MV3"})

		entity := stringKeyEntity{Name: "ulid"}
		require.NoError(t, query.Create(&entity))
		assert.Equal(t, "01AN4Z07BY79KA1307SR9X4MV3", entity.ID, "a string key must come back as a string")
		assert.Equal(t, `insert into "insert_key_entities" ("name") values ($1) returning "id"`, recorder.last(t))
	})

	t.Run("an integer key comes back as the model's integer type", func(t *testing.T) {
		query, recorder := newRecordingDialectQuery(t, postgres.NewDialect(), postgres.Name, &intKeyEntity{})
		recorder.returnRows("returning", []string{"id"}, []driver.Value{int64(42)})

		entity := intKeyEntity{Name: "user"}
		require.NoError(t, query.Create(&entity))
		assert.Equal(t, uint(42), entity.ID)
	})
}

// TestCreateWithARawSelectPermitsOmit mirrors database/gorm's TestCreateAndSaveSelectOmitConflict: a raw
// select is not a column list, so it does not conflict with Omit, and the omitted column never reaches
// the insert. The dialect's returning clause is what reads the generated key back.
func TestCreateWithARawSelectPermitsOmit(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(contractsorm.Query, any) error
	}{
		{"Create", func(q contractsorm.Query, value any) error { return q.Create(value) }},
		{"Save", func(q contractsorm.Query, value any) error { return q.Save(value) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			query, recorder := newRecordingDialectQuery(t, postgres.NewDialect(), postgres.Name, &dialectUser{})
			recorder.returnRows("returning", []string{"id"}, []driver.Value{int64(1)})

			value := &dialectUser{Name: "kept", Avatar: "omitted"}
			require.NoError(t, operation.run(query.SelectRaw("count(*) as total").Omit("avatar"), value))
			assert.Equal(t, uint(1), value.ID)
			assert.Equal(t, `insert into "users" ("name") values ($1) returning "id"`, recorder.last(t))
			assert.Equal(t, []string{"kept"}, recorder.lastArgs(t))
		})
	}
}

// TestOrWhereJsonLengthKeepsTheOrConnector mirrors database/gorm's
// TestCompileSelect_OrConnectorSurvivesEveryDispatchBranch json_length case: the JSON-length dispatch
// used the builder's plain WhereJsonLength, which is hard-wired to "and", so an OrWhereJsonLength
// rendered "and" and matched nothing. It only renders on a dialect that supports the operation, so the
// real Postgres grammar is needed to observe the "or".
func TestOrWhereJsonLengthKeepsTheOrConnector(t *testing.T) {
	query, recorder := newRecordingDialectQuery(t, postgres.NewDialect(), postgres.Name, &dialectUser{})

	var users []dialectUser
	require.NoError(t, query.Where("name", "John").OrWhereJsonLength("tags", 2).Get(&users))

	assert.Contains(t, recorder.last(t), `"name" = $1 or jsonb_array_length`)
}

type versionContextKey struct{}

// TestWithContextCompilesAgainstTheBranchesServerVersion mirrors database/gorm's
// TestORMWithContextRebindsVersionAndKeepsSuppliedProcessor: a branch taken with WithContext re-derives
// its compiler from the branch's own context, so a version-dependent grammar compiles against that
// context's server version. The MySQL grammar renders json_value for the version MariaDB reports and
// json_unquote(json_extract(...)) otherwise; the root keeps only the processor-identity half of that
// test, since it runs on the base grammar.
func TestWithContextCompilesAgainstTheBranchesServerVersion(t *testing.T) {
	sourceContext := context.WithValue(context.Background(), versionContextKey{}, "8.0.32")
	mariaContext := context.WithValue(context.Background(), versionContextKey{}, "10.11.8-MariaDB")
	versionSource := func(ctx context.Context) (string, error) { return ctx.Value(versionContextKey{}).(string), nil }

	options := contractsdriver.GrammarOptions{Driver: mysql.Name, Prefix: "app_", ServerVersion: func() (string, error) { return versionSource(sourceContext) }}
	components, err := databasedriver.ResolveQueryComponents(mysql.NewDialect(), options)
	require.NoError(t, err)
	components.VersionSource = versionSource

	query := databasegorm.NewOrmBuilder(sourceContext, nil, contractsdatabase.Config{Driver: mysql.Name, Prefix: "app_"},
		nil, nil, nil, nil, nil, nil, nil, components).Table("users").Select("data->language").(*databasegorm.OrmBuilder)

	child := query.WithContext(mariaContext).(*databasegorm.OrmBuilder)
	for _, current := range []*databasegorm.OrmBuilder{child, child.Clone().(*databasegorm.OrmBuilder)} {
		assert.Equal(t, "select json_value(`data`, '$.\"language\"') from `app_users`", renderSelect(current))
	}

	// The receiver keeps compiling against the version it was built with.
	assert.Equal(t, "select json_unquote(json_extract(`data`, '$.\"language\"')) from `app_users`", renderSelect(query))
}

func renderSelect(query *databasegorm.OrmBuilder) string {
	return databasegorm.NewToSql(query, nil, false).Find(&[]dialectUser{})
}

// --- recording pool --------------------------------------------------------

// dialectRecorder is a database/sql driver that records every statement the ORM executes and replays a
// canned result set for the ones a test asks it to, so the key read-back can be driven without a
// database. It is the corpus's own copy of the framework root's recording double.
type dialectRecorder struct {
	mu     sync.Mutex
	stmts  []recordedStatement
	canned []cannedResult
}

type recordedStatement struct {
	sql  string
	args []driver.Value
}

type cannedResult struct {
	match string
	cols  []string
	rows  [][]driver.Value
}

func (d *dialectRecorder) Open(string) (driver.Conn, error) { return &dialectConn{recorder: d}, nil }

func (d *dialectRecorder) record(statement string, args []driver.Value) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.stmts = append(d.stmts, recordedStatement{sql: statement, args: args})
}

func (d *dialectRecorder) last(t *testing.T) string {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	require.NotEmpty(t, d.stmts, "the ORM executed no statement")

	return d.stmts[len(d.stmts)-1].sql
}

func (d *dialectRecorder) lastArgs(t *testing.T) []string {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	require.NotEmpty(t, d.stmts, "the ORM executed no statement")

	args := make([]string, 0, len(d.stmts[len(d.stmts)-1].args))
	for _, arg := range d.stmts[len(d.stmts)-1].args {
		args = append(args, arg.(string))
	}

	return args
}

// returnRows makes the driver hand back rows for any later statement whose SQL contains match.
func (d *dialectRecorder) returnRows(match string, columns []string, rows ...[]driver.Value) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.canned = append(d.canned, cannedResult{match: match, cols: columns, rows: rows})
}

func (d *dialectRecorder) rowsFor(statement string) driver.Rows {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, c := range d.canned {
		if strings.Contains(statement, c.match) {
			return &cannedRows{cols: c.cols, rows: c.rows}
		}
	}

	return &cannedRows{}
}

type dialectConn struct{ recorder *dialectRecorder }

func (c *dialectConn) Prepare(statement string) (driver.Stmt, error) {
	return &dialectStmt{recorder: c.recorder, statement: statement}, nil
}
func (c *dialectConn) Close() error              { return nil }
func (c *dialectConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

type dialectStmt struct {
	recorder  *dialectRecorder
	statement string
}

func (s *dialectStmt) Close() error  { return nil }
func (s *dialectStmt) NumInput() int { return -1 }
func (s *dialectStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.recorder.record(s.statement, args)

	return dialectResult{}, nil
}
func (s *dialectStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.recorder.record(s.statement, args)

	return s.recorder.rowsFor(s.statement), nil
}

// dialectResult answers LastInsertId so an insert that reads its key from the driver still completes;
// the drivers pinned here report the key through a returning clause, not through the driver.
type dialectResult struct{}

func (dialectResult) LastInsertId() (int64, error) { return 0, nil }
func (dialectResult) RowsAffected() (int64, error) { return 0, nil }

type cannedRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *cannedRows) Columns() []string { return r.cols }
func (r *cannedRows) Close() error      { return nil }
func (r *cannedRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}

	copy(dest, r.rows[r.i])
	r.i++

	return nil
}

type dialectConnector struct{ recorder *dialectRecorder }

func (c dialectConnector) Connect(context.Context) (driver.Conn, error) {
	return &dialectConn{recorder: c.recorder}, nil
}
func (c dialectConnector) Driver() driver.Driver { return c.recorder }
