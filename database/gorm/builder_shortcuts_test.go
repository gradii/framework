package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
)

// shortcutUser carries the timestamp columns the orderings and writes default to, so the
// shortcuts that read a model's created-at or updated-at column have one to find.
type shortcutUser struct {
	ID        uint
	Name      string
	Score     int
	CreatedAt int64 `gorm:"autoCreateTime"`
	UpdatedAt int64 `gorm:"autoUpdateTime"`
}

func (shortcutUser) TableName() string { return "shortcut_users" }

func compileShortcut(t *testing.T, query *OrmBuilder) (string, []any) {
	t.Helper()

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

// --- WhereKey / WhereKeyNot / Except -------------------------------------------------------------

// whereKey compares the model's qualified primary key: a scalar with "=", a list with IN, a model
// with its own key. Mirrors Builder::whereKey, which uses getQualifiedKeyName() for all three.
func TestWhereKeyMatchesLaravelShapes(t *testing.T) {
	for _, test := range []struct {
		name     string
		id       any
		want     string
		bindings []any
	}{
		{"scalar", 5, `select * from "shortcut_users" where "shortcut_users"."id" = ?`, []any{5}},
		{"list", []any{1, 2}, `select * from "shortcut_users" where "shortcut_users"."id" in (?, ?)`, []any{1, 2}},
		{"typed list", []uint{3, 4}, `select * from "shortcut_users" where "shortcut_users"."id" in (?, ?)`, []any{uint(3), uint(4)}},
		{"model", &shortcutUser{ID: 9}, `select * from "shortcut_users" where "shortcut_users"."id" = ?`, []any{uint(9)}},
		{"model list", []*shortcutUser{{ID: 3}, {ID: 4}}, `select * from "shortcut_users" where "shortcut_users"."id" in (?, ?)`, []any{uint(3), uint(4)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := compiledQuery(t, &shortcutUser{}, Conditions{}).WhereKey(test.id).(*OrmBuilder)

			statement, bindings := compileShortcut(t, query)
			assert.Equal(t, test.want, statement)
			assert.Equal(t, test.bindings, bindings)
		})
	}
}

// whereKeyNot is whereKey with the comparison negated: "!=" for a scalar, NOT IN for a list.
func TestWhereKeyNotNegatesTheComparison(t *testing.T) {
	scalar := compiledQuery(t, &shortcutUser{}, Conditions{}).WhereKeyNot(5).(*OrmBuilder)
	statement, bindings := compileShortcut(t, scalar)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" != ?`, statement)
	assert.Equal(t, []any{5}, bindings)

	list := compiledQuery(t, &shortcutUser{}, Conditions{}).WhereKeyNot([]any{1, 2}).(*OrmBuilder)
	statement, bindings = compileShortcut(t, list)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" not in (?, ?)`, statement)
	assert.Equal(t, []any{1, 2}, bindings)
}

// except excludes the given models by their keys, which is whereKeyNot over modelKeys(). A value
// slice and a pointer slice read the same keys.
func TestExceptExcludesModelsByKey(t *testing.T) {
	pointers := compiledQuery(t, &shortcutUser{}, Conditions{}).Except([]*shortcutUser{{ID: 3}, {ID: 4}}).(*OrmBuilder)
	statement, bindings := compileShortcut(t, pointers)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" not in (?, ?)`, statement)
	assert.Equal(t, []any{uint(3), uint(4)}, bindings)

	values := compiledQuery(t, &shortcutUser{}, Conditions{}).Except([]shortcutUser{{ID: 3}}).(*OrmBuilder)
	statement, bindings = compileShortcut(t, values)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" not in (?)`, statement)
	assert.Equal(t, []any{uint(3)}, bindings)

	single := compiledQuery(t, &shortcutUser{}, Conditions{}).Except(&shortcutUser{ID: 7}).(*OrmBuilder)
	statement, bindings = compileShortcut(t, single)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" != ?`, statement)
	assert.Equal(t, []any{uint(7)}, bindings)
}

// A query with no model has no key column to compare, which is reported rather than compiled.
func TestWhereKeyRequiresAModel(t *testing.T) {
	query := compiledQuery(t, nil, Conditions{}).WhereKey(1).(*OrmBuilder)

	_, _, err := query.compileSelect()
	assert.ErrorIs(t, err, errors.OrmQueryPrimaryKeyNotFound)
}

// --- Latest / Oldest -----------------------------------------------------------------------------

// latest() and oldest() order by created_at by default, descending and ascending; a column names a
// different one. Mirrors Builder::latest / oldest, which read getCreatedAtColumn() ?? 'created_at'.
func TestLatestAndOldestOrderByTheCreatedAtColumn(t *testing.T) {
	latest := compiledQuery(t, &shortcutUser{}, Conditions{}).Latest().(*OrmBuilder)
	statement, _ := compileShortcut(t, latest)
	assert.Equal(t, `select * from "shortcut_users" order by "created_at" desc`, statement)

	oldest := compiledQuery(t, &shortcutUser{}, Conditions{}).Oldest().(*OrmBuilder)
	statement, _ = compileShortcut(t, oldest)
	assert.Equal(t, `select * from "shortcut_users" order by "created_at" asc`, statement)

	named := compiledQuery(t, &shortcutUser{}, Conditions{}).Latest("score").(*OrmBuilder)
	statement, _ = compileShortcut(t, named)
	assert.Equal(t, `select * from "shortcut_users" order by "score" desc`, statement)
}

// A model without a created-at column falls back to the literal "created_at", as Laravel does when
// getCreatedAtColumn() is null.
func TestLatestFallsBackToTheLiteralColumn(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).Latest().(*OrmBuilder)

	statement, _ := compileShortcut(t, query)
	assert.Equal(t, `select * from "rel_users" order by "created_at" desc`, statement)
}

// --- Value / Sole / FindMany --------------------------------------------------------------------

// value() reads one column of the first row: the statement selects that column alone and is
// limited to one row. Mirrors Builder::value, which is first([$column]).
func TestValueSelectsTheColumnOfTheFirstRow(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)
	recorder.returnRows(`select "name"`, []string{"name"}, []driver.Value{"Jane"})

	var name string
	require.NoError(t, query.Where("id", 1).(*OrmBuilder).Value("name", &name))

	assert.Equal(t, "Jane", name)
	assert.Equal(t, `select "name" from "users" where "id" = ? limit 1`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, recorder.last(t).args)

	// The column is installed for this read only: the query's own selection is still the wildcard.
	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "users" where "id" = ?`, statement)
}

// value() answers a missing row the way first() does: dest is left alone and no error is reported.
// Mirrors Builder::value, which returns null when first() finds nothing.
func TestValueLeavesDestAloneWhenNoRowMatches(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)

	name := "unchanged"
	require.NoError(t, query.Where("id", 999).(*OrmBuilder).Value("name", &name))

	assert.Equal(t, "unchanged", name)
	assert.Equal(t, `select "name" from "users" where "id" = ? limit 1`, recorder.last(t).sql)
}

// sole() limits the read to two rows so that both a missing row and a second row are detected.
// Mirrors BuildsQueries::sole and the RecordsNotFound / MultipleRecordsFound it raises.
func TestSoleReadsTwoRowsAndReportsNoneOrMany(t *testing.T) {
	t.Run("one row", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"}, []driver.Value{int64(1), "only"})

		var user getColumnsUser
		require.NoError(t, query.Sole(&user))

		assert.Equal(t, uint(1), user.ID)
		assert.Equal(t, "only", user.Name)
		assert.Equal(t, `select * from "users" limit 2`, recorder.last(t).sql)
	})

	t.Run("no rows", func(t *testing.T) {
		query, _ := newGetColumnsQuery(t)

		var user getColumnsUser
		assert.ErrorIs(t, query.Sole(&user), errors.OrmRecordNotFound)
	})

	t.Run("many rows", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select * from "users"`, []string{"id", "name"}, []driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})

		var user getColumnsUser
		err := query.Sole(&user)
		assert.ErrorIs(t, err, errors.OrmMultipleRecordsFound)
		assert.Contains(t, err.Error(), "2 records")
	})
}

// findMany() is whereKey($ids)->get(); an empty list reads nothing and leaves the destination empty.
func TestFindManyReadsByKeyAndSkipsAnEmptyList(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)

	var users []getColumnsUser
	require.NoError(t, query.FindMany(&users, []any{1, 2}))
	assert.Equal(t, `select * from "users" where "users"."id" in (?, ?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), int64(2)}, recorder.last(t).args)

	empty, emptyRecorder := newGetColumnsQuery(t)
	var none []getColumnsUser
	require.NoError(t, empty.FindMany(&none, nil))
	assert.Empty(t, none)
	assert.Equal(t, 0, emptyRecorder.count(), "an empty key list must not reach the database")
}

// find($id) is whereKey($id)->first(): the scalar form reads one row — `limit 1` with no ordering —
// while the key list stays on findMany's unlimited get() above. Oracle (sqlite, illuminate/database
// v12.55.1): `User::find(1)` compiles `select * from "users" where "users"."id" = ? limit 1`, and
// `findOrFail(1)` runs the same read before it decides.
func TestFindReadsOneRowByScalarKey(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)
	recorder.returnRows(`select * from "users"`, []string{"id", "name"}, []driver.Value{int64(1), "only"})

	var user getColumnsUser
	require.NoError(t, query.Find(&user, 1))

	assert.Equal(t, `select * from "users" where "users"."id" = ? limit 1`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, recorder.last(t).args)
	assert.Equal(t, uint(1), user.ID)
	assert.Equal(t, "only", user.Name)

	orFail, orFailRecorder := newGetColumnsQuery(t)
	orFailRecorder.returnRows(`select * from "users"`, []string{"id", "name"}, []driver.Value{int64(1), "only"})

	var second getColumnsUser
	require.NoError(t, orFail.FindOrFail(&second, 1))

	assert.Equal(t, `select * from "users" where "users"."id" = ? limit 1`, orFailRecorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, orFailRecorder.last(t).args)
}

// --- WithAttributes --------------------------------------------------------------------------------

// withAttributes adds each attribute as a where equality, qualified by the model's table as
// Model::qualifyColumn does, and records it for later creates. Mirrors Builder::withAttributes with
// its default $asConditions = true.
func TestWithAttributesAddsWheresAndRecordsTheAttributes(t *testing.T) {
	mapped := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes(map[string]any{"name": "x", "score": 3}).(*OrmBuilder)
	statement, bindings := compileShortcut(t, mapped)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."name" = ? and "shortcut_users"."score" = ?`, statement)
	assert.Equal(t, []any{"x", 3}, bindings)
	assert.Equal(t, map[string]any{"name": "x", "score": 3}, mapped.conditions.pendingAttributes)

	pair := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("name", "x").(*OrmBuilder)
	statement, bindings = compileShortcut(t, pair)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."name" = ?`, statement)
	assert.Equal(t, []any{"x"}, bindings)
}

// A Go field name resolves to its column before it is qualified, and a name that already carries a
// table is left as written, which is the str_contains($column, '.') guard in Model::qualifyColumn.
func TestWithAttributesQualifiesTheColumnOnce(t *testing.T) {
	field := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("Score", 3).(*OrmBuilder)
	statement, _ := compileShortcut(t, field)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."score" = ?`, statement)

	qualified := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("other.name", "x").(*OrmBuilder)
	statement, _ = compileShortcut(t, qualified)
	assert.Equal(t, `select * from "shortcut_users" where "other"."name" = ?`, statement)
}

// $asConditions = false records the attributes without touching the where clause.
func TestWithAttributesCanSkipTheConditions(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("name", "x", false).(*OrmBuilder)

	statement, bindings := compileShortcut(t, query)
	assert.Equal(t, `select * from "shortcut_users"`, statement)
	assert.Empty(t, bindings)
	assert.Equal(t, map[string]any{"name": "x"}, query.conditions.pendingAttributes)
}

// The recorded attributes reach a model the query creates, the way newModelInstance() merges
// $pendingAttributes into the attributes it is given; a value the caller already set wins.
func TestWithAttributesReachTheCreatedModel(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("name", "preset").(*OrmBuilder)

	blank := &shortcutUser{Score: 3}
	require.NoError(t, query.applyPendingAttributes(blank))
	assert.Equal(t, "preset", blank.Name)
	assert.Equal(t, 3, blank.Score)

	set := &shortcutUser{Name: "mine"}
	require.NoError(t, query.applyPendingAttributes(set))
	assert.Equal(t, "mine", set.Name, "the caller's value is kept")

	batch := []*shortcutUser{{}, {Name: "kept"}}
	require.NoError(t, query.applyPendingAttributes(batch))
	assert.Equal(t, "preset", batch[0].Name)
	assert.Equal(t, "kept", batch[1].Name)
}

// The pending attributes are written to the row when the model is created.
func TestCreateWritesThePendingAttributes(t *testing.T) {
	query, recorder := newPostgresKeyQuery(t, &shortcutUser{})
	recorder.returnLastInsertID(1)

	user := &shortcutUser{Score: 3}
	require.NoError(t, query.WithAttributes("name", "preset", false).Create(user))

	assert.Equal(t, "preset", user.Name)
	assert.Contains(t, recorder.last(t).sql, `insert into "shortcut_users"`)
	assert.Contains(t, recorder.last(t).sql, `"name"`)
	assert.Contains(t, recorder.last(t).args, "preset")
}

// make() fills the instance from the pending attributes and the attributes it is given, the given
// ones winning (newModelInstance's array_merge($pendingAttributes, $attributes)), and runs no
// statement. Mirrors Builder::make.
func TestMakeFillsTheInstanceWithoutTouchingTheDatabase(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)

	model := getColumnsUser{}
	require.NoError(t, query.WithAttributes("name", "preset", false).Make(&model))
	assert.Equal(t, "preset", model.Name)

	given := getColumnsUser{}
	require.NoError(t, query.WithAttributes("name", "preset", false).Make(&given, map[string]any{"name": "mine"}))
	assert.Equal(t, "mine", given.Name, "the given attribute wins over the pending one")

	assert.Equal(t, 0, recorder.count(), "make() must not reach the database")
}

// FirstOrNew merges the pending attributes into a miss, underneath the attributes and values it is
// given — the way firstOrNew routes the new instance through newModelInstance() — and leaves a
// found row untouched.
func TestFirstOrNewMergesPendingAttributes(t *testing.T) {
	t.Run("miss", func(t *testing.T) {
		pendingOnly := getColumnsUser{}
		query, _ := newGetColumnsQuery(t)
		require.NoError(t, query.WithAttributes("name", "preset", false).FirstOrNew(&pendingOnly, nil))
		assert.Equal(t, "preset", pendingOnly.Name)

		overridden := getColumnsUser{}
		query, _ = newGetColumnsQuery(t)
		require.NoError(t, query.WithAttributes("name", "preset", false).
			FirstOrNew(&overridden, map[string]any{"name": "attrs"}, map[string]any{"name": "values"}))
		assert.Equal(t, "values", overridden.Name, "values win over attributes, both win over pending")

		attributed := getColumnsUser{}
		query, _ = newGetColumnsQuery(t)
		require.NoError(t, query.WithAttributes("name", "preset", false).
			FirstOrNew(&attributed, map[string]any{"name": "attrs"}))
		assert.Equal(t, "attrs", attributed.Name)
	})

	t.Run("found", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows("from \"users\"", []string{"id", "name"}, []driver.Value{int64(1), "row"})

		user := getColumnsUser{}
		require.NoError(t, query.WithAttributes("name", "preset", false).FirstOrNew(&user, nil))
		assert.Equal(t, "row", user.Name, "a found row is not overlaid with pending attributes")
	})
}

// --- Increment / Decrement / Touch ------------------------------------------------------------------

// increment() and decrement() write a raw arithmetic assignment and stamp updated_at, as
// Builder::increment does through addUpdatedAtColumn. The query's where clause scopes the rows.
func TestIncrementAndDecrementCompileArithmeticAndStampUpdatedAt(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*OrmBuilder)

	values := query.stepValues("score", "+", []any{5, map[string]any{"name": "bumped"}})
	assignments, err := query.updateAssignments(values)
	require.NoError(t, err)

	statement, bindings, err := query.compileUpdate(assignments)
	require.NoError(t, err)
	assert.Equal(t, `update "shortcut_users" set "name" = ?, "score" = "score" + 5, "updated_at" = ? where "id" = ?`, statement)
	assert.Equal(t, "bumped", bindings[0])
	assert.NotNil(t, bindings[1], "updated_at is stamped")
	assert.Equal(t, 1, bindings[2])

	values = query.stepValues("score", "-", nil)
	assignments, err = query.updateAssignments(values)
	require.NoError(t, err)
	statement, _, err = query.compileUpdate(assignments)
	require.NoError(t, err)
	assert.Equal(t, `update "shortcut_users" set "score" = "score" - 1, "updated_at" = ? where "id" = ?`, statement)
}

// A non-numeric amount is refused the way Builder::increment refuses it.
func TestIncrementRefusesANonNumericAmount(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*OrmBuilder)

	_, err := query.Increment("score", "abc")
	assert.ErrorIs(t, err, errors.OrmQueryIncrementNonNumeric)

	_, err = query.Decrement("score", struct{}{})
	assert.ErrorIs(t, err, errors.OrmQueryIncrementNonNumeric)
}

// A bare increment - no where, no model key, no scope - is still refused. Laravel would run
// `update "shortcut_users" set "score" = "score" + 1, "updated_at" = ?` over the whole table; the ORM
// deliberately keeps its mass-update guard for that case (the one retained divergence).
func TestIncrementRefusesABareUnconditionedQuery(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{})

	_, err := query.Increment("score")
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)

	_, err = query.Decrement("score")
	assert.ErrorIs(t, err, errors.OrmMissingWhereClause)
}

// softDeleteTimestampUser is the scoped-increment pin's model: the framework's soft-delete field
// plus the automatic updated-at timestamp, the shape the corpus's soft-delete model
// (SoftDeletesTestUser) carries. The package's bare softDeleteUser fixture declares no timestamps,
// so an increment of it compiles without the updated_at Laravel stamps.
type softDeleteTimestampUser struct {
	ID        uint
	Name      string
	UpdatedAt int64                 `gorm:"autoUpdateTime"`
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at"`
}

func (softDeleteTimestampUser) TableName() string { return "soft_users" }

// A scoped increment passes the guard on the scope's WHERE alone and stamps updated_at into the SET
// list, exactly as Laravel compiles `update "users" set "id" = "id" + 1, "updated_at" = ? where
// "users"."deleted_at" is null` (oracle v12.55.1, executed: /tmp/massupdate_scope_guard_probe.php,
// `bare scoped increment(id)`).
func TestIncrementScopedBySoftDeletePassesTheGuard(t *testing.T) {
	query := newRelQueryWith(t, &softDeleteTimestampUser{})

	assignments, err := query.updateAssignments(query.stepValues("id", "+", nil))
	require.NoError(t, err)

	statement, bindings, err := query.compileUpdate(assignments)
	require.NoError(t, err)
	assert.Equal(t, `update "soft_users" set "id" = "id" + 1, "updated_at" = ? where "soft_users"."deleted_at" is null`, statement)
	require.Len(t, bindings, 1)
	assert.NotNil(t, bindings[0], "updated_at is stamped")
}

// An extra value naming the incremented column replaces the raw expression: incrementEach ends in
// `$this->update(array_merge($columns, $extra))`, so the caller's extra wins and the statement is
// `update "users" set "count" = ?, "updated_at" = ? where "id" = ?` (oracle v12.55.1, executed:
// /tmp/inc_extra_probe.php, `Builder::increment extra names the column`; Model::increment compiles
// the same SET list). Decrement shares stepValues.
func TestIncrementExtraNamingTheIncrementedColumnKeepsTheCallerValue(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*OrmBuilder)

	values := query.stepValues("score", "+", []any{5, map[string]any{"score": 99}})
	assignments, err := query.updateAssignments(values)
	require.NoError(t, err)

	statement, bindings, err := query.compileUpdate(assignments)
	require.NoError(t, err)
	assert.Equal(t, `update "shortcut_users" set "score" = ?, "updated_at" = ? where "id" = ?`, statement)
	assert.Equal(t, 99, bindings[0], "the extra value replaces the raw expression")
	assert.NotNil(t, bindings[1], "updated_at is stamped")
	assert.Equal(t, 1, bindings[2])
}

// touch() sets updated_at to now, or the named column; a model without an updated-at column has
// nothing to touch and says so. Mirrors Builder::touch.
func TestTouchStampsTheUpdatedAtColumn(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*OrmBuilder)
	assert.Equal(t, "updated_at", query.updatedAtColumn())

	assignments, err := query.updateAssignments(map[string]any{query.updatedAtColumn(): 123})
	require.NoError(t, err)
	statement, bindings, err := query.compileUpdate(assignments)
	require.NoError(t, err)
	assert.Equal(t, `update "shortcut_users" set "updated_at" = ? where "id" = ?`, statement)
	assert.Equal(t, []any{123, 1}, bindings)

	noTimestamps := compiledQuery(t, &relUser{}, Conditions{}).Where("id", 1).(*OrmBuilder)
	_, err = noTimestamps.Touch()
	assert.ErrorIs(t, err, errors.OrmQueryTimestampColumnNotFound)
}

// --- Upsert ------------------------------------------------------------------------------------------

// upsert() on a model stamps both timestamps onto every row, updates every inserted column except
// created_at by default, and always refreshes updated_at. Mirrors Builder::upsert with
// addTimestampsToUpsertValues and addUpdatedAtToUpsertColumns. The compiled upsert statement is dialect specific
// (Postgres spells it "on conflict ... do update set"); it is pinned in the integration corpus
// (tests/database/query) against the real driver grammar.
func TestUpsertStampsTimestampsAndUpdatesAllButCreatedAt(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{})

	records, err := query.insertRecords([]shortcutUser{{Name: "a", Score: 1}, {Name: "b", Score: 2}})
	require.NoError(t, err)
	records = uniformInsertRecords(records)
	query.addUpsertTimestamps(records)

	// shortcutUser holds its timestamps as integers, so the stamp is a Unix timestamp rather than the
	// DateTime a time-typed column would receive; either way it is no longer the zero the struct held.
	for _, record := range records {
		assert.NotZero(t, record["created_at"], "created_at is stamped")
		assert.NotZero(t, record["updated_at"], "updated_at is stamped")
		assert.IsType(t, int64(0), record["created_at"], "an integer column takes a Unix timestamp")
	}

	updates, insertOnly, err := query.upsertUpdates(records, nil)
	require.NoError(t, err)
	assert.False(t, insertOnly)
	columns := make([]string, 0, len(updates))
	for _, update := range updates {
		columns = append(columns, update.Column)
	}
	assert.Equal(t, []string{"name", "score", "updated_at"}, columns, "created_at is never updated; updated_at always is")

}

// An explicit update list is used as given, with updated_at joining it; an empty list inserts only.
func TestUpsertHonoursAnExplicitUpdateList(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{})
	records := []map[string]any{{"name": "a", "score": 1}}

	updates, insertOnly, err := query.upsertUpdates(records, []any{[]string{"score"}})
	require.NoError(t, err)
	assert.False(t, insertOnly)
	assert.Len(t, updates, 2)
	assert.Equal(t, "score", updates[0].Column)
	assert.Equal(t, "updated_at", updates[1].Column)

	_, insertOnly, err = query.upsertUpdates(records, []any{[]string{}})
	require.NoError(t, err)
	assert.True(t, insertOnly, "an empty update list means insert only, as $update === [] does")

	bound, _, err := query.upsertUpdates(records, []any{map[string]any{"score": 9}})
	require.NoError(t, err)
	assert.Equal(t, "score", bound[0].Column)
	assert.True(t, bound[0].Bound)
	assert.Equal(t, 9, bound[0].Value)
	assert.Equal(t, "updated_at", bound[1].Column)
}

// TestValueKeepsASelectionTheReadFoundMatchesTheOracle pins the onceWithColumns guard Value shares
// with pluck: an existing select list survives the call — the single column is installed only when
// the scoped query carries none — and the value is read back by the column's bare name, whether
// the kept statement selected one column or several. Oracle (illuminate/database v12.55.1,
// executed against sqlite): select('name')->value('email') compiles `select "name" from "users"
// limit 1` and answers null; select('id','email')->value('email') keeps `select "id", "email" from
// "users" limit 1` and reads "taylorotwell@gmail.com".
func TestValueKeepsASelectionTheReadFoundMatchesTheOracle(t *testing.T) {
	t.Run("a single kept column that is not the requested one", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select "name"`, []string{"name"}, []driver.Value{"Taylor"})

		email := "unchanged"
		require.NoError(t, query.Select("name").(*OrmBuilder).Value("email", &email))

		assert.Equal(t, `select "name" from "users" limit 1`, recorder.last(t).sql)
		// Oracle answers null for the missing column; the Go destination keeps the value it had.
		assert.Equal(t, "unchanged", email)
	})

	t.Run("a kept multi-column list reads the requested column back by name", func(t *testing.T) {
		query, recorder := newGetColumnsQuery(t)
		recorder.returnRows(`select "id", "email"`, []string{"id", "email"},
			[]driver.Value{int64(1), "taylorotwell@gmail.com"})

		var email string
		require.NoError(t, query.Select("id", "email").(*OrmBuilder).Value("email", &email))

		assert.Equal(t, `select "id", "email" from "users" limit 1`, recorder.last(t).sql)
		assert.Equal(t, "taylorotwell@gmail.com", email)
	})
}

// TestValueScopeProvidedSelectListMatchesTheOracle pins the same guard against a scope-written
// select: the scopes are applied before the read narrows the select (first([$column]) ->
// get($columns)), so selectRaw('name') compiles `select name from "users" limit 1` for
// value('email') and the missing column answers null — dest keeps its value (oracle,
// illuminate/database v12.55.1, executed against sqlite; a select('name') scope compiles
// `select "name" from "users" limit 1`).
func TestValueScopeProvidedSelectListMatchesTheOracle(t *testing.T) {
	t.Run("selectRaw scope", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckScopeRawUser{})
		recorder.returnRows(`select name from "users"`, []string{"name"}, []driver.Value{"Taylor"})

		email := "unchanged"
		require.NoError(t, query.Value("users.email", &email))

		assert.Equal(t, `select name from "users" limit 1`, recorder.last(t).sql)
		assert.Equal(t, "unchanged", email)
	})

	t.Run("select scope", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckScopeSelectUser{})
		recorder.returnRows(`select "name" from "users"`, []string{"name"}, []driver.Value{"Taylor"})

		email := "unchanged"
		require.NoError(t, query.Value("users.email", &email))

		assert.Equal(t, `select "name" from "users" limit 1`, recorder.last(t).sql)
		assert.Equal(t, "unchanged", email)
	})
}

// TestValueScopeProvidedLimitMatchesTheOracle pins the limit half of the read Value makes:
// Eloquent's value() is first([$column]) — limit(1) applied before get() runs applyScopes() — so a
// scope's own limit() overrides the one-row limit instead of being clobbered by it (oracle,
// illuminate/database v12.55.1, executed against sqlite, /tmp/pluck-verify/zz_limit_qualified_probe.php
// + zz_limit_vof_probe.php: a scope doing selectRaw('name')->limit(5) then value('users.email')
// compiles `select name from "users" limit 5` and answers null, while value('users.name') reads
// "Taylor" back; a scope doing limit(0) compiles `select "users"."email" from "users" limit 0` and
// answers null).
// TestValueSelectSubsAreAnInstalledSelectListMatchesTheOracle pins the onceWithColumns guard's
// selectSubs half on the Value/ValueOrFail read: withCount put `from.*` and the subselect into
// Laravel's select list before the read ran, so the single column is never installed and the
// statement keeps the aggregate list (oracle, illuminate/database v12.55.1, executed against
// sqlite, /tmp/pluck-verify/oracle_scope_repin2.php: withCount('posts')->value('users.email') and
// ->valueOrFail('users.email') both compile `select "users".*, (select count(*) from "posts" where
// "users"."id" = "posts"."user_id") as "posts_count" from "users" limit 1` and answer
// "taylorotwell@gmail.com").
func TestValueSelectSubsAreAnInstalledSelectListMatchesTheOracle(t *testing.T) {
	const withCountSQL = `select "users".*, (select count(*) from "posts" where "users"."id" = "posts"."user_id") as "posts_count" from "users" limit 1`

	columns := []string{"id", "email", "name", "posts_count"}
	row := []driver.Value{int64(1), "taylorotwell@gmail.com", "Taylor", int64(2)}

	t.Run("value", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`posts_count`, columns, row)

		var email string
		require.NoError(t, query.WithCount("Posts").(*OrmBuilder).Value("users.email", &email))

		assert.Equal(t, withCountSQL, recorder.last(t).sql)
		assert.Equal(t, "taylorotwell@gmail.com", email)
	})

	t.Run("valueOrFail", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckUser{})
		recorder.returnRows(`posts_count`, columns, row)

		var email string
		require.NoError(t, query.WithCount("Posts").(*OrmBuilder).ValueOrFail("users.email", &email))

		assert.Equal(t, withCountSQL, recorder.last(t).sql)
		assert.Equal(t, "taylorotwell@gmail.com", email)
	})
}

func TestValueScopeProvidedLimitMatchesTheOracle(t *testing.T) {
	t.Run("a scope limit overrides the one-row limit", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckScopeLimitUser{})
		recorder.returnRows(`from "users"`, []string{"name"}, []driver.Value{"Taylor"})

		email := "unchanged"
		require.NoError(t, query.Value("users.email", &email))

		assert.Equal(t, `select name from "users" limit 5`, recorder.last(t).sql)
		assert.Equal(t, "unchanged", email)

		var name string
		require.NoError(t, query.Value("users.name", &name))

		assert.Equal(t, `select name from "users" limit 5`, recorder.last(t).sql)
		assert.Equal(t, "Taylor", name)
	})

	t.Run("a limit(0) scope reads no row", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &pluckScopeZeroLimitUser{})

		email := "unchanged"
		require.NoError(t, query.Value("users.email", &email))

		assert.Equal(t, `select "users"."email" from "users" limit 0`, recorder.last(t).sql)
		assert.Equal(t, "unchanged", email)
	})
}

// The decrement mirror of TestIncrementExtraNamingTheIncrementedColumnKeepsTheCallerValue: an extra
// naming the decremented column replaces the raw expression, since decrement shares stepValues. The
// round-6 verify flagged the decrement branch of the collision as having no dedicated Go test.
// Oracle v12.55.1, executed: /tmp/inc_extra_probe.php, "dec extra names count" compiles
// `update "users" set "count" = ?, "updated_at" = ? where "id" = ?`.
func TestDecrementExtraNamingTheDecrementedColumnKeepsTheCallerValue(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*OrmBuilder)

	values := query.stepValues("score", "-", []any{5, map[string]any{"score": 99}})
	assignments, err := query.updateAssignments(values)
	require.NoError(t, err)

	statement, bindings, err := query.compileUpdate(assignments)
	require.NoError(t, err)
	assert.Equal(t, `update "shortcut_users" set "score" = ?, "updated_at" = ? where "id" = ?`, statement)
	assert.Equal(t, 99, bindings[0], "the extra value replaces the raw expression")
	assert.NotNil(t, bindings[1], "updated_at is stamped")
	assert.Equal(t, 1, bindings[2])
}
