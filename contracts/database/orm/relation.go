package orm

// RelationCallback is the signature of a closure used to scope a relationship existence query.
// Mirrors the (q) => void closure shape from the QueriesRelationships mixin. The returned Query
// is the inner subquery used for has / whereHas / withCount / etc.
type RelationCallback func(query Query) Query

// MorphRelationCallback is the per-type variant of RelationCallback used by the *HasMorph family,
// matching the `function ($query, $type)` callback for whereHasMorph. The second argument is the
// morph type currently being scoped (the related model's morph class - the table name in GORM's
// polymorphic convention).
type MorphRelationCallback func(query Query, morphType string) Query

// PivotQuery is a small builder surface for scoping the pivot-table SQL emitted by Sync / Detach /
// UpdateExistingPivot. It is intentionally narrower than the full Query
// interface — only the WHERE-style methods that make sense on a single-table pivot read/write are
// exposed. Mirrors fedaco's `wherePivot` / `wherePivotIn` / `wherePivotNull` accumulators on
// BelongsToMany.
//
// NOTE: PivotQuery filters only SELECT / UPDATE / DELETE on the pivot table — equality conditions
// added here are NOT auto-injected into INSERT rows on Attach. Callers that need the conditions
// to appear on inserted rows should pass them through Attach attrs.
type PivotQuery interface {
	// Where adds a `column op value` clause to the pivot query. operator defaults to "=" when
	// only two args are passed (column, value).
	Where(column string, args ...any) PivotQuery
	// WhereIn adds a `column in (...)` clause to the pivot query.
	WhereIn(column string, values []any) PivotQuery
	// WhereNotIn adds a `column not in (...)` clause to the pivot query.
	WhereNotIn(column string, values []any) PivotQuery
	// WhereNull adds a `column is null` clause to the pivot query.
	WhereNull(column string) PivotQuery
	// WhereNotNull adds a `column is not null` clause to the pivot query.
	WhereNotNull(column string) PivotQuery
}

// PivotCallback scopes pivot-table reads (and the corresponding diff-driven writes) for a
// BelongsToMany relation. Declared via Many2Many / MorphToMany / MorphedByMany's OnPivotQuery
// field; applied automatically to Sync / Detach / UpdateExistingPivot and Attach's duplicate-
// detection SELECT.
type PivotCallback func(query PivotQuery) PivotQuery

// QueryWithRelations is the Go port of the QueriesRelationships mixin from Laravel and the 1:1
// TypeScript port in fedaco at libs/fedaco/src/fedaco/mixins/queries-relationships.ts.
//
// Where the upstream framework has first-class Relation objects with getRelationExistenceQuery
// methods, GORM models its relationships through struct-tag metadata. The bridge here surfaces
// relationship-existence and aggregate-subselect queries on top of that metadata. Callers can
// write, for example:
//
//	users := []User{}
//	query.Query().Has("Books", ">=", 3).WithCount("Roles").Get(&users)
//
// Existence-style methods (Has / OrHas / DoesntHave / WhereHas / ...) accept a variadic args
// slice that may carry, in any order:
//   - a RelationCallback or func(Query) Query to scope the inner subquery
//   - a string operator (e.g. ">=", "<", ">", "=") - defaults to ">="
//   - an int count - defaults to 1
//
// Morph-style methods take an additional types []any of model instances; the morph value used in
// the type column is derived from each model's GORM-resolved table name (e.g. *User -> "users").
//
// QueryWithRelations is embedded into Query, so all of these methods are also reachable directly
// off Query without a type assertion.
type QueryWithRelations interface {
	// Has adds a relationship count / exists condition to the query.
	// Defaults to operator ">=" and count 1.
	Has(relation string, args ...any) Query
	// OrHas adds a relationship count / exists condition to the query with an "or" conjunction.
	OrHas(relation string, args ...any) Query
	// DoesntHave adds a relationship absence condition - equivalent to Has(rel, "<", 1).
	DoesntHave(relation string, args ...any) Query
	// OrDoesntHave adds a relationship absence condition with an "or" conjunction.
	OrDoesntHave(relation string, args ...any) Query
	// WhereHas adds a relationship count / exists condition to the query with where clauses.
	// Identical semantics to Has but conventionally used with a callback first arg.
	WhereHas(relation string, args ...any) Query
	// OrWhereHas adds a relationship count / exists condition to the query with where clauses
	// and an "or" conjunction.
	OrWhereHas(relation string, args ...any) Query
	// WhereDoesntHave adds a relationship absence condition to the query with where clauses.
	WhereDoesntHave(relation string, args ...any) Query
	// OrWhereDoesntHave adds a relationship absence condition to the query with where clauses
	// and an "or" conjunction.
	OrWhereDoesntHave(relation string, args ...any) Query

	// HasMorph adds a polymorphic relationship count / exists condition to the query.
	// types is a slice of model instances (e.g. []any{&Post{}, &Video{}}); the morph value
	// used in the type column is derived from each model's table name. Pass []any{"*"} to
	// resolve the distinct morph values from the parent table's type column at compile time,
	// mirroring Eloquent's hasMorph(['*']) auto-discovery; Has()/WhereHas() on a MorphTo
	// relation takes that path automatically, the way Eloquent's has() delegates to hasMorph.
	HasMorph(relation string, types []any, args ...any) Query
	// OrHasMorph adds a polymorphic relationship count / exists condition with an "or" conjunction.
	OrHasMorph(relation string, types []any, args ...any) Query
	// DoesntHaveMorph adds a polymorphic relationship absence condition.
	DoesntHaveMorph(relation string, types []any, args ...any) Query
	// OrDoesntHaveMorph adds a polymorphic relationship absence condition with an "or" conjunction.
	OrDoesntHaveMorph(relation string, types []any, args ...any) Query
	// WhereHasMorph adds a polymorphic relationship count / exists condition to the query with
	// where clauses. Callbacks may be MorphRelationCallback for per-type scoping.
	WhereHasMorph(relation string, types []any, args ...any) Query
	// OrWhereHasMorph adds a polymorphic relationship count / exists condition with where clauses
	// and an "or" conjunction.
	OrWhereHasMorph(relation string, types []any, args ...any) Query
	// WhereDoesntHaveMorph adds a polymorphic relationship absence condition with where clauses.
	WhereDoesntHaveMorph(relation string, types []any, args ...any) Query
	// OrWhereDoesntHaveMorph adds a polymorphic relationship absence condition with where clauses
	// and an "or" conjunction.
	OrWhereDoesntHaveMorph(relation string, types []any, args ...any) Query

	// WithAggregate adds a sub-select query to include an aggregate value for a relationship.
	// fn must be one of: count, max, min, sum, avg, exists. column accepts a column name, a
	// table-qualified name, "*", or a raw expression (database/query's Raw/Expression), the
	// string|Expression union Laravel's withAggregate accepts; relation may carry an "as" rename
	// ("Books as book_total").
	WithAggregate(relation string, column any, fn string, args ...any) Query
	// WithCount adds sub-select queries to count the relations. Each entry may be either a
	// string ("Books", or "Books as book_total" for a rename) or a RelationCount struct for
	// scoped/aliased counts.
	WithCount(relations ...any) Query
	// WithMax adds sub-select queries to include the max of the relation's column.
	WithMax(relation string, column any, args ...any) Query
	// WithMin adds sub-select queries to include the min of the relation's column.
	WithMin(relation string, column any, args ...any) Query
	// WithSum adds sub-select queries to include the sum of the relation's column.
	WithSum(relation string, column any, args ...any) Query
	// WithAvg adds sub-select queries to include the average of the relation's column.
	WithAvg(relation string, column any, args ...any) Query
	// WithExists adds sub-select queries to include the existence of related models. The column
	// is Laravel's `exists(...) as alias` (an integer 0/1 on MySQL and SQLite, a native boolean on
	// PostgreSQL), and `CASE WHEN EXISTS (...) THEN 1 ELSE 0 END` on SQL Server, where EXISTS cannot
	// stand in a select list. Laravel casts the alias to bool; a `bool` dest field reads it on every
	// driver, an integer one does not on PostgreSQL.
	WithExists(relations ...string) Query

	// WhereRelation adds a relationship existence condition with a single where on the related
	// rows, mirroring Laravel's whereRelation: WhereRelation("Books", "title", "Go") is
	// WhereHas("Books", func(q) { q.Where("title", "Go") }). A callback may be given in place of
	// the column to constrain the related rows freely.
	WhereRelation(relation string, column any, args ...any) Query
	// OrWhereRelation is WhereRelation with an "or" conjunction.
	OrWhereRelation(relation string, column any, args ...any) Query
	// WhereDoesntHaveRelation adds a relationship absence condition with a single where on the
	// related rows, mirroring Laravel's whereDoesntHaveRelation.
	WhereDoesntHaveRelation(relation string, column any, args ...any) Query
	// OrWhereDoesntHaveRelation is WhereDoesntHaveRelation with an "or" conjunction.
	OrWhereDoesntHaveRelation(relation string, column any, args ...any) Query
	// WithWhereHas constrains the query by the relation and eager loads it with the same
	// constraint, mirroring Laravel's withWhereHas.
	WithWhereHas(relation string, args ...any) Query
	// WhereBelongsTo constrains the query to the rows that belong to the given model (or models),
	// mirroring Laravel's whereBelongsTo: the parent's foreign key is matched against the related
	// models' keys. The relation defaults to the related model's type name.
	WhereBelongsTo(related any, relation ...string) Query
	// OrWhereBelongsTo is WhereBelongsTo with an "or" conjunction.
	OrWhereBelongsTo(related any, relation ...string) Query
	// WhereAttachedTo constrains the query to the rows attached to the given model through a
	// many-to-many relation, mirroring Laravel's whereAttachedTo: the relation defaults to the
	// plural camel form of the related model's type name, and the related model's key list matches
	// inside the relation's existence subquery the way whereKey writes it (integer keys inlined
	// through whereIntegerInRaw, other keys bound).
	WhereAttachedTo(related any, relation ...string) Query
	// OrWhereAttachedTo is WhereAttachedTo with an "or" conjunction. Mirrors Laravel's
	// orWhereAttachedTo.
	OrWhereAttachedTo(related any, relation ...string) Query
	// WhereAttachedToCollection is WhereAttachedTo for a collection of models, mirroring Laravel's
	// whereAttachedTo called with a collection: every model's key joins the list the existence
	// subquery matches against the related table's key.
	WhereAttachedToCollection(related any, relation ...string) Query
}

// RelationCount is an entry accepted by WithCount that pairs a relation name with an optional
// scope callback and result alias. Equivalent to the array-keyed `withCount(['posts as p_count' =>
// fn ...])` idiom in Laravel, expressed as a Go struct:
//
//	q.WithCount(orm.RelationCount{Name: "Books", Alias: "book_total", Callback: func(q) q.Where(...)})
type RelationCount struct {
	// Name is the relation method/field name on the parent model (e.g. "Books").
	Name string
	// Alias overrides the default `<relation>_count` column alias when non-empty.
	Alias string
	// Callback scopes the inner count subquery, mirroring the upstream array-keyed callback shape.
	Callback RelationCallback
}

// RelationKind names a relationship flavour for diagnostic / error-message use only. The
// per-kind structs below (HasOne, HasMany, ...) are the actual user-facing declaration types;
// the RelationKind constants exist purely so error messages can refer to a kind by name.
type RelationKind string

const (
	KindHasOne         RelationKind = "hasOne"
	KindHasMany        RelationKind = "hasMany"
	KindBelongsTo      RelationKind = "belongsTo"
	KindMany2Many      RelationKind = "many2Many"
	KindMorphOne       RelationKind = "morphOne"
	KindMorphMany      RelationKind = "morphMany"
	KindMorphTo        RelationKind = "morphTo"
	KindMorphToMany    RelationKind = "morphToMany"
	KindMorphedByMany  RelationKind = "morphedByMany"
	KindHasOneThrough  RelationKind = "hasOneThrough"
	KindHasManyThrough RelationKind = "hasManyThrough"
)

// Relation is the sealed interface implemented by every per-kind relation declaration struct
// (HasOne, HasMany, BelongsTo, Many2Many, MorphOne, MorphMany, MorphTo, MorphToMany,
// MorphedByMany, HasOneThrough, HasManyThrough). The relation() method is unexported so external
// packages cannot define new kinds — the resolver type-switches on the closed set defined here.
//
// Models declare their relationships in a single map:
//
//	func (User) Relations() map[string]orm.Relation {
//	    return map[string]orm.Relation{
//	        "Books":   orm.HasMany{Related: &Book{}},
//	        "Roles":   orm.Many2Many{Related: &Role{}, Table: "user_roles"},
//	        "Houses":  orm.MorphMany{Related: &House{}, Name: "houseable"},
//	        "Posts":   orm.HasManyThrough{Related: &Post{}, Through: &Account{}},
//	    }
//	}
//
// All relation fields on the model struct must be tagged `gorm:"-"` so GORM doesn't try to
// auto-resolve them from struct tags.
type Relation interface {
	// Kind returns the relation flavour for diagnostics (error messages, logging). The resolver
	// itself dispatches by Go type, not by the Kind value.
	Kind() RelationKind
}

// HasOne declares a one-to-one relation where the related row holds a foreign key referencing
// this model.
//
// Defaults: ForeignKey = singular(parentTable) + "_id"; LocalKey = "id".
type HasOne struct {
	// Related is a sample instance of the related model (e.g. &Profile{}).
	Related any
	// ForeignKey is the column on the related table referencing the parent. Optional.
	ForeignKey string
	// LocalKey is the column on the parent referenced by ForeignKey. Optional, defaults to "id".
	LocalKey string
	// OnQuery is a default scope applied to every query built for this relation (eager loads,
	// existence checks, aggregates, Related). Applied before any caller-supplied callback.
	OnQuery RelationCallback
	// Inverse names a relation on the related model that every loader read of this relation fills
	// with the parent instance — Laravel's ->inverse('user') / ->chaperone() on HasOne / HasMany /
	// MorphOne / MorphMany. Lazy loads (Load / LoadMissing), eager loads (With and the parent
	// collections it reads) and a read that falls back to the WithDefault instance all store the
	// same parent — the Go form of assertSame($parent, $child->inverse) — with no extra query.
	// The name must be declared in the related model's Relations() map; resolving this relation
	// otherwise reports OrmRelationNotFound. Loading without the write-back is a relation declared
	// without this field: the declaration is static metadata, so Laravel's per-call withoutInverse()
	// reaches the same state one call earlier instead.
	Inverse string
	// WithDefault mirrors Laravel's withDefault(): a relation read that finds no related row —
	// or whose parent local key is empty — loads a default related instance in place of nil.
	// Accepted values:
	//
	//   - true: a fresh instance of the related model with its foreign key pre-set to the
	//     parent's local-key value (HasOne::newRelatedInstanceFor).
	//   - map[string]any: the fresh instance, force-filled with the map's attributes; keys name
	//     the related model's columns. An empty map is falsy in Laravel's check, like
	//     withDefault([]), and yields no default.
	//   - func(related, parent any) any: the fresh instance and the actual parent model are
	//     passed to the callback. A PHP-truthy return replaces the instance; a PHP-falsy return
	//     — false, 0, 0.0, "", "0", an empty slice or map, nil — keeps it (Laravel's
	//     `call_user_func($callback, $instance, $parent) ?: $instance`).
	//
	// A related row, when one exists, always wins: the default only stands in where the read
	// found nothing. false / nil mean no default. Defaults apply to Load / LoadMissing / With;
	// the raw Related() relation query keeps returning no rows, exactly as
	// `$parent->relation()->first()` does in Laravel.
	WithDefault any
	// Touches — see BelongsTo.Touches.
	Touches bool
}

func (HasOne) Kind() RelationKind { return KindHasOne }

// HasMany declares a one-to-many relation — the multi-result variant of HasOne.
//
// Defaults: ForeignKey = singular(parentTable) + "_id"; LocalKey = "id".
type HasMany struct {
	Related    any
	ForeignKey string
	LocalKey   string
	OnQuery    RelationCallback
	// Inverse — see HasOne.Inverse.
	Inverse string
	// Touches — see BelongsTo.Touches.
	Touches bool
}

func (HasMany) Kind() RelationKind { return KindHasMany }

// BelongsTo declares the inverse of HasOne / HasMany — this model holds a foreign key
// referencing the related row.
//
// Defaults: ForeignKey = singular(relatedTable) + "_id"; OwnerKey = "id".
type BelongsTo struct {
	Related any
	// ForeignKey is the column on the parent table referencing the related row. Optional.
	ForeignKey string
	// OwnerKey is the column on the related table referenced by ForeignKey. Optional, "id".
	OwnerKey string
	OnQuery  RelationCallback
	// WithDefault — the BelongsTo form of HasOne.WithDefault. The default instance is a plain
	// newInstance of the related model with no attributes set (BelongsTo::newRelatedInstanceFor),
	// and it also stands in when the parent's foreign key is null (getResults' is_null
	// short-circuit).
	WithDefault any
	// Touches, when true, makes saving or deleting the declaring model touch the rows this
	// relation resolves to — their updated-at column is stamped, and the loaded related models'
	// own Touches declarations carry the propagation onward — mirroring an entry in Eloquent's
	// `protected $touches` array and the HasRelationships::touchOwners walk behind it. The stamp
	// itself is skipped while the related model's class is in a withoutTouching ignore list
	// (Model::isIgnoringTouch), while the walk still resolves the related models — reusing the
	// relation the model already carries loaded, or lazy-loading and caching it — and continues
	// through them either way. Declared on the BelongsTo / HasOne / HasMany kinds the corpus's
	// touching family exercises.
	Touches bool
}

func (BelongsTo) Kind() RelationKind { return KindBelongsTo }

// PivotValue is one recorded "pivot column = value" pair — the contract form of one entry of
// Eloquent's BelongsToMany::$pivotValues (BelongsToMany.php:110, `['column' => ..., 'value' =>
// ...]`): the pair BelongsToMany::withPivotValue records at relation-declaration time
// (BelongsToMany.php:481-498, `$this->pivotValues[] = compact('column', 'value'); return
// $this->wherePivot($column, '=', $value);`). A relation DECLARATION carries the recorded list
// (Many2Many.PivotValues / MorphToMany.PivotValues / MorphedByMany.PivotValues) so the write
// shells stamp the pairs onto every pivot INSERT row — baseAttachRecord's values loop,
// InteractsWithPivotTable.php:454-472 — and newPivot merges them as defaults (:629). The column
// is stored as the caller supplied it (Laravel records the raw argument, an Expression included);
// only string columns can become Go attribute keys. The relations object layer aliases it as
// relations.PivotValue.
type PivotValue struct {
	Column any
	Value  any
}

// Many2Many declares a many-to-many relation through a pivot table.
//
// Defaults:
//
//	Table            = alphabetical singular pair (e.g. "post_tag")
//	ForeignPivotKey  = singular(parentTable) + "_id"
//	RelatedPivotKey  = singular(relatedTable) + "_id"
//	ParentKey        = "id"
//	RelatedKey       = "id"
type Many2Many struct {
	Related any
	// Table is the pivot table name. Optional.
	Table string
	// ForeignPivotKey is the pivot column referencing the parent. Optional.
	ForeignPivotKey string
	// RelatedPivotKey is the pivot column referencing the related. Optional.
	RelatedPivotKey string
	// ParentKey is the column on the parent referenced by ForeignPivotKey. Optional, "id".
	ParentKey string
	// RelatedKey is the column on the related referenced by RelatedPivotKey. Optional, "id".
	RelatedKey string
	// PivotField is the name of the struct field on the related model that the eager loader will
	// hydrate with pivot column values (e.g. "Pivot", "UserPivot"). Optional — defaults to
	// "Pivot". The field's Go type drives both the pivot SELECT list (every db-tagged column on
	// the struct) and the hydration target. When the related model has no field by this name,
	// no Pivot hydration happens — the relation still works for joining, just doesn't surface
	// pivot columns. Use a non-default name when one related model serves multiple m2m relations
	// with different pivot schemas (e.g. Role with both UserPivot and GroupPivot fields).
	PivotField string
	// PivotTimestamps enables auto-stamping of the pivot table's timestamp columns on Attach /
	// Sync / Save (and the updated side on UpdateExistingPivot) — the Go form of Laravel's
	// relation-level withTimestamps(): the columns are the PARENT model's created-at / updated-at
	// columns (Laravel's bare withTimestamps() leaves the names null, so BelongsToMany::createdAt
	// resolves `$this->parent->getCreatedAtColumn() ?? Model::CREATED_AT`), "created_at" /
	// "updated_at" when the parent declares none. Most users don't need to set this flag
	// explicitly — see "Detection priority" below.
	//
	// Detection priority for pivot timestamps (highest first):
	//
	//   1. Pivot struct field with `gorm:"autoCreateTime"` / `gorm:"autoUpdateTime"` tag. Works
	//      for any field name; column name is taken from the struct's GORM schema.
	//   2. Pivot struct field named CreatedAt / UpdatedAt of type time.Time (Go/GORM convention).
	//   3. PivotTimestamps: true. Fallback for when no Pivot struct is declared (or its struct
	//      has no timestamp fields), filling each missing side with the parent model's
	//      corresponding column.
	//
	// Customize column names via `gorm:"column:..."` on the Pivot struct field. There is
	// intentionally no relation-level column override in the declaration — the Pivot struct (or,
	// for the PivotTimestamps: true fallback, the parent model) is the source of column metadata.
	// Laravel's withTimestamps(false) disables only the created side (the omitted updated
	// argument still resolves the parent's column); the Go flag's false covers the
	// withTimestamps(false, false) shape — the pivot struct can name the updated column instead.
	PivotTimestamps bool
	// PivotValues is the declaration's recorded "pivot column = value" list — Laravel's
	// BelongsToMany::$pivotValues as declared through withPivotValue (BelongsToMany.php:110/481).
	// The relation object seeds its call-time list from it, so the create / attach writes stamp
	// the pairs onto every pivot INSERT row (baseAttachRecord's values loop) and newPivot merges
	// them as defaults. The read-side wherePivot condition withPivotValue also writes is the
	// OnPivotQuery scope; the declaration splits Laravel's single call into its write side
	// (PivotValues) and read side (OnPivotQuery).
	PivotValues []PivotValue
	OnQuery     RelationCallback
	// OnPivotQuery scopes pivot-table SELECT / UPDATE / DELETE for Sync / Detach /
	// UpdateExistingPivot operations on this relation. Equality conditions added here are NOT
	// auto-injected into Attach INSERT rows — pass them via Attach attrs if needed.
	OnPivotQuery PivotCallback
	// Touches, when true, causes Sync / Attach / Detach / Toggle / UpdateExistingPivot on this
	// relation to bump the parent's `updated_at` after the pivot write completes — Attach after
	// every successful call, empty id list included (oracle v12.55.1, executed, C1); the other
	// shells only when the operation actually changed pivot rows. The bump is skipped while the
	// parent's class
	// ignores touches (Model::isIgnoringTouch) — the gate Eloquent's per-pivot touch rides through
	// Relation::touch. Mirrors fedaco's `touchIfTouching`. Silently no-ops when the parent's
	// schema doesn't carry an updated_at field.
	Touches bool
}

func (Many2Many) Kind() RelationKind { return KindMany2Many }

// MorphOne declares a one-to-one polymorphic relation — the related row holds <Name>_id and
// <Name>_type referencing one of several possible parent kinds.
//
// Defaults: TypeColumn = Name + "_type"; IDColumn = Name + "_id"; LocalKey = "id".
type MorphOne struct {
	Related any
	// Name is the polymorphic name (e.g. "imageable", "taggable"). Required.
	Name string
	// TypeColumn is the polymorphic type column on the related table. Optional.
	TypeColumn string
	// IDColumn is the polymorphic id column on the related table. Optional.
	IDColumn string
	// LocalKey is the column on the parent referenced by IDColumn. Optional, "id".
	LocalKey string
	OnQuery  RelationCallback
	// Inverse — see HasOne.Inverse.
	Inverse string
	// WithDefault — the MorphOne form of HasOne.WithDefault. The default instance is a fresh
	// related instance whose polymorphic id column is pre-set from the parent's local key and
	// whose type column carries the relation's morph value (MorphOne::newRelatedInstanceFor);
	// accepted values and the falsy-closure rule match HasOne.WithDefault.
	WithDefault any
}

func (MorphOne) Kind() RelationKind { return KindMorphOne }

// MorphMany is the multi-result variant of MorphOne.
type MorphMany struct {
	Related    any
	Name       string
	TypeColumn string
	IDColumn   string
	LocalKey   string
	OnQuery    RelationCallback
	// Inverse — see HasOne.Inverse.
	Inverse string
}

func (MorphMany) Kind() RelationKind { return KindMorphMany }

// MorphTo declares the inverse polymorphic side: this model holds <Name>_id + <Name>_type and
// resolves to one of several parent kinds via the morph map registry. There is no Related — the
// concrete type is determined per row from the type column.
//
// Defaults: TypeColumn = Name + "_type"; IDColumn = Name + "_id"; OwnerKey = "id".
type MorphTo struct {
	// Name is the polymorphic name. Required.
	Name string
	// TypeColumn is the polymorphic type column on this table. Optional.
	TypeColumn string
	// IDColumn is the polymorphic id column on this table. Optional.
	IDColumn string
	// OwnerKey is the column on each related table referenced by IDColumn. Optional, "id".
	OwnerKey string
	OnQuery  RelationCallback
	// MorphWith names extra relations to eager load for one polymorphic type — Laravel's
	// MorphTo::morphWith([Class => ['relation', ...]]) (MorphTo.php:311-322), applied by
	// getResultsByType to each resolved type's query. The map key is the type's morph-map alias —
	// the string the type column carries, the 1:1 Go counterpart of Laravel's class-name key —
	// and each value is a nested eager-load list in the With grammar ("user", "comments.author",
	// "user:id,name"). The class's list merges into the relation's own nested loads the way
	// getResultsByType's array_merge places it: being a plain relation list, its entries sit beside
	// the relation-keyed nested loads and never overwrite one — a name the nested loads already
	// carry is absorbed, the nested entry keeping its constraint and select list (oracle,
	// illuminate/database v12.55.1, executed, /tmp/morphverify-probes/collision_dotted.php: the
	// caller's `with(['commentable.author' => ...])` constraint survives the collision and the
	// author read still carries `"name" = ?`, the author null). Applies to the eager and Load
	// paths only, the paths Eloquent's getEager serves (oracle, illuminate/database v12.55.1,
	// executed: /tmp/morphport-probes/morph_with.php — the Post type loads its author in a
	// follow-up `select * from "mpw_authors" where "mpw_authors"."id" in (1)`, the unconfigured
	// Video type receives no such query).
	MorphWith map[string][]string
	// MorphWithCount names relationship counts to load for one polymorphic type — Laravel's
	// MorphTo::morphWithCount (MorphTo.php:337-346), applied per resolved type like MorphWith.
	// Entries are the shapes Query.WithCount accepts (a "relation" or "relation as alias" string,
	// or a RelationCount struct); the map key is the type's morph-map alias. Oracle (same probe):
	// the Post type's rows carry `(select count(*) from "mpw_comments" where "mpw_posts"."id" =
	// "mpw_comments"."post_id") as "comments_count"`, the unconfigured Video type no count column.
	MorphWithCount map[string][]any
	// Constrain holds one query constraint per polymorphic type — Laravel's
	// MorphTo::constrain([Class => fn ($query) => ...]) (MorphTo.php:352-361), applied to the
	// type's query after its eager loads and counts and before the owner-key in-list, the position
	// getResultsByType gives it. The map key is the type's morph-map alias. Oracle (same probe):
	// `where "title" != ? and "mpw_posts"."id" in (1, 2)` — the constraint's where leads the
	// in-list, and the constrained row drops out of the loaded set.
	Constrain map[string]RelationCallback
	// WithTrashed / WithoutTrashed / OnlyTrashed carry the trashed-scope family Laravel's MorphTo
	// buffers and replays per type (MorphTo::withTrashed / withoutTrashed / onlyTrashed,
	// MorphTo.php:366/383/400 — each records a `when(true, callback)` macro and applies it
	// immediately). Every flag is a no-op for a related type that declares no soft deletes — the
	// state Laravel's macro-existence check leaves (`$query->hasMacro('withTrashed') ? $query->
	// withTrashed() : $query`). Precedence when several flags are declared: OnlyTrashed >
	// WithoutTrashed > WithTrashed. Oracle (illuminate/database v12.55.1, executed,
	// /tmp/morphport-probes/trashed.php): Laravel's per-type read carries `"mpt_posts"."deleted_at"
	// is null` by default, WithTrashed drops the predicate (include — `select * from "mpt_posts"
	// where "mpt_posts"."id" in (2, 1)`), OnlyTrashed renders `"mpt_posts"."deleted_at" is not
	// null`, WithoutTrashed keeps the default exclusion. The Go per-type eager read binds the resolved
	// type to its child, so it carries Laravel's default `is null` predicate when no flag is declared;
	// a declared WithTrashed drops it (matching Laravel's include), OnlyTrashed renders `is not null`,
	// and WithoutTrashed keeps the default exclusion — as the object-path read, which carries the
	// related model, honors all three flags directly.
	WithTrashed    bool
	WithoutTrashed bool
	OnlyTrashed    bool
	// WithDefault — the MorphTo form of HasOne.WithDefault. The default instance's class follows
	// the type column: a value in the morph map (or a morph class name) yields that class, while
	// an empty type column yields the parent's own class — MorphTo::newRelatedInstanceFor
	// re-invokes $parent->relation(), and the null-type branch (morphEagerTo) carries the
	// parent's own query as the related model. No attributes are set on the instance.
	WithDefault any
}

func (MorphTo) Kind() RelationKind { return KindMorphTo }

// MorphToMany declares a polymorphic many-to-many — through a pivot that carries
// <Name>_id + <Name>_type plus a related FK.
//
// Defaults:
//
//	Table            = pluralize(Name)  (e.g. "taggables")
//	TypeColumn       = Name + "_type"
//	ForeignPivotKey  = Name + "_id"
//	RelatedPivotKey  = singular(relatedTable) + "_id"
//	ParentKey        = "id"
//	RelatedKey       = "id"
type MorphToMany struct {
	Related         any
	Name            string
	Table           string
	TypeColumn      string
	ForeignPivotKey string
	RelatedPivotKey string
	ParentKey       string
	RelatedKey      string
	// PivotField — see Many2Many.PivotField.
	PivotField string
	// PivotTimestamps — see Many2Many.PivotTimestamps.
	PivotTimestamps bool
	// PivotValues — see Many2Many.PivotValues. The morph declaration records the same list;
	// unlike the base kind, the MorphToMany object never merges it into newPivot (MorphToMany.php
	// :156-172), but every attach path still stamps it (baseAttachRecord).
	PivotValues []PivotValue
	OnQuery     RelationCallback
	// OnPivotQuery — see Many2Many.OnPivotQuery.
	OnPivotQuery PivotCallback
	// Touches — see Many2Many.Touches.
	Touches bool
}

func (MorphToMany) Kind() RelationKind { return KindMorphToMany }

// MorphedByMany is the inverse side of MorphToMany — the morph value pins on the related rather
// than the parent. Field semantics and defaults match MorphToMany.
type MorphedByMany struct {
	Related         any
	Name            string
	Table           string
	TypeColumn      string
	ForeignPivotKey string
	RelatedPivotKey string
	ParentKey       string
	RelatedKey      string
	// PivotField — see Many2Many.PivotField.
	PivotField string
	// PivotTimestamps — see Many2Many.PivotTimestamps.
	PivotTimestamps bool
	// PivotValues — see Many2Many.PivotValues / MorphToMany.PivotValues; the inverse declaration
	// records the same list and stamps it on every attach.
	PivotValues []PivotValue
	OnQuery     RelationCallback
	// OnPivotQuery — see Many2Many.OnPivotQuery.
	OnPivotQuery PivotCallback
	// Touches — see Many2Many.Touches.
	Touches bool
}

func (MorphedByMany) Kind() RelationKind { return KindMorphedByMany }

// HasOneThrough declares a relation reached through an intermediate ("through") table.
//
// Defaults:
//
//	FirstKey       = singular(parentTable) + "_id"
//	SecondKey      = singular(throughTable) + "_id"
//	LocalKey       = "id"
//	SecondLocalKey = "id"
type HasOneThrough struct {
	Related any
	// Through is the intermediate model.
	Through any
	// FirstKey is the FK on the through table pointing at parent. Optional.
	FirstKey string
	// SecondKey is the FK on the related table pointing at through. Optional.
	SecondKey string
	// LocalKey is the PK on the parent referenced by FirstKey. Optional, "id".
	LocalKey string
	// SecondLocalKey is the PK on the through table referenced by SecondKey. Optional, "id".
	SecondLocalKey string
	OnQuery        RelationCallback
	// WithDefault — the HasOneThrough form of HasOne.WithDefault. The default instance is a
	// plain fresh instance of the related model with no attributes set
	// (HasOneThrough::newRelatedInstanceFor); accepted values and the falsy-closure rule match
	// HasOne.WithDefault.
	WithDefault any
}

func (HasOneThrough) Kind() RelationKind { return KindHasOneThrough }

// HasManyThrough is the multi-result variant of HasOneThrough.
type HasManyThrough struct {
	Related        any
	Through        any
	FirstKey       string
	SecondKey      string
	LocalKey       string
	SecondLocalKey string
	OnQuery        RelationCallback
}

func (HasManyThrough) Kind() RelationKind { return KindHasManyThrough }

// ModelWithRelations is implemented by every model that declares relationships. The single map
// returned by Relations() is the only place relations are declared. GORM relation struct tags
// (`foreignKey:`, `references:`, `many2many:`, `polymorphic:`) are forbidden — fields that hold
// related rows must be tagged `gorm:"-"`.
type ModelWithRelations interface {
	Relations() map[string]Relation
}
