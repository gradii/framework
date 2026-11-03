package relations

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
	"github.com/gradii/framework/support/str"
)

// This file ports Illuminate\Database\Eloquent\Relations\Pivot — vendor, illuminate/database
// v12.55.1; Pivot.php:8 `class Pivot extends Model` — together with the AsPivot trait its whole
// API lives in (Relations/Concerns/AsPivot.php). It is the first user-facing pivot model of this
// ORM: an attribute bag with dirty tracking, a table and a connection, the pivot key pair, the
// pivotParent link the timestamp columns are delegated to, and the save / delete / queueable /
// restoration behaviours of the trait.
//
// Class mapping. PHP's `class Pivot extends Model` is mirrored by this struct carrying the Model
// facets the trait actually uses — the attribute and original bags, exists, the key name, the
// per-instance timestamps flag — instead of a Model base type, and `use AsPivot` by the AsPivot
// methods living on the struct itself. The static constructors become package functions taking
// the execution surface first: FromAttributes / FromRawAttributes mirror `static::fromAttributes`
// / `static::fromRawAttributes` and NewPivot mirrors `new static` (the instance the statics start
// from). Pivot.php's `public $incrementing = false` is reflected by the insert path not reading
// back a generated key (the table-scoped insert carries no key column), and `protected $guarded
// = []` by the attribute bag accepting every column.
//
// The frozen Builder widening (declared on relations.Builder in wrap.go — this is the complete
// shape, no other Builder change):
//
//   - NewPivotQuery(table string, connection string) (contractsorm.Query, error)
//     AsPivot's newQueryWithoutRelationships() / newQueryWithoutScopes() / newModelQuery(): a
//     fresh builder bound to the pivot's own table on the pivot's connection — "" keeps the
//     current connection — and every select / update / delete / insert the pivot runs goes
//     through the handle it returns (AsPivot.php:154 getDeleteQuery, AsPivot.php:120
//     setKeysForSaveQuery, Model.php:1294 performUpdate, Model.php:1378 performInsert,
//     Model.php:1669 newQueryForRestoration).
//   - ReadPivotParent(parent any) (PivotParentInfo, error)
//     The parent reads AsPivot performs through the pivotParent link: the declared connection
//     name stamped at construction (AsPivot.php:56 `setConnection($parent->getConnectionName())`)
//     and the getCreatedAtColumn()/getUpdatedAtColumn() delegation (AsPivot.php:254,266).
//   - PivotTouchOwners(pivot any) error
//     AsPivot.php:140's touchOwners() call (Model::touchOwners) on the no-key delete path.
//   - FirePivotEvent(name string, pivot any) error
//     fireModelEvent over the pivot's event family: deleting/deleted (AsPivot.php:136,145) and
//     saving/updating/updated/saved (Model::save / performUpdate). A handler registered for the
//     pivot's type receives the event; with none registered the call is Laravel's no-op
//     fireModelEvent without a dispatcher. The PHP listener's `return false` cancel has no Go
//     form — a handler reports failure by returning an error, which aborts the operation.
//
// Divergences from the PHP class, all deliberate (this ORM has no cast/mutator pipeline and no
// in-memory relation registry):
//
//   - Attribute values are stored exactly as written: fromAttributes does not date-normalize.
//     Oracle v12.55.1, executed (/tmp/pivotprobes/state_probe.php): fromAttributes with
//     created_at "2015-09-12" reports a Carbon dumped as "2015-09-12 00:00:00"; the Go port
//     keeps the string the caller passed.
//
//   - WithoutRelations / UnsetRelations clear the pivotParent / pivotRelated links only; there
//     is no relations registry to clone or clear.
//
//   - GetTable()'s no-table fallback derives from the Go type name — the Go stand-in for
//     class_basename(Pivot) — through Str::snake(Str::singular(...)): oracle, executed,
//     `(new Pivot)->getTable()` reports "pivot".
//
//   - UsesTimestamps() reports the per-instance flag only (no withoutTimestampsOn registry).
//
//   - ReadPivotParent resolves the parent's timestamp columns from the parent's declared Go
//     schema (the automatic field's column, else the conventional created_at / updated_at
//     column, else ""); Laravel returns a class constant, and "" is the Go reading of PHP's null.
//
//   - The own-key delete path reports the statement's affected rows; Laravel reports (int) true.
//
//   - Model::save's insert branch adopts the query connection's name when the model declares
//     none (Model.php:1242-1245); the Go save keeps the constructor-stamped "" because the
//     frozen widening exposes no connection-name read. Oracle v12.55.1, executed (verifier
//     probe /tmp/vpivot/sql_probe.php I1): the keyless insert reports connectionAfter
//     'default'; the Go path (verifier overlay probe) reports "" — later statements still
//     run on the same connection, so only the reported name differs.
//
// Oracle probes executed against illuminate/database v12.55.1 (both print
// "illuminate/database: v12.55.1" on stderr):
//
//	php .claude/laravel-oracle/oracle.php /tmp/pivotprobes/state_probe.php
//	php .claude/laravel-oracle/oracle.php /tmp/pivotprobes/sql_probe.php
//
// State probe (key outputs the comments below cite): fromAttributes -> attributes {foo: bar,
// created_at: Carbon "2015-09-12 00:00:00"}, connection "connection", table "table", exists
// true, pivotParent identical, timestamps true, dirty []; no created_at -> timestamps false;
// fromRawAttributes (exists false) -> timestamps true with parent column created_at, dirty
// {foo, created_at}; raw + exists true -> dirty []; dirtied attr -> dirty {shimy: changed};
// int 1 vs string "1" -> clean; setPivotKeys("foreign","other") -> foreign/other/other;
// custom-columns parent -> parent_created_at/parent_updated_at, bare pivot ->
// created_at/updated_at, table "pivot"; queueable ids "foreign:foreign.value:other:other.value",
// 7, "foreign::other:", "foreign:f:other:"; restoration SQLs `select * from "table" where
// "foreign" = ? and "other" = ?`, `select * from "table" where ("foreign" = ? and "other" = ?)
// or ("foreign" = ? and "other" = ?)`, `select * from "table" where "table"."id" = ?`,
// `select * from "table" where "table"."id" in (7, 8)`; keyName "id".
//
// SQL probe (executed against in-memory sqlite; the comments on Save / Delete cite these):
// save no key -> `update "role_user" set "note" = ? where "foreign" = ? and "other" = ?`;
// timestamps flag on -> `update "role_user" set "note" = ?, "updated_at" = ? where "foreign" =
// ? and "other" = ?`; own key -> `update "role_user" set "note" = ? where "id" = ?`; key
// changed after construction -> `update "role_user" set "id" = ?, "note" = ? where "id" = ?`;
// delete no key -> `delete from "role_user" where ("foreign" = ? and "other" = ?)`; delete with
// key -> `delete from "role_user" where "id" = ?`; a not-existing pivot with an own key emits
// no statement; save of a new pivot -> `insert into "role_user" ("foreign", "other",
// "created_at", "updated_at") values (?, ?, ?, ?)` re-stamping both timestamps.
type Pivot struct {
	builder Builder

	attributes map[string]any
	original   map[string]any

	table      string
	connection string
	exists     bool
	// timestamps is AsPivot's per-instance flag: Eloquent's public $timestamps after the
	// constructors set it from hasTimestampAttributes($attributes).
	timestamps bool

	// pivotParent / pivotRelated mirror AsPivot's public properties: the parent model the
	// timestamp columns are delegated to, and the related model SetRelatedModel records.
	pivotParent  any
	pivotRelated any

	foreignKey string
	relatedKey string
	keyName    string
}

// PivotParentInfo is the parent-model metadata AsPivot reads through its pivotParent link: the
// declared connection name and the created-at / updated-at column names. ReadPivotParent (the
// frozen widening) projects it; "" for a column is the Go reading of PHP's null (AsPivot.php:245
// `($createdAt = $this->getCreatedAtColumn()) !== null` gates the timestamps flag).
type PivotParentInfo struct {
	Connection      string
	CreatedAtColumn string
	UpdatedAtColumn string
}

// Pivot's own column defaults — Eloquent's Model constants CREATED_AT/UPDATED_AT and the
// $primaryKey default the AsPivot methods read through getKeyName().
const (
	defaultPivotKeyName   = "id"
	defaultPivotCreatedAt = "created_at"
	defaultPivotUpdatedAt = "updated_at"
)

// NewPivot builds an empty pivot bound to builder — the Go form of `new static` inside
// AsPivot::fromAttributes: no attributes, no original bag, no table, no parent, not existing.
func NewPivot(builder Builder) *Pivot {
	return &Pivot{
		builder:    builder,
		attributes: map[string]any{},
		original:   map[string]any{},
		keyName:    defaultPivotKeyName,
	}
}

// FromAttributes mirrors AsPivot::fromAttributes($parent, $attributes, $table, $exists = false)
// (AsPivot.php:47): the per-instance timestamps flag is set from hasTimestampAttributes BEFORE
// the parent link exists — so it weighs the pivot's own created-at column name, which is what
// Laravel's parent::getCreatedAtColumn() returns at that moment — then the connection name and
// table are stamped, the attributes fill the bag, the original bag is synced, and the parent
// link plus the exists flag are recorded.
//
// Oracle v12.55.1, executed (state probe): fromAttributes(..., ["foo" => "bar", "created_at" =>
// "2015-09-12"], "table", true) reports connection "connection", table "table", exists true,
// pivotParent identical, timestamps true, dirty []; without created_at timestamps is false; with
// a custom-columns parent the flag still comes from the default "created_at" name (observed
// true), while getCreatedAtColumn() afterwards reports the parent's column.
func FromAttributes(builder Builder, parent any, attributes map[string]any, table string, exists bool) (*Pivot, error) {
	pivot := NewPivot(builder)
	pivot.timestamps = pivot.HasTimestampAttributes(attributes)

	if builder != nil {
		info, err := builder.ReadPivotParent(parent)
		if err != nil {
			return nil, err
		}
		pivot.connection = info.Connection
	}

	pivot.table = table
	pivot.attributes = copyPivotAttributes(attributes)
	pivot.syncOriginal()
	pivot.pivotParent = parent
	pivot.exists = exists

	return pivot, nil
}

// FromRawAttributes mirrors AsPivot::fromRawAttributes($parent, $attributes, $table, $exists =
// false) (AsPivot.php:80): a fromAttributes instance over an empty bag — which records the
// parent link first — then the timestamps flag is re-evaluated from the raw attribute names
// against the parent-delegated created-at column, and the raw attributes merge onto the raw
// original bag (empty at this point). The merge replaces setRawAttributes($attributes, $exists):
// with $exists true the bag syncs into the original bag, so the instance is NOT dirty; with
// false the original bag stays empty, so every attribute reads dirty.
//
// Oracle v12.55.1, executed (state probe): raw + exists false reports timestamps true (with the
// parent's created_at name present) and dirty {foo, created_at}; raw + exists true reports dirty
// []; a custom-columns parent makes the flag follow parent_created_at.
func FromRawAttributes(builder Builder, parent any, attributes map[string]any, table string, exists bool) (*Pivot, error) {
	pivot, err := FromAttributes(builder, parent, map[string]any{}, table, exists)
	if err != nil {
		return nil, err
	}

	pivot.timestamps = pivot.HasTimestampAttributes(attributes)

	merged := copyPivotAttributes(pivot.original)
	for column, value := range attributes {
		merged[column] = value
	}
	pivot.setRawAttributes(merged, exists)

	return pivot, nil
}

// HasTimestampAttributes is AsPivot::hasTimestampAttributes (AsPivot.php:243): the created-at
// column name is non-null and the given attribute bag — the pivot's own bag when the argument is
// nil — carries it. The custom columns the parent delegates are honoured because
// GetCreatedAtColumn() reads through the parent link.
func (p *Pivot) HasTimestampAttributes(attributes ...map[string]any) bool {
	column := p.GetCreatedAtColumn()
	if column == "" {
		return false
	}

	bag := p.attributes
	if len(attributes) > 0 && attributes[0] != nil {
		bag = attributes[0]
	}

	_, ok := bag[column]

	return ok
}

// SetKeysForSelectQuery is AsPivot::setKeysForSelectQuery (AsPivot.php:99): an own primary key
// present in the attributes constrains the query to it — Model::setKeysForSelectQuery, where
// the key value is the original bag's value falling back to the current one — and otherwise the
// foreign key / related key pair constrains it, each read from the original bag with the current
// attribute as the fallback (getOriginal($key, $this->getAttribute($key)): a null original is
// used as-is).
func (p *Pivot) SetKeysForSelectQuery(query contractsorm.Query) contractsorm.Query {
	if _, ok := p.getOwnKey(); ok {
		return query.Where(p.GetKeyName(), "=", p.keyForSaveQuery())
	}

	return query.Where(p.foreignKey, "=", p.asPivotKeyValue(p.foreignKey)).
		Where(p.relatedKey, "=", p.asPivotKeyValue(p.relatedKey))
}

// SetKeysForSaveQuery is AsPivot::setKeysForSaveQuery (AsPivot.php:120), the select variant.
func (p *Pivot) SetKeysForSaveQuery(query contractsorm.Query) contractsorm.Query {
	return p.SetKeysForSelectQuery(query)
}

// Save mirrors AsPivot's Model::save (Model.php:1215), including performUpdate (Model.php:1294)
// and the insert branch (Model.php:1378):
//
//   - the saving event fires first;
//   - an existing pivot: the updating event, then — when the per-instance timestamps flag is on
//     — the update stamp (the updated-at column is set to now unless already dirty; the
//     created-at column is not re-stamped because the pivot exists). When anything is dirty the
//     update runs through setKeysForSaveQuery on the pivot's own table and the updated event
//     fires; then the saved event, a touchOwners call when something was dirty (finishSave) and
//     a syncOriginal;
//   - a new pivot: the creating event, the timestamps stamp (now BOTH created-at, unless
//     already dirty, and updated-at — the oracle re-stamps a provided created_at), a plain
//     insert of the attribute bag through the table-scoped handle (Pivot::$incrementing is
//     false, so no key is read back), then created / saved, touchOwners and syncOriginal.
//
// Oracle v12.55.1, executed (SQL probe): existing, no key, one dirty column ->
// `update "role_user" set "note" = ? where "foreign" = ? and "other" = ?`; timestamps flag on ->
// `update "role_user" set "note" = ?, "updated_at" = ? where "foreign" = ? and "other" = ?`;
// own key -> `update "role_user" set "note" = ? where "id" = ?`; the key attribute changed after
// construction -> `update "role_user" set "id" = ?, "note" = ? where "id" = ?` (the where keeps
// the original value); a new pivot -> `insert into "role_user" ("foreign", "other",
// "created_at", "updated_at") values (?, ?, ?, ?)`.
func (p *Pivot) Save() error {
	if err := p.fireEvent("saving"); err != nil {
		return err
	}

	if p.exists {
		return p.saveExisting()
	}

	return p.saveNew()
}

// saveExisting is Model::save's update branch through performUpdate.
func (p *Pivot) saveExisting() error {
	// Model::save only runs performUpdate when the model is dirty (`$this->isDirty() ?
	// $this->performUpdate($query) : true` - Model.php save()), so a clean pivot skips the
	// updating event and the timestamps stamp entirely; finishSave still fires the saved event
	// and syncs the original bag. Oracle v12.55.1, executed (verifier probe
	// /tmp/morphverify/p1_saveflow.php, cases A/B/E): events [saving, saved], no statement.
	if !p.IsDirty() {
		if err := p.fireEvent("saved"); err != nil {
			return err
		}
		p.SyncOriginal()

		return nil
	}

	if err := p.fireEvent("updating"); err != nil {
		return err
	}

	if p.UsesTimestamps() {
		p.updateTimestamps()
	}

	dirty := p.GetDirty()
	if len(dirty) > 0 {
		query, err := p.builder.NewPivotQuery(p.GetTable(), p.connection)
		if err != nil {
			return err
		}

		if _, err := p.SetKeysForSaveQuery(query).Update(dirty); err != nil {
			return err
		}

		if err := p.fireEvent("updated"); err != nil {
			return err
		}
	}

	if err := p.fireEvent("saved"); err != nil {
		return err
	}
	if len(dirty) > 0 {
		if err := p.builder.PivotTouchOwners(p); err != nil {
			return err
		}
	}
	p.SyncOriginal()

	return nil
}

// saveNew is Model::save's insert branch through performInsert. The insert itself rides the
// table-scoped handle's Create with the attribute map — the framework's map-shaped insert — so
// no extra widening is needed for it. The map is copied: the ORM may write a generated key back
// into the record, and Pivot::$incrementing false means the pivot's own bag must not learn one.
func (p *Pivot) saveNew() error {
	if err := p.fireEvent("creating"); err != nil {
		return err
	}

	if p.UsesTimestamps() {
		p.updateTimestamps()
	}

	// performInsert's empty-attribute early return (`if (empty($attributes)) { return true; }`
	// in Model.php performInsert): a new pivot with no attributes emits no statement and no
	// created event, and `exists` stays false; Model::save still runs finishSave — the saved
	// event and the original sync. Oracle v12.55.1, executed (verifier probe
	// /tmp/morphverify/p8_empty_save.php): events [saving, creating, saved], exists false,
	// no statement.
	if len(p.attributes) == 0 {
		if err := p.fireEvent("saved"); err != nil {
			return err
		}
		p.SyncOriginal()

		return nil
	}

	query, err := p.builder.NewPivotQuery(p.GetTable(), p.connection)
	if err != nil {
		return err
	}
	if err := query.Create(copyPivotAttributes(p.attributes)); err != nil {
		return err
	}
	p.exists = true

	if err := p.fireEvent("created"); err != nil {
		return err
	}
	if err := p.fireEvent("saved"); err != nil {
		return err
	}
	if p.IsDirty() {
		if err := p.builder.PivotTouchOwners(p); err != nil {
			return err
		}
	}
	p.SyncOriginal()

	return nil
}

// Delete mirrors AsPivot::delete (AsPivot.php:130).
//
// With an own primary key present the call is Model::delete (Model.php:1486): a pivot that does
// not exist is a no-op before any event; otherwise the deleting event fires, touchOwners runs,
// the row is deleted through setKeysForSaveQuery (Model::performDeleteOnModel), exists turns
// false and the deleted event fires. Without an own key the AsPivot override runs: the deleting
// event, touchOwners, then the key-pair delete — where(["foreign" => ..., "other" => ...]), the
// nested-parens form — followed by exists = false and the deleted event. This path has NO exists
// check: a never-existing pivot still runs the statement (both oracle-pinned below). Note the
// key-pair read: the foreign/related values come from the original bag with the current
// attribute as the fallback.
//
// Oracle v12.55.1, executed (SQL probe): `delete from "role_user" where ("foreign" = ? and
// "other" = ?)` on the key-pair path; `delete from "role_user" where "id" = ?` on the own-key
// path; a not-existing pivot with an own key emits no statement and reports 0; exists turns
// false after either deletion.
func (p *Pivot) Delete() (int64, error) {
	if _, ok := p.getOwnKey(); ok {
		if !p.exists {
			return 0, nil
		}

		if err := p.fireEvent("deleting"); err != nil {
			return 0, err
		}
		if err := p.builder.PivotTouchOwners(p); err != nil {
			return 0, err
		}

		query, err := p.builder.NewPivotQuery(p.GetTable(), p.connection)
		if err != nil {
			return 0, err
		}

		result, err := p.SetKeysForSaveQuery(query).Delete()
		if err != nil {
			return 0, err
		}

		p.exists = false

		if err := p.fireEvent("deleted"); err != nil {
			return 0, err
		}

		return result.RowsAffected, nil
	}

	if err := p.fireEvent("deleting"); err != nil {
		return 0, err
	}
	if err := p.builder.PivotTouchOwners(p); err != nil {
		return 0, err
	}

	query, err := p.getDeleteQuery()
	if err != nil {
		return 0, err
	}

	result, err := query.Delete()
	if err != nil {
		return 0, err
	}

	p.exists = false

	if err := p.fireEvent("deleted"); err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

// getDeleteQuery is AsPivot::getDeleteQuery (AsPivot.php:154): a fresh table-scoped query
// constrained by the key pair. The where uses the ordered-pair array form so the compiled clause
// is the nested group Laravel's where([...]) map produces — oracle: `where ("foreign" = ? and
// "other" = ?)` — with the foreign-key pair first, deterministically.
func (p *Pivot) getDeleteQuery() (contractsorm.Query, error) {
	query, err := p.builder.NewPivotQuery(p.GetTable(), p.connection)
	if err != nil {
		return nil, err
	}

	pairs := []any{
		[]any{p.foreignKey, p.asPivotKeyValue(p.foreignKey)},
		[]any{p.relatedKey, p.asPivotKeyValue(p.relatedKey)},
	}

	return query.Where(pairs), nil
}

// GetTable is AsPivot::getTable (AsPivot.php:167): the table stamped at construction, or — when
// none was — the name derived from the type the way Laravel derives it from the class basename
// (Str::snake(Str::singular(class_basename(...)))). Oracle v12.55.1, executed: `(new
// Pivot)->getTable()` reports "pivot"; a MorphPivot derives "morph_pivot".
func (p *Pivot) GetTable() string {
	if p.table != "" {
		return p.table
	}

	return str.Of(pivotTypeName(p)).Snake().Singular().String()
}

// SetTable stamps the table name, mirroring Model::setTable; the fluent return mirrors the
// constructor chain AsPivot::fromAttributes builds.
func (p *Pivot) SetTable(table string) *Pivot {
	p.table = table

	return p
}

// GetForeignKey is AsPivot::getForeignKey (AsPivot.php:183).
func (p *Pivot) GetForeignKey() string { return p.foreignKey }

// GetRelatedKey is AsPivot::getRelatedKey (AsPivot.php:193).
func (p *Pivot) GetRelatedKey() string { return p.relatedKey }

// GetOtherKey is AsPivot::getOtherKey (AsPivot.php:203), the related key's alias.
func (p *Pivot) GetOtherKey() string { return p.GetRelatedKey() }

// SetPivotKeys is AsPivot::setPivotKeys (AsPivot.php:215). Oracle v12.55.1, executed:
// setPivotKeys("foreign", "other") reports getForeignKey() "foreign" and getOtherKey() "other".
func (p *Pivot) SetPivotKeys(foreignKey, relatedKey string) *Pivot {
	p.foreignKey = foreignKey
	p.relatedKey = relatedKey

	return p
}

// SetRelatedModel is AsPivot::setRelatedModel (AsPivot.php:230).
func (p *Pivot) SetRelatedModel(related any) *Pivot {
	p.pivotRelated = related

	return p
}

// GetCreatedAtColumn is AsPivot::getCreatedAtColumn (AsPivot.php:254): the parent's column when
// the parent link is set, else the pivot's own default. The parent's column resolves through the
// frozen ReadPivotParent widening; a read failure reports "" the way an undeclared column does.
//
// Oracle v12.55.1, executed: a custom-columns parent reports "parent_created_at"; no parent
// reports "created_at".
func (p *Pivot) GetCreatedAtColumn() string {
	if p.pivotParent == nil {
		return defaultPivotCreatedAt
	}
	if p.builder == nil {
		return ""
	}

	info, err := p.builder.ReadPivotParent(p.pivotParent)
	if err != nil {
		return ""
	}

	return info.CreatedAtColumn
}

// GetUpdatedAtColumn is AsPivot::getUpdatedAtColumn (AsPivot.php:266). Oracle v12.55.1,
// executed: a custom-columns parent reports "parent_updated_at"; no parent reports "updated_at".
func (p *Pivot) GetUpdatedAtColumn() string {
	if p.pivotParent == nil {
		return defaultPivotUpdatedAt
	}
	if p.builder == nil {
		return ""
	}

	info, err := p.builder.ReadPivotParent(p.pivotParent)
	if err != nil {
		return ""
	}

	return info.UpdatedAtColumn
}

// GetQueueableID is AsPivot::getQueueableId (AsPivot.php:278): the own key's current value when
// the attributes carry one, else the four-segment foreignKey:value:relatedKey:value string (a
// nil value formats as an empty segment — PHP's sprintf %s).
//
// Oracle v12.55.1, executed: {foreign, other} -> "foreign:foreign.value:other:other.value"; an
// own id 7 -> 7 (the key value itself); null values -> "foreign::other:"; a null id ->
// "foreign:f:other:".
func (p *Pivot) GetQueueableID() any {
	if value, ok := p.attributes[p.GetKeyName()]; ok && value != nil {
		return value
	}

	return fmt.Sprintf("%s:%s:%s:%s",
		p.foreignKey, phpAttributeString(p.GetAttribute(p.foreignKey)),
		p.relatedKey, phpAttributeString(p.GetAttribute(p.relatedKey)))
}

// NewQueryForRestoration is AsPivot::newQueryForRestoration (AsPivot.php:297): a list of ids
// goes through the collection variant; a string carrying ":" parses as the four-segment
// restoration id; anything else falls back to Model::newQueryForRestoration — whereKey over the
// pivot's own qualified key (Model::whereKey: an inline list constrains with whereIn).
//
// The query is the pivot's table-scoped handle, the Go stand-in for newQueryWithoutScopes. A
// segmented id with fewer than four segments reports the named
// errors.OrmPivotRestorationIDInvalid — PHP's undefined-offset error on the same input.
//
// Oracle v12.55.1, executed (state probe): "foreign:f1:other:o1" ->
// `select * from "table" where "foreign" = ? and "other" = ?`; a list of segmented ids ->
// `select * from "table" where ("foreign" = ? and "other" = ?) or ("foreign" = ? and "other" =
// ?)`; the scalar 7 -> `select * from "table" where "table"."id" = ?`; the list [7, 8] ->
// `select * from "table" where "table"."id" in (7, 8)` (Laravel inlines integer key lists; the
// Go port binds them through WhereIn — the framework-wide key-list rendering).
func (p *Pivot) NewQueryForRestoration(ids any) (contractsorm.Query, error) {
	if list, ok := pivotIDList(ids); ok {
		return p.newQueryForCollectionRestoration(list)
	}

	if text, ok := ids.(string); ok && strings.Contains(text, ":") {
		return p.restoreFromSegments(text)
	}

	return p.restoreByKey(ids)
}

// newQueryForCollectionRestoration is AsPivot::newQueryForCollectionRestoration
// (AsPivot.php:320): only the first id decides the shape — a first id that does not carry ":"
// sends the whole list through Model::newQueryForRestoration (whereKey; an empty list does the
// same, the Go reading of PHP's undefined index), otherwise every id must parse as a segmented
// restoration id and the groups join with orWhere, the first group included, exactly as
// Laravel's unconditional orWhere renders.
func (p *Pivot) newQueryForCollectionRestoration(ids []any) (contractsorm.Query, error) {
	if len(ids) == 0 {
		return p.restoreByKey(ids)
	}
	if text, ok := ids[0].(string); !ok || !strings.Contains(text, ":") {
		return p.restoreByKey(ids)
	}

	query, err := p.builder.NewPivotQuery(p.GetTable(), p.connection)
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		text, ok := id.(string)
		if !ok || !strings.Contains(text, ":") {
			return nil, errors.OrmPivotRestorationIDInvalid.Args(id)
		}

		segments := strings.Split(text, ":")
		if len(segments) < 4 {
			return nil, errors.OrmPivotRestorationIDInvalid.Args(id)
		}

		first, second, third, fourth := segments[0], segments[1], segments[2], segments[3]
		query = query.OrWhere(func(inner contractsorm.Query) contractsorm.Query {
			return inner.Where(first, "=", second).Where(third, "=", fourth)
		})
	}

	return query, nil
}

// restoreFromSegments parses one "foreignKey:value:relatedKey:value" id into its where pair —
// AsPivot.php:310-311's `->where($segments[0], $segments[1])->where($segments[2], $segments[3])`.
func (p *Pivot) restoreFromSegments(id string) (contractsorm.Query, error) {
	segments := strings.Split(id, ":")
	if len(segments) < 4 {
		return nil, errors.OrmPivotRestorationIDInvalid.Args(id)
	}

	query, err := p.builder.NewPivotQuery(p.GetTable(), p.connection)
	if err != nil {
		return nil, err
	}

	return query.Where(segments[0], "=", segments[1]).Where(segments[2], "=", segments[3]), nil
}

// restoreByKey is Model::newQueryForRestoration (Model.php:1669) over the pivot's own qualified
// key: a scalar key is an equality, a list a whereIn.
func (p *Pivot) restoreByKey(ids any) (contractsorm.Query, error) {
	query, err := p.builder.NewPivotQuery(p.GetTable(), p.connection)
	if err != nil {
		return nil, err
	}

	column := p.GetTable() + "." + p.GetKeyName()
	if list, ok := pivotIDList(ids); ok {
		return query.WhereIn(column, list), nil
	}

	return query.Where(column, "=", ids), nil
}

// UnsetRelations is AsPivot::unsetRelations (AsPivot.php:347): the parent and related links
// clear and the instance returns. The registry clear Laravel performs has no Go counterpart —
// there is no in-memory relation registry on a pivot.
func (p *Pivot) UnsetRelations() *Pivot {
	p.pivotParent = nil
	p.pivotRelated = nil

	return p
}

// WithoutRelations returns a copy with the pivotParent / pivotRelated links cleared — AsPivot's
// inherited Model::withoutRelations for the facets a pivot carries. Oracle v12.55.1, executed:
// the copy's parent is null, the original's kept; the copy is a different instance.
func (p *Pivot) WithoutRelations() *Pivot {
	copied := *p
	copied.attributes = copyPivotAttributes(p.attributes)
	copied.original = copyPivotAttributes(p.original)
	copied.pivotParent = nil
	copied.pivotRelated = nil

	return &copied
}

// --- Model facets the trait and this package's callers read -------------------------------

// Attributes returns a copy of the attribute bag — Model::getAttributes' by-value semantics.
func (p *Pivot) Attributes() map[string]any {
	return copyPivotAttributes(p.attributes)
}

// GetAttribute is Model::getAttribute's raw read for the shapes a pivot carries.
func (p *Pivot) GetAttribute(key string) any { return p.attributes[key] }

// SetAttribute writes one attribute; the cast and mutator pipeline Model::setAttribute runs does
// not exist in Go, so the value is stored exactly as given.
func (p *Pivot) SetAttribute(key string, value any) *Pivot {
	p.attributes[key] = value

	return p
}

// GetOriginal is Model::getOriginal over the raw original bag: the stored value when the key is
// present (a stored nil included), else the default.
func (p *Pivot) GetOriginal(key string, def ...any) any {
	if value, ok := p.original[key]; ok {
		return value
	}
	if len(def) > 0 {
		return def[0]
	}

	return nil
}

// SyncOriginal is Model::syncOriginal: the original bag becomes a copy of the attribute bag, so
// nothing reads dirty.
func (p *Pivot) SyncOriginal() *Pivot {
	p.syncOriginal()

	return p
}

func (p *Pivot) syncOriginal() {
	p.original = copyPivotAttributes(p.attributes)
}

// GetDirty is Model::getDirty (HasAttributes.php:2277): every attribute whose current value is
// not equivalent to the original — a key the original bag does not carry is dirty. Oracle
// v12.55.1, executed: a freshly built pivot reads []; one assignment reads exactly that key;
// 1 against "1" stays clean (the numeric strcmp branch of originalIsEquivalent).
func (p *Pivot) GetDirty() map[string]any {
	dirty := make(map[string]any)
	for key, value := range p.attributes {
		if !p.originalIsEquivalent(key, value) {
			dirty[key] = value
		}
	}

	return dirty
}

// IsDirty reports whether any — or any of the named — attributes are dirty.
func (p *Pivot) IsDirty(columns ...string) bool {
	dirty := p.GetDirty()
	if len(columns) == 0 {
		return len(dirty) > 0
	}

	for _, column := range columns {
		if _, ok := dirty[column]; ok {
			return true
		}
	}

	return false
}

// GetKey is Model::getKey: the current value of the pivot's key attribute.
func (p *Pivot) GetKey() any { return p.GetAttribute(p.GetKeyName()) }

// GetKeyName is Model::getKeyName; the default is "id" (Model::$primaryKey).
func (p *Pivot) GetKeyName() string { return p.keyName }

// Exists reports the instance flag the constructors set and Save / Delete maintain — Eloquent's
// public $exists.
func (p *Pivot) Exists() bool { return p.exists }

// SetExists stamps the exists flag; Save / Delete maintain it themselves, this is the testing
// and wiring handle for the flag Eloquent exposes as a property.
func (p *Pivot) SetExists(exists bool) *Pivot {
	p.exists = exists

	return p
}

// UsesTimestamps is Model::usesTimestamps over the per-instance flag the constructors set —
// the Go reading of AsPivot's public $timestamps. The class-level withoutTimestampsOn registry
// has no Go counterpart.
func (p *Pivot) UsesTimestamps() bool { return p.timestamps }

// GetConnectionName is Model::getConnectionName: the connection name stamped at construction
// ("" declares none).
func (p *Pivot) GetConnectionName() string { return p.connection }

// SetConnection is Model::setConnection; the fluent return mirrors the constructor chain.
func (p *Pivot) SetConnection(name string) *Pivot {
	p.connection = name

	return p
}

// GetPivotParent reports the parent link — AsPivot's public $pivotParent.
func (p *Pivot) GetPivotParent() any { return p.pivotParent }

// SetPivotParent stamps the parent link (the constructors set it; Eloquent exposes it as a
// public property).
func (p *Pivot) SetPivotParent(parent any) *Pivot {
	p.pivotParent = parent

	return p
}

// GetPivotRelated reports the related-model link — AsPivot's public $pivotRelated.
func (p *Pivot) GetPivotRelated() any { return p.pivotRelated }

// PivotKeys satisfies contracts/database/orm.PivotKeyed: the (foreignKey, relatedKey) pair, in
// the order SetKeysForSelectQuery writes the clauses — the columns Fresh/Refresh match a
// keyless pivot row by.
func (p *Pivot) PivotKeys() []string {
	return []string{p.foreignKey, p.relatedKey}
}

// --- internals ----------------------------------------------------------------------------

// fireEvent dispatches one pivot event through the frozen FirePivotEvent widening; a pivot
// without a builder has no dispatcher and the event is a no-op.
func (p *Pivot) fireEvent(name string) error {
	if p.builder == nil {
		return nil
	}

	return p.builder.FirePivotEvent(name, p)
}

// updateTimestamps is HasTimestamps::updateTimestamps (Concerns/HasTimestamps.php:63) with the
// pivot's dynamic columns: the updated-at column takes now unless already dirty, and a
// not-existing pivot's created-at column likewise. Oracle v12.55.1, executed (SQL probe): the
// update path stamps only updated_at; the insert path re-stamps a provided created_at and stamps
// updated_at.
func (p *Pivot) updateTimestamps() {
	now := carbon.Now().StdTime()

	if column := p.GetUpdatedAtColumn(); column != "" && !p.IsDirty(column) {
		p.attributes[column] = now
	}

	if !p.exists {
		if column := p.GetCreatedAtColumn(); column != "" && !p.IsDirty(column) {
			p.attributes[column] = now
		}
	}
}

// getOwnKey reports the own key value the way isset($this->attributes[$this->getKeyName()])
// reads it: present and non-null.
func (p *Pivot) getOwnKey() (any, bool) {
	value, ok := p.attributes[p.GetKeyName()]

	return value, ok && value != nil
}

// keyForSaveQuery is Model::getKeyForSaveQuery ($this->original[$keyName] ?? $this->getKey():
// a null original value falls through to the current key).
func (p *Pivot) keyForSaveQuery() any {
	if value, ok := p.original[p.GetKeyName()]; ok && value != nil {
		return value
	}

	return p.GetKey()
}

// asPivotKeyValue is AsPivot's getOriginal($key, $this->getAttribute($key)): a key present in
// the original bag is used as stored — a stored nil included — and a missing one falls back to
// the current attribute.
func (p *Pivot) asPivotKeyValue(column string) any {
	if value, ok := p.original[column]; ok {
		return value
	}

	return p.GetAttribute(column)
}

// setRawAttributes is HasAttributes::setRawAttributes (HasAttributes.php:2039) over the bags a
// pivot carries: the attributes replace, and $sync syncs the original bag.
func (p *Pivot) setRawAttributes(attributes map[string]any, sync bool) {
	p.attributes = attributes
	if sync {
		p.syncOriginal()
	}
}

// originalIsEquivalent is HasAttributes::originalIsEquivalent (HasAttributes.php:2326) for the
// shapes a pivot carries over Go: a key the original bag does not carry is never equivalent;
// strict identity wins; a null attribute is never equivalent; two numbers compare through their
// string forms (PHP's strcmp((string) $a, (string) $b) — the branch that keeps 1 and "1" clean).
// The date/cast branches have no Go counterpart because no cast pipeline exists.
func (p *Pivot) originalIsEquivalent(key string, attribute any) bool {
	original, ok := p.original[key]
	if !ok {
		return false
	}

	if attributeIdentical(attribute, original) {
		return true
	}
	if attribute == nil {
		return false
	}
	if isNumericAttribute(attribute) && isNumericAttribute(original) {
		return fmt.Sprint(attribute) == fmt.Sprint(original)
	}

	return false
}

// attributeIdentical is PHP's === for the scalar shapes attribute values carry: same dynamic
// type and equal value; two nils are identical. Structured values report false (no pivot
// attribute path compares arrays by value today).
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
		// PHP's === is object identity, and Eloquent's syncOriginal keeps the SAME instance on
		// both sides, so two stamps of one time read clean. Go copies values, so the reading is
		// same type and equal value: a comparable type (time.Time among them) compares with ==.
		if leftValue.Type().Comparable() {
			return leftValue.Interface() == rightValue.Interface()
		}
	}

	return false
}

// isNumericAttribute is the numeric half of PHP's is_numeric over the shapes attribute values
// carry: number kinds, and strings that parse as numbers.
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

// phpAttributeString renders an attribute the way PHP's sprintf %s does: null formats as the
// empty string and a bool as "1" / "" (PHP's %s cast; oracle-executed, verifier probe
// /tmp/vpivot/state_probe.php F7: queueable "foreign:1:other:").
func phpAttributeString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	if flag, ok := value.(bool); ok {
		if flag {
			return "1"
		}

		return ""
	}

	return fmt.Sprint(value)
}

// copyPivotAttributes copies an attribute bag — PHP arrays pass by value, so every constructor
// and accessor hands out copies rather than sharing the live maps.
func copyPivotAttributes(attributes map[string]any) map[string]any {
	copied := make(map[string]any, len(attributes))
	for column, value := range attributes {
		copied[column] = value
	}

	return copied
}

// pivotIDList normalises a restoration id argument into the list shape: any slice or array
// becomes []any; everything else reports false (the scalar branch).
func pivotIDList(ids any) ([]any, bool) {
	if ids == nil {
		return nil, false
	}

	reflected := reflect.ValueOf(ids)
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		list := make([]any, 0, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			list = append(list, reflected.Index(index).Interface())
		}

		return list, true
	}

	return nil, false
}

// pivotTypeName names the pivot type the way class_basename does in PHP — the type's own name,
// pointer and embedding layers dropped (the Go stand-in AsPivot::getTable's fallback uses).
func pivotTypeName(p *Pivot) string {
	reflected := reflect.TypeOf(p)
	if reflected == nil {
		return "Pivot"
	}
	if reflected.Kind() == reflect.Pointer {
		reflected = reflected.Elem()
	}

	return reflected.Name()
}

// Compile assertions: the pivot satisfies the PivotKeyed contract Fresh/Refresh match keyless
// pivot rows by, and both pivot types satisfy the PivotModel surface the many-to-many relation's
// NewPivot / NewExistingPivot methods hand back.
var (
	_ contractsorm.PivotKeyed = (*Pivot)(nil)
	_ contractsorm.PivotModel = (*Pivot)(nil)
	_ contractsorm.PivotModel = (*MorphPivot)(nil)
)
