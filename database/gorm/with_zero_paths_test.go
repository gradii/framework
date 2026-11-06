package gorm

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// These tests pin the write paths round 15a's verification of 6ebc1dbf (the removal of the implicit
// original-value tracking, user decision 2026-11-06) found outside Save's column set: a Save with
// nothing to write, Update with a struct, the unstamped automatic timestamps of an insert, and the
// empty-attribute guard of a keyless insert. Each follows the struct write rule — a zero field is an
// unset attribute unless WithZero (or Select) names it — and the Eloquent behaviour the oracle
// measured (illuminate/database v12.55.1, real sqlite, /tmp/r15v/probe_v.php -> probe_v.out).

// withZeroEventsModel records the model events a write fires; it carries no timestamps, so a keyed
// Save of a zero-valued instance has nothing at all to write.
type withZeroEventsModel struct {
	ID   uint   `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (withZeroEventsModel) TableName() string { return "events_models" }

var withZeroEventLog []string

func (withZeroEventsModel) DispatchesEvents() map[contractsorm.EventType]func(contractsorm.Event) error {
	record := func(name string) func(contractsorm.Event) error {
		return func(contractsorm.Event) error {
			withZeroEventLog = append(withZeroEventLog, name)

			return nil
		}
	}

	return map[contractsorm.EventType]func(contractsorm.Event) error{
		contractsorm.EventSaving:   record("saving"),
		contractsorm.EventUpdating: record("updating"),
		contractsorm.EventUpdated:  record("updated"),
		contractsorm.EventSaved:    record("saved"),
	}
}

// withZeroKeylessRow is a non-incrementing model: no primary key and no timestamps, the shape whose
// empty insert Eloquent's performInsert skips.
type withZeroKeylessRow struct {
	Flag  bool   `gorm:"column:flag"`
	Label string `gorm:"column:label"`
}

func (withZeroKeylessRow) TableName() string { return "flags" }

func newWithZeroQuery(t *testing.T, model any) (*OrmBuilder, *recordingSQLDriver) {
	t.Helper()

	recorder := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })

	conditions := Conditions{model: model}

	return newDialectQuery(context.Background(), nil, contractsdatabase.Config{}, sqlDB, nil, nil, nil, nil, &conditions, nil), recorder
}

// Model::save runs performUpdate only for a model with something to write; the clean branch fires
// saving and saved alone and writes nothing (oracle P4: no statement, events ["saving","saved"]; P4b:
// a dirty save fires ["saving","updating","updated","saved"]). With no attribute bag the Go form of
// "nothing to write" is an empty Save column set.
func TestSaveWithNothingToWriteFiresNoUpdatingAndRunsNoStatement(t *testing.T) {
	query, recorder := newWithZeroQuery(t, &withZeroEventsModel{})
	withZeroEventLog = nil
	t.Cleanup(func() { withZeroEventLog = nil })

	require.NoError(t, query.Save(&withZeroEventsModel{ID: 1}))
	assert.Equal(t, 0, recorder.count(), "a keyed model with no non-zero field and no WithZero writes nothing")
	assert.Equal(t, []string{"saving", "saved"}, withZeroEventLog)

	withZeroEventLog = nil
	dirty, recorder := newWithZeroQuery(t, &withZeroEventsModel{})
	require.NoError(t, dirty.Save(&withZeroEventsModel{ID: 1, Name: "a"}))
	assert.Equal(t, `update "events_models" set "name" = ? where "id" = ?`, recorder.last(t).sql)
	assert.Equal(t, []string{"saving", "updating", "updated", "saved"}, withZeroEventLog)

	withZeroEventLog = nil
	named, recorder := newWithZeroQuery(t, &withZeroEventsModel{})
	require.NoError(t, named.WithZero("name").Save(&withZeroEventsModel{ID: 1}))
	assert.Equal(t, `update "events_models" set "name" = ? where "id" = ?`, recorder.last(t).sql)
	assert.Equal(t, []string{"saving", "updating", "updated", "saved"}, withZeroEventLog, "a WithZero column is something to write")
}

// finishSave touches the owners only after a save that wrote (`if ($this->isDirty() && ...)`;
// oracle P4: no owner update on the clean save, one on the dirty save P4b). The post's only column
// is omitted here, so the save has nothing to write and the touched user is left alone.
func TestSaveWithNothingToWriteDoesNotTouchOwners(t *testing.T) {
	query, recorder := newWithZeroQuery(t, &touchingUnitPost{})

	require.NoError(t, query.Omit("user_id").Save(&touchingUnitPost{ID: 1, UserID: 2}))
	assert.Equal(t, 0, recorder.count(), "no update and no owner touch")

	written, recorder := newWithZeroQuery(t, &touchingUnitPost{})
	require.NoError(t, written.Save(&touchingUnitPost{ID: 1, UserID: 2}))
	require.NotZero(t, recorder.count())
	assert.Equal(t, `update "touching_posts" set "user_id" = ? where "id" = ?`, recorder.at(t, 0).sql)
	var statements []string
	for index := 1; index < recorder.count(); index++ {
		statements = append(statements, recorder.at(t, index).sql)
	}
	assert.Contains(t, statements, `update "touching_users" set "updated_at" = ? where "touching_users"."id" = ?`, "a save that wrote touches the owner")
}

// Update with a struct follows the struct write rule Create and Save share: a zero field stays out
// unless WithZero names it.
func TestUpdateWithAStructHonoursWithZero(t *testing.T) {
	stampNow(t)

	plain, recorder := newWithZeroQuery(t, &saveSemanticsUser{})
	_, err := plain.Where("id", 1).Update(saveSemanticsUser{Name: "a"})
	require.NoError(t, err)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, recorder.last(t).sql)

	named, recorder := newWithZeroQuery(t, &saveSemanticsUser{})
	_, err = named.Where("id", 1).WithZero("age", "Email").Update(saveSemanticsUser{Name: "a"})
	require.NoError(t, err)
	update := recorder.last(t)
	assert.Equal(t, `update "users" set "age" = ?, "email" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	assert.Equal(t, int64(0), update.args[0])
	assert.Nil(t, update.args[1])
}

// A WithoutTimestamps insert leaves an unset automatic timestamp to the database default; WithZero
// naming the column writes it all the same, as SQL NULL.
func TestCreateWithoutTimestampsWritesAWithZeroTimestampAsNull(t *testing.T) {
	plain, recorder := newWithZeroQuery(t, &saveSemanticsUser{})
	require.NoError(t, plain.WithoutTimestamps(func() error { return plain.Create(&saveSemanticsUser{Name: "a"}) }))
	assert.Equal(t, `insert into "users" ("name") values (?)`, recorder.last(t).sql)

	named, recorder := newWithZeroQuery(t, &saveSemanticsUser{})
	require.NoError(t, named.WithZero("updated_at").WithoutTimestamps(func() error {
		return named.Create(&saveSemanticsUser{Name: "a"})
	}))
	insert := recorder.last(t)
	assert.Equal(t, `insert into "users" ("name", "updated_at") values (?, ?)`, insert.sql)
	assert.Nil(t, insert.args[1])
}

// Eloquent's performInsert skips the insert of a non-incrementing model with no attributes, but an
// assigned false is an attribute: create(['flag' => false]) inserts it (oracle P3), while create([])
// writes nothing (P3b). WithZero is the Go way to say the false was assigned.
func TestCreateKeylessRowWithAWithZeroFalseIsNotSkipped(t *testing.T) {
	plain, recorder := newWithZeroQuery(t, &withZeroKeylessRow{})
	require.NoError(t, plain.Create(&withZeroKeylessRow{}))
	assert.Equal(t, 0, recorder.count(), "an all-zero keyless row carries no attribute")

	named, recorder := newWithZeroQuery(t, &withZeroKeylessRow{})
	require.NoError(t, named.WithZero("flag").Create(&withZeroKeylessRow{}))
	require.Equal(t, 1, recorder.count(), "the assigned false is an attribute, so the insert runs")
	insert := recorder.last(t)
	assert.Equal(t, `insert into "flags" ("flag") values (?)`, insert.sql)
	assert.Equal(t, false, insert.args[0])
}

// withZeroStampedUser carries both automatic timestamps.
type withZeroStampedUser struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	Name      string           `gorm:"column:name"`
	Age       int              `gorm:"column:age"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (withZeroStampedUser) TableName() string { return "users" }

// The "*" wildcard names every column but the automatic timestamps: a zero created_at on a hand-built
// keyed model stands for a timestamp the struct does not carry, so a wildcard Save must not clear the
// stored one, the rule updated_at already follows. Naming the column explicitly writes NULL.
func TestWithZeroWildcardLeavesTheAutomaticTimestampsAlone(t *testing.T) {
	stampNow(t)

	wildcard, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	require.NoError(t, wildcard.WithZero("*").Save(&withZeroStampedUser{ID: 1, Name: "a"}))
	update := recorder.last(t)
	assert.Equal(t, `update "users" set "age" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, update.sql, "the wildcard must not write created_at = NULL")
	assert.NotNil(t, update.args[2])

	named, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	require.NoError(t, named.WithZero("*", "created_at").Save(&withZeroStampedUser{ID: 1, Name: "a"}))
	update = recorder.last(t)
	assert.Equal(t, `update "users" set "age" = ?, "created_at" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	assert.Nil(t, update.args[1], "an explicitly named zero created_at is written as NULL")

	updated, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	_, err := updated.Where("id", 1).WithZero("*").Update(withZeroStampedUser{Name: "a"})
	require.NoError(t, err)
	assert.Equal(t, `update "users" set "age" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, recorder.last(t).sql, "Update with a struct follows the same wildcard rule")

	inserted, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	require.NoError(t, inserted.WithZero("*").WithoutTimestamps(func() error {
		return inserted.Create(&withZeroStampedUser{Name: "a"})
	}))
	assert.Equal(t, `insert into "users" ("age", "name") values (?, ?)`, recorder.last(t).sql, "an unstamped insert leaves the timestamps to the database under the wildcard")
}

// With the stamping withheld (WithoutTimestamps, or the model's own flag) Eloquent's performUpdate
// skips updateTimestamps and writes the updated_at the model carries like any other attribute: an
// assigned null (oracle /tmp/r15vv/probe_ts.out T1: `update "users" set "id" = ?, "name" = ?,
// "updated_at" = ? where "id" = ?` with null) or an assigned value (T6, the value bound). A keyed Save
// therefore follows the struct write rule for it: a non-zero updated_at is written, a zero one only
// when WithZero names it outright (as NULL), and never through the "*" wildcard.
func TestSaveWithoutTimestampsWritesTheCarriedUpdatedAt(t *testing.T) {
	named, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	require.NoError(t, named.WithZero("updated_at").WithoutTimestamps(func() error {
		return named.Save(&withZeroStampedUser{ID: 1, Name: "a"})
	}))
	update := recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql, "WithZero names the zero updated_at")
	assert.Nil(t, update.args[1])

	carried, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	at := carbon.NewDateTime(carbon.FromDateTime(2020, 1, 2, 3, 4, 5))
	require.NoError(t, carried.WithoutTimestamps(func() error {
		return carried.Save(&withZeroStampedUser{ID: 1, Name: "a", UpdatedAt: at})
	}))
	update = recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql, "a carried updated_at is written as it is")
	assert.NotNil(t, update.args[1])

	wildcard, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	require.NoError(t, wildcard.WithZero("*").WithoutTimestamps(func() error {
		return wildcard.Save(&withZeroStampedUser{ID: 1, Name: "a"})
	}))
	assert.Equal(t, `update "users" set "age" = ?, "name" = ? where "id" = ?`, recorder.last(t).sql, "the wildcard does not reach the automatic timestamps")
}

// Builder::update keeps a caller's updated_at over the fresh stamp (addUpdatedAtColumn merges the
// caller's values last), with or without timestamps (oracle /tmp/r15vv/probe_ts.out T5 and T3:
// `update "users" set "name" = ?, "updated_at" = ? where "id" = ?` with null). For Update with a
// struct, WithZero naming the zero updated_at outright is that null; without it the zero is unset
// and the stamp applies while the model uses timestamps.
func TestUpdateWithAStructWritesAWithZeroUpdatedAtAsNull(t *testing.T) {
	stampNow(t)

	stamped, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	_, err := stamped.Where("id", 1).WithZero("updated_at").Update(withZeroStampedUser{Name: "a"})
	require.NoError(t, err)
	update := recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	assert.Nil(t, update.args[1], "the named zero is the caller's null, not stamped over")

	plain, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	_, err = plain.Where("id", 1).Update(withZeroStampedUser{Name: "a"})
	require.NoError(t, err)
	assert.NotNil(t, recorder.last(t).args[1], "an unnamed zero updated_at is stamped")

	unstamped, recorder := newWithZeroQuery(t, &withZeroStampedUser{})
	require.NoError(t, unstamped.WithZero("updated_at").WithoutTimestamps(func() error {
		_, err := unstamped.Where("id", 1).Update(withZeroStampedUser{Name: "a"})

		return err
	}))
	update = recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	assert.Nil(t, update.args[1])
}

// withZeroTimeStampedUser carries its automatic timestamps as plain time.Time values, whose zero would
// bind as a '0001-01-01' time rather than NULL.
type withZeroTimeStampedUser struct {
	ID        uint      `gorm:"column:id;primaryKey"`
	Name      string    `gorm:"column:name"`
	UpdatedAt time.Time `gorm:"autoUpdateTime;column:updated_at"`
}

func (withZeroTimeStampedUser) TableName() string { return "users" }

// A WithZero-named zero updated_at a keyed Save writes with the stamping withheld binds SQL NULL, the
// null Eloquent writes (oracle /tmp/r15vv/probe_ts.out T1), never the zero time.Time.
func TestSaveWithoutTimestampsBindsAWithZeroUpdatedAtAsNull(t *testing.T) {
	query, recorder := newWithZeroQuery(t, &withZeroTimeStampedUser{})
	require.NoError(t, query.WithZero("updated_at").WithoutTimestamps(func() error {
		return query.Save(&withZeroTimeStampedUser{ID: 1, Name: "a"})
	}))
	update := recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	assert.Nil(t, update.args[1], "the zero time.Time is written as NULL")

	// Update with a struct binds the same NULL, the caller's null Builder::update keeps (T5).
	updated, recorder := newWithZeroQuery(t, &withZeroTimeStampedUser{})
	_, err := updated.Where("id", 1).WithZero("updated_at").Update(withZeroTimeStampedUser{Name: "a"})
	require.NoError(t, err)
	update = recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	assert.Nil(t, update.args[1], "Update with a struct writes the zero time.Time as NULL")
}
