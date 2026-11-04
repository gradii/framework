package gorm

import (
	"fmt"
	"reflect"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// Related is the public OrmBuilder-level entry point. The Orm-level entry (Orm.Related) delegates
// here. Returns the relation builder object pre-scoped to the related rows for parent.relation.
func (r *OrmBuilder) Related(parent any, relation string) contractsorm.RelationQuery {
	return r.newRelationQuery(parent, relation)
}

// newRelationQuery returns the relation builder object pre-scoped to the related rows for
// parent.relation. Mirrors fedaco's model.NewRelation('foo') for the read path. Caller can chain
// Where / OrderBy / Get / etc. on it; the object also carries the relation semantics (see
// contractsorm.RelationQuery) and forwards writes to the relation write shells. Write operations
// also remain reachable through RelationWriter (see OrmBuilder.Relation).
//
// A relation that declares an Inverse carries the chaperone write-back: the returned builder
// records the parent and the inverse name, so every model-row read it runs — Get / First and the
// chunked pages — stores the parent on each result model, the after-query callback Eloquent's
// SupportsInverseRelations::chaperone registers on the relation's builder.
//
// Public entry: Orm.Related delegates here on a fresh-session *OrmBuilder.
func (r *OrmBuilder) newRelationQuery(parent any, relation string) contractsorm.RelationQuery {
	if !isValidParent(parent) {
		return r.guardedRelation(errors.OrmRelationParentNotPointer.Args(parent), parent, relation)
	}

	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return r.guardedRelation(err, parent, relation)
	}

	var built contractsorm.Query
	switch desc.Kind {
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
		return r.guardedRelation(errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), fmt.Sprintf("kind=%d", desc.Kind)), parent, relation)
	}
	// Apply the relation's default scope on top of the per-kind constraints. Caller still gets
	// a chainable OrmBuilder and can layer further conditions via Where / OrderBy / etc.
	if desc.OnQuery != nil {
		built = desc.OnQuery(built)
	}
	// A one-of-many relation reads through the aggregate join Laravel's ofMany installs, so Get /
	// First / Count / Exists / WhereKey all see the one-of-many rows.
	if b, ok := built.(*OrmBuilder); ok && b.conditions.oneOfMany != nil {
		if err := r.applyOneOfManyToReadPath(b, parent, desc); err != nil {
			return r.guardedRelation(err, parent, relation)
		}
	}
	// The relation's Inverse declaration rides on the builder as the after-query write-back,
	// the state Eloquent's chaperone() keeps on the relation. The callback reads the actual
	// result set, so a query the caller widened links every row it returns.
	if desc.Inverse != "" {
		if b, ok := built.(*OrmBuilder); ok {
			b.inverseChaperone = &inverseChaperoneState{
				relation: desc.Inverse,
				parent:   reflect.ValueOf(parent).Elem(),
			}
		}
	}

	builder, ok := built.(*OrmBuilder)
	if !ok {
		// A relation scope that returned a foreign Query implementation cannot be projected into
		// the relation object; surface it as a guarded handle instead of mis-typing the wrap.
		return r.guardedRelation(errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", built), "relation scope returned a non-builder query"), parent, relation)
	}

	return relations.Wrap(builder, parent, relation, relationInfoFromDescriptor(desc, builder, parent), relationHooks(builder, desc))
}

// inverseChaperoneState is the after-query write-back a relation read carries once it runs: the
// parent the relation was resolved from and the related model's declared inverse relation name.
// It is the Go form of the state SupportsInverseRelations::$inverseRelationship holds — while
// set, every execution of the relation query links the returned models back to the parent.
type inverseChaperoneState struct {
	relation string
	parent   reflect.Value
}

// InverseRelationship reports the inverse relation name the after-query write-back links results
// to — "" when none is in effect (the relation was declared without an inverse, or
// WithoutInverseChaperone cleared it). The builder half of the relation object's
// GetInverseRelationship (contracts.QueryWithInverseChaperone).
func (r *OrmBuilder) InverseRelationship() string {
	if r.inverseChaperone == nil {
		return ""
	}

	return r.inverseChaperone.relation
}

// WithoutInverseChaperone clears the inverse write-back state — the builder half of the relation
// object's WithoutChaperone / WithoutInverse (SupportsInverseRelations::withoutChaperone,
// Concerns/SupportsInverseRelations.php:151). Cleared stays distinguishable from never installed:
// the sentinel keeps a non-nil state whose relation is "", so a write on this builder stops
// linking the created child (inverseWriteBackName, database/gorm/relation_writes.go) while a flat
// write on a declared inverse — no state installed — still does.
func (r *OrmBuilder) WithoutInverseChaperone() {
	if r.inverseChaperone != nil {
		r.inverseChaperone = &inverseChaperoneState{}
	}
}

// Compile assertion: the builder carries the inverse state the relation objects reach through
// contracts.QueryWithInverseChaperone.
var _ contractsorm.QueryWithInverseChaperone = (*OrmBuilder)(nil)

// applyInverseChaperone runs the relation read's after-query write-back over the models dest now
// holds — the callback Eloquent's SupportsInverseRelations::chaperone registers, which
// Builder::get() runs over the whole result collection once the rows have hydrated (Builder::first(),
// a limit(1)->get(), inherits). The callback sees the actual result set, so a widened query links
// every row it returns, including rows whose foreign key belongs to another parent (oracle: a
// posts()->orWhere(...)->get() links all 5 rows of the result to the same parent instance).
// Queries that are not relation reads carry no state and this is a no-op, as are non-model
// destinations and empty results.
func (r *OrmBuilder) applyInverseChaperone(dest any) error {
	state := r.inverseChaperone
	if state == nil || state.relation == "" {
		return nil
	}

	value := reflect.ValueOf(dest)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil
	}
	value = value.Elem()

	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := r.chaperoneResultModel(state, value.Index(index)); err != nil {
				return err
			}
		}

		return nil
	case reflect.Struct:
		return r.chaperoneResultModel(state, value)
	}

	return nil
}

// chaperoneResultModel stores the parent on one result model's inverse relation — the per-model
// half of the callback, SupportsInverseRelations::applyInverseRelationToModel, through the same
// setter the loaders use. Entries that are not a struct or a non-nil struct pointer are skipped,
// the way the PHP callback touches only the Model instances of a result.
func (r *OrmBuilder) chaperoneResultModel(state *inverseChaperoneState, model reflect.Value) error {
	if model.Kind() == reflect.Pointer {
		if model.IsNil() {
			return nil
		}
		model = model.Elem()
	}
	if model.Kind() != reflect.Struct {
		return nil
	}

	return setRelationField(model, state.relation, []reflect.Value{inverseParentRef(state.parent)})
}

// applyOneOfManyToReadPath installs the one-of-many join on the relation read path, mirroring
// HasOne / MorphOne / HasOneThrough's ofMany: the aggregate subquery carries the relation
// constraints (foreign key = parent, foreign key is not null; a through relation's through key =
// parent instead) plus anything the relation's own of-many chain wrote, the outer query re-states
// the relation constraints, and the select is pruned to the related table so the aggregate column
// cannot scan into the model. Everything the caller chains afterwards — WhereKey, Count, Exists —
// sees the joined read.
func (r *OrmBuilder) applyOneOfManyToReadPath(built *OrmBuilder, parent any, desc *relationDescriptor) error {
	var parentColumn string
	switch desc.Kind {
	case relKindHasOne, relKindMorphOne:
		if len(desc.References) == 0 {
			return nil
		}
		parentColumn = desc.References[0].PrimaryColumn
	case relKindHasOneThrough:
		parentColumn = desc.LocalKey
	default:
		return nil
	}

	parentValue, err := readParentColumn(r, parent, parentColumn)
	if err != nil {
		return err
	}

	join, args, err := r.oneOfManyJoin(built, desc, built.conditions.oneOfMany, parentValue, true)
	if err != nil {
		return err
	}
	if join == "" {
		return nil
	}

	built.query.JoinRaw(join, args, "inner")

	if len(built.query.Selects()) == 0 {
		built.query.SelectRaw(r.ident(desc.RelatedTable, "*"), nil)
	}

	return nil
}

// throughJoinFragment renders the join Laravel's performJoin gives a through relation's related
// query — the oracle compiles it as `inner join "intermediates" on "intermediates"."id" =
// "prices"."intermediate_id"`. The caller supplies the join keyword.
func (r *OrmBuilder) throughJoinFragment(desc *relationDescriptor) string {
	return r.tableRef(desc.ThroughTable) + " on " +
		r.ident(desc.ThroughTable, desc.SecondLocalKey) + " = " +
		r.ident(desc.RelatedTable, desc.SecondKey)
}

// --- per-kind builders -----------------------------------------------------

// newHasOneOrManyQuery: SELECT * FROM <related> WHERE <related>.<fk> = parent.<local_key> AND
// <related>.<fk> is not null. Mirrors HasOneOrMany::addConstraints: the foreign key is qualified
// and a null key never matches (oracle, sqlite: `select * from "rel_books" where
// "rel_books"."user_id" = ? and "rel_books"."user_id" is not null`).
func (r *OrmBuilder) newHasOneOrManyQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	if len(desc.References) == 0 {
		return r.guardedQuery(errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, "no references"))
	}
	ref := desc.References[0]
	parentVal, err := readParentColumn(r, parent, ref.PrimaryColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	foreign := desc.RelatedTable + "." + ref.ForeignColumn
	return r.relatedQuery(desc.RelatedModel).
		Where(foreign, parentVal).
		WhereNotNull(foreign)
}

// newBelongsToQuery: SELECT * FROM <related> WHERE <related>.<owner_key> = parent.<fk_col>. The
// owner key is qualified, the shape BelongsTo::addConstraints installs — oracle (illuminate/
// database v12.55.1, sqlite, `$post->user()->toSql()`): `select * from "users" where
// "users"."id" = ?`.
func (r *OrmBuilder) newBelongsToQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	if len(desc.References) == 0 {
		return r.guardedQuery(errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, "no references"))
	}
	ref := desc.References[0]
	parentVal, err := readParentColumn(r, parent, ref.ForeignColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	return r.relatedQuery(desc.RelatedModel).Where(desc.RelatedTable+"."+ref.PrimaryColumn, parentVal)
}

// newMorphOneOrManyQuery: HasMany shape + WHERE <related>.<type_col> = desc.morphValue. Mirrors
// MorphOneOrMany::addConstraints: the morph type filter comes first, then the qualified foreign key
// with its not-null guard (oracle, sqlite: `select * from "rel_logos" where "rel_logos"."logoable_type"
// = ? and "rel_logos"."logoable_id" = ? and "rel_logos"."logoable_id" is not null`).
func (r *OrmBuilder) newMorphOneOrManyQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	if len(desc.References) == 0 {
		return r.guardedQuery(errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, "no references"))
	}
	ref := desc.References[0]
	parentVal, err := readParentColumn(r, parent, ref.PrimaryColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	foreign := desc.RelatedTable + "." + ref.ForeignColumn
	return r.relatedQuery(desc.RelatedModel).
		Where(desc.RelatedTable+"."+desc.MorphTypeColumn, desc.MorphValue).
		Where(foreign, parentVal).
		WhereNotNull(foreign)
}

// newMorphToQuery resolves the related model per-row from the parent's <type_col> via the morph
// map, then issues SELECT * FROM <resolved> WHERE <resolved>.<owner_key> = parent.<id_col>.
//
// If the parent's *_type is empty, the returned builder yields no rows through the guaranteed-false
// `1 = 0` WHERE — the read still executes that one statement. An unregistered type is a guarded
// builder reporting OrmMorphTypeUnknown before any statement runs.
func (r *OrmBuilder) newMorphToQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	morphType, err := readParentColumn(r, parent, desc.MorphTypeColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	morphID, err := readParentColumn(r, parent, desc.MorphIDColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	// The type may sit behind a pointer or a driver.Valuer wrapper (a nullable morph_type
	// column); the morph map must see the value the field carries, never the pointer address
	// fmt.Sprint would render — the deref loadMorphTo already applies on the eager read.
	typeStr := morphTypeToString(derefKeyValue(morphType))
	if typeStr == "" {
		return r.zeroRowQuery()
	}
	sample := morphmap.Find(typeStr)
	if sample == nil {
		return r.guardedQuery(errors.OrmMorphTypeUnknown.Args(typeStr))
	}
	ownerKey := desc.MorphOwnerKey
	if ownerKey == "" {
		ownerKey = "id"
	}
	relatedTable, err := tableNameFor(sample)
	if err != nil {
		return r.guardedQuery(err)
	}
	// The owner key is qualified by the resolved table, named the way a caller writes a column —
	// unquoted, the compiler quotes each segment itself (oracle: `where "ghost_relateds"."id" = ?`).
	// The id carries the same shapes; the WHERE binds the value the field holds — an invalid
	// wrapper (NullString{Valid: false}) or a nil pointer derefs to no value, whose null check
	// matches no row: the relation stays absent.
	//
	// The declaration's trashed-scope family applies to the resolved related query — the
	// instance-form half of Laravel's buffered withTrashed / withoutTrashed / onlyTrashed
	// (MorphTo.php:360-398: an immediate `when(true, callback)` whose replay getResultsByType
	// repeats per type). OnlyTrashed wins, WithTrashed drops the default "not deleted" predicate,
	// WithoutTrashed keeps it, and every flag is a no-op for a model without soft deletes
	// (applySoftDelete's own gating — Laravel's macro-existence check). Oracle (illuminate/
	// database v12.55.1, executed, /tmp/morphport-probes/trashed.php): the default read compiles
	// `where "mpt_posts"."id" = ? and "mpt_posts"."deleted_at" is null`; WithTrashed drops the
	// predicate; OnlyTrashed renders `is not null`.
	q := r.relatedQuery(sample).Where(relatedTable+"."+ownerKey, derefKeyValue(morphID))
	switch {
	case desc.MorphOnlyTrashed:
		q = q.OnlyTrashed()
	case desc.MorphWithTrashed && !desc.MorphWithoutTrashed:
		q = q.WithTrashed()
	}

	return q
}

// newMany2ManyQuery: SELECT <related>.*, <pivot>.<fk> as pivot_<fk>, ...
//
//	FROM <related> INNER JOIN <pivot> ON <related>.<rel_pk> = <pivot>.<rel_fk>
//	WHERE <pivot>.<parent_fk> = parent.<pk>  [AND <pivot>.<type_col> = morphValue]
//
// The select list wears Laravel's relation read shape (BelongsToMany::shouldSelect): the related
// table's columns first, then every pivot key aliased pivot_<column> — the morph type next to them
// on a polymorphic relation and the related model's declared Pivot-struct columns after them. The
// aliases are what the read hydrates the Pivot struct from, and what keeps the joined pivot row
// from overwriting the related model's own columns.
func (r *OrmBuilder) newMany2ManyQuery(parent any, desc *relationDescriptor, isMorph bool) contractsorm.Query {
	parentVal, err := readParentColumn(r, parent, desc.PivotParentRef.PrimaryColumn)
	if err != nil {
		return r.guardedQuery(err)
	}
	relatedTable := desc.RelatedTable
	pivotTable := desc.PivotTable

	// The related model's declared Pivot field, when it has one, contributes the pivot row's extra
	// columns — the Go form of the withPivot list — to the aliased select list and supplies the
	// destination the read hydrates after each row scan.
	pivotPlan, err := preparePivotHydration(r, desc)
	if err != nil {
		return r.guardedQuery(err)
	}

	q := r.relatedQuery(desc.RelatedModel).
		Join(r.tableRef(pivotTable)+" ON "+
			r.ident(pivotTable, desc.PivotRelatedRef.ForeignColumn)+" = "+
			r.ident(relatedTable, desc.PivotRelatedRef.PrimaryColumn)).
		WhereRaw(r.ident(pivotTable, desc.PivotParentRef.ForeignColumn)+" = ?", parentVal)
	if isMorph {
		q = q.WhereRaw(r.ident(pivotTable, desc.MorphTypeColumn)+" = ?", desc.MorphValue)
	}
	built := applyOnPivotQueryToQuery(r.queryGrammar, q, desc)
	// Remember the pivot relation's shape so every read over it renders Laravel's relation read
	// shape: the select wears the related table's columns plus the pivot columns aliased
	// pivot_<column> (BelongsToMany::shouldSelect, installed by get()/first()/cursor/ and by
	// prepareQueryBuilder before chunk/each/lazy), the scanned aliases hydrate the related model's
	// Pivot struct, and a chunked read's keyset defaults to the related key qualified by its table
	// (orderedChunkById).
	if query, ok := built.(*OrmBuilder); ok {
		query.conditions.pivotRead = &pivotReadConfig{
			relatedTable:    relatedTable,
			relatedKey:      desc.PivotRelatedRef.PrimaryColumn,
			pivotTable:      pivotTable,
			foreignPivotKey: desc.PivotParentRef.ForeignColumn,
			relatedPivotKey: desc.PivotRelatedRef.ForeignColumn,
			morphTypeColumn: desc.MorphTypeColumn,
			pivotField:      desc.PivotField,
			plan:            pivotPlan,
		}
	}
	return built
}

// newThroughQuery:
//
//	SELECT <related>.* FROM <related>
//	INNER JOIN <through> ON <related>.<second_key> = <through>.<second_local_key>
//	WHERE <through>.<first_key> = parent.<local_key>
func (r *OrmBuilder) newThroughQuery(parent any, desc *relationDescriptor) contractsorm.Query {
	parentVal, err := readParentColumn(r, parent, desc.LocalKey)
	if err != nil {
		return r.guardedQuery(err)
	}
	built := r.relatedQuery(desc.RelatedModel).
		Join(r.throughJoinFragment(desc)).
		WhereRaw(r.ident(desc.ThroughTable, desc.FirstKey)+" = ?", parentVal)
	// Remember the through relation's shape so the model-row reads rewrite their select list the
	// way HasOneOrManyThrough::shouldSelect does: the related table's columns plus the through key
	// aliased as laravel_through_key, so the joined through row's same-named columns cannot
	// overwrite the related row's on hydration.
	if query, ok := built.(*OrmBuilder); ok {
		query.conditions.throughRead = &throughReadConfig{
			relatedTable: desc.RelatedTable,
			throughTable: desc.ThroughTable,
			firstKey:     desc.FirstKey,
		}
		query.conditions.throughSoftDeleteTable = desc.ThroughTable
		query.conditions.throughSoftDeleteColumn = throughSoftDeleteColumn(desc)
	}
	return built
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

// zeroRowQuery returns an OrmBuilder whose Get/First yields no rows: the WHERE evaluates to the
// guaranteed-false `1 = 0`, so a read still executes that one statement and matches nothing.
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

// --- relations object layer --------------------------------------------------

// ormBuilderOf unwraps a query handle to its *OrmBuilder: a *OrmBuilder directly (relation reads
// for the non-relation paths still hand back the builder itself), or a relation object, which
// reports the builder it forwards to through UnwrapBuilder. It reports false for any other
// implementation — a mock, or a foreign driver — so callers keep their non-*OrmBuilder path.
func ormBuilderOf(query any) (*OrmBuilder, bool) {
	if built, ok := query.(*OrmBuilder); ok {
		return built, true
	}
	if unwrapper, ok := query.(interface{ UnwrapBuilder() contractsorm.Query }); ok {
		return ormBuilderOf(unwrapper.UnwrapBuilder())
	}

	return nil, false
}

// guardedRelation wraps a guarded builder in the base-only relation object so the caller's Get /
// First — and every other promoted terminal — keeps surfacing the guarded builder's error while
// the handle carries the RelationQuery shape.
func (r *OrmBuilder) guardedRelation(err error, parent any, relation string) contractsorm.RelationQuery {
	guarded, _ := r.guardedQuery(err).(*OrmBuilder)

	return relations.WrapBase(guarded, parent, relation, relationHooks(guarded, nil))
}

// relationInfoFromDescriptor completes the descriptor's own projection (relations.Descriptor.Info)
// with the facts bound to the built query and to the parent model — the boundary that keeps
// database/gorm/relations free of GORM types.
func relationInfoFromDescriptor(desc *relationDescriptor, built *OrmBuilder, parent any) relations.RelationInfo {
	info := desc.Info()
	info.OneOfMany = built != nil && built.conditions.oneOfMany != nil
	// The parent's own primary key column — Eloquent's getKeyName() of the parent (child) model —
	// is what BelongsTo / MorphTo getQualifiedParentKeyName qualifies next to ParentTable.
	if parentSchema, err := parseSchema(parent); err == nil && len(parentSchema.PrimaryFields) > 0 {
		info.ParentPrimaryKeyColumn = parentSchema.PrimaryFields[0].DBName
	}
	// The related model of a MorphTo is resolved per parent row, from the morph type — the
	// resolved sample sits on the builder (newMorphToQuery bound it), and the table / connection
	// comparisons of Is weigh against it. The other kinds carry it on the descriptor.
	related := desc.RelatedModel
	if related == nil && built != nil {
		related = built.conditions.model
	}
	info.RelatedConnection = declaredConnectionName(related)
	// Eloquent's newRelatedInstance hands a related instance that declares no connection the
	// declaring model's connection ($this->connection) — the value the related side of is()
	// then compares (oracle: bt.alt-parent.related.connection is "alt" for a relation declared
	// on the alt-connected parent and resolved to a class that declares none).
	if info.RelatedConnection == "" {
		info.RelatedConnection = declaredConnectionName(parent)
	}
	if info.RelatedTable == "" && built != nil {
		if table, err := tableNameFor(built.conditions.model); err == nil {
			info.RelatedTable = table
		}
	}

	return info
}

// ConfigurePivotRead applies the relation object's call-time pivot-read state to this builder —
// the Go form of Eloquent's withPivot()/as() mutating BelongsToMany::$pivotColumns and
// BelongsToMany::$accessor at call time: state aliasedPivotColumns (:970-983) consumes when
// get() installs shouldSelect and hydratePivotRelation (:1234-1242) consumes after the read.
// accessor re-points the hydration field name ("" keeps the declaration's desc.pivotField); an
// accessor with no struct field by its name keeps the declared plan — the select list stays, the
// way Laravel's frozen alias list never changes, while hydration goes nowhere (Laravel sets the
// dynamic attribute a Go struct cannot carry). columns replaces the object's explicit
// pivot-column list — the render deduplicates it, the raw list stays on the object. The returned handle is this builder, or a guarded copy reporting
// the resolution error when accessor names a field the related model cannot hydrate into — the
// same OrmRelationPivotFieldNotStruct preparePivotHydration raises for a declaration-time typo
// (the leaf rebuilds onto the guarded handle, so the reads surface it). A builder carrying no
// many-to-many pivot read (a guarded handle, a non-m2m relation, a foreign driver) is returned
// unchanged.
func (r *OrmBuilder) ConfigurePivotRead(accessor string, columns []string) contractsorm.Query {
	config := r.conditions.pivotRead
	if config == nil {
		return r
	}

	field := accessor
	if field == "" {
		field = config.pivotField
	}

	var plan *pivotHydrationPlan
	if r.conditions.model != nil {
		resolved, err := preparePivotHydrationFor(r.conditions.model, field)
		if err != nil {
			return r.guardedQuery(err)
		}

		plan = resolved
	}
	if plan == nil && accessor != "" && config.plan != nil {
		// An accessor the related model has no struct field for: Laravel keeps its frozen select
		// list and sets the dynamic attribute; Go cannot hydrate, so the read keeps the declared
		// plan under the new name — writePivotField misses the field and surfaces no pivot.
		renamed := *config.plan
		renamed.fieldName = accessor
		plan = &renamed
	}
	config.plan = plan
	config.withPivotColumns = columns

	return r
}

// relationHooks binds the object layer's GORM-internal reads to the builder the object wraps.
// desc may be nil for the guarded (error-only) objects, where only the terminals matter.
func relationHooks(built *OrmBuilder, desc *relationDescriptor) relations.Hooks {
	relatedModel := any(nil)
	if desc != nil {
		relatedModel = desc.RelatedModel
	}
	if relatedModel == nil && built != nil {
		// MorphTo resolves its related model per parent row: the resolved sample sits on the
		// builder (newMorphToQuery bound it), and the touch helpers below name the same class the
		// relation read resolves.
		relatedModel = built.conditions.model
	}

	return relations.Hooks{
		ReadColumn: func(model any, column string) (any, error) {
			return readParentColumn(built, model, column)
		},
		ReadModelColumn: func(model any, column string) (any, error) {
			return readParentColumn(built, model, column)
		},
		ModelTable: func(model any) string {
			table, err := tableNameFor(model)
			if err != nil {
				return ""
			}

			return table
		},
		FirstRow: func(query contractsorm.Query, dest any) (bool, error) {
			return firstFoundOn(query, dest)
		},
		BuildDefault: func(value any, parent any) (any, bool, error) {
			if built == nil || desc == nil {
				return nil, false, nil
			}
			parentValue := reflect.ValueOf(parent)
			if !parentValue.IsValid() || parentValue.Kind() != reflect.Pointer || parentValue.IsNil() {
				return nil, false, nil
			}
			instance, ok, err := built.buildRelationDefaultValue(desc, value, parentValue.Elem(), parent)
			if err != nil || !ok {
				return nil, ok, err
			}
			if instance.Kind() == reflect.Pointer {
				return instance.Interface(), true, nil
			}

			return parentAsAny(instance), true, nil
		},
		PendingAttributes: func() map[string]any {
			if built == nil {
				return nil
			}

			return built.conditions.pendingAttributes
		},
		ModelConnection: func(model any) string {
			return declaredConnectionName(model)
		},
		ModelKey: func(model any) any {
			if built == nil {
				return nil
			}

			return built.modelKeyValue(model)
		},
		UpdatedAtColumn: func() (string, error) {
			if relatedModel == nil {
				return "", nil
			}

			return touchedUpdatedAtColumn(relatedModel)
		},
		ModelCreatedAtColumn: func(model any) (string, error) {
			return modelCreatedAtColumn(model)
		},
		ModelUpdatedAtColumn: func(model any) (string, error) {
			return modelUpdatedAtColumn(model)
		},
		IgnoringTouch: func() bool {
			if relatedModel == nil {
				return false
			}

			// Read per call: a WithoutTouching scope toggles the ignore list only for the span of
			// its callback.
			return databaseorm.IsIgnoringTouch(relatedModel)
		},
	}
}

// declaredConnectionName reads a model's declared connection name without connecting — the value
// Eloquent's getConnectionName() reports; "" for a model that declares none (or is not a model).
func declaredConnectionName(model any) string {
	if model == nil {
		return ""
	}

	if connectionModel, ok := model.(contractsorm.ModelWithConnection); ok {
		return connectionModel.Connection()
	}

	return ""
}

// modelKeyValue reads a model's primary key value — the value Eloquent's getKey() reports — for
// the relation object's one-of-many guard. nil when the value carries no primary key field.
func (r *OrmBuilder) modelKeyValue(model any) any {
	schema, err := parseSchema(model)
	if err != nil || len(schema.PrimaryFields) == 0 {
		return nil
	}

	value := reflect.ValueOf(model)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return nil
	}

	key, _ := schema.PrimaryFields[0].ValueOf(r.ctx, value)

	return derefKeyValue(key)
}

// The relations object layer forwards every write through *OrmBuilder, so the builder must carry
// the full write-shell surface the objects declare.
var _ relations.Builder = (*OrmBuilder)(nil)
