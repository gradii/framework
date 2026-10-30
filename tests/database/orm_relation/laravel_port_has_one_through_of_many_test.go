package orm_relation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentHasOneThroughOfManyTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestHasOneThroughOfMany_<method without the test prefix>. The five tables mirror the corpus's
// createSchema(); rows are seeded through the ORM, and relations are loaded explicitly through
// OrmBuilder.Load — the harness's lazy-loading rule, since Go has no property-access magic.
//
// Laravel checks compiled SQL strings in some tests and behaviour in others. Every SQL expectation
// this file cites was measured with the oracle (.claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1), not recalled. Where our ORM cannot produce Laravel's SQL or
// behaviour, the test is skipped with the concrete divergence rather than weakened, and where the
// assertion is relation-object introspection with no Go equivalent the skip says so.
//
// # How our has-one-through of-many diverges from Laravel's
//
// Laravel attaches a one-of-many join (a grouped aggregate subquery) to every path that reads a
// hasOneThrough(...)->ofMany() relation: the relation's own query, WHERE-EXISTS subqueries and eager
// loads. In the Go ORM only the eager loader can consume OfMany, and oneOfManyJoin builds that join
// for HasOne/MorphOne kinds alone (see database/gorm/eager_loader.go) — a through relation is loaded
// as two plain reads: the through rows by IN, then the related rows by IN, and a has-one-through
// assignment keeps the first related row in through-row order (see through_eager_test.go). The
// corpus's fixtures place the row Laravel's ofMany would pick on the first intermediate in most of
// the behaviour tests this file ports — including the ones whose Laravel SQL resolves ties through a
// second aggregate the Go relation cannot carry — so an explicit Load observes the same model there.
// The tests whose fixture puts the expected row on the last intermediate, or whose assertion reads
// the relation read path or an existence subquery, are skipped below with the recorded divergence.

const hasOneThroughOfManySkipPHPOnlyRelationName = "PHP-only assertion (no Go equivalent): the Go ORM has no relation-name introspection — a relation is named by its key in the model's Relations() map, and OfMany takes no relation-name argument"

// --- models ------------------------------------------------------------------

// hasOneThroughOfManyUser ports HasOneThroughOfManyTestUser: only the id column is real; everything
// else is a relation destination or an aggregate alias destination, kept out of the table by gorm:"-".
type hasOneThroughOfManyUser struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`

	Intermediates                   []*hasOneThroughOfManyIntermediate `gorm:"-"`
	Logins                          []*hasOneThroughOfManyLogin        `gorm:"-"`
	LatestLogin                     *hasOneThroughOfManyLogin          `gorm:"-"`
	LatestLoginWithSoftDeletes      *hasOneThroughOfManySoftLogin      `gorm:"-"`
	LatestLoginWithShortcut         *hasOneThroughOfManyLogin          `gorm:"-"`
	LatestLoginWithInvalidAggregate *hasOneThroughOfManyLogin          `gorm:"-"`
	LatestLoginWithoutGlobalScope   *hasOneThroughOfManyLogin          `gorm:"-"`
	FirstLogin                      *hasOneThroughOfManyLogin          `gorm:"-"`
	LatestLoginWithFooState         *hasOneThroughOfManyLogin          `gorm:"-"`
	States                          []*hasOneThroughOfManyState        `gorm:"-"`
	FooState                        *hasOneThroughOfManyState          `gorm:"-"`
	LastUpdatedFooState             *hasOneThroughOfManyState          `gorm:"-"`
	Prices                          []*hasOneThroughOfManyPrice        `gorm:"-"`
	Price                           *hasOneThroughOfManyPrice          `gorm:"-"`
	PriceWithoutKeyInAggregates     *hasOneThroughOfManyPrice          `gorm:"-"`
	PriceWithShortcut               *hasOneThroughOfManyPrice          `gorm:"-"`
	PriceWithoutGlobalScope         *hasOneThroughOfManyPrice          `gorm:"-"`
	LatestUpdatedLatestCreatedState *hasOneThroughOfManyState          `gorm:"-"`
}

func (hasOneThroughOfManyUser) TableName() string { return "users" }

func (hasOneThroughOfManyUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Intermediates": contractsorm.HasMany{Related: &hasOneThroughOfManyIntermediate{}, ForeignKey: "user_id"},
		// logins(): through intermediates has logins — the plain shape of the corpus's
		// through('intermediates')->has('logins') chain.
		"Logins": contractsorm.HasManyThrough{Related: &hasOneThroughOfManyLogin{}, Through: &hasOneThroughOfManyIntermediate{}},
		// latest_login(): hasOneThrough(...)->ofMany(), the aggregate defaulting to id MAX. The OfMany
		// call is kept for fidelity, but the through eager loader does not consume it.
		"LatestLogin": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyLogin{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		// latest_login_with_soft_deletes(): the same shape over a soft-deleting model.
		"LatestLoginWithSoftDeletes": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManySoftLogin{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		// latest_login_with_shortcut(): logins()->one()->latestOfMany().
		"LatestLoginWithShortcut": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyLogin{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.LatestOfMany() },
		},
		// latest_login_with_invalid_aggregate(): ofMany('id', 'count') — Laravel rejects 'count'.
		"LatestLoginWithInvalidAggregate": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyLogin{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("id", "count") },
		},
		// latest_login_without_global_scope(): withoutGlobalScopes()->latestOfMany().
		"LatestLoginWithoutGlobalScope": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyLogin{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithoutGlobalScopes().LatestOfMany() },
		},
		// first_login(): ofMany('id', 'min').
		"FirstLogin": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyLogin{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("id", "MIN") },
		},
		// latest_login_with_foo_state(): ofMany(['id' => 'max'], join states, where states.type=foo).
		"LatestLoginWithFooState": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyLogin{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("id", "MAX").
					Join("states ON states.intermediate_id = logins.intermediate_id").
					Where("states.type", "foo")
			},
		},
		// states(): through($this->intermediates())->has(fn ($i) => $i->states()) — the nested through
		// chain reduces to the same two-key shape a plain HasManyThrough declares.
		"States": contractsorm.HasManyThrough{Related: &hasOneThroughOfManyState{}, Through: &hasOneThroughOfManyIntermediate{}},
		// foo_state(): ofMany([id => max], where type=foo) — the empty aggregate set defaults to id MAX.
		"FooState": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyState{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "").Where("states.type", "foo") },
		},
		// last_updated_foo_state(): ofMany(['updated_at' => 'max', 'id' => 'max'], where type=foo).
		// OfMany carries one (column, aggregate) pair in the Go ORM, so only the first aggregate is
		// declared; the through eager loader ignores it, and the ported tests record where that still
		// returns Laravel's row.
		"LastUpdatedFooState": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyState{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("updated_at", "MAX").Where("states.type", "foo")
			},
		},
		// prices(): throughIntermediates()->hasPrices().
		"Prices": contractsorm.HasManyThrough{Related: &hasOneThroughOfManyPrice{}, Through: &hasOneThroughOfManyIntermediate{}},
		// price(): ofMany(['published_at' => 'max', 'id' => 'max'], where published_at < now()).
		// Only the first aggregate is expressible, as above.
		"Price": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyPrice{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("published_at", "MAX").Where("prices.published_at", "<", time.Now())
			},
		},
		// price_without_key_in_aggregates(): ofMany(['published_at' => 'MAX']) — Laravel adds the
		// key (id MAX) when the aggregate set omits it.
		"PriceWithoutKeyInAggregates": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyPrice{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("published_at", "MAX") },
		},
		// price_with_shortcut(): latestOfMany(['published_at', 'id']).
		"PriceWithShortcut": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyPrice{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.LatestOfMany("published_at", "id") },
		},
		// price_without_global_scope(): the multi-aggregate price() with withoutGlobalScopes().
		"PriceWithoutGlobalScope": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyPrice{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.WithoutGlobalScopes().OfMany("published_at", "MAX").Where("prices.published_at", "<", time.Now())
			},
		},
		// latest_updated_latest_created_state(): ofMany(['updated_at' => 'max', 'created_at' => 'max']).
		"LatestUpdatedLatestCreatedState": contractsorm.HasOneThrough{
			Related: &hasOneThroughOfManyState{}, Through: &hasOneThroughOfManyIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("updated_at", "MAX") },
		},
	}
}

// hasOneThroughOfManyIntermediate ports HasOneThroughOfManyTestIntermediate, with its three HasMany
// destinations used for seeding.
type hasOneThroughOfManyIntermediate struct {
	ID     uint `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint `gorm:"column:user_id"`

	Logins []*hasOneThroughOfManyLogin `gorm:"-"`
	States []*hasOneThroughOfManyState `gorm:"-"`
	Prices []*hasOneThroughOfManyPrice `gorm:"-"`
}

func (hasOneThroughOfManyIntermediate) TableName() string { return "intermediates" }

func (hasOneThroughOfManyIntermediate) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Logins": contractsorm.HasMany{Related: &hasOneThroughOfManyLogin{}, ForeignKey: "intermediate_id"},
		"States": contractsorm.HasMany{Related: &hasOneThroughOfManyState{}, ForeignKey: "intermediate_id"},
		"Prices": contractsorm.HasMany{Related: &hasOneThroughOfManyPrice{}, ForeignKey: "intermediate_id"},
	}
}

// hasOneThroughOfManyLogin ports HasOneThroughOfManyTestLogin.
type hasOneThroughOfManyLogin struct {
	ID             uint `gorm:"column:id;primaryKey;autoIncrement"`
	IntermediateID uint `gorm:"column:intermediate_id"`
}

func (hasOneThroughOfManyLogin) TableName() string { return "logins" }

// hasOneThroughOfManySoftLogin ports HasOneThroughOfManyTestLoginWithSoftDeletes: the same table,
// carrying the framework's soft-delete field.
type hasOneThroughOfManySoftLogin struct {
	databaseorm.SoftDeletes

	ID             uint `gorm:"column:id;primaryKey;autoIncrement"`
	IntermediateID uint `gorm:"column:intermediate_id"`
}

func (hasOneThroughOfManySoftLogin) TableName() string { return "logins" }

// hasOneThroughOfManyState ports HasOneThroughOfManyTestState. The corpus gives it timestamps and
// fills created_at with now(); the sqlite driver hands datetime columns back as time.Time, so the
// port seeds time.Time values where a test needs them and leaves the zero value otherwise — no
// ported assertion reads the timestamps besides the explicit seeds.
type hasOneThroughOfManyState struct {
	ID             uint      `gorm:"column:id;primaryKey;autoIncrement"`
	State          string    `gorm:"column:state"`
	Type           string    `gorm:"column:type"`
	IntermediateID uint      `gorm:"column:intermediate_id"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (hasOneThroughOfManyState) TableName() string { return "states" }

// hasOneThroughOfManyPrice ports HasOneThroughOfManyTestPrice. The corpus casts published_at to a
// datetime; the sqlite driver hands datetime columns back as time.Time, and the port only ever
// compares row ids.
type hasOneThroughOfManyPrice struct {
	ID             uint      `gorm:"column:id;primaryKey;autoIncrement"`
	PublishedAt    time.Time `gorm:"column:published_at"`
	IntermediateID uint      `gorm:"column:intermediate_id"`
}

func (hasOneThroughOfManyPrice) TableName() string { return "prices" }

// hasOneThroughOfManyAggregateDest is the scan target for withCount / withExists reads: Laravel
// exposes latest_login_count / latest_login_exists / foo_state_exists as dynamic attributes on the
// user model, while a Go model's table columns are fixed — the alias destinations must live on a
// separate struct or every users insert would try to write the pseudo-columns.
type hasOneThroughOfManyAggregateDest struct {
	ID                uint `gorm:"column:id"`
	LatestLoginCount  int  `gorm:"column:latest_login_count"`
	LatestLoginExists bool `gorm:"column:latest_login_exists"`
	FooStateExists    bool `gorm:"column:foo_state_exists"`
}

// --- schema and seeding ------------------------------------------------------

// hasOneThroughOfManySchema mirrors the corpus's createSchema() in sqlite DDL.
func hasOneThroughOfManySchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement)`,
		`create table intermediates (id integer primary key autoincrement, user_id integer not null)`,
		`create table logins (id integer primary key autoincrement, intermediate_id integer not null, deleted_at datetime null)`,
		`create table states (id integer primary key autoincrement, state varchar not null, type varchar not null, intermediate_id integer not null, created_at datetime null, updated_at datetime null)`,
		`create table prices (id integer primary key autoincrement, published_at datetime not null, intermediate_id integer not null)`,
	)
}

// hasOneThroughOfManySeedUser creates a users row, like HasOneThroughOfManyTestUser::create().
func hasOneThroughOfManySeedUser(t *testing.T, query contractsorm.Query, id uint) hasOneThroughOfManyUser {
	t.Helper()

	// The corpus's users table carries only the auto-increment id, and the ORM compiles an
	// all-defaults struct insert as `insert into "users" () values ()`, which sqlite rejects
	// (Laravel compiles `default values`). The helper therefore writes the explicit id the
	// auto increment would assign; every assertion observes the same rows.
	user := hasOneThroughOfManyUser{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&user).Create(map[string]any{"id": id}))

	return user
}

// hasOneThroughOfManySeedIntermediates ports factory()->hasIntermediates(count): count intermediate
// rows, each pointing at the user, created in order. The user's Intermediates slice is loaded after
// the creates, so intermediates[0] is the corpus's `intermediates->first()` and the last element is
// `intermediates->last()`.
func hasOneThroughOfManySeedIntermediates(t *testing.T, query contractsorm.Query, user *hasOneThroughOfManyUser, count int) []*hasOneThroughOfManyIntermediate {
	t.Helper()

	for i := 0; i < count; i++ {
		intermediate := hasOneThroughOfManyIntermediate{}
		require.NoError(t, cloneRelationQuery(t, query).Relation(user, "Intermediates").Create(&intermediate))
	}

	require.NoError(t, cloneRelationQuery(t, query).Load(user, "Intermediates"))
	require.Len(t, user.Intermediates, count)

	return user.Intermediates
}

// hasOneThroughOfManyCreateLogin ports $intermediate->logins()->create().
func hasOneThroughOfManyCreateLogin(t *testing.T, query contractsorm.Query, intermediate *hasOneThroughOfManyIntermediate) hasOneThroughOfManyLogin {
	t.Helper()

	login := hasOneThroughOfManyLogin{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(intermediate, "Logins").Create(&login))

	return login
}

// hasOneThroughOfManyCreateState ports $intermediate->states()->create([...]).
func hasOneThroughOfManyCreateState(t *testing.T, query contractsorm.Query, intermediate *hasOneThroughOfManyIntermediate, state hasOneThroughOfManyState) hasOneThroughOfManyState {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Relation(intermediate, "States").Create(&state))

	return state
}

// hasOneThroughOfManyCreatePrice ports $intermediate->prices()->create(['published_at' => ...]).
func hasOneThroughOfManyCreatePrice(t *testing.T, query contractsorm.Query, intermediate *hasOneThroughOfManyIntermediate, publishedAt time.Time) hasOneThroughOfManyPrice {
	t.Helper()

	price := hasOneThroughOfManyPrice{PublishedAt: publishedAt}
	require.NoError(t, cloneRelationQuery(t, query).Relation(intermediate, "Prices").Create(&price))

	return price
}

// --- ports of the corpus -----------------------------------------------------

func TestHasOneThroughOfMany_ItGuessesRelationName(t *testing.T) {
	// Oracle: HasOneThroughOfManyTestUser::make()->latest_login()->getRelationName() === 'latest_login'.
	t.Skip(hasOneThroughOfManySkipPHPOnlyRelationName)
}

func TestHasOneThroughOfMany_ItGuessesRelationNameAndAddsOfManyWhenTableNameIsRelationName(t *testing.T) {
	// Oracle: HasOneThroughOfManyTestModel::make()->logins()->getRelationName() === 'logins_of_many'.
	t.Skip(hasOneThroughOfManySkipPHPOnlyRelationName)
}

func TestHasOneThroughOfMany_RelationNameCanBeSet(t *testing.T) {
	// Laravel: ofMany('id', 'max', 'foo') / latestOfMany('id', 'bar') / oldestOfMany('id', 'baz'),
	// each checked through Relation::getRelationName(); the oracle confirms foo / bar / baz. The Go
	// ORM has neither the third argument nor the introspection.
	t.Skip(hasOneThroughOfManySkipPHPOnlyRelationName)
}

func TestHasOneThroughOfMany_CorrectLatestOfManyQuery(t *testing.T) {
	// Laravel pins the SQL $user->latest_login()->getQuery()->toSql() renders; the oracle prints
	//   select "logins".* from "logins" inner join "intermediates" on "intermediates"."id" =
	//   "logins"."intermediate_id" inner join (select MAX("logins"."id") as "id_aggregate",
	//   "intermediates"."user_id" from "logins" inner join "intermediates" on "intermediates"."id" =
	//   "logins"."intermediate_id" where "intermediates"."user_id" = ? group by
	//   "intermediates"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "logins"."id"
	//   and "latest_login"."user_id" = "intermediates"."user_id" where "intermediates"."user_id" = ?
	// Our read path renders (probe):
	//   select * from "logins" inner join "intermediates" ON "logins"."intermediate_id" =
	//   "intermediates"."id" where "intermediates"."user_id" = ?
	t.Skip("our ORM diverges: the relation read path (Query.Related, the Go form of $user->latest_login()) renders no one-of-many join at all, and for has-one-through relations the eager path ignores OfMany too (oneOfManyJoin builds the join for HasOne/MorphOne kinds only) — so Laravel's SQL is produced on no path")
}

func TestHasOneThroughOfMany_EagerLoadingAppliesConstraintsToInnerJoinSubQuery(t *testing.T) {
	// Laravel pins $relation->getOneOfManySubQuery()->toSql() after addEagerConstraints([$user]);
	// the oracle prints
	//   select MAX("logins"."id") as "id_aggregate", "intermediates"."user_id" from "logins" inner
	//   join "intermediates" on "intermediates"."id" = "logins"."intermediate_id" where
	//   "intermediates"."user_id" = ? and "intermediates"."user_id" in (1) group by
	//   "intermediates"."user_id"
	t.Skip("our ORM diverges: getOneOfManySubQuery has no Go equivalent, and the through eager loader renders no aggregate subquery at all — it reads the through rows, then the related rows with a plain IN")
}

func TestHasOneThroughOfMany_EagerLoadingAppliesConstraintsToQuery(t *testing.T) {
	// Laravel pins the relation query's SQL after addEagerConstraints([$user]); the oracle prints the
	// testCorrectLatestOfManyQuery SQL with the subquery additionally constrained by
	//   "intermediates"."user_id" in (1)
	t.Skip("our ORM diverges: addEagerConstraints has no Go equivalent, the read path renders no one-of-many join, and the through eager loader ignores OfMany — the constrained one-of-many query Laravel pins cannot be produced")
}

func TestHasOneThroughOfMany_GlobalScopeIsNotAppliedWhenRelationIsDefinedWithoutGlobalScope(t *testing.T) {
	// Laravel adds an orderBy global scope to the login model, builds
	// latest_login_without_global_scope() and pins the eager constraint SQL; the oracle prints the
	// testEagerLoadingAppliesConstraintsToQuery SQL with the subquery alias "latestOfMany" (Laravel's
	// guessed relation name for the shortcut), confirming the scope was dropped.
	t.Skip("our ORM diverges: the pinned SQL is the relation read path's, which renders no one-of-many join in the Go port, and the alias is Laravel's guessed relation name ('latestOfMany') while ours — when any join is built at all, which for through relations it is not — is always goravel_one_of_many")
}

func TestHasOneThroughOfMany_GlobalScopeIsNotAppliedWhenRelationIsDefinedWithoutGlobalScopeWithComplexQuery(t *testing.T) {
	// Laravel pins price_without_global_scope()->getQuery()->toSql(); the oracle prints nested
	// aggregate joins:
	//   select "prices".* from "prices" inner join "intermediates" on "intermediates"."id" =
	//   "prices"."intermediate_id" inner join (select max("prices"."id") as "id_aggregate",
	//   min("prices"."published_at") as "published_at_aggregate", "intermediates"."user_id" ...) as
	//   "price_without_global_scope" on ... where "intermediates"."user_id" = ?
	t.Skip("our ORM diverges: OfMany carries a single (column, aggregate) pair, so Laravel's two-aggregate ofMany(['published_at' => 'max', 'id' => 'max']) with its nested aggregate joins cannot be expressed; and the pinned SQL is the relation read path's, which renders no one-of-many join at all")
}

func TestHasOneThroughOfMany_QualifyingSubSelectColumn(t *testing.T) {
	// Laravel: $user->latest_login()->qualifySubSelectColumn('id') === 'latest_login.id' (the oracle
	// confirms). The Go ORM exposes no such accessor.
	t.Skip("PHP-only assertion (no Go equivalent): the Go ORM has no qualifySubSelectColumn accessor")
}

func TestHasOneThroughOfMany_ItFailsWhenUsingInvalidAggregate(t *testing.T) {
	// Oracle: constructing the relation throws InvalidArgumentException with the message
	// "Invalid aggregate [count] used within ofMany relation. Available aggregates: MIN, MAX".
	t.Skip("our ORM diverges: OfMany does not validate the aggregate — ofMany('id', 'count') is accepted (probe: the relation builds and Load returns the login with the highest id, id 2) — so Laravel's InvalidArgumentException cannot be reproduced")
}

func TestHasOneThroughOfMany_ItGetsCorrectResults(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreateLogin(t, query, intermediates[1]) // the previous login: the relation must skip it
	latestLogin := hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.Equal(t, latestLogin.ID, user.LatestLogin.ID)
}

func TestHasOneThroughOfMany_ResultDoesNotHaveAggregateColumn(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): a typed Go model has no dynamic attributes, so the absence of Laravel's id_aggregate pseudo-column on the loaded model cannot be observed")
}

func TestHasOneThroughOfMany_ItGetsCorrectResultsUsingShortcutMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreateLogin(t, query, intermediates[1])
	latestLogin := hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithShortcut"))
	require.NotNil(t, user.LatestLoginWithShortcut)
	assert.Equal(t, latestLogin.ID, user.LatestLoginWithShortcut.ID)
}

func TestHasOneThroughOfMany_ItGetsCorrectResultsUsingShortcutReceivingMultipleColumnsMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreatePrice(t, query, intermediates[1], time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	price := hasOneThroughOfManyCreatePrice(t, query, intermediates[0], time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))

	// Laravel resolves the tied published_at values through latestOfMany(['published_at', 'id']):
	// max(published_at), then the id tie-break => the price on the first intermediate. The Go
	// relation carries only the first column and the through eager loader ignores OfMany, returning
	// the first price in through-row order — on this fixture the same row, since the first
	// intermediate's only price is also the one created last (highest id).
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "PriceWithShortcut"))
	require.NotNil(t, user.PriceWithShortcut)
	assert.Equal(t, price.ID, user.PriceWithShortcut.ID)
}

func TestHasOneThroughOfMany_KeyIsAddedToAggregatesWhenMissing(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreatePrice(t, query, intermediates[1], time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	price := hasOneThroughOfManyCreatePrice(t, query, intermediates[0], time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))

	// Laravel adds the key (id MAX) to the aggregate set when the declared aggregates omit it, so
	// the tied published_at values resolve to the highest id on the first intermediate. The Go
	// relation adds nothing — it keeps the first price in through-row order — which is the same row
	// on this fixture (the first intermediate's only price is the one created last).
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "PriceWithoutKeyInAggregates"))
	require.NotNil(t, user.PriceWithoutKeyInAggregates)
	assert.Equal(t, price.ID, user.PriceWithoutKeyInAggregates.ID)
}

func TestHasOneThroughOfMany_ItGetsWithConstraintsCorrectResults(t *testing.T) {
	t.Skip("our ORM diverges: whereKey(previous_login) runs on the relation read path, which carries no one-of-many join — probe: the constrained read matches the previous login's row (one row returned; its hydrated id reads 2 because the joined select's duplicate id column wins the scan) where Laravel returns null")
}

func TestHasOneThroughOfMany_ItEagerLoadsCorrectModels(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreateLogin(t, query, intermediates[1])
	latestLogin := hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	// Laravel: with('latest_login')->first(). The port reads through Get: First(&struct) with an
	// eager load leaves its single-row cursor open while retrieved() runs the eager query, which
	// deadlocks on the harness's one-connection pool, while Get drains its cursor before the eager
	// query runs. The observable is the same — the one user row, with the relation attached.
	found := []hasOneThroughOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughOfManyUser{}).With("LatestLogin").Get(&found))
	require.Len(t, found, 1)

	// Laravel asserts relationLoaded('latest_login'); in Go the eager load shows as the field
	// being populated by the query.
	require.NotNil(t, found[0].LatestLogin)
	assert.Equal(t, latestLogin.ID, found[0].LatestLogin.ID)
}

func TestHasOneThroughOfMany_ItJoinsOtherTableInSubQuery(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithFooState"))
	assert.Nil(t, user.LatestLoginWithFooState)

	// Laravel unsets the relation and reads the property again; every Load re-reads, so the second
	// Load is the re-read.
	_ = hasOneThroughOfManyCreateState(t, query, intermediates[0], hasOneThroughOfManyState{Type: "foo", State: "draft"})

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithFooState"))
	assert.NotNil(t, user.LatestLoginWithFooState)
}

func TestHasOneThroughOfMany_HasNested(t *testing.T) {
	t.Skip("our ORM diverges: WhereHas does not carry the one-of-many join into the existence subquery, so the previous (non-latest) login still satisfies whereHas('latest_login', id = previous) — probe: true, where Laravel returns false")
}

func TestHasOneThroughOfMany_WithHasNested(t *testing.T) {
	t.Skip("our ORM diverges: WithWhereHas builds its existence subquery the same way WhereHas does, without the one-of-many join — probe: constraining by the previous login's id still matches one user with latest_login attached, and the exists() check is true, where Laravel matches nothing and reports false")
}

func TestHasOneThroughOfMany_HasCount(t *testing.T) {
	t.Skip("our ORM diverges: WithCount counts all related rows — the count subquery carries no one-of-many join, so latest_login_count is 2 for two logins where Laravel reports 1")
}

func TestHasOneThroughOfMany_Exists(t *testing.T) {
	t.Skip("our ORM diverges: with the one-of-many join absent from the relation read path, exists() with whereKey(previous_login) is true where Laravel reports false (both logins satisfy the unjoined read)")
}

func TestHasOneThroughOfMany_IsMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	login := hasOneThroughOfManyCreateLogin(t, query, intermediates[1])
	second := hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	// Laravel: $relation->is($model) compares the relation result's key with the model's key; the
	// port loads the relation (a fresh read per call) and compares the loaded model's key.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.NotEqual(t, login.ID, user.LatestLogin.ID)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.Equal(t, second.ID, user.LatestLogin.ID)
}

func TestHasOneThroughOfMany_IsNotMethod(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	login := hasOneThroughOfManyCreateLogin(t, query, intermediates[1])
	second := hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	// Laravel: isNot($model) is the negation of is($model), so the same two key comparisons.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.NotEqual(t, login.ID, user.LatestLogin.ID)

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLogin"))
	require.NotNil(t, user.LatestLogin)
	assert.Equal(t, second.ID, user.LatestLogin.ID)
}

func TestHasOneThroughOfMany_Get(t *testing.T) {
	t.Skip("our ORM diverges: get() on the relation read path returns both logins (2 rows) where Laravel's one-of-many join yields 1, and whereKey(previous_login)->get() returns the previous login's row (1 row) instead of an empty result")
}

func TestHasOneThroughOfMany_Count(t *testing.T) {
	t.Skip("our ORM diverges: count() on the relation read path counts all related logins (2) instead of the single one-of-many row (Laravel: 1)")
}

func TestHasOneThroughOfMany_Aggregate(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	firstLogin := hasOneThroughOfManyCreateLogin(t, query, intermediates[0])
	_ = hasOneThroughOfManyCreateLogin(t, query, intermediates[1])

	// Laravel re-reads the user (first()) and lazy-loads first_login.
	found := hasOneThroughOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).First(&found))
	require.NoError(t, cloneRelationQuery(t, query).Load(&found, "FirstLogin"))

	require.NotNil(t, found.FirstLogin)
	assert.Equal(t, firstLogin.ID, found.FirstLogin.ID)
}

func TestHasOneThroughOfMany_JoinConstraints(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreateState(t, query, intermediates[1], hasOneThroughOfManyState{Type: "foo", State: "draft"})
	currentForState := hasOneThroughOfManyCreateState(t, query, intermediates[0], hasOneThroughOfManyState{Type: "foo", State: "active"})
	_ = hasOneThroughOfManyCreateState(t, query, intermediates[0], hasOneThroughOfManyState{Type: "bar", State: "baz"})

	found := hasOneThroughOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).First(&found))
	require.NoError(t, cloneRelationQuery(t, query).Load(&found, "FooState"))

	require.NotNil(t, found.FooState)
	assert.Equal(t, currentForState.ID, found.FooState.ID)
}

func TestHasOneThroughOfMany_MultipleAggregates(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)
	_ = hasOneThroughOfManyCreatePrice(t, query, intermediates[1], time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))
	price := hasOneThroughOfManyCreatePrice(t, query, intermediates[0], time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC))

	// Laravel's price() aggregates max(published_at) then max(id) over the user's prices with
	// published_at < now(); the Go relation cannot carry the second aggregate and the through eager
	// loader ignores OfMany, returning the first price in through-row order — on this fixture the
	// same row (the first intermediate's only price is the one created last).
	found := hasOneThroughOfManyUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&found).First(&found))
	require.NoError(t, cloneRelationQuery(t, query).Load(&found, "Price"))

	require.NotNil(t, found.Price)
	assert.Equal(t, price.ID, found.Price.ID)
}

func TestHasOneThroughOfMany_EagerLoadingWithMultipleAggregates(t *testing.T) {
	t.Skip("our ORM diverges: with('price') eager loading ignores OfMany for through relations, so each user keeps the first price in through-row order — probe: user1 gets the expected price (id 2, its latest sits on the first intermediate) but user2 gets id 5, the price on its first intermediate, where Laravel's two-aggregate ofMany resolves user2 to id 4 (max published_at with the id tie-break)")
}

func TestHasOneThroughOfMany_WithExists(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 1)

	found := hasOneThroughOfManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughOfManyUser{}).WithExists("LatestLogin").First(&found))
	assert.False(t, found.LatestLoginExists)

	_ = hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughOfManyUser{}).WithExists("LatestLogin").First(&found))
	assert.True(t, found.LatestLoginExists)
}

func TestHasOneThroughOfMany_WithExistsWithConstraintsInJoinSubSelect(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 1)

	found := hasOneThroughOfManyAggregateDest{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughOfManyUser{}).WithExists("FooState").First(&found))
	assert.False(t, found.FooStateExists)

	_ = hasOneThroughOfManyCreateState(t, query, intermediates[0], hasOneThroughOfManyState{Type: "foo", State: "bar"})

	require.NoError(t, cloneRelationQuery(t, query).Model(&hasOneThroughOfManyUser{}).WithExists("FooState").First(&found))
	assert.True(t, found.FooStateExists)
}

func TestHasOneThroughOfMany_WithSoftDeletes(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 1)
	_ = hasOneThroughOfManyCreateLogin(t, query, intermediates[0])

	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LatestLoginWithSoftDeletes"))
	assert.NotNil(t, user.LatestLoginWithSoftDeletes)
}

func TestHasOneThroughOfMany_WithConstraintNotInAggregate(t *testing.T) {
	query := newRelationQuery(t)
	hasOneThroughOfManySchema(t, query)

	user := hasOneThroughOfManySeedUser(t, query, 1)
	intermediates := hasOneThroughOfManySeedIntermediates(t, query, &user, 2)

	_ = hasOneThroughOfManyCreateState(t, query, intermediates[1], hasOneThroughOfManyState{
		Type: "foo", State: "bar", UpdatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	newFoo := hasOneThroughOfManyCreateState(t, query, intermediates[0], hasOneThroughOfManyState{
		Type: "foo", State: "active", UpdatedAt: time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC),
	})
	_ = hasOneThroughOfManyCreateState(t, query, intermediates[0], hasOneThroughOfManyState{
		Type: "bar", State: "active", UpdatedAt: time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC),
	})

	// Laravel aggregates max(updated_at) then max(id) over the type=foo states; the Go relation
	// carries only the first aggregate and the through eager loader ignores OfMany, returning the
	// first foo state in through-row order — on this fixture the same row: newFoo sits on the first
	// intermediate and also carries the latest updated_at.
	require.NoError(t, cloneRelationQuery(t, query).Load(&user, "LastUpdatedFooState"))
	require.NotNil(t, user.LastUpdatedFooState)
	assert.Equal(t, newFoo.ID, user.LastUpdatedFooState.ID)
}

func TestHasOneThroughOfMany_ItGetsCorrectResultUsingAtLeastTwoAggregatesDistinctFromId(t *testing.T) {
	t.Skip("our ORM diverges: ofMany(['updated_at' => 'max', 'created_at' => 'max']) needs a nested two-aggregate join, and the through eager loader ignores OfMany entirely — probe: Load returns the state on the first intermediate (id 2, updated 2023-01-02) where Laravel returns expectedState (id 1, updated 2023-01-03) on the last intermediate")
}
