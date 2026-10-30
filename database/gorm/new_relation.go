package gorm

import (
	"fmt"
	"reflect"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// Related is the public OrmBuilder-level entry point. The Orm-level entry (Orm.Related) delegates
// here. Returns a fresh OrmBuilder pre-scoped to the related rows for parent.relation.
func (r *OrmBuilder) Related(parent any, relation string) contractsorm.Query {
	return r.newRelationQuery(parent, relation)
}

// newRelationQuery returns a fresh OrmBuilder pre-scoped to the related rows for parent.relation.
// Mirrors fedaco's model.NewRelation('foo') for the read path. Caller can chain Where / OrderBy
// / Get / etc. on the returned OrmBuilder. Write operations live on RelationWriter (see OrmBuilder.Relation).
//
// Public entry: Orm.Related delegates here on a fresh-session *OrmBuilder.
func (r *OrmBuilder) newRelationQuery(parent any, relation string) contractsorm.Query {
	if !isValidParent(parent) {
		return r.guardedQuery(errors.OrmRelationParentNotPointer.Args(parent))
	}

	desc, err := resolveRelation(parent, relation)
	if err != nil {
		return r.guardedQuery(err)
	}

	var built contractsorm.Query
	switch desc.kind {
	case relKindHasOne, relKindHasMany:
		built = r.newHasOneOrManyQuery(parent, desc)
	case relKindBelongsTo:
		built = r.newBelongsToQuery(parent, desc)
	case relKindMorphOne, relKindMorphMany:
		built = r.newMorphOneOrManyQuery(parent, desc)
	case relKindMorphTo:
		built = r.newMorphToQuery(parent, desc)
	case relKindMany2Many:
		built = r.newMany2ManyQuery(parent, desc, false)
	case relKindMorphToMany:
		built = r.newMany2ManyQuery(parent, desc, true)
	case relKindHasOneThrough, relKindHasManyThrough:
		built = r.newThroughQuery(parent, desc)
	default:
		return r.guardedQuery(errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), fmt.Sprintf("kind=%d", desc.kind)))
	}
	// Apply the relation's default scope on top of the per-kind constraints. Caller still gets
	// a chainable OrmBuilder and can layer further conditions via Where / OrderBy / etc.
	if desc.onQuery != nil {
		built = desc.onQuery(built)
	}
	return built
}

// --- per-kind builders -----------------------------------------------------

// newHasOneOrManyQuery: SELECT * FROM <related> WHERE <related>.<id_col> = parent.<local_key>.
func (r *OrmBuilder) newHasOneOrManyQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	if len(desc.references) == 0 {
		return r.guardedQuery(errors.OrmRelationUnsupported.Args(desc.name, desc.parentTable, "no references"))
	}
	ref := desc.references[0]
	parentVal, err := readParentColumn(r, parent, ref.primaryColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	return r.relatedQuery(desc.relatedModel).Where(ref.foreignColumn, parentVal)
}

// newBelongsToQuery: SELECT * FROM <related> WHERE <related>.<owner_key> = parent.<fk_col>.
func (r *OrmBuilder) newBelongsToQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	if len(desc.references) == 0 {
		return r.guardedQuery(errors.OrmRelationUnsupported.Args(desc.name, desc.parentTable, "no references"))
	}
	ref := desc.references[0]
	parentVal, err := readParentColumn(r, parent, ref.foreignColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	return r.relatedQuery(desc.relatedModel).Where(ref.primaryColumn, parentVal)
}

// newMorphOneOrManyQuery: HasMany shape + WHERE <related>.<type_col> = desc.morphValue.
func (r *OrmBuilder) newMorphOneOrManyQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	if len(desc.references) == 0 {
		return r.guardedQuery(errors.OrmRelationUnsupported.Args(desc.name, desc.parentTable, "no references"))
	}
	ref := desc.references[0]
	parentVal, err := readParentColumn(r, parent, ref.primaryColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	return r.relatedQuery(desc.relatedModel).
		Where(ref.foreignColumn, parentVal).
		Where(desc.morphTypeColumn, desc.morphValue)
}

// newMorphToQuery resolves the related model per-row from the parent's <type_col> via the morph
// map, then issues SELECT * FROM <resolved> WHERE <resolved>.<owner_key> = parent.<id_col>.
//
// If the parent's *_type is empty or unregistered, the returned OrmBuilder is guarded so subsequent
// Get / First yields no rows without a database round-trip.
func (r *OrmBuilder) newMorphToQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	morphType, err := readParentColumn(r, parent, desc.morphTypeColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	morphID, err := readParentColumn(r, parent, desc.morphIDColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	typeStr := morphTypeToString(morphType)
	if typeStr == "" {
		return r.zeroRowQuery()
	}
	sample := morphmap.Find(typeStr)
	if sample == nil {
		return r.guardedQuery(errors.OrmMorphTypeUnknown.Args(typeStr))
	}
	ownerKey := desc.morphOwnerKey
	if ownerKey == "" {
		ownerKey = "id"
	}
	return r.relatedQuery(sample).Where(ownerKey, morphID)
}

// newMany2ManyQuery: SELECT <related>.* FROM <related> INNER JOIN <pivot>
//
//	ON <related>.<rel_pk> = <pivot>.<rel_fk>
//	WHERE <pivot>.<parent_fk> = parent.<pk>  [AND <pivot>.<type_col> = morphValue]
//
// Pivot columns are not surfaced — call sites that need them should add Select / a future
// WithPivot helper.
func (r *OrmBuilder) newMany2ManyQuery(parent any, desc *relationDescriptor, isMorph bool) contractsorm.Query {
	parentVal, err := readParentColumn(r, parent, desc.pivotParentRef.primaryColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	relatedTable := desc.relatedTable
	pivotTable := desc.pivotTable

	q := r.relatedQuery(desc.relatedModel).
		Join(r.tableRef(pivotTable)+" ON "+
			r.ident(pivotTable, desc.pivotRelatedRef.foreignColumn)+" = "+
			r.ident(relatedTable, desc.pivotRelatedRef.primaryColumn)).
		WhereRaw(r.ident(pivotTable, desc.pivotParentRef.foreignColumn)+" = ?", parentVal)
	if isMorph {
		q = q.WhereRaw(r.ident(pivotTable, desc.morphTypeColumn)+" = ?", desc.morphValue)
	}
	return applyOnPivotQueryToQuery(r.queryGrammar, q, desc)
}

// newThroughQuery:
//
//	SELECT <related>.* FROM <related>
//	INNER JOIN <through> ON <related>.<second_key> = <through>.<second_local_key>
//	WHERE <through>.<first_key> = parent.<local_key>
func (r *OrmBuilder) newThroughQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	parentVal, err := readParentColumn(r, parent, desc.localKey)
	if err != nil {
		return r.guardedQuery(err)
	}
	return r.relatedQuery(desc.relatedModel).
		Join(r.tableRef(desc.throughTable)+" ON "+
			r.ident(desc.relatedTable, desc.secondKey)+" = "+
			r.ident(desc.throughTable, desc.secondLocalKey)).
		WhereRaw(r.ident(desc.throughTable, desc.firstKey)+" = ?", parentVal)
}

// --- helpers ---------------------------------------------------------------

// relatedQuery returns a fresh OrmBuilder bound to the given related model, sharing connection /
// config / context with the receiver. Subsequent unqualified Where("col", v) calls default to
// the related table's column space.
func (r *OrmBuilder) relatedQuery(related any) contractsorm.Query {
	q := r.fresh()
	return q.Model(related)
}

// guardedQuery returns a fresh OrmBuilder that reports err from whichever terminal runs on it, without a
// database round-trip. The error travels on the query rather than on the connection handle.
func (r *OrmBuilder) guardedQuery(err error) contractsorm.Query {
	return r.fresh().withError(err)
}

// zeroRowQuery returns an OrmBuilder whose Get/First yields no rows without a database round-trip —
// the WHERE evaluates to a guaranteed-false condition.
func (r *OrmBuilder) zeroRowQuery() contractsorm.Query {
	return r.fresh().WhereRaw("1 = 0")
}

// readParentColumn reads the value of the given DB column from parent via GORM's parsed schema.
func readParentColumn(r *OrmBuilder, parent any, dbColumn string) (any, error) {
	parentSchema, err := parseSchema(parent)
	if err != nil {
		return nil, err
	}
	field, ok := parentSchema.FieldsByDBName[dbColumn]
	if !ok {
		return nil, errors.OrmRelationUnsupported.Args("", parentSchema.Name, "no parent field for "+dbColumn)
	}
	rv := reflect.ValueOf(parent)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	val, _ := field.ValueOf(r.ctx, rv)
	return val, nil
}

// morphTypeToString coerces a morph_type column value (which the driver may return as string,
// []byte, or sql.NullString) into a plain string.
func morphTypeToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	}
	return fmt.Sprint(v)
}

// isValidParent returns true if v is a non-nil pointer to a struct.
func isValidParent(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false
	}
	return rv.Elem().Kind() == reflect.Struct
}
