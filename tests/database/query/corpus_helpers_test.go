package query

import (
	. "github.com/gradii/framework/database/query"
)

// newTestBuilder returns a builder over the base grammar, which quotes with double quotes and
// binds with "?" exactly like Laravel's generic grammar.
func newTestBuilder() *Builder {
	return NewBuilder(NewBaseGrammar(""))
}
