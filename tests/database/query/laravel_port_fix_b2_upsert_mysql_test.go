package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// TestPreservedAreAppliedByUpsert ports the use_upsert_alias=false branch of Laravel's
// testPreservedAreAppliedByUpsert (corpus L4606-4626): the MySQL builder compiles the from() the
// callback supplies. The use_upsert_alias=true branch belongs to the MySQL upsert-alias work item,
// not to the preserve mechanism, and is not expressible here yet.
func TestPreservedAreAppliedByUpsert(t *testing.T) {
	b := NewBuilder(NewMySqlGrammar(""))
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })

	sql, bindings := b.ToUpsertSQL([]map[string]any{{"email": "foo"}}, []string{"id"}, []core.UpsertUpdate{{Column: "email"}})

	assert.Equal(t, "insert into `users` (`email`) values (?) on duplicate key update `email` = values(`email`)", sql)
	assert.Equal(t, []any{"foo"}, bindings)
}
