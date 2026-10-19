package query

// Expression is a raw SQL fragment. The grammar emits Value verbatim and records no binding for it.
//
// Mirrors Illuminate\Database\Query\Expression.
type Expression struct {
	Value string
}

// Raw creates a raw SQL expression.
func Raw(value string) Expression { return Expression{Value: value} }

// GetValue returns the raw SQL. Mirrors Expression::getValue().
func (e Expression) GetValue() string { return e.Value }

// IsExpression reports whether value is a raw SQL expression. Mirrors Grammar::isExpression().
func IsExpression(value any) bool {
	switch value.(type) {
	case Expression, *Expression:
		return true
	default:
		return false
	}
}

// ExpressionValue returns the raw SQL of an expression, or "" when value is not one.
func ExpressionValue(value any) string {
	switch v := value.(type) {
	case Expression:
		return v.Value
	case *Expression:
		if v != nil {
			return v.Value
		}
	}
	return ""
}
