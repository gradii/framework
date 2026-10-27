package gorm

import (
	"reflect"
	"strings"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/errors"
)

// This file ports the relationship shorthands of Eloquent\Concerns\QueriesRelationships: each is a
// WhereHas (or WhereDoesntHave) whose callback is written for the caller, exactly as the PHP methods
// are.

// WhereRelation adds an existence condition with a single where on the related rows. Mirrors
// whereRelation: the column may instead be a callback that constrains the related rows freely.
func (r *Query) WhereRelation(relation string, column any, args ...any) contractsorm.Query {
	return r.WhereHas(relation, relationWhere(column, args))
}

// OrWhereRelation is WhereRelation with an "or" conjunction. Mirrors orWhereRelation.
func (r *Query) OrWhereRelation(relation string, column any, args ...any) contractsorm.Query {
	return r.OrWhereHas(relation, relationWhere(column, args))
}

// WhereDoesntHaveRelation adds an absence condition with a single where on the related rows.
// Mirrors whereDoesntHaveRelation.
func (r *Query) WhereDoesntHaveRelation(relation string, column any, args ...any) contractsorm.Query {
	return r.WhereDoesntHave(relation, relationWhere(column, args))
}

// OrWhereDoesntHaveRelation is WhereDoesntHaveRelation with an "or" conjunction. Mirrors
// orWhereDoesntHaveRelation.
func (r *Query) OrWhereDoesntHaveRelation(relation string, column any, args ...any) contractsorm.Query {
	return r.OrWhereDoesntHave(relation, relationWhere(column, args))
}

// relationWhere builds the callback the shorthands hand to WhereHas: a callback passes through, and a
// column with its arguments becomes q.Where(column, args...).
func relationWhere(column any, args []any) contractsorm.RelationCallback {
	switch callback := column.(type) {
	case contractsorm.RelationCallback:
		return callback
	case func(contractsorm.Query) contractsorm.Query:
		return contractsorm.RelationCallback(callback)
	}

	return func(query contractsorm.Query) contractsorm.Query {
		return query.Where(column, args...)
	}
}

// WithWhereHas constrains the query by the relation and eager loads the relation under the same
// constraint. Mirrors withWhereHas: the relation may carry a column list ("Books:id,title"), which
// applies to the eager load alone, as Str::before($relation, ':') strips it from the whereHas.
func (r *Query) WithWhereHas(relation string, args ...any) contractsorm.Query {
	name, _, _ := strings.Cut(relation, ":")

	callback, _, _, err := parseRelationArgs(args)
	if err != nil {
		return r.withError(err)
	}

	query := r.WhereHas(name, args...)
	if callback != nil {
		return query.With(map[string]contractsorm.RelationCallback{relation: callback})
	}

	return query.With(relation)
}

// WhereBelongsTo constrains the query to the rows that belong to the given model or models. Mirrors
// whereBelongsTo: the parent's foreign key is matched against the related models' keys, and the
// relation defaults to the related model's type name.
func (r *Query) WhereBelongsTo(related any, relation ...string) contractsorm.Query {
	return r.whereBelongsTo(related, relation, false)
}

// OrWhereBelongsTo is WhereBelongsTo with an "or" conjunction. Mirrors orWhereBelongsTo.
func (r *Query) OrWhereBelongsTo(related any, relation ...string) contractsorm.Query {
	return r.whereBelongsTo(related, relation, true)
}

func (r *Query) whereBelongsTo(related any, relation []string, or bool) contractsorm.Query {
	models := relatedModels(related)
	if len(models) == 0 {
		return r.withError(errors.OrmWhereBelongsToEmpty)
	}

	parent := r.parentModel()
	if parent == nil {
		return r.withError(errors.OrmQueryInvalidModel.Args("nil"))
	}

	name := ""
	if len(relation) > 0 {
		name = relation[0]
	}
	if name == "" {
		name = reflect.Indirect(reflect.ValueOf(models[0])).Type().Name()
	}

	desc, err := resolveRelation(parent, name)
	if err != nil {
		return r.withError(err)
	}
	if desc.kind != relKindBelongsTo || len(desc.references) == 0 {
		return r.withError(errors.OrmWhereBelongsToNotBelongsTo.Args(name, modelName(parent)))
	}

	// For a BelongsTo the parent carries the foreign key and the related model the owner key.
	reference := desc.references[0]
	keys := make([]any, 0, len(models))
	for _, model := range models {
		key, err := readParentColumn(r, model, reference.primaryColumn)
		if err != nil {
			return r.withError(err)
		}
		keys = append(keys, key)
	}

	column := reference.foreignTable + "." + reference.foreignColumn
	if or {
		return r.OrWhereIn(column, keys)
	}

	return r.WhereIn(column, keys)
}

// relatedModels flattens the argument whereBelongsTo accepts: a model, a pointer to one, or a slice
// of either. Each element is returned as a pointer so its columns can be read.
func relatedModels(related any) []any {
	if related == nil {
		return nil
	}

	reflected := reflect.ValueOf(related)
	if reflected.Kind() == reflect.Pointer && reflected.IsNil() {
		return nil
	}

	indirect := reflect.Indirect(reflected)
	switch indirect.Kind() {
	case reflect.Slice, reflect.Array:
		models := make([]any, 0, indirect.Len())
		for index := 0; index < indirect.Len(); index++ {
			element := indirect.Index(index)
			if element.Kind() != reflect.Pointer {
				element = element.Addr()
			}
			if !element.IsNil() {
				models = append(models, element.Interface())
			}
		}

		return models
	case reflect.Struct:
		if reflected.Kind() == reflect.Pointer {
			return []any{related}
		}
		pointer := reflect.New(indirect.Type())
		pointer.Elem().Set(indirect)

		return []any{pointer.Interface()}
	default:
		return nil
	}
}
