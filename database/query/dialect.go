package query

import (
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/database/query/grammars"
)

const (
	DialectMySQL     = core.DialectMySQL
	DialectPostgres  = core.DialectPostgres
	DialectSqlite    = core.DialectSqlite
	DialectSqlServer = core.DialectSqlServer
)

func CanonicalDialect(driver string) string            { return core.CanonicalDialect(driver) }
func NewGrammar(driver, prefix string) (Grammar, bool) { return grammars.NewGrammar(driver, prefix) }
func UnrecognisedDriverWarning(driver string) error {
	return grammars.UnrecognisedDriverWarning(driver)
}
func ReplacePlaceholders(sql, driver string) string { return core.ReplacePlaceholders(sql, driver) }
