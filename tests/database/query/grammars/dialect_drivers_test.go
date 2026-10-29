package grammars

import (
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
)

// Constructors point at the external drivers so the original grammar corpus stays in the
// framework without introducing a production dependency on a particular database.
var (
	NewMySqlGrammar     = mysql.NewQueryGrammar
	NewMariaDbGrammar   = mysql.NewMariaDBQueryGrammar
	NewPostgresGrammar  = postgres.NewQueryGrammar
	NewSqliteGrammar    = sqlite.NewQueryGrammar
	NewSqlServerGrammar = sqlserver.NewQueryGrammar
	CustomOperators     = postgres.CustomOperators
	CascadeOnTruncate   = postgres.CascadeOnTruncate
)
