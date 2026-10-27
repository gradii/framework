package gorm

import (
	querybuilder "github.com/gradii/framework/database/query"
)

// Random ordering is compiled here rather than by the driver modules' GORM grammar.
//
// The method used to live on contracts/database/driver.GormGrammar, which forced every driver
// module to import GORM just to say "RAND()". The dialect set is closed and tiny, so the framework
// owns it and the driver contract no longer mentions GORM.

// randomOrderFunction returns the dialect's random-ordering expression.
func randomOrderFunction(driver string) string {
	switch querybuilder.CanonicalDialect(driver) {
	case querybuilder.DialectMySQL:
		return "RAND()"
	case querybuilder.DialectSqlServer:
		return "NEWID()"
	default:
		return "RANDOM()"
	}
}
