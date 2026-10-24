package schema

import (
	"github.com/goravel/framework/contracts/database/driver"
	contractsschema "github.com/goravel/framework/contracts/database/schema"
)

func (r *Blueprint) Engine(engine string)       { r.engine = engine }
func (r *Blueprint) InnoDb()                    { r.engine = "InnoDB" }
func (r *Blueprint) Charset(charset string)     { r.charset = charset }
func (r *Blueprint) Collation(collation string) { r.collation = collation }
func (r *Blueprint) Temporary()                 { r.temporary = true }
func (r *Blueprint) Binary(column string, length ...int) driver.ColumnDefinition {
	value := r.createAndAddColumn("binary", column)
	if len(length) > 0 {
		value.length = &length[0]
	}
	return value
}
func (r *Blueprint) Year(column string) driver.ColumnDefinition {
	return r.createAndAddColumn("year", column)
}
func (r *Blueprint) IpAddress(column string) driver.ColumnDefinition {
	return r.createAndAddColumn("ipAddress", column)
}
func (r *Blueprint) MacAddress(column string) driver.ColumnDefinition {
	return r.createAndAddColumn("macAddress", column)
}
func (r *Blueprint) Set(column string, allowed []any) driver.ColumnDefinition {
	value := r.createAndAddColumn("set", column)
	value.allowed = append([]any(nil), allowed...)
	return value
}
func (r *Blueprint) Geometry(column string, options ...any) driver.ColumnDefinition {
	return r.spatialColumn("geometry", column, options)
}
func (r *Blueprint) Geography(column string, options ...any) driver.ColumnDefinition {
	return r.spatialColumn("geography", column, options)
}
func (r *Blueprint) spatialColumn(kind, column string, options []any) driver.ColumnDefinition {
	value := r.createAndAddColumn(kind, column)
	if kind == "geography" {
		srid := 4326
		value.srid = &srid
	}
	if len(options) > 0 {
		value.subtype, _ = options[0].(string)
	}
	if len(options) > 1 {
		if srid, ok := options[1].(int); ok {
			value.srid = &srid
		}
	}
	return value
}
func (r *Blueprint) Vector(column string, dimensions ...int) driver.ColumnDefinition {
	value := r.createAndAddColumn("vector", column)
	if len(dimensions) > 0 {
		value.dimensions = &dimensions[0]
	}
	return value
}
func (r *Blueprint) Computed(column string, expression any) driver.ColumnDefinition {
	value := r.createAndAddColumn("computed", column)
	value.expression = expression
	return value
}
func (r *Blueprint) RawColumn(column, definition string) driver.ColumnDefinition {
	value := r.createAndAddColumn("raw", column)
	value.rawDefinition = definition
	return value
}
func (r *Blueprint) SpatialIndex(columns ...string) contractsschema.IndexDefinition {
	command := r.indexCommand("spatialIndex", columns)
	return NewIndexDefinition(command)
}
func (r *Blueprint) DropSpatialIndex(columns ...string) {
	r.indexCommand("dropSpatialIndex", columns, contractsschema.IndexConfig{Name: r.createIndexName("spatialIndex", columns)})
}
func (r *Blueprint) RawIndex(expression, name string) contractsschema.IndexDefinition {
	command := r.indexCommand("rawIndex", nil, contractsschema.IndexConfig{Name: name})
	command.Value = expression
	return NewIndexDefinition(command)
}
func (r *Blueprint) NullableTimestamps(precision ...int)   { r.Timestamps(precision...) }
func (r *Blueprint) NullableTimestampsTz(precision ...int) { r.TimestampsTz(precision...) }
func (r *Blueprint) SoftDeletesDatetime(column ...string) {
	name := "deleted_at"
	if len(column) > 0 {
		name = column[0]
	}
	r.DateTime(name).Nullable()
}
func (r *Blueprint) NullableNumericMorphs(name string, indexName ...string) {
	r.String(name + "_type").Nullable()
	r.UnsignedBigInteger(name + "_id").Nullable()
	r.createMorphIndex(name, indexName...)
}
func (r *Blueprint) NullableUuidMorphs(name string, indexName ...string) {
	r.String(name + "_type").Nullable()
	r.Uuid(name + "_id").Nullable()
	r.createMorphIndex(name, indexName...)
}
func (r *Blueprint) NullableUlidMorphs(name string, indexName ...string) {
	r.String(name + "_type").Nullable()
	r.Ulid(name + "_id").Nullable()
	r.createMorphIndex(name, indexName...)
}
func (r *Blueprint) RememberToken() driver.ColumnDefinition {
	return r.String("remember_token", 100).Nullable()
}
func (r *Blueprint) DropRememberToken() { r.DropColumn("remember_token") }
func (r *Blueprint) DropMorphs(name string, indexName ...string) {
	r.DropColumn(name+"_type", name+"_id")
	if len(indexName) > 0 {
		r.DropIndexByName(indexName[0])
	} else {
		r.DropIndex(name+"_type", name+"_id")
	}
}
func (r *Blueprint) DropConstrainedForeignId(column string) {
	r.DropForeign(column)
	r.DropColumn(column)
}
func (r *Blueprint) hasAdvanced() bool {
	if r.temporary || r.engine != "" || r.charset != "" || r.collation != "" {
		return true
	}
	for _, column := range r.columns {
		if column.advanced() {
			return true
		}
	}
	return false
}
