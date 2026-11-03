package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	querybuilder "github.com/gradii/framework/database/query"
)

// This file drives the pivot accessor surface — WithPivot / HasPivotColumn / GetPivotColumns /
// GetPivotAccessor / As / OrderByPivotDesc (item lalign-pivot-surface) — through a real
// *gorm.OrmBuilder on the recording transport. Every statement and accessor value was measured
// against the oracle (php .claude/laravel-oracle/oracle.php /tmp/lalign-probes/pivot_surface.php,
// illuminate/database v12.55.1, sqlite; captured at /tmp/lalign-probes/pivot_surface.out):
//
//   - P1a: the default list is [], the accessor 'pivot', and hasPivotColumn is false for the pivot
//     keys; P9: hasPivotColumn('lax_user_id') is true once that key itself is passed to withPivot
//     — the Go list is the WithPivot accumulation, so keys and declared-struct columns are not
//     members until passed (a documented mapping: the declared half rides the read itself);
//   - P2/P3/P4: withPivot('level') makes the executed read select
//     `..., "lax_role_user"."level" as "pivot_level"`; the raw list keeps duplicates while the
//     SELECT dedupes (unique()); chained calls append in call order and the array form keeps its
//     order (['type', 'level']);
//   - P5: as('subscription') hydrates the pivot under the new name while the aliases stay
//     pivot_-prefixed — the accessor never renames them;
//   - P8: orderByPivotDesc renders `order by "lax_role_user"."level" desc`;
//   - P10: withPivot and as return the relation object.
//
// The statements below differ from the oracle's only in the join rendering every relation read in
// this repository shares (`ON` upper-cased and the pivot-side operand first). The call-time select
// position (after the keys and the morph type, in call order) mirrors the executed oracle select
// of probe run 2 (pivot_surface2.php): `..., "ps_taggables"."taxonomy" as "pivot_taxonomy",
// "ps_taggables"."extra" as "pivot_extra"` for the declared-plus-call-time morph form.

// The aliased morph read frame with the call-time pivot-column aliases appended, each rendered
// `<pivot>.<column> as pivot_<column>` (the oracle's `as pivot_<column>` spelling).
const (
	mmMorphReadPivotType      = `select "mm_tags".*, "mm_taggables"."taggable_id" as "pivot_taggable_id", "mm_taggables"."tag_id" as "pivot_tag_id", "mm_taggables"."taggable_type" as "pivot_taggable_type", "mm_taggables"."type" as "pivot_type" from "mm_tags" inner join "mm_taggables" ON "mm_taggables"."tag_id" = "mm_tags"."id" where "mm_taggables"."taggable_id" = ? and "mm_taggables"."taggable_type" = ?`
	mmMorphReadPivotTypeExtra = `select "mm_tags".*, "mm_taggables"."taggable_id" as "pivot_taggable_id", "mm_taggables"."tag_id" as "pivot_tag_id", "mm_taggables"."taggable_type" as "pivot_taggable_type", "mm_taggables"."type" as "pivot_type", "mm_taggables"."extra" as "pivot_extra" from "mm_tags" inner join "mm_taggables" ON "mm_taggables"."tag_id" = "mm_tags"."id" where "mm_taggables"."taggable_id" = ? and "mm_taggables"."taggable_type" = ?`
)

func TestMany2Many_PivotAccessorDefaults(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	relation := mmTagsRelation(t, builder, &mmPost{ID: 1})

	// Oracle P1a (the plain form): the default list is empty, the accessor the class default —
	// Go spells it "Pivot", the declared field's name (Many2Many.PivotField's default) — and the
	// read compiles the plain aliased frame.
	assert.Nil(t, relation.GetPivotColumns())
	assert.Equal(t, "Pivot", relation.GetPivotAccessor())
	assert.False(t, relation.HasPivotColumn("type"))
	assert.False(t, relation.HasPivotColumn("taggable_id"))

	tags := []mmTag{}
	statement := mmReadStatement(t, recorder, relation, &tags)
	assert.Equal(t, mmMorphReadBase, statement.query)
}

func TestMany2Many_WithPivotExtendsTheReadSelect(t *testing.T) {
	cases := []struct {
		name   string
		apply  func(contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation
		expect string
	}{
		{"one column", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WithPivot("type")
		}, mmMorphReadPivotType},
		// The duplicate renders once — the oracle's unique() over the aliased list (P3).
		{"duplicate column", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WithPivot("type").WithPivot("type")
		}, mmMorphReadPivotType},
		// Chained calls append in call order (P4).
		{"chained columns", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WithPivot("type").WithPivot("extra")
		}, mmMorphReadPivotTypeExtra},
		// The variadic form is the array form's Go spelling (P4: order kept).
		{"variadic column list", func(r contractsorm.BelongsToManyRelation) contractsorm.BelongsToManyRelation {
			return r.WithPivot("type", "extra")
		}, mmMorphReadPivotTypeExtra},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder, recorder := newRelationTestBuilder(t)
			relation := tc.apply(mmTagsRelation(t, builder, &mmPost{ID: 1}))

			tags := []mmTag{}
			statement := mmReadStatement(t, recorder, relation, &tags)

			assert.Equal(t, tc.expect, statement.query)
			require.Len(t, statement.args, 2)
			assert.EqualValues(t, 1, deref(statement.args[0]))
			assert.Equal(t, "mm_post", statement.args[1])
		})
	}
}

func TestMany2Many_PivotColumnListAndAccessorState(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	relation := mmTagsRelation(t, builder, &mmPost{ID: 1})

	// P1a/P9: membership is the WithPivot accumulation — the keys and any declared columns are
	// not members until passed explicitly.
	relation = relation.WithPivot("type")
	assert.Equal(t, []string{"type"}, relation.GetPivotColumns())
	assert.True(t, relation.HasPivotColumn("type"))
	assert.False(t, relation.HasPivotColumn("taggable_id"))
	relation = relation.WithPivot("taggable_id")
	assert.True(t, relation.HasPivotColumn("taggable_id"))

	// P3: the raw list keeps duplicates (array_merge has no unique); the SELECT is what dedupes.
	duplicated := mmTagsRelation(t, builder, &mmPost{ID: 1}).WithPivot("type").WithPivot("type")
	assert.Equal(t, []string{"type", "type"}, duplicated.GetPivotColumns())

	// P4: chained calls append in call order; the variadic form keeps its argument order.
	ordered := mmTagsRelation(t, builder, &mmPost{ID: 1}).WithPivot("type").WithPivot("extra", "level")
	assert.Equal(t, []string{"type", "extra", "level"}, ordered.GetPivotColumns())

	// The returned slice is a copy (PHP returns arrays by value).
	reported := ordered.GetPivotColumns()
	reported[0] = "mutated"
	assert.Equal(t, []string{"type", "extra", "level"}, ordered.GetPivotColumns())

	// P5: the accessor is the As override; an empty accessor restores the declared field.
	assert.Equal(t, "TagPivot", ordered.As("TagPivot").GetPivotAccessor())
	assert.Equal(t, "Pivot", ordered.As("").GetPivotAccessor())
}

func TestMany2Many_AsKeepsTheAliasedSelectAndChains(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// P5: the accessor never renames the SQL aliases — the read keeps the plain pivot_* frame —
	// and the call returns the relation object, so the family still chains on it.
	relation := mmTagsRelation(t, builder, &mmPost{ID: 1}).As("TagPivot").WherePivot("type", "t1")

	tags := []mmTag{}
	statement := mmReadStatement(t, recorder, relation, &tags)

	assert.Equal(t, mmMorphReadBase+` and "mm_taggables"."type" = ?`, statement.query)
	require.Len(t, statement.args, 3)
	assert.Equal(t, "t1", statement.args[2])
}

func TestMany2Many_OrderByPivotDesc(t *testing.T) {
	t.Run("plain column", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)
		relation := mmTagsRelation(t, builder, &mmPost{ID: 1}).OrderByPivotDesc("type")

		tags := []mmTag{}
		statement := mmReadStatement(t, recorder, relation, &tags)

		// Oracle P8: `... order by "lax_role_user"."level" desc` (pivot-qualified, desc).
		assert.Equal(t, mmMorphReadBase+` order by "mm_taggables"."type" desc`, statement.query)
	})

	t.Run("expression column", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)
		relation := mmTagsRelation(t, builder, &mmPost{ID: 1}).
			OrderByPivotDesc(querybuilder.Expression{Value: "CONCAT(foo, '_', bar)"})

		tags := []mmTag{}
		statement := mmReadStatement(t, recorder, relation, &tags)

		// The Expression rides OrderByRaw verbatim, exactly as orderByPivot's expression path
		// (oracle run pivot_surface.php: `order by CONCAT(foo, '_', bar) desc`).
		assert.Equal(t, mmMorphReadBase+` order by CONCAT(foo, '_', bar) desc`, statement.query)
	})
}

func TestMany2Many_PivotAccessorSurfaceWithoutBuilder(t *testing.T) {
	relation := relations.Wrap(nil, nil, "Roles", relations.RelationInfo{
		Kind:            relations.KindMany2Many,
		PivotTable:      "mm_user_roles",
		PivotParentRef:  relations.RefKey{PrimaryColumn: "id", ForeignColumn: "user_id"},
		PivotRelatedRef: relations.RefKey{PrimaryColumn: "id", ForeignColumn: "role_id"},
		PivotField:      "Pivot",
	}, relations.Hooks{}).(contractsorm.BelongsToManyRelation)

	// The state half stays on the object even when the wrapped builder carries no pivot-read
	// capability (a foreign implementation): the list and accessor accessors keep reporting it,
	// and the read-select extension is the documented degradation.
	relation = relation.WithPivot("level")
	assert.Equal(t, []string{"level"}, relation.GetPivotColumns())
	assert.True(t, relation.HasPivotColumn("level"))
	assert.False(t, relation.HasPivotColumn("type"))
	assert.Equal(t, "Pivot", relation.GetPivotAccessor())
	assert.Equal(t, "subscription", relation.As("subscription").GetPivotAccessor())
	assert.Equal(t, "Pivot", relation.As("").GetPivotAccessor())
}
