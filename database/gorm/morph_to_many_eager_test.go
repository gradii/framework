package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLoadMorphToMany_ReadsAreCompiled pins both queries loadMorphToMany builds: the pivot-table
// read (with the extra morph-type predicate that separates it from a plain Many2Many read) and the
// related-rows read. Both are addressed through the package's child builders, so the framework
// compiler renders the statement instead of GORM. The loader is driven with parent keys too, so a
// change to either half of its body fails the test.
//
// The binding slice is asserted exactly, since its order and arity carry the chunk keys and the morph
// value.
func TestLoadMorphToMany_ReadsAreCompiled(t *testing.T) {
	t.Run("pivot read without a pivot scope", func(t *testing.T) {
		q := newRelQueryWith(t, &morphPost{})
		desc, err := resolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.Equal(t, relKindMorphToMany, desc.kind)
		assert.Equal(t, "taggables", desc.pivotTable)
		assert.Equal(t, "taggable_id", desc.pivotParentRef.foreignColumn)
		assert.Equal(t, "morph_tag_id", desc.pivotRelatedRef.foreignColumn)
		assert.Equal(t, "taggable_type", desc.morphTypeColumn)
		assert.Equal(t, "morph_tags", desc.relatedTable)

		cols := []string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn}
		chunk := []any{uint(1), uint(2)}

		// The child is named the way the loader names it: the pivot table verbatim, the IN column
		// unquoted and table-qualified, the select list bare so the returned column names — the map
		// keys the hydration loop indexes — stay the FK names, and the morph-type predicate the
		// descriptor carries.
		child := q.pivotChild(desc.pivotTable, desc.pivotTable+"."+desc.pivotParentRef.foreignColumn, chunk, cols).
			Where(desc.pivotTable+"."+desc.morphTypeColumn, desc.morphValue).(*OrmBuilder)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t,
			`select "taggable_id", "morph_tag_id" from "taggables" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ?`,
			compiled)
		assert.Equal(t, []any{uint(1), uint(2), desc.morphValue}, bindings)

		// No model is attached, so the child starts from the pivot table alone and inherits none of
		// the parent query's state — the same shape the fresh GORM session had. The morph-type
		// predicate is the second where, which is what makes this read differ from Many2Many's.
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.conditions.where, 2)
		assert.Len(t, child.query.Selects(), 2)
	})

	t.Run("pivot read receives the relation scope", func(t *testing.T) {
		q := newRelQueryWith(t, &scopeMorphPost{})
		desc, err := resolveRelation(&scopeMorphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onPivotQuery, "the fixture declares OnPivotQuery")

		// scopeMorphTag declares no Pivot field, so the select list stays the two FK columns.
		plan, err := preparePivotHydration(q, desc)
		assert.NoError(t, err)
		assert.Nil(t, plan)

		cols := []string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn}
		chunk := []any{uint(1), uint(2)}

		child := q.pivotChild(desc.pivotTable, desc.pivotTable+"."+desc.pivotParentRef.foreignColumn, chunk, cols).
			Where(desc.pivotTable+"."+desc.morphTypeColumn, desc.morphValue).(*OrmBuilder)
		scoped, ok := applyOnPivotQueryToQuery(q.queryGrammar, child, desc).(*OrmBuilder)
		assert.True(t, ok)

		compiled, bindings, err := scoped.compileSelect()
		assert.NoError(t, err)
		// Order: parent IN clause, morph-type clause, then OnPivotQuery scope.
		assert.Equal(t,
			`select "taggable_id", "scope_morph_tag_id" from "taggables" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ? and "taggables"."deleted_at" IS NULL`,
			compiled)
		// The chunk's keys and the morph value first, then the scope's own binding (none here: the
		// scope is an IS NULL predicate).
		assert.Equal(t, []any{uint(1), uint(2), desc.morphValue}, bindings)

	})

	t.Run("related read is compiled", func(t *testing.T) {
		q := newRelQueryWith(t, &morphPost{})
		desc, err := resolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		chunk := []any{uint(9)}
		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+desc.pivotRelatedRef.primaryColumn, chunk)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select * from "morph_tags" where "morph_tags"."id" in (?)`, compiled)
		assert.Equal(t, chunk, bindings)
	})

	// MorphedByMany is the same loader with the morph value taken from the related side; the
	// descriptor is the only place that differs, so both directions must pin the same value.
	t.Run("inverse pins the same morph value as the forward relation", func(t *testing.T) {
		forwardDesc, err := resolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)

		inverseDesc, err := resolveRelation(&morphTag{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.Equal(t, relKindMorphToMany, inverseDesc.kind)
		assert.Equal(t, "taggables", inverseDesc.pivotTable)
		assert.Equal(t, "morph_post_id", inverseDesc.pivotRelatedRef.foreignColumn)
		assert.Equal(t, forwardDesc.morphValue, inverseDesc.morphValue)
	})

	// No parent keys: the loader takes its empty path and never reaches a statement at all, so the
	// relation field is cleared rather than queried for an IN list that cannot match.
	t.Run("no parent keys issues no statement", func(t *testing.T) {
		q := newRelQueryWith(t, &morphPost{})
		desc, err := resolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.NoError(t, q.loadMorphToMany(nil, &morphPost{}, desc, eagerLoadEntry{relation: "Tags"}, nil))
	})
	// Drive the loader itself with parent keys. The recording driver hands back one pivot row, which
	// is what makes loadMorphToMany run its second read, so the assertion lands on the statements the
	// loader built and executed: the pivot read carrying the morph-type predicate, then the related
	// read. A change to either half of the loader's body fails the test.
	t.Run("loader reads the pivot table then the related table", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &morphPost{})
		desc, err := resolveRelation(&morphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		// One pivot row links parent 1 to related 9; the loader then reads the related row for key 9.
		recorder.returnRows(`from "taggables"`,
			[]string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn},
			[]driver.Value{int64(1), int64(9)})

		parents := []reflect.Value{
			reflect.ValueOf(&morphPost{ID: 1}).Elem(),
			reflect.ValueOf(&morphPost{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadMorphToMany(parents, &morphPost{}, desc, eagerLoadEntry{relation: "Tags"}, nil))
		assert.Equal(t, 2, recorder.count(), "the loader reads the pivot table and then the related table")

		pivot := recorder.at(t, 0)
		assert.Equal(t,
			`select "taggable_id", "morph_tag_id" from "taggables" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ?`,
			pivot.sql)
		assert.Equal(t, append(driverBindings([]any{uint(1), uint(2)}), desc.morphValue), pivot.args)

		related := recorder.at(t, 1)
		assert.Equal(t, `select * from "morph_tags" where "morph_tags"."id" in (?)`, related.sql)
		// The key comes off the pivot row as the driver-typed int64, not the uint the parents carried.
		assert.Equal(t, []driver.Value{int64(9)}, related.args)
	})

	// The relation's OnPivotQuery scope must reach the statement the loader actually runs, not just
	// the child this test could rebuild: findMapsCompiled threads it in before compiling.
	t.Run("loader's pivot read carries the relation scope", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &scopeMorphPost{})
		desc, err := resolveRelation(&scopeMorphPost{}, "Tags")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onPivotQuery, "the fixture declares OnPivotQuery")

		recorder.returnRows(`from "taggables"`,
			[]string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn},
			[]driver.Value{int64(1), int64(9)})

		parents := []reflect.Value{
			reflect.ValueOf(&scopeMorphPost{ID: 1}).Elem(),
			reflect.ValueOf(&scopeMorphPost{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadMorphToMany(parents, &scopeMorphPost{}, desc, eagerLoadEntry{relation: "Tags"}, nil))

		pivot := recorder.at(t, 0)
		assert.Equal(t,
			`select "taggable_id", "scope_morph_tag_id" from "taggables" where "taggables"."taggable_id" in (?, ?) and "taggables"."taggable_type" = ? and "taggables"."deleted_at" IS NULL`,
			pivot.sql)
		// The chunk's keys and the morph value first, then the scope's own binding (none here: the
		// scope is an IS NULL predicate).
		assert.Equal(t, append(driverBindings([]any{uint(1), uint(2)}), desc.morphValue), pivot.args)
	})
}
