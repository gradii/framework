package gorm

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// These tests pin the struct write rule. A Go struct has no attribute bag and the ORM keeps no record
// of the values a model was loaded with (the implicit original-value tracking was removed by the
// user's decision), so Save reads the struct alone:
//
//   - a keyed Save updates the non-zero fields plus the automatic updated_at, by the key;
//   - a zero field is not written, because a Go zero cannot be told apart from an unset field;
//   - WithZero names columns that ARE written when zero, without narrowing the write; "*" names
//     every column, and Omit wins over it.
//
// The written-attribute rule follows Eloquent, which writes the attributes a model carries and an
// explicitly assigned null/false/0 among them (oracle illuminate/database v12.55.1, real sqlite,
// /Users/dog/.claude/jobs/013c27a0/tmp/r14-probes/probe_a.out U1-U6). WithZero is the Go way to say a
// zero was assigned; it has no Laravel counterpart.

type saveSemanticsUser struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	Name      string           `gorm:"column:name"`
	Age       int              `gorm:"column:age"`
	Email     *string          `gorm:"column:email"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (saveSemanticsUser) TableName() string { return "users" }

func newSaveSemanticsQuery(t *testing.T) (*OrmBuilder, *recordingSQLDriver) {
	t.Helper()

	recorder := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })

	conditions := Conditions{model: &saveSemanticsUser{}}

	return newDialectQuery(context.Background(), nil, contractsdatabase.Config{}, sqlDB, nil, nil, nil, nil, &conditions, nil), recorder
}

func stampNow(t *testing.T) {
	t.Helper()
	carbon.SetTestNow(carbon.Parse("2026-11-03 01:02:03"))
	t.Cleanup(carbon.ClearTestNow)
}

func TestSaveWritesOnlyNonZeroFields(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	require.NoError(t, query.Save(&saveSemanticsUser{ID: 1, Name: "a"}))

	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, recorder.last(t).sql)
}

func TestSaveRunsEveryTimeWithoutPriorState(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	user := &saveSemanticsUser{ID: 1, Name: "a"}
	require.NoError(t, query.Save(user))
	before := recorder.count()
	require.NoError(t, query.Save(user))

	assert.Equal(t, before+1, recorder.count(), "no prior state is consulted, so an unchanged model still updates")
}

func TestSaveWithZeroWritesTheNamedZeroColumnsWithoutNarrowing(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	require.NoError(t, query.WithZero("age", "Email").Save(&saveSemanticsUser{ID: 1, Name: "a"}))

	update := recorder.last(t)
	assert.Equal(t, `update "users" set "age" = ?, "email" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	assert.Equal(t, int64(0), update.args[0])
	assert.Nil(t, update.args[1])
}

func TestSaveWithZeroWildcardAndOmitPrecedence(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	require.NoError(t, query.WithZero("*").Omit("email").Save(&saveSemanticsUser{ID: 1}))

	assert.Equal(t, `update "users" set "age" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, recorder.last(t).sql)
}

func TestSaveStampsUpdatedAtEvenWhenTheModelCarriesOne(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	loaded := carbon.NewDateTime(carbon.Parse("2019-05-05 05:05:05"))
	require.NoError(t, query.Save(&saveSemanticsUser{ID: 1, Name: "a", UpdatedAt: loaded}))

	update := recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	stale, err := loaded.Value()
	require.NoError(t, err)
	assert.NotEqual(t, stale, update.args[1], "a carried updated_at could be the loaded one, so Save stamps")
}

func TestSaveSelectedUpdatedAtIsWrittenAsGiven(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	set := carbon.NewDateTime(carbon.Parse("2019-05-05 05:05:05"))
	require.NoError(t, query.Select("name", "updated_at").Save(&saveSemanticsUser{ID: 1, Name: "a", UpdatedAt: set}))

	update := recorder.last(t)
	want, err := set.Value()
	require.NoError(t, err)
	assert.Equal(t, want, update.args[1])
}

func TestCreateWithZeroWritesTheNamedZeroColumn(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	require.NoError(t, query.WithZero("age").Create(&saveSemanticsUser{Name: "a"}))
	sqlText := recorder.last(t).sql
	assert.Contains(t, sqlText, `"age"`)
	assert.NotContains(t, sqlText, `"email"`)
	assert.NotContains(t, sqlText, `"id"`, "a zero sole key stays out unless named explicitly")

	fresh, recorder2 := newSaveSemanticsQuery(t)
	require.NoError(t, fresh.Create(&saveSemanticsUser{Name: "b"}))
	assert.NotContains(t, recorder2.last(t).sql, `"age"`, "without WithZero a zero field stays out")
}

func TestToSqlSaveMatchesTheExecutedStatement(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	user := &saveSemanticsUser{ID: 1, Name: "a"}
	preview := query.WithZero("age").(*OrmBuilder).ToSql().Save(user)
	before := recorder.count()
	assert.Equal(t, `update "users" set "age" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, preview)
	assert.Equal(t, before, recorder.count(), "previewing must not write")

	require.NoError(t, query.WithZero("age").Save(user))
	assert.Equal(t, preview, recorder.last(t).sql)
}

var _ contractsorm.Query = (*OrmBuilder)(nil)

func TestSaveWithZeroUpdatedAtClearsInsteadOfStamping(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)
	stampNow(t)

	require.NoError(t, query.WithZero("updated_at").Save(&saveSemanticsUser{ID: 1, Name: "a"}))
	assert.Nil(t, recorder.last(t).args[1])

	stamped, recorder2 := newSaveSemanticsQuery(t)
	require.NoError(t, stamped.WithZero("*").Save(&saveSemanticsUser{ID: 1, Name: "a"}))
	assert.NotNil(t, recorder2.last(t).args[len(recorder2.last(t).args)-2], "the wildcard never clears the automatic timestamp")
}
