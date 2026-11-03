package gorm

import (
	"fmt"
	"reflect"

	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// This file implements Laravel's SupportsDefaultModels semantics for the relation kinds whose
// descriptors carry WithDefault — BelongsTo, HasOne, MorphOne, MorphTo and HasOneThrough. A
// relation read that finds no row (or whose parent key, foreign key, or morph type is empty) loads
// the relation's default related instance instead of nil, exactly where Laravel's getResults() /
// eager initRelation would:
//
//   - BelongsTo: `$this->query->first() ?: $this->getDefaultFor($this->parent)`, with the default
//     also standing in when the parent's foreign key is null (the is_null short-circuit).
//   - HasOne: the same shape on the parent key; the default instance is a fresh related instance
//     whose foreign key is pre-set from the parent's local key (HasOne::newRelatedInstanceFor).
//   - MorphTo: BelongsTo::getResults checks the morph id column; the default instance's class is
//     derived from the parent's morph type column — a type registered in the morph map (or a morph
//     class name) yields that class, while an empty type column yields the parent's own class,
//     because newRelatedInstanceFor re-invokes $parent->relation() and a null type takes the
//     morphEagerTo branch, which carries the parent's own query as the related model.
//   - MorphOne: the same shape on the parent local key; the default instance pre-sets the
//     polymorphic id column from the parent's local key and the type column to the relation's
//     morph value, like MorphOne::newRelatedInstanceFor.
//   - HasOneThrough: the same shape on the parent local key; the default instance is a plain
//     newInstance of the related model, like HasOneThrough::newRelatedInstanceFor.
//
// A row, when one exists, always wins: matching results overwrite the seeded defaults, mirroring
// Laravel's initRelation-then-match ordering. The merge semantics were measured by executing the
// corpus's scenarios through the behavioral oracle (.claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1, script .claude/laravel-oracle/withdefault_oracle.php,
// in-memory sqlite): a fresh
// default carries attributes [] ({"foreign_key":1} for HasOne) with exists false, the closure and
// array forms fill it, an empty map / false yield no default, a callback returning a PHP-falsy
// value (false / 0 / 0.0 / "" / "0" / [] / null — probed: all report result-is-related => true)
// keeps the fresh instance while only a truthy return replaces it, and `$parent->relation()->first()`
// — the Go Related() query — ignores the default even when declared. The MorphOne /
// HasOneThrough defaults were probed the same way: a fresh instance (id + type set for MorphOne,
// no attributes for HasOneThrough) with exists false, eager parents seeded, matched rows winning.

// phpFalsy reports whether a callback's return is falsy under PHP's `?:` — the condition Laravel's
// SupportsDefaultModels::getDefaultFor applies to `call_user_func($callback, $instance, $parent)
// ?: $instance`. The set the oracle pins (illuminate/database v12.55.1): false, 0, 0.0, "", "0",
// an empty slice or map, and nil keep the fresh instance; every object — a Go struct or a non-nil
// pointer — is truthy (a Go slice/map maps to PHP's array, where only the empty one is falsy; a
// PHP object, e.g. an empty Collection, is truthy under `?:` and replaces the instance).
func phpFalsy(value any) bool {
	if value == nil {
		return true
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() == 0
	case reflect.String:
		return rv.String() == "" || rv.String() == "0"
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

// relationDefaultFor returns a builder for desc's default related row per parent, or nil when the
// descriptor carries no WithDefault. The closure mirrors getDefaultFor(parent): a bool default
// yields the fresh instance, a map force-fills it, and a callback may replace it with a PHP-truthy
// return (a PHP-falsy return keeps the fresh instance).
func (r *OrmBuilder) relationDefaultFor(desc *relationDescriptor, parentModel any) func(parent reflect.Value) ([]reflect.Value, error) {
	if desc.WithDefault == nil {
		return nil
	}

	return func(parent reflect.Value) ([]reflect.Value, error) {
		row, ok, err := r.buildRelationDefault(desc, parent, parentModel)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return []reflect.Value{row}, nil
	}
}

// buildRelationDefault materialises the descriptor's declared default — buildRelationDefaultValue
// with the declaration's WithDefault value. The eager path (relationDefaultFor) uses this: eager
// reads are driven by the declaration, never by a call-time WithDefault on some relation object.
func (r *OrmBuilder) buildRelationDefault(desc *relationDescriptor, parent reflect.Value, parentModel any) (reflect.Value, bool, error) {
	return r.buildRelationDefaultValue(desc, desc.WithDefault, parent, parentModel)
}

// buildRelationDefaultValue materialises Laravel's getDefaultFor(parent) from value — the
// relation's effective default: the declaration's WithDefault option, or the value a call-time
// WithDefault call replaced it with (SupportsDefaultModels::withDefault). nil yields no default —
// the Go reading of the falsy `if (! $this->withDefault) return;` gate; false and an empty map are
// falsy too (ok=false, the way PHP treats both — oracle: withDefault([]) / withDefault(false) are
// null). A callback's return follows Laravel's `?:`: a PHP-falsy return keeps the fresh instance,
// a PHP-truthy one installs the returned value.
func (r *OrmBuilder) buildRelationDefaultValue(desc *relationDescriptor, value any, parent reflect.Value, parentModel any) (reflect.Value, bool, error) {
	if value == nil {
		return reflect.Value{}, false, nil
	}

	instance, err := r.newDefaultRelatedInstance(desc, parent, parentModel)
	if err != nil {
		return reflect.Value{}, false, err
	}

	switch value := value.(type) {
	case bool:
		if !value {
			return reflect.Value{}, false, nil
		}
	case map[string]any:
		if len(value) == 0 {
			return reflect.Value{}, false, nil
		}
		if err := r.forceFillDefault(instance, value); err != nil {
			return reflect.Value{}, false, err
		}
	case func(related, parent any) any:
		// Laravel's `call_user_func($callback, $instance, $parent) ?: $instance`: a PHP-truthy
		// return replaces the instance, a PHP-falsy return keeps the fresh one.
		if replacement := value(instance.Interface(), parentAsAny(parent)); !phpFalsy(replacement) {
			return reflect.ValueOf(replacement), true, nil
		}
	default:
		return reflect.Value{}, false, errors.OrmRelationWithDefaultInvalid.Args(fmt.Sprintf("%T", value), desc.Name)
	}

	return instance, true, nil
}

// newDefaultRelatedInstance builds the per-kind fresh instance Laravel's newRelatedInstanceFor
// produces.
func (r *OrmBuilder) newDefaultRelatedInstance(desc *relationDescriptor, parent reflect.Value, parentModel any) (reflect.Value, error) {
	switch desc.Kind {
	case relKindBelongsTo:
		// BelongsTo::newRelatedInstanceFor: $this->related->newInstance() — a plain instance.
		return newRelatedInstance(desc.RelatedModel, desc, nil)
	case relKindHasOne:
		// HasOne::newRelatedInstanceFor: the fresh instance with the foreign key set from the
		// parent's local key, plus the inverse relation when one is in effect — the live state,
		// cleared by WithoutChaperone (newRelatedInstanceFor applies it to the default instance
		// too; oracle probe_c.php E/F/H report linked false after a clear).
		instance, err := newRelatedInstance(desc.RelatedModel, desc, nil)
		if err != nil {
			return reflect.Value{}, err
		}
		if err := r.setDefaultForeignKey(desc, instance, parent, parentModel); err != nil {
			return reflect.Value{}, err
		}
		if err := r.applyInverseRelationToModel(desc, instance, parent); err != nil {
			return reflect.Value{}, err
		}
		return instance, nil
	case relKindMorphOne:
		// MorphOne::newRelatedInstanceFor: the fresh instance with the polymorphic id column set
		// from the parent's local key and the type column set to the relation's morph value, plus
		// the inverse relation when one is in effect (the live state, cleared by WithoutChaperone).
		instance, err := newRelatedInstance(desc.RelatedModel, desc, nil)
		if err != nil {
			return reflect.Value{}, err
		}
		if err := r.setDefaultForeignKey(desc, instance, parent, parentModel); err != nil {
			return reflect.Value{}, err
		}
		if err := r.setDefaultMorphType(desc, instance); err != nil {
			return reflect.Value{}, err
		}
		if err := r.applyInverseRelationToModel(desc, instance, parent); err != nil {
			return reflect.Value{}, err
		}
		return instance, nil
	case relKindMorphTo:
		return r.newMorphToDefaultInstance(desc, parent, parentModel)
	case relKindHasOneThrough:
		// HasOneThrough::newRelatedInstanceFor: $this->related->newInstance() — a plain instance.
		return newRelatedInstance(desc.RelatedModel, desc, nil)
	default:
		return reflect.Value{}, errors.OrmRelationWithDefaultInvalid.Args(fmt.Sprintf("%T", desc.WithDefault), desc.Name)
	}
}

// newRelatedInstance returns a fresh pointer to the model's struct type. An explicit model (used
// by MorphTo, whose class comes from the type column) overrides desc.relatedModel, which those
// descriptors leave nil.
func newRelatedInstance(model any, desc *relationDescriptor, explicit any) (reflect.Value, error) {
	if explicit != nil {
		model = explicit
	}
	modelType := indirectStructType(reflect.TypeOf(model))
	if modelType == nil {
		return reflect.Value{}, errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, "no related model")
	}
	return reflect.New(modelType), nil
}

// setDefaultForeignKey writes the parent's local-key value into the default instance's foreign
// key — the Go form of HasOne::newRelatedInstanceFor's setAttribute(getForeignKeyName(),
// $parent->{$this->localKey}).
func (r *OrmBuilder) setDefaultForeignKey(desc *relationDescriptor, instance reflect.Value, parent reflect.Value, parentModel any) error {
	if len(desc.References) == 0 {
		return nil
	}
	ref := desc.References[0]

	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return err
	}
	parentField, ok := parentSchema.FieldsByDBName[ref.PrimaryColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(desc.Name, parentSchema.Name, "no parent field for "+ref.PrimaryColumn)
	}
	value, _ := parentField.ValueOf(r.ctx, parent)

	relatedSchema, err := parseSchema(desc.RelatedModel)
	if err != nil {
		return err
	}
	foreignField, ok := relatedSchema.FieldsByDBName[ref.ForeignColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(desc.Name, relatedSchema.Name, "no related FK field for "+ref.ForeignColumn)
	}

	return foreignField.Set(r.ctx, instance, value)
}

// setDefaultMorphType writes the relation's morph value into the default instance's type column —
// the Go form of MorphOne::newRelatedInstanceFor's setAttribute($this->getMorphType(),
// $this->morphClass).
func (r *OrmBuilder) setDefaultMorphType(desc *relationDescriptor, instance reflect.Value) error {
	schema, err := parseSchema(desc.RelatedModel)
	if err != nil {
		return err
	}
	typeField, ok := schema.FieldsByDBName[desc.MorphTypeColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(desc.Name, schema.Name, "no morph type field "+desc.MorphTypeColumn)
	}

	return typeField.Set(r.ctx, instance, desc.MorphValue)
}

// newMorphToDefaultInstance resolves the default's class the way MorphTo::newRelatedInstanceFor
// does — through $parent->relation(), whose type column decides between morphInstanceTo (a
// registered type resolves to its class) and morphEagerTo (an empty type column resolves to the
// parent's own class).
func (r *OrmBuilder) newMorphToDefaultInstance(desc *relationDescriptor, parent reflect.Value, parentModel any) (reflect.Value, error) {
	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return reflect.Value{}, err
	}
	typeField, ok := parentSchema.FieldsByDBName[desc.MorphTypeColumn]
	if !ok {
		return reflect.Value{}, errors.OrmRelationUnsupported.Args(desc.Name, parentSchema.Name, "no parent field for "+desc.MorphTypeColumn)
	}
	// The type may sit behind a pointer or a driver.Valuer wrapper (a nullable morph_type column);
	// the morph map must see the value the field carries, never the pointer address, as loadMorphTo reads it.
	typeValue, _ := typeField.ValueOf(r.ctx, parent)
	morphType := morphTypeToString(derefKeyValue(typeValue))

	if morphType == "" {
		parentType := indirectStructType(parent.Type())
		if parentType == nil {
			return reflect.Value{}, errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, "no parent model")
		}
		return reflect.New(parentType), nil
	}

	sample := morphmap.Find(morphType)
	if sample == nil {
		return reflect.Value{}, errors.OrmMorphTypeUnknown.Args(morphType)
	}
	return newRelatedInstance(desc.RelatedModel, desc, sample)
}

// forceFillDefault copies the default's attribute map onto the instance — the Go form of
// forceFill. Keys name the related model's columns; entries that name no column are skipped, the
// way row hydration skips columns the destination does not map.
func (r *OrmBuilder) forceFillDefault(instance reflect.Value, attributes map[string]any) error {
	schema, err := parseSchema(instance.Interface())
	if err != nil {
		return err
	}
	for key, value := range attributes {
		field, ok := schema.FieldsByDBName[key]
		if !ok {
			continue
		}
		if err := field.Set(r.ctx, instance, value); err != nil {
			return err
		}
	}
	return nil
}

// setRelationDefaultOrNil assigns the relation's default row to parent when WithDefault is
// active, and clears the field otherwise — the state Laravel's initRelation leaves (the default
// when declared, null when not).
func (r *OrmBuilder) setRelationDefaultOrNil(parent reflect.Value, relation string, desc *relationDescriptor, parentModel any) error {
	if desc.WithDefault == nil {
		return setRelationField(parent, relation, nil)
	}
	row, ok, err := r.buildRelationDefault(desc, parent, parentModel)
	if err != nil {
		return err
	}
	if !ok {
		return setRelationField(parent, relation, nil)
	}
	return setRelationField(parent, relation, []reflect.Value{row})
}

// parentAsAny hands a callback the actual parent model. Parents arrive as addressable struct
// values, so the pointer is taken; a copy is the fallback for a non-addressable value.
func parentAsAny(parent reflect.Value) any {
	if parent.CanAddr() {
		return parent.Addr().Interface()
	}
	return parent.Interface()
}
