// Package relations_test drives the relation-object base semantics through a real
// *gorm.OrmBuilder: the models, the relations and the statements are the production objects, and
// the only stand-in is the connection transport — a recording database/sql driver that captures
// every statement (and answers reads with an empty result set, the relation-miss shape the
// default materialisation is about).
package relations_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedriver "github.com/gradii/framework/database/driver"
	databasegorm "github.com/gradii/framework/database/gorm"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/support/carbon"
)

// --- fixtures --------------------------------------------------------------------------------------

type sqlUser struct {
	ID        uint                `gorm:"column:id;primaryKey"`
	Name      string              `gorm:"column:name"`
	UpdatedAt *carbon.DateTime    `gorm:"autoUpdateTime;column:updated_at"`
	Books     []*sqlBook          `gorm:"-"`
	Profile   *sqlProfile         `gorm:"-"`
	Stampless []*sqlStamplessBook `gorm:"-"`
}

func (sqlUser) TableName() string { return "sql_users" }

func (sqlUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books":     contractsorm.HasMany{Related: &sqlBook{}, ForeignKey: "user_id"},
		"Profile":   contractsorm.HasOne{Related: &sqlProfile{}, ForeignKey: "user_id", WithDefault: map[string]any{"bio": "fallback"}},
		"Stampless": contractsorm.HasMany{Related: &sqlStamplessBook{}, ForeignKey: "user_id"},
	}
}

type sqlBook struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	UserID    uint             `gorm:"column:user_id"`
	AuthorID  *uint64          `gorm:"column:author_id"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
	Author    *sqlAuthor       `gorm:"-"`
}

func (sqlBook) TableName() string { return "sql_books" }

func (sqlBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &sqlAuthor{}, ForeignKey: "author_id"},
	}
}

type sqlAuthor struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (sqlAuthor) TableName() string { return "sql_authors" }

type sqlProfile struct {
	ID     uint   `gorm:"column:id;primaryKey"`
	UserID uint   `gorm:"column:user_id"`
	Bio    string `gorm:"column:bio"`
}

func (sqlProfile) TableName() string { return "sql_profiles" }

// sqlStamplessBook declares no updated-at column — the class shape that ignores touches.
type sqlStamplessBook struct {
	ID     uint `gorm:"column:id;primaryKey"`
	UserID uint `gorm:"column:user_id"`
}

func (sqlStamplessBook) TableName() string { return "sql_books" }

// --- recording transport ---------------------------------------------------------------------------

// statementRecorder captures every statement the ORM executes through the recording driver.
type statementRecorder struct {
	mu         sync.Mutex
	statements []recordedStatement
}

type recordedStatement struct {
	query string
	args  []any
}

func (r *statementRecorder) record(query string, args []driver.NamedValue) {
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, arg.Value)
	}

	r.mu.Lock()
	r.statements = append(r.statements, recordedStatement{query: query, args: values})
	r.mu.Unlock()
}

func (r *statementRecorder) take() []recordedStatement {
	r.mu.Lock()
	defer r.mu.Unlock()

	statements := r.statements
	r.statements = nil

	return statements
}

// sqlConnector / recordingConn / emptyRows form the transport: writes report zero affected rows,
// reads answer with an empty result set, and both record the statement.
type sqlConnector struct{ recorder *statementRecorder }

func (c sqlConnector) Connect(context.Context) (driver.Conn, error) {
	return &recordingConn{recorder: c.recorder}, nil
}

func (c sqlConnector) Driver() driver.Driver { return recordingDriver(c) }

type recordingDriver struct{ recorder *statementRecorder }

func (d recordingDriver) Open(string) (driver.Conn, error) {
	return &recordingConn{recorder: d.recorder}, nil
}

type recordingConn struct{ recorder *statementRecorder }

func (c *recordingConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }

func (c *recordingConn) Close() error { return nil }

func (c *recordingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("the recording driver does not support transactions")
}

func (c *recordingConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.recorder.record(query, args)

	return driver.RowsAffected(0), nil
}

func (c *recordingConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.recorder.record(query, args)

	return emptyRows{}, nil
}

func (c *recordingConn) Ping(context.Context) error { return nil }

// emptyRows answers every read with no rows — the relation miss.
type emptyRows struct{}

func (emptyRows) Columns() []string {
	return []string{"id", "user_id", "bio", "name", "author_id", "updated_at"}
}

func (emptyRows) Close() error { return nil }

func (emptyRows) Next([]driver.Value) error { return io.EOF }

// newRelationTestBuilder builds a real *gorm.OrmBuilder on the recording transport: the in-tree
// grammar / processor components keep the builder's construction error empty, and the driver-backed
// *sql.DB is the writer every statement runs on.
func newRelationTestBuilder(t *testing.T) (*databasegorm.OrmBuilder, *statementRecorder) {
	t.Helper()

	recorder := &statementRecorder{}
	db := sql.OpenDB(sqlConnector{recorder: recorder})

	grammar := querybuilder.NewBaseGrammar("")
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: querybuilder.NewProcessor("")}

	builder := databasegorm.NewOrmBuilder(
		context.Background(),
		nil,
		contractsdatabase.Config{Driver: "sqlite"},
		db,
		nil,
		nil,
		nil,
		nil,
		&databasegorm.Conditions{},
		nil,
		components,
	)
	t.Cleanup(func() { _ = db.Close() })

	return builder, recorder
}

// deref unwraps a pointer binding so an assertion sees the key value, whichever shape the read
// produced.
func deref(value any) any {
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Pointer && !reflected.IsNil() {
		return reflected.Elem().Interface()
	}

	return value
}

// --- touch SQL shapes ------------------------------------------------------------------------------

func TestRelationTouch_HasManySQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	rel := builder.Related(&sqlUser{ID: 7}, "Books")
	result, err := rel.Touch()
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.RowsAffected)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle (illuminate/database v12.55.1, sqlite, `$user->books()->touch()`): the relation's
	// constraint keeps its not-null guard, the related model's updated-at column is stamped, and
	// global scopes are dropped.
	assert.Equal(t, `update "sql_books" set "updated_at" = ? where "sql_books"."user_id" = ? and "sql_books"."user_id" is not null`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, 7, deref(statements[0].args[1]))
	stamp, ok := statements[0].args[0].(time.Time)
	require.True(t, ok, "expected a timestamp binding, got %T", statements[0].args[0])
	assert.WithinDuration(t, time.Now(), stamp, time.Minute)
}

func TestRelationTouch_BelongsToSQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	authorID := uint64(9)
	rel := builder.Related(&sqlBook{AuthorID: &authorID}, "Author")
	_, err := rel.Touch()
	require.NoError(t, err)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle: `$book->author()->touch()` -> `update "t_authors" set "updated_at" = ? where
	// "t_authors"."id" = ?` — the qualified owner key BelongsTo::addConstraints installs.
	assert.Equal(t, `update "sql_authors" set "updated_at" = ? where "sql_authors"."id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, 9, deref(statements[0].args[1]))
}

func TestRelationTouch_BelongsToAbsentKeyWritesNothing(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	rel := builder.Related(&sqlBook{}, "Author")
	result, err := rel.Touch()
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.RowsAffected)
	assert.Empty(t, recorder.take(), "BelongsTo::touch() skips the statement for a null key")
}

func TestRelationTouch_IgnoringRelatedWritesNothing(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	rel := builder.Related(&sqlUser{ID: 7}, "Stampless")
	result, err := rel.Touch()
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.RowsAffected)
	assert.Empty(t, recorder.take(), "a related class without timestamps ignores touches")
}

// --- GetResults ------------------------------------------------------------------------------------

func TestRelationGetResults_MissMaterialisesDefault(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	rel := builder.Related(&sqlUser{ID: 7}, "Profile")
	var profile sqlProfile
	require.NoError(t, rel.GetResults(&profile))
	assert.Equal(t, "fallback", profile.Bio)

	statements := recorder.take()
	require.Len(t, statements, 1, "a miss still runs the relation read")
	assert.True(t, strings.HasPrefix(statements[0].query, `select * from "sql_profiles" where `), statements[0].query)
	assert.Contains(t, statements[0].query, `limit 1`)
	require.Len(t, statements[0].args, 1)
	assert.EqualValues(t, 7, deref(statements[0].args[0]))
}

func TestRelationGetResults_AbsentParentKeySkipsTheRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	rel := builder.Related(&sqlUser{}, "Profile")
	var profile sqlProfile
	require.NoError(t, rel.GetResults(&profile))
	assert.Equal(t, "fallback", profile.Bio)
	assert.Empty(t, recorder.take(), "HasOne::getResults short-circuits the read for an absent parent key")
}

// The many-kind statement-level pin: an empty parent key executes the relation read as usual, the
// FK constraint yielding no rows — the alignment of the many/through kinds with Laravel's
// query->get() shape (oracle, illuminate/database v12.55.1, executed against sqlite,
// /tmp/lalign-getresults/getresults.php: hasMany zero key queries=1 with the same constraint; the
// strictly-null key Laravel returns as an empty collection without a query cannot reach a
// value-typed Go key).
func TestRelationGetResults_EmptyParentKeyManyKindsRunTheRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	rel := builder.Related(&sqlUser{}, "Books")
	var books []sqlBook
	require.NoError(t, rel.GetResults(&books))

	statements := recorder.take()
	require.Len(t, statements, 1, "a many-kind read runs its constrained query for an absent parent key")
	assert.Equal(t, `select * from "sql_books" where "sql_books"."user_id" = ? and "sql_books"."user_id" is not null`, statements[0].query)
	require.Len(t, statements[0].args, 1)
	assert.EqualValues(t, 0, deref(statements[0].args[0]), "the empty key binds as its zero value")
	assert.Empty(t, books, "the FK constraint yields no rows for the empty key")
}

// --- Is --------------------------------------------------------------------------------------------

func TestRelationIs_RealBuilder(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	rel := builder.Related(&sqlUser{ID: 7}, "Books")
	assert.True(t, rel.Is(&sqlBook{UserID: 7}), "same table, equal keys")
	assert.False(t, rel.Is(&sqlBook{UserID: 8}), "different key")
	assert.False(t, rel.Is(&sqlBook{UserID: 0}), "an empty model key is no match")
	assert.False(t, rel.Is(nil), "nil never matches")
	assert.True(t, rel.IsNot(&sqlBook{UserID: 8}))

	authorID := uint64(9)
	author := builder.Related(&sqlBook{AuthorID: &authorID}, "Author")
	assert.True(t, author.Is(&sqlAuthor{ID: 9}), "the parent foreign key against the owner key")
	assert.False(t, author.Is(&sqlAuthor{ID: 10}), "different owner key")
}

func TestRelationGetQualifiedParentKeyName_RealBuilder(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	// Oracle (illuminate/database v12.55.1): $user->books()->getQualifiedParentKeyName() prints
	// "p5_users.id" (the parent table + the local key) and $book->author()->... prints
	// "p6_books.id" (the child's own qualified primary key).
	assert.Equal(t, "sql_users.id", builder.Related(&sqlUser{ID: 7}, "Books").GetQualifiedParentKeyName())

	authorID := uint64(9)
	assert.Equal(t, "sql_books.id", builder.Related(&sqlBook{AuthorID: &authorID}, "Author").GetQualifiedParentKeyName())
}
