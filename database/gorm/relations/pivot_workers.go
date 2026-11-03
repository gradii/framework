package relations

import (
	"fmt"
	"reflect"
	"strconv"
	"time"

	dbcontract "github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/errors"
)

// This file is S2 step 3 of the gorm reduction (LARAVEL_1TO1_ALIGNMENT_REVIEW.md §C.4): the
// many-to-many / pivot Tier-2 write workers of database/gorm/relation_writes.go — the attach /
// detach / sync / toggle / update-existing-pivot engines of Eloquent's InteractsWithPivotTable —
// ported as free functions over the widened Builder and the RelationInfo projection. The 18
// *OrmBuilder pivot shells stay where Go method ownership keeps them and forward here one line
// each; the relation leaves (many2many.go / morph_to_many.go) keep calling the shells through the
// Builder interface, so the OrFail transaction wrapper's re-dispatch onto the transaction session
// (session.AttachRelation, ...) reaches the same forwarders.
//
// Nothing here changes behavior: every function is the gorm original transcribed statement for
// statement, each gorm-internal member replaced by the seam twin the decision table above Builder
// names — pivotTableQuery + pivotWhere + findMapsCompiled → SelectRows, pivotTableQuery +
// pivotWhere + execDelete → DeleteRows, pivotTableQuery + pivotWhere + compileUpdate +
// execStatement → UpdateRows, insertPivotRows → InsertRows, ident → Ident, readParentColumn →
// ColumnValue, resolveRelation → RelationInfoOf, dictKey → DictionaryKey, the (*OrmBuilder)
// transaction cast → BuilderOf. The predicates are appended in the original's order and the pivot
// scope (RelationInfo.OnPivotQuery) is replayed where the original replayed it, so every
// oracle-pinned statement — pivotWhere's two fragment shapes, the BasePivotRow stamps, the
// attach / detach / sync statements — compiles byte for byte. The sync cores detach before they
// attach, Laravel's order (InteractsWithPivotTable::sync :123 detach, :133 attachNew; ::toggle
// :39 detach, :50 attach).

// --- the shared front-half ---------------------------------------------------------------------

// ResolvePivot is the shared front-half for all pivot operations: validates parent, resolves the
// relation's projection, asserts a pivot-friendly kind, and reads the parent's PK that anchors
// every pivot row. Returns the projection + parent's PK value. op names the operation the
// kind-not-supported error reports.
func ResolvePivot(b Builder, parent any, relation, op string) (RelationInfo, any, error) {
	if !IsValidParent(parent) {
		return RelationInfo{}, nil, errors.OrmRelationParentNotPointer.Args(parent)
	}
	info, err := b.RelationInfoOf(parent, relation)
	if err != nil {
		return RelationInfo{}, nil, err
	}
	if info.Kind != KindMany2Many && info.Kind != KindMorphToMany {
		return RelationInfo{}, nil, errors.OrmRelationKindNotSupported.Args(op, relation, KindName(info.Kind))
	}
	parentVal, err := b.ColumnValue(parent, info.PivotParentRef.PrimaryColumn)
	if err != nil {
		return RelationInfo{}, nil, err
	}

	return info, parentVal, nil
}

// KindName returns a human-friendly name for a Kind, used in error messages — database/gorm's
// kindName over the descriptor kind, duplicated so the kind-not-supported message a ported worker
// reports carries the same word.
func KindName(k Kind) string {
	switch k {
	case KindHasOne:
		return "hasOne"
	case KindHasMany:
		return "hasMany"
	case KindBelongsTo:
		return "belongsTo"
	case KindMany2Many:
		return "many2Many"
	case KindMorphOne:
		return "morphOne"
	case KindMorphMany:
		return "morphMany"
	case KindMorphTo:
		return "morphTo"
	case KindMorphToMany:
		return "morphToMany"
	case KindHasOneThrough:
		return "hasOneThrough"
	case KindHasManyThrough:
		return "hasManyThrough"
	}

	return fmt.Sprintf("kind=%d", k)
}

// --- the OrFail transaction wrapper ------------------------------------------------------------

// RelationTransaction runs fn inside a transaction — Eloquent's
// `$this->parent->getConnection()->transaction(...)` around each OrFail method
// (InteractsWithPivotTable.php:75/:157/:170/:200/:299/:365/:562). The transaction opens on the
// builder the relation writes run on, the parent's connection in the single-connection relation
// model the port supports; a call made while that connection already holds a transaction runs
// inside a savepoint, the nesting rule Connection::transaction applies. The base call's error
// rolls the transaction back and propagates. The session fn receives is the transaction's own
// builder, which is why the OrFail entries re-dispatch the base shell on it.
func RelationTransaction(b Builder, fn func(session Builder) error) error {
	if b.InTransaction() {
		return b.WithSavepoint(func() error { return fn(b) })
	}

	tx, err := b.BeginTransaction()
	if err != nil {
		return err
	}

	// BeginTransaction hands back the driver's own builder — the one *OrmBuilder this ORM
	// produces, which implements the seam; a query that does not is reported, not asserted on.
	txBuilder, ok := BuilderOf(tx)
	if !ok {
		return errors.OrmRelationTransactionNotSeam.Args(tx)
	}
	if err := fn(txBuilder); err != nil {
		if rollbackErr := txBuilder.Rollback(); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}

		return err
	}

	return txBuilder.Commit()
}

// --- attach ------------------------------------------------------------------------------------

// Attach inserts pivot rows linking parent to each id in ids. Skips ids that already have a pivot
// row. Supported kinds: Many2Many, MorphToMany, MorphedByMany. The body of the AttachRelation
// shell (RelationWriter.Attach, contracts/database/orm/relation_writer.go:134).
func Attach(b Builder, parent any, relation string, ids []any) error {
	info, parentVal, err := ResolvePivot(b, parent, relation, "Attach")
	if err != nil {
		return err
	}
	affected, err := DoAttach(b, info, parentVal, ids, nil)
	if err != nil {
		return err
	}
	if affected > 0 {
		return TouchIfTouching(b, info, parent, parentVal)
	}

	return nil
}

// AttachOrFail is Attach inside a transaction — InteractsWithPivotTable::attachOrFail
// (InteractsWithPivotTable.php:365). The parent guard runs before the transaction opens, the
// same order every shell validates in.
func AttachOrFail(b Builder, parent any, relation string, ids []any) error {
	if !IsValidParent(parent) {
		return errors.OrmRelationParentNotPointer.Args(parent)
	}

	return RelationTransaction(b, func(session Builder) error {
		return session.AttachRelation(parent, relation, ids)
	})
}

// AttachWithPivot is Attach with per-row pivot column values.
func AttachWithPivot(b Builder, parent any, relation string, idsWithAttrs map[any]map[string]any) error {
	info, parentVal, err := ResolvePivot(b, parent, relation, "AttachWithPivot")
	if err != nil {
		return err
	}
	affected, err := DoAttachWithPivot(b, info, parentVal, idsWithAttrs)
	if err != nil {
		return err
	}
	if affected > 0 {
		return TouchIfTouching(b, info, parent, parentVal)
	}

	return nil
}

// DoAttach is the shared work-horse for Attach / AttachWithPivot. It does not trigger
// TouchIfTouching — callers (the public entries, and SyncCore) own when to touch. Returns the
// number of newly-inserted pivot rows; zero when every id was already attached.
//
// idsWithAttrs is optional: when non-nil, attrs from this map are merged into each new row;
// when nil, ids alone drive the insert.
func DoAttach(b Builder, info RelationInfo, parentVal any, ids []any, idsWithAttrs map[any]map[string]any) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	existing, err := ExistingPivotIDs(b, info, parentVal, ids)
	if err != nil {
		return 0, err
	}
	rows := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		if _, dup := existing[DictionaryKey(id)]; dup {
			continue
		}
		var attrs map[string]any
		if idsWithAttrs != nil {
			attrs = idsWithAttrs[id]
		}
		rows = append(rows, BasePivotRow(info, parentVal, id, attrs))
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if err := b.InsertRows(info.PivotTable, rows); err != nil {
		return 0, err
	}

	return int64(len(rows)), nil
}

// DoAttachWithPivot is DoAttach for the with-attrs entry shape (map keyed by id). Equivalent in
// outcome to DoAttach with the same attrs; kept separate to avoid materialising an ids slice
// just to satisfy the DoAttach signature when callers already have a map.
func DoAttachWithPivot(b Builder, info RelationInfo, parentVal any, idsWithAttrs map[any]map[string]any) (int64, error) {
	if len(idsWithAttrs) == 0 {
		return 0, nil
	}
	ids := make([]any, 0, len(idsWithAttrs))
	for id := range idsWithAttrs {
		ids = append(ids, id)
	}
	existing, err := ExistingPivotIDs(b, info, parentVal, ids)
	if err != nil {
		return 0, err
	}
	rows := make([]map[string]any, 0, len(ids))
	for id, attrs := range idsWithAttrs {
		if _, dup := existing[DictionaryKey(id)]; dup {
			continue
		}
		rows = append(rows, BasePivotRow(info, parentVal, id, attrs))
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if err := b.InsertRows(info.PivotTable, rows); err != nil {
		return 0, err
	}

	return int64(len(rows)), nil
}

// BasePivotRow builds the column map for one pivot INSERT row. Always includes the parent FK and
// the related FK; for MorphToMany also includes the morph_type column. When the relation's
// resolved created_at / updated_at columns are non-empty, stamps those columns with time.Now().
// Caller-supplied attrs are merged on top — the caller wins on column-name conflicts.
func BasePivotRow(info RelationInfo, parentVal, relatedID any, attrs map[string]any) map[string]any {
	row := map[string]any{
		info.PivotParentRef.ForeignColumn:  parentVal,
		info.PivotRelatedRef.ForeignColumn: relatedID,
	}
	if info.Kind == KindMorphToMany {
		row[info.MorphTypeColumn] = info.MorphValue
	}
	now := time.Now()
	if info.PivotCreatedAtColumn != "" {
		row[info.PivotCreatedAtColumn] = now
	}
	if info.PivotUpdatedAtColumn != "" {
		row[info.PivotUpdatedAtColumn] = now
	}
	for k, v := range attrs {
		row[k] = v
	}

	return row
}

// --- the pivot reads the diffs run on ----------------------------------------------------------

// pivotEquality is the equality fragment pivotWhere renders: the dialect-quoted qualified column
// followed by " = ?" with the value as its single binding.
func pivotEquality(b Builder, info RelationInfo, column string, value any) RowWhere {
	return RowWhere{Query: fmt.Sprintf("%s = ?", b.Ident(info.PivotTable, column)), Args: []any{value}}
}

// pivotInList is the IN fragment pivotWhere renders: the unquoted qualified column with the id
// slice as its single binding, which the compiler renders as a typed IN clause.
func pivotInList(info RelationInfo, column string, ids []any) RowWhere {
	return RowWhere{Query: info.PivotTable + "." + column, Args: []any{ids}}
}

// ExistingPivotIDs returns the set of already-attached related ids among ids. Used by Attach to
// skip duplicates.
func ExistingPivotIDs(b Builder, info RelationInfo, parentVal any, ids []any) (map[string]struct{}, error) {
	wheres := []RowWhere{
		pivotEquality(b, info, info.PivotParentRef.ForeignColumn, parentVal),
		pivotInList(info, info.PivotRelatedRef.ForeignColumn, ids),
	}
	if info.Kind == KindMorphToMany {
		wheres = append(wheres, pivotEquality(b, info, info.MorphTypeColumn, info.MorphValue))
	}
	rows, err := b.SelectRows(info.PivotTable, []string{info.PivotRelatedRef.ForeignColumn}, wheres, info.OnPivotQuery)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		out[DictionaryKey(row[info.PivotRelatedRef.ForeignColumn])] = struct{}{}
	}

	return out, nil
}

// AllPivotIDs returns the set of all currently-attached related ids for parent. Used by Sync /
// Toggle to compute the diff.
func AllPivotIDs(b Builder, info RelationInfo, parentVal any) ([]any, error) {
	wheres := []RowWhere{pivotEquality(b, info, info.PivotParentRef.ForeignColumn, parentVal)}
	if info.Kind == KindMorphToMany {
		wheres = append(wheres, pivotEquality(b, info, info.MorphTypeColumn, info.MorphValue))
	}
	rows, err := b.SelectRows(info.PivotTable, []string{info.PivotRelatedRef.ForeignColumn}, wheres, info.OnPivotQuery)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row[info.PivotRelatedRef.ForeignColumn])
	}

	return out, nil
}

// --- detach ------------------------------------------------------------------------------------

// Detach removes pivot rows linking parent to the given ids. With nil ids, removes all pivot rows
// for parent (and morph type, for polymorphic); an explicitly empty (non-nil) id list is
// Laravel's detach([]) no-op — 0, no statement (InteractsWithPivotTable.php:530-535; oracle
// v12.55.1, executed, /tmp/lalign-cleanups/detach_empty.php: "detach([]) return => 0",
// "detach([]) queries => []"). Returns the number of rows removed. The body of the DetachRelation
// shell.
func Detach(b Builder, parent any, relation string, ids []any) (int64, error) {
	info, parentVal, err := ResolvePivot(b, parent, relation, "Detach")
	if err != nil {
		return 0, err
	}
	affected, err := DoDetach(b, info, parentVal, ids)
	if err != nil {
		return 0, err
	}
	if affected > 0 {
		if err := TouchIfTouching(b, info, parent, parentVal); err != nil {
			return affected, err
		}
	}

	return affected, nil
}

// DetachOrFail is Detach inside a transaction — InteractsWithPivotTable::detachOrFail (:562).
func DetachOrFail(b Builder, parent any, relation string, ids []any) (int64, error) {
	if !IsValidParent(parent) {
		return 0, errors.OrmRelationParentNotPointer.Args(parent)
	}

	var affected int64
	err := RelationTransaction(b, func(session Builder) error {
		var inner error
		affected, inner = session.DetachRelation(parent, relation, ids)

		return inner
	})
	if err != nil {
		return 0, err
	}

	return affected, nil
}

// DoDetach is the no-touch worker behind Detach. Returns rows affected by the DELETE.
func DoDetach(b Builder, info RelationInfo, parentVal any, ids []any) (int64, error) {
	// Laravel detach($ids) parses the id list first: an explicitly empty list yields no ids and
	// returns 0 before any statement runs (InteractsWithPivotTable.php:530-535; oracle v12.55.1,
	// executed, /tmp/lalign-cleanups/detach_empty.php: detach([]) => 0, no queries). A nil list is
	// Laravel's detach(null) and still detaches every pivot row of the parent. The single
	// empty-slice argument (Detach([]any{})) reads the same way — the list inside is empty.
	if ids != nil {
		if len(ids) == 0 {
			return 0, nil
		}
		if len(ids) == 1 {
			if _, isBytes := ids[0].([]byte); !isBytes {
				value := reflect.ValueOf(ids[0])
				if (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) && value.Len() == 0 {
					return 0, nil
				}
			}
		}
	}

	wheres := []RowWhere{pivotEquality(b, info, info.PivotParentRef.ForeignColumn, parentVal)}
	if info.Kind == KindMorphToMany {
		wheres = append(wheres, pivotEquality(b, info, info.MorphTypeColumn, info.MorphValue))
	}
	if len(ids) > 0 {
		wheres = append(wheres, pivotInList(info, info.PivotRelatedRef.ForeignColumn, ids))
	}

	return b.DeleteRows(info.PivotTable, wheres, info.OnPivotQuery)
}

// --- sync / toggle -----------------------------------------------------------------------------

// Sync replaces parent's pivot rows so they exactly match ids: detaches missing entries, attaches
// new ones, leaves existing untouched. Returns the per-id outcome. The body of the SyncRelation
// shell (RelationWriter.Sync, contracts/database/orm/relation_writer.go:150).
func Sync(b Builder, parent any, relation string, ids []any) (*dbcontract.SyncResult, error) {
	return SyncCore(b, parent, relation, ids, true /*detach*/, false /*toggle*/, "Sync")
}

// SyncOrFail is Sync inside a transaction — InteractsWithPivotTable::syncOrFail (:157).
func SyncOrFail(b Builder, parent any, relation string, ids []any) (*dbcontract.SyncResult, error) {
	if !IsValidParent(parent) {
		return nil, errors.OrmRelationParentNotPointer.Args(parent)
	}

	var out *dbcontract.SyncResult
	err := RelationTransaction(b, func(session Builder) error {
		var inner error
		out, inner = session.SyncRelation(parent, relation, ids)

		return inner
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// SyncWithoutDetaching is Sync minus the detach step.
func SyncWithoutDetaching(b Builder, parent any, relation string, ids []any) (*dbcontract.SyncResult, error) {
	return SyncCore(b, parent, relation, ids, false /*detach*/, false /*toggle*/, "SyncWithoutDetaching")
}

// SyncWithoutDetachingOrFail is SyncWithoutDetaching inside a transaction —
// InteractsWithPivotTable::syncWithoutDetachingOrFail (:170, `$this->syncOrFail($ids, false)`).
func SyncWithoutDetachingOrFail(b Builder, parent any, relation string, ids []any) (*dbcontract.SyncResult, error) {
	if !IsValidParent(parent) {
		return nil, errors.OrmRelationParentNotPointer.Args(parent)
	}

	var out *dbcontract.SyncResult
	err := RelationTransaction(b, func(session Builder) error {
		var inner error
		out, inner = session.SyncWithoutDetachingRelation(parent, relation, ids)

		return inner
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Toggle attaches missing entries and detaches existing ones.
func Toggle(b Builder, parent any, relation string, ids []any) (*dbcontract.SyncResult, error) {
	return SyncCore(b, parent, relation, ids, false, true /*toggle*/, "Toggle")
}

// ToggleOrFail is Toggle inside a transaction — InteractsWithPivotTable::toggleOrFail (:75).
func ToggleOrFail(b Builder, parent any, relation string, ids []any) (*dbcontract.SyncResult, error) {
	if !IsValidParent(parent) {
		return nil, errors.OrmRelationParentNotPointer.Args(parent)
	}

	var out *dbcontract.SyncResult
	err := RelationTransaction(b, func(session Builder) error {
		var inner error
		out, inner = session.ToggleRelation(parent, relation, ids)

		return inner
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// SyncCore is the shared engine for Sync / SyncWithoutDetaching / Toggle.
func SyncCore(b Builder, parent any, relation string, ids []any, detachMissing bool, toggle bool, op string) (*dbcontract.SyncResult, error) {
	info, parentVal, err := ResolvePivot(b, parent, relation, op)
	if err != nil {
		return nil, err
	}
	current, err := AllPivotIDs(b, info, parentVal)
	if err != nil {
		return nil, err
	}
	currentSet := make(map[string]any, len(current))
	for _, id := range current {
		currentSet[DictionaryKey(id)] = id
	}
	wantSet := make(map[string]any, len(ids))
	for _, id := range ids {
		wantSet[DictionaryKey(id)] = id
	}

	out := &dbcontract.SyncResult{}
	switch {
	case toggle:
		// Anything in `ids` that exists -> detach; anything that doesn't -> attach.
		var attachIDs, detachIDs []any
		for k, v := range wantSet {
			if _, exists := currentSet[k]; exists {
				detachIDs = append(detachIDs, v)
			} else {
				attachIDs = append(attachIDs, v)
			}
		}
		// Detach before attach — Laravel's toggle removes the intersection first
		// (InteractsWithPivotTable::toggle :39 detach, :50 attach).
		if len(detachIDs) > 0 {
			if _, err := DoDetach(b, info, parentVal, detachIDs); err != nil {
				return nil, err
			}
		}
		if len(attachIDs) > 0 {
			if _, err := DoAttach(b, info, parentVal, attachIDs, nil); err != nil {
				return nil, err
			}
		}
		out.Attached = CastKeys(attachIDs, info.RelatedKeyType)
		out.Detached = CastKeys(detachIDs, info.RelatedKeyType)
	default:
		if detachMissing {
			// Detach first — Laravel's sync runs detach before attachNew
			// (InteractsWithPivotTable::sync :123 detach, :133 attachNew). Detach anything in
			// `currentSet` that isn't in `wantSet`.
			var detachIDs []any
			for k, v := range currentSet {
				if _, keep := wantSet[k]; !keep {
					detachIDs = append(detachIDs, v)
				}
			}
			if len(detachIDs) > 0 {
				if _, err := DoDetach(b, info, parentVal, detachIDs); err != nil {
					return nil, err
				}
			}
			out.Detached = CastKeys(detachIDs, info.RelatedKeyType)
		}

		// Attach anything in `wantSet` that isn't yet attached.
		var attachIDs []any
		for k, v := range wantSet {
			if _, exists := currentSet[k]; !exists {
				attachIDs = append(attachIDs, v)
			}
		}
		if len(attachIDs) > 0 {
			if _, err := DoAttach(b, info, parentVal, attachIDs, nil); err != nil {
				return nil, err
			}
		}
		out.Attached = CastKeys(attachIDs, info.RelatedKeyType)
	}

	if SyncResultChanged(out) {
		if err := TouchIfTouching(b, info, parent, parentVal); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// SyncWithPivot is Sync with per-ID pivot column values. The map key is the related id; the map
// value is the column-name-to-value map applied to that pivot row. For existing pivot rows with
// non-empty attrs, updates the pivot columns (reported in SyncResult.Updated). Mirrors fedaco's
// sync(map).
func SyncWithPivot(b Builder, parent any, relation string, idsWithAttrs map[any]map[string]any) (*dbcontract.SyncResult, error) {
	return SyncCoreWithPivot(b, parent, relation, idsWithAttrs, true /*detach*/, false /*toggle*/, "SyncWithPivot")
}

// SyncWithPivotValues applies the same pivot column values to all ids. Mirrors fedaco's
// syncWithPivotValues.
func SyncWithPivotValues(b Builder, parent any, relation string, ids []any, pivotValues map[string]any) (*dbcontract.SyncResult, error) {
	idsWithAttrs := make(map[any]map[string]any, len(ids))
	for _, id := range ids {
		idsWithAttrs[id] = pivotValues
	}

	return SyncCoreWithPivot(b, parent, relation, idsWithAttrs, true /*detach*/, false /*toggle*/, "SyncWithPivotValues")
}

// SyncWithPivotValuesOrFail is SyncWithPivotValues inside a transaction —
// InteractsWithPivotTable::syncWithPivotValuesOrFail (:200).
func SyncWithPivotValuesOrFail(b Builder, parent any, relation string, ids []any, pivotValues map[string]any) (*dbcontract.SyncResult, error) {
	if !IsValidParent(parent) {
		return nil, errors.OrmRelationParentNotPointer.Args(parent)
	}

	var out *dbcontract.SyncResult
	err := RelationTransaction(b, func(session Builder) error {
		var inner error
		out, inner = session.SyncRelationWithPivotValues(parent, relation, ids, pivotValues)

		return inner
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// SyncWithoutDetachingWithPivot is SyncWithPivot minus the detach step.
func SyncWithoutDetachingWithPivot(b Builder, parent any, relation string, idsWithAttrs map[any]map[string]any) (*dbcontract.SyncResult, error) {
	return SyncCoreWithPivot(b, parent, relation, idsWithAttrs, false /*detach*/, false /*toggle*/, "SyncWithoutDetachingWithPivot")
}

// ToggleWithPivot is Toggle with per-ID pivot column values for newly attached rows.
func ToggleWithPivot(b Builder, parent any, relation string, idsWithAttrs map[any]map[string]any) (*dbcontract.SyncResult, error) {
	return SyncCoreWithPivot(b, parent, relation, idsWithAttrs, false, true /*toggle*/, "ToggleWithPivot")
}

// SyncCoreWithPivot is the shared engine for SyncWithPivot / SyncWithPivotValues /
// SyncWithoutDetachingWithPivot / ToggleWithPivot. Similar to SyncCore but accepts a map of IDs
// to pivot attributes and updates existing pivot rows when attrs are non-empty.
func SyncCoreWithPivot(b Builder, parent any, relation string, idsWithAttrs map[any]map[string]any, detachMissing bool, toggle bool, op string) (*dbcontract.SyncResult, error) {
	info, parentVal, err := ResolvePivot(b, parent, relation, op)
	if err != nil {
		return nil, err
	}
	current, err := AllPivotIDs(b, info, parentVal)
	if err != nil {
		return nil, err
	}
	currentSet := make(map[string]any, len(current))
	for _, id := range current {
		currentSet[DictionaryKey(id)] = id
	}
	wantSet := make(map[string]any, len(idsWithAttrs))
	for id := range idsWithAttrs {
		wantSet[DictionaryKey(id)] = id
	}

	out := &dbcontract.SyncResult{}
	switch {
	case toggle:
		// Anything in `idsWithAttrs` that exists -> detach; anything that doesn't -> attach with attrs.
		var detachIDs []any
		attachMap := make(map[any]map[string]any)
		for k, v := range wantSet {
			if _, exists := currentSet[k]; exists {
				detachIDs = append(detachIDs, v)
			} else {
				attachMap[v] = idsWithAttrs[v]
			}
		}
		// Detach before attach — Laravel's toggle removes the intersection first
		// (InteractsWithPivotTable::toggle :39 detach, :50 attach).
		if len(detachIDs) > 0 {
			if _, err := DoDetach(b, info, parentVal, detachIDs); err != nil {
				return nil, err
			}
		}
		if len(attachMap) > 0 {
			if _, err := DoAttachWithPivot(b, info, parentVal, attachMap); err != nil {
				return nil, err
			}
			for id := range attachMap {
				out.Attached = append(out.Attached, id)
			}
		}
		out.Attached = CastKeys(out.Attached, info.RelatedKeyType)
		out.Detached = CastKeys(detachIDs, info.RelatedKeyType)
	default:
		if detachMissing {
			// Detach first — Laravel's sync runs detach before attachNew
			// (InteractsWithPivotTable::sync :123 detach, :133 attachNew). Detach anything in
			// `currentSet` that isn't in `wantSet`.
			var detachIDs []any
			for k, v := range currentSet {
				if _, keep := wantSet[k]; !keep {
					detachIDs = append(detachIDs, v)
				}
			}
			if len(detachIDs) > 0 {
				if _, err := DoDetach(b, info, parentVal, detachIDs); err != nil {
					return nil, err
				}
			}
			out.Detached = CastKeys(detachIDs, info.RelatedKeyType)
		}

		// Attach anything in `wantSet` that isn't yet attached; update existing if attrs non-empty.
		attachMap := make(map[any]map[string]any)
		var updateIDs []any
		for k, v := range wantSet {
			if _, exists := currentSet[k]; !exists {
				attachMap[v] = idsWithAttrs[v]
			} else {
				// Already attached — if attrs non-empty, update the pivot row.
				attrs := idsWithAttrs[v]
				if len(attrs) > 0 {
					if _, err := DoUpdateExistingPivot(b, info, parentVal, v, attrs); err != nil {
						return nil, err
					}
					updateIDs = append(updateIDs, v)
				}
			}
		}
		if len(attachMap) > 0 {
			if _, err := DoAttachWithPivot(b, info, parentVal, attachMap); err != nil {
				return nil, err
			}
			for id := range attachMap {
				out.Attached = append(out.Attached, id)
			}
		}
		out.Attached = CastKeys(out.Attached, info.RelatedKeyType)
		out.Updated = CastKeys(updateIDs, info.RelatedKeyType)
	}

	if SyncResultChanged(out) {
		if err := TouchIfTouching(b, info, parent, parentVal); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// SyncResultChanged reports whether out indicates any actual pivot-table mutation. Used by
// SyncCore / SyncCoreWithPivot to decide whether to call TouchIfTouching at the end.
func SyncResultChanged(out *dbcontract.SyncResult) bool {
	return len(out.Attached) > 0 || len(out.Detached) > 0 || len(out.Updated) > 0
}

// --- update existing pivot ---------------------------------------------------------------------

// UpdateExistingPivot updates pivot columns for an already-attached id. When pivot timestamps are
// resolved and attrs doesn't already set updated_at, injects time.Now() into the update map.
// No-op (returns 0) if no matching pivot row exists. The body of the UpdateExistingPivotRelation
// shell.
func UpdateExistingPivot(b Builder, parent any, relation string, id any, attrs map[string]any) (int64, error) {
	info, parentVal, err := ResolvePivot(b, parent, relation, "UpdateExistingPivot")
	if err != nil {
		return 0, err
	}
	affected, err := DoUpdateExistingPivot(b, info, parentVal, id, attrs)
	if err != nil {
		return 0, err
	}
	if affected > 0 {
		if err := TouchIfTouching(b, info, parent, parentVal); err != nil {
			return affected, err
		}
	}

	return affected, nil
}

// UpdateExistingPivotOrFail is UpdateExistingPivot inside a transaction —
// InteractsWithPivotTable::updateExistingPivotOrFail (:299).
func UpdateExistingPivotOrFail(b Builder, parent any, relation string, id any, attrs map[string]any) (int64, error) {
	if !IsValidParent(parent) {
		return 0, errors.OrmRelationParentNotPointer.Args(parent)
	}

	var affected int64
	err := RelationTransaction(b, func(session Builder) error {
		var inner error
		affected, inner = session.UpdateExistingPivotRelation(parent, relation, id, attrs)

		return inner
	})
	if err != nil {
		return 0, err
	}

	return affected, nil
}

// DoUpdateExistingPivot is the no-touch worker behind UpdateExistingPivot. Returns the number of
// pivot rows actually updated. When the relation has a resolved updated_at column and attrs
// doesn't already set it, injects time.Now() into the UPDATE map.
func DoUpdateExistingPivot(b Builder, info RelationInfo, parentVal any, id any, attrs map[string]any) (int64, error) {
	if len(attrs) == 0 && info.PivotUpdatedAtColumn == "" {
		return 0, nil
	}
	updateMap := make(map[string]any, len(attrs)+1)
	for k, v := range attrs {
		updateMap[k] = v
	}
	if info.PivotUpdatedAtColumn != "" {
		if _, hasUpdatedAt := updateMap[info.PivotUpdatedAtColumn]; !hasUpdatedAt {
			updateMap[info.PivotUpdatedAtColumn] = time.Now()
		}
	}
	wheres := []RowWhere{
		pivotEquality(b, info, info.PivotParentRef.ForeignColumn, parentVal),
		pivotEquality(b, info, info.PivotRelatedRef.ForeignColumn, id),
	}
	if info.Kind == KindMorphToMany {
		wheres = append(wheres, pivotEquality(b, info, info.MorphTypeColumn, info.MorphValue))
	}

	return b.UpdateRows(info.PivotTable, wheres, info.OnPivotQuery, updateMap)
}

// --- key normalisation -------------------------------------------------------------------------

// CastKeys returns a copy of ids with each value normalised to keyType (the related model's PK
// type). Used by the sync cores to ensure SyncResult elements carry a stable Go type regardless
// of what the caller passed in or what the driver scanned out of the pivot table.
//
// Mirrors fedaco's _castKeys / _getTypeSwapValue. Returns nil for a nil input slice (preserving
// the "no rows touched" signal).
func CastKeys(ids []any, keyType reflect.Type) []any {
	if ids == nil {
		return nil
	}
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = CastKey(id, keyType)
	}

	return out
}

// CastKey converts v to the Go type t, handling the common cross-type cases (int/uint/float
// numeric widening + narrowing, string ↔ numeric). Returns v unchanged when t is nil, when v is
// already the right type, or when conversion isn't safely representable. database/gorm's flat
// pluck shares it (builder_shortcuts.go pluckValue, through the castKey forwarder).
func CastKey(v any, t reflect.Type) any {
	if v == nil || t == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	if rv.Type() == t {
		return v
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return rv.Convert(t).Interface()
		case reflect.String:
			if i, err := strconv.ParseInt(rv.String(), 10, 64); err == nil {
				return reflect.ValueOf(i).Convert(t).Interface()
			}
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return rv.Convert(t).Interface()
		case reflect.String:
			if u, err := strconv.ParseUint(rv.String(), 10, 64); err == nil {
				return reflect.ValueOf(u).Convert(t).Interface()
			}
		}
	case reflect.Float32, reflect.Float64:
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return rv.Convert(t).Interface()
		case reflect.String:
			if f, err := strconv.ParseFloat(rv.String(), 64); err == nil {
				return reflect.ValueOf(f).Convert(t).Interface()
			}
		}
	case reflect.String:
		// Numeric / []byte → string. Avoid reflect.Convert here because int→string interprets the
		// int as a Unicode code point (e.g. 65 → "A"), not a decimal digit string.
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return fmt.Sprint(v)
		case reflect.Slice:
			if rv.Type().Elem().Kind() == reflect.Uint8 {
				return string(rv.Bytes())
			}
		}
	}

	return v
}
