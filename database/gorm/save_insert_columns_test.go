package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/database/types"
	"github.com/gradii/framework/support/carbon"
)

// This file pins the INSERT column set Save/Create send for a struct model — Eloquent's
// getAttributesForInsert (HasAttributes.php:2027 -> getAttributes :2015), the $attributes bag a
// caller fills by assignment: an insert carries only the attributes the model holds, plus the
// automatic timestamps, never a column the model does not carry. A Go struct has no attribute bag,
// so, as everywhere else in this port, a field left at its zero value stands for an attribute the
// model does not carry and is omitted, while a non-zero field — or a non-nil pointer to a zero, the
// typed presence marker — is carried; a Select list, or the map form, carries an explicitly assigned
// zero. Oracle v12.55.1, executed (/tmp/r13/insert_columns_probe.php), against a twelve-column users
// table:
//
//	I1 new model, name + email set   insert into "probe_users" ("name", "email", "updated_at", "created_at") values (...)  ["x","e@x",<stamp>,<stamp>]
//	I3 name + explicit score 0       insert into "probe_users" ("name", "score", "updated_at", "created_at") values (...)   ["z",0,<stamp>,<stamp>]
//	I8 new model, nothing set        insert into "probe_users" ("updated_at", "created_at") values (...)                     [<stamp>,<stamp>]
//
// The port orders a record's columns by name, so the same set renders alphabetically (Laravel keeps
// the bag's insertion order). The UPDATE path is deliberately untouched: a model with no captured
// original bag still updates every non-key field (save_semantics_test.go's
// TestSaveSemanticsWritesEveryColumnWithoutACapturedBag), the round-6 whole-struct reading.

// insertColumnsUser carries a nullable pointer column, two string columns, a bool and an int left at
// their zeros, so the set/omitted split is observable in one model.
type insertColumnsUser struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	Name      string           `gorm:"column:name"`
	Nickname  string           `gorm:"column:nickname"`
	Email     *string          `gorm:"column:email"`
	IsAdmin   bool             `gorm:"column:is_admin"`
	Score     int              `gorm:"column:score"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (insertColumnsUser) TableName() string { return "users" }

func freezeInsertColumnsNow(t *testing.T) {
	t.Helper()

	carbon.SetTestNow(carbon.Parse("2026-11-03 01:02:03"))
	t.Cleanup(carbon.ClearTestNow)
}

// TestInsertColumns_ListsOnlyTheSetFieldsAndTimestamps pins the primary case: a model with two of its
// columns set, the rest left at their zeros, inserts the two set fields plus the stamped timestamps.
func TestInsertColumns_ListsOnlyTheSetFieldsAndTimestamps(t *testing.T) {
	freezeInsertColumnsNow(t)

	query, recorder := newRecordingRelQuery(t, &insertColumnsUser{})
	user := insertColumnsUser{Name: "x", Nickname: "y"}
	require.NoError(t, query.Create(&user))

	statement := recorder.last(t)
	assert.Equal(t, `insert into "users" ("created_at", "name", "nickname", "updated_at") values (?, ?, ?, ?)`, statement.sql)
	require.Len(t, statement.args, 4)
	assert.Equal(t, "x", statement.args[1])
	assert.Equal(t, "y", statement.args[2])
	require.NotNil(t, user.CreatedAt, "the insert stamps the automatic timestamps")
}

// TestInsertColumns_NothingSetListsOnlyTheTimestamps pins the empty-attribute-bag shape: a freshly
// allocated model carries no attribute at all, so the insert lists only the stamped timestamps.
func TestInsertColumns_NothingSetListsOnlyTheTimestamps(t *testing.T) {
	freezeInsertColumnsNow(t)

	query, recorder := newRecordingRelQuery(t, &insertColumnsUser{})
	recorder.returnLastInsertID(1)

	user := insertColumnsUser{}
	require.NoError(t, query.Create(&user))

	statement := recorder.last(t)
	assert.Equal(t, `insert into "users" ("created_at", "updated_at") values (?, ?)`, statement.sql)
	require.Len(t, statement.args, 2)
	assert.Equal(t, uint(1), user.ID, "the generated key is still read back on the timestamp-only insert")
}

// TestInsertColumns_NonNullPointerCarriesAnExplicitZero pins the typed presence marker: a non-nil
// pointer to the zero value is an attribute the model carries — Eloquent's `$u->email = null`, a
// value setAttribute records — so it joins the insert where a plain zero field would not. The plain
// IsAdmin=false / Score=0 fields on the same model are omitted.
func TestInsertColumns_NonNullPointerCarriesAnExplicitZero(t *testing.T) {
	freezeInsertColumnsNow(t)

	query, recorder := newRecordingRelQuery(t, &insertColumnsUser{})
	empty := ""
	user := insertColumnsUser{Name: "z", Email: &empty}
	require.NoError(t, query.Create(&user))

	statement := recorder.last(t)
	assert.Equal(t, `insert into "users" ("created_at", "email", "name", "updated_at") values (?, ?, ?, ?)`, statement.sql)
	require.Len(t, statement.args, 4)
	assert.Equal(t, "", statement.args[1])
}

// TestInsertColumns_ExplicitSelectWritesTheNamedZeroColumn pins the column-scoped write: a column
// named by Select is written whatever it holds, so Select("Score") carries an explicit zero the
// unselected shape would omit.
func TestInsertColumns_ExplicitSelectWritesTheNamedZeroColumn(t *testing.T) {
	freezeInsertColumnsNow(t)

	query, recorder := newRecordingRelQuery(t, &insertColumnsUser{})
	user := insertColumnsUser{Name: "x"}
	require.NoError(t, query.Select("Score").Create(&user))

	statement := recorder.last(t)
	assert.Equal(t, `insert into "users" ("score") values (?)`, statement.sql)
	assert.Equal(t, []driver.Value{int64(0)}, statement.args)
}

// TestInsertColumns_WildcardKeepsEveryZeroColumn pins Select("*"): a wildcard selects every field,
// including those holding a zero value, so the insert carries them all.
func TestInsertColumns_WildcardKeepsEveryZeroColumn(t *testing.T) {
	freezeInsertColumnsNow(t)

	query, recorder := newRecordingRelQuery(t, &insertColumnsUser{})
	user := insertColumnsUser{Name: "x"}
	require.NoError(t, query.Select("*").Create(&user))

	statement := recorder.last(t)
	for _, column := range []string{"created_at", "email", "is_admin", "name", "nickname", "score", "updated_at"} {
		assert.Contains(t, statement.sql, `"`+column+`"`, "a wildcard keeps every column including %s", column)
	}
}

// TestInsertColumns_SaveOnANewStructSharesTheInsertColumnSet pins the shape candidate 40 observed:
// Save of a keyless struct goes through the same insert column set, not the whole struct.
func TestInsertColumns_SaveOnANewStructSharesTheInsertColumnSet(t *testing.T) {
	freezeInsertColumnsNow(t)

	query, recorder := newRecordingRelQuery(t, &insertColumnsUser{})
	user := insertColumnsUser{Name: "s"}
	require.NoError(t, query.Save(&user))

	statement := recorder.last(t)
	assert.Equal(t, `insert into "users" ("created_at", "name", "updated_at") values (?, ?, ?)`, statement.sql)
}

// TestInsertColumns_BatchWritesEachRowItsOwnColumnSet pins the batch shape: execution inserts a row
// at a time, so each record carries its own columns (the union belongs to the reported statement
// only, compileInsertAll/uniformInsertRecords).
func TestInsertColumns_BatchWritesEachRowItsOwnColumnSet(t *testing.T) {
	freezeInsertColumnsNow(t)

	query, recorder := newRecordingRelQuery(t, &insertColumnsUser{})
	batch := []insertColumnsUser{{Name: "a"}, {Name: "b", Nickname: "n"}}
	require.NoError(t, query.Create(&batch))

	require.Equal(t, 2, recorder.count())
	assert.Equal(t, `insert into "users" ("created_at", "name", "updated_at") values (?, ?, ?)`, recorder.at(t, 0).sql)
	assert.Equal(t, `insert into "users" ("created_at", "name", "nickname", "updated_at") values (?, ?, ?, ?)`, recorder.at(t, 1).sql)
}

// insertColumnsDocument carries a JSON column, whose value binds through a jsonColumnValuer wrapper.
type insertColumnsDocument struct {
	ID      uint       `gorm:"column:id;primaryKey"`
	Name    string     `gorm:"column:name"`
	Payload types.JSON `gorm:"column:payload;type:json"`
}

func (insertColumnsDocument) TableName() string { return "documents" }

// TestInsertColumns_JSONColumnFollowsTheSameZeroRule pins that a JSON column obeys the same zero
// rule as a plain field: a bound jsonColumnValuer must not read as present, so a zero column is
// omitted, and a set one is carried.
func TestInsertColumns_JSONColumnFollowsTheSameZeroRule(t *testing.T) {
	query, recorder := newRecordingRelQuery(t, &insertColumnsDocument{})
	require.NoError(t, query.Create(&insertColumnsDocument{Name: "a"}))
	assert.Equal(t, `insert into "documents" ("name") values (?)`, recorder.last(t).sql)

	query, recorder = newRecordingRelQuery(t, &insertColumnsDocument{})
	require.NoError(t, query.Create(&insertColumnsDocument{Name: "b", Payload: types.JSON(`{"k":1}`)}))
	assert.Equal(t, `insert into "documents" ("name", "payload") values (?, ?)`, recorder.last(t).sql)
}
