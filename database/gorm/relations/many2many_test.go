package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasegorm "github.com/gradii/framework/database/gorm"
	querybuilder "github.com/gradii/framework/database/query"
	frameworkerrors "github.com/gradii/framework/errors"
)

// This file drives the many-to-many relation object — the wherePivot family, WithPivotValue,
// OrderByPivot and AllRelatedIds — through a real *gorm.OrmBuilder on the recording transport.
// Every statement pinned below was measured against the oracle (.claude/laravel-oracle/oracle.php
// against illuminate/database v12.55.1, sqlite, over a morphToMany post->tags relation and its
// plain many-to-many sibling). The oracle statements differ from the port's only in the join
// rendering every relation read in this repository shares (`ON` upper-cased and the pivot-side
// operand first: `inner join "taggables" ON "taggables"."tag_id" = "tags"."id"` vs the oracle's
// `inner join "taggables" on "tags"."id" = "taggables"."tag_id"`).

// --- fixtures ----------------------------------------------------------------------------------

// mmUser ports a plain many-to-many parent: the pivot mm_user_roles carries user_id / role_id.
type mmUser struct {
	ID   uint   `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`

	Roles []*mmRole `gorm:"-"`
}

func (mmUser) TableName() string { return "mm_users" }

func (mmUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{
			Related:         &mmRole{},
			Table:           "mm_user_roles",
			ForeignPivotKey: "user_id",
			RelatedPivotKey: "role_id",
		},
	}
}

type mmRole struct {
	ID   uint   `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (mmRole) TableName() string { return "mm_roles" }

// mmPost ports the corpus's taggable parent — the MorphToMany shape the wherePivot family is
// oracle-pinned against.
type mmPost struct {
	ID   uint     `gorm:"column:id;primaryKey"`
	Tags []*mmTag `gorm:"-"`
}

func (mmPost) TableName() string { return "mm_posts" }

func (mmPost) MorphClass() string { return "mm_post" }

func (mmPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related:         &mmTag{},
			Name:            "taggable",
			Table:           "mm_taggables",
			ForeignPivotKey: "taggable_id",
			RelatedPivotKey: "tag_id",
		},
	}
}

type mmTag struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (mmTag) TableName() string { return "mm_tags" }

// mmRolesRelation resolves the mmUser "Roles" relation object.
func mmRolesRelation(t *testing.T, builder *databasegorm.OrmBuilder, user *mmUser) contractsorm.BelongsToManyRelation {
	t.Helper()

	relation, ok := builder.Related(user, "Roles").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")

	return relation
}

// mmTagsRelation resolves the mmPost "Tags" relation object.
func mmTagsRelation(t *testing.T, builder *databasegorm.OrmBuilder, post *mmPost) contractsorm.BelongsToManyRelation {
	t.Helper()

	relation, ok := builder.Related(post, "Tags").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")

	return relation
}

// mmReadStatement runs the relation read into dest and returns the single recorded statement.
func mmReadStatement(t *testing.T, recorder *statementRecorder, relation contractsorm.BelongsToManyRelation, dest any) recordedStatement {
	t.Helper()

	require.NoError(t, relation.Get(dest))

	statements := recorder.take()
	require.Len(t, statements, 1)

	return statements[0]
}

// The morph and plain read frames the family clauses append to (select list and constraints are
// the pinned relation-read shape; see pivot_read_test.go in database/gorm).
const (
	mmMorphReadBase = `select "mm_tags".*, "mm_taggables"."taggable_id" as "pivot_taggable_id", "mm_taggables"."tag_id" as "pivot_tag_id", "mm_taggables"."taggable_type" as "pivot_taggable_type" from "mm_tags" inner join "mm_taggables" ON "mm_taggables"."tag_id" = "mm_tags"."id" where "mm_taggables"."taggable_id" = ? and "mm_taggables"."taggable_type" = ?`
	mmPlainReadBase = `select "mm_roles".*, "mm_user_roles"."user_id" as "pivot_user_id", "mm_user_roles"."role_id" as "pivot_role_id" from "mm_roles" inner join "mm_user_roles" ON "mm_user_roles"."role_id" = "mm_roles"."id" where "mm_user_roles"."user_id" = ?`
)

func TestMany2Many_WherePivotFamilyReadClauses(t *testing.T) {
	cases := []struct {
		name   string
		apply  func(contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation
		clause string
		extra  []any
	}{
		{"wherePivot two args", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivot("type", "t1")
		}, ` and "mm_taggables"."type" = ?`, []any{"t1"}},
		{"wherePivot operator", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivot("type", "=", "t1")
		}, ` and "mm_taggables"."type" = ?`, []any{"t1"}},
		{"wherePivot qualified string", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivot("mm_taggables.type", "t1")
		}, ` and "mm_taggables"."type" = ?`, []any{"t1"}},
		{"orWherePivot", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivot("type", "t1")
		}, ` or "mm_taggables"."type" = ?`, []any{"t1"}},
		{"wherePivotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotIn("type", []any{"t1", "t2"})
		}, ` and "mm_taggables"."type" in (?, ?)`, []any{"t1", "t2"}},
		{"wherePivotNotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotIn("type", []any{"t1"})
		}, ` and "mm_taggables"."type" not in (?)`, []any{"t1"}},
		{"orWherePivotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotIn("type", []any{"t1"})
		}, ` or "mm_taggables"."type" in (?)`, []any{"t1"}},
		{"orWherePivotNotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNotIn("type", []any{"t1"})
		}, ` or "mm_taggables"."type" not in (?)`, []any{"t1"}},
		{"wherePivotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNull("type")
		}, ` and "mm_taggables"."type" is null`, nil},
		{"wherePivotNotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotNull("type")
		}, ` and "mm_taggables"."type" is not null`, nil},
		{"orWherePivotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNull("type")
		}, ` or "mm_taggables"."type" is null`, nil},
		// Documented divergence: the plain path rides a one-clause group because the Query
		// contract has no OrWhereNotNull; the oracle renders `or "taggables"."type" is not null`.
		{"orWherePivotNotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNotNull("type")
		}, ` or ("mm_taggables"."type" is not null)`, nil},
		{"wherePivotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotBetween("type", "a", "b")
		}, ` and "mm_taggables"."type" between ? and ?`, []any{"a", "b"}},
		{"wherePivotNotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotBetween("type", "a", "b")
		}, ` and "mm_taggables"."type" not between ? and ?`, []any{"a", "b"}},
		{"orWherePivotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotBetween("type", "a", "b")
		}, ` or "mm_taggables"."type" between ? and ?`, []any{"a", "b"}},
		{"orWherePivotNotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNotBetween("type", "a", "b")
		}, ` or "mm_taggables"."type" not between ? and ?`, []any{"a", "b"}},
		{"withPivotValue", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WithPivotValue("type", "t1")
		}, ` and "mm_taggables"."type" = ?`, []any{"t1"}},
		{"orderByPivot", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrderByPivot("type")
		}, ` order by "mm_taggables"."type" asc`, nil},
		{"orderByPivot desc", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrderByPivot("type", "desc")
		}, ` order by "mm_taggables"."type" desc`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder, recorder := newRelationTestBuilder(t)
			relation := tc.apply(mmTagsRelation(t, builder, &mmPost{ID: 1}))

			tags := []mmTag{}
			statement := mmReadStatement(t, recorder, relation, &tags)

			assert.Equal(t, mmMorphReadBase+tc.clause, statement.query)
			require.Len(t, statement.args, 2+len(tc.extra))
			assert.EqualValues(t, 1, deref(statement.args[0]))
			assert.Equal(t, "mm_post", statement.args[1])
			for i, extra := range tc.extra {
				assert.EqualValues(t, extra, deref(statement.args[2+i]))
			}
		})
	}
}

func TestMany2Many_WherePivotExpressionClauses(t *testing.T) {
	expression := querybuilder.Expression{Value: "CONCAT(foo, '_', bar)"}

	cases := []struct {
		name   string
		apply  func(contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation
		clause string
		extra  []any
	}{
		{"wherePivot", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivot(expression, "=", "pivot_value")
		}, ` and CONCAT(foo, '_', bar) = ?`, []any{"pivot_value"}},
		{"orWherePivot", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivot(expression, "pivot_value")
		}, ` or CONCAT(foo, '_', bar) = ?`, []any{"pivot_value"}},
		{"withPivotValue", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WithPivotValue(expression, "pivot_value")
		}, ` and CONCAT(foo, '_', bar) = ?`, []any{"pivot_value"}},
		{"wherePivotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotBetween(expression, "pivot_value", "pivot_value")
		}, ` and CONCAT(foo, '_', bar) between ? and ?`, []any{"pivot_value", "pivot_value"}},
		{"wherePivotNotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotBetween(expression, "pivot_value", "pivot_value")
		}, ` and CONCAT(foo, '_', bar) not between ? and ?`, []any{"pivot_value", "pivot_value"}},
		{"orWherePivotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotBetween(expression, "pivot_value", "pivot_value")
		}, ` or CONCAT(foo, '_', bar) between ? and ?`, []any{"pivot_value", "pivot_value"}},
		{"orWherePivotNotBetween", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNotBetween(expression, "pivot_value", "pivot_value")
		}, ` or CONCAT(foo, '_', bar) not between ? and ?`, []any{"pivot_value", "pivot_value"}},
		{"wherePivotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotIn(expression, []any{"pivot_value"})
		}, ` and CONCAT(foo, '_', bar) in (?)`, []any{"pivot_value"}},
		{"wherePivotNotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotIn(expression, []any{"pivot_value"})
		}, ` and CONCAT(foo, '_', bar) not in (?)`, []any{"pivot_value"}},
		{"orWherePivotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotIn(expression, []any{"pivot_value"})
		}, ` or CONCAT(foo, '_', bar) in (?)`, []any{"pivot_value"}},
		{"orWherePivotNotIn", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNotIn(expression, []any{"pivot_value"})
		}, ` or CONCAT(foo, '_', bar) not in (?)`, []any{"pivot_value"}},
		{"wherePivotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNull(expression)
		}, ` and CONCAT(foo, '_', bar) is null`, nil},
		{"wherePivotNotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotNull(expression)
		}, ` and CONCAT(foo, '_', bar) is not null`, nil},
		{"orWherePivotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNull(expression)
		}, ` or CONCAT(foo, '_', bar) is null`, nil},
		// Byte-equal to the oracle's `or CONCAT(foo, '_', bar) is not null` — the raw splice has
		// no group (unlike the plain path).
		{"orWherePivotNotNull", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrWherePivotNotNull(expression)
		}, ` or CONCAT(foo, '_', bar) is not null`, nil},
		// Empty value lists render Laravel's constants (oracle-probed `and 0 = 1` / `and 1 = 1`).
		{"wherePivotIn empty", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotIn(expression, []any{})
		}, ` and 0 = 1`, nil},
		{"wherePivotNotIn empty", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotIn(expression, []any{})
		}, ` and 1 = 1`, nil},
		{"orderByPivot", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrderByPivot(expression)
		}, ` order by CONCAT(foo, '_', bar) asc`, nil},
		{"orderByPivot desc", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.OrderByPivot(expression, "desc")
		}, ` order by CONCAT(foo, '_', bar) desc`, nil},
		{"corpus notIn expression", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotIn(querybuilder.Expression{Value: "tag_id || '_' || type"}, []any{"1_t1"})
		}, ` and tag_id || '_' || type not in (?)`, []any{"1_t1"}},
		{"corpus notIn qualified expression", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WherePivotNotIn(querybuilder.Expression{Value: "mm_taggables.tag_id || '_' || mm_taggables.type"}, []any{"1_t1"})
		}, ` and mm_taggables.tag_id || '_' || mm_taggables.type not in (?)`, []any{"1_t1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder, recorder := newRelationTestBuilder(t)
			relation := tc.apply(mmTagsRelation(t, builder, &mmPost{ID: 1}))

			tags := []mmTag{}
			statement := mmReadStatement(t, recorder, relation, &tags)

			assert.Equal(t, mmMorphReadBase+tc.clause, statement.query)
			require.Len(t, statement.args, 2+len(tc.extra))
			assert.EqualValues(t, 1, deref(statement.args[0]))
			assert.Equal(t, "mm_post", statement.args[1])
			for i, extra := range tc.extra {
				assert.EqualValues(t, extra, deref(statement.args[2+i]))
			}
		})
	}
}

// TestMany2Many_ExpressionColumnsOnTheSixHelpersMirrorsTheMorphCorpus pins the combined statement
// the corpus's testQueryExpressionCanBePassedToDifferentPivotQueryBuilderClauses drives through
// the six pivot-clause helpers — wherePivot, withPivotValue, wherePivotBetween, wherePivotIn,
// wherePivotNull, orderByPivot — with one Expression column and one value. Oracle (sqlite):
// ... and CONCAT(foo, '_', bar) = ? and CONCAT(foo, '_', bar) = ? and CONCAT(foo, '_', bar)
// between ? and ? and CONCAT(foo, '_', bar) in (?) and CONCAT(foo, '_', bar) is null
// order by CONCAT(foo, '_', bar) asc, bindings [1, the parent class, 'pivot_value' x5].
func TestMany2Many_ExpressionColumnsOnTheSixHelpersMirrorsTheMorphCorpus(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	expression := querybuilder.Expression{Value: "CONCAT(foo, '_', bar)"}
	relation := mmTagsRelation(t, builder, &mmPost{ID: 1}).
		WherePivot(expression, "=", "pivot_value").
		WithPivotValue(expression, "pivot_value").
		WherePivotBetween(expression, "pivot_value", "pivot_value").
		WherePivotIn(expression, []any{"pivot_value"}).
		WherePivotNull(expression).
		OrderByPivot(expression)

	tags := []mmTag{}
	statement := mmReadStatement(t, recorder, relation, &tags)

	assert.Equal(t, mmMorphReadBase+` and CONCAT(foo, '_', bar) = ? and CONCAT(foo, '_', bar) = ? and CONCAT(foo, '_', bar) between ? and ? and CONCAT(foo, '_', bar) in (?) and CONCAT(foo, '_', bar) is null order by CONCAT(foo, '_', bar) asc`, statement.query)
	require.Len(t, statement.args, 7)
	assert.EqualValues(t, 1, deref(statement.args[0]))
	assert.Equal(t, "mm_post", statement.args[1])
	for i := 2; i < 7; i++ {
		assert.Equal(t, "pivot_value", statement.args[i])
	}
}

// TestMany2Many_WithPivotValueNilPairsTheParentPivotKey pins the frozen contract's argument swap:
// with a nil value the argument column pairs with the pivot's parent-side foreign key column (the
// value becomes the clause's value), and the swapped pair is the one recorded for the pivot
// construction. Laravel 12.55.1 throws InvalidArgumentException there — the swap is the frozen Go
// contract's pre-12 shape (oracle-probed message: "The provided value may not be null.").
func TestMany2Many_WithPivotValueNilPairsTheParentPivotKey(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	relation := mmRolesRelation(t, builder, &mmUser{ID: 7}).WithPivotValue("77", nil)

	roles := []mmRole{}
	statement := mmReadStatement(t, recorder, relation, &roles)

	assert.Equal(t, mmPlainReadBase+` and "mm_user_roles"."user_id" = ?`, statement.query)
	require.Len(t, statement.args, 2)
	assert.EqualValues(t, 7, deref(statement.args[0]))
	assert.EqualValues(t, "77", deref(statement.args[1]))

	// The recorded pair is the applied one, so a pivot built from this relation merges it — the
	// pair every other withPivotValue call records is the clause's pair too.
	pivot, err := relation.NewPivot(nil, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"user_id": "77"}, pivot.Attributes())
}

// TestMany2Many_OrderByPivotExpressionInvalidDirectionRecordsTheOrderError: the expression path
// delegates direction validation to OrderBy, so an invalid direction records the same error the
// structured path records (Laravel throws InvalidArgumentException).
func TestMany2Many_OrderByPivotExpressionInvalidDirectionRecordsTheOrderError(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	relation := mmTagsRelation(t, builder, &mmPost{ID: 1}).
		OrderByPivot(querybuilder.Expression{Value: "CONCAT(foo, '_', bar)"}, "sideways")

	tags := []mmTag{}
	err := relation.Get(&tags)

	require.Error(t, err)
	assert.ErrorIs(t, err, frameworkerrors.QueryOrderDirectionInvalid)
}

// TestMany2Many_AllRelatedIdsPlucksTheRelatedPivotKey pins the reachable form of Eloquent's
// allRelatedIds: Laravel runs a fresh pivot statement (`select "tag_id" from "taggables" where
// ...`), which the object layer cannot construct (no newQuery/from on the Query contract), so the
// Go form plucks the related pivot key through the pivot-aware relation read — the statement
// Laravel's `$relation->pluck($relatedPivotKey)` compiles — with any call-time wherePivot clause
// already recorded riding along.
func TestMany2Many_AllRelatedIdsPlucksTheRelatedPivotKey(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)
		relation := mmTagsRelation(t, builder, &mmPost{ID: 1})

		ids := []uint{}
		require.NoError(t, relation.AllRelatedIds(&ids))

		statements := recorder.take()
		require.Len(t, statements, 1)
		assert.Equal(t, `select "tag_id" from "mm_tags" inner join "mm_taggables" ON "mm_taggables"."tag_id" = "mm_tags"."id" where "mm_taggables"."taggable_id" = ? and "mm_taggables"."taggable_type" = ?`, statements[0].query)
		require.Len(t, statements[0].args, 2)
		assert.EqualValues(t, 1, deref(statements[0].args[0]))
		assert.Equal(t, "mm_post", statements[0].args[1])
		assert.Empty(t, ids)
	})

	t.Run("wherePivot scoping rides the statement", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)
		relation := mmTagsRelation(t, builder, &mmPost{ID: 1}).WherePivot("type", "t1")

		ids := []uint{}
		require.NoError(t, relation.AllRelatedIds(&ids))

		statements := recorder.take()
		require.Len(t, statements, 1)
		assert.Equal(t, `select "tag_id" from "mm_tags" inner join "mm_taggables" ON "mm_taggables"."tag_id" = "mm_tags"."id" where "mm_taggables"."taggable_id" = ? and "mm_taggables"."taggable_type" = ? and "mm_taggables"."type" = ?`, statements[0].query)
		require.Len(t, statements[0].args, 3)
		assert.Equal(t, "t1", statements[0].args[2])
	})
}
