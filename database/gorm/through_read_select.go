package gorm

import querybuilder "github.com/gradii/framework/database/query"

// applyThroughReadSelect rewrites a plain through relation read's select list the way
// HasOneOrManyThrough::shouldSelect does for get()/first()/cursor()/find()/findMany(): the related
// table's own columns — `<related>.*`, or the columns the caller already chose — plus the through's
// parent key aliased as `laravel_through_key`. The oracle (illuminate/database v12.55.1, executed
// against sqlite with a country->posts-through-users schema) measures `$country->posts()->get()` as
//
//	select "posts".*, "users"."country_id" as "laravel_through_key"
//	from "posts" inner join "users" on "users"."id" = "posts"."user_id"
//	where "users"."country_id" = ?
//
// and `first(['title', 'body'])` as `select "title", "body", "users"."country_id" as
// "laravel_through_key" ... limit 1`. The alias is what keeps the joined through row's same-named
// columns (id, email, timestamps) from overwriting the related row's when the row hydrates.
//
// One-of-many through reads keep the select the relation read path already installed (`<related>.*`
// next to the aggregate join): the port's of-many corpora pin the relation builder's own select,
// which carries no alias.
//
// The reads Laravel forwards to the Eloquent builder rather than the relation — pluck, value and
// exists — suppress this rewrite through withoutThroughReadSelect, because there the builder
// replaces or writes the select list without the alias.
func (r *OrmBuilder) applyThroughReadSelect(builder *querybuilder.Builder) {
	cfg := r.conditions.throughRead
	if cfg == nil || r.conditions.oneOfMany != nil {
		return
	}

	if len(builder.Selects()) == 0 {
		builder.SelectRaw(r.ident(cfg.relatedTable, "*"), nil)
	}

	builder.SelectRaw(r.ident(cfg.throughTable, cfg.firstKey)+" as "+r.ident("laravel_through_key"), nil)
}

// withoutThroughReadSelect returns a private copy of the query with the through-read select rewrite
// skipped for one statement: pluck()/value()/exists() run on the Eloquent builder in Laravel, which
// replaces or writes the select list itself, so no laravel_through_key alias is appended there.
func (r *OrmBuilder) withoutThroughReadSelect() *OrmBuilder {
	query := r.clone()
	query.conditions.throughRead = nil

	return query
}
