package gorm

import (
	"github.com/goravel/framework/database/internal/reflectx"
	rowscan "github.com/goravel/framework/database/internal/scan"
	basedatabase "github.com/goravel/framework/support/database"
	"github.com/goravel/framework/support/str"
)

var rowMapper = reflectx.NewMapperTagFunc("gorm", func(name string) string {
	return str.Of(name).Snake().String()
}, func(tag string) string {
	annotation := basedatabase.ParseAnnotation(tag)
	if annotation.Skip {
		return "-"
	}
	return annotation.Column
})

type Row struct {
	err   error
	query *Query
	row   map[string]any
}

func (r *Row) Err() error {
	return r.err
}

func (r *Row) Scan(value any) error {
	if r.err != nil {
		return r.err
	}
	if err := scanRow(r.row, value); err != nil {
		return err
	}

	if r.query == nil || len(r.query.conditions.eagerLoad) == 0 {
		return nil
	}

	// Per-row eager loading for the Cursor() path. applyEagerLoads consumes its own slice
	// (sets it to nil after running), so we copy into a fresh query so subsequent rows in the
	// cursor still see the queued entries.
	query := r.query.fresh()
	query.clearConditions()
	query.conditions.eagerLoad = append([]eagerLoadEntry(nil), r.query.conditions.eagerLoad...)

	return query.applyEagerLoads(value)
}

// scanRow maps cursor results with the same gorm tags used by SQL row scanning.
func scanRow(row map[string]any, dest any) error {
	return rowscan.Map(row, dest, rowMapper)
}
