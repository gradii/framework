package resource_collection

import "testing"

// This file ports Laravel's tests/Database/DatabaseEloquentResourceCollectionTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestResourceCollection_<method without the test prefix>. The corpus never reaches a database —
// each method builds an in-memory Collection of never-persisted models and calls
// Collection::toResourceCollection() — so there is no schema for runRelationSchema and no rows to
// seed. Every assertion is an instanceof check on the returned Http\Resources object, and the Go
// framework has no HTTP resource layer (no JsonResource / ResourceCollection /
// AnonymousResourceCollection types, and support/collect has no toResourceCollection), so all five
// methods are skipped with the PHP-only reason rather than weakened.
//
// The skip reasons describe behaviour measured with .claude/laravel-oracle/oracle_checkout.php
// against the laravel/framework checkout the corpus comes from (12.43.1), by executing each
// method's calls and pins against it:
//
//   - the explicit-resource and guessed-resource calls return an AnonymousResourceCollection;
//   - the unresolvable call throws LogicException with the corpus's exact message;
//   - the UseResourceCollection-attribute call returns EloquentResourceTestJsonResourceCollection;
//   - the UseResource-attribute call returns an AnonymousResourceCollection whose offset 0 is an
//     EloquentResourceTestJsonResource.
//
// Two corpus quirks are kept faithfully: the throwing method depends on PHPUnit's declaration-order
// execution — it must run before the name-guessing method's class_alias is registered, which would
// otherwise let the guess resolve and return a resource instead of throwing (oracle-confirmed) —
// and the two attribute methods' names are swapped relative to the attributes their fixture models
// carry. The corpus contains no compiled-SQL pins, so there are no oracle SQL expectations to cite.

func TestResourceCollection_ItCanTransformToExplicitResource(t *testing.T) {
	// Laravel: (new Collection([new EloquentResourceCollectionTestModel()]))
	//     ->toResourceCollection(EloquentResourceCollectionTestResource::class), then
	// assertInstanceOf(JsonResource::class, $resource). Oracle: AnonymousResourceCollection.
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts that Collection::toResourceCollection(EloquentResourceCollectionTestResource::class) returns an instance of the given resource class as a JsonResource (its collection() helper wraps the collection in an AnonymousResourceCollection), and the Go framework has no HTTP resource types to return or assert on")
}

func TestResourceCollection_ItThrowsExceptionWhenResourceCannotBeFound(t *testing.T) {
	// Laravel: toResourceCollection() with no argument on a collection whose model has no
	// UseResource/UseResourceCollection attribute and no guessable resource, expecting
	// LogicException('Failed to find resource class for model
	// [Illuminate\Tests\Database\Fixtures\Models\EloquentResourceCollectionTestModel].'). Oracle:
	// the same exception class and message.
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts that with no resource attribute and no guessable resource, Collection::toResourceCollection() throws LogicException('Failed to find resource class for model [Illuminate\\Tests\\Database\\Fixtures\\Models\\EloquentResourceCollectionTestModel].'), and the Go framework has no resource-guessing machinery")
}

func TestResourceCollection_ItCanGuessResourceWhenNotProvided(t *testing.T) {
	// Laravel: class_alias(EloquentResourceCollectionTestResource::class,
	// 'Illuminate\Tests\Database\Fixtures\Http\Resources\EloquentResourceCollectionTestModelResource')
	// then toResourceCollection() with no argument, expecting instanceof JsonResource. Oracle:
	// AnonymousResourceCollection over the aliased resource class. The alias must exist before the
	// call, and must not leak into the throwing method's run (declaration order is load-bearing in
	// PHPUnit; in this port each test is independent).
	t.Skip("PHP-only assertion (no Go equivalent): after class_alias registers Illuminate\\Tests\\Database\\Fixtures\\Http\\Resources\\EloquentResourceCollectionTestModelResource, the test asserts toResourceCollection() returns a JsonResource (an AnonymousResourceCollection over the aliased resource class), and Go has no runtime class aliasing or class_exists-style resolution to guess resources with")
}

func TestResourceCollection_ItCanTransformToResourceViaUseResourceAttribute(t *testing.T) {
	// Laravel: the fixture model carries
	// #[UseResourceCollection(EloquentResourceTestJsonResourceCollection::class)] — the corpus's
	// swapped naming is ported verbatim — and the assertion is instanceof that collection class.
	// Oracle: EloquentResourceTestJsonResourceCollection.
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts that toResourceCollection() returns an EloquentResourceTestJsonResourceCollection for a model carrying the #[UseResourceCollection(EloquentResourceTestJsonResourceCollection::class)] attribute, and Go has no PHP attributes and no HTTP resource layer")
}

func TestResourceCollection_ItCanTransformToResourceViaUseResourceCollectionAttribute(t *testing.T) {
	// Laravel: the fixture model carries #[UseResource(EloquentResourceTestJsonResource::class)]
	// and the assertions are instanceof AnonymousResourceCollection and $resource[0] instanceof
	// EloquentResourceTestJsonResource. Oracle: both hold.
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts that toResourceCollection() returns an AnonymousResourceCollection whose offset 0 is an EloquentResourceTestJsonResource for a model carrying the #[UseResource(EloquentResourceTestJsonResource::class)] attribute, and Go has no PHP attributes, HTTP resource layer, or array-offset access on collection wrappers")
}
