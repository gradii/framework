package query

import "github.com/goravel/framework/database/query/core"

type Expression = core.Expression

func Raw(value string) Expression      { return core.Raw(value) }
func IsExpression(value any) bool      { return core.IsExpression(value) }
func ExpressionValue(value any) string { return core.ExpressionValue(value) }
