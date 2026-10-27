package grammars

import "github.com/gradii/framework/database/query/core"

// MariaDbGrammar mirrors Illuminate\Database\Query\Grammars\MariaDbGrammar, which extends the MySQL
// grammar and overrides a handful of methods. Only the override this port exercises is declared here:
// CompileJoinLateral, which refuses lateral joins.
//
// Laravel's other MariaDbGrammar overrides (compileJsonValueCast, compileThreadCount,
// useLegacyGroupLimit and wrapJsonSelector) are not modelled, so a MariaDB connection reuses the MySQL
// SQL for those clauses. They are out of scope for this port and carry no corpus coverage here; adding
// them silently would change MySQL-shared rendering for JSON and group-limit clauses.
type MariaDbGrammar struct {
	*MySqlGrammar
}

// NewMariaDbGrammar creates the MariaDB grammar, which quotes identifiers the MySQL way (backticks).
func NewMariaDbGrammar(prefix string) *MariaDbGrammar {
	g := &MariaDbGrammar{MySqlGrammar: NewMySqlGrammar(prefix)}
	// Re-attach the concrete dialect. NewMySqlGrammar set the embedded MySQL grammar as the shared
	// base's self reference, and Go embedding does not dispatch virtually, so without this the shared
	// methods would keep routing through MySQL and never reach this type's CompileJoinLateral override.
	g.Attach(g)

	return g
}

// CompileJoinLateral mirrors MariaDbGrammar::compileJoinLateral(): MariaDB has no lateral joins, so the
// dialect refuses. Returning the empty string makes BaseGrammar.CompileJoins record
// errLateralJoinNotSupported, mirroring the RuntimeException Laravel throws.
func (g *MariaDbGrammar) CompileJoinLateral(join core.Join, expression string) string {
	return ""
}
