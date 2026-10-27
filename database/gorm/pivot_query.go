package gorm

import (
	"fmt"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	querybuilder "github.com/gradii/framework/database/query"
)

// queryPivotQuery adapts a contractsorm.Query to the PivotQuery interface so OnPivotQuery
// callbacks can be threaded through builder-level reads (e.g. Query.Related). Each Where* call
// is rewritten with the pivot table qualifier before being appended to the underlying Query.
type queryPivotQuery struct {
	grammar   querybuilder.Grammar
	q         contractsorm.Query
	tableName string
}

func newQueryPivotQuery(grammar querybuilder.Grammar, q contractsorm.Query, tableName string) *queryPivotQuery {
	return &queryPivotQuery{grammar: grammar, q: q, tableName: tableName}
}

func (p *queryPivotQuery) qualified(column string) string {
	return p.grammar.Wrap(p.tableName + "." + column)
}

func (p *queryPivotQuery) Where(column string, args ...any) contractsorm.PivotQuery {
	switch len(args) {
	case 1:
		p.q = p.q.Where(fmt.Sprintf("%s = ?", p.qualified(column)), args[0])
	case 2:
		op, ok := args[0].(string)
		if !ok {
			p.q = p.q.Where(fmt.Sprintf("%s = ?", p.qualified(column)), args[0]).
				Where(fmt.Sprintf("%s = ?", p.qualified(column)), args[1])
			return p
		}
		p.q = p.q.Where(fmt.Sprintf("%s %s ?", p.qualified(column), op), args[1])
	}
	return p
}

func (p *queryPivotQuery) WhereIn(column string, values []any) contractsorm.PivotQuery {
	p.q = p.q.Where(fmt.Sprintf("%s IN ?", p.qualified(column)), values)
	return p
}

func (p *queryPivotQuery) WhereNotIn(column string, values []any) contractsorm.PivotQuery {
	p.q = p.q.Where(fmt.Sprintf("%s NOT IN ?", p.qualified(column)), values)
	return p
}

func (p *queryPivotQuery) WhereNull(column string) contractsorm.PivotQuery {
	p.q = p.q.Where(fmt.Sprintf("%s IS NULL", p.qualified(column)))
	return p
}

func (p *queryPivotQuery) WhereNotNull(column string) contractsorm.PivotQuery {
	p.q = p.q.Where(fmt.Sprintf("%s IS NOT NULL", p.qualified(column)))
	return p
}

// applyOnPivotQueryToQuery threads the descriptor's OnPivotQuery callback through q, the
// builder-level read path used by Query.Related's many-to-many read and the eager loader's
// compiled pivot reads. No-op if the callback is nil.
func applyOnPivotQueryToQuery(grammar querybuilder.Grammar, q contractsorm.Query, desc *relationDescriptor) contractsorm.Query {
	if desc.onPivotQuery == nil {
		return q
	}
	pq := newQueryPivotQuery(grammar, q, desc.pivotTable)
	desc.onPivotQuery(pq)
	return pq.q
}
