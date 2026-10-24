package query

import (
	"github.com/goravel/framework/database/query/core"
	"github.com/goravel/framework/errors"
)

// LeftJoinWhere mirrors Laravel's leftJoinWhere.
func (b *Builder) LeftJoinWhere(table, first, operator, second any) *Builder {
	return b.JoinWhere(table, first, operator, second, "left")
}

// RightJoinWhere mirrors Laravel's rightJoinWhere.
func (b *Builder) RightJoinWhere(table, first, operator, second any) *Builder {
	return b.JoinWhere(table, first, operator, second, "right")
}

// OrWhereLike mirrors Laravel's orWhereLike.
func (b *Builder) OrWhereLike(column any, value string, caseSensitive bool) *Builder {
	return b.WhereLike(column, value, caseSensitive, core.BooleanOr, false)
}

// OrWhereNotLike mirrors Laravel's orWhereNotLike.
func (b *Builder) OrWhereNotLike(column any, value string, caseSensitive bool) *Builder {
	return b.WhereLike(column, value, caseSensitive, core.BooleanOr, true)
}

// OrWhereIntegerInRaw mirrors Laravel's orWhereIntegerInRaw.
func (b *Builder) OrWhereIntegerInRaw(column any, values []any) *Builder {
	return b.WhereIntegerInRaw(column, values, core.BooleanOr, false)
}

// WhereIntegerNotInRaw mirrors Laravel's whereIntegerNotInRaw.
func (b *Builder) WhereIntegerNotInRaw(column any, values []any, boolean string) *Builder {
	return b.WhereIntegerInRaw(column, values, boolean, true)
}

// OrWhereIntegerNotInRaw mirrors Laravel's orWhereIntegerNotInRaw.
func (b *Builder) OrWhereIntegerNotInRaw(column any, values []any) *Builder {
	return b.WhereIntegerInRaw(column, values, core.BooleanOr, true)
}

// OrWhereBetweenColumns mirrors Laravel's orWhereBetweenColumns.
func (b *Builder) OrWhereBetweenColumns(column any, values []any) *Builder {
	return b.WhereBetweenColumns(column, values, core.BooleanOr, false)
}

// WhereNotBetweenColumns mirrors Laravel's whereNotBetweenColumns.
func (b *Builder) WhereNotBetweenColumns(column any, values []any, boolean string) *Builder {
	return b.WhereBetweenColumns(column, values, boolean, true)
}

// OrWhereNotBetween mirrors Laravel's orWhereNotBetween.
func (b *Builder) OrWhereNotBetween(column any, values []any) *Builder {
	return b.WhereBetween(column, values, core.BooleanOr, true)
}

// OrWhereNotBetweenColumns mirrors Laravel's orWhereNotBetweenColumns.
func (b *Builder) OrWhereNotBetweenColumns(column any, values []any) *Builder {
	return b.WhereBetweenColumns(column, values, core.BooleanOr, true)
}

// OrWhereValueBetween mirrors Laravel's orWhereValueBetween.
func (b *Builder) OrWhereValueBetween(value any, columns []any) *Builder {
	return b.WhereValueBetween(value, columns, core.BooleanOr, false)
}

// WhereValueNotBetween mirrors Laravel's whereValueNotBetween.
func (b *Builder) WhereValueNotBetween(value any, columns []any, boolean string) *Builder {
	return b.WhereValueBetween(value, columns, boolean, true)
}

// OrWhereValueNotBetween mirrors Laravel's orWhereValueNotBetween.
func (b *Builder) OrWhereValueNotBetween(value any, columns []any) *Builder {
	return b.WhereValueBetween(value, columns, core.BooleanOr, true)
}

// OrWhereDate mirrors Laravel's orWhereDate.
func (b *Builder) OrWhereDate(column any, args ...any) *Builder {
	return b.whereDatePart(core.WhereDateType, column, args, core.BooleanOr)
}

// OrWhereNotExists mirrors Laravel's orWhereNotExists.
func (b *Builder) OrWhereNotExists(query any) *Builder {
	return b.WhereExists(query, core.BooleanOr, true)
}

// OrWhereJsonOverlaps mirrors Laravel's orWhereJsonOverlaps.
func (b *Builder) OrWhereJsonOverlaps(column string, value any) *Builder {
	return b.WhereJsonOverlaps(column, value, core.BooleanOr, false)
}

// WhereJsonDoesntOverlap mirrors Laravel's whereJsonDoesntOverlap.
func (b *Builder) WhereJsonDoesntOverlap(column string, value any, boolean string) *Builder {
	return b.WhereJsonOverlaps(column, value, boolean, true)
}

// OrWhereJsonDoesntOverlap mirrors Laravel's orWhereJsonDoesntOverlap.
func (b *Builder) OrWhereJsonDoesntOverlap(column string, value any) *Builder {
	return b.WhereJsonOverlaps(column, value, core.BooleanOr, true)
}

// OrWhereFullText mirrors Laravel's orWhereFullText.
func (b *Builder) OrWhereFullText(columns any, value string, options []string) *Builder {
	return b.WhereFullText(columns, value, options, core.BooleanOr)
}

// OrHavingNull mirrors Laravel's orHavingNull.
func (b *Builder) OrHavingNull(columns any) *Builder {
	return b.HavingNull(columns, core.BooleanOr, false)
}

// OrHavingNotNull mirrors Laravel's orHavingNotNull.
func (b *Builder) OrHavingNotNull(columns any) *Builder {
	return b.HavingNull(columns, core.BooleanOr, true)
}

// ReorderDesc mirrors Laravel's reorderDesc.
func (b *Builder) ReorderDesc(column any) *Builder {
	return b.Reorder(column, "desc")
}

// WhereNowOrPast mirrors Laravel's whereNowOrPast.
func (b *Builder) WhereNowOrPast(columns any) *Builder {
	return b.wherePastOrFuture(columns, "<=", core.BooleanAnd)
}

// OrWhereNowOrPast mirrors Laravel's orWhereNowOrPast.
func (b *Builder) OrWhereNowOrPast(columns any) *Builder {
	return b.wherePastOrFuture(columns, "<=", core.BooleanOr)
}

// WhereNowOrFuture mirrors Laravel's whereNowOrFuture.
func (b *Builder) WhereNowOrFuture(columns any) *Builder {
	return b.wherePastOrFuture(columns, ">=", core.BooleanAnd)
}

// OrWhereNowOrFuture mirrors Laravel's orWhereNowOrFuture.
func (b *Builder) OrWhereNowOrFuture(columns any) *Builder {
	return b.wherePastOrFuture(columns, ">=", core.BooleanOr)
}

// WhereBeforeToday mirrors Laravel's whereBeforeToday.
func (b *Builder) WhereBeforeToday(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, "<", core.BooleanAnd)
}

// OrWhereBeforeToday mirrors Laravel's orWhereBeforeToday.
func (b *Builder) OrWhereBeforeToday(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, "<", core.BooleanOr)
}

// WhereTodayOrBefore mirrors Laravel's whereTodayOrBefore.
func (b *Builder) WhereTodayOrBefore(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, "<=", core.BooleanAnd)
}

// OrWhereTodayOrBefore mirrors Laravel's orWhereTodayOrBefore.
func (b *Builder) OrWhereTodayOrBefore(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, "<=", core.BooleanOr)
}

// WhereAfterToday mirrors Laravel's whereAfterToday.
func (b *Builder) WhereAfterToday(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, ">", core.BooleanAnd)
}

// OrWhereAfterToday mirrors Laravel's orWhereAfterToday.
func (b *Builder) OrWhereAfterToday(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, ">", core.BooleanOr)
}

// WhereTodayOrAfter mirrors Laravel's whereTodayOrAfter.
func (b *Builder) WhereTodayOrAfter(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, ">=", core.BooleanAnd)
}

// OrWhereTodayOrAfter mirrors Laravel's orWhereTodayOrAfter.
func (b *Builder) OrWhereTodayOrAfter(columns any) *Builder {
	return b.whereTodayBeforeOrAfter(columns, ">=", core.BooleanOr)
}

func (b *Builder) GetColumns() []string {
	columns := make([]string, 0, len(b.State().Columns))
	for _, column := range b.State().Columns {
		columns = append(columns, core.GetValueString(column))
	}
	return columns
}

func (b *Builder) GetLimit() *int {
	if value, ok := b.LimitValue(); ok {
		return &value
	}
	return nil
}

func (b *Builder) GetOffset() *int {
	if value, ok := b.OffsetValue(); ok {
		return &value
	}
	return nil
}

func (b *Builder) GetRawBindings() map[string][]any { return b.Bindings() }
func (b *Builder) GetGrammar() Grammar              { return b.Grammar() }
func (b *Builder) GetProcessor() Processor          { return b.Processor() }
func (b *Builder) Raw(value string) Expression      { return Raw(value) }

// AfterQuery registers a result transformation. Like Laravel, callbacks survive execution
// and a false-like callback result retains the preceding result.
func (b *Builder) AfterQuery(callback func(any) any) *Builder {
	b.afterQueryCallbacks = append(b.afterQueryCallbacks, callback)
	return b
}

func (b *Builder) ApplyAfterQueryCallbacks(result any) any {
	for _, callback := range b.afterQueryCallbacks {
		if replacement := callback(result); truthy(replacement) {
			result = replacement
		}
	}
	return result
}

func (b *Builder) HasAfterQueryCallbacks() bool { return len(b.afterQueryCallbacks) > 0 }

// DecrementEach validates amounts in Laravel's order, then preserves ordered assignments
// and the overriding extra values just as incrementEach does.
func (b *Builder) DecrementEach(columns any, extra ...core.UpdateValue) *Builder {
	var entries core.UpdateValues
	switch values := columns.(type) {
	case core.UpdateValues:
		entries = values
	case []core.UpdateValue:
		entries = core.UpdateValues(values)
	default:
		var associative bool
		entries, associative = mapEntries(columns)
		if !associative {
			values, list := arrayValues(columns)
			if !list || len(values) > 0 {
				if list && !isNumericValue(values[0]) {
					b.setError(errors.QueryDecrementAmountNonNumeric.Args(0))
				} else {
					b.setError(errors.QueryDecrementEachNonAssociative)
				}
				return b
			}
		}
	}
	rewritten := make(core.UpdateValues, len(entries))
	for i, entry := range entries {
		if !isNumericValue(entry.Value) {
			b.setError(errors.QueryDecrementAmountNonNumeric.Args(entry.Column))
			return b
		}
		rewritten[i] = core.UpdateValue{Column: entry.Column, Value: Raw(b.grammar.Wrap(entry.Column) + " - " + amountString(entry.Value))}
	}
	b.updateValues = mergeUpdateValues(rewritten, extra)
	return b
}

// ToUpdateFromSQL is PostgreSQL's explicit UPDATE ... FROM path.
func (b *Builder) ToUpdateFromSQL(values any) (string, []any) {
	grammar, ok := b.grammar.(interface {
		CompileUpdateFrom(GrammarQuery, core.UpdateValues) string
		PrepareBindingsForUpdateFrom(map[string][]any, core.UpdateValues) []any
	})
	if !ok {
		b.setError(errors.QueryUpdateFromNotSupported)
		return "", nil
	}
	b.ApplyBeforeQueryCallbacks()
	ordered := updateValuesFrom(values)
	return grammar.CompileUpdateFrom(b, ordered), CleanBindings(grammar.PrepareBindingsForUpdateFrom(b.Bindings(), ordered))
}
