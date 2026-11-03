package gorm

import (
	"reflect"
	"strings"
)

// This file completes Laravel's many-to-many read shape next to applyPivotReadSelect:
// BelongsToMany::hydratePivotRelation migrates every pivot_-prefixed attribute of a returned row
// into the relation's pivot model, and the reads Laravel forwards to the plain builder — value,
// pluck, exists — bypass shouldSelect, so their statements carry no pivot_ aliases.

// withoutPivotReadSelect returns a private copy of the query with the pivot-read select rewrite
// skipped for one statement. value()/pluck()/exists() are forwarded to the Eloquent builder in
// Laravel, which writes its own select list, so no alias list is appended there (oracle,
// illuminate/database v12.55.1, executed against sqlite: `$user->friends()->value('email')`
// compiles `select "email" from "users" inner join "friends" on "users"."id" =
// "friends"."friend_id" where "friends"."user_id" = ? limit 1`).
func (r *OrmBuilder) withoutPivotReadSelect() *OrmBuilder {
	query := r.clone()
	query.conditions.pivotRead = nil

	return query
}

// hydratePivotRow migrates a scanned row's pivot_-prefixed aliases into dest's declared pivot
// struct — the Go form of BelongsToMany::hydratePivotRelation and its migratePivotAttributes,
// which strip the prefix off each pivot_ attribute and set the rest as the pivot model's own.
// dest is the related-model value the row was scanned into: a struct or a pointer to one. A read
// with no pivot config, or a related model declaring no field by the relation's pivot field name,
// hydrates nothing.
func (r *OrmBuilder) hydratePivotRow(row map[string]any, dest reflect.Value) error {
	cfg := r.conditions.pivotRead
	if cfg == nil || cfg.plan == nil {
		return nil
	}

	data := make(map[string]any, len(cfg.plan.extraColumns)+2)
	for key, value := range row {
		if column, ok := strings.CutPrefix(key, "pivot_"); ok {
			data[column] = value
		}
	}
	if len(data) == 0 {
		return nil
	}

	return writePivotField(r.ctx, dest, data, cfg.plan)
}
