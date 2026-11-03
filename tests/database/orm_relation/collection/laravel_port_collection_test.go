package collection

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gradii/framework/support/collect"
)

// This file ports Laravel's tests/Database/DatabaseEloquentCollectionTest.php (12.x) onto the
// framework's support/collect package: one Go test per Laravel test method, named
// TestCollection_<method without the test prefix>.
//
// Eloquent's collection only partially maps onto a generic Go collection, so the port is
// deliberately skip-heavy. The operations whose semantics support/collect reproduces are ported
// for real, with the Go call documented where its shape differs from PHP:
//
//   - Collection::add (the appending form) is support/collect's Push.
//   - max('foo') / min('foo') attribute reads are Max/Min with an accessor closure.
//   - contains($key, $operator, $value) is the where() predicate (Laravel's contains delegates to
//     operatorForWhere), so the port calls Where(field, operator, value) plus IsEmpty/IsNotEmpty;
//     attribute reads happen through exported struct fields instead of ArrayAccess offsets.
//   - contains(closure) is Filter(closure).IsNotEmpty().
//
// Everything else is built on API the Go collection does not have, and each such test is skipped
// with the concrete reason: Eloquent's model-key dictionary (find, findOrFail, modelKeys, merge,
// diff, intersect, unique, only, except, duplicates), dynamic model attributes (hidden/visible
// lists, appends/accessors, loadExists' comments_exists pseudo-column), collection-level load,
// fresh, toQuery, multiply, and the PHP queue serialization API
// (getQueueableClass/getQueueableRelations). The corpus's setUp schema (users/articles/comments)
// is used by none of the ported tests; the two DB-touching tests (withoutAppends, loadExists) are
// Eloquent-only and skipped.
//
// Every Eloquent-side semantic cited in a skip reason below was measured by execution, not recall,
// with the oracle (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1): four
// models keyed 1,1,1,2 printed unique() = 3,4 (key dictionary, last per key) and unique(null, true)
// = 3,4, duplicates() = original indices 1,2, merge() = 3 keyed items, diff() = a, intersect() = b,
// intersect(null) = empty, only(1) = the key-1 model, only([2,3]) = b,c, except(2) = a,c,
// except([2,3]) = a, contains(1) = true, find(9, 'dflt') = 'dflt', modelKeys() = 1,2,3, pluck() = a
// base collection (keyed [1=>1,2=>2,3=>3], closure = mapped strings), map(model) = Eloquent
// Collection while map(non-model) = Illuminate\Support\Collection, mapWithKeys() = a keyed Eloquent
// Collection, multiply(3) = 6 items, add() = an appended Eloquent Collection, and an empty
// collection's fresh() = an equal empty Eloquent Collection.

// collectionIDItem is the port's stand-in for the corpus's keyed models: an integer identity field
// where Laravel reads Model::getKey().
type collectionIDItem struct {
	ID int
}

// collectionFooItem is the corpus's plain attribute object, (object) ['foo' => 10].
type collectionFooItem struct {
	Foo int
}

// collectionNamedItem stands in for the ArrayAccess models that carry a name attribute; the Go
// collection reads the exported field instead of an offsetGet() offset.
type collectionNamedItem struct {
	Name string
}

func TestCollection_AddingItemsToCollection(t *testing.T) {
	// Laravel: (new Collection(['foo']))->add('bar')->add('baz') equals ['foo', 'bar', 'baz'].
	// add() is the collection's appending form; Go's Push is the same operation.
	c := collect.New("foo")
	c.Push("bar").Push("baz")

	assert.Equal(t, []string{"foo", "bar", "baz"}, c.All())
}

func TestCollection_GettingMaxItemsFromCollection(t *testing.T) {
	// Laravel: (new Collection([(object) ['foo' => 10], (object) ['foo' => 20]]))->max('foo') is 20.
	// Go's Max takes the attribute accessor as a closure.
	items := collect.New(collectionFooItem{Foo: 10}, collectionFooItem{Foo: 20})

	assert.Equal(t, 20.0, items.Max(func(item collectionFooItem) float64 { return float64(item.Foo) }))
}

func TestCollection_GettingMinItemsFromCollection(t *testing.T) {
	// Laravel: the same collection's min('foo') is 10.
	items := collect.New(collectionFooItem{Foo: 10}, collectionFooItem{Foo: 20})

	assert.Equal(t, 10.0, items.Min(func(item collectionFooItem) float64 { return float64(item.Foo) }))
}

func TestCollection_ContainsWithMultipleArguments(t *testing.T) {
	// Laravel: over [['id' => 1], ['id' => 2]] —
	//   contains('id', 1) is true, contains('id', '>=', 2) is true, contains('id', '>', 2) is false,
	//   doesntContain('id', 1) is false, doesntContain('id', '>=', 2) is false,
	//   doesntContain('id', '>', 2) is true.
	// contains(key, operator, value) delegates to the where() predicate, so the port calls
	// Where(field, operator, value) and asserts emptiness; the corpus's array rows become a struct
	// for the Go reflection field lookup.
	c := collect.New(collectionIDItem{ID: 1}, collectionIDItem{ID: 2})

	assert.True(t, c.Where("ID", 1).IsNotEmpty())
	assert.True(t, c.Where("ID", ">=", 2).IsNotEmpty())
	assert.False(t, c.Where("ID", ">", 2).IsNotEmpty())

	assert.False(t, c.Where("ID", 1).IsEmpty())
	assert.False(t, c.Where("ID", ">=", 2).IsEmpty())
	assert.True(t, c.Where("ID", ">", 2).IsEmpty())
}

func TestCollection_ContainsIndicatesIfModelInArray(t *testing.T) {
	// Asserts contains($model)/doesntContain($model) match a collection of models by Model::is()
	// (same class and key).
	t.Skip("PHP-only assertion (no Go equivalent): the test drives mocked Model::is() identity through the collection; Go models have no is(), and collect.Contains compares whole values with reflect.DeepEqual")
}

func TestCollection_ContainsIndicatesIfDifferentModelInArray(t *testing.T) {
	// Asserts a mocked Foo-class model is not contained in a collection holding only Bar-class
	// mocks.
	t.Skip("PHP-only assertion (no Go equivalent): Eloquent's model identity is class name plus key (is()), asserted here through named mocks; the generic Go collection cannot express it")
}

func TestCollection_ContainsIndicatesIfKeyedModelInArray(t *testing.T) {
	// Asserts contains(1)/contains(2)/doesntContain(3) match models through mocked getKey().
	t.Skip("PHP-only assertion (no Go equivalent): the corpus matches models by mocked getKey() through contains(); the generic Go collection has no model-key containment (Contains compares whole values)")
}

func TestCollection_ContainsKeyAndValueIndicatesIfModelInArray(t *testing.T) {
	// Laravel: over models exposing name via ArrayAccess —
	//   contains('name', 'Taylor') is true, contains('name', 'Abigail') is true,
	//   contains('name', 'Dayle') is false, and the three doesntContain negations.
	// The ArrayAccess offsets become exported struct fields; contains(key, value) is the where()
	// predicate, so the port calls Where("Name", value) and asserts emptiness.
	c := collect.New(collectionNamedItem{Name: "Taylor"}, collectionNamedItem{Name: "Abigail"})

	assert.True(t, c.Where("Name", "Taylor").IsNotEmpty())
	assert.True(t, c.Where("Name", "Abigail").IsNotEmpty())
	assert.False(t, c.Where("Name", "Dayle").IsNotEmpty())

	assert.False(t, c.Where("Name", "Taylor").IsEmpty())
	assert.False(t, c.Where("Name", "Abigail").IsEmpty())
	assert.True(t, c.Where("Name", "Dayle").IsEmpty())
}

func TestCollection_ContainsClosureIndicatesIfModelInArray(t *testing.T) {
	// Laravel: over models keyed 1 and 2 —
	//   contains(fn ($model) => $model->getKey() < 2) is true,
	//   contains(fn ($model) => $model->getKey() > 2) is false, and the doesntContain negations.
	// contains(closure) is a filter: the Go port calls Filter(closure) and reads IsEmpty/IsNotEmpty;
	// the closure reads the model's ID field where Laravel calls getKey().
	c := collect.New(collectionIDItem{ID: 1}, collectionIDItem{ID: 2})

	assert.True(t, c.Filter(func(item collectionIDItem, _ int) bool { return item.ID < 2 }).IsNotEmpty())
	assert.False(t, c.Filter(func(item collectionIDItem, _ int) bool { return item.ID > 2 }).IsNotEmpty())

	assert.False(t, c.Filter(func(item collectionIDItem, _ int) bool { return item.ID < 2 }).IsEmpty())
	assert.True(t, c.Filter(func(item collectionIDItem, _ int) bool { return item.ID > 2 }).IsEmpty())
}

func TestCollection_FindMethodFindsModelById(t *testing.T) {
	// Asserts find(1) returns the model whose mocked getKey() is 1 and find(2, 'taylor') the
	// default.
	t.Skip("our ORM diverges: the Go collection has no find() -- Eloquent's collection find matches models by key and returns the given default when absent (oracle: find(9, 'dflt') returns 'dflt'), and the corpus drives it through mocked getKey()")
}

func TestCollection_FindMethodFindsManyModelsById(t *testing.T) {
	// Asserts find([]) returns an empty collection, find([1]) only the pushed key-1 model, and
	// find([2, 3, 4]) the models keyed 2 and 3.
	t.Skip("our ORM diverges: the Go collection has no find() to take a list of keys -- Eloquent's find([]) returns an empty collection and find([2,3,4]) the matching models, keyed by the model key")
}

func TestCollection_FindOrFailFindsModelById(t *testing.T) {
	// Asserts findOrFail(1) returns the model whose mocked getKey() is 1.
	t.Skip("our ORM diverges: the Go collection has no findOrFail() (or ModelNotFoundException) -- Eloquent's findOrFail returns the model whose key matches")
}

func TestCollection_FindOrFailFindsManyModelsById(t *testing.T) {
	// Asserts the many-keys form: findOrFail([]) is an empty collection, findOrFail([1,2]) the two
	// models, and findOrFail([1,2,3]) throws ModelNotFoundException.
	t.Skip("our ORM diverges: the Go collection has no findOrFail() (or ModelNotFoundException) -- Eloquent's multi-key form returns the models found and throws for the missing key")
}

func TestCollection_FindOrFailThrowsExceptionWithMessageWhenOtherModelsArePresent(t *testing.T) {
	// Asserts findOrFail(2) on a collection holding only key 1 throws with the model class and the
	// missing key in the message.
	t.Skip("our ORM diverges: the Go collection has no findOrFail() (or ModelNotFoundException) -- Laravel throws with the model class and missing key in the message")
}

func TestCollection_FindOrFailThrowsExceptionWithoutMessageWhenOtherModelsAreNotPresent(t *testing.T) {
	// Asserts findOrFail(1) on an empty collection throws ModelNotFoundException with an empty
	// message.
	t.Skip("our ORM diverges: the Go collection has no findOrFail() (or ModelNotFoundException) -- Laravel throws with an empty message when the collection is empty")
}

func TestCollection_LoadMethodEagerLoadsGivenRelationships(t *testing.T) {
	// Mocks Collection::first() and asserts load('bar', 'baz') delegates to the first item's
	// newQueryWithoutRelationships()->with(['bar', 'baz'])->eagerLoadRelations(['foo']).
	t.Skip("PHP-only assertion (no Go equivalent): the test mocks Collection::first() and verifies the load() delegation chain; the Go ORM's Load takes a single model -- there is no collection-level load to observe")
}

func TestCollection_CollectionDictionaryReturnsModelKeys(t *testing.T) {
	// Asserts modelKeys() returns [1, 2, 3] for models keyed 1-3.
	t.Skip("our ORM diverges: support/collect has no modelKeys() -- Eloquent reads each model's key through the keyed dictionary (oracle: modelKeys() = 1,2,3), and the generic Go collection has no key introspection")
}

func TestCollection_CollectionMergesWithGivenCollection(t *testing.T) {
	// Asserts merging [one, two] with [two, three] yields [one, two, three] -- deduped by model
	// key.
	t.Skip("our ORM diverges: Eloquent's merge() dedupes by model key through the keyed dictionary (oracle: 3 items, the duplicate replaced); support/collect has Concat (concatenates) and Union (dedupes by printed value) -- no key-based merge")
}

func TestCollection_Map(t *testing.T) {
	// Laravel: mapping over [one, two] with an identity closure returns the same items and the
	// result is still an Eloquent Collection.
	c := collect.New(collectionIDItem{ID: 1}, collectionIDItem{ID: 2})

	cAfterMap := c.Map(func(item collectionIDItem, _ int) any { return item })

	assert.Equal(t, []any{collectionIDItem{ID: 1}, collectionIDItem{ID: 2}}, cAfterMap.All())
	// The corpus's assertInstanceOf(Collection::class) is static in Go: Map always returns
	// *Collection[any], so the check documents the same guarantee.
	assert.IsType(t, &collect.Collection[any]{}, cAfterMap)
}

func TestCollection_MappingToNonModelsReturnsABaseCollection(t *testing.T) {
	// Asserts mapping models to 'not-a-model' returns Illuminate\Support\Collection rather than the
	// Eloquent Collection.
	t.Skip("PHP-only assertion (no Go equivalent): Go has one generic Collection type -- the Eloquent/Base split the corpus asserts (oracle: map(non-model) yields Illuminate\\Support\\Collection) is unobservable")
}

func TestCollection_MapWithKeys(t *testing.T) {
	// Asserts mapWithKeys with a running counter preserves the items under keys 0 and 1 and returns
	// an Eloquent Collection.
	t.Skip("our ORM diverges: support/collect's MapWithKeys returns a Go map, not a keyed collection -- the corpus asserts the result is an Eloquent Collection and compares its all() (oracle: keys preserved), a shape the Go map cannot carry")
}

func TestCollection_MapWithKeysToNonModelsReturnsABaseCollection(t *testing.T) {
	// Asserts mapWithKeys mapping models to 'not-a-model' returns Illuminate\Support\Collection.
	t.Skip("PHP-only assertion (no Go equivalent): Go has one generic Collection type and MapWithKeys returns a plain map -- the Eloquent/Base class split is unobservable")
}

func TestCollection_CollectionDiffsWithGivenCollection(t *testing.T) {
	// Asserts diffing [one, two] against [two, three] yields [one] -- by model key.
	t.Skip("our ORM diverges: Eloquent's diff() compares model keys through the keyed dictionary (oracle: [one]); support/collect's Diff compares printed values (the fmt default format) -- a different semantics")
}

func TestCollection_CollectionReturnsDuplicateBasedOnlyOnKeys(t *testing.T) {
	// Asserts duplicates()/duplicatesStrict() over models keyed 1,1,1,2 return the second and third
	// models under the original indices 1 and 2 -- grouped by key, not by attribute values.
	t.Skip("our ORM diverges: Eloquent's duplicates()/duplicatesStrict() compare models by is() (class + key) and preserve the original indices (oracle: keys 1,2 for the corpus's four models); support/collect's Duplicates compares printed values and re-indexes")
}

func TestCollection_CollectionIntersectWithNull(t *testing.T) {
	// Asserts intersect(null) yields an empty collection.
	t.Skip("our ORM diverges: Eloquent's intersect(null) returns an empty collection (oracle-verified); support/collect's Intersect takes a *Collection and would dereference nil")
}

func TestCollection_CollectionIntersectsWithGivenCollection(t *testing.T) {
	// Asserts intersecting [one, two] with [two, three] yields [two] -- by model key.
	t.Skip("our ORM diverges: Eloquent's intersect() intersects model keys (oracle: [two]); support/collect's Intersect compares printed values (the fmt default format) -- a different semantics")
}

func TestCollection_CollectionReturnsUniqueItems(t *testing.T) {
	// Asserts unique() over models keyed 1,2,2 returns [one, two] -- by model key.
	t.Skip("our ORM diverges: Eloquent's unique(null) dedupes by model key keeping the last of each group (oracle: the third and fourth models for keys 1,1,1,2); support/collect's Unique compares printed values and keeps the first")
}

func TestCollection_CollectionReturnsUniqueStrictBasedOnKeysOnly(t *testing.T) {
	// Asserts unique() and unique(null, true) over models keyed 1,1,1,2 both keep only the last
	// model of key 1 and the key-2 model -- identity resolved by key, not by attributes.
	t.Skip("our ORM diverges: unique(null, true) resolves through the same model-key dictionary and keeps the last duplicate (oracle-verified); support/collect has no keyed or strict variant of Unique")
}

func TestCollection_OnlyReturnsCollectionWithGivenModelKeys(t *testing.T) {
	// Asserts only(null) is the whole collection, only(1) the key-1 model and only([2, 3]) the
	// models keyed 2 and 3.
	t.Skip("our ORM diverges: Eloquent's only() selects by model key (oracle: only(1) returns the key-1 model); support/collect's Only selects by positional index")
}

func TestCollection_ExceptReturnsCollectionWithoutGivenModelKeys(t *testing.T) {
	// Asserts except(null) is the whole collection, except(2) drops the key-2 model and
	// except([2, 3]) drops the models keyed 2 and 3.
	t.Skip("our ORM diverges: Eloquent's except() removes by model key (oracle: except(2) leaves the key-1 and key-3 models); support/collect's Except removes by positional index")
}

func TestCollection_MakeHiddenAddsHiddenOnEntireCollection(t *testing.T) {
	// Asserts Collection::makeHidden(['visible']) appends to every model's hidden list, leaving
	// ['hidden', 'visible'].
	t.Skip("our ORM diverges: Go models are plain structs with no hidden attribute list -- the collection's makeHidden() has nothing to mutate (oracle: Laravel appends to the model's hidden list)")
}

func TestCollection_MakeVisibleRemovesHiddenFromEntireCollection(t *testing.T) {
	// Asserts makeVisible(['hidden']) removes 'hidden' from every model's hidden list.
	t.Skip("our ORM diverges: Go models are plain structs with no hidden attribute list -- makeVisible() has nothing to remove")
}

func TestCollection_MergeHiddenAddsHiddenOnEntireCollection(t *testing.T) {
	// Asserts mergeHidden(['merged']) extends every model's hidden list to ['hidden', 'merged'].
	t.Skip("our ORM diverges: Go models have no hidden attribute list -- mergeHidden() has nothing to merge into")
}

func TestCollection_MergeVisibleRemovesHiddenFromEntireCollection(t *testing.T) {
	// Asserts mergeVisible(['merged']) extends every model's visible list to ['visible', 'merged'].
	t.Skip("our ORM diverges: Go models have no hidden/visible attribute lists -- mergeVisible() has nothing to merge into")
}

func TestCollection_SetVisibleReplacesVisibleOnEntireCollection(t *testing.T) {
	// Asserts setVisible(['hidden']) replaces every model's visible list with ['hidden'].
	t.Skip("our ORM diverges: Go models have no visible attribute list -- setVisible() has nothing to replace")
}

func TestCollection_SetHiddenReplacesHiddenOnEntireCollection(t *testing.T) {
	// Asserts setHidden(['visible']) replaces every model's hidden list with ['visible'].
	t.Skip("our ORM diverges: Go models have no hidden attribute list -- setHidden() has nothing to replace")
}

func TestCollection_AppendsAddsTestOnEntireCollection(t *testing.T) {
	// Asserts makeVisible('test')->append('test') makes toArray() contain the accessor value
	// getTestAttribute() = 'test'.
	t.Skip("our ORM diverges: Go models have no accessor/appends system -- the appended 'test' attribute and Collection::append() have no counterpart")
}

func TestCollection_SetAppendsSetsAppendedPropertiesOnEntireCollection(t *testing.T) {
	// Asserts setAppends(['other_appended_field']) makes toArray() return only the
	// getOtherAppendedFieldAttribute() accessor value 'bye'.
	t.Skip("our ORM diverges: Go models have no accessor/appends system -- setAppends() and the accessor it surfaces have no counterpart")
}

func TestCollection_WithoutAppendsRemovesAppendsOnEntireCollection(t *testing.T) {
	// Seeds a user and asserts withoutAppends() removes the appended_field accessor from
	// toArray().
	t.Skip("our ORM diverges: Go models have no accessor/appends system -- withoutAppends() and the appended_field accessor have no counterpart")
}

func TestCollection_NonModelRelatedMethods(t *testing.T) {
	// Asserts pluck/keys/collapse/flatten/zip/countBy/flip/partition all return
	// Illuminate\Support\Collection rather than the Eloquent Collection.
	t.Skip("PHP-only assertion (no Go equivalent): the corpus only checks the returned base-collection class (oracle: pluck yields Illuminate\\Support\\Collection); Go has one generic collection type, keys/zip/countBy return plain Go values, and flatten and flip do not exist")
}

func TestCollection_MakeVisibleRemovesHiddenAndIncludesVisible(t *testing.T) {
	// Asserts makeVisible('hidden') empties the hidden list and extends the visible list to
	// ['visible', 'hidden'].
	t.Skip("our ORM diverges: Go models have no hidden/visible attribute lists -- makeVisible() removing a hidden entry and adding a visible one has no counterpart")
}

func TestCollection_Multiply(t *testing.T) {
	// Asserts multiply(-1)/multiply(0) yield empty collections, multiply(1) the same items and
	// multiply(3) the items repeated three times.
	t.Skip("our ORM diverges: the Go collection has no multiply() (oracle: multiply(3) repeats the models six times); neither support/collect nor the ORM builder exposes it")
}

func TestCollection_QueueableCollectionImplementation(t *testing.T) {
	// Asserts getQueueableClass() returns the single model class of the collection.
	t.Skip("PHP-only assertion (no Go equivalent): the queueable API serves Laravel's queue job serialization -- the Go ORM has no queueable collection (getQueueableClass/getQueueableRelations)")
}

func TestCollection_QueueableCollectionImplementationThrowsExceptionOnMultipleModelTypes(t *testing.T) {
	// Asserts getQueueableClass() throws LogicException when the collection mixes model types.
	t.Skip("PHP-only assertion (no Go equivalent): asserts queue serialization rejects mixed model types -- the Go ORM has no queueable collection API")
}

func TestCollection_QueueableRelationshipsReturnsOnlyRelationsCommonToAllModels(t *testing.T) {
	// Asserts getQueueableRelations() returns only the relations every model of the collection
	// shares (['user'] here), guarding polymorphic model collections.
	t.Skip("PHP-only assertion (no Go equivalent): asserts queue serialization intersects the models' getQueueableRelations() -- queue serialization only, and the corpus's anonymous classes are PHP-only")
}

func TestCollection_QueueableRelationshipsIgnoreCollectionKeys(t *testing.T) {
	// Asserts getQueueableRelations() ignores the collection's string keys and returns an empty
	// intersection.
	t.Skip("PHP-only assertion (no Go equivalent): queue serialization only -- the Go ORM has no queueable collection API (getQueueableRelations)")
}

func TestCollection_EmptyCollectionStayEmptyOnFresh(t *testing.T) {
	// Asserts fresh() on an empty collection returns an equal empty collection.
	t.Skip("our ORM diverges: the Go collection has no fresh() -- OrmBuilder.Fresh resets a query builder, and there is no model-collection reload (oracle: an empty Eloquent collection's fresh() returns an equal empty Eloquent Collection)")
}

func TestCollection_CanConvertCollectionOfModelsToEloquentQueryBuilder(t *testing.T) {
	// Asserts toQuery() converts a model collection into a Builder constrained by
	// whereKey($c->modelKeys()).
	t.Skip("our ORM diverges: the Go collection has no toQuery() -- Eloquent converts a model collection into a whereKey() query (oracle: toQuery() builds a Builder); Go models expose no key interface for such a query")
}

func TestCollection_ConvertingEmptyCollectionToQueryThrowsException(t *testing.T) {
	// Asserts toQuery() on an empty collection throws LogicException.
	t.Skip("our ORM diverges: the Go collection has no toQuery() -- Laravel's empty-collection toQuery() throws LogicException 'Unable to create query for empty collection.' (oracle-checked), with no Go counterpart")
}

func TestCollection_LoadExistsShouldCastBool(t *testing.T) {
	// Seeds users/articles/comments, eager loads a user's articles, then asserts
	// $user->articles->loadExists('comments') attaches a comments_exists attribute that pluck()
	// reports as a bool for every article.
	t.Skip("our ORM diverges: the Go ORM has no collection loadExists -- Laravel attaches a synthetic comments_exists attribute to the already-loaded models (loadAggregate), while Go models are typed structs without dynamic attributes; the query-level WithExists only fills a separate scan destination")
}

func TestCollection_WithNonScalarKey(t *testing.T) {
	// Asserts only()/except() with a non-scalar (Stringable) key select the model whose id
	// stringifies to that key.
	t.Skip("our ORM diverges: only()/except() are model-key based in Eloquent and stringify non-scalar keys (EloquentTestKey); support/collect's Only/Except select by positional index and Go has no Stringable object keys")
}

func TestCollection_Pluck(t *testing.T) {
	// Asserts pluck('id') returns [1, 2, 3], keyed pluck('id', 'id') returns [1 => 1, 2 => 2,
	// 3 => 3], pluck('test') of a missing attribute returns a base collection, and closure pluck
	// returns the mapped strings.
	t.Skip("our ORM diverges: support/collect's Pluck takes exactly one Go-exported field name -- Laravel's keyed pluck('id', 'id') (oracle: [1=>1,2=>2,3=>3]) and closure pluck(fn) (oracle: mapped strings) have no counterpart, so the corpus's assertions cannot all be reproduced")
}
