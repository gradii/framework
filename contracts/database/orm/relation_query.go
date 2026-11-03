package orm

import (
	"github.com/gradii/framework/contracts/database/db"
)

// RelationQuery is the base surface of the relation builder object Orm.Related / Query.Related
// return — the Go form of Eloquent's Relation classes as first-class query builders. The object
// embeds Query, so every builder method (Where / OrderBy / Get / First / Count / the chunk family
// / ...) chains on it already scoped to the relation's related rows, and the base adds the
// relation semantics Eloquent's Relation carries: the read result shape, the parent/related
// comparisons, the raw update and the parent/related accessors.
//
// The per-kind write families live on the kind-specific interfaces below, reached through a Go
// narrowing assertion:
//
//	rel := q.Related(&user, "Books")            // RelationQuery
//	books := rel.(orm.HasManyRelation)          // + HasOneOrManyRelation's write family
//	require.NoError(t, books.Save(&book))
//
// Documented Go divergences (vs PHP):
//   - Promoted builder methods return the builder, not the relation object, so relation-only
//     methods (One, the wherePivot family) are called before the first builder chain.
//   - Methods that share their name with a Query method (Make / Save / SaveQuietly / Create /
//     FirstOrNew /
//     FirstOrCreate / UpdateOrCreate / CreateOrFirst / Upsert) keep the Query signature — a Go
//     type cannot carry two same-named methods — and the object applies relation semantics
//     (foreign key / pivot scoping) underneath it. RelationWriter (Orm.Relation) keeps the
//     legacy map-first signatures for callers that prefer them.
type RelationQuery interface {
	Query

	// GetResults runs the relation read Eloquent's Relation::getResults runs: for the
	// single-result kinds (HasOne / MorphOne / BelongsTo / MorphTo / HasOneThrough) it reads one
	// row into dest — the WithDefault instance materialises on a miss when the relation declares
	// one or sets one at call time through WithDefault — and for the many-result kinds it reads
	// every matching row.
	GetResults(dest any) error
	// Is reports whether model is this relation's related model for the parent it was resolved
	// from, mirroring Eloquent's ComparesRelatedModels::is: the related tables must match and the
	// per-kind keys — the parent key and the model's related key — must be equal.
	Is(model any) bool
	// IsNot is the negation of Is, mirroring ComparesRelatedModels::isNot.
	IsNot(model any) bool
	// RawUpdate updates the related rows through the relation query without global scopes,
	// mirroring Relation::rawUpdate. attrs maps column names to values and the result reports the
	// affected rows.
	RawUpdate(attrs map[string]any) (*db.Result, error)
	// Touch stamps the related model's updated-at column (or the named column) to now on every row
	// the relation resolves to, mirroring Relation::touch: the constrained update with global
	// scopes dropped, no model events. BelongsTo / MorphTo with an absent parent key and a related
	// model that ignores touches are no-ops reporting zero affected rows. It shadows Query.Touch by
	// design — same signature, relation semantics.
	Touch(column ...string) (*db.Result, error)
	// GetParent gets the parent model instance the relation was resolved from.
	GetParent() any
	// GetRelated gets the related model instance the relation targets. A MorphTo relation
	// resolves its related model per parent row, so it reports nil.
	GetRelated() any
	// GetQualifiedParentKeyName gets the key the relation's parent side hangs off, qualified with
	// the table that owns it, mirroring Relation::getQualifiedParentKeyName and its per-kind
	// overrides: the has-family and the many-to-many qualify the parent's local (parent) key on
	// the parent table, BelongsTo / MorphTo the parent's own primary key on its table, and the
	// through kinds the through parent's second local key on the through table.
	GetQualifiedParentKeyName() string
	// CreatedAt gets the name of the parent model's "created at" column — Eloquent's
	// Relation::createdAt (vendor, illuminate/database v12.55.1, Relations/Relation.php:373,
	// `return $this->parent->getCreatedAtColumn();` — the parent model's column, not the
	// related's; oracle v12.55.1, executed, /tmp/lalign-probes/accessors.php: $user->posts()
	// reports the user class's "user_created_at" while the posts class declares "post_created_at").
	// The Go form resolves the column from the parent model's schema — the field the write path
	// stamps on insert (database/gorm's modelCreatedAtColumn) — and reports Laravel's
	// static::CREATED_AT default "created_at" when the parent declares none. BelongsToMany
	// overrides the pair to prefer the pivot's withTimestamps columns (BelongsToMany.php:1559/1569,
	// `$this->pivotCreatedAt ?? $this->parent->getCreatedAtColumn() ?? Model::CREATED_AT`): the
	// many-to-many object reports the pivot column its timestamp declaration resolved — the Pivot
	// struct's tagged / conventional fields, or, for PivotTimestamps: true (the bare
	// withTimestamps()), the parent model's column — and the parent's column when none is
	// declared, each column on its own (oracle v12.55.1, executed, /tmp/btm-pivot-ts-probe.php:
	// withTimestamps('pivot_created_at', 'pivot_updated_at') reports the pivot names, no
	// withTimestamps and withTimestamps(false, false) report the parent's "user_created_at" /
	// "user_updated_at", and withTimestamps('pivot_created_at') mixes them).
	CreatedAt() string
	// UpdatedAt is CreatedAt's updated-at sibling — Relation::updatedAt (Relations/Relation.php:383,
	// `$this->parent->getUpdatedAtColumn();`; oracle: "user_updated_at" from the parent class's
	// UPDATED_AT), with Laravel's static::UPDATED_AT default "updated_at" when the parent declares
	// none, and the same BelongsToMany pivot-column preference (BelongsToMany.php:1569).
	UpdatedAt() string
}

// HasOneOrManyRelation is the write surface shared by the HasOne / HasMany / MorphOne / MorphMany
// objects — Eloquent's HasOneOrMany methods on the relation. The methods whose names also exist
// on Query (Make / Save / Create / FirstOrNew / FirstOrCreate / UpdateOrCreate / CreateOrFirst /
// Upsert) take the Query signature — see RelationQuery's shadowing rule — and the attribute
// arguments accept the map-shaped values the relation write shells take.
type HasOneOrManyRelation interface {
	RelationQuery

	// Make builds an unsaved related model instance into dest: the given attributes (a
	// map-shaped value, optional) merged over the query's pending attributes (WithAttributes),
	// with the relation's foreign key (and morph type) pre-set — Eloquent's HasOneOrMany::make.
	// Nothing is written. The signature mirrors Query.Make.
	Make(dest any, attributes ...any) error
	// MakeMany is the slice form of Make: one unsaved instance per record, in record order.
	MakeMany(records []map[string]any, dests any) error
	// Save inserts or updates child as a member of the relation: the foreign key (and morph
	// type, for MorphOne/MorphMany) is set from the parent, then child is persisted. Kind
	// support matches RelationWriter.Save.
	Save(child any) error
	// SaveMany is the slice form of Save.
	SaveMany(children any) error
	// Create persists a new related row from dest, pre-setting the foreign key (and morph type)
	// from the parent, then inserts dest. Kind support matches RelationWriter.Create.
	Create(dest any) error
	// CreateMany is the slice form of Create.
	CreateMany(dests any) error
	// FindOrNew finds the related row with primary key id; on a miss it fills dest with a new
	// instance of the related model, foreign key pre-set. Nothing is written on the miss.
	FindOrNew(id any, dest any) error
	// FirstOrNew finds the first related row matching the attributes; on a miss it fills dest
	// with a new instance carrying the attributes (and values) and the pre-set foreign key
	// (and morph type). Nothing is written. The signature mirrors Query.FirstOrNew: attributes
	// is a map-shaped value and values a variadic list of maps.
	FirstOrNew(dest any, attributes any, values ...any) error
	// FirstOrCreate is FirstOrNew that persists on a miss. The signature mirrors
	// Query.FirstOrCreate: conds carries the attribute and value maps.
	FirstOrCreate(dest any, conds ...any) error
	// UpdateOrCreate finds the first related row matching the attributes (or creates one), then
	// overlays values onto it and persists. The signature mirrors Query.UpdateOrCreate.
	UpdateOrCreate(dest any, attributes any, values any) error
	// CreateOrFirst attempts the insert with the attributes (and values) immediately and, when
	// the insert trips a unique constraint, reads the row matching the attributes back. The
	// signature mirrors Query.CreateOrFirst.
	CreateOrFirst(dest any, conds ...any) error
	// Upsert merges the parent key (and morph type, for MorphOne/MorphMany) into every row and
	// delegates to the query-level upsert. values is a []map[string]any, the shape
	// Query.Upsert takes; the signature mirrors it.
	Upsert(values any, uniqueBy []string, update ...any) (*db.Result, error)

	// SaveQuietly is Save with every model event suppressed — Laravel's HasOneOrMany::saveQuietly
	// (vendor v12.55.1, HasOneOrMany.php:345), which runs save inside Model::withoutEvents
	// (Concerns/HasEvents.php:445, the NullDispatcher swap). The write is otherwise identical:
	// the foreign key (and morph type) still stamps and the row still persists. The oracle
	// (v12.55.1, executed) measured zero events on the quiet call against
	// saving/creating/created/saved on the loud one, with byte-identical insert SQL. It shadows
	// Query.SaveQuietly by design — same signature, relation semantics.
	SaveQuietly(child any) error
	// SaveManyQuietly is the slice form of SaveQuietly — HasOneOrMany::saveManyQuietly (:373).
	SaveManyQuietly(children any) error
	// CreateQuietly is Create with every model event suppressed — HasOneOrMany::createQuietly
	// (:403). The oracle measured zero events with the row persisted and the foreign key set.
	CreateQuietly(dest any) error
	// CreateManyQuietly is the slice form of CreateQuietly — HasOneOrMany::createManyQuietly
	// (:455).
	CreateManyQuietly(dests any) error
	// ForceCreate persists a new related row without the mass-assignment guard —
	// HasOneOrMany::forceCreate (:414), overridden by MorphOneOrMany::forceCreate (:78) and
	// MorphMany::forceCreate (:63) to stamp the morph type. The Go create path applies no guard
	// and already stamps the morph type, so this is the 1:1 alias of Create; the oracle measured
	// forceCreate firing the same saving/creating/created/saved events with the foreign key set,
	// a guarded attribute written where create silently discards it, and a pending
	// WithAttributes pair NOT merged (Laravel's forceCreate skips setForeignAttributesForCreate's
	// pending loop, :414-419; the Go write shells persist through a fresh session, so they drop
	// pending the same way).
	ForceCreate(dest any) error
	// ForceCreateQuietly is ForceCreate with every model event suppressed —
	// HasOneOrMany::forceCreateQuietly (:427).
	ForceCreateQuietly(dest any) error
	// ForceCreateMany is the slice form of ForceCreate — HasOneOrMany::forceCreateMany (:466).
	ForceCreateMany(dests any) error
	// ForceCreateManyQuietly is the slice form of ForceCreateQuietly —
	// HasOneOrMany::forceCreateManyQuietly (:483).
	ForceCreateManyQuietly(dests any) error

	// GetExistenceCompareKey gets the key the existence query compares the parent key against —
	// Eloquent's HasOneOrMany::getExistenceCompareKey (HasOneOrMany.php:571,
	// `return $this->getQualifiedForeignKeyName();`).
	GetExistenceCompareKey() string
	// GetForeignKeyName gets the plain (unqualified) foreign key — HasOneOrMany::getForeignKeyName
	// (HasOneOrMany.php:601, the last dot-segment of the stored qualified key). Oracle v12.55.1,
	// executed: $user->posts() reports "user_id"; the morphMany sibling "commentable_id".
	GetForeignKeyName() string
	// GetQualifiedForeignKeyName gets the foreign key qualified with the related table —
	// HasOneOrMany::getQualifiedForeignKeyName (HasOneOrMany.php:613, the column the declaration
	// stored qualified — Concerns/HasRelationships.php:220/543/314/641). Oracle:
	// "lax_posts.lax_user_id"; "lax_comments.commentable_id" for the morph form.
	GetQualifiedForeignKeyName() string
	// GetLocalKeyName gets the parent's local key — HasOneOrMany::getLocalKeyName
	// (HasOneOrMany.php:623, `return $this->localKey;`). Oracle: "id".
	GetLocalKeyName() string

	// GetInverseRelationship gets the name of the inverse relationship the relation applies, or ""
	// when none is in effect — Eloquent's SupportsInverseRelations::getInverseRelationship
	// (vendor, illuminate/database v12.55.1, Relations/Concerns/SupportsInverseRelations.php:129,
	// `return $this->inverseRelationship;`). The Go declaration is the relation descriptor's
	// Inverse field, installed at wrap time as the state every read links its results and every
	// write its created child back to the parent through; WithoutChaperone / WithoutInverse clear
	// it. Oracle (v12.55.1, executed, /tmp/lalign-defaults-inverse/inverse_writeback_runtime.php):
	// $user->posts()->getInverseRelationship() reports 'user' before the state is cleared, NULL
	// after withoutChaperone / withoutInverse, and NULL on a relation declared without one.
	GetInverseRelationship() string
	// WithoutChaperone removes the chaperone / inverse write-back for this relation and returns
	// the relation — Eloquent's SupportsInverseRelations::withoutChaperone (:151,
	// `$this->inverseRelationship = null;`, chained by its `return $this;`). Oracle (same probe):
	// a posts()->withoutChaperone()->get() result links nothing, and a
	// withoutChaperone()->create()/make() child carries no inverse relation, where the plain
	// create()/make() link the parent instance.
	WithoutChaperone() HasOneOrManyRelation
	// WithoutInverse is the alias of WithoutChaperone — SupportsInverseRelations::withoutInverse
	// (:141, `return $this->withoutChaperone();`). Oracle (same probe): withoutInverse()->get()
	// links nothing and getInverseRelationship() reports NULL afterwards.
	WithoutInverse() HasOneOrManyRelation
}

// HasManyRelation extends HasOneOrManyRelation with Eloquent's HasMany::one(): the relation
// narrowed to a single result. The narrowed object shares this object's builder and constraints;
// call One before the first builder chain.
type HasManyRelation interface {
	HasOneOrManyRelation

	// One narrows this relation to a HasOne relation — Laravel's HasMany::one() (and
	// MorphMany::one(), which narrows to MorphOne).
	One() HasOneRelation
}

// BelongsToRelation is the write surface of the BelongsTo / MorphTo objects — Eloquent's
// BelongsTo methods on the relation.
type BelongsToRelation interface {
	RelationQuery

	// Associate sets the parent's foreign key (and morph type, for MorphTo) to point at owner,
	// then persists the parent. A nil owner clears the foreign key (and morph type) instead.
	Associate(owner any) error
	// Dissociate clears the parent's foreign key (and morph type) and persists the parent.
	Dissociate() error
	// Disassociate is the MorphTo spelling of Dissociate — Eloquent's MorphTo::disassociate.
	Disassociate() error
	// GetChild gets the child model that holds the foreign key — for a BelongsTo the parent the
	// relation was resolved from, Eloquent's BelongsTo::getChild.
	GetChild() any

	// GetForeignKeyName gets the plain foreign key the child holds — BelongsTo::getForeignKeyName
	// (BelongsTo.php:306, `return $this->foreignKey;`). Oracle v12.55.1, executed: $post->user()
	// reports "lax_user_id"; the MorphTo form reports the morph id column ("commentable_id").
	GetForeignKeyName() string
	// GetQualifiedForeignKeyName gets the foreign key qualified with the child's own table —
	// BelongsTo::getQualifiedForeignKeyName (BelongsTo.php:316,
	// `$this->child->qualifyColumn(...)`). Oracle: "lax_posts.lax_user_id";
	// "lax_comments.commentable_id" for MorphTo.
	GetQualifiedForeignKeyName() string
	// GetOwnerKeyName gets the related model's owner key — BelongsTo::getOwnerKeyName
	// (BelongsTo.php:336, `return $this->ownerKey;`; MorphTo inherits it unchanged). Oracle: "id";
	// "custom_key" for an explicit morphTo(..., 'custom_key'); null on the MorphTo eager form (the Go
	// descriptor's build-time default reports "id" on an eager real descriptor; "" surfaces
	// only for a projection that carries no owner key). The MorphTo descriptor pins the instance path's resolution
	// (`$ownerKey ?? $instance->getKeyName()`, Concerns/HasRelationships.php:446-453) at build
	// time — see MorphToRelation.MorphOwnerKey.
	GetOwnerKeyName() string
	// GetQualifiedOwnerKeyName gets the owner key qualified with the related table —
	// BelongsTo::getQualifiedOwnerKeyName (BelongsTo.php:346), overridden by MorphTo
	// (MorphTo.php:429-437) to report "" when the owner key is null (the oracle's eager form).
	// Oracle: "lax_users.id".
	GetQualifiedOwnerKeyName() string
	// GetRelationName gets the declared relation name — BelongsTo::getRelationName
	// (BelongsTo.php:380, `return $this->relationName;`; MorphTo passes the name through).
	// Oracle: "user"; "commentable".
	GetRelationName() string

	// WithDefault overrides the relation's default related instance at call time — BelongsTo's
	// SupportsDefaultModels::withDefault (BelongsTo.php:8 composes the trait, :78-85 getResults).
	// The default instance is a plain newInstance of the related model with no attributes set
	// (BelongsTo::newRelatedInstanceFor, :286), standing in on a missing row and on a null foreign
	// key; MorphTo inherits the method through this interface (MorphTo extends BelongsTo in PHP).
	// Oracle (v12.55.1, executed, /tmp/lalign-defaults-inverse/runtime_withdefault.php):
	// belongsTo.missing-row.attributes [] and belongsTo.null-fk.attributes [].
	WithDefault(value any) BelongsToRelation
}

// HasOneThroughRelation is the write surface of the HasOneThrough object — the through create
// subset Eloquent's HasOneOrManyThrough carries. The through kinds hold no key on the related
// row, so the find-then-write methods carry no foreign key to set.
type HasOneThroughRelation interface {
	RelationQuery

	// FirstOrNew — see HasOneOrManyRelation.FirstOrNew. The signature mirrors Query.FirstOrNew.
	FirstOrNew(dest any, attributes any, values ...any) error
	// FirstOrCreate — see HasOneOrManyRelation.FirstOrCreate. The signature mirrors
	// Query.FirstOrCreate.
	FirstOrCreate(dest any, conds ...any) error
	// UpdateOrCreate — see HasOneOrManyRelation.UpdateOrCreate. The signature mirrors
	// Query.UpdateOrCreate.
	UpdateOrCreate(dest any, attributes any, values any) error
	// CreateOrFirst — see HasOneOrManyRelation.CreateOrFirst. The signature mirrors
	// Query.CreateOrFirst.
	CreateOrFirst(dest any, conds ...any) error

	// WithDefault overrides the relation's default related instance at call time — HasOneThrough's
	// SupportsDefaultModels::withDefault (HasOneThrough.php:22/24 composes the trait; :27-34
	// getResults). The default instance is a plain newInstance of the related model
	// (HasOneThrough::newRelatedInstanceFor, :108). The method rides the shared through base, so a
	// HasManyThrough object carries it too — a Go surface superset: Laravel's HasManyThrough has no
	// SupportsDefaultModels, and a many-result read never consults the slot (GetResults materialises
	// a default for the single-result kinds only). Oracle (v12.55.1, executed,
	// /tmp/lalign-defaults-inverse/runtime_withdefault.php): hasOneThrough.missing-row.class
	// "RtCar" with attributes [], and hasOneThrough.null-key.queries 0.
	WithDefault(value any) HasOneThroughRelation
}

// HasManyThroughRelation extends HasOneThroughRelation with Eloquent's HasManyThrough::one().
type HasManyThroughRelation interface {
	HasOneThroughRelation

	// One narrows this relation to a HasOneThrough relation sharing this object's builder and
	// constraints.
	One() HasOneThroughRelation
}

// PivotModel is the minimal pivot-object surface the many-to-many relation's pivot-construction
// methods hand back and the port's pivot call sites act on — the Go stand-in for the common
// supertype every Laravel pivot class shares by inheriting Illuminate\Database\Eloquent\Relations
// \Pivot. A value of this interface can be handed to Fresh/Refresh (PivotKeyed names the
// identifying columns a keyless pivot row is re-read by), and its attribute bag, table and exists
// flag are the state a hydrated $model->pivot exposes. A caller needing more — the fluent
// setters, the save / delete family, the morph getters — narrows to the concrete type (this
// port's relations.Pivot / relations.MorphPivot), the way PHP code narrows on the class.
type PivotModel interface {
	PivotKeyed
	// Attributes gets the pivot's attribute bag (Model::getAttributes' by-value semantics).
	Attributes() map[string]any
	// GetTable gets the pivot table the row lives on.
	GetTable() string
	// Exists reports the persisted flag the constructors set and save / delete maintain.
	Exists() bool
}

// PivotFactory builds a pivot object for a many-to-many relation — the Go stand-in for Laravel's
// `using(SomePivot::class)` custom pivot class (BelongsToMany::using, vendor v12.55.1,
// BelongsToMany.php:350). The factory receives the arguments AsPivot's constructors take — the
// relation's parent, the pivot attribute map, the pivot table and the exists flag — and returns
// the object the relation's NewPivot / NewExistingPivot surfaces hand back.
//
// The factory stands in for the custom class Laravel dispatches to through Model::newPivot
// (Model.php:1726, `$using::fromRawAttributes($parent, $attributes, $table, $exists)`), so it
// should build with fromRawAttributes semantics — the original bag left empty while !exists, so
// the attributes read dirty (oracle, executed, /tmp/decide-light-using/using_probe.php:
// `U2c custom newPivot dirty => ['level' => 'gold']`). The result must satisfy PivotModel; a
// result of this port's own Pivot / MorphPivot types is stamped by the relation itself (the
// fluent setPivotKeys / setRelatedModel pair a Go interface cannot re-dispatch), while any other
// custom type must return fully stamped: pivot keys in place and the related model set. nil keeps
// the default constructors.
type PivotFactory func(parent any, attributes map[string]any, table string, exists bool) (PivotModel, error)

// BelongsToManyRelation is the write surface of the Many2Many / MorphToMany / MorphedByMany
// objects — Eloquent's BelongsToMany methods on the relation: the pivot write family, the
// pivot-column query family and the related-id list. The methods whose names also exist on
// Query (Save / Create / FirstOrNew / FirstOrCreate / UpdateOrCreate / CreateOrFirst) take the
// Query signature — see RelationQuery's shadowing rule.
type BelongsToManyRelation interface {
	RelationQuery

	// FindOrNew finds the related row with primary key id; on a miss it fills dest with a new
	// instance of the related model. Nothing is written on the miss.
	FindOrNew(id any, dest any) error
	// FindSole returns the attached related row with primary key id, erroring when no row matches
	// and when several do — Laravel BelongsToMany::findSole (BelongsToMany.php:750-757,
	// `$this->where($this->getRelated()->getQualifiedKeyName(), '=', $this->parseId($id))->sole($columns)`).
	// The Go form composes WhereKey(id)->Sole(dest) over the relation's pivot-aware read; oracle
	// v12.55.1, executed (/tmp/lalign-probes/accessors.php): the statement carries the pivot
	// aliases and `... and "lax_roles"."id" = ? limit 2`; a missing id throws
	// ModelNotFoundException (the Go read reports errors.OrmRecordNotFound); a model argument
	// stands for its own key.
	FindSole(id any, dest any) error
	// FirstWhere adds the where clause and returns the first matching row into dest — Laravel
	// BelongsToMany::firstWhere (BelongsToMany.php:853-856,
	// `$this->where($column, $operator, $value, $boolean)->first()`); a miss leaves dest empty and
	// reports no error. Oracle: `... and "name" = ? limit 1`; a miss returns null.
	FirstWhere(dest any, column any, args ...any) error
	// FirstOrNew — see HasOneOrManyRelation.FirstOrNew. The signature mirrors Query.FirstOrNew.
	FirstOrNew(dest any, attributes any, values ...any) error
	// FirstOrCreate — see HasOneOrManyRelation.FirstOrCreate; on a fresh create a pivot row is
	// written too. The signature mirrors Query.FirstOrCreate.
	FirstOrCreate(dest any, conds ...any) error
	// UpdateOrCreate — Laravel BelongsToMany::updateOrCreate (BelongsToMany.php:707): FirstOrCreate's
	// three-level search — an attached hit returns with its pivot, an unattached hit is attached,
	// a miss is created and attached — followed by fill+save on every row that was not freshly
	// created (wasRecentlyCreated false). The signature mirrors Query.UpdateOrCreate.
	UpdateOrCreate(dest any, attributes any, values any) error
	// CreateOrFirst — see HasOneOrManyRelation.CreateOrFirst. The signature mirrors
	// Query.CreateOrFirst.
	CreateOrFirst(dest any, conds ...any) error

	// Save inserts or updates child as a member of the relation. Kind support matches
	// RelationWriter.Save.
	Save(child any) error
	// SaveQuietly is Save with every model event suppressed — Laravel's BelongsToMany::saveQuietly
	// (BelongsToMany.php:1366), save inside Model::withoutEvents (HasEvents.php:445). The oracle
	// measured zero events on the quiet call while the related row and the pivot row still land
	// and the model is returned; the pivot attach and the parent touch raise no model events on
	// either path.
	SaveQuietly(child any) error
	// SaveMany is the slice form of Save.
	SaveMany(children any) error
	// SaveManyQuietly is the slice form of SaveQuietly — BelongsToMany::saveManyQuietly (:1402).
	SaveManyQuietly(children any) error
	// SaveWithPivot is Save with caller-supplied pivot column values.
	SaveWithPivot(child any, attrs map[string]any) error
	// SaveManyWithPivot is the slice form of SaveWithPivot. attrsPerChild is keyed by the
	// related PK of each child; an entry may be nil.
	SaveManyWithPivot(children any, attrsPerChild map[any]map[string]any) error
	// Create persists a new related row and the pivot row linking it to the parent.
	Create(dest any) error
	// CreateMany is the slice form of Create.
	CreateMany(dests any) error

	// Attach inserts one pivot row per id — duplicates included, with no pivot-existence read; a
	// duplicate row is written or the pivot's unique constraint raises.
	Attach(ids []any) error
	// AttachOrFail is Attach inside a transaction on the parent's connection — IWPT::attachOrFail
	// (InteractsWithPivotTable.php:365; a 12.55-only addition),
	// $this->parent->getConnection()->transaction(fn () => $this->attach(...)). The oracle
	// measured a begin+commit pair around the call and a begin+rollback pair when the insert
	// fails.
	AttachOrFail(ids []any) error
	// AttachWithPivot is Attach with per-row pivot column values, keyed by the related id.
	AttachWithPivot(idsWithAttrs map[any]map[string]any) error
	// Detach removes the pivot rows for the given ids; nil ids remove every pivot row of the
	// parent. It reports the removed row count.
	Detach(ids ...any) (int64, error)
	// DetachOrFail is Detach inside a parent-connection transaction — IWPT::detachOrFail (:562).
	DetachOrFail(ids ...any) (int64, error)
	// Sync replaces the parent's pivot rows so they exactly match ids.
	Sync(ids []any) (*db.SyncResult, error)
	// SyncOrFail is Sync inside a parent-connection transaction — IWPT::syncOrFail (:157).
	SyncOrFail(ids []any) (*db.SyncResult, error)
	// SyncWithPivot is Sync with per-ID pivot column values, keyed by the related id.
	SyncWithPivot(idsWithAttrs map[any]map[string]any) (*db.SyncResult, error)
	// SyncWithPivotValues applies the same pivot column values to every id.
	SyncWithPivotValues(ids []any, pivotValues map[string]any) (*db.SyncResult, error)
	// SyncWithPivotValuesOrFail is SyncWithPivotValues inside a parent-connection transaction —
	// IWPT::syncWithPivotValuesOrFail (:200).
	SyncWithPivotValuesOrFail(ids []any, pivotValues map[string]any) (*db.SyncResult, error)
	// SyncWithoutDetaching is Sync minus the detach step — adds missing entries only.
	SyncWithoutDetaching(ids []any) (*db.SyncResult, error)
	// SyncWithoutDetachingOrFail is SyncWithoutDetaching inside a parent-connection transaction —
	// IWPT::syncWithoutDetachingOrFail (:170), Laravel's syncOrFail($ids, false).
	SyncWithoutDetachingOrFail(ids []any) (*db.SyncResult, error)
	// SyncWithoutDetachingWithPivot is SyncWithPivot minus the detach step.
	SyncWithoutDetachingWithPivot(idsWithAttrs map[any]map[string]any) (*db.SyncResult, error)
	// Toggle attaches missing entries and detaches existing ones.
	Toggle(ids []any) (*db.SyncResult, error)
	// ToggleOrFail is Toggle inside a parent-connection transaction — IWPT::toggleOrFail (:75).
	ToggleOrFail(ids []any) (*db.SyncResult, error)
	// ToggleWithPivot is Toggle with per-ID pivot column values for newly attached rows.
	ToggleWithPivot(idsWithAttrs map[any]map[string]any) (*db.SyncResult, error)
	// UpdateExistingPivot updates the pivot columns of an already-attached id.
	UpdateExistingPivot(id any, attrs map[string]any) (int64, error)
	// UpdateExistingPivotOrFail is UpdateExistingPivot inside a parent-connection transaction —
	// IWPT::updateExistingPivotOrFail (:299).
	UpdateExistingPivotOrFail(id any, attrs map[string]any) (int64, error)
	// AllRelatedIds collects the related ids into dest — a pointer to a slice — plucking the
	// related pivot key, Eloquent's allRelatedIds.
	AllRelatedIds(dest any) error

	// WherePivot adds a "where" clause on the pivot table into dest-side queries; the column is
	// qualified with the pivot table and args follow Query.Where. Mirrors BelongsToMany::wherePivot.
	WherePivot(column any, args ...any) BelongsToManyRelation
	// OrWherePivot is WherePivot with the "or" connector.
	OrWherePivot(column any, args ...any) BelongsToManyRelation
	// WherePivotIn adds a "where column in" clause on the pivot table.
	WherePivotIn(column any, values []any) BelongsToManyRelation
	// WherePivotNotIn adds a "where column not in" clause on the pivot table.
	WherePivotNotIn(column any, values []any) BelongsToManyRelation
	// OrWherePivotIn is WherePivotIn with the "or" connector.
	OrWherePivotIn(column any, values []any) BelongsToManyRelation
	// OrWherePivotNotIn is WherePivotNotIn with the "or" connector.
	OrWherePivotNotIn(column any, values []any) BelongsToManyRelation
	// WherePivotNull adds a "pivot column is null" clause.
	WherePivotNull(column any) BelongsToManyRelation
	// WherePivotNotNull adds a "pivot column is not null" clause.
	WherePivotNotNull(column any) BelongsToManyRelation
	// OrWherePivotNull is WherePivotNull with the "or" connector.
	OrWherePivotNull(column any) BelongsToManyRelation
	// OrWherePivotNotNull is WherePivotNotNull with the "or" connector.
	OrWherePivotNotNull(column any) BelongsToManyRelation
	// WherePivotBetween adds a "pivot column between x and y" clause.
	WherePivotBetween(column any, x, y any) BelongsToManyRelation
	// WherePivotNotBetween adds a "pivot column not between x and y" clause.
	WherePivotNotBetween(column any, x, y any) BelongsToManyRelation
	// OrWherePivotBetween is WherePivotBetween with the "or" connector.
	OrWherePivotBetween(column any, x, y any) BelongsToManyRelation
	// OrWherePivotNotBetween is WherePivotNotBetween with the "or" connector.
	OrWherePivotNotBetween(column any, x, y any) BelongsToManyRelation
	// WithPivotValue adds a "pivot column = value" clause and records the pair for the pivot
	// construction, mirroring BelongsToMany::withPivotValue (BelongsToMany.php:481-498): base
	// NewPivot merges the recorded values into its attribute bag (InteractsWithPivotTable.php:629,
	// recorded values as defaults so the caller's attributes win), while the morph override does
	// not (MorphToMany.php:156-172). The attach / attachOrFail / sync / toggle families stamp the
	// recorded pairs onto every pivot INSERT row through baseAttachRecord's values loop
	// (InteractsWithPivotTable.php:454-472) — recorded defaults, the caller's attributes win, the
	// last recorded pair for a column winning. A nil value pairs the argument column with the
	// pivot's parent-side foreign key column — the frozen Go contract's pre-12 argument swap
	// (Laravel 12 throws InvalidArgumentException on a null value).
	WithPivotValue(column any, value any) BelongsToManyRelation
	// OrderByPivot adds an "order by pivot column" clause. Mirrors BelongsToMany::orderByPivot;
	// the Go object keeps the relation (PHP returns the builder), so further wherePivot calls
	// stay on the object.
	OrderByPivot(column any, direction ...string) BelongsToManyRelation

	// OrderByPivotDesc adds a descending "order by pivot column" clause — BelongsToMany::orderByPivotDesc
	// (vendor v12.55.1, BelongsToMany.php:605-608,
	// `return $this->orderBy($this->qualifyPivotColumn($column), 'desc');`; 12.55-only — the
	// v12.43.1 checkout has no Desc sibling, its orderByPivot ends at BelongsToMany.php:583-586).
	// The Go form is the one-liner over OrderByPivot(column, "desc"): an Expression column and an
	// invalid direction ride the exact paths OrderByPivot documents beneath it.
	OrderByPivotDesc(column any) BelongsToManyRelation

	// WithPivot registers pivot-table columns to retrieve with this relation's reads —
	// InteractsWithPivotTable::withPivot (vendor v12.55.1, Concerns/InteractsWithPivotTable.php:702-707,
	// `$this->pivotColumns = array_merge($this->pivotColumns, is_array($columns) ? $columns :
	// func_get_args());`). Laravel merges the call-time list into $pivotColumns, which
	// aliasedPivotColumns (:970-983) then appends to the relation read's select list as
	// `qualifyPivotColumn($column).' as pivot_'.$column`, deduplicated with ->unique(). The Go form
	// carries the call-time list on the object and feeds it to the read's select rewrite after the
	// related model's declared PivotField-struct columns — the Go stand-in for the declaration-time
	// half of the list (see Many2Many.PivotField). Oracle (v12.55.1, executed, sqlite;
	// /tmp/lalign-probes/pivot_surface.out): P2 — `withPivot('level')` makes the get() statement
	// carry `"lax_role_user"."level" as "pivot_level"` and the hydrated pivot report 'gold'; P3 —
	// the raw list keeps duplicates (["level","level"]) while the SELECT deduplicates; P4 — chained
	// calls append and the array form keeps its order; P7 — attach's insert and sync's pivot read
	// never replay the list. An entry with no declared struct field is selected but not surfaced in
	// Go (PHP hydrates it as a dynamic pivot attribute — the documented divergence), and the Go
	// write shells resolve their own statements per call, so call-time entries cannot reach them
	// either (the wherePivot-style frozen-shell divergence).
	WithPivot(columns ...string) BelongsToManyRelation
	// HasPivotColumn reports whether column was explicitly registered through WithPivot — IWPT
	// ::hasPivotColumn (:508-510, `return in_array($column, $this->pivotColumns);`), the explicit
	// list only: the default pivot keys are not members until passed to WithPivot themselves
	// (oracle P1a false for both foreign keys; P9 true after `withPivot('lax_user_id')`), and the
	// declared PivotField struct's columns are not either.
	HasPivotColumn(column string) bool
	// GetPivotColumns gets the explicit pivot-column list WithPivot accumulated —
	// BelongsToMany::getPivotColumns (vendor v12.55.1, BelongsToMany.php:1689-1691,
	// `return $this->pivotColumns;`). The raw merged list in call order: duplicates are kept
	// (oracle P3) and nothing is filtered; nil before any WithPivot — the oracle's [] default
	// (P1a). The returned slice is a fresh copy (PHP arrays return by value), so a caller mutating
	// it cannot reach the relation.
	GetPivotColumns() []string
	// GetPivotAccessor gets the name the pivot hydration lands under — BelongsToMany
	// ::getPivotAccessor (vendor v12.55.1, BelongsToMany.php:1679-1681, `return $this->accessor;`:
	// the As override, else the class default 'pivot', BelongsToMany.php:145). The Go accessor is
	// the name of the struct field the read hydrates on the related model — the declared
	// PivotField (default "Pivot") or the As override — because a Go model carries its pivot in a
	// declared field, not in a dynamic relation map; a projection that names no field reports "".
	GetPivotAccessor() string
	// As declares the name the pivot hydration lands under, replacing the relation's declared
	// accessor — BelongsToMany::as (vendor v12.55.1, BelongsToMany.php:367-372,
	// `$this->accessor = $accessor; return $this;`). In Go that name selects the struct field the
	// read hydrates on the related model (the PivotField convention): As re-resolves the read's
	// pivot plan against the field with that name — the same resolution the declaration performs,
	// so a name the related model has no field for hydrates nothing while the read keeps its
	// declared select list under the fixed pivot-key aliases (oracle P5: as('subscription') moves
	// the hydrated pivot to
	// $role->subscription and leaves $role->pivot null, and the SQL aliases stay pivot_-prefixed —
	// the accessor never renames them). A field that exists but is not a struct reports the read's
	// error (errors.OrmRelationPivotFieldNotStruct — the declaration-time typo error). An empty
	// accessor restores the declared field. The list half is unaffected (oracle P5:
	// getPivotColumns() unchanged under as()).
	As(accessor string) BelongsToManyRelation

	// Using records the pivot factory the relation's pivot-construction methods dispatch through —
	// Laravel's BelongsToMany::using (vendor v12.55.1, BelongsToMany.php:350-355, `$this->using =
	// $class; return $this;`) — the Go stand-in for the custom pivot class string, held per
	// relation instance exactly as Laravel holds $using. nil clears it back to the default
	// constructors. Oracle (v12.55.1, executed, /tmp/decide-light-using/using_probe.php): the
	// default relation reports the class default Pivot, `using(LuRoleUser::class)` reports
	// LuRoleUser (`U1a default getPivotClass => 'Illuminate\Database\Eloquent\Relations\Pivot'`,
	// `U1c getPivotClass after using => 'LuRoleUser'`). getPivotClass itself and the class-string
	// argument are deferred — Go has no class strings; the factory replaces the class.
	Using(factory PivotFactory) BelongsToManyRelation
	// NewPivot builds the pivot instance the relation's surfaces construct —
	// InteractsWithPivotTable::newPivot (vendor v12.55.1, InteractsWithPivotTable.php:627-642):
	// the Using factory is consulted when one is recorded (Laravel dispatches through
	// Model::newPivot, Model.php:1726), else the relation builds the class default
	// Pivot::fromAttributes; the finished pivot then carries the relation's pivot keys and the
	// related model (setPivotKeys -> setRelatedModel, :640-642). Oracle (same probe): `U2a default
	// newPivot class/exists => [Pivot, false]`, `U2a default newPivot table/keys => ['lu_role_user',
	// 'lu_user_id', 'role_id']`, `U2c custom newPivot class/exists => ['LuRoleUser', false]`. The
	// morph kinds override with MorphPivot and the morph stamps (MorphToMany::newPivot,
	// MorphToMany.php:156), and the override does not merge the withPivotValue-recorded values
	// (:629). Base construction does merge them first — `array_merge(array_column(
	// $this->pivotValues, 'value', 'column'), $attributes)`, recorded values as defaults and the
	// caller's attributes winning (oracle v12.55.1, executed, /tmp/reverify429/pv_probe.php:
	// `C21 base attrs => ['level' => 'pv']`, `C21 caller wins => ['level' => 'caller']`,
	// `C21 morph attrs => ['taggable_type' => 'PvPost']`).
	NewPivot(attributes map[string]any, exists bool) (PivotModel, error)
	// NewExistingPivot is NewPivot with exists true — InteractsWithPivotTable::newExistingPivot
	// (:646, `return $this->newPivot($attributes, true);`), the constructor the read path's
	// BelongsToMany::hydratePivotRelation (BelongsToMany.php:1234) feeds migrated pivot attributes
	// into (oracle: `U2b default newExistingPivot class/exists => [Pivot, true]`, `U2d custom
	// newExistingPivot class/exists/dirty => [LuRoleUser, true, []]`).
	NewExistingPivot(attributes map[string]any) (PivotModel, error)
}

// HasOneRelation is the HasOne object — HasOneOrManyRelation plus the call-time WithDefault slot
// Eloquent's HasOne composes through SupportsDefaultModels (HasOne.php:22). Declared rather than
// aliased because it adds a method: a plain HasMany object narrows through HasOneOrManyRelation,
// not through this name — the same rule the morph contracts below follow.
type HasOneRelation interface {
	HasOneOrManyRelation

	// WithDefault overrides the relation's default related instance at call time — Eloquent's
	// SupportsDefaultModels::withDefault (vendor v12.55.1, Relations/Concerns/SupportsDefaultModels
	// .php:32, `$this->withDefault = $callback; return $this;`; HasOne.php:22 composes the trait).
	// Accepted values match the declaration's WithDefault field — true / map[string]any /
	// func(related, parent any) any; False, nil and an empty map yield no default (PHP falsy) —
	// and the call replaces the one slot the declaration seeded. The default materialises exactly
	// where the declaration's does: GetResults on a miss, or on a null parent key with no query at
	// all (the fresh instance still carries the foreign key stamp — and the morph type, on the
	// morph kinds); a real row always wins. Oracle (v12.55.1, executed,
	// /tmp/lalign-defaults-inverse/runtime_withdefault.php):
	// hasOne.missing-row.attributes {"foreign_key":2}, hasOne.null-key.queries 0,
	// hasOne.declared-then-runtime.name "runtime".
	WithDefault(value any) HasOneRelation
}

// QueryWithInverseChaperone is the optional capability a Query implements when it carries the
// inverse (chaperone) write-back state a relation read installs — the Go form of the state
// SupportsInverseRelations::$inverseRelationship keeps on the relation. The relation objects'
// WithoutChaperone reaches it through a type assertion (the relations package cannot name
// database/gorm types); a Query implementation without the capability — a mock, a foreign driver —
// is left as it stands. database/gorm implements it on OrmBuilder.
type QueryWithInverseChaperone interface {
	// InverseRelationship reports the inverse relation name the write-back links results to, ""
	// when none is in effect (never installed, or cleared).
	InverseRelationship() string
	// WithoutInverseChaperone clears the write-back state — the object's WithoutChaperone /
	// WithoutInverse call.
	WithoutInverseChaperone()
}

// Aliases (Go structural typing): each name denotes the same interface, so a returned object
// narrows through any of its kind's names.
type (
	Many2ManyRelation     = BelongsToManyRelation
	MorphedByManyRelation = BelongsToManyRelation
)

// The morph contracts below are declared rather than aliased: each embeds its kind's base
// interface and adds the morph-field accessors Eloquent's morph classes carry (MorphOneOrMany's
// getMorphType / getQualifiedMorphType / getMorphClass, MorphTo's getMorphType plus its owner key,
// MorphToMany's getMorphType / getQualifiedMorphTypeName / getMorphClass / getInverse;
// illuminate/database v12.55.1). Because they add methods, only the morph relation objects
// satisfy them — `rel.(orm.MorphOneRelation)` is a meaningful kind check, where a plain HasOne
// object (which still narrows through the HasOneRelation alias above) is not. The only non-mock
// implementations are the dedicated morph leaves (morphOneRelationQuery / morphManyRelationQuery
// / morphToRelationQuery / morphToManyRelationQuery).

// MorphOneRelation is the MorphOne object — HasOneRelation (the call-time WithDefault slot
// included, MorphOne.php:22 composes SupportsDefaultModels) plus the morph-field accessors.
type MorphOneRelation interface {
	HasOneRelation

	// MorphType reports the morph type column without its table qualifier — Eloquent's
	// MorphOneOrMany::getMorphType.
	MorphType() string
	// QualifiedMorphType reports the morph type column qualified with the related table —
	// Eloquent's MorphOneOrMany::getQualifiedMorphType.
	QualifiedMorphType() string
	// MorphClass reports the parent's morph class value — the declared morph-map alias, or the
	// parent's table name — Eloquent's MorphOneOrMany::getMorphClass.
	MorphClass() string
}

// MorphManyRelation is the MorphMany object — HasManyRelation plus the morph-field accessors.
type MorphManyRelation interface {
	HasManyRelation

	// MorphType — see MorphOneRelation.MorphType.
	MorphType() string
	// QualifiedMorphType — see MorphOneRelation.QualifiedMorphType.
	QualifiedMorphType() string
	// MorphClass — see MorphOneRelation.MorphClass.
	MorphClass() string
}

// MorphToRelation is the MorphTo object — BelongsToRelation plus the morph-field accessors.
type MorphToRelation interface {
	BelongsToRelation

	// MorphType reports the morph type column this table carries — Eloquent's MorphTo::getMorphType.
	MorphType() string
	// MorphOwnerKey reports the owner key column on each resolved related table — Eloquent's
	// MorphTo owner key (read through BelongsTo::getOwnerKeyName); the default is "id".
	MorphOwnerKey() string
}

// MorphToManyRelation is the MorphToMany / MorphedByMany object — BelongsToManyRelation plus the
// morph-field accessors.
type MorphToManyRelation interface {
	BelongsToManyRelation

	// MorphType reports the pivot's morph type column name — Eloquent's MorphToMany::getMorphType.
	MorphType() string
	// QualifiedMorphType reports the pivot's morph type column qualified with the pivot table —
	// Eloquent's MorphToMany::getQualifiedMorphTypeName.
	QualifiedMorphType() string
	// MorphClass reports the morph class value the pivot rows pin — the parent's morph class for
	// MorphToMany, the related's for MorphedByMany — Eloquent's MorphToMany::getMorphClass.
	MorphClass() string
	// Inverse reports whether the relation was declared as the inverse side (morphedByMany) —
	// Eloquent's MorphToMany::getInverse.
	Inverse() bool
}
