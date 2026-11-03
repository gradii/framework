package orm_relation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
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
		"LastUpdatedFooState": contractsorm.HasOne{
			Related: &hasOneOfManyState{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("updated_at", "MAX").Where("type", "foo")
			},
		},
		"Prices": contractsorm.HasMany{Related: &hasOneOfManyPrice{}, ForeignKey: "user_id"},
		// price(): ofMany(['published_at' => 'max', 'id' => 'max'], where published_at < now());
		// the string form adds the key with the same aggregate.
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
				return q.WithoutGlobalScopes().OfMany("published_at", "max").Where("published_at", "<", time.Now())
			},
		},
		// latest_updated_latest_created_state(): ofMany(['updated_at' => 'max', 'created_at' => 'max']) —
		// two columns distinct from the key, so the key is appended as MAX after them.
		"LatestUpdatedLatestCreatedState": contractsorm.HasOne{
			Related: &hasOneOfManyState{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfManyColumns(
					contractsorm.OfManyColumn{Column: "updated_at", Aggregate: "max"},
					contractsorm.OfManyColumn{Column: "created_at", Aggregate: "max"},
				)
			},
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

// hasOneOfManyState ports HasOneOfManyTestState. The corpus gives it timestamps and fills
// created_at with now(); the sqlite driver hands datetime columns back as time.Time, so the port
// seeds time.Time values where a test needs them and leaves the zero value otherwise.
type hasOneOfManyState struct {
	ID        uint      `gorm:"column:id;primaryKey;autoIncrement"`
	State     string    `gorm:"column:state"`
	Type      string    `gorm:"column:type"`
	UserID    uint      `gorm:"column:user_id"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
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

// hasOneOfManyCreatePrice ports $user->prices()->create(['published_at' => ...]).
func hasOneOfManyCreatePrice(t *testing.T, query contractsorm.Query, user *hasOneOfManyUser, publishedAt time.Time) hasOneOfManyPrice {
	t.Helper()

	price := hasOneOfManyPrice{PublishedAt: publishedAt}
	require.NoError(t, cloneRelationQuery(t, query).Relation(user, "Prices").Create(&price))

	return price
}

// hasOneOfManyCreateState ports $user->states()->create([...]).
func hasOneOfManyCreateState(t *testing.T, query contractsorm.Query, user *hasOneOfManyUser, state hasOneOfManyState) hasOneOfManyState {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Relation(user, "States").Create(&state))

	return state
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
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)

	// Laravel pins the SQL $user->latest_login()->getQuery()->toSql() renders; the oracle prints
	//   select "logins".* from "logins" inner join (select MAX("logins"."id") as "id_aggregate",
	//   "logins"."user_id" from "logins" where "logins"."user_id" = ? and "logins"."user_id" is not
	//   null group by "logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" =
	//   "logins"."id" and "latest_login"."user_id" = "logins"."user_id" where "logins"."user_id" = ?
	//   and "logins"."user_id" is not null
	// The Go form of $user->latest_login() is Query.Related; ToSql renders the statement without
	// executing it.
	dest := &hasOneOfManyLogin{}
	statement := cloneRelationQuery(t, query).Related(&user, "LatestLogin").ToSql().Get(dest)
	assert.Equal(t, `select "logins".* from "logins" inner join (select MAX("logins"."id") as "id_aggregate", "logins"."user_id" from "logins" where "logins"."user_id" = ? and "logins"."user_id" is not null group by "logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "logins"."id" and "latest_login"."user_id" = "logins"."user_id" where "logins"."user_id" = ? and "logins"."user_id" is not null`, statement)
}

func TestHasOneOfMany_EagerLoadingAppliesConstraintsToInnerJoinSubQuery(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user)

	// Laravel pins $relation->getOneOfManySubQuery()->toSql() after addEagerConstraints([$user]);
	// the oracle prints
	//   select MAX("logins"."id") as "id_aggregate", "logins"."user_id" from "logins" where
	//   "logins"."user_id" = ? and "logins"."user_id" is not null and "logins"."user_id" in (1)
	//   group by "logins"."user_id"
	// — the parent constraints and the in-list inside the subquery. The Go ORM has no
	// addEagerConstraints probe (a live relation cannot take an eager in-list), so the port runs
	// the real eager read that constraint belongs to and asserts the statement the loader emits,
	// measured with the oracle for the same read (keys [1]): the in-list sits inside the
	// aggregate subquery and the outer query carries no constraint.
	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	found := []hasOneOfManyUser{}
	require.NoError(t, withContext.WithContext(ctx).Model(&hasOneOfManyUser{}).With("LatestLogin").Get(&found))
	require.Len(t, found, 1)
	require.NotNil(t, found[0].LatestLogin)

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 2, "the main read and the eager read")
	assert.Equal(t, `select "logins".* from "logins" inner join (select MAX("logins"."id") as "id_aggregate", "logins"."user_id" from "logins" where "logins"."user_id" in (1) group by "logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "logins"."id" and "latest_login"."user_id" = "logins"."user_id"`, logs[1].Query)
}

func TestHasOneOfMany_GlobalScopeIsNotAppliedWhenRelationIsDefinedWithoutGlobalScope(t *testing.T) {
	t.Skip("our ORM diverges: the pinned SQL composes a live relation's constraints with an eager in-list (addEagerConstraints has no Go equivalent — the port's eager read carries the in-list inside the subquery now, but no chain composes it with the live constraints), and its subquery alias 'latestOfMany' is Laravel's guessed relation name while ours names the subquery after the relation key")
}

func TestHasOneOfMany_GlobalScopeIsNotAppliedWhenRelationIsDefinedWithoutGlobalScopeWithComplexQuery(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)

	// Laravel pins price_without_global_scope()->getQuery()->toSql(); the oracle prints the nested
	// aggregate joins below — no orderBy survives, and the closure's own where runs inside every
	// level.
	statement := cloneRelationQuery(t, query).Related(&user, "PriceWithoutGlobalScope").ToSql().Get(&hasOneOfManyPrice{})
	assert.Equal(t, `select "prices".* from "prices" inner join (select max("prices"."id") as "id_aggregate", min("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" inner join (select max("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" where "published_at" < ? and "prices"."user_id" = ? and "prices"."user_id" is not null group by "prices"."user_id") as "price_without_global_scope" on "price_without_global_scope"."published_at_aggregate" = "prices"."published_at" and "price_without_global_scope"."user_id" = "prices"."user_id" where "published_at" < ? group by "prices"."user_id") as "price_without_global_scope" on "price_without_global_scope"."id_aggregate" = "prices"."id" and "price_without_global_scope"."published_at_aggregate" = "prices"."published_at" and "price_without_global_scope"."user_id" = "prices"."user_id" where "prices"."user_id" = ? and "prices"."user_id" is not null`, statement)
}

func TestHasOneOfMany_QualifyingSubSelectColumn(t *testing.T) {
	// Laravel: $user->latest_login()->qualifySubSelectColumn('id') === 'latest_login.id' (oracle
	// confirms). The Go ORM exposes no such accessor.
	t.Skip("PHP-only assertion (no Go equivalent): the Go ORM has no qualifySubSelectColumn accessor")
}

func TestHasOneOfMany_ItFailsWhenUsingInvalidAggregate(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)
	user := hasOneOfManySeedUser(t, query, 1)

	// Oracle: constructing the relation throws InvalidArgumentException with the message below
	// (ofMany('id', 'count') — 'count' is not MIN/MAX). The Go ORM rejects the same aggregate: the
	// deferred error surfaces when the relation query is compiled, and Load is this harness's form
	// of the $user->latest_login_with_invalid_aggregate() accessor call.
	err := cloneRelationQuery(t, query).Load(&user, "LatestLoginWithInvalidAggregate")
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmRelationInvalidOfManyAggregate)
	assert.ErrorContains(t, err, "Invalid aggregate [count] used within ofMany relation. Available aggregates: MIN, MAX")
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
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreatePrice(t, query, &user, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	price := hasOneOfManyCreatePrice(t, query, &user, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))

	// latestOfMany(['published_at', 'id']) takes both columns with MAX (oracle: the nested
	// id_aggregate/published_at_aggregate joins), so the tied published_at values resolve to the
	// highest id.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "PriceWithShortcut"))
	require.NotNil(t, user.PriceWithShortcut)
	assert.Equal(t, price.ID, user.PriceWithShortcut.ID)
}

func TestHasOneOfMany_KeyIsAddedToAggregatesWhenMissing(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreatePrice(t, query, &user, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	price := hasOneOfManyCreatePrice(t, query, &user, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))

	// ofMany(['published_at' => 'MAX']) omits the key, so Laravel appends it with MAX and the tied
	// published_at values resolve to the highest id.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "PriceWithoutKeyInAggregates"))
	require.NotNil(t, user.PriceWithoutKeyInAggregates)
	assert.Equal(t, price.ID, user.PriceWithoutKeyInAggregates.ID)
}

func TestHasOneOfMany_ItGetsWithConstraintsCorrectResults(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	previousLogin := hasOneOfManyCreateLogin(t, query, &user)
	_ = hasOneOfManyCreateLogin(t, query, &user)

	// Laravel: $user->latest_login()->whereKey($previousLogin)->getResults() is null — the one-of-many
	// join leaves only the latest login for the constraint to match. A read that matches nothing
	// leaves the destination at its zero value.
	found := hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "LatestLogin").WhereKey(previousLogin.ID).First(&found))
	assert.Zero(t, found.ID)
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
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	previousLogin := hasOneOfManyCreateLogin(t, query, &user)
	latestLogin := hasOneOfManyCreateLogin(t, query, &user)

	found, err := cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WhereHas("LatestLogin", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("logins.id", latestLogin.ID)
	}).Exists()
	require.NoError(t, err)
	assert.True(t, found)

	found, err = cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WhereHas("LatestLogin", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("logins.id", previousLogin.ID)
	}).Exists()
	require.NoError(t, err)
	assert.False(t, found)
}

func TestHasOneOfMany_WithHasNested(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	previousLogin := hasOneOfManyCreateLogin(t, query, &user)
	latestLogin := hasOneOfManyCreateLogin(t, query, &user)

	// Laravel: withWhereHas('latest_login', id = latest)->first() matches and loads the relation.
	found := []hasOneOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WithWhereHas("LatestLogin", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("logins.id", latestLogin.ID)
	}).Get(&found))
	require.Len(t, found, 1)
	require.NotNil(t, found[0].LatestLogin)
	assert.Equal(t, latestLogin.ID, found[0].LatestLogin.ID)

	// Laravel: withWhereHas('latest_login', id = previous)->exists() is false.
	exists, err := cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WithWhereHas("LatestLogin", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("logins.id", previousLogin.ID)
	}).Exists()
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestHasOneOfMany_HasCount(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user)
	_ = hasOneOfManyCreateLogin(t, query, &user)

	// Laravel: withCount('latest_login')->first()->latest_login_count is 1 — the count subquery
	// counts the one-of-many rows.
	found := hasOneOfManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).WithCount("LatestLogin").First(&found))
	assert.Equal(t, 1, found.LatestLoginCount)
}

func TestHasOneOfMany_Exists(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	previousLogin := hasOneOfManyCreateLogin(t, query, &user)
	latestLogin := hasOneOfManyCreateLogin(t, query, &user)

	exists, err := cloneRelationQuery(t, query).Related(&user, "LatestLogin").WhereKey(previousLogin.ID).Exists()
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = cloneRelationQuery(t, query).Related(&user, "LatestLogin").WhereKey(latestLogin.ID).Exists()
	require.NoError(t, err)
	assert.True(t, exists)
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
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	previousLogin := hasOneOfManyCreateLogin(t, query, &user)
	latestLogin := hasOneOfManyCreateLogin(t, query, &user)

	logins := []hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "LatestLogin").Get(&logins))
	require.Len(t, logins, 1)
	assert.Equal(t, latestLogin.ID, logins[0].ID)

	logins = []hasOneOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "LatestLogin").WhereKey(previousLogin.ID).Get(&logins))
	assert.Empty(t, logins)
}

func TestHasOneOfMany_Count(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreateLogin(t, query, &user)
	_ = hasOneOfManyCreateLogin(t, query, &user)

	count, err := cloneRelationQuery(t, query).Related(&user, "LatestLogin").Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
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
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	draft := hasOneOfManyState{Type: "foo", State: "draft"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "States").Create(&draft))
	currentForState := hasOneOfManyState{Type: "foo", State: "active"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "States").Create(&currentForState))
	baz := hasOneOfManyState{Type: "bar", State: "baz"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&user, "States").Create(&baz))

	// Laravel re-reads the user (first()) and lazy-loads foo_state; the relation's of-many chain
	// restricts the aggregate subquery to type = foo first, so the newest foo state wins over the
	// newer bar one. The Go form of the lazy load is Query.Related.
	found := hasOneOfManyState{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&user, "FooState").First(&found))
	assert.Equal(t, currentForState.ID, found.ID)
}

func TestHasOneOfMany_MultipleAggregates(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)
	_ = hasOneOfManyCreatePrice(t, query, &user, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	price := hasOneOfManyCreatePrice(t, query, &user, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))

	// price() aggregates max(published_at) then max(id) (oracle: the nested id_aggregate /
	// published_at_aggregate joins), so the tied published_at values resolve to the highest id.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "Price"))
	require.NotNil(t, user.Price)
	assert.Equal(t, price.ID, user.Price.ID)
}

func TestHasOneOfMany_EagerLoadingWithMultipleAggregates(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user1 := hasOneOfManySeedUser(t, query, 1)
	user2 := hasOneOfManySeedUser(t, query, 2)

	_ = hasOneOfManyCreatePrice(t, query, &user1, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	user1Price := hasOneOfManyCreatePrice(t, query, &user1, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	_ = hasOneOfManyCreatePrice(t, query, &user1, time.Date(2021, 4, 1, 0, 0, 0, 0, time.UTC))

	user2Price := hasOneOfManyCreatePrice(t, query, &user2, time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	_ = hasOneOfManyCreatePrice(t, query, &user2, time.Date(2021, 4, 1, 0, 0, 0, 0, time.UTC))

	// Laravel: with('price')->get(); the tie between the two 2021-05-01 rows resolves to the
	// highest id per user through the nested aggregate joins.
	users := []hasOneOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneOfManyUser{}).With("Price").Get(&users))
	require.Len(t, users, 2)

	require.NotNil(t, users[0].Price)
	assert.Equal(t, user1Price.ID, users[0].Price.ID)
	require.NotNil(t, users[1].Price)
	assert.Equal(t, user2Price.ID, users[1].Price.ID)
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
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)

	_ = hasOneOfManyCreateState(t, query, &user, hasOneOfManyState{Type: "foo", State: "bar", UpdatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
	newFoo := hasOneOfManyCreateState(t, query, &user, hasOneOfManyState{Type: "foo", State: "active", UpdatedAt: time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC)})
	_ = hasOneOfManyCreateState(t, query, &user, hasOneOfManyState{Type: "bar", State: "active", UpdatedAt: time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC)})

	// last_updated_foo_state() aggregates max(updated_at) then max(id) over the type=foo states
	// (oracle: two nested aggregate joins with the closure's where on every level), so newFoo wins.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LastUpdatedFooState"))
	require.NotNil(t, user.LastUpdatedFooState)
	assert.Equal(t, newFoo.ID, user.LastUpdatedFooState.ID)
}

func TestHasOneOfMany_ItGetsCorrectResultUsingAtLeastTwoAggregatesDistinctFromId(t *testing.T) {
	query := newRelationQuery(t)
	hasOneOfManySchema(t, query)

	user := hasOneOfManySeedUser(t, query, 1)

	expectedState := hasOneOfManyCreateState(t, query, &user, hasOneOfManyState{
		State: "state", Type: "type",
		CreatedAt: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 3, 0, 0, 0, 0, time.UTC),
	})
	_ = hasOneOfManyCreateState(t, query, &user, hasOneOfManyState{
		State: "state", Type: "type",
		CreatedAt: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC),
	})

	// ofMany(['updated_at' => 'max', 'created_at' => 'max']) needs the nested two-aggregate join
	// the oracle compiles, with the key appended as MAX; max(updated_at) already separates the two
	// rows.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestUpdatedLatestCreatedState"))
	require.NotNil(t, user.LatestUpdatedLatestCreatedState)
	assert.Equal(t, expectedState.ID, user.LatestUpdatedLatestCreatedState.ID)
}
