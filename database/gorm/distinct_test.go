package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	querybuilder "github.com/goravel/framework/database/query"
)

// Laravel keeps the DISTINCT columns separate from both the projection and its bindings.
func TestDistinctPreservesSelection(t *testing.T) {
	for _, test := range []struct {
		name     string
		grammar  querybuilder.Grammar
		selected string
		all      string
		count    string
	}{
		{
			name:     "postgres",
			grammar:  querybuilder.NewPostgresGrammar(""),
			selected: `select distinct on ("name") "name", ? as flag from "users" where "id" = ?`,
			all:      `select distinct "name", ? as flag from "users" where "id" = ?`,
			count:    `select count(distinct "name") as aggregate from "users" where "id" = ?`,
		},
		{
			name:     "mysql",
			grammar:  querybuilder.NewMySqlGrammar(""),
			selected: "select distinct `name`, ? as flag from `users` where `id` = ?",
			all:      "select distinct `name`, ? as flag from `users` where `id` = ?",
			count:    "select count(distinct `name`) as aggregate from `users` where `id` = ?",
		},
		{
			name:     "sqlite",
			grammar:  querybuilder.NewSqliteGrammar(""),
			selected: `select distinct "name", ? as flag from "users" where "id" = ?`,
			all:      `select distinct "name", ? as flag from "users" where "id" = ?`,
			count:    `select count(distinct "name") as aggregate from "users" where "id" = ?`,
		},
		{
			name:     "sqlserver",
			grammar:  querybuilder.NewSqlServerGrammar(""),
			selected: "select distinct [name], ? as flag from [users] where [id] = ?",
			all:      "select distinct [name], ? as flag from [users] where [id] = ?",
			count:    "select count(distinct [name]) as aggregate from [users] where [id] = ?",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := rebindGrammar(compiledQuery(t, &getColumnsUser{}, Conditions{}), test.name, test.grammar)
			base := query.Select("name").SelectRaw("? as flag", 1).Where("id", 9).(*Query)
			columns := []string{"name"}
			selected := base.clone().Distinct(columns...).(*Query)
			columns[0] = "id"
			all := selected.clone().Distinct().(*Query)

			statement, bindings, err := selected.compileSelect()
			require.NoError(t, err)
			assert.Equal(t, test.selected, statement)
			assert.Equal(t, []any{1, 9}, bindings)

			statement, bindings, err = all.compileSelect()
			require.NoError(t, err)
			assert.Equal(t, test.all, statement)
			assert.Equal(t, []any{1, 9}, bindings)

			statement, bindings, err = selected.compileAggregate("count", []any{"*"})
			require.NoError(t, err)
			assert.Equal(t, test.count, statement)
			assert.Equal(t, []any{9}, bindings)

			assert.False(t, base.builder.DistinctFlag())
			assert.Len(t, base.builder.Selects(), 2)
		})
	}
}

func TestDistinctPreservesRelationAggregate(t *testing.T) {
	query := newRelQueryWith(t, &relUser{}).WithCount("Books").Distinct("name").(*Query)
	query.queryGrammar = querybuilder.NewPostgresGrammar("")

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Contains(t, statement, `select distinct on ("name") "rel_users".*`)
	assert.Contains(t, statement, `(select count(*) from "rel_books"`)
	assert.Contains(t, statement, `AS "books_count"`)
	assert.Empty(t, bindings)
}

func TestSelectPreservesDistinctColumns(t *testing.T) {
	query := rebindGrammar(compiledQuery(t, &getColumnsUser{}, Conditions{}), "postgres", querybuilder.NewPostgresGrammar(""))
	selected := query.SelectRaw("? as old", 1).Distinct("name").Select("id").(*Query)

	statement, bindings, err := selected.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select distinct on ("name") "id" from "users"`, statement)
	assert.Empty(t, bindings)
}
