package relations

import (
	"fmt"
	"reflect"
	"strings"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/str"
)

// This file ports Illuminate\Database\Eloquent\Relations\MorphPivot — vendor, illuminate/database
// v12.55.1; MorphPivot.php:5 `class MorphPivot extends Pivot`; byte-identical in the user checkout
// at /Users/dog/OpenSource/framework (diff=0), so the vendor text is the single source. The Go
// embedding (MorphPivot embedding its Pivot) mirrors the PHP extends: the methods PHP defines on
// the class live here, everything else is promoted from the base the way inheritance promotes it.
//
// Class mapping. morphType / morphClass are plain Go fields. PHP declares the two protected
// properties explicitly "so it's not included in saved attributes" (MorphPivot.php:7-23) — Go
// fields are out of the attribute bag by construction. Oracle v12.55.1, executed
// (/tmp/morphpivotprobes/state_probe.php): after setMorphType/setMorphClass the attribute bag
// reads keys ["foreign","other"], the morph key is absent from getAttributes(), and getDirty()
// is [] — the setters dirty nothing. The write path carries the same receipt
// (/tmp/morphpivotprobes/sql_probe.php: the insert reads `insert into "role_user" ("foreign",
// "other") values (?, ?)` — no morph column).
//
// The static constructors become package functions: NewMorphPivot is the Go form of `new static`
// and FromMorphAttributes / FromMorphRawAttributes are `MorphPivot::fromAttributes` /
// `MorphPivot::fromRawAttributes` — the base constructor runs first and the fully built state is
// embedded (PHP's late static binding reads the same: the AsPivot statics run on the subclass and
// return it).
//
// Dynamic dispatch. PHP's save / delete reach setKeysForSaveQuery virtually (Model::performUpdate
// and Model::performDeleteOnModel call `$this->setKeysForSaveQuery`), so a MorphPivot update and
// own-key delete run through the morph override; Go method promotion is static, so Save and
// Delete are re-stated on *MorphPivot with the base flow and the morph-aware key call wired in.
// The morph clause appears TWICE on those paths because the PHP source itself writes it twice:
// MorphPivot::setKeysForSaveQuery (MorphPivot.php:31) adds the morph where and then delegates to
// parent::setKeysForSaveQuery — Pivot's — whose body is `return $this->setKeysForSelectQuery($query)`,
// the MORPH override again — before the base key where is written. Oracle v12.55.1, executed (SQL
// probe): save no PK -> `update "role_user" set "note" = ? where "taggable_type" = ? and
// "taggable_type" = ? and "foreign" = ? and "other" = ?` (bindings ["changed","App\Models\Post",
// "App\Models\Post","fv","ov"]); save with PK -> `... where "taggable_type" = ? and
// "taggable_type" = ? and "id" = ?`; delete with PK -> `delete from "role_user" where
// "taggable_type" = ? and "taggable_type" = ? and "id" = ?`.
//
// Event dispatch. PHP's fireModelEvent names the event after the instance class (static::class)
// and runs it on `$this`, so listeners and observers registered for MorphPivot (or a custom
// subclass) see a morph pivot's events. Go's observer matcher compares the concrete type
// (getObserver: package path + name), so this port re-states fireEvent — and the insert flow it
// is called from — to hand the *MorphPivot to FirePivotEvent; the promoted base method would
// pass the embedded base pointer and dispatch under relations.Pivot.
// Oracle v12.55.1, executed (/tmp/morphpivotprobes/events_probe.php): a MorphPivot save logs
// ["morph.saving"] with a Pivot-registered listener silent, and a custom subclass logs
// ["evprobe.saving"] alone.
//
// The no-key delete quirk. MorphPivot::delete (MorphPivot.php:56-75) overrides the keyless branch
// WITHOUT the touchOwners() call AsPivot::delete makes (AsPivot.php:130,140), and appends the
// morph where after the key-pair group: oracle, executed -> `delete from "role_user" where
// ("foreign" = ? and "other" = ?) and "taggable_type" = ?` (bindings ["fv","ov",
// "App\Models\Post"]). The own-key branch keeps Model::delete's touchOwners (it delegates with
// `return (int) parent::delete()`), as does finishSave's on save.
//
// Divergences from the PHP class, all deliberate:
//
//   - A segmented restoration id with fewer than six segments reports the named
//     errors.OrmMorphPivotRestorationIDInvalid. PHP reads the missing offsets as warnings and the
//     null segment makes the where clause vanish (Arr::wrap(null) adds nothing) — oracle,
//     executed (/tmp/morphpivotprobes/short_id_probe.php): 'foreign:f1:other:o1' on a MorphPivot
//     warns "Undefined array key 4/5" and compiles `select * from "taggables" where "foreign" = ?
//     and "other" = ?` with bindings ["f1","o1"], silently dropping the morph segment; a
//     collection whose later id is short warns per missing offset and drops the null segments the
//     same way. The Go port follows the base Pivot port's named-error policy instead of
//     reproducing the silent drop.
//
//   - A collection restoration whose first id carries no ":" (or an empty list) runs the Model
//     whereKey path directly. Upstream v12.55.1 fatally recurses there — MorphPivot::
//     newQueryForCollectionRestoration's `parent::newQueryForRestoration($ids)` re-dispatches
//     `$this->newQueryForCollectionRestoration` back into the override until the stack dies.
//     Oracle, executed (state probe): `newQueryForRestoration([7, 8])` aborts with "Maximum call
//     stack size ... Infinite recursion?" at MorphPivot.php:162. The Go port completes the branch
//     the code intends — the base restoreByKey (Model::newQueryForRestoration over the qualified
//     pivot key) — the same destination the scalar path reaches.
//
//   - An id with MORE than six segments reads the first six (the extra segments are ignored) —
//     oracle, executed (/tmp/morphpivotprobes/short_id_probe.php): 'foreign:f1:other:o1:
//     taggable_type:A:EXTRA' compiles the same three wheres with bindings ["f1","o1","A"].
//
//   - The morph-class where reads the Go string's zero value as PHP's null default: a
//     class-unset pivot compiles the null check the probe shows (`... and "taggable_type" is
//     null`, bindings ["fv","ov"]); an explicitly empty string class is not distinguishable
//     from unset in Go.
//
//   - GetTable() is re-stated for the type-name fallback: a table-less MorphPivot derives
//     "morph_pivot" from the Go type name (oracle: `(new MorphPivot)->getTable()` reports
//     "morph_pivot"), where promoted base-receiver internals would still read the base name.
//     Every constructor stamps a table, so the fallback only matters for a bare
//     `new MorphPivot`-shaped instance.
//
//   - PivotKeys reports the morph column first — the order setKeysForSelectQuery writes the keys
//     (contracts PivotKeyed; oracle, executed: `$morphPivot->fresh()` compiles `select * from
//     "taggables" where "taggable_type" = ? and "taggable_id" = ? and "tag_id" = ? limit 1`).
//
// Oracle probes executed against illuminate/database v12.55.1 (each prints
// "illuminate/database: v12.55.1" on stderr):
//
//	php .claude/laravel-oracle/oracle.php /tmp/morphpivotprobes/state_probe.php
//	php .claude/laravel-oracle/oracle.php /tmp/morphpivotprobes/sql_probe.php
//	php .claude/laravel-oracle/oracle.php /tmp/morphpivotprobes/short_id_probe.php
//	php .claude/laravel-oracle/oracle.php /tmp/morphpivotprobes/events_probe.php
//
// State probe (key outputs cited above): a fresh pivot's getMorphType() is null; the fluent
// setters return the same instance; attributes keys ["foreign","other"]; dirty [] after the
// setters; queueable ids "foreign:fv:other:ov:taggable_type:App\Models\Post", 7 (own key),
// "foreign::other::t:C" (null values), "foreign:f:other::t:" (unset class); bare table
// "morph_pivot"; restoration SQLs quoted in the method comments; the [7, 8] fatal recursion.
//
// SQL probe (executed against in-memory sqlite): the save shapes quoted above, the timestamped
// save (`set "note" = ?, "updated_at" = ?` before the double morph clause), the insert without
// morph columns, the keyless delete with the appended morph clause, the class-unset delete
// rendering `and "taggable_type" is null`, the own-key delete (double morph clause, affected 1),
// the absent-row keyless delete still running, and the not-existing own-key delete emitting
// nothing.
type MorphPivot struct {
	Pivot

	// morphType / morphClass mirror the PHP protected properties of the same names
	// (MorphPivot.php:14,23): declared so they are NOT part of the attribute bag — the Go fields
	// are out of it by construction, and the setters below dirty nothing.
	morphType  string
	morphClass string
}

// NewMorphPivot builds an empty morph pivot bound to builder — the Go form of `new static` inside
// MorphPivot::fromAttributes: no attributes, no original bag, no table, no parent, not existing,
// no morph state.
func NewMorphPivot(builder Builder) *MorphPivot {
	return &MorphPivot{Pivot: *NewPivot(builder)}
}

// FromMorphAttributes mirrors MorphPivot::fromAttributes($parent, $attributes, $table, $exists =
// false): the base AsPivot constructor runs first (timestamps flag, connection stamp, table,
// attribute and original bags, parent link, exists) and the built state is embedded — the Go
// reading of the trait's statics running on the late-bound subclass.
func FromMorphAttributes(builder Builder, parent any, attributes map[string]any, table string, exists bool) (*MorphPivot, error) {
	pivot, err := FromAttributes(builder, parent, attributes, table, exists)
	if err != nil {
		return nil, err
	}

	return &MorphPivot{Pivot: *pivot}, nil
}

// FromMorphRawAttributes mirrors MorphPivot::fromRawAttributes($parent, $attributes, $table,
// $exists = false): the base raw constructor runs first, exactly as above.
func FromMorphRawAttributes(builder Builder, parent any, attributes map[string]any, table string, exists bool) (*MorphPivot, error) {
	pivot, err := FromRawAttributes(builder, parent, attributes, table, exists)
	if err != nil {
		return nil, err
	}

	return &MorphPivot{Pivot: *pivot}, nil
}

// MorphType is MorphPivot::getMorphType (MorphPivot.php:82). Oracle v12.55.1, executed: null on a
// fresh pivot — the Go zero value.
func (m *MorphPivot) MorphType() string { return m.morphType }

// SetMorphType is MorphPivot::setMorphType (MorphPivot.php:93), fluent; the morph state is not an
// attribute, so the pivot does not dirty. Oracle v12.55.1, executed: returns the same instance.
func (m *MorphPivot) SetMorphType(morphType string) *MorphPivot {
	m.morphType = morphType

	return m
}

// SetMorphClass is MorphPivot::setMorphClass (MorphPivot.php:106), fluent. Oracle v12.55.1,
// executed: returns the same instance; the value stays out of the attribute bag.
func (m *MorphPivot) SetMorphClass(morphClass string) *MorphPivot {
	m.morphClass = morphClass

	return m
}

// SetKeysForSelectQuery is MorphPivot::setKeysForSelectQuery (MorphPivot.php:44): the morph where
// first (MorphPivot.php:49), then the base AsPivot key logic — the own primary key when the
// attributes carry one, else the foreign key / related key pair. The method is protected in PHP;
// the observable SQL shapes are the save / delete statements probed below, which reach it through
// the override chain.
func (m *MorphPivot) SetKeysForSelectQuery(query contractsorm.Query) contractsorm.Query {
	query = query.Where(m.morphType, "=", m.morphClassValue())

	return m.Pivot.SetKeysForSelectQuery(query)
}

// SetKeysForSaveQuery is MorphPivot::setKeysForSaveQuery (MorphPivot.php:31): the morph where
// first, then the parent call. The parent (Pivot's) body is `return $this->setKeysForSelectQuery
// ($query)` — in PHP that re-dispatches into THIS type's SetKeysForSelectQuery, which writes the
// morph clause a second time before the base key where; this Go port mirrors the same two writes
// (see the file comment; oracle SQL quoted there).
func (m *MorphPivot) SetKeysForSaveQuery(query contractsorm.Query) contractsorm.Query {
	query = query.Where(m.morphType, "=", m.morphClassValue())

	return m.SetKeysForSelectQuery(query)
}

// Save mirrors the inherited Model::save (Model.php:1215) with the morph-aware key call on the
// update path: PHP's performUpdate reaches setKeysForSaveQuery virtually, so a MorphPivot update
// carries the morph filter; the Go flow is re-stated here because the promoted base save would
// call the base keys statically. The insert path is identical to the base — an insert runs no key
// where — and is delegated to the morph saveNew below (re-stated so the insert events dispatch on the concrete type).
func (m *MorphPivot) Save() error {
	if err := m.fireEvent("saving"); err != nil {
		return err
	}

	if m.exists {
		return m.saveExistingWithMorphKeys()
	}

	return m.saveNew()
}

// saveExistingWithMorphKeys is the base Pivot.saveExisting flow with m.SetKeysForSaveQuery wired
// in — the update, updated, saved and touchOwners steps and the dirty guard are the base's,
// oracle-pinned there; the SQL delta the morph key adds is probed here (file comment).
func (m *MorphPivot) saveExistingWithMorphKeys() error {
	// Model::save only runs performUpdate when the model is dirty (`$this->isDirty() ?
	// $this->performUpdate($query) : true` - Model.php save()), so a clean pivot skips the
	// updating event and the timestamps stamp entirely; finishSave still fires the saved event
	// and syncs the original bag. Oracle v12.55.1, executed (verifier probe
	// /tmp/morphverify/p1_saveflow.php, cases A/B/E): events [saving, saved], no statement.
	if !m.IsDirty() {
		if err := m.fireEvent("saved"); err != nil {
			return err
		}
		m.SyncOriginal()

		return nil
	}

	if err := m.fireEvent("updating"); err != nil {
		return err
	}

	if m.UsesTimestamps() {
		m.updateTimestamps()
	}

	dirty := m.GetDirty()
	if len(dirty) > 0 {
		query, err := m.builder.NewPivotQuery(m.GetTable(), m.connection)
		if err != nil {
			return err
		}

		if _, err := m.SetKeysForSaveQuery(query).Update(dirty); err != nil {
			return err
		}

		if err := m.fireEvent("updated"); err != nil {
			return err
		}
	}

	if err := m.fireEvent("saved"); err != nil {
		return err
	}
	if len(dirty) > 0 {
		if err := m.builder.PivotTouchOwners(m); err != nil {
			return err
		}
	}
	m.SyncOriginal()

	return nil
}

// fireEvent re-states the base dispatch over the concrete morph pivot: PHP's fireModelEvent runs
// on `$this`, and this ORM's observer matcher keys on the concrete type, so passing the embedded
// base pointer (what the promoted method does) would dispatch a MorphPivot's events under
// relations.Pivot. The nil-builder guard is the base's: a pivot without a builder has no
// dispatcher.
func (m *MorphPivot) fireEvent(name string) error {
	if m.builder == nil {
		return nil
	}

	return m.builder.FirePivotEvent(name, m)
}

// saveNew is the base Pivot.saveNew flow re-stated so every event it fires dispatches on the
// concrete morph pivot (see fireEvent): the creating event, the timestamps stamp, the plain map
// insert through the table-scoped handle, created / saved, the dirty-gated touchOwners walk and
// syncOriginal — the base's steps and oracle receipts otherwise verbatim.
func (m *MorphPivot) saveNew() error {
	if err := m.fireEvent("creating"); err != nil {
		return err
	}

	if m.UsesTimestamps() {
		m.updateTimestamps()
	}

	// performInsert's empty-attribute early return (`if (empty($attributes)) { return true; }`
	// in Model.php performInsert): a new pivot with no attributes emits no statement and no
	// created event, and `exists` stays false; Model::save still runs finishSave — the saved
	// event and the original sync. Oracle v12.55.1, executed (verifier probe
	// /tmp/morphverify/p8_empty_save.php): events [saving, creating, saved], exists false,
	// no statement.
	if len(m.attributes) == 0 {
		if err := m.fireEvent("saved"); err != nil {
			return err
		}
		m.SyncOriginal()

		return nil
	}

	query, err := m.builder.NewPivotQuery(m.GetTable(), m.connection)
	if err != nil {
		return err
	}
	if err := query.Create(copyPivotAttributes(m.attributes)); err != nil {
		return err
	}
	m.exists = true

	if err := m.fireEvent("created"); err != nil {
		return err
	}
	if err := m.fireEvent("saved"); err != nil {
		return err
	}
	if m.IsDirty() {
		if err := m.builder.PivotTouchOwners(m); err != nil {
			return err
		}
	}
	m.SyncOriginal()

	return nil
}

// Delete mirrors MorphPivot::delete (MorphPivot.php:56-75).
//
// With an own primary key present the call is Model::delete (the PHP branch
// `return (int) parent::delete()`): not-existing short-circuits first, then the deleting event,
// touchOwners, the morph-aware key delete, exists = false and the deleted event — where the
// morph clause the key call adds is written twice, see SetKeysForSaveQuery. Without an own key
// the MorphPivot override runs: the deleting event, NO touchOwners (the quirk — AsPivot's
// keyless delete calls it, this override drops it), the key-pair delete with the morph where
// appended after the group, then exists = false and the deleted event. Like the base, this path
// has NO exists check: a never-existing keyless pivot still runs the statement.
//
// Oracle v12.55.1, executed (SQL probe): `delete from "role_user" where ("foreign" = ? and
// "other" = ?) and "taggable_type" = ?` on the keyless path; `delete from "role_user" where
// "taggable_type" = ? and "taggable_type" = ? and "id" = ?` on the own-key path (affected 1); a
// not-existing own-key pivot emits nothing and reports 0; exists turns false after either
// deletion.
func (m *MorphPivot) Delete() (int64, error) {
	if _, ok := m.getOwnKey(); ok {
		if !m.exists {
			return 0, nil
		}

		if err := m.fireEvent("deleting"); err != nil {
			return 0, err
		}
		if err := m.builder.PivotTouchOwners(m); err != nil {
			return 0, err
		}

		query, err := m.builder.NewPivotQuery(m.GetTable(), m.connection)
		if err != nil {
			return 0, err
		}

		result, err := m.SetKeysForSaveQuery(query).Delete()
		if err != nil {
			return 0, err
		}

		m.exists = false

		if err := m.fireEvent("deleted"); err != nil {
			return 0, err
		}

		return result.RowsAffected, nil
	}

	if err := m.fireEvent("deleting"); err != nil {
		return 0, err
	}

	query, err := m.Pivot.getDeleteQuery()
	if err != nil {
		return 0, err
	}
	query = query.Where(m.morphType, "=", m.morphClassValue())

	result, err := query.Delete()
	if err != nil {
		return 0, err
	}

	m.exists = false

	if err := m.fireEvent("deleted"); err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

// GetQueueableID is MorphPivot::getQueueableId (MorphPivot.php:118): the own key's current value
// when the attributes carry one, else the SIX-segment
// foreignKey:value:relatedKey:value:morphType:morphClass string (nil attribute values format as
// empty segments — PHP's sprintf %s).
//
// Oracle v12.55.1, executed (state probe): {foreign: fv, other: ov} -> "foreign:fv:other:ov:
// taggable_type:App\Models\Post"; an own id 7 -> 7; null values -> "foreign::other::t:C"; a null
// own id falls back to the segmented form -> "foreign:f:other::t:".
func (m *MorphPivot) GetQueueableID() any {
	if value, ok := m.attributes[m.GetKeyName()]; ok && value != nil {
		return value
	}

	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		m.foreignKey, phpAttributeString(m.GetAttribute(m.foreignKey)),
		m.relatedKey, phpAttributeString(m.GetAttribute(m.relatedKey)),
		m.morphType, m.morphClass)
}

// NewQueryForRestoration is MorphPivot::newQueryForRestoration (MorphPivot.php:138): a list of
// ids goes through the collection variant; a string carrying ":" parses as the six-segment
// restoration id; anything else falls back to the base (Model::newQueryForRestoration — whereKey
// over the pivot's own qualified key).
//
// Oracle v12.55.1, executed (state probe): "foreign:f1:other:o1:taggable_type:A" ->
// `select * from "taggables" where "foreign" = ? and "other" = ? and "taggable_type" = ?`
// (bindings ["f1","o1","A"] — the morph where LAST here; the method explodes the id and writes
// the three pair wheres in segment order); the scalar 7 ->
// `select * from "taggables" where "taggables"."id" = ?`.
//
// A segmented id with fewer than six segments reports the named
// errors.OrmMorphPivotRestorationIDInvalid (PHP warns and drops the null segments — the file
// comment quotes the executed evidence); more than six segments use the first six.
func (m *MorphPivot) NewQueryForRestoration(ids any) (contractsorm.Query, error) {
	if list, ok := pivotIDList(ids); ok {
		return m.newQueryForCollectionRestoration(list)
	}

	if text, ok := ids.(string); ok && strings.Contains(text, ":") {
		return m.restoreFromMorphSegments(text)
	}

	return m.Pivot.NewQueryForRestoration(ids)
}

// newQueryForCollectionRestoration is MorphPivot::newQueryForCollectionRestoration
// (MorphPivot.php:162): only the first id decides the shape — a first id that does not carry ":"
// (or an empty list) runs the base Model path, otherwise every id must parse as a six-segment
// restoration id and the three-where groups join with orWhere, the first group included, exactly
// as Laravel's unconditional orWhere renders.
//
// The base call here is deliberately the base restoreByKey, not `parent::newQueryForRestoration`:
// upstream v12.55.1 fatally recurses on that branch (parent::newQueryForRestoration re-dispatches
// back into this override until the stack dies — executed evidence in the file comment), so the
// Go port completes the Model whereKey path the source intends.
func (m *MorphPivot) newQueryForCollectionRestoration(ids []any) (contractsorm.Query, error) {
	if len(ids) == 0 {
		return m.Pivot.restoreByKey(ids)
	}
	if text, ok := ids[0].(string); !ok || !strings.Contains(text, ":") {
		return m.Pivot.restoreByKey(ids)
	}

	query, err := m.builder.NewPivotQuery(m.GetTable(), m.connection)
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		text, ok := id.(string)
		if !ok || !strings.Contains(text, ":") {
			return nil, errors.OrmMorphPivotRestorationIDInvalid.Args(id)
		}

		segments := strings.Split(text, ":")
		if len(segments) < 6 {
			return nil, errors.OrmMorphPivotRestorationIDInvalid.Args(id)
		}

		foreign, foreignValue := segments[0], segments[1]
		related, relatedValue := segments[2], segments[3]
		morphType, morphClass := segments[4], segments[5]
		query = query.OrWhere(func(inner contractsorm.Query) contractsorm.Query {
			return inner.Where(foreign, "=", foreignValue).
				Where(related, "=", relatedValue).
				Where(morphType, "=", morphClass)
		})
	}

	return query, nil
}

// restoreFromMorphSegments parses one
// "foreignKey:value:relatedKey:value:morphType:morphClass" id into its three where pairs —
// MorphPivot.php:149-153's `->where($segments[0], $segments[1])->where($segments[2],
// $segments[3])->where($segments[4], $segments[5])`.
func (m *MorphPivot) restoreFromMorphSegments(id string) (contractsorm.Query, error) {
	segments := strings.Split(id, ":")
	if len(segments) < 6 {
		return nil, errors.OrmMorphPivotRestorationIDInvalid.Args(id)
	}

	query, err := m.builder.NewPivotQuery(m.GetTable(), m.connection)
	if err != nil {
		return nil, err
	}

	return query.Where(segments[0], "=", segments[1]).
		Where(segments[2], "=", segments[3]).
		Where(segments[4], "=", segments[5]), nil
}

// GetTable is the AsPivot getTable read with the morph type name in the fallback: a table-less
// MorphPivot derives "morph_pivot" (Str::snake(Str::singular(class_basename(MorphPivot)))), the
// oracle-pinned result. The promoted base method would name the base Pivot instead, so the read
// is re-stated here; every constructor stamps a table, and base-receiver internals still see the
// base fallback for a table-less instance.
func (m *MorphPivot) GetTable() string {
	if m.table != "" {
		return m.table
	}

	return str.Of(morphPivotGoTypeName(m)).Snake().Singular().String()
}

// PivotKeys satisfies contracts PivotKeyed with the morph column first — AsPivot's pair plus the
// morph type, the order MorphPivot::setKeysForSelectQuery writes the keys (the contracts comment
// and the executed fresh() oracle in the file header).
func (m *MorphPivot) PivotKeys() []string {
	return []string{m.morphType, m.foreignKey, m.relatedKey}
}

// --- internals ----------------------------------------------------------------------------

// morphClassValue is the property read the morph wheres bind: PHP's declared property starts null
// and the Go string's zero value reads as that null (the framework's Where turns a nil value into
// the "is null" clause the class-unset probe shows). An explicitly empty string class is not
// distinguishable from unset in Go.
func (m *MorphPivot) morphClassValue() any {
	if m.morphClass == "" {
		return nil
	}

	return m.morphClass
}

// morphPivotGoTypeName names the morph pivot type the way class_basename does in PHP — the
// type's own name, pointer and embedding layers dropped (the GetTable fallback input).
func morphPivotGoTypeName(m *MorphPivot) string {
	reflected := reflect.TypeOf(m)
	if reflected == nil {
		return "MorphPivot"
	}
	if reflected.Kind() == reflect.Pointer {
		reflected = reflected.Elem()
	}

	return reflected.Name()
}

// Compile assertion: the morph pivot satisfies the PivotKeyed contract Fresh/Refresh match pivot
// rows by.
var _ contractsorm.PivotKeyed = (*MorphPivot)(nil)
