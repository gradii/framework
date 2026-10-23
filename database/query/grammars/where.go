package grammars

import (
	"fmt"
	"strings"
)

// CompileWheres mirrors Grammar::compileWheres().
func (g *BaseGrammar) CompileWheres(b Query) string {
	if len(b.State().Wheres) == 0 {
		return ""
	}

	d := g.dialect()
	parts := make([]string, 0, len(b.State().Wheres))

	for _, where := range b.State().Wheres {
		parts = append(parts, where.Boolean+" "+d.CompileWhere(b, where))
	}

	conjunction := "where"
	if b.State().IsJoinClause {
		conjunction = "on"
	}

	return conjunction + " " + removeLeadingBoolean(strings.Join(parts, " "))
}

// CompileWhere dispatches on the descriptor type. Mirrors Grammar::compileWheresToArray(), which
// builds the method name as "where{$where['type']}".
func (g *BaseGrammar) CompileWhere(b Query, where Where) string {
	d := g.dialect()

	switch where.Type {
	case WhereRawType:
		return d.WhereRaw(b, where)
	case WhereBasic:
		return d.WhereBasic(b, where)
	case WhereBitwise:
		return d.WhereBitwise(b, where)
	case WhereLikeType:
		return d.WhereLike(b, where)
	case WhereInType:
		return d.WhereIn(b, where)
	case WhereNotInType:
		return d.WhereNotIn(b, where)
	case WhereInRawType:
		return d.WhereInRaw(b, where)
	case WhereNotInRawType:
		return d.WhereNotInRaw(b, where)
	case WhereNullType:
		return d.WhereNull(b, where)
	case WhereNotNullType:
		return d.WhereNotNull(b, where)
	case WhereBetweenType:
		return d.WhereBetween(b, where)
	case WhereBetweenColsType:
		return d.WhereBetweenColumns(b, where)
	case WhereValueBetweenType:
		return d.WhereValueBetween(b, where)
	case WhereDateType:
		return d.WhereDate(b, where)
	case WhereTimeType:
		return d.WhereTime(b, where)
	case WhereDayType:
		return d.WhereDay(b, where)
	case WhereMonthType:
		return d.WhereMonth(b, where)
	case WhereYearType:
		return d.WhereYear(b, where)
	case WhereColumnType:
		return d.WhereColumn(b, where)
	case WhereNestedType:
		return d.WhereNested(b, where)
	case WhereSubType:
		return d.WhereSub(b, where)
	case WhereExistsType:
		return d.WhereExists(b, where)
	case WhereNotExistsType:
		return d.WhereNotExists(b, where)
	case WhereRowValuesType:
		return d.WhereRowValues(b, where)
	case WhereJsonBoolean:
		return d.WhereJsonBoolean(b, where)
	case WhereJsonContainsType:
		return d.WhereJsonContains(b, where)
	case WhereJsonOverlapsType:
		return d.WhereJsonOverlaps(b, where)
	case WhereJsonContainsKeyType:
		return d.WhereJsonContainsKey(b, where)
	case WhereJsonLengthType:
		return d.WhereJsonLength(b, where)
	case WhereFullTextType:
		return d.WhereFullText(b, where)
	case WhereExpressionType:
		return d.WhereExpression(b, where)
	default:
		return ""
	}
}

// WhereRaw mirrors Grammar::whereRaw().
func (g *BaseGrammar) WhereRaw(b Query, where Where) string {
	if IsExpression(where.SQL) {
		return ExpressionValue(where.SQL)
	}

	return fmt.Sprint(where.SQL)
}

// WhereBasic mirrors Grammar::whereBasic().
func (g *BaseGrammar) WhereBasic(b Query, where Where) string {
	value := g.dialect().Parameter(where.Value)
	operator := strings.ReplaceAll(where.Operator, "?", "??")

	return g.dialect().Wrap(where.Column) + " " + operator + " " + value
}

// WhereBitwise mirrors Grammar::whereBitwise(), which delegates to whereBasic.
func (g *BaseGrammar) WhereBitwise(b Query, where Where) string {
	return g.dialect().WhereBasic(b, where)
}

// WhereLike mirrors Grammar::whereLike().
func (g *BaseGrammar) WhereLike(b Query, where Where) string {
	if where.CaseSensitive {
		b.SetError(errCaseSensitiveLikeNotSupported)
		return ""
	}

	operator := "like"
	if where.Not {
		operator = "not like"
	}

	basic := where
	basic.Operator = operator

	return g.dialect().WhereBasic(b, basic)
}

// WhereIn mirrors Grammar::whereIn().
func (g *BaseGrammar) WhereIn(b Query, where Where) string {
	if len(where.Values) > 0 {
		return g.dialect().Wrap(where.Column) + " in (" + g.dialect().Parameterize(where.Values) + ")"
	}

	return "0 = 1"
}

// WhereNotIn mirrors Grammar::whereNotIn().
func (g *BaseGrammar) WhereNotIn(b Query, where Where) string {
	if len(where.Values) > 0 {
		return g.dialect().Wrap(where.Column) + " not in (" + g.dialect().Parameterize(where.Values) + ")"
	}

	return "1 = 1"
}

// WhereInRaw mirrors Grammar::whereInRaw().
func (g *BaseGrammar) WhereInRaw(b Query, where Where) string {
	if len(where.Values) > 0 {
		return g.dialect().Wrap(where.Column) + " in (" + joinAny(where.Values) + ")"
	}

	return "0 = 1"
}

// WhereNotInRaw mirrors Grammar::whereNotInRaw().
func (g *BaseGrammar) WhereNotInRaw(b Query, where Where) string {
	if len(where.Values) > 0 {
		return g.dialect().Wrap(where.Column) + " not in (" + joinAny(where.Values) + ")"
	}

	return "1 = 1"
}

// WhereNull mirrors Grammar::whereNull().
func (g *BaseGrammar) WhereNull(b Query, where Where) string {
	return g.dialect().Wrap(where.Column) + " is null"
}

// WhereNotNull mirrors Grammar::whereNotNull().
func (g *BaseGrammar) WhereNotNull(b Query, where Where) string {
	return g.dialect().Wrap(where.Column) + " is not null"
}

// WhereBetween mirrors Grammar::whereBetween().
func (g *BaseGrammar) WhereBetween(b Query, where Where) string {
	between := "between"
	if where.Not {
		between = "not between"
	}

	min, max := firstAndLast(where.Values)

	return g.dialect().Wrap(where.Column) + " " + between + " " + g.dialect().Parameter(min) + " and " + g.dialect().Parameter(max)
}

// WhereBetweenColumns mirrors Grammar::whereBetweenColumns().
func (g *BaseGrammar) WhereBetweenColumns(b Query, where Where) string {
	between := "between"
	if where.Not {
		between = "not between"
	}

	min, max := firstAndLast(where.Values)

	return g.dialect().Wrap(where.Column) + " " + between + " " + g.dialect().Wrap(min) + " and " + g.dialect().Wrap(max)
}

// WhereValueBetween mirrors Grammar::whereValueBetween().
func (g *BaseGrammar) WhereValueBetween(b Query, where Where) string {
	between := "between"
	if where.Not {
		between = "not between"
	}

	min, max := firstAndLast(where.Columns)

	return g.dialect().Parameter(where.Value) + " " + between + " " + g.dialect().Wrap(min) + " and " + g.dialect().Wrap(max)
}

// dateBasedWhere mirrors Grammar::dateBasedWhere().
func (g *BaseGrammar) dateBasedWhere(typ string, where Where) string {
	return typ + "(" + g.dialect().Wrap(where.Column) + ") " + where.Operator + " " + g.dialect().Parameter(where.Value)
}

// WhereDate mirrors Grammar::whereDate().
func (g *BaseGrammar) WhereDate(b Query, where Where) string {
	return g.dateBasedWhere("date", where)
}

// WhereTime mirrors Grammar::whereTime().
func (g *BaseGrammar) WhereTime(b Query, where Where) string {
	return g.dateBasedWhere("time", where)
}

// WhereDay mirrors Grammar::whereDay().
func (g *BaseGrammar) WhereDay(b Query, where Where) string { return g.dateBasedWhere("day", where) }

// WhereMonth mirrors Grammar::whereMonth().
func (g *BaseGrammar) WhereMonth(b Query, where Where) string {
	return g.dateBasedWhere("month", where)
}

// WhereYear mirrors Grammar::whereYear().
func (g *BaseGrammar) WhereYear(b Query, where Where) string {
	return g.dateBasedWhere("year", where)
}

// WhereColumn mirrors Grammar::whereColumn().
func (g *BaseGrammar) WhereColumn(b Query, where Where) string {
	return g.dialect().Wrap(where.First) + " " + where.Operator + " " + g.dialect().Wrap(where.Second)
}

// WhereNested mirrors Grammar::whereNested(). The child compiles to "where ..." or "on ...", so the
// leading keyword is stripped by its length.
func (g *BaseGrammar) WhereNested(b Query, where Where) string {
	if where.Query == nil {
		return ""
	}

	compiled := g.dialect().CompileWheres(where.Query)

	offset := 6
	if where.Query.State().IsJoinClause {
		offset = 3
	}

	if len(compiled) <= offset {
		return "(" + compiled + ")"
	}

	return "(" + compiled[offset:] + ")"
}

// WhereSub mirrors Grammar::whereSub().
func (g *BaseGrammar) WhereSub(b Query, where Where) string {
	if where.Query == nil {
		return ""
	}

	return g.dialect().Wrap(where.Column) + " " + where.Operator + " (" + g.dialect().CompileSelect(where.Query) + ")"
}

// WhereExists mirrors Grammar::whereExists().
func (g *BaseGrammar) WhereExists(b Query, where Where) string {
	if where.Query == nil {
		return ""
	}

	return "exists (" + g.dialect().CompileSelect(where.Query) + ")"
}

// WhereNotExists mirrors Grammar::whereNotExists().
func (g *BaseGrammar) WhereNotExists(b Query, where Where) string {
	if where.Query == nil {
		return ""
	}

	return "not exists (" + g.dialect().CompileSelect(where.Query) + ")"
}

// WhereRowValues mirrors Grammar::whereRowValues().
func (g *BaseGrammar) WhereRowValues(b Query, where Where) string {
	return "(" + g.dialect().Columnize(where.Columns) + ") " + where.Operator + " (" + g.dialect().Parameterize(where.Values) + ")"
}

// WhereJsonBoolean mirrors Grammar::whereJsonBoolean(). The base grammar has no JSON selector of its
// own, so wrapping the column comes back empty; Laravel's base grammar throws "this database engine
// does not support JSON operations" there, and the port records the same capability error on the
// builder, exactly as the JSON contains, key and length compilers do.
func (g *BaseGrammar) WhereJsonBoolean(b Query, where Where) string {
	column := g.dialect().WrapJsonBooleanSelector(AsString(where.Column))
	if column == "" {
		b.SetError(errJsonSelectorNotSupported)
		return ""
	}

	value := g.dialect().WrapJsonBooleanValue(g.dialect().Parameter(where.Value))

	return column + " " + where.Operator + " " + value
}

// WhereJsonContains mirrors Grammar::whereJsonContains().
func (g *BaseGrammar) WhereJsonContains(b Query, where Where) string {
	compiled := g.dialect().CompileJsonContains(AsString(where.Column), g.dialect().Parameter(where.Value))
	if compiled == "" {
		b.SetError(errJsonContainsNotSupported)
		return ""
	}

	not := ""
	if where.Not {
		not = "not "
	}

	return not + compiled
}

// WhereJsonOverlaps mirrors Grammar::whereJsonOverlaps().
func (g *BaseGrammar) WhereJsonOverlaps(b Query, where Where) string {
	compiled := g.dialect().CompileJsonOverlaps(AsString(where.Column), g.dialect().Parameter(where.Value))
	if compiled == "" {
		b.SetError(errJsonOverlapsNotSupported)
		return ""
	}

	not := ""
	if where.Not {
		not = "not "
	}

	return not + compiled
}

// WhereJsonContainsKey mirrors Grammar::whereJsonContainsKey().
func (g *BaseGrammar) WhereJsonContainsKey(b Query, where Where) string {
	compiled := g.dialect().CompileJsonContainsKey(AsString(where.Column))
	if compiled == "" {
		b.SetError(errJsonContainsKeyNotSupported)
		return ""
	}

	not := ""
	if where.Not {
		not = "not "
	}

	return not + compiled
}

// WhereJsonLength mirrors Grammar::whereJsonLength().
func (g *BaseGrammar) WhereJsonLength(b Query, where Where) string {
	compiled := g.dialect().CompileJsonLength(AsString(where.Column), where.Operator, g.dialect().Parameter(where.Value))
	if compiled == "" {
		b.SetError(errJsonLengthNotSupported)
		return ""
	}

	return compiled
}

// WhereFullText mirrors Grammar::whereFullText(). The base refuses.
func (g *BaseGrammar) WhereFullText(b Query, where Where) string {
	b.SetError(errFullTextNotSupported)
	return ""
}

// WhereExpression mirrors Grammar::whereExpression().
func (g *BaseGrammar) WhereExpression(b Query, where Where) string {
	if expr, ok := where.Column.(Expression); ok {
		return g.dialect().GetValue(expr)
	}

	return fmt.Sprint(where.Column)
}
