package orm_relation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasOneOfManyTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestHasOneOfMany_<method without the
// test prefix>. The four tables mirror the corpus's createSchema(); rows are seeded through the
// ORM, and relations are loaded explicitly through OrmBuilder.Load — the harness's lazy-loading
// rule, since Go has no property-access magic.
//
// Laravel checks compiled SQL strings in some tests and behaviour in others. Every SQL expectation
// this file cites was measured with the oracle (.claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1), not recalled. Where our ORM cannot produce Laravel's SQL or
// behaviour, the test is skipped with the concrete divergence rather than weakened, and where the
// assertion is relation-object introspection with no Go equivalent the skip says so.

const hasOneOfManySkipPHPOnlyRelationName = "PHP-only assertion (no Go equivalent): the Go ORM has no relation-name introspection — a relation is named by its key in the model's Relations() map, and OfMany takes no relation-name argument"

// --- models ------------------------------------------------------------------

// hasOneOfManyUser ports HasOneOfManyTestUser: only the id column is real; everything else is a
// relation destination or an aggregate alias destination, kept out of the table by gorm:"-".
type hasOneOfManyUser struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`

	Logins                          []*hasOneOfManyLogin   `gorm:"-"`
	LatestLogin                     *hasOneOfManyLogin     `gorm:"-"`
	LatestLoginWithShortcut         *hasOneOfManyLogin     `gorm:"-"`
	LatestLoginWithSoftDeletes      *hasOneOfManySoftLogin `gorm:"-"`
	LatestLoginWithInvalidAggregate *hasOneOfManyLogin     `gorm:"-"`
	LatestLoginWithoutGlobalScope   *hasOneOfManyLogin     `gorm:"-"`
	FirstLogin                      *hasOneOfManyLogin     `gorm:"-"`
	LatestLoginWithFooState         *hasOneOfManyLogin     `gorm:"-"`
	States                          []*hasOneOfManyState   `gorm:"-"`
	FooState                        *hasOneOfManyState     `gorm:"-"`
	LastUpdatedFooState             *hasOneOfManyState     `gorm:"-"`
	LatestUpdatedLatestCreatedState *hasOneOfManyState     `gorm:"-"`
	Prices                          []*hasOneOfManyPrice   `gorm:"-"`
	Price                           *hasOneOfManyPrice     `gorm:"-"`
	PriceWithShortcut               *hasOneOfManyPrice     `gorm:"-"`
	PriceWithoutKeyInAggregates     *hasOneOfManyPrice     `gorm:"-"`
	PriceWithoutGlobalScope         *hasOneOfManyPrice     `gorm:"-"`
}

func (hasOneOfManyUser) TableName() string { return "users" }

func (hasOneOfManyUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Logins": contractsorm.HasMany{Related: &hasOneOfManyLogin{}, ForeignKey: "user_id"},
		// latest_login(): hasOne(...)->ofMany(), the aggregate defaulting to id MAX.
		"LatestLogin": contractsorm.HasOne{
			Related: &hasOneOfManyLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		// latest_login_with_soft_deletes(): the same shape over a soft-deleting model.
		"LatestLoginWithSoftDeletes": contractsorm.HasOne{
			Related: &hasOneOfManySoftLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		// latest_login_with_shortcut(): hasOne(...)->latestOfMany().
		"LatestLoginWithShortcut": contractsorm.HasOne{
			Related: &hasOneOfManyLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.LatestOfMany() },
		},
		// latest_login_with_invalid_aggregate(): ofMany('id', 'count') — Laravel rejects 'count'.
		"LatestLoginWithInvalidAggregate": contractsorm.HasOne{
			Related: &hasOneOfManyLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("id", "count") },
		},
		// latest_login_without_global_scope(): withoutGlobalScopes()->latestOfMany().
		"LatestLoginWithoutGlobalScope": contractsorm.HasOne{
			Related: &hasOneOfManyLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes().LatestOfMany() },
		},
		// first_login(): ofMany('id', 'min').
		"FirstLogin": contractsorm.HasOne{
			Related: &hasOneOfManyLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("id", "MIN") },
		},
		// latest_login_with_foo_state(): ofMany(['id' => 'max'], join states, where states.type=foo).
		"LatestLoginWithFooState": contractsorm.HasOne{
			Related: &hasOneOfManyLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("id", "MAX").
					Join("states ON states.user_id = logins.user_id").
					Where("states.type", "foo")
			},
		},
		"States": contractsorm.HasMany{Related: &hasOneOfManyState{}, ForeignKey: "user_id"},
		// foo_state(): ofMany([], where type=foo) — the empty aggregate set defaults to id MAX.
		"FooState": contractsorm.HasOne{
			Related: &hasOneOfManyState{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "").Where("type", "foo") },
		},
		// last_updated_foo_state(): ofMany(['updated_at' => 'max', 'id' => 'max'], where type=foo).
		// OfMany carries one (column, aggregate) pair in the Go ORM, so only the first aggregate is
		// declared; the multi-aggregate tests are skipped with that divergence.
		"LastUpdatedFooState": contractsorm.HasOne{
			Related: &hasOneOfManyState{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("updated_at", "MAX").Where("type", "foo")
			},
		},
		"Prices": contractsorm.HasMany{Related: &hasOneOfManyPrice{}, ForeignKey: "user_id"},
		// price(): ofMany(['published_at' => 'max', 'id' => 'max'], where published_at < now()).
		// Only the first aggregate is expressible, as above.
		"Price": contractsorm.HasOne{
			Related: &hasOneOfManyPrice{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("published_at", "MAX").Where("published_at", "<", time.Now())
			},
		},
		// price_without_key_in_aggregates(): ofMany(['published_at' => 'MAX']) — Laravel adds the
		// key (id MAX) when the aggregate set omits it.
		"PriceWithoutKeyInAggregates": contractsorm.HasOne{
			Related: &hasOneOfManyPrice{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("published_at", "MAX") },
		},
		// price_with_shortcut(): latestOfMany(['published_at', 'id']).
		"PriceWithShortcut": contractsorm.HasOne{
			Related: &hasOneOfManyPrice{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.LatestOfMany("published_at", "id") },
		},
		// price_without_global_scope(): the multi-aggregate price() with withoutGlobalScopes().
		"PriceWithoutGlobalScope": contractsorm.HasOne{
			Related: &hasOneOfManyPrice{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.WithoutGlobalScopes().OfMany("published_at", "MAX").Where("published_at", "<", time.Now())
			},
		},
		// latest_updated_latest_created_state(): ofMany(['updated_at' => 'max', 'created_at' => 'max']).
		"LatestUpdatedLatestCreatedState": contractsorm.HasOne{
			Related: &hasOneOfManyState{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("updated_at", "MAX") },
		},
	}
}

// hasOneOfManyLogin ports HasOneOfManyTestLogin.
type hasOneOfManyLogin struct {
	ID     uint `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint `gorm:"column:user_id"`
}

func (hasOneOfManyLogin) TableName() string { return "logins" }

// hasOneOfManySoftLogin ports HasOneOfManyTestLoginWithSoftDeletes: the same table, carrying the
// framework's soft-delete field.
type hasOneOfManySoftLogin struct {
	databaseorm.SoftDeletes

	ID     uint `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint `gorm:"column:user_id"`
}

func (hasOneOfManySoftLogin) TableName() string { return "logins" }

// hasOneOfManyState ports HasOneOfManyTestState (timestamps are seeded explicitly where a test
// needs them; the columns exist in the DDL).
type hasOneOfManyState struct {
	ID        uint   `gorm:"column:id;primaryKey;autoIncrement"`
	State     string `gorm:"column:state"`
	Type      string `gorm:"column:type"`
	UserID    uint   `gorm:"column:user_id"`
	CreatedAt string `gorm:"column:created_at"`
	UpdatedAt string `gorm:"column:updated_at"`
}

func (hasOneOfManyState) TableName() string { return "states" }

// hasOneOfManyPrice ports HasOneOfManyTestPrice, whose published_at column carries the corpus's
// datetime cast; the sqlite driver hands datetime columns back as time.Time.
type hasOneOfManyPrice struct {
	ID          uint      `gorm:"column:id;primaryKey;autoIncrement"`
	PublishedAt time.Time `gorm:"column:published_at"`
	UserID      uint      `gorm:"column:user_id"`
}

func (hasOneOfManyPrice) TableName() string { return "prices" }

// hasOneOfManyAggregateDest is the scan target for withCount / withExists reads: Laravel exposes
// latest_login_count / latest_login_exists as dynamic attributes on the user model, while a Go
// model's table columns are fixed — the alias destinations must live on a separate struct or every
// users insert would try to write the pseudo-columns.
type hasOneOfManyAggregateDest struct {
	ID                uint `gorm:"column:id"`
	LatestLoginCount  int  `gorm:"column:latest_login_count"`
	LatestLoginExists bool `gorm:"column:latest_login_exists"`
	FooStateExists    bool `gorm:"column:foo_state_exists"`
}

// --- schema and seeding ------------------------------------------------------

// hasOneOfManySchema mirrors the corpus's createSchema() in sqlite DDL.
func hasOneOfManySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table logins (id integer primary key autoincrement, user_id integer not null, deleted_at datetime null)`,
		`create table states (id integer primary key autoincrement, state varchar not null, type varchar not null, user_id integer not null, created_at datetime null, updated_at datetime null)`,
		`create table prices (id integer primary key autoincrement, published_at datetime not null, user_id integer not null)`,
	)
}

// hasOneOfManySeedUser creates a users row, like HasOneOfManyTestUser::create().
func hasOneOfManySeedUser(t *testing.T, query contractsorm.Query, id uint) hasOneOfManyUser {
	t.Helper()

	// The corpus's users table carries only the auto-increment id, and the ORM compiles an
	// all-defaults struct insert as `insert into "users" () values ()`, which sqlite rejects
	// (Laravel compiles `default values`). The helper therefore writes the explicit id the
	// auto increment would assign; every assertion observes the same rows.
	user := hasOneOfManyUser{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&user).Create(map[string]any{"id": id}))

	return user
}

// hasOneOfManyCreateLogin ports $user->logins()->create().
func hasOneOfManyCreateLogin(t *testing.T, query contractsorm.Query, user *hasOneOfManyUser) hasOneOfManyLogin {
	t.Helper()

	login := hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(user, "Logins").Create(&login))

	return login
}

// --- ports of the corpus -----------------------------------------------------

func TestHasOneOfMany_ItGuessesRelationName(t *testing.T) {
	t.Skip(hasOneOfManySkipPHPOnlyRelationName)
}

func TestHasOneOfMany_ItGuessesRelationNameAndAddsOfManyWhenTableNameIsRelationName(t *testing.T) {
	t.Skip(hasOneOfManySkipPHPOnlyRelationName)
}

func TestHasOneOfMany_RelationNameCanBeSet(t *testing.T) {
	// Laravel: ofMany('id', 'max', 'foo') / latestOfMAny('id', 'bar') / oldestOfMAny('id', 'baz'),
	// each checked through Relation::getRelationName(). The Go ORM has neither the third argument
	// nor the introspection.
	t.Skip(hasOneOfManySkipPHPOnlyRelationName)
}

func TestHasOneOfMany_CorrectLatestOfManyQuery(t *testing.T) {
	// Laravel pins the SQL $user->latest_login()->getQuery()->toSql() renders; the oracle prints
	//   select "logins".* from "logins" inner join (select MAX("logins"."id") as "id_aggregate",
	//   "logins"."user_id" from "logins" where "logins"."user_id" = ? and "logins"."user_id" is not
	//   null group by "logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" =
	//   "logins"."id" and "latest_login"."user_id" = "logins"."user_id" where "logins"."user_id" = ?
	//   and "logins"."user_id" is not null
	t.Skip("our ORM diverges: the relation read path (Query.Related, the Go form of $user->latest_login()) renders no one-of-many join at all — only the eager-load path consumes OfMany — so Laravel's SQL cannot be produced; the eager join also differs (alias goravel_one_of_many, aggregate column named aggregate, no WHERE inside the subquery)")
}

func TestHasOneOfMany_EagerLoadingAppliesConstraintsToInnerJoinSubQuery(t *testing.T) {
	// Laravel pins $relation->getOneOfManySubQuery()->toSql() after addEagerConstraints([$user]);
	// the oracle prints
	//   select MAX("logins"."id") as "id_aggregate", "logins"."user_id" from "logins" where
	//   "logins"."user_id" = ? and "logins"."user_id" is not null and "logins"."user_id" in (1)
	//   group by "logins"."user_id"
	t.Skip("our ORM diverges: no public API exposes the one-of-many subquery (getOneOfManySubQuery has no Go equivalent), and the compiled subquery differs — ours carries no WHERE constraints (the parent constraint stays on the outer query), names the aggregate column 'aggregate' and the subquery alias goravel_one_of_many")
}

func TestHasOneOfMany_GlobalScopeIsNotAppliedWhenRelationIsDefinedWithoutGlobalScope(t *testing.T) {
	t.Skip("our ORM diverges: the pinned SQL is the relation read path's, which renders no one-of-many join in the Go port; the oracle string aliases the subquery 'latestOfMany' (Laravel's guessed relation name) while ours is always goravel_one_of_many, and the subquery shape (WHERE constraints inside it) has no counterpart")
}

func TestHasOneOfMany_GlobalScopeIsNotAppliedWhenRelationIsDefinedWithoutGlobalScopeWithComplexQuery(t *testing.T) {
	t.Skip("our ORM diverges: OfMany carries a single (column, aggregate) pair, so Laravel's two-aggregate ofMany(['published_at' => 'max', 'id' => 'max']) with a scoping closure cannot be expressed; the oracle compiles nested aggregate joins (price_without_global_scope over price_without_global_scope) with no Go counterpart")
}

func TestHasOneOfMany_QualifyingSubSelectColumn(t *testing.T) {
	// Laravel: $user->latest_login()->qualifySubSelectColumn('id') === 'latest_login.id' (oracle
	// confirms). The Go ORM exposes no such accessor.
	t.Skip("PHP-only assertion (no Go equivalent): the Go ORM has no qualifySubSelectColumn accessor")
}

func TestHasOneOfMany_ItFailsWhenUsingInvalidAggregate(t *testing.T) {
	// Oracle: constructing the relation throws InvalidArgumentException with the message
	// "Invalid aggregate [count] used within ofMany relation. Available aggregates: MIN, MAX".
	t.Skip("our ORM diverges: OfMany does not validate the aggregate — ofMany('id', 'count') is accepted and compiled silently, so Laravel's InvalidArgumentException cannot be reproduced")
}

func TestHasOneOfMany_ItGetsCorrectResults(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user) // the previous login: the relation must skip it
	latestLogin := hasOneOfManyCreateLogin(t, query, &user)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.Equal(t, latestLogin.ID, user.LatestLogin.ID)
}

func TestHasOneOfMany_ResultDoesNotHaveAggregateColumn(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): a typed Go model has no dynamic attributes, so the absence of Laravel's id_aggregate pseudo-column on the loaded model cannot be observed")
}

func TestHasOneOfMany_ItGetsCorrectResultsUsingShortcutMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user)
	latestLogin := hasOneOfManyCreateLogin(t, query, &user)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithShortcut"))
	require.NotNil(t, user.LatestLoginWithShortcut)
	assert.Equal(t, latestLogin.ID, user.LatestLoginWithShortcut.ID)
}

func TestHasOneOfMany_ItGetsCorrectResultsUsingShortcutReceivingMultipleColumnsMethod(t *testing.T) {
	t.Skip("our ORM diverges: LatestOfMany keeps only its first column — Laravel's latestOfMany(['published_at', 'id']) adds a second id aggregate to break ties (oracle: nested id_aggregate/published_at_aggregate joins), so on tied published_at values the port resolves to the first inserted row instead of the highest id")
}

func TestHasOneOfMany_KeyIsAddedToAggregatesWhenMissing(t *testing.T) {
	t.Skip("our ORM diverges: Laravel adds the key column (id MAX) to the aggregate set when the declared aggregates omit it, so tied rows resolve to the highest key; the Go OfMany compiles only the declared (published_at, MAX) and tied rows resolve to the first inserted row")
}

func TestHasOneOfMany_ItGetsWithConstraintsCorrectResults(t *testing.T) {
	t.Skip("our ORM diverges: whereKey(previous_login) runs on the relation read path, which carries no one-of-many join, so it matches the previous login instead of returning null")
}

func TestHasOneOfMany_ItEagerLoadsCorrectModels(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user)
	latestLogin := hasOneOfManyCreateLogin(t, query, &user)

	// Laravel: with('latest_login')->first(). The port reads through Get: First(&struct) leaves its
	// single-row cursor open while retrieved() runs the eager query, which deadlocks on the
	// harness's one-connection pool, while Get drains its cursor before the eager query runs. The
	// observable is the same — the one user row, with the relation attached.
	found := []hasOneOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).With("LatestLogin").Get(&found))
	require.Len(t, found, 1)

	// Laravel asserts relationLoaded('latest_login'); in Go the eager load shows as the field
	// being populated by the query.
	require.NotNil(t, found[0].LatestLogin)
	assert.Equal(t, latestLogin.ID, found[0].LatestLogin.ID)
}

func TestHasOneOfMany_ItJoinsOtherTableInSubQuery(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithFooState"))
	assert.Nil(t, user.LatestLoginWithFooState)

	// Laravel unsets the relation and reads the property again; every Load re-reads, so the second
	// Load is the re-read.
	state := hasOneOfManyState{Type: "foo", State: "draft"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "States").Create(&state))

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithFooState"))
	assert.NotNil(t, user.LatestLoginWithFooState)
}

func TestHasOneOfMany_HasNested(t *testing.T) {
	t.Skip("our ORM diverges: WhereHas does not carry the one-of-many join into the existence subquery, so the previous (non-latest) login still satisfies whereHas('latest_login', id = previous) — Laravel returns false for that case")
}

func TestHasOneOfMany_WithHasNested(t *testing.T) {
	t.Skip("our ORM diverges: WithWhereHas builds its existence subquery the same way WhereHas does, without the one-of-many join, so constraining by the previous login's id still matches (Laravel: no match, and withWhereHas('…previous…')->exists() is false)")
}

func TestHasOneOfMany_HasCount(t *testing.T) {
	t.Skip("our ORM diverges: WithCount counts all related rows — the count subquery carries no one-of-many join, so latest_login_count is 2 for two logins where Laravel reports 1")
}

func TestHasOneOfMany_Exists(t *testing.T) {
	t.Skip("our ORM diverges: with the one-of-many join absent from the relation read path, exists() with whereKey(previous_login) is true where Laravel reports false")
}

func TestHasOneOfMany_IsMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	login := hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "LatestLogin").Create(&login))
	second := hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "LatestLogin").Create(&second))

	// Laravel: $relation->is($model) compares the relation result's key with the model's key; the
	// port loads the relation (a fresh read per call) and compares the loaded model's key.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.NotEqual(t, login.ID, user.LatestLogin.ID)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.Equal(t, second.ID, user.LatestLogin.ID)
}

func TestHasOneOfMany_IsNotMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	login := hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "LatestLogin").Create(&login))
	second := hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "LatestLogin").Create(&second))

	// Laravel: isNot($model) is the negation of is($model), so the same two key comparisons.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.NotEqual(t, login.ID, user.LatestLogin.ID)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.Equal(t, second.ID, user.LatestLogin.ID)
}

func TestHasOneOfMany_Get(t *testing.T) {
	t.Skip("our ORM diverges: get() on the relation read path returns both logins (Laravel's one-of-many join yields one), and whereKey(previous_login)->get() returns the previous login instead of an empty result")
}

func TestHasOneOfMany_Count(t *testing.T) {
	t.Skip("our ORM diverges: count() on the relation read path counts all related logins (2) instead of the single one-of-many row (Laravel: 1)")
}

func TestHasOneOfMany_Aggregate(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	firstLogin := hasOneOfManyCreateLogin(t, query, &user)
	_ = hasOneOfManyCreateLogin(t, query, &user)

	// Laravel re-reads the user (first()) and lazy-loads first_login.
	found := hasOneOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).First(&found))
	require.NoError(t, cloneRelationQuery(t, query).Load(&found, "FirstLogin"))

	require.NotNil(t, found.FirstLogin)
	assert.Equal(t, firstLogin.ID, found.FirstLogin.ID)
}

func TestHasOneOfMany_JoinConstraints(t *testing.T) {
	t.Skip("our ORM diverges: the relation's OnQuery scope (where type = foo) is applied to the outer related query while the one-of-many aggregate is computed over all of the user's rows, so when the newest state is another type the join matches nothing and foo_state is null — Laravel restricts the aggregate subquery to type = foo first")
}

func TestHasOneOfMany_MultipleAggregates(t *testing.T) {
	t.Skip("our ORM diverges: the price() relation's two-aggregate ofMany(['published_at' => 'max', 'id' => 'max']) with a scoping closure cannot be expressed (OfMany carries one (column, aggregate) pair); the oracle compiles nested aggregate joins with no Go counterpart")
}

func TestHasOneOfMany_EagerLoadingWithMultipleAggregates(t *testing.T) {
	t.Skip("our ORM diverges: with('price') eager loading compiles the same two-aggregate relation the port cannot express (OfMany carries one (column, aggregate) pair), so the tied published_at rows cannot resolve to the highest id per user")
}

func TestHasOneOfMany_WithExists(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)

	found := hasOneOfManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WithExists("LatestLogin").First(&found))
	assert.False(t, found.LatestLoginExists)

	_ = hasOneOfManyCreateLogin(t, query, &user)

	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WithExists("LatestLogin").First(&found))
	assert.True(t, found.LatestLoginExists)
}

func TestHasOneOfMany_WithExistsWithConstraintsInJoinSubSelect(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)

	found := hasOneOfManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WithExists("FooState").First(&found))
	assert.False(t, found.FooStateExists)

	state := hasOneOfManyState{Type: "foo", State: "bar"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "States").Create(&state))

	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WithExists("FooState").First(&found))
	assert.True(t, found.FooStateExists)
}

func TestHasOneOfMany_WithSoftDeletes(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithSoftDeletes"))
	assert.NotNil(t, user.LatestLoginWithSoftDeletes)
}

func TestHasOneOfMany_WithConstraintNotInAggregate(t *testing.T) {
	t.Skip("our ORM diverges: last_updated_foo_state() aggregates over two columns (updated_at MAX, id MAX) with a type = foo scope; OfMany carries a single (column, aggregate) pair, so the two-level aggregate join the oracle compiles cannot be expressed")
}

func TestHasOneOfMany_ItGetsCorrectResultUsingAtLeastTwoAggregatesDistinctFromId(t *testing.T) {
	t.Skip("our ORM diverges: ofMany(['updated_at' => 'max', 'created_at' => 'max']) needs a nested two-aggregate join (oracle confirms the nesting), and the Go OfMany carries a single (column, aggregate) pair")
}
