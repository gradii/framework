package gorm

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// These pins cover three deferred-where regressions found in review of 2392d7ed. Each expectation is
// the Laravel oracle's (illuminate/database v12.55.1, real sqlite,
// /Users/dog/.claude/jobs/013c27a0/tmp/r15d/oracle.php -> labels quoted per test). Laravel writes an
// onlyTrashed / has clause at call time, onto the builder it is called on, against that builder's
// model; the port defers rendering to compile time and must give the same result.

// dwcOnlyTrashedOr is the "x" scope the B2ref pin mounts on the builder: a no-op OnlyTrashed() for
// a model without the framework soft-delete type, then OrWhere. It used to be declared
// GlobalScopes(); scopes mount on the builder with ApplyScope now.
func dwcOnlyTrashedOr(q contractsorm.Query) contractsorm.Query {
	return q.OnlyTrashed().OrWhere("id", 6)
}

// dwcPlainTrashedOr is the same scope without the no-op OnlyTrashed: the reference shape.
func dwcPlainTrashedOr(q contractsorm.Query) contractsorm.Query {
	return q.OrWhere("id", 6)
}

// dwcPlainOnlyTrashedOr is the model the inert-scope pin compiles over, with dwcOnlyTrashedOr
// mounted at the test's query-construction site.
type dwcPlainOnlyTrashedOr struct{ ID uint }

func (dwcPlainOnlyTrashedOr) TableName() string { return "plain_users" }

// dwcPlainOr is the same model with the no-op-free dwcPlainTrashedOr mounted: the reference shape.
type dwcPlainOr struct{ ID uint }

func (dwcPlainOr) TableName() string { return "plain_users" }

func dwcCompile(t *testing.T, q contractsorm.Query) (string, []any) {
	t.Helper()

	statement, bindings, err := q.(*OrmBuilder).compileSelect()
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
	reference, referenceBindings := dwcCompile(t, compiledQuery(t, &dwcPlainOr{}, Conditions{}).
		ApplyScope("x", dwcPlainTrashedOr).(*OrmBuilder).Where("id", 1))
	require.Equal(t, `select * from "plain_users" where "id" = ? or ("id" = ?)`, reference)
	require.Equal(t, []any{1, 6}, referenceBindings)

	statement, bindings := dwcCompile(t, compiledQuery(t, &dwcPlainOnlyTrashedOr{}, Conditions{}).
		ApplyScope("x", dwcOnlyTrashedOr).(*OrmBuilder).Where("id", 1))
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

// --- second review of 22aaa27d: the model is the closure's final one, grouping is decided on the
// statement copy. Oracle: /Users/dog/.claude/jobs/013c27a0/tmp/r15e/oracle.php (v12.55.1, real sqlite).

type dwcNode struct {
	ID       uint
	ParentID uint
	Children []*dwcNode `gorm:"-"`
	databaseorm.SoftDeletes
}

func (dwcNode) TableName() string { return "nodes" }

func (dwcNode) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{"Children": contractsorm.HasMany{Related: &dwcNode{}, ForeignKey: "parent_id"}}
}

type dwcProjection struct {
	ID    uint
	Email string
}

func (dwcProjection) TableName() string { return "sd_users" }

type dwcPlain struct{ ID uint }

func (dwcPlain) TableName() string { return "plain_users" }

// Oracle F1: SdUser::onlyTrashed() selects and updates with `"sd_users"."deleted_at" is not null`. A
// reused Table query previewed into a plain projection first must not lose that predicate when a
// scope adds OnlyTrashed: no decision may be made against the earlier read's destination.
func TestDeferredWhere_EarlierDestinationDoesNotPruneOnlyTrashed(t *testing.T) {
	q := compiledQuery(t, nil, Conditions{}).Table("sd_users")
	_ = q.ToSql().Get(&[]dwcProjection{})
	q = q.Scopes(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() })

	assert.Equal(t, `select * from "sd_users" where "sd_users"."deleted_at" is not null`, q.ToSql().Get(&[]scpSdUser{}))
	assert.Equal(t, `update "sd_users" set "email" = ? where "sd_users"."deleted_at" is not null`, q.Model(&scpSdUser{}).ToSql().Update("email", "x"))
}

// Oracle F2: Node::whereHas('children', fn ($q) => $q->onlyTrashed()) filters the self-join alias. A
// slice-shaped model on the callback query names the same table, so the alias is kept.
func TestDeferredWhere_SliceModelKeepsTheSelfJoinAlias(t *testing.T) {
	q := compiledQuery(t, &dwcNode{}, Conditions{}).WhereHas("Children", func(q contractsorm.Query) contractsorm.Query {
		return q.Model(&[]dwcNode{}).OnlyTrashed()
	})

	statement, bindings := dwcCompile(t, q)
	assert.Regexp(t, regexp.MustCompile(`^select \* from "nodes" where exists \(select \* from "nodes" as "laravel_reserved_\d+" where "nodes"\."id" = "laravel_reserved_\d+"\."parent_id" and "laravel_reserved_\d+"\."deleted_at" is not null\) and "nodes"\."deleted_at" is null$`), statement)
	assert.Empty(t, bindings)
}

// The closure's model counts whenever the closure sets it, before or after its OnlyTrashed / Has call
// (oracle B1c/B1b shapes).
func TestDeferredWhere_ClosureModelSetAfterTheCall(t *testing.T) {
	trashed := compiledQuery(t, nil, Conditions{}).Table("sd_users").Where(func(q contractsorm.Query) contractsorm.Query {
		return q.OnlyTrashed().Model(&scpSdUser{})
	})
	statement, _ := dwcCompile(t, trashed)
	assert.Equal(t, `select * from "sd_users" where ("sd_users"."deleted_at" is not null)`, statement)

	has := compiledQuery(t, nil, Conditions{}).Table("sd_users").Where(func(q contractsorm.Query) contractsorm.Query {
		return q.Has("Posts").Model(&scpSdUser{})
	})
	statement, _ = dwcCompile(t, has)
	assert.Equal(t, `select * from "sd_users" where (exists (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "sd_posts"."deleted_at" is null))`, statement)
}

// Oracle F4 / F4b: where('id', 1) then a scope adding orWhere('id', 6) - with or without an empty
// where closure first - groups as `"id" = ? or ("id" = ?)`. An inert OnlyTrashed in the scope, top
// level or inside a closure, with the model known only from the read's destination, gives the same.
func TestDeferredWhere_InertOnlyTrashedLateModelOrNested(t *testing.T) {
	const want = `select * from "plain_users" where "id" = ? or ("id" = ?)`

	late := compiledQuery(t, nil, Conditions{}).Table("plain_users").Where("id", 1).
		Scopes(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed().OrWhere("id", 6) })
	assert.Equal(t, want, late.ToSql().Get(&[]dwcPlain{}))

	nested := compiledQuery(t, nil, Conditions{}).Table("plain_users").Where("id", 1).
		Scopes(func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() }).OrWhere("id", 6)
		})
	assert.Equal(t, want, nested.ToSql().Get(&[]dwcPlain{}))

	// The query object is not changed by compiling against a destination.
	assert.Equal(t, want, late.ToSql().Get(&[]dwcPlain{}))
}

// Oracle F2c: Node::whereHas('children', fn ($q) => $q->where(fn ($q) => $q->onlyTrashed())) qualifies
// the closure's predicate with the self-join alias - the closure's builder shares the related model,
// whose table Laravel set to the alias - and so does a closure naming the related model as a slice.
func TestDeferredWhere_ClosureInsideASelfRelationCallbackUsesTheAlias(t *testing.T) {
	want := regexp.MustCompile(`^select \* from "nodes" where exists \(select \* from "nodes" as "(laravel_reserved_\d+)" where "nodes"\."id" = "laravel_reserved_\d+"\."parent_id" and \("laravel_reserved_\d+"\."deleted_at" is not null\) and "laravel_reserved_\d+"\."deleted_at" is null\) and "nodes"\."deleted_at" is null$`)

	for name, closure := range map[string]func(contractsorm.Query) contractsorm.Query{
		"no model":    func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() },
		"slice model": func(q contractsorm.Query) contractsorm.Query { return q.Model(&[]dwcNode{}).OnlyTrashed() },
	} {
		t.Run(name, func(t *testing.T) {
			q := compiledQuery(t, &dwcNode{}, Conditions{}).WhereHas("Children", func(q contractsorm.Query) contractsorm.Query {
				return q.Where(closure)
			})

			statement, _ := dwcCompile(t, q)
			assert.Regexp(t, want, statement)
		})
	}
}
