package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the "preserved callbacks" family of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php: a closure registered with beforeQuery() must run once,
// before a query is compiled, and its effect must be reflected in the SQL every entry point produces.
//
// Laravel's write cases assert through a connection or processor spy; what the spy receives is the
// compiled statement, which is what the write cases here compare directly against ToInsertSQL,
// ToInsertGetIdSQL, ToUpsertSQL, ToUpdateSQL, ToDeleteSQL, ToTruncateSQL and ToExistsSQL, together
// with the bindings. Every compile entry point calls ApplyBeforeQueryCallbacks at its head
// (Builder::beforeQuery and Builder::applyBeforeQueryCallbacks, corpus L3014-3034).
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php. The line ranges name the corpus method's slice.
//
// testPreservedAreAppliedByInsertUsing (corpus L4596-4605) is ported in
// laravel_port_fix_b9_builder_insertusing_builder_toinsertusingsql_test.go, alongside the InsertUsing
// builder method it exercises; Laravel's insertUsing applies the same callbacks at its head.

// TestPreservedAreAppliedByToSql ports Laravel's testPreservedAreAppliedByToSql (corpus L4565-4574).
// The callback's where() becomes part of the compiled select — with no from clause, which Laravel
// also omits — and its binding is carried by the builder.
func TestPreservedAreAppliedByToSql(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"before query applies to toSql", func(b *Builder) *Builder {
			return b.BeforeQuery(func(q *Builder) *Builder { return q.Where("foo", "bar") })
		}, `select * where "foo" = ?`},
	})

	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q.Where("foo", "bar") })
	b.ToSQL()

	assert.Equal(t, []any{"bar"}, b.GetBindings())
}

// TestPreservedAreAppliedByInsert ports Laravel's testPreservedAreAppliedByInsert (corpus L4575-4584):
// the callback supplies the from() the insert needs.
func TestPreservedAreAppliedByInsert(t *testing.T) {
	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })

	sql, bindings := b.ToInsertSQL([]map[string]any{{"email": "foo"}})

	assert.Equal(t, `insert into "users" ("email") values (?)`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

// TestPreservedAreAppliedByInsertGetId ports Laravel's testPreservedAreAppliedByInsertGetId
// (corpus L4585-4595).
func TestPreservedAreAppliedByInsertGetId(t *testing.T) {
	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })

	sql, bindings := b.ToInsertGetIdSQL([]map[string]any{{"email": "foo"}}, "id")

	assert.Equal(t, `insert into "users" ("email") values (?)`, sql)
	assert.Equal(t, []any{"foo"}, bindings)
}

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

// TestPreservedAreAppliedByUpdate ports Laravel's testPreservedAreAppliedByUpdate (corpus L4627-4636):
// the from() is on the builder and the callback adds the where().
func TestPreservedAreAppliedByUpdate(t *testing.T) {
	b := newTestBuilder().From("users", "")
	b.BeforeQuery(func(q *Builder) *Builder { return q.Where("id", 1) })

	sql, bindings := b.ToUpdateSQL(map[string]any{"email": "foo"})

	assert.Equal(t, `update "users" set "email" = ? where "id" = ?`, sql)
	assert.Equal(t, []any{"foo", 1}, bindings)
}

// TestPreservedAreAppliedByDelete ports Laravel's testPreservedAreAppliedByDelete (corpus L4637-4646).
func TestPreservedAreAppliedByDelete(t *testing.T) {
	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })

	sql, bindings := b.ToDeleteSQL()

	assert.Equal(t, `delete from "users"`, sql)
	assert.Empty(t, bindings)
}

// TestPreservedAreAppliedByTruncate ports Laravel's testPreservedAreAppliedByTruncate
// (corpus L4647-4656). The base grammar emits one truncate statement.
func TestPreservedAreAppliedByTruncate(t *testing.T) {
	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })

	statements := b.ToTruncateSQL()

	assert.Len(t, statements, 1)
	assert.Equal(t, `truncate table "users"`, statements[0].SQL)
}

// TestPreservedAreAppliedByExists ports Laravel's testPreservedAreAppliedByExists (corpus L4657-4666).
func TestPreservedAreAppliedByExists(t *testing.T) {
	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q.From("users", "") })

	sql, bindings := b.ToExistsSQL()

	assert.Equal(t, `select exists(select * from "users") as "exists"`, sql)
	assert.Empty(t, bindings)
}

// TestApplyPreserveCleansArray ports Laravel's testApplyPreserveCleansArray (corpus L4555-4563):
// registering one before-query callback leaves one queued, and ApplyBeforeQueryCallbacks drains the
// queue to empty. The Go builder keeps Laravel's $beforeQueryCallbacks as the unexported
// beforeQueryCallbacks field, so this assertion lives in-package. Laravel's callback takes no
// arguments and returns nothing; the Go shape receives and returns the builder, so the closure
// returns its receiver.
func TestApplyPreserveCleansArray(t *testing.T) {
	b := newTestBuilder()
	b.BeforeQuery(func(q *Builder) *Builder { return q })

	assert.Len(t, b.beforeQueryCallbacks, 1)

	b.ApplyBeforeQueryCallbacks()

	assert.Len(t, b.beforeQueryCallbacks, 0)
}
