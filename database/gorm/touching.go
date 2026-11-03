package gorm

import (
	"fmt"
	"reflect"
	"sort"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file carries out Eloquent's HasRelationships::touchOwners for the ORM's model save and
// delete paths.
//
// Laravel's Model declares the relations to touch in `protected $touches`; saving a model runs
// finishSave, and deleting one runs touchOwners before performDeleteOnModel. The walk touches
// each declared relation through Relation::touch() — a raw, unscoped UPDATE of the related rows'
// updated-at column, skipped while the related model's class ignores touches
// (Model::isIgnoringTouch) — then resolves the relation the way `$this->$relation` does: the
// instance the model already carries loaded is reused without a query, and a relation that is
// not loaded yet is queried for and cached back onto the model (the write-back Eloquent's lazy
// load performs by keeping the result in its loaded-relations registry). The walk continues
// through the resolved models' own $touches declarations. The recursion is guarded: Eloquent
// keys its guard by loaded object identity and its object graph closes through chaperone
// relations, while the Go walk may reuse a cached relation or load a fresh instance at each hop,
// so the guard stands on the row identity — a model class plus its key — instead.
//
// The declaration is the Touches flag on the BelongsTo / HasOne / HasMany descriptors the
// corpus's touching family exercises; Many2Many.Touches keeps its distinct pivot-write meaning
// (touchIfTouching) and is not a save-or-delete owner touch.

// touchOwners runs the touched-relation walk for a model that was just saved or is about to be
// deleted. Models that declare no touching relation — and any value that is not a model pointer —
// are left alone.
func (r *OrmBuilder) touchOwners(model any) error {
	return r.touchOwnersWalk(model, nil)
}

// touchOwnersWalk is touchOwners with the visited set a recursive walk shares. A model already
// in the set is skipped whole — Eloquent's withoutRecursion guard, which returns before touching
// anything when the same object is reached again mid-walk.
func (r *OrmBuilder) touchOwnersWalk(model any, visited map[touchIdentity]bool) error {
	if !isValidParent(model) {
		return nil
	}

	declaredRelations, ok := relations.TryGetRelations(model)
	if !ok {
		return nil
	}

	names := touchedRelationNames(declaredRelations)
	if len(names) == 0 {
		return nil
	}

	if identity := touchIdentityOf(model); identity != nil {
		if visited == nil {
			visited = make(map[touchIdentity]bool)
		}
		if visited[*identity] {
			return nil
		}
		visited[*identity] = true
	}

	for _, name := range names {
		desc, err := relations.ResolveRelation(model, name)
		if err != nil {
			return err
		}

		// BelongsTo::touch() returns before stamping when the parent's foreign key is null, and
		// the lazy load behind the walk short-circuits the same way (getResults returns the
		// default for a null key without querying) — so a null key neither stamps nor loads. A
		// value-typed key cannot hold NULL in Go, so its zero (0, "") reads as null too; see
		// isNullValue.
		if desc.Kind == relKindBelongsTo {
			if _, parentColumn, ok := touchColumns(desc); ok {
				value, err := readParentColumn(r, model, parentColumn)
				if err != nil {
					return err
				}
				if isNullValue(value) {
					continue
				}
			}
		}

		// Relation::touch(): stamp the related rows, unless the related class ignores touches.
		if err := r.touchRelationRows(model, desc); err != nil {
			return err
		}

		// `if ($this->$relation instanceof self) { $this->$relation->touchOwners(); } elseif
		// ($this->$relation instanceof EloquentCollection) { $this->$relation->each->touchOwners(); }`
		if err := r.touchOwnersOfLoadedRelation(model, name, desc, visited); err != nil {
			return err
		}
	}

	return nil
}

// isNullValue reports whether a parent key read from a model stands for PHP's null: a nil
// interface or a typed nil pointer, and — because a value-typed key cannot hold NULL in Go — its
// value-typed zero (0, ""), the never-set reading the fresh-key convention uses. Eloquent's
// BelongsTo::touch() and the lazy load behind getResults() both short-circuit on null, so neither
// the stamp nor the load runs for such a key.
func isNullValue(value any) bool {
	if value == nil {
		return true
	}

	ref := reflect.ValueOf(value)
	switch ref.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return ref.IsNil()
	case reflect.String:
		return ref.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return ref.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return ref.Uint() == 0
	}

	return false
}

// touchRelationRows stamps the related model's updated-at column on every row the relation
// resolves to — Relation::touch()'s rawUpdate: an unscoped UPDATE of the related table matched
// by the relation's key pair, with no model events and no further propagation.
func (r *OrmBuilder) touchRelationRows(model any, desc *relationDescriptor) error {
	if databaseorm.IsIgnoringTouch(desc.RelatedModel) {
		return nil
	}

	relatedColumn, parentColumn, ok := touchColumns(desc)
	if !ok {
		return nil
	}

	updatedAt, err := touchedUpdatedAtColumn(desc.RelatedModel)
	if err != nil {
		return err
	}
	if updatedAt == "" {
		return nil
	}

	value, err := readParentColumn(r, model, parentColumn)
	if err != nil {
		return err
	}

	update, err := r.touchRelationUpdate(desc, relatedColumn, value)
	if err != nil {
		return err
	}

	_, err = update.Update(map[string]any{updatedAt: carbon.Now().StdTime()})

	return err
}

// touchRelationUpdate builds the standalone statement a relation touch runs on the related
// model's table, scoped by the relation's key pair. The statement compiles as
// `update "<related>" set "<updated_at>" = ? where "<related>"."<column>" = ?`, the shape
// Relation::touch's `withoutGlobalScopes()->update(...)` renders for a BelongsTo (e.g.
// `update "users" set "updated_at" = ? where "users"."id" = ?`); a HasOne / HasMany touch carries
// the not-null guard its constraint adds (`update "posts" set "updated_at" = ? where
// "posts"."user_id" = ? and "posts"."user_id" is not null`). Oracle-measured with
// illuminate/database v12.55.1 against sqlite.
func (r *OrmBuilder) touchRelationUpdate(desc *relationDescriptor, relatedColumn string, value any) (*OrmBuilder, error) {
	query := r.touchTableQuery(desc.RelatedTable)

	column := desc.RelatedTable + "." + relatedColumn
	where, ok := query.Where(column, value).(*OrmBuilder)
	if !ok {
		return nil, errors.OrmRelationUnsupported.Args("", desc.RelatedTable, "touch update is not an OrmBuilder")
	}

	// HasOneOrMany::addConstraints guards the foreign key with whereNotNull, and Relation::touch's
	// rawUpdate carries the constraint along; a BelongsTo's qualified owner key has no such guard.
	if desc.Kind == relKindHasOne || desc.Kind == relKindHasMany {
		if notNull, ok := where.WhereNotNull(column).(*OrmBuilder); ok {
			where = notNull
		}
	}

	return where, nil
}

// touchTableQuery mirrors pivotTableQuery for a related model's table: a fresh session bound to
// one table, so the touch's statement runs without disturbing the receiver's builder.
func (r *OrmBuilder) touchTableQuery(table string) *OrmBuilder {
	child := r.fresh()
	child.query.From(table, "")

	return child
}

// touchOwnersOfLoadedRelation resolves the models Eloquent's `$this->$relation` hands the walk:
// the relation the model already carries loaded is reused without a query — `$this->$relation`
// reads the cached value and only queries when nothing is loaded yet — and a relation that is
// not loaded is queried for (the lazy load, default scope and global scopes included) with its
// rows cached back onto the model's relation field. The recursion runs even while the related
// class ignores touches, exactly as Eloquent's walk does; only the rawUpdate above is gated by
// isIgnoringTouch.
func (r *OrmBuilder) touchOwnersOfLoadedRelation(model any, name string, desc *relationDescriptor, visited map[touchIdentity]bool) error {
	parent := reflect.ValueOf(model).Elem()
	if parent.Kind() != reflect.Struct {
		return nil
	}

	if owners, loaded := loadedRelationOwners(parent, name, desc); loaded {
		return r.walkTouchedOwners(owners, desc, visited)
	}

	elementType := indirectStructType(reflect.TypeOf(desc.RelatedModel))
	if elementType == nil {
		return nil
	}

	loaded, ok := ormBuilderOf(r.fresh().Related(model, name))
	if !ok {
		return nil
	}

	// A single-result owner's lazy load is getResults() -> first(), so it carries Laravel's limit 1.
	if desc.Kind == relKindBelongsTo || desc.Kind == relKindHasOne {
		loaded = loaded.Limit(1).(*OrmBuilder)
	}

	dest := reflect.New(reflect.SliceOf(elementType))
	if err := loaded.Get(dest.Interface()); err != nil {
		return err
	}

	models := dest.Elem()
	owners := make([]reflect.Value, 0, models.Len())
	for index := 0; index < models.Len(); index++ {
		owners = append(owners, models.Index(index).Addr())
	}

	// Cache the load on the model, the way the lazy load keeps its result: a later walk over the
	// same model reuses it. Best-effort — a field that cannot carry the relation (a mismatched
	// type, an unexported field) is left alone, and a single-result load that found no row lands
	// as a nil field, which reads as "not loaded" again.
	if field := parent.FieldByName(name); field.IsValid() && field.CanSet() {
		_ = setRelationField(parent, name, owners)
	}

	return r.walkTouchedOwners(owners, desc, visited)
}

// walkTouchedOwners carries the walk on through each resolved owner. A single-result owner
// receives Eloquent's `fireModelEvent('saved', false)` before its own walk — the collection
// branch fires no event on its elements. The fire rides the event pipeline, so withoutEvents and
// observer lookup behave exactly as on the save paths, and a nil element is skipped like the
// non-model value it is.
func (r *OrmBuilder) walkTouchedOwners(owners []reflect.Value, desc *relationDescriptor, visited map[touchIdentity]bool) error {
	for _, owner := range owners {
		if owner.Kind() == reflect.Pointer && owner.IsNil() {
			continue
		}

		ownerModel := owner.Interface()

		// `if ($this->$relation instanceof self) { $this->$relation->fireModelEvent('saved', false); ... }`
		if desc.Kind == relKindBelongsTo || desc.Kind == relKindHasOne {
			if _, err := r.event(contractsorm.EventSaved, ownerModel, ownerModel); err != nil {
				return err
			}
		}

		if err := r.touchOwnersWalk(ownerModel, visited); err != nil {
			return err
		}
	}

	return nil
}

// loadedRelationOwners reads the relation the model already carries loaded — the Go reading of
// the loaded-relations registry Eloquent consults before `$this->$relation` runs a query: a
// non-nil pointer field for a single-result relation, a non-nil slice field for a collection,
// its elements handed over as owner pointers. loaded is false when the field is nil or has
// another shape, and the caller then lazy-loads and caches.
func loadedRelationOwners(parent reflect.Value, name string, desc *relationDescriptor) ([]reflect.Value, bool) {
	field := parent.FieldByName(name)
	if !field.IsValid() || !field.CanInterface() || !relationFieldLoaded(field) {
		return nil, false
	}

	switch desc.Kind {
	case relKindBelongsTo, relKindHasOne:
		if field.Kind() != reflect.Pointer {
			return nil, false
		}

		return []reflect.Value{field}, true
	case relKindHasMany:
		if field.Kind() != reflect.Slice {
			return nil, false
		}

		owners := make([]reflect.Value, 0, field.Len())
		for index := 0; index < field.Len(); index++ {
			element := field.Index(index)
			switch element.Kind() {
			case reflect.Pointer:
				owners = append(owners, element)
			case reflect.Struct:
				owners = append(owners, element.Addr())
			default:
				return nil, false
			}
		}

		return owners, true
	}

	return nil, false
}

// touchedRelationNames returns, sorted, the names of the relations the model declares with
// Touches. Eloquent walks its $touches array in declaration order; a Go map has no order, so the
// names are sorted for a deterministic statement sequence. Only the BelongsTo / HasOne / HasMany
// descriptors carry the flag — Many2Many.Touches is the pivot-write behaviour of touchIfTouching,
// not a save-or-delete owner touch.
func touchedRelationNames(relations map[string]contractsorm.Relation) []string {
	names := make([]string, 0, len(relations))

	for name, relation := range relations {
		touches := false
		switch typed := relation.(type) {
		case contractsorm.BelongsTo:
			touches = typed.Touches
		case contractsorm.HasOne:
			touches = typed.Touches
		case contractsorm.HasMany:
			touches = typed.Touches
		}

		if touches {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	return names
}

// touchColumns names the columns a touch matches on: the column on the related table holding the
// match value, and the column on the parent table the value is read from. The descriptor's single
// reference pair points related -> parent for BelongsTo (the parent carries the foreign key) and
// parent -> related for HasOne / HasMany (the related carries it).
func touchColumns(desc *relationDescriptor) (relatedColumn string, parentColumn string, ok bool) {
	if len(desc.References) == 0 {
		return "", "", false
	}

	ref := desc.References[0]
	switch desc.Kind {
	case relKindBelongsTo:
		return ref.PrimaryColumn, ref.ForeignColumn, true
	case relKindHasOne, relKindHasMany:
		return ref.ForeignColumn, ref.PrimaryColumn, true
	}

	return "", "", false
}

// touchedUpdatedAtColumn returns the column a touch stamps on the related model, the Go stand-in
// for Eloquent's getUpdatedAtColumn(): the automatic updated-at field's column, or the column
// named updated_at. Empty when the model declares neither.
func touchedUpdatedAtColumn(related any) (string, error) {
	schema, err := parseSchema(related)
	if err != nil {
		return "", err
	}

	for _, field := range schema.Fields {
		if field.AutoUpdateTime != 0 {
			return field.DBName, nil
		}
	}

	if field, ok := schema.FieldsByDBName["updated_at"]; ok {
		return field.DBName, nil
	}

	return "", nil
}

// touchIdentity names a model in the walk's visited set: its class plus its key. Eloquent's
// recursion guard holds object identities; the Go walk may reuse cached relations or load fresh
// instances as it goes, so the guard stands on the row identity instead — the same rows are never
// walked twice, which terminates on cyclic graphs where object identities alone could not.
type touchIdentity struct {
	class reflect.Type
	key   string
}

// touchIdentityOf reads a model's walk identity, or nil when it carries no key.
func touchIdentityOf(model any) *touchIdentity {
	key := databaseorm.GetID(model)
	if key == nil {
		return nil
	}

	return &touchIdentity{class: indirectStructType(reflect.TypeOf(model)), key: fmt.Sprint(key)}
}
