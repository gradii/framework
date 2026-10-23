package query

import "github.com/goravel/framework/database/query/grammars"

type Expression = grammars.Expression

func Raw(value string) Expression      { return grammars.Raw(value) }
func IsExpression(value any) bool      { return grammars.IsExpression(value) }
func ExpressionValue(value any) string { return grammars.ExpressionValue(value) }
