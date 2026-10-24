package db

import (
	"github.com/goravel/framework/errors"
)

type ToSql struct {
	query *Query
	raw   bool
}

func NewToSql(query *Query, raw bool) *ToSql {
	return &ToSql{query: query, raw: raw}
}

func (r *ToSql) Count() string {
	query := r.query.clone()
	if err := buildSelectForCount(query); err != nil {
		return r.generate("", nil, err)
	}

	sql, args, err := query.buildSelect()

	return r.generate(sql, args, err)
}

func (r *ToSql) Delete() string {
	sql, args, err := r.query.buildDelete()

	return r.generate(sql, args, err)
}

func (r *ToSql) First() string {
	sql, args, err := r.query.Limit(1).(*Query).buildSelect()

	return r.generate(sql, args, err)
}

func (r *ToSql) Get() string {
	sql, args, err := r.query.buildSelect()

	return r.generate(sql, args, err)
}

func (r *ToSql) Insert(data any) string {
	mapData, err := convertToSliceMap(data)
	if err != nil {
		return r.generate("", nil, err)
	}
	if len(mapData) == 0 {
		return r.generate("", nil, errors.DatabaseDataIsEmpty)
	}

	sql, args, err := r.query.buildInsert(mapData)

	return r.generate(sql, args, err)
}

func (r *ToSql) Pluck(column string, dest any) string {
	var statement string
	var bindings []any
	err := r.query.onceWithColumns([]any{column}, func() error {
		var err error
		statement, bindings, err = r.query.buildSelect()
		return err
	})

	return r.generate(statement, bindings, err)
}

func (r *ToSql) Update(column any, value ...any) string {
	columnStr, ok := column.(string)
	if ok {
		if len(value) != 1 {
			return r.generate("", nil, errors.DatabaseInvalidArgumentNumber.Args(len(value), "1"))
		}

		return r.Update(map[string]any{columnStr: value[0]})
	}

	mapData, err := convertToMap(column)
	if err != nil {
		return r.generate("", nil, err)
	}

	sql, args, err := r.query.buildUpdate(mapData)

	return r.generate(sql, args, err)
}

func (r *ToSql) generate(sql string, args []any, err error) string {
	if err == nil && r.raw {
		sql, err = explainWithDriver(sql, args, r.query.driver)
	}
	if err != nil {
		if r.query.logger != nil {
			r.query.logger.Errorf(r.query.ctx, errors.DatabaseFailedToGetSql.Args(err).Error())
		}

		return ""
	}

	return sql
}
