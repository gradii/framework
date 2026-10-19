package gorm

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormio "gorm.io/gorm"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
	basedatabase "github.com/goravel/framework/support/database"
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
	desc, err := resolveRelation(q.instance, model, relation)
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
	desc, err := resolveRelation(q.instance, model, relation)
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

// A related model still declaring gorm.DeletedAt gets no predicate on the aggregate path either,
// matching the existence path and the pre-conversion renderer.
func TestCompileAggregateSubquery_LegacyGormSoftDeleteNotPredicated(t *testing.T) {
	sql := runAggregateRelationSQL(t, &relSoftOwner{}, "GBooks", selectSub{relation: "GBooks", column: "*", function: "count"})
	assert.Equal(t, `select count(*) from "rel_gorm_books" where "rel_soft_owners"."id" = "rel_gorm_books"."user_id"`, sql)
	assert.NotContains(t, sql, "deleted_at")
}

// ---------------------------------------------------------------------------
// Soft-delete handling inside the existence subquery
// ---------------------------------------------------------------------------

// relSoftOwner's relations exercise the two soft-delete carriers: relSoftBook declares the
// framework's own type (basedatabase.DeletedAt), relGormBook still declares gorm.DeletedAt.
type relSoftBook struct {
	ID        uint
	UserID    uint
	DeletedAt basedatabase.DeletedAt `gorm:"column:deleted_at"`
}

func (relSoftBook) TableName() string { return "rel_soft_books" }

type relGormBook struct {
	ID        uint
	UserID    uint
	DeletedAt gormio.DeletedAt `gorm:"column:deleted_at"`
}

func (relGormBook) TableName() string { return "rel_gorm_books" }

type relSoftHouse struct {
	ID            uint
	HouseableID   uint
	HouseableType string
	DeletedAt     basedatabase.DeletedAt `gorm:"column:deleted_at"`
}

func (relSoftHouse) TableName() string { return "rel_soft_houses" }

type relSoftOwner struct {
	ID     uint
	Books  []*relSoftBook  `gorm:"-"`
	GBooks []*relGormBook  `gorm:"-"`
	Houses []*relSoftHouse `gorm:"-"`
}

func (relSoftOwner) TableName() string { return "rel_soft_owners" }

func (relSoftOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books":  contractsorm.HasMany{Related: &relSoftBook{}, ForeignKey: "user_id"},
		"GBooks": contractsorm.HasMany{Related: &relGormBook{}, ForeignKey: "user_id"},
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
	desc, err := resolveRelation(q.instance, &relSoftOwner{}, "Houses")
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

// A related model still declaring gorm.DeletedAt is left to GORM on every compiled path, so the
// existence subquery adds no predicate for it. This is the deliberate difference from a
// framework-soft-delete model - and it is NOT a regression: the pre-conversion GORM renderer (see the
// differential test below) emitted no predicate here either.
func TestCompileExistenceSubquery_LegacyGormSoftDeleteNotPredicated(t *testing.T) {
	sql := runExistenceSQL(t, &relSoftOwner{}, "GBooks", &[]relGormBook{})

	assert.Equal(t, `select 1 from "rel_gorm_books" where "rel_soft_owners"."id" = "rel_gorm_books"."user_id"`, sql)
	assert.NotContains(t, sql, "deleted_at")
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
	desc, err := resolveRelation(q.instance, &relUser{}, "Books")
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

// ---------------------------------------------------------------------------
// Differential: the pre-conversion GORM rendering vs the framework builder
// ---------------------------------------------------------------------------

// oldExistenceSubquerySQL reproduces the pre-conversion GORM chain that built a relation existence
// subquery - freshSession().Table(related) + the per-kind Where/Joins + Select("1") - and renders it
// the way that path rendered it. It keeps the OLD renderer available as a fixture so the new builder
// can be compared against real old output rather than against an assertion.
func oldExistenceSubquerySQL(t *testing.T, r *Query, desc *relationDescriptor) string {
	t.Helper()

	inner := r.freshSession().Table(desc.relatedTable)
	switch desc.kind {
	case relKindHasOne, relKindHasMany:
		for _, ref := range desc.references {
			inner = inner.Where(r.ident(ref.foreignTable, ref.foreignColumn) + " = " + r.ident(ref.primaryTable, ref.primaryColumn))
		}
	case relKindBelongsTo:
		for _, ref := range desc.references {
			inner = inner.Where(r.ident(ref.primaryTable, ref.primaryColumn) + " = " + r.ident(ref.foreignTable, ref.foreignColumn))
		}
	case relKindMany2Many:
		inner = inner.Joins("INNER JOIN " + r.ident(desc.pivotTable) + " ON " + r.ident(desc.pivotTable, desc.pivotRelatedRef.foreignColumn) + " = " + r.ident(desc.relatedTable, desc.pivotRelatedRef.primaryColumn))
		inner = inner.Where(r.ident(desc.pivotTable, desc.pivotParentRef.foreignColumn) + " = " + r.ident(desc.pivotParentRef.primaryTable, desc.pivotParentRef.primaryColumn))
	case relKindMorphToMany:
		inner = inner.Joins("INNER JOIN " + r.ident(desc.pivotTable) + " ON " + r.ident(desc.pivotTable, desc.pivotRelatedRef.foreignColumn) + " = " + r.ident(desc.relatedTable, desc.pivotRelatedRef.primaryColumn))
		inner = inner.Where(r.ident(desc.pivotTable, desc.pivotParentRef.foreignColumn) + " = " + r.ident(desc.pivotParentRef.primaryTable, desc.pivotParentRef.primaryColumn))
		inner = inner.Where(r.ident(desc.pivotTable, desc.morphTypeColumn)+" = ?", desc.morphValue)
	case relKindMorphOne, relKindMorphMany:
		for _, ref := range desc.references {
			inner = inner.Where(r.ident(ref.foreignTable, ref.foreignColumn) + " = " + r.ident(ref.primaryTable, ref.primaryColumn))
		}
		inner = inner.Where(r.ident(desc.relatedTable, desc.morphTypeColumn)+" = ?", desc.morphValue)
	case relKindHasOneThrough, relKindHasManyThrough:
		inner = inner.Joins("INNER JOIN " + r.ident(desc.throughTable) + " ON " + r.ident(desc.relatedTable, desc.secondKey) + " = " + r.ident(desc.throughTable, desc.secondLocalKey))
		inner = inner.Where(r.ident(desc.throughTable, desc.firstKey) + " = " + r.ident(desc.parentTable, desc.localKey))
	}
	inner = inner.Select("1")

	stmt := inner.Session(&gormio.Session{DryRun: true}).Find(&struct{ X int }{})
	require.NoError(t, stmt.Error)

	return stmt.Statement.SQL.String()
}

var existenceEqPair = regexp.MustCompile(`([a-z_][a-z0-9_.]*) = ([a-z_][a-z0-9_.]*)`)

// normalizeExistenceSQL folds the two renderers' cosmetic differences - keyword case, identifier
// quoting, and equality operand order - so the differential test compares the parts that must be
// identical: the tables, joins and predicates. Any other difference is a real parity break and makes
// the equality fail.
//
// Operand order is folded deliberately, because it cannot be compared against this fixture: the old
// GORM chain wrote `foreign = primary` for has-one/has-many/morph and `primary = foreign` for
// belongs-to, while the framework builder (like Laravel's whereColumn in
// Relation::getRelationExistenceQuery) always writes the parent key first. Equality is symmetric in
// SQL, so either rendering is correct, but a direction swap would fail the differential test for
// reasons that are not parity breaks. Operand order is therefore pinned where it is meaningful: the
// exact-SQL tests above assert the full string, direction included, for every relation kind. The fold
// only ever touches `identifier = identifier` pairs, so a missing, extra or changed predicate still
// makes the differential test fail.
func normalizeExistenceSQL(sql string) string {
	sql = strings.ToLower(sql)
	sql = strings.NewReplacer(`"`, "", "`", "").Replace(sql)
	sql = existenceEqPair.ReplaceAllStringFunc(sql, func(match string) string {
		sides := strings.Split(match, " = ")
		if sides[0] <= sides[1] {
			return match
		}
		return sides[1] + " = " + sides[0]
	})

	return strings.Join(strings.Fields(sql), " ")
}

// normalizeExistenceSQL's contract, stated as a test: it folds case, quoting and equality operand
// order; it does NOT fold a predicate. This is what lets the differential test below stay sensitive
// to a missing or extra predicate while tolerating the fixture's reversed operand order.
func TestNormalizeExistenceSQL_FoldsOnlyCosmetics(t *testing.T) {
	const newSQL = `select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "rel_books"."deleted_at" IS NULL`

	assert.Equal(t,
		normalizeExistenceSQL(newSQL),
		normalizeExistenceSQL(`SELECT 1 FROM rel_books WHERE rel_books.user_id = rel_users.id AND rel_books.deleted_at IS NULL`),
		"case, quoting and operand order are cosmetic")

	assert.NotEqual(t,
		normalizeExistenceSQL(newSQL),
		normalizeExistenceSQL(`select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`),
		"a missing predicate must survive normalisation")

	assert.NotEqual(t,
		normalizeExistenceSQL(newSQL),
		normalizeExistenceSQL(`select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "rel_books"."deleted_at" IS NULL and "rel_books"."x" = ?`),
		"an extra predicate must survive normalisation")
}

// For every relation kind the pre-conversion GORM rendering and the framework-builder rendering must
// be the same subquery once keyword case, identifier quoting and equality operand order are folded
// away. This turns the "same text modulo case" parity claim into something the suite checks instead of
// something a report asserts.
func TestCompileExistenceSubquery_MatchesOldGormRendering(t *testing.T) {
	cases := []struct {
		name     string
		model    any
		relation string
	}{
		{"has one", &relUser{}, "Profile"},
		{"has many", &relUser{}, "Books"},
		{"belongs to", &relBook{}, "Author"},
		{"many to many", &relUser{}, "Roles"},
		{"morph one", &relUser{}, "Logo"},
		{"morph many", &relUser{}, "Houses"},
		{"morph to many", &morphPost{}, "Tags"},
		{"morphed by many", &morphTag{}, "Posts"},
		{"has many through", &relCountry{}, "Posts"},
		{"has one through", &relCountry{}, "FirstPost"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			query := newRelQueryWith(t, test.model)

			desc, err := resolveRelation(query.instance, test.model, test.relation)
			require.NoError(t, err)

			builder, err := query.compileExistenceSubquery(desc, nil)
			require.NoError(t, err)
			newSQL, _ := builder.ToSelectSQL()

			oldSQL := oldExistenceSubquerySQL(t, query, desc)

			assert.Equal(t, normalizeExistenceSQL(oldSQL), normalizeExistenceSQL(newSQL))
		})
	}
}
