package grammars

import "github.com/goravel/framework/database/query/core"

// Compatibility aliases preserve existing grammar consumers. Shared definitions
// and value helpers are owned by core; new consumers can import that package directly.
type (
	Query            = core.Query
	Join             = core.Join
	State            = core.State
	Expression       = core.Expression
	IndexHint        = core.IndexHint
	WhereType        = core.WhereType
	Where            = core.Where
	HavingType       = core.HavingType
	Having           = core.Having
	OrderType        = core.OrderType
	Order            = core.Order
	Union            = core.Union
	Aggregate        = core.Aggregate
	UpsertUpdate     = core.UpsertUpdate
	Statement        = core.Statement
	UpdateValue      = core.UpdateValue
	UpdateValues     = core.UpdateValues
	SubQueryBindings = core.SubQueryBindings
	InsertValue      = core.InsertValue
	InsertValues     = core.InsertValues
)

const (
	BooleanAnd               = core.BooleanAnd
	BooleanOr                = core.BooleanOr
	BooleanAndNot            = core.BooleanAndNot
	BooleanOrNot             = core.BooleanOrNot
	WhereBasic               = core.WhereBasic
	WhereBitwise             = core.WhereBitwise
	WhereJsonBoolean         = core.WhereJsonBoolean
	WhereColumnType          = core.WhereColumnType
	WhereRawType             = core.WhereRawType
	WhereInType              = core.WhereInType
	WhereNotInType           = core.WhereNotInType
	WhereInRawType           = core.WhereInRawType
	WhereNotInRawType        = core.WhereNotInRawType
	WhereNullType            = core.WhereNullType
	WhereNotNullType         = core.WhereNotNullType
	WhereBetweenType         = core.WhereBetweenType
	WhereBetweenColsType     = core.WhereBetweenColsType
	WhereValueBetweenType    = core.WhereValueBetweenType
	WhereNestedType          = core.WhereNestedType
	WhereSubType             = core.WhereSubType
	WhereExistsType          = core.WhereExistsType
	WhereNotExistsType       = core.WhereNotExistsType
	WhereRowValuesType       = core.WhereRowValuesType
	WhereJsonContainsType    = core.WhereJsonContainsType
	WhereJsonContainsKeyType = core.WhereJsonContainsKeyType
	WhereJsonLengthType      = core.WhereJsonLengthType
	WhereJsonOverlapsType    = core.WhereJsonOverlapsType
	WhereFullTextType        = core.WhereFullTextType
	WhereDateType            = core.WhereDateType
	WhereTimeType            = core.WhereTimeType
	WhereDayType             = core.WhereDayType
	WhereMonthType           = core.WhereMonthType
	WhereYearType            = core.WhereYearType
	WhereLikeType            = core.WhereLikeType
	WhereExpressionType      = core.WhereExpressionType
	HavingBasic              = core.HavingBasic
	HavingRawType            = core.HavingRawType
	HavingBetweenType        = core.HavingBetweenType
	HavingNullType           = core.HavingNullType
	HavingNotNullType        = core.HavingNotNullType
	HavingBitType            = core.HavingBitType
	HavingExpressionType     = core.HavingExpressionType
	HavingNestedType         = core.HavingNestedType
	OrderRawType             = core.OrderRawType
	DialectMySQL             = core.DialectMySQL
	DialectPostgres          = core.DialectPostgres
	DialectSqlite            = core.DialectSqlite
	DialectSqlServer         = core.DialectSqlServer
)

var BindingTypes = core.BindingTypes

func Raw(value string) Expression               { return core.Raw(value) }
func IsExpression(value any) bool               { return core.IsExpression(value) }
func ExpressionValue(value any) string          { return core.ExpressionValue(value) }
func SortedKeys(values map[string]any) []string { return core.SortedKeys(values) }
func AsString(value any) string                 { return core.AsString(value) }
func GetValueString(value any) string           { return core.GetValueString(value) }
func ToAnySlice(value any) []any                { return core.ToAnySlice(value) }
func RenderLiteral(value any) string            { return core.RenderLiteral(value) }
func CanonicalDialect(driver string) string     { return core.CanonicalDialect(driver) }
func ReplacePlaceholders(statement, driver string) string {
	return core.ReplacePlaceholders(statement, driver)
}
