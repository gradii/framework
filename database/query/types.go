package query

import "github.com/goravel/framework/database/query/grammars"

type (
	WhereType        = grammars.WhereType
	Where            = grammars.Where
	HavingType       = grammars.HavingType
	Having           = grammars.Having
	OrderType        = grammars.OrderType
	Order            = grammars.Order
	Union            = grammars.Union
	Aggregate        = grammars.Aggregate
	UpsertUpdate     = grammars.UpsertUpdate
	Statement        = grammars.Statement
	UpdateValue      = grammars.UpdateValue
	UpdateValues     = grammars.UpdateValues
	subQueryBindings = grammars.SubQueryBindings
	InsertValue      = grammars.InsertValue
	InsertValues     = grammars.InsertValues
)

const (
	OrderRawType             = grammars.OrderRawType
	BooleanAnd               = grammars.BooleanAnd
	BooleanOr                = grammars.BooleanOr
	BooleanAndNot            = grammars.BooleanAndNot
	BooleanOrNot             = grammars.BooleanOrNot
	WhereBasic               = grammars.WhereBasic
	WhereBitwise             = grammars.WhereBitwise
	WhereJsonBoolean         = grammars.WhereJsonBoolean
	WhereColumnType          = grammars.WhereColumnType
	WhereRawType             = grammars.WhereRawType
	WhereInType              = grammars.WhereInType
	WhereNotInType           = grammars.WhereNotInType
	WhereInRawType           = grammars.WhereInRawType
	WhereNotInRawType        = grammars.WhereNotInRawType
	WhereNullType            = grammars.WhereNullType
	WhereNotNullType         = grammars.WhereNotNullType
	WhereBetweenType         = grammars.WhereBetweenType
	WhereBetweenColsType     = grammars.WhereBetweenColsType
	WhereValueBetweenType    = grammars.WhereValueBetweenType
	WhereNestedType          = grammars.WhereNestedType
	WhereSubType             = grammars.WhereSubType
	WhereExistsType          = grammars.WhereExistsType
	WhereNotExistsType       = grammars.WhereNotExistsType
	WhereRowValuesType       = grammars.WhereRowValuesType
	WhereJsonContainsType    = grammars.WhereJsonContainsType
	WhereJsonContainsKeyType = grammars.WhereJsonContainsKeyType
	WhereJsonLengthType      = grammars.WhereJsonLengthType
	WhereJsonOverlapsType    = grammars.WhereJsonOverlapsType
	WhereFullTextType        = grammars.WhereFullTextType
	WhereDateType            = grammars.WhereDateType
	WhereTimeType            = grammars.WhereTimeType
	WhereDayType             = grammars.WhereDayType
	WhereMonthType           = grammars.WhereMonthType
	WhereYearType            = grammars.WhereYearType
	WhereLikeType            = grammars.WhereLikeType
	WhereExpressionType      = grammars.WhereExpressionType
	HavingBasic              = grammars.HavingBasic
	HavingRawType            = grammars.HavingRawType
	HavingBetweenType        = grammars.HavingBetweenType
	HavingNullType           = grammars.HavingNullType
	HavingNotNullType        = grammars.HavingNotNullType
	HavingBitType            = grammars.HavingBitType
	HavingExpressionType     = grammars.HavingExpressionType
	HavingNestedType         = grammars.HavingNestedType
)
