package timestamps

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentTimestampsTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestTimestamps_<method without the test
// prefix>. The corpus's three tables mirror its createSchema() in sqlite DDL; rows are seeded through
// the ORM while carbon.SetTestNow freezes the clock the way Carbon::setTestNow does, and every
// assertion reads the in-memory model exactly as the corpus's toDateTimeString() comparisons do.
//
// Laravel's timestamp suspension family is ported as far as the Go model design reaches:
// HasTimestamps::withoutTimestamps became the query-level Query.WithoutTimestamps scope (a closure
// that suspends the query's automatic created_at / updated_at stamping, re-entrantly, and restores
// the state even when the callback fails), Query.UsesTimestamps is the query-side form of the
// trait's usesTimestamps(), and the per-instance $timestamps flag became the Disabled field on
// databaseorm.Timestamps, read back through its UsesTimestamps method. The class-scoped statics —
// Eloquent::withoutTimestamps(), withoutTimestampsOn() and isIgnoringTimestamps(), which PHP scopes
// to the model class through one static list — still have no Go counterpart, because the Go scope
// suspends a query rather than a class; the test built on them stays skipped with the concrete
// divergence rather than weakened.
//
// The SQL behind the ported behaviour was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, sqlite):
//
//	insert   insert into "users" ("email", "updated_at", "created_at") values (?, ?, ?)
//	         ["test@test.com", "1995-01-01 00:00:00", "1995-01-01 00:00:00"]
//	insert   insert into "users_created_at" ("email", "created_at") values (?, ?)
//	         ["test@test.com", "788918400"]           (UPDATED_AT = null, $dateFormat = 'U')
//	insert   insert into "users_updated_at" ("email", "updated_at") values (?, ?)
//	         ["test@test.com", "788918400"]           (CREATED_AT = null, $dateFormat = 'U')
//	update   update "users" set "email" = ?, "updated_at" = ? where "id" = ?
//	         ["baz@example.com", "1995-01-01 01:00:00", 1]
//	suspended update "users" set "email" = ? where "id" = ?
//	         ["bar@example.com", 1]     (inside withoutTimestamps / with $timestamps = false; no
//	         updated_at, stored updated_at stays 1995-01-01 00:00:00)
//	suspended insert into "users" ("email") values (?)
//	         ["no-ts@example.com"]      (inside withoutTimestamps; no created_at / updated_at at
//	         all, both stored NULL)
//	usesTimestamps() inside a scope: false — nested scope: false, after the nested scope: false,
//	after the scope: true; with $timestamps = false: false before, inside, and after; after a
//	closure that throws: $timestamps true again and usesTimestamps() true.
//
// The Go side stores timestamps as RFC3339 text: sqlite3 quote(created_at) reads back
// '1995-01-01T00:00:00Z' for all three tables, where Laravel writes '1995-01-01 00:00:00' for the
// default models and '788918400' for the 'U' models. The assertions here read the in-memory values,
// which match; the byte encoding is an ORM-level divergence this corpus does not observe.

const timestampsSkipClassSuspension = "our ORM diverges: timestamp suspension is scoped to a query (Query.WithoutTimestamps), not tracked per model class — Eloquent::withoutTimestamps(), withoutTimestampsOn() and isIgnoringTimestamps() keep a class-scoped static list, so the global, class-list-shaped suspension cannot be reproduced"

// errTimestampsThrown stands in for the RuntimeException the corpus's throwing closure raises: the
// Go form of "the closure threw" is the callback returning an error, which WithoutTimestamps
// restores around and returns unchanged.
var errTimestampsThrown = errors.New("thrown by the closure")

// --- models ------------------------------------------------------------------

// userWithCreatedAndUpdated ports UserWithCreatedAndUpdated ($table = 'users'): the plain model
// carrying both automatic timestamps.
type userWithCreatedAndUpdated struct {
	databaseorm.Timestamps

	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Email string `gorm:"column:email"`
}

func (userWithCreatedAndUpdated) TableName() string { return "users" }

// userWithCreated ports UserWithCreated (UPDATED_AT = null, $table = 'users_created_at',
// $dateFormat = 'U'). The Go model declares only the created-at field, so nothing ever writes an
// updated_at the table does not have, mirroring the null constant, and types it *carbon.Timestamp
// so the in-memory value renders 1995-01-01 00:00:00 like the corpus's Carbon attribute. The
// stored encoding diverges: Laravel's oracle insert binds created_at = "788918400" ($dateFormat =
// 'U'), while this ORM writes RFC3339 text (probed: sqlite3 quote(created_at) reads back
// '1995-01-01T00:00:00Z'). The corpus compares only in-memory values, which match.
type userWithCreated struct {
	ID        uint              `gorm:"column:id;primaryKey;autoIncrement"`
	Email     string            `gorm:"column:email"`
	CreatedAt *carbon.Timestamp `gorm:"autoCreateTime;column:created_at"`
}

func (userWithCreated) TableName() string { return "users_created_at" }

// userWithUpdated ports UserWithUpdated (CREATED_AT = null, $table = 'users_updated_at'; see
// userWithCreated for the timestamp storage-encoding note).
type userWithUpdated struct {
	ID        uint              `gorm:"column:id;primaryKey;autoIncrement"`
	Email     string            `gorm:"column:email"`
	UpdatedAt *carbon.Timestamp `gorm:"autoUpdateTime;column:updated_at"`
}

func (userWithUpdated) TableName() string { return "users_updated_at" }

// --- schema ------------------------------------------------------------------

// timestampsSchema mirrors the corpus's createSchema() in sqlite DDL. Laravel's timestamps() adds
// nullable created_at / updated_at columns; the single-timestamp tables carry the remaining column
// as a NOT NULL varchar.
func timestampsSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table users (id integer primary key autoincrement, email varchar not null unique, created_at datetime null, updated_at datetime null)`,
		`create table users_created_at (id integer primary key autoincrement, email varchar not null unique, created_at varchar not null)`,
		`create table users_updated_at (id integer primary key autoincrement, email varchar not null unique, updated_at varchar not null)`,
	)
}

// --- ports of the corpus -----------------------------------------------------

func TestTimestamps_UserWithCreatedAtAndUpdatedAt(t *testing.T) {
	query := newRelationQuery(t)
	timestampsSchema(t, query)

	now := carbon.Now()
	carbon.SetTestNow(now)
	t.Cleanup(carbon.ClearTestNow)

	user := userWithCreatedAndUpdated{Email: "test@test.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	require.NotNil(t, user.CreatedAt)
	require.NotNil(t, user.UpdatedAt)
	assert.Equal(t, now.ToDateTimeString(), user.CreatedAt.ToDateTimeString())
	assert.Equal(t, now.ToDateTimeString(), user.UpdatedAt.ToDateTimeString())
}

func TestTimestamps_UserWithCreatedAt(t *testing.T) {
	query := newRelationQuery(t)
	timestampsSchema(t, query)

	now := carbon.Now()
	carbon.SetTestNow(now)
	t.Cleanup(carbon.ClearTestNow)

	user := userWithCreated{Email: "test@test.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	require.NotNil(t, user.CreatedAt)
	assert.Equal(t, now.ToDateTimeString(), user.CreatedAt.ToDateTimeString())
}

func TestTimestamps_UserWithUpdatedAt(t *testing.T) {
	query := newRelationQuery(t)
	timestampsSchema(t, query)

	now := carbon.Now()
	carbon.SetTestNow(now)
	t.Cleanup(carbon.ClearTestNow)

	user := userWithUpdated{Email: "test@test.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	require.NotNil(t, user.UpdatedAt)
	assert.Equal(t, now.ToDateTimeString(), user.UpdatedAt.ToDateTimeString())
}

func TestTimestamps_WithoutTimestamp(t *testing.T) {
	query := newRelationQuery(t)
	timestampsSchema(t, query)

	carbon.SetTestNow(carbon.Parse("1995-01-01 00:00:00"))
	t.Cleanup(carbon.ClearTestNow)

	user := userWithCreatedAndUpdated{Email: "test@test.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))
	carbon.SetTestNow(carbon.Parse("1995-01-01 01:00:00"))

	// Laravel: $user->withoutTimestamps() suspends the model's automatic timestamps for the closure
	// (re-entrantly — the nested call keeps them suspended), usesTimestamps() reports the state, and
	// the update() inside writes email while leaving updated_at at the create-time value; the oracle
	// measured the suspended statement as `update "users" set "email" = ? where "id" = ?` with the
	// stored updated_at still 1995-01-01 00:00:00. The Go scope (Query.WithoutTimestamps) suspends
	// the query's stamping the same way, and the query log pins the statement.
	attached := query.Model(&user)
	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := attached.(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")
	logged := withContext.WithContext(ctx)

	assert.True(t, logged.UsesTimestamps())

	require.NoError(t, logged.WithoutTimestamps(func() error {
		assert.False(t, logged.UsesTimestamps())

		require.NoError(t, logged.WithoutTimestamps(func() error {
			assert.False(t, logged.UsesTimestamps())

			return nil
		}))
		assert.False(t, logged.UsesTimestamps())

		_, err := logged.Update(map[string]any{"email": "bar@example.com"})

		return err
	}))

	assert.True(t, logged.UsesTimestamps())

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "the suspended update is one statement without updated_at")
	assert.Equal(t, `update "users" set "email" = 'bar@example.com' where "id" = 1`, logs[0].Query)

	// The stored row — and the in-memory model, the way the corpus compares
	// $now->equalTo($user->updated_at) — keep the create-time updated_at.
	var reloaded userWithCreatedAndUpdated
	require.NoError(t, cloneRelationQuery(t, query).Where("id", user.ID).First(&reloaded))
	assert.Equal(t, "bar@example.com", reloaded.Email)
	require.NotNil(t, reloaded.UpdatedAt)
	assert.Equal(t, "1995-01-01 00:00:00", reloaded.UpdatedAt.ToDateTimeString())
	assert.Equal(t, "bar@example.com", user.Email)
	require.NotNil(t, user.UpdatedAt)
	assert.Equal(t, "1995-01-01 00:00:00", user.UpdatedAt.ToDateTimeString())
}

func TestTimestamps_WithoutTimestampWhenAlreadyIgnoringTimestamps(t *testing.T) {
	query := newRelationQuery(t)
	timestampsSchema(t, query)

	carbon.SetTestNow(carbon.Parse("1995-01-01 00:00:00"))
	t.Cleanup(carbon.ClearTestNow)

	user := userWithCreatedAndUpdated{Email: "test@test.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))
	carbon.SetTestNow(carbon.Parse("1995-01-01 01:00:00"))

	// Laravel: $user->timestamps = false — the per-instance flag, which the Go model design carries
	// as the Disabled field on the embedded databaseorm.Timestamps.
	user.Disabled = true

	attached := query.Model(&user)
	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := attached.(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")
	logged := withContext.WithContext(ctx)

	assert.False(t, logged.UsesTimestamps())

	require.NoError(t, logged.WithoutTimestamps(func() error {
		assert.False(t, logged.UsesTimestamps())

		_, err := logged.Update(map[string]any{"email": "bar@example.com"})

		return err
	}))

	// The scope restores to the flag's own state — still ignoring.
	assert.False(t, logged.UsesTimestamps())

	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "the already-ignored update is one statement without updated_at")
	assert.Equal(t, `update "users" set "email" = 'bar@example.com' where "id" = 1`, logs[0].Query)

	var reloaded userWithCreatedAndUpdated
	require.NoError(t, cloneRelationQuery(t, query).Where("id", user.ID).First(&reloaded))
	assert.Equal(t, "bar@example.com", reloaded.Email)
	require.NotNil(t, reloaded.UpdatedAt)
	assert.Equal(t, "1995-01-01 00:00:00", reloaded.UpdatedAt.ToDateTimeString())
	assert.Equal(t, "bar@example.com", user.Email)
	require.NotNil(t, user.UpdatedAt)
	assert.Equal(t, "1995-01-01 00:00:00", user.UpdatedAt.ToDateTimeString())
}

func TestTimestamps_WithoutTimestampRestoresWhenClosureThrowsException(t *testing.T) {
	query := newRelationQuery(t)
	timestampsSchema(t, query)

	user := userWithCreatedAndUpdated{Email: "test@test.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	// Laravel: $user->timestamps = true;
	user.Disabled = false

	attached := query.Model(&user)

	// Laravel's closure throws a RuntimeException; the Go form of "the closure threw" is the
	// callback returning an error, which WithoutTimestamps restores around and returns unchanged.
	require.ErrorIs(t, attached.WithoutTimestamps(func() error {
		assert.False(t, attached.UsesTimestamps())

		return errTimestampsThrown
	}), errTimestampsThrown)

	// Laravel asserts $user->timestamps is true again afterwards; the Go flag reads back enabled and
	// the query reports the suspension lifted.
	assert.False(t, user.Disabled)
	assert.True(t, attached.UsesTimestamps())
}

func TestTimestamps_WithoutTimestampsRespectsClasses(t *testing.T) {
	// Laravel: Eloquent::withoutTimestamps() suspends every model's timestamps (oracle:
	// usesTimestamps() false inside, Eloquent::isIgnoringTimestamps(UserWithCreatedAndUpdated::class)
	// true), while withoutTimestampsOn() suspends only the listed classes. The Go suspension
	// (Query.WithoutTimestamps) suspends one query rather than a model class, and there is no
	// isIgnoringTimestamps()-style class list, so the class scoping cannot be reproduced.
	t.Skip(timestampsSkipClassSuspension)
}
