package relations

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file is S2 step 2 of the gorm reduction (LARAVEL_1TO1_ALIGNMENT_REVIEW.md §C.4): the
// HasOneOrMany / BelongsTo / through-family Tier-2 write workers of database/gorm/relation_writes.go,
// ported as free functions over the widened Builder and the RelationInfo projection. The 27
// *OrmBuilder write shells stay where Go method ownership keeps them and forward here; the
// BelongsToMany arms of the shared find-then-write shells stay in database/gorm until the pivot
// workers port (S2 step 3), because they read the related table through a builder member this
// package cannot reach yet (relatedQuery).
//
// Nothing here changes behavior: every function is the gorm original transcribed statement for
// statement, each gorm-internal member replaced by the seam twin the decision table above Builder
// names (parseSchema field access → ColumnType / SetColumn / ColumnValue / PrimaryKey, fresh() →
// NewQuery, withSavepointIfNeeded → WithSavepoint, firstRow → FirstFound, the touch UPDATE →
// UpdateRows). The order of every schema read, error and statement is the original's, so an
// oracle-pinned statement shape compiles byte for byte and an error path reports the same error.

// isHasFamily reports the HasOne / HasMany / MorphOne / MorphMany kinds — the kinds that stamp
// the parent key on the related row (HasOneOrMany::setForeignAttributesForCreate).
func isHasFamily(kind Kind) bool {
	switch kind {
	case KindHasOne, KindHasMany, KindMorphOne, KindMorphMany:
		return true
	}

	return false
}

// isThrough reports the HasOneThrough / HasManyThrough kinds, which carry no key on the related row.
func isThrough(kind Kind) bool {
	return kind == KindHasOneThrough || kind == KindHasManyThrough
}

// --- BelongsTo / MorphTo: associate and dissociate --------------------------------------------

// ApplyAssociate writes owner's PK into parent's FK column (and the morph_type column for
// MorphTo, resolved from the morph map / MorphClass()), then persists parent — BelongsTo::associate
// followed by the parent save the Go shells perform (the corpus ports' documented divergence).
func ApplyAssociate(b Builder, info RelationInfo, parent, owner any, isMorph bool) error {
	if err := MutateAssociate(b, info, parent, owner, isMorph); err != nil {
		return err
	}

	return b.NewQuery().Save(parent)
}

// MutateAssociate is the pure-mutation half of ApplyAssociate. Writes owner's PK into parent's
// FK column (and the morph_type column for MorphTo). No persistence.
func MutateAssociate(b Builder, info RelationInfo, parent, owner any, isMorph bool) error {
	var fkColumn string
	if isMorph {
		fkColumn = info.MorphIDColumn
	} else if len(info.References) > 0 {
		fkColumn = info.References[0].ForeignColumn
	}
	// The parent's schema is parsed first — the original's parseSchema(parent) — so a parse error
	// outranks the descriptor checks that follow.
	_, hasFK, err := b.ColumnType(parent, fkColumn)
	if err != nil {
		return err
	}
	if !isMorph && len(info.References) == 0 {
		return errors.OrmRelationUnsupported.Args(info.Name, info.ParentTable, "no references")
	}
	if !hasFK {
		return errors.OrmRelationUnsupported.Args(info.Name, ModelTypeName(parent), "no FK field "+fkColumn)
	}

	ownerPKColumn := "id"
	if !isMorph && len(info.References) > 0 {
		ownerPKColumn = info.References[0].PrimaryColumn
	} else if isMorph && info.MorphOwnerKey != "" {
		ownerPKColumn = info.MorphOwnerKey
	}
	ownerPK, err := b.ColumnValue(owner, ownerPKColumn)
	if err != nil {
		return err
	}
	if _, err := b.SetColumn(parent, fkColumn, ownerPK); err != nil {
		return err
	}

	if isMorph {
		_, hasType, err := b.ColumnType(parent, info.MorphTypeColumn)
		if err != nil {
			return err
		}
		if !hasType {
			return errors.OrmRelationUnsupported.Args(info.Name, ModelTypeName(parent), "no morph type field "+info.MorphTypeColumn)
		}
		alias, ok, err := b.MorphAliasOf(owner)
		if err != nil {
			return err
		}
		if !ok {
			tbl, terr := b.TableOf(owner)
			if terr != nil {
				return terr
			}
			alias = tbl
		}
		if _, err := b.SetColumn(parent, info.MorphTypeColumn, alias); err != nil {
			return err
		}
	}

	return nil
}

// ApplyDissociate sets parent's FK to the zero value (and morph_type to "" for MorphTo), then
// persists parent — BelongsTo::dissociate followed by the parent save.
func ApplyDissociate(b Builder, info RelationInfo, parent any, isMorph bool) error {
	if err := MutateDissociate(b, info, parent, isMorph); err != nil {
		return err
	}

	return b.NewQuery().Save(parent)
}

// MutateDissociate is the pure-mutation half of ApplyDissociate.
func MutateDissociate(b Builder, info RelationInfo, parent any, isMorph bool) error {
	var fkColumn string
	if isMorph {
		fkColumn = info.MorphIDColumn
	} else if len(info.References) > 0 {
		fkColumn = info.References[0].ForeignColumn
	}
	fkType, hasFK, err := b.ColumnType(parent, fkColumn)
	if err != nil {
		return err
	}
	if !isMorph && len(info.References) == 0 {
		return errors.OrmRelationUnsupported.Args(info.Name, info.ParentTable, "no references")
	}
	if !hasFK {
		return errors.OrmRelationUnsupported.Args(info.Name, ModelTypeName(parent), "no FK field "+fkColumn)
	}
	zero := reflect.Zero(fkType).Interface()
	if _, err := b.SetColumn(parent, fkColumn, zero); err != nil {
		return err
	}

	if isMorph {
		typeType, hasType, err := b.ColumnType(parent, info.MorphTypeColumn)
		if err != nil {
			return err
		}
		if !hasType {
			return errors.OrmRelationUnsupported.Args(info.Name, ModelTypeName(parent), "no morph type field "+info.MorphTypeColumn)
		}
		zeroType := reflect.Zero(typeType).Interface()
		if _, err := b.SetColumn(parent, info.MorphTypeColumn, zeroType); err != nil {
			return err
		}
	}

	return nil
}

// --- HasOneOrMany: the foreign-key stamp and the inverse write-back ---------------------------

// SetRelationFKOnChild reads parent's local key, then writes that value into child's FK column
// (and the morph_type column for MorphOne/MorphMany) — HasOneOrMany::setForeignAttributesForCreate
// (vendor v12.55.1, HasOneOrMany.php:506). Mutates child in place; child must be a pointer to a
// struct. The trailing inverse call links the child back to the parent in memory.
func SetRelationFKOnChild(b Builder, info RelationInfo, parent, child any) error {
	if len(info.References) == 0 {
		return errors.OrmRelationUnsupported.Args(info.Name, info.ParentTable, "no references")
	}
	ref := info.References[0]
	parentVal, err := b.ColumnValue(parent, ref.PrimaryColumn)
	if err != nil {
		return err
	}
	_, hasFK, err := b.ColumnType(child, ref.ForeignColumn)
	if err != nil {
		return err
	}
	if !hasFK {
		return errors.OrmRelationUnsupported.Args(info.Name, ModelTypeName(child), "no FK field "+ref.ForeignColumn)
	}
	if _, err := b.SetColumn(child, ref.ForeignColumn, parentVal); err != nil {
		return err
	}
	if info.Kind == KindMorphOne || info.Kind == KindMorphMany {
		_, hasType, err := b.ColumnType(child, info.MorphTypeColumn)
		if err != nil {
			return err
		}
		if !hasType {
			return errors.OrmRelationUnsupported.Args(info.Name, ModelTypeName(child), "no morph type field "+info.MorphTypeColumn)
		}
		if _, err := b.SetColumn(child, info.MorphTypeColumn, info.MorphValue); err != nil {
			return err
		}
	}

	// The trailing inverse call of Laravel's setForeignAttributesForCreate: the created / made
	// child is linked back to the parent in memory under the relation's inverse name. No
	// statement runs.
	return ApplyInverseRelationToCreated(b, info, child, parent)
}

// ApplyInverseRelationToCreated stores parent on the created (or freshly made) child under the
// relation's inverse name — SupportsInverseRelations::applyInverseRelationToModel, the call
// HasOneOrMany::make (vendor v12.55.1, HasOneOrMany.php:65) and ::create (:393) make after the
// foreign key stamp, and the trailing call of setForeignAttributesForCreate (:506) that every
// FK-stamping path shares (save :334, findOrNew :233, firstOrNew :251, createOrFirst via create,
// upsert excepted — upsert stamps the key per row and applies no inverse, HasOneOrMany.php:313).
// In memory only: no statement runs, and the stored pointer is the parent instance the relation was
// resolved from — the oracle's assertSame. The inverse name is the live chaperone state when the
// builder carries one (WithoutChaperone / WithoutInverse cleared it to ""), the declaration
// otherwise (Builder.InverseWriteBackName). Oracle (v12.55.1, executed,
// /tmp/lalign-defaults-inverse/inverse_writeback_runtime.php): create / make / save / forceCreate /
// the many variants / findOrNew / firstOrNew / firstOrCreate / updateOrCreate / createOrFirst all
// report relationLoaded true with the parent identical; withoutChaperone()->create()/make() report
// relationLoaded false.
func ApplyInverseRelationToCreated(b Builder, info RelationInfo, child any, parent any) error {
	inverse := b.InverseWriteBackName(info.Inverse)
	if inverse == "" {
		return nil
	}

	childValue := reflect.ValueOf(child)
	if !childValue.IsValid() || childValue.Kind() != reflect.Pointer || childValue.IsNil() {
		return nil
	}
	if childValue.Elem().Kind() != reflect.Struct {
		return nil
	}

	parentValue := reflect.ValueOf(parent)
	if !parentValue.IsValid() || parentValue.Kind() != reflect.Pointer || parentValue.IsNil() {
		return nil
	}

	return b.SetRelation(child, inverse, parent)
}

// ApplyAttrMap overlays attrs onto dest through the parsed schema, mapping column names to struct
// fields. dest must be a pointer to a struct. Skips columns that don't map to a field.
func ApplyAttrMap(b Builder, dest any, attrs map[string]any) error {
	if len(attrs) == 0 {
		return nil
	}
	for col, val := range attrs {
		if _, err := b.SetColumn(dest, col, val); err != nil {
			return err
		}
	}

	return nil
}

// MergeAttrMaps merges values over attrs — the array_merge($attributes, $values) order the
// createOrFirst path receives.
func MergeAttrMaps(attrs, values map[string]any) map[string]any {
	merged := make(map[string]any, len(attrs)+len(values))
	for key, value := range attrs {
		merged[key] = value
	}
	for key, value := range values {
		merged[key] = value
	}

	return merged
}

// --- the parent touch of the pivot writes ------------------------------------------------------

// TouchIfTouching bumps parent's updated_at column when the relation declares Touches. Silently
// no-ops when it does not, when the parent's class ignores touches — Builder.IsIgnoringTouch, the
// gate Laravel's per-pivot touches ride through Relation::touch: a custom pivot's save or delete
// walks its $touches one row at a time, while this writer bumps once after the batch, and both are
// suppressed while the bumped class sits in a withoutTouching scope — when the parent's schema
// doesn't expose an updated_at field, or when the parent has no primary-key column to anchor the
// WHERE clause. Mirrors fedaco's touchIfTouching on BelongsToMany.
//
// Called at the tail end of the public Sync / Attach / Detach / Toggle / UpdateExistingPivot
// shells — Attach touches after every successful call, an empty id list included (Laravel's attach
// touch step is unconditional on success; oracle v12.55.1, executed, C1/C3) — the other shells
// only when the operation actually affected pivot rows. The internal doAttach / doDetach /
// doUpdateExistingPivot helpers do NOT touch — sync* paths chain multiple internal calls and touch
// at most once at the end through this helper.
//
// The stamp comes from the framework clock — carbon.Now(), the value every other write path
// stamps — so a frozen test clock (carbon.SetTestNow) observes the touch. The statement is the
// one UpdateRows compiles with no pivot scope: `update <parent table> set updated_at = ? where
// <key> = ?`.
func TouchIfTouching(b Builder, info RelationInfo, parent any, parentVal any) error {
	if !info.Touches {
		return nil
	}
	// The per-pivot touch Eloquent runs on every custom-pivot save and delete is a BelongsTo
	// touch of the parent, gated by Model::isIgnoringTouch(); the single bump here is gated the
	// same way (oracle: a sync inside Model::withoutTouching deletes the pivot row with no
	// `update "articles"` and the article's updated_at unchanged).
	if b.IsIgnoringTouch(parent) {
		return nil
	}
	_, hasUpdatedAt, err := b.ColumnType(parent, "updated_at")
	if err != nil {
		return err
	}
	if !hasUpdatedAt {
		// Parent doesn't have an updated_at column — silently skip.
		return nil
	}
	pkColumn, _, hasKey, err := b.PrimaryKey(parent)
	if err != nil {
		return err
	}
	if !hasKey {
		return nil
	}
	table, err := b.TableOf(parent)
	if err != nil {
		return err
	}
	now := carbon.Now().StdTime()
	wheres := []RowWhere{{Query: fmt.Sprintf("%s = ?", b.Ident(pkColumn)), Args: []any{parentVal}}}
	if _, err := b.UpdateRows(table, wheres, nil, map[string]any{"updated_at": now}); err != nil {
		return err
	}
	// Mirror the change into the in-memory parent struct so subsequent reads see the bump.
	_, err = b.SetColumn(parent, "updated_at", now)

	return err
}

// --- the find-then-write family ---------------------------------------------------------------

// RelationFirst runs the relation query as a single-row read and reports whether a row was found —
// the flag Query.First discards. writePdo forces the read onto the write connection, which the
// createOrFirst catch does for the HasOneOrMany kinds (Laravel's useWritePdo) so the row a
// concurrent writer just committed is visible; the through kinds read on the default connection,
// as HasOneOrManyThrough::createOrFirst does. The attrs map is applied in sorted key order so the
// compiled where list is stable across runs (Go map iteration is unordered).
func RelationFirst(b Builder, parent any, relation string, attrs map[string]any, dest any, writePdo bool) (bool, error) {
	query, ok := BuilderOf(b.Related(parent, relation))
	if !ok {
		return false, errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), "no relation query")
	}
	for _, col := range slices.Sorted(maps.Keys(attrs)) {
		next, ok := BuilderOf(query.Where(col, attrs[col]))
		if !ok {
			return false, errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), "no relation query")
		}
		query = next
	}
	if writePdo {
		write, ok := BuilderOf(query.UseWritePdo())
		if !ok {
			return false, errors.OrmRelationUnsupported.Args(relation, fmt.Sprintf("%T", parent), "no write query")
		}
		query = write
	}

	return query.FirstFound(dest)
}

// CreateOrFirstEngine is the shared insert-first engine of the create-or-first family: attrs and
// values are overlaid on dest (values win), the parent key (and morph type) is pre-set for the
// FK-on-related kinds, the insert runs inside WithSavepoint, and a unique-constraint violation
// reads the row relookupAttrs names back — over the write connection for the HasOneOrMany kinds
// (Laravel's useWritePdo), on the default connection for the through kinds
// (HasOneOrManyThrough::createOrFirst). It reports whether the violation was resolved by reading
// an existing row back (wasRecentlyCreated false in Laravel).
func CreateOrFirstEngine(b Builder, info RelationInfo, parent any, relation string, attrs, values map[string]any, dest any, relookupAttrs map[string]any) (bool, error) {
	if err := ApplyAttrMap(b, dest, attrs); err != nil {
		return false, err
	}
	if err := ApplyAttrMap(b, dest, values); err != nil {
		return false, err
	}
	if isHasFamily(info.Kind) {
		if err := SetRelationFKOnChild(b, info, parent, dest); err != nil {
			return false, err
		}
	}

	err := b.WithSavepoint(func() error { return b.NewQuery().Create(dest) })
	if err == nil {
		return false, nil
	}
	if !b.IsUniqueConstraintViolation(err) {
		return false, err
	}

	// HasOneOrMany::createOrFirst re-reads with useWritePdo; HasOneOrManyThrough::createOrFirst
	// reads on the default connection — mirror each.
	found, readErr := RelationFirst(b, parent, relation, relookupAttrs, dest, isHasFamily(info.Kind))
	if readErr != nil {
		return false, readErr
	}
	if found {
		return true, nil
	}

	return false, errors.OrmUniqueConstraintViolation.Args(err)
}

// FindOrNew is the HasOneOrMany arm of FindOrNewRelation: finds the related row with primary key
// id through the relation read; when absent, pre-sets the FK (and morph type) on the zero-valued
// dest — HasOneOrMany::findOrNew (vendor v12.55.1, HasOneOrMany.php:228-236) — and does NOT
// persist. Whether Find populated dest is read off dest's primary key.
func FindOrNew(b Builder, info RelationInfo, parent any, relation string, id any, dest any) error {
	if err := b.Related(parent, relation).Find(dest, id); err != nil {
		return err
	}
	// Check if Find actually populated dest by inspecting the PK field.
	_, isZero, hasKey, err := b.PrimaryKey(dest)
	if err != nil {
		return err
	}
	if !hasKey {
		return errors.OrmRelationUnsupported.Args(relation, ModelTypeName(dest), "no primary key")
	}
	if isZero {
		// Not found — set FK on the zero-valued dest.
		return SetRelationFKOnChild(b, info, parent, dest)
	}

	return nil
}

// FirstOrNew is the HasOneOrMany / through arm of FirstOrNewRelation: the first related row
// matching attrs, or dest filled with attrs+values (values win) and — for the HasOneOrMany kinds
// — the pre-set FK (and morph type); nothing is persisted. HasOneOrMany::firstOrNew
// (HasOneOrMany.php:244-254) and HasOneOrManyThrough::firstOrNew, whose through kinds carry no key
// on the related row, so newInstance() alone is the whole response.
func FirstOrNew(b Builder, info RelationInfo, parent any, relation string, attrs map[string]any, values map[string]any, dest any) error {
	found, err := RelationFirst(b, parent, relation, attrs, dest, false)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	// Not found — newModelInstance(array_merge(attrs, values)), then the foreign attributes.
	if err := ApplyAttrMap(b, dest, attrs); err != nil {
		return err
	}
	if err := ApplyAttrMap(b, dest, values); err != nil {
		return err
	}
	if isHasFamily(info.Kind) {
		return SetRelationFKOnChild(b, info, parent, dest)
	}

	return nil
}

// FirstOrCreate is the HasOneOrMany / through arm of FirstOrCreateRelation: the lookup runs
// first, and a miss routes through CreateOrFirstEngine — the insert is attempted, and a
// unique-constraint violation reads the row back instead of failing. HasOneOrMany::firstOrCreate's
// catch re-reads where(attrs) — attrs only; the values live on the insert — while
// HasOneOrManyThrough::firstOrCreate funnels attrs+values through createOrFirst as a single
// attribute set, so its violation re-read carries both.
func FirstOrCreate(b Builder, info RelationInfo, parent any, relation string, attrs map[string]any, values map[string]any, dest any) error {
	found, err := RelationFirst(b, parent, relation, attrs, dest, false)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	relookup := attrs
	if isThrough(info.Kind) {
		relookup = MergeAttrMaps(attrs, values)
	}
	_, err = CreateOrFirstEngine(b, info, parent, relation, attrs, values, dest, relookup)

	return err
}

// UpdateOrCreate is the HasOneOrMany / through arm of UpdateOrCreateRelation: the first related
// row matching attrs (or one created through CreateOrFirstEngine), overlaid with values and
// persisted. A row read back from a unique-constraint race is filled with the values and saved
// too — Laravel's wasRecentlyCreated() branch. Always saves dest on the found and race-read paths.
func UpdateOrCreate(b Builder, info RelationInfo, parent any, relation string, attrs map[string]any, values map[string]any, dest any) error {
	found, err := RelationFirst(b, parent, relation, attrs, dest, false)
	if err != nil {
		return err
	}
	if !found {
		relookup := attrs
		if isThrough(info.Kind) {
			relookup = MergeAttrMaps(attrs, values)
		}
		readBack, err := CreateOrFirstEngine(b, info, parent, relation, attrs, values, dest, relookup)
		if err != nil {
			return err
		}
		if !readBack {
			return nil
		}
		// The insert lost a unique-constraint race: fill the winning row with the values and
		// save it, the wasRecentlyCreated() branch.
	}
	if err := ApplyAttrMap(b, dest, values); err != nil {
		return err
	}

	return b.NewQuery().Save(dest)
}
