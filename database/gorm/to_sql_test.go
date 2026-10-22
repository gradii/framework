package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	"github.com/goravel/framework/errors"
	mockslog "github.com/goravel/framework/mocks/log"
)

// TestToSql_ReportsTheStatementTheQueryRuns pins the forms ToSql reports. The statement comes from the
// same compiler the ORM executes, so it is the statement that would run rather than a second builder's
// impression of it.
func TestToSql_ReportsTheStatementTheQueryRuns(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{1}}},
	})

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
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "name", Args: []any{"Jane"}}},
	})

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

	selected := NewToSql(compiledQuery(t, &relUser{}, Conditions{
		selectItems: []any{"name"},
	}), log, false)

	assert.Contains(t, selected.Count(), `count("name")`)

	// Counting a distinct column writes the DISTINCT into the aggregate; a leading DISTINCT over
	// count(*) would count every row.
	distinct := NewToSql(compiledQuery(t, &relUser{}, Conditions{
		distinct:      true,
		selectItems: []any{"name"},
	}), log, false)

	assert.Contains(t, distinct.Count(), `count(distinct "name")`)

	// A wildcard, or several columns, counts the rows themselves.
	wildcard := NewToSql(compiledQuery(t, &relUser{}, Conditions{distinct: true}), log, false)

	assert.Contains(t, wildcard.Count(), `count(*)`)
}

// TestToSql_SumAndPluckResolveTheModelFromDest pins that Sum and Pluck name their table through the
// model they are handed, so the reported statement carries the FROM clause the executed one does.
func TestToSql_SumAndPluckResolveTheModelFromDest(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{1}}},
	})

	toSql := NewToSql(query, log, false)

	sum := toSql.Sum("id", relUser{})
	assert.Contains(t, sum, `sum("id") as aggregate`)
	assert.Contains(t, sum, `from "rel_users"`)

	pluck := toSql.Pluck("id", relUser{})
	assert.Contains(t, pluck, `select "id"`)
	assert.Contains(t, pluck, `from "rel_users"`)
}

// TestToSql_FindQualifiesThePrimaryKeyShorthand pins that Find's lone-scalar condition names the
// model's qualified key column, the way GORM wrote it.
func TestToSql_FindQualifiesThePrimaryKeyShorthand(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, relUser{}, Conditions{})

	assert.Contains(t, NewToSql(query, log, false).Find(&relUser{}, 1), `"rel_users"."id" = ?`)
}

// TestToSql_DeleteRewritesSoftDeleteToUpdate pins that Delete reports the update a soft-delete model
// compiles to, rather than the hard delete the ORM would never run.
func TestToSql_DeleteRewritesSoftDeleteToUpdate(t *testing.T) {
	log := mockslog.NewLog(t)
	query := compiledQuery(t, softDeleteUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{1}}},
	})

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
