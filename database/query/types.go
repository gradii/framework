package query

// Boolean connectors. Negation is carried in the connector, never in a separate flag, exactly as
// Laravel does, so "and not" and "or not" are values of the same field.
const (
	BooleanAnd    = "and"
	BooleanOr     = "or"
	BooleanAndNot = "and not"
	BooleanOrNot  = "or not"
)

// WhereType matches Laravel's $where['type'] strings. The grammar dispatches on the string, so the
// values must match exactly.
type WhereType string

const (
	WhereBasic               WhereType = "Basic"
	WhereBitwise             WhereType = "Bitwise"
	WhereJsonBoolean         WhereType = "JsonBoolean"
	WhereColumnType          WhereType = "Column"
	WhereRawType             WhereType = "Raw"
	WhereInType              WhereType = "In"
	WhereNotInType           WhereType = "NotIn"
	WhereInRawType           WhereType = "InRaw"
	WhereNotInRawType        WhereType = "NotInRaw"
	WhereNullType            WhereType = "Null"
	WhereNotNullType         WhereType = "NotNull"
	WhereBetweenType         WhereType = "between"
	WhereBetweenColsType     WhereType = "betweenColumns"
	WhereValueBetweenType    WhereType = "valueBetween"
	WhereNestedType          WhereType = "Nested"
	WhereSubType             WhereType = "Sub"
	WhereExistsType          WhereType = "Exists"
	WhereNotExistsType       WhereType = "NotExists"
	WhereRowValuesType       WhereType = "RowValues"
	WhereJsonContainsType    WhereType = "JsonContains"
	WhereJsonContainsKeyType WhereType = "JsonContainsKey"
	WhereJsonLengthType      WhereType = "JsonLength"
	WhereJsonOverlapsType    WhereType = "JsonOverlaps"
	WhereFullTextType        WhereType = "Fulltext"
	WhereDateType            WhereType = "Date"
	WhereTimeType            WhereType = "Time"
	WhereDayType             WhereType = "Day"
	WhereMonthType           WhereType = "Month"
	WhereYearType            WhereType = "Year"
	WhereLikeType            WhereType = "Like"
	WhereExpressionType      WhereType = "Expression"
)

// Where is one where descriptor. Laravel stores these as associative arrays holding only the
// fields relevant to the type; Go needs a single struct, so each field names the types that use it.
type Where struct {
	Type WhereType
	// Boolean is the connector: and, or, "and not" or "or not".
	Boolean string

	// Column is used by Basic, Bitwise, JsonBoolean, In, NotIn, InRaw, NotInRaw, Null, NotNull,
	// between, betweenColumns, Sub, JsonContains, JsonContainsKey, JsonLength, JsonOverlaps,
	// the Date family, Like and Expression.
	Column any
	// First and Second are used by Column.
	First  any
	Second any
	// Operator is used by Basic, Bitwise, Column, Sub, JsonLength, RowValues and the Date family.
	Operator string
	// Value is used by Basic, JsonBoolean, JsonLength, JsonContains, JsonOverlaps, valueBetween,
	// Like, Fulltext and the Date family.
	Value any
	// Values is used by In, NotIn, InRaw, NotInRaw, between, betweenColumns and RowValues.
	Values []any
	// Columns is used by valueBetween, RowValues and Fulltext.
	Columns []any

	// Not is used by between, betweenColumns, valueBetween, JsonContains, JsonContainsKey,
	// JsonLength, JsonOverlaps and Like.
	Not bool
	// CaseSensitive is used by Like.
	CaseSensitive bool
	// Options is used by Fulltext.
	Options []string

	// Query is the child builder for Nested, Sub, Exists and NotExists.
	Query *Builder
	// SQL is the raw fragment for the raw type.
	SQL any
}

// HavingType matches Laravel's $having['type'] strings.
type HavingType string

const (
	HavingBasic          HavingType = "Basic"
	HavingRawType        HavingType = "Raw"
	HavingBetweenType    HavingType = "between"
	HavingNullType       HavingType = "Null"
	HavingNotNullType    HavingType = "NotNull"
	HavingBitType        HavingType = "bit"
	HavingExpressionType HavingType = "Expression"
	HavingNestedType     HavingType = "Nested"
)

// Having is one having descriptor.
type Having struct {
	Type     HavingType
	Boolean  string
	Column   any
	Operator string
	Value    any
	Values   []any
	Not      bool
	SQL      any
	Query    *Builder
}

// OrderType matches Laravel's order descriptor: an empty type is a column plus direction, and
// OrderRaw carries a raw fragment.
type OrderType string

const OrderRawType OrderType = "Raw"

// Order is one order descriptor.
type Order struct {
	Type      OrderType
	Column    any
	Direction string
	SQL       any
}

// Union is one union descriptor.
type Union struct {
	Query *Builder
	All   bool
}

// Aggregate is the aggregate function and its columns.
type Aggregate struct {
	Function string
	Columns  []any
}

// UpsertUpdate is one upsert update target. Mirrors the assoc array Laravel passes as $update: a
// bare column means "copy the value from the proposed row", a bound entry means "assign this
// value".
type UpsertUpdate struct {
	// Column is the target column.
	Column string
	// Value is the bound value. It is only meaningful when Bound is true.
	Value any
	// Bound reports whether Value should be bound as a parameter.
	Bound bool
}

// Statement is one compiled SQL statement plus its bindings. Laravel returns a map of sql =>
// bindings from compileTruncate because SQLite truncation needs two statements.
type Statement struct {
	SQL      string
	Bindings []any
}

// UpdateValue is one column assignment of an update statement. Laravel's Builder::update() receives an
// associative array whose insertion order fixes both the SET list and the order of the bound values;
// a Go map cannot carry that order, so callers that care about it pass UpdateValues.
type UpdateValue struct {
	// Column is the column or JSON selector being assigned.
	Column string
	// Value is the value assigned to it.
	Value any
}

// UpdateValues is the ordered list of assignments an update carries. ToUpdateSQL accepts either an
// UpdateValues, which is used exactly as given, or a plain map[string]any, which is ordered by column
// name (the historic behaviour of the map-shaped API this replaced, and the same ksort an insert
// performs). Every grammar iterates the UpdateValues in order, exactly as Laravel's grammars map over
// the update array.
type UpdateValues []UpdateValue

// subQueryBindings carries the bindings a subquery-valued assignment contributes when an update is
// prepared for execution. It is a struct rather than a plain slice so the dialect transforms that
// json-encode array values can tell it apart from a real array value, mirroring Laravel, where the
// same role is played by the bindings closure Builder::update() wraps a query value in.
type subQueryBindings struct {
	values []any
}

// InsertValue is one column/value pair of an insert record. Laravel's Builder::insertGetId() receives
// an associative array whose insertion order fixes the column list and the placeholder order; a Go map
// cannot carry that order, so callers that care about it pass InsertValues.
type InsertValue struct {
	// Column is the column being written.
	Column string
	// Value is the value written to it.
	Value any
}

// InsertValues is the ordered single record an insertGetId carries. ToInsertGetIdSQL accepts either an
// InsertValues, which is used exactly as given, or a []map[string]any, whose columns are ordered by
// name (the historic behaviour of the map-shaped API, and the same ksort an insert performs). Laravel's
// insertGetId() does not ksort a single record, so the ordered form is what reproduces its SQL.
type InsertValues []InsertValue
