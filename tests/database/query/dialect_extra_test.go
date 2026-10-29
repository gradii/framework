package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file keeps the dialect-specific SQL the framework root can no longer compile. The root now runs
// on the base grammar alone (it no longer depends on the driver modules), so a clause a driver spells
// differently is pinned here against the real driver grammars. Each case mirrors a root test that
// asserted the clause with a driver grammar before the drivers moved out of the framework's module.

// TestDistinctOnPerDialect mirrors database/gorm's TestDistinctPreservesSelection: Postgres keeps the
// DISTINCT columns in a "distinct on (...)" clause, while every other dialect drops the column list and
// renders a plain "distinct". The count(distinct ...) aggregate follows the same split.
func TestDistinctOnPerDialect(t *testing.T) {
	for _, test := range []struct {
		name     string
		grammar  Grammar
		selected string
		all      string
		count    string
	}{
		{
			name:     "postgres",
			grammar:  NewPostgresGrammar(""),
			selected: `select distinct on ("name") "name", ? as flag from "users" where "id" = ?`,
			all:      `select distinct "name", ? as flag from "users" where "id" = ?`,
			count:    `select count(distinct "name") as aggregate from "users" where "id" = ?`,
		},
		{
			name:     "mysql",
			grammar:  NewMySqlGrammar(""),
			selected: "select distinct `name`, ? as flag from `users` where `id` = ?",
			all:      "select distinct `name`, ? as flag from `users` where `id` = ?",
			count:    "select count(distinct `name`) as aggregate from `users` where `id` = ?",
		},
		{
			name:     "sqlite",
			grammar:  NewSqliteGrammar(""),
			selected: `select distinct "name", ? as flag from "users" where "id" = ?`,
			all:      `select distinct "name", ? as flag from "users" where "id" = ?`,
			count:    `select count(distinct "name") as aggregate from "users" where "id" = ?`,
		},
		{
			name:     "sqlserver",
			grammar:  NewSqlServerGrammar(""),
			selected: "select distinct [name], ? as flag from [users] where [id] = ?",
			all:      "select distinct [name], ? as flag from [users] where [id] = ?",
			count:    "select count(distinct [name]) as aggregate from [users] where [id] = ?",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := NewBuilder(test.grammar).From("users", "").Select("name").SelectRaw("? as flag", []any{1}).Where("id", 9)
			selected := base.Clone().Distinct("name")

			sql, bindings := selected.ToSelectSQL()
			assert.Equal(t, test.selected, sql)
			assert.Equal(t, []any{1, 9}, bindings)

			all := selected.Clone().Distinct()
			sql, bindings = all.ToSelectSQL()
			assert.Equal(t, test.all, sql)
			assert.Equal(t, []any{1, 9}, bindings)

			sql, bindings = selected.ToAggregateSQL(core.Aggregate{Function: "count", Columns: []any{"*"}})
			assert.Equal(t, test.count, sql)
			assert.Equal(t, []any{9}, bindings)
		})
	}
}

// TestSelectKeepsDistinctColumns mirrors database/gorm's TestSelectPreservesDistinctColumns: a later
// Select replaces the projection but leaves the DISTINCT columns in place, which is what keeps the
// Postgres "distinct on (...)" columns alive across a Select.
func TestSelectKeepsDistinctColumns(t *testing.T) {
	selected := NewBuilder(NewPostgresGrammar("")).From("users", "").
		SelectRaw("? as old", []any{1}).Distinct("name").Select("id")

	sql, bindings := selected.ToSelectSQL()
	assert.Equal(t, `select distinct on ("name") "id" from "users"`, sql)
	assert.Empty(t, bindings)
}

// TestPostgresUpsertKeepsEveryUpdateColumn mirrors database/gorm's
// TestUpsertStampsTimestampsAndUpdatesAllButCreatedAt: the upsert updates every inserted column except
// created_at, and always refreshes updated_at, which Postgres renders as "on conflict ... do update
// set ... = excluded...". The base grammar refuses upserts, so the statement is dialect specific.
func TestPostgresUpsertKeepsEveryUpdateColumn(t *testing.T) {
	builder := NewBuilder(NewPostgresGrammar("")).From("shortcut_users", "")
	records := []map[string]any{
		{"created_at": 1, "name": "a", "score": 1, "updated_at": 1},
		{"created_at": 1, "name": "b", "score": 2, "updated_at": 1},
	}
	updates := []core.UpsertUpdate{{Column: "name"}, {Column: "score"}, {Column: "updated_at"}}

	statement, _ := builder.ToUpsertSQL(records, []string{"name"}, updates)
	assert.Equal(t, `insert into "shortcut_users" ("created_at", "name", "score", "updated_at") values (?, ?, ?, ?), (?, ?, ?, ?) on conflict ("name") do update set "name" = "excluded"."name", "score" = "excluded"."score", "updated_at" = "excluded"."updated_at"`, statement)
}
