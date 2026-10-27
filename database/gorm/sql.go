package gorm

import (
	"fmt"
	"reflect"
	"strings"

	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/database/query/core"
	"github.com/goravel/framework/errors"
)

// This file compiles a query's conditions into SQL.
//
// The ORM used to hand its conditions to GORM and let GORM build the statement. That made GORM the
// only thing that knew what SQL the ORM produced. Compiling here instead puts the ORM on the
// framework's own query builder, which already owns the grammars, and leaves execution as the only
// thing still borrowed.

// compileSelect renders the query as a select statement.
//
// A raw statement is used as given: the caller wrote the whole thing, so none of the query's own
// clauses — including the soft-delete predicate — are applied to it.
func (r *Query) compileSelect() (string, []any, error) {
	if raw := r.conditions.raw; raw != nil {
		return fmt.Sprint(raw.query), raw.args, nil
	}

	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	sql, bindings := builder.ToSelectSQL()
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// compileFirst renders the query as the single-row select First uses: the model's primary key
// orders it when the caller asked for no order, and one row is returned.
func (r *Query) compileFirst() (string, []any, error) {
	if len(r.builder.State().Orders) == 0 {
		if column := r.primaryKeyColumn(); column != "" {
			r.builder.OrderByRaw(column, nil)
		}
	}

	r.builder.Limit(1)

	sql, bindings, err := r.compileSelect()
	if err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// primaryKeyColumnName returns the model's first primary key column, unqualified, or "" when the
// model declares none.
func (r *Query) primaryKeyColumnName() string {
	model := r.parentModel()
	if model == nil {
		return ""
	}

	schema, err := parseSchema(model)
	if err != nil || len(schema.PrimaryFields) == 0 {
		return ""
	}

	return schema.PrimaryFields[0].DBName
}

// primaryKeyColumn returns the model's first primary key column, qualified by its table, or "" when
// the model declares none.
func (r *Query) primaryKeyColumn() string {
	model := r.parentModel()
	if model == nil {
		return ""
	}

	schema, err := parseSchema(model)
	if err != nil || len(schema.PrimaryFields) == 0 {
		return ""
	}

	return r.ident(schema.Table, schema.PrimaryFields[0].DBName)
}

// compileAggregate renders the query as an aggregate select, such as a count.
func (r *Query) compileAggregate(function string, columns []any) (string, []any, error) {
	builder, err := r.newBuilder()
	if err != nil {
		return "", nil, err
	}

	// ToAggregateSQL mirrors Builder::aggregate(): with neither unions nor havings it drops the select
	// list and, with it, the select bindings a subselect contributed, so the bindings handed to the
	// executor match the placeholders left in the SQL. SetAggregate + ToSelectSQL compiled the same SQL
	// but leaked those bindings.
	sql, bindings := builder.ToAggregateSQL(core.Aggregate{Function: function, Columns: columns})
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// compileDelete renders the query as a delete statement.
//
// The rows are named by the query's destination or model when either carries a key, and a statement
// that would remove every row is refused rather than compiled. The query's scopes — the caller's own
// and the model's global ones — are folded in first, so a scope's where is what the guard sees: GORM
// built the scopes onto the statement and then checked it, and a scoped delete is safe for the same
// reason an explicit one is.
func (r *Query) compileDelete() (string, []any, error) {
	query := r.withScopesApplied()

	builder, err := query.newBuilder()
	if err != nil {
		return "", nil, err
	}

	if !query.applyDeleteKeyWhere(builder) && !query.hasWhereConditions() {
		return "", nil, errors.OrmMissingWhereClause
	}

	sql, bindings := builder.ToDeleteSQL()
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// compileUpdate renders the query as an update statement for the given values.
func (r *Query) compileUpdate(values map[string]any) (string, []any, error) {
	return r.compileUpdateScoped(values, false)
}

// compileSoftDeleteUpdate renders the update a soft delete rewrites into, which is scoped the way a
// delete is rather than the way an update is.
func (r *Query) compileSoftDeleteUpdate(values map[string]any) (string, []any, error) {
	return r.compileUpdateScoped(values, true)
}

// compileUpdateScoped renders an update statement, scoping it by the query's model key.
//
// A soft delete scopes by the destination's key instead, because Delete(&user) names the row through
// its destination rather than through Model(). Whichever applies joins the where clause last, after
// the caller's conditions and the soft-delete predicate, which is the order GORM built it in. A
// statement that would write every row is refused rather than compiled.
//
// The query's scopes — the caller's own and the model's global ones — are folded in before the guard,
// as they are for a delete, so a scope's where satisfies it.
func (r *Query) compileUpdateScoped(values map[string]any, byDest bool) (string, []any, error) {
	query := r.withScopesApplied()

	builder, err := query.newBuilder()
	if err != nil {
		return "", nil, err
	}

	keyed := query.applyUpdateKeyWhere(builder)
	if byDest {
		keyed = query.applyDeleteKeyWhere(builder) || keyed
	}

	if !keyed && !query.hasWhereConditions() {
		return "", nil, errors.OrmMissingWhereClause
	}

	sql, bindings := builder.ToUpdateSQL(values)
	if err := builder.Error(); err != nil {
		return "", nil, err
	}

	return sql, bindings, nil
}

// applySelectSubAggregates adds a sub-select column per queued WithCount / WithMax / WithExists and the
// like.
//
// The subqueries are appended to whatever columns the caller already asked for, so the parent's own
// columns still come first and the row can be scanned into the model. Each alias is the one fixed when
// the sub-select was queued: aggregateAlias' `<relation>_<fn>[_<column>]` shape, or the explicit alias
// of a contractsorm.RelationCount, so the column comes back under the name the caller expects.
func (r *Query) applySelectSubAggregates(builder *querybuilder.Builder) error {
	if len(r.conditions.selectSubs) == 0 {
		return nil
	}

	parent := r.parentModel()
	if parent == nil {
		return errors.OrmQueryEmptyRelation
	}

	for _, sub := range r.conditions.selectSubs {
		desc, err := resolveRelation(parent, sub.relation)
		if err != nil {
			return err
		}

		inner, err := r.compileAggregateSubquery(desc, sub)
		if err != nil {
			return err
		}
		if inner == nil {
			continue
		}

		subquery, bindings := inner.ToSelectSQL()

		alias := sub.alias
		if alias == "" {
			alias = aggregateAlias(sub.relation, sub.function, sub.column)
		}

		if sub.function == "exists" {
			// A bare EXISTS as a column is a native bool on PostgreSQL, which will not scan into an
			// integer field, and SQL Server rejects it outright, so it is wrapped into a portable 0/1.
			builder.SelectRaw("CASE WHEN EXISTS ("+subquery+") THEN 1 ELSE 0 END AS "+r.ident(alias), bindings)

			continue
		}

		builder.SelectRaw("("+subquery+") AS "+r.ident(alias), bindings)
	}

	return nil
}

// newBuilder builds a query builder carrying everything the conditions describe. The persistent
// builder already holds the SQL clauses (select, join, where, group, having, order, limit, offset,
// distinct, lock); this clones it and adds the ORM-specific parts that compile differently per
// statement: table, where, relation-existence subqueries, soft-delete predicate, and select-subs.
func (r *Query) newBuilder() (*querybuilder.Builder, error) {
	// A scope is a function the caller queued with Scopes, or one addGlobalScopes folded in from the
	// model. They mutate the query, so they're applied first.
	if len(r.conditions.scopes) > 0 {
		scoped := r.withScopesApplied()
		if scoped.conditions.err != nil {
			return nil, scoped.conditions.err
		}

		return scoped.newBuilder()
	}

	// A condition the ORM could not accept is carried on the query rather than returned outright, so
	// it is reported from here.
	if r.conditions.err != nil {
		return nil, r.conditions.err
	}

	// Clone the persistent builder so compiling multiple statements from the same query doesn't
	// accumulate state (soft-delete predicate, relation subqueries).
	builder := r.builder.Clone()

	if err := r.applyTable(builder); err != nil {
		return nil, err
	}

	// selectSubs are ORM-level select expressions (WithCount, WithSum), so they live in conditions
	// rather than on the builder. When present, the parent's own columns must be in the select too.
	if len(r.conditions.selectSubs) > 0 && len(builder.Selects()) == 0 {
		if parent := r.parentModel(); parent != nil {
			builder.SelectRaw(r.ident(r.parentTable(parent), "*"), nil)
		}
	}

	if err := r.applySelectSubAggregates(builder); err != nil {
		return nil, err
	}

	r.applyWhere(builder)

	// Relation existence conditions join the where clause before the soft-delete predicate, which is
	// the order GORM built them in.
	if err := r.applyRelations(builder); err != nil {
		return nil, err
	}

	// The soft-delete predicate belongs to the compiled statement too, not only to the GORM
	// statement: any read that runs through this path would otherwise see deleted rows. It is
	// appended last so it applies to the whole query, exactly as the GORM clause did.
	if err := r.applySoftDelete(builder); err != nil {
		return nil, err
	}

	return builder, nil
}

// applySoftDelete adds the soft-delete predicate when the model declares the framework's soft-delete
// type and the statement is not unscoped: "deleted_at IS NULL" by default, "deleted_at IS NOT NULL"
// when OnlyTrashed asked for the trashed rows alone. WithTrashed drops the predicate entirely, so it
// is only ignored here when OnlyTrashed has not overridden it.
func (r *Query) applySoftDelete(builder *querybuilder.Builder) error {
	if (r.conditions.withTrashed && !r.conditions.onlyTrashed) || !r.usesFrameworkSoftDelete() {
		return nil
	}

	model := r.parentModel()
	if model == nil {
		return nil
	}

	column, ok := softDeleteColumn(model)
	if !ok {
		return nil
	}

	table, err := tableNameFor(model)
	if err != nil || table == "" {
		return err
	}

	operator := "IS NULL"
	if r.conditions.onlyTrashed {
		operator = "IS NOT NULL"
	}

	builder.WhereRaw(r.ident(table, column)+" "+operator, nil, core.BooleanAnd)

	return nil
}

// applyTable points the builder at the query's table, whether it was named directly or derived from
// the model.
func (r *Query) applyTable(builder *querybuilder.Builder) error {
	if r.conditions.table != nil {
		builder.From(r.conditions.table.name, "")

		return nil
	}

	model := r.parentModel()
	if model == nil {
		return nil
	}

	table, err := tableNameFor(model)
	if err != nil {
		return err
	}
	builder.From(table, "")

	return nil
}

// applyWhere translates the conditions' where clauses onto the builder.
//
// The shapes are the ones the ORM accepts: a raw fragment with its bindings, a column with its
// value, a slice standing for an IN list, a callback producing a nested group, and the JSON
// operators the conditions mark with a type.
func (r *Query) applyWhere(builder *querybuilder.Builder) *querybuilder.Builder {
	for _, item := range r.conditions.where {
		boolean := core.BooleanAnd
		if item.Or {
			boolean = core.BooleanOr
		}

		r.applySingleWhere(builder, item, boolean)
	}

	return builder
}

func (r *Query) applySingleWhere(builder *querybuilder.Builder, item contractsdriver.Where, boolean string) {
	switch item.Type {
	case contractsdriver.WhereTypeJsonContains:
		builder.WhereJsonContains(fmt.Sprint(item.Query), firstArg(item.Args), boolean, item.IsNot)

		return
	case contractsdriver.WhereTypeJsonContainsKey:
		builder.WhereJsonContainsKey(fmt.Sprint(item.Query), boolean, item.IsNot)

		return
	case contractsdriver.WhereTypeJsonLength:
		// WhereJsonLength is the one shape with no boolean argument on the builder: its plain method
		// is hard-wired to "and", so an OrWhereJsonLength used to compile to "and" and its clause
		// could only match when the other side also matched. The builder's own orWhereJsonLength is
		// the OR counterpart, so pick it when the condition carries the "or" connector.
		if boolean == core.BooleanOr {
			builder.OrWhereJsonLength(fmt.Sprint(item.Query), item.Args...)
		} else {
			builder.WhereJsonLength(fmt.Sprint(item.Query), item.Args...)
		}

		return
	}

	switch query := item.Query.(type) {
	case string:
		switch {
		case len(item.Args) == 0:
			builder.WhereRaw(query, nil, boolean)
		case len(item.Args) == 1 && isSlice(item.Args[0]) && !isByteSlice(item.Args[0]) && containsPlaceholder(query):
			// A raw fragment whose one placeholder is fed a slice is the ORM's "col in ?" shorthand
			// (the pivot and WhereAny chains emit the same "… IN ?" shape). The GORM-backed builder
			// expanded the slice into one placeholder per value, wrapped in parentheses; binding the
			// slice whole leaves one placeholder against N bindings, and the database rejects the
			// statement. Expand it the same way, leaving every other fragment shape on the WhereRaw
			// path below.
			//
			// A byte slice is not a list: isSlice also matches []byte, but a caller binds bytes to the
			// fragment's placeholder as one binary value, so it must keep the single placeholder and
			// fall through to WhereRaw below. Laravel renders the same fragment with one placeholder,
			// because a PHP string (its only byte-string type) is a single value (oracle.php).
			expanded, bindings := expandPlaceholderSlice(query, item.Args[0])
			builder.WhereRaw(expanded, bindings, boolean)
		case containsPlaceholder(query):
			builder.WhereRaw(query, item.Args, boolean)
		case len(item.Args) == 1 && isSlice(item.Args[0]) && !isByteSlice(item.Args[0]):
			builder.WhereIn(query, item.Args[0], boolean, item.IsNot)
		default:
			applySimpleWhere(builder, query, item.Args, boolean)
		}
	case func(contractsorm.Query) contractsorm.Query:
		builder.WhereNested(func(nested *querybuilder.Builder) *querybuilder.Builder {
			return r.applyNestedWhere(query, nested)
		}, boolean)
	default:
		applySimpleWhere(builder, item.Query, item.Args, boolean)
	}
}

// applySimpleWhere routes a (column, value) condition to the builder method that carries
// the connector's boolean. applySingleWhere computes the boolean for every other shape; the
// default (column, value) form used to be handed straight to builder.Where, which is always
// "and", so an OrWhere rendered as "and" and its clause could never match. Mirrors the
// facade's addSimpleWhere.
func applySimpleWhere(builder *querybuilder.Builder, query any, args []any, boolean string) {
	switch boolean {
	case core.BooleanOr:
		builder.OrWhere(query, args...)
	case core.BooleanAndNot:
		builder.WhereNot(query, args...)
	case core.BooleanOrNot:
		builder.OrWhereNot(query, args...)
	default:
		builder.Where(query, args...)
	}
}

// applyNestedWhere runs a callback against a child builder, so an `or` group the caller wrote keeps
// its own grouping.
func (r *Query) applyNestedWhere(callback func(contractsorm.Query) contractsorm.Query, builder *querybuilder.Builder) *querybuilder.Builder {
	child := NewQuery(r.ctx, r.config, r.dbConfig, r.writer, r.tx, r.grammar, r.log, r.modelToObserver, nil, r.telemetryResolver)
	child.queryGrammar = r.queryGrammar
	child.conditions = r.conditions
	child.conditions.where = nil

	// The child compiles its own wheres onto the builder it was handed.
	result := callback(child)
	if nested, ok := result.(*Query); ok {
		return nested.applyWhere(builder)
	}

	return builder
}

// expandPlaceholderSlice rewrites the fragment's first binding placeholder into one placeholder per
// element of the slice, as the GORM-backed builder expanded a slice argument. The placeholders are
// wrapped in parentheses unless the replaced placeholder already sits just inside one, so both
// "col in ?" and "col in (?)" render a valid IN list. A placeholder-less fragment, or a value that is
// not a slice, is returned unchanged.
func expandPlaceholderSlice(query string, value any) (string, []any) {
	values, ok := sliceValues(value)
	if !ok {
		return query, []any{value}
	}

	index := strings.IndexByte(query, '?')
	if index < 0 {
		return query, values
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", ")
	if index > 0 && query[index-1] == '(' {
		return query[:index] + placeholders + query[index+1:], values
	}

	return query[:index] + "(" + placeholders + ")" + query[index+1:], values
}

// isByteSlice reports whether value is a byte slice or byte array. []byte is how a caller binds one
// binary value to a placeholder, so it is a value rather than a list to expand, even though isSlice
// also matches it. Pointer and interface indirection is peeled the same way sliceValues peels it, so a
// *[]byte behaves like its target.
func isByteSlice(value any) bool {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return false
	}
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return false
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return false
	}

	return reflected.Type().Elem().Kind() == reflect.Uint8
}

// containsPlaceholder reports whether a raw fragment carries a binding placeholder.
func containsPlaceholder(query string) bool {
	for _, char := range query {
		if char == '?' {
			return true
		}
	}

	return false
}

// firstArg returns the first binding, or nil when there is none.
func firstArg(args []any) any {
	if len(args) == 0 {
		return nil
	}

	return args[0]
}
