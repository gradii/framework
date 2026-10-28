package query

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	contractsquery "github.com/gradii/framework/contracts/database/query"
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// addWhere is the shared implementation of where and orWhere. Laravel reads func_num_args() to
// detect the two-argument shorthand; Go reads len(args) for the same purpose.
func (b *Builder) addWhere(column any, args []any, boolean string) *Builder {
	// Laravel short-circuits a bare `where` descriptor only for a ConditionExpression, a marker
	// interface that Illuminate\Database\Query\Expression does NOT implement (Builder::where()'s
	// `$column instanceof ConditionExpression` branch). A raw Expression used as a column therefore
	// falls through to the ordinary path: with no operator or value it becomes a whereNull, and with
	// one it keeps its operator and value (or, for a "->" selector compared against a bool, the
	// JSON-boolean rewrite). The Go port has no separate ConditionExpression type, so short-circuiting
	// on Expression here swallowed the operator and value of every raw column.
	// An array first argument expands to one clause per entry: associative key/value pairs, or a list
	// of [column, operator, value] entries. Mirrors Builder::where()'s is_array($column) branch.
	if isArrayCondition(column) {
		return b.addArrayOfWheres(column, boolean, "where")
	}

	var operator, value any
	switch len(args) {
	case 0:
	case 1:
		// PHP binds the second positional argument into $operator for the shorthand form and
		// lets prepareValueAndOperator swap it back.
		operator = args[0]
	default:
		operator, value = args[0], args[1]
	}

	value, operator, err := b.PrepareValueAndOperator(value, operator, len(args) == 1)
	if err != nil {
		b.setError(err)
		return b
	}

	// A callback with no operator starts a nested where.
	if callback, ok := column.(func(*Builder) *Builder); ok && operator == nil {
		return b.WhereNested(callback, boolean)
	}

	// A queryable column with an operator compares the subquery result.
	if IsQueryable(column) && operator != nil {
		sub, bindings := b.createSub(column)
		_ = b.AddBinding(bindings, "where")
		return b.addWhere(Expression{Value: "(" + sub + ")"}, []any{operator, value}, boolean)
	}

	// Shortcut for the "=" operator.
	if b.InvalidOperator(operator) {
		value, operator = operator, "="
	}

	// A queryable value is a sub-select compared against the column.
	if IsQueryable(value) {
		return b.WhereSub(column, operator, value, boolean)
	}

	// A nil value becomes a null check.
	if value == nil {
		op, _ := operator.(string)
		return b.WhereNull(column, boolean, !inStrings(op, []string{"=", "<=>"}))
	}

	typ := core.WhereBasic
	columnString := ""
	switch c := column.(type) {
	case string:
		columnString = c
	case Expression:
		if b.grammar != nil {
			columnString = b.grammar.GetValue(c)
		}
	}

	// A JSON reference compared against a bool becomes a raw true or false literal. The caller may
	// hand the bool behind a pointer (a nullable Go bool, e.g. convert.Pointer(false)); dereference
	// it first so it is treated as the JSON boolean Laravel sees via is_bool($value), rather than
	// binding the pointer address and matching nothing.
	if strings.Contains(columnString, "->") {
		booleanValue, isBool := boolValue(value)
		if isBool {
			if booleanValue {
				value = Expression{Value: "true"}
			} else {
				value = Expression{Value: "false"}
			}
			if _, isString := column.(string); isString {
				typ = core.WhereJsonBoolean
			}
		}
	}

	op, _ := operator.(string)

	// A bitwise operator becomes its own clause type. Mirrors Builder::where()'s isBitwiseOperator()
	// branch: the base grammar renders it as a plain comparison, and the dialects that define a
	// whereBitwise cast or compare it.
	if b.IsBitwiseOperator(op) {
		typ = core.WhereBitwise
	}

	b.State().Wheres = append(b.State().Wheres, core.Where{Type: typ, Column: column, Operator: op, Value: value, Boolean: boolean})

	if !IsExpression(value) {
		_ = b.AddBinding(FlattenValue(value), "where")
	}

	return b
}

// boolValue reports the boolean behind a bool or a non-nil *bool. Laravel's where() tests
// is_bool($value); Go callers express a nullable boolean as *bool, so both forms are accepted here.
func boolValue(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case *bool:
		if v != nil {
			return *v, true
		}
	}

	return false, false
}

// Where adds an "and" where clause. Mirrors Builder::where().
func (b *Builder) Where(column any, args ...any) *Builder {
	return b.addWhere(column, args, core.BooleanAnd)
}

// OrWhere adds an "or" where clause. Mirrors Builder::orWhere().
func (b *Builder) OrWhere(column any, args ...any) *Builder {
	return b.addWhere(column, args, core.BooleanOr)
}

// WhereNot adds an "and not" where clause. Mirrors Builder::whereNot(). An array first argument is
// wrapped in the negation: Laravel runs whereNested(..., $boolean.' not') and re-enters where() inside,
// so the nested clauses keep the plain boolean and the negation wraps the whole group.
func (b *Builder) WhereNot(column any, args ...any) *Builder {
	if isArrayCondition(column) {
		return b.WhereNested(func(q *Builder) *Builder {
			return q.addWhere(column, args, core.BooleanAnd)
		}, core.BooleanAndNot)
	}

	return b.addWhere(column, args, core.BooleanAndNot)
}

// OrWhereNot adds an "or not" where clause. Mirrors Builder::orWhereNot(), which is whereNot(...,
// boolean: 'or'): the array form nests under "or not" with the inner clauses joined by "or".
func (b *Builder) OrWhereNot(column any, args ...any) *Builder {
	if isArrayCondition(column) {
		return b.WhereNested(func(q *Builder) *Builder {
			return q.addWhere(column, args, core.BooleanOr)
		}, core.BooleanOrNot)
	}

	return b.addWhere(column, args, core.BooleanOrNot)
}

// WhereColumn compares two columns. Mirrors Builder::whereColumn(). An array first argument is a list
// of column comparisons, expanded through addArrayOfWheres like Laravel's is_array($first) branch.
func (b *Builder) WhereColumn(first any, operator any, second any, boolean string) *Builder {
	if isArrayCondition(first) {
		return b.addArrayOfWheres(first, boolean, "whereColumn")
	}

	if b.InvalidOperator(operator) {
		second, operator = operator, "="
	}

	op, _ := operator.(string)
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereColumnType, First: first, Operator: op, Second: second, Boolean: boolean})

	return b
}

// OrWhereColumn compares two columns with "or". Mirrors Builder::orWhereColumn().
func (b *Builder) OrWhereColumn(first any, operator any, second any) *Builder {
	return b.WhereColumn(first, operator, second, core.BooleanOr)
}

// WhereRaw adds a raw where clause. Mirrors Builder::whereRaw().
func (b *Builder) WhereRaw(sql any, bindings []any, boolean string) *Builder {
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereRawType, SQL: sql, Boolean: boolean})
	_ = b.AddBinding(bindings, "where")

	return b
}

// OrWhereRaw adds a raw where clause with "or". Mirrors Builder::orWhereRaw().
func (b *Builder) OrWhereRaw(sql any, bindings []any) *Builder {
	return b.WhereRaw(sql, bindings, core.BooleanOr)
}

// WhereIn adds an "in" clause. Mirrors Builder::whereIn().
func (b *Builder) WhereIn(column any, values any, boolean string, not bool) *Builder {
	typ := core.WhereInType
	if not {
		typ = core.WhereNotInType
	}

	// A queryable value is a subquery used as the value list.
	if IsQueryable(values) {
		sub, bindings := b.createSub(values)
		values = []any{Expression{Value: sub}}
		_ = b.AddBinding(bindings, "where")
	}

	list := core.ToAnySlice(values)

	b.State().Wheres = append(b.State().Wheres, core.Where{Type: typ, Column: column, Values: list, Boolean: boolean})

	// An untyped nil is PHP's null, not an empty list. Laravel counts $values right after queueing the
	// clause and count(null) is a TypeError, so whereIn('col', null) never compiles on any dialect.
	// Reading it as an empty list compiled the constant "0 = 1", or "1 = 1" for not in, so a value list
	// left unset silently matched nothing, or every row. A nil slice of any element type has length
	// zero, so it is Laravel's empty array and keeps compiling to that constant.
	if values == nil {
		b.setError(errors.QueryNilValueListNotAllowed)

		return b
	}

	// Laravel rejects a nested list whose one-level flatten would change the value count, because such
	// a list cannot be bound positionally; a nested list of single-value arrays is still allowed.
	if len(list) != len(flatten(list, 1)) {
		b.setError(errNestedArraysNotAllowed)

		return b
	}

	_ = b.AddBinding(CleanBindings(list), "where")

	return b
}

// OrWhereIn adds an "in" clause with "or". Mirrors Builder::orWhereIn().
func (b *Builder) OrWhereIn(column any, values any) *Builder {
	return b.WhereIn(column, values, core.BooleanOr, false)
}

// WhereNotIn adds a "not in" clause. Mirrors Builder::whereNotIn().
func (b *Builder) WhereNotIn(column any, values any, boolean string) *Builder {
	return b.WhereIn(column, values, boolean, true)
}

// OrWhereNotIn adds a "not in" clause with "or". Mirrors Builder::orWhereNotIn().
func (b *Builder) OrWhereNotIn(column any, values any) *Builder {
	return b.WhereNotIn(column, values, core.BooleanOr)
}

// WhereIntegerInRaw adds an "in" clause whose values are inlined, never parameterised.
// Mirrors Builder::whereIntegerInRaw().
func (b *Builder) WhereIntegerInRaw(column any, values []any, boolean string, not bool) *Builder {
	typ := core.WhereInRawType
	if not {
		typ = core.WhereNotInRawType
	}

	// Laravel flattens the whole list before casting each value to an integer.
	flat := flatten(values, 0)
	flattened := make([]any, 0, len(flat))
	for _, v := range flat {
		flattened = append(flattened, toInt(v))
	}

	b.State().Wheres = append(b.State().Wheres, core.Where{Type: typ, Column: column, Values: flattened, Boolean: boolean})

	return b
}

// WhereNull adds a "is null" clause. Mirrors Builder::whereNull().
func (b *Builder) WhereNull(columns any, boolean string, not bool) *Builder {
	typ := core.WhereNullType
	if not {
		typ = core.WhereNotNullType
	}

	for _, column := range wrapValues(columns) {
		b.State().Wheres = append(b.State().Wheres, core.Where{Type: typ, Column: column, Boolean: boolean})
	}

	return b
}

// OrWhereNull adds a "is null" clause with "or". Mirrors Builder::orWhereNull().
func (b *Builder) OrWhereNull(column any) *Builder {
	return b.WhereNull(column, core.BooleanOr, false)
}

// WhereNotNull adds a "is not null" clause. Mirrors Builder::whereNotNull().
func (b *Builder) WhereNotNull(columns any, boolean string) *Builder {
	return b.WhereNull(columns, boolean, true)
}

// OrWhereNotNull adds a "is not null" clause with "or". Mirrors Builder::orWhereNotNull().
func (b *Builder) OrWhereNotNull(column any) *Builder {
	return b.WhereNotNull(column, core.BooleanOr)
}

// WhereBetween adds a "between" clause. Mirrors Builder::whereBetween().
func (b *Builder) WhereBetween(column any, values []any, boolean string, not bool) *Builder {
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereBetweenType, Column: column, Values: values, Boolean: boolean, Not: not})

	// Laravel resolves the bounds with Arr::flatten(), so [[1, 2, 3]] binds 1 and 2, not one array.
	bounds := CleanBindings(flatten(values, 0))
	if len(bounds) > 2 {
		bounds = bounds[:2]
	}
	_ = b.AddBinding(bounds, "where")

	return b
}

// OrWhereBetween adds a "between" clause with "or". Mirrors Builder::orWhereBetween().
func (b *Builder) OrWhereBetween(column any, values []any) *Builder {
	return b.WhereBetween(column, values, core.BooleanOr, false)
}

// WhereNotBetween adds a "not between" clause. Mirrors Builder::whereNotBetween().
func (b *Builder) WhereNotBetween(column any, values []any, boolean string) *Builder {
	return b.WhereBetween(column, values, boolean, true)
}

// WhereBetweenColumns compares a column against two other columns.
// Mirrors Builder::whereBetweenColumns().
func (b *Builder) WhereBetweenColumns(column any, values []any, boolean string, not bool) *Builder {
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereBetweenColsType, Column: column, Values: values, Boolean: boolean, Not: not})

	return b
}

// WhereValueBetween compares a value against two columns. Mirrors Builder::whereValueBetween().
func (b *Builder) WhereValueBetween(value any, columns []any, boolean string, not bool) *Builder {
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereValueBetweenType, Value: value, Columns: columns, Boolean: boolean, Not: not})
	_ = b.AddBinding(value, "where")

	return b
}

// addDateBasedWhere stores a date-part comparison. Mirrors Builder::addDateBasedWhere().
func (b *Builder) addDateBasedWhere(typ core.WhereType, column any, operator any, value any, boolean string) *Builder {
	op, _ := operator.(string)
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: typ, Column: column, Operator: op, Value: value, Boolean: boolean})

	if !IsExpression(value) {
		_ = b.AddBinding(value, "where")
	}

	return b
}

// whereDatePart resolves the operator and value then stores the date comparison.
//
// Every date part formats a date-like value with the part's own pattern before it is bound, the way
// Laravel's whereDate ("Y-m-d"), whereTime ("H:i:s"), whereYear ("Y"), whereDay ("d") and whereMonth
// ("m") format a DateTimeInterface. A value that is not date-like (the common int and string cases)
// is left as given, so whereYear(2024) keeps the int 2024 and whereYear("2024") keeps the string.
//
// Day and Month then zero-pad the formatted value to two digits, so whereDay(1) binds "01" and
// whereMonth(5) binds "05"; the padding is scoped to those two parts because whereDate, whereTime and
// whereYear have no such step - a blanket pad here would turn whereYear(2024)'s int into a string. An
// expression is neither formatted nor padded.
func (b *Builder) whereDatePart(typ core.WhereType, column any, args []any, boolean string) *Builder {
	var operator, value any
	switch len(args) {
	case 1:
		operator = args[0]
	default:
		if len(args) >= 2 {
			operator, value = args[0], args[1]
		}
	}

	value, operator, err := b.PrepareValueAndOperator(value, operator, len(args) == 1)
	if err != nil {
		b.setError(err)
		return b
	}

	if b.InvalidOperator(operator) {
		value, operator = operator, "="
	}

	value = FlattenValue(value)

	if format, ok := datePartValueFormat[typ]; ok && !IsExpression(value) {
		value = formatDatePartValue(value, format)

		if typ == core.WhereDayType || typ == core.WhereMonthType {
			value = padDatePart(value)
		}
	}

	return b.addDateBasedWhere(typ, column, operator, value, boolean)
}

// datePartValueFormat holds the pattern each part applies to a date-like value before it is bound:
// "Y-m-d" for Date, "H:i:s" for Time, "Y" for Year, "d" for Day and "m" for Month. It mirrors the
// DateTimeInterface formatting Laravel performs inside whereDate / whereTime / whereYear / whereDay /
// whereMonth. A value the formatting helper does not recognise as date-like is returned unchanged,
// which is what keeps whereYear(2024) binding the int 2024 and whereYear("2024") binding the string.
var datePartValueFormat = map[core.WhereType]string{
	core.WhereDateType:  "Y-m-d",
	core.WhereTimeType:  "H:i:s",
	core.WhereYearType:  "Y",
	core.WhereDayType:   "d",
	core.WhereMonthType: "m",
}

// formatDatePartValue renders a date-like value with the given format. Values that are not date-like
// (the common int and string cases) are returned unchanged.
func formatDatePartValue(value any, format string) any {
	switch v := value.(type) {
	case *carbon.Carbon:
		if v == nil {
			return value
		}
		return v.Format(format)
	case carbon.Carbon:
		return v.Format(format)
	case time.Time:
		return v.Format(phpTimeLayout(format))
	default:
		return value
	}
}

// phpTimeLayout translates the PHP date patterns used by the date-part methods into Go layouts.
func phpTimeLayout(format string) string {
	switch format {
	case "Y-m-d":
		return "2006-01-02"
	case "H:i:s":
		return "15:04:05"
	case "Y":
		return "2006"
	case "d":
		return "02"
	case "m":
		return "01"
	default:
		return format
	}
}

// padDatePart zero-pads a value to two digits with PHP's sprintf("%02d", $value) semantics: the
// value is cast to an int first, so "5abc" becomes 5, "abc" and nil become 0, and a float truncates.
func padDatePart(value any) any {
	return fmt.Sprintf("%02d", toInt(value))
}

// WhereDate compares the date part. Mirrors Builder::whereDate().
func (b *Builder) WhereDate(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereDateType, column, args, core.BooleanAnd)
}

// WhereToday compares the date part with today's date. Mirrors Builder::whereToday().
func (b *Builder) WhereToday(columns any, boolean string) *Builder {
	return b.whereTodayBeforeOrAfter(columns, "=", boolean)
}

// OrWhereToday adds an "or where date = today" clause. Mirrors Builder::orWhereToday().
func (b *Builder) OrWhereToday(columns any) *Builder {
	return b.WhereToday(columns, core.BooleanOr)
}

// whereTodayBeforeOrAfter stores one Date comparison per column against today's date. Mirrors
// Builder::whereTodayBeforeOrAfter(): the value is today's date in "Y-m-d" form, formatted from the
// clock Laravel reads through Carbon::today()->format('Y-m-d'); each column becomes its own clause
// joined by boolean.
func (b *Builder) whereTodayBeforeOrAfter(columns any, operator string, boolean string) *Builder {
	value := carbon.Now().ToDateString()

	for _, column := range core.ToAnySlice(columns) {
		b.addDateBasedWhere(core.WhereDateType, column, operator, value, boolean)
	}

	return b
}

// WherePast adds a where clause testing whether a date column is in the past. Mirrors
// Builder::wherePast(): the "<" operator, joined to the previous predicate by "and".
func (b *Builder) WherePast(columns any) *Builder {
	return b.wherePastOrFuture(columns, "<", core.BooleanAnd)
}

// OrWherePast adds an "or where" clause testing whether a date column is in the past. Mirrors
// Builder::orWherePast().
func (b *Builder) OrWherePast(columns any) *Builder {
	return b.wherePastOrFuture(columns, "<", core.BooleanOr)
}

// WhereFuture adds a where clause testing whether a date column is in the future. Mirrors
// Builder::whereFuture(): the ">" operator, joined to the previous predicate by "and". It shares
// wherePastOrFuture with the past family.
func (b *Builder) WhereFuture(columns any) *Builder {
	return b.wherePastOrFuture(columns, ">", core.BooleanAnd)
}

// OrWhereFuture adds an "or where" clause testing whether a date column is in the future. Mirrors
// Builder::orWhereFuture().
func (b *Builder) OrWhereFuture(columns any) *Builder {
	return b.wherePastOrFuture(columns, ">", core.BooleanOr)
}

// wherePastOrFuture stores one Basic comparison per column against the current timestamp. Mirrors
// Builder::wherePastOrFuture(): unlike the "today" family it neither formats the value nor wraps the
// column in date(); it pushes a plain "Basic" descriptor carrying Carbon::now() (the full timestamp)
// and binds that value once per column. The operator and boolean are supplied by the caller
// (wherePast/orWherePast), and the value is read once so every column shares the same instant.
func (b *Builder) wherePastOrFuture(columns any, operator string, boolean string) *Builder {
	value := carbon.Now()

	for _, column := range core.ToAnySlice(columns) {
		b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereBasic, Column: column, Operator: operator, Value: value, Boolean: boolean})
		_ = b.AddBinding(value, "where")
	}

	return b
}

// WhereTime compares the time part. Mirrors Builder::whereTime().
func (b *Builder) WhereTime(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereTimeType, column, args, core.BooleanAnd)
}

// OrWhereTime adds an "or where time" statement. Mirrors Builder::orWhereTime().
func (b *Builder) OrWhereTime(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereTimeType, column, args, core.BooleanOr)
}

// WhereDay compares the day of month. Mirrors Builder::whereDay().
func (b *Builder) WhereDay(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereDayType, column, args, core.BooleanAnd)
}

// OrWhereDay adds an "or where day" statement. Mirrors Builder::orWhereDay().
func (b *Builder) OrWhereDay(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereDayType, column, args, core.BooleanOr)
}

// WhereMonth compares the month. Mirrors Builder::whereMonth().
func (b *Builder) WhereMonth(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereMonthType, column, args, core.BooleanAnd)
}

// OrWhereMonth adds an "or where month" statement. Mirrors Builder::orWhereMonth().
func (b *Builder) OrWhereMonth(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereMonthType, column, args, core.BooleanOr)
}

// WhereYear compares the year. Mirrors Builder::whereYear().
func (b *Builder) WhereYear(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereYearType, column, args, core.BooleanAnd)
}

// OrWhereYear adds an "or where year" statement. Mirrors Builder::orWhereYear().
func (b *Builder) OrWhereYear(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereYearType, column, args, core.BooleanOr)
}

// WhereLike adds a "like" clause. Mirrors Builder::whereLike().
func (b *Builder) WhereLike(column any, value string, caseSensitive bool, boolean string, not bool) *Builder {
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereLikeType, Column: column, Value: value, CaseSensitive: caseSensitive, Boolean: boolean, Not: not})

	if b.grammar != nil {
		value = b.grammar.PrepareWhereLikeBinding(value, caseSensitive)
	}
	_ = b.AddBinding(value, "where")

	return b
}

// WhereNotLike adds a "not like" clause. Mirrors Builder::whereNotLike().
func (b *Builder) WhereNotLike(column any, value string, caseSensitive bool, boolean string) *Builder {
	return b.WhereLike(column, value, caseSensitive, boolean, true)
}

// WhereRowValues compares tuples. Mirrors Builder::whereRowValues().
func (b *Builder) WhereRowValues(columns []any, operator string, values []any, boolean ...string) *Builder {
	if len(columns) != len(values) {
		b.setError(errRowValuesCountMismatch)
		return b
	}

	boolVal := core.BooleanAnd
	if len(boolean) > 0 {
		boolVal = boolean[0]
	}

	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereRowValuesType, Columns: columns, Operator: operator, Values: values, Boolean: boolVal})
	_ = b.AddBinding(CleanBindings(values), "where")

	return b
}

// OrWhereRowValues adds an "or" tuple comparison. Mirrors Builder::orWhereRowValues().
func (b *Builder) OrWhereRowValues(columns []any, operator string, values []any) *Builder {
	return b.WhereRowValues(columns, operator, values, core.BooleanOr)
}

// WhereJsonContains adds a JSON contains clause. Mirrors Builder::whereJsonContains().
func (b *Builder) WhereJsonContains(column string, value any, boolean string, not bool) *Builder {
	var binding any
	if !IsExpression(value) {
		var err error
		if preparer, ok := b.grammar.(contractsquery.JsonContainsPreparer); ok {
			value, binding, err = preparer.PrepareJsonContains(CastBinding(value))
		} else {
			value, err = core.NormalizeJSONValue(CastBinding(value))
			if err == nil && b.grammar != nil {
				binding = b.grammar.PrepareBindingForJsonContains(value)
			}
		}
		if err != nil {
			b.setError(err)
			return b
		}
	}
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereJsonContainsType, Column: column, Value: value, Boolean: boolean, Not: not})

	if !IsExpression(value) && b.grammar != nil {
		_ = b.AddBinding(binding, "where")
	}

	return b
}

// OrWhereJsonContains adds a JSON "contains" clause with "or". Mirrors Builder::orWhereJsonContains().
func (b *Builder) OrWhereJsonContains(column string, value any) *Builder {
	return b.WhereJsonContains(column, value, core.BooleanOr, false)
}

// WhereJsonDoesntContain adds a JSON "doesn't contain" clause.
// Mirrors Builder::whereJsonDoesntContain().
func (b *Builder) WhereJsonDoesntContain(column string, value any, boolean string) *Builder {
	return b.WhereJsonContains(column, value, boolean, true)
}

// OrWhereJsonDoesntContain adds a JSON "doesn't contain" clause with "or".
// Mirrors Builder::orWhereJsonDoesntContain().
func (b *Builder) OrWhereJsonDoesntContain(column string, value any) *Builder {
	return b.WhereJsonDoesntContain(column, value, core.BooleanOr)
}

// WhereJsonOverlaps adds a JSON overlaps clause. Mirrors Builder::whereJsonOverlaps().
func (b *Builder) WhereJsonOverlaps(column string, value any, boolean string, not bool) *Builder {
	if !IsExpression(value) {
		var err error
		value, err = core.NormalizeJSONValue(CastBinding(value))
		if err != nil {
			b.setError(err)
			return b
		}
	}
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereJsonOverlapsType, Column: column, Value: value, Boolean: boolean, Not: not})

	if !IsExpression(value) && b.grammar != nil {
		_ = b.AddBinding(b.grammar.PrepareBindingForJsonContains(value), "where")
	}

	return b
}

// WhereJsonContainsKey adds a JSON key existence clause. Mirrors Builder::whereJsonContainsKey().
func (b *Builder) WhereJsonContainsKey(column string, boolean string, not bool) *Builder {
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereJsonContainsKeyType, Column: column, Boolean: boolean, Not: not})

	return b
}

// OrWhereJsonContainsKey adds a JSON key existence clause with "or". Mirrors Builder::orWhereJsonContainsKey().
func (b *Builder) OrWhereJsonContainsKey(column string) *Builder {
	return b.WhereJsonContainsKey(column, core.BooleanOr, false)
}

// WhereJsonDoesntContainKey adds a clause that determines if a JSON path does not exist.
// Mirrors Builder::whereJsonDoesntContainKey().
func (b *Builder) WhereJsonDoesntContainKey(column string, boolean string) *Builder {
	return b.WhereJsonContainsKey(column, boolean, true)
}

// OrWhereJsonDoesntContainKey adds an "or" clause that determines if a JSON path does not exist.
// Mirrors Builder::orWhereJsonDoesntContainKey().
func (b *Builder) OrWhereJsonDoesntContainKey(column string) *Builder {
	return b.WhereJsonDoesntContainKey(column, core.BooleanOr)
}

// OrWhereJsonLength compares a JSON array length with "or". Mirrors Builder::orWhereJsonLength().
func (b *Builder) OrWhereJsonLength(column string, args ...any) *Builder {
	return b.whereJsonLength(column, args, core.BooleanOr)
}

// WhereJsonLength compares a JSON array length. Mirrors Builder::whereJsonLength().
func (b *Builder) WhereJsonLength(column string, args ...any) *Builder {
	return b.whereJsonLength(column, args, core.BooleanAnd)
}

func (b *Builder) whereJsonLength(column string, args []any, boolean string) *Builder {
	var operator, value any
	switch len(args) {
	case 1:
		operator = args[0]
	default:
		if len(args) >= 2 {
			operator, value = args[0], args[1]
		}
	}

	value, operator, err := b.PrepareValueAndOperator(value, operator, len(args) == 1)
	if err != nil {
		b.setError(err)
		return b
	}
	if b.InvalidOperator(operator) {
		value, operator = operator, "="
	}

	op, _ := operator.(string)
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereJsonLengthType, Column: column, Operator: op, Value: value, Boolean: boolean})

	if !IsExpression(value) {
		_ = b.AddBinding(toInt(FlattenValue(value)), "where")
	}

	return b
}

// WhereFullText adds a fulltext clause. Mirrors Builder::whereFullText().
func (b *Builder) WhereFullText(columns any, value string, options []string, boolean string) *Builder {
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereFullTextType, Columns: wrapValues(columns), Value: value, Options: options, Boolean: boolean})
	_ = b.AddBinding(value, "where")

	return b
}

// WhereAll nests one condition repeated across columns joined by and. Mirrors Builder::whereAll().
func (b *Builder) WhereAll(columns []any, operator any, value any, boolean string) *Builder {
	return b.WhereNested(func(q *Builder) *Builder {
		for _, column := range columns {
			q.addWhere(column, []any{operator, value}, core.BooleanAnd)
		}
		return q
	}, boolean)
}

// OrWhereAll nests one condition repeated across columns joined by and, queued with "or".
// Mirrors Builder::orWhereAll(), which is whereAll(..., boolean: 'or'): the columns, operator and
// value are handed through unchanged, only the connector differs.
func (b *Builder) OrWhereAll(columns []any, args ...any) *Builder {
	// Laravel's orWhereAll($columns, $operator = null, $value = null) fixes the argument positions:
	// a lone extra argument is the operator (the "=" shorthand is resolved downstream, in addWhere,
	// exactly as where() does it), and a second is the value. The closure form passes no arguments,
	// leaving both nil so each callback becomes its own nested where.
	var operator, value any
	switch len(args) {
	case 0:
	case 1:
		operator = args[0]
	default:
		operator, value = args[0], args[1]
	}

	return b.WhereAll(columns, operator, value, core.BooleanOr)
}

// WhereAny nests one condition repeated across columns joined by or. Mirrors Builder::whereAny().
func (b *Builder) WhereAny(columns []any, operator any, value any, boolean string) *Builder {
	return b.WhereNested(func(q *Builder) *Builder {
		for _, column := range columns {
			q.addWhere(column, []any{operator, value}, core.BooleanOr)
		}
		return q
	}, boolean)
}

// WhereNone nests one condition repeated across columns, negated. Mirrors Builder::whereNone().
func (b *Builder) WhereNone(columns []any, operator any, value any, boolean string) *Builder {
	return b.WhereAny(columns, operator, value, boolean+" not")
}

// OrWhereNone nests one condition repeated across columns, negated, queued with "or".
// Mirrors Builder::orWhereNone(), which is whereNone(..., boolean: 'or') and so whereAny(...,
// boolean: 'or not'): the columns, operator and value pass through unchanged, only the connector
// differs, and the queued group is negated while its inner clauses stay joined by "or".
func (b *Builder) OrWhereNone(columns []any, args ...any) *Builder {
	// Laravel's orWhereNone($columns, $operator = null, $value = null) fixes the argument positions:
	// a lone extra argument is the operator (the "=" shorthand is resolved downstream, in addWhere,
	// exactly as where() does it), and a second is the value. The closure form passes no arguments,
	// leaving both nil so each callback becomes its own nested where.
	var operator, value any
	switch len(args) {
	case 0:
	case 1:
		operator = args[0]
	default:
		operator, value = args[0], args[1]
	}

	return b.WhereNone(columns, operator, value, core.BooleanOr)
}

// OrWhereAny nests one condition repeated across columns joined by or, queued with "or".
// Mirrors Builder::orWhereAny(), which is whereAny(..., boolean: 'or'): the columns, operator and
// value are handed through unchanged, only the connector differs.
func (b *Builder) OrWhereAny(columns []any, args ...any) *Builder {
	// Laravel's orWhereAny($columns, $operator = null, $value = null) fixes the argument positions:
	// a lone extra argument is the operator (the "=" shorthand is resolved downstream, in addWhere,
	// exactly as where() does it), and a second is the value. The closure form passes no arguments,
	// leaving both nil so each callback becomes its own nested where.
	var operator, value any
	switch len(args) {
	case 0:
	case 1:
		operator = args[0]
	default:
		operator, value = args[0], args[1]
	}

	return b.WhereAny(columns, operator, value, core.BooleanOr)
}

// isArrayCondition reports whether a where's first argument is an array of wheres: a map of key/value
// pairs, or a list of [column, operator, value] entries. Mirrors the is_array($column) checks Laravel
// makes in where() and whereColumn().
func isArrayCondition(column any) bool {
	switch column.(type) {
	case map[string]any, []any:
		return true
	}

	return false
}

// addArrayOfWheres expands an array first argument into one nested where holding a clause per entry.
// Mirrors Builder::addArrayOfWheres().
//
// Laravel walks a PHP array in insertion order: a numeric key whose value is an array is splatted into
// the target method as ($column, $operator, $value, boolean: $boolean), and any other entry is a
// key/value equality pair. Go has no ordered map, so the port accepts either shape: a []any of ordered
// [column, operator, value] pairs (the corpus's numeric form, and the only way to fix the clause order
// in Go), or a map[string]any for the associative form. A Go map has no iteration order, so its
// entries are walked in sorted-key order to stay deterministic; pass the ordered-pair form when the
// clause order matters.
func (b *Builder) addArrayOfWheres(column any, boolean string, method string) *Builder {
	return b.WhereNested(func(q *Builder) *Builder {
		switch values := column.(type) {
		case map[string]any:
			for _, key := range core.SortedKeys(values) {
				q.addArrayWhere(key, []any{"=", values[key]}, method, boolean)
			}
		case []any:
			for _, value := range values {
				if pair, ok := value.([]any); ok && len(pair) > 0 {
					q.addArrayWhere(pair[0], pair[1:], method, boolean)
				}
			}
		}

		return q
	}, boolean)
}

// addArrayWhere adds one entry of an array of wheres. Laravel splats the entry's values into the target
// method as ($column, $operator, $value, boolean: $boolean); args is the operator/value tail after the
// column, so a two-element entry is the "=" shorthand and a three-element entry carries its operator.
func (b *Builder) addArrayWhere(column any, args []any, method string, boolean string) *Builder {
	if method == "whereColumn" {
		return b.whereColumnArgs(column, args, boolean)
	}

	return b.addWhere(column, args, boolean)
}

// whereColumnArgs maps the tail of an array-of-wheres entry onto Builder::whereColumn's ($first,
// $operator, $second) arguments: a single value is $second under the default operator, two values are
// $operator and $second.
func (b *Builder) whereColumnArgs(column any, args []any, boolean string) *Builder {
	var operator, second any
	if len(args) > 0 {
		operator = args[0]
	}
	if len(args) > 1 {
		second = args[1]
	}

	return b.WhereColumn(column, operator, second, boolean)
}

// WhereNested starts a nested where. Mirrors Builder::whereNested().
func (b *Builder) WhereNested(callback func(*Builder) *Builder, boolean string) *Builder {
	query := b.ForNestedWhere()
	callback(query)

	return b.AddNestedWhereQuery(query, boolean)
}

// ForNestedWhere creates a nested builder seeded with the parent's from.
// Mirrors Builder::forNestedWhere().
func (b *Builder) ForNestedWhere() *Builder {
	nested := b.NewQuery()
	nested.State().From = b.State().From

	return nested
}

// AddNestedWhereQuery adds a builder as a nested where. Mirrors Builder::addNestedWhereQuery().
func (b *Builder) AddNestedWhereQuery(query *Builder, boolean string) *Builder {
	if len(query.State().Wheres) > 0 {
		b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereNestedType, Query: query, Boolean: boolean})
		_ = b.AddBinding(query.State().Bindings["where"], "where")
	}

	return b
}

// WhereExists adds an exists clause. Mirrors Builder::whereExists().
func (b *Builder) WhereExists(callback any, boolean string, not bool) *Builder {
	var query *Builder
	if cb, ok := callback.(func(*Builder) *Builder); ok {
		query = b.ForSubQuery()
		cb(query)
	} else if existing, ok := callback.(*Builder); ok {
		query = existing
	}

	return b.AddWhereExistsQuery(query, boolean, not)
}

// OrWhereExists adds an exists clause with "or". Mirrors Builder::orWhereExists().
func (b *Builder) OrWhereExists(callback any) *Builder {
	return b.WhereExists(callback, core.BooleanOr, false)
}

// WhereNotExists adds a "not exists" clause. Mirrors Builder::whereNotExists().
func (b *Builder) WhereNotExists(callback any, boolean string) *Builder {
	return b.WhereExists(callback, boolean, true)
}

// AddWhereExistsQuery adds a builder as an exists clause. Mirrors Builder::addWhereExistsQuery().
func (b *Builder) AddWhereExistsQuery(query *Builder, boolean string, not bool) *Builder {
	typ := core.WhereExistsType
	if not {
		typ = core.WhereNotExistsType
	}

	b.State().Wheres = append(b.State().Wheres, core.Where{Type: typ, Query: query, Boolean: boolean})

	if query != nil {
		_ = b.AddBinding(query.GetBindings(), "where")
	}

	return b
}

// WhereSub compares a column against a sub-select. Mirrors Builder::whereSub().
func (b *Builder) WhereSub(column any, operator any, callback any, boolean string) *Builder {
	var query *Builder
	if cb, ok := callback.(func(*Builder) *Builder); ok {
		query = b.ForSubQuery()
		cb(query)
	} else if existing, ok := callback.(*Builder); ok {
		query = existing
	}

	op, _ := operator.(string)
	b.State().Wheres = append(b.State().Wheres, core.Where{Type: core.WhereSubType, Column: column, Operator: op, Query: query, Boolean: boolean})

	if query != nil {
		_ = b.AddBinding(query.GetBindings(), "where")
	}

	return b
}

// createSub resolves a callback or builder into SQL and bindings.
// Mirrors Builder::createSub().
func (b *Builder) createSub(query any) (string, []any) {
	if cb, ok := query.(func(*Builder) *Builder); ok {
		sub := b.ForSubQuery()
		cb(sub)
		query = sub
	}

	return b.parseSub(query)
}

// parseSub flattens a subquery into SQL and bindings. Mirrors Builder::parseSub().
func (b *Builder) parseSub(query any) (string, []any) {
	switch q := query.(type) {
	case *Builder:
		return q.ToSQL(), q.GetBindings()
	case string:
		return q, nil
	default:
		b.setError(errSubqueryTypeInvalid)
		return "", nil
	}
}

// dynamicWhereConnector matches the "(And|Or)(?=[A-Z])" delimiters PHP's preg_split captures while
// splitting a dynamic method name: the connector word is kept as its own segment. Go's regexp has no
// lookahead, so the pattern consumes the following uppercase letter as a second group and the scan
// resumes right after the connector, letting that letter open the next segment.
var dynamicWhereConnector = regexp.MustCompile(`(And|Or)([A-Z])`)

// DynamicWhere handles dynamic "where" clauses for a magic whereXxx method, such as
// whereFooBarAndBazOrQux. Mirrors Builder::dynamicWhere() (Builder.php:2245). In PHP this is reached
// through Builder::__call() with the whole method name; Go has no magic dispatch, so callers pass the
// full method name (including the leading "where") and its parameters. Like Laravel it drops the first
// five characters, then walks the And/Or-delimited segments, adding one equality clause per column.
func (b *Builder) DynamicWhere(method string, parameters []any) *Builder {
	finder := ""
	if len(method) > 5 {
		finder = method[5:]
	}

	connector := core.BooleanAnd
	index := 0

	for _, segment := range splitDynamicWhere(finder) {
		if segment != "And" && segment != "Or" {
			b.addDynamic(segment, connector, parameters, index)
			index++
		} else {
			connector = strings.ToLower(segment)
		}
	}

	return b
}

// addDynamic adds one dynamic where clause. Mirrors Builder::addDynamic() (Builder.php:2290): the
// segment becomes a snake-cased column compared for equality against parameters[index], joined by the
// lowercased connector. Laravel calls where($column, '=', $value, $bool); the port's where() fixes the
// boolean, so the clause is pushed through addWhere directly.
func (b *Builder) addDynamic(segment string, connector string, parameters []any, index int) *Builder {
	var value any
	if index < len(parameters) {
		value = parameters[index]
	}

	return b.addWhere(laravelSnake(segment), []any{"=", value}, strings.ToLower(connector))
}

// splitDynamicWhere splits a dynamic method's tail on the And/Or connectors that are followed by an
// uppercase letter, keeping the connectors as their own segments. It mirrors the preg_split in
// Builder::dynamicWhere(): because Go's regexp has no lookahead the pattern consumes the uppercase
// letter, so the next search resumes at the end of the connector rather than after the whole match.
func splitDynamicWhere(finder string) []string {
	var segments []string
	last := 0

	for search := 0; search < len(finder); {
		loc := dynamicWhereConnector.FindStringSubmatchIndex(finder[search:])
		if loc == nil {
			break
		}

		matchStart := search + loc[0]
		delimEnd := search + loc[3]

		segments = append(segments, finder[last:matchStart], finder[search+loc[2]:search+loc[3]])

		last = delimEnd
		search = delimEnd
	}

	return append(segments, finder[last:])
}
