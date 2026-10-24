package grammars

// Query is the builder view used by SQL compilers. It contains no execution or fluent API.
type Query interface {
	State() *State
	CloneForGrammar() Query
	SetError(error)
	Error() error
	ToSQL() string
	GetBindings() []any
	RawBindings(string) []any
	SetBindings([]any, string) error
}

// Join adds the join metadata to the compilation view.
type Join interface {
	Query
	JoinType() string
	JoinTable() any
	IsLateral() bool
}

// State holds the clauses compiled by a grammar, matching Laravel's public builder properties.
type State struct {
	UseWritePdo      bool
	ServerVersion    func() (string, error)
	UseUpsertAlias   bool
	Bindings         map[string][]any
	Aggregate        *Aggregate
	Columns          []any
	Distinct         bool
	DistinctColumns  []any
	From             any
	Joins            []Join
	Wheres           []Where
	Groups           []any
	Havings          []Having
	Orders           []Order
	Limit            *int
	Offset           *int
	IndexHint        *IndexHint
	GroupLimit       int
	GroupLimitSet    bool
	GroupLimitColumn string
	Unions           []Union
	UnionLimit       *int
	UnionOffset      *int
	UnionOrders      []Order
	Lock             any
	InsertColumns    []string
	IsJoinClause     bool
}

// BindingTypes fixes the order of positional binding groups.
var BindingTypes = []string{"select", "from", "join", "where", "groupBy", "having", "order", "union", "unionOrder"}
