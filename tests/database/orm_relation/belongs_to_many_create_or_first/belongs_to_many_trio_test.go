package belongs_to_many_create_or_first

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins the two lookup leaves of the repaired BtM trio that the corpus file next to it
// does not cover, because DatabaseEloquentBelongsToManyCreateOrFirstTest never exercises them:
// BelongsToMany::findOrNew (BelongsToMany.php:621) and BelongsToMany::firstOrNew (:637),
// illuminate/database v12.55.1. Before the D1 repair of the 1:1 alignment round both leaves
// forwarded into shells whose kind switch excluded Many2Many and failed with
// OrmRelationKindNotSupported at runtime. Every statement and outcome quoted below was re-measured
// with /tmp/lalign_btm_trio_probe.php, executed against a real sqlite database over the corpus's
// source_table / related_table / pivot_table shapes.

func TestBelongsToManyFindOrNew_ReturnsAttachedRelatedWithPivot(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").Attach([]any{uint64(456)}))

	// Laravel: findOrNew(:621) delegates to BelongsToMany::find (:729) - where(<related key> = id)
	// ->first() through the pivot-joined relation read (probe: `select "related_table".*,
	// "pivot_table"."source_id" as "pivot_source_id", "pivot_table"."related_id" as
	// "pivot_related_id" from "related_table" inner join "pivot_table" on "related_table"."id" =
	// "pivot_table"."related_id" where "pivot_table"."source_id" = ? and "related_table"."id" = ?
	// limit 1` bindings [123,6]) - and the hit carries the hydrated pivot (probe: pivot
	// {"source_id":123,"related_id":6}).
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").FindOrNew(uint64(456), &result))

	assert.Equal(t, uint64(456), result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, uint64(123), result.Pivot.SourceID, "find must hydrate the pivot")
	assert.Equal(t, uint64(456), result.Pivot.RelatedID)

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	require.Len(t, pivots, 1)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query), "findOrNew must not write")
}

func TestBelongsToManyFindOrNew_ReturnsNewInstanceOnMiss(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	// Laravel: the relation read misses and findOrNew returns a bare newInstance() (probe:
	// exists=false, toArray=[], pivot=(no pivot loaded); the pivot-joined select with bindings
	// [123,999] matches nothing) - nothing is written and no foreign key lands on the related row.
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").FindOrNew(uint64(999), &result))

	assert.Zero(t, result.ID)
	assert.Empty(t, result.Attr)
	assert.Zero(t, result.Pivot)

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	assert.Empty(t, pivots)
	assert.Zero(t, belongsToManyCreateOrFirstCount(t, query))
}

func TestBelongsToManyFirstOrNew_ReadsTheRelatedTableDirectly(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seed))

	// Laravel: firstOrNew(:637) searches $this->related - the plain related-table read behind the
	// pivot join (probe: `select * from "related_table" where ("attr" = ?) limit 1` bindings
	// ["foo"], no join) - and returns the found row without pivot values (probe: pivot=(no pivot
	// loaded)); nothing is attached.
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").FirstOrNew(
		map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &result))

	assert.Equal(t, uint64(456), result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val, "$values are not applied to a found row")
	assert.Zero(t, result.Pivot)

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	assert.Empty(t, pivots)
	assert.Equal(t, int64(1), belongsToManyCreateOrFirstCount(t, query))
}

func TestBelongsToManyFirstOrNew_BuildsNewInstanceOnMiss(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyCreateOrFirstSchema(t, query)

	// Laravel: the plain related read misses and firstOrNew returns
	// newInstance(array_merge($attributes, $values)) - values win (probe: toArray
	// {"attr":"foo","val":"baz"}, exists=false, pivot=(no pivot loaded)) - and nothing is written.
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, query).Relation(source, "Related").FirstOrNew(
		map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &result))

	assert.Zero(t, result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "baz", result.Val)
	assert.Zero(t, result.Pivot)

	pivots := belongsToManyCreateOrFirstPivotRows(t, query)
	assert.Empty(t, pivots)
	assert.Zero(t, belongsToManyCreateOrFirstCount(t, query))
}
