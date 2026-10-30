package gorm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
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
	assert.Equal(t, `select * from "rel_profiles" where "user_id" = ?`, sql)
}

func TestRelated_HasMany_SQL_Exact(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 7}, "Books")
	sql := newRelationSQL(t, rel, &[]relBook{})
	assert.Equal(t, `select * from "rel_books" where "user_id" = ?`, sql)
}

func TestRelated_BelongsTo_SQL_Exact(t *testing.T) {
	q := newRelQueryWith(t, &relBook{})
	rel := q.Related(&relBook{AuthorID: 5}, "Author")
	sql := newRelationSQL(t, rel, &relUser{})
	assert.Equal(t, `select * from "rel_users" where "id" = ?`, sql)
}

func TestRelated_Many2Many_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 7}, "Roles")
	sql := newRelationSQL(t, rel, &[]relRole{})
	assert.Equal(t, `select * from "rel_roles" inner join "rel_user_roles" ON "rel_user_roles"."rel_role_id" = "rel_roles"."id" where "rel_user_roles"."rel_user_id" = ?`, sql)
}

func TestRelated_MorphOne_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 9}, "Logo")
	sql := newRelationSQL(t, rel, &relLogo{})
	assert.Equal(t, `select * from "rel_logos" where "logoable_id" = ? and "logoable_type" = ?`, sql)
}

func TestRelated_MorphMany_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 9}, "Houses")
	sql := newRelationSQL(t, rel, &[]relHouse{})
	assert.Equal(t, `select * from "rel_houses" where "houseable_id" = ? and "houseable_type" = ?`, sql)
}

func TestRelated_MorphToMany_SQL(t *testing.T) {
	q := newRelQueryWith(t, &morphPost{})
	rel := q.Related(&morphPost{ID: 3}, "Tags")
	sql := newRelationSQL(t, rel, &[]morphTag{})
	assert.Equal(t, `select * from "morph_tags" inner join "taggables" ON "taggables"."morph_tag_id" = "morph_tags"."id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?`, sql)
}

func TestRelated_MorphedByMany_SQL(t *testing.T) {
	q := newRelQueryWith(t, &morphTag{})
	rel := q.Related(&morphTag{ID: 1}, "Posts")
	sql := newRelationSQL(t, rel, &[]morphPost{})
	assert.Equal(t, `select * from "morph_posts" inner join "taggables" ON "taggables"."morph_post_id" = "morph_posts"."id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?`, sql)
}

func TestRelated_HasManyThrough_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relCountry{})
	rel := q.Related(&relCountry{ID: 1}, "Posts")
	sql := newRelationSQL(t, rel, &[]relPost{})
	assert.Equal(t, `select * from "rel_posts" inner join "rel_users" ON "rel_posts"."rel_user_id" = "rel_users"."id" where "rel_users"."rel_country_id" = ?`, sql)
}

func TestRelated_HasOneThrough_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relCountry{})
	rel := q.Related(&relCountry{ID: 1}, "FirstPost")
	sql := newRelationSQL(t, rel, &relPost{})
	assert.Equal(t, `select * from "rel_posts" inner join "rel_users" ON "rel_posts"."rel_user_id" = "rel_users"."id" where "rel_users"."rel_country_id" = ?`, sql)
}

// ---------------------------------------------------------------------------
// compileExistenceSubquery SQL — used by Has / WhereHas / DoesntHave
// ---------------------------------------------------------------------------

func runExistenceSQL(t *testing.T, model any, relation string, _ any) string {
	t.Helper()
	q := newRelQueryWith(t, model)
	desc, err := resolveRelation(model, relation)
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	return sql
}

func TestCompileExistenceSubquery_HasOne_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Profile", &relProfile{})
	assert.Equal(t, `select 1 from "rel_profiles" where "rel_users"."id" = "rel_profiles"."user_id"`, sql)
}

func TestCompileExistenceSubquery_HasMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Books", &[]relBook{})
	assert.Equal(t, `select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
}

func TestCompileExistenceSubquery_BelongsTo_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relBook{}, "Author", &[]relUser{})
	assert.Equal(t, `select 1 from "rel_users" where "rel_users"."id" = "rel_books"."author_id"`, sql)
}

func TestCompileExistenceSubquery_Many2Many_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Roles", &[]relRole{})
	assert.Equal(t, `select 1 from "rel_roles" inner join "rel_user_roles" on "rel_user_roles"."rel_role_id" = "rel_roles"."id" where "rel_users"."id" = "rel_user_roles"."rel_user_id"`, sql)
}

func TestCompileExistenceSubquery_MorphOne_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Logo", &relLogo{})
	assert.Equal(t, `select 1 from "rel_logos" where "rel_users"."id" = "rel_logos"."logoable_id" and "rel_logos"."logoable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_MorphMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relUser{}, "Houses", &[]relHouse{})
	assert.Equal(t, `select 1 from "rel_houses" where "rel_users"."id" = "rel_houses"."houseable_id" and "rel_houses"."houseable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_MorphToMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &morphPost{}, "Tags", &[]morphTag{})
	assert.Equal(t, `select 1 from "morph_tags" inner join "taggables" on "taggables"."morph_tag_id" = "morph_tags"."id" where "morph_posts"."id" = "taggables"."taggable_id" and "taggables"."taggable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_MorphedByMany_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &morphTag{}, "Posts", &[]morphPost{})
	assert.Equal(t, `select 1 from "morph_posts" inner join "taggables" on "taggables"."morph_post_id" = "morph_posts"."id" where "morph_tags"."id" = "taggables"."taggable_id" and "taggables"."taggable_type" = ?`, sql)
}

func TestCompileExistenceSubquery_HasManyThrough_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relCountry{}, "Posts", &[]relPost{})
	assert.Equal(t, `select 1 from "rel_posts" inner join "rel_users" on "rel_posts"."rel_user_id" = "rel_users"."id" where "rel_countries"."id" = "rel_users"."rel_country_id"`, sql)
}

func TestCompileExistenceSubquery_HasOneThrough_SQL(t *testing.T) {
	sql := runExistenceSQL(t, &relCountry{}, "FirstPost", &relPost{})
	assert.Equal(t, `select 1 from "rel_posts" inner join "rel_users" on "rel_posts"."rel_user_id" = "rel_users"."id" where "rel_countries"."id" = "rel_users"."rel_country_id"`, sql)
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
	desc, err := resolveRelation(model, relation)
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

// The aggregate subquery (WithCount / WithExists / WithMax / WithMin / WithSum / WithAvg) is
// compiled through compileExistenceSubquery, so applySubquerySoftDelete's predicate must appear
// there too. Nothing pinned that path when the predicate was introduced: disabling
// applySubquerySoftDelete left the whole aggregate suite green while only the existence pins
// failed, so a regression would have silently counted soft-deleted rows.
func TestCompileAggregateSubquery_FrameworkSoftDeletePredicate(t *testing.T) {
	sql := runAggregateRelationSQL(t, &relSoftOwner{}, "Books", selectSub{relation: "Books", column: "*", function: "count"})
	assert.Equal(t, `select count(*) from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" IS NULL`, sql)
}

// The same aggregate path for a morph relation must carry both the morph type predicate and the
// soft-delete predicate.
func TestCompileAggregateSubquery_MorphFrameworkSoftDeletePredicate(t *testing.T) {
	sql := runAggregateRelationSQL(t, &relSoftOwner{}, "Houses", selectSub{relation: "Houses", column: "*", function: "count"})
	assert.Equal(t, `select count(*) from "rel_soft_houses" where "rel_soft_owners"."id" = "rel_soft_houses"."houseable_id" and "rel_soft_houses"."houseable_type" = ? and "rel_soft_houses"."deleted_at" IS NULL`, sql)
}

// The pin above is exercised end to end through the public WithCount / WithExists entry points, so
// it covers applySelectSubAggregates too - not only the inner builder.
func TestWithCount_FrameworkSoftDeleteRelation(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	builder, err := toQuery(q.WithCount("Books")).newBuilder()
	require.NoError(t, err)
	sql, _ := builder.ToSelectSQL()

	assert.Equal(t, `select "rel_soft_owners".*, (select count(*) from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" IS NULL) AS "books_count" from "rel_soft_owners"`, sql)
}

func TestWithExists_FrameworkSoftDeleteRelation(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	builder, err := toQuery(q.WithExists("Books")).newBuilder()
	require.NoError(t, err)
	sql, _ := builder.ToSelectSQL()

	assert.Equal(t, `select "rel_soft_owners".*, CASE WHEN EXISTS (select 1 from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" IS NULL) THEN 1 ELSE 0 END AS "books_exists" from "rel_soft_owners"`, sql)
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
	assert.Equal(t, `select 1 from "rel_soft_books" where "rel_soft_owners"."id" = "rel_soft_books"."user_id" and "rel_soft_books"."deleted_at" IS NULL`, sql)
}

func TestCompileMorphExistenceSubquery_FrameworkSoftDeletePredicate(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	desc, err := resolveRelation(&relSoftOwner{}, "Houses")
	require.NoError(t, err)

	inner := q.compileMorphExistenceSubquery(desc, "rel_soft_owners", nil)
	sql, _ := inner.ToSelectSQL()

	assert.Equal(t, `select 1 from "rel_soft_houses" where "rel_soft_owners"."id" = "rel_soft_houses"."houseable_id" and "rel_soft_houses"."houseable_type" = ? and "rel_soft_houses"."deleted_at" IS NULL`, sql)
}

// The compiled relation read already filters soft-deleted related rows; the existence subquery shares
// that predicate, so this pins the read side the fix was aligned with.
func TestRelated_FrameworkSoftDeleteReadPredicate(t *testing.T) {
	q := newRelQueryWith(t, &relSoftOwner{})
	rel := q.Related(&relSoftOwner{ID: 7}, "Books")
	sql := newRelationSQL(t, rel, &[]relSoftBook{})

	assert.Contains(t, sql, `"rel_soft_books"."deleted_at" IS NULL`)
}

// ---------------------------------------------------------------------------
// Relation callback non-where clauses inside the existence subquery
// ---------------------------------------------------------------------------

// A relation callback's non-where clauses (here OrderBy) are not carried into the existence subquery:
// applyRelationScope translates the callback's where conditions only. Laravel DOES emit the order by
// inside the subquery, and so did the pre-conversion GORM renderer, so this is a real - though
// semantically harmless, since an existence subquery selects 1 and aggregates nothing - parity
// difference. It is pinned here so it stays deliberate rather than being mistaken for parity.
func TestCompileExistenceSubquery_CallbackOrderByNotReflected(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(&relUser{}, "Books")
	require.NoError(t, err)

	callback := func(query contractsorm.Query) contractsorm.Query {
		return query.OrderBy("id", "desc")
	}
	inner, err := q.compileExistenceSubquery(desc, callback)
	require.NoError(t, err)
	sql, _ := inner.ToSelectSQL()

	assert.Equal(t, `select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`, sql)
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
	assert.Contains(t, sql, `) > ?`)
	assert.Equal(t, []any{3}, bindings)
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
