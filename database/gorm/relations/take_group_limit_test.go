package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file pins the relation-level take() / limit() of the has-many and has-many-through objects
// (item followup-take-grouplimit) on the recording transport: HasOneOrMany::limit
// (HasOneOrMany.php:555) and HasOneOrManyThrough::limit (HasOneOrManyThrough.php:772) set the
// plain query limit when the parent exists and route through the query builder's groupLimit —
// the row_number() over (partition by <key>) derived-table rewrite — when it does not. Every
// statement shape was measured by the oracle (php .claude/laravel-oracle/oracle.php
// /tmp/followup-take-grouplimit/take_grouplimit.php, illuminate/database v12.55.1, real sqlite
// 3.51.0; output in take_grouplimit.out):
//
//	H1  existing user  ->posts()->limit(3)->toSql()
//	    select * from "gl_posts" where "gl_posts"."user_id" = ? and "gl_posts"."user_id" is not null limit 3
//	H7  new user       ->posts()->limit(3)->toSql()
//	    select * from (select *, row_number() over (partition by "gl_posts"."user_id") as "laravel_row"
//	    from "gl_posts" where "gl_posts"."user_id" is null and "gl_posts"."user_id" is not null)
//	    as "laravel_table" where "laravel_row" <= 3 order by "laravel_row"
//	H13 new user with an assigned key ->posts()->limit(3)->toSql(): the same rewrite over
//	    `"gl_posts"."user_id" = ?` — the branch follows Model::$exists, not the key.
//	H14 new user ->profile()->limit(1): HasOne inherits the branch (`<= 1`).
//	T1  existing country ->posts()->limit(3)->toSql()
//	    select * from "gl_tposts" inner join "gl_tusers" on "gl_tusers"."id" = "gl_tposts"."user_id"
//	    where "gl_tusers"."country_id" = ? limit 3
//	T5  new country ->posts()->limit(3)->toSql(): the rewrite partitioned by "gl_tusers"."country_id"
//	    (T4 getQualifiedFirstKeyName) over `where "gl_tusers"."country_id" is null`.
//	E2  the eager get() wears shouldSelect inside the rewrite:
//	    select * from (select "gl_tposts".*, "gl_tusers"."country_id" as "laravel_through_key",
//	    row_number() over (partition by "gl_tusers"."country_id") as "laravel_row" from ...
//
// The morph leaves inherit the branch (/tmp/followup-take-grouplimit/morph_limit.php, same oracle):
//
//	M4  new parent ->image()->limit(1)->toSql() (MorphOne)
//	    select * from (select *, row_number() over (partition by "gl_images"."imageable_id") as "laravel_row"
//	    from "gl_images" where "gl_images"."imageable_type" = ? and "gl_images"."imageable_id" is null
//	    and "gl_images"."imageable_id" is not null) as "laravel_table" where "laravel_row" <= 1 order by "laravel_row"
//	M3  getExistenceCompareKey 'gl_images.imageable_id'; M5 / M6 take() returns Relations\MorphMany / MorphOne.
//
// A Go model carries no Model::$exists flag; the object reads the parent-side key instead (the
// framework's fresh-key convention): a present key is the existing parent, an absent one — the
// prototype an eager constraint runs on — is not. The one observable difference from the oracle
// is therefore the key rendering of the prototype: Laravel's null key compiles `is null`, the Go
// zero key binds `= ?` — the exact shape the oracle's H13 (a new model with a key) compiles.

// --- has-many-through fixtures -----------------------------------------------------------------

type glCountry struct {
	ID    uint      `gorm:"column:id;primaryKey"`
	Posts []*glPost `gorm:"-"`
}

func (glCountry) TableName() string { return "gl_countries" }

func (glCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{Related: &glPost{}, Through: &glUser{}, FirstKey: "country_id", SecondKey: "user_id"},
	}
}

type glUser struct {
	ID        uint `gorm:"column:id;primaryKey"`
	CountryID uint `gorm:"column:country_id"`
}

func (glUser) TableName() string { return "gl_users" }

type glPost struct {
	ID     uint   `gorm:"column:id;primaryKey"`
	UserID uint   `gorm:"column:user_id"`
	Title  string `gorm:"column:title"`
}

func (glPost) TableName() string { return "gl_posts" }

// --- has-many ---------------------------------------------------------------------------------

func TestTakeGroupLimit_HasManyExistingParentCompilesThePlainLimit(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle H1 / H2: limit(3) and take(3) on an existing parent compile the plain `limit 3`
	// (H4: toBase()->limit 3, groupLimit NULL).
	require.NoError(t, builder.Related(&sqlUser{ID: 7}, "Books").Limit(3).Get(&[]sqlBook{}))
	require.NoError(t, builder.Related(&sqlUser{ID: 7}, "Books").Take(3).Get(&[]sqlBook{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	for _, statement := range statements {
		assert.Equal(t, `select * from "sql_books" where "sql_books"."user_id" = ? and "sql_books"."user_id" is not null limit 3`, statement.query)
		assert.Equal(t, []any{int64(7)}, derefAll(statement.args))
	}
}

func TestTakeGroupLimit_HasManyNewParentCompilesTheGroupLimit(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle H7 / H8 (shape H13 for the bound key): a parent that does not exist routes limit(3)
	// / take(3) through groupLimit(3, getExistenceCompareKey()) — the row_number() over
	// (partition by "gl_posts"."user_id") rewrite — and the plain limit stays unset (H9:
	// toBase()->limit NULL, groupLimit ['value' => 3, 'column' => 'gl_posts.user_id']).
	require.NoError(t, builder.Related(&sqlUser{}, "Books").Limit(3).Get(&[]sqlBook{}))
	require.NoError(t, builder.Related(&sqlUser{}, "Books").Take(3).Get(&[]sqlBook{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	for _, statement := range statements {
		assert.Equal(t, `select * from (select *, row_number() over (partition by "sql_books"."user_id") as "laravel_row" from "sql_books" where "sql_books"."user_id" = ? and "sql_books"."user_id" is not null) as "laravel_table" where "laravel_row" <= 3 order by "laravel_row"`, statement.query)
		assert.Equal(t, []any{int64(0)}, derefAll(statement.args))
	}
}

func TestTakeGroupLimit_HasManyNewParentFoldsOffsetAndOrderIntoTheRewrite(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle H11: limit(3)->offset(1) folds the offset into the row bound —
	// `where "laravel_row" <= 4 and "laravel_row" > 1`. Oracle H12: an order by moves inside the
	// window — `row_number() over (partition by "gl_posts"."user_id" order by "title" desc)` —
	// and leaves the derived table with no order of its own. The relation-level Limit leads each
	// chain: Offset / OrderBy are promoted builder methods and hand back the builder (the Go
	// promotion rule, contracts relation_query.go), where Laravel's __call hands back $this.
	require.NoError(t, builder.Related(&sqlUser{}, "Books").Limit(3).Offset(1).Get(&[]sqlBook{}))
	require.NoError(t, builder.Related(&sqlUser{}, "Books").Limit(2).OrderBy("id", "desc").Get(&[]sqlBook{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	assert.Equal(t, `select * from (select *, row_number() over (partition by "sql_books"."user_id") as "laravel_row" from "sql_books" where "sql_books"."user_id" = ? and "sql_books"."user_id" is not null) as "laravel_table" where "laravel_row" <= 4 and "laravel_row" > 1 order by "laravel_row"`, statements[0].query)
	assert.Equal(t, `select * from (select *, row_number() over (partition by "sql_books"."user_id" order by "id" desc) as "laravel_row" from "sql_books" where "sql_books"."user_id" = ? and "sql_books"."user_id" is not null) as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`, statements[1].query)
}

func TestTakeGroupLimit_HasOneInheritsTheBranch(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle H15 / H14: HasOne composes HasOneOrMany::limit — the existing parent compiles the
	// plain `limit 1`, the new parent the rewrite bounded at 1.
	require.NoError(t, builder.Related(&sqlUser{ID: 7}, "Profile").Limit(1).Get(&[]sqlProfile{}))
	require.NoError(t, builder.Related(&sqlUser{}, "Profile").Limit(1).Get(&[]sqlProfile{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	assert.Equal(t, `select * from "sql_profiles" where "sql_profiles"."user_id" = ? and "sql_profiles"."user_id" is not null limit 1`, statements[0].query)
	assert.Equal(t, `select * from (select *, row_number() over (partition by "sql_profiles"."user_id") as "laravel_row" from "sql_profiles" where "sql_profiles"."user_id" = ? and "sql_profiles"."user_id" is not null) as "laravel_table" where "laravel_row" <= 1 order by "laravel_row"`, statements[1].query)
}

// --- has-many-through -------------------------------------------------------------------------

func TestTakeGroupLimit_ThroughExistingParentCompilesThePlainLimit(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle T1 / T2 (+ the get() select of E2's shape): the existing far parent compiles the
	// plain `limit 3` under the through read select (T3: toBase()->limit 3, groupLimit NULL).
	require.NoError(t, builder.Related(&glCountry{ID: 1}, "Posts").Limit(3).Get(&[]glPost{}))
	require.NoError(t, builder.Related(&glCountry{ID: 1}, "Posts").Take(3).Get(&[]glPost{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	for _, statement := range statements {
		assert.Equal(t, `select "gl_posts".*, "gl_users"."country_id" as "laravel_through_key" from "gl_posts" inner join "gl_users" on "gl_users"."id" = "gl_posts"."user_id" where "gl_users"."country_id" = ? limit 3`, statement.query)
		assert.Equal(t, []any{int64(1)}, derefAll(statement.args))
	}
}

func TestTakeGroupLimit_ThroughNewParentCompilesTheGroupLimit(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle T5 / T6 with the get() select of E2: a far parent that does not exist routes
	// limit(3) / take(3) through groupLimit(3, getQualifiedFirstKeyName()) — partitioned by the
	// through table's first key "gl_users"."country_id" (T4), not the related foreign key (T7:
	// toBase()->limit NULL, groupLimit ['value' => 3, 'column' => 'gl_tusers.country_id']).
	require.NoError(t, builder.Related(&glCountry{}, "Posts").Limit(3).Get(&[]glPost{}))
	require.NoError(t, builder.Related(&glCountry{}, "Posts").Take(3).Get(&[]glPost{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	for _, statement := range statements {
		assert.Equal(t, `select * from (select "gl_posts".*, "gl_users"."country_id" as "laravel_through_key", row_number() over (partition by "gl_users"."country_id") as "laravel_row" from "gl_posts" inner join "gl_users" on "gl_users"."id" = "gl_posts"."user_id" where "gl_users"."country_id" = ?) as "laravel_table" where "laravel_row" <= 3 order by "laravel_row"`, statement.query)
		assert.Equal(t, []any{int64(0)}, derefAll(statement.args))
	}
}

// --- the object surface ------------------------------------------------------------------------

func TestTakeGroupLimit_ReturnsTheRelationObject(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	// Oracle H3: take(3) returns the relation itself (Illuminate\...\Relations\HasMany) — `return
	// $this` — so the relation surface stays reachable after the call.
	_, ok := builder.Related(&sqlUser{ID: 7}, "Books").Take(3).(contractsorm.HasManyRelation)
	assert.True(t, ok, "Take must hand back the relation object, not the bare builder")
	_, ok = builder.Related(&sqlUser{}, "Books").Limit(3).(contractsorm.HasManyRelation)
	assert.True(t, ok, "Limit must hand back the relation object, not the bare builder")
	_, ok = builder.Related(&glCountry{}, "Posts").Take(3).(contractsorm.HasManyThroughRelation)
	assert.True(t, ok, "the through Take must hand back the relation object")
	// Verifier pin (oracle P14b / P14d, /tmp/verify-take-grouplimit/probe.php: HasOne and
	// HasOneThrough return their own class as well). The HasOne leaf is visible through
	// HasOneRelation — WithDefault lives on the leaf; the HasOneThrough leaf is not, the embedded
	// through part already satisfies HasOneThroughRelation, so that one is pinned by identity: Take
	// hands back the very object it was called on, not the embedded part.
	_, ok = builder.Related(&sqlUser{ID: 7}, "Profile").Limit(1).(contractsorm.HasOneRelation)
	assert.True(t, ok, "the HasOne Limit must hand back the relation object")
	oneThrough := builder.Related(&glCountry{}, "Posts").(contractsorm.HasManyThroughRelation).One()
	assert.Same(t, oneThrough, oneThrough.Take(1), "the HasOneThrough Take must hand back the relation object")
	// Oracle M6 / M5: the morph leaves return their own class as well.
	_, ok = builder.Related(&morphOneParent{}, "Image").Take(1).(contractsorm.MorphOneRelation)
	assert.True(t, ok, "the MorphOne Take must hand back the relation object")
	_, ok = builder.Related(&morphManyParent{}, "Comments").Limit(2).(contractsorm.MorphManyRelation)
	assert.True(t, ok, "the MorphMany Limit must hand back the relation object")
}

func TestTakeGroupLimit_MorphOneInheritsTheBranch(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle M4 / M1: MorphOne composes HasOneOrMany::limit — the new parent compiles the rewrite
	// partitioned by the morph foreign key (M3 getExistenceCompareKey) under the morph type
	// constraint, the existing parent the plain limit.
	require.NoError(t, builder.Related(&morphOneParent{}, "Image").Limit(1).Get(&[]morphOneImage{}))
	require.NoError(t, builder.Related(&morphOneParent{ID: 9}, "Image").Limit(1).Get(&[]morphOneImage{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	assert.Equal(t, `select * from (select *, row_number() over (partition by "morph_one_images"."imageable_id") as "laravel_row" from "morph_one_images" where "morph_one_images"."imageable_type" = ? and "morph_one_images"."imageable_id" = ? and "morph_one_images"."imageable_id" is not null) as "laravel_table" where "laravel_row" <= 1 order by "laravel_row"`, statements[0].query)
	assert.Equal(t, `select * from "morph_one_images" where "morph_one_images"."imageable_type" = ? and "morph_one_images"."imageable_id" = ? and "morph_one_images"."imageable_id" is not null limit 1`, statements[1].query)
}

func TestTakeGroupLimit_SeamReportsNoLegacyGroupLimitOnTheBaseGrammar(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	// HasOneOrManyThrough::limit (:779-783) swaps the partition column for 'laravel_through_key'
	// only when the MySQL grammar's useLegacyGroupLimit() is true (MySQL < 8.0.11); the seam
	// reports the grammar's answer, false on the base grammar this transport runs.
	legacy, ok := any(builder).(interface{ UseLegacyGroupLimit() bool })
	require.True(t, ok, "the seam must expose the grammar's legacy group-limit decision")
	assert.False(t, legacy.UseLegacyGroupLimit())
}

// --- many-to-many ----------------------------------------------------------------------------

// TestTakeGroupLimit_Many2ManyExistingParentCompilesThePlainLimit pins BelongsToMany::limit's
// existing-parent branch: the plain query limit on the pivot-joined relation read, with the group
// limit unset. Oracle B4 (illuminate/database v12.55.1, executed against sqlite from
// /tmp/eager-m2m-limit/probe.php): `$u1->roles()->limit(2)` reports toBase()->limit 2 (groupLimit
// NULL) and compiles the plain `... where "m2m_role_user"."user_id" = ? limit 2`.
func TestTakeGroupLimit_Many2ManyExistingParentCompilesThePlainLimit(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	require.NoError(t, builder.Related(&mmUser{ID: 7}, "Roles").Limit(2).Get(&[]mmRole{}))
	require.NoError(t, builder.Related(&mmUser{ID: 7}, "Roles").Take(2).Get(&[]mmRole{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	for _, statement := range statements {
		assert.Equal(t, mmPlainReadBase+` limit 2`, statement.query)
		assert.Equal(t, []any{int64(7)}, derefAll(statement.args))
	}
}

// TestTakeGroupLimit_Many2ManyNewParentCompilesTheGroupLimit pins BelongsToMany::limit's
// non-existing-parent branch (the prototype an eager-load constraint runs the relation on): the
// pivot-partitioned group limit — row_number() over (partition by <qualified foreign pivot key>) —
// with the plain limit unset. Oracle B1/B2/B3 (/tmp/eager-m2m-limit/probe.php): the eager BtM
// limit(2) compiles `select * from (select "m2m_roles".*, <pivot aliases>, row_number() over
// (partition by "m2m_role_user"."user_id") as "laravel_row" from "m2m_roles" inner join
// "m2m_role_user" on ... where "m2m_role_user"."user_id" in (1, 2)) as "laravel_table" where
// "laravel_row" <= 2 order by "laravel_row"`; the non-existing parent's toBase() reports limit
// NULL and groupLimit ['value' => 2, 'column' => 'm2m_role_user.user_id']. The partition column is
// getExistenceCompareKey(), the qualified foreign pivot key (BelongsToMany.php:1527).
func TestTakeGroupLimit_Many2ManyNewParentCompilesTheGroupLimit(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	require.NoError(t, builder.Related(&mmUser{}, "Roles").Limit(2).Get(&[]mmRole{}))
	require.NoError(t, builder.Related(&mmUser{}, "Roles").Take(2).Get(&[]mmRole{}))

	statements := recorder.take()
	require.Len(t, statements, 2)
	for _, statement := range statements {
		assert.Equal(t, `select * from (select "mm_roles".*, "mm_user_roles"."user_id" as "pivot_user_id", "mm_user_roles"."role_id" as "pivot_role_id", row_number() over (partition by "mm_user_roles"."user_id") as "laravel_row" from "mm_roles" inner join "mm_user_roles" ON "mm_user_roles"."role_id" = "mm_roles"."id" where "mm_user_roles"."user_id" = ?) as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`, statement.query)
		assert.Equal(t, []any{int64(0)}, derefAll(statement.args))
	}
}

// TestTakeGroupLimit_MorphToManyNewParentPartitionsByTheForeignPivotKey pins the morph sibling:
// MorphToMany inherits BelongsToMany::limit, so it partitions by the qualified foreign pivot key
// and keeps the morph-type predicate inside the rewrite. Oracle M1 (/tmp/eager-m2m-limit/probe.php):
// `select * from (select "m2m_tags".*, <pivot aliases>, row_number() over (partition by
// "m2m_taggables"."taggable_id") as "laravel_row" from "m2m_tags" inner join "m2m_taggables" on ...
// where "m2m_taggables"."taggable_id" in (1) and "m2m_taggables"."taggable_type" = ?) as
// "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`.
func TestTakeGroupLimit_MorphToManyNewParentPartitionsByTheForeignPivotKey(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	require.NoError(t, builder.Related(&mmPost{}, "Tags").Limit(2).Get(&[]mmTag{}))

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from (select "mm_tags".*, "mm_taggables"."taggable_id" as "pivot_taggable_id", "mm_taggables"."tag_id" as "pivot_tag_id", "mm_taggables"."taggable_type" as "pivot_taggable_type", row_number() over (partition by "mm_taggables"."taggable_id") as "laravel_row" from "mm_tags" inner join "mm_taggables" ON "mm_taggables"."tag_id" = "mm_tags"."id" where "mm_taggables"."taggable_id" = ? and "mm_taggables"."taggable_type" = ?) as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`, statements[0].query)
}

// TestTakeGroupLimit_Many2ManyReturnsTheRelationAndExistenceKey pins the object surface: take() /
// limit() return the relation itself (Laravel's `return $this`), and getExistenceCompareKey() is
// the qualified foreign pivot key (oracle B3: 'm2m_role_user.user_id'). GetExistenceCompareKey is
// asserted structurally — it is not on the BelongsToManyRelation contract (only HasOneOrMany's is),
// the same way PHP declares it on BelongsToMany without a shared interface.
func TestTakeGroupLimit_Many2ManyReturnsTheRelationAndExistenceKey(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	relation := builder.Related(&mmUser{}, "Roles")
	assert.Same(t, relation, relation.Limit(2), "Limit must hand back the relation object")
	assert.Same(t, relation, relation.Take(2), "Take must hand back the relation object")

	keyed, ok := relation.(interface{ GetExistenceCompareKey() string })
	require.True(t, ok, "the many-to-many object carries getExistenceCompareKey")
	assert.Equal(t, "mm_user_roles.user_id", keyed.GetExistenceCompareKey())
}

// derefAll unwraps every pointer binding so an assertion sees the bound values.
func derefAll(values []any) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, deref(value))
	}

	return out
}
