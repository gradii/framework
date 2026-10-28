package gorm

import (
	mysqldialect "github.com/gradii/mysql"
	postgresdriver "github.com/gradii/postgres"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
)

// refreshTarget is a model that names a connection of its own, which is what makes a query refresh
// onto another connection instead of running where it was built.
type refreshTarget struct {
	ID uint
}

func (refreshTarget) TableName() string { return "target_rows" }

func (refreshTarget) Connection() string { return "target" }

// A query that moves onto another connection keeps its clauses and takes that connection's dialect.
// The clauses are recorded dialect-neutral, so handing the receiver's builder over unchanged
// compiles them with the source connection's grammar — a MySQL-built handle then renders backticks
// on Postgres. TestCustomConnection covers the same defect end to end; this pins it without a
// database behind it.
func TestRefreshConnectionRebindsTheDialect(t *testing.T) {
	source := rebindGrammar(compiledQuery(t, &refreshTarget{}, Conditions{}), "mysql", mysqldialect.NewQueryGrammar(""))
	source.dbConfig = contractsdatabase.Config{Driver: "mysql", Connection: "source"}
	source.Where("id", 1)

	target := rebindGrammar(compiledQuery(t, &refreshTarget{}, Conditions{}), "postgres", postgresdriver.NewQueryGrammar(""))
	target.dbConfig = contractsdatabase.Config{Driver: "postgres", Connection: "target"}

	// The connection being moved to is already built, so no config lookup happens here.
	source.queries = map[string]*Query{"target": target}

	refreshed, err := source.refreshConnection()
	require.NoError(t, err)
	assert.Same(t, target.components.Processor, refreshed.builder.Processor())

	statement, bindings, err := refreshed.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "target_rows" where "id" = ?`, statement)
	assert.Equal(t, []any{1}, bindings)

	// The receiver keeps its clauses and its dialect, so it still compiles as it was built.
	assert.Equal(t, "mysql", source.dbConfig.Driver)
	sourceStatement, sourceBindings, err := source.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, "select * from `target_rows` where `id` = ?", sourceStatement, "the receiver still renders the dialect it was built for")
	assert.Equal(t, []any{1}, sourceBindings)
}

// Clone owns its clauses. A branch that adds one leaves the query it came from alone, which is what
// makes branching safe now that a fluent call writes to its receiver instead of returning a new
// query.
func TestCloneOwnsItsClauses(t *testing.T) {
	base := compiledQuery(t, &getColumnsUser{}, Conditions{}).Select("a").(*Query)
	branch := base.Clone().(*Query)
	branch.Where("id", 1)

	baseStatement, _, err := base.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select "a" from "users"`, baseStatement)

	branchStatement, bindings, err := branch.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select "a" from "users" where "id" = ?`, branchStatement)
	assert.Equal(t, []any{1}, bindings)

	// Writing to the base afterwards must not reach the branch either.
	base.Where("name", "x")
	branchStatement, _, err = branch.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select "a" from "users" where "id" = ?`, branchStatement)
}
