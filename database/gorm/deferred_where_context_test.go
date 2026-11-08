package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// These pins cover three deferred-where regressions found in review of 2392d7ed. Each expectation is
// the Laravel oracle's (illuminate/database v12.55.1, real sqlite,
// /Users/dog/.claude/jobs/013c27a0/tmp/r15d/oracle.php -> labels quoted per test). Laravel writes an
// onlyTrashed / has clause at call time, onto the builder it is called on, against that builder's
// model; the port defers rendering to compile time and must give the same result.

// dwcPlainOnlyTrashedOr is a model without the framework soft-delete type whose global scope calls
// OnlyTrashed() (a no-op for it) and then OrWhere.
type dwcPlainOnlyTrashedOr struct{ ID uint }

func (dwcPlainOnlyTrashedOr) TableName() string { return "plain_users" }

func (dwcPlainOnlyTrashedOr) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{{Name: "x", Scope: func(q contractsorm.Query) contractsorm.Query {
		return q.OnlyTrashed().OrWhere("id", 6)
	}}}
}

// dwcPlainOr is the same scope without the no-op OnlyTrashed: the reference shape.
type dwcPlainOr struct{ ID uint }

func (dwcPlainOr) TableName() string { return "plain_users" }

func (dwcPlainOr) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{{Name: "x", Scope: func(q contractsorm.Query) contractsorm.Query {
		return q.OrWhere("id", 6)
	}}}
}

func dwcCompile(t *testing.T, q contractsorm.Query) (string, []any) {
	t.Helper()

	statement, bindings, err := q.(*OrmBuilder).addGlobalScopes().compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

// Oracle B1c: SdUser::withoutGlobalScopes()->where(fn ($q) => $q->onlyTrashed()) compiles
// `select * from "sd_users" where ("sd_users"."deleted_at" is not null)`. The Go form names the model
// only inside the closure, under a Table-only query; the entry renders against the closure's model.
func TestDeferredWhere_ClosureModelRendersOnlyTrashed(t *testing.T) {
	q := compiledQuery(t, nil, Conditions{}).Table("sd_users").Where(func(q contractsorm.Query) contractsorm.Query {
		return q.Model(&scpSdUser{}).OnlyTrashed()
	})

	statement, bindings := dwcCompile(t, q)
	assert.Equal(t, `select * from "sd_users" where ("sd_users"."deleted_at" is not null)`, statement)
	assert.Empty(t, bindings)
}

// Oracle B1b: the closure's has('posts') correlates to the closure builder's model.
func TestDeferredWhere_ClosureModelRendersHas(t *testing.T) {
	q := compiledQuery(t, nil, Conditions{}).Table("sd_users").Where(func(q contractsorm.Query) contractsorm.Query {
		return q.Model(&scpSdUser{}).Has("Posts")
	})

	statement, bindings := dwcCompile(t, q)
	assert.Equal(t, `select * from "sd_users" where (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null))`, statement)
	assert.Empty(t, bindings)
}

// Oracle B2ref: where('id', 1) plus a scope adding orWhere('id', 6) compiles `"id" = ? or ("id" =
// ?)`. Laravel has no onlyTrashed on a model without SoftDeletes; the port treats it as a no-op, so a
// scope calling it before the orWhere must group exactly like the scope without it.
func TestDeferredWhere_InertOnlyTrashedDoesNotDecideTheScopeConnector(t *testing.T) {
	reference, referenceBindings := dwcCompile(t, compiledQuery(t, &dwcPlainOr{}, Conditions{}).Where("id", 1))
	require.Equal(t, `select * from "plain_users" where "id" = ? or ("id" = ?)`, reference)
	require.Equal(t, []any{1, 6}, referenceBindings)

	statement, bindings := dwcCompile(t, compiledQuery(t, &dwcPlainOnlyTrashedOr{}, Conditions{}).Where("id", 1))
	assert.Equal(t, reference, statement)
	assert.Equal(t, referenceBindings, bindings)
}

// Oracle B3: one whereHas call runs its callback once, so the same pre-built query nested by two where
// closures carries the same clause and bindings twice ([1,1]).
func TestDeferredWhere_SharedPayloadRendersOncePerStatement(t *testing.T) {
	n := 0
	pre := compiledQuery(t, &scpSdUser{}, Conditions{}).WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query {
		n++

		return q.Where("id", n)
	})

	q := compiledQuery(t, &scpSdUser{}, Conditions{}).
		Where(func(contractsorm.Query) contractsorm.Query { return pre }).
		Where(func(contractsorm.Query) contractsorm.Query { return pre })

	exists := `(exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and "sd_posts"."deleted_at" is null))`
	statement, bindings := dwcCompile(t, q)
	assert.Equal(t, `select * from "sd_users" where `+exists+` and `+exists+` and "sd_users"."deleted_at" is null`, statement)
	assert.Equal(t, []any{1, 1}, bindings)
	assert.Equal(t, 1, n, "the callback runs once for the statement")
}

// A callback whose output varies per run must not leave the SQL and its bindings out of step: the
// placeholder count has to match the binding count.
func TestDeferredWhere_SharedPayloadKeepsPlaceholdersAndBindingsAligned(t *testing.T) {
	m := 0
	pre := compiledQuery(t, &scpSdUser{}, Conditions{}).WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query {
		m++
		ids := make([]any, m)
		for i := range ids {
			ids[i] = i + 1
		}

		return q.WhereIn("id", ids)
	})

	q := compiledQuery(t, &scpSdUser{}, Conditions{}).
		Where(func(contractsorm.Query) contractsorm.Query { return pre }).
		Where(func(contractsorm.Query) contractsorm.Query { return pre })

	statement, bindings := dwcCompile(t, q)
	placeholders := 0
	for _, r := range statement {
		if r == '?' {
			placeholders++
		}
	}
	assert.Equal(t, placeholders, len(bindings))
	assert.Equal(t, []any{1, 1}, bindings)
}
