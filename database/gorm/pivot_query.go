package gorm

import (
	"fmt"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	querybuilder "github.com/gradii/framework/database/query"
)

// queryPivotQuery adapts a contractsorm.Query to the PivotQuery interface so OnPivotQuery
// callbacks can be threaded through builder-level reads (e.g. OrmBuilder.Related). Each Where* call
// is rewritten with the pivot table qualifier before being appended to the underlying OrmBuilder.
type queryPivotQuery struct {
	grammar   querybuilder.Grammar
	q         contractsorm.Query
	tableName string
}

// whereFragment appends one raw pivot predicate, identified with the wrapped table.column name, to
// the underlying query's condition queue. The queue keeps the fragment's rendering — the wrapped
// identifier verbatim and the "col in ?" slice shorthand expanded — which the structured where
// methods cannot take because they would wrap the already-wrapped name a second time.
func (p *queryPivotQuery) whereFragment(fragment string, args ...any) {
	if builder, ok := p.q.(*OrmBuilder); ok {
		builder.addWhere(contractsdriver.Where{Query: fragment, Args: args})

		return
	}

	p.q = p.q.WhereRaw(fragment, args...)
}

func newQueryPivotQuery(grammar querybuilder.Grammar, q contractsorm.Query, tableName string) *queryPivotQuery {
	return &queryPivotQuery{grammar: grammar, q: q, tableName: tableName}
}

func (p *queryPivotQuery) qualified(column string) string {
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
	p.whereFragment(fmt.Sprintf("%s IN ?", p.qualified(column)), values)
	return p
}

func (p *queryPivotQuery) WhereNotIn(column string, values []any) contractsorm.PivotQuery {
	p.whereFragment(fmt.Sprintf("%s NOT IN ?", p.qualified(column)), values)
	return p
}

func (p *queryPivotQuery) WhereNull(column string) contractsorm.PivotQuery {
	p.whereFragment(fmt.Sprintf("%s IS NULL", p.qualified(column)))
	return p
}

func (p *queryPivotQuery) WhereNotNull(column string) contractsorm.PivotQuery {
	p.whereFragment(fmt.Sprintf("%s IS NOT NULL", p.qualified(column)))
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
