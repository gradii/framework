package db

// JoinClause carries the column comparisons and value predicates of a join.
type JoinClause interface {
	On(first any, args ...string) JoinClause
	OrOn(first any, args ...string) JoinClause
	Where(query any, args ...any) JoinClause
	OrWhere(query any, args ...any) JoinClause
	WhereNot(query any, args ...any) JoinClause
	OrWhereNot(query any, args ...any) JoinClause
	WhereColumn(column string, columns ...string) JoinClause
	OrWhereColumn(column string, columns ...string) JoinClause
	WhereRaw(sql string, bindings []any) JoinClause
	OrWhereRaw(sql string, bindings []any) JoinClause
	WhereIn(column string, values []any) JoinClause
	OrWhereIn(column string, values []any) JoinClause
	WhereNotIn(column string, values []any) JoinClause
	OrWhereNotIn(column string, values []any) JoinClause
	WhereNull(column string) JoinClause
	OrWhereNull(column string) JoinClause
	WhereNotNull(column string) JoinClause
	OrWhereNotNull(column string) JoinClause
	WhereBetween(column string, lower, upper any) JoinClause
	OrWhereBetween(column string, lower, upper any) JoinClause
	WhereNotBetween(column string, lower, upper any) JoinClause
	OrWhereNotBetween(column string, lower, upper any) JoinClause
	WhereBetweenColumns(column, lower, upper string) JoinClause
	OrWhereBetweenColumns(column, lower, upper string) JoinClause
	WhereNotBetweenColumns(column, lower, upper string) JoinClause
	OrWhereNotBetweenColumns(column, lower, upper string) JoinClause
	WhereValueBetween(value any, lower, upper string) JoinClause
	OrWhereValueBetween(value any, lower, upper string) JoinClause
	WhereValueNotBetween(value any, lower, upper string) JoinClause
	OrWhereValueNotBetween(value any, lower, upper string) JoinClause
	WhereIntegerInRaw(column string, values []any) JoinClause
	OrWhereIntegerInRaw(column string, values []any) JoinClause
	WhereIntegerNotInRaw(column string, values []any) JoinClause
	OrWhereIntegerNotInRaw(column string, values []any) JoinClause
	WhereRowValues(columns []string, operator string, values []any) JoinClause
	OrWhereRowValues(columns []string, operator string, values []any) JoinClause
	WhereJsonOverlaps(column string, value any) JoinClause
	OrWhereJsonOverlaps(column string, value any) JoinClause
	WhereJsonDoesntOverlap(column string, value any) JoinClause
	OrWhereJsonDoesntOverlap(column string, value any) JoinClause
	WhereFullText(columns []string, value string, options ...string) JoinClause
	OrWhereFullText(columns []string, value string, options ...string) JoinClause
	OrWhereAll(columns []string, args ...any) JoinClause
	OrWhereAny(columns []string, args ...any) JoinClause
	OrWhereNone(columns []string, args ...any) JoinClause
	WhereDate(column string, args ...any) JoinClause
	OrWhereDate(column string, args ...any) JoinClause
	WhereTime(column string, args ...any) JoinClause
	OrWhereTime(column string, args ...any) JoinClause
	WhereDay(column string, args ...any) JoinClause
	OrWhereDay(column string, args ...any) JoinClause
	WhereMonth(column string, args ...any) JoinClause
	OrWhereMonth(column string, args ...any) JoinClause
	WhereYear(column string, args ...any) JoinClause
	OrWhereYear(column string, args ...any) JoinClause
	WherePast(columns ...string) JoinClause
	OrWherePast(columns ...string) JoinClause
	WhereNowOrPast(columns ...string) JoinClause
	OrWhereNowOrPast(columns ...string) JoinClause
	WhereFuture(columns ...string) JoinClause
	OrWhereFuture(columns ...string) JoinClause
	WhereNowOrFuture(columns ...string) JoinClause
	OrWhereNowOrFuture(columns ...string) JoinClause
	WhereBeforeToday(columns ...string) JoinClause
	OrWhereBeforeToday(columns ...string) JoinClause
	WhereTodayOrBefore(columns ...string) JoinClause
	OrWhereTodayOrBefore(columns ...string) JoinClause
	WhereAfterToday(columns ...string) JoinClause
	OrWhereAfterToday(columns ...string) JoinClause
	WhereTodayOrAfter(columns ...string) JoinClause
	OrWhereTodayOrAfter(columns ...string) JoinClause
	WhereToday(columns ...string) JoinClause
	OrWhereToday(columns ...string) JoinClause
	OrWhereExists(query any) JoinClause
	WhereNotExists(query any) JoinClause
	OrWhereNotExists(query any) JoinClause
}
