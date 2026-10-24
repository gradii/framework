package db

import (
	"reflect"
	"sort"

	"github.com/goravel/framework/contracts/database/db"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
)

func emptyBatch(data any) bool {
	if data == nil {
		return true
	}
	value := reflect.ValueOf(data)
	switch value.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return value.Len() == 0
	}
	return false
}

func (r *Query) writeReady() error {
	if r.err != nil {
		return r.err
	}
	if err := r.builder.Error(); err != nil {
		return err
	}
	if r.builder.TableSource() == nil {
		return errors.DatabaseTableIsRequired
	}
	return nil
}

func (r *Query) executeWrite(statement string, bindings []any) (*db.Result, error) {
	if err := r.writeReady(); err != nil {
		return nil, err
	}
	statement = querybuilder.ReplacePlaceholders(statement, r.driver)
	begin := carbon.Now()
	result, err := r.writeBuilder.ExecContext(r.ctx, statement, bindings...)
	if err != nil {
		r.trace(r.writeBuilder, statement, bindings, begin, -1, err)
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		r.trace(r.writeBuilder, statement, bindings, begin, -1, err)
		return nil, err
	}
	r.trace(r.writeBuilder, statement, bindings, begin, affected, nil)
	return &db.Result{RowsAffected: affected}, nil
}

func (r *Query) InsertOrIgnore(data any) (*db.Result, error) {
	if emptyBatch(data) {
		return &db.Result{}, nil
	}
	values, err := convertToSliceMap(data)
	if err != nil {
		return nil, err
	}
	statement, bindings := r.builder.ToInsertOrIgnoreSQL(uniformInsertRecords(values))
	return r.executeWrite(statement, bindings)
}

func (r *Query) InsertUsing(columns []string, query any) (*db.Result, error) {
	r.builder.InsertUsing(toStrings(columns), r.subquery(query))
	statement, bindings := r.builder.ToInsertUsingSQL()
	return r.executeWrite(statement, bindings)
}

func (r *Query) InsertOrIgnoreUsing(columns []string, query any) (*db.Result, error) {
	r.builder.ApplyBeforeQueryCallbacks()
	r.builder.InsertOrIgnoreUsing(toStrings(columns), r.subquery(query))
	statement, bindings := r.builder.ToInsertOrIgnoreUsingSQL()
	return r.executeWrite(statement, bindings)
}

func (r *Query) UpdateFrom(values any) (*db.Result, error) {
	statement, bindings := r.builder.ToUpdateFromSQL(values)
	return r.executeWrite(statement, bindings)
}

// Upsert follows Laravel's distinction between an omitted update argument and an empty array.
func (r *Query) Upsert(data any, uniqueBy []string, update ...any) (*db.Result, error) {
	if emptyBatch(data) {
		return &db.Result{}, nil
	}
	values, err := convertToSliceMap(data)
	if err != nil {
		return nil, err
	}
	values = uniformInsertRecords(values)
	updates, insertOnly, err := upsertUpdates(values, update)
	if err != nil {
		return nil, err
	}
	if insertOnly {
		result, err := r.Insert(data)
		if err == nil {
			result.RowsAffected = 1
		}
		return result, err
	}
	statement, bindings := r.builder.ToUpsertSQL(values, uniqueBy, updates)
	return r.executeWrite(statement, bindings)
}

func upsertUpdates(values []map[string]any, update []any) ([]querybuilder.UpsertUpdate, bool, error) {
	var result []querybuilder.UpsertUpdate
	if len(update) == 0 || update[0] == nil {
		if len(values) > 0 {
			for _, key := range sortedMapKeys(values[0]) {
				result = append(result, querybuilder.UpsertUpdate{Column: key})
			}
		}
		return result, false, nil
	}
	switch columns := update[0].(type) {
	case []string:
		for _, column := range columns {
			result = append(result, querybuilder.UpsertUpdate{Column: column})
		}
	case []querybuilder.UpsertUpdate:
		result = append(result, columns...)
	case map[string]any:
		for _, column := range sortedMapKeys(columns) {
			result = append(result, querybuilder.UpsertUpdate{Column: column, Value: columns[column], Bound: true})
		}
	default:
		return nil, false, errors.DatabaseUnsupportedType.Args(reflect.TypeOf(update[0]).String(), "[]string, map[string]any, or []query.UpsertUpdate")
	}
	return result, len(result) == 0, nil
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func arithmeticExtra(extra []map[string]any) querybuilder.UpdateValues {
	var values querybuilder.UpdateValues
	if len(extra) > 0 {
		for _, column := range sortedMapKeys(extra[0]) {
			values = append(values, querybuilder.UpdateValue{Column: column, Value: extra[0][column]})
		}
	}
	return values
}
func (r *Query) IncrementEach(columns any, extra ...map[string]any) (*db.Result, error) {
	r.builder.IncrementEach(columns, arithmeticExtra(extra)...)
	if err := r.builder.Error(); err != nil {
		return nil, err
	}
	statement, bindings := r.builder.ToUpdateSQL(r.builder.UpdateValues())
	return r.executeWrite(statement, bindings)
}
func (r *Query) DecrementEach(columns any, extra ...map[string]any) (*db.Result, error) {
	r.builder.DecrementEach(columns, arithmeticExtra(extra)...)
	if err := r.builder.Error(); err != nil {
		return nil, err
	}
	statement, bindings := r.builder.ToUpdateSQL(r.builder.UpdateValues())
	return r.executeWrite(statement, bindings)
}

func (r *Query) Truncate() error {
	if err := r.writeReady(); err != nil {
		return err
	}
	statements := r.builder.ToTruncateSQL()
	if err := r.builder.Error(); err != nil {
		return err
	}
	for _, statement := range statements {
		sql := querybuilder.ReplacePlaceholders(statement.SQL, r.driver)
		begin := carbon.Now()
		_, err := r.writeBuilder.ExecContext(r.ctx, sql, statement.Bindings...)
		r.trace(r.writeBuilder, sql, statement.Bindings, begin, -1, err)
		if err != nil {
			return err
		}
	}
	return nil
}
