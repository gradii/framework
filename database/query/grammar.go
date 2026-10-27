package query

import (
	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/database/query/grammars"
)

// These aliases preserve the builder API while dialect implementations live in grammars.
type Grammar = grammars.Grammar
type GrammarQuery = core.Query
type GrammarJoin = core.Join
type State = core.State
type BaseGrammar = grammars.BaseGrammar

func NewBaseGrammar(prefix string) *BaseGrammar { return grammars.NewBaseGrammar(prefix) }

type MySqlGrammar = grammars.MySqlGrammar

func NewMySqlGrammar(prefix string) *MySqlGrammar { return grammars.NewMySqlGrammar(prefix) }

type MariaDbGrammar = grammars.MariaDbGrammar

func NewMariaDbGrammar(prefix string) *MariaDbGrammar { return grammars.NewMariaDbGrammar(prefix) }

type PostgresGrammar = grammars.PostgresGrammar

func NewPostgresGrammar(prefix string) *PostgresGrammar { return grammars.NewPostgresGrammar(prefix) }

type SqliteGrammar = grammars.SqliteGrammar

func NewSqliteGrammar(prefix string) *SqliteGrammar { return grammars.NewSqliteGrammar(prefix) }

type SqlServerGrammar = grammars.SqlServerGrammar

func NewSqlServerGrammar(prefix string) *SqlServerGrammar {
	return grammars.NewSqlServerGrammar(prefix)
}

var SelectComponents = grammars.SelectComponents
var BindingTypes = core.BindingTypes

func CascadeOnTruncate(value bool) { grammars.CascadeOnTruncate(value) }
