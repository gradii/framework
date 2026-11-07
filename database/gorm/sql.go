package gorm

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
)

// This file compiles a query's conditions into SQL.
//
// The ORM used to hand its conditions to GORM and let GORM build the statement. That made GORM the
// only thing that knew what SQL the ORM produced. Compiling here instead puts the ORM on the
// framework's own query builder, which already owns the grammars, and leaves execution as the only
// thing still borrowed.

// compileSelect renders the query as a select statement.
//
// A raw statement is used as given: the caller wrote the whole thing, so none of the query's own
// clauses — including the soft-delete predicate — are applied to it.
func (r *OrmBuilder) compileSelect() (string, []any, error) {
	if raw := r.conditions.raw; raw != nil {
		return fmt.Sprint(raw.query), raw.args, nil
	}

	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	// A many-to-many relation read wears its own select list — the related columns plus the pivot
	// columns aliased pivot_<column> — the way BelongsToMany::shouldSelect gives get()/first()/
	// sole()/find()/cursor() theirs (and prepareQueryBuilder gives chunk()/each()/lazy()).
	r.applyPivotReadSelect(builder)

	// A through relation read wears its own select list — the related columns plus the
	// laravel_through_key alias — the way HasOneOrManyThrough::shouldSelect gives get()/first()/
	// cursor()/find() theirs.
	r.applyThroughReadSelect(builder)

	sql, bindings := builder.ToSelectSQL()
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// compileFirst renders the query as the single-row select First uses: the model's primary key
// orders it when the caller asked for no order, and one row is returned.
func (r *OrmBuilder) compileFirst() (string, []any, error) {
	if len(r.query.State().Orders) == 0 {
		if column := r.primaryKeyColumn(); column != "" {
			r.query.OrderByRaw(column, nil)
		}
	}

	r.query.Limit(1)

	sql, bindings, err := r.compileSelect()
	if err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// compileFind renders the read Find performs. Laravel's find($id) is whereKey($id)->first(): the
// scalar form reads one row and its statement carries `limit 1` with no ordering — unlike first(),
// which this ORM also orders by the model's key. A key list or a caller's own conditions keep the
// query's shape, so findMany's get() stays unlimited.
func (r *OrmBuilder) compileFind(conds []any) (string, []any, error) {
	if isScalarFindKey(conds) {
		r.query.Limit(1)
	}

	return r.compileSelect()
}

// primaryKeyColumn returns the model's first primary key column, qualified by its table, or "" when
// the model declares none.
func (r *OrmBuilder) primaryKeyColumn() string {
	model := r.parentModel()
	if model == nil {
		return ""
	}

	schema, err := parseSchema(model)
	if err != nil || len(schema.PrimaryFields) == 0 {
		return ""
	}

	return r.ident(schema.Table, schema.PrimaryFields[0].DBName)
}

// compileAggregate renders the query as an aggregate select, such as a count.
func (r *OrmBuilder) compileAggregate(function string, columns []any) (string, []any, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	// ToAggregateSQL mirrors Builder::aggregate(): with neither unions nor havings it drops the select
	// list and, with it, the select bindings a subselect contributed, so the bindings handed to the
	// executor match the placeholders left in the SQL. SetAggregate + ToSelectSQL compiled the same SQL
	// but leaked those bindings.
	sql, bindings := builder.ToAggregateSQL(core.Aggregate{Function: function, Columns: columns})
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// compileDelete renders the query as a delete statement.
//
// The rows are named by the query's destination or model when either carries a key, and a statement
// that would remove every row is refused rather than compiled. The query's scopes — the caller's own
// and the model's global ones — are folded in first, so a scope's where is what the guard sees: GORM
// built the scopes onto the statement and then checked it, and a scoped delete is safe for the same
// reason an explicit one is. The guard reads the compiled where clause (hasWhereConditions), so the
// model's soft-delete predicate counts the same way.
func (r *OrmBuilder) compileDelete() (string, []any, error) {
	query := r.withScopesApplied()

	builder, err := query.newBuilder()
	if err != nil {
		return "", nil, err
	}

	if !query.applyDeleteKeyWhere(builder) && !query.hasWhereConditions(builder) {
		return "", nil, errors.OrmMissingWhereClause
	}

	sql, bindings := builder.ToDeleteSQL()
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// compileUpdate renders the query as an update statement for the given values.
func (r *OrmBuilder) compileUpdate(values map[string]any) (string, []any, error) {
	return r.compileUpdateScoped(values, false)
}

// compileSoftDeleteUpdate renders the update a soft delete rewrites into, which is scoped the way a
// delete is rather than the way an update is.
func (r *OrmBuilder) compileSoftDeleteUpdate(values map[string]any) (string, []any, error) {
	return r.compileUpdateScoped(values, true)
}

// compileUpdateScoped renders an update statement, scoping it by the query's model key.
//
// A soft delete scopes by the destination's key instead, because Delete(&user) names the row through
// its destination rather than through Model(). Whichever applies joins the where clause last, after
// the caller's conditions and the soft-delete predicate, which is the order GORM built it in. A
// statement that would write every row is refused rather than compiled.
//
// The query's scopes — the caller's own and the model's global ones — are folded in before the guard,
// as they are for a delete, so a scope's where satisfies it, and so does the model's soft-delete
// predicate: the guard reads the compiled where clause (hasWhereConditions), which is how Laravel's
// `User::query()->increment('id')` updates the live rows alone. Only a statement whose where clause
// would be empty is refused.
func (r *OrmBuilder) compileUpdateScoped(values map[string]any, byDest bool) (string, []any, error) {
	query := r.withScopesApplied()

	builder, err := query.newBuilder()
	if err != nil {
		return "", nil, err
	}

	keyed := query.applyUpdateKeyWhere(builder)
	if byDest {
		keyed = query.applyDeleteKeyWhere(builder) || keyed
	}

	if !keyed && !query.hasWhereConditions(builder) {
		return "", nil, errors.OrmMissingWhereClause
	}

	sql, bindings := builder.ToUpdateSQL(values)
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// applySelectSubAggregates adds a sub-select column per queued WithCount / WithMax / WithExists and the
// like.
//
// The subqueries are appended to whatever columns the caller already asked for, so the parent's own
// columns still come first and the row can be scanned into the model. Each alias is the one fixed when
// the sub-select was queued - a "rel as alias" rename, the explicit alias of a
// contractsorm.RelationCount, or aggregateAlias' Str::snake derivation - so the column comes back
// under the name the caller expects and the connector renders lowercase `as`, the way
// Builder::selectSub emits it.
func (r *OrmBuilder) applySelectSubAggregates(builder *querybuilder.Builder) error {
	if len(r.conditions.selectSubs) == 0 {
		return nil
	}

	parent := r.parentModel()
	if parent == nil {
		return errors.OrmQueryEmptyRelation
	}

	for _, sub := range r.conditions.selectSubs {
		desc, err := relations.ResolveRelation(parent, sub.relation)
		if err != nil {
			return err
		}

		inner, err := r.compileAggregateSubquery(desc, sub)
		if err != nil {
			return err
		}
		if inner == nil {
			continue
		}

		subquery, bindings := inner.ToSelectSQL()

		alias := sub.alias
		if alias == "" {
			columnValue, _ := aggregateColumnValue(sub.column)
			alias = aggregateAlias(sub.relation, sub.function, columnValue)
		}

		if sub.function == "exists" {
			// A bare EXISTS as a column is a native bool on PostgreSQL, which will not scan into an
			// integer field, and SQL Server rejects it outright, so it is wrapped into a portable 0/1.
			builder.SelectRaw("CASE WHEN EXISTS ("+subquery+") THEN 1 ELSE 0 END AS "+r.ident(alias), bindings)

			continue
		}

		builder.SelectRaw("("+subquery+") as "+r.ident(alias), bindings)
	}

	return nil
}

// newBuilder builds a query builder carrying everything the conditions describe. The persistent
// builder already holds the SQL clauses (select, join, where, group, having, order, limit, offset,
// distinct, lock); this clones it and adds the ORM-specific parts that compile differently per
// statement: table, where, relation-existence subqueries, soft-delete predicate, and select-subs.
func (r *OrmBuilder) newBuilder() (*querybuilder.Builder, error) {
	// A queued scope is one of the model's global scopes, folded in by addGlobalScopes: Laravel
	// registers global scopes with withGlobalScope() and applies them when the statement compiles
	// (applyScopes), and this is that compile-time application. The caller's own Scopes() call
	// applied immediately (Builder::scopes) and left nothing queued. The scopes mutate the query,
	// so they run first.
	if len(r.conditions.scopes) > 0 || !r.conditions.softDeleteScopeApplied {
		scoped := r.withScopesApplied()
		if scoped.conditions.err != nil {
			return nil, scoped.conditions.err
		}

		return scoped.newBuilder()
	}

	// A condition the ORM could not accept is carried on the query rather than returned outright, so
	// it is reported from here.
	if r.conditions.err != nil {
		return nil, r.conditions.err
	}

	// Clone the persistent builder so compiling multiple statements from the same query doesn't
	// accumulate state (soft-delete predicate, relation subqueries).
	builder := r.query.Clone()

	if err := r.applyTable(builder); err != nil {
		return nil, err
	}

	// selectSubs are ORM-level select expressions (WithCount, WithSum), so they live in conditions
	// rather than on the builder. When present, the parent's own columns must be in the select too.
	if len(r.conditions.selectSubs) > 0 && len(builder.Selects()) == 0 {
		if parent := r.parentModel(); parent != nil {
			builder.SelectRaw(r.ident(r.parentTable(parent), "*"), nil)
		}
	}

	if err := r.applySelectSubAggregates(builder); err != nil {
		return nil, err
	}

	// The where clause is already complete on the clone: the declared where methods and the
	// conditions the ORM records internally (Find's, the FirstOr* fold's, the pivot chain's) all
	// write to the persistent builder when they are called.

	// Relation existence conditions a user scope queued join the where clause at the positions that
	// scope recorded; the ones the caller queued were spliced in by withScopesApplied.
	if err := r.applyRelations(builder); err != nil {
		return nil, err
	}

	// The soft-delete predicate was composed by withScopesApplied, as the first applied scope. The
	// relation existence clauses spliced above are the ones a user scope queued after it ran, at the
	// positions that scope recorded.

	// A through read filters a soft-deletable through model too, its predicate after the related
	// model's - Laravel applies the related SoftDeletingScope first, then the named through scope.
	r.applyThroughSoftDelete(builder)

	return builder, nil
}

// applySoftDelete adds the model's default soft-delete predicate when the statement is not unscoped,
// composed as Laravel's first applied scope (withScopesApplied calls it before any user scope, and
// the wheres before it are regrouped when they carry an or); OnlyTrashed's "is not null" is written
// by applyOnlyTrashed at its call position instead. The
// predicate is written as a structured where so the grammar compiles it through compileWhereNull -
// the same lowercase path Laravel's SoftDeletingScope::apply uses - instead of as a raw fragment.
//
// The predicate is the port's twin of the named SoftDeletingScope Laravel registers
// (contractsorm.SoftDeletingScope): removing that scope by name (WithoutGlobalScopes(
// contractsorm.SoftDeletingScope)) drops it exactly as Laravel's
// withoutGlobalScope(SoftDeletingScope::class) does, and removing every scope (WithoutGlobalScopes()
// with no names, the port's "*") drops it too, as Laravel's withoutGlobalScopes() drops every
// registered scope. WithTrashed drops it as well - Laravel's withTrashed macro is
// withoutGlobalScope(SoftDeletingScope), the same removal. OnlyTrashed keeps its "is not null":
// Laravel's onlyTrashed macro removes the scope and THEN adds whereNotNull, so that explicit
// predicate survives a named removal and overrides WithTrashed when both are set.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe.php §B/§C): the
// default read compiles `... where "sr_users"."deleted_at" is null and "email" = ?`;
// withoutGlobalScope(SoftDeletingScope::class) compiles `... where "email" = ?`;
// withoutGlobalScopes() compiles `select * from "sr_users"`; onlyTrashed() compiles `... where
// "sr_users"."deleted_at" is not null and "email" = ?`; and withoutGlobalScope(SoftDeletingScope)
// combined with onlyTrashed() keeps the `is not null`.
func (r *OrmBuilder) applySoftDelete(builder *querybuilder.Builder) error {
	if r.conditions.withTrashed && !r.conditions.onlyTrashed {
		return nil
	}

	if !r.usesFrameworkSoftDelete() {
		return nil
	}

	// The default predicate is the named scope's: a removal of that name (or of every scope) drops
	// it. OnlyTrashed's "is not null" is the whereNotNull onlyTrashed adds after removing the scope,
	// so it is kept even when the name is removed.
	if !r.conditions.onlyTrashed && r.globalScopeRemoved(contractsorm.SoftDeletingScope) {
		return nil
	}

	// onlyTrashed's predicate is not a scope: applyOnlyTrashed wrote it at its call position.
	if r.conditions.onlyTrashed {
		return nil
	}

	// The default predicate is SoftDeletingScope applied through Eloquent's callScope: the wheres
	// written before it and the predicate are each rebuilt as their own slice
	// (addNewWheresWithinGroup), so a user or-chain is wrapped in one group before the predicate is
	// and-ed on.
	originalWhereCount := len(builder.State().Wheres)
	if err := r.appendSoftDeletePredicate(builder, false); err != nil {
		return err
	}
	if len(builder.State().Wheres) > originalWhereCount {
		addNewWheresWithinGroupIn(builder, originalWhereCount)
	}

	return nil
}

// applyOnlyTrashed writes OnlyTrashed's "is not null" predicate where Laravel's onlyTrashed macro
// writes it: the macro removes SoftDeletingScope and calls whereNotNull on the builder at call time,
// so the predicate sits at the where position the call recorded and nothing is regrouped (no scope
// is applied for it). Each call writes its own predicate, as each macro call does. The relation
// existence clauses are spliced in afterwards, so each one moves right by the predicates written
// before it: those whose call recorded a smaller where count, and those whose call recorded the same
// where count before the clause itself was recorded.
//
// Oracle (illuminate/database v12.55.1, real sqlite, under
// /Users/dog/.claude/jobs/013c27a0/tmp/r15-probes/r15-r14-mutants/: probe2.out P8, probe3.out
// R1-R4): where->onlyTrashed->where->onlyTrashed compiles `"email" = ? and "deleted_at" is not null
// and "id" = ? and "deleted_at" is not null`; whereHas->onlyTrashed compiles `exists (...) and
// "deleted_at" is not null`, onlyTrashed->whereHas compiles `"deleted_at" is not null and exists
// (...)`.
func (r *OrmBuilder) applyOnlyTrashed(builder *querybuilder.Builder) error {
	if !r.conditions.onlyTrashed || !r.usesFrameworkSoftDelete() {
		return nil
	}

	return r.spliceOnlyTrashed(builder)
}

// spliceOnlyTrashed is applyOnlyTrashed's splice without the framework-type gate: it writes one
// "is not null" predicate per recorded OnlyTrashed call at that call's where position. A where
// closure's child query calls it directly for its own group (addNestedWhereQuery).
func (r *OrmBuilder) spliceOnlyTrashed(builder *querybuilder.Builder) error {
	whereCount := len(builder.State().Wheres)
	if err := r.appendSoftDeletePredicate(builder, true); err != nil {
		return err
	}
	if len(builder.State().Wheres) == whereCount {
		return nil
	}

	wheres := builder.State().Wheres
	predicate := wheres[whereCount]
	original := slices.Clone(wheres[:whereCount])

	calls := r.conditions.onlyTrashedCalls
	if len(calls) == 0 {
		calls = []onlyTrashedCall{{}}
	}

	// Rebuild the where list with one predicate per call, each at its call's position. The calls
	// are in call order, so their where counts never decrease.
	rebuilt := make([]core.Where, 0, whereCount+len(calls))
	next := 0
	for _, call := range calls {
		index := min(call.whereIndex, whereCount)
		rebuilt = append(rebuilt, original[next:max(next, index)]...)
		next = max(next, index)
		rebuilt = append(rebuilt, predicate)
	}
	rebuilt = append(rebuilt, original[next:]...)
	builder.State().Wheres = rebuilt

	items := make([]relationExistence, len(r.conditions.relations))
	for i, item := range r.conditions.relations {
		shift := 0
		for _, call := range calls {
			if call.whereIndex < item.whereIndex || (call.whereIndex == item.whereIndex && i >= call.relationIndex) {
				shift++
			}
		}
		item.whereIndex += shift
		items[i] = item
	}
	r.conditions.relations = items

	return nil
}

// globalScopeRemoved reports whether the query removed the named global scope - Laravel's
// withoutGlobalScope($name), which unsets $builder->scopes[$name] - or every scope at once: the
// port's "*" wildcard records WithoutGlobalScopes() with no names, the shape Laravel's
// withoutGlobalScopes(null) has when it removes array_keys($this->scopes). It is the single gate the
// two built-in soft-delete predicates consult, so both answer to the names Laravel registers them
// under (contractsorm.SoftDeletingScope, contractsorm.SoftDeletableHasManyThrough) in addition to
// the all-scope "*".
func (r *OrmBuilder) globalScopeRemoved(name string) bool {
	return slices.Contains(r.conditions.withoutGlobalScopes, "*") ||
		slices.Contains(r.conditions.withoutGlobalScopes, name)
}

// appendSoftDeletePredicate writes the model's soft-delete predicate onto a compiled builder through
// the builder's own WhereNull, so the grammar renders Laravel's compileWhereNull output ("is null",
// lowercase - "is not null" for the trashed side). It is shared by the compiled reads
// (applySoftDelete) and by the nested where closure a child query merges from, whose OnlyTrashed
// cannot wait for applySoftDelete: the child is never compiled on its own.
func (r *OrmBuilder) appendSoftDeletePredicate(builder *querybuilder.Builder, not bool) error {
	model := r.parentModel()
	if model == nil {
		return nil
	}

	column, ok := softDeleteColumn(model)
	if !ok {
		return nil
	}

	table, err := tableNameFor(model)
	if err != nil || table == "" {
		return err
	}

	builder.WhereNull(table+"."+column, core.BooleanAnd, not)

	return nil
}

// applyThroughSoftDelete appends the through model's soft-delete predicate to a through relation
// read: Laravel's HasOneOrManyThrough::performJoin registers a named "SoftDeletableHasManyThrough"
// global scope - a whereNull on the through table's deleted-at column - whenever the through model
// uses SoftDeletes, so the through table is filtered exactly like the related table. It runs after
// applySoftDelete so the related predicate precedes the through one, the order Laravel compiles
// (the related SoftDeletingScope registers before the named through scope).
//
// The predicate is the port's twin of that named scope (contractsorm.SoftDeletableHasManyThrough):
// removing the name - directly through WithoutGlobalScopes(contractsorm.SoftDeletableHasManyThrough)
// or through the relation's WithTrashedParents, the through-side switch Laravel exposes - or
// removing every scope (WithoutGlobalScopes(), the port's "*") drops it. It is NOT lifted by
// WithTrashed / OnlyTrashed, which target the related SoftDeletingScope alone. It keys off the
// query's through soft-delete fields, NOT the throughRead config, so a read that suppresses the
// relation's select-list alias (pluck / value / exists) still carries the predicate - Laravel
// registers it as a global scope on the relation's builder, independent of the select list. Only a
// HasOneThrough / HasManyThrough read with a soft-deletable through model is affected.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe3.php §E/§F2): the
// through read compiles `... where "sr_users"."country_id" = ? and "sr_posts"."deleted_at" is null
// and "sr_users"."deleted_at" is null`; withTrashedParents() (== withoutGlobalScope(
// 'SoftDeletableHasManyThrough')) drops only the through predicate; withTrashed()/onlyTrashed keep
// it; and withoutGlobalScopes() drops both.
func (r *OrmBuilder) applyThroughSoftDelete(builder *querybuilder.Builder) {
	if r.conditions.throughSoftDeleteColumn == "" {
		return
	}

	if r.globalScopeRemoved(contractsorm.SoftDeletableHasManyThrough) {
		return
	}

	// A structured where, so the grammar compiles Laravel's lowercase "is null" through
	// compileWhereNull instead of a raw fragment's own casing.
	// It is a named scope applied through callScope as well, so the wheres before it are regrouped
	// when they carry an or (addNewWheresWithinGroup).
	originalWhereCount := len(builder.State().Wheres)
	builder.WhereNull(r.conditions.throughSoftDeleteTable+"."+r.conditions.throughSoftDeleteColumn, core.BooleanAnd, false)
	addNewWheresWithinGroupIn(builder, originalWhereCount)
}

// applyTable points the builder at the query's table when it was not named directly: a table named
// through Table() is already the from clause the clone carries, and only a query without one derives
// it from the model.
func (r *OrmBuilder) applyTable(builder *querybuilder.Builder) error {
	if builder.State().From != nil {
		return nil
	}

	model := r.parentModel()
	if model == nil {
		return nil
	}

	table, err := tableNameFor(model)
	if err != nil {
		return err
	}
	builder.From(table, "")

	return nil
}

// applySingleWhere writes one internally recorded condition onto a builder in the shape the ORM
// records it: a raw fragment with its bindings, a column with its value, or the "col in ?"
// shorthand. addWhere routes the pivot chain's conditions through it at call time; the declared
// where methods write straight to the query builder when they are called, the way Eloquent's do.
func (r *OrmBuilder) applySingleWhere(builder *querybuilder.Builder, item contractsdriver.Where, boolean string) {
	applyWhereShape(builder, item.Query, item.Args, boolean, item.IsNot)
}

// applyWhereShape applies one condition to the builder in the shape the ORM records internally: a
// raw fragment with its bindings, a column with its value, a slice standing for an IN list, or a
// string carrying a slice for the "col in ?" shorthand Find's conditions and the pivot chain queue.
// The declared where methods do not pass through here; they write to the inner builder directly, and
// raw SQL has its own entry point in WhereRaw.
func applyWhereShape(builder *querybuilder.Builder, query any, args []any, boolean string, not bool) {
	if column, ok := query.(string); ok {
		applyRawOrColumnWhere(builder, column, args, boolean, not)

		return
	}

	applySimpleWhere(builder, query, args, boolean)
}

// applyRawOrColumnWhere routes a string condition: a fragment carrying a placeholder is rendered raw
// (the shape Builder::whereRaw takes), a bare fragment with no bindings likewise, and anything else
// is a column with its value.
func applyRawOrColumnWhere(builder *querybuilder.Builder, column string, args []any, boolean string, not bool) {
	switch {
	case len(args) == 0:
		builder.WhereRaw(column, nil, boolean)
	case len(args) == 1 && isSlice(args[0]) && !isByteSlice(args[0]) && containsPlaceholder(column):
		// A raw fragment whose one placeholder is fed a slice is the ORM's "col in ?" shorthand,
		// which the pivot chain and Find's conditions queue. The builder expands the slice into one
		// placeholder per value, wrapped in parentheses; binding the slice whole leaves one
		// placeholder against N bindings, and the database rejects the statement.
		//
		// A byte slice is not a list: isSlice also matches []byte, but a caller binds bytes to the
		// fragment's placeholder as one binary value, so it must keep the single placeholder and
		// fall through to WhereRaw below. Laravel renders the same fragment with one placeholder,
		// because a PHP string (its only byte-string type) is a single value (oracle.php).
		expanded, bindings := expandPlaceholderSlice(column, args[0])
		builder.WhereRaw(expanded, bindings, boolean)
	case containsPlaceholder(column):
		builder.WhereRaw(column, args, boolean)
	case len(args) == 1 && isSlice(args[0]) && !isByteSlice(args[0]):
		builder.WhereIn(column, args[0], boolean, not)
	default:
		applySimpleWhere(builder, column, args, boolean)
	}
}

// applySimpleWhere routes a (column, value) condition to the builder method that carries
// the connector's boolean. applySingleWhere computes the boolean for every other shape; the
// default (column, value) form used to be handed straight to builder.Where, which is always
// "and", so an OrWhere rendered as "and" and its clause could never match. Mirrors the
// facade's addSimpleWhere.
func applySimpleWhere(builder *querybuilder.Builder, query any, args []any, boolean string) {
	switch boolean {
	case core.BooleanOr:
		builder.OrWhere(query, args...)
	case core.BooleanAndNot:
		builder.WhereNot(query, args...)
	case core.BooleanOrNot:
		builder.OrWhereNot(query, args...)
	default:
		builder.Where(query, args...)
	}
}

// expandPlaceholderSlice rewrites the fragment's first binding placeholder into one placeholder per
// element of the slice, as the GORM-backed builder expanded a slice argument. The placeholders are
// wrapped in parentheses unless the replaced placeholder already sits just inside one, so both
// "col in ?" and "col in (?)" render a valid IN list. A placeholder-less fragment, or a value that is
// not a slice, is returned unchanged.
func expandPlaceholderSlice(query string, value any) (string, []any) {
	values, ok := sliceValues(value)
	if !ok {
		return query, []any{value}
	}

	index := strings.IndexByte(query, '?')
	if index < 0 {
		return query, values
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", ")
	if index > 0 && query[index-1] == '(' {
		return query[:index] + placeholders + query[index+1:], values
	}

	return query[:index] + "(" + placeholders + ")" + query[index+1:], values
}

// isByteSlice reports whether value is a byte slice or byte array. []byte is how a caller binds one
// binary value to a placeholder, so it is a value rather than a list to expand, even though isSlice
// also matches it. Pointer and interface indirection is peeled the same way sliceValues peels it, so a
// *[]byte behaves like its target.
func isByteSlice(value any) bool {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return false
	}
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return false
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return false
	}

	return reflected.Type().Elem().Kind() == reflect.Uint8
}

// containsPlaceholder reports whether a raw fragment carries a binding placeholder.
func containsPlaceholder(query string) bool {
	for _, char := range query {
		if char == '?' {
			return true
		}
	}

	return false
}

// applyInSliceGroup compiles an IN or NOT IN condition whose value is a slice for the WhereAll /
// WhereAny / WhereNone family: one WhereIn per column, joined by inner and wrapped in one nested
// group carrying group. Mirrors database/db's expandInGroup, and the empty list keeps the grammar's
// "0 = 1" (matches nothing) and "1 = 1" (matches everything) constants. It reports false when the
// condition is not an IN against a slice, so the caller falls back to the family's builder method.
func applyInSliceGroup(builder *querybuilder.Builder, columns []string, operator, value any, inner, group string) bool {
	if !isInOperator(operator) {
		return false
	}
	values, ok := sliceValues(value)
	if !ok {
		return false
	}

	not := isNotInOperator(operator)
	builder.WhereNested(func(b *querybuilder.Builder) *querybuilder.Builder {
		for _, column := range columns {
			b.WhereIn(column, values, inner, not)
		}

		return b
	}, group)

	return true
}

// toStrings converts a string slice to []any for the builder.
func toStrings(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}

	return out
}
