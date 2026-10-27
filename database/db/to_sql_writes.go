package db

import (
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
)

func (r *ToSql) InsertGetID(data any, sequence ...string) string {
	values, err := convertToMap(data)
	if err != nil {
		return r.generate("", nil, err)
	}
	if len(values) == 0 {
		return r.generate("", nil, errors.DatabaseUnsupportedType.Args("nil", "struct, map[string]any").SetModule("DB"))
	}
	statement, bindings := r.query.builder.ToInsertGetIdSQL([]map[string]any{values}, firstString(sequence, ""))
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) InsertOrIgnore(data any) string {
	if emptyBatch(data) {
		return ""
	}
	values, err := convertToSliceMap(data)
	if err != nil {
		return r.generate("", nil, err)
	}
	statement, bindings := r.query.builder.ToInsertOrIgnoreSQL(uniformInsertRecords(values))
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) InsertUsing(columns []string, query any) string {
	r.query.builder.InsertUsing(toStrings(columns), r.query.subquery(query))
	statement, bindings := r.query.builder.ToInsertUsingSQL()
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) InsertOrIgnoreUsing(columns []string, query any) string {
	r.query.builder.ApplyBeforeQueryCallbacks()
	r.query.builder.InsertOrIgnoreUsing(toStrings(columns), r.query.subquery(query))
	statement, bindings := r.query.builder.ToInsertOrIgnoreUsingSQL()
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) UpdateFrom(values any) string {
	statement, bindings := r.query.builder.ToUpdateFromSQL(values)
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) Upsert(data any, uniqueBy []string, update ...any) string {
	if emptyBatch(data) {
		return ""
	}
	values, err := convertToSliceMap(data)
	if err != nil {
		return r.generate("", nil, err)
	}
	values = uniformInsertRecords(values)
	updates, insertOnly, err := upsertUpdates(values, update)
	if err != nil {
		return r.generate("", nil, err)
	}
	if insertOnly {
		return r.Insert(data)
	}
	statement, bindings := r.query.builder.ToUpsertSQL(values, uniqueBy, updates)
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) IncrementEach(columns any, extra ...map[string]any) string {
	r.query.builder.IncrementEach(columns, arithmeticExtra(extra)...)
	statement, bindings := r.query.builder.ToUpdateSQL(r.query.builder.UpdateValues())
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) DecrementEach(columns any, extra ...map[string]any) string {
	r.query.builder.DecrementEach(columns, arithmeticExtra(extra)...)
	statement, bindings := r.query.builder.ToUpdateSQL(r.query.builder.UpdateValues())
	return r.generateWrite(statement, bindings)
}

func (r *ToSql) Truncate() []string {
	statements := r.query.builder.ToTruncateSQL()
	if err := r.query.writeReady(); err != nil {
		r.generate("", nil, err)
		return nil
	}
	result := make([]string, len(statements))
	for i, statement := range statements {
		result[i] = r.generateWrite(statement.SQL, statement.Bindings)
		if result[i] == "" {
			return nil
		}
	}
	return result
}

func (r *ToSql) generateWrite(statement string, bindings []any) string {
	return r.generate(querybuilder.ReplacePlaceholders(statement, r.query.driver), bindings, r.query.writeReady())
}
