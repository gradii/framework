// Package relations hosts the relation builder objects Orm.Related / Query.Related return — the
// Go form of Eloquent's Relation classes as first-class query builders.
//
// The dependency arrow is one-way: database/gorm imports this package (never the reverse).
// Everything gorm-specific the object layer needs — column reads, table resolution, single-row
// reads, the WithDefault builder, the pending attributes — travels through the plain-data
// RelationInfo projection and the Hooks closures gorm binds at wrap time, so this package
// depends only on the contracts.
package relations

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	dbcontract "github.com/gradii/framework/contracts/database/db"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// Kind identifies the relationship flavour of a wrapped relation. The values mirror the internal
// descriptor kinds; Wrap switches on them to build the per-kind leaf object.
type Kind int

const (
	KindHasOne Kind = iota
	KindHasMany
	KindBelongsTo
	KindMany2Many
	KindMorphOne
	KindMorphMany
	KindMorphTo
	KindMorphToMany
	KindHasOneThrough
	KindHasManyThrough
)

// singleResult reports whether the kind's relation read returns one row — Eloquent's
// HasOne / MorphOne / BelongsTo / MorphTo / HasOneThrough getResults shape.
func (k Kind) singleResult() bool {
	switch k {
	case KindHasOne, KindMorphOne, KindBelongsTo, KindMorphTo, KindHasOneThrough:
		return true
	}

	return false
}

// RefKey projects one reference-column pair of the GORM-side descriptor: the primary side (the
// key the row is identified by) and the foreign side referencing it.
type RefKey struct {
	PrimaryColumn string
	ForeignColumn string
}

// RelationInfo is the plain-data projection of the internal relation descriptor the object layer
// reads: the kind, the tables and models involved, the reference pairs, the morph columns and the
// through / pivot specifics. It carries no GORM types.
type RelationInfo struct {
	Kind        Kind
	ParentTable string
	// ParentPrimaryKeyColumn is the parent (child, for the belongs-to kinds) model's own primary
	// key column — Eloquent's getKeyName() — the column BelongsTo / MorphTo
	// getQualifiedParentKeyName qualifies alongside ParentTable. Empty when the parent's schema
	// carries no primary key.
	ParentPrimaryKeyColumn string
	RelatedTable           string
	RelatedModel           any
	// RelatedConnection is the related model's declared connection name ("" when it declares
	// none) — the reference side of Eloquent's getConnectionName() comparison in Is. For MorphTo
	// it names the resolved morph class.
	RelatedConnection string
	References        []RefKey

	// WithDefault carries the WithDefault option of the single-result kinds: nil (no default),
	// bool, map[string]any or func(related, parent any) any. The field is the object's single
	// default slot — Eloquent's SupportsDefaultModels::$withDefault: the declaration seeds it, and
	// the object's call-time WithDefault call replaces it (setWithDefault).
	WithDefault any
	// Inverse carries the relation's declared inverse relation name — the related model's
	// relation that receives the parent instance on every read and on every write-created child
	// (Eloquent's ->inverse('user') / ->chaperone(); the name
	// SupportsInverseRelations::$inverseRelationship holds). "" when the relation was declared
	// without one. The write-back mechanics are engine-side: database/gorm installs the state on
	// the wrapped builder at wrap time. The projection seeds the object's GetInverseRelationship
	// answer and its WithoutChaperone / WithoutInverse clearing.
	Inverse string
	// OneOfMany reports that the relation's own scope turned it into a one-of-many relation.
	OneOfMany bool

	// polymorphic specifics
	MorphTypeColumn string
	MorphIDColumn   string
	MorphValue      string
	MorphOwnerKey   string
	MorphInverse    bool

	// through specifics
	ThroughTable string
	// FirstKey is the through table's foreign key to the far parent — HasOneOrManyThrough's
	// $firstKey, the column getQualifiedFirstKeyName qualifies with ThroughTable and the
	// non-existing-parent limit() partitions its group limit by.
	FirstKey       string
	SecondKey      string
	LocalKey       string
	SecondLocalKey string

	// many-to-many specifics
	PivotTable      string
	PivotParentRef  RefKey
	PivotRelatedRef RefKey

	// PivotField is the declared name of the struct field on the related model that m2m reads
	// hydrate with pivot column values — desc.pivotField (Many2Many.PivotField / the morph
	// variants, default "Pivot"). It is the Go stand-in for Eloquent's pivot accessor
	// (BelongsToMany::$accessor — 'pivot' in the class, BelongsToMany.php:145): GetPivotAccessor
	// reports it and As overrides it for the object's own reads. "" for the kinds that carry no
	// pivot.
	PivotField string

	// --- S2 seam widening (W9): the descriptor fields the Tier-2 write workers read ---

	// Name is the relation's declared name on the parent model — desc.name, the head segment of
	// the relation path the caller resolved: the relation method name Eloquent's
	// BelongsTo::getRelationName reports and the kind / FK error messages carry.
	Name string
	// Touches reports that the many-to-many declaration asked the pivot writes to bump the
	// parent's updated_at after a Sync / Attach / Detach / Toggle / UpdateExistingPivot that
	// changed rows — desc.touches (Many2Many.Touches / MorphToMany.Touches / MorphedByMany.Touches),
	// the declaration-time answer to BelongsToMany::touchIfTouching's touchingParent().
	Touches bool
	// PivotCreatedAtColumn / PivotUpdatedAtColumn name the pivot-table columns the pivot writes
	// stamp with the current time — both on insert, the updated one on update —
	// desc.pivotCreatedAtColumn / desc.pivotUpdatedAtColumn, resolved at declaration from the
	// Pivot struct's automatic timestamp fields, with PivotTimestamps: true filling a missing
	// side from the parent model's timestamp columns (Laravel's bare withTimestamps()). ""
	// leaves the column unstamped (BelongsToMany::$withTimestamps false).
	PivotCreatedAtColumn string
	PivotUpdatedAtColumn string
	// RelatedKeyType is the Go type of the related model's primary key — desc.relatedKeyType —
	// the type the sync family casts its SyncResult ids back to (castKeys). nil for the kinds
	// without a related-side pivot key.
	RelatedKeyType reflect.Type
	// OnPivotQuery is the declaration's pivot-table scope (Many2Many.OnPivotQuery and the morph
	// variants) — desc.onPivotQuery, the callback the pivot-table SELECT / UPDATE / DELETE
	// primitives replay before compiling (applyOnPivotQueryToQuery). nil when none is declared.
	OnPivotQuery contractsorm.PivotCallback
}

// Builder is the structural interface the object layer forwards to: the full query builder —
// *gorm.OrmBuilder satisfies it — plus the relation write shells, which resolve the relation
// descriptor and run each write on a fresh session themselves. It is declared here so this
// package never imports database/gorm.
type Builder interface {
	contractsorm.Query

	// GroupLimit sets the query builder's "group limit" — Query\Builder::groupLimit
	// (Builder.php:3128), reached from the relation objects' limit() through
	// Eloquent\Builder::__call when the parent does not exist: the select compiles as the
	// row_number() over (partition by column) derived table bounded at value rows per group.
	GroupLimit(value int, column string) contractsorm.Query
	// UseLegacyGroupLimit reports whether the bound grammar compiles the group limit through
	// the legacy rewrite (MySqlGrammar::useLegacyGroupLimit — MySQL before 8.0.11), the
	// decision HasOneOrManyThrough::limit (:779-783) and BelongsToMany::limit (:1510-1514)
	// consult before choosing the partition column.
	UseLegacyGroupLimit() bool

	SaveRelation(parent any, relation string, child any) error
	SaveManyRelation(parent any, relation string, children any) error
	SaveRelationWithPivot(parent any, relation string, child any, attrs map[string]any) error
	SaveManyRelationWithPivot(parent any, relation string, children any, attrsPerChild map[any]map[string]any) error
	AssociateRelation(parent any, relation string, owner any) error
	DissociateRelation(parent any, relation string) error
	CreateRelation(parent any, relation string, dest any) error
	CreateManyRelation(parent any, relation string, dests any) error
	FindOrNewRelation(parent any, relation string, id any, dest any) error
	FirstOrNewRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any) error
	FirstOrCreateRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any) error
	UpdateOrCreateRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any) error
	CreateOrFirstRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any) error
	MakeRelation(parent any, relation string, attrs map[string]any, dest any) error
	MakeManyRelation(parent any, relation string, records []map[string]any, dests any) error
	UpsertRelation(parent any, relation string, values []map[string]any, uniqueBy []string, update ...any) (*dbcontract.Result, error)
	AttachRelation(parent any, relation string, ids []any, recorded ...PivotValue) error
	AttachWithPivotRelation(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...PivotValue) error
	DetachRelation(parent any, relation string, ids []any) (int64, error)
	SyncRelation(parent any, relation string, ids []any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	SyncRelationWithPivot(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	SyncRelationWithPivotValues(parent any, relation string, ids []any, pivotValues map[string]any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	SyncWithoutDetachingRelation(parent any, relation string, ids []any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	SyncWithoutDetachingRelationWithPivot(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	ToggleRelation(parent any, relation string, ids []any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	ToggleRelationWithPivot(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	UpdateExistingPivotRelation(parent any, relation string, id any, attrs map[string]any) (int64, error)

	// Quiet and OrFail write families: the shells the relation objects' quiet / forceCreate /
	// OrFail methods forward to — Laravel's HasOneOrMany quiet family
	// (HasOneOrMany.php:345/373/403/455; forceCreateQuietly :427) and InteractsWithPivotTable's
	// OrFail family (InteractsWithPivotTable.php:75/157/170/200/299/365/562). The quiet shells
	// run the same write with the without-events flag set on the fresh persistence session; the
	// OrFail shells wrap the base shell in a transaction on this builder's connection.
	SaveRelationQuietly(parent any, relation string, child any) error
	SaveManyRelationQuietly(parent any, relation string, children any) error
	CreateRelationQuietly(parent any, relation string, dest any) error
	CreateManyRelationQuietly(parent any, relation string, dests any) error
	AttachRelationOrFail(parent any, relation string, ids []any, recorded ...PivotValue) error
	DetachRelationOrFail(parent any, relation string, ids []any) (int64, error)
	SyncRelationOrFail(parent any, relation string, ids []any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	SyncWithoutDetachingRelationOrFail(parent any, relation string, ids []any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	SyncRelationWithPivotValuesOrFail(parent any, relation string, ids []any, pivotValues map[string]any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	ToggleRelationOrFail(parent any, relation string, ids []any, recorded ...PivotValue) (*dbcontract.SyncResult, error)
	UpdateExistingPivotRelationOrFail(parent any, relation string, id any, attrs map[string]any) (int64, error)

	// Pivot execution surface — the frozen widening relations.Pivot runs on (the complete design
	// note, justified call by call against AsPivot, lives in relations/pivot.go):
	//
	// NewPivotQuery builds a fresh builder bound to the pivot's own table on the pivot's
	// connection ("" keeps the current one) — the handle every select / update / delete / insert
	// the pivot runs goes through. ReadPivotParent reads the parent model's pivot metadata (the
	// declared connection name and the created-at / updated-at column names). PivotTouchOwners is
	// AsPivot's touchOwners() call. FirePivotEvent dispatches one model event on the pivot when a
	// handler is registered for the pivot's type.
	NewPivotQuery(table string, connection string) (contractsorm.Query, error)
	ReadPivotParent(parent any) (PivotParentInfo, error)
	PivotTouchOwners(pivot any) error
	FirePivotEvent(name string, pivot any) error

	// --- S2 seam widening (step 1, 2026-11-02) ------------------------------------------------
	//
	// The surface the Tier-2 relation write workers of database/gorm/relation_writes.go port
	// onto as free functions over (Builder, RelationInfo) — LARAVEL_1TO1_ALIGNMENT_REVIEW.md §C.3
	// W1-W9 plus the [V1] measured requirement. Nothing here changes behavior: every member is a
	// thin exported twin of the gorm-internal member the workers call today, so a ported worker
	// assembles the same query state and compiles the same statement byte for byte.
	//
	// Decision table — every gorm-internal member the workers were measured to reach, and where
	// it lives for the port (this package cannot import database/gorm — an import cycle — so an
	// interface-only widening had to be completed by duplicating the pure helpers it cannot reach):
	//
	//   receiver member / field       decision
	//   fresh()                       Builder.NewQuery (contracts Query.Fresh is the model refresh,
	//                                 hence the different name)
	//   relationWriteSession()        Builder.WriteSession
	//   addWhere()                    RowWhere — UpdateRows / DeleteRows / SelectRows append each
	//                                 fragment through the very addWhere call pivotWhere makes
	//   ident()                       Builder.Ident
	//   newBuilder()                  folded into InsertRows (the pivotInsertStatement path)
	//   newRelationQuery()            contracts Query.Related — already on the contract
	//   findMapsCompiled()            folded into SelectRows
	//   withSavepointIfNeeded()       Builder.WithSavepoint (W8)
	//   firstRow() / firstFoundOn()   Builder.FirstFound
	//   ctx, queryGrammar fields      never cross the seam: ColumnValue / SetColumn / ColumnType
	//                                 carry the context, Ident and the row primitives the grammar
	//   inverseChaperone state        Builder.InverseWriteBackName
	//   setRelationField()            Builder.SetRelation (with the inverseParentRef addressing)
	//   parseSchema().PrimaryFields   Builder.PrimaryKey (W10, added by the step-2 port: the
	//                                 findOrNew found-check and the touch WHERE anchor)
	//
	//   package func                  decision
	//   parseSchema()                 ColumnValue / SetColumn / ColumnType / TableOf, plus the
	//                                 duplicated ModelTypeName for the Go type name the error
	//                                 messages carry
	//   tableNameFor()                Builder.TableOf
	//   readParentColumn()            Builder.ColumnValue (W2 — the Builder-level twin of
	//                                 Hooks.ReadColumn, which the object core keeps)
	//   resolveRelation()             Builder.RelationInfoOf — the descriptor lives here since S3 (descriptor.go), the
	//                                 projection crosses
	//   resolveMorphAlias()           Builder.MorphAliasOf — database/orm/morphmap imports this
	//                                 package, so calling it here would be a cycle
	//   isValidParent()               duplicated: IsValidParent (pure reflection)
	//   isUniqueConstraintViolation() Builder.IsUniqueConstraintViolation — dialect classification,
	//                                 kept single-sourced on purpose
	//   dictKey()                     duplicated: DictionaryKey (pure; Eloquent's
	//                                 InteractsWithDictionary::getDictionaryKey)
	//   uniformInsertRecords()        folded into InsertRows
	//   isIgnoringTouch()             Builder.IsIgnoringTouch — database/orm imports this package
	//                                 (through morphmap), so calling it here would be a cycle
	//   ormBuilderOf()                duplicated: BuilderOf (pure unwrap over Builder)
	//   applyOnPivotQueryToQuery()    the scope argument of UpdateRows / DeleteRows / SelectRows,
	//                                 fed from RelationInfo.OnPivotQuery
	//   (*OrmBuilder) casts           re-homed onto the Builder assertion (BuilderOf)
	//
	//   descriptor field              RelationInfo (W9)
	//   name                          Name
	//   touches                       Touches
	//   pivotCreatedAtColumn          PivotCreatedAtColumn
	//   pivotUpdatedAtColumn          PivotUpdatedAtColumn
	//   relatedKeyType                RelatedKeyType
	//   onPivotQuery                  OnPivotQuery

	// ColumnValue reads the value of a DB column off a model — W2, the promoted Hooks.ReadColumn:
	// the schema-resolved read readParentColumn performs, with its "no parent field" error for a
	// column the model's schema lacks.
	ColumnValue(model any, column string) (any, error)
	// SetColumn writes value into the model's column through the parsed schema — W1, the
	// schemaField.Set call mutateAssociate / mutateDissociate / setRelationFKOnChild / applyAttrMap
	// / touchIfTouching make. model must be a non-nil pointer to a struct. The bool reports
	// whether the schema carries the column: false (with no error) lets a caller keep the
	// workers' own absent-column handling — applyAttrMap skips, the FK writers raise
	// OrmRelationUnsupported with their relation-specific message.
	SetColumn(model any, column string, value any) (bool, error)
	// ColumnType reports the Go type of the model's column — W3, the schemaField.FieldType the
	// dissociate writers zero and castKeys targets. false when the schema carries no such column.
	ColumnType(model any, column string) (reflect.Type, bool, error)
	// TableOf resolves a model's declared table name without the connection prefix — tableNameFor,
	// the error-reporting form of Hooks.ModelTable.
	TableOf(model any) (string, error)
	// RelationInfoOf resolves parent's relation by name and hands back its projection — the
	// resolveRelation call every write shell opens with, projected through the same bridge Wrap
	// receives (relationInfoFromDescriptor), including the W9 fields.
	RelationInfoOf(parent any, relation string) (RelationInfo, error)
	// NewQuery builds the same query with no state of its own — fresh(), the session
	// applyAssociate / applyDissociate / createOrFirstRelation persist through.
	NewQuery() contractsorm.Query
	// WriteSession is the fresh session a relation write persists through, keeping the caller's
	// event suppression — relationWriteSession(), what the Save / Create shells spawn.
	WriteSession() contractsorm.Query
	// WithSavepoint runs scope directly outside a transaction and inside a savepoint within one —
	// W8, withSavepointIfNeeded, Eloquent's Builder::withSavepointIfNeeded.
	WithSavepoint(scope func() error) error
	// FirstFound runs this query as a single-row read into dest and reports whether a row was
	// found — firstRow, the flag Query.First discards and the find-then-write family branches on.
	FirstFound(dest any) (bool, error)
	// Ident renders a dotted identifier with the connection's own quoting — ident, the column
	// side of every equality fragment the pivot workers hand pivotWhere.
	Ident(parts ...string) string
	// InsertRows inserts rows into table through the compiled insert — W4, insertPivotRows: the
	// rows are given the union of their columns first (uniformInsertRecords) and the statement
	// runs on the connection pool. No pivot scope is replayed — Laravel's attach writes the
	// pivot row without the wherePivot constraints as well.
	InsertRows(table string, rows []map[string]any) error
	// UpdateRows compiles and runs an update of values on table — W5, the
	// doUpdateExistingPivot / touchIfTouching statement: a fresh builder bound to table, each
	// RowWhere appended in order through addWhere, the pivot scope replayed last (nil skips it),
	// then compileUpdate + execStatement. Reports the rows affected.
	UpdateRows(table string, wheres []RowWhere, scope contractsorm.PivotCallback, values map[string]any) (int64, error)
	// DeleteRows compiles and runs a delete on table — W6, the doDetach statement, assembled the
	// way UpdateRows assembles its update and run through execDelete. Reports the rows affected.
	DeleteRows(table string, wheres []RowWhere, scope contractsorm.PivotCallback) (int64, error)
	// SelectRows reads the given columns of table into column-keyed maps — W7, the
	// allPivotIDs read: the select list set on the fresh builder, each RowWhere
	// appended in order, the pivot scope replayed and the statement read through findMapsCompiled.
	SelectRows(table string, columns []string, wheres []RowWhere, scope contractsorm.PivotCallback) ([]map[string]any, error)
	// IsIgnoringTouch reports whether a model's class ignores touches — database/orm's
	// IsIgnoringTouch, Eloquent's Model::isIgnoringTouch, read per call because a WithoutTouching
	// scope toggles the ignore list only for the span of its callback.
	IsIgnoringTouch(model any) bool
	// MorphAliasOf reports the morph alias a model registers through MorphClass() or the morph
	// map, without the table-name fallback — resolveMorphAlias, the Associate owner-type read.
	// ok is false for an unmapped model; strict morph mode reports the violation through err.
	MorphAliasOf(model any) (string, bool, error)
	// IsUniqueConstraintViolation reports whether err is a unique-constraint violation the way
	// each supported connection classifies one — isUniqueConstraintViolation, Eloquent's
	// Connection::isUniqueConstraintError, the createOrFirst catch.
	IsUniqueConstraintViolation(err error) bool
	// InverseWriteBackName reports the inverse relation name a relation write links the child
	// back to — inverseWriteBackName: the live chaperone state when this builder carries one (a
	// relation object installed it at wrap time and WithoutChaperone / WithoutInverse cleared it
	// to ""), the declared name otherwise. "" means no write-back.
	InverseWriteBackName(declared string) string
	// SetRelation stores value on the model's relation field by name — the setRelationField call
	// applyInverseRelationToCreated makes with the parent pointer (inverseParentRef). In memory
	// only. model and value must both be non-nil pointers; any other shape is the guard's no-op.
	SetRelation(model any, relation string, value any) error
	// PrimaryKey reads the model's primary key through the parsed schema — W10, the
	// parseSchema(model).PrimaryFields[0] read the has-family findOrNew found-check and the
	// touchIfTouching WHERE anchor make: the key column, whether the field holds its Go zero
	// (schemaField.ValueOf's flag — the "did Find populate dest" test) and whether the schema
	// carries a primary key at all (ok false: column "" and isZero true, the "no primary key"
	// branch). err is the schema parse error.
	PrimaryKey(model any) (column string, isZero bool, ok bool, err error)
}

// Hooks carries the GORM-internal services the object layer cannot express through the contracts:
// descriptor-driven column reads, table resolution, the single-row read and the WithDefault
// builder. Every closure is bound, at wrap time, to the builder the object wraps.
type Hooks struct {
	// ReadColumn reads the value of a DB column off the parent model. Builder.ColumnValue is its
	// Builder-level twin (W2) for the free functions the write workers port into.
	ReadColumn func(parent any, column string) (any, error)
	// ReadModelColumn reads the value of a DB column off a model instance the caller compares.
	ReadModelColumn func(model any, column string) (any, error)
	// ModelTable resolves a model instance's table name without the connection prefix ("" when
	// the model declares none).
	ModelTable func(model any) string
	// FirstRow runs the query as a single-row read into dest, reporting whether a row was found.
	FirstRow func(query contractsorm.Query, dest any) (bool, error)
	// BuildDefault materialises a WithDefault value into the default related instance for a
	// parent, reporting whether the value enables one. value is the relation's effective default —
	// the declaration's option as seeded into the projection (RelationInfo.WithDefault), replaced
	// by the object's call-time WithDefault call. nil reports false, the Go reading of Laravel's
	// falsy `if (! $this->withDefault) return;` gate.
	BuildDefault func(value any, parent any) (any, bool, error)
	// PendingAttributes reports the attributes the builder's WithAttributes recorded.
	PendingAttributes func() map[string]any
	// ModelConnection reads a model instance's declared connection name without connecting — the
	// value Eloquent's getConnectionName() reports ("" when the model declares none).
	ModelConnection func(model any) string
	// ModelKey reads a model instance's primary key value — the value Eloquent's getKey() reports —
	// for the one-of-many partial-relation guard.
	ModelKey func(model any) any
	// UpdatedAtColumn resolves the column the relation's Touch stamps: the related model's
	// automatic updated-at column, or "" when the model declares none.
	UpdatedAtColumn func() (string, error)
	// ModelCreatedAtColumn resolves a model's created-at column from its schema — the field the
	// write path stamps on insert. Relation::createdAt reads it off the parent model
	// (`$this->parent->getCreatedAtColumn()`, Relations/Relation.php:373; Eloquent's
	// Model::getCreatedAtColumn reports static::CREATED_AT, default "created_at"). "" when the
	// model declares none.
	ModelCreatedAtColumn func(model any) (string, error)
	// ModelUpdatedAtColumn resolves a model's updated-at column from its schema.
	// Relation::updatedAt reads it off the parent model (Relations/Relation.php:383; Eloquent's
	// Model::getUpdatedAtColumn reports static::UPDATED_AT). "" when the model declares none.
	ModelUpdatedAtColumn func(model any) (string, error)
	// IgnoringTouch reports whether the related model's class ignores touches — Eloquent's
	// Model::isIgnoringTouch() (no automatic updated-at column, or sitting in a WithoutTouching
	// ignore list).
	IgnoringTouch func() bool
}

// relationQuery is the core every relation object embeds: the builder it forwards to (the
// contractsorm.Query INTERFACE, implemented at runtime by *gorm.OrmBuilder — embedding the
// interface promotes the whole builder method set), the plain-data descriptor projection, the
// parent/name pair the writes resolve through and the gorm-bound hooks.
type relationQuery struct {
	Builder

	info   RelationInfo
	parent any
	name   string
	hooks  Hooks
}

// UnwrapBuilder reports the builder this object forwards to — the hook database/gorm's
// ormBuilderOf uses to recover the *OrmBuilder behind a relation object.
func (r *relationQuery) UnwrapBuilder() contractsorm.Query { return r.Builder }

// GetParent gets the parent model instance the relation was resolved from.
func (r *relationQuery) GetParent() any { return r.parent }

// GetRelated gets the related model instance the relation targets.
func (r *relationQuery) GetRelated() any { return r.info.RelatedModel }

// GetQualifiedParentKeyName gets the key the relation's parent side hangs off, qualified with the
// table that owns it, mirroring Relation::getQualifiedParentKeyName and the per-kind overrides: the
// has-family and many-to-many qualify the parent's local (parent) key on the parent table,
// BelongsTo / MorphTo the parent's own primary key on its table — Eloquent's
// $this->parent->getQualifiedKeyName() — and the through kinds the through parent's second local
// key on the through table.
func (r *relationQuery) GetQualifiedParentKeyName() string {
	switch r.info.Kind {
	case KindBelongsTo, KindMorphTo:
		if r.info.ParentTable != "" && r.info.ParentPrimaryKeyColumn != "" {
			return r.info.ParentTable + "." + r.info.ParentPrimaryKeyColumn
		}
	case KindHasOneThrough, KindHasManyThrough:
		if r.info.ThroughTable != "" && r.info.SecondLocalKey != "" {
			return r.info.ThroughTable + "." + r.info.SecondLocalKey
		}
	}

	columns := r.parentKeyColumns()
	if r.info.ParentTable == "" || len(columns) == 0 || columns[0] == "" {
		return ""
	}

	return r.info.ParentTable + "." + columns[0]
}

// CreatedAt gets the name of the parent model's "created at" column — Eloquent's
// Relation::createdAt (vendor, illuminate/database v12.55.1, Relations/Relation.php:373,
// `return $this->parent->getCreatedAtColumn();` — the parent model's column, not the related's;
// oracle v12.55.1, executed, /tmp/lalign-probes/accessors.php: $user->posts() reports the user
// class's "user_created_at" while the posts class declares "post_created_at", and
// $post->user()->createdAt() reports the post class's). The column resolves through the bound
// schema hook — the field the write path stamps on insert — and falls back to Laravel's
// static::CREATED_AT default "created_at" when the parent declares none (a model that declares no
// timestamps still reports the constant, the oracle's model with $timestamps = false does).
func (r *relationQuery) CreatedAt() string {
	if r.hooks.ModelCreatedAtColumn != nil {
		if name, err := r.hooks.ModelCreatedAtColumn(r.parent); err == nil && name != "" {
			return name
		}
	}

	return "created_at"
}

// UpdatedAt is CreatedAt's updated-at sibling — Relation::updatedAt (Relations/Relation.php:383,
// `$this->parent->getUpdatedAtColumn();`; oracle: "user_updated_at" from the parent class's
// UPDATED_AT), with Laravel's static::UPDATED_AT default "updated_at" when the parent declares
// none. The related model's own column — Eloquent's relatedUpdatedAt — is the Touch hook
// (Hooks.UpdatedAtColumn) instead.
func (r *relationQuery) UpdatedAt() string {
	if r.hooks.ModelUpdatedAtColumn != nil {
		if name, err := r.hooks.ModelUpdatedAtColumn(r.parent); err == nil && name != "" {
			return name
		}
	}

	return "updated_at"
}

// GetResults runs the relation read: the single-result kinds read one row — materialising the
// WithDefault instance on a miss when the declaration enables one — and the many-result kinds
// read every match.
//
// Only the single-result kinds short-circuit on an absent parent key: HasOne::getResults and
// BelongsTo::getResults — carried by MorphOne, MorphTo and HasOneThrough — materialise the default
// without the read running once the parent key is null (the Go reading of null includes the
// never-set zero of a value-typed key). A many-result read never consults the key: it executes the
// constrained query as usual — HasMany::getResults' query->get() shape, whose FK constraint yields
// no rows for an empty key (or exactly the zero-FK rows a zero key matches); the strictly-null key
// Laravel short-circuits to an empty collection cannot reach a value-typed Go key. Oracle
// (illuminate/database v12.55.1, executed, /tmp/lalign-getresults/getresults.php): hasMany null
// key queries=0 collection count=0; hasMany zero key queries=1 — the FK=0 row returned; single
// kinds null key queries=0 with the default materialised. The raw Related().First() builder path
// keeps ignoring the default, the way `$parent->relation()->first()` does.
func (r *relationQuery) GetResults(dest any) error {
	if !r.info.Kind.singleResult() {
		return r.Builder.Get(dest)
	}

	if value, known, err := r.readParentKey(); err != nil {
		return err
	} else if known && keyIsNull(value) {
		return r.applyDefault(dest)
	}

	found, err := r.firstRow(dest)
	if err != nil {
		return err
	}
	if found {
		return nil
	}

	return r.applyDefault(dest)
}

// applyDefault materialises the relation's WithDefault instance into dest when the effective
// default enables one — Eloquent's getDefaultFor($parent) over the single slot the declaration
// seeded and a call-time WithDefault call may have replaced. Nothing is assigned when the slot is
// absent or yields no instance.
func (r *relationQuery) applyDefault(dest any) error {
	if r.hooks.BuildDefault == nil {
		return nil
	}

	instance, ok, err := r.hooks.BuildDefault(r.info.WithDefault, r.parent)
	if err != nil {
		return err
	}
	if ok {
		assignRelationDefault(dest, instance)
	}

	return nil
}

// setWithDefault replaces the relation's WithDefault slot — the one value Eloquent's
// SupportsDefaultModels::$withDefault carries, seeded from the declaration at wrap time
// (RelationInfo.WithDefault) and overwritten by the object's call-time WithDefault call
// (HasOne/MorphOne/BelongsTo/HasOneThrough, SupportsDefaultModels.php:32). The default only
// materialises through applyDefault (GetResults), so the raw Related().First() builder path keeps
// ignoring it, call-time or declared.
func (r *relationQuery) setWithDefault(value any) {
	r.info.WithDefault = value
}

// firstRow runs a single-row read through the bound hook; without one — a unit-test-only shape —
// it falls back to the builder's First.
func (r *relationQuery) firstRow(dest any) (bool, error) {
	if r.hooks.FirstRow != nil {
		return r.hooks.FirstRow(r.Builder, dest)
	}
	if err := r.Builder.First(dest); err != nil {
		return false, err
	}

	return true, nil
}

// readParentKey reads the relation's parent-side key off the parent model — Eloquent's
// getParentKey() per kind: the parent local key for the has-family, the parent foreign key for
// BelongsTo, the morph id column for MorphTo, the parent local key for the through kinds. known
// reports whether a column and a read hook were available to read it.
func (r *relationQuery) readParentKey() (value any, known bool, err error) {
	column := r.parentKeyColumn()
	if column == "" || r.hooks.ReadColumn == nil {
		return nil, false, nil
	}

	value, err = r.hooks.ReadColumn(r.parent, column)
	if err != nil {
		return nil, false, err
	}

	return value, true, nil
}

// parentExists is the Go reading of Eloquent's Model::$exists on the relation's parent — the
// flag HasOneOrMany::limit (HasOneOrMany.php:557), HasOneOrManyThrough::limit
// (HasOneOrManyThrough.php:774) and BelongsToMany::limit (BelongsToMany.php:1505) branch on. A
// Go model carries no such flag, so the framework's fresh-key convention stands in
// (database/gorm/fresh.go emptyKeyValue: a model whose key holds no value was never stored): a
// parent whose parent-side key reads a value exists, one whose key is absent — nil, the zero
// value, "" — does not; the latter is the prototype an eager-load constraint runs the relation
// on. The one shape the key cannot tell apart is a never-saved model with an assigned key
// (oracle /tmp/followup-take-grouplimit/take_grouplimit.php H13: exists false, group limit),
// which reads as existing here. A parent whose key cannot be read — no key column projected,
// no read hook, a read error — is treated as existing, so the plain limit the promoted Query
// path always compiled is kept for it.
func (r *relationQuery) parentExists() bool {
	value, known, err := r.readParentKey()
	if err != nil || !known {
		return true
	}

	return !keyIsNull(value)
}

// Is reports whether model is the related model of this relation's parent, mirroring
// ComparesRelatedModels::is: the parent's key against the model's related key — compared with the
// compareKeys rules (an empty key on either side is no match) — then related-table equality and
// connection equality, and finally the one-of-many partial-relation guard when the relation
// installs one (whereKey($model->getKey())->exists() on the constrained query). The keys are
// weighed first: when they fail, the model's table and connection are never consulted — the order
// Eloquent's && chain gives. Kinds whose comparison keys are not expressible through the
// projection report false, and a MorphTo whose morph type names no resolvable related class
// reports false as well — Eloquent's null/empty-type branch compares a null owner key (never a
// match), and an unregistered class cannot build the relation at all.
func (r *relationQuery) Is(model any) bool {
	if modelIsNil(model) {
		return false
	}

	// A MorphTo whose type column names no resolvable related class cannot be compared: a null
	// or empty type takes Eloquent's morphEagerTo branch, whose owner key is null, so is()
	// weighs a null model key and never matches, and an unregistered type cannot build the
	// related instance at all (PHP raises a class-not-found error while resolving the relation).
	// The projection carries no related table for either state — oracle:
	// mt.eager.is.parent-class-model, mt.eager.is.target-model and mt.empty.is.parent-class-model
	// are all false.
	if r.info.Kind == KindMorphTo && r.info.RelatedTable == "" {
		return false
	}

	parentColumn, modelColumn, ok := r.comparisonColumns()
	if !ok {
		return false
	}

	parentKey, err := r.readParentColumnValue(parentColumn)
	if err != nil {
		return false
	}
	modelKey, err := r.readModelColumnValue(model, modelColumn)
	if err != nil {
		return false
	}

	if !compareRelationKeys(parentKey, modelKey) {
		return false
	}

	if r.hooks.ModelTable != nil && r.info.RelatedTable != "" && r.hooks.ModelTable(model) != r.info.RelatedTable {
		return false
	}

	if r.hooks.ModelConnection != nil && r.hooks.ModelConnection(model) != r.info.RelatedConnection {
		return false
	}

	if r.info.OneOfMany && r.kindSupportsPartialRelations() && r.Builder != nil && r.hooks.ModelKey != nil {
		key := r.hooks.ModelKey(model)
		if key == nil {
			return false
		}

		exists, err := r.Builder.WhereKey(key).Exists()
		if err != nil {
			return false
		}

		return exists
	}

	return true
}

// IsNot is the negation of Is, mirroring ComparesRelatedModels::isNot.
func (r *relationQuery) IsNot(model any) bool { return !r.Is(model) }

// RawUpdate updates the related rows without global scopes, mirroring Relation::rawUpdate: the
// constrained update runs on an independent handle so dropping the scopes never reaches the
// relation object's own builder.
func (r *relationQuery) RawUpdate(attrs map[string]any) (*dbcontract.Result, error) {
	return r.cloneForWrite().WithoutGlobalScopes().Update(attrs)
}

// cloneForWrite returns an independent handle of the object's builder when it carries the
// QueryWithClone capability, so a write's scope changes cannot reach the builder the object
// forwards to; a builder without the capability is used as it stands.
func (r *relationQuery) cloneForWrite() contractsorm.Query {
	var builder contractsorm.Query = r.Builder
	if cloner, ok := builder.(contractsorm.QueryWithClone); ok {
		builder = cloner.Clone()
	}

	return builder
}

// Touch stamps the relation's related rows with the current time — Eloquent's Relation::touch():
// the related model's updated-at column (or the column the caller names) is written through the
// relation-constrained query with global scopes dropped and no model events. The signature
// mirrors Query.Touch; the relation semantics live underneath it (the documented shadowing rule).
//
// The BelongsTo / MorphTo kinds check the parent's key first — BelongsTo::touch(), which MorphTo
// overrides identically, skips the statement entirely for an absent key — and, without an
// explicit column, a related model that ignores touches stamps nothing (Relation::touch's
// isIgnoringTouch check; a model declaring no updated-at column likewise). Every no-op reports a
// zero-row result, never an error.
func (r *relationQuery) Touch(column ...string) (*dbcontract.Result, error) {
	name := ""
	if len(column) > 0 {
		name = column[0]
	}

	if r.info.Kind == KindBelongsTo || r.info.Kind == KindMorphTo {
		value, known, err := r.readParentKey()
		if err != nil {
			return nil, err
		}
		if known && keyIsNull(value) {
			return &dbcontract.Result{}, nil
		}
	}

	if name == "" {
		if r.hooks.IgnoringTouch != nil && r.hooks.IgnoringTouch() {
			return &dbcontract.Result{}, nil
		}
		if r.hooks.UpdatedAtColumn == nil {
			return &dbcontract.Result{}, nil
		}

		resolved, err := r.hooks.UpdatedAtColumn()
		if err != nil {
			return nil, err
		}
		if resolved == "" {
			return &dbcontract.Result{}, nil
		}

		name = resolved
	}

	return r.cloneForWrite().WithoutGlobalScopes().Update(map[string]any{name: carbon.Now().StdTime()})
}

// kindSupportsPartialRelations reports whether the kind carries Eloquent's
// SupportsPartialRelations — HasOne, MorphOne and HasOneThrough carry CanBeOneOfMany, so only
// their relations can install the one-of-many read the guard checks against.
func (r *relationQuery) kindSupportsPartialRelations() bool {
	switch r.info.Kind {
	case KindHasOne, KindMorphOne, KindHasOneThrough:
		return true
	}

	return false
}

// readParentColumnValue reads a DB column off the parent model through the bound hooks.
func (r *relationQuery) readParentColumnValue(column string) (any, error) {
	switch {
	case r.hooks.ReadColumn != nil:
		return r.hooks.ReadColumn(r.parent, column)
	case r.hooks.ReadModelColumn != nil:
		return r.hooks.ReadModelColumn(r.parent, column)
	}

	return nil, nil
}

// readModelColumnValue reads a DB column off the model the caller compares through the bound hooks.
func (r *relationQuery) readModelColumnValue(model any, column string) (any, error) {
	switch {
	case r.hooks.ReadModelColumn != nil:
		return r.hooks.ReadModelColumn(model, column)
	case r.hooks.ReadColumn != nil:
		return r.hooks.ReadColumn(model, column)
	}

	return nil, nil
}

// comparisonColumns returns the (parent column, model column) pair Is compares per kind — the
// ComparesRelatedModels getParentKey / getRelatedKeyFrom overrides: the has family weighs the
// parent's local key against the model's foreign key, BelongsTo the parent's foreign key against
// the model's owner key, MorphTo the parent's morph id against the owner key, and the through
// kinds the parent local key against the related model's foreign key.
func (r *relationQuery) comparisonColumns() (string, string, bool) {
	both := func(parentColumn, modelColumn string) (string, string, bool) {
		return parentColumn, modelColumn, parentColumn != "" && modelColumn != ""
	}

	switch r.info.Kind {
	case KindHasOne, KindHasMany, KindMorphOne, KindMorphMany:
		if len(r.info.References) > 0 {
			ref := r.info.References[0]
			return both(ref.PrimaryColumn, ref.ForeignColumn)
		}
	case KindBelongsTo:
		if len(r.info.References) > 0 {
			ref := r.info.References[0]
			return both(ref.ForeignColumn, ref.PrimaryColumn)
		}
	case KindMorphTo:
		ownerKey := r.info.MorphOwnerKey
		if ownerKey == "" {
			ownerKey = "id"
		}

		return both(r.info.MorphIDColumn, ownerKey)
	case KindHasOneThrough, KindHasManyThrough:
		return both(r.info.LocalKey, r.info.SecondKey)
	}

	return "", "", false
}

// parentKeyColumns returns the parent-side key columns of the relation in preference order — the
// first is the column the absent-key reads use and the has-family / many-to-many parent-key
// qualification picks; the parent side Is compares through comparisonColumns.
func (r *relationQuery) parentKeyColumns() []string {
	switch r.info.Kind {
	case KindHasOne, KindHasMany, KindMorphOne, KindMorphMany:
		if len(r.info.References) > 0 {
			return []string{r.info.References[0].PrimaryColumn}
		}
	case KindBelongsTo:
		if len(r.info.References) > 0 {
			return []string{r.info.References[0].ForeignColumn}
		}
	case KindMorphTo:
		return []string{r.info.MorphIDColumn}
	case KindMany2Many, KindMorphToMany:
		return []string{r.info.PivotParentRef.PrimaryColumn}
	case KindHasOneThrough, KindHasManyThrough:
		return []string{r.info.LocalKey}
	}

	return nil
}

// parentKeyColumn names the single parent-side key column GetResults' single-result-kind
// absent-key short-circuit and Touch's BelongsTo / MorphTo guard read: the first of the kind's
// parent key columns, "" when the projection names none.
func (r *relationQuery) parentKeyColumn() string {
	columns := r.parentKeyColumns()
	if len(columns) == 0 {
		return ""
	}

	return columns[0]
}

// pivotQualified qualifies a caller's pivot column with the relation's pivot table; a column
// that already carries a qualifier passes through — Laravel's BelongsToMany::qualifyPivotColumn.
func (r *relationQuery) pivotQualified(column any) string {
	name := fmt.Sprint(column)
	if r.info.PivotTable == "" || strings.Contains(name, ".") {
		return name
	}

	return r.info.PivotTable + "." + name
}

// qualifyRelatedColumn qualifies column with table the way Eloquent's Model::qualifyColumn does:
// a column that already carries a qualifier passes through, and so do a missing table or column —
// the convention pivotQualified and the morph-leaf QualifiedMorphType already document here.
func qualifyRelatedColumn(table, column string) string {
	if table == "" || column == "" || strings.Contains(column, ".") {
		return column
	}

	return table + "." + column
}

// lastColumnSegment returns the last dot-segment of a column name — PHP's
// array_last(explode('.', $column)), the plain-name half of HasOneOrMany::getForeignKeyName.
func lastColumnSegment(column string) string {
	if index := strings.LastIndex(column, "."); index >= 0 {
		return column[index+1:]
	}

	return column
}

// rebuild keeps the object's builder handle in step with a builder method's return value: builder
// methods hand back the builder (usually the same instance, mutated in place), and a foreign
// implementation could hand back a fresh copy.
func (r *relationQuery) rebuild(query contractsorm.Query) {
	if next, ok := query.(Builder); ok {
		r.Builder = next
	}
}

// attributeMap interprets an attribute argument the write shells accept: a map[string]any (or
// map[string]string) becomes the attribute map; nil and other shapes become nil — the shape a
// missing attribute argument produces.
func attributeMap(value any) map[string]any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return v
	case map[string]string:
		attrs := make(map[string]any, len(v))
		for column, attribute := range v {
			attrs[column] = attribute
		}

		return attrs
	}

	return nil
}

// attributeMapFrom interprets the first of a variadic attribute argument list.
func attributeMapFrom(values ...any) map[string]any {
	if len(values) == 0 {
		return nil
	}

	return attributeMap(values[0])
}

// attributeMapAt interprets the attribute argument at the given index of a variadic list.
func attributeMapAt(values []any, index int) map[string]any {
	if index < 0 || index >= len(values) {
		return nil
	}

	return attributeMap(values[index])
}

// upsertRows narrows the query-convention upsert values argument to the row slice the write
// shell accepts.
func upsertRows(values any) []map[string]any {
	rows, _ := values.([]map[string]any)

	return rows
}

// compareRelationKeys compares two key values the way Eloquent's ComparesRelatedModels::compareKeys
// does: an empty key on either side is never a match (PHP empty(): null, 0, "0" and "" are empty);
// when either value is an integer kind both sides compare numerically after PHP's (int) cast —
// the branch that equates 1 with "1", "01" or 1.5 — and everything else compares strictly, PHP's
// ===: same type family, equal value. The matrix was pinned against illuminate/database v12.55.1
// by reflection: compareKeys('abc','abc') true, ('1','1.5') false, (1,'1.5') true, (1,true) true,
// (1.5,'1.5') false.
func compareRelationKeys(parentKey, relatedKey any) bool {
	parentKey = normalizeKeyValue(parentKey)
	relatedKey = normalizeKeyValue(relatedKey)

	if phpEmptyKey(parentKey) || phpEmptyKey(relatedKey) {
		return false
	}

	if isIntegerKey(parentKey) || isIntegerKey(relatedKey) {
		return phpIntKey(parentKey) == phpIntKey(relatedKey)
	}

	return phpStrictKeyEqual(parentKey, relatedKey)
}

// normalizeKeyValue unwraps the pointer layers a key read may carry — a nullable key column sits
// behind a pointer — and renders byte slices as their string form, the shape PHP attributes carry.
// nil pointers read as nil.
func normalizeKeyValue(value any) any {
	value = derefValue(value)
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}

	return value
}

// modelIsNil reports whether the model a caller compares stands for PHP's null: an untyped nil or
// a nil pointer — ComparesRelatedModels::is's `! is_null($model)` guard.
func modelIsNil(model any) bool {
	if model == nil {
		return true
	}

	value := reflect.ValueOf(model)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return value.IsNil()
	}

	return false
}

// keyIsNull reports whether a parent key read stands for PHP's null: nil, a nil pointer, or —
// because a value-typed Go key cannot hold NULL — its zero value, the never-set reading the ORM's
// fresh-key convention uses (database/gorm's BelongsTo touch and read short-circuits treat the
// same zero as null). GetResults consumes it for the single-result kinds' absent-key short-circuit
// only — the many-result kinds never consult the key; Touch's BelongsTo / MorphTo guard shares it.
func keyIsNull(value any) bool {
	if value == nil {
		return true
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return reflected.IsNil()
	case reflect.String:
		return reflected.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint() == 0
	}

	return false
}

// phpEmptyKey ports PHP's empty() over the scalar shapes a key read can carry: null, false, 0,
// "0" and "" are empty — 0 vs 0 is therefore no key match.
func phpEmptyKey(value any) bool {
	if value == nil {
		return true
	}

	if boolean, ok := value.(bool); ok {
		return !boolean
	}
	if text, ok := value.(string); ok {
		return text == "" || text == "0"
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return reflected.Float() == 0
	case reflect.Slice, reflect.Map, reflect.Array:
		return reflected.Len() == 0
	}

	return false
}

// isIntegerKey reports whether the value is one of Go's integer kinds — PHP's is_int, whose
// presence on either side switches compareKeys to the numeric branch.
func isIntegerKey(value any) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return true
	}

	return false
}

// phpIntKey ports PHP's (int) cast over the shapes a key read can carry.
func phpIntKey(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		return int64(typed)
	case uintptr:
		return int64(typed)
	case float32:
		return phpIntFromFloat(float64(typed))
	case float64:
		return phpIntFromFloat(typed)
	case bool:
		if typed {
			return 1
		}

		return 0
	case string:
		return phpIntCastString(typed)
	}

	return 0
}

// phpIntCastString ports the int branch of PHP's (int) cast for a string: leading whitespace is
// skipped, the leading numeric run — sign, digits, fraction, exponent — is parsed and truncated
// toward zero, and a string without digits casts to 0 ('1.5' -> 1, ' 1' -> 1, '1abc' -> 1,
// 'abc' -> 0). A pure integer run casts through strconv.ParseInt — PHP's strtoll — so it stays
// exact across the full 64-bit range and saturates at the int64 bounds on overflow ((int)
// '9007199254740993' keeps every digit; (int) '9223372036854775807999' saturates to PHP_INT_MAX);
// routing it through float64 would round keys past 2^53, where PHP does not. Runs carrying a
// fraction or an exponent go through the float branch, PHP's strtod, and its (int) wrap below.
func phpIntCastString(value string) int64 {
	text := strings.TrimLeft(value, " \t\n\r\v\f")
	end := 0
	digits := 0
	integral := true

	if end < len(text) && (text[end] == '+' || text[end] == '-') {
		end++
	}
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
		digits++
	}
	if end < len(text) && text[end] == '.' {
		integral = false
		end++
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
			digits++
		}
	}
	if digits == 0 {
		return 0
	}
	if end < len(text) && (text[end] == 'e' || text[end] == 'E') {
		exponentStart := end
		end++
		if end < len(text) && (text[end] == '+' || text[end] == '-') {
			end++
		}
		exponentDigits := 0
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
			exponentDigits++
		}
		if exponentDigits == 0 {
			end = exponentStart
		} else {
			integral = false
		}
	}

	run := text[:end]
	if integral {
		number, err := strconv.ParseInt(run, 10, 64)
		if err == nil {
			return number
		}
		if numberError, ok := err.(*strconv.NumError); ok && numberError.Err == strconv.ErrRange {
			if run[0] == '-' {
				return math.MinInt64
			}

			return math.MaxInt64
		}

		return 0
	}

	number, err := strconv.ParseFloat(run, 64)
	if err != nil {
		return 0
	}

	return phpIntFromFloat(number)
}

// phpIntFromFloat ports the float branch of PHP's (int) cast — the zend_dval_to_lval wrap the
// oracle pins: the value truncates toward zero, NaN and the infinities cast to 0 ((int) INF -> 0),
// and a magnitude beyond the int64 range wraps modulo 2^64, the low bits re-read as a signed
// integer ((int) 1e20 -> 7766279631452241920).
func phpIntFromFloat(number float64) int64 {
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0
	}
	if number >= 9223372036854775808.0 || number < -9223372036854775808.0 {
		modulus := math.Mod(math.Trunc(number), 18446744073709551616.0)
		if modulus < 0 {
			modulus += 18446744073709551616.0
		}

		return int64(uint64(modulus))
	}

	return int64(number)
}

// phpStrictKeyEqual ports the strict branch of PHP's === over the non-integer shapes compareKeys
// can see: strings compare with strings, floats with floats, bools with bools — never across
// families ('1.5' !== 1.5).
func phpStrictKeyEqual(left, right any) bool {
	switch typed := left.(type) {
	case string:
		other, ok := right.(string)

		return ok && typed == other
	case bool:
		other, ok := right.(bool)

		return ok && typed == other
	case float32, float64:
		leftNumber, ok := keyFloat(typed)
		if !ok {
			return false
		}
		rightNumber, ok := keyFloat(right)

		return ok && leftNumber == rightNumber
	}

	return false
}

// keyFloat extracts a float value from a Go float.
func keyFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	}

	return 0, false
}

// derefValue unwraps the pointer layers a key read may carry (a nullable key column sits behind a
// pointer); nil pointers read as nil.
func derefValue(value any) any {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return nil
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return nil
	}

	return reflected.Interface()
}

// assignRelationDefault installs the WithDefault instance a single-result read materialises on a
// miss into dest — a pointer to the model, or to a model pointer.
func assignRelationDefault(dest any, instance any) {
	destValue := reflect.ValueOf(dest)
	if !destValue.IsValid() || destValue.Kind() != reflect.Pointer || destValue.IsNil() {
		return
	}

	target := destValue.Elem()
	instanceValue := reflect.ValueOf(instance)
	if !instanceValue.IsValid() {
		return
	}

	if target.Kind() == reflect.Pointer {
		if instanceValue.Type().AssignableTo(target.Type()) {
			target.Set(instanceValue)
		}

		return
	}

	if instanceValue.Kind() == reflect.Pointer {
		if instanceValue.IsNil() {
			return
		}
		instanceValue = instanceValue.Elem()
	}
	if instanceValue.Type().AssignableTo(target.Type()) {
		target.Set(instanceValue)
	}
}

// Wrap builds the relation object for the given descriptor projection: the per-kind leaf for the
// known kinds — the morph kinds build their dedicated leaves (morphOneRelationQuery /
// morphManyRelationQuery / morphToRelationQuery / morphToManyRelationQuery, mirroring the Laravel
// class tree) — the bare core otherwise.
func Wrap(builder Builder, parent any, name string, info RelationInfo, hooks Hooks) contractsorm.RelationQuery {
	core := &relationQuery{Builder: builder, info: info, parent: parent, name: name, hooks: hooks}

	switch info.Kind {
	case KindHasOne:
		return &hasOneRelationQuery{hasOneOrManyRelation{core}}
	case KindMorphOne:
		return &morphOneRelationQuery{morphOneOrManyRelation{hasOneOrManyRelation{core}}}
	case KindHasMany:
		return &hasManyRelationQuery{hasOneOrManyRelation{core}}
	case KindMorphMany:
		return &morphManyRelationQuery{morphOneOrManyRelation{hasOneOrManyRelation{core}}}
	case KindBelongsTo:
		return &belongsToRelationQuery{core}
	case KindMorphTo:
		return &morphToRelationQuery{belongsToRelationQuery{core}}
	case KindMany2Many:
		return &belongsToManyRelationQuery{relationQuery: core}
	case KindMorphToMany:
		return &morphToManyRelationQuery{belongsToManyRelationQuery{relationQuery: core}}
	case KindHasOneThrough:
		return &hasOneThroughRelationQuery{hasOneOrManyThroughRelation{core}}
	case KindHasManyThrough:
		return &hasManyThroughRelationQuery{hasOneOrManyThroughRelation{core}}
	default:
		return core
	}
}

// WrapBase wraps a builder in the bare relation object — the shape the gorm error paths use: a
// relation that could not be resolved still returns a RelationQuery handle whose promoted
// terminals (Get / First / ...) report the guarded builder's error.
func WrapBase(builder Builder, parent any, name string, hooks Hooks) contractsorm.RelationQuery {
	return &relationQuery{Builder: builder, parent: parent, name: name, hooks: hooks}
}

// Compile assertions: the core and every leaf satisfy their contracts.
var (
	_ contractsorm.RelationQuery          = (*relationQuery)(nil)
	_ contractsorm.HasOneRelation         = (*hasOneRelationQuery)(nil)
	_ contractsorm.HasManyRelation        = (*hasManyRelationQuery)(nil)
	_ contractsorm.BelongsToRelation      = (*belongsToRelationQuery)(nil)
	_ contractsorm.BelongsToManyRelation  = (*belongsToManyRelationQuery)(nil)
	_ contractsorm.HasOneThroughRelation  = (*hasOneThroughRelationQuery)(nil)
	_ contractsorm.HasManyThroughRelation = (*hasManyThroughRelationQuery)(nil)
	_ contractsorm.HasOneRelation         = (*morphOneRelationQuery)(nil)
	_ contractsorm.HasManyRelation        = (*morphManyRelationQuery)(nil)
	_ contractsorm.BelongsToRelation      = (*morphToRelationQuery)(nil)
	_ contractsorm.BelongsToManyRelation  = (*morphToManyRelationQuery)(nil)
)
