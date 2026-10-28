package schema

import "github.com/gradii/framework/contracts/database/driver"

func copyOption[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (r *Blueprint) DescribeBlueprint() driver.BlueprintDescription {
	return driver.BlueprintDescription{Table: r.table, Prefix: r.prefix, Engine: r.engine, Charset: r.charset,
		Collation: r.collation, Temporary: r.temporary, Columns: r.GetAddedColumns(), Commands: append([]*driver.Command(nil), r.commands...)}
}

func (r *ColumnDefinition) DescribeColumn() driver.ColumnDescription {
	return driver.ColumnDescription{Name: r.GetName(), Type: r.GetType(), After: r.after, Charset: r.charset,
		Collation: r.collation, Subtype: r.subtype, RawDefinition: r.rawDefinition,
		StoredAs: r.storedAs, VirtualAs: r.virtualAs, Expression: r.expression, Default: r.def, OnUpdate: r.onUpdate,
		Allowed: append([]any(nil), r.allowed...), StoredSet: r.storedSet, VirtualSet: r.virtualSet,
		Persisted: r.persisted, Invisible: r.invisible, Fixed: r.fixed, Instant: r.instant, Always: r.always, Change: r.change, First: r.first,
		AutoIncrement: copyOption(r.autoIncrement), Nullable: copyOption(r.nullable), Unsigned: copyOption(r.unsigned),
		UseCurrent: copyOption(r.useCurrent), UseCurrentOnUpdate: copyOption(r.useCurrentOnUpdate), Comment: copyOption(r.comment),
		GeneratedAs: copyOption(r.generatedAs), Length: copyOption(r.length), Places: copyOption(r.places),
		Precision: copyOption(r.precision), Total: copyOption(r.total), Srid: copyOption(r.srid), Dimensions: copyOption(r.dimensions),
		StartingValue: copyOption(r.startingValue)}
}
