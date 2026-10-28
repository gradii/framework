package grammars

import (
	"github.com/gradii/framework/database/query/core"
	"strings"
)

// aliasOf returns the alias of a "table as alias" source, or the table itself.
func aliasOf(from any) string {
	value := core.AsString(from)
	if index := strings.LastIndex(strings.ToLower(value), " as "); index >= 0 {
		return strings.TrimSpace(value[index+4:])
	}

	return value
}

// TableAlias returns the SQL alias or table name of a query source.
func TableAlias(from any) string { return aliasOf(from) }

// AliasOf returns the alias or unaliased table name of a query source.
func AliasOf(from any) string { return aliasOf(from) }
