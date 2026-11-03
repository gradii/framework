package dynamic_relations

// This file ports Laravel's tests/Database/DatabaseEloquentDynamicRelationsTest.php (12.x): one Go
// test per Laravel test method, named TestDynamicRelations_<method without the test prefix>.
//
// The corpus is the unit test of Model::resolveRelationUsing — registering a relation resolver by
// name at runtime, overriding and inheriting registrations across model classes, keeping them from
// shadowing hard-coded relation methods, and reading the registered relation through property
// access, getRelationValue() and isRelation(). It has no setUp and no schema: every model is a
// stub without a table, and every registered relation is a FakeHasManyRel whose constructor does
// nothing and whose getResults() returns the constant ['many' => 'related'] without touching a
// database. Not one test in the corpus compiles or runs a query, so there is no SQL assertion to
// pin against the oracle.
//
// The Go ORM has no dynamic-relation surface, so the harness database has nothing to seed and
// every test is a skip. The missing pieces, captured with a temporary probe test (deleted after
// evidence capture; three tables, one declared HasMany, the framework ORM):
//
//   - No runtime registration. resolveRelationUsing has no counterpart — a relation is a static
//     entry in the model's Relations() map and the resolver consults nothing else. Probe: Load on
//     an undeclared name answers OrmRelationNotFound (`relation "DynamicRel2" not found on model
//     *dynamic_relations.probeUnknownModel`), and a `gorm:"-"` struct field that is not in the map
//     is ignored the same way (`relation "FieldRel" not found on model ...`).
//   - No relation test double. The resolver dispatches only on the framework's built-in relation
//     kinds — a custom Relation implementation is rejected with OrmMorphRelationKindUnknown — and
//     a declared relation always compiles SQL against its related table. Probe: a HasMany over an
//     empty related table loads `[]`; over a table that was never created it fails with sqlite's
//     `no such table`. Nothing can make a relation's read return a constant the way
//     FakeHasManyRel's getResults() does.
//   - No read/introspection surface. There is no property-access lazy loading, no
//     getRelationValue(), no isRelation(), and no API returns the resolved relation object the
//     corpus checks with `instanceof HasOne/HasMany`. Go reads go through an explicit
//     Load/LoadMissing on a statically declared name.
//
// Every test's assertions therefore either need the registration machinery that does not exist
// (classified as an ORM divergence below) or are PHP-only introspection of the registered
// relation (no Go equivalent); each skip records both sides where they apply.
//
// The sibling relation corpora in this repository's other orm_relation packages exercise the same
// reading surface without the dynamic-registration machinery and are ported there.

import "testing"

// skipDynamicRegistration is the divergence every test in this corpus runs into: the dynamic
// relation resolver registration and its read path do not exist in the Go ORM. Probe evidence in
// the package comment: relations resolve only from the static Relations() map, and nothing can
// make a declared relation return a constant.
const skipDynamicRegistration = "our ORM diverges: the Go ORM has no runtime relation-resolver registration (resolveRelationUsing) and no relation test double — a relation resolves only from the model's static Relations() map (probe: Load on an undeclared name answers `relation \"DynamicRel2\" not found on model ...`), and a declared relation always runs SQL against its related table (probe: an empty related table loads [], a missing one fails with sqlite's `no such table`), so the corpus's FakeHasManyRel, whose getResults() returns the constant ['many' => 'related'] without any database, cannot be reproduced"

// --- ports of the corpus -----------------------------------------------------

func TestDynamicRelations_BasicDynamicRelations(t *testing.T) {
	t.Skip(skipDynamicRegistration + "; the test registers 'dynamicRel_2' and asserts both reads of it return the fake's constant — with no registration API the name cannot resolve at all, and the property access and getRelationValue('dynamicRel_2') calls are PHP-only accessors a Go model does not have (the Go read is an explicit Load, which yields the related table's rows)")
}

func TestDynamicRelations_BasicDynamicRelationsOverride(t *testing.T) {
	t.Skip(skipDynamicRegistration + "; the test registers 'dynamicRelConflict' twice and pins that the last resolver wins — reported through a HasMany instanceof and the fake's constant — and that isRelation('dynamicRelConflict') is true; no registry exists whose entries could be overwritten, no resolved relation object is exposed for the instanceof, and isRelation is PHP-only introspection")
}

// Name as spelled in the corpus (testInharitedDynamicRelations).
func TestDynamicRelations_InharitedDynamicRelations(t *testing.T) {
	t.Skip(skipDynamicRegistration + "; the test pins that a registration on one model is inherited by a subclass and absent on an unrelated class (isRelation checks), and that the subclass's resolved relation object and value compare equal to the parent's — with relations declared statically per type there is no registry to inherit, no resolved relation object to compare, and the isRelation checks are PHP-only")
}

func TestDynamicRelations_InheritedDynamicRelationsOverride(t *testing.T) {
	t.Skip(skipDynamicRegistration + "; the test re-registers 'dynamicRelConflict' on the subclass so the parent keeps resolving HasOne while the child resolves HasMany, asserted with instanceof checks before and after — there is no per-class registry to override, and no resolved relation object is exposed to inspect")
}

func TestDynamicRelations_DynamicRelationsCanNotHaveTheSameNameAsNormalRelations(t *testing.T) {
	t.Skip(skipDynamicRegistration + "; the test pins that a registration must not shadow a hard-coded relation method — the method keeps returning the fake relation and the reads still return its constant — but the Go ORM has no registration to shadow with and no relation methods to shadow: relations live only in the Relations() map, so the precedence scenario cannot be constructed (and getRelationValue/isRelation are PHP-only)")
}

func TestDynamicRelations_RelationResolvers(t *testing.T) {
	t.Skip(skipDynamicRegistration + "; the test pins that same-named registrations on two models do not conflict or override — HasOne instanceof on one model, HasMany on the other, isRelation true on both — while the Go ORM's per-type Relations() map cannot be registered into at runtime and exposes neither the resolved relation object nor isRelation")
}
