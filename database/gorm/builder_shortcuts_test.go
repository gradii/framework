package gorm

import (
	"database/sql/driver"
	postgresdriver "github.com/gradii/postgres"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func compileShortcut(t *testing.T, query *Query) (string, []any) {
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
			query := compiledQuery(t, &shortcutUser{}, Conditions{}).WhereKey(test.id).(*Query)

			statement, bindings := compileShortcut(t, query)
			assert.Equal(t, test.want, statement)
			assert.Equal(t, test.bindings, bindings)
		})
	}
}

// whereKeyNot is whereKey with the comparison negated: "!=" for a scalar, NOT IN for a list.
func TestWhereKeyNotNegatesTheComparison(t *testing.T) {
	scalar := compiledQuery(t, &shortcutUser{}, Conditions{}).WhereKeyNot(5).(*Query)
	statement, bindings := compileShortcut(t, scalar)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" != ?`, statement)
	assert.Equal(t, []any{5}, bindings)

	list := compiledQuery(t, &shortcutUser{}, Conditions{}).WhereKeyNot([]any{1, 2}).(*Query)
	statement, bindings = compileShortcut(t, list)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" not in (?, ?)`, statement)
	assert.Equal(t, []any{1, 2}, bindings)
}

// except excludes the given models by their keys, which is whereKeyNot over modelKeys(). A value
// slice and a pointer slice read the same keys.
func TestExceptExcludesModelsByKey(t *testing.T) {
	pointers := compiledQuery(t, &shortcutUser{}, Conditions{}).Except([]*shortcutUser{{ID: 3}, {ID: 4}}).(*Query)
	statement, bindings := compileShortcut(t, pointers)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" not in (?, ?)`, statement)
	assert.Equal(t, []any{uint(3), uint(4)}, bindings)

	values := compiledQuery(t, &shortcutUser{}, Conditions{}).Except([]shortcutUser{{ID: 3}}).(*Query)
	statement, bindings = compileShortcut(t, values)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" not in (?)`, statement)
	assert.Equal(t, []any{uint(3)}, bindings)

	single := compiledQuery(t, &shortcutUser{}, Conditions{}).Except(&shortcutUser{ID: 7}).(*Query)
	statement, bindings = compileShortcut(t, single)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."id" != ?`, statement)
	assert.Equal(t, []any{uint(7)}, bindings)
}

// A query with no model has no key column to compare, which is reported rather than compiled.
func TestWhereKeyRequiresAModel(t *testing.T) {
	query := compiledQuery(t, nil, Conditions{}).WhereKey(1).(*Query)

	_, _, err := query.compileSelect()
	assert.ErrorIs(t, err, errors.OrmQueryPrimaryKeyNotFound)
}

// --- Latest / Oldest -----------------------------------------------------------------------------

// latest() and oldest() order by created_at by default, descending and ascending; a column names a
// different one. Mirrors Builder::latest / oldest, which read getCreatedAtColumn() ?? 'created_at'.
func TestLatestAndOldestOrderByTheCreatedAtColumn(t *testing.T) {
	latest := compiledQuery(t, &shortcutUser{}, Conditions{}).Latest().(*Query)
	statement, _ := compileShortcut(t, latest)
	assert.Equal(t, `select * from "shortcut_users" order by "created_at" desc`, statement)

	oldest := compiledQuery(t, &shortcutUser{}, Conditions{}).Oldest().(*Query)
	statement, _ = compileShortcut(t, oldest)
	assert.Equal(t, `select * from "shortcut_users" order by "created_at" asc`, statement)

	named := compiledQuery(t, &shortcutUser{}, Conditions{}).Latest("score").(*Query)
	statement, _ = compileShortcut(t, named)
	assert.Equal(t, `select * from "shortcut_users" order by "score" desc`, statement)
}

// A model without a created-at column falls back to the literal "created_at", as Laravel does when
// getCreatedAtColumn() is null.
func TestLatestFallsBackToTheLiteralColumn(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).Latest().(*Query)

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
	require.NoError(t, query.Where("id", 1).(*Query).Value("name", &name))

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
	require.NoError(t, query.Where("id", 999).(*Query).Value("name", &name))

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

// --- WithAttributes --------------------------------------------------------------------------------

// withAttributes adds each attribute as a where equality, qualified by the model's table as
// Model::qualifyColumn does, and records it for later creates. Mirrors Builder::withAttributes with
// its default $asConditions = true.
func TestWithAttributesAddsWheresAndRecordsTheAttributes(t *testing.T) {
	mapped := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes(map[string]any{"name": "x", "score": 3}).(*Query)
	statement, bindings := compileShortcut(t, mapped)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."name" = ? and "shortcut_users"."score" = ?`, statement)
	assert.Equal(t, []any{"x", 3}, bindings)
	assert.Equal(t, map[string]any{"name": "x", "score": 3}, mapped.conditions.pendingAttributes)

	pair := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("name", "x").(*Query)
	statement, bindings = compileShortcut(t, pair)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."name" = ?`, statement)
	assert.Equal(t, []any{"x"}, bindings)
}

// A Go field name resolves to its column before it is qualified, and a name that already carries a
// table is left as written, which is the str_contains($column, '.') guard in Model::qualifyColumn.
func TestWithAttributesQualifiesTheColumnOnce(t *testing.T) {
	field := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("Score", 3).(*Query)
	statement, _ := compileShortcut(t, field)
	assert.Equal(t, `select * from "shortcut_users" where "shortcut_users"."score" = ?`, statement)

	qualified := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("other.name", "x").(*Query)
	statement, _ = compileShortcut(t, qualified)
	assert.Equal(t, `select * from "shortcut_users" where "other"."name" = ?`, statement)
}

// $asConditions = false records the attributes without touching the where clause.
func TestWithAttributesCanSkipTheConditions(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("name", "x", false).(*Query)

	statement, bindings := compileShortcut(t, query)
	assert.Equal(t, `select * from "shortcut_users"`, statement)
	assert.Empty(t, bindings)
	assert.Equal(t, map[string]any{"name": "x"}, query.conditions.pendingAttributes)
}

// The recorded attributes reach a model the query creates, the way newModelInstance() merges
// $pendingAttributes into the attributes it is given; a value the caller already set wins.
func TestWithAttributesReachTheCreatedModel(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).WithAttributes("name", "preset").(*Query)

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
	recorder.returnRows(`insert into "shortcut_users"`, []string{"id"}, []driver.Value{int64(1)})

	user := &shortcutUser{Score: 3}
	require.NoError(t, query.WithAttributes("name", "preset", false).Create(user))

	assert.Equal(t, "preset", user.Name)
	assert.Contains(t, recorder.last(t).sql, `insert into "shortcut_users"`)
	assert.Contains(t, recorder.last(t).sql, `"name"`)
	assert.Contains(t, recorder.last(t).args, "preset")
}

// --- Increment / Decrement / Touch ------------------------------------------------------------------

// increment() and decrement() write a raw arithmetic assignment and stamp updated_at, as
// Builder::increment does through addUpdatedAtColumn. The query's where clause scopes the rows.
func TestIncrementAndDecrementCompileArithmeticAndStampUpdatedAt(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*Query)

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
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*Query)

	_, err := query.Increment("score", "abc")
	assert.ErrorIs(t, err, errors.OrmQueryIncrementNonNumeric)

	_, err = query.Decrement("score", struct{}{})
	assert.ErrorIs(t, err, errors.OrmQueryIncrementNonNumeric)
}

// touch() sets updated_at to now, or the named column; a model without an updated-at column has
// nothing to touch and says so. Mirrors Builder::touch.
func TestTouchStampsTheUpdatedAtColumn(t *testing.T) {
	query := compiledQuery(t, &shortcutUser{}, Conditions{}).Where("id", 1).(*Query)
	assert.Equal(t, "updated_at", query.updatedAtColumn())

	assignments, err := query.updateAssignments(map[string]any{query.updatedAtColumn(): 123})
	require.NoError(t, err)
	statement, bindings, err := query.compileUpdate(assignments)
	require.NoError(t, err)
	assert.Equal(t, `update "shortcut_users" set "updated_at" = ? where "id" = ?`, statement)
	assert.Equal(t, []any{123, 1}, bindings)

	noTimestamps := compiledQuery(t, &relUser{}, Conditions{}).Where("id", 1).(*Query)
	_, err = noTimestamps.Touch()
	assert.ErrorIs(t, err, errors.OrmQueryTimestampColumnNotFound)
}

// --- Upsert ------------------------------------------------------------------------------------------

// upsert() on a model stamps both timestamps onto every row, updates every inserted column except
// created_at by default, and always refreshes updated_at. Mirrors Builder::upsert with
// addTimestampsToUpsertValues and addUpdatedAtToUpsertColumns.
func TestUpsertStampsTimestampsAndUpdatesAllButCreatedAt(t *testing.T) {
	query := rebindGrammar(compiledQuery(t, &shortcutUser{}, Conditions{}), "postgres", postgresdriver.NewQueryGrammar(""))

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

	builder, err := query.newBuilder()
	require.NoError(t, err)
	statement, _ := builder.ToUpsertSQL(records, []string{"name"}, updates)
	require.NoError(t, builder.Error())
	assert.Equal(t, `insert into "shortcut_users" ("created_at", "name", "score", "updated_at") values (?, ?, ?, ?), (?, ?, ?, ?) on conflict ("name") do update set "name" = "excluded"."name", "score" = "excluded"."score", "updated_at" = "excluded"."updated_at"`, statement)
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
