package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// This file pins the ORM query's chunked reads — Chunk, ChunkById, ChunkByIdDesc, Each, EachById,
// Lazy, LazyById — against a stub pool that hands back one queued result set per statement, in
// call order. The package's other recording driver matches results by SQL text, which cannot drive
// a keyset walk: every page of one repeats the same statement shape and differs only in bindings.

// --- fixtures --------------------------------------------------------------------------------

type chunkUser struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`
}

func (chunkUser) TableName() string { return "chunk_users" }

// --- FIFO stub pool --------------------------------------------------------------------------

type chunkStubDriver struct {
	statements []recordedStatement
	queue      []chunkStubResult
}

// chunkStubResult is one queued result set: the columns and rows one statement answers with.
type chunkStubResult struct {
	columns []string
	rows    [][]driver.Value
}

func (d *chunkStubDriver) Open(string) (driver.Conn, error) { return &chunkStubConn{driver: d}, nil }

// queueRows queues one result set — every row it carries is handed back for one statement —
// for the next statement to run.
func (d *chunkStubDriver) queueRows(columns []string, rows ...[]driver.Value) {
	d.queue = append(d.queue, chunkStubResult{columns: columns, rows: rows})
}

// next pops the next queued result set; an empty queue is an empty result.
func (d *chunkStubDriver) next() chunkStubResult {
	if len(d.queue) == 0 {
		return chunkStubResult{}
	}

	result := d.queue[0]
	d.queue = d.queue[1:]

	return result
}

func (d *chunkStubDriver) at(t *testing.T, i int) recordedStatement {
	t.Helper()

	if i >= len(d.statements) {
		t.Fatalf("chunked read executed %d statement(s); want at least %d", len(d.statements), i+1)
	}

	return d.statements[i]
}

func (d *chunkStubDriver) count() int { return len(d.statements) }

type chunkStubConn struct{ driver *chunkStubDriver }

func (c *chunkStubConn) Prepare(query string) (driver.Stmt, error) {
	return &chunkStubStmt{driver: c.driver, query: query}, nil
}
func (c *chunkStubConn) Close() error              { return nil }
func (c *chunkStubConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

type chunkStubStmt struct {
	driver *chunkStubDriver
	query  string
}

func (s *chunkStubStmt) Close() error  { return nil }
func (s *chunkStubStmt) NumInput() int { return -1 }
func (s *chunkStubStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, driver.ErrSkip
}
func (s *chunkStubStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.driver.statements = append(s.driver.statements, recordedStatement{sql: s.query, args: args})

	result := s.driver.next()

	return &chunkStubRows{columns: result.columns, rows: result.rows}, nil
}

type chunkStubRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *chunkStubRows) Columns() []string { return r.columns }
func (r *chunkStubRows) Close() error      { return nil }
func (r *chunkStubRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}

	copy(dest, r.rows[r.index])
	r.index++

	return nil
}

type chunkStubConnector struct{ driver *chunkStubDriver }

func (c chunkStubConnector) Connect(context.Context) (driver.Conn, error) {
	return &chunkStubConn{driver: c.driver}, nil
}
func (c chunkStubConnector) Driver() driver.Driver { return c.driver }

// newChunkQuery builds a query over chunkUser whose statements run against the stub pool, so the
// page SQL a chunked read emits and the rows it hydrates can be exercised with no database. The
// connection names MySQL, which is the grammar the identifiers below are pinned against.
func newChunkQuery(t *testing.T) (*OrmBuilder, *chunkStubDriver) {
	t.Helper()

	return newChunkQueryFor(t, &chunkUser{})
}

// newChunkQueryFor is newChunkQuery over an arbitrary model, so a relation read's pages can be
// walked through the same stub pool.
func newChunkQueryFor(t *testing.T, model any) (*OrmBuilder, *chunkStubDriver) {
	t.Helper()

	stub := &chunkStubDriver{}
	sqlDB := sql.OpenDB(chunkStubConnector{driver: stub})
	t.Cleanup(func() { _ = sqlDB.Close() })

	conditions := Conditions{model: model}

	return newDialectQuery(context.Background(), nil, contractsdatabase.Config{Driver: "mysql"}, sqlDB, nil, nil, nil, nil, &conditions, nil, mysqlTestComponents()), stub
}

// --- tests -----------------------------------------------------------------------------------

// TestChunkPagesModelsWithTheQualifiedKeyOrder pins chunk()'s offset walk: the query carries no
// order, so the model's qualified key orders it ascending (Eloquent's Builder::enforceOrderBy),
// pages arrive at rising offsets, and the walk ends on the short page. Oracle
// (illuminate/database v12.55.1, plain model): `select * from "users" order by "users"."id" asc
// limit 2 offset 0`, then the same at offset 2.
func TestChunkPagesModelsWithTheQualifiedKeyOrder(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(3), "c"})

	var (
		pageNumbers []int
		pages       [][]chunkUser
		users       []chunkUser
	)

	err := query.Chunk(2, &users, func(page int) error {
		pageNumbers = append(pageNumbers, page)
		pages = append(pages, append([]chunkUser(nil), users...))

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []int{1, 2}, pageNumbers)
	require.Len(t, pages, 2)
	assert.Equal(t, uint(1), pages[0][0].ID)
	assert.Equal(t, uint(3), pages[1][0].ID)
	assert.Equal(t, 2, stub.count())
	assert.Equal(t, `select * from "chunk_users" order by "chunk_users"."id" asc limit 2 offset 0`, stub.at(t, 0).sql)
	assert.Equal(t, `select * from "chunk_users" order by "chunk_users"."id" asc limit 2 offset 2`, stub.at(t, 1).sql)
}

// TestChunkWithZeroSizeDoesNoWork pins Laravel's testChunkWithCountZero: chunk(0) short-circuits
// before any statement.
func TestChunkWithZeroSizeDoesNoWork(t *testing.T) {
	query, stub := newChunkQuery(t)

	called := false
	require.NoError(t, query.Chunk(0, &[]chunkUser{}, func(int) error {
		called = true

		return nil
	}))

	assert.False(t, called)
	assert.Equal(t, 0, stub.count())
}

// TestChunkByIdWalksTheKeyset pins chunkById's page SQL, mirroring BuildsQueries::chunkById: page
// one filters on the key being not null, every later page on the previous page's last id, and the
// walk continues through the trailing empty page the way Laravel's loop does (the corpus oracle
// measures four statements for chunkById(1) over three rows). Oracle (plain model):
// `select * from "articles" where "id" is not null order by "id" asc limit 1`, later pages
// `where "id" > ?`.
func TestChunkByIdWalksTheKeyset(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(2), "b"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(3), "c"})
	// The trailing page is the stub's empty default.

	var (
		pageNumbers []int
		ids         []uint
		users       []chunkUser
	)

	err := query.ChunkById(1, &users, func(page int) error {
		pageNumbers = append(pageNumbers, page)
		ids = append(ids, users[0].ID)

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []int{1, 2, 3}, pageNumbers)
	assert.Equal(t, []uint{1, 2, 3}, ids)
	assert.Equal(t, 4, stub.count())
	assert.Equal(t, `select * from "chunk_users" where "id" is not null order by "id" asc limit 1`, stub.at(t, 0).sql)
	assert.Equal(t, `select * from "chunk_users" where "id" > ? order by "id" asc limit 1`, stub.at(t, 1).sql)
	assert.Equal(t, []driver.Value{int64(1)}, stub.at(t, 1).args)
	assert.Equal(t, []driver.Value{int64(2)}, stub.at(t, 2).args)
	assert.Equal(t, []driver.Value{int64(3)}, stub.at(t, 3).args)
}

// TestChunkByIdDescWalksTheKeysetBackwards pins chunkByIdDesc: page one orders descending with the
// not-null guard, later pages compare "< ?".
func TestChunkByIdDescWalksTheKeysetBackwards(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(3), "c"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(2), "b"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"})

	var (
		ids   []uint
		users []chunkUser
	)

	err := query.ChunkByIdDesc(1, &users, func(int) error {
		ids = append(ids, users[0].ID)

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []uint{3, 2, 1}, ids)
	assert.Equal(t, `select * from "chunk_users" where "id" is not null order by "id" desc limit 1`, stub.at(t, 0).sql)
	assert.Equal(t, `select * from "chunk_users" where "id" < ? order by "id" desc limit 1`, stub.at(t, 1).sql)
	assert.Equal(t, []driver.Value{int64(3)}, stub.at(t, 1).args)
}

// TestChunkByIdStopsCleanlyOnTheSentinel pins the stopping condition: errors.DatabaseChunkStopped
// (Go's analogue of Laravel's `return false`) halts the walk, reports no error, and issues no
// further statement.
func TestChunkByIdStopsCleanlyOnTheSentinel(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(2), "b"})

	calls := 0
	err := query.ChunkById(1, &[]chunkUser{}, func(int) error {
		calls++

		return errors.DatabaseChunkStopped
	})

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, stub.count())
}

// TestChunkByIdReportsAMissingAliasColumn pins the RuntimeException lane: when the page's result
// carries no alias column, the walk aborts with DatabaseChunkByIdColumnNotPresent naming it.
func TestChunkByIdReportsAMissingAliasColumn(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"email"}, []driver.Value{"a"})

	err := query.ChunkById(1, &[]chunkUser{}, func(int) error { return nil })

	assert.ErrorIs(t, err, errors.DatabaseChunkByIdColumnNotPresent)
	assert.Equal(t, 1, stub.count())
}

// TestEachVisitsModelsWithPageKeys pins each(): one statement of 1000 rows, and the callback's
// index is the key within the page, exactly the key Laravel's each() passes.
func TestEachVisitsModelsWithPageKeys(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"},
		[]driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"}, []driver.Value{int64(3), "c"})

	var (
		indexes []int
		ids     []uint
		user    chunkUser
	)

	err := query.Each(&user, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, user.ID)

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []int{0, 1, 2}, indexes)
	assert.Equal(t, []uint{1, 2, 3}, ids)
	assert.Equal(t, uint(3), user.ID)
	assert.Equal(t, 1, stub.count())
	assert.Equal(t, `select * from "chunk_users" order by "chunk_users"."id" asc limit 1000 offset 0`, stub.at(t, 0).sql)
}

// TestEachByIdCountsModelsAcrossPages pins eachById(): the index is the model's 0-based ordinal
// across the whole walk — Laravel's ((page-1)*size + key) arithmetic.
func TestEachByIdCountsModelsAcrossPages(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(3), "c"})

	var (
		indexes []int
		ids     []uint
		user    chunkUser
	)

	err := query.EachById(2, &user, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, user.ID)

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []int{0, 1, 2}, indexes)
	assert.Equal(t, []uint{1, 2, 3}, ids)
	assert.Equal(t, 2, stub.count())
	assert.Equal(t, `select * from "chunk_users" where "id" is not null order by "id" asc limit 2`, stub.at(t, 0).sql)
	assert.Equal(t, `select * from "chunk_users" where "id" > ? order by "id" asc limit 2`, stub.at(t, 1).sql)
}

// TestLazyWalksOffsetPages pins lazy(): ascending pages of chunkSize ordered by the model's
// qualified key, the walk ending on the short page.
func TestLazyWalksOffsetPages(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(3), "c"})

	var (
		indexes []int
		ids     []uint
		user    chunkUser
	)

	err := query.Lazy(&user, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, user.ID)

		return nil
	}, 2)
	require.NoError(t, err)

	assert.Equal(t, []int{0, 1, 2}, indexes)
	assert.Equal(t, []uint{1, 2, 3}, ids)
	assert.Equal(t, `select * from "chunk_users" order by "chunk_users"."id" asc limit 2 offset 0`, stub.at(t, 0).sql)
	assert.Equal(t, `select * from "chunk_users" order by "chunk_users"."id" asc limit 2 offset 2`, stub.at(t, 1).sql)
}

// TestLazyRefusesAZeroChunkSize pins orderedLazyById's InvalidArgumentException lane:
// errors.QueryInvalidChunkSize, no statement issued.
func TestLazyRefusesAZeroChunkSize(t *testing.T) {
	query, stub := newChunkQuery(t)

	err := query.Lazy(&chunkUser{}, func(int) error { return nil }, 0)

	assert.ErrorIs(t, err, errors.QueryInvalidChunkSize)
	assert.Equal(t, 0, stub.count())
}

// TestLazyByIdWalksTheKeyset pins lazyById's page sequence, which the corpus oracle measures as
// four statements for lazyById(1) over three rows: not-null page, two keyset pages, and the empty
// trailing page that ends the loop.
func TestLazyByIdWalksTheKeyset(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(2), "b"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(3), "c"})

	var (
		indexes []int
		ids     []uint
		user    chunkUser
	)

	err := query.LazyById(1, &user, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, user.ID)

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []int{0, 1, 2}, indexes)
	assert.Equal(t, []uint{1, 2, 3}, ids)
	assert.Equal(t, 4, stub.count())
	assert.Equal(t, `select * from "chunk_users" where "id" is not null order by "id" asc limit 1`, stub.at(t, 0).sql)
	assert.Equal(t, `select * from "chunk_users" where "id" > ? order by "id" asc limit 1`, stub.at(t, 1).sql)
}

// --- global scopes ---------------------------------------------------------------------------

// chunkScopedUser carries a global scope so the walk's scope folding is pinned: Eloquent applies
// the model's global scopes inside every page read (chunk -> get -> applyScopes). Oracle
// (illuminate/database v12.55.1, sqlite, model with a `where active = 1` scope): a plain read
// compiles `select * from "users" where "active" = ?`; chunk(2) adds the scope before the order
// (`... where "active" = ? order by "users"."id" asc limit 2 offset 0`); chunkById(2) appends it
// after the keyset guard (`where "id" is not null and "active" = ? order by "id" asc limit 2`,
// page two `where "id" > ? and "active" = ?`).
type chunkScopedUser struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`
}

func (chunkScopedUser) TableName() string { return "chunk_users" }

func (chunkScopedUser) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{{
		Name:  "PlainEmail",
		Scope: func(query contractsorm.Query) contractsorm.Query { return query.Where("email", "plain") },
	}}
}

// newChunkScopedQuery is newChunkQuery over chunkScopedUser.
func newChunkScopedQuery(t *testing.T) (*OrmBuilder, *chunkStubDriver) {
	t.Helper()

	stub := &chunkStubDriver{}
	sqlDB := sql.OpenDB(chunkStubConnector{driver: stub})
	t.Cleanup(func() { _ = sqlDB.Close() })

	conditions := Conditions{model: &chunkScopedUser{}}

	return newDialectQuery(context.Background(), nil, contractsdatabase.Config{Driver: "mysql"}, sqlDB, nil, nil, nil, nil, &conditions, nil, mysqlTestComponents()), stub
}

// TestChunkAppliesTheModelsGlobalScopes pins the model's global scope on every chunked read: the
// scope's where and binding land on every page, the keyset guard first and the scope after it.
func TestChunkAppliesTheModelsGlobalScopes(t *testing.T) {
	scopedRow := func(id int64) []driver.Value { return []driver.Value{id, "plain"} }

	t.Run("Chunk", func(t *testing.T) {
		query, stub := newChunkScopedQuery(t)
		stub.queueRows([]string{"id", "email"}, scopedRow(1))

		var users []chunkScopedUser
		require.NoError(t, query.Chunk(2, &users, func(int) error { return nil }))

		require.Equal(t, 1, stub.count())
		assert.Equal(t, `select * from "chunk_users" where "email" = ? order by "chunk_users"."id" asc limit 2 offset 0`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{"plain"}, stub.at(t, 0).args)
	})

	t.Run("ChunkById", func(t *testing.T) {
		query, stub := newChunkScopedQuery(t)
		stub.queueRows([]string{"id", "email"}, scopedRow(1))
		stub.queueRows([]string{"id", "email"}, scopedRow(2))

		pages := 0
		var users []chunkScopedUser
		require.NoError(t, query.ChunkById(1, &users, func(int) error { pages++; return nil }))

		require.Equal(t, 3, stub.count())
		assert.Equal(t, 2, pages)
		assert.Equal(t, `select * from "chunk_users" where "id" is not null and "email" = ? order by "id" asc limit 1`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{"plain"}, stub.at(t, 0).args)
		assert.Equal(t, `select * from "chunk_users" where "id" > ? and "email" = ? order by "id" asc limit 1`, stub.at(t, 1).sql)
		assert.Equal(t, []driver.Value{int64(1), "plain"}, stub.at(t, 1).args)
	})

	t.Run("Each", func(t *testing.T) {
		query, stub := newChunkScopedQuery(t)
		stub.queueRows([]string{"id", "email"}, scopedRow(1))

		user := chunkScopedUser{}
		require.NoError(t, query.Each(&user, func(int) error { return nil }))

		require.Equal(t, 1, stub.count())
		assert.Equal(t, `select * from "chunk_users" where "email" = ? order by "chunk_users"."id" asc limit 1000 offset 0`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{"plain"}, stub.at(t, 0).args)
	})

	t.Run("EachById", func(t *testing.T) {
		query, stub := newChunkScopedQuery(t)
		stub.queueRows([]string{"id", "email"}, scopedRow(1))

		user := chunkScopedUser{}
		require.NoError(t, query.EachById(2, &user, func(int) error { return nil }))

		require.Equal(t, 1, stub.count())
		assert.Equal(t, `select * from "chunk_users" where "id" is not null and "email" = ? order by "id" asc limit 2`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{"plain"}, stub.at(t, 0).args)
	})

	t.Run("Lazy", func(t *testing.T) {
		query, stub := newChunkScopedQuery(t)
		stub.queueRows([]string{"id", "email"}, scopedRow(1))

		user := chunkScopedUser{}
		require.NoError(t, query.Lazy(&user, func(int) error { return nil }, 2))

		require.Equal(t, 1, stub.count())
		assert.Equal(t, `select * from "chunk_users" where "email" = ? order by "chunk_users"."id" asc limit 2 offset 0`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{"plain"}, stub.at(t, 0).args)
	})

	t.Run("LazyById", func(t *testing.T) {
		query, stub := newChunkScopedQuery(t)
		stub.queueRows([]string{"id", "email"}, scopedRow(1))

		user := chunkScopedUser{}
		require.NoError(t, query.LazyById(2, &user, func(int) error { return nil }))

		require.Equal(t, 1, stub.count())
		assert.Equal(t, `select * from "chunk_users" where "id" is not null and "email" = ? order by "id" asc limit 2`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{"plain"}, stub.at(t, 0).args)
	})
}

// --- caller-supplied keyset columns ------------------------------------------------------------
//
// The alias a caller-supplied column is read back under — orderedChunkById's `$alias ??= $column`,
// and the relation overrides' related-key-name default — pinned against the oracle
// (illuminate/database v12.55.1, executed against sqlite; probe
// .claude/laravel-oracle/_chunk_alias_probe.php).

// TestChunkByIdKeepsACallerQualifiedColumnVerbatim pins the plain builder's alias: orderedChunkById
// aliases the caller's column to itself, verbatim (`$column ??= $this->defaultKeyName(); $alias ??=
// $column`), never to its unqualified name — so a qualified column cannot be read from a plain
// `select *` result. Oracle: chunkById(1, cb, 'articles.id') issues the page below and then throws
// RuntimeException "... the [articles.id] column is not present in the query result." The port
// reads the same page back (chunk_users for articles) and reports the same missing alias.
func TestChunkByIdKeepsACallerQualifiedColumnVerbatim(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"})

	err := query.ChunkById(1, &[]chunkUser{}, func(int) error { return nil }, "chunk_users.id")

	assert.ErrorIs(t, err, errors.DatabaseChunkByIdColumnNotPresent)
	assert.ErrorContains(t, err, "chunk_users.id")
	assert.Equal(t, 1, stub.count())
	assert.Equal(t, `select * from "chunk_users" where "chunk_users"."id" is not null order by "chunk_users"."id" asc limit 1`, stub.at(t, 0).sql)
}

// TestChunkByIdWalksACallerQualifiedColumnUnderAnExplicitAlias pins the other half of the plain
// builder's rule: a caller-supplied alias wins and drives the keyset, while the qualified column
// itself orders and filters every page. Oracle: chunkById(1, cb, 'articles.id', 'id') over three
// rows issues the not-null page, then `where "articles"."id" > ?` binding [1], [2], [3].
func TestChunkByIdWalksACallerQualifiedColumnUnderAnExplicitAlias(t *testing.T) {
	query, stub := newChunkQuery(t)
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(1), "a"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(2), "b"})
	stub.queueRows([]string{"id", "email"}, []driver.Value{int64(3), "c"})

	var (
		ids   []uint
		users []chunkUser
	)
	err := query.ChunkById(1, &users, func(int) error {
		ids = append(ids, users[0].ID)

		return nil
	}, "chunk_users.id", "id")
	require.NoError(t, err)

	assert.Equal(t, []uint{1, 2, 3}, ids)
	assert.Equal(t, 4, stub.count())
	assert.Equal(t, `select * from "chunk_users" where "chunk_users"."id" is not null order by "chunk_users"."id" asc limit 1`, stub.at(t, 0).sql)
	assert.Equal(t, `select * from "chunk_users" where "chunk_users"."id" > ? order by "chunk_users"."id" asc limit 1`, stub.at(t, 1).sql)
	assert.Equal(t, []driver.Value{int64(1)}, stub.at(t, 1).args)
	assert.Equal(t, []driver.Value{int64(2)}, stub.at(t, 2).args)
	assert.Equal(t, []driver.Value{int64(3)}, stub.at(t, 3).args)
}

// TestChunkByIdKeepsTheRelatedKeyAliasForACallerColumn pins the relation overrides: a caller
// column is walked verbatim, but the page's last row is read back under the related model's key
// name — the alias the override defaults even when a column is passed. Oracle (BelongsToMany):
// $user->articles()->chunkById(1, cb, 'article_user.user_id') binds the next page [1, 3] — the
// article id read under the related key name, not a value of the caller's column. Oracle
// (HasOneOrManyThrough): $country->posts()->chunkById(1, cb, 'users.country_id') binds [2, 2], the
// post id.
func TestChunkByIdKeepsTheRelatedKeyAliasForACallerColumn(t *testing.T) {
	t.Run("many-to-many", func(t *testing.T) {
		query, stub := newChunkQueryFor(t, &prUser{})
		stub.queueRows([]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
			[]driver.Value{int64(7), "abigail", int64(1), int64(7), int64(1)})

		var ids []uint
		friends := []prUser{}
		err := query.Related(&prUser{ID: 1}, "Friends").ChunkById(1, &friends, func(int) error {
			for index := range friends {
				ids = append(ids, friends[index].ID)
			}

			return nil
		}, "pr_friends.user_id")
		require.NoError(t, err)

		assert.Equal(t, []uint{7}, ids)
		require.Equal(t, 2, stub.count())
		assert.Equal(t, `select "pr_users".*, "pr_friends"."user_id" as "pivot_user_id", "pr_friends"."friend_id" as "pivot_friend_id", "pr_friends"."friend_level_id" as "pivot_friend_level_id" from "pr_users" inner join "pr_friends" ON "pr_friends"."friend_id" = "pr_users"."id" where "pr_friends"."user_id" = ? and "pr_friends"."user_id" is not null order by "pr_friends"."user_id" asc limit 1`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{int64(1)}, stub.at(t, 0).args)
		assert.Equal(t, `select "pr_users".*, "pr_friends"."user_id" as "pivot_user_id", "pr_friends"."friend_id" as "pivot_friend_id", "pr_friends"."friend_level_id" as "pivot_friend_level_id" from "pr_users" inner join "pr_friends" ON "pr_friends"."friend_id" = "pr_users"."id" where "pr_friends"."user_id" = ? and "pr_friends"."user_id" > ? order by "pr_friends"."user_id" asc limit 1`, stub.at(t, 1).sql)
		assert.Equal(t, []driver.Value{int64(1), int64(7)}, stub.at(t, 1).args)
	})

	t.Run("through", func(t *testing.T) {
		query, stub := newChunkQueryFor(t, &relCountry{})
		stub.queueRows([]string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
			[]driver.Value{int64(7), "A title", int64(1), int64(1), int64(1)})

		var ids []uint
		posts := []relPost{}
		err := query.Related(&relCountry{ID: 1}, "Posts").ChunkById(1, &posts, func(int) error {
			for index := range posts {
				ids = append(ids, posts[index].ID)
			}

			return nil
		}, "rel_users.rel_country_id")
		require.NoError(t, err)

		assert.Equal(t, []uint{7}, ids)
		require.Equal(t, 2, stub.count())
		assert.Equal(t, `select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ? and "rel_users"."rel_country_id" is not null order by "rel_users"."rel_country_id" asc limit 1`, stub.at(t, 0).sql)
		assert.Equal(t, []driver.Value{int64(1)}, stub.at(t, 0).args)
		assert.Equal(t, `select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ? and "rel_users"."rel_country_id" > ? order by "rel_users"."rel_country_id" asc limit 1`, stub.at(t, 1).sql)
		assert.Equal(t, []driver.Value{int64(1), int64(7)}, stub.at(t, 1).args)
	})
}

// TestChunkByIdDoesNotReadPivotPrefixedAliases pins the alias's read source: Laravel reads the
// page's last id with data_get() on the hydrated model, and a many-to-many read has already
// migrated every pivot_-prefixed attribute off that model (BelongsToMany::hydratePivotRelation
// and its migratePivotAttributes, which unset them), so a caller alias naming one is not present
// and the walk aborts. Oracle (BelongsToMany, illuminate/database v12.55.1, executed against
// sqlite): $user->articles()->chunkById(1, cb, 'article_user.user_id', 'pivot_user_id') issues the
// page below once and then throws RuntimeException "... the [pivot_user_id] column is not present
// in the query result."; lazyById with the same arguments throws the same way (the pivot migration
// in BelongsToMany::lazyById's map callback runs before the walk reads the next keyset). A through
// read's laravel_through_key stays readable: HasOneOrManyThrough keeps the attribute on the model,
// and the oracle's chunkById(1, cb, 'users.country_id', 'laravel_through_key') walks two pages
// cleanly.
func TestChunkByIdDoesNotReadPivotPrefixedAliases(t *testing.T) {
	t.Run("chunkById", func(t *testing.T) {
		query, stub := newChunkQueryFor(t, &prUser{})
		stub.queueRows([]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
			[]driver.Value{int64(7), "abigail", int64(1), int64(7), int64(1)})

		friends := []prUser{}
		err := query.Related(&prUser{ID: 1}, "Friends").ChunkById(1, &friends, func(int) error { return nil },
			"pr_friends.user_id", "pivot_user_id")

		assert.ErrorIs(t, err, errors.DatabaseChunkByIdColumnNotPresent)
		assert.ErrorContains(t, err, "pivot_user_id")
		assert.Equal(t, 1, stub.count())
	})

	t.Run("lazyById", func(t *testing.T) {
		query, stub := newChunkQueryFor(t, &prUser{})
		stub.queueRows([]string{"id", "name", "pivot_user_id", "pivot_friend_id", "pivot_friend_level_id"},
			[]driver.Value{int64(7), "abigail", int64(1), int64(7), int64(1)})

		friend := prUser{}
		err := query.Related(&prUser{ID: 1}, "Friends").LazyById(1, &friend, func(int) error { return nil },
			"pr_friends.user_id", "pivot_user_id")

		assert.ErrorIs(t, err, errors.DatabaseChunkByIdColumnNotPresent)
		assert.Equal(t, 1, stub.count())
	})

	t.Run("through alias stays readable", func(t *testing.T) {
		query, stub := newChunkQueryFor(t, &relCountry{})
		stub.queueRows([]string{"id", "title", "user_id", "rel_user_id", "laravel_through_key"},
			[]driver.Value{int64(7), "A title", int64(1), int64(1), int64(1)})

		posts := []relPost{}
		err := query.Related(&relCountry{ID: 1}, "Posts").ChunkById(1, &posts, func(int) error { return nil },
			"rel_users.rel_country_id", "laravel_through_key")

		require.NoError(t, err)
		assert.Equal(t, 2, stub.count())
	})
}
