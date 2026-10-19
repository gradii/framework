package query

// IndexHint mirrors Illuminate\Database\Query\IndexHint.
type IndexHint struct {
	// Type is hint, force or ignore.
	Type string
	// Index is the index name.
	Index string
}

// UseIndex suggests an index. Mirrors Builder::useIndex().
func (b *Builder) UseIndex(index string) *Builder {
	b.indexHint = &IndexHint{Type: "hint", Index: index}
	return b
}

// ForceIndex forces an index. Mirrors Builder::forceIndex().
func (b *Builder) ForceIndex(index string) *Builder {
	b.indexHint = &IndexHint{Type: "force", Index: index}
	return b
}

// IgnoreIndex ignores an index. Mirrors Builder::ignoreIndex().
func (b *Builder) IgnoreIndex(index string) *Builder {
	b.indexHint = &IndexHint{Type: "ignore", Index: index}
	return b
}
