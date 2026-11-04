package has_many_through_soft_delete

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file is the round-10 behavioural supplement for a HasManyThrough whose THROUGH model is
// soft-deletable. Laravel's HasOneOrManyThrough::performJoin registers the named
// "SoftDeletableHasManyThrough" global scope (a whereNull on the through table's deleted-at column)
// whenever the through model uses SoftDeletes, so a soft-deleted through row hides its related rows.
// The port landed that predicate in database/gorm (eager_through_soft_delete_test.go pins the
// compiled SQL); this test exercises it end to end against a real sqlite database.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r10/through_softdelete_probe.php):
// the relation read compiles
//
//	select "tposts".*, "tusers"."country_id" as "laravel_through_key"
//	from "tposts" inner join "tusers" on "tusers"."id" = "tposts"."user_id"
//	where "tusers"."country_id" = ? and "tposts"."deleted_at" is null and "tusers"."deleted_at" is null
//
// and the eager read the same with `in (1)`, so a soft-deleted through row's posts never hydrate.
// The corpus's own intermediate-soft-delete test (TestHasManyThrough_IntermediateSoftDeletesAreIgnored)
// goes through the PLAIN user class, so it does not cover this; the fixtures here declare the
// relation through the SOFT-DELETABLE user class, as Laravel's HasManyThroughSoftDeletesTestCountry
// does for its users() accessor.

// tchCountry is the parent: a posts() HasManyThrough through the soft-deletable user class, and the
// plain-through control reading the same rows through a model with no soft-delete field.
type tchCountry struct {
	ID         uint       `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string     `gorm:"column:name"`
	Posts      []*tchPost `gorm:"-"`
	PlainPosts []*tchPost `gorm:"-"`
}

func (tchCountry) TableName() string { return "countries" }

func (tchCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts":      contractsorm.HasManyThrough{Related: &tchPost{}, Through: &tchUser{}},
		"PlainPosts": contractsorm.HasManyThrough{Related: &tchPost{}, Through: &tchPlainUser{}},
	}
}

// tchUser is the soft-deletable through model (Laravel's HasManyThroughSoftDeletesTestUser).
type tchUser struct {
	databaseorm.SoftDeletes

	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CountryID uint             `gorm:"column:country_id"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (tchUser) TableName() string { return "users" }

// tchPlainUser is the control through model: the same table, no soft-delete field, so the relation
// ignores the soft delete exactly as the corpus's plain hasManyThrough does.
type tchPlainUser struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CountryID uint             `gorm:"column:country_id"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (tchPlainUser) TableName() string { return "users" }

// tchPost is the related model (not soft-deletable, so the through predicate is the only filter).
type tchPost struct {
	ID     uint   `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint   `gorm:"column:user_id"`
	Title  string `gorm:"column:title"`
}

func (tchPost) TableName() string { return "posts" }

func tchSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table countries (id integer primary key autoincrement, name varchar not null)`,
		`create table users (id integer primary key autoincrement, country_id integer not null, email varchar not null, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
		`create table posts (id integer primary key autoincrement, user_id integer not null, title varchar not null)`,
	)
}

// tchSeed creates country 1 with two users and a post each, then soft-deletes user 2, so its post
// ("B title") must disappear from the soft-deletable-through read while "A title" survives.
func tchSeed(t *testing.T, query contractsorm.Query) {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Create(&tchCountry{ID: 1, Name: "United States of America"}))
	require.NoError(t, cloneRelationQuery(t, query).Create(&tchUser{ID: 1, CountryID: 1, Email: "u1@example.com"}))
	require.NoError(t, cloneRelationQuery(t, query).Create(&tchUser{ID: 2, CountryID: 1, Email: "u2@example.com"}))
	require.NoError(t, cloneRelationQuery(t, query).Create(&tchPost{UserID: 1, Title: "A title"}))
	require.NoError(t, cloneRelationQuery(t, query).Create(&tchPost{UserID: 2, Title: "B title"}))

	soft := tchUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&tchUser{}).Where("id", 2).First(&soft))
	_, err := cloneRelationQuery(t, query).Delete(&soft)
	require.NoError(t, err)
}

// TestHasManyThroughThroughSoftDeletes_HidesRelatedOfSoftDeletedThrough is the lazy read: the
// soft-deleted through row (user 2) hides its post.
func TestHasManyThroughThroughSoftDeletes_HidesRelatedOfSoftDeletedThrough(t *testing.T) {
	query := newRelationQuery(t)
	tchSchema(t, query)
	tchSeed(t, query)

	country := tchCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&tchCountry{}).First(&country))
	require.NoError(t, cloneRelationQuery(t, query).Load(&country, "Posts"))

	require.Len(t, country.Posts, 1, "the soft-deleted through row's post must be filtered")
	assert.Equal(t, "A title", country.Posts[0].Title)
}

// TestHasManyThroughThroughSoftDeletes_EagerHidesRelatedOfSoftDeletedThrough is the eager read.
func TestHasManyThroughThroughSoftDeletes_EagerHidesRelatedOfSoftDeletedThrough(t *testing.T) {
	query := newRelationQuery(t)
	tchSchema(t, query)
	tchSeed(t, query)

	found := []tchCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&tchCountry{}).With("Posts").Get(&found))
	require.Len(t, found, 1)
	require.Len(t, found[0].Posts, 1, "the eager read must filter the soft-deleted through row too")
	assert.Equal(t, "A title", found[0].Posts[0].Title)
}

// TestHasManyThroughThroughSoftDeletes_PlainThroughIgnoresTheSoftDelete is the control: the same
// relation through a model with no soft-delete field reads both posts, matching the corpus's
// TestHasManyThrough_IntermediateSoftDeletesAreIgnored.
func TestHasManyThroughThroughSoftDeletes_PlainThroughIgnoresTheSoftDelete(t *testing.T) {
	query := newRelationQuery(t)
	tchSchema(t, query)
	tchSeed(t, query)

	country := tchCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&tchCountry{}).First(&country))
	require.NoError(t, cloneRelationQuery(t, query).Load(&country, "PlainPosts"))

	require.Len(t, country.PlainPosts, 2, "a plain through model ignores the soft delete")
}

// TestHasManyThroughThroughSoftDeletes_ExistsSeesTheFilter pins the non-eager read the relation
// forwards to the Eloquent builder: exists() drops the laravel_through_key alias but must keep the
// through soft-delete predicate (oracle, /tmp/r10/verify/pluck_exists_probe.php P3), so a country
// whose only related row hangs off a soft-deleted through user reads as empty.
func TestHasManyThroughThroughSoftDeletes_ExistsSeesTheFilter(t *testing.T) {
	query := newRelationQuery(t)
	tchSchema(t, query)
	tchSeed(t, query)

	// Country 2: a single user, soft-deleted, carrying one post.
	require.NoError(t, cloneRelationQuery(t, query).Create(&tchCountry{ID: 2, Name: "Canada"}))
	require.NoError(t, cloneRelationQuery(t, query).Create(&tchUser{ID: 3, CountryID: 2, Email: "u3@example.com"}))
	require.NoError(t, cloneRelationQuery(t, query).Create(&tchPost{UserID: 3, Title: "C title"}))
	soft := tchUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&tchUser{}).Where("id", 3).First(&soft))
	_, err := cloneRelationQuery(t, query).Delete(&soft)
	require.NoError(t, err)

	exists, err := cloneRelationQuery(t, query).Related(&tchCountry{ID: 2}, "Posts").Exists()
	require.NoError(t, err)
	assert.False(t, exists, "exists() must not see a post whose through row is soft-deleted")

	// Control: country 1's surviving post is visible to the same read.
	exists, err = cloneRelationQuery(t, query).Related(&tchCountry{ID: 1}, "Posts").Exists()
	require.NoError(t, err)
	assert.True(t, exists, "exists() must see a post whose through row is live")
}

// TestHasManyThroughThroughSoftDeletes_WithTrashedParentsIncludesTheThroughRows pins the through-side
// switch Laravel exposes: withTrashedParents() drops exactly the named "SoftDeletableHasManyThrough"
// scope, so a soft-deleted through row's post is read again - the lazy and eager forms alike. The
// related model (tchPost) is not soft-deletable, so the dropped predicate is the only one that
// matters. Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe3.php
// §E/§F2): `->withTrashedParents()` and `->withoutGlobalScope('SoftDeletableHasManyThrough')`
// compile the same statement as the default read minus the through predicate.
func TestHasManyThroughThroughSoftDeletes_WithTrashedParentsIncludesTheThroughRows(t *testing.T) {
	query := newRelationQuery(t)
	tchSchema(t, query)
	tchSeed(t, query)

	// Lazy: the relation object's switch.
	rel := cloneRelationQuery(t, query).Related(&tchCountry{ID: 1}, "Posts").(contractsorm.HasManyThroughRelation)
	posts := []tchPost{}
	require.NoError(t, rel.WithTrashedParents().Get(&posts))
	require.Len(t, posts, 2, "withTrashedParents must include the soft-deleted through row's post")
	assert.ElementsMatch(t, []string{"A title", "B title"}, []string{posts[0].Title, posts[1].Title})

	// Eager: the callback receives the relation object and its withTrashedParents reaches the child.
	found := []tchCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&tchCountry{}).
		With("Posts", func(q contractsorm.Query) contractsorm.Query {
			return q.(contractsorm.HasManyThroughRelation).WithTrashedParents()
		}).Get(&found))
	require.Len(t, found, 1)
	require.Len(t, found[0].Posts, 2, "the eager withTrashedParents must include the soft-deleted through row's post")
}
