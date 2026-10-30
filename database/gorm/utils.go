package gorm

import (
	"strings"
)

// ident renders a dotted identifier with the dialect's own quoting. It replaces quoteIdent, which
// stripped quotes instead of applying them, so relation SQL emitted bare table.column comparisons
// while the rest of the query was quoted.
func (r *OrmBuilder) ident(parts ...string) string {
	return r.queryGrammar.Wrap(strings.Join(parts, "."))
}

// tableRef renders a table standing on its own — a join target, a raw FROM — with the dialect's
// quoting and the connection prefix. It is the table counterpart of ident: ident applies the prefix
// to the leading segment of a dotted table.column fragment, tableRef to a table that has no column
// to ride along with. The name passed in must be bare, or the prefix is applied twice.
func (r *OrmBuilder) tableRef(table string) string {
	return r.queryGrammar.WrapTable(table)
}
