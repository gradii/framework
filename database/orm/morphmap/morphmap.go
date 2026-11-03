// Package morphmap holds the process-wide registry of polymorphic aliases shared by the orm
// and gorm packages. It mirrors fedaco's static Relation._morphMap (libs/fedaco/src/fedaco/
// relations/relation.ts:31) while remaining safe for concurrent reads from many query goroutines.
//
// Most app code interacts with the registry via the wrappers in package orm
// (orm.MorphMap, orm.MorphedModel, orm.MorphAlias, orm.RequireMorphMap, orm.RequiresMorphMap,
// orm.EnforceMorphMap). The lower-level gorm wrapper imports this package directly to resolve
// morph values during query construction without introducing a circular dependency on package
// orm.
//
// Strict morph mode (Relation::requireMorphMap / requiresMorphMap / enforceMorphMap —
// Relation.php:437/447/459) is a process-level flag: when on, MorphValue raises
// errors.OrmClassMorphViolation for a model that resolves neither through ModelWithMorphClass
// nor the registry, instead of returning the not-found pair. The base relations.Pivot type stays
// exempt (HasRelationships.php:1014); the flag defaults to off.
package morphmap

import (
	"reflect"
	"sync"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
)

var (
	mu          sync.RWMutex
	aliasToType = map[string]reflect.Type{}
	typeToAlias = map[reflect.Type]string{}
	// requireMorphMapFlag is the process-level strict-morph flag (Relation::$requireMorphMap,
	// Relation.php:77 — default false). Guarded by mu.
	requireMorphMapFlag bool
)

// pivotMorphType is the base Pivot type of the relations package — the Go reading of PHP's
// `Pivot::class` in HasRelationships::getMorphClass (HasRelationships.php:1014). The carve-out is
// type identity only, never inheritance: relations.MorphPivot (a distinct Go type, the port of
// `class MorphPivot extends Pivot`) and any user struct embedding Pivot are not exempt. Oracle
// v12.55.1, executed (/tmp/strictmorphs-probes/strict_morphs_probe.php): strict base Pivot
// getMorphClass => 'Illuminate\Database\Eloquent\Relations\Pivot'; strict base MorphPivot
// threw => 'Illuminate\Database\ClassMorphViolationException: No morph map defined for model
// [Illuminate\Database\Eloquent\Relations\MorphPivot].'
var pivotMorphType = reflect.TypeOf(relations.Pivot{})

// Register stores a set of alias-to-sample-model bindings. Subsequent calls merge with previously
// registered entries; later writes win on conflict. Pass false in the optional merge argument to
// replace the registry instead of merging.
func Register(entries map[string]any, merge ...bool) {
	doMerge := true
	if len(merge) > 0 {
		doMerge = merge[0]
	}
	mu.Lock()
	defer mu.Unlock()
	if !doMerge {
		aliasToType = map[string]reflect.Type{}
		typeToAlias = map[reflect.Type]string{}
	}
	for alias, sample := range entries {
		typ := indirectType(reflect.TypeOf(sample))
		if typ == nil {
			continue
		}
		// Drop any previous bindings on either side so re-registration leaves a single canonical
		// mapping in both directions.
		if oldType, ok := aliasToType[alias]; ok {
			delete(typeToAlias, oldType)
		}
		if oldAlias, ok := typeToAlias[typ]; ok {
			delete(aliasToType, oldAlias)
		}
		aliasToType[alias] = typ
		typeToAlias[typ] = alias
	}
}

// Find returns a fresh pointer to a new instance of the model registered under alias, or nil if
// no model is registered.
func Find(alias string) any {
	mu.RLock()
	typ, ok := aliasToType[alias]
	mu.RUnlock()
	if !ok {
		return nil
	}
	return reflect.New(typ).Interface()
}

// AliasOf returns the alias registered for the given model's underlying type.
func AliasOf(model any) (string, bool) {
	typ := indirectType(reflect.TypeOf(model))
	if typ == nil {
		return "", false
	}
	mu.RLock()
	defer mu.RUnlock()
	alias, ok := typeToAlias[typ]
	return alias, ok
}

// All returns a snapshot copy of the alias-to-type registry.
func All() map[string]reflect.Type {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]reflect.Type, len(aliasToType))
	for alias, typ := range aliasToType {
		out[alias] = typ
	}
	return out
}

// Reset clears all entries. Intended for tests. Like Relation::morphMap([], false) it leaves the
// strict flag untouched — reset that with RequireMorphMap(false), the two halves of the corpus's
// tearDown (DatabaseEloquentStrictMorphsTest.php:71-77).
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	aliasToType = map[string]reflect.Type{}
	typeToAlias = map[reflect.Type]string{}
}

// RequireMorphMap ports Relation::requireMorphMap (Relation.php:437): it turns the process-level
// strict-morph flag on — or off when passed false, the explicit form Laravel's $requireMorphMap
// parameter takes. The default is true, matching the PHP default argument.
func RequireMorphMap(requireMorphMap ...bool) {
	strict := true
	if len(requireMorphMap) > 0 {
		strict = requireMorphMap[0]
	}
	mu.Lock()
	defer mu.Unlock()
	requireMorphMapFlag = strict
}

// RequiresMorphMap is Relation::requiresMorphMap (Relation.php:447): the getter half of the flag.
func RequiresMorphMap() bool {
	mu.RLock()
	defer mu.RUnlock()
	return requireMorphMapFlag
}

// EnforceMorphMap is Relation::enforceMorphMap (Relation.php:459): requireMorphMap(true) plus
// Register(entries, merge...) — registering the map and switching strict mode on in one call.
// PHP returns the resulting map; the Go registry's Register has no return, so neither does this.
func EnforceMorphMap(entries map[string]any, merge ...bool) {
	RequireMorphMap()
	Register(entries, merge...)
}

// MorphValue resolves the morph class value for a model — the port of Eloquent's
// Model::getMorphClass (HasRelationships.php:1006). Resolution order:
//  1. model.MorphClass() if the model (or its pointer) implements ModelWithMorphClass — an
//     override replaces the whole resolution, exactly as a getMorphClass override does in PHP
//  2. global morph map (registered via Register) — HasRelationships.php:1011
//  3. the base relations.Pivot type — the `static::class === Pivot::class` carve-out
//     (HasRelationships.php:1014): exempt from strict checking and resolved to its own class name
//  4. strict mode (RequireMorphMap / EnforceMorphMap): any other unmapped model raises
//     errors.OrmClassMorphViolation — the ClassMorphViolationException throw at
//     HasRelationships.php:1019
//
// Returns "" and false with a nil error when nothing resolves and strict mode is off. The caller
// is then expected to fall back to GORM's `polymorphicValue:` tag or the parent's table name
// (where PHP would return the class name — the documented divergence on orm.MorphAlias).
func MorphValue(model any) (string, bool, error) {
	if alias, ok := tryMorphClass(model); ok && alias != "" {
		return alias, true, nil
	}
	if alias, ok := AliasOf(model); ok {
		return alias, true, nil
	}
	if typ := indirectType(reflect.TypeOf(model)); typ != nil {
		if typ == pivotMorphType {
			return classOf(typ), true, nil
		}
		if RequiresMorphMap() {
			return "", false, errors.OrmClassMorphViolation.Args(classOf(typ))
		}
	}
	return "", false, nil
}

// classOf renders a type the way PHP's get_class renders a class: package path plus type name,
// e.g. "github.com/gradii/framework/database/gorm/relations.Pivot". It names the model in
// errors.OrmClassMorphViolation and is the value the base-Pivot carve-out resolves to. Types
// without a package path fall back to reflect's own rendering.
func classOf(t reflect.Type) string {
	if t.PkgPath() == "" {
		return t.String()
	}
	return t.PkgPath() + "." + t.Name()
}

// tryMorphClass invokes MorphClass() on model whether it has a value-receiver method or a
// pointer-receiver method, and whether the caller passed a value or a pointer.
func tryMorphClass(model any) (string, bool) {
	if m, ok := model.(contractsorm.ModelWithMorphClass); ok {
		return m.MorphClass(), true
	}
	rv := reflect.ValueOf(model)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return "", false
		}
		if m, ok := rv.Elem().Interface().(contractsorm.ModelWithMorphClass); ok {
			return m.MorphClass(), true
		}
	case reflect.Struct:
		ptr := reflect.New(rv.Type())
		ptr.Elem().Set(rv)
		if m, ok := ptr.Interface().(contractsorm.ModelWithMorphClass); ok {
			return m.MorphClass(), true
		}
	}
	return "", false
}

func indirectType(t reflect.Type) reflect.Type {
	if t == nil {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}
