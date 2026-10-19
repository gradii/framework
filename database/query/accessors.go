package query

// The accessors below exist so database/db can stay a thin adapter over the builder: it needs to
// inspect and rewrite the select list for count and pluck, and to set an aggregate without going
// through a fluent call that would also change other state.

// Selects returns the selected columns, or nil when none were set.
func (b *Builder) Selects() []any { return b.columns }

// SetSelects replaces the selected columns.
func (b *Builder) SetSelects(columns []any) { b.columns = columns }

// SetAggregate sets the aggregate function and its columns. It mirrors Builder::setAggregate:
// when the query has no groups the orders and their bindings are cleared too, so an ordered
// query aggregated through the accessor does not keep an ORDER BY Laravel drops (and the order
// binding cannot outnumber the placeholders left in the compiled SQL). With a group by the order
// survives, because it still orders the grouped rows.
func (b *Builder) SetAggregate(function string, columns []any) {
	b.aggregate = &Aggregate{Function: function, Columns: columns}

	if len(b.groups) == 0 {
		b.orders = nil
		if b.bindings != nil {
			b.bindings["order"] = []any{}
		}
	}
}

// Aggregate returns the current aggregate, or nil.
func (b *Builder) Aggregate() *Aggregate { return b.aggregate }

// DistinctFlag reports whether distinct is set.
func (b *Builder) DistinctFlag() bool { return b.distinct }

// DistinctColumns returns the distinct-on columns.
func (b *Builder) DistinctColumns() []any { return b.distinctColumns }

// ClearDistinct clears both distinct flags.
func (b *Builder) ClearDistinct() {
	b.distinct = false
	b.distinctColumns = nil
}

// Driver returns the dialect name the builder was created for, if one was recorded.
func (b *Builder) Driver() string { return b.driver }

// UpdateValues returns the ordered assignments IncrementEach recorded, or nil when it recorded none.
// The caller compiles them with ToUpdateSQL, mirroring the array Laravel's incrementEach hands to
// update().
func (b *Builder) UpdateValues() UpdateValues { return b.updateValues }
