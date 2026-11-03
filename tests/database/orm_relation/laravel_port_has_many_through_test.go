package orm_relation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasManyThroughIntegrationTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestHasManyThrough_<method without the test prefix>. The corpus's three tables (plus the
// _default tables its default-relation test migrates) mirror createSchema() in sqlite DDL; rows are
// seeded through the ORM, and relations are loaded explicitly through OrmBuilder.Load — the
// harness's lazy-loading rule, since Go has no property-access magic.
//
// The corpus checks behaviour, not compiled SQL: it has no ->toSql() / ->toRawSql() pins. The
// oracle (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1) was still used to
// measure the SQL behind that behaviour, and the comments below cite those measurements: the
// relation builder's own toSql is `select * from "posts" inner join "users" on "users"."id" =
// "posts"."user_id" where "users"."country_id" = ?`, the first()/get() path rewrites the select to
// `"posts".*, "users"."country_id" as "laravel_through_key"`, first(['title', 'body']) selects
// `"title", "body", "users"."country_id" as "laravel_through_key"`, and the whereHas existence
// subquery is `select * from "countries" where exists (select * from "posts" inner join "users" on
// "users"."email" = "posts"."email" where "countries"."shortname" = "users"."country_short" and
// "title" = ?)`.
//
// # How our has-many-through diverges from Laravel's
//
//   - first()/get()/cursor() wear Laravel's read select — `<related>.*` plus the through key
//     aliased as laravel_through_key (HasOneOrManyThrough::shouldSelect) — so a hydrated model
//     carries its own columns only: reading country 2's posts back, the six posts 3-8 read their
//     own ids and emails, where the joined through row used to overwrite them.
//   - Find / FindOr / FindOrFail with a slice delegate to the many-form read, the way Laravel's
//     find() delegates an array to findMany(): one `and "posts"."id" in (?, ?)` on the qualified
//     key column — the bare `id in (...)` was ambiguous on the joined read — collecting every
//     match.
//   - ModelNotFoundException messages name the model and its ids; the Go ORM reports the fixed
//     sentinel errors.OrmRecordNotFound ("record not found") for the single-key form, and
//     errors.OrmModelNotFound naming the Go model and every requested id for the list form.
//   - The six iteration ports run through Query.Related: the walk visits country 2's posts in
//     key order and every page's SQL is the oracle's, pinned through the query log. The
//     corpus's callback attribute-list assertions (array_keys(getAttributes()), including
//     laravel_through_key) stay PHP-only: a typed Go model has no attribute bag, and the
//     through key is selected by the read but captured by no field.

// --- models ------------------------------------------------------------------

// hasManyThroughUser ports HasManyThroughTestUser.
type hasManyThroughUser struct {
	ID           uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Email        string           `gorm:"column:email"`
	CountryID    uint             `gorm:"column:country_id"`
	CountryShort string           `gorm:"column:country_short"`
	CreatedAt    *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt    *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasManyThroughUser) TableName() string { return "users" }

func (hasManyThroughUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &hasManyThroughPost{}, ForeignKey: "user_id"},
	}
}

// hasManyThroughPost ports HasManyThroughTestPost.
type hasManyThroughPost struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint             `gorm:"column:user_id"`
	Title     string           `gorm:"column:title"`
	Body      string           `gorm:"column:body"`
	Email     string           `gorm:"column:email"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasManyThroughPost) TableName() string { return "posts" }

// hasManyThroughCountry ports HasManyThroughTestCountry: posts through users, with the corpus's
// explicit keys (users.country_id -> posts.user_id, on countries.id).
type hasManyThroughCountry struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	Shortname string           `gorm:"column:shortname"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts []*hasManyThroughPost `gorm:"-"`
}

func (hasManyThroughCountry) TableName() string { return "countries" }

func (hasManyThroughCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related: &hasManyThroughPost{}, Through: &hasManyThroughUser{},
			FirstKey: "country_id", SecondKey: "user_id",
		},
		"Users": contractsorm.HasMany{Related: &hasManyThroughUser{}, ForeignKey: "country_id"},
	}
}

// hasManyThroughIntermediateCountry ports HasManyThroughIntermediateTestCountry: the same tables
// related by country_short -> email, with the local keys shortname -> email.
type hasManyThroughIntermediateCountry struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	Shortname string           `gorm:"column:shortname"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts []*hasManyThroughPost `gorm:"-"`
}

func (hasManyThroughIntermediateCountry) TableName() string { return "countries" }

func (hasManyThroughIntermediateCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related: &hasManyThroughPost{}, Through: &hasManyThroughUser{},
			FirstKey: "country_short", SecondKey: "email", LocalKey: "shortname", SecondLocalKey: "email",
		},
		"Users": contractsorm.HasMany{Related: &hasManyThroughUser{}, ForeignKey: "country_id"},
	}
}

// hasManyThroughDefaultUser ports HasManyThroughDefaultTestUser: the class-named default keys
// (has_many_through_default_test_*_id) are declared explicitly, because the Go ORM derives its
// defaults from table names where Laravel derives them from class names (oracle: a class
// basename of OracleHmtDefaultUser yields oracle_hmt_default_user_id).
type hasManyThroughDefaultUser struct {
	ID               uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Email            string           `gorm:"column:email"`
	DefaultCountryID uint             `gorm:"column:has_many_through_default_test_country_id"`
	CreatedAt        *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt        *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts []*hasManyThroughDefaultPost `gorm:"-"`
}

func (hasManyThroughDefaultUser) TableName() string { return "users_default" }

func (hasManyThroughDefaultUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &hasManyThroughDefaultPost{}, ForeignKey: "has_many_through_default_test_user_id"},
	}
}

// hasManyThroughDefaultPost ports HasManyThroughDefaultTestPost.
type hasManyThroughDefaultPost struct {
	ID            uint             `gorm:"column:id;primaryKey;autoIncrement"`
	DefaultUserID uint             `gorm:"column:has_many_through_default_test_user_id"`
	Title         string           `gorm:"column:title"`
	Body          string           `gorm:"column:body"`
	CreatedAt     *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt     *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasManyThroughDefaultPost) TableName() string { return "posts_default" }

// hasManyThroughDefaultCountry ports HasManyThroughDefaultTestCountry.
type hasManyThroughDefaultCountry struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts []*hasManyThroughDefaultPost `gorm:"-"`
}

func (hasManyThroughDefaultCountry) TableName() string { return "countries_default" }

func (hasManyThroughDefaultCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related: &hasManyThroughDefaultPost{}, Through: &hasManyThroughDefaultUser{},
			FirstKey: "has_many_through_default_test_country_id", SecondKey: "has_many_through_default_test_user_id",
		},
		"Users": contractsorm.HasMany{Related: &hasManyThroughDefaultUser{}, ForeignKey: "has_many_through_default_test_country_id"},
	}
}

// hasManyThroughSoftUser ports HasManyThroughSoftDeletesTestUser: the users table with the
// framework's soft-delete field. The corpus's soft-delete test deletes through this class and then
// reads the country's posts through HasManyThroughTestUser — the plain class — so the relation
// never carries this trait.
type hasManyThroughSoftUser struct {
	databaseorm.SoftDeletes

	ID           uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Email        string           `gorm:"column:email"`
	CountryID    uint             `gorm:"column:country_id"`
	CountryShort string           `gorm:"column:country_short"`
	CreatedAt    *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt    *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (hasManyThroughSoftUser) TableName() string { return "users" }

// hasManyThroughSoftCountry ports HasManyThroughSoftDeletesTestCountry. In the corpus its related
// class (HasManyThroughSoftDeletesTestPost) differs from HasManyThroughTestPost only in name —
// neither carries SoftDeletes — so the port reuses hasManyThroughPost; the through model is the
// plain user class, exactly as the corpus declares it.
type hasManyThroughSoftCountry struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	Name      string           `gorm:"column:name"`
	Shortname string           `gorm:"column:shortname"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Posts []*hasManyThroughPost `gorm:"-"`
}

func (hasManyThroughSoftCountry) TableName() string { return "countries" }

func (hasManyThroughSoftCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related: &hasManyThroughPost{}, Through: &hasManyThroughUser{},
			FirstKey: "country_id", SecondKey: "user_id",
		},
		"Users": contractsorm.HasMany{Related: &hasManyThroughUser{}, ForeignKey: "country_id"},
	}
}

// --- schema and seeding ------------------------------------------------------

// hasManyThroughSchema mirrors the corpus's createSchema() in sqlite DDL: users carries
// softDeletes(), and every table carries timestamps() (nullable datetime columns).
func hasManyThroughSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique, country_id integer not null, country_short varchar not null, created_at datetime null, updated_at datetime null, deleted_at datetime null)`,
		`create table posts (id integer primary key autoincrement, user_id integer not null, title varchar not null, body text not null, email varchar not null, created_at datetime null, updated_at datetime null)`,
		`create table countries (id integer primary key autoincrement, name varchar not null, shortname varchar not null, created_at datetime null, updated_at datetime null)`,
	)
}

// hasManyThroughDefaultSchema mirrors migrateDefault(): the tables backing the corpus's
// "default" hasManyThrough setup, whose foreign keys carry the class-derived names.
func hasManyThroughDefaultSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users_default (id integer primary key autoincrement, email varchar not null unique, has_many_through_default_test_country_id integer not null, created_at datetime null, updated_at datetime null)`,
		`create table posts_default (id integer primary key autoincrement, has_many_through_default_test_user_id integer not null, title varchar not null, body text not null, created_at datetime null, updated_at datetime null)`,
		`create table countries_default (id integer primary key autoincrement, name varchar not null, created_at datetime null, updated_at datetime null)`,
	)
}

// hasManyThroughSeedData ports seedData(): country 1 (us) -> user 1 -> A title / Another title.
func hasManyThroughSeedData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	country := hasManyThroughCountry{ID: 1, Name: "United States of America", Shortname: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))

	user := hasManyThroughUser{ID: 1, Email: "taylorotwell@gmail.com", CountryShort: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))

	posts := []hasManyThroughPost{
		{Title: "A title", Body: "A body", Email: "taylorotwell@gmail.com"},
		{Title: "Another title", Body: "Another body", Email: "taylorotwell@gmail.com"},
	}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&posts))
}

// hasManyThroughSeedDataExtended ports seedDataExtended(): country 2 (uk) with users 2-4, two
// posts each, for the iteration ports.
func hasManyThroughSeedDataExtended(t *testing.T, query contractsorm.Query) {
	t.Helper()

	country := hasManyThroughCountry{ID: 2, Name: "United Kingdom", Shortname: "uk"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))

	user := hasManyThroughUser{ID: 2, Email: "example1@gmail.com", CountryShort: "uk"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&[]hasManyThroughPost{
		{Title: "Example1 title1", Body: "Example1 body1", Email: "example1post1@gmail.com"},
		{Title: "Example1 title2", Body: "Example1 body2", Email: "example1post2@gmail.com"},
	}))

	user = hasManyThroughUser{ID: 3, Email: "example2@gmail.com", CountryShort: "uk"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&[]hasManyThroughPost{
		{Title: "Example2 title1", Body: "Example2 body1", Email: "example2post1@gmail.com"},
		{Title: "Example2 title2", Body: "Example2 body2", Email: "example2post2@gmail.com"},
	}))

	user = hasManyThroughUser{ID: 4, Email: "example3@gmail.com", CountryShort: "uk"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&[]hasManyThroughPost{
		{Title: "Example3 title1", Body: "Example3 body1", Email: "example3post1@gmail.com"},
		{Title: "Example3 title2", Body: "Example3 body2", Email: "example3post2@gmail.com"},
	}))
}

// hasManyThroughSeedDefaultData ports seedDefaultData(): country 1 -> user 1 -> two posts on the
// _default tables.
func hasManyThroughSeedDefaultData(t *testing.T, query contractsorm.Query) {
	t.Helper()

	country := hasManyThroughDefaultCountry{ID: 1, Name: "United States of America"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))

	user := hasManyThroughDefaultUser{ID: 1, Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))

	posts := []hasManyThroughDefaultPost{
		{Title: "A title", Body: "A body"},
		{Title: "Another title", Body: "Another body"},
	}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&posts))
}

// hasManyThroughSeedFindFixture ports the seeding the two find tests share inline: country 1,
// user 1, and posts with explicit ids 1 and 2.
func hasManyThroughSeedFindFixture(t *testing.T, query contractsorm.Query) hasManyThroughCountry {
	t.Helper()

	country := hasManyThroughCountry{ID: 1, Name: "United States of America", Shortname: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))

	user := hasManyThroughUser{ID: 1, Email: "taylorotwell@gmail.com", CountryShort: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))

	posts := []hasManyThroughPost{
		{ID: 1, Title: "A title", Body: "A body", Email: "taylorotwell@gmail.com"},
		{ID: 2, Title: "Another title", Body: "Another body", Email: "taylorotwell@gmail.com"},
	}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&posts))

	return country
}

// --- ports of the corpus -----------------------------------------------------

func TestHasManyThrough_ItLoadsAHasManyThroughRelationWithCustomKeys(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))
	require.NoError(t, cloneRelationQuery(t, query).Load(&country, "Posts"))

	require.Len(t, country.Posts, 2)
	assert.Equal(t, "A title", country.Posts[0].Title)
}

func TestHasManyThrough_ItLoadsADefaultHasManyThroughRelation(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughDefaultSchema(t, query)
	hasManyThroughSeedDefaultData(t, query)

	country := hasManyThroughDefaultCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))
	require.NoError(t, cloneRelationQuery(t, query).Load(&country, "Posts"))

	require.Len(t, country.Posts, 2)
	assert.Equal(t, "A title", country.Posts[0].Title)

	// Laravel drops the _default tables afterwards (resetDefault) to keep its shared in-memory
	// database clean; the harness gives every test its own database, so there is nothing to reset.
}

func TestHasManyThrough_ItLoadsARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	country := hasManyThroughIntermediateCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))
	require.NoError(t, cloneRelationQuery(t, query).Load(&country, "Posts"))

	require.Len(t, country.Posts, 2)
	assert.Equal(t, "A title", country.Posts[0].Title)
}

func TestHasManyThrough_EagerLoadingARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	found := []hasManyThroughIntermediateCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyThroughIntermediateCountry{}).With("Posts").Get(&found))
	require.Len(t, found, 1)
	require.Len(t, found[0].Posts, 2)
	assert.Equal(t, "A title", found[0].Posts[0].Title)
}

func TestHasManyThrough_WhereHasOnARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	countries := []hasManyThroughIntermediateCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyThroughIntermediateCountry{}).
		WhereHas("Posts", func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "A title") }).
		Get(&countries))

	assert.Len(t, countries, 1)
}

func TestHasManyThrough_WithWhereHasOnARelationWithCustomIntermediateAndLocalKey(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	countries := []hasManyThroughIntermediateCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyThroughIntermediateCountry{}).
		WithWhereHas("Posts", func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "A title") }).
		Get(&countries))

	require.Len(t, countries, 1)

	// Laravel asserts relationLoaded('posts') and posts.pluck('title')->unique() == ['A title']; in
	// Go the eager load shows as the field being populated, and the unique title set is the single
	// loaded post's title.
	require.Len(t, countries[0].Posts, 1)
	assert.Equal(t, "A title", countries[0].Posts[0].Title)
}

func TestHasManyThrough_FindMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	country := hasManyThroughSeedFindFixture(t, query)

	post := hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Find(&post, 1))
	assert.Equal(t, uint(1), post.ID)
	assert.Equal(t, "A title", post.Title)

	// $country->posts()->find([1, 2]) — Laravel's find() delegates an array to findMany(), and the
	// Go port's Find does the same for a slice destination. Oracle: the read is the relation's
	// findMany — `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts"
	// inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = ? and
	// "posts"."id" in (?, ?)` with bindings 1, 1, 2 — collecting every match.
	posts := []hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Find(&posts, []any{1, 2}))
	require.Len(t, posts, 2)
	assert.Equal(t, uint(1), posts[0].ID)
	assert.Equal(t, uint(2), posts[1].ID)
	assert.Equal(t, "A title", posts[0].Title)
	assert.Equal(t, "Another title", posts[1].Title)

	// find(new Collection([1, 2])) delegates the same way; a Go slice is that collection.
	posts = nil
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Find(&posts, []any{1, 2}))
	require.Len(t, posts, 2)
}

func TestHasManyThrough_FindManyMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	country := hasManyThroughSeedFindFixture(t, query)

	posts := []hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").FindMany(&posts, []any{1, 2}))
	assert.Len(t, posts, 2)

	// findMany(new Collection([1, 2])) is the same list in Go.
	posts = nil
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").FindMany(&posts, []any{1, 2}))
	assert.Len(t, posts, 2)
}

func TestHasManyThrough_FirstOrFailThrowsAnException(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)

	country := hasManyThroughCountry{ID: 1, Name: "United States of America", Shortname: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))
	user := hasManyThroughUser{ID: 1, Email: "taylorotwell@gmail.com", CountryShort: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))

	post := hasManyThroughPost{}
	err := cloneRelationQuery(t, query).Related(&country, "Posts").FirstOrFail(&post)
	require.Error(t, err)

	// Laravel throws ModelNotFoundException with the message "No query results for model
	// [Illuminate\Tests\Database\HasManyThroughTestPost]."; the Go ORM reports the fixed sentinel
	// errors.OrmRecordNotFound, whose message names no model.
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
}

func TestHasManyThrough_FindOrFailThrowsAnException(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)

	country := hasManyThroughCountry{ID: 1, Name: "United States of America", Shortname: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))
	user := hasManyThroughUser{ID: 1, Email: "taylorotwell@gmail.com", CountryShort: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))

	post := hasManyThroughPost{}
	err := cloneRelationQuery(t, query).Related(&country, "Posts").FindOrFail(&post, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
}

func TestHasManyThrough_FindOrFailWithManyThrowsAnException(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)

	country := hasManyThroughCountry{ID: 1, Name: "United States of America", Shortname: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))
	user := hasManyThroughUser{ID: 1, Email: "taylorotwell@gmail.com", CountryShort: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").Create(&hasManyThroughPost{
		ID: 1, Title: "A title", Body: "A body", Email: "taylorotwell@gmail.com",
	}))

	posts := []hasManyThroughPost{}
	err := cloneRelationQuery(t, query).Related(&country, "Posts").FindOrFail(&posts, []any{1, 2})
	require.Error(t, err)

	// The read is the relation's findMany: oracle `findOrFail([1, 2])` runs `select "posts".*,
	// "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id"
	// = "posts"."user_id" where "users"."country_id" = ? and "posts"."id" in (?, ?)` with bindings
	// 1, 1, 2, then demands every requested id resolve. Post 1 exists and 2 does not, so Laravel
	// throws ModelNotFoundException naming the model and every requested id, found or missing, in
	// the order given: "No query results for model [Illuminate\Tests\Database\HasManyThroughTestPost]
	// 1, 2". The Go ORM names the Go model type instead of the PHP class and wraps
	// errors.OrmRecordNotFound, so callers that match the sentinel keep working.
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
	assert.EqualError(t, err, "No query results for model [orm_relation.hasManyThroughPost] 1, 2")
}

func TestHasManyThrough_FindOrFailWithManyUsingCollectionThrowsAnException(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)

	country := hasManyThroughCountry{ID: 1, Name: "United States of America", Shortname: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))
	user := hasManyThroughUser{ID: 1, Email: "taylorotwell@gmail.com", CountryShort: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").Create(&hasManyThroughPost{
		ID: 1, Title: "A title", Body: "A body", Email: "taylorotwell@gmail.com",
	}))

	// findOrFail(new Collection([1, 2])) is the same Go slice call as the array form, and the
	// oracle behaves identically: with post 1 present and 2 missing it throws "No query results for
	// model [Illuminate\Tests\Database\HasManyThroughTestPost] 1, 2".
	posts := []hasManyThroughPost{}
	err := cloneRelationQuery(t, query).Related(&country, "Posts").FindOrFail(&posts, []any{1, 2})
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmRecordNotFound)
	assert.EqualError(t, err, "No query results for model [orm_relation.hasManyThroughPost] 1, 2")
}

func TestHasManyThrough_FindOrMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)

	country := hasManyThroughCountry{ID: 1, Name: "United States of America", Shortname: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&country))
	user := hasManyThroughUser{ID: 1, Email: "taylorotwell@gmail.com", CountryShort: "us"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&country, "Users").Create(&user))
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "Posts").CreateMany(&[]hasManyThroughPost{
		{ID: 1, Title: "A title", Body: "A body", Email: "taylorotwell@gmail.com"},
	}))

	// findOr(1, fn) — the row exists.
	post := hasManyThroughPost{}
	called := false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").
		FindOr(&post, 1, func() error { called = true; return nil }))
	assert.False(t, called)
	assert.Equal(t, uint(1), post.ID)
	assert.Equal(t, "A title", post.Title)

	// findOr(1, ['posts.id'], fn) — only the key column is selected. Go's FindOr takes no column
	// list, so the columns are pinned on the chain first; the observable is the same, only the key
	// is hydrated, and the read appends Laravel's laravel_through_key alias after them (oracle).
	post = hasManyThroughPost{}
	called = false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Select("posts.id").
		FindOr(&post, 1, func() error { called = true; return nil }))
	assert.False(t, called)
	assert.Equal(t, uint(1), post.ID)
	assert.Equal(t, "", post.Title)

	// findOr(2, fn) — no row; Laravel returns the callback's result ('callback result'), the Go
	// callback returns only an error, so the port observes it through the call and an empty dest.
	post = hasManyThroughPost{}
	called = false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").
		FindOr(&post, 2, func() error { called = true; return nil }))
	assert.True(t, called)
	assert.Zero(t, post.ID)
}

func TestHasManyThrough_FindOrMethodWithMany(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	country := hasManyThroughSeedFindFixture(t, query)

	// findOr([1, 2], fn) — every id resolves, so Laravel returns the two-model Collection and the
	// callback never runs. Oracle: the read is `... where "users"."country_id" = ? and "posts"."id"
	// in (?, ?)` with bindings 1, 1, 2.
	posts := []hasManyThroughPost{}
	called := false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").
		FindOr(&posts, []any{1, 2}, func() error { called = true; return nil }))
	assert.False(t, called)
	require.Len(t, posts, 2)
	// Laravel asserts $result[0]->id === 1 and $result[1]->id === 2. The read is findMany's, and
	// the oracle select `"posts".*, "users"."country_id" as "laravel_through_key"` keeps the
	// through user's columns out of the way, so each post reads back its own id.
	assert.Equal(t, uint(1), posts[0].ID)
	assert.Equal(t, uint(2), posts[1].ID)
	assert.Equal(t, "A title", posts[0].Title)
	assert.Equal(t, "Another title", posts[1].Title)

	// findOr([1, 2], ['posts.id'], fn) — only the key column is selected. Go's FindOr takes no
	// column list, so the columns are pinned on the chain first, as in TestHasManyThrough_FindOrMethod;
	// the read appends Laravel's laravel_through_key alias after them.
	posts = nil
	called = false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Select("posts.id").
		FindOr(&posts, []any{1, 2}, func() error { called = true; return nil }))
	assert.False(t, called)
	require.Len(t, posts, 2)
	assert.Equal(t, uint(1), posts[0].ID)
	assert.Equal(t, uint(2), posts[1].ID)
	assert.Equal(t, "", posts[0].Title)
	assert.Equal(t, "", posts[1].Title)

	// findOr([1, 2, 3], fn) — id 3 is missing, so Laravel runs the callback and returns its result
	// ('callback result'); the Go callback returns only an error, so the port observes the call and
	// that dest was left alone.
	posts = nil
	called = false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").
		FindOr(&posts, []any{1, 2, 3}, func() error { called = true; return nil }))
	assert.True(t, called)
	assert.Empty(t, posts)
}

func TestHasManyThrough_FindOrMethodWithManyUsingCollection(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	country := hasManyThroughSeedFindFixture(t, query)

	// findOr(new Collection([1, 2]), fn) is the same Go slice call as the array form: the oracle
	// returns the two-model Collection for [1, 2] and falls to the callback for [1, 2, 3].
	posts := []hasManyThroughPost{}
	called := false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").
		FindOr(&posts, []any{1, 2}, func() error { called = true; return nil }))
	assert.False(t, called)
	require.Len(t, posts, 2)
	// Laravel asserts $result[0]->id === 1 and $result[1]->id === 2. The read is findMany's, and
	// the oracle select `"posts".*, "users"."country_id" as "laravel_through_key"` keeps the
	// through user's columns out of the way, so each post reads back its own id.
	assert.Equal(t, uint(1), posts[0].ID)
	assert.Equal(t, uint(2), posts[1].ID)
	assert.Equal(t, "A title", posts[0].Title)
	assert.Equal(t, "Another title", posts[1].Title)

	posts = nil
	called = false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Select("posts.id").
		FindOr(&posts, []any{1, 2}, func() error { called = true; return nil }))
	assert.False(t, called)
	require.Len(t, posts, 2)
	assert.Equal(t, uint(1), posts[0].ID)
	assert.Equal(t, uint(2), posts[1].ID)
	assert.Equal(t, "", posts[0].Title)
	assert.Equal(t, "", posts[1].Title)

	posts = nil
	called = false
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").
		FindOr(&posts, []any{1, 2, 3}, func() error { called = true; return nil }))
	assert.True(t, called)
	assert.Empty(t, posts)
}

func TestHasManyThrough_FirstRetrievesFirstRecord(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))

	post := hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").First(&post))
	assert.NotZero(t, post.ID)
	assert.Equal(t, "A title", post.Title)
}

func TestHasManyThrough_AllColumnsAreRetrievedByDefault(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))

	post := hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").First(&post))

	// Laravel compares array_keys(getAttributes()) with ['id', 'user_id', 'title', 'body',
	// 'email', 'created_at', 'updated_at', 'laravel_through_key']. A typed Go model has no dynamic
	// attribute bag, so the port asserts every real column is hydrated; laravel_through_key is the
	// through key alias Laravel's first() selects alongside `"posts".*` (oracle), which this read
	// selects too and the row scan ignores. The fixture keeps this post's key and email equal to
	// the through user's, so the joined read returns them unchanged.
	assert.Equal(t, uint(1), post.ID)
	assert.Equal(t, uint(1), post.UserID)
	assert.Equal(t, "A title", post.Title)
	assert.Equal(t, "A body", post.Body)
	assert.Equal(t, "taylorotwell@gmail.com", post.Email)
	assert.NotNil(t, post.CreatedAt)
	assert.NotNil(t, post.UpdatedAt)
}

func TestHasManyThrough_OnlyProperColumnsAreSelectedIfProvided(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&country).First(&country))

	// Laravel: $country->posts()->first(['title', 'body']). Go's First takes no column list, so the
	// columns are pinned on the chain before First; the observable — title and body hydrated and
	// nothing else — is the same, and the read appends Laravel's alias after them (oracle:
	// `"title", "body", "users"."country_id" as "laravel_through_key"`).
	post := hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Select("title", "body").First(&post))
	assert.Equal(t, "A title", post.Title)
	assert.Equal(t, "A body", post.Body)
	assert.Zero(t, post.ID)
	assert.Zero(t, post.UserID)
	assert.Equal(t, "", post.Email)
}

func TestHasManyThrough_ChunkReturnsCorrectModels(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	// Laravel: $country->posts()->chunk(10, cb) over country 2 - one page holding the six posts.
	// Oracle (illuminate/database v12.55.1, executed against sqlite with this seed): one
	// statement, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts"
	// inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = ?
	// order by "posts"."id" asc limit 10 offset 0` (bindings [2]); the callback's first post
	// id 3. The corpus's attribute-list assertion (getAttributes() including
	// laravel_through_key) is PHP-only; the typed-model port asserts the hydrated columns.
	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&country, 2))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	pages := 0
	posts := []hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&country, "Posts").Chunk(10, &posts, func(page int) error {
		pages++

		return nil
	}))

	assert.Equal(t, 1, pages)
	require.Len(t, posts, 6)
	assert.Equal(t, uint(3), posts[0].ID)
	assert.Equal(t, "Example1 title1", posts[0].Title)
	assert.Equal(t, "example1post1@gmail.com", posts[0].Email)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "chunk(10) reads the six posts in one statement")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 order by "posts"."id" asc limit 10 offset 0`, logs[0].Query)
}

func TestHasManyThrough_ChunkById(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	// Laravel: $country->posts()->chunkById(2, cb) - three chunks of two over the six posts,
	// key-ordered by the related model's qualified key (HasOneOrManyThrough::chunkById's
	// defaults). Oracle: four statements - page one filters `"posts"."id" is not null`, every
	// later page `"posts"."id" > ?` with the previous page's last id ([2,4], [2,6], [2,8]),
	// each ordered by "posts"."id" ascending and limited to two; the fourth is the empty
	// terminator page.
	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&country, 2))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	chunks := 0
	count := 0
	posts := []hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&country, "Posts").ChunkById(2, &posts, func(page int) error {
		chunks++
		count += len(posts)

		return nil
	}))

	assert.Equal(t, 3, chunks)
	assert.Equal(t, 6, count)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 4, "chunkById(2) issues one statement per page plus the empty terminator page")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" is not null order by "posts"."id" asc limit 2`, logs[0].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" > 4 order by "posts"."id" asc limit 2`, logs[1].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" > 6 order by "posts"."id" asc limit 2`, logs[2].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" > 8 order by "posts"."id" asc limit 2`, logs[3].Query)
}

func TestHasManyThrough_CursorReturnsCorrectModels(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	// Laravel: HasManyThroughTestCountry::find(2).
	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&country, 2))

	// Laravel's cursor() reads `"posts".*, "users"."country_id" as "laravel_through_key"`; the
	// oracle (illuminate/database v12.55.1, executed against sqlite) logs `select "posts".*,
	// "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id"
	// = "posts"."user_id" where "users"."country_id" = ?` for `$country->posts()->cursor()`, with
	// the binding inlined here the way the query log renders it.
	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	posts := []hasManyThroughPost{}
	for row := range withContext.WithContext(ctx).Related(&country, "Posts").Cursor() {
		require.NoError(t, row.Err())

		post := hasManyThroughPost{}
		require.NoError(t, row.Scan(&post))

		posts = append(posts, post)
	}

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "the cursor read is one statement")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2`, logs[0].Query)

	// The corpus asserts every cursor row's attribute list — the model's own columns plus
	// laravel_through_key; the typed-model port reads country 2's six posts back with their own
	// ids and emails, where the joined through row used to overwrite them (the probe's 2, 2, 3,
	// 3, 4, 4 and example1/2/3@gmail.com). The LazyCollection instance the corpus also checks is
	// PHP-only.
	require.Len(t, posts, 6)
	wantIDs := []uint{3, 4, 5, 6, 7, 8}
	wantTitles := []string{
		"Example1 title1", "Example1 title2",
		"Example2 title1", "Example2 title2",
		"Example3 title1", "Example3 title2",
	}
	wantEmails := []string{
		"example1post1@gmail.com", "example1post2@gmail.com",
		"example2post1@gmail.com", "example2post2@gmail.com",
		"example3post1@gmail.com", "example3post2@gmail.com",
	}
	for index, post := range posts {
		assert.Equal(t, wantIDs[index], post.ID)
		assert.Equal(t, wantTitles[index], post.Title)
		assert.Equal(t, wantEmails[index], post.Email)
	}
}

func TestHasManyThrough_EachReturnsCorrectModels(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	// Laravel: $country->posts()->each(cb) - chunk(1000) under the hood. Oracle: one statement,
	// `... where "users"."country_id" = ? order by "posts"."id" asc limit 1000 offset 0`
	// (bindings [2]), the callback visiting ids 3-8 with keys 0-5.
	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&country, 2))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	indexes := []int{}
	ids := []uint{}
	post := hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&country, "Posts").Each(&post, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, post.ID)

		return nil
	}))

	assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, indexes)
	assert.Equal(t, []uint{3, 4, 5, 6, 7, 8}, ids)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "each reads the relation in one 1000-row page")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 order by "posts"."id" asc limit 1000 offset 0`, logs[0].Query)
}

func TestHasManyThrough_EachByIdReturnsCorrectModels(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	// Laravel: $country->posts()->eachById(cb, 2) - key-ordered keyset pages of two. Oracle:
	// four statements (the fourth the empty page past id 8), the callback visiting ids 3-8 with
	// the corpus's ((page - 1) * count + key) ordinals 0-5.
	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&country, 2))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	indexes := []int{}
	ids := []uint{}
	post := hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&country, "Posts").EachById(2, &post, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, post.ID)

		return nil
	}))

	assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, indexes)
	assert.Equal(t, []uint{3, 4, 5, 6, 7, 8}, ids)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 4, "eachById(2) issues one statement per page plus the empty terminator page")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" is not null order by "posts"."id" asc limit 2`, logs[0].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" > 4 order by "posts"."id" asc limit 2`, logs[1].Query)
}

func TestHasManyThrough_LazyReturnsCorrectModels(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	// Laravel: $country->posts()->lazy(10)->each(cb) - one 10-row page over the six posts.
	// Oracle: one statement, `... order by "posts"."id" asc limit 10 offset 0` (bindings [2]),
	// ids 3-8.
	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&country, 2))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	indexes := []int{}
	ids := []uint{}
	post := hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&country, "Posts").Lazy(&post, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, post.ID)

		return nil
	}, 10))

	assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, indexes)
	assert.Equal(t, []uint{3, 4, 5, 6, 7, 8}, ids)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "lazy(10) reads the six posts in one statement")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 order by "posts"."id" asc limit 10 offset 0`, logs[0].Query)
}

func TestHasManyThrough_LazyById(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)
	hasManyThroughSeedDataExtended(t, query)

	// Laravel: $country->posts()->lazyById(2)->each(cb) - keyset pages of two, read on demand.
	// Oracle: four statements - page one `"posts"."id" is not null`, then `"posts"."id" > ?`
	// with [4], [6], [8]; ids 3-8.
	country := hasManyThroughCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Find(&country, 2))

	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	indexes := []int{}
	ids := []uint{}
	post := hasManyThroughPost{}
	require.NoError(t, withContext.WithContext(ctx).Related(&country, "Posts").LazyById(2, &post, func(index int) error {
		indexes = append(indexes, index)
		ids = append(ids, post.ID)

		return nil
	}))

	assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, indexes)
	assert.Equal(t, []uint{3, 4, 5, 6, 7, 8}, ids)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 4, "lazyById(2) issues one statement per page plus the empty terminator page")
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" is not null order by "posts"."id" asc limit 2`, logs[0].Query)
	assert.Equal(t, `select "posts".*, "users"."country_id" as "laravel_through_key" from "posts" inner join "users" on "users"."id" = "posts"."user_id" where "users"."country_id" = 2 and "posts"."id" > 8 order by "posts"."id" asc limit 2`, logs[3].Query)
}

func TestHasManyThrough_IntermediateSoftDeletesAreIgnored(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	// Laravel: HasManyThroughSoftDeletesTestUser::first()->delete() — a soft delete of user 1
	// through the class that carries SoftDeletes.
	softUser := hasManyThroughSoftUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyThroughSoftUser{}).First(&softUser))
	_, err := cloneRelationQuery(t, query).Delete(&softUser)
	require.NoError(t, err)

	// The country's relation goes through the plain user class, which does not carry SoftDeletes,
	// so the soft delete is ignored and the posts still load.
	country := hasManyThroughSoftCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyThroughSoftCountry{}).First(&country))
	require.NoError(t, cloneRelationQuery(t, query).Load(&country, "Posts"))

	require.Len(t, country.Posts, 2)
	assert.Equal(t, "A title", country.Posts[0].Title)
}

func TestHasManyThrough_EagerLoadingLoadsRelatedModelsCorrectly(t *testing.T) {
	query := newRelationQuery(t)
	hasManyThroughSchema(t, query)
	hasManyThroughSeedData(t, query)

	// Laravel: HasManyThroughSoftDeletesTestCountry::with('posts')->first(). The port reads through
	// Get: First(&struct) with an eager load leaves its single-row cursor open while retrieved()
	// runs the eager query, which deadlocks on the harness's one-connection pool, while Get drains
	// its cursor before the eager query runs. The observable is the same — the one country row,
	// with the relation attached.
	found := []hasManyThroughSoftCountry{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasManyThroughSoftCountry{}).With("Posts").Get(&found))
	require.Len(t, found, 1)
	assert.Equal(t, "us", found[0].Shortname)

	require.Len(t, found[0].Posts, 2)
	assert.Equal(t, "A title", found[0].Posts[0].Title)
}
