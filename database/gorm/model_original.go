package gorm

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// This file carries Eloquent's original-attribute tracking to Go models — the "no getOriginal()"
// residue this port had left from the 1:1 campaign.
//
// Eloquent keeps each model's loaded attribute values in its $original bag. syncOriginal fills the
// bag with the current attributes (HasAttributes.php:2142), hydration leaves it equal to the values
// read from the row (setRawAttributes with $sync), and Save leans on it three ways (Model.php:1215,
// :1277, :1294): save() skips performUpdate entirely while the model is clean (`$saved =
// $this->isDirty() ? $this->performUpdate($query) : true`), performUpdate updates exactly
// getDirtyForUpdate() — the attributes that changed since the last sync — and nothing when that set
// is empty (`if (count($dirty) > 0)`), and finishSave syncs the bag again so the written values
// become the model's new baseline. getDirty (HasAttributes.php:2277) and originalIsEquivalent
// (HasAttributes.php:2326) are the comparison behind all of that.
//
// A Go model is a plain struct with no bag to hold the snapshot, so the ORM keeps the bag beside
// the model: syncOriginal captures the model's field values when a row is scanned into it — the Go
// form of setRawAttributes($attributes, sync: true) during a find — and refreshes them after a
// successful insert or update, the finishSave step. The bag is keyed by the model's pointer
// identity and concrete type and reclaimed by runtime.AddCleanup once the model is unreachable
// (verified: heap models, slice elements and escaped locals all release their entries after a GC
// cycle).
//
// Divergences from the PHP shape, all deliberate:
//
//   - A model with no captured bag reads as one whose original bag is empty: every attribute is
//     dirty. That is exactly the whole-struct write Save performed before this file existed, so
//     models built by hand keep their behaviour; Eloquent reaches the same reading for a manually
//     populated instance with the exists flag forced (`$model->id = 1; $model->name = 'x';
//     $model->exists = true; $model->save()` — oracle, executed, probe case C: the update carries
//     `set "id" = ?, "name" = ?, "updated_at" = ? where "id" = ?`). The port's primary key never
//     joins an update (saveAssignments' documented rule), so the hand-built Go model compiles
//     `set "name" = ?, "updated_at" = ? where "id" = ?`.
//
//   - Between an unreachable model's collection and its cleanup running, a newly allocated model
//     of the same type at the same address could read the dead model's bag. The window is one GC
//     cycle plus cleanup dispatch and requires an allocator address reuse in between; Go exposes no
//     liveness query for a raw pointer, so the residual risk is documented rather than
//     preventable. Its worst case is a dirty-only write against a stranger's snapshot.
//
//   - Zero stands in for "unset" the way it does across this port, so a hand-built model's zero
//     updated_at is stamped at save time exactly as Eloquent stamps an attribute it does not hold.
//
//   - The comparison set for dirty reads the schema's non-primary columns: a struct has no
//     attribute bag, so "the attributes" are the model's columns, and a value the struct holds in
//     a zero field is indistinguishable from an unset one.
//
// Oracle probes executed against illuminate/database v12.55.1 (each prints "illuminate/database:
// v12.55.1" on stderr):
//
//	php .claude/laravel-oracle/oracle.php /tmp/save11/semantics_probe.php
//	php .claude/laravel-oracle/oracle.php /tmp/save11/events_probe.php
//
// The receipts behind Save's alignment: a loaded-and-changed model compiles `update "s11_users" set
// "name" = ?, "updated_at" = ? where "id" = ?` binding ["new","2026-11-03 01:02:03",1] — the
// save-time stamp, not the loaded 2020-01-01 value; a clean model runs NO statement and keeps its
// stored updated_at; a caller-set updated_at (alone or beside other changes) is written as given,
// never re-stamped; a loaded NULL updated_at is stamped on a dirty save; moving updated_at within
// the same second reads clean; the second save after a dirty one runs nothing. The event flow is
// [saving, updating, updated, saved] for the dirty save and [saving, saved] for the clean one.

// originalKey identifies one model object in the original-attribute registry: its pointer identity
// and concrete struct type. The type guards the key against a recycled address serving a different
// model shape.
type originalKey struct {
	pointer   uintptr
	modelType reflect.Type
}

// originalState is one model's original bag. The values map is written whole by syncOriginal and
// only read afterwards; concurrent access to a single model is the caller's concern, as it is in
// Eloquent.
type originalState struct {
	values map[string]any
}

var modelOriginalState sync.Map

// deleteModelOriginal drops a dead model's bag. It is the cleanup captureModelOriginal registers
// through runtime.AddCleanup.
func deleteModelOriginal(key originalKey) {
	modelOriginalState.Delete(key)
}

// syncOriginal is Model::syncOriginal (HasAttributes.php:2142): the original bag becomes the
// model's current attribute values. The ORM calls it after hydration and after every successful
// insert or update (finishSave); it accepts a model pointer, a slice or array of models (or model
// pointers), or a pointer to either, and ignores anything else — maps and value copies carry no
// identity to key a bag by.
func syncOriginal(model any) {
	if model == nil {
		return
	}

	syncOriginalValue(reflect.ValueOf(model))
}

func syncOriginalValue(value reflect.Value) {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return
		}

		element := value.Elem()
		if element.Kind() == reflect.Slice || element.Kind() == reflect.Array {
			syncOriginalElements(element)

			return
		}

		captureModelOriginal(value)
	case reflect.Slice, reflect.Array:
		syncOriginalElements(value)
	}
}

// syncOriginalElements captures every model element of a slice or array. A value element is
// captured through its address — the identity Save is later called with (&slice[i]).
func syncOriginalElements(elements reflect.Value) {
	for index := 0; index < elements.Len(); index++ {
		item := elements.Index(index)
		switch item.Kind() {
		case reflect.Pointer:
			if !item.IsNil() {
				captureModelOriginal(item)
			}
		default:
			if item.CanAddr() {
				captureModelOriginal(item.Addr())
			}
		}
	}
}

// captureModelOriginal stores one model's current field values as its original bag. Pointer chains
// resolve to the innermost struct, so a pointer-to-pointer destination (the shape Create(&u) and
// Save(&u) accept) keys the same object the scan wrote into. The runtime.AddCleanup attached on
// first capture reclaims the entry when the model becomes unreachable; re-captures replace the
// values in place and leave the cleanup alone, so a model saved many times accumulates no cleanup
// records.
func captureModelOriginal(pointer reflect.Value) {
	for pointer.Kind() == reflect.Pointer && !pointer.IsNil() && pointer.Elem().Kind() == reflect.Pointer {
		pointer = pointer.Elem()
	}
	if pointer.Kind() != reflect.Pointer || pointer.IsNil() {
		return
	}

	element := pointer.Elem()
	if element.Kind() != reflect.Struct {
		return
	}

	schema, err := modelStateSchema(pointer.Interface())
	if err != nil {
		return
	}

	values := make(map[string]any, len(schema.Fields))
	for _, field := range schema.Fields {
		value, _ := field.ValueOf(context.Background(), element)
		values[field.DBName] = value
	}

	key := originalKey{pointer: pointer.Pointer(), modelType: element.Type()}
	if existing, ok := modelOriginalState.Load(key); ok {
		if state, ok := existing.(*originalState); ok {
			state.values = values

			return
		}
	}

	modelOriginalState.Store(key, &originalState{values: values})

	runtime.AddCleanup((*byte)(pointer.UnsafePointer()), deleteModelOriginal, key)
}

// originalStateOf reads a model's captured original bag, reporting false when the model has no
// identity or no bag — Eloquent's empty-$original case.
func originalStateOf(model any) (map[string]any, bool) {
	key, ok := originalKeyOf(model)
	if !ok {
		return nil, false
	}

	entry, ok := modelOriginalState.Load(key)
	if !ok {
		return nil, false
	}

	state, ok := entry.(*originalState)
	if !ok {
		return nil, false
	}

	return state.values, true
}

// originalKeyOf resolves a model value to its registry key: pointer chains resolve to the innermost
// non-nil struct pointer, and anything else reports no identity.
func originalKeyOf(model any) (originalKey, bool) {
	value := reflect.ValueOf(model)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return originalKey{}, false
		}
		if value.Elem().Kind() != reflect.Pointer {
			break
		}

		value = value.Elem()
	}
	if value.Kind() != reflect.Pointer {
		return originalKey{}, false
	}

	element := value.Elem()
	if element.Kind() != reflect.Struct {
		return originalKey{}, false
	}

	return originalKey{pointer: value.Pointer(), modelType: element.Type()}, true
}

// modelStateSchema describes a model for the state registry or the dirty comparison. A model that
// names no table can still be loaded when the query names one (Table("...")), so the table is not
// demanded here — only the columns are read.
func modelStateSchema(model any) (*modelSchema, error) {
	schema, err := parseSchema(model)
	if err == nil {
		return schema, nil
	}

	return parseSchemaWithTable(model, "-")
}

// getDirty is Model::getDirty (HasAttributes.php:2277) over a Go model: a column is dirty when the
// original bag does not carry it or its current value is not equivalent. The primary key is left
// out — this port's documented rule that the key identifies the row rather than describing it; it
// is the one place the comparison set differs from Eloquent's, whose dirty array can carry the key
// when the original bag never held one (executed probe case C). The schema is passed in because
// the Save path resolved it against the query's table already.
func getDirty(schema *modelSchema, model any) map[string]any {
	value := reflect.ValueOf(model)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}

		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil
	}

	original, hasOriginal := originalStateOf(model)

	dirty := make(map[string]any)
	for _, field := range schema.Fields {
		if field.primaryKey {
			continue
		}

		current, _ := field.ValueOf(context.Background(), value)
		if snapshot, known := original[field.DBName]; hasOriginal && known && originalIsEquivalent(current, snapshot) {
			continue
		}

		dirty[field.DBName] = current
	}

	return dirty
}

// isDirty is Model::isDirty (HasAttributes.php:2198): any writable attribute differs from the
// original bag. A model the registry cannot describe reads dirty, which keeps every existing write
// path — a map, a value struct, a model without a schema — on its previous behaviour.
func isDirty(model any) bool {
	schema, err := modelStateSchema(model)
	if err != nil {
		return true
	}

	return len(getDirty(schema, model)) > 0
}

// originalIsEquivalent is HasAttributes::originalIsEquivalent (HasAttributes.php:2326) for the
// shapes a Go model carries: a null side matches only a null; strict identity wins; two time-typed
// values compare through their second-precision standard form, the date branch of fromDateTime
// under the default 'Y-m-d H:i:s' format; two numbers compare through their string forms (PHP's
// strcmp((string) $a, (string) $b) — the branch that keeps 1 and "1" clean); and equal composite
// values (a []byte column, say) compare by value. The cast pipeline branches have no Go counterpart
// — a field's declared type is its only representation.
func originalIsEquivalent(attribute, original any) bool {
	if attribute == nil || original == nil {
		return attribute == nil && original == nil
	}
	if attributeIdentical(attribute, original) {
		return true
	}
	if timeAttributesEquivalent(attribute, original) {
		return true
	}
	if isNumericAttribute(attribute) && isNumericAttribute(original) && fmt.Sprint(attribute) == fmt.Sprint(original) {
		return true
	}

	return reflect.DeepEqual(attribute, original)
}

// timeAttributesEquivalent is the date branch of originalIsEquivalent: two time-typed values compare
// through their second-precision standard forms, the way fromDateTime() renders a date attribute
// with the default 'Y-m-d H:i:s' date format (sub-second precision formats away). Oracle
// v12.55.1, executed (semantics probe, case 3d): moving updated_at to the same second with a
// half-second offset reads clean — getDirty [] and save() runs no statement.
func timeAttributesEquivalent(left, right any) bool {
	leftTime, ok := standardTimeOf(left)
	if !ok {
		return false
	}

	rightTime, ok := standardTimeOf(right)
	if !ok {
		return false
	}

	return leftTime.Truncate(time.Second).Equal(rightTime.Truncate(time.Second))
}

// standardTimeOf reads a value as a standard time, when it carries one: the built-in time types,
// and the carbon types whose promoted StdTime() exposes the instant (carbon.StdTime is an alias of
// time.Time). A nil pointer carries no time.
func standardTimeOf(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed, true
	case *time.Time:
		if typed == nil {
			return time.Time{}, false
		}

		return *typed, true
	case interface{ StdTime() time.Time }:
		if reflected := reflect.ValueOf(typed); reflected.Kind() == reflect.Pointer && reflected.IsNil() {
			return time.Time{}, false
		}

		return typed.StdTime(), true
	}

	return time.Time{}, false
}

// attributeIdentical is PHP's === for the scalar shapes attribute values carry: same dynamic type
// and equal value; two nils are identical; a comparable type (time.Time among them) compares with
// ==. Structured, non-comparable values report false here and are left to the DeepEqual branch of
// originalIsEquivalent. This duplicates relations/pivot.go's helper of the same name — accepted
// duplication, the two packages are independent.
func attributeIdentical(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() {
		return false
	}

	switch leftValue.Kind() {
	case reflect.Bool:
		return leftValue.Bool() == rightValue.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return leftValue.Int() == rightValue.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return leftValue.Uint() == rightValue.Uint()
	case reflect.Float32, reflect.Float64:
		return leftValue.Float() == rightValue.Float()
	case reflect.String:
		return leftValue.String() == rightValue.String()
	default:
		if leftValue.Type().Comparable() {
			return leftValue.Interface() == rightValue.Interface()
		}
	}

	return false
}

// isNumericAttribute is the numeric half of PHP's is_numeric over the shapes attribute values
// carry: number kinds, and strings that parse as numbers. Duplicates relations/pivot.go's helper of
// the same name — accepted duplication.
func isNumericAttribute(value any) bool {
	switch typed := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr, float32, float64:
		return true
	case string:
		_, err := strconv.ParseFloat(typed, 64)

		return err == nil
	}

	return false
}

// syncOriginalAttribute is HasAttributes::syncOriginalAttribute (HasAttributes.php:2175): one
// attribute's entry in the original bag takes the model's current value, so that attribute reads
// clean while every other changed attribute stays dirty. Model::incrementOrDecrement runs it on
// the column it moved (Model.php:1063) while the extra values it force-filled stay dirty — which
// is why a save() after an incrementOrCreate with extras writes the extras and the refreshed
// updated_at alone (oracle v12.55.1, executed: /tmp/cof_inc_events_probe.php — the follow-up save
// compiles `update "table" set "val" = ?, "updated_at" = ? where "id" = ?`). A model with no
// captured bag has no baseline to update and keeps its whole-struct-dirty reading; Eloquent's
// empty-$original case reads the same way for a hand-built instance.
func syncOriginalAttribute(model any, attribute string) {
	schema, err := modelStateSchema(model)
	if err != nil {
		return
	}

	field, ok := schema.FieldsByName[attribute]
	if !ok {
		field, ok = schema.FieldsByDBName[attribute]
	}
	if !ok {
		return
	}

	key, ok := originalKeyOf(model)
	if !ok {
		return
	}

	entry, ok := modelOriginalState.Load(key)
	if !ok {
		return
	}

	state, ok := entry.(*originalState)
	if !ok || state.values == nil {
		return
	}

	value := reflect.ValueOf(model)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}

		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return
	}

	current, _ := field.ValueOf(context.Background(), value)
	state.values[field.DBName] = current
}
