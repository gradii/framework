package gorm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
)

// This file tests the exact SQL emitted by Goravel's relation system for every relation kind.
// The Related() statements are compiled through the same path the ORM executes and pinned here, so
// any change to the query builder is flagged loudly.

// ---------------------------------------------------------------------------
// Related() SQL — one query method per relation kind
// ---------------------------------------------------------------------------

func TestRelated_HasOne_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 7}, "Profile")
	sql := newRelationSQL(t, rel, &relProfile{})
	// Oracle (sqlite): $user->profile()->getQuery()->toSql() renders the qualified foreign key and
	// HasOneOrMany::addConstraints' not-null guard.
	assert.Equal(t, `select * from "rel_profiles" where "rel_profiles"."user_id" = ? and "rel_profiles"."user_id" is not null`, sql)
}

func TestRelated_HasMany_SQL_Exact(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 7}, "Books")
	sql := newRelationSQL(t, rel, &[]relBook{})
	// Oracle (sqlite): $user->books()->getQuery()->toSql().
	assert.Equal(t, `select * from "rel_books" where "rel_books"."user_id" = ? and "rel_books"."user_id" is not null`, sql)
}

func TestRelated_BelongsTo_SQL_Exact(t *testing.T) {
	q := newRelQueryWith(t, &relBook{})
	rel := q.Related(&relBook{AuthorID: 5}, "Author")
	sql := newRelationSQL(t, rel, &relUser{})
	// Oracle (illuminate/database v12.55.1, executed against sqlite): $book->author()->getQuery()
	// ->toSql() renders the qualified owner key — BelongsTo::addConstraints installs
	// `"users"."id" = ?`, not a bare "id".
	assert.Equal(t, `select * from "rel_users" where "rel_users"."id" = ?`, sql)
}

func TestRelated_Many2Many_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 7}, "Roles")
	sql := newRelationSQL(t, rel, &[]relRole{})
	// Oracle (illuminate/database v12.55.1, executed against sqlite): $user->friends()->get()
	// selects the related columns plus every pivot key aliased pivot_<column> — BelongsToMany::
	// shouldSelect — `"users".*, "friends"."user_id" as "pivot_user_id", "friends"."friend_id" as
	// "pivot_friend_id" ...`, measured with these fixture names below; the alias is what keeps the
	// joined pivot row (an `id` column on the pivot, say) from overwriting the related model's own
	// columns on hydration.
	assert.Equal(t, `select "rel_roles".*, "rel_user_roles"."rel_user_id" as "pivot_rel_user_id", "rel_user_roles"."rel_role_id" as "pivot_rel_role_id" from "rel_roles" inner join "rel_user_roles" ON "rel_user_roles"."rel_role_id" = "rel_roles"."id" where "rel_user_roles"."rel_user_id" = ?`, sql)
}

func TestRelated_MorphOne_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 9}, "Logo")
	sql := newRelationSQL(t, rel, &relLogo{})
	// Oracle (sqlite): $user->logo()->getQuery()->toSql() filters the morph type first, then the
	// qualified foreign key with its not-null guard.
	assert.Equal(t, `select * from "rel_logos" where "rel_logos"."logoable_type" = ? and "rel_logos"."logoable_id" = ? and "rel_logos"."logoable_id" is not null`, sql)
}

func TestRelated_MorphMany_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 9}, "Houses")
	sql := newRelationSQL(t, rel, &[]relHouse{})
	// Oracle (sqlite): $user->houses()->getQuery()->toSql().
	assert.Equal(t, `select * from "rel_houses" where "rel_houses"."houseable_type" = ? and "rel_houses"."houseable_id" = ? and "rel_houses"."houseable_id" is not null`, sql)
}

func TestRelated_MorphToMany_SQL(t *testing.T) {
	q := newRelQueryWith(t, &morphPost{})
	rel := q.Related(&morphPost{ID: 3}, "Tags")
	sql := newRelationSQL(t, rel, &[]morphTag{})
	// Oracle (executed against sqlite): $post->tags()->get() selects `"tags".*, "taggables".
	// "taggable_id" as "pivot_taggable_id", "taggables"."tag_id" as "pivot_tag_id", "taggables".
	// "taggable_type" as "pivot_taggable_type"` — MorphToMany::aliasedPivotColumns interleaves the
	// morph type between the related key and the withPivot columns; the fixture names render below.
	assert.Equal(t, `select "morph_tags".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."morph_tag_id" as "pivot_morph_tag_id", "taggables"."taggable_type" as "pivot_taggable_type" from "morph_tags" inner join "taggables" ON "taggables"."morph_tag_id" = "morph_tags"."id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?`, sql)
}

func TestRelated_MorphedByMany_SQL(t *testing.T) {
	q := newRelQueryWith(t, &morphTag{})
	rel := q.Related(&morphTag{ID: 1}, "Posts")
	sql := newRelationSQL(t, rel, &[]morphPost{})
	// The inverse morph read wears the same aliased select; the morph type column sits between the
	// related pivot key (morph_post_id on this side) and any withPivot columns.
	assert.Equal(t, `select "morph_posts".*, "taggables"."taggable_id" as "pivot_taggable_id", "taggables"."morph_post_id" as "pivot_morph_post_id", "taggables"."taggable_type" as "pivot_taggable_type" from "morph_posts" inner join "taggables" ON "taggables"."morph_post_id" = "morph_posts"."id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?`, sql)
}

func TestRelated_HasManyThrough_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relCountry{})
	rel := q.Related(&relCountry{ID: 1}, "Posts")
	sql := newRelationSQL(t, rel, &[]relPost{})
	// Oracle (illuminate/database v12.55.1, executed against sqlite): $country->posts()->get()
	// selects `"posts".*, "users"."country_id" as "laravel_through_key"` — HasOneOrManyThrough::
	// shouldSelect's list — measured with these fixture names as the statement below.
	assert.Equal(t, `select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ?`, sql)
}

func TestRelated_HasOneThrough_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relCountry{})
	rel := q.Related(&relCountry{ID: 1}, "FirstPost")
	sql := newRelationSQL(t, rel, &relPost{})
	// Oracle: $position->contract()->get() selects `"contracts".*, "users"."position_id" as
	// "laravel_through_key"` for a has-one-through read; the has-many-through fixture above
	// measures the same shape.
	assert.Equal(t, `select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" = ?`, sql)
}

// ---------------------------------------------------------------------------
// compileExistenceSubquery SQL — used by Has / WhereHas / DoesntHave
// ---------------------------------------------------------------------------

func runExistenceSQL(t *testing.T, model any, relation string, _ any) string {
	t.Helper()
	q := newRelQueryWith(t, model)
	desc, err := relations.ResolveRelation(model, relation)
	assert.NoError(t, err)
	inner, _, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	return sql
}

func TestCompileExistenceSubquery_HasOne_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Profile", &relProfile{})
	assert.Equal(t, `select * from "rel_profiles" where "rel_users"."id" = "rel_profiles"."user_id"`, sql)
}

func TestCompileExistenceSubquery_HasMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Books", &[]relBook{})
	assert.Equal(t, `select * from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

func TestCompileExistenceSubquery_BelongsTo_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relBook{}, "Author", &[]relUser{})
	assert.Equal(t, `select * from "rel_users" where "rel_books"."author_id" = "rel_users"."id"`, sql)
}

func TestCompileExistenceSubquery_Many2Many_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Roles", &[]relRole{})
	assert.Equal(t, `select * from "rel_roles" inner join "rel_user_roles" on "rel_roles"."id" = "rel_user_roles"."rel_role_id" where "rel_users"."id" = "rel_user_roles"."rel_user_id"`, sql)
}

func TestCompileExistenceSubquery_MorphOne_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Logo", &relLogo{})
	assert.Equal(t, `select * from "rel_logos" where "rel_users"."id" = "rel_logos"."logoable_id" and "rel_logos"."logoable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_MorphMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Houses", &[]relHouse{})
	assert.Equal(t, `select * from "rel_houses" where "rel_users"."id" = "rel_houses"."houseable_id" and "rel_houses"."houseable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_MorphToMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &morphPost{}, "Tags", &[]morphTag{})
	assert.Equal(t, `select * from "morph_tags" inner join "taggables" on "morph_tags"."id" = "taggables"."morph_tag_id" where "morph_posts"."id" = "taggables"."taggable_id" and "taggables"."taggable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_MorphedByMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &morphTag{}, "Posts", &[]morphPost{})
	assert.Equal(t, `select * from "morph_posts" inner join "taggables" on "morph_posts"."id" = "taggables"."morph_post_id" where "morph_tags"."id" = "taggables"."taggable_id" and "taggables"."taggable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_HasManyThrough_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relCountry{}, "Posts", &[]relPost{})
	assert.Equal(t, `select * from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_countries"."id" = "rel_users"."rel_country_id"`, sql)
}

func TestCompileExistenceSubquery_HasOneThrough_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relCountry{}, "FirstPost", &relPost{})
	assert.Equal(t, `select * from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_countries"."id" = "rel_users"."rel_country_id"`, sql)
}

// ---------------------------------------------------------------------------
// compileAggregateSubquery SQL — used by WithCount / WithMax / WithMin / WithSum / WithAvg / WithExists
// ---------------------------------------------------------------------------

func runAggregateSQL(t *testing.T, sub selectSub) string {
	t.Helper()
	return runAggregateRelationSQL(t, &relUser{}, "Books", sub)
}

func runAggregateRelationSQL(t *testing.T, model any, relation string, sub selectSub) string {
	t.Helper()
	q := newRelQueryWith(t, model)
	desc, err := relations.ResolveRelation(model, relation)
	assert.NoError(t, err)
	inner, err := q.compileAggregateSubquery(desc, sub)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	return sql
}

func TestCompileAggregateSubquery_Count_SQL(t *testing.T) {
	sql := runAggregateSQL(t, selectSub{relation: "Books", column: "*", function: "count"})
	assert.Equal(t, `select count(*) from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

func TestCompileAggregateSubquery_Sum_SQL(t *testing.T) {
	sql := runAggregateSQL(t, selectSub{relation: "Books", column: "id", function: "sum"})
	assert.Equal(t, `select sum("rel_books"."id") from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

func TestCompileAggregateSubquery_Max_SQL(t *testing.T) {
	sql := runAggregateSQL(t, selectSub{relation: "Books", column: "id", function: "max"})
	assert.Equal(t, `select max("rel_books"."id") from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

func TestCompileAggregateSubquery_Min_SQL(t *testing.T) {
	sql := runAggregateSQL(t, selectSub{relation: "Books", column: "id", function: "min"})
	assert.Equal(t, `select min("rel_books"."id") from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

func TestCompileAggregateSubquery_Avg_SQL(t *testing.T) {
	sql := runAggregateSQL(t, selectSub{relation: "Books", column: "id", function: "avg"})
	assert.Equal(t, `select avg("rel_books"."id") from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

func TestCompileAggregateSubquery_Exists_SQL(t *testing.T) {
	sql := runAggregateSQL(t, selectSub{relation: "Books", column: "*", function: "exists"})
	assert.Equal(t, `select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

// The aggregate body over a through relation wears the through join the oracle renders
// (`inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where
// "rel_countries"."id" = "rel_users"."rel_country_id"`, measured against illuminate/database
// v12.55.1) - the same order the through existence subquery pins.
func TestCompileAggregateSubquery_HasManyThrough_SQL(t *testing.T) {
	sql := runAggregateRelationSQL(t, &relCountry{}, "Posts", selectSub{relation: "Posts", column: "*", function: "count"})
	assert.Equal(t, `select count(*) from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_countries"."id" = "rel_users"."rel_country_id"`, sql)
}

// The aggregate subquery (WithCount / WithExists / WithMax / WithMin / WithSum / WithAvg) is
// compiled through compileExistenceSubquery, so applySubquerySoftDelete's predicate must appear
// there too. Nothing pinned that path when the predicate was introduced: disabling
// applySubquerySoftDelete left the whole aggregate suite green while only the existence pins
// failed, so a regression would have silently counted soft-deleted rows.
func TestCompileAggregateSubquery_FrameworkSoftDeletePredicate(t *testing.T) {
	sql := runAggregateRelationSQL(t, &relSoftOwner{}, "Books", selectSub{relation: "Books", column: "*", function: "count"})
	assert.Equal(t, `select count(*) from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" is null`, sql)
}

// The same aggregate path for a morph relation must carry both the morph type predicate and the
// soft-delete predicate.
func TestCompileAggregateSubquery_MorphFrameworkSoftDeletePredicate(t *testing.T) {
	sql := runAggregateRelationSQL(t, &relSoftOwner{}, "Houses", selectSub{relation: "Houses", column: "*", function: "count"})
	assert.Equal(t, `select count(*) from "rel_soft_houses" where "rel_soft_owners"."id" = "rel_soft_houses"."houseable_id" and "rel_soft_houses"."houseable_type" = ? and "rel_soft_houses"."deleted_at" is null`, sql)
}

// The pin above is exercised end to end through the public WithCount / WithExists entry points, so
// it covers applySelectSubAggregates too - not only the inner builder.
func TestWithCount_FrameworkSoftDeleteRelation(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	builder, err := toQuery(q.WithCount("Books")).newBuilder()
	require.NoError(t, err)
	sql, _ := builder.ToSelectSQL()

	assert.Equal(t, `select "rel_soft_owners".*, (select count(*) from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" is null) as "books_count" from "rel_soft_owners"`, sql)
}

func TestWithExists_FrameworkSoftDeleteRelation(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	builder, err := toQuery(q.WithExists("Books")).newBuilder()
	require.NoError(t, err)
	sql, _ := builder.ToSelectSQL()

	assert.Equal(t, `select "rel_soft_owners".*, CASE WHEN EXISTS (select 1 from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" is null) THEN 1 ELSE 0 END AS "books_exists" from "rel_soft_owners"`, sql)
}

// ---------------------------------------------------------------------------
// Soft-delete handling inside the existence subquery
// ---------------------------------------------------------------------------

// relSoftOwner's relations exercise the two soft-delete shapes: relSoftBook declares the framework's
// own type (databaseorm.DeletedAt), which is the one the compiled paths add a predicate for.
type relSoftBook struct {
	ID        uint
	UserID    uint
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at"`
}

func (relSoftBook) TableName() string { return "rel_soft_books" }

type relSoftHouse struct {
	ID            uint
	HouseableID   uint
	HouseableType string
	DeletedAt     databaseorm.DeletedAt `gorm:"column:deleted_at"`
}

func (relSoftHouse) TableName() string { return "rel_soft_houses" }

type relSoftOwner struct {
	ID     uint
	Books  []*relSoftBook  `gorm:"-"`
	Houses []*relSoftHouse `gorm:"-"`
}

func (relSoftOwner) TableName() string { return "rel_soft_owners" }

func (relSoftOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books":  contractsorm.HasMany{Related: &relSoftBook{}, ForeignKey: "user_id"},
		"Houses": contractsorm.MorphMany{Related: &relSoftHouse{}, Name: "houseable"},
	}
}

// A related model that declares the framework's own soft-delete type must carry its "not deleted"
// predicate inside the existence subquery - exactly as Laravel's SoftDeletingScope does and as the
// compiled relation read already does. Without it, has("Books") matches a parent whose only books
// are soft-deleted, which is a wrong result.
func TestCompileExistenceSubquery_FrameworkSoftDeletePredicate(t *testing.T) {
	sql := runExistenceSQL(t, &relSoftOwner{}, "Books", &[]relSoftBook{})
	assert.Equal(t, `select * from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" is null`, sql)
}

func TestCompileMorphExistenceSubquery_FrameworkSoftDeletePredicate(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	desc, err := relations.ResolveRelation(&relSoftOwner{}, "Houses")
	require.NoError(t, err)

	inner := q.compileMorphExistenceSubquery(desc, "rel_soft_owners", nil)
	sql, _ := inner.ToSelectSQL()

	assert.Equal(t, `select * from "rel_soft_houses" where "rel_soft_owners"."id" = "rel_soft_houses"."houseable_id" and "rel_soft_houses"."houseable_type" = ? and "rel_soft_houses"."deleted_at" is null`, sql)
}

// The compiled relation read already filters soft-deleted related rows; the existence subquery shares
// that predicate, so this pins the read side the fix was aligned with.
func TestRelated_FrameworkSoftDeleteReadPredicate(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	rel := q.Related(&relSoftOwner{ID: 7}, "Books")
	sql := newRelationSQL(t, rel, &[]relSoftBook{})

	assert.Contains(t, sql, `"rel_soft_books"."deleted_at" is null`)
}

// ---------------------------------------------------------------------------
// Relation callback non-where clauses inside the existence subquery
// ---------------------------------------------------------------------------

// A relation callback's non-where clauses (here OrderBy) are not carried into the existence subquery:
// applyRelationScope translates the callback's where conditions only. Laravel DOES emit the order by
// inside the subquery, and so did the pre-conversion GORM renderer, so this is a real - though
// semantically harmless, since an existence subquery selects `*` and aggregates nothing - parity
// difference. It is pinned here so it stays deliberate rather than being mistaken for parity.
func TestCompileExistenceSubquery_CallbackOrderByNotReflected(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := relations.ResolveRelation(&relUser{}, "Books")
	require.NoError(t, err)

	callback := func(query contractsorm.Query) contractsorm.Query {
		return query.OrderBy("id", "desc")
	}
	inner, _, err := q.compileExistenceSubquery(desc, callback)
	require.NoError(t, err)
	sql, _ := inner.ToSelectSQL()

	assert.Equal(t, `select * from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
	assert.NotContains(t, strings.ToLower(sql), "order by")
}

// The operator a caller passes to Has / WhereHas reaches the count comparison through the compiled
// builder, so a valid operator renders the comparison unchanged. Before, the string was concatenated
// into the fragment.
func TestHas_CountComparisonKeepsValidOperators(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).Has("Books", ">", 3).(*OrmBuilder)

	sql, bindings, err := q.dest(&[]relUser{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Contains(t, sql, `(select count(*) from "rel_books"`)
	// The count inlines like Laravel's addWhereCountQuery (new Expression($count)): the oracle
	// renders `) > 3` with no binding.
	assert.Contains(t, sql, `) > 3`)
	assert.Empty(t, bindings)
}

// An operator the builder does not know is swapped for "=" and bound as the value, the handling
// Laravel's addWhereCountQuery inherits from Builder::where() (which drops the original count, as
// Laravel does). Before, the operator was concatenated, so ">= 1) OR 1=1 --" reached the SQL.
func TestHas_CountComparisonValidatesTheOperator(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).Has("Books", ">= 1) OR 1=1 --", 3).(*OrmBuilder)

	sql, bindings, err := q.dest(&[]relUser{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Contains(t, sql, `) = ?`)
	assert.NotContains(t, sql, "OR 1=1")
	assert.Equal(t, []any{">= 1) OR 1=1 --"}, bindings)
}

// ---------------------------------------------------------------------------
// Existence subqueries: callScope grouping and nested-clause positioning
// ---------------------------------------------------------------------------

// relScopeOwner / relScopeScoped pin a related model's global scopes inside an existence subquery:
// a scope's or-chain must become its own nested group, the way Laravel's callScope regroups the
// wheres a scope added, so a correlation-less row cannot satisfy the exists.
type relScopeOwner struct {
	ID     uint
	Scoped []*relScopeScoped `gorm:"-"`
}

func (relScopeOwner) TableName() string { return "rel_scope_owners" }

func (relScopeOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Scoped": contractsorm.HasMany{Related: &relScopeScoped{}, ForeignKey: "owner_id"},
	}
}

type relScopeScoped struct {
	ID      uint
	OwnerID uint
	Foo     int
	Bar     int
	Baz     int
}

func (relScopeScoped) TableName() string { return "rel_scope_scoped" }

func (relScopeScoped) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "either", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("foo", 1).OrWhere("bar", 2) }},
		{Name: "second", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("baz", 3) }},
	}
}

// A scope's or-chain inside the exists body must be grouped exactly as Laravel's callScope groups
// it: without the group the body would read `corr and foo or bar and baz`, which unrelated rows can
// satisfy. Fresh oracle (illuminate/database v12.55.1, sqlite; bindings [1, 2, 3]):
//
//	select * from "rel_scope_owners" where exists (select * from "rel_scope_scoped" where
//	"rel_scope_owners"."id" = "rel_scope_scoped"."owner_id" and ("foo" = ? or "bar" = ?) and "baz" = ?)
func TestCompileExistenceSubquery_RelatedGlobalScopeOrChainsGroupPerScope(t *testing.T) {
	q := newRelQueryWith(t, &relScopeOwner{}).Has("Scoped").(*OrmBuilder)

	sql, bindings, err := q.dest(&[]relScopeOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_scope_owners" where exists (select * from "rel_scope_scoped" where "rel_scope_owners"."id" = "rel_scope_scoped"."owner_id" and ("foo" = ? or "bar" = ?) and "baz" = ?)`, sql)
	assert.Equal(t, []any{1, 2, 3}, bindings)
}

// relDeepOwner / relDeepMid / relDeepLeaf pin a dotted chain whose intermediate level carries both
// a global scope and a soft delete: hasNested runs the nested relation inside the closure callScope
// applies, so the nested exists must lead the intermediate model's own wheres, its global scopes and
// its soft-delete predicate.
type relDeepOwner struct {
	ID   uint
	Mids []*relDeepMid `gorm:"-"`
}

func (relDeepOwner) TableName() string { return "rel_deep_owners" }

func (relDeepOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Mids": contractsorm.HasMany{Related: &relDeepMid{}, ForeignKey: "owner_id"},
	}
}

type relDeepMid struct {
	ID        uint
	OwnerID   uint
	Active    int
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at"`
	Leaves    []*relDeepLeaf        `gorm:"-"`
}

func (relDeepMid) TableName() string { return "rel_deep_mids" }

func (relDeepMid) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Leaves": contractsorm.HasMany{Related: &relDeepLeaf{}, ForeignKey: "mid_id"},
	}
}

func (relDeepMid) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "active", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("active", 1) }},
	}
}

type relDeepLeaf struct {
	ID        uint
	MidID     uint
	A         int
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at"`
	Pets      []*relDeepPet         `gorm:"-"`
}

func (relDeepLeaf) TableName() string { return "rel_deep_leaves" }

func (relDeepLeaf) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Pets": contractsorm.HasMany{Related: &relDeepPet{}, ForeignKey: "leaf_id"},
	}
}

// relDeepPet gives the dotted chain a third hop, so a middle segment's plain exists can be pinned.
type relDeepPet struct {
	ID     uint
	LeafID uint
	B      int
}

func (relDeepPet) TableName() string { return "rel_deep_pets" }

// Fresh oracle (illuminate/database v12.55.1, sqlite; bindings [1, 1]):
//
//	select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves"
//	where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves".
//	"deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)
func TestCompileExistenceSubquery_NestedExistsLeadsIntermediateScopeAndSoftDelete(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("a", 1) }
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids.Leaves", callback).(*OrmBuilder)

	sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1}, bindings)
}

// hasNested renders every segment before the last as a plain whereHas and hands the caller's
// operator/count to the final has() call, so a dotted count comparison must sit on the leaf -
// inside the outer exists - not on the outer relation. Fresh oracle (illuminate/database v12.55.1,
// sqlite; bindings [1]):
//
//	select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves"
//	where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is
//	null) > 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null)
//
// A count of 1 with ">" or "=" still renders the count comparison: only ">=" and "<" at count 1
// take Laravel's exists optimisation (canUseExistsForExistenceCheck), the same boundary
// shouldUseExists draws.
func TestCompileExistenceSubquery_NestedCountComparisonRidesTheLeaf(t *testing.T) {
	cases := []struct {
		operator string
		count    int
		want     string
	}{
		{">", 2, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) > 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`},
		{"=", 3, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) = 3 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`},
		{"<", 2, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) < 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`},
		{">", 1, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) > 1 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`},
		{"=", 1, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) = 1 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`},
	}

	for _, c := range cases {
		q := newRelQueryWith(t, &relDeepOwner{}).Has("Mids.Leaves", c.operator, c.count).(*OrmBuilder)

		sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
		require.NoError(t, err)
		assert.Equal(t, c.want, sql)
		assert.Equal(t, []any{1}, bindings)
	}
}

// The caller's callback rides down with the operator/count: whereHas("mids.leaves", cb, ...) runs
// cb on the leaf query, so the count subquery carries the callback's wheres. Fresh oracle
// (illuminate/database v12.55.1, sqlite; bindings [1, 1]):
//
//	select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves"
//	where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves".
//	"deleted_at" is null) >= 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null)
func TestCompileExistenceSubquery_NestedCallbackCountRidesTheLeaf(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("a", 1) }

	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids.Leaves", callback, ">=", 2).(*OrmBuilder)
	sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves"."deleted_at" is null) >= 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1}, bindings)

	q2 := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids.Leaves", callback, ">", 1).(*OrmBuilder)
	sql, bindings, err = q2.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves"."deleted_at" is null) > 1 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1}, bindings)
}

// doesntHave is hasNested's "<",1 shape: the outer segment wears the not-exists and the leaf resets
// to a plain exists, whether the condition came from DoesntHave or from Has(rel, "<", 1) directly.
// Fresh oracle (illuminate/database v12.55.1, sqlite; bindings [1] for the no-callback forms):
//
//	select * from "rel_deep_owners" where not exists (select * from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves"
//	where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is
//	null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)
func TestCompileExistenceSubquery_NestedDoesntHaveNegatesTheOuterOnly(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("a", 1) }

	// A callback on the doesntHave form stays on the leaf's positive exists (bindings [1, 1]).
	q := newRelQueryWith(t, &relDeepOwner{}).WhereDoesntHave("Mids.Leaves", callback).(*OrmBuilder)
	sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where not exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1}, bindings)

	notExists := `select * from "rel_deep_owners" where not exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)`

	q2 := newRelQueryWith(t, &relDeepOwner{}).DoesntHave("Mids.Leaves").(*OrmBuilder)
	sql, bindings, err = q2.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, notExists, sql)
	assert.Equal(t, []any{1}, bindings)

	q3 := newRelQueryWith(t, &relDeepOwner{}).Has("Mids.Leaves", "<", 1).(*OrmBuilder)
	sql, bindings, err = q3.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, notExists, sql)
	assert.Equal(t, []any{1}, bindings)
}

// or-variants keep Laravel's split: the or-conjunction lands on the outer clause while the count
// comparison stays on the leaf with an "and". Fresh oracle (illuminate/database v12.55.1, sqlite;
// bindings [1, 1, 1] for the first form, [1, 1] for the second):
//
//	select * from "rel_deep_owners" where "x" = ? or exists (select * from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves"
//	where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves".
//	"deleted_at" is null) > 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null)
func TestCompileExistenceSubquery_NestedOrVariantsKeepTheLeafPlacement(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("a", 1) }

	q := toQuery(newRelQueryWith(t, &relDeepOwner{}).Where("x", 1)).OrWhereHas("Mids.Leaves", callback, ">", 2).(*OrmBuilder)
	sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where "x" = ? or exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "a" = ? and "rel_deep_leaves"."deleted_at" is null) > 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1, 1}, bindings)

	q2 := toQuery(newRelQueryWith(t, &relDeepOwner{}).Where("x", 1)).OrDoesntHave("Mids.Leaves").(*OrmBuilder)
	sql, bindings, err = q2.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where "x" = ? or not exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1}, bindings)
}

// A three-segment chain keeps every segment before the last a plain exists: only the pets segment
// carries the count comparison, and the middle leaves segment stays "exists (... and (select
// count(*) from "rel_deep_pets" ...) > 2 ...)". Fresh oracle (illuminate/database v12.55.1, sqlite;
// bindings [1]):
//
//	select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves"
//	where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and (select count(*) from
//	"rel_deep_pets" where "rel_deep_leaves"."id" = "rel_deep_pets"."leaf_id") > 2 and
//	"rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is
//	null)
func TestCompileExistenceSubquery_ThreeSegmentChainKeepsTheMiddlePlainExists(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("b", 1) }

	q := newRelQueryWith(t, &relDeepOwner{}).Has("Mids.Leaves.Pets", ">", 2).(*OrmBuilder)
	sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and (select count(*) from "rel_deep_pets" where "rel_deep_leaves"."id" = "rel_deep_pets"."leaf_id") > 2 and "rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1}, bindings)

	q2 := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids.Leaves.Pets", callback, ">=", 2).(*OrmBuilder)
	sql, bindings, err = q2.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and (select count(*) from "rel_deep_pets" where "rel_deep_leaves"."id" = "rel_deep_pets"."leaf_id" and "b" = ?) >= 2 and "rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1}, bindings)

	q3 := newRelQueryWith(t, &relDeepOwner{}).WhereDoesntHave("Mids.Leaves.Pets", callback).(*OrmBuilder)
	sql, bindings, err = q3.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where not exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and exists (select * from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and exists (select * from "rel_deep_pets" where "rel_deep_leaves"."id" = "rel_deep_pets"."leaf_id" and "b" = ?) and "rel_deep_leaves"."deleted_at" is null) and "active" = ? and "rel_deep_mids"."deleted_at" is null)`, sql)
	assert.Equal(t, []any{1, 1}, bindings)
}

// The clause keeps its call position between an earlier and a later where even when the leaf
// carries the count comparison, so the bindings stay in call order. Fresh oracle
// (illuminate/database v12.55.1, sqlite; bindings [1, 1, 2]):
//
//	select * from "rel_deep_owners" where "x" = ? and exists (select * from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves"
//	where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is
//	null) > 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null) and "y" = ?
func TestCompileExistenceSubquery_LeafCountClauseStaysBetweenSurroundingWheres(t *testing.T) {
	q := toQuery(toQuery(newRelQueryWith(t, &relDeepOwner{}).Where("x", 1)).Has("Mids.Leaves", ">", 2)).Where("y", 2).(*OrmBuilder)

	sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where "x" = ? and exists (select * from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and (select count(*) from "rel_deep_leaves" where "rel_deep_mids"."id" = "rel_deep_leaves"."mid_id" and "rel_deep_leaves"."deleted_at" is null) > 2 and "active" = ? and "rel_deep_mids"."deleted_at" is null) and "y" = ?`, sql)
	assert.Equal(t, []any{1, 1, 2}, bindings)
}

// A single-segment count condition keeps its comparison on the outer query - the placement
// hasNested leaves untouched. Fresh oracle (illuminate/database v12.55.1, sqlite; bindings
// [5, 1]):
//
//	select * from "rel_deep_owners" where (select count(*) from "rel_deep_mids" where
//	"rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "owner_id" = ? and "active" = ? and
//	"rel_deep_mids"."deleted_at" is null) >= 2
func TestCompileExistenceSubquery_SingleSegmentCountStaysOnTheOuterQuery(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("owner_id", 5) }
	q := newRelQueryWith(t, &relDeepOwner{}).WhereHas("Mids", callback, ">=", 2).(*OrmBuilder)

	sql, bindings, err := q.dest(&[]relDeepOwner{}).addGlobalScopes().compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_deep_owners" where (select count(*) from "rel_deep_mids" where "rel_deep_owners"."id" = "rel_deep_mids"."owner_id" and "owner_id" = ? and "active" = ? and "rel_deep_mids"."deleted_at" is null) >= 2`, sql)
	assert.Equal(t, []any{5, 1}, bindings)
}
