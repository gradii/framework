package db

import (
	"github.com/gradii/framework/contracts/database/db"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
)

func (r *Query) SelectRaw(sql string, bindings ...any) db.Query {
	r.builder.SelectRaw(sql, bindings)
	return r
}

func (r *Query) SelectSub(query any, as string) db.Query {
	r.builder.SelectSub(r.subquery(query), as)
	return r
}

func (r *Query) AddSelect(columns ...any) db.Query {
	r.builder.AddSelect(r.subqueries(columns)...)
	return r
}

func (r *Query) From(table any, alias ...string) db.Query {
	r.builder.From(r.subquery(table), firstString(alias, ""))
	return r
}

func (r *Query) FromSub(query any, as string) db.Query {
	r.builder.FromSub(r.subquery(query), as)
	return r
}

func (r *Query) FromRaw(sql string, bindings ...any) db.Query {
	r.builder.FromRaw(sql, bindings)
	return r
}

func (r *Query) UseIndex(index string) db.Query {
	r.builder.UseIndex(index)
	return r
}

func (r *Query) ForceIndex(index string) db.Query {
	r.builder.ForceIndex(index)
	return r
}

func (r *Query) IgnoreIndex(index string) db.Query {
	r.builder.IgnoreIndex(index)
	return r
}

func (r *Query) JoinLateral(query any, as string) db.Query {
	r.builder.JoinLateral(r.subquery(query), as, "inner")
	return r
}

func (r *Query) LeftJoinLateral(query any, as string) db.Query {
	r.builder.LeftJoinLateral(r.subquery(query), as)
	return r
}

func (r *Query) CrossJoinSub(query any, as string) db.Query {
	r.builder.CrossJoinSub(r.subquery(query), as)
	return r
}

func (r *Query) Union(query any) db.Query {
	r.builder.Union(r.subquery(query), false)
	return r
}

func (r *Query) UnionAll(query any) db.Query {
	r.builder.UnionAll(r.subquery(query))
	return r
}

func (r *Query) GroupByRaw(sql string, bindings ...any) db.Query {
	r.builder.GroupByRaw(sql, bindings)
	return r
}

func (r *Query) OrHaving(column any, args ...any) db.Query {
	r.builder.OrHaving(column, args...)
	return r
}

func (r *Query) HavingRaw(sql string, bindings ...any) db.Query {
	r.builder.HavingRaw(sql, bindings, core.BooleanAnd)
	return r
}

func (r *Query) OrHavingRaw(sql string, bindings ...any) db.Query {
	r.builder.OrHavingRaw(sql, bindings)
	return r
}

func (r *Query) HavingNull(columns ...string) db.Query {
	r.builder.HavingNull(columns, core.BooleanAnd, false)
	return r
}

func (r *Query) HavingNotNull(columns ...string) db.Query {
	r.builder.HavingNotNull(columns, core.BooleanAnd)
	return r
}

func (r *Query) OrHavingNull(columns ...string) db.Query {
	r.builder.OrHavingNull(columns)
	return r
}

func (r *Query) OrHavingNotNull(columns ...string) db.Query {
	r.builder.OrHavingNotNull(columns)
	return r
}

func (r *Query) HavingBetween(column string, lower, upper any) db.Query {
	r.builder.HavingBetween(column, []any{lower, upper}, core.BooleanAnd, false)
	return r
}

func (r *Query) HavingNotBetween(column string, lower, upper any) db.Query {
	r.builder.HavingBetween(column, []any{lower, upper}, core.BooleanAnd, true)
	return r
}

func (r *Query) Reorder(column ...string) db.Query {
	r.builder.Reorder(optionalColumn(column), directionFrom(column))
	return r
}

func (r *Query) ReorderDesc(column string) db.Query {
	r.builder.ReorderDesc(column)
	return r
}

func (r *Query) Oldest(column ...string) db.Query {
	r.builder.Oldest(firstString(column, "created_at"))
	return r
}

func (r *Query) Skip(offset int) db.Query {
	r.builder.Skip(offset)
	return r
}

func (r *Query) Take(limit int) db.Query {
	r.builder.Take(limit)
	return r
}

func (r *Query) GroupLimit(limit int, column string) db.Query {
	r.builder.GroupLimit(limit, column)
	return r
}

func (r *Query) ForPage(page int, perPage ...int) db.Query {
	r.builder.ForPage(page, perPage...)
	return r
}

func (r *Query) ForPageBeforeId(perPage int, lastID any, column ...string) db.Query {
	r.builder.ForPageBeforeId(perPage, lastID, firstString(column, "id"))
	return r
}

func (r *Query) ForPageAfterId(perPage int, lastID any, column ...string) db.Query {
	r.builder.ForPageAfterId(perPage, lastID, firstString(column, "id"))
	return r
}

func (r *Query) Lock(value any) db.Query {
	r.builder.Lock(value)
	return r
}

func (r *Query) WhereBetweenColumns(column, lower, upper string) db.Query {
	r.builder.WhereBetweenColumns(column, []any{lower, upper}, core.BooleanAnd, false)
	return r
}

func (r *Query) OrWhereBetweenColumns(column, lower, upper string) db.Query {
	r.builder.OrWhereBetweenColumns(column, []any{lower, upper})
	return r
}

func (r *Query) WhereNotBetweenColumns(column, lower, upper string) db.Query {
	r.builder.WhereNotBetweenColumns(column, []any{lower, upper}, core.BooleanAnd)
	return r
}

func (r *Query) OrWhereNotBetweenColumns(column, lower, upper string) db.Query {
	r.builder.OrWhereNotBetweenColumns(column, []any{lower, upper})
	return r
}

func (r *Query) WhereValueBetween(value any, lower, upper string) db.Query {
	r.builder.WhereValueBetween(value, []any{lower, upper}, core.BooleanAnd, false)
	return r
}

func (r *Query) OrWhereValueBetween(value any, lower, upper string) db.Query {
	r.builder.OrWhereValueBetween(value, []any{lower, upper})
	return r
}

func (r *Query) WhereValueNotBetween(value any, lower, upper string) db.Query {
	r.builder.WhereValueNotBetween(value, []any{lower, upper}, core.BooleanAnd)
	return r
}

func (r *Query) OrWhereValueNotBetween(value any, lower, upper string) db.Query {
	r.builder.OrWhereValueNotBetween(value, []any{lower, upper})
	return r
}

func (r *Query) WhereIntegerInRaw(column string, values []any) db.Query {
	r.builder.WhereIntegerInRaw(column, values, core.BooleanAnd, false)
	return r
}

func (r *Query) OrWhereIntegerInRaw(column string, values []any) db.Query {
	r.builder.OrWhereIntegerInRaw(column, values)
	return r
}

func (r *Query) WhereIntegerNotInRaw(column string, values []any) db.Query {
	r.builder.WhereIntegerNotInRaw(column, values, core.BooleanAnd)
	return r
}

func (r *Query) OrWhereIntegerNotInRaw(column string, values []any) db.Query {
	r.builder.OrWhereIntegerNotInRaw(column, values)
	return r
}

func (r *Query) WhereRowValues(columns []string, operator string, values []any) db.Query {
	r.builder.WhereRowValues(toStrings(columns), operator, values)
	return r
}

func (r *Query) OrWhereRowValues(columns []string, operator string, values []any) db.Query {
	r.builder.OrWhereRowValues(toStrings(columns), operator, values)
	return r
}

func (r *Query) WhereJsonOverlaps(column string, value any) db.Query {
	r.builder.WhereJsonOverlaps(column, value, core.BooleanAnd, false)
	return r
}

func (r *Query) OrWhereJsonOverlaps(column string, value any) db.Query {
	r.builder.OrWhereJsonOverlaps(column, value)
	return r
}

func (r *Query) WhereJsonDoesntOverlap(column string, value any) db.Query {
	r.builder.WhereJsonDoesntOverlap(column, value, core.BooleanAnd)
	return r
}

func (r *Query) OrWhereJsonDoesntOverlap(column string, value any) db.Query {
	r.builder.OrWhereJsonDoesntOverlap(column, value)
	return r
}

func (r *Query) WhereFullText(columns []string, value string, options ...string) db.Query {
	r.builder.WhereFullText(columns, value, options, core.BooleanAnd)
	return r
}

func (r *Query) OrWhereFullText(columns []string, value string, options ...string) db.Query {
	r.builder.OrWhereFullText(columns, value, options)
	return r
}

func (r *Query) OrWhereAll(columns []string, args ...any) db.Query {
	r.builder.OrWhereAll(toStrings(columns), args...)
	return r
}

func (r *Query) OrWhereAny(columns []string, args ...any) db.Query {
	r.builder.OrWhereAny(toStrings(columns), args...)
	return r
}

func (r *Query) OrWhereNone(columns []string, args ...any) db.Query {
	r.builder.OrWhereNone(toStrings(columns), args...)
	return r
}

func (r *Query) DynamicWhere(method string, parameters ...any) db.Query {
	r.builder.DynamicWhere(method, parameters)
	return r
}

func (r *Query) WhereDate(column string, args ...any) db.Query {
	r.builder.WhereDate(column, args...)
	return r
}

func (r *Query) OrWhereDate(column string, args ...any) db.Query {
	r.builder.OrWhereDate(column, args...)
	return r
}

func (r *Query) WhereTime(column string, args ...any) db.Query {
	r.builder.WhereTime(column, args...)
	return r
}

func (r *Query) OrWhereTime(column string, args ...any) db.Query {
	r.builder.OrWhereTime(column, args...)
	return r
}

func (r *Query) WhereDay(column string, args ...any) db.Query {
	r.builder.WhereDay(column, args...)
	return r
}

func (r *Query) OrWhereDay(column string, args ...any) db.Query {
	r.builder.OrWhereDay(column, args...)
	return r
}

func (r *Query) WhereMonth(column string, args ...any) db.Query {
	r.builder.WhereMonth(column, args...)
	return r
}

func (r *Query) OrWhereMonth(column string, args ...any) db.Query {
	r.builder.OrWhereMonth(column, args...)
	return r
}

func (r *Query) WhereYear(column string, args ...any) db.Query {
	r.builder.WhereYear(column, args...)
	return r
}

func (r *Query) OrWhereYear(column string, args ...any) db.Query {
	r.builder.OrWhereYear(column, args...)
	return r
}

func (r *Query) WherePast(columns ...string) db.Query {
	r.builder.WherePast(columns)
	return r
}

func (r *Query) OrWherePast(columns ...string) db.Query {
	r.builder.OrWherePast(columns)
	return r
}

func (r *Query) WhereNowOrPast(columns ...string) db.Query {
	r.builder.WhereNowOrPast(columns)
	return r
}

func (r *Query) OrWhereNowOrPast(columns ...string) db.Query {
	r.builder.OrWhereNowOrPast(columns)
	return r
}

func (r *Query) WhereFuture(columns ...string) db.Query {
	r.builder.WhereFuture(columns)
	return r
}

func (r *Query) OrWhereFuture(columns ...string) db.Query {
	r.builder.OrWhereFuture(columns)
	return r
}

func (r *Query) WhereNowOrFuture(columns ...string) db.Query {
	r.builder.WhereNowOrFuture(columns)
	return r
}

func (r *Query) OrWhereNowOrFuture(columns ...string) db.Query {
	r.builder.OrWhereNowOrFuture(columns)
	return r
}

func (r *Query) WhereBeforeToday(columns ...string) db.Query {
	r.builder.WhereBeforeToday(columns)
	return r
}

func (r *Query) OrWhereBeforeToday(columns ...string) db.Query {
	r.builder.OrWhereBeforeToday(columns)
	return r
}

func (r *Query) WhereTodayOrBefore(columns ...string) db.Query {
	r.builder.WhereTodayOrBefore(columns)
	return r
}

func (r *Query) OrWhereTodayOrBefore(columns ...string) db.Query {
	r.builder.OrWhereTodayOrBefore(columns)
	return r
}

func (r *Query) WhereAfterToday(columns ...string) db.Query {
	r.builder.WhereAfterToday(columns)
	return r
}

func (r *Query) OrWhereAfterToday(columns ...string) db.Query {
	r.builder.OrWhereAfterToday(columns)
	return r
}

func (r *Query) WhereTodayOrAfter(columns ...string) db.Query {
	r.builder.WhereTodayOrAfter(columns)
	return r
}

func (r *Query) OrWhereTodayOrAfter(columns ...string) db.Query {
	r.builder.OrWhereTodayOrAfter(columns)
	return r
}

func (r *Query) WhereToday(columns ...string) db.Query {
	r.builder.WhereToday(columns, core.BooleanAnd)
	return r
}

func (r *Query) OrWhereToday(columns ...string) db.Query {
	r.builder.OrWhereToday(columns)
	return r
}

func (r *Query) OrWhereExists(query any) db.Query {
	r.builder.WhereExists(r.subquery(query), core.BooleanOr, false)
	return r
}

func (r *Query) WhereNotExists(query any) db.Query {
	r.builder.WhereExists(r.subquery(query), core.BooleanAnd, true)
	return r
}

func (r *Query) OrWhereNotExists(query any) db.Query {
	r.builder.WhereExists(r.subquery(query), core.BooleanOr, true)
	return r
}

func firstString(values []string, fallback string) string {
	if len(values) > 0 {
		return values[0]
	}
	return fallback
}
func optionalColumn(values []string) any {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}
func directionFrom(values []string) string {
	if len(values) > 1 {
		return values[1]
	}
	return "asc"
}

// subquery adapts facade callbacks and queries to the compiler's query interface.
func (r *Query) subquery(value any) any {
	switch query := value.(type) {
	case *Query:
		if query.err != nil {
			r.builder.SetError(query.err)
		}
		return query.builder
	case func(db.Query) db.Query:
		return func(builder *querybuilder.Builder) *querybuilder.Builder {
			child := r.withBuilder(builder)
			query(child)
			if child.err != nil {
				builder.SetError(child.err)
			}
			return builder
		}
	case func(db.Query):
		return r.subquery(func(child db.Query) db.Query {
			query(child)
			return child
		})
	case func() db.Query:
		return r.subquery(query())
	case map[string]any:
		result := make(map[string]any, len(query))
		for key, value := range query {
			result[key] = r.subquery(value)
		}
		return result
	default:
		return value
	}
}
func (r *Query) subqueries(values []any) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = r.subquery(value)
	}
	return result
}

// withBuilder creates a wrapper for Laravel's fresh nested/subquery builder.
func (r *Query) withBuilder(builder *querybuilder.Builder) *Query {
	q := *r
	q.builder, q.err = builder, nil
	return &q
}
func (r *Query) NewQuery() db.Query {
	q := r.withBuilder(r.builder.NewQuery())
	q.readBuilder = r.originalReadBuilder
	q.allowTableless = true
	return q
}
func (r *Query) Clone() db.Query { return r.clone() }
func (r *Query) CloneWithout(components ...string) db.Query {
	q := r.withBuilder(r.builder.CloneWithout(components...))
	q.err = r.err
	return q
}
func (r *Query) CloneWithoutBindings(bindings ...string) db.Query {
	q := r.withBuilder(r.builder.CloneWithoutBindings(bindings...))
	q.err = r.err
	return q
}
func (r *Query) GetBindings() []any               { return r.builder.GetBindings() }
func (r *Query) GetRawBindings() map[string][]any { return r.builder.Clone().Bindings() }
func (r *Query) GetColumns() []string             { return r.builder.GetColumns() }
func (r *Query) GetLimit() *int                   { return r.builder.GetLimit() }
func (r *Query) GetOffset() *int                  { return r.builder.GetOffset() }
func (r *Query) SetBindings(values []any, component string) db.Query {
	if err := r.builder.SetBindings(values, component); err != nil {
		r.err = err
	}
	return r
}
func (r *Query) AddBinding(value any, component string) db.Query {
	if err := r.builder.AddBinding(value, component); err != nil {
		r.err = err
	}
	return r
}
func (r *Query) WhereNested(callback func(db.Query) db.Query) db.Query { return r.Where(callback) }
func (r *Query) HavingNested(callback func(db.Query) db.Query) db.Query {
	r.builder.HavingNested(func(b *querybuilder.Builder) *querybuilder.Builder { return r.nested(callback, b) }, core.BooleanAnd)
	return r
}
func (r *Query) BeforeQuery(callback func(db.Query) db.Query) db.Query {
	r.builder.BeforeQuery(func(b *querybuilder.Builder) *querybuilder.Builder {
		r.nested(callback, b)
		return b
	})
	return r
}
func (r *Query) AfterQuery(callback func(any) any) db.Query {
	r.builder.AfterQuery(callback)
	return r
}
func (r *Query) Tap(callback func(db.Query)) db.Query { callback(r); return r }
func (r *Query) Pipe(callback func(db.Query) any) any { return callback(r) }
func (r *Query) Unless(condition bool, callback func(db.Query) db.Query, otherwise ...func(db.Query) db.Query) db.Query {
	if !condition {
		if result := callback(r); result != nil {
			return result
		}
	} else if len(otherwise) > 0 && otherwise[0] != nil {
		if result := otherwise[0](r); result != nil {
			return result
		}
	}
	return r
}
