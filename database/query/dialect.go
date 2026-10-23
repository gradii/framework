package query

import "github.com/goravel/framework/database/query/grammars"

const (
	DialectMySQL     = grammars.DialectMySQL
	DialectPostgres  = grammars.DialectPostgres
	DialectSqlite    = grammars.DialectSqlite
	DialectSqlServer = grammars.DialectSqlServer
)

func CanonicalDialect(driver string) string            { return grammars.CanonicalDialect(driver) }
func NewGrammar(driver, prefix string) (Grammar, bool) { return grammars.NewGrammar(driver, prefix) }
func UnrecognisedDriverWarning(driver string) error {
	return grammars.UnrecognisedDriverWarning(driver)
}
func ReplacePlaceholders(sql, driver string) string { return grammars.ReplacePlaceholders(sql, driver) }
