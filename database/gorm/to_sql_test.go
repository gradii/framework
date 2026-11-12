package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/errors"
	mockslog "github.com/gradii/framework/mocks/log"
)

// TestToSql_ReportsTheStatementTheQueryRuns pins the forms ToSql reports. The statement comes from the
// same compiler the ORM executes, so it is the statement that would run rather than a second builder's
// impression of it.
func TestToSql_ReportsTheStatementTheQueryRuns(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQueryWhere(t, &relUser{}, []contractsdriver.Where{{Query: "id", Args: []any{1}}}...)

	toSql := NewToSql(query, log, false)

	assert.Contains(t, toSql.Count(), `select count(*) as aggregate from "rel_users"`)
	assert.Contains(t, toSql.Count(), `"id" = ?`)

	// First adds the ordering and the single-row limit the executed read applies, ordering by the
	// model's qualified key.
	assert.Contains(t, toSql.First(&relUser{}), `order by "rel_users"."id"`)
	assert.Contains(t, toSql.First(&relUser{}), "limit 1")

	assert.Contains(t, toSql.Get(&relUser{}), `select * from "rel_users"`)

	// Update reports the set values followed by the where clause, in binding order.
	updated := toSql.Update("name", "Jane")
	assert.Contains(t, updated, `update "rel_users" set "name" = ?`)
	assert.Contains(t, updated, `"id" = ?`)

	// An empty or malformed shape reports nothing at all.
	assert.Empty(t, toSql.Update(0, "Jane"))
}

// TestToSql_SaveAcceptsABatchDestination pins that a slice destination reaches Save's insert branch.
// GetID reports no key for a slice instead of panicking on NumField, so the batch compiles to one
// statement with a row per record rather than failing before the branch is entered.
func TestToSql_SaveAcceptsABatchDestination(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, &relUser{}, Conditions{})

	assert.Equal(t, `insert into "rel_users" ("name") values (?), (?)`, NewToSql(query, log, false).Save([]relUser{{Name: "Jane"}, {Name: "John"}}))
}

// TestToSql_RawWritesTheBindingsIn pins the raw form, which reports the statement with its arguments
// rendered into it.
func TestToSql_RawWritesTheBindingsIn(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQueryWhere(t, &relUser{}, []contractsdriver.Where{{Query: "name", Args: []any{"Jane"}}}...)

	assert.Contains(t, NewToSql(query, log, true).Get(&relUser{}), `"name" = 'Jane'`)
}

// TestToSql_ReportsAStatementItCouldNotBuild pins that a statement which cannot be compiled is reported
// as an empty string, with the reason logged, rather than as a statement that would fail.
func TestToSql_ReportsAStatementItCouldNotBuild(t *testing.T) {
	log := mockslog.NewLog(t)
	log.EXPECT().Errorf("failed to get sql: %v", mock.Anything).Once()

	query := compiledQuery(t, &relUser{}, Conditions{})
	query.conditions.err = errors.OrmQueryInvalidModel.Args("invalid")

	assert.Empty(t, NewToSql(query, log, false).Get(&relUser{}))
}

// TestToSql_CountFollowsTheSelection pins the column a count reduces, which is the one the query
// selects, and how DISTINCT changes it.
func TestToSql_CountFollowsTheSelection(t *testing.T) {
	log := mockslog.NewLog(t)

	selected := NewToSql(compiledQuery(t, &relUser{}, Conditions{}).Select("name").(*OrmBuilder), log, false)

	assert.Contains(t, selected.Count(), `count("name")`)

	// Counting a distinct column writes the DISTINCT into the aggregate; a leading DISTINCT over
	// count(*) would count every row.
	distinct := NewToSql(compiledQuery(t, &relUser{}, Conditions{}).Select("name").Distinct().(*OrmBuilder), log, false)

	assert.Contains(t, distinct.Count(), `count(distinct "name")`)

	// A wildcard, or several columns, counts the rows themselves.
	wildcard := NewToSql(compiledQuery(t, &relUser{}, Conditions{}).Distinct().(*OrmBuilder), log, false)

	assert.Contains(t, wildcard.Count(), `count(*)`)
}

// TestToSql_SumAndPluckResolveTheModelFromDest pins that Sum and Pluck name their table through the
// model they are handed, so the reported statement carries the FROM clause the executed one does.
func TestToSql_SumAndPluckResolveTheModelFromDest(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQueryWhere(t, relUser{}, []contractsdriver.Where{{Query: "id", Args: []any{1}}}...)

	toSql := NewToSql(query, log, false)

	sum := toSql.Sum("id", relUser{})
	assert.Contains(t, sum, `sum("id") as aggregate`)
	assert.Contains(t, sum, `from "rel_users"`)

	pluck := toSql.Pluck("id", relUser{})
	assert.Contains(t, pluck, `select "id"`)
	assert.Contains(t, pluck, `from "rel_users"`)
}

// TestToSql_FindQualifiesThePrimaryKeyShorthand pins that Find's lone-scalar condition names the
// model's qualified key column — the way GORM wrote it, and the way Laravel's find($id) compiles
// whereKey($id)->first() — and that the reported read is the one-row form with it: oracle (sqlite,
// illuminate/database v12.55.1) `select * from "users" where "users"."id" = ? limit 1`.
func TestToSql_FindQualifiesThePrimaryKeyShorthand(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, relUser{}, Conditions{})

	sql := NewToSql(query, log, false).Find(&relUser{}, 1)
	assert.Contains(t, sql, `"rel_users"."id" = ?`)
	assert.Contains(t, sql, `"rel_users"."id" = ? limit 1`)
}

// TestToSql_DeleteRewritesSoftDeleteToUpdate pins that Delete reports the update a soft-delete model
// compiles to, rather than the hard delete the ORM would never run.
func TestToSql_DeleteRewritesSoftDeleteToUpdate(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQueryWhere(t, softDeleteUser{}, []contractsdriver.Where{{Query: "id", Args: []any{1}}}...)

	sql := NewToSql(query, log, false).Delete(&softDeleteUser{})
	assert.Contains(t, sql, `update "soft_users" set "deleted_at" = ?`)
	assert.Contains(t, sql, `"soft_users"."deleted_at"`)
}

// TestToSql_CreateCarriesEveryColumnAHeterogeneousBatchWrites pins the reported column list of a batch
// whose records do not all carry the same columns. Execution inserts a row at a time, each with the
// record's own columns, so a column only a later record carries is written; the reported statement has
// to carry it too rather than keying off the first record and hiding it.
func TestToSql_CreateCarriesEveryColumnAHeterogeneousBatchWrites(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, &relUser{}, Conditions{})

	sql := NewToSql(query, log, false).Create([]map[string]any{
		{"name": "row1"},
		{"name": "row2", "weight": 1},
	})

	assert.Equal(t, `insert into "rel_users" ("name", "weight") values (?, ?), (?, ?)`, sql)
}

// TestCompileInsertAllCarriesEveryColumnAHeterogeneousBatchWrites pins the rendered statement and its
// bindings for the same batch. The second record's weight reaches the statement (bound with a nil for
// the first row that does not carry it), so the reported statement covers every column execution writes.
func TestCompileInsertAllCarriesEveryColumnAHeterogeneousBatchWrites(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{})

	statement, bindings, err := query.compileInsertAll([]map[string]any{
		{"name": "row1"},
		{"name": "row2", "weight": 1},
	})
	assert.NoError(t, err)
	assert.Equal(t, `insert into "rel_users" ("name", "weight") values (?, ?), (?, ?)`, statement)
	assert.Equal(t, []any{"row1", nil, "row2", 1}, bindings)
}

// TestCompileInsertAllKeepsAHomogeneousBatchUnchanged pins that the normalisation changes nothing when
// every record already carries the same columns, so the common batch stays byte for byte what it was.
func TestCompileInsertAllKeepsAHomogeneousBatchUnchanged(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{})

	statement, bindings, err := query.compileInsertAll([]map[string]any{
		{"name": "row1", "weight": 1},
		{"name": "row2", "weight": 2},
	})
	assert.NoError(t, err)
	assert.Equal(t, `insert into "rel_users" ("name", "weight") values (?, ?), (?, ?)`, statement)
	assert.Equal(t, []any{"row1", 1, "row2", 2}, bindings)
}

// TestCompileInsertAllLeavesASingleRecordUntouched pins the one-row insert, which is the statement
// execution runs, against being reshaped: it keeps exactly the columns the record carries.
func TestCompileInsertAllLeavesASingleRecordUntouched(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{})

	statement, bindings, err := query.compileInsertAll([]map[string]any{{"name": "row1"}})
	assert.NoError(t, err)
	assert.Equal(t, `insert into "rel_users" ("name") values (?)`, statement)
	assert.Equal(t, []any{"row1"}, bindings)
}

// TestCompileInsertAllCollapsesAnAllEmptyBatchToTheEmptyInsert pins the values list an all-empty
// batch is compiled from: the empty one, so the statement is the empty insert execution runs once
// per record (Laravel's insertGetId([]) per create), not the literal batch
// `insert into "incrementing_records" () values (), ()` (oracle, illuminate/database v12.55.1),
// which sqlite rejects (near ")" syntax error).
func TestCompileInsertAllCollapsesAnAllEmptyBatchToTheEmptyInsert(t *testing.T) {
	query := compiledQuery(t, &incrementingRecord{}, Conditions{})

	statement, bindings, err := query.compileInsertAll([]map[string]any{{}, {}})
	assert.NoError(t, err)
	assert.Equal(t, `insert into "incrementing_records" default values`, statement)
	assert.Empty(t, bindings)
}

// TestCompileInsertAllKeepsAMixedBatchWithAnEmptyRecord pins the boundary of that collapse: one
// record carrying no column and one carrying one is not the empty attribute set asked for twice,
// so the batch stays literal — execution writes the empty record's absent column as NULL, the
// value the batch's column union gives it.
func TestCompileInsertAllKeepsAMixedBatchWithAnEmptyRecord(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{})

	statement, bindings, err := query.compileInsertAll([]map[string]any{{}, {"name": "row2"}})
	assert.NoError(t, err)
	assert.Equal(t, `insert into "rel_users" ("name") values (?), (?)`, statement)
	assert.Equal(t, []any{nil, "row2"}, bindings)
}

// TestToSql_PluckKeepsAPreSetSelectList pins the preview mirror of onceWithColumns: a select list
// the caller set survives, exactly as the executed form keeps it — the column pluck would have
// installed is not in the statement.
func TestToSql_PluckKeepsAPreSetSelectList(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, relUser{}, Conditions{}).Select("name").(*OrmBuilder)

	pluck := NewToSql(query, log, false).Pluck("email", relUser{})

	assert.Contains(t, pluck, `select "name"`)
	assert.NotContains(t, pluck, "email")
}

// TestToSql_PluckRestoresTheSelectListItInstalledMatchesTheOracle pins the preview mirror of the
// restore half of onceWithColumns: the column installed for the preview is written back once it is
// rendered, so a preview compiled from the same builder afterwards carries the caller's state, not
// the pluck select (oracle, illuminate/database v12.55.1, executed against sqlite:
// `$q->pluck('email'); $q->pluck('name')` compiles `select "email" from "users"` and then
// `select "name" from "users"`).
func TestToSql_PluckRestoresTheSelectListItInstalledMatchesTheOracle(t *testing.T) {
	log := mockslog.NewLog(t)
	toSql := NewToSql(compiledQuery(t, &pluckUser{}, Conditions{}), log, false)

	first := toSql.Pluck("users.email", pluckUser{})
	second := toSql.Pluck("users.name", pluckUser{})

	assert.Equal(t, `select "users"."email" from "users"`, first)
	assert.Equal(t, `select "users"."name" from "users"`, second)
}

// TestToSql_PluckTreatsSelectSubsAsAPreSetSelectListMatchesTheOracle pins the preview half of the
// withAggregate guard: a queued selectSub is an installed select list, so pluck neither prepends
// its column nor drops `from.*` (oracle, illuminate/database v12.55.1, executed against sqlite:
// withCount('posts')->pluck('email') compiles `select "users".*, (select count(*) from "posts"
// where "users"."id" = "posts"."user_id") as "posts_count" from "users"`).
func TestToSql_PluckTreatsSelectSubsAsAPreSetSelectListMatchesTheOracle(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, &pluckUser{}, Conditions{}).WithCount("Posts").(*OrmBuilder)

	pluck := NewToSql(query, log, false).Pluck("users.email", pluckUser{})

	assert.Equal(t, `select "users".*, (select count(*) from "posts" where "users"."id" = "posts"."user_id") as "posts_count" from "users"`, pluck)
	assert.NotContains(t, pluck, `"users"."email"`)
}

// TestToSql_CreateEmptyAttributesOnIncrementingModel pins the preview of the empty-attribute
// insert on an incrementing model: the reported statement is the executed form's, Laravel's empty
// insert (`insert into "users" default values`; oracle, illuminate/database v12.55.1, sqlite),
// not the invalid `() values ()` a one-record list would render.
func TestToSql_CreateEmptyAttributesOnIncrementingModel(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, &incrementingRecord{}, Conditions{})

	statement := NewToSql(query, log, false).Create(&incrementingRecord{})

	assert.Equal(t, `insert into "incrementing_records" default values`, statement)

	// A slice of empty models is the same insert asked for once per row: the batch reports the
	// empty insert as well, not the literal `insert into "incrementing_records" () values (), ()`
	// (oracle, illuminate/database v12.55.1) which sqlite rejects (near ")" syntax error).
	batch := NewToSql(query, log, false).Create([]incrementingRecord{{}, {}})

	assert.Equal(t, `insert into "incrementing_records" default values`, batch)
}

// TestToSql_PluckKeepsAScopeProvidedSelectListMatchesTheOracle pins the preview mirror of the
// scope-before-onceWithColumns timing: the scopes are applied before the guard, so a scope-written
// select survives and the pluck column is not installed (oracle, illuminate/database v12.55.1,
// executed against sqlite: scope selectRaw('name') + pluck('email') compiles `select name from
// "users"`; scope select('name') + pluck('email') compiles `select "name" from "users"`).
func TestToSql_PluckKeepsAScopeProvidedSelectListMatchesTheOracle(t *testing.T) {
	log := mockslog.NewLog(t)

	rawUser := scopedCompiledQuery(t, &pluckScopeRawUser{}, "sel", pluckSelectRawScope)
	assert.Equal(t, `select name from "users"`, NewToSql(rawUser, log, false).Pluck("users.email", pluckScopeRawUser{}))

	selectUser := scopedCompiledQuery(t, &pluckScopeSelectUser{}, "sel", pluckSelectScope)
	assert.Equal(t, `select "name" from "users"`, NewToSql(selectUser, log, false).Pluck("users.email", pluckScopeSelectUser{}))

	// The receiver keeps its own state: a second preview compiles the scope's list again, and the
	// receiver's own select list stays empty.
	assert.Equal(t, `select name from "users"`, NewToSql(rawUser, log, false).Pluck("users.email", pluckScopeRawUser{}))
	assert.Empty(t, rawUser.query.Selects())
}
