package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	querybuilder "github.com/gradii/framework/database/query"
)

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
	} {
		t.Run(test.name, func(t *testing.T) {
			query := NewQuery(context.Background(), nil, nil, test.grammar, "", nil, "users", nil)
			base := query.Select("name").(*Query)
			base.builder.SelectRaw("? as flag", []any{1}).Where("id", 9)
			columns := []string{"name"}
			selected := base.Clone().Distinct(columns...).(*Query)
			columns[0] = "id"
			all := selected.Clone().Distinct().(*Query)

			statement, bindings, err := selected.buildSelect()
			require.NoError(t, err)
			assert.Equal(t, test.selected, statement)
			assert.Equal(t, []any{1, 9}, bindings)

			statement, bindings, err = all.buildSelect()
			require.NoError(t, err)
			assert.Equal(t, test.all, statement)
			assert.Equal(t, []any{1, 9}, bindings)

			count := selected.clone()
			require.NoError(t, buildSelectForCount(count))
			statement, bindings, err = count.buildSelect()
			require.NoError(t, err)
			assert.Equal(t, test.count, statement)
			assert.Equal(t, []any{9}, bindings)

			assert.False(t, base.builder.DistinctFlag())
			assert.Len(t, base.builder.Selects(), 2)
		})
	}
}
