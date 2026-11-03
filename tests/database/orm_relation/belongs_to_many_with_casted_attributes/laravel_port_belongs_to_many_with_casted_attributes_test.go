package belongs_to_many_with_casted_attributes

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyWithCastedAttributesTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyWithCastedAttributes_<method without the test prefix>. The corpus declares a
// single test, testModelsAreProperlyMatchedToParents, and the whole file exists for one behaviour:
// match() must pair a casted pivot foreign key with a plainly typed parent key.
//
// getRelation() builds
//
//	belongsToMany($related, 'relation', 'foreign_key', 'related_key', 'parent_key', 'related_key')
//
// over a mocked builder (join / where are no-ops) and then calls
//
//	$relation->match([$model1, $model2], Collection::wrap($result1), 'foo')
//
// with two mocked parent models whose getAttribute('parent_key') answers the ints 1 and 2, and one
// result whose pivot->foreign_key is an object with __toString() '1' — the "casted attribute" of
// the corpus's name. match() keys its dictionary through getDictionaryKey: the object stringifies
// to '1', PHP's array-key coercion turns that into the int key 1, and the parent keyed 1 alone
// matches. The corpus asserts exactly that: $models[0]->foo counts 1 result and contains the same
// result object, while $models[1]->foo stays null. The remaining mock expectations
// (hasGetMutator, hasAttributeMutator, getCasts, ...) are scaffolding that keeps the mocked
// getAttribute path alive — not assertions, and with no Go counterpart.
//
// # How the port maps the corpus onto the Go ORM
//
// Go has no property load and no public match() seam; the framework's dictionary matching lives in
// the eager loader, so the port loads both parents with With("Foo") — the same mapping the
// belongs_to corpus uses for its match() test. The casted pivot attribute becomes the pivot's
// foreign_key column: a text column holding '1' (a Go column has one type, so the casted value's
// string form is the value the row carries), while the parents carry the ints 1 and 2. The loader
// normalises each side through dictKey (fmt.Sprint of the Go value), the same textual comparison
// PHP's dictionary keys perform after getDictionaryKey — so the parent keyed 1 matches the casted
// '1', and the parent keyed 2 attaches nothing.
//
// # Oracle measurements
//
// Replayed with .claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, the
// corpus's fixtures (parent models with parent_key 1 and 2; one result whose pivot.foreign_key is
// an object with __toString() '1') through the real BelongsToMany::match report: models[0]->foo
// count 1; models[0]->foo contains the same result object (strict in_array) true; models[1]->foo
// null. With the eager constraint installed (addEagerConstraints over the parents keyed 1 and 2),
// the relation's query renders
//
//	select * from "relateds" inner join "relation" on "relateds"."related_key" =
//	"relation"."related_key" where "relation"."foreign_key" is null and
//	"relation"."foreign_key" in (?, ?)
//
// with bindings [1, 2]. The leading `"relation"."foreign_key" is null` predicate comes from the
// relation constructor's addWhereConstraints: it reads $this->parent->{$this->parentKey}, and the
// corpus's stub has no parent_key attribute, so the value is null — a null comparison renders as
// `is null` and contributes no binding. The whereIn clause is what addEagerConstraints adds — a
// plain whereIn, because the parent key's name parent_key is not the parent model's key name, so
// Laravel does not pick whereIntegerInRaw here. The corpus-version checkout oracle
// (oracle_checkout.php, commit 0fc645c6) renders the same string.

// --- models ------------------------------------------------------------------

// belongsToManyWithCastedAttributesParent ports the corpus's two parent model mocks (and the
// EloquentBelongsToManyModelStub the relation is constructed with): the relation's parent key is
// parent_key, and the two rows carry the ints 1 and 2 the mocks' getAttribute('parent_key')
// returned.
type belongsToManyWithCastedAttributesParent struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	ParentKey int    `gorm:"column:parent_key"`

	Foo []*belongsToManyWithCastedAttributesRelated `gorm:"-"`
}

func (belongsToManyWithCastedAttributesParent) TableName() string { return "parents" }

func (belongsToManyWithCastedAttributesParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// foo(): belongsToMany(Related::class, 'relation', 'foreign_key', 'related_key',
		// 'parent_key', 'related_key').
		"Foo": contractsorm.Many2Many{
			Related:         &belongsToManyWithCastedAttributesRelated{},
			Table:           "relation",
			ForeignPivotKey: "foreign_key",
			RelatedPivotKey: "related_key",
			ParentKey:       "parent_key",
			RelatedKey:      "related_key",
		},
	}
}

// belongsToManyWithCastedAttributesRelated ports the corpus's mocked $related model: only its
// related_key column is ever addressed (pivot.related_key joins relateds.related_key).
type belongsToManyWithCastedAttributesRelated struct {
	RelatedKey uint64 `gorm:"column:related_key;primaryKey"`
}

func (belongsToManyWithCastedAttributesRelated) TableName() string { return "relateds" }

// --- schema and seeding ------------------------------------------------------

// belongsToManyWithCastedAttributesSchema is the schema the corpus's relation implies: the parents
// table keyed by parent_key, the relateds table keyed by related_key, and the pivot table named
// 'relation', whose foreign_key column is the varchar that carries the casted '1'.
func belongsToManyWithCastedAttributesSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table parents (id integer primary key autoincrement, parent_key integer not null)`,
		`create table relateds (related_key integer primary key not null)`,
		`create table relation (foreign_key varchar not null, related_key integer not null)`,
	)
}

// belongsToManyWithCastedAttributesSeedData creates the corpus's fixtures as rows: the two parents
// keyed 1 and 2, the one related row, and the one pivot row whose foreign_key is the text '1' —
// the Go representation of the corpus's casted pivot attribute (__toString() = '1').
func belongsToManyWithCastedAttributesSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, parentKey := range []int{1, 2} {
		parent := belongsToManyWithCastedAttributesParent{ParentKey: parentKey}
		require.NoError(t, cloneRelationQuery(t, query).Create(&parent))
	}

	related := belongsToManyWithCastedAttributesRelated{RelatedKey: 1}
	require.NoError(t, cloneRelationQuery(t, query).Create(&related))

	// A string value, not an int: the pivot's foreign_key arrives as '1', so matching the parent
	// keyed 1 is the loader's dictKey normalisation at work, not a typed lookup.
	require.NoError(t, cloneRelationQuery(t, query).Table("relation").Create(map[string]any{
		"foreign_key": "1",
		"related_key": 1,
	}))
}

// --- ports of the corpus -----------------------------------------------------

// TestBelongsToManyWithCastedAttributes_ModelsAreProperlyMatchedToParents ports
// testModelsAreProperlyMatchedToParents.
func TestBelongsToManyWithCastedAttributes_ModelsAreProperlyMatchedToParents(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithCastedAttributesSchema(t, query)
	belongsToManyWithCastedAttributesSeedData(t, query)

	// Laravel: $relation->match([$model1, $model2], Collection::wrap($result1), 'foo') — the Go
	// loader performs the same dictionary match inside an eager load, so that is the port's call.
	// The port reads through Get: First would leave its single-row cursor open while the eager
	// query runs, against the harness's one-connection pool.
	found := []belongsToManyWithCastedAttributesParent{}
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&belongsToManyWithCastedAttributesParent{}).
		With("Foo").
		OrderBy("parent_key", "asc").
		Get(&found))
	require.Len(t, found, 2)
	require.Equal(t, 1, found[0].ParentKey)
	require.Equal(t, 2, found[1].ParentKey)

	// assertSame(1, $models[0]->foo->count()): the parent keyed 1 matches the pivot's casted
	// foreign key '1' — exactly one result. assertContains($result1, $models[0]->foo): one related
	// row exists, so the count plus that row's related_key pins the same result the corpus
	// contains.
	require.Len(t, found[0].Foo, 1)
	require.NotNil(t, found[0].Foo[0])
	require.Equal(t, uint64(1), found[0].Foo[0].RelatedKey)

	// assertNull($models[1]->foo): the parent keyed 2 matches no result, so nothing is attached.
	require.Empty(t, found[1].Foo)
}
