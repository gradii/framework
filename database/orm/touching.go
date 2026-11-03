package orm

import (
	"reflect"
	"sync"

	"github.com/gradii/framework/database/internal/annotation"
)

// This file ports Eloquent's model-level touch control to Go: Model::withoutTouching /
// Model::withoutTouchingOn and Model::isIgnoringTouch.
//
// Eloquent keeps one static $ignoreOnTouch list per process and consults it from
// Relation::touch() (and BelongsToMany::touchIfTouching) through isIgnoringTouch(), which
// matches a class when it equals an ignored class or extends it. Go has no class hierarchy, so a
// model "extends" another when its struct embeds it — the touching fixtures embed the
// framework's base Model, mirroring `extends Eloquent` — and the list below therefore holds
// reflect.Types matched by equality or by embedding.

// touchingIgnores is the process-wide list of model classes whose touches are ignored while a
// WithoutTouchingOn scope is open. Eloquent's protected static $ignoreOnTouch.
var touchingIgnores struct {
	mu    sync.RWMutex
	types []reflect.Type
}

// WithoutTouching runs callback with relationship touching disabled for every model: the Go
// form of Model::withoutTouching, which enters withoutTouchingOn([static::class]) for the base
// Model class — and with it every Go model that embeds Model, the way every Eloquent model
// extends the Eloquent base.
func WithoutTouching(callback func() error) error {
	return WithoutTouchingOn([]any{Model{}}, callback)
}

// WithoutTouchingOn runs callback with relationship touching disabled for the given models'
// classes, mirroring Model::withoutTouchingOn: the classes join the process-wide ignore list
// for the duration of the callback and leave it again when the callback returns — an error or a
// panic included, the defer standing in for Eloquent's try / finally. The restore drops every
// occurrence of the entered classes, exactly as Eloquent's
// `array_diff(static::$ignoreOnTouch, $models)` does; a class entered by two nested scopes is
// therefore fully removed when the first of them ends, the Laravel behaviour.
//
// Eloquent takes class names where the Go port takes sample model instances (the MorphMap
// convention): a class is derived from each value's struct type, and a value that is not a
// model is ignored.
func WithoutTouchingOn(models []any, callback func() error) error {
	types := modelTypesOf(models)
	if len(types) > 0 {
		touchingIgnores.mu.Lock()
		touchingIgnores.types = append(touchingIgnores.types, types...)
		touchingIgnores.mu.Unlock()
	}

	defer func() {
		touchingIgnores.mu.Lock()
		defer touchingIgnores.mu.Unlock()

		kept := touchingIgnores.types[:0]
		for _, entering := range touchingIgnores.types {
			if !containsType(types, entering) {
				kept = append(kept, entering)
			}
		}
		touchingIgnores.types = kept
	}()

	return callback()
}

// IsIgnoringTouch reports whether the given model's class ignores touches, mirroring
// Model::isIgnoringTouch: true for a model that carries no automatic updated-at column
// (Eloquent's `! get_class_vars($class)['timestamps'] || ! $class::UPDATED_AT`), and true while
// the class — or one of the classes it embeds, the Go form of is_subclass_of — sits in the
// ignore list a WithoutTouchingOn scope installed. A value that is not a model reports false.
func IsIgnoringTouch(model any) bool {
	class := structTypeOf(reflect.TypeOf(model))
	if class == nil {
		return false
	}

	if !carriesUpdatedAt(class) {
		return true
	}

	touchingIgnores.mu.RLock()
	defer touchingIgnores.mu.RUnlock()

	for _, ignored := range touchingIgnores.types {
		if class == ignored || embedsType(class, ignored) {
			return true
		}
	}

	return false
}

// modelTypesOf normalises the values given to a scope into their struct types, dropping values
// that are not models.
func modelTypesOf(models []any) []reflect.Type {
	types := make([]reflect.Type, 0, len(models))
	for _, model := range models {
		if class := structTypeOf(reflect.TypeOf(model)); class != nil {
			types = append(types, class)
		}
	}

	return types
}

// containsType reports whether types holds class.
func containsType(types []reflect.Type, class reflect.Type) bool {
	for _, candidate := range types {
		if candidate == class {
			return true
		}
	}

	return false
}

// structTypeOf dereferences pointers and reports the struct type, or nil for anything else.
func structTypeOf(modelType reflect.Type) reflect.Type {
	for modelType != nil && modelType.Kind() == reflect.Pointer {
		modelType = modelType.Elem()
	}

	if modelType == nil || modelType.Kind() != reflect.Struct {
		return nil
	}

	return modelType
}

// embedsType reports whether class embeds target, directly or through further embedded
// structs — the Go form of PHP's is_subclass_of.
func embedsType(class, target reflect.Type) bool {
	for index := 0; index < class.NumField(); index++ {
		field := class.Field(index)
		if !field.Anonymous {
			continue
		}

		embedded := structTypeOf(field.Type)
		if embedded == nil {
			continue
		}
		if embedded == target || embedsType(embedded, target) {
			return true
		}
	}

	return false
}

// carriesUpdatedAt reports whether the model declares an automatic updated-at column — the Go
// reading of Eloquent's UPDATED_AT constant being non-null (with $timestamps true): a field
// stamped by autoUpdateTime, a field whose gorm tag names the updated_at column, or an
// UpdatedAt field (whose column the ORM's schema then names by convention).
func carriesUpdatedAt(class reflect.Type) bool {
	for index := 0; index < class.NumField(); index++ {
		field := class.Field(index)
		// An anonymous (embedded) field counts however it is exported: embedsType walks every
		// embed, and a model embedding an unexported base still carries the base's updated-at
		// column - skipping the embed here would report it as carrying none.
		if !field.IsExported() && !field.Anonymous {
			continue
		}

		parsed := annotation.ParseAnnotation(field.Tag.Get("gorm"))
		if field.Anonymous && !parsed.Skip {
			if embedded := structTypeOf(field.Type); embedded != nil {
				if carriesUpdatedAt(embedded) {
					return true
				}

				continue
			}
		}
		if parsed.Skip {
			continue
		}

		if parsed.AutoUpdateTime || parsed.Column == "updated_at" || field.Name == "UpdatedAt" {
			return true
		}
	}

	return false
}
