package db

import (
	"context"
	mysqldialect "github.com/gradii/mysql"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/contracts/database/db"
	querybuilder "github.com/gradii/framework/database/query"
)

// newWhereAnyQuery builds a bare facade query over "users" the tests below can extend. It needs no
// connection: ToSql compiles the statement without executing it.
func newWhereAnyQuery() *Query {
	return newDialectTestQuery(
		context.Background(), nil, nil,
		mysqldialect.NewQueryGrammar(""), querybuilder.DialectMySQL,
		nil, "users", nil,
	)
}

// TestWhereAnyFamilyExpandsInSlice pins the shared behaviour of the DB facade's WhereAny / WhereAll
// / WhereNone with an IN (or NOT IN) whose value is a slice. The DB facade and the ORM must agree:
// both expand the slice to one placeholder per element per column - the shape the sibling WhereIn
// entry point already gives - and an empty list renders the neutral "0 = 1" / "1 = 1" constant
// rather than the malformed "column in ()" every driver rejects. The ORM pins the same cases in
// database/gorm/where_any_slice_test.go.
//
// Before this the facade routed the operator through the builder's where() path, whose
// invalid-operator shortcut swapped "IN" to "=" and bound the literal string "IN" (exactly what
// Laravel's own whereAny does: `whereAny(cols, 'IN', [...])` compiles to `col = ?` bound to "IN"),
// so the clause silently matched nothing and the two facades disagreed.
func TestWhereAnyFamilyExpandsInSlice(t *testing.T) {
	tests := []struct {
		name     string
		build    func(db.Query) db.Query
		want     string
		bindings []any
	}{
		{
			"whereAny IN expands per column",
			func(q db.Query) db.Query { return q.WhereAny([]string{"name", "email"}, "IN", []any{"one", "two"}) },
			"select * from `users` where (`name` in (?, ?) or `email` in (?, ?))",
			[]any{"one", "two", "one", "two"},
		},
		{
			"whereAll IN expands per column",
			func(q db.Query) db.Query { return q.WhereAll([]string{"name", "email"}, "IN", []any{"one", "two"}) },
			"select * from `users` where (`name` in (?, ?) and `email` in (?, ?))",
			[]any{"one", "two", "one", "two"},
		},
		{
			"whereNone IN expands per column and negates the group",
			func(q db.Query) db.Query { return q.WhereNone([]string{"name", "email"}, "IN", []any{"one", "two"}) },
			"select * from `users` where not (`name` in (?, ?) or `email` in (?, ?))",
			[]any{"one", "two", "one", "two"},
		},
		{
			"whereAny NOT IN expands per column",
			func(q db.Query) db.Query { return q.WhereAny([]string{"name"}, "NOT IN", []any{"one", "two"}) },
			"select * from `users` where (`name` not in (?, ?))",
			[]any{"one", "two"},
		},
		{
			"whereAny empty IN matches nothing",
			func(q db.Query) db.Query { return q.WhereAny([]string{"name"}, "IN", []any{}) },
			"select * from `users` where (0 = 1)",
			[]any{},
		},
		{
			"whereAll empty IN matches nothing",
			func(q db.Query) db.Query { return q.WhereAll([]string{"name", "email"}, "IN", []any{}) },
			"select * from `users` where (0 = 1 and 0 = 1)",
			[]any{},
		},
		{
			"whereNone empty IN matches everything",
			func(q db.Query) db.Query { return q.WhereNone([]string{"name"}, "IN", []any{}) },
			"select * from `users` where not (0 = 1)",
			[]any{},
		},
		{
			"whereAny empty NOT IN matches everything",
			func(q db.Query) db.Query { return q.WhereAny([]string{"name"}, "NOT IN", []any{}) },
			"select * from `users` where (1 = 1)",
			[]any{},
		},
		{
			"scalar operator is unchanged",
			func(q db.Query) db.Query { return q.WhereAny([]string{"name", "email"}, "=", "x") },
			"select * from `users` where (`name` = ? or `email` = ?)",
			[]any{"x", "x"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := test.build(newWhereAnyQuery()).(*Query)

			assert.Equal(t, test.want, query.ToSql().Get())
			assert.Equal(t, test.bindings, query.builder.Bindings()["where"])
		})
	}
}
