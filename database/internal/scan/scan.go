// Adapted from github.com/upper/db/v4/internal/sqlbuilder/fetch.go at 6ccd8f5.
// Copyright (c) 2012-present The upper.io/db authors. All rights reserved.
// See LICENSE in this directory.
// Goravel shares this mapping between SQL rows and materialized cursor rows.

package scan

import (
	"database/sql"
	"reflect"
	"slices"
	"strings"

	"github.com/goravel/framework/database/internal/reflectx"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/str"
)

// Row scans a SQL row into fields selected by the mapper's cached metadata.
func Row(rows *sql.Rows, columns []string, dest any, mapper *reflectx.Mapper) error {
	return scanFields(dest, columns, mapper, rows.Scan)
}

// Map scans a materialized row with the same mapping and conversions as Row.
func Map(row map[string]any, dest any, mapper *reflectx.Mapper) error {
	return mapWith(row, dest, mapper, Assign)
}

func mapWith(row map[string]any, dest any, mapper *reflectx.Mapper, assign func(reflect.Value, any) error) error {
	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}

	return scanFields(dest, columns, mapper, func(values ...any) error {
		for index, column := range columns {
			target := values[index].(valueScanner).target
			if !target.IsValid() {
				continue
			}
			if err := assign(target, row[column]); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanFields(dest any, columns []string, mapper *reflectx.Mapper, scan func(...any) error) error {
	target := reflect.ValueOf(dest)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.DatabaseUnsupportedType.Args(target.Kind(), "non-nil pointer")
	}
	for target.Kind() == reflect.Pointer {
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		target = target.Elem()
	}

	values := make([]any, len(columns))
	switch target.Kind() {
	case reflect.Struct:
		fields := mapper.TypeMap(target.Type()).Names
		for index, column := range columns {
			field := fields[column]
			if field == nil {
				field = fields[str.Of(column).Snake().String()]
			}
			if field == nil {
				for name, candidate := range fields {
					if strings.EqualFold(column, name) {
						field = candidate
						break
					}
				}
			}
			// An exact column name takes precedence over a case or naming alias.
			if field == nil || (column != field.Path && slices.Contains(columns, field.Path)) {
				values[index] = valueScanner{}
				continue
			}
			fieldValue := reflectx.FieldByIndexes(target, field.Index)
			if !fieldValue.CanSet() {
				return errors.DatabaseFieldNotSettable.Args(column, target.Type())
			}
			values[index] = valueScanner{target: fieldValue}
		}
		return scan(values...)
	case reflect.Map:
		if target.Type().Key().Kind() != reflect.String {
			return errors.DatabaseUnsupportedType.Args(target.Type().Key(), "string map key")
		}
		for index := range columns {
			values[index] = valueScanner{target: reflect.New(target.Type().Elem()).Elem()}
		}
		if err := scan(values...); err != nil {
			return err
		}
		if target.IsNil() {
			target.Set(reflect.MakeMapWithSize(target.Type(), len(columns)))
		}
		for index, column := range columns {
			key := reflect.ValueOf(column).Convert(target.Type().Key())
			target.SetMapIndex(key, values[index].(valueScanner).target)
		}
		return nil
	default:
		return errors.DatabaseUnsupportedType.Args(target.Kind(), "struct or map with string keys")
	}
}

type valueScanner struct {
	target reflect.Value
}

func (s valueScanner) Scan(source any) error {
	if !s.target.IsValid() {
		return nil
	}
	return Assign(s.target, source)
}
