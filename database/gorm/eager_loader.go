package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/samber/lo"

	contractsorm "github.com/gradii/framework/contracts/database/orm"

	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/database/orm/morphmap"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/str"
)

// defaultEagerLoadChunkSize is the WHERE IN list size at which we split a single eager-load
// query into multiple round-trips. 1000 covers the strictest mainstream limits:
// Oracle's hard cap of 1000 expressions and SQLite's default SQLITE_MAX_VARIABLE_NUMBER of 999.
// PostgreSQL/MySQL/SQL Server have higher limits but their planners also slow down dramatically
// past a few thousand entries, so chunking is a net win even where it isn't strictly required.
//
// The size can be overridden per-app via the `database.eager_load_chunk_size` config key. A value
// <= 0 disables chunking entirely (single IN clause regardless of length).
const defaultEagerLoadChunkSize = 1000

// applyEagerLoads runs all queued With entries against the just-loaded dest. It must be
// called by terminal methods (Get / Find / First / FirstOrFail / FirstOr / Cursor) after the main
// query has populated dest, and only when conditions.eagerLoad is non-empty.
func (r *OrmBuilder) applyEagerLoads(dest any) error {
	if len(r.conditions.eagerLoad) == 0 {
		return nil
	}
	parents, err := collectEagerParents(dest)
	if err != nil {
		return err
	}
	entries := r.conditions.eagerLoad
	r.conditions.eagerLoad = nil
	if len(parents) == 0 {
		return nil
	}
	return r.runEagerLoads(parents, entries)
}

// runEagerLoads iterates the eager-load entries and dispatches each top-level relation to its
// kind-specific loader. Nested entries (those whose name contains a dot) are handled by the
// trickle-down recursion inside each loader, mirroring fedaco's eagerLoadRelations.
func (r *OrmBuilder) runEagerLoads(parents []reflect.Value, entries []eagerLoadEntry) error {
	if len(parents) == 0 || len(entries) == 0 {
		return nil
	}
	parentModel := newSampleModel(parents[0])
	for _, entry := range entries {
		if strings.Contains(entry.Relation, ".") {
			continue
		}
		nested := relations.DirectNestedEntries(entries, entry.Relation)
		if err := r.loadOneRelation(parents, parentModel, entry, nested); err != nil {
			return err
		}
	}
	return nil
}

func (r *OrmBuilder) loadOneRelation(parents []reflect.Value, parentModel any, entry eagerLoadEntry, nested []eagerLoadEntry) error {
	desc, err := relations.ResolveRelation(parentModel, entry.Relation)
	if err != nil {
		return err
	}
	switch desc.Kind {
	case relKindHasOne, relKindHasMany:
		return r.loadHasOneOrMany(parents, parentModel, desc, entry, nested, desc.Kind == relKindHasMany)
	case relKindBelongsTo:
		return r.loadBelongsTo(parents, parentModel, desc, entry, nested)
	case relKindMany2Many:
		return r.loadMany2Many(parents, parentModel, desc, entry, nested)
	case relKindMorphOne, relKindMorphMany:
		return r.loadMorph(parents, parentModel, desc, entry, nested, desc.Kind == relKindMorphMany)
	case relKindMorphTo:
		return r.loadMorphTo(parents, parentModel, desc, entry, nested)
	case relKindMorphToMany:
		return r.loadMorphToMany(parents, parentModel, desc, entry, nested)
	case relKindHasOneThrough, relKindHasManyThrough:
		return r.loadThrough(parents, parentModel, desc, entry, nested, desc.Kind == relKindHasManyThrough)
	}
	return errors.OrmRelationUnsupported.Args(entry.Relation, reflect.TypeOf(parentModel).String(), fmt.Sprintf("kind=%d", desc.Kind))
}

// ---------------------------------------------------------------------------
// Per-kind loaders
// ---------------------------------------------------------------------------

func (r *OrmBuilder) loadHasOneOrMany(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry, isMany bool) error {
	if len(desc.References) == 0 {
		return errors.OrmRelationUnsupported.Args(entry.Relation, "", "no references")
	}
	ref := desc.References[0]
	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return err
	}
	parentField, ok := parentSchema.FieldsByDBName[ref.PrimaryColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, parentSchema.Name, "no parent field for "+ref.PrimaryColumn)
	}

	// HasOne is the to-one member of this family: a WithDefault declaration fills the parents the
	// read leaves unmatched, including those with an empty local key (Laravel's initRelation seeds
	// every model before the query).
	var defaultFor func(parent reflect.Value) ([]reflect.Value, error)
	if !isMany {
		defaultFor = r.relationDefaultFor(desc, parentModel)
	}

	keys := extractKeys(r, parents, parentField)
	if len(keys) == 0 {
		return r.maybeRecurseEmpty(parents, entry.Relation, isMany, nested, defaultFor)
	}

	// The column is named without quotes: the compiler quotes each segment itself, and a name that
	// arrived already quoted would be quoted a second time.
	rows, err := r.runChunkedRelatedQueryCompiled(keys, desc, entry, []string{ref.ForeignColumn}, parentModel, func(chunk []any) *OrmBuilder {
		return r.relatedChild(desc.RelatedTable, desc.RelatedTable+"."+ref.ForeignColumn, chunk)
	})
	if err != nil {
		return err
	}

	relatedSchema, err := parseSchema(desc.RelatedModel)
	if err != nil {
		return err
	}
	fkField, ok := relatedSchema.FieldsByDBName[ref.ForeignColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, relatedSchema.Name, "no related FK field for "+ref.ForeignColumn)
	}

	dict := make(map[string][]reflect.Value, len(rows))
	for _, row := range rows {
		val, _ := fkField.ValueOf(r.ctx, row.Elem())
		dict[dictKey(val)] = append(dict[dictKey(val)], row)
	}

	if err := r.applyInverseRelations(desc, parents, parentField, dict, !isMany); err != nil {
		return err
	}
	if err := r.assignToParents(parents, parentField, entry.Relation, dict, isMany, defaultFor); err != nil {
		return err
	}
	return r.recurseNested(rows, nested)
}

func (r *OrmBuilder) loadBelongsTo(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry) error {
	if len(desc.References) == 0 {
		return errors.OrmRelationUnsupported.Args(entry.Relation, "", "no references")
	}
	ref := desc.References[0]
	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return err
	}
	// For BelongsTo: ref.foreignTable=parent, ref.foreignColumn=FK on parent;
	// ref.primaryTable=related, ref.primaryColumn=PK on related.
	fkField, ok := parentSchema.FieldsByDBName[ref.ForeignColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, parentSchema.Name, "no parent FK field for "+ref.ForeignColumn)
	}

	// With a WithDefault declaration, parents whose foreign key is null or points at no row take
	// the default instance — getResults' is_null short-circuit and its first()-null fallback.
	defaultFor := r.relationDefaultFor(desc, parentModel)

	keys := extractKeys(r, parents, fkField)
	if len(keys) == 0 {
		return r.maybeRecurseEmpty(parents, entry.Relation, false, nested, defaultFor)
	}

	// The column is named without quotes: the compiler quotes each segment itself, and a name that
	// arrived already quoted would be quoted a second time.
	rows, err := r.runChunkedRelatedQueryCompiled(keys, desc, entry, []string{ref.PrimaryColumn}, parentModel, func(chunk []any) *OrmBuilder {
		return r.relatedChild(desc.RelatedTable, desc.RelatedTable+"."+ref.PrimaryColumn, chunk)
	})
	if err != nil {
		return err
	}

	relatedSchema, err := parseSchema(desc.RelatedModel)
	if err != nil {
		return err
	}
	pkField, ok := relatedSchema.FieldsByDBName[ref.PrimaryColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, relatedSchema.Name, "no related PK field for "+ref.PrimaryColumn)
	}

	dict := make(map[string][]reflect.Value, len(rows))
	for _, row := range rows {
		val, _ := pkField.ValueOf(r.ctx, row.Elem())
		dict[dictKey(val)] = append(dict[dictKey(val)], row)
	}

	if err := r.assignToParents(parents, fkField, entry.Relation, dict, false, defaultFor); err != nil {
		return err
	}
	return r.recurseNested(rows, nested)
}

func (r *OrmBuilder) loadMorph(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry, isMany bool) error {
	if len(desc.References) == 0 {
		return errors.OrmRelationUnsupported.Args(entry.Relation, "", "no references")
	}
	ref := desc.References[0]
	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return err
	}
	parentField, ok := parentSchema.FieldsByDBName[ref.PrimaryColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, parentSchema.Name, "no parent field for "+ref.PrimaryColumn)
	}

	// MorphOne is the to-one member of this family: a WithDefault declaration fills the parents
	// the read leaves unmatched, including those with an empty local key, exactly as HasOne does.
	var defaultFor func(parent reflect.Value) ([]reflect.Value, error)
	if !isMany {
		defaultFor = r.relationDefaultFor(desc, parentModel)
	}

	keys := extractKeys(r, parents, parentField)
	if len(keys) == 0 {
		return r.maybeRecurseEmpty(parents, entry.Relation, isMany, nested, defaultFor)
	}

	// The child is the related table filtered by the foreign key and by the relation's own morph
	// value, which is what separates a MorphOne / MorphMany from the plain HasOne / HasMany it is
	// shaped like. Both columns are named without quotes: the compiler quotes each segment itself,
	// and a name that arrived already quoted would be quoted a second time.
	rows, err := r.runChunkedRelatedQueryCompiled(keys, desc, entry, []string{ref.ForeignColumn, desc.MorphTypeColumn}, parentModel, func(chunk []any) *OrmBuilder {
		return r.relatedChild(desc.RelatedTable, desc.RelatedTable+"."+ref.ForeignColumn, chunk).
			Where(desc.RelatedTable+"."+desc.MorphTypeColumn, desc.MorphValue).(*OrmBuilder)
	})
	if err != nil {
		return err
	}

	relatedSchema, err := parseSchema(desc.RelatedModel)
	if err != nil {
		return err
	}
	fkField, ok := relatedSchema.FieldsByDBName[ref.ForeignColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, relatedSchema.Name, "no related FK field for "+ref.ForeignColumn)
	}

	dict := make(map[string][]reflect.Value, len(rows))
	for _, row := range rows {
		val, _ := fkField.ValueOf(r.ctx, row.Elem())
		dict[dictKey(val)] = append(dict[dictKey(val)], row)
	}

	if err := r.applyInverseRelations(desc, parents, parentField, dict, !isMany); err != nil {
		return err
	}
	if err := r.assignToParents(parents, parentField, entry.Relation, dict, isMany, defaultFor); err != nil {
		return err
	}
	return r.recurseNested(rows, nested)
}

// loadMorphTo eager-loads the inverse polymorphic relation. Unlike the outbound MorphOne /
// MorphMany loaders, the related Go type is unknown at descriptor build time and discovered per
// row from the value of the *_type column. Parents are bucketed by morph type, and each bucket
// runs an IN query against its resolved table.
func (r *OrmBuilder) loadMorphTo(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry) error {
	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return err
	}
	typeField, ok := parentSchema.FieldsByDBName[desc.MorphTypeColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, parentSchema.Name, "no parent field for "+desc.MorphTypeColumn)
	}
	idField, ok := parentSchema.FieldsByDBName[desc.MorphIDColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, parentSchema.Name, "no parent field for "+desc.MorphIDColumn)
	}

	// Bucket parents by morph type, deduplicating IDs per bucket.
	type bucket struct {
		keys     []any
		seenKeys map[string]struct{}
	}
	buckets := make(map[string]*bucket)
	parentMorphKey := make([]string, len(parents))  // morph type per parent
	parentBucketKey := make([]string, len(parents)) // dictKey of id per parent
	for i, parent := range parents {
		typeVal, typeZero := typeField.ValueOf(r.ctx, parent)
		idVal, idZero := idField.ValueOf(r.ctx, parent)
		// The type is recorded even when the parent carries no id, so a default instance can be
		// resolved from the class the type column names.
		morphType := ""
		if !typeZero {
			// Like the id half below, the type may sit behind a pointer or a driver.Valuer
			// wrapper (a nullable morph_type column); the morph map must see the value the field
			// carries, never the pointer address fmt.Sprint would render.
			morphType = morphTypeToString(derefKeyValue(typeVal))
		}
		parentMorphKey[i] = morphType
		if typeZero || idZero || morphType == "" {
			continue
		}
		// The morph id may sit in a pointer-typed field; the bucket and the IN list both carry
		// the value it addresses.
		idVal = derefKeyValue(idVal)
		k := dictKey(idVal)
		b, exists := buckets[morphType]
		if !exists {
			b = &bucket{seenKeys: map[string]struct{}{}}
			buckets[morphType] = b
		}
		if _, dup := b.seenKeys[k]; !dup {
			b.seenKeys[k] = struct{}{}
			b.keys = append(b.keys, idVal)
		}
		parentBucketKey[i] = k
	}

	if len(buckets) == 0 {
		// No parents pointed at anything; each takes the relation's default when one is declared
		// (an empty type column resolves it to the parent's own class), nil otherwise.
		for _, parent := range parents {
			if err := r.setRelationDefaultOrNil(parent, entry.Relation, desc, parentModel); err != nil {
				return err
			}
		}
		return nil
	}

	// For each bucket: resolve type, run IN query, build a per-bucket id->row dict.
	type resolvedBucket struct {
		dict map[string]reflect.Value // parent's dictKey(idVal) -> *RelatedModel
		rows []reflect.Value
	}
	resolved := make(map[string]resolvedBucket, len(buckets))
	allRows := make([]reflect.Value, 0)

	for morphType, b := range buckets {
		sample := morphmap.Find(morphType)
		if sample == nil {
			return errors.OrmMorphTypeUnknown.Args(morphType)
		}
		relatedTable, terr := tableNameFor(sample)
		if terr != nil {
			return terr
		}
		// Make a per-bucket descriptor so the compiled runner's user-callback gets a
		// related-shaped query.
		bucketDesc := &relationDescriptor{
			Kind:         relKindBelongsTo, // BelongsTo-shaped: WHERE related.<owner_key> IN ?
			ParentTable:  desc.ParentTable,
			RelatedTable: relatedTable,
			RelatedModel: sample,
			OnQuery:      desc.OnQuery, // propagate so the default scope applies per bucket
		}
		ownerKey := desc.MorphOwnerKey
		if ownerKey == "" {
			ownerKey = "id"
		}

		// The bucket's rows are matched on the owner key and are named the way a caller writes a
		// column: unquoted, with the related table qualifying it, because the compiler quotes each
		// segment itself.
		//
		// The bucket consumes the class's declared config too: the constrain callback layers over
		// the caller's entry callback (the order MorphTo::getResultsByType applies them — the
		// relation's merged constraints first, constrain after). The soft-delete predicate and the
		// count sub-selects compile against the resolved model, which runRelatedQueryCompiled attaches
		// from bucketDesc.RelatedModel, so the per-type read carries the type's SoftDeletingScope.
		bucketEntry := entry
		if constrain := desc.MorphConstrain[morphType]; constrain != nil {
			bucketEntry.Callback = chainRelationCallbacks(entry.Callback, constrain)
		}
		counts := desc.MorphWithCount[morphType]
		rows, qerr := r.runChunkedRelatedQueryCompiled(b.keys, bucketDesc, bucketEntry, []string{ownerKey}, parentModel, func(chunk []any) *OrmBuilder {
			child := r.relatedChild(relatedTable, relatedTable+"."+ownerKey, chunk)
			switch {
			case desc.MorphOnlyTrashed:
				child = child.OnlyTrashed().(*OrmBuilder)
			case desc.MorphWithTrashed && !desc.MorphWithoutTrashed:
				child = child.WithTrashed().(*OrmBuilder)
			}
			if len(counts) > 0 {
				child = child.WithCount(counts...).(*OrmBuilder)
			}

			return child
		})
		if qerr != nil {
			return qerr
		}
		allRows = append(allRows, rows...)

		relatedSchema, perr := parseSchema(sample)
		if perr != nil {
			return perr
		}
		ownerField, ok := relatedSchema.FieldsByDBName[ownerKey]
		if !ok {
			return errors.OrmRelationUnsupported.Args(entry.Relation, relatedSchema.Name, "no owner key field for "+ownerKey)
		}
		dict := make(map[string]reflect.Value, len(rows))
		for _, row := range rows {
			val, _ := ownerField.ValueOf(r.ctx, row.Elem())
			dict[dictKey(val)] = row
		}
		resolved[morphType] = resolvedBucket{dict: dict, rows: rows}
	}

	// Assign each parent the row it pointed to.
	for i, parent := range parents {
		morphType := parentMorphKey[i]
		idKey := parentBucketKey[i]
		if morphType == "" || idKey == "" {
			if err := r.setRelationDefaultOrNil(parent, entry.Relation, desc, parentModel); err != nil {
				return err
			}
			continue
		}
		bucketResult, ok := resolved[morphType]
		if !ok {
			if err := r.setRelationDefaultOrNil(parent, entry.Relation, desc, parentModel); err != nil {
				return err
			}
			continue
		}
		row, ok := bucketResult.dict[idKey]
		if !ok {
			if err := r.setRelationDefaultOrNil(parent, entry.Relation, desc, parentModel); err != nil {
				return err
			}
			continue
		}
		if err := setRelationField(parent, entry.Relation, []reflect.Value{row}); err != nil {
			return err
		}
	}

	if len(desc.MorphWith) > 0 {
		// Per-type eager loads: each resolved type recurses with its own MorphWith list merged
		// over the relation's nested loads — the per-type with() MorphTo::getResultsByType
		// installs. The shared walk below hands mixed types to one sample model, so a declared
		// per-type config takes the per-bucket walk instead.
		for morphType, bucketResult := range resolved {
			entries, err := morphWithEntriesFor(desc, morphType, nested)
			if err != nil {
				return err
			}
			if err := r.recurseNested(bucketResult.rows, entries); err != nil {
				return err
			}
		}

		return nil
	}

	return r.recurseNested(allRows, nested)
}

// chainRelationCallbacks composes the caller's eager-load callback with a per-type constrain
// callback in application order — the caller's first, the constrain second, the order
// MorphTo::getResultsByType gives the merged relation constraints (mergeConstraintsFrom) and the
// constrain callback. A callback that returns a foreign query keeps the builder, the contract
// runRelatedQueryCompiled pins for a single callback.
func chainRelationCallbacks(first, second contractsorm.RelationCallback) contractsorm.RelationCallback {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}

	return func(query contractsorm.Query) contractsorm.Query {
		if scoped, ok := first(query).(*OrmBuilder); ok {
			query = scoped
		}
		if scoped, ok := second(query).(*OrmBuilder); ok {
			query = scoped
		}

		return query
	}
}

// morphWithEntriesFor merges a type's MorphWith list into the relation's own nested eager loads —
// MorphTo::getResultsByType's array_merge of the query's eager loads and the class's list. The
// class's list entries are plain relation names (numeric keys in PHP), so array_merge seats them
// BESIDE the relation-keyed nested loads — a name the nested loads already carry is absorbed by
// with()'s combineConstraints, the nested entry keeping its constraint and select list — rather than
// replacing it. Oracle (illuminate/database v12.55.1, executed, /tmp/morphverify-probes/
// collision_dotted.php — the caller's `with(['commentable.author' => ...])` constraint survives the
// collision, the author read still carrying `"name" = ?`). Each name is parsed with the With grammar
// (dotted paths and column pruning included); an unconfigured type keeps the nested loads untouched.
func morphWithEntriesFor(desc *relationDescriptor, morphType string, nested []eagerLoadEntry) ([]eagerLoadEntry, error) {
	with := desc.MorphWith[morphType]
	if len(with) == 0 {
		return nested, nil
	}

	args := make([]any, 0, len(with))
	for _, name := range with {
		args = append(args, name)
	}
	entries, err := relations.ParseEagerLoad(args)
	if err != nil {
		return nil, err
	}

	merged := append([]eagerLoadEntry(nil), nested...)
	for _, entry := range entries {
		merged = relations.CombineEagerLoadEntry(merged, entry)
	}

	return merged, nil
}

// loadMany2Many eager-loads a regular many-to-many relation. The related rows are read the way the
// relation read reads them — the related table joined to the pivot table and filtered on the
// pivot's parent key, wearing the pivot-read select list (the related columns plus every pivot
// column aliased pivot_<column>) — so a With callback's relation-level limit reaches
// BelongsToMany::limit (vendor v12.55.1, BelongsToMany.php:1503) and compiles the pivot-partitioned
// group limit: the row_number() over (partition by <qualified foreign pivot key>) derived table that
// caps rows per parent. The rows are grouped per parent by the pivot parent key's alias, mirroring
// BelongsToMany::match's `$model->pivot->getAttribute($this->foreignPivotKey)`. Oracle
// (illuminate/database v12.55.1, executed against sqlite from /tmp/eager-m2m-limit/probe.php): B1
// eager BtM limit(2) -> `select * from (select "m2m_roles".*, "m2m_role_user"."user_id" as
// "pivot_user_id", "m2m_role_user"."role_id" as "pivot_role_id", "m2m_role_user"."note" as
// "pivot_note", row_number() over (partition by "m2m_role_user"."user_id") as "laravel_row" from
// "m2m_roles" inner join "m2m_role_user" on "m2m_roles"."id" = "m2m_role_user"."role_id" where
// "m2m_role_user"."user_id" in (1, 2)) as "laravel_table" where "laravel_row" <= 2 order by
// "laravel_row"`, counts [2, 2]; B1n the no-callback read is the same joined statement without the
// rewrite.
func (r *OrmBuilder) loadMany2Many(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry) error {
	return r.loadMany2ManyKind(parents, parentModel, desc, entry, nested, false)
}

// loadMorphToMany eager-loads polymorphic many-to-many. It shares its body with loadMany2Many; the
// only difference is the pivot's morph-type predicate pinning desc.MorphValue, which keeps
// MorphToMany (forward) and MorphedByMany (inverse) on the same path (the descriptor resolves the
// value). Oracle M1 (/tmp/eager-m2m-limit/probe.php): eager MorphToMany limit(2) partitions by
// "m2m_taggables"."taggable_id" and keeps `and "m2m_taggables"."taggable_type" = ?` inside the
// rewrite.
func (r *OrmBuilder) loadMorphToMany(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry) error {
	return r.loadMany2ManyKind(parents, parentModel, desc, entry, nested, true)
}

// loadMany2ManyKind is the shared body of the two many-to-many eager loaders.
func (r *OrmBuilder) loadMany2ManyKind(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry, isMorph bool) error {
	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return err
	}
	parentField, ok := parentSchema.FieldsByDBName[desc.PivotParentRef.PrimaryColumn]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, parentSchema.Name, "no parent field for "+desc.PivotParentRef.PrimaryColumn)
	}

	keys := extractKeys(r, parents, parentField)
	if len(keys) == 0 {
		return r.maybeRecurseEmpty(parents, entry.Relation, true, nested, nil)
	}

	// The related model's Pivot field, when it declares one, supplies the aliased pivot columns the
	// joined read hydrates from — the select rewrite applyPivotReadSelect installs off the pivot-read
	// config. Struct-only: no field by desc.pivotField means no pivot hydration.
	plan, err := preparePivotHydration(r, desc)
	if err != nil {
		return err
	}

	rows, rowParents, err := r.runChunkedMany2ManyQueryCompiled(keys, desc, entry, parentModel, plan, isMorph)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return r.maybeRecurseEmpty(parents, entry.Relation, true, nested, nil)
	}

	// Each joined row is one pivot row, carrying its parent key under the pivot parent key's alias;
	// grouping them per parent is Eloquent's match() by $model->pivot.
	dict := make(map[string][]reflect.Value, len(parents))
	for index, row := range rows {
		dict[rowParents[index]] = append(dict[rowParents[index]], row)
	}

	if err := r.assignToParents(parents, parentField, entry.Relation, dict, true, nil); err != nil {
		return err
	}

	return r.recurseNested(rows, nested)
}

// many2ManyJoinFragment renders the pivot join the many-to-many relation read and eager read share:
// the pivot's related foreign key against the related table's related key — the port's join
// rendering (the pivot-side operand first and the ON keyword upper-cased), the convention every
// relation read in the repository shares (see database/gorm/relations/many2many_test.go).
func (r *OrmBuilder) many2ManyJoinFragment(desc *relationDescriptor) string {
	return r.tableRef(desc.PivotTable) + " ON " +
		r.ident(desc.PivotTable, desc.PivotRelatedRef.ForeignColumn) + " = " +
		r.ident(desc.RelatedTable, desc.PivotRelatedRef.PrimaryColumn)
}

// many2ManyEagerChild builds one chunk's child for the joined many-to-many eager read: the related
// table joined to the pivot and filtered on the pivot's parent key against the chunk, wearing the
// pivot-read select list and — for MorphToMany — the pivot's morph-type predicate. It mirrors the
// relation read newMany2ManyQuery builds, with the parent key's `in (chunk)` where the relation
// read's single-parent equality takes, and the eager constraint landing before the join the way
// addEagerConstraints writes it.
func (r *OrmBuilder) many2ManyEagerChild(desc *relationDescriptor, chunk []any, plan *pivotHydrationPlan, isMorph bool) *OrmBuilder {
	child := r.relatedChild(desc.RelatedTable, desc.PivotTable+"."+desc.PivotParentRef.ForeignColumn, chunk)
	child.query.JoinRaw(r.many2ManyJoinFragment(desc), nil, "inner")
	child.conditions.pivotRead = &pivotReadConfig{
		relatedTable:    desc.RelatedTable,
		relatedKey:      desc.PivotRelatedRef.PrimaryColumn,
		pivotTable:      desc.PivotTable,
		foreignPivotKey: desc.PivotParentRef.ForeignColumn,
		relatedPivotKey: desc.PivotRelatedRef.ForeignColumn,
		morphTypeColumn: desc.MorphTypeColumn,
		pivotField:      desc.PivotField,
		plan:            plan,
	}
	if isMorph {
		child = child.Where(desc.PivotTable+"."+desc.MorphTypeColumn, desc.MorphValue).(*OrmBuilder)
	}

	return child
}

// runChunkedMany2ManyQueryCompiled runs the joined many-to-many eager read once per chunk of parent
// keys, returning the hydrated related rows together with each row's parent key — the value of the
// pivot parent key's alias (pivot_<column>), so the loader groups the rows per parent.
func (r *OrmBuilder) runChunkedMany2ManyQueryCompiled(keys []any, desc *relationDescriptor, entry eagerLoadEntry, protoParent any, plan *pivotHydrationPlan, isMorph bool) ([]reflect.Value, []string, error) {
	chunks := chunkKeys(keys, r.chunkSize())
	var rows []reflect.Value
	var parents []string
	for _, chunk := range chunks {
		chunkRows, chunkParents, err := r.runMany2ManyQueryCompiled(r.many2ManyEagerChild(desc, chunk, plan, isMorph), desc, entry, protoParent)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, chunkRows...)
		parents = append(parents, chunkParents...)
	}

	return rows, parents, nil
}

// runMany2ManyQueryCompiled compiles and runs one joined many-to-many eager read. The relation's
// pivot scope (OnPivotQuery) then its declaration scope (OnQuery) layer on top of the child the way
// newMany2ManyQuery installs them; the caller's callback constrains the wrapped relation object
// (eagerCallbackTarget), so its Limit reaches BelongsToMany::limit and compiles the pivot-partitioned
// group limit. Each row is scanned into its related model — the pivot_-aliased columns hydrate the
// model's declared Pivot field — and collected with the parent key the pivot parent key's alias
// carries.
func (r *OrmBuilder) runMany2ManyQueryCompiled(child *OrmBuilder, desc *relationDescriptor, entry eagerLoadEntry, protoParent any) ([]reflect.Value, []string, error) {
	child = child.flushWheres()

	if scoped, ok := applyOnPivotQueryToQuery(r.queryGrammar, child, desc).(*OrmBuilder); ok {
		child = scoped
	}

	if desc.OnQuery != nil {
		if scoped, ok := desc.OnQuery(child).(*OrmBuilder); ok {
			child = scoped
		}
	}

	if entry.Callback != nil {
		if scoped, ok := ormBuilderOf(entry.Callback(r.eagerCallbackTarget(child, desc, protoParent, entry.Relation))); ok {
			child = scoped
		}
	}

	// The joined read is a query on the related model - the child the relation read newMany2ManyQuery
	// builds is bound to desc.RelatedModel - so the related model's soft-delete scope reaches the
	// eager read the way Laravel's does (oracle, /tmp/eager-softdelete/probe.php E10: the eager read
	// appends `and "roles"."deleted_at" is null`). The pivot table's own soft-delete field is never
	// consulted here; that is the relation's OnPivotQuery scope's job. The binding runs after the
	// callback (so its scopes are visible) and is skipped when WithoutGlobalScopes() removed every
	// scope ("*"): with all scopes gone there is nothing to bind. When only the soft-delete scope is
	// removed by name (WithoutGlobalScopes(contractsorm.SoftDeletingScope)) the binding stays - the
	// related model's user scopes must still queue - and applySoftDelete drops the predicate through
	// its named-removal gate. The related model's user-defined global scopes are queued on top of it
	// too (addGlobalScopes; see runRelatedQueryCompiled for the full note).
	if desc.RelatedModel != nil && !slices.Contains(child.conditions.withoutGlobalScopes, "*") {
		child = child.Model(desc.RelatedModel).(*OrmBuilder)
		child = child.addGlobalScopes()
	}

	// The related key survives column pruning, so the model still hydrates its own identity.
	if len(entry.Columns) > 0 {
		cols := append([]string(nil), entry.Columns...)
		for _, req := range []string{desc.PivotRelatedRef.PrimaryColumn} {
			if !slices.ContainsFunc(cols, func(c string) bool {
				if c == req {
					return true
				}
				_, suffix, ok := strings.Cut(c, ".")
				return ok && suffix == req
			}) {
				cols = append(cols, req)
			}
		}

		selects := make([]any, 0, len(cols))
		for _, col := range cols {
			selects = append(selects, col)
		}
		child.query.SetSelects(selects)
	}

	statement, bindings, err := child.compileSelect()
	if err != nil {
		return nil, nil, err
	}

	rows, err := child.queryRows(statement, bindings)
	if err != nil {
		return nil, nil, err
	}
	defer errors.Ignore(rows.Close)

	relatedType := indirectStructType(reflect.TypeOf(desc.RelatedModel))
	if relatedType == nil {
		return nil, nil, errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, "no related model")
	}

	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	parentAlias := "pivot_" + desc.PivotParentRef.ForeignColumn
	var out []reflect.Value
	var parents []string
	for rows.Next() {
		row, err := scanRowMap(rows, columns)
		if err != nil {
			return nil, nil, err
		}

		element := reflect.New(relatedType)
		if err := scanRow(row, element.Interface()); err != nil {
			return nil, nil, err
		}
		if err := child.hydratePivotRow(row, element); err != nil {
			return nil, nil, err
		}
		syncOriginal(element.Interface())

		out = append(out, element)
		parents = append(parents, dictKey(row[parentAlias]))
	}

	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return out, parents, nil
}

func (r *OrmBuilder) loadThrough(parents []reflect.Value, parentModel any, desc *relationDescriptor, entry eagerLoadEntry, nested []eagerLoadEntry, isMany bool) error {
	parentSchema, err := parseSchema(parentModel)
	if err != nil {
		return err
	}
	parentField, ok := parentSchema.FieldsByDBName[desc.LocalKey]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, parentSchema.Name, "no parent field for "+desc.LocalKey)
	}

	// HasOneThrough is the to-one member of the through family: a WithDefault declaration fills
	// the parents the read leaves unmatched, including those with an empty local key, exactly as
	// HasOne does.
	var defaultFor func(parent reflect.Value) ([]reflect.Value, error)
	if !isMany {
		defaultFor = r.relationDefaultFor(desc, parentModel)
	}

	keys := extractKeys(r, parents, parentField)
	if len(keys) == 0 {
		return r.maybeRecurseEmpty(parents, entry.Relation, isMany, nested, defaultFor)
	}

	// The through rows are read through the compiled path, like the pivot reads the other loaders
	// use: the child names the through table and the parent FK column the way a caller writes them —
	// unquoted, with the table qualifying the IN column — and keeps the two key columns bare in the
	// SELECT list, so the returned map stays keyed by the names the hydration loop indexes.
	throughRows, err := r.runChunkedPivotQueryCompiled(keys, desc, func(chunk []any) *OrmBuilder {
		return r.pivotChild(desc.ThroughTable, desc.ThroughTable+"."+desc.FirstKey, chunk,
			[]string{desc.FirstKey, desc.SecondLocalKey})
	})
	if err != nil {
		return err
	}
	if len(throughRows) == 0 {
		return r.maybeRecurseEmpty(parents, entry.Relation, isMany, nested, defaultFor)
	}

	// The related rows are read the way HasOneOrManyThrough::addEagerConstraints reads them: the
	// related query joined to the through table and filtered on the through table's first key —
	// whereInEager writes `through.<first key> in (...)` onto getRelationQuery, which for a through
	// relation is the joined query — wearing the through read's select list (related.* plus the
	// through key aliased laravel_through_key, the list shouldSelect installs on the relation read
	// path). A callback's relation-level limit partitions by that first key, so the join has to be
	// on the child before the rewrite runs. The one-of-many variant layers the aggregate join over
	// this same base and keeps its own select.
	buildRelatedChild := func(chunk []any) *OrmBuilder {
		child := r.relatedChild(desc.RelatedTable, desc.ThroughTable+"."+desc.FirstKey, chunk)
		child.query.JoinRaw(r.throughJoinFragment(desc), nil, "inner")
		child.conditions.throughRead = &throughReadConfig{
			relatedTable: desc.RelatedTable,
			throughTable: desc.ThroughTable,
			firstKey:     desc.FirstKey,
		}
		// The through soft-delete predicate is a global scope on the relation's builder, not part of
		// the through select-list rewrite, so it is recorded on the query separately: a read that
		// suppresses the alias (pluck / value / exists) still filters a soft-deleted through row.
		child.conditions.throughSoftDeleteTable = desc.ThroughTable
		child.conditions.throughSoftDeleteColumn = throughSoftDeleteColumn(desc)

		return child
	}

	rows, err := r.runChunkedRelatedQueryCompiled(keys, desc, entry, []string{desc.SecondKey}, parentModel, buildRelatedChild)
	if err != nil {
		return err
	}

	relatedSchema, err := parseSchema(desc.RelatedModel)
	if err != nil {
		return err
	}
	secondField, ok := relatedSchema.FieldsByDBName[desc.SecondKey]
	if !ok {
		return errors.OrmRelationUnsupported.Args(entry.Relation, relatedSchema.Name, "no related field for "+desc.SecondKey)
	}
	relatedByThrough := make(map[string][]reflect.Value, len(rows))
	for _, row := range rows {
		val, _ := secondField.ValueOf(r.ctx, row.Elem())
		k := dictKey(val)
		relatedByThrough[k] = append(relatedByThrough[k], row)
	}

	dict := make(map[string][]reflect.Value, len(parents))
	for _, t := range throughRows {
		parentKey := dictKey(t[desc.FirstKey])
		secondKey := dictKey(t[desc.SecondLocalKey])
		if rels, ok := relatedByThrough[secondKey]; ok {
			dict[parentKey] = append(dict[parentKey], rels...)
		}
	}

	if err := r.assignToParents(parents, parentField, entry.Relation, dict, isMany, defaultFor); err != nil {
		return err
	}
	return r.recurseNested(rows, nested)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// eagerCallbackTarget is the query a With / Load constraint callback constrains. Laravel hands the
// callback the relation object itself, resolved against a fresh — non-existing — parent instance
// (`Relation::noConstraints(fn () => $this->getModel()->newInstance()->$name())`,
// Eloquent/Builder.php:965), then runs `$constraints($relation)` on it (Builder.php:943). A
// relation-level limit / take branches on `$this->parent->exists` (HasOneOrMany::limit,
// HasOneOrMany.php:555; HasOneOrManyThrough::limit, :772) and routes a non-existing parent through
// the query builder's groupLimit — the row_number() over (partition by <key>) derived table that
// caps rows per parent. Oracle (illuminate/database v12.55.1, executed against sqlite from
// /tmp/eager-callback-limit/eager_callback_limit.php): E1 loads two posts per user through
// `select * from (select *, row_number() over (partition by "gl_posts"."user_id") as "laravel_row"
// from "gl_posts" where "gl_posts"."user_id" in (1, 2)) as "laravel_table" where "laravel_row" <= 2
// order by "laravel_row"`; E2 compiles the same per-country rewrite for the through relation.
//
// BelongsToMany::limit (BelongsToMany.php:1503) carries the same branch (the plain limit for an
// existing parent, the per-parent group limit otherwise, partitioned by the qualified foreign pivot
// key) and MorphToMany inherits it. Oracle (/tmp/eager-m2m-limit/probe.php): B1 eager BtM limit(2)
// compiles the pivot-partitioned rewrite over the joined relation read; B1n the no-callback read is
// the same join without the rewrite.
//
// The Go callback keeps its Query signature, so the relation object — which embeds the builder — is
// what it receives, and the caller unwraps the return value back to the child. Only the kinds whose
// relation objects carry the branch are wrapped: the has-one/many and through kinds plus the
// many-to-many kinds (their relation objects implement Limit/Take). A BelongsTo / MorphTo callback
// observes the same query either way and keeps receiving the builder.
func (r *OrmBuilder) eagerCallbackTarget(child *OrmBuilder, desc *relationDescriptor, protoParent any, relation string) contractsorm.Query {
	if protoParent == nil {
		return child
	}

	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany, relKindHasOneThrough, relKindHasManyThrough,
		relKindMany2Many, relKindMorphToMany:
		return relations.Wrap(child, protoParent, relation, relationInfoFromDescriptor(desc, child, protoParent), relationHooks(child, desc))
	}

	return child
}

// runRelatedQueryCompiled runs one related query that this package compiles itself.
//
// The loader used to hand GORM a statement per chunk; the statement is compiled and run here instead,
// through the same path the rest of the ORM reads with. The child is bound to the relation's related
// model — the same binding the non-eager read installs through relatedQuery — so the model's
// soft-delete scope reaches the eager load the way Laravel's SoftDeletingScope reaches
// Relation::noConstraints' new query (unless WithoutGlobalScopes() removed every scope; see the
// attach in the body). The related model's OTHER (user-defined) global scopes are queued here too,
// through addGlobalScopes below — the same path the non-eager relation read and every other
// compiled read share; the query's own removals (WithoutGlobalScopes) gate them.
//
// protoParent is a fresh instance of the parent model — Laravel's newInstance() — that the relation
// object handed to entry.Callback is built on, so the callback's relation-level limit / take sees a
// non-existing parent and caps rows per parent (eagerCallbackTarget).
func (r *OrmBuilder) runRelatedQueryCompiled(child *OrmBuilder, desc *relationDescriptor, entry eagerLoadEntry, requiredCols []string, protoParent any) ([]reflect.Value, error) {
	// The chunk's IN constraint was written when the child was built; flush anything still queued so
	// the scopes that follow keep their place, the order Eloquent builds eager-load constraints in.
	child = child.flushWheres()

	// The relation's own scope applies first, and the caller's callback layers on top of it.
	if desc.OnQuery != nil {
		if scoped, ok := desc.OnQuery(child).(*OrmBuilder); ok {
			child = scoped
		}
	}

	// OfMany asks for one row per parent, which is a join against a grouped subquery. The relation's
	// own scope ran first, so the constraints it wrote after OfMany — the Go form of Laravel's
	// ofMany closure — are moved into that subquery as the join is built.
	joined := false
	if cfg := child.conditions.oneOfMany; cfg != nil {
		join, args, err := r.oneOfManyJoin(child, desc, cfg, nil, false)
		if err != nil {
			return nil, err
		}
		if join != "" {
			child.query.JoinRaw(join, args, "inner")
			joined = true
		}
	}

	// The callback constrains the relation object the way Laravel's does — see eagerCallbackTarget —
	// and its return value is unwrapped back to the child.
	if entry.Callback != nil {
		if scoped, ok := ormBuilderOf(entry.Callback(r.eagerCallbackTarget(child, desc, protoParent, entry.Relation))); ok {
			child = scoped
		}
	}

	// A caller callback that installed OfMany itself (the With(...) form the contract documents)
	// gets the same join; the marks OfMany recorded delimit its own follow-on constraints.
	if !joined {
		if cfg := child.conditions.oneOfMany; cfg != nil {
			join, args, err := r.oneOfManyJoin(child, desc, cfg, nil, false)
			if err != nil {
				return nil, err
			}
			if join != "" {
				child.query.JoinRaw(join, args, "inner")
				joined = true
			}
		}
	}
	if joined && len(child.query.Selects()) == 0 {
		child.query.SelectRaw(r.ident(desc.RelatedTable, "*"), nil)
	}

	// The child reads the related model - the same binding the non-eager relation read installs
	// through relatedQuery(desc.RelatedModel) - so the model's soft-delete scope reaches the eager
	// read exactly as Laravel's SoftDeletingScope reaches Relation::noConstraints' new query:
	// applySoftDelete appends `deleted_at is null` (or `is not null` under onlyTrashed; WithTrashed
	// drops it). Only the RELATED model is attached here, never the pivot table.
	//
	// A soft-deletable THROUGH model is filtered too: Laravel's HasOneOrManyThrough::performJoin
	// registers a named "SoftDeletableHasManyThrough" global scope - a whereNull on the through
	// table's deleted-at column - whenever the through model uses SoftDeletes, so the eager read
	// carries `and "through"."deleted_at" is null` after the related predicate (oracle,
	// illuminate/database v12.55.1, /tmp/r10/through_softdelete_probe.php N3:
	// `... where "tusers"."country_id" in (1) and "tposts"."deleted_at" is null and "tusers"."deleted_at"
	// is null`). loadThrough records the through model's deleted-at column on the child as a soft-delete field
	// (throughSoftDeleteColumn), kept apart from the through select-list config so the reads that
	// suppress the alias (pluck / value / exists) keep it, and applyThroughSoftDelete lands the predicate
	// at compile time; the
	// mapping read (pivotChild) stays unfiltered, which is inert because the joined related read's
	// through predicate already excludes a soft-deleted through row's related rows. The through
	// model's OTHER (user-defined) global scopes do NOT reach the read - performJoin adds only the
	// soft-delete named scope (oracle, /tmp/r10/through_userscope_probe.php U1).
	//
	// It runs after the callback so the callback's scopes are visible: WithoutGlobalScopes() with no
	// names (recorded as "*") removes every scope, the soft-delete one included, so the binding is
	// skipped - with all scopes gone there is nothing to bind. When only the soft-delete scope is
	// removed by name (WithoutGlobalScopes(contractsorm.SoftDeletingScope)) the binding stays - the
	// related model's user scopes must still queue - and applySoftDelete drops the predicate, the
	// same named gate the non-eager path consults, so the two paths agree (Laravel's
	// withoutGlobalScope(SoftDeletingScope::class) drops it on both; the old non-eager divergence is
	// gone). The related model's user-defined global scopes are queued below (addGlobalScopes):
	// Laravel builds the eager child from the related model's newQuery(), whose registered scopes
	// applyScopes() folds in when the read runs, so a scope registered with withGlobalScope reaches
	// the eager statement exactly as it reaches the non-eager relation read (Related(...).Get) and
	// every other compiled read. addGlobalScopes drops the scopes WithoutGlobalScopes() names; the
	// outer check already handled the all-removed ("*") case. The built-in soft-delete predicate is
	// applied by applySoftDelete.
	if desc.RelatedModel != nil && !slices.Contains(child.conditions.withoutGlobalScopes, "*") {
		child = child.Model(desc.RelatedModel).(*OrmBuilder)
		child = child.addGlobalScopes()
	}

	if len(entry.Columns) > 0 {
		cols := append([]string(nil), entry.Columns...)
		for _, req := range requiredCols {
			if !slices.ContainsFunc(cols, func(c string) bool {
				if c == req {
					return true
				}
				_, suffix, ok := strings.Cut(c, ".")
				return ok && suffix == req
			}) {
				cols = append(cols, req)
			}
		}

		selects := make([]any, 0, len(cols))
		for _, col := range cols {
			selects = append(selects, col)
		}
		child.query.SetSelects(selects)
	}

	statement, bindings, err := child.compileSelect()
	if err != nil {
		return nil, err
	}

	rows, err := child.queryRows(statement, bindings)
	if err != nil {
		return nil, err
	}
	defer errors.Ignore(rows.Close)

	relatedType := indirectStructType(reflect.TypeOf(desc.RelatedModel))
	if relatedType == nil {
		return nil, errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, "no related model")
	}

	slicePtr := reflect.New(reflect.SliceOf(reflect.PointerTo(relatedType)))
	if _, err := child.scanRows(rows, slicePtr.Interface()); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	slice := slicePtr.Elem()
	out := make([]reflect.Value, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		out = append(out, slice.Index(i))
	}

	return out, nil
}

// runChunkedRelatedQueryCompiled runs runRelatedQueryCompiled once per chunk of keys and concatenates
// rows, so a callback's constraints and the column pruning are applied per chunk.
func (r *OrmBuilder) runChunkedRelatedQueryCompiled(keys []any, desc *relationDescriptor, entry eagerLoadEntry, requiredCols []string, protoParent any, buildChild func(chunk []any) *OrmBuilder) ([]reflect.Value, error) {
	chunks := chunkKeys(keys, r.chunkSize())
	var all []reflect.Value
	for _, chunk := range chunks {
		rows, err := r.runRelatedQueryCompiled(buildChild(chunk), desc, entry, requiredCols, protoParent)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
	}

	return all, nil
}

// oneOfManyJoin renders the join every read of a one-of-many relation carries: one nested grouped
// subquery per declared (column, aggregate) pair — each aggregating its own column, carrying every
// earlier column as a tie-break through min(...), and joining the level below — the outermost one
// joined on every aggregate column. The single-column shape is Laravel's (oracle, sqlite):
//
//	inner join (select MAX("logins"."id") as "id_aggregate", "logins"."user_id" from "logins" where
//	"logins"."user_id" = ? and "logins"."user_id" is not null group by "logins"."user_id") as
//	"latest_login" on "latest_login"."id_aggregate" = "logins"."id" and "latest_login"."user_id" =
//	"logins"."user_id"
//
// and the two-column price() carries the nested joins the oracle compiles:
//
//	select "prices".* from "prices" inner join (select max("prices"."id") as "id_aggregate",
//	min("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices"
//	inner join (select max("prices"."published_at") as "published_at_aggregate", "prices"."user_id"
//	from "prices" where ... group by "prices"."user_id") as "price" on
//	"price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" =
//	"prices"."user_id" where ... group by "prices"."user_id") as "price" on "price"."id_aggregate" =
//	"prices"."id" and "price"."published_at_aggregate" = "prices"."published_at" and
//	"price"."user_id" = "prices"."user_id"
//
// The related model's key is appended as a MAX pair when the declaration omits it, so ties always
// resolve to one row. The alias is the relation name in snake case ("_of_many" appended when it
// equals the related table's name), matching Laravel's getDefaultOneOfManyJoinAlias, and each
// aggregate column is "<column>_aggregate". Constraints the relation's own of-many chain wrote
// after the configuration (Laravel's ofMany closure) are moved off builder and into every subquery,
// and so is the eager loader's parent-key in-list — with the morph-type filter a morph relation
// follows it with. Laravel's addEagerConstraints writes that in-list through getRelationQuery,
// which for a one-of-many relation is the innermost aggregate subquery, so the eager read carries
// no constraint on the outer query. readPath asks for the relation constraints to be re-stated
// inside the innermost subquery too (foreign key = parent, foreign key is not null; a through
// relation's through key = parent instead), which Laravel's ofMany re-applies through
// addConstraints.
//
// It returns the UNKEYED join fragment — "(sub) as alias on ..." with no join keyword — because the
// join list is rendered with a keyword prefix appended (JoinRaw(..., "inner")); carrying an
// "inner join " prefix here would render the keyword twice.
func (r *OrmBuilder) oneOfManyJoin(builder *OrmBuilder, desc *relationDescriptor, cfg *oneOfManyConfig, parentValue any, readPath bool) (string, []any, error) {
	if cfg == nil {
		return "", nil, nil
	}

	alias := oneOfManyAlias(desc)
	var (
		groupCols   []string // the columns every subquery selects and groups by besides its aggregates
		joinTail    []string // the ON conditions appended after a subquery's aggregate columns
		inListCol   string   // the outer column the eager in-list was written on
		throughJoin string   // the inner join a through subquery carries on every level
	)

	switch desc.Kind {
	case relKindHasOne, relKindMorphOne:
		if len(desc.References) == 0 {
			return "", nil, nil
		}
		ref := desc.References[0]
		foreign := r.ident(desc.RelatedTable, ref.ForeignColumn)
		groupCols = []string{foreign}
		joinTail = []string{r.ident(alias, ref.ForeignColumn) + " = " + foreign}
		inListCol = desc.RelatedTable + "." + ref.ForeignColumn
		if desc.Kind == relKindMorphOne {
			// MorphOne::addOneOfManyJoinSubQueryConstraints constrains the type first, then the
			// foreign key — the reverse of the select and group-by order.
			morph := r.ident(desc.RelatedTable, desc.MorphTypeColumn)
			groupCols = append(groupCols, morph)
			joinTail = []string{r.ident(alias, desc.MorphTypeColumn) + " = " + morph, r.ident(alias, ref.ForeignColumn) + " = " + foreign}
		}
	case relKindHasOneThrough:
		key := r.ident(desc.ThroughTable, desc.FirstKey)
		groupCols = []string{key}
		joinTail = []string{r.ident(alias, desc.FirstKey) + " = " + key}
		inListCol = desc.ThroughTable + "." + desc.FirstKey
		throughJoin = r.throughJoinFragment(desc)
	default:
		// Ignored on unsupported kinds rather than erroring, so a generic with-callback that calls
		// LatestOfMany does not break unrelated relations downstream.
		return "", nil, nil
	}

	columns := oneOfManyEffectiveColumns(desc, cfg)

	// The eager loader's parent-key in-list (and a morph relation's type filter) was written when
	// the child was built; pop it so the innermost subquery re-states it, and shift the marks
	// OfMany recorded by the popped clauses.
	eagerKeys, eagerMorph, poppedWheres, poppedBindings := builder.takeEagerOneOfManyConstraints(desc)
	clauses := *cfg
	clauses.whereMark -= poppedWheres
	clauses.whereBindMark -= poppedBindings
	if clauses.whereMark < 0 {
		clauses.whereMark = 0
	}
	if clauses.whereBindMark < 0 {
		clauses.whereBindMark = 0
	}
	movedWheres, movedWhereBindings, movedJoins, movedJoinBindings := builder.takeOneOfManyClauses(&clauses)

	// Build the levels innermost first; every level is aliased with the relation name, the way
	// Laravel's ofMany chains them.
	var (
		levelSQL  string
		levelArgs []any
		levelCols []string
	)

	for i, col := range columns {
		sub := r.subqueryBuilder(desc.RelatedTable)

		if i == 0 {
			// The closure constraints run before the relation constraints, the order ofMany writes
			// them in: the closure executes inside the loop and addConstraints runs at its end —
			// where a through relation's through join is performed too.
			for _, join := range movedJoins {
				sub.State().Joins = append(sub.State().Joins, join)
			}
			if len(movedJoinBindings) > 0 {
				_ = sub.AddBinding(movedJoinBindings, "join")
			}
			if throughJoin != "" {
				sub.JoinRaw(throughJoin, nil, "inner")
			}
		} else {
			// A later level performs the through join while it is built, then the closure
			// constraints, and joins the level below last (Laravel's beforeQuery callback).
			if throughJoin != "" {
				sub.JoinRaw(throughJoin, nil, "inner")
			}
			for _, join := range movedJoins {
				sub.State().Joins = append(sub.State().Joins, join)
			}
			if len(movedJoinBindings) > 0 {
				_ = sub.AddBinding(movedJoinBindings, "join")
			}

			on := make([]string, 0, len(levelCols)+len(joinTail))
			for _, prev := range levelCols {
				on = append(on, r.ident(alias, prev+"_aggregate")+" = "+r.ident(desc.RelatedTable, prev))
			}
			on = append(on, joinTail...)
			sub.JoinRaw("("+levelSQL+") as "+r.ident(alias)+" on "+strings.Join(on, " and "), levelArgs, "inner")
		}

		selectCols := []string{col.aggregate + "(" + r.ident(desc.RelatedTable, col.column) + ") as " + r.ident(col.column+"_aggregate")}
		for _, prev := range levelCols {
			selectCols = append(selectCols, "min("+r.ident(desc.RelatedTable, prev)+") as "+r.ident(prev+"_aggregate"))
		}
		selectCols = append(selectCols, groupCols...)
		sub.SelectRaw(strings.Join(selectCols, ", "), nil)

		if len(movedWheres) > 0 {
			sub.State().Wheres = append(sub.State().Wheres, movedWheres...)
			_ = sub.AddBinding(movedWhereBindings, "where")
		}

		if i == 0 {
			if eagerKeys != nil {
				// Eloquent's addEagerConstraints writes the parent-key in-list into the aggregate
				// subquery (getRelationQuery is the subquery for a one-of-many relation).
				// Relation::whereInMethod inlines the keys through whereIntegerInRaw only when the
				// parent key is an integer; a string-keyed parent (UUID) keeps its bindings, so the
				// subquery reads `in (?, ?)` with the keys bound (oracle, sqlite). A morph relation's
				// type filter follows the in-list (oracle: `where "logos"."logoable_id" in (1, 2)
				// and "logos"."logoable_type" = ?`).
				if allIntegerKeys(eagerKeys) {
					sub.WhereIntegerInRaw(inListCol, eagerKeys, core.BooleanAnd, false)
				} else {
					sub.WhereIn(inListCol, eagerKeys, core.BooleanAnd, false)
				}
				if eagerMorph {
					sub.WhereRaw(r.ident(desc.RelatedTable, desc.MorphTypeColumn)+" = ?", []any{desc.MorphValue}, core.BooleanAnd)
				}
			}

			if readPath {
				// HasOneOrMany::addConstraints / HasOneOrManyThrough::addConstraints, re-applied to
				// the innermost subquery at the end of ofMany: the morph type filter first for
				// MorphOne, then the key and — except for a through relation — its not-null half.
				if desc.Kind == relKindMorphOne {
					sub.WhereRaw(r.ident(desc.RelatedTable, desc.MorphTypeColumn)+" = ?", []any{desc.MorphValue}, core.BooleanAnd)
				}
				if desc.Kind == relKindHasOneThrough {
					sub.WhereRaw(r.ident(desc.ThroughTable, desc.FirstKey)+" = ?", []any{parentValue}, core.BooleanAnd)
				} else {
					foreign := r.ident(desc.RelatedTable, desc.References[0].ForeignColumn)
					sub.WhereRaw(foreign+" = ?", []any{parentValue}, core.BooleanAnd)
					sub.WhereRaw(foreign+" is not null", nil, core.BooleanAnd)
				}
			}
		}

		// The related model's soft-delete predicate and its remaining global scopes reach every
		// aggregate level the way Laravel's scopes do: the SoftDeletingScope first (the trait boots
		// before the model's own scopes), then each user scope in declaration order; the relation's
		// own removals - its OnQuery's withTrashed()/onlyTrashed()/withoutGlobalScope() - govern
		// both, the way mergeConstraintsFrom folds the relation's removedScopes into the ofMany
		// subquery. The caller's existence/count closure does NOT reach this subquery (oracle,
		// /tmp/r13v/ofmany_probe.php + ofmany2_probe.php + ofmany3_probe.php: the aggregate carries
		// `and "om_posts"."deleted_at" is null and "published" = ?`, dropping either under the
		// relation's removals; a caller's withoutGlobalScopes() removes them only from the outer
		// query).
		removals := scopeRemovalsOf(builder)
		r.applySubquerySoftDelete(sub, desc.RelatedTable, desc.RelatedModel, removals)
		r.applyRelatedGlobalScopes(sub, desc.RelatedModel, removals)

		// A soft-deletable through model's predicate reaches every aggregate level too, after the
		// related model's: each level performs the through join, so the through table is filtered
		// there the way performJoin's named scope filters the relation read (oracle,
		// /tmp/r10/through_softdelete_probe.php N9/N10 - the through predicate sits in the
		// aggregate subquery and on the outer query).
		r.applySubquerySoftDelete(sub, desc.ThroughTable, desc.ThroughModel, subqueryScopeRemovals{})

		sub.GroupByRaw(strings.Join(groupCols, ", "), nil)
		levelSQL, levelArgs = sub.ToSelectSQL()
		levelCols = append([]string{col.column}, levelCols...)
	}

	on := make([]string, 0, len(levelCols)+len(joinTail))
	for _, col := range levelCols {
		on = append(on, r.ident(alias, col+"_aggregate")+" = "+r.ident(desc.RelatedTable, col))
	}
	on = append(on, joinTail...)

	return fmt.Sprintf("(%s) as %s on %s", levelSQL, r.ident(alias), strings.Join(on, " and ")), levelArgs, nil
}

// oneOfManyEffectiveColumns is the configuration's columns with the related model's key appended
// when the declaration omits it — Laravel's ofMany rule: the string form of OfMany carries the key
// with the same aggregate (keyAggregate), the ordered forms append it with MAX.
func oneOfManyEffectiveColumns(desc *relationDescriptor, cfg *oneOfManyConfig) []oneOfManyColumn {
	key := "id"
	if schema, err := parseSchema(desc.RelatedModel); err == nil && len(schema.PrimaryFields) > 0 {
		key = schema.PrimaryFields[0].DBName
	}

	for _, col := range cfg.columns {
		if col.column == key {
			return cfg.columns
		}
	}

	aggregate := cfg.keyAggregate
	if aggregate == "" {
		aggregate = "MAX"
	}

	return append(append([]oneOfManyColumn(nil), cfg.columns...), oneOfManyColumn{column: key, aggregate: aggregate})
}

// oneOfManyAlias names the aggregate subquery the way Laravel's getDefaultOneOfManyJoinAlias does:
// the relation name in snake case, with "_of_many" appended when that name equals the related
// table's name.
func oneOfManyAlias(desc *relationDescriptor) string {
	alias := str.Of(desc.Name).Snake().String()
	if alias == desc.RelatedTable {
		alias += "_of_many"
	}

	return alias
}

// takeOneOfManyClauses removes every clause added after OfMany was called and returns them, so the
// join can move the relation's of-many closure constraints into the aggregate subquery. Laravel's
// ofMany closure receives the subquery builder and writes to it directly; the Go formulation chains
// the closure constraints after OfMany inside the relation's OnQuery, and the marks OfMany recorded
// delimit them.
func (b *OrmBuilder) takeOneOfManyClauses(cfg *oneOfManyConfig) (wheres []core.Where, whereBindings []any, joins []core.Join, joinBindings []any) {
	state := b.query.State()

	if cfg.whereMark < len(state.Wheres) {
		wheres = append([]core.Where(nil), state.Wheres[cfg.whereMark:]...)
		state.Wheres = state.Wheres[:cfg.whereMark]
	}
	if cfg.whereBindMark < len(state.Bindings["where"]) {
		whereBindings = append([]any(nil), state.Bindings["where"][cfg.whereBindMark:]...)
		state.Bindings["where"] = state.Bindings["where"][:cfg.whereBindMark]
	}
	if cfg.joinMark < len(state.Joins) {
		joins = append([]core.Join(nil), state.Joins[cfg.joinMark:]...)
		state.Joins = state.Joins[:cfg.joinMark]
	}
	if cfg.joinBindMark < len(state.Bindings["join"]) {
		joinBindings = append([]any(nil), state.Bindings["join"][cfg.joinBindMark:]...)
		state.Bindings["join"] = state.Bindings["join"][:cfg.joinBindMark]
	}

	return wheres, whereBindings, joins, joinBindings
}

// takeEagerOneOfManyConstraints pops the constraints the eager loader wrote when it built the child
// — the parent-key in-list, and the morph-type filter that follows it for a morph relation — and
// returns them, so the join can re-state them inside the innermost aggregate subquery the way
// Laravel's addEagerConstraints writes through getRelationQuery. Only the loader's own leading
// clauses are touched: the in-list must be the child's first where clause and name the column the
// relation constrains — the related foreign key, or the through key for a through relation — and
// every value must bind one-to-one (a subquery value list is left alone). The counts let the caller
// shift the marks OfMany recorded.
func (b *OrmBuilder) takeEagerOneOfManyConstraints(desc *relationDescriptor) (keys []any, morphType bool, poppedWheres, poppedBindings int) {
	var inListColumn string
	switch desc.Kind {
	case relKindHasOne, relKindMorphOne:
		if len(desc.References) == 0 {
			return nil, false, 0, 0
		}
		inListColumn = desc.RelatedTable + "." + desc.References[0].ForeignColumn
	case relKindHasOneThrough:
		inListColumn = desc.ThroughTable + "." + desc.FirstKey
	default:
		return nil, false, 0, 0
	}

	state := b.query.State()
	if len(state.Wheres) == 0 {
		return nil, false, 0, 0
	}

	first := state.Wheres[0]
	if first.Type != core.WhereInType || fmt.Sprint(first.Column) != inListColumn {
		return nil, false, 0, 0
	}

	bound := querybuilder.CleanBindings(first.Values)
	if len(bound) != len(first.Values) {
		// An expression in the list does not bind one-to-one; leave the child as it was built.
		return nil, false, 0, 0
	}

	keys = append([]any(nil), first.Values...)
	state.Wheres = state.Wheres[1:]
	if len(state.Bindings["where"]) >= len(bound) {
		state.Bindings["where"] = state.Bindings["where"][len(bound):]
	} else {
		state.Bindings["where"] = nil
	}
	poppedWheres, poppedBindings = 1, len(bound)

	if desc.Kind == relKindMorphOne && len(state.Wheres) > 0 {
		next := state.Wheres[0]
		if next.Type == core.WhereBasic && fmt.Sprint(next.Column) == desc.RelatedTable+"."+desc.MorphTypeColumn &&
			fmt.Sprint(next.Value) == desc.MorphValue {
			state.Wheres = state.Wheres[1:]
			if len(state.Bindings["where"]) > 0 {
				state.Bindings["where"] = state.Bindings["where"][1:]
			}
			poppedWheres++
			poppedBindings++
			morphType = true
		}
	}

	return keys, morphType, poppedWheres, poppedBindings
}

// allIntegerKeys reports whether every eager key is an integer kind — the Go stand-in for the
// integer key-type check Relation::whereInMethod makes before it chooses whereIntegerInRaw. Keys
// of any other type (a string-keyed parent) bind through whereIn instead, as Laravel's do.
func allIntegerKeys(keys []any) bool {
	for _, key := range keys {
		switch key.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		default:
			return false
		}
	}

	return true
}

// pivotChild returns the child query a many-to-many loader reads its pivot rows with: one pivot
// table, a select list, and the pivot's parent key matched against the chunk.
//
// It is relatedChild for a table that carries no model of its own: the bare table name is stored the
// same way a related child stores its model's table, and the grammar applies the connection prefix
// once when the statement is rendered. The select list keeps the bare column names the pivot table
// declares, because the rows come back as a column-named map that the hydration loop indexes by
// those names.
func (r *OrmBuilder) pivotChild(table string, column string, values []any, columns []string) *OrmBuilder {
	child := r.relatedChild(table, column, values)

	selects := make([]any, 0, len(columns))
	for _, col := range columns {
		selects = append(selects, col)
	}
	child.query.SetSelects(selects)

	return child
}

// runChunkedPivotQueryCompiled runs the pivot-table read once per chunk of keys and accumulates the
// rows into a single []map[string]any, keyed by column name. It is the reader the loaders call:
// the pivot and through reads are compiled here instead of being handed to GORM.
func (r *OrmBuilder) runChunkedPivotQueryCompiled(keys []any, desc *relationDescriptor, buildChild func(chunk []any) *OrmBuilder) ([]map[string]any, error) {
	chunks := chunkKeys(keys, r.chunkSize())
	var all []map[string]any
	for _, chunk := range chunks {
		rows, err := r.findMapsCompiled(buildChild(chunk), desc)
		if err != nil {
			return nil, err
		}

		all = append(all, rows...)
	}

	return all, nil
}

// findMapsCompiled compiles one pivot child and reads its rows into column-keyed maps. The
// descriptor's OnPivotQuery is applied first, so the per-relation pivot scope reaches every pivot
// read without the loader restating it.
func (r *OrmBuilder) findMapsCompiled(child *OrmBuilder, desc *relationDescriptor) ([]map[string]any, error) {
	if scoped, ok := applyOnPivotQueryToQuery(r.queryGrammar, child, desc).(*OrmBuilder); ok {
		child = scoped
	}

	statement, bindings, err := child.compileSelect()
	if err != nil {
		return nil, err
	}

	rows, err := child.queryRows(statement, bindings)
	if err != nil {
		return nil, err
	}
	defer errors.Ignore(rows.Close)

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var out []map[string]any
	for rows.Next() {
		row, err := scanRowMap(rows, columns)
		if err != nil {
			return nil, err
		}

		out = append(out, row)
	}

	return out, rows.Err()
}

// assignToParents writes the dictionary entries onto each parent's relation field using
// setRelationField. When isMany is false and a parent has multiple matches, only the first one is
// assigned (HasOne / BelongsTo / MorphOne / HasOneThrough cases).
//
// defaultFor, when non-nil, builds the relation's default related row for a parent the read left
// unmatched — an empty parent key, or a key nothing in the dictionary answered. That is the merge
// Laravel's eager load performs: initRelation seeds every model with getDefaultFor(model), then
// match overwrites the ones a result was found for, so an existing row always wins.
func (r *OrmBuilder) assignToParents(parents []reflect.Value, parentField *schemaField, relation string, dict map[string][]reflect.Value, isMany bool, defaultFor func(parent reflect.Value) ([]reflect.Value, error)) error {
	for _, parent := range parents {
		val, zero := parentField.ValueOf(r.ctx, parent)
		if zero {
			if !isMany {
				if defaultFor != nil {
					rows, err := defaultFor(parent)
					if err != nil {
						return err
					}
					if err := setRelationField(parent, relation, rows); err != nil {
						return err
					}
				}
				continue
			}
			if err := setRelationField(parent, relation, nil); err != nil {
				return err
			}
			continue
		}
		match := dict[dictKey(val)]
		if !isMany {
			if len(match) > 1 {
				match = match[:1]
			}
			if len(match) == 0 && defaultFor != nil {
				rows, err := defaultFor(parent)
				if err != nil {
					return err
				}
				if err := setRelationField(parent, relation, rows); err != nil {
					return err
				}
				continue
			}
		}
		if err := setRelationField(parent, relation, match); err != nil {
			return err
		}
	}
	return nil
}

// applyInverseRelations writes the parent back onto every related model a has-one-or-many /
// morph-one-or-many load matched to it — the write-back a Laravel relation declaring
// ->inverse()/->chaperone() performs once its query has run
// (SupportsInverseRelations::applyInverseRelationToCollection, applied per parent by
// HasOneOrMany::matchOneOrMany on the eager path and by the after-query callback on
// getResults). The very parent instance is stored — Eloquent's assertSame($parent,
// $child->$inverse), no extra query — so it is applied before the parent's own destination
// field is written: a value-slice destination copies the child, and the copy must already carry
// the inverse. dict is keyed the way the loaders built it (by the child's foreign-key value);
// one caps the match at a single child for HasOne / MorphOne, mirroring matchOneOrMany's to-one
// truncation. A parent whose key is empty or matches no child writes nothing, exactly as
// matchOneOrMany iterates only dictionary hits.
func (r *OrmBuilder) applyInverseRelations(desc *relationDescriptor, parents []reflect.Value, parentField *schemaField, dict map[string][]reflect.Value, one bool) error {
	if desc.Inverse == "" {
		return nil
	}
	for _, parent := range parents {
		val, zero := parentField.ValueOf(r.ctx, parent)
		if zero {
			continue
		}
		match := dict[dictKey(val)]
		if one && len(match) > 1 {
			match = match[:1]
		}
		for _, child := range match {
			if err := r.applyInverseRelationToModel(desc, child, parent); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyInverseRelationToModel stores parent on the related model's inverse relation —
// SupportsInverseRelations::applyInverseRelationToModel, the per-model half of the chaperone.
// child is the pointer the loaders carry; the field receives the pointer to the same parent row
// the model was matched to, matching Eloquent's instance identity. The name comes from the live
// inverse state, not the bare declaration: Eloquent's applyInverseRelationToModel reads
// getInverseRelationship(), null after withoutChaperone / withoutInverse, so a cleared relation
// links neither read results nor default instances (oracle probe_c.php E/F/H report linked false);
// the eager / never-installed paths fall back to the declaration through inverseWriteBackName.
func (r *OrmBuilder) applyInverseRelationToModel(desc *relationDescriptor, child reflect.Value, parent reflect.Value) error {
	inverse := r.inverseWriteBackName(desc)
	if inverse == "" {
		return nil
	}
	if child.Kind() == reflect.Pointer {
		child = child.Elem()
	}
	return setRelationField(child, inverse, []reflect.Value{inverseParentRef(parent)})
}

// inverseParentRef hands the child's relation field the parent instance itself. Parents arrive as
// addressable struct values (collectEagerParents and recurseNested both carry addressable
// elements), so the stored pointer is the one the caller already holds; a non-addressable value
// falls back to a copy, like parentAsAny.
func inverseParentRef(parent reflect.Value) reflect.Value {
	if parent.CanAddr() {
		return parent.Addr()
	}
	dup := reflect.New(parent.Type())
	dup.Elem().Set(parent)

	return dup
}

func (r *OrmBuilder) recurseNested(rows []reflect.Value, nested []eagerLoadEntry) error {
	if len(rows) == 0 || len(nested) == 0 {
		return nil
	}
	nestedParents := make([]reflect.Value, 0, len(rows))
	for _, row := range rows {
		nestedParents = append(nestedParents, row.Elem())
	}
	return r.runEagerLoads(nestedParents, nested)
}

// maybeRecurseEmpty handles the no-parent-keys fast path: with no keys to load against, a to-one
// parent keeps the relation's declared default (Laravel's initRelation seeds every model before
// the query) and stays nil otherwise, while a to-many parent gets the empty slice. Nested loads
// are skipped either way.
func (r *OrmBuilder) maybeRecurseEmpty(parents []reflect.Value, relation string, isMany bool, _ []eagerLoadEntry, defaultFor func(parent reflect.Value) ([]reflect.Value, error)) error {
	if !isMany {
		if defaultFor == nil {
			return nil
		}
		for _, parent := range parents {
			rows, err := defaultFor(parent)
			if err != nil {
				return err
			}
			if err := setRelationField(parent, relation, rows); err != nil {
				return err
			}
		}
		return nil
	}
	for _, parent := range parents {
		if err := setRelationField(parent, relation, nil); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Reflect / extraction helpers
// ---------------------------------------------------------------------------

// collectEagerParents extracts the addressable struct values from dest. dest may be *Struct,
// *[]Struct, or *[]*Struct; each form yields a flat slice of struct values whose fields can be
// mutated.
func collectEagerParents(dest any) ([]reflect.Value, error) {
	if dest == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, nil
	}
	elem := rv.Elem()
	switch elem.Kind() {
	case reflect.Struct:
		return []reflect.Value{elem}, nil
	case reflect.Slice:
		out := make([]reflect.Value, 0, elem.Len())
		for i := 0; i < elem.Len(); i++ {
			item := elem.Index(i)
			if item.Kind() == reflect.Pointer {
				if item.IsNil() {
					continue
				}
				item = item.Elem()
			}
			if item.Kind() != reflect.Struct {
				continue
			}
			out = append(out, item)
		}
		return out, nil
	}
	return nil, nil
}

// newSampleModel returns a fresh pointer-to-struct of the same type as parent. resolveRelation
// expects an addressable model instance (it parses the schema and inspects its tags), and we
// don't want to hand it one of our actual parent rows (which may carry mutated fields).
func newSampleModel(parent reflect.Value) any {
	t := parent.Type()
	return reflect.New(t).Interface()
}

// extractKeys pulls the unique non-zero values of field from the parent slice.
func extractKeys(r *OrmBuilder, parents []reflect.Value, field *schemaField) []any {
	seen := make(map[string]struct{}, len(parents))
	out := make([]any, 0, len(parents))
	for _, parent := range parents {
		val, zero := field.ValueOf(r.ctx, parent)
		if zero {
			continue
		}
		// A pointer-typed field reads as a pointer; the IN list and the dedup key both take the
		// value it addresses, the scalar every non-pointer key field contributes.
		val = derefKeyValue(val)
		if val == nil {
			// A wrapper that unwraps to no value (NullString{Valid: false}) is as absent as a
			// nil pointer.
			continue
		}
		k := dictKey(val)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, val)
	}
	return out
}

// derefKeyValue unwraps pointer values down to the value they address, so a key read off a
// pointer-typed model field — the Go shape of a nullable foreign key — is the scalar itself,
// never the pointer fmt.Sprint would render as an address. A driver.Valuer referent unwraps one
// level further to its Value() — the database value keys the comparison, where fmt.Sprint of the
// wrapper ("{1 true}") never could — and an invalid wrapper (NullString{Valid: false}) or a nil
// pointer has no value at all: it reads as nil.
func derefKeyValue(v any) any {
	for {
		rv := reflect.ValueOf(v)
		if !rv.IsValid() {
			return nil
		}
		if rv.Kind() == reflect.Pointer && rv.IsNil() {
			return nil
		}
		// A driver.Valuer — sql.NullString, sql.NullInt64, a custom column type — carries its
		// key in its Value(); stringifying the wrapper itself can never equal the related row's
		// key.
		if valuer, ok := v.(driver.Valuer); ok {
			return unwrapValuerValue(valuer)
		}
		if rv.Kind() != reflect.Pointer {
			if reflect.PointerTo(rv.Type()).Implements(valuerInterface) {
				// Value() lives on the pointer receiver and the key read off the field is a copy;
				// take its address the way BindingValueOf does for binding.
				addr := reflect.New(rv.Type())
				addr.Elem().Set(rv)

				return unwrapValuerValue(addr.Interface().(driver.Valuer))
			}

			return v
		}
		v = rv.Elem().Interface()
	}
}

// unwrapValuerValue reduces a driver.Valuer to the value it keys on: nil when the wrapper is
// invalid or Value() errors — absent, exactly like a nil pointer — the string form of []byte
// text, and the driver value itself (string, int64) otherwise.
func unwrapValuerValue(valuer driver.Valuer) any {
	value, err := valuer.Value()
	if err != nil || value == nil {
		return nil
	}
	if data, ok := value.([]byte); ok {
		return string(data)
	}

	return value
}

// dictKey reduces any value to a canonical string for use as a map key, paving over the type
// mismatch between Go field types (uint, int64, string) and database-layer scan types
// (often int64 or []byte). Pointer values unwrap to the value they address first, so a pointer
// FK keys the dictionary by value rather than by address. Mirrors fedaco's _getDictionaryKey.
func dictKey(v any) string {
	v = derefKeyValue(v)
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// chunkSize returns the eager-load IN-clause chunk size, falling back to the default when the
// config key is absent. A non-positive value disables chunking.
func (r *OrmBuilder) chunkSize() int {
	if r.config == nil {
		return defaultEagerLoadChunkSize
	}
	return r.config.GetInt("database.eager_load_chunk_size", defaultEagerLoadChunkSize)
}

// chunkKeys splits keys into batches of at most size. Returns the input unchanged in a single
// batch when size <= 0 or len(keys) <= size, which lets callers stay on the cheap single-query
// path for typical workloads.
func chunkKeys(keys []any, size int) [][]any {
	if size <= 0 || len(keys) <= size {
		return [][]any{keys}
	}
	return lo.Chunk(keys, size)
}

// setRelationField writes loaded rows back to parent's relation field. Supports *Model,
// []*Model, []Model, and `any` (interface) field shapes — the last is used by MorphTo, where the
// concrete loaded type is determined per row from the morph map.
func setRelationField(parent reflect.Value, fieldName string, rows []reflect.Value) error {
	field := parent.FieldByName(fieldName)
	if !field.IsValid() {
		return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())
	}
	if !field.CanSet() {
		return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())
	}

	switch field.Kind() {
	case reflect.Interface:
		if len(rows) == 0 {
			field.Set(reflect.Zero(field.Type()))
			return nil
		}
		row := rows[0]
		if row.Type().Implements(field.Type()) {
			field.Set(row)
			return nil
		}
		// `any` (empty interface) — anything implements it.
		if field.Type().NumMethod() == 0 {
			field.Set(row)
			return nil
		}
		return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())

	case reflect.Pointer:
		if len(rows) == 0 {
			field.Set(reflect.Zero(field.Type()))
			return nil
		}
		row := rows[0]
		if row.Type() == field.Type() {
			field.Set(row)
			return nil
		}
		if row.Kind() == reflect.Pointer && row.Type().Elem() == field.Type().Elem() {
			field.Set(row)
			return nil
		}
		return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())

	case reflect.Slice:
		elemType := field.Type().Elem()
		out := reflect.MakeSlice(field.Type(), 0, len(rows))
		for _, row := range rows {
			switch elemType.Kind() {
			case reflect.Pointer:
				if row.Type() == elemType {
					out = reflect.Append(out, row)
					continue
				}
				if row.Kind() == reflect.Pointer && row.Type().Elem() == elemType.Elem() {
					out = reflect.Append(out, row)
					continue
				}
				return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())
			case reflect.Struct:
				if row.Kind() == reflect.Pointer && row.Type().Elem() == elemType {
					out = reflect.Append(out, row.Elem())
					continue
				}
				if row.Type() == elemType {
					out = reflect.Append(out, row)
					continue
				}
				return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())
			default:
				return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())
			}
		}
		field.Set(out)
		return nil
	}
	return errors.OrmEagerLoadCannotAssign.Args(fieldName, parent.Type().String())
}

// pivotHydrationPlan precomputes everything writePivotField needs to copy a row of pivot column
// values into the configured pivot field on each eager-loaded related model. It is built once
// per loadMany2Many / loadMorphToMany invocation by preparePivotHydration. nil means "no Pivot
// hydration" (related model has no field by desc.pivotField).
type pivotHydrationPlan struct {
	// fieldName is the struct field on the related model that we hydrate (typically "Pivot").
	fieldName string
	// extraColumns is the pivot SELECT list contributed by the pivot struct (every db-tagged
	// field's column name), not including the two FK columns which loadMany2Many always selects.
	extraColumns []string
	// fieldByColumn maps each db column name to the *schemaField that owns it on the pivot
	// struct. Used by writePivotField to set struct fields from the SELECT row.
	fieldByColumn map[string]*schemaField
}

// preparePivotHydration inspects the related model for a field named desc.pivotField. When found,
// returns a plan that drives the pivot SELECT list and field-by-field hydration. Returns nil (no
// error) when the related model has no field by that name — pivot data still flows through the
// SELECT but nothing is surfaced. Returns an error when the field exists but isn't a struct
// (catches typos like `Pivot string`).
func preparePivotHydration(r *OrmBuilder, desc *relationDescriptor) (*pivotHydrationPlan, error) {
	return preparePivotHydrationFor(desc.RelatedModel, desc.PivotField)
}

// preparePivotHydrationFor builds the plan for one pivot field name on a related model — the core
// preparePivotHydration (the declaration-time path) and OrmBuilder.ConfigurePivotRead (the
// relation object's call-time As/WithPivot capability) share. A nil or non-struct model, or a
// model without a field by the name, yields (nil, nil); a field that exists but is not a struct
// yields the named OrmRelationPivotFieldNotStruct error.
func preparePivotHydrationFor(relatedModel any, pivotField string) (*pivotHydrationPlan, error) {
	relatedType := reflect.TypeOf(relatedModel)
	if relatedType == nil {
		return nil, nil
	}
	if relatedType.Kind() == reflect.Pointer {
		relatedType = relatedType.Elem()
	}
	if relatedType.Kind() != reflect.Struct {
		return nil, nil
	}
	pivotStructField, ok := relatedType.FieldByName(pivotField)
	if !ok {
		// No field by this name — silently skip hydration; the relation still works for joining.
		return nil, nil
	}
	if pivotStructField.Type.Kind() != reflect.Struct {
		return nil, errors.OrmRelationPivotFieldNotStruct.Args(
			relatedType.String(), pivotField, pivotStructField.Type.Kind().String(),
		)
	}
	// Parse the field type's GORM schema by instantiating a zero value of it.
	usingSchema, err := parseSchema(reflect.New(pivotStructField.Type).Interface())
	if err != nil {
		return nil, err
	}
	cols := make([]string, 0, len(usingSchema.Fields))
	byCol := make(map[string]*schemaField, len(usingSchema.Fields))
	for _, f := range usingSchema.Fields {
		if f.DBName == "" {
			continue
		}
		cols = append(cols, f.DBName)
		byCol[f.DBName] = f
	}
	return &pivotHydrationPlan{
		fieldName:     pivotField,
		extraColumns:  cols,
		fieldByColumn: byCol,
	}, nil
}

// writePivotField copies the column values in data into rv's pivot struct field (named
// plan.fieldName), using plan.fieldByColumn to resolve column names to *schemaFields on the
// pivot struct. Caller guarantees plan is non-nil.
func writePivotField(ctx context.Context, rv reflect.Value, data map[string]any, plan *pivotHydrationPlan) error {
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	pivotField := rv.FieldByName(plan.fieldName)
	if !pivotField.IsValid() || !pivotField.CanSet() {
		return nil
	}
	for col, val := range data {
		f, ok := plan.fieldByColumn[col]
		if !ok {
			continue
		}
		if err := f.Set(ctx, pivotField, val); err != nil {
			return err
		}
	}
	return nil
}
