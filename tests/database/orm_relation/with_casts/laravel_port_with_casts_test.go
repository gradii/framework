package with_casts

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentWithCastsTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestWithCasts_<method without the test
// prefix>. The corpus's setUp creates two tables — times and unique_times, each an auto-incrementing
// id, a time column, and timestamps() — mirrored by withCastsSchema; rows are seeded through the ORM
// and read back through the same firstOrNew/firstOrCreate calls the corpus makes.
//
// # The withCasts(['time' => 'string']) override, in a typed model
//
// Every corpus chain starts with withCasts(['time' => 'string']). In Laravel this merges a cast into
// the builder's model (Builder::withCasts calls mergeCasts, and Model::newInstance merges the
// builder model's casts into the instance it makes), overriding the Time model's default datetime
// cast so the value the test reads back is the raw '07:30' string. A typed Go model has no attribute
// bag and no per-query cast layer, and the two value paths disagree on a plain string field:
//
//   - A not-found firstOrNew fills the model from the attributes map, and a plain string field takes
//     '07:30' unchanged.
//   - A found row is decoded by the sqlite engine, which hands a column declared "time" back as
//     time.Time on the 2000-01-01 base date (measured: '07:30' returns as
//     time.Date(2000, time.January, 1, 7, 30, 0, 0, time.UTC)), and the scan layer then refuses to
//     store a time.Time into a string field ("cannot scan value ... of type time.Time into string").
//
// The model therefore declares the column as withCastsTimeOfDay, a named string whose Scan turns the
// engine's time.Time back into the stored HH:MM text and whose Value writes the raw text: the
// typed-model equivalent of the corpus's string cast. Under it, each port observes exactly the
// '07:30' the corpus observes.
//
// # Oracle measurements
//
// Every SQL expectation this file cites was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled — each
// corpus method replayed in isolation:
//
//   - testWithFirstOrNew compiles: select * from "times" where ("time" = ?) limit 1 (miss),
//     insert into "times" ("time") values (?) (the row's created_at/updated_at stay NULL, as the
//     oracle's raw row shows), then the same select (hit, id 1).
//   - testWithFirstOrCreate compiles: the select (miss), the save() insert
//     insert into "times" ("time", "updated_at", "created_at") values (?, ?, ?), then the select
//     (hit); both instances carry id 1.
//   - testWithCreateOrFirst compiles: insert into "unique_times" ("time", "updated_at",
//     "created_at") values (?, ?, ?) first, then — after the unique index rejects the second
//     insert — select * from "unique_times" where ("time" = ?) limit 1; both instances carry id 1.
//
// and the setUp schema compiles to
//
//	create table "times" ("id" integer primary key autoincrement not null, "time" time not null,
//	  "created_at" datetime, "updated_at" datetime)
//	create table "unique_times" (... the same columns ...)
//	create unique index "unique_times_time_unique" on "unique_times" ("time")

// errWithCastsTimeOfDayScan reports a driver value the string cast has no counterpart for.
var errWithCastsTimeOfDayScan = errors.New("withCastsTimeOfDay cannot scan the driver value")

// withCastsTimeOfDay is the time column of a typed model under the corpus's string cast: the sqlite
// engine decodes the stored HH:MM text to a time.Time on the 2000-01-01 base date, and this type
// turns it back into the text the corpus asserts (and writes the raw text back on insert).
type withCastsTimeOfDay string

func (t *withCastsTimeOfDay) Scan(value any) error {
	switch v := value.(type) {
	case time.Time:
		// The corpus stores minute-precision text ('07:30'); restore it, keeping any decoded
		// seconds.
		*t = withCastsTimeOfDay(strings.TrimSuffix(v.Format("15:04:05"), ":00"))
	case string:
		*t = withCastsTimeOfDay(v)
	case []byte:
		*t = withCastsTimeOfDay(v)
	case nil:
		*t = ""
	default:
		return errWithCastsTimeOfDayScan
	}

	return nil
}

// Value binds the raw text, so the stored column stays the corpus's '07:30'.
func (t withCastsTimeOfDay) Value() (driver.Value, error) {
	return string(t), nil
}

// withCastsTime ports the corpus's Time model: the time column over times, with the framework's
// automatic timestamp fields standing in for Eloquent's save() stamping.
type withCastsTime struct {
	ID        uint               `gorm:"column:id;primaryKey;autoIncrement"`
	Time      withCastsTimeOfDay `gorm:"column:time"`
	CreatedAt *carbon.DateTime   `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime   `gorm:"autoUpdateTime;column:updated_at"`
}

func (withCastsTime) TableName() string { return "times" }

// withCastsUniqueTime ports the corpus's UniqueTime: the same model over unique_times, whose time
// column carries a unique index.
type withCastsUniqueTime struct {
	ID        uint               `gorm:"column:id;primaryKey;autoIncrement"`
	Time      withCastsTimeOfDay `gorm:"column:time"`
	CreatedAt *carbon.DateTime   `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime   `gorm:"autoUpdateTime;column:updated_at"`
}

func (withCastsUniqueTime) TableName() string { return "unique_times" }

// withCastsSchema mirrors the corpus's createSchema() in sqlite DDL; the oracle compiles Laravel's
// version to the same two tables plus create unique index "unique_times_time_unique" on
// "unique_times" ("time").
func withCastsSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table times (id integer primary key autoincrement, time time not null, created_at datetime null, updated_at datetime null)`,
		`create table unique_times (id integer primary key autoincrement, time time not null, created_at datetime null, updated_at datetime null)`,
		`create unique index unique_times_time_unique on unique_times (time)`,
	)
}

func TestWithCasts_WithFirstOrNew(t *testing.T) {
	query := newRelationQuery(t)
	withCastsSchema(t, query)

	// Laravel: Time::query()->withCasts(['time' => 'string'])->firstOrNew(['time' => '07:30']) on the
	// empty table — oracle: select * from "times" where ("time" = ?) limit 1. Not found, so the
	// instance carries the attributes it was looked up with.
	time1 := withCastsTime{}
	require.NoError(t, cloneRelationQuery(t, query).FirstOrNew(&time1, map[string]any{"time": "07:30"}))

	// Laravel: Time::query()->insert(['time' => '07:30']) — oracle: insert into "times" ("time")
	// values (?); a map Create writes just the map's columns, so the row's stamps stay NULL like the
	// oracle's raw row.
	require.NoError(t, cloneRelationQuery(t, query).Model(&withCastsTime{}).Create(map[string]any{"time": "07:30"}))

	// The same firstOrNew now finds the stored row — oracle: select * from "times" where
	// ("time" = ?) limit 1, one row.
	time2 := withCastsTime{}
	require.NoError(t, cloneRelationQuery(t, query).FirstOrNew(&time2, map[string]any{"time": "07:30"}))
	require.NotZero(t, time2.ID, "the second firstOrNew must find the inserted row for the read to happen")

	// Laravel: assertSame('07:30', $time1->time) — the not-found instance carries the attribute.
	assert.Equal(t, "07:30", string(time1.Time))
	// Laravel: assertSame($time1->time, $time2->time) — the found instance reads the same value.
	assert.Equal(t, time1.Time, time2.Time)
}

func TestWithCasts_WithFirstOrCreate(t *testing.T) {
	query := newRelationQuery(t)
	withCastsSchema(t, query)

	// Laravel: Time::query()->withCasts(['time' => 'string'])->firstOrCreate(['time' => '07:30']),
	// twice. The first call misses (oracle: select ... limit 1) and saves (oracle: insert into
	// "times" ("time", "updated_at", "created_at") values (?, ?, ?)); the second finds the row the
	// first one created (oracle: select ... limit 1) and must carry the same id.
	time1 := withCastsTime{}
	require.NoError(t, cloneRelationQuery(t, query).FirstOrCreate(&time1, map[string]any{"time": "07:30"}))

	time2 := withCastsTime{}
	require.NoError(t, cloneRelationQuery(t, query).FirstOrCreate(&time2, map[string]any{"time": "07:30"}))

	require.NotZero(t, time1.ID, "the first firstOrCreate must have created a row")
	assert.Equal(t, time1.ID, time2.ID)
}

func TestWithCasts_WithCreateOrFirst(t *testing.T) {
	query := newRelationQuery(t)
	withCastsSchema(t, query)

	// Laravel: UniqueTime::query()->withCasts(['time' => 'string'])->createOrFirst(['time' =>
	// '07:30']), twice. Oracle (/tmp/cof_withcasts_probe.php, real sqlite): the first call runs
	// insert into "unique_times" ("time", "updated_at", "created_at") values (?, ?, ?) — createOrFirst
	// inserts first, no lookup — and the second's insert is rejected by the unique index, so it
	// re-selects with select * from "unique_times" where ("time" = ?) limit 1; time1 is
	// wasRecentlyCreated true, time2 false, both carry id 1 and one row is stored.
	time1 := withCastsUniqueTime{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&time1).CreateOrFirst(&time1, map[string]any{"time": "07:30"}))

	time2 := withCastsUniqueTime{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&time2).CreateOrFirst(&time2, map[string]any{"time": "07:30"}))

	require.NotZero(t, time1.ID, "the first createOrFirst must have created a row")
	// Laravel: assertSame($time1->id, $time2->id).
	assert.Equal(t, time1.ID, time2.ID)
	assert.Equal(t, "07:30", string(time2.Time), "the re-selected row reads back under the string cast")

	count, err := cloneRelationQuery(t, query).Model(&withCastsUniqueTime{}).Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "the rejected second insert must not leave a row")
}

func TestWithCasts_ThrowsExceptionIfCastableAttributeWasNotRetrievedAndPreventMissingAttributesIsEnabled(t *testing.T) {
	// Laravel: after Model::preventAccessingMissingAttributes(), reading $time->time on a model
	// loaded with select('id')->first() throws MissingAttributeException.
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts that reading a castable attribute that was not retrieved throws MissingAttributeException under Model::preventAccessingMissingAttributes(); a typed Go model has no attribute bag — an unretrieved column is simply the field's zero value")
}
