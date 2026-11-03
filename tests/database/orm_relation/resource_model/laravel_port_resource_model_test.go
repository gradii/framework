package resource_model

import "testing"

// This file ports Laravel's tests/Database/DatabaseEloquentResourceModelTest.php (12.x) onto the
// framework: one Go test per Laravel test method, named TestResourceModel_<method without the test
// prefix>.
//
// The corpus exercises the resource transformation API Laravel adds to models through
// Illuminate\Database\Eloquent\Concerns\TransformsToResource — Model::toResource(),
// Model::guessResourceName() and the #[UseResource] class attribute — whose every entry point
// resolves an Illuminate\Http\Resources\Json\JsonResource. The corpus defines no schema and touches
// no database: the models are instantiated with `new` and each assertion is about class resolution
// and resource wrapping, so no SQL is issued and the oracle has no string to pin in this file.
//
// The framework has no resource transformation layer: no toResource / guessResourceName exists on
// the ORM, no JsonResource type exists anywhere, and Go resolves types at compile time, so PHP's
// class_exists / class_alias guessing cannot run. No method of the corpus therefore has an
// executable Go equivalent; the six methods are not reproduced (reported, not approximated), and
// each test below is skipped as PHP-only with the behavior it asserts:
//
//   testItCanTransformToExplicitResource, testItThrowsExceptionWhenResourceCannotBeFound,
//   testItCanGuessResourceWhenNotProvided, testItCanGuessResourceWhenNotProvidedWithNonResourceSuffix,
//   testItCanGuessResourceName, testItCanTransformToResourceViaUseResourceAttribute.
//
// The package ships the shared harness (harness_test.go) as the corpus's porting rules require;
// these six skips never open a connection.

// --- ports of the corpus -----------------------------------------------------

// TestResourceModel_ItCanTransformToExplicitResource ports testItCanTransformToExplicitResource.
// Laravel: $model->toResource(EloquentResourceTestJsonResource::class) is an instance of that class
// and $resource->resource is the same model.
func TestResourceModel_ItCanTransformToExplicitResource(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts that $model->toResource(EloquentResourceTestJsonResource::class) returns an instance of that resource class whose $resource is the same model, but the framework has no model-to-resource API (no toResource, no JsonResource) to call")
}

// TestResourceModel_ItThrowsExceptionWhenResourceCannotBeFound ports
// testItThrowsExceptionWhenResourceCannotBeFound.
// Laravel: $model->toResource() throws a LogicException with the message "Failed to find resource
// class for model [Illuminate\Tests\Database\Fixtures\Models\EloquentResourceTestResourceModel].".
func TestResourceModel_ItThrowsExceptionWhenResourceCannotBeFound(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts that $model->toResource() throws a LogicException naming the model when no resource class can be resolved, but the framework has no resource-resolving path that could raise it")
}

// TestResourceModel_ItCanGuessResourceWhenNotProvided ports testItCanGuessResourceWhenNotProvided.
// Laravel: class_alias makes the conventional
// Illuminate\Tests\Database\Fixtures\Http\Resources\EloquentResourceTestResourceModelWithGuessableResourceResource
// class exist, and toResource() guesses it as the model's resource.
func TestResourceModel_ItCanGuessResourceWhenNotProvided(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts that $model->toResource() guesses the conventionally named Http\\Resources\\<ModelName>Resource class once class_alias makes it exist, but Go has no class_alias / class_exists runtime resolution and the framework no guessResource API")
}

// TestResourceModel_ItCanGuessResourceWhenNotProvidedWithNonResourceSuffix ports
// testItCanGuessResourceWhenNotProvidedWithNonResourceSuffix.
// Laravel: the same guessing also resolves the conventional name without the Resource suffix,
// Illuminate\Tests\Database\Fixtures\Http\Resources\EloquentResourceTestResourceModelWithGuessableResource.
func TestResourceModel_ItCanGuessResourceWhenNotProvidedWithNonResourceSuffix(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts that the class_exists guessing falls back to the conventional name without the Resource suffix (Http\\Resources\\<ModelName>) once aliased, with no Go runtime class resolution to reproduce it")
}

// TestResourceModel_ItCanGuessResourceName ports testItCanGuessResourceName.
// Laravel: EloquentResourceTestResourceModel::guessResourceName() equals
// ['Illuminate\Tests\Database\Fixtures\Http\Resources\EloquentResourceTestResourceModelResource',
// 'Illuminate\Tests\Database\Fixtures\Http\Resources\EloquentResourceTestResourceModel'].
func TestResourceModel_ItCanGuessResourceName(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts that the model's guessResourceName() returns the two candidate FQCNs built from its Models namespace (the Http\\Resources\\<ModelName>Resource form first, then the suffix-less form), but the Go ORM has no guessResourceName API")
}

// TestResourceModel_ItCanTransformToResourceViaUseResourceAttribute ports
// testItCanTransformToResourceViaUseResourceAttribute.
// Laravel: the #[UseResource(EloquentResourceTestJsonResource::class)] PHP attribute names the
// resource class toResource() returns.
func TestResourceModel_ItCanTransformToResourceViaUseResourceAttribute(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts that the #[UseResource(EloquentResourceTestJsonResource::class)] attribute names the JsonResource class toResource() resolves to, but Go has no class attributes and the framework's ORM resolves no such tag")
}
