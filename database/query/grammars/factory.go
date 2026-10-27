package grammars

import (
	"sync"

	"github.com/goravel/framework/database/query/core"
	"github.com/goravel/framework/errors"
)

// NewGrammar returns the grammar for a driver name. The second result reports whether the driver
// is recognised; an unrecognised driver yields the base grammar and false.
//
// The dialect is identified by the connection's driver string rather than by the driver module,
// because the four driver modules are separate repositories and must not change for this port.
func NewGrammar(driver string, prefix string) (Grammar, bool) {
	// MariaDB ships its own grammar in Laravel (MariaDbGrammar), which extends the MySQL grammar but
	// refuses lateral joins. CanonicalDialect deliberately folds "mariadb" onto core.DialectMySQL for the
	// quoting, placeholder and locking consumers, so the grammar is selected from the driver name here.
	if core.NormalizeDriver(driver) == "mariadb" {
		return NewMariaDbGrammar(prefix), true
	}

	switch core.CanonicalDialect(driver) {
	case core.DialectMySQL:
		return NewMySqlGrammar(prefix), true
	case core.DialectPostgres:
		return NewPostgresGrammar(prefix), true
	case core.DialectSqlite:
		return NewSqliteGrammar(prefix), true
	case core.DialectSqlServer:
		return NewSqlServerGrammar(prefix), true
	default:
		return NewBaseGrammar(prefix), false
	}
}

// unrecognisedDrivers remembers the driver names already reported through
// UnrecognisedDriverWarning, so the warning is logged once per driver per process.
var unrecognisedDrivers sync.Map

// UnrecognisedDriverWarning returns the declared error to log for a driver NewGrammar does not
// recognise, and nil for a recognised driver or one already reported. Callers log the error as a
// warning rather than failing: the framework deliberately lets a connection carry a database/sql
// driver name with no dedicated grammar, because a driver module may bring its own, and such a
// connection runs on the base grammar. The warning names the driver and the recognised set, so a
// mistyped driver is signalled at configuration time instead of surfacing as base-grammar SQL at
// the database. It reports each driver at most once per process: the construction paths run per
// transaction and per query clone, where a repeating warning would be noise.
func UnrecognisedDriverWarning(driver string) error {
	if core.CanonicalDialect(driver) != "" {
		return nil
	}

	if _, reported := unrecognisedDrivers.LoadOrStore(driver, struct{}{}); reported {
		return nil
	}

	return errors.QueryGrammarDriverNotRecognised.Args(driver)
}
