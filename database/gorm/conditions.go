package gorm

import (
	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
)

// Conditions holds the state that belongs to the ORM layer rather than to the SQL builder: the model
// and destination a statement addresses, eager loads, relationship subqueries, scopes, soft-delete
// switches and a few write-path filters. The SQL clauses themselves — select, where, join, group,
// having, order, limit, offset, distinct and lock — live on the Query's persistent builder, the way
// Laravel keeps them on Query\Builder. Conditions deliberately has no second copy of them.
type Conditions struct {
	// err carries a condition that could not be accepted — an unusable operator or value — so the
	// error surfaces from whichever statement is eventually compiled, rather than from a query object
	// built only to hold it.
	err                 error
	dest                any
	model               any
	table               *Table
	omit                []string
	scopes              []func(contractsorm.Query) contractsorm.Query
	raw                 *Select
	selectSubs          []selectSub
	where               []contractsdriver.Where
	eagerLoad           []eagerLoadEntry
	relations           []relationExistence
	oneOfMany           *oneOfManyConfig
	useWritePdo         bool
	withoutEvents       bool
	withoutGlobalScopes []string
	withTrashed         bool
	onlyTrashed         bool
}

// oneOfManyConfig captures the column + aggregate for OfMany / LatestOfMany / OldestOfMany when
// they are called inside a With() eager-load callback. It's read by runRelatedQueryCompiled, which
// rewrites the inner query into an INNER JOIN over a per-parent aggregate subquery.
type oneOfManyConfig struct {
	column    string
	aggregate string // "MAX" | "MIN" | other SQL aggregate
}

type Select struct {
	query any
	args  []any
}

type Table struct {
	// name is a bare table name, which the grammar prefixes at render time, or a
	// querybuilder.Expression naming a table that is already qualified. An expression is rendered
	// verbatim, so the connection prefix is not applied to it.
	name any
	args []any
}

// selectSub describes a deferred sub-select aggregate (WithCount / WithMax / etc.).
// The relation is resolved when the query is compiled to SQL, where the parent model is known.
type selectSub struct {
	relation string
	column   string
	function string // count | max | min | sum | avg | exists
	alias    string
	callback contractsorm.RelationCallback
}

// relationExistence describes a deferred relationship existence/absence condition.
// Building is deferred so the parent model can be resolved from conditions.model or conditions.dest
// (the latter is set by Find/First/Get when the user passes a dest).
type relationExistence struct {
	relation    string
	operator    string
	count       int
	conjunction string // "and" | "or"
	callback    contractsorm.RelationCallback

	// morph specifics (zero-valued for non-morph queries)
	morphTypes    []any
	morphCallback contractsorm.MorphRelationCallback
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
	out.scopes = append([]func(contractsorm.Query) contractsorm.Query(nil), c.scopes...)
	out.selectSubs = append([]selectSub(nil), c.selectSubs...)
	out.where = append([]contractsdriver.Where(nil), c.where...)
	out.eagerLoad = append([]eagerLoadEntry(nil), c.eagerLoad...)
	out.relations = append([]relationExistence(nil), c.relations...)
	out.withoutGlobalScopes = append([]string(nil), c.withoutGlobalScopes...)

	if c.table != nil {
		table := *c.table
		out.table = &table
	}
	if c.raw != nil {
		raw := *c.raw
		out.raw = &raw
	}
	if c.oneOfMany != nil {
		oneOfMany := *c.oneOfMany
		out.oneOfMany = &oneOfMany
	}

	return out
}
