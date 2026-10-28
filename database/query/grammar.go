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

var SelectComponents = grammars.SelectComponents
var BindingTypes = core.BindingTypes
