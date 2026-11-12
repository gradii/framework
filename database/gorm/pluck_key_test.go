package gorm

import (
	"database/sql/driver"
	"testing"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pluckUser is the model the keyed-pluck tests fold: the corpus's users row shape (id/email/name).
type pluckUser struct {
	ID    uint
	Email string
	Name  string
}

func (pluckUser) TableName() string { return "users" }

func (pluckUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &pluckPost{}, ForeignKey: "user_id"},
	}
}

// TestPluck_KeyedFormMatchesTheOracle pins the select list pluck builds and the fold it applies.
// Laravel's pluck forwards to Query\Builder::pluck, which selects [$column, $key] (onceWithColumns;
// a key equal to the column collapses to one column) and folds rows through pluckFromArrayColumn.
// Oracle (illuminate/database v12.55.1, sqlite): flat `select "users"."email" from "users" order by
// "id" asc`; keyed `select "users"."email", "users"."id" from "users" order by "id" asc`.
func TestPluck_KeyedFormMatchesTheOracle(t *testing.T) {
	t.Run("flat", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"email"},
			[]driver.Value{"taylorotwell@gmail.com"},
			[]driver.Value{"abigailotwell@gmail.com"})

		emails := []string{}
		require.NoError(t, query.Oldest("id").Pluck("users.email", &emails))

		assert.Equal(t, `select "users"."email" from "users" order by "id" asc`, recorder.last(t).sql)
		assert.Equal(t, []string{"taylorotwell@gmail.com", "abigailotwell@gmail.com"}, emails)
	})

	t.Run("keyed", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"email", "id"},
			[]driver.Value{"taylorotwell@gmail.com", int64(1)},
			[]driver.Value{"abigailotwell@gmail.com", int64(2)})

		keyed := map[uint64]string{}
		require.NoError(t, query.Oldest("id").Pluck("users.email", &keyed, "users.id"))

		assert.Equal(t, `select "users"."email", "users"."id" from "users" order by "id" asc`, recorder.last(t).sql)
		assert.Equal(t, map[uint64]string{1: "taylorotwell@gmail.com", 2: "abigailotwell@gmail.com"}, keyed)
	})

	t.Run("key equal to the column collapses to one column", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"email"},
			[]driver.Value{"taylorotwell@gmail.com"},
			[]driver.Value{"abigailotwell@gmail.com"})

		keyed := map[string]string{}
		require.NoError(t, query.Pluck("users.email", &keyed, "users.email"))

		assert.Equal(t, `select "users"."email" from "users"`, recorder.last(t).sql)
		assert.Equal(t, map[string]string{
			"taylorotwell@gmail.com":  "taylorotwell@gmail.com",
			"abigailotwell@gmail.com": "abigailotwell@gmail.com",
		}, keyed)
	})
}

// TestPluck_KeyedFormValidatesTheDestination pins the two destination shapes: a slice without a key,
// a map with one.
func TestPluck_KeyedFormValidatesTheDestination(t *testing.T) {
	query, _ := newRecordingRelQuery(t, &pluckUser{})

	err := query.Pluck("users.email", &[]string{}, "users.id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected pointer to map")

	err = query.Pluck("users.email", &map[uint64]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected pointer to slice")
}

// TestPluck_JoinedKeyedFormMatchesTheOracle pins the joined shapes the corpus's testPluckWithJoin
// asserts: Builder::join installs a structured on-clause — `inner join "posts" on "users"."id" =
// "posts"."user_id"` (oracle) — and pluck selects the value and its key, an aliased key column
// rendered `"users"."email" as "user_email"` (oracle).
func TestPluck_JoinedKeyedFormMatchesTheOracle(t *testing.T) {
	t.Run("keyed by posts.id", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"name", "id"},
			[]driver.Value{"First post", int64(1)})

		byPost := map[uint64]string{}
		require.NoError(t, query.Join("posts", "users.id", "=", "posts.user_id").Pluck("posts.name", &byPost, "posts.id"))

		assert.Equal(t, `select "posts"."name", "posts"."id" from "users" inner join "posts" on "users"."id" = "posts"."user_id"`, recorder.last(t).sql)
		assert.Equal(t, map[uint64]string{1: "First post"}, byPost)
	})

	t.Run("aliased key column", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"name", "user_email"},
			[]driver.Value{"First post", "abigailotwell@gmail.com"})

		byEmail := map[string]string{}
		require.NoError(t, query.Join("posts", "users.id", "=", "posts.user_id").Pluck("posts.name", &byEmail, "users.email AS user_email"))

		assert.Equal(t, `select "posts"."name", "users"."email" as "user_email" from "users" inner join "posts" on "users"."id" = "posts"."user_id"`, recorder.last(t).sql)
		assert.Equal(t, map[string]string{"abigailotwell@gmail.com": "First post"}, byEmail)
	})
}

// TestPluck_KeepsAPreSetSelectListMatchesTheOracle pins the other half of onceWithColumns: pluck
// installs [$column, $key] only when the query carries no select list, so a select() the caller
// already wrote survives verbatim (oracle, illuminate/database v12.55.1, sqlite —
// select('name')->pluck('email') compiles `select "name" from "users"`;
// select('name','email')->pluck('email') compiles `select "name", "email" from "users"`;
// select('id','email')->pluck('email','id') compiles `select "id", "email" from "users"`). The
// fold reads the row's columns by name, so a plucked column the select list left out folds to
// null — the Go destination's zero value here — and one the list carried folds to its value.
func TestPluck_KeepsAPreSetSelectListMatchesTheOracle(t *testing.T) {
	t.Run("a carried column survives; the missing one folds to its zero value", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"name"},
			[]driver.Value{"Taylor"},
			[]driver.Value{"Abigail"})

		emails := []string{}
		require.NoError(t, query.Select("name").Pluck("users.email", &emails))

		assert.Equal(t, `select "name" from "users"`, recorder.last(t).sql)
		// Oracle folds the absent column to null per row ([null, null]); a Go []string carries the
		// zero value, one entry per row.
		assert.Equal(t, []string{"", ""}, emails)
	})

	t.Run("a carried two-column list survives; the plucked column folds by name", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"name", "email"},
			[]driver.Value{"Taylor", "taylorotwell@gmail.com"},
			[]driver.Value{"Abigail", "abigailotwell@gmail.com"})

		emails := []string{}
		require.NoError(t, query.Select("name", "email").Pluck("users.email", &emails))

		assert.Equal(t, `select "name", "email" from "users"`, recorder.last(t).sql)
		assert.Equal(t, []string{"taylorotwell@gmail.com", "abigailotwell@gmail.com"}, emails)
	})

	t.Run("a carried list keyed by a carried column", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`from "users"`, []string{"id", "email"},
			[]driver.Value{int64(1), "taylorotwell@gmail.com"},
			[]driver.Value{int64(2), "abigailotwell@gmail.com"})

		keyed := map[uint64]string{}
		require.NoError(t, query.Select("id", "email").Pluck("users.email", &keyed, "users.id"))

		assert.Equal(t, `select "id", "email" from "users"`, recorder.last(t).sql)
		assert.Equal(t, map[uint64]string{1: "taylorotwell@gmail.com", 2: "abigailotwell@gmail.com"}, keyed)
	})
}

// pluckPost is the related row the selectSub tests count: the corpus's posts table.
type pluckPost struct {
	ID     uint
	UserID uint
}

func (pluckPost) TableName() string { return "posts" }

// pluckUserCount reads a withCount row back: the users columns plus the aggregate alias, declared
// the way the corpus's aggregate DTOs are (the alias as a gorm column tag).
type pluckUserCount struct {
	ID         uint   `gorm:"column:id"`
	Email      string `gorm:"column:email"`
	Name       string `gorm:"column:name"`
	PostsCount int64  `gorm:"column:posts_count"`
}

func (pluckUserCount) TableName() string { return "users" }

// TestPluck_RestoresTheSelectListItInstalledMatchesTheOracle pins the second half of
// onceWithColumns: the columns written for the read are restored once it completes, so a query that
// plucks can still be executed — or plucked again — without wearing the first read's select list.
// Oracle (illuminate/database v12.55.1, executed against sqlite —
// `$q = User::query(); $q->pluck('email'); $q->pluck('name')` runs two statements,
// `select "email" from "users"` then `select "name" from "users"`; `pluck('email')` followed by
// `get()` compiles `select "email" from "users"` then `select * from "users"`).
func TestPluck_RestoresTheSelectListItInstalledMatchesTheOracle(t *testing.T) {
	t.Run("a second pluck is not left wearing the first read's column", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`select "users"."email"`, []string{"email"},
			[]driver.Value{"taylorotwell@gmail.com"})
		recorder.returnRows(`select "users"."name"`, []string{"name"},
			[]driver.Value{"Taylor"})

		emails := []string{}
		require.NoError(t, query.Pluck("users.email", &emails))

		names := []string{}
		require.NoError(t, query.Pluck("users.name", &names))

		assert.Equal(t, `select "users"."email" from "users"`, recorder.at(t, 0).sql)
		assert.Equal(t, `select "users"."name" from "users"`, recorder.at(t, 1).sql)
		assert.Equal(t, []string{"taylorotwell@gmail.com"}, emails)
		assert.Equal(t, []string{"Taylor"}, names)
	})

	t.Run("a later read compiles without it", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`select "users"."email"`, []string{"email"},
			[]driver.Value{"taylorotwell@gmail.com"})
		recorder.returnRows(`select * from "users"`, []string{"id", "email", "name"},
			[]driver.Value{int64(1), "taylorotwell@gmail.com", "Taylor"})

		emails := []string{}
		require.NoError(t, query.Pluck("users.email", &emails))

		users := []pluckUser{}
		require.NoError(t, query.Get(&users))

		assert.Equal(t, `select "users"."email" from "users"`, recorder.at(t, 0).sql)
		assert.Equal(t, `select * from "users"`, recorder.at(t, 1).sql)
		assert.Equal(t, []pluckUser{{ID: 1, Email: "taylorotwell@gmail.com", Name: "Taylor"}}, users)
	})
}

// TestPluck_SelectSubsAreAnInstalledSelectListMatchesTheOracle pins the onceWithColumns guard
// against withAggregate's write: withCount put `from.*` and the subselect into Laravel's select
// list before pluck ran, so pluck keeps them and adds no column of its own — where a bare pluck
// installs [column, key]. Oracle (illuminate/database v12.55.1, executed against sqlite):
// withCount('posts')->pluck('email') and ->pluck('email', 'id') both compile
// `select "users".*, (select count(*) from "posts" where "users"."id" = "posts"."user_id") as
// "posts_count" from "users"`, and a later get() compiles the same statement again — the
// withCount list persists, where the bare pluck's pair is restored away.
func TestPluck_SelectSubsAreAnInstalledSelectListMatchesTheOracle(t *testing.T) {
	const withCountSQL = `select "users".*, (select count(*) from "posts" where "users"."id" = "posts"."user_id") as "posts_count" from "users"`

	// The canned row carries the columns the statement returns: users.* plus the alias.
	columns := []string{"id", "email", "name", "posts_count"}
	row := []driver.Value{int64(1), "taylorotwell@gmail.com", "Taylor", int64(2)}

	t.Run("flat", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`posts_count`, columns, row)

		emails := []string{}
		require.NoError(t, query.WithCount("Posts").(*OrmBuilder).Pluck("users.email", &emails))

		assert.Equal(t, withCountSQL, recorder.last(t).sql)
		assert.Equal(t, []string{"taylorotwell@gmail.com"}, emails)
	})

	t.Run("keyed", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`posts_count`, columns, row)

		keyed := map[uint64]string{}
		require.NoError(t, query.WithCount("Posts").(*OrmBuilder).Pluck("users.email", &keyed, "users.id"))

		assert.Equal(t, withCountSQL, recorder.last(t).sql)
		assert.Equal(t, map[uint64]string{1: "taylorotwell@gmail.com"}, keyed)
	})

	t.Run("the withCount list survives the pluck", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`posts_count`, columns, row)

		q := query.WithCount("Posts").(*OrmBuilder)

		emails := []string{}
		require.NoError(t, q.Pluck("users.email", &emails))

		users := []pluckUserCount{}
		require.NoError(t, q.Get(&users))

		assert.Equal(t, withCountSQL, recorder.at(t, 0).sql)
		assert.Equal(t, withCountSQL, recorder.at(t, 1).sql)
		require.Len(t, users, 1)
		assert.Equal(t, int64(2), users[0].PostsCount)
	})

	t.Run("a pre-set select still leads the subselect", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`posts_count`, []string{"name", "posts_count"},
			[]driver.Value{"Taylor", int64(2)})

		emails := []string{}
		require.NoError(t, query.Select("name").WithCount("Posts").(*OrmBuilder).Pluck("users.email", &emails))

		assert.Equal(t, `select "name", (select count(*) from "posts" where "users"."id" = "posts"."user_id") as "posts_count" from "users"`, recorder.last(t).sql)
		// Oracle folds the column the kept list left out to null per row (the PHP read warns and
		// yields null); the Go destination holds the zero value, one entry per row.
		assert.Equal(t, []string{""}, emails)
	})
}

// The pluckScope* fixtures used to declare their scopes GlobalScopes(); scopes mount on the
// builder with ApplyScope now, so each read mounts the same callback at its construction site
// through scopedRecorderQuery / scopedCompiledQuery below.

// pluckSelectRawScope writes the select list with selectRaw('name'): the scopes are applied before
// pluck's onceWithColumns decision (toBase() timing), so the scope's list wins and the pluck pair
// is never installed.
func pluckSelectRawScope(q contractsorm.Query) contractsorm.Query { return q.SelectRaw("name") }

// pluckSelectScope is the same callback with a select('name') list.
func pluckSelectScope(q contractsorm.Query) contractsorm.Query { return q.Select("name") }

// pluckLimitScope writes the select list and a limit(5): Eloquent's value()/valueOrFail() are
// first([$column]) — limit(1) applied before get() runs applyScopes() — so the scope's limit(5)
// overrides the read's one-row limit and the statement stays `select name from "users" limit 5`
// (oracle, illuminate/database v12.55.1, executed against sqlite,
// /tmp/pluck-verify/zz_limit_qualified_probe.php).
func pluckLimitScope(q contractsorm.Query) contractsorm.Query { return q.SelectRaw("name").Limit(5) }

// pluckZeroLimitScope pins the read to no rows: the scope's limit(0) overrides the read's one-row
// limit, so the statement reads nothing (oracle: value answers null; valueOrFail throws
// ModelNotFoundException; pluck/get yield the empty set).
func pluckZeroLimitScope(q contractsorm.Query) contractsorm.Query { return q.Limit(0) }

// scopedRecorderQuery is newRecordingRelQuery over model with the named scope mounted on the
// builder — the mount the model used to declare — so the scope runs when the read compiles.
func scopedRecorderQuery(t *testing.T, model any, name string, scope func(contractsorm.Query) contractsorm.Query) (*OrmBuilder, *recordingSQLDriver) {
	t.Helper()

	query, recorder := newRecordingRelQuery(t, model)

	return query.ApplyScope(name, scope).(*OrmBuilder), recorder
}

// scopedCompiledQuery is compiledQuery over the pluckScope* model with the named scope mounted on
// the builder, for the preview (ToSql) paths.
func scopedCompiledQuery(t *testing.T, model any, name string, scope func(contractsorm.Query) contractsorm.Query) *OrmBuilder {
	t.Helper()

	return compiledQuery(t, model, Conditions{}).ApplyScope(name, scope).(*OrmBuilder)
}

// pluckScopeRawUser is pluckUser behind a mounted selectRaw('name') scope (pluckSelectRawScope).
type pluckScopeRawUser struct {
	ID    uint
	Email string
	Name  string
}

func (pluckScopeRawUser) TableName() string { return "users" }

// pluckScopeSelectUser is the same fixture with a mounted select('name') scope (pluckSelectScope).
type pluckScopeSelectUser struct {
	ID    uint
	Email string
	Name  string
}

func (pluckScopeSelectUser) TableName() string { return "users" }

// pluckScopeLimitUser is the fixture behind a mounted selectRaw('name')->limit(5) scope
// (pluckLimitScope).
type pluckScopeLimitUser struct {
	ID    uint
	Email string
	Name  string
}

func (pluckScopeLimitUser) TableName() string { return "users" }

// pluckScopeZeroLimitUser is the fixture behind a mounted limit(0) scope (pluckZeroLimitScope).
type pluckScopeZeroLimitUser struct {
	ID    uint
	Email string
	Name  string
}

func (pluckScopeZeroLimitUser) TableName() string { return "users" }

// TestPluck_ScopeProvidedSelectListBeatsTheInstalledPairMatchesTheOracle pins the scope-before-
// onceWithColumns timing: pluck() reads through toBase() — applyScopes()->getQuery() — so a model
// whose global scope writes the select list keeps that list; the pluck pair is never installed,
// and the fold then reads the requested column out of the scope's list by name, folding a missing
// column to null (the Go destination's zero value). Oracle (illuminate/database v12.55.1, executed
// against sqlite, /tmp/pluck-verify/oracle_scoperaw_probe.php + oracle_scope_reuse.php): scope
// selectRaw('name') + pluck('email') compiles `select name from "users"` and yields [null,null];
// the keyed pluck('email','id') compiles the same and yields {"":null}; scope select('name')
// compiles `select "name" from "users"` for both forms; a second read on the same query compiles
// the scope's list again.
func TestPluck_ScopeProvidedSelectListBeatsTheInstalledPairMatchesTheOracle(t *testing.T) {
	t.Run("selectRaw scope, flat", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeRawUser{}, "sel", pluckSelectRawScope)
		recorder.returnRows(`from "users"`, []string{"name"},
			[]driver.Value{"Taylor"},
			[]driver.Value{"Abigail"})

		emails := []string{}
		require.NoError(t, query.Pluck("users.email", &emails))

		assert.Equal(t, `select name from "users"`, recorder.last(t).sql)
		// Oracle folds the missing column to null per row ([null,null]); a Go []string carries one
		// zero value per row.
		assert.Equal(t, []string{"", ""}, emails)
	})

	t.Run("select scope, flat", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeSelectUser{}, "sel", pluckSelectScope)
		recorder.returnRows(`from "users"`, []string{"name"},
			[]driver.Value{"Taylor"},
			[]driver.Value{"Abigail"})

		emails := []string{}
		require.NoError(t, query.Pluck("users.email", &emails))

		assert.Equal(t, `select "name" from "users"`, recorder.last(t).sql)
		assert.Equal(t, []string{"", ""}, emails)
	})

	t.Run("selectRaw scope, keyed", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeRawUser{}, "sel", pluckSelectRawScope)
		recorder.returnRows(`from "users"`, []string{"name"}, []driver.Value{"Taylor"})

		keyed := map[uint64]string{}
		require.NoError(t, query.Pluck("users.email", &keyed, "users.id"))

		assert.Equal(t, `select name from "users"`, recorder.last(t).sql)
		// Oracle keys the null value by null folded to "" ({"":null}); a Go map keyed by uint64
		// carries the zero key and the zero value.
		assert.Equal(t, map[uint64]string{0: ""}, keyed)
	})

	t.Run("the scope still applies to the receiver's later read", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeRawUser{}, "sel", pluckSelectRawScope)
		recorder.returnRows(`select name from "users"`, []string{"name"},
			[]driver.Value{"Taylor"},
			[]driver.Value{"Abigail"})

		emails := []string{}
		require.NoError(t, query.Pluck("users.email", &emails))

		users := []pluckScopeRawUser{}
		require.NoError(t, query.Get(&users))

		assert.Equal(t, `select name from "users"`, recorder.at(t, 0).sql)
		assert.Equal(t, `select name from "users"`, recorder.at(t, 1).sql)
		assert.Equal(t, []pluckScopeRawUser{{Name: "Taylor"}, {Name: "Abigail"}}, users)
	})

	t.Run("a scope-provided limit survives", func(t *testing.T) {
		query, recorder := scopedRecorderQuery(t, &pluckScopeLimitUser{}, "sel", pluckLimitScope)
		recorder.returnRows(`from "users"`, []string{"name"}, []driver.Value{"Taylor"})

		names := []string{}
		require.NoError(t, query.Pluck("users.name", &names))

		assert.Equal(t, `select name from "users" limit 5`, recorder.last(t).sql)
		assert.Equal(t, []string{"Taylor"}, names)
	})
}
