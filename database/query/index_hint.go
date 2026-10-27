package query

import "github.com/gradii/framework/database/query/core"

// IndexHint mirrors Illuminate\Database\Query\IndexHint.
type IndexHint = core.IndexHint

// UseIndex suggests an index. Mirrors Builder::useIndex().
func (b *Builder) UseIndex(index string) *Builder {
	b.State().IndexHint = &IndexHint{Type: "hint", Index: index}
	return b
}

// ForceIndex forces an index. Mirrors Builder::forceIndex().
func (b *Builder) ForceIndex(index string) *Builder {
	b.State().IndexHint = &IndexHint{Type: "force", Index: index}
	return b
}

// IgnoreIndex ignores an index. Mirrors Builder::ignoreIndex().
func (b *Builder) IgnoreIndex(index string) *Builder {
	b.State().IndexHint = &IndexHint{Type: "ignore", Index: index}
	return b
}
