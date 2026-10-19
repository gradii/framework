package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
)

// compiledQuery builds a query over a model with the given conditions, compiled by this package
// rather than handed to GORM.
func compiledQuery(t *testing.T, model any, conditions Conditions) *Query {
	t.Helper()

	conditions.model = model

	return NewQuery(context.Background(), nil, contractsdatabase.Config{}, newStubGormDB(t), nil, nil, nil, &conditions, nil)
}

func TestCompileSelect_WhereOrderLimit(t *testing.T) {
	limit := 10
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{1}}},
		order: []any{"id desc"},
		limit: &limit,
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, `from "rel_users"`)
	assert.Contains(t, sql, `"id" = ?`)
	assert.Contains(t, sql, "order by id desc")
	// Limit and offset are inlined rather than bound, which is what the DB facade pins too.
	assert.Contains(t, sql, "limit 10")
	assert.Equal(t, []any{1}, bindings)
}

func TestCompileSelect_RawWhereKeepsPlaceholder(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "name = ?", Args: []any{"John"}}},
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, "name = ?")
	assert.Equal(t, []any{"John"}, bindings)
}

func TestCompileSelect_SliceBecomesAnInList(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{[]any{1, 2}}}},
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, `"id" in (`)
	assert.Len(t, bindings, 2)
}

// TestCompileSelect_ExpandsSliceInRawPlaceholderFragment guards the ORM's "col in ?" shorthand: a raw
// fragment carrying one placeholder and a slice argument expands to one placeholder per value, as the
// GORM-backed builder did, instead of binding the whole slice to that single placeholder.
func TestCompileSelect_ExpandsSliceInRawPlaceholderFragment(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id in ?", Args: []any{[]any{1, 2, 3}}}},
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, `id in (?, ?, ?)`)
	assert.Equal(t, []any{1, 2, 3}, bindings)
}

// TestCompileSelect_ExpandsSliceAlreadyInsideParens covers the same shorthand when the caller wrote the
// parentheses, so the expansion must reuse them rather than add a second pair.
func TestCompileSelect_ExpandsSliceAlreadyInsideParens(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id in (?)", Args: []any{[]any{1, 2}}}},
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, `id in (?, ?)`)
	assert.NotContains(t, sql, `(?, ?))`)
	assert.Equal(t, []any{1, 2}, bindings)
}

func TestCompileSelect_NestedCallbackIsGrouped(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{
			{Query: "id", Args: []any{1}},
			{
				Or: true,
				Query: func(child contractsorm.Query) contractsorm.Query {
					return child.Where("name", "John")
				},
			},
		},
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, `("name" = ?)`)
	assert.Equal(t, []any{1, "John"}, bindings)
}

// TestCompileSelect_OrConnectorSurvivesEveryDispatchBranch pins that applySingleWhere carries the
// connector into each branch that has a boolean argument. The (column, value) default branch used to
// hand the condition to a hard-wired-and primitive, so an OrWhere rendered "and" and matched nothing;
// the JSON-length branch did the same through the builder's plain WhereJsonLength. Both must keep the
// "or".
func TestCompileSelect_OrConnectorSurvivesEveryDispatchBranch(t *testing.T) {
	t.Run("column and value", func(t *testing.T) {
		query := compiledQuery(t, &relUser{}, Conditions{
			where: []contractsdriver.Where{
				{Query: "name", Args: []any{"John"}},
				{Query: "email", Args: []any{"j@example.com"}, Or: true},
			},
		})

		sql, _, err := query.compileSelect()
		assert.NoError(t, err)

		assert.Contains(t, sql, `"name" = ? or "email" = ?`)
	})

	t.Run("json length", func(t *testing.T) {
		// WhereJsonLength only renders on a dialect that supports it, so compile with Postgres.
		conditions := Conditions{
			model: &relUser{},
			where: []contractsdriver.Where{
				{Query: "name", Args: []any{"John"}},
				{Query: "tags", Args: []any{2}, Or: true, Type: contractsdriver.WhereTypeJsonLength},
			},
		}
		query := NewQuery(context.Background(), nil, contractsdatabase.Config{Driver: "postgres"}, newStubGormDB(t), nil, nil, nil, &conditions, nil)
		query.queryGrammar, _ = querybuilder.NewGrammar("postgres", "")

		sql, _, err := query.compileSelect()
		assert.NoError(t, err)

		assert.Contains(t, sql, `"name" = ? or jsonb_array_length`)
	})
}

func TestCompileDelete(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{1}}},
	})

	sql, bindings, err := query.compileDelete()
	assert.NoError(t, err)

	assert.Contains(t, sql, `delete from "rel_users"`)
	assert.Contains(t, sql, `"id" = ?`)
	assert.Equal(t, []any{1}, bindings)
}

func TestCompileUpdate(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "id", Args: []any{1}}},
	})

	sql, bindings, err := query.compileUpdate(map[string]any{"name": "Jane"})
	assert.NoError(t, err)

	assert.Contains(t, sql, `update "rel_users" set "name" = ?`)
	assert.Contains(t, sql, `"id" = ?`)
	assert.Equal(t, []any{"Jane", 1}, bindings)
}

// TestCompileInsert_NamesTheTable guards the statement an insert names its table in. A table that
// fails to resolve compiles to `insert into "<nil>"`, which no dialect rejects at compile time and
// every dialect rejects at execution, so it is asserted here rather than only against a database.
func TestCompileInsert_NamesTheTable(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{})

	sql, bindings, err := query.compileInsert(map[string]any{"name": "John"})
	assert.NoError(t, err)

	assert.Contains(t, sql, `insert into "rel_users"`)
	assert.NotContains(t, sql, "<nil>")
	assert.Equal(t, []any{"John"}, bindings)
}

// TestUpdateAssignments_FromMap checks a map keyed by column names, which is the shape Update builds
// for a single column and value.
func TestUpdateAssignments_FromMap(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{})

	assignments, err := query.updateAssignments(map[string]any{"name": "Jane"})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "Jane"}, assignments)
}

// TestUpdateAssignments_FromStruct checks that a model writes its non-zero columns and leaves its
// primary key out, because the key identifies the row rather than describing it.
func TestUpdateAssignments_FromStruct(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{})

	assignments, err := query.updateAssignments(relUser{ID: 7, Name: "Jane"})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "Jane"}, assignments)
}

// TestUpdateAssignments_SelectAndOmitFilter checks the query's own column filter, which is how
// Select("name").Update(model) writes one column.
func TestUpdateAssignments_SelectAndOmitFilter(t *testing.T) {
	selected := compiledQuery(t, &relUser{}, Conditions{selectColumns: []string{"name"}})

	assignments, err := selected.updateAssignments(map[string]any{"id": 7, "name": "Jane"})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "Jane"}, assignments)

	omitted := compiledQuery(t, &relUser{}, Conditions{omit: []string{"name"}})

	assignments, err = omitted.updateAssignments(map[string]any{"id": 7, "name": "Jane"})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"id": 7}, assignments)

	// A column named outright is written even when it holds the zero value, which is what lets
	// Select("name").Update(model) clear a column; every other column is left out when it is zero.
	clearing := compiledQuery(t, &relUser{}, Conditions{selectColumns: []string{"name"}})

	assignments, err = clearing.updateAssignments(relUser{ID: 7})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": ""}, assignments)
}

// TestCompileUpdate_ScopesToTheModelKey checks the model's primary key reaching the where clause,
// which is what makes Model(user).Update(...) touch one row.
func TestCompileUpdate_ScopesToTheModelKey(t *testing.T) {
	query := compiledQuery(t, &relUser{ID: 7}, Conditions{})

	sql, bindings, err := query.compileUpdate(map[string]any{"name": "Jane"})
	assert.NoError(t, err)

	assert.Contains(t, sql, `update "rel_users" set "name" = ?`)
	assert.Contains(t, sql, `"id" = ?`)
	// relUser.ID is a uint, so the key binds as the field's own type rather than a widened int.
	assert.Equal(t, []any{"Jane", uint(7)}, bindings)
}

// TestCompiledStatement_CompilesPolymorphicExistence pins the compiled form of the polymorphic
// existence conditions, whose types are separate tables and therefore become one clause each.
func TestCompiledStatement_CompilesPolymorphicExistence(t *testing.T) {
	outbound := newRelQueryWith(t, &relUser{})
	outbound.conditions.relations = []relationExistence{{
		relation: "Houses", operator: ">=", count: 1, conjunction: "and", morphTypes: []any{&relUser{}},
	}}

	statement, bindings, err := outbound.compileSelect()
	require.NoError(t, err)

	assert.Contains(t, statement, "exists (")
	// The type is pinned on the related table, and the morph value is bound rather than inlined.
	assert.Contains(t, statement, `"houseable_type" = ?`)
	assert.Len(t, bindings, 1)

	negated := newRelQueryWith(t, &relUser{})
	negated.conditions.relations = []relationExistence{{
		relation: "Houses", operator: "<", count: 1, conjunction: "and", morphTypes: []any{&relUser{}},
	}}

	negatedStatement, _, err := negated.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, negatedStatement, "not exists (")

	// A count comparison re-selects the subquery as an aggregate and appends its own limit.
	counted := newRelQueryWith(t, &relUser{})
	counted.conditions.relations = []relationExistence{{
		relation: "Houses", operator: ">", count: 3, conjunction: "and", morphTypes: []any{&relUser{}},
	}}

	countedStatement, countedBindings, err := counted.compileSelect()
	require.NoError(t, err)

	assert.Contains(t, countedStatement, "(select count(*)")
	require.NotEmpty(t, countedBindings)
	assert.Equal(t, 3, countedBindings[len(countedBindings)-1])

	// The inverse relation pins the type against the parent's own morph column.
	inverse := newRelQueryWith(t, &morphImage{})
	inverse.conditions.relations = []relationExistence{{
		relation: "Imageable", operator: ">=", count: 1, conjunction: "and", morphTypes: []any{&relUser{}},
	}}

	inverseStatement, _, err := inverse.compileSelect()
	require.NoError(t, err)

	assert.Contains(t, inverseStatement, `"imageable_type" = ?`)
	assert.Contains(t, inverseStatement, "exists (")
}

// TestCompiledStatement_SurfacesAQueuedCondition pins that a condition the ORM could not accept is
// reported by the statement it would have affected, which is how an unusable operator or value reaches
// the caller now that the conditions are no longer flushed onto a GORM statement.
func TestCompiledStatement_SurfacesAQueuedCondition(t *testing.T) {
	query := newRelQueryWith(t, &relUser{})
	query.conditions.err = errors.OrmQueryConditionRequired

	_, _, err := query.compileSelect()
	assert.ErrorIs(t, err, errors.OrmQueryConditionRequired)

	_, _, err = query.compileAggregate("count", []any{"*"})
	assert.ErrorIs(t, err, errors.OrmQueryConditionRequired)

	_, _, err = query.compileDelete()
	assert.ErrorIs(t, err, errors.OrmQueryConditionRequired)

	_, err = query.execStatement("select 1", nil)
	assert.ErrorIs(t, err, errors.OrmQueryConditionRequired)
}

// TestCompiledStatement_SurfacesAnUnusableCondition pins the path a caller actually takes when an
// operator or value count is rejected: the helper cannot return an error, so the query carries it and
// the statement compiled from that query reports it.
func TestCompiledStatement_SurfacesAnUnusableCondition(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).WhereAll([]string{"name"}, "=", 1, 2).(*Query)

	_, _, err := query.compileSelect()
	assert.ErrorIs(t, err, errors.DatabaseInvalidArgumentNumber)

	_, _, err = query.compileDelete()
	assert.ErrorIs(t, err, errors.DatabaseInvalidArgumentNumber)
}

// TestCompileSelect_CombinesSelectAndSelectRaw pins Laravel's rule that select() and selectRaw()
// share one ordered list: a query that calls both keeps the raw expression alongside the named
// columns rather than dropping it. Laravel's selectRaw() calls addSelect(new Expression(...)), and
// the oracle renders this chain as `select "name", count(avatar) as count from "users"`. Before this
// fix the compile path chose between the two with an if/else and silently discarded the raw select.
func TestCompileSelect_CombinesSelectAndSelectRaw(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).Select("name").SelectRaw("count(avatar) as count").(*Query)

	sql, _, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, `select "name", count(avatar) as count from "rel_users"`)
}

// TestCompileSelect_SelectRawOnlyStillApplies pins that the raw select is still used when it is the
// only selection, so the combine change did not disturb the SelectRaw-on-its-own path.
func TestCompileSelect_SelectRawOnlyStillApplies(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).SelectRaw("count(avatar) as count").(*Query)

	sql, _, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Contains(t, sql, `select count(avatar) as count from "rel_users"`)
}

// TestCompileSelect_ByteSliceStaysOneBinding guards the value side of the raw-placeholder expansion:
// a []byte is how a caller binds one binary value to a `?`, so it must stay a single binding even
// though isSlice also matches it. Before this, Where("note = ?", []byte("ab")) expanded to one
// placeholder per byte ("note = (?, ?)"). Laravel has no separate byte-string type -- a PHP string
// is a byte string and renders one placeholder for the fragment's single binding -- so a byte slice
// is a value, not a list.
func TestCompileSelect_ByteSliceStaysOneBinding(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{
		where: []contractsdriver.Where{{Query: "note = ?", Args: []any{[]byte("ab")}}},
	})

	sql, bindings, err := query.compileSelect()
	assert.NoError(t, err)

	assert.Equal(t, `select * from "rel_users" where note = ?`, sql)
	assert.Equal(t, []any{[]byte("ab")}, bindings)
}
