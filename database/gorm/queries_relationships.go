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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/database/orm/morphmap"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/deep"
	"github.com/gradii/framework/support/str"
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
func (r *OrmBuilder) Has(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", false)
}

// OrHas adds a relationship count / exists condition to the query with an "or" conjunction.
func (r *OrmBuilder) OrHas(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", false)
}

// DoesntHave adds a relationship absence condition to the query.
// Equivalent to Has(rel, "<", 1).
func (r *OrmBuilder) DoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", true)
}

// OrDoesntHave adds a relationship absence condition with an "or" conjunction.
func (r *OrmBuilder) OrDoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", true)
}

// WhereHas adds a relationship count / exists condition to the query with where clauses.
// Functionally identical to Has - the rename merely makes call sites that always pass a callback
// read more naturally.
func (r *OrmBuilder) WhereHas(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", false)
}

// OrWhereHas adds a relationship count / exists condition with where clauses and an "or"
// conjunction.
func (r *OrmBuilder) OrWhereHas(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", false)
}

// WhereDoesntHave adds a relationship absence condition to the query with where clauses.
func (r *OrmBuilder) WhereDoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "and", true)
}

// OrWhereDoesntHave adds a relationship absence condition with where clauses and an "or"
// conjunction.
func (r *OrmBuilder) OrWhereDoesntHave(relation string, args ...any) contractsorm.Query {
	return r.queueRelationExistence(relation, args, "or", true)
}

// HasMorph adds a polymorphic relationship count / exists condition to the query.
// types is a slice of model instances; the morph value used in the polymorphic type column is
// derived from each model's GORM-resolved table name. Pass []any{"*"} to resolve the values from
// the parent table instead — Laravel's hasMorph(['*']), whose types come from the distinct
// morph-type column read at compile time; Has / WhereHas / DoesntHave on a MorphTo take that path
// automatically, mirroring how Eloquent's has() delegates to hasMorph($relation, ['*']).
func (r *OrmBuilder) HasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", false)
}

// OrHasMorph adds a polymorphic relationship count / exists condition with an "or" conjunction.
func (r *OrmBuilder) OrHasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", false)
}

// DoesntHaveMorph adds a polymorphic relationship absence condition.
func (r *OrmBuilder) DoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", true)
}

// OrDoesntHaveMorph adds a polymorphic relationship absence condition with an "or" conjunction.
func (r *OrmBuilder) OrDoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", true)
}

// WhereHasMorph adds a polymorphic existence condition with where clauses; callbacks may be
// MorphRelationCallback for per-type scoping.
func (r *OrmBuilder) WhereHasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", false)
}

// OrWhereHasMorph adds a polymorphic existence condition with where clauses and an "or"
// conjunction.
func (r *OrmBuilder) OrWhereHasMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", false)
}

// WhereDoesntHaveMorph adds a polymorphic absence condition with where clauses.
func (r *OrmBuilder) WhereDoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "and", true)
}

// OrWhereDoesntHaveMorph adds a polymorphic absence condition with where clauses and an "or"
// conjunction.
func (r *OrmBuilder) OrWhereDoesntHaveMorph(relation string, types []any, args ...any) contractsorm.Query {
	return r.queueMorphExistence(relation, types, args, "or", true)
}

// ---------------------------------------------------------------------------
// Public API: attached-to (whereAttachedTo / orWhereAttachedTo / whereAttachedToCollection)
//
// Both port QueriesRelationships::whereAttachedTo: the relation's existence subquery is scoped
// with the given models' keys the way Builder::whereKey writes them - integer keys inlined
// through whereIntegerInRaw, other keys bound through whereIn.
// ---------------------------------------------------------------------------

// WhereAttachedTo constrains the query to the rows attached to the given model through a
// many-to-many relation. Mirrors whereAttachedTo($model, $relationshipName = null): the relation
// defaults to the plural camel form of the related model's type name and must resolve to a
// many-to-many relation.
func (r *OrmBuilder) WhereAttachedTo(related any, relation ...string) contractsorm.Query {
	return r.whereAttachedTo(related, relation, false)
}

// OrWhereAttachedTo is WhereAttachedTo with an "or" conjunction. Mirrors orWhereAttachedTo,
// which forwards to whereAttachedTo under Laravel's "or" boolean.
func (r *OrmBuilder) OrWhereAttachedTo(related any, relation ...string) contractsorm.Query {
	return r.whereAttachedTo(related, relation, true)
}

// WhereAttachedToCollection is WhereAttachedTo for a collection of models, mirroring
// whereAttachedTo called with a collection: every model's key joins the list the existence
// subquery matches against the related table's key.
func (r *OrmBuilder) WhereAttachedToCollection(related any, relation ...string) contractsorm.Query {
	return r.whereAttachedTo(related, relation, false)
}

func (r *OrmBuilder) whereAttachedTo(related any, relation []string, or bool) contractsorm.Query {
	models := relatedModels(related)
	if len(models) == 0 {
		return r.withError(errors.OrmWhereAttachedToEmpty)
	}

	parent := r.parentModel()
	if parent == nil {
		return r.withError(errors.OrmQueryInvalidModel.Args("nil"))
	}

	name := ""
	if len(relation) > 0 {
		name = relation[0]
	}
	if name == "" {
		name = str.Of(reflect.Indirect(reflect.ValueOf(models[0])).Type().Name()).Camel().Plural().String()
	}

	desc, err := relations.ResolveRelation(parent, name)
	if err != nil {
		return r.withError(err)
	}
	if desc.Kind != relKindMany2Many && desc.Kind != relKindMorphToMany {
		return r.withError(errors.OrmWhereAttachedToNotBelongsToMany.Args(name, modelName(parent)))
	}

	// The plucked column is the related model's own key name, the way $related->getKeyName()
	// reads it, and each key value is read through the model's schema.
	schema, err := parseSchema(desc.RelatedModel)
	if err != nil || len(schema.PrimaryFields) == 0 {
		return r.withError(errors.OrmQueryPrimaryKeyNotFound.Args(modelName(desc.RelatedModel)))
	}
	keyColumn := schema.PrimaryFields[0].DBName

	keys := make([]any, 0, len(models))
	for _, model := range models {
		key, err := readParentColumn(r, model, keyColumn)
		if err != nil {
			return r.withError(err)
		}
		keys = append(keys, key)
	}

	// Builder::whereKey inlines an integer key list through whereIntegerInRaw - the oracle's
	// `in (49)` carries no binding - and binds any other list through whereIn; the qualified
	// column is the related model's, as getQualifiedKeyName qualifies it.
	column := desc.RelatedTable + "." + keyColumn
	callback := func(query contractsorm.Query) contractsorm.Query {
		if inner, ok := query.(*OrmBuilder); ok && allIntegerKeys(keys) {
			inner.query.WhereIntegerInRaw(column, keys, core.BooleanAnd, false)

			return inner
		}

		return query.WhereIn(column, keys)
	}

	// Laravel's whereAttachedTo hands its $boolean to has(), so the or form joins the
	// statement's wheres with the "or exists" clause the way OrHas renders it.
	if or {
		return r.OrHas(name, callback)
	}

	return r.Has(name, callback)
}

// ---------------------------------------------------------------------------
// Public API: aggregate sub-selects (withCount / withMax / withSum / ...)
//
// Each method appends a selectSub to conditions.selectSubs; the actual sub-select column is
// emitted when the query is compiled to SQL (so the parent model can be resolved and the alias derived).
// ---------------------------------------------------------------------------

// WithAggregate adds a sub-select to include an aggregate value for a relationship.
// fn must be one of: count, max, min, sum, avg, exists. column is a column name, a
// table-qualified name, "*", or a raw expression (database/query's Raw / Expression) - the
// string|Expression union Laravel's withAggregate accepts. relation may carry an "as" rename
// ("foo as foo_bar"): the rename becomes the select alias verbatim, otherwise the alias is the
// Str::snake derivation Laravel computes from name, function and column.
func (r *OrmBuilder) WithAggregate(relation string, column any, fn string, args ...any) contractsorm.Query {
	if relation == "" {
		return r
	}
	if !validAggregateFn(fn) {
		return r.withError(errors.OrmRelationInvalidAggregate.Args(fn))
	}
	columnValue, ok := aggregateColumnValue(column)
	if !ok {
		return r.withError(errors.OrmRelationInvalidArgument.Args(column))
	}
	cb, _, _, err := parseRelationArgs(args)
	if err != nil {
		return r.withError(err)
	}
	name, alias := parseAggregateRelationName(relation)
	if alias == "" {
		alias = aggregateAlias(name, fn, columnValue)
	}
	conditions := r.conditions
	conditions.selectSubs = deep.Append(conditions.selectSubs, selectSub{
		relation: name,
		column:   column,
		function: fn,
		alias:    alias,
		callback: cb,
	})
	return r.setConditions(conditions)
}

// WithCount adds sub-select queries to count the relations. Each entry may be either:
//   - a string ("Books", or "Books as book_total" for a rename) - emits `(SELECT COUNT(*) FROM ...) as books_count`
//   - a contractsorm.RelationCount struct for scoped counts and/or custom alias
func (r *OrmBuilder) WithCount(relations ...any) contractsorm.Query {
	current := r
	for _, raw := range relations {
		switch v := raw.(type) {
		case string:
			next, ok := current.WithAggregate(v, "*", "count").(*OrmBuilder)
			if !ok {
				return current
			}
			current = next
		case contractsorm.RelationCount:
			args := []any{}
			if v.Callback != nil {
				args = append(args, v.Callback)
			}
			next, ok := current.WithAggregate(v.Name, "*", "count", args...).(*OrmBuilder)
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
func (r *OrmBuilder) WithMax(relation string, column any, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "max", args...)
}

// WithMin adds sub-select queries to include the min of the relation's column.
func (r *OrmBuilder) WithMin(relation string, column any, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "min", args...)
}

// WithSum adds sub-select queries to include the sum of the relation's column.
func (r *OrmBuilder) WithSum(relation string, column any, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "sum", args...)
}

// WithAvg adds sub-select queries to include the average of the relation's column.
func (r *OrmBuilder) WithAvg(relation string, column any, args ...any) contractsorm.Query {
	return r.WithAggregate(relation, column, "avg", args...)
}

// WithExists adds sub-select queries to include the existence of related models -
// withExists($relation), which is withAggregate($relation, '*', 'exists'). The column is
// existsSelectExpression's: Laravel's `exists(select * ...) as alias` (0/1 on MySQL and SQLite, a
// native boolean on PostgreSQL), folded into a CASE 0/1 on SQL Server. Laravel casts the alias to
// bool, so a `bool` dest field is the one that reads it on every driver; an integer field cannot
// take PostgreSQL's boolean.
func (r *OrmBuilder) WithExists(relations ...string) contractsorm.Query {
	current := r
	for _, rel := range relations {
		next, ok := current.WithAggregate(rel, "*", "exists").(*OrmBuilder)
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
func (r *OrmBuilder) With(args ...any) contractsorm.Query {
	entries, err := relations.ParseEagerLoad(args)
	if err != nil {
		return r.withError(err)
	}
	conditions := r.conditions
	for _, e := range entries {
		conditions.eagerLoad = relations.MergeEagerLoadEntry(conditions.eagerLoad, e)
	}
	return r.setConditions(conditions)
}

// Without removes the named relations from the eager-load list. Mirrors fedaco's
// without(). Names must match exactly (including dot-paths, e.g. "Books.Author").
func (r *OrmBuilder) Without(relations ...string) contractsorm.Query {
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
		if _, omit := drop[e.Relation]; omit {
			continue
		}
		filtered = append(filtered, e)
	}
	conditions.eagerLoad = filtered
	return r.setConditions(conditions)
}

// WithOnly clears the eager-load list, then adds the given relations. Mirrors fedaco's
// withOnly(). Useful when a default-scoped query has eager loads you want to override.
func (r *OrmBuilder) WithOnly(args ...any) contractsorm.Query {
	conditions := r.conditions
	conditions.eagerLoad = nil
	return r.setConditions(conditions).With(args...)
}

// ---------------------------------------------------------------------------
// Internal: queueing
// ---------------------------------------------------------------------------

func (r *OrmBuilder) queueRelationExistence(relation string, args []any, conjunction string, doesntHave bool) contractsorm.Query {
	cb, op, count, err := parseRelationArgs(args)
	if err != nil {
		return r.withError(err)
	}
	if doesntHave {
		op = "<"
		count = 1
	}

	// Laravel appends the exists clause to the builder's wheres at call time, so the clause sits
	// between the wheres written before and after the call. The clause itself is rendered at compile
	// time, when the parent model is known, so its position is recorded here: the earlier wheres are
	// flushed now and the builder's counts become the splice points applyRelations uses.
	r = r.flushWheres()
	whereIndex := len(r.query.State().Wheres)
	bindingIndex := len(r.query.State().Bindings["where"])

	conditions := r.conditions
	conditions.relations = deep.Append(conditions.relations, relationExistence{
		relation:     relation,
		operator:     op,
		count:        count,
		conjunction:  conjunction,
		callback:     cb,
		whereIndex:   whereIndex,
		bindingIndex: bindingIndex,
	})
	return r.setConditions(conditions)
}

func (r *OrmBuilder) queueMorphExistence(relation string, types []any, args []any, conjunction string, doesntHave bool) contractsorm.Query {
	if len(types) == 0 {
		return r.withError(errors.OrmRelationMorphTypesEmpty)
	}

	// A lone "*" is Laravel's auto-discovery marker: hasMorph compares `$types === ['*']` and
	// reads the morph values from the parent table itself. The marker is consumed here; the values
	// are resolved when the clause compiles.
	morphAuto := false
	if marker, ok := types[0].(string); ok && marker == "*" && len(types) == 1 {
		morphAuto = true
		types = nil
	}

	cb, mcb, op, count, err := parseMorphRelationArgs(args)
	if err != nil {
		return r.withError(err)
	}
	if doesntHave {
		op = "<"
		count = 1
	}

	// Same position recording as queueRelationExistence: the morph clause is appended to the
	// statement's wheres at call time in Laravel, so the earlier wheres flush here and the current
	// counts are remembered as the splice points.
	r = r.flushWheres()
	whereIndex := len(r.query.State().Wheres)
	bindingIndex := len(r.query.State().Bindings["where"])

	conditions := r.conditions
	conditions.relations = deep.Append(conditions.relations, relationExistence{
		relation:      relation,
		operator:      op,
		count:         count,
		conjunction:   conjunction,
		callback:      cb,
		morphTypes:    types,
		morphCallback: mcb,
		morphAuto:     morphAuto,
		whereIndex:    whereIndex,
		bindingIndex:  bindingIndex,
	})
	return r.setConditions(conditions)
}

// ---------------------------------------------------------------------------
// Internal: existence subquery construction
// ---------------------------------------------------------------------------

// compileExistenceSubquery builds the correlated inner SELECT for a relation as a framework
// builder. Callers render it with ToSelectSQL and inline the result, so no GORM statement carries
// the subquery. The builder is nil when the relation kind is unsupported. The second return value
// is the self-join alias when the relation reads the parent's own table (Laravel's
// laravel_reserved_N), empty otherwise: the aggregate path qualifies its column with it, the way
// getRelationHashedColumn reuses getRelationCountHash(false).
//
// The operator/count belong to the final segment of a dotted chain (Laravel's hasNested hands them
// to the last has() call); this entry point leaves them at the defaults the aggregate paths and the
// direct callers expect.
func (r *OrmBuilder) compileExistenceSubquery(desc *relationDescriptor, callback contractsorm.RelationCallback) (*querybuilder.Builder, string, error) {
	return r.compileExistenceSubqueryIn(desc, callback, desc.ParentTable, desc.ParentTable, ">=", 1)
}

// compileExistenceSubqueryIn renders one existence level with the enclosing query's table identity
// threaded in: parentFrom is the enclosing statement's from string ("posts", or
// "posts as laravel_reserved_0" once that level aliased itself) and parentQualifier the table name
// the correlation's parent side binds to. Laravel's self branches compare the related query's from
// against the parent query's from string (HasOneOrMany::getRelationExistenceQuery and
// BelongsToMany::getRelationExistenceQuery both do), so only a level whose table equals the
// enclosing from string is aliased - a nested level under an already-aliased parent compares
// against "table as hash" and keeps the plain name, correlating back to the enclosing alias.
//
// leafOperator/leafCount travel with the recursion and land on the final segment's clause, the way
// hasNested hands the caller's operator/count to the last has() call; every segment before the last
// stays a plain exists.
func (r *OrmBuilder) compileExistenceSubqueryIn(desc *relationDescriptor, callback contractsorm.RelationCallback, parentFrom, parentQualifier, leafOperator string, leafCount int) (*querybuilder.Builder, string, error) {
	builder := r.subqueryBuilder(desc.RelatedTable)

	// A relation reading the parent's own table aliases the inner copy of the table, so the
	// correlation does not bind the subquery's table to itself. HasOneOrMany and BelongsToMany
	// branch on `$query->from == $parentQuery->from` and take the alias from
	// Relation::$selfJoinCount; the outer references keep the plain table name. Through relations
	// keep Laravel's through-specific self handling, which is not ported, so they never alias.
	selfAlias := ""
	relatedQualifier := desc.RelatedTable
	if existenceLevelAliases(desc.Kind) && desc.ParentTable != "" && desc.RelatedTable == parentFrom {
		selfAlias = nextSelfJoinAlias()
		relatedQualifier = selfAlias
		builder.From(desc.RelatedTable, selfAlias)
	}
	fromString := desc.RelatedTable
	if selfAlias != "" {
		fromString = desc.RelatedTable + " as " + selfAlias
	}
	if parentQualifier == "" {
		parentQualifier = desc.ParentTable
	}

	switch desc.Kind {
	case relKindHasOne, relKindHasMany, relKindMorphOne, relKindMorphMany:
		for _, ref := range desc.References {
			builder.WhereRaw(r.ident(parentQualifier, ref.PrimaryColumn)+" = "+r.ident(relatedQualifier, ref.ForeignColumn), nil, core.BooleanAnd)
		}
		if desc.Kind == relKindMorphOne || desc.Kind == relKindMorphMany {
			builder.WhereRaw(r.ident(relatedQualifier, desc.MorphTypeColumn)+" = ?", []any{desc.MorphValue}, core.BooleanAnd)
		}
	case relKindBelongsTo:
		// BelongsTo writes the parent's foreign key first ("parent"."foo_id" = "close"."id"); the
		// self form flips to the aliased owner key first, the order
		// BelongsTo::getRelationExistenceQueryForSelfRelation pins.
		for _, ref := range desc.References {
			if selfAlias != "" {
				builder.WhereRaw(r.ident(relatedQualifier, ref.PrimaryColumn)+" = "+r.ident(parentQualifier, ref.ForeignColumn), nil, core.BooleanAnd)
			} else {
				builder.WhereRaw(r.ident(parentQualifier, ref.ForeignColumn)+" = "+r.ident(relatedQualifier, ref.PrimaryColumn), nil, core.BooleanAnd)
			}
		}
	case relKindMany2Many, relKindMorphToMany:
		// The join reads the related table first, the operand order the oracle emits for the
		// relation existence join (`on "far"."id" = "user_role"."related_id"`).
		builder.JoinRaw(r.tableRef(desc.PivotTable)+" on "+
			r.ident(relatedQualifier, desc.PivotRelatedRef.PrimaryColumn)+" = "+
			r.ident(desc.PivotTable, desc.PivotRelatedRef.ForeignColumn), nil, "inner")
		builder.WhereRaw(r.ident(parentQualifier, desc.PivotParentRef.PrimaryColumn)+" = "+
			r.ident(desc.PivotTable, desc.PivotParentRef.ForeignColumn), nil, core.BooleanAnd)
		if desc.Kind == relKindMorphToMany {
			builder.WhereRaw(r.ident(desc.PivotTable, desc.MorphTypeColumn)+" = ?", []any{desc.MorphValue}, core.BooleanAnd)
		}
	case relKindHasOneThrough, relKindHasManyThrough:
		// The oracle renders the through side first ("rel_users"."id" = "rel_posts"."rel_user_id"),
		// the same order the through read join wears.
		builder.JoinRaw(r.tableRef(desc.ThroughTable)+" on "+
			r.ident(desc.ThroughTable, desc.SecondLocalKey)+" = "+
			r.ident(relatedQualifier, desc.SecondKey), nil, "inner")
		builder.WhereRaw(r.ident(parentQualifier, desc.LocalKey)+" = "+
			r.ident(desc.ThroughTable, desc.FirstKey), nil, core.BooleanAnd)
	default:
		return nil, "", errors.OrmRelationUnsupported.Args(desc.Name, desc.ParentTable, fmt.Sprintf("kind=%d", desc.Kind))
	}

	// The relation's default scope contributes its one-of-many JOIN at join position and its where
	// clauses after the caller's closure: Laravel merges the join inside getRelationExistenceQuery
	// but mergeConstraintsFrom appends the relation's own wheres after callScope has run the
	// closure, so the closure's clauses bind first.
	relationScoped, joinSQL, joinArgs, err := r.relationScopeQuery(desc, relatedQualifier, desc.RelatedModel)
	if err != nil {
		return nil, "", err
	}
	if joinSQL != "" {
		builder.JoinRaw(joinSQL, joinArgs, "inner")
	}
	// The caller's closure binds at the leaf, the way hasNested hands it to the final has() call; an
	// intermediate level of a chained relation contributes the nested relation's clause instead.
	// Both sit in the callScope position Laravel gives them: hasNested runs the chain through the
	// closure callScope applies before addHasWhere merges the relation constraints and before
	// toBase applies the related model's scopes, so the nested clause leads the intermediate
	// model's own wheres, its global scopes and its soft-delete predicate. Like the closure, the
	// caller's operator/count ride down to the final segment: hasNested builds every earlier
	// segment as a plain whereHas, so only the leaf clause carries the count comparison.
	var callbackScoped *OrmBuilder
	if desc.Nested == nil {
		callbackScoped, err = r.applyRelationScope(builder, callback, relatedQualifier, desc.RelatedModel)
		if err != nil {
			return nil, "", err
		}
	} else {
		nested, _, err := r.compileExistenceSubqueryIn(desc.Nested, callback, fromString, relatedQualifier, leafOperator, leafCount)
		if err != nil {
			return nil, "", err
		}
		if nested != nil {
			operator, count := ">=", 1
			if desc.Nested.Nested == nil {
				operator, count = leafOperator, leafCount
			}
			applyExistenceClause(builder, nested, operator, count, core.BooleanAnd)
		}
	}
	if relationScoped != nil {
		appendWhereClauses(builder, relationScoped.query)
	}

	// The removals the relation's default query and the caller's closure recorded land on one
	// subquery builder, the way Laravel merges the relation's removedScopes into the closure's
	// builder, so both govern the related model's scopes inside the exists body.
	removals := scopeRemovalsOf(relationScoped).merge(scopeRemovalsOf(callbackScoped))

	// Laravel applies the related model's deferred global scopes in registration order: the
	// SoftDeletingScope first (the SoftDeletes trait boots before the model's own scopes), then each
	// user scope - the order the oracle pins (/tmp/r13/scope_removal_probe5.php G1:
	// `... and "sr_posts"."deleted_at" is null and "published" = ?`).
	r.applySubquerySoftDelete(builder, relatedQualifier, desc.RelatedModel, removals)
	r.applyRelatedGlobalScopes(builder, relatedQualifier, desc.RelatedModel, removals)

	// A soft-deletable through model filters the through table inside the existence body too, after
	// the related predicate - performJoin adds its named scope to the existence subquery (oracle,
	// /tmp/r10/through_softdelete_probe.php N12/N13). A no-op for every non-through kind.
	r.applySubqueryThroughSoftDelete(builder, desc.ThroughTable, desc.ThroughModel, removals)

	// addHasWhere merges the relation query's own constraints - the wherePivot scopes - into the
	// existence query after the caller's closure ran, so the pivot filter binds last, after a
	// nested level's exists. Only pivot-scoped relations carry the callback.
	if desc.OnPivotQuery != nil {
		scoped := r.fresh()
		if returned, ok := applyOnPivotQueryToQuery(r.queryGrammar, scoped, desc).(*OrmBuilder); ok {
			scoped = returned
		}
		appendWhereClauses(builder, scoped.flushWheres().query)
	}

	// The body selects `*`, the shape Relation::getRelationExistenceQuery's `$columns = ['*']`
	// default renders; the aggregate paths overwrite the select with their own expression.
	return builder, selfAlias, nil
}

// existenceLevelAliases reports whether a relation kind takes Laravel's self-join alias when it
// reads the parent's own table: the HasOneOrMany family, BelongsTo and the many-to-many family do;
// the through family has its own self handling (aliasing the through table in one of its two
// cases) which this port does not model, so those levels keep the plain table name.
func existenceLevelAliases(kind relationKind) bool {
	switch kind {
	case relKindHasOne, relKindHasMany, relKindBelongsTo, relKindMorphOne, relKindMorphMany, relKindMany2Many, relKindMorphToMany:
		return true
	}

	return false
}

// compileMorphExistenceSubquery is the morph-specific variant. The morph_type column is pinned on the
// related table, inside the subquery, and the type comparison the caller needs lives in the subquery.
func (r *OrmBuilder) compileMorphExistenceSubquery(desc *relationDescriptor, morphValue string, callback contractsorm.RelationCallback) *querybuilder.Builder {
	builder := r.subqueryBuilder(desc.RelatedTable)
	for _, ref := range desc.References {
		builder.WhereRaw(r.ident(ref.PrimaryTable, ref.PrimaryColumn)+" = "+r.ident(ref.ForeignTable, ref.ForeignColumn), nil, core.BooleanAnd)
	}
	builder.WhereRaw(r.ident(desc.RelatedTable, desc.MorphTypeColumn)+" = ?", []any{morphValue}, core.BooleanAnd)
	// This variant reports no error of its own, so a nested clause a callback could not render is
	// carried on the query (conditions.err) for the statement to report.
	relationScoped, joinSQL, joinArgs, err := r.relationScopeQuery(desc, desc.RelatedTable, desc.RelatedModel)
	r.keepFirstError(err)
	if joinSQL != "" {
		builder.JoinRaw(joinSQL, joinArgs, "inner")
	}
	callbackScoped, err := r.applyRelationScope(builder, callback, desc.RelatedTable, desc.RelatedModel)
	r.keepFirstError(err)
	if relationScoped != nil {
		appendWhereClauses(builder, relationScoped.query)
	}
	removals := scopeRemovalsOf(relationScoped).merge(scopeRemovalsOf(callbackScoped))
	r.applySubquerySoftDelete(builder, desc.RelatedTable, desc.RelatedModel, removals)
	r.applyRelatedGlobalScopes(builder, desc.RelatedTable, desc.RelatedModel, removals)

	return builder
}

// compileAggregateSubquery returns the framework builder for the inner SELECT of an aggregate. The
// select expression is withAggregate's (QueriesRelationships.php:876-887, vendor v12.55.1): the
// aggregate column - an Expression's raw value, or the wrapped column qualified with the related
// table ("*" kept bare) - inside the function call, and the bare aggregate column for exists:
//
//	$expression = $function === 'exists' ? $aggregateColumn : sprintf('%s(%s)', $function, $aggregateColumn);
//
// so withExists('foo') reads `exists(select * from ...)`.
func (r *OrmBuilder) compileAggregateSubquery(desc *relationDescriptor, sub selectSub) (*querybuilder.Builder, error) {
	inner, selfAlias, err := r.compileExistenceSubquery(desc, sub.callback)
	if err != nil {
		return nil, err
	}
	if inner == nil {
		return nil, nil
	}

	columnValue, _ := aggregateColumnValue(sub.column)

	var aggregateColumn string
	if core.IsExpression(sub.column) {
		// An Expression column is spliced verbatim: getValue($column).
		aggregateColumn = columnValue
	} else {
		aggregateColumn = "*"
		switch {
		case columnValue == "*" || columnValue == "":
			// The default "*" stands.
		case strings.Contains(columnValue, "."):
			// A qualified column is wrapped as-is; qualifyColumn leaves a dotted name alone, so a
			// pivot column like "user_role.price" keeps its own table.
			aggregateColumn = r.queryGrammar.Wrap(columnValue)
		case selfAlias != "":
			// The self-join's column wears the subquery alias, the way getRelationHashedColumn
			// prefixes getRelationCountHash(false) for a same-table column.
			aggregateColumn = r.ident(selfAlias, columnValue)
		default:
			aggregateColumn = r.ident(desc.RelatedTable, columnValue)
		}
	}

	selectExpr := aggregateColumn
	if sub.function != "exists" {
		selectExpr = fmt.Sprintf("%s(%s)", sub.function, aggregateColumn)
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
func (r *OrmBuilder) applyRelations(builder *querybuilder.Builder) error {
	if len(r.conditions.relations) == 0 {
		return nil
	}

	parent := r.parentModel()

	// Every clause is rendered at the tail; relocateAppendedClause then moves it back to the where
	// position its call recorded, so the statement preserves Laravel's call order (a has() clause
	// sits between the wheres written before and after it). The counters shift the recorded positions
	// and binding offsets by what the clauses spliced before them added.
	splicedWheres := 0
	splicedBindings := 0

	for _, item := range r.conditions.relations {
		boolean := core.BooleanAnd
		if item.conjunction == "or" {
			boolean = core.BooleanOr
		}

		whereStart := len(builder.State().Wheres)
		bindingStart := len(builder.State().Bindings["where"])

		if item.nestedGroup != nil {
			// The group keeps the where call's own boolean, "and not" / "or not" included
			// (WhereNot / OrWhereNot closures).
			if err := applyNestedGroup(builder, parent, item.nestedGroup, item.conjunction); err != nil {
				return err
			}
		} else if parent == nil {
			return errors.OrmQueryEmptyRelation
		} else if item.morphAuto || len(item.morphTypes) > 0 {
			if err := r.applyMorphRelation(builder, parent, item, boolean); err != nil {
				return err
			}
		} else {
			desc, err := relations.ResolveRelation(parent, item.relation)
			if err != nil {
				return err
			}

			if desc.Kind == relKindMorphTo {
				// Laravel's has() on a MorphTo delegates to hasMorph($relation, ['*'], ...): the
				// morph values are read from the parent table itself rather than from model
				// instances, so the clause resolves its types through the auto path.
				item.morphAuto = true
				if err := r.applyMorphRelation(builder, parent, item, boolean); err != nil {
					return err
				}
			} else {
				// hasNested always renders the first segment as a plain exists - not exists for the
				// doesntHave shape (operator "<", count 1) - and hands the caller's operator/count to
				// the final segment, so a dotted chain keeps its count comparison on the leaf.
				leafOperator, leafCount := item.operator, item.count
				outerOperator, outerCount := item.operator, item.count
				if desc.Nested != nil {
					outerOperator, outerCount = ">=", 1
					if item.operator == "<" && item.count == 1 {
						outerOperator = "<"
						leafOperator, leafCount = ">=", 1
					}
				}

				inner, _, err := r.compileExistenceSubqueryIn(desc, item.callback, desc.ParentTable, desc.ParentTable, leafOperator, leafCount)
				if err != nil {
					return err
				}
				if inner == nil {
					continue
				}

				applyExistenceClause(builder, inner, outerOperator, outerCount, boolean)
			}
		}

		appendedWheres := len(builder.State().Wheres) - whereStart
		appendedBindings := len(builder.State().Bindings["where"]) - bindingStart

		relocateAppendedClause(builder, whereStart, bindingStart, item.whereIndex+splicedWheres, item.bindingIndex+splicedBindings)

		splicedWheres += appendedWheres
		splicedBindings += appendedBindings
	}

	return nil
}

// applyNestedGroup finishes a deferred where-closure group and appends it as one nested where: the
// closure's onlyTrashed predicates and existence clauses are written into a copy of its where list
// at the positions their calls recorded - the predicates first, since they shift the clauses'
// positions, as withScopesApplied orders them on the main path - and the group joins the statement
// through AddNestedWhereQuery, its bindings with it. The copy keeps the recorded group untouched, so
// compiling the same query twice writes the clauses once each time. A closure nested in the closure
// is itself a deferred group of the copy, finished by the same recursion.
//
// Oracle (illuminate/database v12.55.1, real sqlite): /Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle.php W1-W8, V4, V5 -
// where(fn => whereHas('posts')) compiles `where "id" = ? and (exists (select * from "sd_posts"
// where ...)) and "sd_users"."deleted_at" is null` - and
// /Users/dog/.claude/jobs/013c27a0/tmp/r15-probes/r15-r14-mutants/probe5.out N1, N2, N4 -
// where(fn => onlyTrashed()->where('email', 'a')) compiles `("sr_users"."deleted_at" is not null and
// "email" = ?)`.
func applyNestedGroup(builder *querybuilder.Builder, parent any, nested *OrmBuilder, boolean string) error {
	group := nested.clone()
	if parent != nil {
		group.conditions.model = parent
	}

	if group.conditions.onlyTrashed {
		if err := group.spliceOnlyTrashed(group.query); err != nil {
			return err
		}
	}

	if err := group.applyRelations(group.query); err != nil {
		return err
	}

	builder.AddNestedWhereQuery(group.query, boolean)

	return nil
}

// relocateAppendedClause moves the clauses a relation existence condition appended at the tail back
// to the positions the condition recorded when its method was called, restoring Laravel's
// call-order placement for a clause whose rendering is deferred to compile time. The move is a pure
// re-slice of the where list and of its binding list: the bindings appended with the clause travel
// to the parallel offset, so the statement's placeholders keep matching their values.
func relocateAppendedClause(builder *querybuilder.Builder, whereStart, bindingStart, whereTarget, bindingTarget int) {
	state := builder.State()

	if moved := state.Wheres[whereStart:]; len(moved) > 0 && whereTarget < whereStart {
		wheres := make([]core.Where, 0, len(state.Wheres))
		wheres = append(wheres, state.Wheres[:whereTarget]...)
		wheres = append(wheres, moved...)
		wheres = append(wheres, state.Wheres[whereTarget:whereStart]...)
		state.Wheres = wheres
	}

	bindings := state.Bindings["where"]
	if moved := bindings[bindingStart:]; len(moved) > 0 && bindingTarget < bindingStart {
		relocated := make([]any, 0, len(bindings))
		relocated = append(relocated, bindings[:bindingTarget]...)
		relocated = append(relocated, moved...)
		relocated = append(relocated, bindings[bindingTarget:bindingStart]...)
		state.Bindings["where"] = relocated
	}
}

// applyMorphRelation adds a polymorphic existence condition. Each requested type is a table of its
// own, so the types contribute one alternative each to a single group, which the condition's
// conjunction then attaches to the query: every type after the first is joined with "or", the shape
// Laravel's hasMorph renders with one orWhere closure per type.
//
// A `['*']` condition (morphAuto) resolves its types first — hasMorph reads the parent table's
// distinct morph-type values at call time, keeping the non-empty ones — and reports a relation kind
// other than MorphTo as unsupported, because Eloquent's auto-discovery builds per-type belongs-to
// queries, which only a MorphTo can feed.
func (r *OrmBuilder) applyMorphRelation(builder *querybuilder.Builder, parent any, item relationExistence, boolean string) error {
	desc, err := relations.ResolveRelation(parent, item.relation)
	if err != nil {
		return err
	}

	if item.morphAuto {
		return r.applyAutoMorphRelation(builder, parent, desc, item, boolean)
	}

	switch desc.Kind {
	case relKindMorphOne, relKindMorphMany, relKindMorphTo:
	default:
		return errors.OrmRelationUnsupported.Args(item.relation, fmt.Sprintf("%T", parent), "morph (must be polymorphic relation)")
	}

	// Strict morph mode pre-flight: Laravel's hasMorph resolves every requested type with
	// (new $type)->getMorphClass() while the per-type orWhere closures are built — the where()
	// closure runs synchronously (QueriesRelationships.php:289) — so an unmapped type raises
	// ClassMorphViolationException at call time. The clause builder below runs inside WhereNested,
	// which cannot return an error, and its per-type skip stays the lax GORM-path parity for
	// types whose table does not resolve; the strict failure is raised here, where it can leave
	// the function. While strict mode is off resolveMorphValue never fails and this loop changes
	// nothing.
	for _, typeModel := range item.morphTypes {
		if relatedTable, err := tableNameFor(typeModel); err == nil {
			if _, err := relations.ResolveMorphValue(typeModel, relatedTable); err != nil {
				return err
			}
		}
	}

	builder.WhereNested(func(group *querybuilder.Builder) *querybuilder.Builder {
		r.addMorphAlternatives(group, desc, item, item.morphTypes)

		return group
	}, boolean)

	return nil
}

// applyAutoMorphRelation adds the `types = ['*']` form: the morph values are read from the parent
// table itself instead of coming from model instances. Every value resolves through the morph map —
// the port has no class strings to instantiate — and a value the map does not know is reported with
// OrmMorphTypeUnknown, where Laravel would fail constructing the class the stored value names.
func (r *OrmBuilder) applyAutoMorphRelation(builder *querybuilder.Builder, parent any, desc *relationDescriptor, item relationExistence, boolean string) error {
	if desc.Kind != relKindMorphTo {
		return errors.OrmRelationUnsupported.Args(item.relation, fmt.Sprintf("%T", parent), `auto morph discovery ("*") requires a MorphTo relation`)
	}

	aliases, err := r.distinctMorphTypes(parent, desc)
	if err != nil {
		return err
	}

	// An empty type list degenerates to `where 0 <operator> <count>` on the outer query — the
	// hasMorph branch that keeps has() over a table with no morph values from matching anything
	// (oracle: `select * from "photos" where 0 >= ?`, binding 1).
	if len(aliases) == 0 {
		applySimpleWhere(builder, querybuilder.Expression{Value: "0"}, []any{item.operator, item.count}, boolean)

		return nil
	}

	samples := make([]any, 0, len(aliases))
	for _, alias := range aliases {
		sample := morphmap.Find(alias)
		if sample == nil {
			return errors.OrmMorphTypeUnknown.Args(alias)
		}

		samples = append(samples, sample)
	}

	builder.WhereNested(func(group *querybuilder.Builder) *querybuilder.Builder {
		r.addMorphAlternatives(group, desc, item, samples)

		// The negated forms carry Laravel's null-morph alternative: without it a row that stores no
		// morph type at all could never match a "not having the relation" condition (oracle:
		// `or "photos"."imageable_type" is null`).
		if morphNullApplies(item.operator, item.count) {
			// The column is written the way a caller writes one — unquoted, table-qualified — because
			// the compiler quotes each segment itself; an already-quoted name would be quoted again.
			group.WhereNull(desc.ParentTable+"."+desc.MorphTypeColumn, core.BooleanOr, false)
		}

		return group
	}, boolean)

	return nil
}

// addMorphAlternatives adds one set of clauses per type to the group, joining every type after the
// first with an "or" conjunction. A type the lax path cannot resolve contributes nothing rather
// than failing the query, which is what the GORM path does: it skips the type and keeps the
// others. An unmapped model under strict mode never reaches this skip — applyMorphRelation's
// pre-flight has already raised the class violation.
func (r *OrmBuilder) addMorphAlternatives(group *querybuilder.Builder, desc *relationDescriptor, item relationExistence, types []any) {
	first := true
	for _, typeModel := range types {
		typeBoolean := core.BooleanOr
		if first {
			typeBoolean = core.BooleanAnd
		}

		if err := r.applyMorphTypeClause(group, desc, typeModel, item, typeBoolean); err != nil {
			continue
		}

		first = false
	}
}

// morphNullApplies mirrors hasMorph's `$checkMorphNull` for the auto-discovery form: the negated
// comparisons are the ones that add `or <morph type> is null`.
func morphNullApplies(operator string, count int) bool {
	switch operator {
	case "<":
		return count >= 1
	case "<=":
		return count >= 0
	case "=":
		return count == 0
	case "!=":
		return count >= 1
	}

	return false
}

// distinctMorphTypes reads the MorphTo relation's distinct morph-type values from the parent table,
// the way hasMorph(['*']) plucks `$this->model->newModelQuery()->distinct()->pluck($type)` and drops
// the empty values. The read runs on the receiver's connection while the clause is compiled, and it
// is deliberately unscoped: newModelQuery registers no global scopes and no soft-delete filter, so
// trashed or scope-excluded parents still contribute their types, exactly as they do in Laravel.
func (r *OrmBuilder) distinctMorphTypes(parent any, desc *relationDescriptor) ([]string, error) {
	values := []any{}
	read := r.relatedQuery(parent).WithoutGlobalScopes().WithTrashed().Distinct(desc.MorphTypeColumn)
	if err := read.Pluck(desc.MorphTypeColumn, &values); err != nil {
		return nil, err
	}

	aliases := make([]string, 0, len(values))
	for _, value := range values {
		if alias := morphTypeToString(value); alias != "" {
			aliases = append(aliases, alias)
		}
	}

	return aliases, nil
}

// applyMorphTypeClause adds the clauses one requested type contributes to the group, joined to the
// earlier alternatives with typeBoolean.
//
// The type is pinned inside the subquery for the outbound kinds, whose morph column lives on the
// related table. For the inverse kind the pair lives in a group of its own — Laravel wraps each type
// in an orWhere closure — so it reads `(<type> = ? and exists (...))`, the shape the oracle pins for
// `has('imageable')` over photos.
func (r *OrmBuilder) applyMorphTypeClause(builder *querybuilder.Builder, desc *relationDescriptor, typeModel any, item relationExistence, typeBoolean string) error {
	relatedTable, err := tableNameFor(typeModel)
	if err != nil {
		return err
	}

	morphValue, err := relations.ResolveMorphValue(typeModel, relatedTable)
	if err != nil {
		return err
	}

	callback := item.callback
	if item.morphCallback != nil {
		morphCallback := item.morphCallback
		callback = func(query contractsorm.Query) contractsorm.Query {
			return morphCallback(query, morphValue)
		}
	}

	if desc.Kind == relKindMorphTo {
		inner := r.compileMorphToExistenceSubquery(desc, relatedTable, typeModel, callback)

		builder.WhereNested(func(pair *querybuilder.Builder) *querybuilder.Builder {
			pair.WhereRaw(r.ident(desc.ParentTable, desc.MorphTypeColumn)+" = ?", []any{morphValue}, core.BooleanAnd)

			applyExistenceClause(pair, inner, item.operator, item.count, core.BooleanAnd)

			return pair
		}, typeBoolean)

		return nil
	}

	inner := r.compileMorphExistenceSubquery(desc, morphValue, callback)

	applyExistenceClause(builder, inner, item.operator, item.count, typeBoolean)

	return nil
}

// compileMorphToExistenceSubquery builds the belongs-to-shaped inner query an inverse polymorphic
// condition correlates against one of its types: the parent's morph id against that type's own key,
// the operand order Laravel's per-type BelongsTo writes.
//
// The type is one of the morph map's models, so each type decides its own soft-delete predicate: a
// type still declaring gorm.DeletedAt is left to GORM, exactly as on every other compiled path, while
// a type carrying the framework's own soft-delete type gets its "not deleted" predicate inside the
// subquery - the same per-type treatment Laravel's SoftDeletingScope gives the BelongsTo it builds.
func (r *OrmBuilder) compileMorphToExistenceSubquery(desc *relationDescriptor, relatedTable string, relatedModel any, callback contractsorm.RelationCallback) *querybuilder.Builder {
	ownerKey := desc.MorphOwnerKey
	if ownerKey == "" {
		ownerKey = "id"
	}

	builder := r.subqueryBuilder(relatedTable)
	// The parent's morph id leads the correlation (oracle: `exists (select * from "posts" where
	// "photos"."imageable_id" = "posts"."id")`).
	builder.WhereRaw(r.ident(desc.ParentTable, desc.MorphIDColumn)+" = "+r.ident(relatedTable, ownerKey), nil, core.BooleanAnd)

	// As compileMorphExistenceSubquery: a nested clause a callback could not render is carried on
	// the query (conditions.err).
	relationScoped, joinSQL, joinArgs, err := r.relationScopeQuery(desc, relatedTable, relatedModel)
	r.keepFirstError(err)
	if joinSQL != "" {
		builder.JoinRaw(joinSQL, joinArgs, "inner")
	}
	callbackScoped, err := r.applyRelationScope(builder, callback, relatedTable, relatedModel)
	r.keepFirstError(err)
	if relationScoped != nil {
		appendWhereClauses(builder, relationScoped.query)
	}
	removals := scopeRemovalsOf(relationScoped).merge(scopeRemovalsOf(callbackScoped))
	r.applySubquerySoftDelete(builder, relatedTable, relatedModel, removals)
	r.applyRelatedGlobalScopes(builder, relatedTable, relatedModel, removals)

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
	_ = builder.AddBinding(bindings, "where")
	// The count is inlined as a raw expression, the way Laravel's addWhereCountQuery writes
	// new Expression($count) for a numeric count, and the comparison goes through the builder's
	// where path so the operator is validated: an unknown operator is swapped for "=" and bound as
	// the value, discarding the count, the handling Builder::where() gives both. Concatenating the
	// comparison put a caller string into the statement verbatim.
	applySimpleWhere(builder, querybuilder.Expression{Value: "(" + subquery + ")"},
		[]any{operator, querybuilder.Expression{Value: strconv.Itoa(count)}}, boolean)
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
// values. It is the shape every eager loader builds per chunk: the constraint goes straight onto the
// child's builder, the way Eloquent's addEagerConstraints writes it, so it keeps its place ahead of
// anything a relation scope or callback adds.
//
// The column is named the way a caller writes one — unquoted, with a dot separating a table from its
// column — because the compiler quotes each segment. A name that arrived already quoted would be
// quoted again and the statement would not run.
func (r *OrmBuilder) relatedChild(table string, column string, values []any) *OrmBuilder {
	// The child starts from no conditions of its own, as the fresh session it replaced did. Inheriting
	// the receiver's would carry the parent query's where clause into the child and narrow it to the
	// rows the parent matched.
	child := r.fresh()
	child.query.From(table, "")

	child.query.WhereIn(column, values, core.BooleanAnd, false)

	return child
}

// subqueryScopeRemovals carries the global-scope-removal state a relation's default query (OnQuery)
// and the caller's existence/count closure leave on the subquery builder, so the related model's
// soft-delete predicate and its remaining global scopes honour it the way Laravel's subquery builder
// does. Laravel runs the closure on the subquery (Builder::has -> callScope) and merges the relation's
// own removals (addHasWhere -> mergeConstraintsFrom -> withoutGlobalScopes($from->removedScopes())),
// so both sets land on one builder and every deferred scope is applied on top of them: a name (or the
// port's "*" for withoutGlobalScopes()) drops exactly that scope, and withTrashed() and onlyTrashed()
// drop the SoftDeletingScope - onlyTrashed's own "is not null" is a where its call wrote, spliced at
// the call's position by spliceSubqueryOnlyTrashed, not a scope.
type subqueryScopeRemovals struct {
	names       []string
	withTrashed bool
	onlyTrashed bool
}

// removes reports whether the named scope was removed - by its own name or, through the port's "*"
// wildcard, by a withoutGlobalScopes() that dropped every scope.
func (s subqueryScopeRemovals) removes(name string) bool {
	return slices.Contains(s.names, "*") || slices.Contains(s.names, name)
}

// scopeRemovalsOf collects the removal state a relation callback or OnQuery left on its scoped
// builder. A nil builder (no callback, no default query) removes nothing.
func scopeRemovalsOf(scoped *OrmBuilder) subqueryScopeRemovals {
	if scoped == nil {
		return subqueryScopeRemovals{}
	}

	return subqueryScopeRemovals{
		names:       append([]string(nil), scoped.conditions.withoutGlobalScopes...),
		withTrashed: scoped.conditions.withTrashed,
		onlyTrashed: scoped.conditions.onlyTrashed,
	}
}

// merge folds other's removals into s - the closure's and the relation default's sets both apply to
// one subquery builder in Laravel, so a name from either removes the scope and a trashed flag from
// either governs the soft-delete predicate (onlyTrashed winning over withTrashed, as the whereNotNull
// the onlyTrashed macro adds does).
func (s subqueryScopeRemovals) merge(other subqueryScopeRemovals) subqueryScopeRemovals {
	s.names = append(s.names, other.names...)
	s.withTrashed = s.withTrashed || other.withTrashed
	s.onlyTrashed = s.onlyTrashed || other.onlyTrashed

	return s
}

// applySubquerySoftDelete adds the related model's "not soft deleted" predicate inside an existence
// subquery. Laravel applies the related model's SoftDeletingScope to the subquery, and the compiled
// relation read (applySoftDelete) already filters deleted rows, so without this an existence check
// would match a parent whose only related rows are soft-deleted. Only the framework's own soft-delete
// type is handled: a related model still declaring gorm.DeletedAt is left to GORM, exactly as on every
// other compiled path.
func (r *OrmBuilder) applySubquerySoftDelete(builder *querybuilder.Builder, table string, model any, removals subqueryScopeRemovals) {
	if !usesFrameworkSoftDeleteModel(model) {
		return
	}

	column, ok := softDeleteColumn(model)
	if !ok {
		return
	}

	// The predicate is a global scope on the subquery builder, so a withTrashed()/onlyTrashed() the
	// relation's default query or the caller's closure left on it - or a withoutGlobalScope of the
	// SoftDeletingScope name / withoutGlobalScopes() - governs it as applySoftDelete governs the
	// main-path predicate: each of them drops it. onlyTrashed is Laravel's macro, which removes the
	// scope and writes its whereNotNull where it was called; spliceSubqueryOnlyTrashed wrote that
	// predicate among the closure's / relation's own wheres, so nothing is written for it here.
	if removals.onlyTrashed || removals.withTrashed || removals.removes(contractsorm.SoftDeletingScope) {
		return
	}

	// A structured where, so the grammar compiles Laravel's lowercase "is null" through
	// compileWhereNull instead of rendering a raw fragment's own casing.
	builder.WhereNull(table+"."+column, core.BooleanAnd, false)
}

// applySubqueryThroughSoftDelete adds a soft-deletable through model's "not soft deleted" predicate
// inside an existence subquery. Laravel registers it as the named "SoftDeletableHasManyThrough"
// global scope (HasOneOrManyThrough::performJoin), so - exactly as on the main path
// (applyThroughSoftDelete) - a removal of that name or the "*" wildcard withoutGlobalScopes() drops
// it, while a withTrashed()/onlyTrashed() or a SoftDeletingScope removal leaves it in place: those
// target the related model's scope alone. Oracle (illuminate/database v12.55.1, real sqlite,
// /tmp/g13b_v/probeC.php): the whereHas body carries `... and "tc_posts"."deleted_at" is null and
// "tc_users"."deleted_at" is null`; withoutGlobalScopes() drops both, a
// withoutGlobalScope('SoftDeletableHasManyThrough') drops only the through predicate, and
// withTrashed()/withoutGlobalScope(SoftDeletingScope) keep it.
func (r *OrmBuilder) applySubqueryThroughSoftDelete(builder *querybuilder.Builder, table string, model any, removals subqueryScopeRemovals) {
	if !usesFrameworkSoftDeleteModel(model) {
		return
	}

	column, ok := softDeleteColumn(model)
	if !ok {
		return
	}

	if removals.removes(contractsorm.SoftDeletableHasManyThrough) {
		return
	}

	builder.WhereNull(table+"."+column, core.BooleanAnd, false)
}

// applyRelatedGlobalScopes folds the related model's global scopes into an existence subquery.
// Laravel builds the subquery from `$relation->getRelated()->newQueryWithoutRelationships()`, a
// fresh query whose global scopes are registered, and toBase() applies them inside the subquery;
// the parent's own scope removals do not reach them. The scopes land after the correlation and the
// relation's constraints, each in its declaration order - the order addGlobalScopes gives the
// scopes of a top-level query - and each is regrouped the way the callScope addGlobalScopes runs it,
// so a scope's or-chain becomes its own nested group before the next scope's clauses land.
//
// table is the qualifier the subquery reads the related model under, as for applySubquerySoftDelete:
// an onlyTrashed a scope calls is Laravel's macro running inside callScope after SoftDeletingScope
// already applied, so its whereNotNull lands among the scope's own wheres at the call position and
// the "is null" stays (oracle, illuminate/database v12.55.1, real sqlite, /Users/dog/.claude/jobs/013c27a0/tmp/r15b-verify-probes/paths.php E1,
// E2, E3 and /Users/dog/.claude/jobs/013c27a0/tmp/r15b-verify-probes/related_scope.php E5, E7, E8: whereHas('tposts') over a scope
// where('id', '>', 0)->onlyTrashed() compiles `... and "sd_posts"."deleted_at" is null and "id" > ?
// and "sd_posts"."deleted_at" is not null`).
func (r *OrmBuilder) applyRelatedGlobalScopes(builder *querybuilder.Builder, table string, relatedModel any, removals subqueryScopeRemovals) {
	scopes := relatedGlobalScopes(relatedModel)
	if len(scopes) == 0 {
		return
	}

	scoped := r.fresh()
	scopedWhereCount := 0
	scopedBindingCount := 0
	for _, globalScope := range scopes {
		// A scope the relation's default query or the caller's closure removed by name - or, for the
		// "*" wildcard withoutGlobalScopes(), every scope - is skipped, the way withoutGlobalScope
		// unsets it before the deferred scopes apply.
		if removals.removes(globalScope.Name) {
			continue
		}

		originalWhereCount := len(builder.State().Wheres)
		originalOnlyTrashedCalls := len(scoped.conditions.onlyTrashedCalls)

		if returned, ok := globalScope.Scope(scoped).(*OrmBuilder); ok {
			scoped = returned
		}

		// Flush after each scope so the next one's clauses land after the previous one's, the order
		// withScopesApplied gives the scopes of a top-level query.
		scoped = scoped.flushWheres()

		// The onlyTrashed calls this scope made write the related model's "is not null" at their
		// call positions, before the scope's wheres move onto the subquery and are regrouped.
		if calls := scoped.conditions.onlyTrashedCalls; len(calls) > originalOnlyTrashedCalls && usesFrameworkSoftDeleteModel(relatedModel) {
			if column, ok := softDeleteColumn(relatedModel); ok {
				whereCount := len(scoped.query.State().Wheres)
				scoped.query.WhereNull(table+"."+column, core.BooleanAnd, true)
				scoped.spliceOnlyTrashedPredicate(scoped.query, whereCount, calls[originalOnlyTrashedCalls:])
			}
		}

		// The scope starts from what the previous scopes produced - addGlobalScopes reuses its
		// query per scope - so only the wheres it added move onto the subquery now.
		allWheres := scoped.query.State().Wheres
		addedWheres := allWheres[scopedWhereCount:]
		scopedWhereCount = len(allWheres)
		if len(addedWheres) == 0 {
			continue
		}

		allBindings := scoped.query.State().Bindings["where"]
		addedBindings := allBindings[scopedBindingCount:]
		scopedBindingCount = len(allBindings)

		builder.State().Wheres = append(builder.State().Wheres, addedWheres...)
		_ = builder.AddBinding(addedBindings, "where")

		if len(builder.State().Wheres) > originalWhereCount {
			addNewWheresWithinGroupIn(builder, originalWhereCount)
		}
	}
}

// relatedGlobalScopes resolves the global scopes - name and callback - a related model declares, in
// declaration order. Nil when the model declares none or is not a global-scope model.
func relatedGlobalScopes(relatedModel any) []contractsorm.GlobalScope {
	if relatedModel == nil {
		return nil
	}

	model, err := modelToStruct(relatedModel)
	if err != nil {
		return nil
	}

	modelWithGlobalScopes, ok := model.(contractsorm.ModelWithGlobalScopes)
	if !ok {
		return nil
	}

	modelGlobalScopes := modelWithGlobalScopes.GlobalScopes()
	if len(modelGlobalScopes) == 0 {
		return nil
	}

	// The scopes keep their declaration order - the order a model lists them in, which is the order
	// addGlobalScopes applies a top-level query's scopes in.
	// The names travel with the scopes: a removal names a scope by its key, the way
	// withoutGlobalScope unsets $builder->scopes[$name] before the deferred scopes apply.
	return append([]contractsorm.GlobalScope(nil), modelGlobalScopes...)
}

// subqueryBuilder starts a framework builder for a correlated subquery over table.
//
// The name is bare, like every table name the ORM holds, and goes in through tableRef so the
// grammar renders it with the dialect quoting and the connection prefix exactly once.
func (r *OrmBuilder) subqueryBuilder(table string) *querybuilder.Builder {
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

// applyRelationScope runs a relation callback and carries its where conditions onto the subquery
// builder, in order after the correlation wheres. Laravel runs the callback through callScope, which
// regroups the subquery's wheres around the clauses the callback added, so an or-chain the callback
// leaves becomes its own nested group instead of leaking past the correlation; the same regroup is
// applied here. Non-where clauses the callback sets (an order by, a limit) are deliberately not
// carried: Laravel emits them inside the subquery, but SQL Server rejects ORDER BY in a subquery,
// and relation_sql_test.go pins the difference.
//
// table and model are the related side the subquery reads (table is the self-join alias when the
// level aliased itself): the clauses the callback deferred - its onlyTrashed predicates, the
// existence clauses and where-closure groups it wrote - are finished against the related model
// (finishSubqueryScope) before the clauses are carried over.
func (r *OrmBuilder) applyRelationScope(builder *querybuilder.Builder, callback contractsorm.RelationCallback, table string, model any) (*OrmBuilder, error) {
	if callback == nil {
		return nil, nil
	}

	originalWhereCount := len(builder.State().Wheres)

	scoped := r.fresh()
	if returned, ok := callback(scoped).(*OrmBuilder); ok {
		scoped = returned
	}

	scoped = scoped.flushWheres()
	if err := finishSubqueryScope(scoped, table, model); err != nil {
		return nil, err
	}

	appendWhereClauses(builder, scoped.query)

	if len(builder.State().Wheres) > originalWhereCount {
		addNewWheresWithinGroupIn(builder, originalWhereCount)
	}

	return scoped, nil
}

// applyOneOfManyRelationScope applies a relation's default scope to an existence subquery. A plain
// scope contributes its where clauses, as before. A one-of-many relation instead joins the
// aggregate subquery Laravel's ofMany builds — with the relation's of-many closure constraints
// inside it — so the existence check is evaluated against the one-of-many rows, the way
// HasOne::getRelationExistenceQuery merges the join before the exists runs.
// relationScopeQuery resolves a relation's default scope without applying it: the returned query
// holds the where clauses the caller appends after its own callback, and the join string (with its
// bindings) is the one-of-many JOIN Laravel's ofMany installs, which the caller applies at join
// position. The split mirrors Laravel: HasOne::getRelationExistenceQuery merges the of-many join
// into the existence query, while mergeConstraintsFrom appends the relation's own wheres after the
// caller's closure has run.
//
// table and model name the related side, as for applyRelationScope: an onlyTrashed the relation's
// default query called writes the related model's "is not null" among its wheres at its call
// position (Laravel's macro writes it on the relation query, and mergeConstraintsFrom carries it
// with the relation's other wheres). It is spliced after the one-of-many join took the constraints
// written after OfMany, so the predicate stays on the existence query.
func (r *OrmBuilder) relationScopeQuery(desc *relationDescriptor, table string, model any) (*OrmBuilder, string, []any, error) {
	if desc.OnQuery == nil {
		return nil, "", nil, nil
	}

	scoped := r.fresh()
	if returned, ok := desc.OnQuery(scoped).(*OrmBuilder); ok {
		scoped = returned
	}

	if scoped.conditions.oneOfMany != nil {
		join, args, err := r.oneOfManyJoin(scoped, desc, scoped.conditions.oneOfMany, nil, false)
		if err == nil && join != "" {
			// Constraints written before OfMany stay on the existence query itself; the ones after
			// it were moved into the aggregate subquery by the join builder.
			scoped = scoped.flushWheres()
			if err := finishSubqueryScope(scoped, table, model); err != nil {
				return nil, "", nil, err
			}

			return scoped, join, args, nil
		}
	}

	scoped = scoped.flushWheres()
	if err := finishSubqueryScope(scoped, table, model); err != nil {
		return nil, "", nil, err
	}

	return scoped, "", nil, nil
}

// finishSubqueryScope writes what a relation scope or caller closure deferred onto its own builder,
// against the related side the subquery reads: first each onlyTrashed predicate at its call position
// (spliceSubqueryOnlyTrashed), then the existence clauses and where-closure groups it recorded
// (applyRelations, with the related model as their parent), each at the position its call recorded.
// Laravel wrote all of them onto the subquery builder at call time - a whereHas inside a whereHas
// callback correlates its own exists to the related table - so they lead the related model's
// deferred scopes, which follow in the subquery. Oracle (illuminate/database v12.55.1, real sqlite,
// /Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle_sub.php S1-S5): whereHas('mids', fn => whereHas('leaves')) compiles `exists
// (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and
// exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and
// "rel_deep_leaves"."deleted_at" is null) and "rel_deep_mids"."deleted_at" is null and "active" =
// ?)`, and a where closure's onlyTrashed there writes `("id" = ? and "rel_deep_mids"."deleted_at" is
// not null)` while the subquery keeps its own "is null" - the closure's builder is the one whose
// scope the macro removed.
func finishSubqueryScope(scoped *OrmBuilder, table string, model any) error {
	spliceSubqueryOnlyTrashed(scoped, table, model)

	if len(scoped.conditions.relations) == 0 {
		return nil
	}

	scoped.conditions.model = model
	if err := scoped.applyRelations(scoped.query); err != nil {
		return err
	}
	scoped.conditions.relations = nil

	return nil
}

// keepFirstError carries err on the query for the statement to report, unless an earlier error is
// already carried. The morph existence variants use it: they report no error of their own.
func (r *OrmBuilder) keepFirstError(err error) {
	if err != nil && r.conditions.err == nil {
		r.conditions.err = err
	}
}

// spliceSubqueryOnlyTrashed writes the related model's "is not null" predicate onto a relation
// scope's or caller closure's builder once per OnlyTrashed call it made, at the where position each
// call recorded - the shape Laravel's onlyTrashed macro leaves: the macro runs whereNotNull on the
// subquery builder at call time, between the wheres written before and after it, and each call
// writes its own predicate. The scoped builder is a fresh query with no model of its own, so the
// related side is named explicitly: table is the qualifier the subquery reads the related model
// under (the self-join alias when the level aliased itself, as Laravel's getQualifiedDeletedAtColumn
// does after getRelationExistenceQueryForSelfRelation renamed the model's table), model the related
// model. Only the framework's own soft-delete type is handled, as everywhere else on the compiled
// path. Oracle (illuminate/database v12.55.1, real sqlite, /Users/dog/.claude/jobs/013c27a0/tmp/r15b-probes/oracle.php X1, X4, X5, X6, O6,
// O8): whereHas('posts', fn => where('id', 1)->onlyTrashed()->where('id', 2)) compiles `exists
// (select * from "sd_posts" where "sd_users"."id" = "sd_posts"."sd_user_id" and "id" = ? and
// "sd_posts"."deleted_at" is not null and "id" = ?)`.
func spliceSubqueryOnlyTrashed(scoped *OrmBuilder, table string, model any) {
	if scoped == nil || !scoped.conditions.onlyTrashed || !usesFrameworkSoftDeleteModel(model) {
		return
	}

	column, ok := softDeleteColumn(model)
	if !ok {
		return
	}

	whereCount := len(scoped.query.State().Wheres)
	scoped.query.WhereNull(table+"."+column, core.BooleanAnd, true)
	scoped.spliceOnlyTrashedPredicate(scoped.query, whereCount, scoped.conditions.onlyTrashedCalls)
}

// appendWhereClauses moves a child query's where clauses onto the subquery builder, bindings
// included, so they keep their place after the clauses already on it.
func appendWhereClauses(dst, src *querybuilder.Builder) {
	if len(src.State().Wheres) == 0 {
		return
	}

	dst.State().Wheres = append(dst.State().Wheres, src.State().Wheres...)
	_ = dst.AddBinding(src.State().Bindings["where"], "where")
}

func (r *OrmBuilder) parentModel() any {
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

func (r *OrmBuilder) parentTable(parent any) string {
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

// parseAggregateRelationName splits a withAggregate relation string on the "as" rename Laravel
// parses: "foo as foo_bar" yields ("foo", "foo_bar"); anything else keeps the whole string as the
// relation and reports no alias. Mirrors withAggregate's explode(' ') and Str::lower check.
func parseAggregateRelationName(relation string) (name, alias string) {
	segments := strings.Split(relation, " ")
	if len(segments) == 3 && strings.EqualFold(segments[1], "as") {
		return segments[0], segments[2]
	}

	return relation, ""
}

// aggregateColumnValue returns the string value of an aggregate column and whether the column was
// acceptable: a string is returned as written, an expression (database/query's Raw / Expression)
// contributes its raw SQL - the value Grammar::getValue reads. Mirrors the string|Expression union
// of withAggregate's $column.
func aggregateColumnValue(column any) (string, bool) {
	if value, ok := column.(string); ok {
		return value, true
	}
	if core.IsExpression(column) {
		return core.ExpressionValue(column), true
	}

	return "", false
}

// selfJoinAliasCount names the aliased copies of a table that self-referencing subqueries read.
// Laravel's Relation::$selfJoinCount is a process-wide static; the same numbering is reproduced
// here, so the Nth self-join compiled in a process aliases laravel_reserved_(N-1) - the first is
// laravel_reserved_0. Tests that assert the alias normalise the index, as Laravel's own corpus
// tests do.
var selfJoinAliasCount atomic.Int64

// nextSelfJoinAlias hands out the next alias. The post-increment matches
// Relation::getRelationCountHash(): the first alias a process hands out is laravel_reserved_0.
func nextSelfJoinAlias() string {
	return fmt.Sprintf("laravel_reserved_%d", selfJoinAliasCount.Add(1)-1)
}

// aggregateAlias derives the select alias for an aggregate without an explicit rename, byte for
// byte the way withAggregate computes it:
//
//	Str::snake(preg_replace('/[^[:alnum:][:space:]_]/u', '', sprintf('%s %s %s',
//	    $name, $function, strtolower($grammar->getValue($column)))))
//
// so min(price - discount) over "foo" aliases as "foo_min_price_discount" and no punctuation from
// an expression column can leak into the column name.
func aggregateAlias(name, fn, column string) string {
	stripped := make([]rune, 0, len(name)+len(fn)+len(column)+2)
	for _, char := range fmt.Sprintf("%s %s %s", name, fn, phpLower(column)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || unicode.IsSpace(char) || char == '_' {
			stripped = append(stripped, char)
		}
	}

	return laravelSnake(string(stripped))
}

// phpLower lowercases the way PHP's strtolower does: ASCII letters only, every other rune left
// alone.
func phpLower(value string) string {
	return strings.Map(func(char rune) rune {
		if char >= 'A' && char <= 'Z' {
			return char + ('a' - 'A')
		}

		return char
	}, value)
}

// laravelSnake mirrors Str::snake's transformation for an alias input: the first letter of every
// whitespace-separated word is uppercased, the whitespace is removed, a delimiter is inserted
// before every interior capital, and the result is lowercased - the sequence that turns
// "foo min price  discount" into "foo_min_price_discount" and
// "foo sum timestampdiffsecond created_at updated_at" into
// "foo_sum_timestampdiffsecond_created_at_updated_at". Str::snake's ctype_lower fast path is not
// needed here: an alias input always contains the sprintf's spaces.
func laravelSnake(value string) string {
	upper := []rune(value)
	atWordStart := true
	for index, char := range upper {
		if atWordStart && unicode.IsLetter(char) {
			upper[index] = unicode.ToUpper(char)
			atWordStart = false

			continue
		}

		atWordStart = isPHPSpace(char)
	}

	joined := make([]rune, 0, len(upper))
	for _, char := range upper {
		if !isPHPSpace(char) {
			joined = append(joined, char)
		}
	}

	var builder strings.Builder
	builder.Grow(len(joined))
	for index, char := range joined {
		if index > 0 && char >= 'A' && char <= 'Z' {
			builder.WriteRune('_')
		}
		builder.WriteRune(char)
	}

	return strings.ToLower(builder.String())
}

// isPHPSpace reports whether a rune is one of the characters PHP's ucwords treats as a word
// separator and \s matches: space, tab, newline, carriage return, form feed, vertical tab.
func isPHPSpace(char rune) bool {
	switch char {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}

	return false
}

// Sanity: ensure *Query satisfies contractsorm.QueryWithRelations at compile time.
var _ contractsorm.QueryWithRelations = (*OrmBuilder)(nil)
