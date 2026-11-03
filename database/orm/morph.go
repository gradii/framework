package orm

import (
	"github.com/gradii/framework/database/orm/morphmap"
)

// MorphMap registers polymorphic aliases. Each entry maps an alias (the value stored in a
// `*_type` column) to a sample model instance from which the registry derives the underlying Go
// type.
//
// Subsequent calls merge with previously registered entries; later writes win on conflict. Pass
// false in the optional merge argument to replace the registry instead of merging.
//
//	orm.MorphMap(map[string]any{
//	    "post":  &Post{},
//	    "video": &Video{},
//	})
//
// Models that implement orm.ModelWithMorphClass take precedence over the registry.
func MorphMap(entries map[string]any, merge ...bool) {
	morphmap.Register(entries, merge...)
}

// MorphedModel returns a fresh pointer to a new instance of the model registered under alias, or
// nil if no model is registered. Used by the MorphTo loader to allocate the right Go type for a
// row whose `*_type` column contains alias.
func MorphedModel(alias string) any {
	return morphmap.Find(alias)
}

// MorphAlias returns the alias registered for the given model's underlying type. Useful at insert
// time when writing the value of a `*_type` column from a Go value.
func MorphAlias(model any) (string, bool) {
	return morphmap.AliasOf(model)
}

// RequireMorphMap is Relation::requireMorphMap (Relation.php:437): it turns the process-level
// strict-morph flag on — or off when passed false. When on, resolving an unmapped model for a
// polymorphic `*_type` column raises errors.OrmClassMorphViolation instead of falling back to the
// model's table name, and a MorphTo associate raises when the associated model is unmapped.
// RequiresMorphMap reads the flag; nothing changes while it is off.
func RequireMorphMap(requireMorphMap ...bool) {
	morphmap.RequireMorphMap(requireMorphMap...)
}

// RequiresMorphMap is Relation::requiresMorphMap (Relation.php:447): the strict-flag getter.
func RequiresMorphMap() bool {
	return morphmap.RequiresMorphMap()
}

// EnforceMorphMap is Relation::enforceMorphMap (Relation.php:459): RequireMorphMap(true) plus
// MorphMap(entries, merge...) — register the map and switch strict mode on in one call.
func EnforceMorphMap(entries map[string]any, merge ...bool) {
	morphmap.EnforceMorphMap(entries, merge...)
}
