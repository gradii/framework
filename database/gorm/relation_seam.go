package gorm

import (
	"reflect"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
)

// This file is the gorm-side implementation of the S2 seam widening (step 1): the exported twins
// of the gorm-internal members the Tier-2 relation write workers (relation_writes.go) reach, so
// the workers can later be ported into database/gorm/relations as free functions over
// (relations.Builder, relations.RelationInfo). The decision table — which internal member each
// twin stands for, and which pure helpers were duplicated into relations instead — sits above the
// Builder interface in relations/wrap.go. Every method forwards to, or re-assembles verbatim, the
// internal path it names: no statement shape, no error and no session semantics change here.

// --- model / column access (W1-W3; parseSchema / readParentColumn / tableNameFor) ---------------

// ColumnValue reads the value of a DB column off a model through the parsed schema — W2, the
// promoted Hooks.ReadColumn; it is readParentColumn, error text included.
func (r *OrmBuilder) ColumnValue(model any, column string) (any, error) {
	return readParentColumn(r, model, column)
}

// SetColumn writes value into model's column through the parsed schema — W1, the schemaField.Set
// call the FK / attribute / touch writers make. The bool reports whether the schema carries the
// column; a model that is not a non-nil pointer to a struct cannot be written and reports
// OrmQueryInvalidModel (a nil model reports it from the schema parse).
func (r *OrmBuilder) SetColumn(model any, column string, value any) (bool, error) {
	schema, err := parseSchema(model)
	if err != nil {
		return false, err
	}
	field, ok := schema.FieldsByDBName[column]
	if !ok {
		return false, nil
	}

	target := reflect.ValueOf(model)
	if target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Struct {
		return false, errors.OrmQueryInvalidModel.Args(target.Type().String())
	}
	if err := field.Set(r.ctx, target.Elem(), value); err != nil {
		return false, err
	}

	return true, nil
}

// ColumnType reports the Go type of model's column — W3, the schemaField.FieldType the
// dissociate writers zero and castKeys targets; false when the schema carries no such column.
func (r *OrmBuilder) ColumnType(model any, column string) (reflect.Type, bool, error) {
	schema, err := parseSchema(model)
	if err != nil {
		return nil, false, err
	}
	field, ok := schema.FieldsByDBName[column]
	if !ok {
		return nil, false, nil
	}

	return field.FieldType, true, nil
}

// TableOf resolves a model's declared table name without the connection prefix — tableNameFor.
func (r *OrmBuilder) TableOf(model any) (string, error) {
	return tableNameFor(model)
}

// RelationInfoOf resolves parent's relation by name and projects it — the resolveRelation call
// every write shell opens with, projected through relationInfoFromDescriptor the way Wrap
// receives it. The projection is built without a bound read builder: OneOfMany and the MorphTo
// related-table fallback are read-path facts the write workers never consult.
func (r *OrmBuilder) RelationInfoOf(parent any, relation string) (relations.RelationInfo, error) {
	desc, err := relations.ResolveRelation(parent, relation)
	if err != nil {
		return relations.RelationInfo{}, err
	}

	return relationInfoFromDescriptor(desc, nil, parent), nil
}

// --- sessions, transactions, single-row reads (fresh / relationWriteSession / W8 / firstRow) -----

// NewQuery builds the same query with no state of its own — fresh(). contracts Query.Fresh is
// the model refresh, hence the name.
func (r *OrmBuilder) NewQuery() contractsorm.Query {
	query := r.fresh()
	// The columns WithZero named travel with the relation write the new query persists.
	conditions := query.conditions
	conditions.withZero = append([]string(nil), r.conditions.withZero...)

	return query.setConditions(conditions)
}

// WriteSession is the fresh session a relation write persists through, keeping the caller's
// event suppression — relationWriteSession().
func (r *OrmBuilder) WriteSession() contractsorm.Query {
	return r.relationWriteSession()
}

// ApplyPendingAttributes writes the query's WithAttributes-recorded pending attributes onto dest,
// the caller's own non-zero values winning — applyPendingAttributes, the twin SetRelationFKOnChild
// calls to run setForeignAttributesForCreate's pending loop (HasOneOrMany.php:498-504).
func (r *OrmBuilder) ApplyPendingAttributes(dest any) error {
	return r.applyPendingAttributes(dest)
}

// WithSavepoint runs scope directly outside a transaction and inside a savepoint within one — W8,
// withSavepointIfNeeded.
func (r *OrmBuilder) WithSavepoint(scope func() error) error {
	return r.withSavepointIfNeeded(scope)
}

// FirstFound runs this query as a single-row read into dest and reports whether a row was found —
// firstRow, the flag firstFoundOn surfaces for the find-then-write family.
func (r *OrmBuilder) FirstFound(dest any) (bool, error) {
	return r.firstRow(dest)
}

// --- the relation-level limit (followup-take-grouplimit): the group limit and its legacy decision --

// GroupLimit sets the query builder's "group limit" — Query\Builder::groupLimit (Builder.php:3128),
// which the relation objects' limit() reaches through Eloquent\Builder::__call when the parent does
// not exist. The value and the partition column ride the persistent builder (newBuilder clones the
// whole state), so the select compiles through the grammar's group-limit dispatch — the
// row_number() over (partition by column) derived table bounded at value rows per group — in place
// of the plain select; a negative value is ignored, as groupLimit ignores it.
func (r *OrmBuilder) GroupLimit(value int, column string) contractsorm.Query {
	r.query.GroupLimit(value, column)

	return r
}

// UseLegacyGroupLimit reports whether the bound grammar compiles the group limit through the
// legacy rewrite (MySqlGrammar::useLegacyGroupLimit — MySQL before 8.0.11, never MariaDB), the
// decision HasOneOrManyThrough::limit (:779-783) and BelongsToMany::limit (:1510-1514) consult
// before choosing the partition column. False when no grammar is bound.
func (r *OrmBuilder) UseLegacyGroupLimit() bool {
	if r.queryGrammar == nil {
		return false
	}

	return r.queryGrammar.UseLegacyGroupLimit(r.query)
}

// --- row primitives (W4-W7; ident / addWhere / newBuilder / findMapsCompiled / pivot scope) -----

// Ident renders a dotted identifier with the connection's own quoting — ident.
func (r *OrmBuilder) Ident(parts ...string) string {
	return r.ident(parts...)
}

// InsertRows inserts rows into table — W4, insertPivotRows verbatim.
func (r *OrmBuilder) InsertRows(table string, rows []map[string]any) error {
	return r.insertPivotRows(table, rows)
}

// UpdateRows compiles and runs an update of values on the rows of table the predicates select —
// W5, the doUpdateExistingPivot / touchIfTouching statement: compileUpdate + execStatement over
// the scoped row query. Reports the rows affected.
func (r *OrmBuilder) UpdateRows(table string, wheres []relations.RowWhere, scope contractsorm.PivotCallback, values map[string]any) (int64, error) {
	query := r.scopedRowQuery(table, nil, wheres, scope)

	statement, bindings, err := query.compileUpdate(values)
	if err != nil {
		return 0, err
	}
	result, err := query.execStatement(statement, bindings)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

// DeleteRows compiles and runs a delete of the rows of table the predicates select — W6, the
// doDetach statement: execDelete over the scoped row query. Reports the rows affected.
func (r *OrmBuilder) DeleteRows(table string, wheres []relations.RowWhere, scope contractsorm.PivotCallback) (int64, error) {
	result, err := r.scopedRowQuery(table, nil, wheres, scope).execDelete()
	if err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

// SelectRows reads columns of the rows of table the predicates select into column-keyed maps —
// W7, the allPivotIDs read: the select list set on the fresh table query, the
// pivot scope replayed, then the predicates; findMapsCompiled is handed no scope, since the row
// query already carries it.
func (r *OrmBuilder) SelectRows(table string, columns []string, wheres []relations.RowWhere, scope contractsorm.PivotCallback) ([]map[string]any, error) {
	return r.findMapsCompiled(r.scopedRowQuery(table, columns, wheres, scope), pivotScopeDescriptor(table, nil))
}

// scopedRowQuery assembles the table-scoped query the row primitives run: the fresh builder bound
// to table (pivotTableQuery), the select list when one is given (the SetSelects call
// allPivotIDs makes first), the pivot scope replayed through
// applyOnPivotQueryToQueryBare (a nil scope is the adapter's own no-op), then each predicate
// appended through the very addWhere call pivotWhere makes.
//
// The scope replays BARE here - the row primitives are Laravel's newPivotQuery, whose recorded
// pivot wheres reach the statement with the raw (unqualified) column - while the relation read
// path (applyOnPivotQueryToQuery) keeps the pivot-table qualifier, the shape wherePivot applies to
// the relation query. Oracle v12.55.1, executed, /tmp/c27_pivot_qualification_probe.php: the
// pivot-row statements carry `... where "role" = ? ...` (U3/D4/S3/T3), the relation read E3
// carries `"c27_post_tag"."role" = ?`.
//
// The scope's clauses sit ahead of the predicates in the compiled where clause. That is the order
// the statements have always carried: the predicates used to wait on the ORM's internal where
// list and joined the builder when the statement compiled, after the scope had written its
// clauses straight to it. Now that every condition is written to the builder when it is called,
// the scope is replayed first so the compiled statement is byte-identical.
func (r *OrmBuilder) scopedRowQuery(table string, columns []string, wheres []relations.RowWhere, scope contractsorm.PivotCallback) *OrmBuilder {
	query := r.pivotTableQuery(table)
	if len(columns) > 0 {
		selects := make([]any, 0, len(columns))
		for _, column := range columns {
			selects = append(selects, column)
		}
		query.query.SetSelects(selects)
	}
	if scoped, ok := applyOnPivotQueryToQueryBare(r.queryGrammar, query, pivotScopeDescriptor(table, scope)).(*OrmBuilder); ok {
		query = scoped
	}
	for _, where := range wheres {
		query = query.pivotWhere(where.Query, where.Args...)
	}

	return query
}

// pivotScopeDescriptor carries exactly the two descriptor fields the pivot-scope adapter reads —
// the qualifier table and the callback — so the row primitives replay a scope through the one
// adapter the workers use (applyOnPivotQueryToQueryBare / findMapsCompiled) without a full
// descriptor.
func pivotScopeDescriptor(table string, scope contractsorm.PivotCallback) *relationDescriptor {
	return &relationDescriptor{PivotTable: table, OnPivotQuery: scope}
}

// --- gorm-bound helpers the relations package cannot reach (cycle) or duplicate (single source) -

// IsIgnoringTouch reports whether a model's class ignores touches — database/orm's
// IsIgnoringTouch, read per call.
func (r *OrmBuilder) IsIgnoringTouch(model any) bool {
	return databaseorm.IsIgnoringTouch(model)
}

// MorphAliasOf reports the morph alias a model registers through MorphClass() or the morph map,
// without the table-name fallback — resolveMorphAlias.
func (r *OrmBuilder) MorphAliasOf(model any) (string, bool, error) {
	return relations.ResolveMorphAlias(model)
}

// IsUniqueConstraintViolation reports whether err is a unique-constraint violation the way each
// supported connection classifies one — isUniqueConstraintViolation.
func (r *OrmBuilder) IsUniqueConstraintViolation(err error) bool {
	return isUniqueConstraintViolation(err)
}

// InverseWriteBackName reports the inverse relation name a relation write links the child back
// to — inverseWriteBackName: the live chaperone state when this builder carries one (installed at
// wrap time; WithoutChaperone / WithoutInverse clear it to ""), the declared name otherwise.
func (r *OrmBuilder) InverseWriteBackName(declared string) string {
	if r.inverseChaperone != nil {
		return r.inverseChaperone.relation
	}

	return declared
}

// SetRelation stores value on model's relation field by name — the setRelationField call
// applyInverseRelationToCreated makes, with the same guards: model and value must both be non-nil
// pointers (model to a struct), any other shape is the no-op. value is passed as the pointer it
// is, which is what inverseParentRef yields for an addressable parent.
func (r *OrmBuilder) SetRelation(model any, relation string, value any) error {
	target := reflect.ValueOf(model)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return nil
	}
	target = target.Elem()
	if target.Kind() != reflect.Struct {
		return nil
	}

	related := reflect.ValueOf(value)
	if !related.IsValid() || related.Kind() != reflect.Pointer || related.IsNil() {
		return nil
	}

	return setRelationField(target, relation, []reflect.Value{related})
}

// PrimaryKey reads model's primary key through the parsed schema — W10, the
// parseSchema(model).PrimaryFields[0] read FindOrNewRelation's found-check and touchIfTouching's
// WHERE anchor make: the first primary-key column, whether that field holds its Go zero
// (schemaField.ValueOf's flag) and whether the schema carries a primary key at all. The value is
// read off the struct the way readParentColumn addresses it (a pointer is dereferenced once).
func (r *OrmBuilder) PrimaryKey(model any) (string, bool, bool, error) {
	schema, err := parseSchema(model)
	if err != nil {
		return "", true, false, err
	}
	if len(schema.PrimaryFields) == 0 {
		return "", true, false, nil
	}
	field := schema.PrimaryFields[0]
	value := reflect.ValueOf(model)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	_, isZero := field.ValueOf(r.ctx, value)

	return field.DBName, isZero, true, nil
}
