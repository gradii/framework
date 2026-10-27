// Package gorm contains the GORM-backed implementation of the framework's database/orm contracts.
//
// Where the upstream framework has first-class Relation objects with `getRelationExistenceQuery` /
// `getRelationExistenceCountQuery` methods, GORM models its relationships through struct-tag
// metadata. The two are bridged here by relation.go's resolver: it inspects gorm.Schema and a
// model's optional ModelWithThroughRelations declaration, then produces a relationDescriptor
// that knows how to emit a correlated EXISTS / count subquery for any of HasOne, HasMany,
// BelongsTo, BelongsToMany, MorphOne, MorphMany, HasOneThrough or HasManyThrough.
package gorm

import (
	"fmt"
	"strings"

	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/database/query/core"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/deep"
	"github.com/goravel/framework/support/str"
)

// ---------------------------------------------------------------------------
// Public API: existence (has / whereHas / doesntHave / orWhereDoesntHave / ...)
//
// Each method appends a relationExistence to conditions.relations; the actual subquery is built
// when the query is compiled to SQL (so the parent model from .Model() / dest can be resolved).
// ---------------------------------------------------------------------------

// Has adds a relationship count / exists condition to the query.
//
// args may include any combination of a RelationCallback (or func(Query) Query) for scoping the
// inner subquery, a string operator (defaults to ">="), and an int count (defaults to 1). For
// nested relations dot-notation is honoured: `Has("Books.Author")` is shorthand for
// `WhereHas("Books", q -> q.Has("Author"))`.
func (r *Query) Has(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", false)
}

// OrHas adds a relationship count / exists condition to the query with an "or" conjunction.
func (r *Query) OrHas(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", false)
}

// DoesntHave adds a relationship absence condition to the query.
// Equivalent to Has(rel, "<", 1).
func (r *Query) DoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", true)
}

// OrDoesntHave adds a relationship absence condition with an "or" conjunction.
func (r *Query) OrDoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", true)
}

// WhereHas adds a relationship count / exists condition to the query with where clauses.
// Functionally identical to Has - the rename merely makes call sites that always pass a callback
// read more naturally.
func (r *Query) WhereHas(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", false)
}

// OrWhereHas adds a relationship count / exists condition with where clauses and an "or"
// conjunction.
func (r *Query) OrWhereHas(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", false)
}

// WhereDoesntHave adds a relationship absence condition to the query with where clauses.
func (r *Query) WhereDoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", true)
}

// OrWhereDoesntHave adds a relationship absence condition with where clauses and an "or"
// conjunction.
func (r *Query) OrWhereDoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", true)
}

// HasMorph adds a polymorphic relationship count / exists condition to the query.
// types is a slice of model instances; the morph value used in the polymorphic type column is
// derived from each model's GORM-resolved table name.
//
// Note: auto-discovery of distinct morph values via `types = ['*']` is not supported; an explicit
// list of model instances is required.
func (r *Query) HasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", false)
}

// OrHasMorph adds a polymorphic relationship count / exists condition with an "or" conjunction.
func (r *Query) OrHasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", false)
}

// DoesntHaveMorph adds a polymorphic relationship absence condition.
func (r *Query) DoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", true)
}

// OrDoesntHaveMorph adds a polymorphic relationship absence condition with an "or" conjunction.
func (r *Query) OrDoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", true)
}

// WhereHasMorph adds a polymorphic existence condition with where clauses; callbacks may be
// MorphRelationCallback for per-type scoping.
func (r *Query) WhereHasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", false)
}

// OrWhereHasMorph adds a polymorphic existence condition with where clauses and an "or"
// conjunction.
func (r *Query) OrWhereHasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", false)
}

// WhereDoesntHaveMorph adds a polymorphic absence condition with where clauses.
func (r *Query) WhereDoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", true)
}

// OrWhereDoesntHaveMorph adds a polymorphic absence condition with where clauses and an "or"
// conjunction.
func (r *Query) OrWhereDoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", true)
}

// ---------------------------------------------------------------------------
// Public API: aggregate sub-selects (withCount / withMax / withSum / ...)
//
// Each method appends a selectSub to conditions.selectSubs; the actual sub-select column is
// emitted when the query is compiled to SQL (so the parent model can be resolved and the alias derived).
// ---------------------------------------------------------------------------

// WithAggregate adds a sub-select to include an aggregate value for a relationship.
// fn must be one of: count, max, min, sum, avg, exists.
func (r *Query) WithAggregate(relation, column, fn string, args ...any) contractsorm.Query {
	if !validAggregateFn(fn) {
		return r.withError(errors.OrmRelationInvalidAggregate.Args(fn))
	}
	cb, _, _, err := parseRelationArgs(args)
	if err != nil {
		return r.withError(err)
	}
	conditions := r.conditions
	conditions.selectSubs = deep.Append(conditions.selectSubs, selectSub{
		relation: relation,
		column:   column,
		function: fn,
		alias:    aggregateAlias(relation, fn, column),
		callback: cb,
	})
	return r.setConditions(conditions)
}

// WithCount adds sub-select queries to count the relations. Each entry may be either:
//   - a string ("Books") - emits `(SELECT COUNT(*) FROM ...) AS books_count`
//   - a contractsorm.RelationCount struct for scoped counts and/or custom alias
func (r *Query) WithCount(relations ...any) contractsorm.Query {
	current := r
	for _, raw := range relations {
		switch v := raw.(type) {
		case string:
			next, ok := current.WithAggregate(v, "*", "count").(*Query)
			if !ok {
				return current
			}
			current = next
		case contractsorm.RelationCount:
			args := []any{}
			if v.Callback != nil {
				args = append(args, v.Callback)
			}
			next, ok := current.WithAggregate(v.Name, "*", "count", args...).(*Query)
			if !ok {
				return current
			}
			if v.Alias != "" {
				if n := len(next.conditions.selectSubs); n > 0 {
					next.conditions.selectSubs[n-1].alias = v.Alias
				}
			}
			current = next
		default:
			return r.withError(errors.OrmRelationInvalidArgument.Args(raw))
		}
	}
	return current
}

// WithMax adds sub-select queries to include the max of the relation's column.
func (r *Query) WithMax(relation, column string, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "max", args...)
}

// WithMin adds sub-select queries to include the min of the relation's column.
func (r *Query) WithMin(relation, column string, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "min", args...)
}

// WithSum adds sub-select queries to include the sum of the relation's column.
func (r *Query) WithSum(relation, column string, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "sum", args...)
}

// WithAvg adds sub-select queries to include the average of the relation's column.
func (r *Query) WithAvg(relation, column string, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "avg", args...)
}

// WithExists adds sub-select queries to include the existence of related models. The result is
// emitted as `CASE WHEN EXISTS (...) THEN 1 ELSE 0 END` for cross-dialect portability (SQL Server
// has no boolean literal). The dest field may be either `bool` or an integer type - Go's
// database/sql layer converts 0/1 ints to bool automatically.
func (r *Query) WithExists(relations ...string) contractsorm.Query {
	current := r
	for _, rel := range relations {
		next, ok := current.WithAggregate(rel, "*", "exists").(*Query)
		if !ok {
			return current
		}
		current = next
	}
	return current
}

// ---------------------------------------------------------------------------
// Public API: eager loading (With / Without / WithOnly)
//
// These methods build up conditions.eagerLoad. The actual loader runs after the main query
// returns; see eager_loader.go for the execution side.
// ---------------------------------------------------------------------------

// With eagerly loads the given relationships using Goravel's own loader. Accepts the
// union of fedaco's with(...) shapes; see the orm.Query interface comment for the full grammar.
func (r *Query) With(args ...any) contractsorm.Query {
	entries, err := parseEagerLoad(args)
	if err != nil {
		return r.withError(err)
	}
	conditions := r.conditions
	for _, e := range entries {
		conditions.eagerLoad = upsertEagerLoadEntry(conditions.eagerLoad, e, e.callback != nil || e.columns != nil)
	}
	return r.setConditions(conditions)
}

// Without removes the named relations from the eager-load list. Mirrors fedaco's
// without(). Names must match exactly (including dot-paths, e.g. "Books.Author").
func (r *Query) Without(relations ...string) contractsorm.Query {
	if len(relations) == 0 || len(r.conditions.eagerLoad) == 0 {
		return r
	}
	drop := make(map[string]struct{}, len(relations))
	for _, n := range relations {
		drop[n] = struct{}{}
	}
	conditions := r.conditions
	filtered := make([]eagerLoadEntry, 0, len(conditions.eagerLoad))
	for _, e := range conditions.eagerLoad {
		if _, omit := drop[e.relation]; omit {
			continue
		}
		filtered = append(filtered, e)
	}
	conditions.eagerLoad = filtered
	return r.setConditions(conditions)
}

// WithOnly clears the eager-load list, then adds the given relations. Mirrors fedaco's
// withOnly(). Useful when a default-scoped query has eager loads you want to override.
func (r *Query) WithOnly(args ...any) contractsorm.Query {
	conditions := r.conditions
	conditions.eagerLoad = nil
	return r.setConditions(conditions).With(args...)
}

// ---------------------------------------------------------------------------
// Internal: queueing
// ---------------------------------------------------------------------------

func (r *Query) queueRelationExistence(relation string, args []any, conjunction string, doesntHave bool) contractsorm.Query {
	cb, op, count, err := parseRelationArgs(args)
	if err != nil {
		return r.withError(err)
	}
	if doesntHave {
		op = "<"
		count = 1
	}
	conditions := r.conditions
	conditions.relations = deep.Append(conditions.relations, relationExistence{
		relation:    relation,
		operator:    op,
		count:       count,
		conjunction: conjunction,
		callback:    cb,
	})
	return r.setConditions(conditions)
}

func (r *Query) queueMorphExistence(relation string, types []any, args []any, conjunction string, doesntHave bool) contractsorm.Query {
	if len(types) == 0 {
		return r.withError(errors.OrmRelationMorphTypesEmpty)
	}
	cb, mcb, op, count, err := parseMorphRelationArgs(args)
	if err != nil {
		return r.withError(err)
	}
	if doesntHave {
		op = "<"
		count = 1
	}
	conditions := r.conditions
	conditions.relations = deep.Append(conditions.relations, relationExistence{
		relation:      relation,
		operator:      op,
		count:         count,
		conjunction:   conjunction,
		callback:      cb,
		morphTypes:    types,
		morphCallback: mcb,
	})
	return r.setConditions(conditions)
}

// ---------------------------------------------------------------------------
// Internal: existence subquery construction
// ---------------------------------------------------------------------------

// compileExistenceSubquery builds the correlated inner SELECT for a relation as a framework builder.
// Callers render it with ToSelectSQL and inline the result, so no GORM statement carries the
// subquery. The builder is nil when the relation kind is unsupported.
func (r *Query) compileExistenceSubquery(desc *relationDescriptor, callback contractsorm.RelationCallback) (*querybuilder.Builder, error) {
	builder := r.subqueryBuilder(desc.relatedTable)

	switch desc.kind {
	case relKindHasOne, relKindHasMany, relKindBelongsTo, relKindMorphOne, relKindMorphMany:
		for _, ref := range desc.references {
			builder.WhereRaw(r.ident(ref.primaryTable, ref.primaryColumn)+" = "+r.ident(ref.foreignTable, ref.foreignColumn), nil, core.BooleanAnd)
		}
		if desc.kind == relKindMorphOne || desc.kind == relKindMorphMany {
			builder.WhereRaw(r.ident(desc.relatedTable, desc.morphTypeColumn)+" = ?", []any{desc.morphValue}, core.BooleanAnd)
		}
	case relKindMany2Many, relKindMorphToMany:
		builder.JoinRaw(r.tableRef(desc.pivotTable)+" on "+
			r.ident(desc.pivotTable, desc.pivotRelatedRef.foreignColumn)+" = "+
			r.ident(desc.relatedTable, desc.pivotRelatedRef.primaryColumn), nil, "inner")
		builder.WhereRaw(r.ident(desc.pivotParentRef.primaryTable, desc.pivotParentRef.primaryColumn)+" = "+
			r.ident(desc.pivotTable, desc.pivotParentRef.foreignColumn), nil, core.BooleanAnd)
		if desc.kind == relKindMorphToMany {
			builder.WhereRaw(r.ident(desc.pivotTable, desc.morphTypeColumn)+" = ?", []any{desc.morphValue}, core.BooleanAnd)
		}
	case relKindHasOneThrough, relKindHasManyThrough:
		builder.JoinRaw(r.tableRef(desc.throughTable)+" on "+
			r.ident(desc.relatedTable, desc.secondKey)+" = "+
			r.ident(desc.throughTable, desc.secondLocalKey), nil, "inner")
		builder.WhereRaw(r.ident(desc.parentTable, desc.localKey)+" = "+
			r.ident(desc.throughTable, desc.firstKey), nil, core.BooleanAnd)
	default:
		return nil, errors.OrmRelationUnsupported.Args(desc.name, desc.parentTable, fmt.Sprintf("kind=%d", desc.kind))
	}

	// Apply the relation's default scope before the caller's callback so the user's WhereHas
	// callback can layer on top of the always-on filter.
	r.applyRelationScope(builder, desc.onQuery)
	r.applyRelationScope(builder, callback)

	r.applySubquerySoftDelete(builder, desc.relatedTable, desc.relatedModel)

	if desc.nested != nil {
		nested, err := r.compileExistenceSubquery(desc.nested, nil)
		if err != nil {
			return nil, err
		}
		if nested != nil {
			nestedSQL, nestedBindings := nested.ToSelectSQL()
			builder.WhereRaw("exists ("+nestedSQL+")", nestedBindings, core.BooleanAnd)
		}
	}

	setSubquerySelect(builder, "1")

	return builder, nil
}

// compileMorphExistenceSubquery is the morph-specific variant. The morph_type column is pinned on the
// related table, inside the subquery, and the type comparison the caller needs lives in the subquery.
func (r *Query) compileMorphExistenceSubquery(desc *relationDescriptor, morphValue string, callback contractsorm.RelationCallback) *querybuilder.Builder {
	builder := r.subqueryBuilder(desc.relatedTable)
	for _, ref := range desc.references {
		builder.WhereRaw(r.ident(ref.primaryTable, ref.primaryColumn)+" = "+r.ident(ref.foreignTable, ref.foreignColumn), nil, core.BooleanAnd)
	}
	builder.WhereRaw(r.ident(desc.relatedTable, desc.morphTypeColumn)+" = ?", []any{morphValue}, core.BooleanAnd)
	r.applyRelationScope(builder, desc.onQuery)
	r.applyRelationScope(builder, callback)
	r.applySubquerySoftDelete(builder, desc.relatedTable, desc.relatedModel)
	setSubquerySelect(builder, "1")

	return builder
}

// compileAggregateSubquery returns the framework builder for the inner SELECT of an aggregate. The
// select expression is dictated by sub.function.
func (r *Query) compileAggregateSubquery(desc *relationDescriptor, sub selectSub) (*querybuilder.Builder, error) {
	inner, err := r.compileExistenceSubquery(desc, sub.callback)
	if err != nil {
		return nil, err
	}
	if inner == nil {
		return nil, nil
	}

	var selectExpr string
	switch sub.function {
	case "count":
		selectExpr = "count(*)"
	case "exists":
		selectExpr = "1"
	default:
		col := "*"
		if sub.column != "*" && sub.column != "" {
			col = r.ident(desc.relatedTable, sub.column)
		}
		selectExpr = fmt.Sprintf("%s(%s)", sub.function, col)
	}
	setSubquerySelect(inner, selectExpr)

	return inner, nil
}

// ---------------------------------------------------------------------------
// Internal: relation conditions on the compiled statement
// ---------------------------------------------------------------------------

// applyRelations puts the query's relation existence conditions on the compiled statement.
//
// The correlated subquery is assembled by the framework builder and rendered to the SQL and bindings
// the statement carries, so what is decided here is only the clause: the same EXISTS optimisation,
// negation and conjunction the GORM path hands over, and for the polymorphic variants the same set of
// per-type clauses joined into one group.
func (r *Query) applyRelations(builder *querybuilder.Builder) error {
	if len(r.conditions.relations) == 0 {
		return nil
	}

	parent := r.parentModel()
	if parent == nil {
		return errors.OrmQueryEmptyRelation
	}

	for _, item := range r.conditions.relations {
		boolean := core.BooleanAnd
		if item.conjunction == "or" {
			boolean = core.BooleanOr
		}

		if len(item.morphTypes) > 0 {
			if err := r.applyMorphRelation(builder, parent, item, boolean); err != nil {
				return err
			}

			continue
		}

		desc, err := resolveRelation(parent, item.relation)
		if err != nil {
			return err
		}

		inner, err := r.compileExistenceSubquery(desc, item.callback)
		if err != nil {
			return err
		}
		if inner == nil {
			continue
		}

		applyExistenceClause(builder, inner, item.operator, item.count, boolean)
	}

	return nil
}

// applyMorphRelation adds a polymorphic existence condition. Each requested type is a table of its own,
// so the types contribute one clause each to a single group, which the condition's conjunction then
// attaches to the query.
func (r *Query) applyMorphRelation(builder *querybuilder.Builder, parent any, item relationExistence, boolean string) error {
	desc, err := resolveRelation(parent, item.relation)
	if err != nil {
		return err
	}

	switch desc.kind {
	case relKindMorphOne, relKindMorphMany, relKindMorphTo:
	default:
		return errors.OrmRelationUnsupported.Args(item.relation, fmt.Sprintf("%T", parent), "morph (must be polymorphic relation)")
	}

	builder.WhereNested(func(group *querybuilder.Builder) *querybuilder.Builder {
		for _, typeModel := range item.morphTypes {
			// A type that cannot be resolved contributes nothing rather than failing the query, which
			// is what the GORM path does: it skips the type and keeps the others.
			if err := r.applyMorphTypeClause(group, desc, typeModel, item); err != nil {
				continue
			}
		}

		return group
	}, boolean)

	return nil
}

// applyMorphTypeClause adds the clauses one requested type contributes to the group.
//
// The type is pinned inside the subquery for the outbound kinds, whose morph column lives on the
// related table, and against the parent table for the inverse kind, whose column lives there. Either
// way the type and its subquery are ANDed inside the group, so the group reads as one alternative per
// type.
func (r *Query) applyMorphTypeClause(builder *querybuilder.Builder, desc *relationDescriptor, typeModel any, item relationExistence) error {
	relatedTable, err := tableNameFor(typeModel)
	if err != nil {
		return err
	}

	morphValue := resolveMorphValue(typeModel, relatedTable)

	callback := item.callback
	if item.morphCallback != nil {
		morphCallback := item.morphCallback
		callback = func(query contractsorm.Query) contractsorm.Query {
			return morphCallback(query, morphValue)
		}
	}

	if desc.kind == relKindMorphTo {
		inner := r.compileMorphToExistenceSubquery(desc, relatedTable, typeModel, callback)

		builder.WhereRaw(r.ident(desc.parentTable, desc.morphTypeColumn)+" = ?", []any{morphValue}, core.BooleanAnd)

		applyExistenceClause(builder, inner, item.operator, item.count, core.BooleanAnd)

		return nil
	}

	inner := r.compileMorphExistenceSubquery(desc, morphValue, callback)

	applyExistenceClause(builder, inner, item.operator, item.count, core.BooleanAnd)

	return nil
}

// compileMorphToExistenceSubquery builds the belongs-to-shaped inner query an inverse polymorphic
// condition correlates against one of its types: that type's own key against the parent's morph id.
//
// The type is one of the morph map's models, so each type decides its own soft-delete predicate: a
// type still declaring gorm.DeletedAt is left to GORM, exactly as on every other compiled path, while
// a type carrying the framework's own soft-delete type gets its "not deleted" predicate inside the
// subquery - the same per-type treatment Laravel's SoftDeletingScope gives the BelongsTo it builds.
func (r *Query) compileMorphToExistenceSubquery(desc *relationDescriptor, relatedTable string, relatedModel any, callback contractsorm.RelationCallback) *querybuilder.Builder {
	ownerKey := desc.morphOwnerKey
	if ownerKey == "" {
		ownerKey = "id"
	}

	builder := r.subqueryBuilder(relatedTable)
	builder.WhereRaw(r.ident(relatedTable, ownerKey)+" = "+r.ident(desc.parentTable, desc.morphIDColumn), nil, core.BooleanAnd)

	r.applyRelationScope(builder, desc.onQuery)
	r.applyRelationScope(builder, callback)

	r.applySubquerySoftDelete(builder, relatedTable, relatedModel)

	return builder
}

// applyExistenceClause adds one existence clause, or the count comparison that stands in for it when
// EXISTS is not the matching shape. The subquery is rendered from the framework builder, so no GORM
// statement carries it.
func applyExistenceClause(builder *querybuilder.Builder, inner *querybuilder.Builder, operator string, count int, boolean string) {
	if shouldUseExists(operator, count) {
		subquery, bindings := inner.ToSelectSQL()
		builder.WhereRaw(existenceKeyword(operator, count)+" ("+subquery+")", bindings, boolean)

		return
	}

	setSubquerySelect(inner, "count(*)")
	subquery, bindings := inner.ToSelectSQL()
	builder.WhereRaw("("+subquery+") "+operator+" ?", append(bindings, count), boolean)
}

// existenceKeyword names the clause an existence condition becomes, negated when the condition asked
// for the rows without the relation.
func existenceKeyword(operator string, count int) string {
	if operator == "<" && count == 1 {
		return "not exists"
	}

	return "exists"
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// relatedChild returns a child query addressing one table and matching a column against a list of
// values. It is the shape every eager loader builds per chunk, expressed in the conditions the compiler
// reads rather than as a statement handed to GORM.
//
// The column is named the way a caller writes one — unquoted, with a dot separating a table from its
// column — because the compiler quotes each segment. A name that arrived already quoted would be
// quoted again and the statement would not run.
func (r *Query) relatedChild(table string, column string, values []any) *Query {
	// The child starts from no conditions of its own, as the fresh session it replaced did. Inheriting
	// the receiver's would carry the parent query's where clause into the child and narrow it to the
	// rows the parent matched.
	child := r.fresh()

	conditions := child.conditions
	conditions.table = &Table{name: table}
	conditions.where = append(conditions.where, contractsdriver.Where{Query: column, Args: []any{values}})

	return child.setConditions(conditions)
}

// applySubquerySoftDelete adds the related model's "not soft deleted" predicate inside an existence
// subquery. Laravel applies the related model's SoftDeletingScope to the subquery, and the compiled
// relation read (applySoftDelete) already filters deleted rows, so without this an existence check
// would match a parent whose only related rows are soft-deleted. Only the framework's own soft-delete
// type is handled: a related model still declaring gorm.DeletedAt is left to GORM, exactly as on every
// other compiled path.
func (r *Query) applySubquerySoftDelete(builder *querybuilder.Builder, table string, model any) {
	if !usesFrameworkSoftDeleteModel(model) {
		return
	}

	column, ok := softDeleteColumn(model)
	if !ok {
		return
	}

	builder.WhereRaw(r.ident(table, column)+" IS NULL", nil, core.BooleanAnd)
}

// subqueryBuilder starts a framework builder for a correlated subquery over table.
//
// The name is bare, like every table name the ORM holds, and goes in through tableRef so the
// grammar renders it with the dialect quoting and the connection prefix exactly once.
func (r *Query) subqueryBuilder(table string) *querybuilder.Builder {
	builder := querybuilder.NewBuilder(r.queryGrammar)
	builder.SetDriver(r.dbConfig.Driver)
	builder.FromRaw(r.tableRef(table), nil)

	return builder
}

// setSubquerySelect replaces a subquery's select list with a raw expression. The builder's Select
// would quote the expression, so a raw one is used instead.
func setSubquerySelect(builder *querybuilder.Builder, expression string) {
	builder.Select(querybuilder.Expression{Value: expression})
}

// applyRelationScope runs a relation callback against a child query and translates its conditions onto
// the subquery builder. The callback is handed a wrapper query, exactly as it was when the subquery
// was assembled with GORM, but its clauses now land on the framework builder.
func (r *Query) applyRelationScope(builder *querybuilder.Builder, callback contractsorm.RelationCallback) {
	if callback == nil {
		return
	}

	child := r.fresh()

	if scoped, ok := callback(child).(*Query); ok {
		scoped.applyWhere(builder)

		return
	}

	child.applyWhere(builder)
}

func (r *Query) parentModel() any {
	if r.conditions.model != nil {
		return r.conditions.model
	}
	if r.conditions.dest != nil {
		if m, err := modelToStruct(r.conditions.dest); err == nil && m != nil {
			return m
		}
	}
	// The statement's model is not consulted: it is only ever set from conditions.model, which the
	// branches above already read.
	return nil
}

func (r *Query) parentTable(parent any) string {
	if t, err := tableNameFor(parent); err == nil {
		return t
	}
	return ""
}

// parseRelationArgs unpacks variadic args of unknown order/length into (callback, op, count).
//
// Acceptable shapes (any combination):
//   - (callback)
//   - (callback, op)
//   - (callback, op, count)
//   - (op)
//   - (op, count)
//
// Defaults: op = ">=", count = 1.
func parseRelationArgs(args []any) (contractsorm.RelationCallback, string, int, error) {
	op := ">="
	count := 1
	var cb contractsorm.RelationCallback

	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case contractsorm.RelationCallback:
			cb = v
		case func(contractsorm.Query) contractsorm.Query:
			cb = contractsorm.RelationCallback(v)
		case string:
			op = v
		case int:
			count = v
		case int64:
			count = int(v)
		default:
			return nil, op, count, errors.OrmRelationInvalidArgument.Args(v)
		}
	}
	return cb, op, count, nil
}

// parseMorphRelationArgs is a variant that also accepts MorphRelationCallback.
func parseMorphRelationArgs(args []any) (contractsorm.RelationCallback, contractsorm.MorphRelationCallback, string, int, error) {
	op := ">="
	count := 1
	var cb contractsorm.RelationCallback
	var mcb contractsorm.MorphRelationCallback

	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case contractsorm.MorphRelationCallback:
			mcb = v
		case func(contractsorm.Query, string) contractsorm.Query:
			mcb = contractsorm.MorphRelationCallback(v)
		case contractsorm.RelationCallback:
			cb = v
		case func(contractsorm.Query) contractsorm.Query:
			cb = contractsorm.RelationCallback(v)
		case string:
			op = v
		case int:
			count = v
		case int64:
			count = int(v)
		default:
			return nil, nil, op, count, errors.OrmRelationInvalidArgument.Args(v)
		}
	}
	return cb, mcb, op, count, nil
}

// shouldUseExists is the standard optimisation: comparisons of the form ">= 1" or "< 1" can be
// emitted as cheaper EXISTS / NOT EXISTS instead of a full COUNT(*) sub-select.
func shouldUseExists(op string, count int) bool {
	return (op == ">=" || op == "<") && count == 1
}

func validAggregateFn(fn string) bool {
	switch fn {
	case "count", "max", "min", "sum", "avg", "exists":
		return true
	}
	return false
}

func aggregateAlias(relation, fn, column string) string {
	rel := str.Of(strings.ReplaceAll(relation, ".", "_")).Snake().String()
	if column == "*" || column == "" {
		return fmt.Sprintf("%s_%s", rel, fn)
	}
	return fmt.Sprintf("%s_%s_%s", rel, fn, column)
}

// Sanity: ensure *Query satisfies contractsorm.QueryWithRelations at compile time.
var _ contractsorm.QueryWithRelations = (*Query)(nil)
