package db

import (
	"strings"

	"github.com/gradii/framework/contracts/database/db"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
)

type joinClause struct{ query *Query }

func (j *joinClause) On(first any, args ...string) db.JoinClause {
	return j.on(first, args, core.BooleanAnd)
}
func (j *joinClause) OrOn(first any, args ...string) db.JoinClause {
	return j.on(first, args, core.BooleanOr)
}
func (j *joinClause) on(first any, args []string, boolean string) db.JoinClause {
	q := j.query
	if callback, ok := first.(func(db.JoinClause) db.JoinClause); ok {
		q.builder.WhereNested(func(b *querybuilder.Builder) *querybuilder.Builder {
			child := q.withBuilder(b)
			callback(&joinClause{query: child})
			if child.err != nil {
				b.SetError(child.err)
			}
			return b
		}, boolean)
	} else {
		operator, second, err := columnComparison(args)
		if err != nil {
			q.err = err
		} else {
			q.builder.WhereColumn(first, operator, second, boolean)
		}
	}
	return j
}
func (j *joinClause) Where(query any, args ...any) db.JoinClause {
	j.query.Where(query, args...)
	return j
}

func (j *joinClause) OrWhere(query any, args ...any) db.JoinClause {
	j.query.OrWhere(query, args...)
	return j
}

func (j *joinClause) WhereNot(query any, args ...any) db.JoinClause {
	j.query.WhereNot(query, args...)
	return j
}

func (j *joinClause) OrWhereNot(query any, args ...any) db.JoinClause {
	j.query.OrWhereNot(query, args...)
	return j
}

func (j *joinClause) WhereColumn(column string, columns ...string) db.JoinClause {
	j.query.WhereColumn(column, columns...)
	return j
}

func (j *joinClause) OrWhereColumn(column string, columns ...string) db.JoinClause {
	j.query.OrWhereColumn(column, columns...)
	return j
}

func (j *joinClause) WhereRaw(sql string, bindings []any) db.JoinClause {
	j.query.WhereRaw(sql, bindings)
	return j
}

func (j *joinClause) OrWhereRaw(sql string, bindings []any) db.JoinClause {
	j.query.OrWhereRaw(sql, bindings)
	return j
}

func (j *joinClause) WhereIn(column string, values []any) db.JoinClause {
	j.query.WhereIn(column, values)
	return j
}

func (j *joinClause) OrWhereIn(column string, values []any) db.JoinClause {
	j.query.OrWhereIn(column, values)
	return j
}

func (j *joinClause) WhereNotIn(column string, values []any) db.JoinClause {
	j.query.WhereNotIn(column, values)
	return j
}

func (j *joinClause) OrWhereNotIn(column string, values []any) db.JoinClause {
	j.query.OrWhereNotIn(column, values)
	return j
}

func (j *joinClause) WhereNull(column string) db.JoinClause {
	j.query.WhereNull(column)
	return j
}

func (j *joinClause) OrWhereNull(column string) db.JoinClause {
	j.query.OrWhereNull(column)
	return j
}

func (j *joinClause) WhereNotNull(column string) db.JoinClause {
	j.query.WhereNotNull(column)
	return j
}

func (j *joinClause) OrWhereNotNull(column string) db.JoinClause {
	j.query.OrWhereNotNull(column)
	return j
}

func (j *joinClause) WhereBetween(column string, lower, upper any) db.JoinClause {
	j.query.WhereBetween(column, lower, upper)
	return j
}

func (j *joinClause) OrWhereBetween(column string, lower, upper any) db.JoinClause {
	j.query.OrWhereBetween(column, lower, upper)
	return j
}

func (j *joinClause) WhereNotBetween(column string, lower, upper any) db.JoinClause {
	j.query.WhereNotBetween(column, lower, upper)
	return j
}

func (j *joinClause) OrWhereNotBetween(column string, lower, upper any) db.JoinClause {
	j.query.OrWhereNotBetween(column, lower, upper)
	return j
}

func (j *joinClause) WhereBetweenColumns(column, lower, upper string) db.JoinClause {
	j.query.WhereBetweenColumns(column, lower, upper)
	return j
}

func (j *joinClause) OrWhereBetweenColumns(column, lower, upper string) db.JoinClause {
	j.query.OrWhereBetweenColumns(column, lower, upper)
	return j
}

func (j *joinClause) WhereNotBetweenColumns(column, lower, upper string) db.JoinClause {
	j.query.WhereNotBetweenColumns(column, lower, upper)
	return j
}

func (j *joinClause) OrWhereNotBetweenColumns(column, lower, upper string) db.JoinClause {
	j.query.OrWhereNotBetweenColumns(column, lower, upper)
	return j
}

func (j *joinClause) WhereValueBetween(value any, lower, upper string) db.JoinClause {
	j.query.WhereValueBetween(value, lower, upper)
	return j
}

func (j *joinClause) OrWhereValueBetween(value any, lower, upper string) db.JoinClause {
	j.query.OrWhereValueBetween(value, lower, upper)
	return j
}

func (j *joinClause) WhereValueNotBetween(value any, lower, upper string) db.JoinClause {
	j.query.WhereValueNotBetween(value, lower, upper)
	return j
}

func (j *joinClause) OrWhereValueNotBetween(value any, lower, upper string) db.JoinClause {
	j.query.OrWhereValueNotBetween(value, lower, upper)
	return j
}

func (j *joinClause) WhereIntegerInRaw(column string, values []any) db.JoinClause {
	j.query.WhereIntegerInRaw(column, values)
	return j
}

func (j *joinClause) OrWhereIntegerInRaw(column string, values []any) db.JoinClause {
	j.query.OrWhereIntegerInRaw(column, values)
	return j
}

func (j *joinClause) WhereIntegerNotInRaw(column string, values []any) db.JoinClause {
	j.query.WhereIntegerNotInRaw(column, values)
	return j
}

func (j *joinClause) OrWhereIntegerNotInRaw(column string, values []any) db.JoinClause {
	j.query.OrWhereIntegerNotInRaw(column, values)
	return j
}

func (j *joinClause) WhereRowValues(columns []string, operator string, values []any) db.JoinClause {
	j.query.WhereRowValues(columns, operator, values)
	return j
}

func (j *joinClause) OrWhereRowValues(columns []string, operator string, values []any) db.JoinClause {
	j.query.OrWhereRowValues(columns, operator, values)
	return j
}

func (j *joinClause) WhereJsonOverlaps(column string, value any) db.JoinClause {
	j.query.WhereJsonOverlaps(column, value)
	return j
}

func (j *joinClause) OrWhereJsonOverlaps(column string, value any) db.JoinClause {
	j.query.OrWhereJsonOverlaps(column, value)
	return j
}

func (j *joinClause) WhereJsonDoesntOverlap(column string, value any) db.JoinClause {
	j.query.WhereJsonDoesntOverlap(column, value)
	return j
}

func (j *joinClause) OrWhereJsonDoesntOverlap(column string, value any) db.JoinClause {
	j.query.OrWhereJsonDoesntOverlap(column, value)
	return j
}

func (j *joinClause) WhereFullText(columns []string, value string, options ...string) db.JoinClause {
	j.query.WhereFullText(columns, value, options...)
	return j
}

func (j *joinClause) OrWhereFullText(columns []string, value string, options ...string) db.JoinClause {
	j.query.OrWhereFullText(columns, value, options...)
	return j
}

func (j *joinClause) OrWhereAll(columns []string, args ...any) db.JoinClause {
	j.query.OrWhereAll(columns, args...)
	return j
}

func (j *joinClause) OrWhereAny(columns []string, args ...any) db.JoinClause {
	j.query.OrWhereAny(columns, args...)
	return j
}

func (j *joinClause) OrWhereNone(columns []string, args ...any) db.JoinClause {
	j.query.OrWhereNone(columns, args...)
	return j
}

func (j *joinClause) WhereDate(column string, args ...any) db.JoinClause {
	j.query.WhereDate(column, args...)
	return j
}

func (j *joinClause) OrWhereDate(column string, args ...any) db.JoinClause {
	j.query.OrWhereDate(column, args...)
	return j
}

func (j *joinClause) WhereTime(column string, args ...any) db.JoinClause {
	j.query.WhereTime(column, args...)
	return j
}

func (j *joinClause) OrWhereTime(column string, args ...any) db.JoinClause {
	j.query.OrWhereTime(column, args...)
	return j
}

func (j *joinClause) WhereDay(column string, args ...any) db.JoinClause {
	j.query.WhereDay(column, args...)
	return j
}

func (j *joinClause) OrWhereDay(column string, args ...any) db.JoinClause {
	j.query.OrWhereDay(column, args...)
	return j
}

func (j *joinClause) WhereMonth(column string, args ...any) db.JoinClause {
	j.query.WhereMonth(column, args...)
	return j
}

func (j *joinClause) OrWhereMonth(column string, args ...any) db.JoinClause {
	j.query.OrWhereMonth(column, args...)
	return j
}

func (j *joinClause) WhereYear(column string, args ...any) db.JoinClause {
	j.query.WhereYear(column, args...)
	return j
}

func (j *joinClause) OrWhereYear(column string, args ...any) db.JoinClause {
	j.query.OrWhereYear(column, args...)
	return j
}

func (j *joinClause) WherePast(columns ...string) db.JoinClause {
	j.query.WherePast(columns...)
	return j
}

func (j *joinClause) OrWherePast(columns ...string) db.JoinClause {
	j.query.OrWherePast(columns...)
	return j
}

func (j *joinClause) WhereNowOrPast(columns ...string) db.JoinClause {
	j.query.WhereNowOrPast(columns...)
	return j
}

func (j *joinClause) OrWhereNowOrPast(columns ...string) db.JoinClause {
	j.query.OrWhereNowOrPast(columns...)
	return j
}

func (j *joinClause) WhereFuture(columns ...string) db.JoinClause {
	j.query.WhereFuture(columns...)
	return j
}

func (j *joinClause) OrWhereFuture(columns ...string) db.JoinClause {
	j.query.OrWhereFuture(columns...)
	return j
}

func (j *joinClause) WhereNowOrFuture(columns ...string) db.JoinClause {
	j.query.WhereNowOrFuture(columns...)
	return j
}

func (j *joinClause) OrWhereNowOrFuture(columns ...string) db.JoinClause {
	j.query.OrWhereNowOrFuture(columns...)
	return j
}

func (j *joinClause) WhereBeforeToday(columns ...string) db.JoinClause {
	j.query.WhereBeforeToday(columns...)
	return j
}

func (j *joinClause) OrWhereBeforeToday(columns ...string) db.JoinClause {
	j.query.OrWhereBeforeToday(columns...)
	return j
}

func (j *joinClause) WhereTodayOrBefore(columns ...string) db.JoinClause {
	j.query.WhereTodayOrBefore(columns...)
	return j
}

func (j *joinClause) OrWhereTodayOrBefore(columns ...string) db.JoinClause {
	j.query.OrWhereTodayOrBefore(columns...)
	return j
}

func (j *joinClause) WhereAfterToday(columns ...string) db.JoinClause {
	j.query.WhereAfterToday(columns...)
	return j
}

func (j *joinClause) OrWhereAfterToday(columns ...string) db.JoinClause {
	j.query.OrWhereAfterToday(columns...)
	return j
}

func (j *joinClause) WhereTodayOrAfter(columns ...string) db.JoinClause {
	j.query.WhereTodayOrAfter(columns...)
	return j
}

func (j *joinClause) OrWhereTodayOrAfter(columns ...string) db.JoinClause {
	j.query.OrWhereTodayOrAfter(columns...)
	return j
}

func (j *joinClause) WhereToday(columns ...string) db.JoinClause {
	j.query.WhereToday(columns...)
	return j
}

func (j *joinClause) OrWhereToday(columns ...string) db.JoinClause {
	j.query.OrWhereToday(columns...)
	return j
}

func (j *joinClause) OrWhereExists(query any) db.JoinClause {
	j.query.OrWhereExists(query)
	return j
}

func (j *joinClause) WhereNotExists(query any) db.JoinClause {
	j.query.WhereNotExists(query)
	return j
}

func (j *joinClause) OrWhereNotExists(query any) db.JoinClause {
	j.query.OrWhereNotExists(query)
	return j
}

func (r *Query) joinCondition(first any, args []any) (any, any, any) {
	if callback, ok := first.(func(db.JoinClause) db.JoinClause); ok {
		return func(b *querybuilder.Builder) *querybuilder.Builder {
			child := r.withBuilder(b)
			callback(&joinClause{query: child})
			if child.err != nil {
				b.SetError(child.err)
			}
			return b
		}, nil, nil
	}
	switch len(args) {
	case 1:
		return first, "=", args[0]
	case 2:
		return first, args[0], args[1]
	default:
		if first != nil {
			r.err = errors.DatabaseInvalidArgumentNumber.Args(len(args), "1 or 2")
		}
		return first, nil, nil
	}
}

// join retains the raw-fragment API while accepting Laravel's column and callback forms.
func (r *Query) join(table string, args []any, typ string) db.Query {
	if len(args) > 0 {
		_, callback := args[0].(func(db.JoinClause) db.JoinClause)
		// A raw fragment supplies its own ON/USING condition; its arguments remain bindings.
		raw := strings.Contains(strings.ToLower(table), " on ") || strings.Contains(strings.ToLower(table), " using ") || strings.Contains(table, "?")
		if callback || (!raw && len(args) >= 2) {
			first, operator, second := r.joinCondition(args[0], args[1:])
			r.builder.Join(table, first, operator, second, typ, false)
			return r
		}
	}
	r.builder.JoinRaw(table, args, typ)
	return r
}
func (r *Query) JoinWhere(table, column, operator string, value any) db.Query {
	r.builder.JoinWhere(table, column, operator, value, "inner")
	return r
}
func (r *Query) LeftJoinWhere(table, column, operator string, value any) db.Query {
	r.builder.LeftJoinWhere(table, column, operator, value)
	return r
}
func (r *Query) RightJoinWhere(table, column, operator string, value any) db.Query {
	r.builder.RightJoinWhere(table, column, operator, value)
	return r
}
func (r *Query) joinSub(query any, as string, first any, args []any, typ string) db.Query {
	first, operator, second := r.joinCondition(first, args)
	r.builder.JoinSub(r.subquery(query), as, first, operator, second, typ, false)
	return r
}
func (r *Query) JoinSub(query any, as string, first any, args ...any) db.Query {
	return r.joinSub(query, as, first, args, "inner")
}
func (r *Query) LeftJoinSub(query any, as string, first any, args ...any) db.Query {
	return r.joinSub(query, as, first, args, "left")
}
func (r *Query) RightJoinSub(query any, as string, first any, args ...any) db.Query {
	return r.joinSub(query, as, first, args, "right")
}
