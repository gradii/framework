package tests

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/sqlserver"
)

// TestReadsLeaveTheQueryUnchanged pins the rule that a read compiles from a copy: running First,
// Find, FindMany, Paginate, FirstOrNew and the rest - or previewing them through ToSql - leaves the
// query as it was, so what runs on it next reads every matching row. Laravel's own builder keeps
// some of these clauses (first() leaves `limit 1`, find() its key condition, paginate() its limit
// and offset; oracle, illuminate/database v12.55.1,
// /Users/dog/.claude/jobs/013c27a0/tmp/ofa/mutation_probe.php -> mutation_probe.out); the port
// deliberately does not, the "reads leave no state" decision recorded in
// .claude/plans/remove-custom-global-scopes.md (item 3). The statements the reads run are unchanged.
func TestReadsLeaveTheQueryUnchanged(t *testing.T) {
	queries := NewTestQueryBuilder().All("", false)
	if queries[sqlserver.Name] == nil && os.Getenv("GRADII_TEST_SQLSERVER_DSN") != "" {
		queries[sqlserver.Name] = NewTestQueryBuilder().Sqlserver("", false)
	}

	for name, fixture := range queries {
		t.Run(name, func(t *testing.T) {
			fixture.CreateTable(TestTableUsers)
			var ids []any
			for _, user := range []string{"reads_state_a", "reads_state_b", "reads_state_c"} {
				created := &User{Name: user}
				require.NoError(t, fixture.Query().Create(created))
				ids = append(ids, created.ID)
			}

			var (
				user  User
				users []User
				total int64
			)
			for _, read := range []struct {
				name string
				run  func(q contractsorm.Query)
			}{
				{"First", func(q contractsorm.Query) { require.NoError(t, q.First(&user)) }},
				{"FirstOr", func(q contractsorm.Query) { require.NoError(t, q.FirstOr(&user, func() error { return nil })) }},
				{"FirstOrFail", func(q contractsorm.Query) { require.NoError(t, q.FirstOrFail(&user)) }},
				{"FirstOrNew", func(q contractsorm.Query) {
					require.NoError(t, q.FirstOrNew(&user, map[string]any{"name": "reads_state_a"}))
				}},
				{"Find", func(q contractsorm.Query) { require.NoError(t, q.Find(&user, ids[0])) }},
				{"FindOrFail", func(q contractsorm.Query) { require.NoError(t, q.FindOrFail(&user, ids[0])) }},
				{"FindOr", func(q contractsorm.Query) { require.NoError(t, q.FindOr(&user, ids[0], func() error { return nil })) }},
				{"FindOrFailKeys", func(q contractsorm.Query) { require.NoError(t, q.FindOrFail(&users, ids[:2])) }},
				{"FindOrKeys", func(q contractsorm.Query) {
					require.NoError(t, q.FindOr(&users, ids[:2], func() error { return nil }))
				}},
				{"FindMany", func(q contractsorm.Query) { require.NoError(t, q.FindMany(&users, ids[:2])) }},
				{"Paginate", func(q contractsorm.Query) { require.NoError(t, q.Paginate(1, 2, &users, &total)) }},
				{"SimplePaginate", func(q contractsorm.Query) {
					_, err := q.SimplePaginate(1, 2, &users)
					require.NoError(t, err)
				}},
				{"ToSqlFirst", func(q contractsorm.Query) { q.ToSql().First(&User{}) }},
				{"ToSqlFind", func(q contractsorm.Query) { q.ToSql().Find(&User{}, ids[0]) }},
				{"ToRawSqlFirst", func(q contractsorm.Query) { q.ToRawSql().First(&User{}) }},
			} {
				t.Run(read.name, func(t *testing.T) {
					query := fixture.Query().Model(&User{}).Where("name", "like", "reads_state_%")
					before := query.ToSql().Get(&[]User{})

					read.run(query)

					assert.Equal(t, before, query.ToSql().Get(&[]User{}), "the read changed the query")
					var after []User
					require.NoError(t, query.Get(&after))
					assert.Len(t, after, 3, "a later Get must read every matching row")
				})
			}
		})
	}
}

// TestReadsLeaveEagerLoadsInPlace pins the other half of the same rule: a read leaves the query's
// queued eager loads alone. Laravel's builder keeps `$eagerLoad` after a read - a second get() runs
// the relation query again and still loads the relation into the models (oracle,
// illuminate/database v12.55.1,
// /Users/dog/.claude/jobs/013c27a0/tmp/ofa/read_state_eager_probe.php -> read_state_eager_probe.out:
// with `with('posts')` every read costs its parent query plus the relation query, on Get, First,
// Find, Paginate, Chunk, Each, Value, Sole and Cursor alike). The port used to consume the entries on
// the receiver, so a shared query loaded the relation only on its first read.
func TestReadsLeaveEagerLoadsInPlace(t *testing.T) {
	queries := NewTestQueryBuilder().All("", false)
	if queries[sqlserver.Name] == nil && os.Getenv("GRADII_TEST_SQLSERVER_DSN") != "" {
		queries[sqlserver.Name] = NewTestQueryBuilder().Sqlserver("", false)
	}

	for name, fixture := range queries {
		t.Run(name, func(t *testing.T) {
			fixture.CreateTable(TestTableUsers, TestTableBooks)
			for _, user := range []string{"reads_eager_a", "reads_eager_b"} {
				created := &User{Name: user}
				require.NoError(t, fixture.Query().Create(created))
				require.NoError(t, fixture.Query().Create(&Book{UserID: created.ID, Name: user + "_book"}))
			}

			query := fixture.Query().Model(&User{}).Where("name", "like", "reads_eager_%").With("Books")

			var first []User
			require.NoError(t, query.Get(&first))
			require.Len(t, first, 2)
			for _, user := range first {
				require.Len(t, user.Books, 1, "the first read must load Books")
			}

			var second []User
			require.NoError(t, query.Get(&second))
			require.Len(t, second, 2)
			for _, user := range second {
				require.Len(t, user.Books, 1, "the second read must still load Books")
			}
		})
	}
}
