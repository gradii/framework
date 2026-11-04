package orm

// The names the two framework-injected, soft-delete predicates are registered under, so a caller
// can remove exactly one of them by name — Laravel's withoutGlobalScope(SoftDeletingScope::class)
// for the model's own predicate and HasOneOrManyThrough::withTrashedParents() /
// withoutGlobalScope('SoftDeletableHasManyThrough') for a through relation's.
//
// Laravel registers global scopes in a per-class map keyed by name: a Scope instance is keyed by
// get_class($scope) (Concerns/HasGlobalScopes::addGlobalScope), a closure by spl_object_hash, and a
// named implementation by the name the caller gave. Both soft-delete predicates are registered
// this way — the model's SoftDeletingScope by its class name, and a through relation's through-side
// predicate by the literal 'SoftDeletableHasManyThrough' (HasOneOrManyThrough::performJoin) — and
// the port's built-in predicates are the twins of those two, so they answer to the same names.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe.php §A):
// getGlobalScopes() reports the keys ["Illuminate\\Database\\Eloquent\\SoftDeletingScope",
// "email_scope"] for a model using SoftDeletes plus one named user scope.

// SoftDeletingScope is the name a soft-deletable model's automatic "not deleted" predicate is
// registered under. The value is the exact key Laravel uses — the SoftDeletingScope class's
// fully-qualified name, which is what withoutGlobalScope(SoftDeletingScope::class) passes — so
// WithoutGlobalScopes(SoftDeletingScope) drops exactly this predicate and leaves every user scope
// applied, and WithoutGlobalScopes(SoftDeletingScope) combined with OnlyTrashed keeps the
// "is not null" the onlyTrashed macro adds after removing the scope.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe.php §B/§C):
// default compiles `select * from "sr_users" where "sr_users"."deleted_at" is null and "email" = ?`;
// withoutGlobalScope(SoftDeletingScope::class) compiles `... where "email" = ?`; the same call plus
// onlyTrashed() compiles `... where "sr_users"."deleted_at" is not null and "email" = ?`.
const SoftDeletingScope = "Illuminate\\Database\\Eloquent\\SoftDeletingScope"

// SoftDeletableHasManyThrough is the name a HasOneThrough / HasManyThrough relation registers its
// through-side soft-delete predicate under — Laravel's literal withGlobalScope(
// 'SoftDeletableHasManyThrough', fn ($query) => $query->whereNull($this->throughParent->
// getQualifiedDeletedAtColumn())) (HasOneOrManyThrough::performJoin). Removing that name —
// directly, or through the relation's withTrashedParents() — drops the through predicate and
// leaves the related model's SoftDeletingScope in place.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe3.php §E/§F):
// `$country->posts()` compiles `... where "sr_users"."country_id" = ? and "sr_posts"."deleted_at"
// is null and "sr_users"."deleted_at" is null`; `->withTrashedParents()` and
// `->withoutGlobalScope('SoftDeletableHasManyThrough')` both drop only the through predicate; an
// eager `with(['posts' => fn ($q) => $q->withTrashedParents()])` drops it too.
const SoftDeletableHasManyThrough = "SoftDeletableHasManyThrough"
