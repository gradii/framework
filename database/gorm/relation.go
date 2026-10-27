package gorm

import (
	"cmp"
	"reflect"
	"strings"
	"time"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/internal/annotation"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/str"
)

// relationKind enumerates every relationship flavour the resolver can describe.
// It is a superset of GORM's RelationshipType because it also covers the inverse polymorphic
// (MorphTo) and the through relations declared via ModelWithThroughRelations.
type relationKind int

const (
	relKindHasOne relationKind = iota
	relKindHasMany
	relKindBelongsTo
	relKindMany2Many
	relKindMorphOne
	relKindMorphMany
	relKindMorphTo
	relKindMorphToMany
	relKindHasOneThrough
	relKindHasManyThrough
)

// referenceKey describes one column-pair from a GORM Reference, with each side already qualified
// by table name. PrimaryKey/ForeignKey naming follows GORM's convention.
type referenceKey struct {
	primaryTable  string
	primaryColumn string
	foreignTable  string
	foreignColumn string
}

// relationDescriptor is the resolver's normalised view of a relationship. It lets the
// queries-relationships builder construct correlated subqueries without ever calling back into
// GORM's relation internals.
type relationDescriptor struct {
	name         string
	kind         relationKind
	parentTable  string
	relatedTable string
	relatedModel any
	references   []referenceKey

	// many-to-many specifics
	pivotTable      string
	pivotParentRef  referenceKey
	pivotRelatedRef referenceKey
	// pivotField is the name of the field on the related model that the eager loader hydrates
	// with pivot column values. Sourced from Many2Many.PivotField (or the morph variants);
	// defaults to "Pivot". When the related model has no field by this name, no Pivot hydration
	// happens. The field's Go type drives both the SELECT list and hydration target.
	pivotField string
	// pivotCreatedAtColumn is the pivot-table column to auto-stamp with the current time on
	// INSERT. Empty string means "don't auto-stamp on INSERT". Resolved by the descriptor builder
	// from (priority): Pivot struct's autoCreateTime field → Pivot struct's CreatedAt field →
	// PivotTimestamps: true fallback (defaults to "created_at").
	pivotCreatedAtColumn string
	// pivotUpdatedAtColumn is the pivot-table column to auto-stamp with the current time on
	// INSERT and UPDATE. Empty string means "don't auto-stamp on INSERT/UPDATE". Same resolution
	// rules as pivotCreatedAtColumn but using autoUpdateTime / UpdatedAt.
	pivotUpdatedAtColumn string

	// relatedKeyType is the Go type of the related model's PK field — used by castKey to normalise
	// SyncResult ids back to the related model's native key type, irrespective of what the caller
	// passed (string, int, uint, etc.) and what GORM scanned from the pivot table (often int64).
	// nil for kinds that don't have a related-side pivot key (HasOne family, BelongsTo, etc.).
	relatedKeyType reflect.Type

	// polymorphic specifics
	morphTypeColumn string // e.g. "imageable_type" — on parent table for MorphTo, on pivot for MorphToMany
	morphIDColumn   string // e.g. "imageable_id"  — on parent table for MorphTo, on pivot for MorphToMany
	morphValue      string // e.g. "post"          — used in WHERE *_type = ? filters
	morphOwnerKey   string // PK on each related model for MorphTo (defaults to "id")
	morphInverse    bool   // true for MorphedByMany — flips morph value source from parent to related

	// through specifics
	throughTable   string
	throughModel   any
	firstKey       string // FK on through pointing at parent
	secondKey      string // FK on related pointing at through
	localKey       string // PK on parent
	secondLocalKey string // PK on through

	// onQuery is the per-relation default scope from Relation.OnQuery. Applied by every code
	// path that builds an inner query for this relation (eager loaders, existence builders,
	// Related), *before* any caller-supplied callback.
	onQuery contractsorm.RelationCallback

	// onPivotQuery is the per-relation default scope for pivot-table SELECT / UPDATE / DELETE,
	// from Many2Many.OnPivotQuery / MorphToMany.OnPivotQuery / MorphedByMany.OnPivotQuery.
	// Applied by existingPivotIDs, allPivotIDs, DetachRelation, UpdateExistingPivotRelation.
	onPivotQuery contractsorm.PivotCallback

	// touches, when true, makes Sync / Attach / Detach / Toggle / UpdateExistingPivot bump the
	// parent's updated_at after the pivot write succeeds (and only when pivot rows actually
	// changed). Source: Many2Many.Touches / MorphToMany.Touches / MorphedByMany.Touches.
	touches bool

	// next link for nested resolution (e.g. "Books.Author")
	nested *relationDescriptor
}

// resolveRelation walks a (possibly dotted) relation path and returns a chain of descriptors
// rooted at the given parent model. The returned descriptor's nested field points at the next
// hop, so callers can recurse to build subqueries for "User.Books.Author"-style queries.
//
// All relations are declared via the parent's Relations() method (ModelWithRelations). GORM
// relation tags (`foreignKey`, `references`, `many2many`, `polymorphic`) are forbidden — if
// detected the resolver returns OrmRelationTagForbidden pointing the user at Relations().
func resolveRelation(parent any, relation string) (*relationDescriptor, error) {
	if relation == "" {
		return nil, errors.OrmQueryEmptyRelation
	}

	head, tail, _ := strings.Cut(relation, ".")

	parentSchema, err := parseSchema(parent)
	if err != nil {
		return nil, err
	}
	parentTable := parentSchema.Table

	// Detect forbidden GORM relation tags. GORM used to populate a Relationships entry for the
	// requested name, which is how the conflict was noticed; the tag itself is inspected now, so the
	// check no longer depends on GORM parsing the model.
	if hasRelationTag(parent, head) {
		return nil, errors.OrmRelationTagForbidden.Args(head, parentSchema.Name)
	}

	desc, err := descriptorFromRelations(parent, parentTable, head)
	if err != nil {
		return nil, err
	}
	desc.name = head

	if tail != "" {
		// Recurse using the *related* model as the new parent.
		nestedParent := desc.relatedModel
		if nestedParent == nil {
			return nil, errors.OrmRelationUnsupported.Args(head, parentSchema.Name, "no related model")
		}
		nested, err := resolveRelation(nestedParent, tail)
		if err != nil {
			return nil, err
		}
		desc.nested = nested
	}
	return desc, nil
}

// hasRelationTag reports whether a model's field of the given name declares a GORM relation.
//
// The framework declares relations through Relations(), so a model that still carries one of GORM's
// relation tags is rejected rather than resolved by a mechanism the model's own declaration does not
// mention. Embedded structs are searched too, because GORM read their tags as if they were the outer
// model's own.
func hasRelationTag(model any, name string) bool {
	modelType := indirectStructType(reflect.TypeOf(model))
	if modelType == nil {
		return false
	}

	return typeHasRelationTag(modelType, name)
}

func typeHasRelationTag(modelType reflect.Type, name string) bool {
	for position := 0; position < modelType.NumField(); position++ {
		field := modelType.Field(position)
		if !field.IsExported() {
			continue
		}

		if field.Anonymous && !isColumnType(field.Type) {
			if embedded := indirectStructType(field.Type); embedded != nil && typeHasRelationTag(embedded, name) {
				return true
			}

			continue
		}

		if field.Name == name {
			return annotation.ParseAnnotation(field.Tag.Get("gorm")).Relation
		}
	}

	return false
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
// and is dispatched by the internal relationKind groups; this function's job is to map the
// user-facing struct to the descriptor those downstream paths consume.
//
// Accepts Relations() declared with either a value receiver (`func (Foo) Relations()`) or a
// pointer receiver (`func (*Foo) Relations()`). Models often mix the two — e.g. value receivers
// for pure-metadata methods and pointer receivers for GORM lifecycle hooks — so the framework
// looks up the interface on whichever form is addressable.
func descriptorFromRelations(parent any, parentTable, name string) (*relationDescriptor, error) {
	relations, ok := tryGetRelations(parent)
	if !ok {
		return nil, errors.OrmRelationNotFound.Args(name, reflect.TypeOf(parent).String())
	}
	rel, ok := relations[name]
	if !ok {
		return nil, errors.OrmRelationNotFound.Args(name, reflect.TypeOf(parent).String())
	}

	var (
		desc    *relationDescriptor
		err     error
		onQuery contractsorm.RelationCallback
	)
	switch r := rel.(type) {
	case contractsorm.HasOne:
		desc, err = descriptorFromHasOneOrMany(parent, parentTable, name, r.Related, r.ForeignKey, r.LocalKey, relKindHasOne)
		onQuery = r.OnQuery
	case contractsorm.HasMany:
		desc, err = descriptorFromHasOneOrMany(parent, parentTable, name, r.Related, r.ForeignKey, r.LocalKey, relKindHasMany)
		onQuery = r.OnQuery
	case contractsorm.BelongsTo:
		desc, err = descriptorFromBelongsTo(parent, parentTable, name, r)
		onQuery = r.OnQuery
	case contractsorm.Many2Many:
		desc, err = descriptorFromMany2Many(parent, parentTable, name, r)
		onQuery = r.OnQuery
		if desc != nil {
			desc.onPivotQuery = r.OnPivotQuery
			desc.touches = r.Touches
		}
	case contractsorm.MorphOne:
		desc, err = descriptorFromMorphOneOrMany(parent, parentTable, name, r.Related, r.Name, r.TypeColumn, r.IDColumn, r.LocalKey, relKindMorphOne)
		onQuery = r.OnQuery
	case contractsorm.MorphMany:
		desc, err = descriptorFromMorphOneOrMany(parent, parentTable, name, r.Related, r.Name, r.TypeColumn, r.IDColumn, r.LocalKey, relKindMorphMany)
		onQuery = r.OnQuery
	case contractsorm.MorphTo:
		desc, err = descriptorFromMorphTo(parent, parentTable, name, r)
		onQuery = r.OnQuery
	case contractsorm.MorphToMany:
		desc, err = descriptorFromMorphToMany(parent, parentTable, name, r, false)
		onQuery = r.OnQuery
		if desc != nil {
			desc.onPivotQuery = r.OnPivotQuery
			desc.touches = r.Touches
		}
	case contractsorm.MorphedByMany:
		desc, err = descriptorFromMorphedByMany(parent, parentTable, name, r)
		onQuery = r.OnQuery
		if desc != nil {
			desc.onPivotQuery = r.OnPivotQuery
			desc.touches = r.Touches
		}
	case contractsorm.HasOneThrough:
		desc, err = descriptorFromThrough(parent, parentTable, name, r.Related, r.Through, r.FirstKey, r.SecondKey, r.LocalKey, r.SecondLocalKey, relKindHasOneThrough)
		onQuery = r.OnQuery
	case contractsorm.HasManyThrough:
		desc, err = descriptorFromThrough(parent, parentTable, name, r.Related, r.Through, r.FirstKey, r.SecondKey, r.LocalKey, r.SecondLocalKey, relKindHasManyThrough)
		onQuery = r.OnQuery
	default:
		return nil, errors.OrmMorphRelationKindUnknown.Args(name, reflect.TypeOf(parent).String(), reflect.TypeOf(rel).String())
	}
	if err != nil {
		return nil, err
	}
	// Carry the per-relation default-scope hook into the descriptor; every consumer (eager
	// loader, existence builder, Related) applies it before any caller callback.
	desc.onQuery = onQuery
	return desc, nil
}

// descriptorFromHasOneOrMany handles the HasOneOrMany family's non-polymorphic members
// (HasOne, HasMany). The polymorphic members (MorphOne, MorphMany) share the same FK-on-related
// shape but add a type-column filter, so they go through descriptorFromMorphOneOrMany instead.
func descriptorFromHasOneOrMany(parent any, parentTable, name string, related any, foreignKey, localKey string, kind relationKind) (*relationDescriptor, error) {
	if related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	relatedTable, err := tableNameFor(related)
	if err != nil {
		return nil, err
	}
	fk := cmp.Or(foreignKey, str.Of(parentTable).Singular().String()+"_id")
	lk := cmp.Or(localKey, "id")
	return &relationDescriptor{
		kind:         kind,
		parentTable:  parentTable,
		relatedTable: relatedTable,
		relatedModel: related,
		references: []referenceKey{{
			primaryTable:  parentTable,
			primaryColumn: lk,
			foreignTable:  relatedTable,
			foreignColumn: fk,
		}},
	}, nil
}

func descriptorFromBelongsTo(parent any, parentTable, name string, rel contractsorm.BelongsTo) (*relationDescriptor, error) {
	if rel.Related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	relatedTable, err := tableNameFor(rel.Related)
	if err != nil {
		return nil, err
	}
	fk := cmp.Or(rel.ForeignKey, str.Of(relatedTable).Singular().String()+"_id")
	owner := cmp.Or(rel.OwnerKey, "id")
	return &relationDescriptor{
		kind:         relKindBelongsTo,
		parentTable:  parentTable,
		relatedTable: relatedTable,
		relatedModel: rel.Related,
		references: []referenceKey{{
			primaryTable:  relatedTable,
			primaryColumn: owner,
			foreignTable:  parentTable,
			foreignColumn: fk,
		}},
	}, nil
}

func descriptorFromMany2Many(parent any, parentTable, name string, rel contractsorm.Many2Many) (*relationDescriptor, error) {
	if rel.Related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	relatedTable, err := tableNameFor(rel.Related)
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
	createdAtCol, updatedAtCol, err := resolvePivotTimestamps(rel.Related, pivotField, rel.PivotTimestamps)
	if err != nil {
		return nil, err
	}

	return &relationDescriptor{
		kind:         relKindMany2Many,
		parentTable:  parentTable,
		relatedTable: relatedTable,
		relatedModel: rel.Related,
		pivotTable:   pivotTable,
		pivotParentRef: referenceKey{
			primaryTable:  parentTable,
			primaryColumn: parentKey,
			foreignTable:  pivotTable,
			foreignColumn: foreignPivotKey,
		},
		pivotRelatedRef: referenceKey{
			primaryTable:  relatedTable,
			primaryColumn: relatedKey,
			foreignTable:  pivotTable,
			foreignColumn: relatedPivotKey,
		},
		pivotField:           pivotField,
		pivotCreatedAtColumn: createdAtCol,
		pivotUpdatedAtColumn: updatedAtCol,
		relatedKeyType:       relatedKeyType,
	}, nil
}

func descriptorFromMorphOneOrMany(parent any, parentTable, name string, related any, morphName, typeCol, idCol, localKey string, kind relationKind) (*relationDescriptor, error) {
	if related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	if morphName == "" {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Name")
	}
	relatedTable, err := tableNameFor(related)
	if err != nil {
		return nil, err
	}
	typeColumn := cmp.Or(typeCol, morphName+"_type")
	idColumn := cmp.Or(idCol, morphName+"_id")
	lk := cmp.Or(localKey, "id")

	return &relationDescriptor{
		kind:            kind,
		parentTable:     parentTable,
		relatedTable:    relatedTable,
		relatedModel:    related,
		morphTypeColumn: typeColumn,
		morphIDColumn:   idColumn,
		morphValue:      resolveMorphValue(parent, parentTable),
		references: []referenceKey{{
			primaryTable:  parentTable,
			primaryColumn: lk,
			foreignTable:  relatedTable,
			foreignColumn: idColumn,
		}},
	}, nil
}

func descriptorFromMorphTo(parent any, parentTable, name string, rel contractsorm.MorphTo) (*relationDescriptor, error) {
	if rel.Name == "" {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Name")
	}
	return &relationDescriptor{
		kind:            relKindMorphTo,
		parentTable:     parentTable,
		morphTypeColumn: cmp.Or(rel.TypeColumn, rel.Name+"_type"),
		morphIDColumn:   cmp.Or(rel.IDColumn, rel.Name+"_id"),
		morphOwnerKey:   cmp.Or(rel.OwnerKey, "id"),
	}, nil
}

// descriptorFromMorphToMany covers MorphToMany. It's separated from MorphedByMany so the
// morph-value derivation source can differ (parent vs. related) without re-reading the kind via
// reflection.
func descriptorFromMorphToMany(parent any, parentTable, name string, rel contractsorm.MorphToMany, inverse bool) (*relationDescriptor, error) {
	return buildMorphPivotDescriptor(parent, parentTable, name,
		rel.Related, rel.Name, rel.Table, rel.TypeColumn,
		rel.ForeignPivotKey, rel.RelatedPivotKey, rel.ParentKey, rel.RelatedKey,
		rel.PivotField, rel.PivotTimestamps,
		inverse,
	)
}

func descriptorFromMorphedByMany(parent any, parentTable, name string, rel contractsorm.MorphedByMany) (*relationDescriptor, error) {
	return buildMorphPivotDescriptor(parent, parentTable, name,
		rel.Related, rel.Name, rel.Table, rel.TypeColumn,
		rel.ForeignPivotKey, rel.RelatedPivotKey, rel.ParentKey, rel.RelatedKey,
		rel.PivotField, rel.PivotTimestamps,
		true,
	)
}

func buildMorphPivotDescriptor(parent any, parentTable, name string, related any, morphName, table, typeCol, foreignPivot, relatedPivot, parentKey, relatedKey string, pivotField string, pivotTimestamps bool, inverse bool) (*relationDescriptor, error) {
	if related == nil {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Related")
	}
	if morphName == "" {
		return nil, errors.OrmMorphRelationMissingField.Args(name, reflect.TypeOf(parent).String(), "Name")
	}
	relatedTable, err := tableNameFor(related)
	if err != nil {
		return nil, err
	}

	pivotTable := cmp.Or(table, str.Of(morphName).Plural().String())
	morphTypeColumn := cmp.Or(typeCol, morphName+"_type")
	morphIDColumn := cmp.Or(foreignPivot, morphName+"_id")
	relatedPivotKey := cmp.Or(relatedPivot, str.Of(relatedTable).Singular().String()+"_id")
	pk := cmp.Or(parentKey, "id")
	rk := cmp.Or(relatedKey, "id")

	morphValue := resolveMorphValue(parent, parentTable)
	if inverse {
		morphValue = resolveMorphValue(related, relatedTable)
	}

	relatedKeyType, err := relatedKeyFieldType(related, rk)
	if err != nil {
		return nil, err
	}
	pivotFieldName := cmp.Or(pivotField, "Pivot")
	createdAtCol, updatedAtCol, err := resolvePivotTimestamps(related, pivotFieldName, pivotTimestamps)
	if err != nil {
		return nil, err
	}

	return &relationDescriptor{
		kind:            relKindMorphToMany,
		parentTable:     parentTable,
		relatedTable:    relatedTable,
		relatedModel:    related,
		pivotTable:      pivotTable,
		morphTypeColumn: morphTypeColumn,
		morphIDColumn:   morphIDColumn,
		morphValue:      morphValue,
		morphInverse:    inverse,
		pivotParentRef: referenceKey{
			primaryTable:  parentTable,
			primaryColumn: pk,
			foreignTable:  pivotTable,
			foreignColumn: morphIDColumn,
		},
		pivotRelatedRef: referenceKey{
			primaryTable:  relatedTable,
			primaryColumn: rk,
			foreignTable:  pivotTable,
			foreignColumn: relatedPivotKey,
		},
		pivotField:           pivotFieldName,
		pivotCreatedAtColumn: createdAtCol,
		pivotUpdatedAtColumn: updatedAtCol,
		relatedKeyType:       relatedKeyType,
	}, nil
}

func descriptorFromThrough(parent any, parentTable, name string, related, through any, firstKey, secondKey, localKey, secondLocalKey string, kind relationKind) (*relationDescriptor, error) {
	if related == nil {
		return nil, errors.OrmRelationThroughNotConfigured.Args(name, reflect.TypeOf(parent).String())
	}
	if through == nil {
		return nil, errors.OrmRelationThroughNotConfigured.Args(name, reflect.TypeOf(parent).String())
	}
	relatedTable, err := tableNameFor(related)
	if err != nil {
		return nil, err
	}
	throughTable, err := tableNameFor(through)
	if err != nil {
		return nil, err
	}
	return &relationDescriptor{
		kind:           kind,
		parentTable:    parentTable,
		relatedTable:   relatedTable,
		relatedModel:   related,
		throughTable:   throughTable,
		throughModel:   through,
		firstKey:       cmp.Or(firstKey, str.Of(parentTable).Singular().String()+"_id"),
		secondKey:      cmp.Or(secondKey, str.Of(throughTable).Singular().String()+"_id"),
		localKey:       cmp.Or(localKey, "id"),
		secondLocalKey: cmp.Or(secondLocalKey, "id"),
	}, nil
}

// tableNameFor returns the table name for a model, without the connection prefix.
//
// The name is deliberately bare: every statement the ORM renders goes through the framework
// grammar, whose WrapTable applies the prefix to a table standing alone and whose Wrap applies it
// to the leading segment of a dotted table.column fragment. Prefixing here as well produced doubly
// prefixed names such as "goravel_goravel_users", which address no table.
//
// The name comes from the model's own declaration through TableName() (see orm.ModelWithTableName)
// and from nowhere else. There is no naming-strategy fallback left to silently pluralize a model
// that forgot to declare its table, so a model that declares none is reported as a named error
// naming its type and the method to add. The connection argument is kept only because every call
// site already passes one.
func tableNameFor(model any) (string, error) {
	if name := declaredTableName(model); name != "" {
		return name, nil
	}

	// A slice names the model it carries, so the error names that model rather than the slice: the
	// caller wrote the destination, and the method to add belongs on its element type.
	modelType := reflect.TypeOf(model)
	pointer := false
	for modelType != nil && modelType.Kind() == reflect.Pointer {
		pointer = true
		modelType = modelType.Elem()
	}
	if modelType != nil && (modelType.Kind() == reflect.Slice || modelType.Kind() == reflect.Array) {
		element := modelType.Elem()
		for element.Kind() == reflect.Slice || element.Kind() == reflect.Array {
			element = element.Elem()
		}
		if element.Kind() != reflect.Pointer {
			element = reflect.PointerTo(element)
		}
		modelType = element
	} else if pointer {
		modelType = reflect.PointerTo(modelType)
	}

	return "", errors.OrmQueryTableNotSet.Args(modelType)
}

// relatedKeyFieldType returns the Go type of the related model's PK field (the column referenced
// by the pivot's RelatedPivotKey). Used by SyncResult to normalise ids back to the related model's
// native key type via castKey. Returns nil (no error) if the column is not a recognised field on
// the related schema — castKey then leaves ids untouched.
func relatedKeyFieldType(related any, columnName string) (reflect.Type, error) {
	schema, err := parseSchema(related)
	if err != nil {
		return nil, err
	}
	if field, ok := schema.FieldsByDBName[columnName]; ok {
		return field.FieldType, nil
	}
	return nil, nil
}

// resolvePivotTimestamps decides which pivot-table columns should be auto-stamped with the
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
func resolvePivotTimestamps(relatedModel any, pivotFieldName string, fallbackEnabled bool) (createdCol, updatedCol string, err error) {
	pivotStructType, ok := pivotFieldStructType(relatedModel, pivotFieldName)
	if ok {
		schema, schemaErr := parseSchema(reflect.New(pivotStructType).Interface())
		if schemaErr != nil {
			return "", "", schemaErr
		}
		// Priority 1: explicit GORM tags on any field.
		for _, f := range schema.Fields {
			if f.AutoCreateTime != 0 && createdCol == "" {
				createdCol = f.DBName
			}
			if f.AutoUpdateTime != 0 && updatedCol == "" {
				updatedCol = f.DBName
			}
		}
		// Priority 2: convention — fields named CreatedAt / UpdatedAt of type time.Time.
		if createdCol == "" {
			if f, found := schema.FieldsByName["CreatedAt"]; found && f.FieldType == reflect.TypeFor[time.Time]() {
				createdCol = f.DBName
			}
		}
		if updatedCol == "" {
			if f, found := schema.FieldsByName["UpdatedAt"]; found && f.FieldType == reflect.TypeFor[time.Time]() {
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

// pivotFieldStructType reflects relatedModel for a struct field named pivotFieldName and returns
// its underlying struct type. Returns ok=false when the related model has no such field, or when
// the field exists but isn't a struct (the eager loader will surface the mismatched-kind error
// later via OrmRelationPivotFieldNotStruct; here we silently fall back to no struct-driven config).
func pivotFieldStructType(relatedModel any, pivotFieldName string) (reflect.Type, bool) {
	relatedType := reflect.TypeOf(relatedModel)
	if relatedType.Kind() == reflect.Pointer {
		relatedType = relatedType.Elem()
	}
	if relatedType.Kind() != reflect.Struct {
		return nil, false
	}
	field, ok := relatedType.FieldByName(pivotFieldName)
	if !ok || field.Type.Kind() != reflect.Struct {
		return nil, false
	}
	return field.Type, true
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

// resolveMorphValue picks the value to use for a polymorphic *_type column. The model-level
// MorphClass() method takes precedence, then the global morph map (registered via orm.MorphMap),
// then GORM's parsed PrimaryValue (which is either a `polymorphicValue:` tag or the parent's
// table name).
func resolveMorphValue(parent any, gormDefault string) string {
	if v, ok := morphmap.MorphValue(parent); ok {
		return v
	}
	return gormDefault
}

// resolveMorphAlias returns the morph alias for model from MorphClass() / morph map only —
// without falling back to the table name. Used by Associate when we want to know whether the
// owner has an explicit registered alias before defaulting to its table.
func resolveMorphAlias(model any) (string, bool) {
	return morphmap.MorphValue(model)
}

// tryGetRelations returns the parent model's Relations() map regardless of whether the method is
// declared with a value receiver (`func (Foo) Relations()`) or a pointer receiver (`func (*Foo)
// Relations()`). Returns ok=false if neither form satisfies ModelWithRelations.
//
// Mirrors the dual-receiver detection in morphmap.tryMorphClass; both helpers exist because Go
// doesn't pick a receiver style for users — and real-world models freely mix value and pointer
// receivers across methods on the same struct.
func tryGetRelations(parent any) (map[string]contractsorm.Relation, bool) {
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
