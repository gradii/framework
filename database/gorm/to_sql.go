package gorm

import (
	"github.com/goravel/framework/contracts/log"
	basedb "github.com/goravel/framework/database/db"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/support/database"
)

// ToSql reports the statement a query would run, without running it.
//
// GORM used to report these by running its own builder in a dry-run session, so what a caller saw was
// GORM's idea of the statement. They are compiled here instead, through the same path the ORM executes,
// so the reported statement is the one that would actually run — dialect placeholders and all.
type ToSql struct {
	log   log.Log
	query *Query
	raw   bool
}

func NewToSql(query *Query, log log.Log, raw bool) *ToSql {
	return &ToSql{
		log:   log,
		query: query,
		raw:   raw,
	}
}

func (r *ToSql) Count() string {
	query := buildSelectForCount(r.query)

	statement, bindings, err := query.compileAggregate("count", countColumns(query))

	return r.render(statement, bindings, err)
}

func (r *ToSql) Create(value any) string {
	// The value names the table and carries the primary key, exactly as it does when Create runs, so
	// the model has to be resolved before the statement is compiled. Without it the insert renders
	// "insert into <nil>", and the auto-increment column cannot be recognised either, so a zero key
	// stays in the column list.
	query := r.query.dest(value).buildConditions()

	records, err := query.insertRecords(value)
	if err != nil {
		return r.render("", nil, err)
	}

	statement, bindings, err := query.compileInsertAll(records)

	return r.render(statement, bindings, err)
}

func (r *ToSql) Delete(dests ...any) string {
	var (
		dest  any
		query *Query
	)

	if len(dests) > 0 {
		dest = dests[0]
		query = r.query.dest(dest).addGlobalScopes().buildConditions()
	} else {
		query = r.query.addGlobalScopes().buildConditions()
	}

	// A soft-delete model is not removed: the statement the ORM runs is the update that stamps the
	// deleted_at column, built from the same assignments the executing path builds.
	if column, ok := query.softDeleteColumn(); ok {
		assignments, err := query.updateAssignments(map[string]any{column: query.softDeleteValue()})
		if err != nil {
			return r.render("", nil, err)
		}

		statement, bindings, err := query.compileSoftDeleteUpdate(assignments)

		return r.render(statement, bindings, err)
	}

	statement, bindings, err := query.compileDelete()

	return r.render(statement, bindings, err)
}

func (r *ToSql) Find(dest any, conds ...any) string {
	query := r.query.dest(dest).addGlobalScopes().buildConditions()
	query = query.withFindConditions(conds)

	statement, bindings, err := query.compileSelect()

	return r.render(statement, bindings, err)
}

func (r *ToSql) First(dest any) string {
	query := r.query.dest(dest).addGlobalScopes().buildConditions()

	statement, bindings, err := query.compileFirst()

	return r.render(statement, bindings, err)
}

func (r *ToSql) ForceDelete(dests ...any) string {
	var (
		dest  any
		query *Query
	)

	if len(dests) > 0 {
		dest = dests[0]
		query = r.query.dest(dest).WithTrashed().(*Query).addGlobalScopes().buildConditions()
	} else {
		query = r.query.WithTrashed().(*Query).addGlobalScopes().buildConditions()
	}

	statement, bindings, err := query.compileDelete()

	return r.render(statement, bindings, err)
}

// Get looks a model up, which is the same statement Find reports without conditions.
func (r *ToSql) Get(dest any) string {
	return r.Find(dest)
}

func (r *ToSql) Pluck(column string, dest any) string {
	// dest names the model the plucked column belongs to, exactly as it does when the ORM runs
	// Pluck through the model: without it the select compiles with no FROM clause.
	query := r.query.dest(dest).addGlobalScopes().buildConditions()

	// The column replaces whatever select list the query carried, which is what the executed form does.
	query.builder.SetSelects([]any{column})
	_ = query.builder.SetBindings(nil, "select")
	query.conditions.raw = nil

	statement, bindings, err := query.compileSelect()

	return r.render(statement, bindings, err)
}

func (r *ToSql) Save(dest any) string {
	if database.GetID(dest) == nil {
		// Same resolution as Create: the insert branch of Save needs the model to name the table and
		// to recognise the auto-increment column.
		query := r.query.dest(dest).buildConditions()

		records, err := query.insertRecords(dest)
		if err != nil {
			return r.render("", nil, err)
		}

		statement, bindings, err := query.compileInsertAll(records)

		return r.render(statement, bindings, err)
	}

	// A model that carries a key is written column by column, addressed by that key, which is the
	// statement Save runs.
	query := r.query.dest(dest).addGlobalScopes().buildConditions()

	assignments, err := query.saveAssignments(dest)
	if err != nil {
		return r.render("", nil, err)
	}

	statement, bindings, err := query.compileUpdateScoped(assignments, true)

	return r.render(statement, bindings, err)
}

func (r *ToSql) Sum(column string, dest any) string {
	// dest names the model the summed column belongs to, exactly as it does when the ORM runs Sum
	// through the model: without it the aggregate compiles with no FROM clause.
	query := r.query.dest(dest).addGlobalScopes().buildConditions()

	statement, bindings, err := query.compileAggregate("sum", []any{column})

	return r.render(statement, bindings, err)
}

func (r *ToSql) Update(column any, value ...any) string {
	if _, ok := column.(string); !ok && len(value) > 0 {
		return ""
	}

	query := r.query.addGlobalScopes().buildConditions()

	values := column
	if name, ok := column.(string); ok && len(value) > 0 {
		values = map[string]any{name: value[0]}
	}

	assignments, err := query.updateAssignments(values)
	if err != nil {
		return r.render("", nil, err)
	}

	statement, bindings, err := query.compileUpdateScoped(assignments, false)

	return r.render(statement, bindings, err)
}

// render returns the statement in the form the caller asked for, or reports why it could not be built.
func (r *ToSql) render(statement string, bindings []any, err error) string {
	if err != nil {
		if r.log != nil {
			r.log.Errorf("failed to get sql: %v", err)
		}

		return ""
	}

	if r.raw {
		return basedb.Explain(statement, bindings...)
	}

	return querybuilder.ReplacePlaceholders(statement, r.query.dbConfig.Driver)
}
