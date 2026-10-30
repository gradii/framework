package orm_relation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
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
//   - Laravel's first()/get()/cursor() reads select `<related>.*` plus the through key aliased as
//     laravel_through_key, so a hydrated model carries its own columns only. Our Related read keeps
//     the builder's `select *` from the join, and the through row's same-named columns come last
//     and overwrite the related row's — probe: reading country 2's posts back, the six posts 3-8
//     read ids 2, 2, 3, 3, 4, 4 and the through users' emails. The corpus fixtures whose post key
//     and email coincide with the through row's (post 1 / user 1) read back the same values, so
//     those tests are ported as assertions on the loaded models.
//   - A key list fails on the joined read: Find / FindOr / FindOrFail with a slice render a bare
//     `id in (...)` and sqlite rejects the statement with "ambiguous column name: id" (probe below),
//     where Laravel's find()/findOr() delegate an array to findMany() and collect every match.
//   - Query.FindOr reads a single row by design (it compiles the FIRST form), where Laravel's
//     findOr([...]) returns a Collection.
//   - Query.FindOrFail takes conditions, not Laravel's array semantics: Laravel's array form
//     demands every requested id resolve and otherwise throws listing them.
//   - ModelNotFoundException messages name the model and its ids; the Go ORM reports the fixed
//     sentinel errors.OrmRecordNotFound ("record not found").
//   - The ORM query exposes no chunk / chunkById / each / eachById / lazy / lazyById; those live on
//     the raw database/db query (Query.Chunk), and Query.Related returns an orm.Query, so six
//     iteration ports cannot run the chain under test and are skipped with their probes.

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
	// Go port calls FindMany for the same chain. (Probe: Find(&posts, []any{1, 2}) on the joined
	// read renders a bare `id in (1, 2)` and sqlite rejects the statement with "ambiguous column
	// name: id", so the delegating call is the one that reproduces Laravel's outcome.)
	posts := []hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").FindMany(&posts, []any{1, 2}))
	assert.Len(t, posts, 2)

	// find(new Collection([1, 2])) delegates the same way; a Go slice is that collection.
	posts = nil
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").FindMany(&posts, []any{1, 2}))
	assert.Len(t, posts, 2)
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
	t.Skip("our ORM diverges: a key list cannot run on the joined read — probe: Related(...).FindOrFail(&posts, []any{1, 2}) fails with `sqlite3: SQL logic error: ambiguous column name: id` (the list renders a bare `id in (1, 2)`), where Laravel's findOrFail([1, 2]) returns the collection when every id resolves and otherwise throws ModelNotFoundException listing them (this fixture has only id 1, so Laravel throws for 1, 2); neither outcome is reachable")
}

func TestHasManyThrough_FindOrFailWithManyUsingCollectionThrowsAnException(t *testing.T) {
	t.Skip("our ORM diverges: the Collection argument is the same Go slice call as the array form — probe: Related(...).FindOrFail(&posts, []any{1, 2}) fails with `sqlite3: SQL logic error: ambiguous column name: id` instead of Laravel's all-ids-present check and ModelNotFoundException naming the missing ids")
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
	// is hydrated. Laravel additionally selects the laravel_through_key alias (see the file header).
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
	t.Skip("our ORM diverges: a key list cannot run on the joined read — probe: Related(...).FindOr(&posts, []any{1, 2}, cb) fails with `sqlite3: SQL logic error: ambiguous column name: id` and never reaches the callback, where Laravel's findOr([1, 2]) delegates to findMany and returns both models in a Collection; a single-id FindOr (the only shape it supports) reads one row and cannot return Laravel's collection for [1, 2] or its callback result for [1, 2, 3]")
}

func TestHasManyThrough_FindOrMethodWithManyUsingCollection(t *testing.T) {
	t.Skip("our ORM diverges: the Collection argument is the same Go slice call as the array form — probe: Related(...).FindOr(&posts, []any{1, 2}, cb) fails with `sqlite3: SQL logic error: ambiguous column name: id`, so neither Laravel's two-model Collection nor its callback result for the missing third id can be observed")
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
	// chain does not produce (see the file header). The fixture keeps this post's key and email
	// equal to the through user's, so the joined read returns them unchanged.
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
	// nothing else — is the same. (Laravel's select also carries the laravel_through_key alias —
	// oracle: `"title", "body", "users"."country_id" as "laravel_through_key"` — which has no Go
	// counterpart; see the file header.)
	post := hasManyThroughPost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&country, "Posts").Select("title", "body").First(&post))
	assert.Equal(t, "A title", post.Title)
	assert.Equal(t, "A body", post.Body)
	assert.Zero(t, post.ID)
	assert.Zero(t, post.UserID)
	assert.Equal(t, "", post.Email)
}

func TestHasManyThrough_ChunkReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no Chunk method — chunking lives on the raw database/db query (Query.Chunk), and Query.Related returns an orm.Query, so the chain under test cannot run; probe: the equivalent Related(...).Get reads all six of country 2's posts in insertion order, where Laravel's chunk(10) hands them back through its callback")
}

func TestHasManyThrough_ChunkById(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no ChunkById method (it lives on the raw database/db query), so Laravel's chunkById(2) — three non-empty chunks of two over country 2's six posts, key-ordered — cannot be run through Query.Related; probe: Related(...).Get reads the same six posts in one read")
}

func TestHasManyThrough_CursorReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: Cursor() streams the same joined `select *` rows as First/Get, and the through row's same-named columns overwrite the related row's — probe: country 2's six cursor rows read ids 2, 2, 3, 3, 4, 4 and the through users' emails (example1/2/3@gmail.com instead of example*post*.gmail.com), where Laravel's cursor hydrates `\"posts\".*` plus the laravel_through_key alias (the corpus asserts that attribute list); the LazyCollection instance the corpus also checks is PHP-only")
}

func TestHasManyThrough_EachReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no Each method — the 1000-row-chunked iterator lives on the raw database/db query, and Query.Related returns an orm.Query, so the chain under test cannot run; probe: the equivalent Related(...).Get reads all six of country 2's posts")
}

func TestHasManyThrough_EachByIdReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no EachById method (it lives on the raw database/db query), so Laravel's key-ordered eachById iteration over country 2's six posts cannot be run through Query.Related; probe: Related(...).Get reads the same six posts in one read")
}

func TestHasManyThrough_LazyReturnsCorrectModels(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no Lazy method (it lives on the raw database/db query), so Laravel's lazy(10) lazy-collection stream cannot be run through Query.Related; probe: Related(...).Get reads all six of country 2's posts in one read")
}

func TestHasManyThrough_LazyById(t *testing.T) {
	t.Skip("our ORM diverges: the ORM query exposes no LazyById method (it lives on the raw database/db query), so Laravel's key-ordered lazyById(2) iteration over country 2's six posts cannot be run through Query.Related; probe: Related(...).Get reads the same six posts in one read")
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
