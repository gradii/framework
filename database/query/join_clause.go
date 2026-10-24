package query

import "github.com/goravel/framework/database/query/core"

// JoinClause is a builder specialised to a join. It embeds *Builder, mirroring PHP's
// `class JoinClause extends Builder`, so on clauses are stored in the same where descriptors and
// compiled by the same compileWheres. Because isJoinClause is set, that compilation emits `on ...`
// instead of `where ...`, and whereNested strips three characters rather than six.
type JoinClause struct {
	*Builder

	// Type is the join type: inner, left, right, cross or lateral.
	Type string
	// Table is the joined table: a string or an Expression.
	Table any
	// isLateral marks a lateral join, mirroring Laravel's JoinLateralClause subclass. compileJoins
	// dispatches these to CompileJoinLateral instead of the generic "type join ... on" shape.
	isLateral bool
}

// NewJoinLateralClause creates a lateral join clause. Mirrors Builder::newJoinLateralClause(), which
// builds a JoinLateralClause rather than a plain JoinClause.
func NewJoinLateralClause(parent *Builder, typ string, table any) *JoinClause {
	join := NewJoinClause(parent, typ, table)
	join.isLateral = true

	return join
}

// NewJoinClause creates a join clause bound to the parent's grammar.
func NewJoinClause(parent *Builder, typ string, table any) *JoinClause {
	inner := NewBuilder(parent.grammar, parent.processor)
	inner.State().IsJoinClause = true

	return &JoinClause{Builder: inner, Type: typ, Table: table}
}

// On adds an on clause. Mirrors JoinClause::on(): a callback becomes a nested where, anything else
// is a column comparison.
func (j *JoinClause) On(first any, operator any, second any, boolean string) *JoinClause {
	if callback, ok := first.(func(*Builder) *Builder); ok {
		j.Builder.WhereNested(callback, boolean)
		return j
	}

	j.Builder.WhereColumn(first, operator, second, boolean)

	return j
}

// OrOn adds an "or on" clause. Mirrors JoinClause::orOn().
func (j *JoinClause) OrOn(first any, operator any, second any) *JoinClause {
	return j.On(first, operator, second, core.BooleanOr)
}

func (j *JoinClause) JoinType() string { return j.Type }
func (j *JoinClause) JoinTable() any   { return j.Table }
func (j *JoinClause) IsLateral() bool  { return j.isLateral }
