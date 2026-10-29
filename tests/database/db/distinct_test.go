package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDistinctPreservesSelection pins each dialect's distinct rendering through the public preview
// API: PostgreSQL turns the column list into "distinct on (...)", MySQL keeps a plain distinct with
// backticks. The builder invariants behind the same code (DistinctFlag, Selects) live in database/db,
// where the test can reach the builder.
func TestDistinctPreservesSelection(t *testing.T) {
	for _, test := range []struct {
		driver   string
		selected string
		all      string
		count    string
	}{
		{
			driver:   "postgres",
			selected: `select distinct on ("name") "name", ? as flag from "users" where "id" = ?`,
			all:      `select distinct "name", ? as flag from "users" where "id" = ?`,
			count:    `select count(distinct "name") as aggregate from "users" where "id" = ?`,
		},
		{
			driver:   "mysql",
			selected: "select distinct `name`, ? as flag from `users` where `id` = ?",
			all:      "select distinct `name`, ? as flag from `users` where `id` = ?",
			count:    "select count(distinct `name`) as aggregate from `users` where `id` = ?",
		},
	} {
		t.Run(test.driver, func(t *testing.T) {
			base := dialectQuery(t, test.driver, "users").Select("name").SelectRaw("? as flag", []any{1}).Where("id", 9)

			selected := base.Clone().Distinct("name")
			assert.Equal(t, test.selected, selected.ToSql().Get())
			assert.Equal(t, []any{1, 9}, selected.GetBindings())

			all := selected.Clone().Distinct()
			assert.Equal(t, test.all, all.ToSql().Get())
			assert.Equal(t, []any{1, 9}, all.GetBindings())

			assert.Equal(t, test.count, selected.ToSql().Count())
		})
	}
}
