package gorm

import (
	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// Conditions holds the state that belongs to the ORM layer rather than to the SQL builder: the model
// and destination a statement addresses, eager loads, relationship subqueries, scopes, soft-delete
// switches and a few write-path filters. The SQL clauses themselves — from, select, where, join,
// group, having, order, limit, offset, distinct and lock — live on the OrmBuilder's persistent
// builder (OrmBuilder.query, read back through its State()), the way Laravel keeps them on
// Query\Builder. Conditions deliberately has no second copy of them: the table a query names
// (Table) and the conditions the ORM records internally (Find's inline conditions, the FirstOr*
// fold, the pivot chain) are written straight onto that builder when they are called.
//
// What remains falls into three groups:
//   - ORM state: the model and destination, the eager loads, the queued global scopes, the
//     write-path filters (omit, pendingAttributes) and switches (useWritePdo, withoutEvents,
//     withoutGlobalScopes, withoutTimestamps, withoutTouch, withTrashed, onlyTrashed), and the
//     error a rejected condition carries;
//   - deferred, model-dependent select expressions: selectSubs (WithCount and the like), rendered
//     at compile time when the parent model is known;
//   - relation shaping: oneOfMany, throughRead and pivotRead, which rewrite a relation read.
//
// The model-dependent where clauses - an OnlyTrashed predicate and a Has / WhereHas / DoesntHave
// existence clause - are not here either: each is a deferred entry in the builder's one where list
// at its call position (deferredWhere), rendered in place on the statement's copy.
//
// raw is the one SQL-level member kept on purpose: a caller-supplied whole statement (Raw) has no
// home on the query builder, whose State models clauses the grammar compiles rather than a finished
// statement — Laravel has no such builder member either (fromQuery runs the statement on the
// connection). compileSelect and compileChunkSelect return it verbatim, and the mass-update /
// delete guard counts it as a constraint, so it stays here as a Select.
type Conditions struct {
	// err carries a condition that could not be accepted — an unusable operator or value — so the
	// error surfaces from whichever statement is eventually compiled, rather than from a query object
	// built only to hold it.
	err   error
	dest  any
	model any
	omit  []string
	// withZero names the columns a struct write carries even when the field holds its zero value;
	// "*" names every column. See OrmBuilder.WithZero.
	withZero []string
	// scopes carries the scopes addGlobalScopes queued from the model — the Go form of Laravel's
	// withGlobalScope() registration, applied when the statement compiles (withScopesApplied). The
	// caller's own Scopes() call applies immediately (Builder::scopes) and never lands here.
	scopes []func(contractsorm.Query) contractsorm.Query
	// scopesApplied marks a query whose queued scopes have already been folded in by
	// withScopesApplied. Eloquent applies a builder's scopes once per terminal call
	// (applyScopes), and the reads that apply them before the onceWithColumns decision — pluck,
	// value, valueOrFail and get($columns) — then run their statement from the scoped copy, so a
	// second addGlobalScopes on that copy must not queue the model's scopes again.
	scopesApplied bool
	// softDeleteScopeApplied marks a query whose built-in soft-delete scope has already been applied
	// by withScopesApplied: the deferred where entries recorded so far are rendered in place and the
	// predicate is composed the way Eloquent's callScope composes the first registered scope
	// (SoftDeletingScope registers before every user scope). A compile from that query must not
	// apply it again.
	softDeleteScopeApplied bool
	raw                    *Select
	selectSubs             []selectSub
	eagerLoad              []eagerLoadEntry
	oneOfMany              *oneOfManyConfig
	useWritePdo            bool
	withoutEvents          bool
	withoutGlobalScopes    []string
	// withoutTimestamps counts the active WithoutTimestamps scopes: automatic created_at / updated_at
	// stamping is withheld while positive, so nested scopes keep the suspension active and each scope
	// restores exactly its own — Eloquent's $ignoreTimestampsOn list, kept as a depth because the Go
	// scope suspends the one query rather than a model class.
	withoutTimestamps int
	// withoutTouch marks the update an Increment / Decrement writes: Laravel's Builder::increment
	// and Model::increment never reach finishSave, so they propagate no touches, while the
	// model-update branch they share in the ORM does; the model-update touch hook skips
	// touchOwners while it is set.
	withoutTouch bool
	// withTrashed and onlyTrashed are the scope-removal switches the soft-delete predicate answers
	// to: WithTrashed removes SoftDeletingScope, and OnlyTrashed removes it too - Laravel's macro is
	// withoutGlobalScope then whereNotNull. The "is not null" each OnlyTrashed call writes is not a
	// switch: it is a deferred where entry at the call's position (deferredWhere).
	withTrashed bool
	onlyTrashed bool
	// existenceAlias is set on a whereHas callback's builder when the existence level it scopes
	// aliased its table (getRelationExistenceQueryForSelfRelation renames the related model's table
	// to the laravel_reserved_N hash): the existence entries the callback deferred then correlate to
	// the alias when they render, and a same-table relation under it sees the aliased from string,
	// so it does not alias.
	existenceAlias string
	// pendingAttributes are the column values WithAttributes recorded for every model the query
	// creates, mirroring Builder::$pendingAttributes. Keys are column names as the caller wrote them;
	// the write path resolves them against the model's schema.
	pendingAttributes map[string]any
	// throughRead names the through relation a model-row read is scoped to (Related on a
	// HasOneThrough / HasManyThrough relation). compileSelect gives the read Laravel's through
	// select list — the related table's columns plus the through key aliased as
	// laravel_through_key — the way HasOneOrManyThrough::shouldSelect does for get()/first()/cursor().
	throughRead *throughReadConfig
	// throughSoftDeleteTable / throughSoftDeleteColumn name the through relation's intermediate
	// table and its soft-delete column when the through model declares the framework's soft-delete
	// type. Laravel's HasOneOrManyThrough::performJoin registers the named
	// "SoftDeletableHasManyThrough" global scope on the relation's builder, so the whereNull reaches
	// every through statement - including the reads that drop the relation's select-list alias
	// (pluck / value / exists, through withoutThroughReadSelect). Kept separate from throughRead,
	// which that suppression clears, so suppressing the alias never drops the predicate.
	throughSoftDeleteTable  string
	throughSoftDeleteColumn string
	// pivotRead names the many-to-many relation a model-row read is scoped to (Related on a
	// Many2Many / MorphToMany / MorphedByMany relation), so the chunked read paths can give their
	// page statements Laravel's relation read shape: the related table's columns plus the pivot
	// columns aliased pivot_<column>, the way BelongsToMany::prepareQueryBuilder installs
	// shouldSelect() before chunk()/each()/lazy(), and the related key qualified by its table as
	// the keyset column, the way BelongsToMany::orderedChunkById qualifies it.
	pivotRead *pivotReadConfig
}

// oneOfManyColumn is one declared column/aggregate pair of a one-of-many configuration: the column
// the aggregate is taken over and the aggregate itself ("MAX" | "MIN" | other SQL aggregate).
type oneOfManyColumn struct {
	column    string
	aggregate string
}

// oneOfManyConfig captures the ordered columns for OfMany / OfManyColumns / LatestOfMany /
// OldestOfMany and the clause positions the query builder stood at when the configuration was
// installed. Every path that reads a one-of-many relation consumes it — the relation read path
// (Related), the eager loader and the existence subqueries behind WhereHas / WithCount / WithExists
// — each rewriting the read into one nested aggregate subquery per declared column and an inner
// join over the outermost, the way Laravel's ofMany installs them.
//
// keyAggregate is the aggregate the related model's key takes when the declared columns omit it:
// the string form of OfMany appends the key with its own aggregate, the ordered forms with "MAX",
// exactly as Laravel's ofMany does.
//
// The mark fields delimit the constraints the relation's own of-many chain wrote after the
// configuration — the Go form of Laravel's ofMany closure, which writes to the aggregate subquery.
// oneOfManyJoin moves everything after the marks into every subquery and leaves the earlier
// constraints (the relation's key constraint) on the outer query.
type oneOfManyConfig struct {
	columns      []oneOfManyColumn
	keyAggregate string

	whereMark     int
	joinMark      int
	whereBindMark int
	joinBindMark  int

	// removals is the scope-removal state the relation query carried when OfMany was called.
	// Laravel's newOneOfManySubQuery builds the aggregate from withoutGlobalScopes(
	// $this->removedScopes()) at ofMany time, so a removal written before OfMany reaches the
	// aggregate while one written after it governs the outer relation query alone.
	removals subqueryScopeRemovals
}

type Select struct {
	query any
	args  []any
}

// throughReadConfig names the tables behind a through relation read, so the read's select list can
// carry Laravel's laravel_through_key alias: `<related>.*` — or the columns the caller already
// chose — plus `<through>.<firstKey> as laravel_through_key`.
type throughReadConfig struct {
	relatedTable string
	throughTable string
	firstKey     string
}

// pivotReadConfig names the tables and keys behind a many-to-many relation read — the related
// table and its relation key, the pivot table and its two pivot keys, and the morph type column
// when the relation is polymorphic — so a relation read can render Laravel's relation select list
// (related.* plus each pivot column aliased pivot_<column>), hydrate the related model's declared
// Pivot struct from those aliases, and, for a chunked read, resolve its qualified keyset column.
// plan is the hydration plan of the related model's declared Pivot field (nil when it declares
// none) and doubles as the source of the withPivot-style extra columns in the select list.
// pivotField is the declared hydration field name (desc.pivotField, default "Pivot") the
// ConfigurePivotRead re-resolution falls back to when no call-time accessor overrides it;
// withPivotColumns carries the object's call-time WithPivot list, appended to the alias list
// after the plan's declared columns the way BelongsToMany::aliasedPivotColumns merges
// $pivotColumns (deduplicated with unique() at render).
type pivotReadConfig struct {
	relatedTable    string
	relatedKey      string
	pivotTable      string
	foreignPivotKey string
	relatedPivotKey string
	morphTypeColumn string
	plan            *pivotHydrationPlan
	// pivotField is the declared hydration field name; "" only when the descriptor carried none.
	pivotField string
	// withPivotColumns is the call-time WithPivot list (object state); nil until WithPivot runs.
	withPivotColumns []string
}

// selectSub describes a deferred sub-select aggregate (WithCount / WithMax / etc.).
// The relation is resolved when the query is compiled to SQL, where the parent model is known.
type selectSub struct {
	relation string
	column   any
	function string // count | max | min | sum | avg | exists
	alias    string
	callback contractsorm.RelationCallback
}

// deferredWhere is the payload of one deferred where entry (core.WhereDeferredType): a clause the
// query writes at call time, the way Laravel writes it onto $this->wheres, but can only render once
// the model it addresses is known - which a Go query may learn from the destination of the read
// (Query().WhereHas("Posts").Get(&users)). Either onlyTrashed is set - one OnlyTrashed call's
// "is not null" predicate (SoftDeletingScope::addOnlyTrashed's whereNotNull) - or relation describes
// one Has / WhereHas / DoesntHave existence clause (Query\Builder::addWhereExistsQuery's exists).
//
// The entry sits in the builder's where list at its call position, its Boolean the call's, and the
// payload pointer itself is the entry's one marker in the builder's "where" bindings, at the place
// the clause's bindings belong. A where closure's entries travel inside its nested group, and their
// markers with the group's bindings. resolveDeferredWheres renders every entry on the statement's
// copy and replaces it, and its marker, in place - or removes both when nothing renders. The payload
// is never changed after the call, so clones may share it.
//
// model is the model a where closure's own query ended with, recorded when the closure finishes
// (addNestedWhereQuery), and only when the closure set it itself because the enclosing query had
// none - a Table-only query whose closure calls Model(&User{}) before or after OnlyTrashed / Has. In
// Laravel a where closure's builder always carries the enclosing builder's model; the Go form can
// name it only inside the closure, and the clause then renders against it (scopeFor). nil otherwise.
type deferredWhere struct {
	onlyTrashed bool
	relation    relationExistence
	model       any
}

// relationExistence describes a deferred relationship existence/absence condition: the payload of a
// deferredWhere for a Has / WhereHas / DoesntHave call (and their or / morph forms). The conjunction
// is the entry's Boolean.
type relationExistence struct {
	relation string
	operator string
	count    int
	callback contractsorm.RelationCallback

	// morph specifics (zero-valued for non-morph queries)
	morphTypes    []any
	morphCallback contractsorm.MorphRelationCallback
	// morphAuto marks a `types = ['*']` condition — Laravel's has() on a MorphTo delegates to
	// hasMorph($relation, ['*'], ...). The morph values are read from the parent table's
	// distinct type-column values when the statement is compiled, instead of coming from
	// model instances.
	morphAuto bool
}

// hasStringColumns returns true if selectItems contains any string (regular column) entries
func hasStringColumns(selectItems []any) bool {
	for _, item := range selectItems {
		if _, ok := item.(string); ok {
			return true
		}
	}
	return false
}

// selectStringColumns extracts only the string (regular column) entries from selectItems
func selectStringColumns(selectItems []any) []string {
	var columns []string
	for _, item := range selectItems {
		if col, ok := item.(string); ok {
			columns = append(columns, col)
		}
	}
	return columns
}

// clone returns a copy that can be changed without affecting the original: every slice is copied
// and every pointer points at its own value.
func (c Conditions) clone() Conditions {
	out := c

	out.omit = append([]string(nil), c.omit...)
	out.withZero = append([]string(nil), c.withZero...)
	out.scopes = append([]func(contractsorm.Query) contractsorm.Query(nil), c.scopes...)
	out.selectSubs = append([]selectSub(nil), c.selectSubs...)
	out.eagerLoad = append([]eagerLoadEntry(nil), c.eagerLoad...)
	out.withoutGlobalScopes = append([]string(nil), c.withoutGlobalScopes...)
	if c.pendingAttributes != nil {
		out.pendingAttributes = make(map[string]any, len(c.pendingAttributes))
		for key, value := range c.pendingAttributes {
			out.pendingAttributes[key] = value
		}
	}

	if c.raw != nil {
		raw := *c.raw
		out.raw = &raw
	}
	if c.oneOfMany != nil {
		oneOfMany := *c.oneOfMany
		oneOfMany.columns = append([]oneOfManyColumn(nil), c.oneOfMany.columns...)
		out.oneOfMany = &oneOfMany
	}
	if c.throughRead != nil {
		throughRead := *c.throughRead
		out.throughRead = &throughRead
	}
	if c.pivotRead != nil {
		pivotRead := *c.pivotRead
		out.pivotRead = &pivotRead
	}

	return out
}
