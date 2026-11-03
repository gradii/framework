package orm

import (
	"github.com/gradii/framework/contracts/database/db"
)

// RelationWriter is the write-side builder for a single (parent, relation) pair, returned by
// Query.Relation / Orm.Relation. All write operations on a relation flow through this interface;
// flat methods like Orm.Save(parent, relation, child) intentionally do not exist.
//
// RelationWriter has NO Where / OrderBy / chain methods — fedaco-style "where(...).updateOrCreate(...)"
// composition is not supported because Goravel's relation system is metadata-driven (declared via
// ModelWithRelations.Relations()), not method-driven. Search criteria for the find-then-write
// methods (FirstOrNew/FirstOrCreate/UpdateOrCreate/FindOrNew) are passed via the attrs map, which
// is combined with the relation's foreign-key scope.
//
// For chained reads on a relation, use Query.Related(parent, name) instead — it returns the
// relation builder object: a full Query chain (Where/OrderBy/Get/...) already scoped by the
// relation's foreign key, which additionally carries the relation semantics (RelationQuery).
//
// Deprecated: RelationWriter is the legacy flat write path. Use the relation builder object
// instead — Query.Related / Orm.Related return a RelationQuery whose per-kind write interfaces
// (e.g. HasOneOrManyRelation, BelongsToRelation, BelongsToManyRelation, the through interfaces)
// carry these write families on the object itself, mirroring Laravel's Relation classes.
// RelationWriter is kept for compatibility; it will be removed in a future major version.
type RelationWriter interface {
	// Save inserts or updates child as a member of the relation. Sets child's foreign key (and
	// morph_type for MorphOne/MorphMany) from parent's local key, then persists child.
	// Supported kinds: HasOne, HasMany, MorphOne, MorphMany, Many2Many, MorphToMany.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Save(child any) error
	// SaveMany is the slice form of Save. children must be a slice or pointer-to-slice.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	SaveMany(children any) error
	// SaveWithPivot is Save with caller-supplied pivot column values for BelongsToMany kinds.
	// On HasOneOrMany kinds attrs is ignored (no pivot row).
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	SaveWithPivot(child any, attrs map[string]any) error
	// SaveManyWithPivot is the slice form of SaveWithPivot. attrsPerChild is keyed by the related
	// PK of each child; an entry may be nil to attach without extra columns.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	SaveManyWithPivot(children any, attrsPerChild map[any]map[string]any) error

	// Create persists a new related row. For HasOneOrMany kinds the framework pre-sets FK (and
	// morph type) on dest from parent, then inserts. For BelongsToMany kinds inserts dest first,
	// then writes a pivot row.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Create(dest any) error
	// CreateMany is the slice form of Create.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	CreateMany(dests any) error

	// Make returns an unsaved new instance of the related model carrying attrs with the foreign
	// key (and morph type, for MorphOne/MorphMany) pre-set from parent — Laravel's
	// HasOneOrMany::make. Nothing is persisted. Supported kinds: HasOne, HasMany, MorphOne,
	// MorphMany.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Make(attrs map[string]any, dest any) error
	// MakeMany is the slice form of Make: one unsaved instance per record, in record order. dests
	// must be a pointer to a slice of structs (or pointers to structs). Supported kinds as Make.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	MakeMany(records []map[string]any, dests any) error

	// FindOrNew finds the related row with primary key id. If absent, fills dest with a new
	// instance of the related model and pre-sets FK (and morph type) — does NOT persist. For the
	// BelongsToMany kinds the lookup is the pivot-joined relation read, a hit carries the hydrated
	// Pivot struct, and a miss leaves dest as it was — no foreign key to pre-set.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	FindOrNew(id any, dest any) error
	// FirstOrNew finds the first related row matching attrs. If absent, fills dest with a new
	// instance carrying attrs+values and pre-set FK — does NOT persist. For the BelongsToMany
	// kinds the search is the plain related read and the miss response is the attrs+values overlay.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	FirstOrNew(attrs, values map[string]any, dest any) error
	// FirstOrCreate is FirstOrNew that persists when no matching row exists. For BelongsToMany
	// kinds also writes a pivot row. The lookup is Laravel's two-level search — the pivot-joined
	// relation read, then the plain related read whose hit is attached — before createOrFirst.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	FirstOrCreate(attrs, values map[string]any, dest any) error
	// UpdateOrCreate finds the first related row matching attrs (or creates one), then overlays
	// values onto it and persists. For the BelongsToMany kinds it is BelongsToMany::updateOrCreate
	// — firstOrCreate's three-level search, which attaches a found-but-unattached row and
	// creates+attaches on a miss, then fill+save on every row that was not freshly created.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	UpdateOrCreate(attrs, values map[string]any, dest any) error
	// CreateOrFirst attempts the insert with attrs+values immediately — no lookup first — and,
	// when the insert trips a unique constraint, reads back the row matching attrs and returns it,
	// mirroring Laravel's createOrFirst with its UniqueConstraintViolationException catch: the
	// HasOneOrMany kinds re-read over the write connection (useWritePdo), the through kinds on the
	// default connection. For the BelongsToMany kinds the insert is the related row plus the pivot
	// attach (BelongsToMany::create), the catch reads the related row back and attaches it, and a
	// missed read-back or a pivot violation re-reads through the relation on the write
	// connection. Supported kinds: HasOne, HasMany, MorphOne, MorphMany,
	// HasOneThrough, HasManyThrough, Many2Many, MorphToMany.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	CreateOrFirst(attrs, values map[string]any, dest any) error
	// Upsert merges the parent key (and the morph type, for MorphOne/MorphMany) into every row and
	// delegates to the query-level upsert, mirroring HasOneOrMany::upsert / MorphOneOrMany::upsert.
	// Supported kinds: HasOne, HasMany, MorphOne, MorphMany.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Upsert(values []map[string]any, uniqueBy []string, update ...any) (*db.Result, error)

	// Associate sets parent's foreign key (and morph_type for MorphTo) to point at owner, then
	// persists parent. Supported kinds: BelongsTo, MorphTo. A nil owner — Laravel's
	// associate(null) — clears the foreign key (and morph type) and persists instead; a non-nil
	// owner must be a pointer to a struct.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Associate(owner any) error
	// Dissociate clears parent's foreign key (and morph_type for MorphTo) and persists parent.
	// Supported kinds: BelongsTo, MorphTo.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Dissociate() error

	// Attach inserts pivot rows linking parent to each id in ids. For polymorphic pivots the
	// morph_type column is filled from the parent's morph value. One pivot row per id, duplicates
	// included — a duplicate is written, or the pivot's unique constraint raises. Supported kinds:
	// Many2Many, MorphToMany, MorphedByMany.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Attach(ids []any) error
	// AttachWithPivot is Attach with per-row pivot column values. The map key is the related id;
	// the map value is the column-name-to-value map applied to that pivot row.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	AttachWithPivot(idsWithAttrs map[any]map[string]any) error
	// Detach removes pivot rows linking parent to the given ids. With nil ids, removes all pivot
	// rows for parent (and morph type, for polymorphic). Returns the number of rows removed.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Detach(ids ...any) (int64, error)

	// Sync replaces parent's pivot rows so they exactly match ids: detaches missing entries,
	// attaches new ones, leaves existing untouched.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Sync(ids []any) (*db.SyncResult, error)
	// SyncWithPivot is Sync with per-ID pivot column values. The map key is the related id; the
	// map value is the column-name-to-value map applied to that pivot row. For existing pivot
	// rows with non-empty attrs, updates the pivot columns (reported in SyncResult.Updated).
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	SyncWithPivot(idsWithAttrs map[any]map[string]any) (*db.SyncResult, error)
	// SyncWithPivotValues is a convenience wrapper that applies the same pivot column values to
	// all ids.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	SyncWithPivotValues(ids []any, pivotValues map[string]any) (*db.SyncResult, error)
	// SyncWithoutDetaching is Sync minus the detach step — adds missing entries only.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	SyncWithoutDetaching(ids []any) (*db.SyncResult, error)
	// SyncWithoutDetachingWithPivot is SyncWithPivot minus the detach step.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	SyncWithoutDetachingWithPivot(idsWithAttrs map[any]map[string]any) (*db.SyncResult, error)
	// Toggle attaches missing entries and detaches existing ones.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	Toggle(ids []any) (*db.SyncResult, error)
	// ToggleWithPivot is Toggle with per-ID pivot column values for newly attached rows.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	ToggleWithPivot(idsWithAttrs map[any]map[string]any) (*db.SyncResult, error)

	// UpdateExistingPivot updates pivot columns for an already-attached id.
	//
	// Deprecated: legacy flat RelationWriter path — use the relation builder object instead (Related / RelationQuery).
	UpdateExistingPivot(id any, attrs map[string]any) (int64, error)
}
