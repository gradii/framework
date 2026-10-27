package db

import (
	"fmt"
	"reflect"

	"github.com/goravel/framework/contracts/database/db"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
)

func (r *Query) resolveReader() {
	if r.builder.State().UseWritePdo || r.builder.State().Lock != nil {
		r.readBuilder = r.writeBuilder
	} else if r.originalReadBuilder != nil {
		r.readBuilder = r.originalReadBuilder
	}
}

func (r *Query) onceWithColumns(columns []any, callback func() error) error {
	original := r.builder.Selects()
	if original == nil {
		r.builder.SetSelects(columns)
	}
	defer r.builder.SetSelects(original)
	return callback()
}

func (r *Query) GetCountForPagination(columns ...string) (int64, error) {
	q := r.clone()
	if q.err != nil {
		return 0, q.err
	}
	if q.builder.TableSource() == nil && !q.allowTableless {
		return 0, errors.DatabaseTableIsRequired
	}
	statement, bindings := q.builder.GetCountForPagination(toStrings(columns)...)
	if err := q.builder.Error(); err != nil {
		return 0, err
	}
	statement = querybuilder.ReplacePlaceholders(statement, q.driver)
	q.resolveReader()
	var count int64
	begin := carbon.Now()
	err := q.readBuilder.GetContext(q.ctx, &count, statement, bindings...)
	q.trace(q.readBuilder, statement, bindings, begin, -1, err)
	return count, err
}

// SimplePaginate probes one extra row. Data stays in dest; pagination has no HTTP metadata.
func (r *Query) SimplePaginate(page, limit int, dest any) (bool, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 15
	}
	maxInt := int(^uint(0) >> 1)
	if limit == maxInt || page-1 > maxInt/limit {
		return false, errors.QueryPaginationOverflow
	}
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Slice {
		return false, errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "pointer to slice")
	}
	if err := r.Offset(uint64((page - 1) * limit)).Limit(uint64(limit + 1)).Get(dest); err != nil {
		return false, err
	}
	rows := target.Elem()
	if rows.Len() > limit {
		rows.Set(rows.Slice(0, limit))
		return true, nil
	}
	return false, nil
}

// Sole reads at most two rows so both absence and ambiguity are observable.
func (r *Query) Sole(dest any) error {
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer")
	}
	rows := reflect.New(reflect.SliceOf(target.Elem().Type()))
	if err := r.Limit(2).Get(rows.Interface()); err != nil {
		return err
	}
	switch rows.Elem().Len() {
	case 0:
		return errors.QueryRecordsNotFound
	case 1:
		target.Elem().Set(rows.Elem().Index(0))
		return nil
	default:
		return errors.QueryMultipleRecordsFound.Args(rows.Elem().Len())
	}
}

func (r *Query) firstTransformed(dest any, missing func() error) error {
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer")
	}
	rows := reflect.New(reflect.SliceOf(target.Elem().Type()))
	if err := r.Get(rows.Interface()); err != nil {
		return err
	}
	if rows.Elem().Len() == 0 {
		target.Elem().SetZero()
		if missing != nil {
			return missing()
		}
		return nil
	}
	target.Elem().Set(rows.Elem().Index(0))
	return nil
}

func (r *Query) SoleValue(column string, dest any) error {
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer")
	}
	values := reflect.New(reflect.SliceOf(target.Elem().Type()))
	if err := r.onceWithColumns([]any{column}, func() error { return r.Limit(2).Get(values.Interface()) }); err != nil {
		return err
	}
	switch values.Elem().Len() {
	case 0:
		return errors.QueryRecordsNotFound
	case 1:
		target.Elem().Set(values.Elem().Index(0))
		return nil
	default:
		return errors.QueryMultipleRecordsFound.Args(values.Elem().Len())
	}
}

func (r *Query) ChunkMap(size uint64, callback func(db.Row) (any, error)) ([]any, error) {
	values := []any{}
	err := r.Chunk(size, func(rows []db.Row) error {
		for _, row := range rows {
			value, err := callback(row)
			if err != nil {
				return err
			}
			values = append(values, value)
		}
		return nil
	})
	return values, err
}

func (r *Query) EachById(size uint64, callback func(db.Row) error, column ...string) error {
	return r.ChunkById(size, func(rows []db.Row) error {
		for _, row := range rows {
			if err := callback(row); err != nil {
				return err
			}
		}
		return nil
	}, column...)
}

func (r *Query) Lazy(chunkSize ...uint64) db.RowIterator {
	size := uint64(1000)
	if len(chunkSize) > 0 {
		size = chunkSize[0]
	}
	return r.lazy(size, nil, false, false)
}
func (r *Query) LazyById(size uint64, column ...string) db.RowIterator {
	return r.lazy(size, column, true, false)
}
func (r *Query) LazyByIdDesc(size uint64, column ...string) db.RowIterator {
	return r.lazy(size, column, true, true)
}
func (r *Query) lazy(size uint64, columns []string, byID, descending bool) db.RowIterator {
	return func(yield func(db.Row, error) bool) {
		if size == 0 || size > uint64(int(^uint(0)>>1)) {
			yield(nil, errors.QueryInvalidChunkSize)
			return
		}
		if !byID && len(r.builder.State().Orders) == 0 && len(r.builder.State().UnionOrders) == 0 {
			yield(nil, errors.QueryOrderRequired)
			return
		}
		column := firstString(columns, "id")
		alias := stripTableForPluck(column)
		if len(columns) > 1 {
			alias = columns[1]
		}
		var lastID any
		for page := 1; ; page++ {
			var q db.Query
			if byID {
				q = r.Clone()
				if descending {
					q.ForPageBeforeId(int(size), lastID, column)
				} else {
					q.ForPageAfterId(int(size), lastID, column)
				}
			} else {
				q = r.ForPage(page, int(size))
			}
			var rows []map[string]any
			if err := q.Get(&rows); err != nil {
				yield(nil, err)
				return
			}
			for _, row := range rows {
				if !yield(NewRow(row, nil), nil) {
					return
				}
			}
			if len(rows) < int(size) {
				return
			}
			if byID {
				var ok bool
				lastID, ok = rows[len(rows)-1][alias]
				if !ok || lastID == nil {
					yield(nil, errors.DatabaseChunkByIdColumnNotPresent.Args(alias))
					return
				}
			}
		}
	}
}

// Explain executes the query plan command; it is distinct from literal interpolation.
func (r *Query) Explain(dest any) error {
	statement, bindings, err := r.buildSelect()
	if err != nil {
		return err
	}
	statement = "EXPLAIN " + statement
	begin := carbon.Now()
	err = r.readBuilder.SelectContext(r.ctx, dest, statement, bindings...)
	r.trace(r.readBuilder, statement, bindings, begin, -1, err)
	return err
}

func (r *Query) applyAfterQuery(dest any) error {
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer")
	}
	result := r.builder.Processor().ProcessSelect(target.Elem().Interface())
	result = r.builder.ApplyAfterQueryCallbacks(result)
	value := reflect.ValueOf(result)
	if !value.IsValid() {
		target.Elem().SetZero()
		return nil
	}
	if value.Type().AssignableTo(target.Elem().Type()) {
		target.Elem().Set(value)
		return nil
	}
	return errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", result), target.Elem().Type().String())
}
