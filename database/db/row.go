package db

import (
	"github.com/gradii/framework/database/internal/reflectx"
	rowscan "github.com/gradii/framework/database/internal/scan"
)

var rowMapper = reflectx.NewMapperFunc("db", NameMapper)

type Row struct {
	err error
	row map[string]any
}

func NewRow(row map[string]any, err error) *Row {
	return &Row{row: row, err: err}
}

func (r *Row) Err() error {
	return r.err
}

// Scan maps column values using db tags and flattens embedded structs.
func (r *Row) Scan(value any) error {
	if r.err != nil {
		return r.err
	}
	return rowscan.Map(r.row, value, rowMapper)
}
