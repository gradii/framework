package gorm

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	dbcontract "github.com/gradii/framework/contracts/database/db"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
)

// writeInfo projects a resolved descriptor for the relation write workers of
// database/gorm/relations — relationInfoFromDescriptor with no bound read builder, the shape
// Builder.RelationInfoOf hands out: the write shells resolve the descriptor themselves and cross
// the seam with its projection.
func (r *OrmBuilder) writeInfo(desc *relationDescriptor, parent any) relations.RelationInfo {
	return relationInfoFromDescriptor(desc, nil, parent)
}

// SaveRelation inserts or updates child as a member of parent's relation. Sets child's foreign
// key (and morph_type for MorphOne/MorphMany) from parent's local key, overlays the relation's
// WithAttributes pending attributes (setForeignAttributesForCreate's loop, HasOneOrMany.php:498 —
// the loop create and save share; oracle save.pending-*), then persists child via Query.Save. The
// FK stamp also links child back to parent in memory under the relation's inverse
// name when one is in effect — the write-back SupportsInverseRelations::applyInverseRelationToModel
// applies (oracle: save.same-instance true). For BelongsToMany kinds (Many2Many, MorphToMany,
// MorphedByMany) persists child first, then writes a pivot row linking parent and child.
//
// RelationWriter.Save shell (contracts/database/orm/relation_writer.go:32) — reached through
// Query.Relation(parent, name); the flat Orm.Save(parent, relation, child) form intentionally
// does not exist (relation_writer.go:9). Named "SaveRelation" to avoid clashing with the
// existing single-arg Query.Save(value any) which persists a model directly.
//
// Supported kinds: HasOne, HasMany, MorphOne, MorphMany, Many2Many, MorphToMany, MorphedByMany.
// Other kinds error with OrmRelationKindNotSupported.
func (r *OrmBuilder) SaveRelation(parent any, relation string, child any, recorded ...relations.PivotValue) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(child) {
		return errors.OrmRelationParentNotPointer.Args(child)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
		if err := relations.SetRelationFKOnChild(r, r.writeInfo(desc, parent), parent, child); err != nil {
			return err
		}
		return r.relationWriteSession().Save(child)
	case relKindMany2Many, relKindMorphToMany:
		// Persist child first, then attach via pivot.
		if err := r.relationWriteSession().Save(child); err != nil {
			return err
		}
		childPK, err := readParentColumn(r, child, desc.PivotRelatedRef.PrimaryColumn)
		if err != nil {
			return err
		}
		return r.AttachRelation(parent, relation, []any{childPK}, recorded...)
	default:
		return errors.OrmRelationKindNotSupported.Args("Save", relation, kindName(desc.Kind))
	}
}

// SaveManyRelation is the slice form of SaveRelation. children must be a slice or pointer-to-
// slice of either pointer-to-struct or struct elements. Iterates and bails on first error.
func (r *OrmBuilder) SaveManyRelation(parent any, relation string, children any, recorded ...relations.PivotValue) error {
	rv := reflect.ValueOf(children)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		return errors.OrmRelationKindNotSupported.Args("SaveMany", relation, fmt.Sprintf("children=%T (must be slice)", children))
	}
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		var elem any
		switch item.Kind() {
		case reflect.Pointer:
			elem = item.Interface()
		case reflect.Struct:
			if !item.CanAddr() {
				ptr := reflect.New(item.Type())
				ptr.Elem().Set(item)
				elem = ptr.Interface()
			} else {
				elem = item.Addr().Interface()
			}
		default:
			return errors.OrmRelationKindNotSupported.Args("SaveMany", relation, fmt.Sprintf("children element=%s", item.Kind()))
		}
		if err := r.SaveRelation(parent, relation, elem, recorded...); err != nil {
			return err
		}
	}
	return nil
}

// SaveRelationQuietly is SaveRelation without raising model events — Eloquent's
// HasOneOrMany::saveQuietly (illuminate/database v12.55.1, HasOneOrMany.php:345,
// `Model::withoutEvents(fn () => $this->save($model))`) and BelongsToMany::saveQuietly
// (BelongsToMany.php:1366) on the relation object. The whole save runs on an event-suppressed
// session — the child's insert or update, the pivot attach and the owner touches are all quiet,
// the reach Eloquent's dispatcher swap has (Model::withoutEvents swaps the dispatcher for a
// NullDispatcher around the callback, HasEvents.php:445-460). Everything else is SaveRelation's.
func (r *OrmBuilder) SaveRelationQuietly(parent any, relation string, child any, recorded ...relations.PivotValue) error {
	return r.quietSession().SaveRelation(parent, relation, child, recorded...)
}

// SaveManyRelationQuietly is the slice form of SaveRelationQuietly — HasOneOrMany::saveManyQuietly
// (HasOneOrMany.php:373) and BelongsToMany::saveManyQuietly (BelongsToMany.php:1402): the whole
// slice saves inside one event-suppressed scope.
func (r *OrmBuilder) SaveManyRelationQuietly(parent any, relation string, children any, recorded ...relations.PivotValue) error {
	return r.quietSession().SaveManyRelation(parent, relation, children, recorded...)
}

// SaveRelationWithPivot is SaveRelation with caller-supplied pivot column values for the
// BelongsToMany family. On HasOneOrMany kinds attrs is ignored (no pivot row).
func (r *OrmBuilder) SaveRelationWithPivot(parent any, relation string, child any, attrs map[string]any, recorded ...relations.PivotValue) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(child) {
		return errors.OrmRelationParentNotPointer.Args(child)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
		// No pivot — just delegate to SaveRelation.
		return r.SaveRelation(parent, relation, child, recorded...)
	case relKindMany2Many, relKindMorphToMany:
		// Persist child first, then attach via pivot with attrs.
		if err := r.relationWriteSession().Save(child); err != nil {
			return err
		}
		childPK, err := readParentColumn(r, child, desc.PivotRelatedRef.PrimaryColumn)
		if err != nil {
			return err
		}
		return r.AttachWithPivotRelation(parent, relation, map[any]map[string]any{childPK: attrs}, recorded...)
	default:
		return errors.OrmRelationKindNotSupported.Args("SaveWithPivot", relation, kindName(desc.Kind))
	}
}

// SaveManyRelationWithPivot is the slice form of SaveRelationWithPivot. attrsPerChild is keyed by
// the related PK of each child; an entry may be nil to attach without extra columns.
func (r *OrmBuilder) SaveManyRelationWithPivot(parent any, relation string, children any, attrsPerChild map[any]map[string]any, recorded ...relations.PivotValue) error {
	rv := reflect.ValueOf(children)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		return errors.OrmRelationKindNotSupported.Args("SaveManyWithPivot", relation, fmt.Sprintf("children=%T (must be slice)", children))
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		var elem any
		switch item.Kind() {
		case reflect.Pointer:
			elem = item.Interface()
		case reflect.Struct:
			if !item.CanAddr() {
				ptr := reflect.New(item.Type())
				ptr.Elem().Set(item)
				elem = ptr.Interface()
			} else {
				elem = item.Addr().Interface()
			}
		default:
			return errors.OrmRelationKindNotSupported.Args("SaveManyWithPivot", relation, fmt.Sprintf("children element=%s", item.Kind()))
		}
		// Read child's PK to look up attrs.
		childPK, err := readParentColumn(r, elem, desc.PivotRelatedRef.PrimaryColumn)
		if err != nil {
			return err
		}
		attrs := attrsPerChild[childPK]
		if err := r.SaveRelationWithPivot(parent, relation, elem, attrs, recorded...); err != nil {
			return err
		}
	}
	return nil
}

// --- D2 write-family wrappers ----------------------------------------------------------------
//
// The quiet and OrFail families of the 1:1 alignment round: thin wrappers over the base write
// shells — the quiet family suppresses the builder's event dispatch for the whole call, the Go
// reading of Eloquent's Model::withoutEvents (HasEvents.php:445-460, the dispatcher swap), and
// the OrFail family runs the base call inside a transaction on the connection the relation
// writes run on — Eloquent's `$this->parent->getConnection()->transaction(...)` around each
// InteractsWithPivotTable OrFail method.

// quietSession returns a copy of the builder with its event dispatch suppressed — the flag
// WithoutEvents records (database/gorm/orm_builder.go:3230), written onto a clone. The clone is
// load-bearing: setConditions changes the receiver, and the relation object's own builder must
// stay loud for the calls that follow the quiet one.
func (r *OrmBuilder) quietSession() *OrmBuilder {
	quiet := r.clone()
	conditions := quiet.conditions
	conditions.withoutEvents = true

	return quiet.setConditions(conditions)
}

// relationWriteSession returns the session a relation write persists through: a fresh builder
// that keeps the receiver's event suppression. Eloquent's Model::withoutEvents swaps the
// dispatcher for the whole callback, so a nested write inside a quiet relation write is quiet
// too; a plain fresh() would drop the flag and the nested Save / Create would fire events
// again. Only the session the write shells spawn carries the flag — every other fresh() call
// site keeps its clean conditions.
func (r *OrmBuilder) relationWriteSession() *OrmBuilder {
	session := r.fresh()
	conditions := session.conditions
	// The columns WithZero named travel with the write, so a zero the caller assigned is written.
	conditions.withZero = append([]string(nil), r.conditions.withZero...)
	if !r.conditions.withoutEvents {
		return session.setConditions(conditions)
	}

	conditions.withoutEvents = true

	return session.setConditions(conditions)
}

// relationCreateSession returns the session a relation CREATE persists the related row through —
// relationWriteSession plus the receiver's WithAttributes-recorded pending attributes, so create()'s
// newModelInstance merge reaches the inserted row. Eloquent merges a relation's $pendingAttributes
// into every related row its create path writes: BelongsToMany::create explicitly
// (`array_merge($this->getQuery()->pendingAttributes, $attributes)`, BelongsToMany.php:1395) and the
// HasOneOrMany family through setForeignAttributesForCreate's pending loop (HasOneOrMany.php:498,
// which both create and its foreign-key stamp funnel through). The session is needed only by the
// BelongsToMany create branch: the HasOneOrMany family's create / save / find-and-create paths all
// reach the loop through SetRelationFKOnChild (setForeignAttributesForCreate), and the
// BelongsToMany save path inserts no related row and does not merge — so the copy lives on this
// session rather than on relationWriteSession itself. Oracle v12.55.1, executed (the probe also
// fixes the merge order: the caller's attributes win, array_merge($pending, $attributes)).
func (r *OrmBuilder) relationCreateSession() *OrmBuilder {
	session := r.relationWriteSession()
	if len(r.conditions.pendingAttributes) == 0 {
		return session
	}

	conditions := session.conditions
	conditions.pendingAttributes = r.conditions.pendingAttributes

	return session.setConditions(conditions)
}

// AssociateRelation sets parent's foreign key (and morph_type for MorphTo) to point at owner,
// then persists parent. Supported kinds: BelongsTo, MorphTo. A nil owner clears the foreign key
// (and the morph type) instead of erroring — Laravel's associate(null); a non-nil owner must be a
// pointer to a struct.
//
// RelationWriter.Associate shell (contracts/database/orm/relation_writer.go:122) — reached through Query.Relation(parent, name); the flat Orm.Associate form intentionally does not exist (relation_writer.go:9).
func (r *OrmBuilder) AssociateRelation(parent any, relation string, owner any) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !relationOwnerIsNil(owner) && !isValidParent(owner) {
		return errors.OrmRelationParentNotPointer.Args(owner)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	if relationOwnerIsNil(owner) {
		// Laravel: associate(null) sets the foreign key (and the morph type) to null and clears
		// the relation — the same write dissociate() performs.
		switch desc.Kind {
		case relKindBelongsTo:
			return relations.ApplyDissociate(r, r.writeInfo(desc, parent), parent, false)
		case relKindMorphTo:
			return relations.ApplyDissociate(r, r.writeInfo(desc, parent), parent, true)
		default:
			return errors.OrmRelationKindNotSupported.Args("Associate", relation, kindName(desc.Kind))
		}
	}
	switch desc.Kind {
	case relKindBelongsTo:
		return relations.ApplyAssociate(r, r.writeInfo(desc, parent), parent, owner, false)
	case relKindMorphTo:
		return relations.ApplyAssociate(r, r.writeInfo(desc, parent), parent, owner, true)
	default:
		return errors.OrmRelationKindNotSupported.Args("Associate", relation, kindName(desc.Kind))
	}
}

// DissociateRelation clears parent's foreign key (and morph_type for MorphTo) and persists
// parent. Supported kinds: BelongsTo, MorphTo.
func (r *OrmBuilder) DissociateRelation(parent any, relation string) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindBelongsTo:
		return relations.ApplyDissociate(r, r.writeInfo(desc, parent), parent, false)
	case relKindMorphTo:
		return relations.ApplyDissociate(r, r.writeInfo(desc, parent), parent, true)
	default:
		return errors.OrmRelationKindNotSupported.Args("Dissociate", relation, kindName(desc.Kind))
	}
}

// CreateRelation persists a new related row. For HasOneOrMany kinds (HasOne, HasMany, MorphOne,
// MorphMany) the framework first sets the FK (and morph type column) on dest from parent, then
// inserts. The created child is linked back to parent in memory under the relation's inverse name
// when one is in effect — HasOneOrMany::create applies applyInverseRelationToModel after the save
// (v12.55.1, HasOneOrMany.php:393; oracle: create.same-instance true). dest must be a non-nil
// pointer to a struct of the related type.
//
// RelationWriter.Create shell (contracts/database/orm/relation_writer.go:53) — reached through Query.Relation(parent, name); the flat Orm.Create form intentionally does not exist (relation_writer.go:9).
func (r *OrmBuilder) CreateRelation(parent any, relation string, dest any, recorded ...relations.PivotValue) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
		if err := relations.SetRelationFKOnChild(r, r.writeInfo(desc, parent), parent, dest); err != nil {
			return err
		}
		return r.relationCreateSession().Create(dest)
	case relKindMany2Many, relKindMorphToMany:
		// Create dest first, then attach via pivot.
		if err := r.relationCreateSession().Create(dest); err != nil {
			return err
		}
		childPK, err := readParentColumn(r, dest, desc.PivotRelatedRef.PrimaryColumn)
		if err != nil {
			return err
		}
		return r.AttachRelation(parent, relation, []any{childPK}, recorded...)
	default:
		return errors.OrmRelationKindNotSupported.Args("Create", relation, kindName(desc.Kind))
	}
}

// CreateManyRelation is the slice form of CreateRelation. dests must be a slice or a pointer to a
// slice; iterates and calls CreateRelation per element, bailing on the first error.
func (r *OrmBuilder) CreateManyRelation(parent any, relation string, dests any, recorded ...relations.PivotValue) error {
	rv := reflect.ValueOf(dests)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		return errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), "CreateMany requires a slice")
	}
	for i := 0; i < rv.Len(); i++ {
		elem := rv.Index(i)
		if elem.Kind() != reflect.Pointer {
			elem = elem.Addr()
		}
		if err := r.CreateRelation(parent, relation, elem.Interface(), recorded...); err != nil {
			return err
		}
	}
	return nil
}

// CreateRelationQuietly is CreateRelation without raising model events — Eloquent's
// HasOneOrMany::createQuietly (HasOneOrMany.php:403). The foreign key (and morph type) stamp and
// the insert are CreateRelation's; only the event dispatch is suppressed.
func (r *OrmBuilder) CreateRelationQuietly(parent any, relation string, dest any) error {
	return r.quietSession().CreateRelation(parent, relation, dest)
}

// CreateManyRelationQuietly is the slice form of CreateRelationQuietly —
// HasOneOrMany::createManyQuietly (HasOneOrMany.php:455): the whole slice creates inside one
// event-suppressed scope.
func (r *OrmBuilder) CreateManyRelationQuietly(parent any, relation string, dests any) error {
	return r.quietSession().CreateManyRelation(parent, relation, dests)
}

// ForceCreateRelation persists a new related row for the ForceCreate family. Laravel's
// HasOneOrMany::forceCreate (vendor v12.55.1, HasOneOrMany.php:414-419) and MorphOneOrMany::
// forceCreate (MorphOneOrMany.php:78-84) fold ONLY the foreign key (and the morph type) into the
// attribute array — they never call setForeignAttributesForCreate, so a relation's WithAttributes
// pending pair is deliberately NOT merged — then persist through the related model's forceCreate
// and apply the inverse write-back. The port therefore runs the whole forced path on
// relationWriteSession (no pendingAttributes copy, unlike relationCreateSession), stamps the FK /
// morph type through relations.SetRelationFKOnChildForced and inserts. Only HasOneOrMany /
// MorphOneOrMany carry forceCreate in Laravel — BelongsToMany and the through kinds expose none —
// so an m2m kind is unsupported. Oracle v12.55.1, executed (/tmp/r13/forcecreate_probe.php):
// forceCreate / forceCreateQuietly / forceCreateMany / forceCreateManyQuietly each insert without
// the pending column, while create inserts it.
func (r *OrmBuilder) ForceCreateRelation(parent any, relation string, dest any) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
		if err := relations.SetRelationFKOnChildForced(r, r.writeInfo(desc, parent), parent, dest); err != nil {
			return err
		}
		return r.relationWriteSession().Create(dest)
	default:
		return errors.OrmRelationKindNotSupported.Args("ForceCreate", relation, kindName(desc.Kind))
	}
}

// ForceCreateManyRelation is the slice form of ForceCreateRelation —
// HasOneOrMany::forceCreateMany (vendor v12.55.1, HasOneOrMany.php:466): one forced create per
// record, each folding only the key (and morph type) into its attribute array.
func (r *OrmBuilder) ForceCreateManyRelation(parent any, relation string, dests any) error {
	rv := reflect.ValueOf(dests)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		return errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), "ForceCreateMany requires a slice")
	}
	for i := 0; i < rv.Len(); i++ {
		elem := rv.Index(i)
		if elem.Kind() != reflect.Pointer {
			elem = elem.Addr()
		}
		if err := r.ForceCreateRelation(parent, relation, elem.Interface()); err != nil {
			return err
		}
	}
	return nil
}

// ForceCreateRelationQuietly is ForceCreateRelation with the model events suppressed —
// HasOneOrMany::forceCreateQuietly (vendor v12.55.1, HasOneOrMany.php:427): the whole forced write
// runs inside Model::withoutEvents, so it is otherwise ForceCreateRelation's.
func (r *OrmBuilder) ForceCreateRelationQuietly(parent any, relation string, dest any) error {
	return r.quietSession().ForceCreateRelation(parent, relation, dest)
}

// ForceCreateManyRelationQuietly is the slice form of ForceCreateRelationQuietly —
// HasOneOrMany::forceCreateManyQuietly (vendor v12.55.1, HasOneOrMany.php:483).
func (r *OrmBuilder) ForceCreateManyRelationQuietly(parent any, relation string, dests any) error {
	return r.quietSession().ForceCreateManyRelation(parent, relation, dests)
}

// FindOrNewRelation finds the related row with primary key id. If absent, fills dest with a new
// instance of the related model and pre-sets the FK (and morph type) — but does NOT persist.
// For the BelongsToMany kinds the lookup is the pivot-joined relation read, a hit hydrates the
// Pivot struct, and a miss leaves dest as it was (no foreign key lands on the related row).
// dest must be a pointer to a struct.
func (r *OrmBuilder) FindOrNewRelation(parent any, relation string, id any, dest any) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
		return relations.FindOrNew(r, r.writeInfo(desc, parent), parent, relation, id, dest)
	case relKindMany2Many, relKindMorphToMany:
		// Laravel BelongsToMany::findOrNew (vendor BelongsToMany.php:621) delegates to
		// BelongsToMany::find (:729): where(<related key> = id)->first() through the pivot-joined
		// relation read, which selects the pivot aliases and hydrates the related model's Pivot
		// struct (oracle v12.55.1, executed: select "related_table".*, "pivot_table"."source_id"
		// as "pivot_source_id", "pivot_table"."related_id" as "pivot_related_id" from
		// "related_table" inner join "pivot_table" on "related_table"."id" =
		// "pivot_table"."related_id" where "pivot_table"."source_id" = ? and
		// "related_table"."id" = ? limit 1). On a miss Laravel returns a bare newInstance() --
		// no foreign key lands on the related row -- so a miss leaves dest as it was.
		return r.newRelationQuery(parent, relation).Find(dest, id)
	default:
		return errors.OrmRelationKindNotSupported.Args("FindOrNew", relation, kindName(desc.Kind))
	}
}

// FirstOrNewRelation finds the first related row matching attrs. If absent, fills dest with a new
// instance carrying attrs+values (values win) and the pre-set FK (and morph type) — does NOT
// persist. Supported kinds: HasOne, HasMany, MorphOne, MorphMany, HasOneThrough, HasManyThrough
// (through kinds carry no key on the related row, so newInstance() alone is the whole response),
// Many2Many, MorphToMany (the search is the plain related read and the miss response is the
// attrs+values overlay; the pivot row is a later, explicit write).
func (r *OrmBuilder) FirstOrNewRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any) error {
	defer r.withZeroFor(attrs, values)()
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany,
		relKindHasOneThrough, relKindHasManyThrough:
		return relations.FirstOrNew(r, r.writeInfo(desc, parent), parent, relation, attrs, values, dest)
	case relKindMany2Many, relKindMorphToMany:
		// Laravel BelongsToMany::firstOrNew (vendor BelongsToMany.php:637) searches the related
		// table directly -- $this->related->where($attributes)->first(), the plain read behind the
		// pivot join (oracle v12.55.1, executed: select * from "related_table" where ("attr" = ?)
		// limit 1, no join) -- and on a miss returns newInstance(array_merge($attributes,
		// $values)). The pivot row is a later, explicit write: nothing is attached here and no
		// foreign key is pre-set.
		query := r.relatedQuery(desc.RelatedModel)
		// Sorted keys keep the compiled where list stable across runs (Go map iteration is unordered).
		for _, col := range slices.Sorted(maps.Keys(attrs)) {
			query = query.Where(col, attrs[col])
		}
		found, err := firstFoundOn(query, dest)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		if err := relations.ApplyAttrMap(r, dest, attrs); err != nil {
			return err
		}
		return relations.ApplyAttrMap(r, dest, values)
	default:
		return errors.OrmRelationKindNotSupported.Args("FirstOrNew", relation, kindName(desc.Kind))
	}
}

// firstOrCreateMany2Many is the BelongsToMany arm of the firstOrCreate family — Laravel
// BelongsToMany::firstOrCreate (vendor v12.55.1, BelongsToMany.php:655). It is the three-level
// search FirstOrCreateRelation's Many2Many branch and UpdateOrCreateRelation's both run: the
// pivot-joined relation read (which hydrates the Pivot struct and returns an attached hit
// untouched), the plain related read whose hit is attached inside a savepoint (a
// unique-constraint violation swallowed, "the model was already attached"), and finally
// createOrFirst. The bool reports whether the related row was freshly created — Laravel's
// wasRecentlyCreated(), read off createOrFirst's insert: true only when that insert went
// through, false for every read / attach / read-back path. It is the flag
// BelongsToMany::updateOrCreate branches on (:707-712).
func (r *OrmBuilder) firstOrCreateMany2Many(desc *relationDescriptor, parent any, relation string, attrs, values map[string]any, dest any, recorded ...relations.PivotValue) (bool, error) {
	// Level 1: the pivot-joined relation read — (clone $this)->where($attributes)->first()
	// (:657), which hydrates the Pivot struct; an attached hit comes back untouched.
	found, err := relations.RelationFirst(r, parent, relation, attrs, dest, false)
	if err != nil {
		return false, err
	}
	if found {
		return false, nil
	}
	// Level 2: the global related read — $this->related->where($attributes)->first() (:659) —
	// whose hit is attached (:661, withSavepointIfNeeded); a unique-constraint violation from
	// that attach is swallowed ("the model was already attached").
	query := r.relatedQuery(desc.RelatedModel)
	// Sorted keys keep the compiled where list stable across runs (Go map iteration is unordered).
	for _, col := range slices.Sorted(maps.Keys(attrs)) {
		query = query.Where(col, attrs[col])
	}
	found, err = firstFoundOn(query, dest)
	if err != nil {
		return false, err
	}
	if found {
		childPK, err := readParentColumn(r, dest, desc.PivotRelatedRef.PrimaryColumn)
		if err != nil {
			return false, err
		}
		attachErr := r.withSavepointIfNeeded(func() error {
			return r.AttachRelation(parent, relation, []any{childPK}, recorded...)
		})
		if attachErr != nil && !isUniqueConstraintViolation(attachErr) {
			return false, attachErr
		}

		return false, nil
	}
	// Level 3: both reads missed — Laravel funnels into createOrFirst($attributes, $values,
	// $joining, $touch) (:664), which inserts related + pivot or resolves the unique-violation
	// race.
	return r.createOrFirstMany2Many(desc, parent, relation, attrs, values, dest, recorded...)
}

// createOrFirstMany2Many is the BelongsToMany arm of CreateOrFirstRelation — Laravel
// BelongsToMany::createOrFirst (vendor v12.55.1, BelongsToMany.php:681): the try inserts the
// related row and attaches the pivot row — create(array_merge($attributes, $values), $joining,
// $touch) (:1417), inside withSavepointIfNeeded — and the bool it reports is Laravel's
// wasRecentlyCreated(): true (oracle v12.55.1, executed: `insert into "related_table" ...` then
// `insert into "pivot_table" ("related_id", "source_id") values (?, ?)`, wasRecentlyCreated =
// true, pivot=(no pivot loaded)), false on every catch.
func (r *OrmBuilder) createOrFirstMany2Many(desc *relationDescriptor, parent any, relation string, attrs, values map[string]any, dest any, recorded ...relations.PivotValue) (bool, error) {
	if err := relations.ApplyAttrMap(r, dest, attrs); err != nil {
		return false, err
	}
	if err := relations.ApplyAttrMap(r, dest, values); err != nil {
		return false, err
	}
	err := r.withSavepointIfNeeded(func() error {
		if err := r.relationCreateSession().Create(dest); err != nil {
			return err
		}
		childPK, err := readParentColumn(r, dest, desc.PivotRelatedRef.PrimaryColumn)
		if err != nil {
			return err
		}

		return r.AttachRelation(parent, relation, []any{childPK}, recorded...)
	})
	if err == nil {
		return true, nil
	}
	if !isUniqueConstraintViolation(err) {
		return false, err
	}
	// First catch (:687): read the row the attributes name back — the plain related read
	// $this->related->where($attributes)->first() (:690; oracle: `select * from
	// "related_table" where ("attr" = ?) limit 1`). A miss rethrows into the second catch, whose
	// write-PDO pivot-joined read still runs (:694; oracle v12.55.1, executed: the pivot-joined
	// select after the plain select, then the rethrow), so the write connection gets its chance to
	// surface a row the read connection missed; the violation is reraised only if that read misses
	// too.
	query := r.relatedQuery(desc.RelatedModel)
	// Sorted keys keep the compiled where list stable across runs (Go map iteration is unordered).
	for _, col := range slices.Sorted(maps.Keys(attrs)) {
		query = query.Where(col, attrs[col])
	}
	found, readErr := firstFoundOn(query, dest)
	if readErr != nil {
		return false, readErr
	}
	if !found {
		relookupFound, relookupErr := relations.RelationFirst(r, parent, relation, attrs, dest, true)
		if relookupErr != nil {
			return false, relookupErr
		}
		if !relookupFound {
			return false, errors.OrmUniqueConstraintViolation.Args(err)
		}

		return false, nil
	}
	// The tap's attach (:691) runs in its own savepoint; when it goes through, the read-back
	// instance is returned. A pivot row that already exists trips the pivot's unique constraint —
	// Laravel's attach has no pre-read — and the second catch (:693) —
	// (clone $this)->useWritePdo()->where($attributes)->first(), the pivot-joined relation read
	// on the write PDO — re-reads it; the same catch is taken when the plain read-back missed
	// (above). A miss there rethrows the violation: the attach's when it fired, the first
	// insert's otherwise. Oracle v12.55.1, executed, /tmp/attach_dedup_probe.php D1: related
	// insert attempt (unlogged), plain read-back, pivot insert attempt (unlogged), pivot-joined
	// select; the instance carries the hydrated pivot, wasRecentlyCreated = false. B1/B3 pin the
	// violation itself (attach -> UCV; attachOrFail -> begin,rollback).
	childPK, err := readParentColumn(r, dest, desc.PivotRelatedRef.PrimaryColumn)
	if err != nil {
		return false, err
	}
	parentVal, err := readParentColumn(r, parent, desc.PivotParentRef.PrimaryColumn)
	if err != nil {
		return false, err
	}
	attachErr := r.withSavepointIfNeeded(func() error {
		_, inner := relations.DoAttach(r, r.writeInfo(desc, parent), parentVal, []any{childPK}, nil, recorded...)

		return inner
	})
	if attachErr == nil {
		return false, r.touchIfTouching(desc, parent, parentVal)
	}
	if !isUniqueConstraintViolation(attachErr) {
		return false, attachErr
	}
	found, relookupErr := relations.RelationFirst(r, parent, relation, attrs, dest, true)
	if relookupErr != nil {
		return false, relookupErr
	}
	if !found {
		return false, errors.OrmUniqueConstraintViolation.Args(attachErr)
	}

	return false, nil
}

// FirstOrCreateRelation is FirstOrNewRelation that persists when no matching row exists: the
// lookup runs first, and a miss routes through createOrFirst — the insert is attempted, and a
// unique-constraint violation reads the row the attributes name back instead of failing. For the
// BelongsToMany kinds the lookup is Laravel's two-level search — the pivot-joined relation read,
// then the plain related read whose hit is attached — before the createOrFirst fallback
// (firstOrCreateMany2Many, shared with UpdateOrCreateRelation).
func (r *OrmBuilder) FirstOrCreateRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any, recorded ...relations.PivotValue) error {
	defer r.withZeroFor(attrs, values)()
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany,
		relKindHasOneThrough, relKindHasManyThrough:
		return relations.FirstOrCreate(r, r.writeInfo(desc, parent), parent, relation, attrs, values, dest)
	case relKindMany2Many, relKindMorphToMany:
		_, err := r.firstOrCreateMany2Many(desc, parent, relation, attrs, values, dest, recorded...)

		return err
	default:
		return errors.OrmRelationKindNotSupported.Args("FirstOrCreate", relation, kindName(desc.Kind))
	}
}

// UpdateOrCreateRelation finds the first related row matching attrs (or creates one), then overlays
// values onto it and persists. A miss creates through createOrFirst, and a row read back from a
// unique-constraint race is filled with the values and saved too — Laravel's wasRecentlyCreated()
// branch. Always saves dest on the found and race-read paths. For the BelongsToMany kinds the
// lookup is BelongsToMany::firstOrCreate's three-level search (firstOrCreateMany2Many): an
// attached hit comes back with its Pivot, an unattached hit is attached first, a miss is created
// and attached — and only that fresh insert skips the fill+save (BelongsToMany::updateOrCreate,
// BelongsToMany.php:707).
func (r *OrmBuilder) UpdateOrCreateRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any, recorded ...relations.PivotValue) error {
	defer r.withZeroFor(attrs, values)()
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany,
		relKindHasOneThrough, relKindHasManyThrough:
		return relations.UpdateOrCreate(r, r.writeInfo(desc, parent), parent, relation, attrs, values, dest)
	case relKindMany2Many, relKindMorphToMany:
		// Laravel BelongsToMany::updateOrCreate (vendor v12.55.1, BelongsToMany.php:707) is
		// firstOrCreate followed by a conditional fill+save (:709-712): a row that already
		// existed -- an attached hit, an unattached hit firstOrCreate's level-2 read just
		// linked, or a createOrFirst unique-constraint read-back -- comes back with
		// wasRecentlyCreated() false, so its values are overlaid and it is saved; only a
		// freshly inserted row (wasRecentlyCreated() true, its insert already carrying
		// attrs+values) is left as it stands. Oracle v12.55.1, executed:
		//   hit-attached   -> pivot-joined select, then update (pivot stays hydrated)
		//   hit-unattached -> pivot-joined select, plain select, pivot insert, then update
		//   miss           -> pivot-joined select, plain select, related insert, pivot insert, no update
		created, err := r.firstOrCreateMany2Many(desc, parent, relation, attrs, values, dest, recorded...)
		if err != nil {
			return err
		}
		if created {
			return nil
		}
		// ! wasRecentlyCreated: fill($values) and save(['touch' => false]) -- Eloquent's
		// finishSave touch step is skipped, so the related model's own $touches relations are
		// NOT stamped (oracle v12.55.1, executed: with the related declaring $touches = ['owner'],
		// updateOrCreate's attached-hit and unattached-hit both leave owner.updated_at untouched,
		// where a plain $related->save() stamps it).
		if err := relations.ApplyAttrMap(r, dest, values); err != nil {
			return err
		}
		saver := r.fresh()
		conditions := saver.conditions
		conditions.withoutTouch = true
		conditions.withZero = append([]string(nil), r.conditions.withZero...)
		saver.setConditions(conditions)

		return saver.Save(dest)
	default:
		return errors.OrmRelationKindNotSupported.Args("UpdateOrCreate", relation, kindName(desc.Kind))
	}
}

// --- the many-to-many / pivot shells (S2 step 3) ----------------------------------------------
//
// Every shell below is a one-line forwarder into database/gorm/relations/pivot_workers.go, where
// the attach / detach / sync / toggle / update-existing-pivot engines live as free functions over
// (relations.Builder, relations.RelationInfo). The methods stay here because Go method ownership
// keeps them on *OrmBuilder — they are the relations.Builder write-shell set the relation leaves
// and the OrFail transaction re-dispatch call. Bodies, Laravel anchors and oracle evidence: the
// relations file.

// AttachRelation inserts one pivot row per id in ids, duplicates included — no pivot-existence
// read (Laravel's attach has none): a duplicate row is written, or the pivot's unique constraint
// raises. Supported kinds: Many2Many, MorphToMany, MorphedByMany.
//
// RelationWriter.Attach shell (contracts/database/orm/relation_writer.go:134) — reached through Query.Relation(parent, name); the flat Orm.Attach form intentionally does not exist (relation_writer.go:9).
func (r *OrmBuilder) AttachRelation(parent any, relation string, ids []any, recorded ...relations.PivotValue) error {
	return relations.Attach(r, parent, relation, ids, recorded...)
}

// AttachRelationOrFail is AttachRelation inside a transaction — InteractsWithPivotTable::attachOrFail
// (InteractsWithPivotTable.php:365).
func (r *OrmBuilder) AttachRelationOrFail(parent any, relation string, ids []any, recorded ...relations.PivotValue) error {
	return relations.AttachOrFail(r, parent, relation, ids, recorded...)
}

// AttachWithPivotRelation is Attach with per-row pivot column values.
func (r *OrmBuilder) AttachWithPivotRelation(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...relations.PivotValue) error {
	return relations.AttachWithPivot(r, parent, relation, idsWithAttrs, recorded...)
}

// DetachRelation removes pivot rows linking parent to the given ids. With nil ids, removes all
// pivot rows for parent (and morph type, for polymorphic); an explicitly empty (non-nil) id list
// is Laravel's detach([]) no-op — 0, no statement. Returns the number of rows removed.
func (r *OrmBuilder) DetachRelation(parent any, relation string, ids []any) (int64, error) {
	return relations.Detach(r, parent, relation, ids)
}

// DetachRelationOrFail is DetachRelation inside a transaction — InteractsWithPivotTable::detachOrFail
// (:562).
func (r *OrmBuilder) DetachRelationOrFail(parent any, relation string, ids []any) (int64, error) {
	return relations.DetachOrFail(r, parent, relation, ids)
}

// SyncRelation replaces parent's pivot rows so they exactly match ids: detaches missing entries,
// attaches new ones, leaves existing untouched. Returns the per-id outcome.
//
// RelationWriter.Sync shell (contracts/database/orm/relation_writer.go:150) — reached through Query.Relation(parent, name); the flat Orm.Sync form intentionally does not exist (relation_writer.go:9).
func (r *OrmBuilder) SyncRelation(parent any, relation string, ids []any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.Sync(r, parent, relation, ids, recorded...)
}

// SyncRelationOrFail is SyncRelation inside a transaction — InteractsWithPivotTable::syncOrFail (:157).
func (r *OrmBuilder) SyncRelationOrFail(parent any, relation string, ids []any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.SyncOrFail(r, parent, relation, ids, recorded...)
}

// SyncWithoutDetachingRelation is SyncRelation minus the detach step.
func (r *OrmBuilder) SyncWithoutDetachingRelation(parent any, relation string, ids []any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.SyncWithoutDetaching(r, parent, relation, ids, recorded...)
}

// SyncWithoutDetachingRelationOrFail is SyncWithoutDetachingRelation inside a transaction —
// InteractsWithPivotTable::syncWithoutDetachingOrFail (:170, `$this->syncOrFail($ids, false)`).
func (r *OrmBuilder) SyncWithoutDetachingRelationOrFail(parent any, relation string, ids []any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.SyncWithoutDetachingOrFail(r, parent, relation, ids, recorded...)
}

// ToggleRelation attaches missing entries and detaches existing ones.
func (r *OrmBuilder) ToggleRelation(parent any, relation string, ids []any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.Toggle(r, parent, relation, ids, recorded...)
}

// ToggleRelationOrFail is ToggleRelation inside a transaction — InteractsWithPivotTable::toggleOrFail
// (:75).
func (r *OrmBuilder) ToggleRelationOrFail(parent any, relation string, ids []any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.ToggleOrFail(r, parent, relation, ids, recorded...)
}

// SyncRelationWithPivot is SyncRelation with per-ID pivot column values. The map key is the
// related id; the map value is the column-name-to-value map applied to that pivot row. For
// existing pivot rows with non-empty attrs, updates the pivot columns (reported in
// SyncResult.Updated). Mirrors fedaco's sync(map).
func (r *OrmBuilder) SyncRelationWithPivot(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.SyncWithPivot(r, parent, relation, idsWithAttrs, recorded...)
}

// SyncRelationWithPivotValues applies the same pivot column values to all ids. Mirrors fedaco's
// syncWithPivotValues.
func (r *OrmBuilder) SyncRelationWithPivotValues(parent any, relation string, ids []any, pivotValues map[string]any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.SyncWithPivotValues(r, parent, relation, ids, pivotValues, recorded...)
}

// SyncRelationWithPivotValuesOrFail is SyncRelationWithPivotValues inside a transaction —
// InteractsWithPivotTable::syncWithPivotValuesOrFail (:200).
func (r *OrmBuilder) SyncRelationWithPivotValuesOrFail(parent any, relation string, ids []any, pivotValues map[string]any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.SyncWithPivotValuesOrFail(r, parent, relation, ids, pivotValues, recorded...)
}

// SyncWithoutDetachingRelationWithPivot is SyncRelationWithPivot minus the detach step.
func (r *OrmBuilder) SyncWithoutDetachingRelationWithPivot(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.SyncWithoutDetachingWithPivot(r, parent, relation, idsWithAttrs, recorded...)
}

// ToggleRelationWithPivot is ToggleRelation with per-ID pivot column values for newly attached rows.
func (r *OrmBuilder) ToggleRelationWithPivot(parent any, relation string, idsWithAttrs map[any]map[string]any, recorded ...relations.PivotValue) (*dbcontract.SyncResult, error) {
	return relations.ToggleWithPivot(r, parent, relation, idsWithAttrs, recorded...)
}

// UpdateExistingPivotRelation updates pivot columns for an already-attached id. When
// pivotTimestamps is enabled and attrs doesn't already set updated_at, injects time.Now() into
// the update map. No-op (returns 0) if no matching pivot row exists.
func (r *OrmBuilder) UpdateExistingPivotRelation(parent any, relation string, id any, attrs map[string]any) (int64, error) {
	return relations.UpdateExistingPivot(r, parent, relation, id, attrs)
}

// UpdateExistingPivotRelationOrFail is UpdateExistingPivotRelation inside a transaction —
// InteractsWithPivotTable::updateExistingPivotOrFail (:299).
func (r *OrmBuilder) UpdateExistingPivotRelationOrFail(parent any, relation string, id any, attrs map[string]any) (int64, error) {
	return relations.UpdateExistingPivotOrFail(r, parent, relation, id, attrs)
}

// pivotTableQuery returns a fresh query addressing the given table directly, so pivot reads,
// inserts, updates and deletes compile through the framework builder instead of being handed to
// GORM. The name is bare, like every table name the ORM holds, so the grammar applies the connection
// prefix once when the statement is rendered.
func (r *OrmBuilder) pivotTableQuery(table string) *OrmBuilder {
	child := r.fresh()
	child.query.From(table, "")

	return child
}

// pivotWhere appends one predicate to the query and returns it. An equality passes the dialect-quoted
// column and a "?" placeholder; an IN list passes the unquoted qualified column with the id slice as
// its single binding, which the compiler renders as a typed IN clause.
func (r *OrmBuilder) pivotWhere(query string, args ...any) *OrmBuilder {
	return r.addWhere(contractsdriver.Where{Query: query, Args: args}).(*OrmBuilder)
}

// insertPivotRows inserts the given rows into the pivot table through the framework builder and runs
// the statement on the connection pool. It replaces the GORM Create the pivot writers used.
func (r *OrmBuilder) insertPivotRows(table string, rows []map[string]any) error {
	child := r.pivotTableQuery(table)

	statement, bindings, err := child.pivotInsertStatement(rows)
	if err != nil {
		return err
	}

	_, err = child.execStatement(statement, bindings)

	return err
}

// pivotInsertStatement compiles the pivot insert. The rows are given the union of their columns first:
// per-id pivot attributes (AttachWithPivotRelation, and sync's attach map) let one row carry a column
// another does not, and the grammar would otherwise key the whole statement off the first row and drop
// the later column, losing its value on the rows that do not carry it in the same position.
func (r *OrmBuilder) pivotInsertStatement(rows []map[string]any) (string, []any, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	statement, bindings := builder.ToInsertSQL(uniformInsertRecords(rows))
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return statement, bindings, nil
}

// inverseWriteBackName reports the inverse relation name a relation write links the child back to —
// the live chaperone state when this builder carries one, the declaration otherwise: the seam's
// InverseWriteBackName over the descriptor's declared name (the eager loader's write-back reads it
// here with the descriptor in hand).
func (r *OrmBuilder) inverseWriteBackName(desc *relationDescriptor) string {
	return r.InverseWriteBackName(desc.Inverse)
}

// touchIfTouching bumps parent's updated_at when desc.touches is true — the descriptor-side entry
// of relations.TouchIfTouching for the CreateOrFirst pivot arm, which holds the descriptor.
func (r *OrmBuilder) touchIfTouching(desc *relationDescriptor, parent any, parentVal any) error {
	return relations.TouchIfTouching(r, r.writeInfo(desc, parent), parent, parentVal)
}

// kindName returns a human-friendly name for a relationKind, used in error messages.
func kindName(k relationKind) string {
	switch k {
	case relKindHasOne:
		return "hasOne"
	case relKindHasMany:
		return "hasMany"
	case relKindBelongsTo:
		return "belongsTo"
	case relKindMany2Many:
		return "many2Many"
	case relKindMorphOne:
		return "morphOne"
	case relKindMorphMany:
		return "morphMany"
	case relKindMorphTo:
		return "morphTo"
	case relKindMorphToMany:
		return "morphToMany"
	case relKindHasOneThrough:
		return "hasOneThrough"
	case relKindHasManyThrough:
		return "hasManyThrough"
	}
	return fmt.Sprintf("kind=%d", k)
}

// castKey forwards to relations.CastKey — the key normalisation the sync cores moved with (S2
// step 3) and the flat pluck still shares (builder_shortcuts.go pluckValue).
func castKey(v any, t reflect.Type) any {
	return relations.CastKey(v, t)
}

// CreateOrFirstRelation attempts the insert immediately — with no lookup first, Laravel's
// createOrFirst contract — and, when the insert trips a unique constraint, reads the row attrs
// name back (over the write connection for the HasOneOrMany kinds, on the default connection for
// the through kinds) and returns it. For the BelongsToMany kinds the insert is the related row
// plus the pivot attach (BelongsToMany::create), the catch reads the related row back plainly and
// attaches it, and a violation from that attach (an already-attached pivot row trips the pivot's
// unique constraint) re-reads through the pivot-joined relation on the write connection,
// hydrating the Pivot struct.
// Supported kinds: HasOne, HasMany, MorphOne, MorphMany, HasOneThrough, HasManyThrough,
// Many2Many, MorphToMany.
//
// RelationWriter.CreateOrFirst shell (contracts/database/orm/relation_writer.go:108) — reached through Query.Relation(parent, name); the flat Orm.CreateOrFirst form intentionally does not exist (relation_writer.go:9).
func (r *OrmBuilder) CreateOrFirstRelation(parent any, relation string, attrs map[string]any, values map[string]any, dest any, recorded ...relations.PivotValue) error {
	defer r.withZeroFor(attrs, values)()
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany,
		relKindHasOneThrough, relKindHasManyThrough:
		_, err := relations.CreateOrFirstEngine(r, r.writeInfo(desc, parent), parent, relation, attrs, values, dest, attrs)
		return err
	case relKindMany2Many, relKindMorphToMany:
		_, err := r.createOrFirstMany2Many(desc, parent, relation, attrs, values, dest, recorded...)

		return err
	default:
		return errors.OrmRelationKindNotSupported.Args("CreateOrFirst", relation, kindName(desc.Kind))
	}
}

// MakeRelation returns an unsaved new instance of the related model carrying attrs with the parent
// key (and morph type) pre-set from parent — Laravel's HasOneOrMany::make, which never saves. The
// made child is linked back to parent in memory under the relation's inverse name when one is in
// effect (HasOneOrMany::make applies applyInverseRelationToModel, v12.55.1,
// HasOneOrMany.php:65; oracle: make.same-instance true). Supported kinds: HasOne, HasMany, MorphOne,
// MorphMany.
//
// RelationWriter.Make shell (contracts/database/orm/relation_writer.go:65) — reached through Query.Relation(parent, name); the flat Orm.Make form intentionally does not exist (relation_writer.go:9).
func (r *OrmBuilder) MakeRelation(parent any, relation string, attrs map[string]any, dest any) error {
	defer r.withZeroFor(attrs)()
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	if !isValidParent(dest) {
		return errors.OrmRelationParentNotPointer.Args(dest)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
		if err := relations.ApplyAttrMap(r, dest, attrs); err != nil {
			return err
		}
		return relations.SetRelationFKOnChild(r, r.writeInfo(desc, parent), parent, dest)
	default:
		return errors.OrmRelationKindNotSupported.Args("Make", relation, kindName(desc.Kind))
	}
}

// MakeManyRelation is the slice form of MakeRelation: one unsaved instance per record, in record
// order, appended to dests — a pointer to a slice of structs (or pointers to structs).
func (r *OrmBuilder) MakeManyRelation(parent any, relation string, records []map[string]any, dests any) error {
	if !isValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}
	slice := reflect.ValueOf(dests)
	if slice.Kind() != reflect.Pointer || slice.IsNil() || slice.Elem().Kind() != reflect.Slice {
		return errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", dests), "MakeMany requires a pointer to a slice")
	}
	slice = slice.Elem()
	elementType := slice.Type().Elem()
	pointerElements := elementType.Kind() == reflect.Pointer
	for _, record := range records {
		var element reflect.Value
		if pointerElements {
			element = reflect.New(elementType.Elem())
		} else {
			element = reflect.New(elementType)
		}
		if err := r.MakeRelation(parent, relation, record, element.Interface()); err != nil {
			return err
		}
		if pointerElements {
			slice.Set(reflect.Append(slice, element))
		} else {
			slice.Set(reflect.Append(slice, element.Elem()))
		}
	}
	return nil
}

// UpsertRelation merges the parent key (and the morph type, for MorphOne/MorphMany) into every row
// and delegates to the query-level upsert — the rows HasOneOrMany::upsert / MorphOneOrMany::upsert
// hand the query. Supported kinds: HasOne, HasMany, MorphOne, MorphMany.
//
// RelationWriter.Upsert shell (contracts/database/orm/relation_writer.go:114) — reached through Query.Relation(parent, name); the flat Orm.Upsert form intentionally does not exist (relation_writer.go:9).
func (r *OrmBuilder) UpsertRelation(parent any, relation string, values []map[string]any, uniqueBy []string, update ...any) (*dbcontract.Result, error) {
	if !isValidParent(parent) {
		return nil, errors.OrmRelationParentNotPointer.Args(parent)
	}
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return nil, err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
	default:
		return nil, errors.OrmRelationKindNotSupported.Args("Upsert", relation, kindName(desc.Kind))
	}
	if len(values) == 0 {
		return &dbcontract.Result{}, nil
	}
	if len(desc.References) == 0 {
		return nil, errors.OrmRelationUnsupported.Args(relation, desc.ParentTable, "no references")
	}

	parentVal, err := readParentColumn(r, parent, desc.References[0].PrimaryColumn)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(values))
	for _, row := range values {
		filled := make(map[string]any, len(row)+2)
		for key, value := range row {
			filled[key] = value
		}
		filled[desc.References[0].ForeignColumn] = parentVal
		if desc.Kind == relKindMorphOne || desc.Kind == relKindMorphMany {
			filled[desc.MorphTypeColumn] = desc.MorphValue
		}
		rows = append(rows, filled)
	}

	query, ok := r.relatedQuery(desc.RelatedModel).(*OrmBuilder)
	if !ok {
		return nil, errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), "no related query")
	}

	return query.Upsert(rows, uniqueBy, update...)
}

// firstFoundOn runs a query as a single-row read and reports the found flag Query.First discards:
// an empty result is not an error, and the find-then-write family must branch on the flag, the way
// Laravel branches on a null first().
func firstFoundOn(query any, dest any) (bool, error) {
	built, ok := query.(*OrmBuilder)
	if !ok {
		return false, errors.OrmRelationUnsupported.Args("", fmt.Sprintf("%T", query), "no single-row read")
	}
	return built.firstRow(dest)
}

// relationOwnerIsNil reports whether an Associate owner is nil — the untyped nil or a nil pointer
// of any pointer type; Laravel receives both shapes as null.
func relationOwnerIsNil(owner any) bool {
	if owner == nil {
		return true
	}
	reflected := reflect.ValueOf(owner)

	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}
