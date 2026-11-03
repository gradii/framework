package gorm

import (
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	querybuilder "github.com/gradii/framework/database/query"
)

// queryPivotQuery adapts a contractsorm.Query to the PivotQuery interface so OnPivotQuery
// callbacks can be threaded through builder-level reads (e.g. OrmBuilder.Related). Each Where* call
// is rewritten with the pivot table qualifier before being appended to the underlying OrmBuilder.
// The In / Null family goes through the structured where methods, so the grammar renders its own
// keywords - lowercase in / not in / is null / is not null - exactly as Laravel's wherePivotIn and
// wherePivotNull delegate to whereIn and whereNull.
type queryPivotQuery struct {
	grammar   querybuilder.Grammar
	q         contractsorm.Query
	tableName string
	// bare replays the caller's column WITHOUT the pivot-table qualifier - the shape Laravel's
	// newPivotQuery uses when it replays the recorded pivot wheres raw (`$query->where(...$arguments)`,
	// InteractsWithPivotTable.php:682, the arguments verbatim). The relation read path keeps the
	// qualifier (wherePivot applies `$this->where($this->qualifyPivotColumn($column))` to the
	// relation query), so the adapter is built bare only for the pivot-row primitives.
	bare bool
}

// pivotColumn names the column the way a caller writes one. The structured where methods wrap each
// segment at compile time; the qualified form carries the pivot table, the bare form the column
// alone, because the grammar-wrapped form the Where method passes as an Expression would be quoted
// a second time.
func (p *queryPivotQuery) pivotColumn(column string) string {
	if p.bare {
		return column
	}

	return p.tableName + "." + column
}

func newQueryPivotQuery(grammar querybuilder.Grammar, q contractsorm.Query, tableName string) *queryPivotQuery {
	return &queryPivotQuery{grammar: grammar, q: q, tableName: tableName}
}

// newBareQueryPivotQuery is newQueryPivotQuery for the pivot-row primitives (DoDetach /
// DoUpdateExistingPivot / the sync-family current-ids read): Laravel's newPivotQuery replays the
// recorded pivot wheres raw, so the column reaches the statement unqualified. Oracle v12.55.1,
// executed, /tmp/c27_pivot_qualification_probe.php U3/D4/S3/T3:
// `... where "role" = ? ...` (cf. the relation read E3, where the same clause reads
// `"c27_post_tag"."role" = ?`).
func newBareQueryPivotQuery(grammar querybuilder.Grammar, q contractsorm.Query, tableName string) *queryPivotQuery {
	return &queryPivotQuery{grammar: grammar, q: q, tableName: tableName, bare: true}
}

func (p *queryPivotQuery) qualified(column string) string {
	if p.bare {
		return p.grammar.Wrap(column)
	}

	return p.grammar.Wrap(p.tableName + "." + column)
}

// Where adds a `column op value` clause to the pivot query. The column is wrapped by the grammar and
// the condition goes through the compiled builder, which validates the operator and swaps an unknown
// one for "=" while binding it as the value - the handling Laravel's wherePivot inherits from
// Builder::where(). Concatenating the operator into a fragment let a caller string reach the SQL.
func (p *queryPivotQuery) Where(column string, args ...any) contractsorm.PivotQuery {
	qualified := querybuilder.Expression{Value: p.qualified(column)}

	switch len(args) {
	case 1:
		p.q = p.q.Where(qualified, "=", args[0])
	case 2:
		if op, ok := args[0].(string); ok {
			p.q = p.q.Where(qualified, op, args[1])
		} else {
			p.q = p.q.Where(qualified, "=", args[0]).Where(qualified, "=", args[1])
		}
	}

	return p
}

func (p *queryPivotQuery) WhereIn(column string, values []any) contractsorm.PivotQuery {
	p.q = p.q.WhereIn(p.pivotColumn(column), values)
	return p
}

func (p *queryPivotQuery) WhereNotIn(column string, values []any) contractsorm.PivotQuery {
	p.q = p.q.WhereNotIn(p.pivotColumn(column), values)
	return p
}

func (p *queryPivotQuery) WhereNull(column string) contractsorm.PivotQuery {
	p.q = p.q.WhereNull(p.pivotColumn(column))
	return p
}

func (p *queryPivotQuery) WhereNotNull(column string) contractsorm.PivotQuery {
	p.q = p.q.WhereNotNull(p.pivotColumn(column))
	return p
}

// applyOnPivotQueryToQuery threads the descriptor's OnPivotQuery callback through q, the
// builder-level read path used by Query.Related's many-to-many read and the eager loader's
// compiled pivot reads. No-op if the callback is nil.
func applyOnPivotQueryToQuery(grammar querybuilder.Grammar, q contractsorm.Query, desc *relationDescriptor) contractsorm.Query {
	if desc.OnPivotQuery == nil {
		return q
	}
	pq := newQueryPivotQuery(grammar, q, desc.PivotTable)
	desc.OnPivotQuery(pq)
	return pq.q
}

// applyOnPivotQueryToQueryBare is applyOnPivotQueryToQuery for the pivot-row primitives: the scope
// replays with the raw column (newBareQueryPivotQuery), matching Laravel's newPivotQuery
// (InteractsWithPivotTable.php:682). The relation read path keeps the qualified form.
func applyOnPivotQueryToQueryBare(grammar querybuilder.Grammar, q contractsorm.Query, desc *relationDescriptor) contractsorm.Query {
	if desc.OnPivotQuery == nil {
		return q
	}
	pq := newBareQueryPivotQuery(grammar, q, desc.PivotTable)
	desc.OnPivotQuery(pq)
	return pq.q
}
