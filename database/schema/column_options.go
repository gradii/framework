package schema

import (
	"github.com/goravel/framework/contracts/database/driver"
	"github.com/goravel/framework/errors"
	"sync/atomic"
)

var configuredStringLength atomic.Int64
var configuredTimePrecision atomic.Int64

func SetDefaultStringLength(length int) error {
	if length <= 0 {
		return errors.SchemaInvalidLength
	}
	configuredStringLength.Store(int64(length))
	return nil
}
func SetDefaultTimePrecision(precision int) error {
	if precision < 0 {
		return errors.SchemaInvalidPrecision
	}
	configuredTimePrecision.Store(int64(precision))
	return nil
}
func currentStringLength() int {
	if length := configuredStringLength.Load(); length > 0 {
		return int(length)
	}
	return DefaultStringLength
}
func (r *Schema) DefaultStringLength(length int) error     { return SetDefaultStringLength(length) }
func (r *Schema) DefaultTimePrecision(precision int) error { return SetDefaultTimePrecision(precision) }
func (r *ColumnDefinition) StoredAs(expression any) driver.ColumnDefinition {
	r.storedAs, r.storedSet = expression, true
	return r
}

func (r *ColumnDefinition) VirtualAs(expression any) driver.ColumnDefinition {
	r.virtualAs, r.virtualSet = expression, true
	return r
}

func (r *ColumnDefinition) Persisted() driver.ColumnDefinition { r.persisted = true; return r }

func (r *ColumnDefinition) Invisible() driver.ColumnDefinition { r.invisible = true; return r }

func (r *ColumnDefinition) Charset(charset string) driver.ColumnDefinition {
	r.charset = charset
	return r
}

func (r *ColumnDefinition) Collation(collation string) driver.ColumnDefinition {
	r.collation = collation
	return r
}

func (r *ColumnDefinition) StartingValue(value uint64) driver.ColumnDefinition {
	r.startingValue = &value
	return r
}

func (r *ColumnDefinition) From(value uint64) driver.ColumnDefinition {
	r.startingValue = &value
	return r
}

func (r *ColumnDefinition) Fixed() driver.ColumnDefinition { r.fixed = true; return r }

func (r *ColumnDefinition) Srid(value int) driver.ColumnDefinition { r.srid = &value; return r }

func (r *ColumnDefinition) Instant() driver.ColumnDefinition { r.instant = true; return r }

func (r *ColumnDefinition) advanced() bool {
	switch r.GetType() {
	case "binary", "year", "ipAddress", "macAddress", "geometry", "geography", "vector", "computed", "set", "raw":
		return true
	}
	return r.storedSet || r.virtualSet || r.persisted || r.invisible || r.charset != "" || r.collation != "" || r.startingValue != nil || r.instant
}
