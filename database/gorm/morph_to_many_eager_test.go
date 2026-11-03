package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/gradii/framework/database/gorm/relations"
	"github.com/stretchr/testify/assert"
)

// TestLoadMorphToMany_ReadsAreCompiled pins the child builders the morph many-to-many eager loader is
// built from and the statement it runs: the joined relation read (the related table joined to the
// pivot, carrying the morph-type predicate that separates it from a plain Many2Many read, wearing the
// aliased pivot select list), addressed through the package's child builders so the framework compiler
// renders the statement instead of GORM. The loader is driven with parent keys too, so a change to its
// body fails the test.
//
// The binding slice is asserted exactly, since its order and arity carry the chunk keys and the morph
// value.
func TestLoadMorphToMany_ReadsAreCompiled(t *testing.T) {
	t.Run("pivot read without a pivot scope", func(t *testing.T) {
		q := newRelQueryWith(t, &morphPost{})
		desc, err := relations.ResolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.Equal(t, relKindMorphToMany, desc.Kind)
		assert.Equal(t, "taggables", desc.PivotTable)
		assert.Equal(t, "taggable_id", desc.PivotParentRef.ForeignColumn)
		assert.Equal(t, "morph_tag_id", desc.PivotRelatedRef.ForeignColumn)
		assert.Equal(t, "taggable_type", desc.MorphTypeColumn)
		assert.Equal(t, "morph_tags", desc.RelatedTable)

		cols := []string{desc.PivotParentRef.ForeignColumn, desc.PivotRelatedRef.ForeignColumn}
		chunk := []any{uint(1), uint(2)}

		// The child is named the way the loader names it: the pivot table verbatim, the IN column
		// unquoted and table-qualified, the select list bare so the returned column names — the map
		// keys the hydration loop indexes — stay the FK names, and the morph-type predicate the
		// descriptor carries.
		child := q.pivotChild(desc.PivotTable, desc.PivotTable+"."+desc.PivotParentRef.ForeignColumn, chunk, cols).
			Where(desc.PivotTable+"."+desc.MorphTypeColumn, desc.MorphValue).(*OrmBuilder)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t,
			`select "taggable_id", "morph_tag_id" from "taggables" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ?`,
			compiled)
		assert.Equal(t, []any{uint(1), uint(2), desc.MorphValue}, bindings)

		// No model is attached, so the child starts from the pivot table alone and inherits none of
		// the parent query's state — the same shape the fresh GORM session had. The morph-type
		// predicate is the second where, which is what makes this read differ from Many2Many's.
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.query.State().Wheres, 2)
		assert.Len(t, child.query.Selects(), 2)
	})

	t.Run("pivot read receives the relation scope", func(t *testing.T) {
		q := newRelQueryWith(t, &scopeMorphPost{})
		desc, err := relations.ResolveRelation(&scopeMorphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.OnPivotQuery, "the fixture declares OnPivotQuery")

		// scopeMorphTag declares no Pivot field, so the select list stays the two FK columns.
		plan, err := preparePivotHydration(q, desc)
		assert.NoError(t, err)
		assert.Nil(t, plan)

		cols := []string{desc.PivotParentRef.ForeignColumn, desc.PivotRelatedRef.ForeignColumn}
		chunk := []any{uint(1), uint(2)}

		child := q.pivotChild(desc.PivotTable, desc.PivotTable+"."+desc.PivotParentRef.ForeignColumn, chunk, cols).
			Where(desc.PivotTable+"."+desc.MorphTypeColumn, desc.MorphValue).(*OrmBuilder)
		scoped, ok := applyOnPivotQueryToQuery(q.queryGrammar, child, desc).(*OrmBuilder)
		assert.True(t, ok)

		compiled, bindings, err := scoped.compileSelect()
		assert.NoError(t, err)
		// Order: parent IN clause, morph-type clause, then OnPivotQuery scope.
		assert.Equal(t,
			`select "taggable_id", "scope_morph_tag_id" from "taggables" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ? and "taggables"."deleted_at" is null`,
			compiled)
		// The chunk's keys and the morph value first, then the scope's own binding (none here: the
		// scope is an `is null` predicate).
		assert.Equal(t, []any{uint(1), uint(2), desc.MorphValue}, bindings)

	})

	t.Run("related read is compiled", func(t *testing.T) {
		q := newRelQueryWith(t, &morphPost{})
		desc, err := relations.ResolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		chunk := []any{uint(9)}
		child := q.relatedChild(desc.RelatedTable, desc.RelatedTable+"."+desc.PivotRelatedRef.PrimaryColumn, chunk)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select * from "morph_tags" where "morph_tags"."id" in (?)`, compiled)
		assert.Equal(t, chunk, bindings)
	})

	// MorphedByMany is the same loader with the morph value taken from the related side; the
	// descriptor is the only place that differs, so both directions must pin the same value.
	t.Run("inverse pins the same morph value as the forward relation", func(t *testing.T) {
		forwardDesc, err := relations.ResolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)

		inverseDesc, err := relations.ResolveRelation(&morphTag{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.Equal(t, relKindMorphToMany, inverseDesc.Kind)
		assert.Equal(t, "taggables", inverseDesc.PivotTable)
		assert.Equal(t, "morph_post_id", inverseDesc.PivotRelatedRef.ForeignColumn)
		assert.Equal(t, forwardDesc.MorphValue, inverseDesc.MorphValue)
	})

	// No parent keys: the loader takes its empty path and never reaches a statement at all, so the
	// relation field is cleared rather than queried for an IN list that cannot match.
	t.Run("no parent keys issues no statement", func(t *testing.T) {
		q := newRelQueryWith(t, &morphPost{})
		desc, err := relations.ResolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.NoError(t, q.loadMorphToMany(nil, &morphPost{}, desc, eagerLoadEntry{Relation: "Tags"}, nil))
	})
	// Drive the loader itself with parent keys. The loader reads the related rows through the joined
	// relation query, so one statement lands: the pivot join carrying the morph-type predicate, wearing
	// the aliased pivot select list.
	t.Run("loader reads the joined relation query", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &morphPost{})
		desc, err := relations.ResolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		recorder.returnRows(`from "morph_tags"`, []string{"id", "name", "pivot_taggable_id", "pivot_taggable_type"},
			[]driver.Value{int64(9), "tag", int64(1), desc.MorphValue})

		parents := []reflect.Value{
			reflect.ValueOf(&morphPost{ID: 1}).Elem(),
			reflect.ValueOf(&morphPost{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadMorphToMany(parents, &morphPost{}, desc, eagerLoadEntry{Relation: "Tags"}, nil))
		assert.Equal(t, 1, recorder.count(), "the loader reads the related rows through the joined relation query")

		joined := recorder.at(t, 0)
		assert.Equal(t,
			`select "morph_tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."morph_tag_id" as "pivot_morph_tag_id", "taggables"."taggable_type" as "pivot_taggable_type" from "morph_tags" inner join "taggables" ON "taggables"."morph_tag_id" = "morph_tags"."id" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ?`,
			joined.sql)
		assert.Equal(t, append(driverBindings([]any{uint(1), uint(2)}), desc.MorphValue), joined.args)
	})

	// The relation's OnPivotQuery scope reaches the joined statement the loader actually runs.
	t.Run("loader's joined read carries the relation scope", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &scopeMorphPost{})
		desc, err := relations.ResolveRelation(&scopeMorphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.OnPivotQuery, "the fixture declares OnPivotQuery")

		recorder.returnRows(`from "scope_morph_tags"`, []string{"id", "name", "pivot_taggable_id", "pivot_taggable_type"},
			[]driver.Value{int64(9), "tag", int64(1), desc.MorphValue})

		parents := []reflect.Value{
			reflect.ValueOf(&scopeMorphPost{ID: 1}).Elem(),
			reflect.ValueOf(&scopeMorphPost{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadMorphToMany(parents, &scopeMorphPost{}, desc, eagerLoadEntry{Relation: "Tags"}, nil))

		joined := recorder.at(t, 0)
		assert.Equal(t,
			`select "scope_morph_tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."scope_morph_tag_id" as "pivot_scope_morph_tag_id", "taggables"."taggable_type" as "pivot_taggable_type" from "scope_morph_tags" inner join "taggables" ON "taggables"."scope_morph_tag_id" = "scope_morph_tags"."id" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ? and "taggables"."deleted_at" is null`,
			joined.sql)
		assert.Equal(t, append(driverBindings([]any{uint(1), uint(2)}), desc.MorphValue), joined.args)
	})
}
