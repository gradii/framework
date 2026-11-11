package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
)

// A HasManyThrough / HasOneThrough read must ALSO filter the through table when the THROUGH model is
// soft-deletable, exactly as Laravel's does: HasOneOrManyThrough::performJoin registers a named
// "SoftDeletableHasManyThrough" global scope — a whereNull on the through table's deleted-at column —
// whenever throughParentSoftDeletes() (the through model uses SoftDeletes), so the through table is
// filtered just like the related table. The compiled reads append it after the related model's own
// predicate (the related SoftDeletingScope registers first).
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r10/through_softdelete_probe.php):
//
//	N1  non-eager $country->posts()->toSql()
//	    `select * from "tposts" inner join "tusers" on "tusers"."id" = "tposts"."user_id"
//	     where "tusers"."country_id" = ? and "tposts"."deleted_at" is null and "tusers"."deleted_at" is null`
//	N3  eager with('posts')
//	    `... where "tusers"."country_id" in (1) and "tposts"."deleted_at" is null and "tusers"."deleted_at" is null`
//	N7  related plain, through soft-deletable -> only `and "tusers"."deleted_at" is null`
//	N8  through plain, related soft-deletable -> only `and "tposts"."deleted_at" is null`
//	N12 whereHas / N13 withCount -> the existence body carries both predicates
//	N9/N10 the of-many subquery AND the outer query each carry both predicates
//	N14 withTrashed / N15 onlyTrashed keep the through predicate (they target the related scope only)
//	/probe2: E1 eager withoutGlobalScopes drops both; E2 withTrashedParents drops the through one
//	(now ported - see TestThroughSoftDelete_WithTrashedParentsDropsTheThroughPredicate); round-12
//	oracle /tmp/r12/scope_removal_probe3.php §E/§F2 re-measured the same shapes.
//
// The through model's other (user-defined) global scopes do NOT reach the read — performJoin adds
// only the soft-delete named scope (oracle, /tmp/r10/through_userscope_probe.php U1). The pins below
// drive the compiled path the loader/caller uses, so the assertion is on the statement the read
// really runs.

type tsdUser struct {
	databaseorm.SoftDeletes

	ID        uint
	CountryID uint
	Email     string
}

func (tsdUser) TableName() string { return "tsd_users" }

type tsdPost struct {
	databaseorm.SoftDeletes

	ID     uint
	UserID uint
	Title  string
}

func (tsdPost) TableName() string { return "tsd_posts" }

// tsdPlainUser has no soft-delete field: the control that reads exactly as before.
type tsdPlainUser struct {
	ID        uint
	CountryID uint
}

func (tsdPlainUser) TableName() string { return "tsd_plain_users" }

type tsdCountry struct {
	ID         uint
	Name       string
	Posts      []*tsdPost `gorm:"-"`
	FirstPost  *tsdPost   `gorm:"-"`
	LatestPost *tsdPost   `gorm:"-"`
	PlainPosts []*tsdPost `gorm:"-"`
}

func (tsdCountry) TableName() string { return "tsd_countries" }

func (tsdCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts":     contractsorm.HasManyThrough{Related: &tsdPost{}, Through: &tsdUser{}, FirstKey: "country_id", SecondKey: "user_id"},
		"FirstPost": contractsorm.HasOneThrough{Related: &tsdPost{}, Through: &tsdUser{}, FirstKey: "country_id", SecondKey: "user_id"},
		"LatestPost": contractsorm.HasOneThrough{
			Related: &tsdPost{}, Through: &tsdUser{}, FirstKey: "country_id", SecondKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		"PlainPosts": contractsorm.HasManyThrough{
			Related: &tsdPost{}, Through: &tsdPlainUser{}, FirstKey: "country_id", SecondKey: "user_id",
		},
	}
}

// TestThroughSoftDelete_NonEagerRead pins the relation read's statement: the related select list plus
// the through predicate, after the related one.
func TestThroughSoftDelete_NonEagerRead(t *testing.T) {
	q := newRelQueryWith(t, &tsdCountry{})
	rel := q.Related(&tsdCountry{ID: 1}, "Posts")
	sql := newRelationSQL(t, rel, &[]tsdPost{})

	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null`, sql)
}

// TestThroughSoftDelete_HasOneRead pins the single-result kind carries the predicate too.
func TestThroughSoftDelete_HasOneRead(t *testing.T) {
	q := newRelQueryWith(t, &tsdCountry{})
	rel := q.Related(&tsdCountry{ID: 1}, "FirstPost")
	sql := newRelationSQL(t, rel, &tsdPost{})

	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null`, sql)
}

// TestThroughSoftDelete_EagerRead pins the eager related read the loader executes: the mapping read
// stays unfiltered (inert — the joined read drops a soft-deleted through row's related rows), the
// related read carries both predicates.
func TestThroughSoftDelete_EagerRead(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &tsdCountry{})
	desc, err := relations.ResolveRelation(&tsdCountry{}, "Posts")
	require.NoError(t, err)

	recorder.returnRows(`from "`+desc.ThroughTable+`"`,
		[]string{desc.FirstKey, desc.SecondLocalKey},
		[]driver.Value{int64(1), int64(1)})

	parents := []reflect.Value{reflect.ValueOf(&tsdCountry{ID: 1}).Elem()}
	require.NoError(t, q.loadThrough(parents, &tsdCountry{}, desc, eagerLoadEntry{Relation: "Posts"}, nil, true))
	require.Equal(t, 2, recorder.count(), "the loader reads the through table and then the related table")

	through := recorder.at(t, 0)
	assert.Equal(t, `select "country_id", "id" from "tsd_users" where "tsd_users"."country_id" in (?)`, through.sql)

	related := recorder.at(t, 1)
	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" in (?) and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null`, related.sql)
}

// TestThroughSoftDelete_RemovalGate pins the "*" removal: WithoutGlobalScopes() drops the whole
// predicate set on the eager read (the related model is not bound, and the through predicate is
// gated off) — oracle E1.
func TestThroughSoftDelete_RemovalGate(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &tsdCountry{})
	desc, err := relations.ResolveRelation(&tsdCountry{}, "Posts")
	require.NoError(t, err)

	recorder.returnRows(`from "`+desc.ThroughTable+`"`,
		[]string{desc.FirstKey, desc.SecondLocalKey},
		[]driver.Value{int64(1), int64(1)})

	parents := []reflect.Value{reflect.ValueOf(&tsdCountry{ID: 1}).Elem()}
	callback := func(query contractsorm.Query) contractsorm.Query { return query.WithoutGlobalScopes() }
	require.NoError(t, q.loadThrough(parents, &tsdCountry{}, desc, eagerLoadEntry{Relation: "Posts", Callback: callback}, nil, true))

	related := recorder.at(t, 1)
	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" in (?)`, related.sql)
}

// TestThroughSoftDelete_TrashedFamilyKeepTheThroughPredicate pins that the trashed switches target the
// related SoftDeletingScope alone: the through predicate survives withTrashed and onlyTrashed, the
// order the oracle compiles (N14 / N15). Laravel's through-side switch, withTrashedParents, is the
// separate named removal ported below (TestThroughSoftDelete_WithTrashedParentsDropsTheThroughPredicate).
func TestThroughSoftDelete_TrashedFamilyKeepTheThroughPredicate(t *testing.T) {
	q := newRelQueryWith(t, &tsdCountry{})
	desc, err := relations.ResolveRelation(&tsdCountry{}, "Posts")
	require.NoError(t, err)

	t.Run("withTrashed", func(t *testing.T) {
		built := q.newThroughQuery(&tsdCountry{ID: 1}, desc).(*OrmBuilder)
		statement, _, err := built.WithTrashed().(*OrmBuilder).compileSelect()
		require.NoError(t, err)
		assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_users"."deleted_at" is null`, statement)
	})

	t.Run("onlyTrashed", func(t *testing.T) {
		built := q.newThroughQuery(&tsdCountry{ID: 1}, desc).(*OrmBuilder)
		statement, _, err := built.OnlyTrashed().(*OrmBuilder).compileSelect()
		require.NoError(t, err)
		assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is not null and "tsd_users"."deleted_at" is null`, statement)
	})
}

// TestThroughSoftDelete_OfMany pins the of-many variant: the through predicate sits in the aggregate
// subquery AND on the outer query, after the related predicate in each (oracle N9).
func TestThroughSoftDelete_OfMany(t *testing.T) {
	q := newRelQueryWith(t, &tsdCountry{})
	rel := q.Related(&tsdCountry{ID: 1}, "LatestPost")
	sql := newRelationSQL(t, rel, &tsdPost{})

	assert.Equal(t, `select "tsd_posts".* from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" inner join (select MAX("tsd_posts"."id") as "id_aggregate", "tsd_users"."country_id" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null group by "tsd_users"."country_id") as "latest_post" on "latest_post"."id_aggregate" = "tsd_posts"."id" and "latest_post"."country_id" = "tsd_users"."country_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null`, sql)
}

// TestThroughSoftDelete_ExistenceSubquery pins the whereHas body carries both predicates (oracle N12).
func TestThroughSoftDelete_ExistenceSubquery(t *testing.T) {
	query := newRelQueryWith(t, &tsdCountry{}).WhereHas("Posts").(*OrmBuilder)

	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "tsd_countries" where exists (select * from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_countries"."id" = "tsd_users"."country_id" and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null)`, statement)
}

// TestThroughSoftDelete_PlainThroughKeepsNoPredicate is the control: a through model with no
// soft-delete field reads with only the related predicate, so the change adds the predicate only
// where Laravel does.
func TestThroughSoftDelete_PlainThroughKeepsNoPredicate(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &tsdCountry{})
	desc, err := relations.ResolveRelation(&tsdCountry{}, "PlainPosts")
	require.NoError(t, err)

	recorder.returnRows(`from "`+desc.ThroughTable+`"`,
		[]string{desc.FirstKey, desc.SecondLocalKey},
		[]driver.Value{int64(1), int64(1)})

	parents := []reflect.Value{reflect.ValueOf(&tsdCountry{ID: 1}).Elem()}
	require.NoError(t, q.loadThrough(parents, &tsdCountry{}, desc, eagerLoadEntry{Relation: "PlainPosts"}, nil, true))

	related := recorder.at(t, 1)
	assert.Equal(t, `select "tsd_posts".*, "tsd_plain_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_plain_users" on "tsd_plain_users"."id" = "tsd_posts"."user_id" where "tsd_plain_users"."country_id" in (?) and "tsd_posts"."deleted_at" is null`, related.sql)
}

// TestThroughSoftDelete_PluckValueExistsKeepTheThroughPredicate pins that the through predicate
// reaches the reads that suppress the relation's select-list alias: Laravel forwards pluck / value /
// exists to the Eloquent builder, so the laravel_through_key alias is dropped, but the named
// "SoftDeletableHasManyThrough" global scope still filters the through table (oracle,
// /tmp/r10/verify/pluck_exists_probe.php: P1 pluck / P2 value / P3 exists each carry
// `and "tusers"."deleted_at" is null`). The predicate is kept on the query separately from the
// through select-list rewrite, so suppressing the alias never drops it.
func TestThroughSoftDelete_PluckValueExistsKeepTheThroughPredicate(t *testing.T) {
	q := newRelQueryWith(t, &tsdCountry{})
	rel, ok := ormBuilderOf(q.Related(&tsdCountry{ID: 1}, "Posts"))
	require.True(t, ok)

	// pluck: the alias is gone, the predicate stays.
	assert.Equal(t, `select "title" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null`, rel.ToSql().Pluck("title", &tsdPost{}))

	// value: first([$column])->limit(1), same predicate.
	value, _, err := rel.singleColumnFirstQuery("title").compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select "title" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null limit 1`, value)
}

// TestThroughSoftDelete_ExistsKeepsTheThroughPredicate pins exists() through the recording driver:
// it executes a statement, so the assertion is on the statement the read really ran - Laravel's
// compileExists over the through read, both predicates kept (oracle P3; the statement itself from
// illuminate/database v12.55.1, /Users/dog/.claude/jobs/013c27a0/tmp/ofa/exists_probe.php "through").
func TestThroughSoftDelete_ExistsKeepsTheThroughPredicate(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &tsdCountry{})
	rel, ok := ormBuilderOf(q.Related(&tsdCountry{ID: 1}, "Posts"))
	require.True(t, ok)

	recorder.returnRows(`from "tsd_posts"`, []string{"1"}, []driver.Value{int64(1)})
	_, err := rel.Exists()
	require.NoError(t, err)

	assert.Equal(t, `select exists(select * from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."deleted_at" is null) as "exists"`, recorder.last(t).sql)
}

// TestThroughSoftDelete_WithTrashedParentsDropsTheThroughPredicate pins the eager through-side
// switch: the callback's relation object drops exactly the named "SoftDeletableHasManyThrough"
// scope, so the through predicate is gone and the related model's own predicate survives. Oracle
// (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe3.php §F2): the eager read
// compiles `... "sr_users"."country_id" in (1) and "sr_posts"."deleted_at" is null`.
func TestThroughSoftDelete_WithTrashedParentsDropsTheThroughPredicate(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &tsdCountry{})
	desc, err := relations.ResolveRelation(&tsdCountry{}, "Posts")
	require.NoError(t, err)

	recorder.returnRows(`from "`+desc.ThroughTable+`"`,
		[]string{desc.FirstKey, desc.SecondLocalKey},
		[]driver.Value{int64(1), int64(1)})

	parents := []reflect.Value{reflect.ValueOf(&tsdCountry{ID: 1}).Elem()}
	callback := func(query contractsorm.Query) contractsorm.Query {
		return query.(contractsorm.HasOneThroughRelation).WithTrashedParents()
	}
	require.NoError(t, q.loadThrough(parents, &tsdCountry{}, desc, eagerLoadEntry{Relation: "Posts", Callback: callback}, nil, true))

	related := recorder.at(t, 1)
	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" in (?) and "tsd_posts"."deleted_at" is null`, related.sql)
}

// TestThroughSoftDelete_NamedRelatedRemovalKeepsTheThroughPredicate pins the other side: removing
// the related model's SoftDeletingScope by name leaves the through predicate in place (oracle,
// /tmp/r12/scope_removal_probe2.php §F4: `... and "sr_users"."deleted_at" is null`).
func TestThroughSoftDelete_NamedRelatedRemovalKeepsTheThroughPredicate(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &tsdCountry{})
	desc, err := relations.ResolveRelation(&tsdCountry{}, "Posts")
	require.NoError(t, err)

	recorder.returnRows(`from "`+desc.ThroughTable+`"`,
		[]string{desc.FirstKey, desc.SecondLocalKey},
		[]driver.Value{int64(1), int64(1)})

	parents := []reflect.Value{reflect.ValueOf(&tsdCountry{ID: 1}).Elem()}
	callback := func(query contractsorm.Query) contractsorm.Query {
		return query.WithoutGlobalScopes(contractsorm.SoftDeletingScope)
	}
	require.NoError(t, q.loadThrough(parents, &tsdCountry{}, desc, eagerLoadEntry{Relation: "Posts", Callback: callback}, nil, true))

	related := recorder.at(t, 1)
	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" in (?) and "tsd_users"."deleted_at" is null`, related.sql)
}

// TestThroughSoftDelete_NonEagerWithTrashedParents pins the object path: the relation object's
// withTrashedParents drops the through predicate and keeps the related one (oracle,
// /tmp/r12/scope_removal_probe3.php §E: `... where "sr_users"."country_id" = ? and
// "sr_posts"."deleted_at" is null`).
func TestThroughSoftDelete_NonEagerWithTrashedParents(t *testing.T) {
	q := newRelQueryWith(t, &tsdCountry{})
	rel := q.Related(&tsdCountry{ID: 1}, "Posts").(contractsorm.HasManyThroughRelation)

	sql := newRelationSQL(t, rel.WithTrashedParents(), &[]tsdPost{})

	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null`, sql)
}

// TestThroughSoftDelete_MountedScopeReplacesTheThroughPredicate pins the built-in through name from
// the mounting side: a scope mounted under it takes the place of the through predicate, the way a
// mount under SoftDeletingScope replaces the related model's predicate
// (TestApplyScope_ReplacesTheBuiltInSoftDeleteScope). Laravel's performJoin registers that predicate
// under the literal name "SoftDeletableHasManyThrough", so a registration under the same key is what
// applies there.
func TestThroughSoftDelete_MountedScopeReplacesTheThroughPredicate(t *testing.T) {
	q := newRelQueryWith(t, &tsdCountry{})
	desc, err := relations.ResolveRelation(&tsdCountry{}, "Posts")
	require.NoError(t, err)

	built := q.newThroughQuery(&tsdCountry{ID: 1}, desc).(*OrmBuilder)
	statement, bindings, err := built.ApplyScope(contractsorm.SoftDeletableHasManyThrough, func(query contractsorm.Query) contractsorm.Query {
		return query.Where("tsd_users.email", "like", "kept%")
	}).(*OrmBuilder).compileSelect()
	require.NoError(t, err)

	assert.Equal(t, `select "tsd_posts".*, "tsd_users"."country_id" as "laravel_through_key" from "tsd_posts" inner join "tsd_users" on "tsd_users"."id" = "tsd_posts"."user_id" where "tsd_users"."country_id" = ? and "tsd_posts"."deleted_at" is null and "tsd_users"."email" like ?`, statement)
	assert.Equal(t, []any{uint(1), "kept%"}, bindings)
}
