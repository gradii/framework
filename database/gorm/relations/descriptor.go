package relations

import (
	"cmp"
	"fmt"
	"reflect"
	"strings"
	"time"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/str"
)

// This file is the relation descriptor — the resolver's normalised view of a declared
// relationship and the per-kind constructors that build it from the parent model's Relations()
// map — moved out of database/gorm by the S3 reduction slice
// (database/tmp/LARAVEL_1TO1_ALIGNMENT_REVIEW.md §C.4). The code is the gorm original verbatim;
// only the model-metadata reads this package cannot perform itself travel through
// DescriptorSeam: database/gorm owns the schema parser (parseSchema), the table resolution
// (tableNameFor), the pivot-field reflection (pivotFieldStructType) and the GORM relation-tag
// check (hasRelationTag — it decides column-vs-embedded exactly the way the schema parser does),
// and database/orm/morphmap imports this package, so the morph map cannot be imported here.
// database/gorm installs the seam once at init. The relation kinds reuse this package's Kind — the
// same iota order the descriptor always had — so no kind mapping step remains between the two.

// ModelSchema is the slice of a parsed model schema the descriptor resolver reads — the view
// database/gorm hands back for its modelSchema through DescriptorSeam.ParseModel.
type ModelSchema interface {
	// Table is the table the model maps to, without the connection prefix.
	Table() string
	// TypeName is the Go type name of the model, used in diagnostics.
	TypeName() string
	// ColumnType reports the Go type of the column and whether the schema carries it.
	ColumnType(column string) (reflect.Type, bool)
	// Columns lists the model's columns in declaration order.
	Columns() []SchemaColumn
	// ColumnByName looks a column up by its Go field name.
	ColumnByName(name string) (SchemaColumn, bool)
}

// SchemaColumn is one column of a ModelSchema: its database name, its Go type and whether it is
// stamped automatically on insert (AutoCreateTime) and on update as well (AutoUpdateTime).
type SchemaColumn struct {
	DBName         string
	FieldType      reflect.Type
	AutoCreateTime bool
	AutoUpdateTime bool
}

// DescriptorSeam is the gorm-side half of the descriptor resolver: the model-metadata readers
// this package cannot reach itself. Every member is a thin exported twin of the gorm-internal
// helper it names, so a descriptor built here carries the values the gorm original built.
type DescriptorSeam struct {
	// ParseModel parses a model's schema — parseSchema, with its OrmQueryInvalidModel /
	// OrmQueryTableNotSet errors.
	ParseModel func(model any) (ModelSchema, error)
	// TableOf resolves a model's declared table name without the connection prefix —
	// tableNameFor.
	TableOf func(model any) (string, error)
	// PivotFieldStructType reflects the related model for the struct field the pivot hydrates
	// into — pivotFieldStructType; ok is false when there is no such struct field.
	PivotFieldStructType func(relatedModel any, pivotFieldName string) (reflect.Type, bool)
	// HasRelationTag reports whether the model's field of the given name declares a GORM
	// relation tag — hasRelationTag.
	HasRelationTag func(model any, name string) bool
	// MorphValue resolves a model's morph alias through MorphClass() and the morph map, raising
	// under strict morph mode — database/orm/morphmap.MorphValue.
	MorphValue func(model any) (string, bool, error)
}

// descriptorSeam is the installed seam; database/gorm sets it at init through UseDescriptorSeam.
var descriptorSeam DescriptorSeam

// UseDescriptorSeam installs the gorm-side readers the descriptor resolver runs on.
func UseDescriptorSeam(seam DescriptorSeam) {
	descriptorSeam = seam
}

// descriptorSeamInstalled reports whether every reader of the seam is in place. The seam is a
// struct of function fields whose zero value is all nil, so a resolver call before database/gorm's
// init() (a relations-only caller, or a test binary that never links database/gorm) would call a
// nil function and panic. The exported resolver entry points check this first and return
// errors.OrmRelationDescriptorSeamMissing instead; once the seam is installed the check is a
// no-op and every path below runs exactly as before. The guard has no Laravel counterpart — it
// protects the Go package split, not an Eloquent behavior.
func descriptorSeamInstalled() bool {
	return descriptorSeam.ParseModel != nil &&
		descriptorSeam.TableOf != nil &&
		descriptorSeam.PivotFieldStructType != nil &&
		descriptorSeam.HasRelationTag != nil &&
		descriptorSeam.MorphValue != nil
}

// ReferenceKey describes one column-pair from a GORM Reference, with each side already qualified
// by table name. PrimaryKey/ForeignKey naming follows GORM's convention.
type ReferenceKey struct {
	PrimaryTable  string
	PrimaryColumn string
	ForeignTable  string
	ForeignColumn string
}

// Descriptor is the resolver's normalised view of a relationship. It lets the
// queries-relationships builder construct correlated subqueries without ever calling back into
// GORM's relation internals.
type Descriptor struct {
	Name         string
	Kind         Kind
	ParentTable  string
	RelatedTable string
	RelatedModel any
	References   []ReferenceKey

	// many-to-many specifics
	PivotTable      string
	PivotParentRef  ReferenceKey
	PivotRelatedRef ReferenceKey
	// PivotField is the name of the field on the related model that the eager loader hydrates
	// with pivot column values. Sourced from Many2Many.PivotField (or the morph variants);
	// defaults to "Pivot". When the related model has no field by this name, no Pivot hydration
	// happens. The field's Go type drives both the SELECT list and hydration target.
	PivotField string
	// PivotCreatedAtColumn is the pivot-table column to auto-stamp with the current time on
	// INSERT. Empty string means "don't auto-stamp on INSERT". Resolved by the descriptor builder
	// from (priority): Pivot struct's autoCreateTime field → Pivot struct's CreatedAt field →
	// PivotTimestamps: true fallback (defaults to "created_at").
	PivotCreatedAtColumn string
	// PivotUpdatedAtColumn is the pivot-table column to auto-stamp with the current time on
	// INSERT and UPDATE. Empty string means "don't auto-stamp on INSERT/UPDATE". Same resolution
	// rules as pivotCreatedAtColumn but using autoUpdateTime / UpdatedAt.
	PivotUpdatedAtColumn string

	// RelatedKeyType is the Go type of the related model's PK field — used by castKey to normalise
	// SyncResult ids back to the related model's native key type, irrespective of what the caller
	// passed (string, int, uint, etc.) and what GORM scanned from the pivot table (often int64).
	// nil for kinds that don't have a related-side pivot key (HasOne family, BelongsTo, etc.).
	RelatedKeyType reflect.Type

	// polymorphic specifics
	MorphTypeColumn string // e.g. "imageable_type" — on parent table for MorphTo, on pivot for MorphToMany
	MorphIDColumn   string // e.g. "imageable_id"  — on parent table for MorphTo, on pivot for MorphToMany
	MorphValue      string // e.g. "post"          — used in WHERE *_type = ? filters
	MorphOwnerKey   string // PK on each related model for MorphTo (defaults to "id")
	MorphInverse    bool   // true for MorphedByMany — flips morph value source from parent to related

	// MorphTo per-type eager config and trashed-scope family — contractsorm.MorphTo's MorphWith /
	// MorphWithCount / Constrain / WithTrashed / WithoutTrashed / OnlyTrashed in declaration form.
	// loadMorphTo consumes them inside each bucket (the per-type query MorphTo::getResultsByType
	// builds) and newMorphToQuery applies the trashed family to the object-path read.
	MorphWith           map[string][]string
	MorphWithCount      map[string][]any
	MorphConstrain      map[string]contractsorm.RelationCallback
	MorphWithTrashed    bool
	MorphWithoutTrashed bool
	MorphOnlyTrashed    bool

	// through specifics
	ThroughTable   string
	FirstKey       string // FK on through pointing at parent
	SecondKey      string // FK on related pointing at through
	LocalKey       string // PK on parent
	SecondLocalKey string // PK on through

	// OnQuery is the per-relation default scope from Relation.OnQuery. Applied by every code
	// path that builds an inner query for this relation (eager loaders, existence builders,
	// Related), *before* any caller-supplied callback.
	OnQuery contractsorm.RelationCallback

	// WithDefault carries the WithDefault option of the BelongsTo / HasOne / MorphOne / MorphTo /
	// HasOneThrough descriptors:
	// nil (no default), bool, map[string]any, or func(related, parent any) any. The relation
	// default helpers in relation_defaults.go materialise it wherever a read finds no row.
	WithDefault any

	// Inverse is the related model's relation name that receives the parent instance after this
	// relation is read — HasOne / HasMany / MorphOne / MorphMany's Inverse field, i.e. Laravel's
	// ->Inverse('user') / ->chaperone(). The loaders write the parent back onto every related
	// model they match (and onto a WithDefault instance) under this name, and the relation read
	// path (Related) applies the same write-back to every model its reads return — the after-query
	// callback chaperone() registers, which runs on every execution of the relation query. An
	// empty string means no write-back. Validated at resolution against the related model's
	// Relations() map, like Eloquent's isRelation() check inside chaperone().
	Inverse string

	// OnPivotQuery is the per-relation default scope for pivot-table SELECT / UPDATE / DELETE,
	// from Many2Many.OnPivotQuery / MorphToMany.OnPivotQuery / MorphedByMany.OnPivotQuery.
	// Applied by existingPivotIDs, allPivotIDs, DetachRelation, UpdateExistingPivotRelation.
	OnPivotQuery contractsorm.PivotCallback

	// Touches, when true, makes Sync / Attach / Detach / Toggle / UpdateExistingPivot bump the
	// parent's updated_at after the pivot write succeeds (and only when pivot rows actually
	// changed). Source: Many2Many.Touches / MorphToMany.Touches / MorphedByMany.Touches.
	Touches bool

	// next link for Nested resolution (e.g. "Books.Author")
	Nested *Descriptor
}

// ResolveRelation walks a (possibly dotted) relation path and returns a chain of descriptors
// rooted at the given parent model. The returned descriptor's nested field points at the next
// hop, so callers can recurse to build subqueries for "User.Books.Author"-style queries.
//
// All relations are declared via the parent's Relations() method (ModelWithRelations). GORM
// relation tags (`foreignKey`, `references`, `many2many`, `polymorphic`) are forbidden — if
// detected the resolver returns OrmRelationTagForbidden pointing the user at Relations().
func ResolveRelation(parent any, relation string) (*Descriptor, error) {
	if relation == "" {
		return nil, errors.OrmQueryEmptyRelation
	}

	if !descriptorSeamInstalled() {
		return nil, errors.OrmRelationDescriptorSeamMissing
	}

	head, tail, _ := strings.Cut(relation, ".")

	parentSchema, err := descriptorSeam.ParseModel(parent)
	if err != nil {
		return nil, err
	}
	parentTable := parentSchema.Table()

	// Detect forbidden GORM relation tags. GORM used to populate a Relationships entry for the
	// requested name, which is how the conflict was noticed; the tag itself is inspected now, so the
	// check no longer depends on GORM parsing the model.
	if descriptorSeam.HasRelationTag(parent, head) {
		return nil, errors.OrmRelationTagForbidden.Args(head, parentSchema.TypeName())
	}

	desc, err := descriptorFromRelations(parent, parentTable, head)
	if err != nil {
		return nil, err
	}
	desc.Name = head

	if tail != "" {
		// Recurse using the *related* model as the new parent.
		nestedParent := desc.RelatedModel
		if nestedParent == nil {
			return nil, errors.OrmRelationUnsupported.Args(head, parentSchema.TypeName(), "no related model")
		}
		nested, err := ResolveRelation(nestedParent, tail)
		if err != nil {
			return nil, err
		}
		desc.Nested = nested
	}
	return desc, nil
}

// descriptorFromRelations resolves a relation declared via the parent's Relations() method.
// Handles all 11 kinds. Returns OrmRelationNotFound when the parent doesn't implement the
// interface or the relation name isn't in its map.
//
// Dispatch is by Go type, not by a discriminator field — each per-kind struct in
// contracts/database/orm satisfies the sealed Relation interface and lands in its own case here.
// The kinds form four families (mirroring fedaco's class hierarchy in
// /workbench/fedaco/libs/fedaco/src/fedaco/relations) which share resolver logic:
//
//   - HasOneOrMany family (HasOne, HasMany, MorphOne, MorphMany): FK lives on the related side.
//   - BelongsTo family (BelongsTo, MorphTo): FK lives on the parent.
//   - BelongsToMany family (Many2Many, MorphToMany, MorphedByMany): joined via a pivot table.
//   - HasManyThrough family (HasOneThrough, HasManyThrough): joined via an intermediate table.
//
// Family-level shared behaviour (Save/Associate/Attach/etc.) lives downstream in relation_writes.go
// and is dispatched by the internal Kind groups; this function's job is to map the
// user-facing struct to the descriptor those downstream paths consume.
//
// Accepts Relations() declared with either a value receiver (`func (Foo) Relations()`) or a
// pointer receiver (`func (*Foo) Relations()`). Models often mix the two — e.g. value receivers
// for pure-metadata methods and pointer receivers for GORM lifecycle hooks — so the framework
// looks up the interface on whichever form is addressable.
func descriptorFromRelations(parent any, parentTable, name string) (*Descriptor, error) {
	relations, ok := TryGetRelations(parent)
	if !ok {
		return nil, errors.OrmRelationNotFound.Args(name, reflect.TypeOf(parent).String())
	}
	rel, ok := relations[name]
	if !ok {
		return nil, errors.OrmRelationNotFound.Args(name, reflect.TypeOf(parent).String())
	}

	var (
		desc        *Descriptor
		err         error
		onQuery     contractsorm.RelationCallback
		withDefault any
		inverse     string
	)
	switch r := rel.(type) {
	case contractsorm.HasOne:
		desc, err = descriptorFromHasOneOrMany(parent, parentTable, name, r.Related, r.ForeignKey, r.LocalKey, KindHasOne)
		onQuery = r.OnQuery
		inverse = r.Inverse
		withDefault = r.WithDefault
	case contractsorm.HasMany:
		desc, err = descriptorFromHasOneOrMany(parent, parentTable, name, r.Related, r.ForeignKey, r.LocalKey, KindHasMany)
		onQuery = r.OnQuery
		inverse = r.Inverse
	case contractsorm.BelongsTo:
		desc, err = descriptorFromBelongsTo(parent, parentTable, name, r)
		onQuery = r.OnQuery
		withDefault = r.WithDefault
	case contractsorm.Many2Many:
		desc, err = descriptorFromMany2Many(parent, parentTable, name, r)
		onQuery = r.OnQuery
		if desc != nil {
			desc.OnPivotQuery = r.OnPivotQuery
			desc.Touches = r.Touches
		}
	case contractsorm.MorphOne:
		desc, err = DescriptorFromMorphOneOrMany(parent, parentTable, name, r.Related, r.Name, r.TypeColumn, r.IDColumn, r.LocalKey, KindMorphOne)
		onQuery = r.OnQuery
		inverse = r.Inverse
		withDefault = r.WithDefault
	case contractsorm.MorphMany:
		desc, err = DescriptorFromMorphOneOrMany(parent, parentTable, name, r.Related, r.Name, r.TypeColumn, r.IDColumn, r.LocalKey, KindMorphMany)
		onQuery = r.OnQuery
		inverse = r.Inverse
	case contractsorm.MorphTo:
		desc, err = descriptorFromMorphTo(parent, parentTable, name, r)
		onQuery = r.OnQuery
		withDefault = r.WithDefault
	case contractsorm.MorphToMany:
		desc, err = descriptorFromMorphToMany(parent, parentTable, name, r, false)
		onQuery = r.OnQuery
		if desc != nil {
			desc.OnPivotQuery = r.OnPivotQuery
			desc.Touches = r.Touches
		}
	case contractsorm.MorphedByMany:
		desc, err = descriptorFromMorphedByMany(parent, parentTable, name, r)
		onQuery = r.OnQuery
		if desc != nil {
			desc.OnPivotQuery = r.OnPivotQuery
			desc.Touches = r.Touches
		}
	case contractsorm.HasOneThrough:
		desc, err = descriptorFromThrough(parent, parentTable, name, r.Related, r.Through, r.FirstKey, r.SecondKey, r.LocalKey, r.SecondLocalKey, KindHasOneThrough)
		onQuery = r.OnQuery
		withDefault = r.WithDefault
	case contractsorm.HasManyThrough:
		desc, err = descriptorFromThrough(parent, parentTable, name, r.Related, r.Through, r.FirstKey, r.SecondKey, r.LocalKey, r.SecondLocalKey, KindHasManyThrough)
		onQuery = r.OnQuery
	default:
		return nil, errors.OrmMorphRelationKindUnknown.Args(name, reflect.TypeOf(parent).String(), reflect.TypeOf(rel).String())
	}
	if err != nil {
		return nil, err
	}
	// Carry the per-relation default-scope hook into the descriptor; every consumer (eager
	// loader, existence builder, Related) applies it before any caller callback.
	desc.OnQuery = onQuery
	// Carry the per-relation default-model option (BelongsTo / HasOne / MorphOne / MorphTo / HasOneThrough) into the
	// descriptor; relation reads that find no row materialise it the way Laravel's getResults /
	// eager initRelation would. A value outside the accepted shapes is a configuration error the
	// relation reports from resolution on.
	if !ValidWithDefault(withDefault) {
		return nil, errors.OrmRelationWithDefaultInvalid.Args(fmt.Sprintf("%T", withDefault), name)
	}
	desc.WithDefault = withDefault
	// Carry the chaperone declaration (HasOne / HasMany / MorphOne / MorphMany's Inverse field)
	// into the descriptor and validate it against the related model's Relations() map now — the
	// point where Eloquent's chaperone() runs Model::isRelation() and throws
	// RelationNotFoundException, so a declaration naming no relation fails resolution instead of
	// every later read.
	desc.Inverse = inverse
	if desc.Inverse != "" && !relationDeclared(desc.RelatedModel, desc.Inverse) {
		return nil, errors.OrmRelationNotFound.Args(desc.Inverse, reflect.TypeOf(desc.RelatedModel).String())
	}
	return desc, nil
}

// descriptorFromHasOneOrMany handles the HasOneOrMany family's non-polymorphic members
// (HasOne, HasMany). The polymorphic members (MorphOne, MorphMany) share the same FK-on-related
// shape but add a type-column filter, so they go through DescriptorFromMorphOneOrMany instead.
func descriptorFromHasOneOrMany(parent any, parentTable, name string, related any, foreignKey, localKey string, kind Kind) (*Descriptor, error) {
	if related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	relatedTable, err := descriptorSeam.TableOf(related)
	if err != nil {
		return nil, err
	}
	fk := cmp.Or(foreignKey, str.Of(parentTable).Singular().String()+"_id")
	lk := cmp.Or(localKey, "id")
	return &Descriptor{
		Kind:         kind,
		ParentTable:  parentTable,
		RelatedTable: relatedTable,
		RelatedModel: related,
		References: []ReferenceKey{{
			PrimaryTable:  parentTable,
			PrimaryColumn: lk,
			ForeignTable:  relatedTable,
			ForeignColumn: fk,
		}},
	}, nil
}

func descriptorFromBelongsTo(parent any, parentTable, name string, rel contractsorm.BelongsTo) (*Descriptor, error) {
	if rel.Related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	relatedTable, err := descriptorSeam.TableOf(rel.Related)
	if err != nil {
		return nil, err
	}
	fk := cmp.Or(rel.ForeignKey, str.Of(relatedTable).Singular().String()+"_id")
	owner := cmp.Or(rel.OwnerKey, "id")
	return &Descriptor{
		Kind:         KindBelongsTo,
		ParentTable:  parentTable,
		RelatedTable: relatedTable,
		RelatedModel: rel.Related,
		References: []ReferenceKey{{
			PrimaryTable:  relatedTable,
			PrimaryColumn: owner,
			ForeignTable:  parentTable,
			ForeignColumn: fk,
		}},
	}, nil
}

func descriptorFromMany2Many(parent any, parentTable, name string, rel contractsorm.Many2Many) (*Descriptor, error) {
	if rel.Related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	relatedTable, err := descriptorSeam.TableOf(rel.Related)
	if err != nil {
		return nil, err
	}
	parentSingular := str.Of(parentTable).Singular().String()
	relatedSingular := str.Of(relatedTable).Singular().String()
	pivotTable := cmp.Or(rel.Table, alphabeticalPivotName(parentSingular, relatedSingular))
	foreignPivotKey := cmp.Or(rel.ForeignPivotKey, parentSingular+"_id")
	relatedPivotKey := cmp.Or(rel.RelatedPivotKey, relatedSingular+"_id")
	parentKey := cmp.Or(rel.ParentKey, "id")
	relatedKey := cmp.Or(rel.RelatedKey, "id")

	relatedKeyType, err := relatedKeyFieldType(rel.Related, relatedKey)
	if err != nil {
		return nil, err
	}
	pivotField := cmp.Or(rel.PivotField, "Pivot")
	createdAtCol, updatedAtCol, err := ResolvePivotTimestamps(rel.Related, pivotField, rel.PivotTimestamps)
	if err != nil {
		return nil, err
	}

	return &Descriptor{
		Kind:         KindMany2Many,
		ParentTable:  parentTable,
		RelatedTable: relatedTable,
		RelatedModel: rel.Related,
		PivotTable:   pivotTable,
		PivotParentRef: ReferenceKey{
			PrimaryTable:  parentTable,
			PrimaryColumn: parentKey,
			ForeignTable:  pivotTable,
			ForeignColumn: foreignPivotKey,
		},
		PivotRelatedRef: ReferenceKey{
			PrimaryTable:  relatedTable,
			PrimaryColumn: relatedKey,
			ForeignTable:  pivotTable,
			ForeignColumn: relatedPivotKey,
		},
		PivotField:           pivotField,
		PivotCreatedAtColumn: createdAtCol,
		PivotUpdatedAtColumn: updatedAtCol,
		RelatedKeyType:       relatedKeyType,
	}, nil
}

func DescriptorFromMorphOneOrMany(parent any, parentTable, name string, related any, morphName, typeCol, idCol, localKey string, kind Kind) (*Descriptor, error) {
	if related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	if morphName == "" {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Name")
	}
	if !descriptorSeamInstalled() {
		return nil, errors.OrmRelationDescriptorSeamMissing
	}
	relatedTable, err := descriptorSeam.TableOf(related)
	if err != nil {
		return nil, err
	}
	typeColumn := cmp.Or(typeCol, morphName+"_type")
	idColumn := cmp.Or(idCol, morphName+"_id")
	lk := cmp.Or(localKey, "id")

	morphValue, err := ResolveMorphValue(parent, parentTable)
	if err != nil {
		return nil, err
	}

	return &Descriptor{
		Kind:            kind,
		ParentTable:     parentTable,
		RelatedTable:    relatedTable,
		RelatedModel:    related,
		MorphTypeColumn: typeColumn,
		MorphIDColumn:   idColumn,
		MorphValue:      morphValue,
		References: []ReferenceKey{{
			PrimaryTable:  parentTable,
			PrimaryColumn: lk,
			ForeignTable:  relatedTable,
			ForeignColumn: idColumn,
		}},
	}, nil
}

func descriptorFromMorphTo(parent any, parentTable, name string, rel contractsorm.MorphTo) (*Descriptor, error) {
	if rel.Name == "" {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Name")
	}
	return &Descriptor{
		Kind:            KindMorphTo,
		ParentTable:     parentTable,
		MorphTypeColumn: cmp.Or(rel.TypeColumn, rel.Name+"_type"),
		MorphIDColumn:   cmp.Or(rel.IDColumn, rel.Name+"_id"),
		MorphOwnerKey:   cmp.Or(rel.OwnerKey, "id"),
		// The per-type eager config and the trashed-scope family travel to loadMorphTo's buckets
		// and to the object-path read — MorphTo::getResultsByType's per-class with() / withCount()
		// / constrain() replay and the buffered withTrashed family, in declaration form.
		MorphWith:           rel.MorphWith,
		MorphWithCount:      rel.MorphWithCount,
		MorphConstrain:      rel.Constrain,
		MorphWithTrashed:    rel.WithTrashed,
		MorphWithoutTrashed: rel.WithoutTrashed,
		MorphOnlyTrashed:    rel.OnlyTrashed,
	}, nil
}

// descriptorFromMorphToMany covers MorphToMany. It's separated from MorphedByMany so the
// morph-value derivation source can differ (parent vs. related) without re-reading the kind via
// reflection.
func descriptorFromMorphToMany(parent any, parentTable, name string, rel contractsorm.MorphToMany, inverse bool) (*Descriptor, error) {
	return BuildMorphPivotDescriptor(parent, parentTable, name,
		rel.Related, rel.Name, rel.Table, rel.TypeColumn,
		rel.ForeignPivotKey, rel.RelatedPivotKey, rel.ParentKey, rel.RelatedKey,
		rel.PivotField, rel.PivotTimestamps,
		inverse,
	)
}

func descriptorFromMorphedByMany(parent any, parentTable, name string, rel contractsorm.MorphedByMany) (*Descriptor, error) {
	return BuildMorphPivotDescriptor(parent, parentTable, name,
		rel.Related, rel.Name, rel.Table, rel.TypeColumn,
		rel.ForeignPivotKey, rel.RelatedPivotKey, rel.ParentKey, rel.RelatedKey,
		rel.PivotField, rel.PivotTimestamps,
		true,
	)
}

func BuildMorphPivotDescriptor(parent any, parentTable, name string, related any, morphName, table, typeCol, foreignPivot, relatedPivot, parentKey, relatedKey string, pivotField string, pivotTimestamps bool, inverse bool) (*Descriptor, error) {
	if related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	if morphName == "" {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Name")
	}
	if !descriptorSeamInstalled() {
		return nil, errors.OrmRelationDescriptorSeamMissing
	}
	relatedTable, err := descriptorSeam.TableOf(related)
	if err != nil {
		return nil, err
	}

	pivotTable := cmp.Or(table, str.Of(morphName).Plural().String())
	morphTypeColumn := cmp.Or(typeCol, morphName+"_type")
	morphIDColumn := cmp.Or(foreignPivot, morphName+"_id")
	relatedPivotKey := cmp.Or(relatedPivot, str.Of(relatedTable).Singular().String()+"_id")
	pk := cmp.Or(parentKey, "id")
	rk := cmp.Or(relatedKey, "id")

	// The resolution source depends on the direction (MorphToMany.php:71): the forward
	// morphToMany stamps the parent's morph class, while morphedByMany (inverse) stamps the
	// related model's — Laravel never resolves the other side, so under strict mode only the
	// stamped side can raise. Oracle v12.55.1, executed: a strict morphedByMany whose owner is
	// unmapped and whose related is mapped resolves the alias without throwing, while an unmapped
	// related side raises. Resolving both sides (parent first) made an unmapped owner raise for
	// inverse relations.
	var morphValue string
	if inverse {
		morphValue, err = ResolveMorphValue(related, relatedTable)
		if err != nil {
			return nil, err
		}
	} else {
		morphValue, err = ResolveMorphValue(parent, parentTable)
		if err != nil {
			return nil, err
		}
	}

	relatedKeyType, err := relatedKeyFieldType(related, rk)
	if err != nil {
		return nil, err
	}
	pivotFieldName := cmp.Or(pivotField, "Pivot")
	createdAtCol, updatedAtCol, err := ResolvePivotTimestamps(related, pivotFieldName, pivotTimestamps)
	if err != nil {
		return nil, err
	}

	return &Descriptor{
		Kind:            KindMorphToMany,
		ParentTable:     parentTable,
		RelatedTable:    relatedTable,
		RelatedModel:    related,
		PivotTable:      pivotTable,
		MorphTypeColumn: morphTypeColumn,
		MorphIDColumn:   morphIDColumn,
		MorphValue:      morphValue,
		MorphInverse:    inverse,
		PivotParentRef: ReferenceKey{
			PrimaryTable:  parentTable,
			PrimaryColumn: pk,
			ForeignTable:  pivotTable,
			ForeignColumn: morphIDColumn,
		},
		PivotRelatedRef: ReferenceKey{
			PrimaryTable:  relatedTable,
			PrimaryColumn: rk,
			ForeignTable:  pivotTable,
			ForeignColumn: relatedPivotKey,
		},
		PivotField:           pivotFieldName,
		PivotCreatedAtColumn: createdAtCol,
		PivotUpdatedAtColumn: updatedAtCol,
		RelatedKeyType:       relatedKeyType,
	}, nil
}

func descriptorFromThrough(parent any, parentTable, name string, related, through any, firstKey, secondKey, localKey, secondLocalKey string, kind Kind) (*Descriptor, error) {
	if related == nil {
		return nil, errors.OrmRelationThroughNotConfigured.Args(name, reflect.TypeOf(parent).String())
	}
	if through == nil {
		return nil, errors.OrmRelationThroughNotConfigured.Args(name, reflect.TypeOf(parent).String())
	}
	relatedTable, err := descriptorSeam.TableOf(related)
	if err != nil {
		return nil, err
	}
	throughTable, err := descriptorSeam.TableOf(through)
	if err != nil {
		return nil, err
	}
	return &Descriptor{
		Kind:           kind,
		ParentTable:    parentTable,
		RelatedTable:   relatedTable,
		RelatedModel:   related,
		ThroughTable:   throughTable,
		FirstKey:       cmp.Or(firstKey, str.Of(parentTable).Singular().String()+"_id"),
		SecondKey:      cmp.Or(secondKey, str.Of(throughTable).Singular().String()+"_id"),
		LocalKey:       cmp.Or(localKey, "id"),
		SecondLocalKey: cmp.Or(secondLocalKey, "id"),
	}, nil
}

// relatedKeyFieldType returns the Go type of the related model's PK field (the column referenced
// by the pivot's RelatedPivotKey). Used by SyncResult to normalise ids back to the related model's
// native key type via castKey. Returns nil (no error) if the column is not a recognised field on
// the related schema — castKey then leaves ids untouched.
func relatedKeyFieldType(related any, columnName string) (reflect.Type, error) {
	schema, err := descriptorSeam.ParseModel(related)
	if err != nil {
		return nil, err
	}
	if fieldType, ok := schema.ColumnType(columnName); ok {
		return fieldType, nil
	}
	return nil, nil
}

// ResolvePivotTimestamps decides which pivot-table columns should be auto-stamped with the
// current time on INSERT (created) and INSERT/UPDATE (updated). Detection priority:
//
//  1. Pivot struct field with `gorm:"autoCreateTime"` / `gorm:"autoUpdateTime"` tag — column
//     name from the field's GORM schema (respects `gorm:"column:..."`).
//  2. Pivot struct field named CreatedAt / UpdatedAt of type time.Time (GORM convention).
//  3. fallbackEnabled (relation-level PivotTimestamps: true) — defaults to "created_at" /
//     "updated_at" for whichever column the pivot struct didn't already provide.
//
// Empty string for either column means "don't auto-stamp on that op". The pivot struct does not
// need to declare both — declaring only CreatedAt is fine and disables update-side stamping.
func ResolvePivotTimestamps(relatedModel any, pivotFieldName string, fallbackEnabled bool) (createdCol, updatedCol string, err error) {
	if !descriptorSeamInstalled() {
		return "", "", errors.OrmRelationDescriptorSeamMissing
	}
	pivotStructType, ok := descriptorSeam.PivotFieldStructType(relatedModel, pivotFieldName)
	if ok {
		schema, schemaErr := descriptorSeam.ParseModel(reflect.New(pivotStructType).Interface())
		if schemaErr != nil {
			return "", "", schemaErr
		}
		// Priority 1: explicit GORM tags on any field.
		for _, f := range schema.Columns() {
			if f.AutoCreateTime && createdCol == "" {
				createdCol = f.DBName
			}
			if f.AutoUpdateTime && updatedCol == "" {
				updatedCol = f.DBName
			}
		}
		// Priority 2: convention — fields named CreatedAt / UpdatedAt of type time.Time.
		if createdCol == "" {
			if f, found := schema.ColumnByName("CreatedAt"); found && f.FieldType == reflect.TypeFor[time.Time]() {
				createdCol = f.DBName
			}
		}
		if updatedCol == "" {
			if f, found := schema.ColumnByName("UpdatedAt"); found && f.FieldType == reflect.TypeFor[time.Time]() {
				updatedCol = f.DBName
			}
		}
	}
	// Priority 3: relation-level fallback. Only fills columns the pivot struct didn't provide.
	if fallbackEnabled {
		if createdCol == "" {
			createdCol = "created_at"
		}
		if updatedCol == "" {
			updatedCol = "updated_at"
		}
	}
	return createdCol, updatedCol, nil
}

// alphabeticalPivotName returns the Eloquent-convention default pivot table for a Many2Many
// relation: the two singular table names sorted alphabetically and joined by "_". E.g.
// (post, tag) -> "post_tag", (user, role) -> "role_user".
func alphabeticalPivotName(a, b string) string {
	if a < b {
		return a + "_" + b
	}
	return b + "_" + a
}

// ResolveMorphValue picks the value to use for a polymorphic *_type column. The model-level
// MorphClass() method takes precedence, then the global morph map (registered via orm.MorphMap),
// then GORM's parsed PrimaryValue (which is either a `polymorphicValue:` tag or the parent's
// table name). Under strict morph mode (orm.RequireMorphMap / orm.EnforceMorphMap) an unmapped
// model raises errors.OrmClassMorphViolation instead of reaching the fallback — the Go reading of
// getMorphClass's ClassMorphViolationException throw (HasRelationships.php:1019); the base
// relations.Pivot stays exempt (HasRelationships.php:1014).
func ResolveMorphValue(parent any, gormDefault string) (string, error) {
	if !descriptorSeamInstalled() {
		return "", errors.OrmRelationDescriptorSeamMissing
	}
	if v, ok, err := descriptorSeam.MorphValue(parent); err != nil {
		return "", err
	} else if ok {
		return v, nil
	}
	return gormDefault, nil
}

// ResolveMorphAlias returns the morph alias for model from MorphClass() / morph map only —
// without falling back to the table name. Used by Associate when we want to know whether the
// owner has an explicit registered alias before defaulting to its table. Under strict morph mode
// an unmapped owner raises errors.OrmClassMorphViolation through the returned error.
func ResolveMorphAlias(model any) (string, bool, error) {
	if !descriptorSeamInstalled() {
		return "", false, errors.OrmRelationDescriptorSeamMissing
	}
	return descriptorSeam.MorphValue(model)
}

// TryGetRelations returns the parent model's Relations() map regardless of whether the method is
// declared with a value receiver (`func (Foo) Relations()`) or a pointer receiver (`func (*Foo)
// Relations()`). Returns ok=false if neither form satisfies ModelWithRelations.
//
// Mirrors the dual-receiver detection in morphmap.tryMorphClass; both helpers exist because Go
// doesn't pick a receiver style for users — and real-world models freely mix value and pointer
// receivers across methods on the same struct.
func TryGetRelations(parent any) (map[string]contractsorm.Relation, bool) {
	// Direct interface satisfaction — covers the common case where parent is *Foo and Relations
	// has a pointer receiver, *or* parent is *Foo and Relations has a value receiver (since
	// pointer-to-T satisfies any value-receiver interface T).
	if m, ok := parent.(contractsorm.ModelWithRelations); ok {
		return m.Relations(), true
	}
	rv := reflect.ValueOf(parent)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil, false
		}
		// Try the dereferenced value — covers value-receiver methods when parent is a pointer.
		// (Already handled above, but keep the branch for completeness; falls through silently.)
		if m, ok := rv.Elem().Interface().(contractsorm.ModelWithRelations); ok {
			return m.Relations(), true
		}
	case reflect.Struct:
		// parent is a value but Relations is on the pointer receiver — wrap in a fresh
		// addressable pointer so the method set includes the pointer-receiver methods.
		ptr := reflect.New(rv.Type())
		ptr.Elem().Set(rv)
		if m, ok := ptr.Interface().(contractsorm.ModelWithRelations); ok {
			return m.Relations(), true
		}
	}
	return nil, false
}

// relationDeclared reports whether model declares a relation named name in its Relations() map —
// the Go form of Eloquent's Model::isRelation(), which SupportsInverseRelations::chaperone()
// validates an inverse relation against (RelationNotFoundException when it is missing).
func relationDeclared(model any, name string) bool {
	relations, ok := TryGetRelations(model)
	if !ok {
		return false
	}
	_, ok = relations[name]

	return ok
}

// Info projects the descriptor onto the plain-data RelationInfo the object layer and the write
// workers read — the descriptor half of database/gorm's relationInfoFromDescriptor. The facts
// bound to the built query or to the parent model (OneOfMany, ParentPrimaryKeyColumn,
// RelatedConnection and the MorphTo related-table fallback) stay gorm's to fill in.
func (d *Descriptor) Info() RelationInfo {
	info := RelationInfo{
		Kind:                 d.Kind,
		ParentTable:          d.ParentTable,
		RelatedTable:         d.RelatedTable,
		RelatedModel:         d.RelatedModel,
		WithDefault:          d.WithDefault,
		Inverse:              d.Inverse,
		MorphTypeColumn:      d.MorphTypeColumn,
		MorphIDColumn:        d.MorphIDColumn,
		MorphValue:           d.MorphValue,
		MorphOwnerKey:        d.MorphOwnerKey,
		MorphInverse:         d.MorphInverse,
		ThroughTable:         d.ThroughTable,
		FirstKey:             d.FirstKey,
		SecondKey:            d.SecondKey,
		LocalKey:             d.LocalKey,
		SecondLocalKey:       d.SecondLocalKey,
		PivotTable:           d.PivotTable,
		PivotParentRef:       RefKey{PrimaryColumn: d.PivotParentRef.PrimaryColumn, ForeignColumn: d.PivotParentRef.ForeignColumn},
		PivotRelatedRef:      RefKey{PrimaryColumn: d.PivotRelatedRef.PrimaryColumn, ForeignColumn: d.PivotRelatedRef.ForeignColumn},
		PivotField:           d.PivotField,
		Name:                 d.Name,
		Touches:              d.Touches,
		PivotCreatedAtColumn: d.PivotCreatedAtColumn,
		PivotUpdatedAtColumn: d.PivotUpdatedAtColumn,
		RelatedKeyType:       d.RelatedKeyType,
		OnPivotQuery:         d.OnPivotQuery,
	}
	for _, ref := range d.References {
		info.References = append(info.References, RefKey{PrimaryColumn: ref.PrimaryColumn, ForeignColumn: ref.ForeignColumn})
	}

	return info
}

// ValidWithDefault reports whether a WithDefault declaration uses one of the accepted forms.
func ValidWithDefault(value any) bool {
	switch value.(type) {
	case nil, bool, map[string]any, func(related, parent any) any:
		return true
	default:
		return false
	}
}
