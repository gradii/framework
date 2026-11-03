package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	"github.com/gradii/framework/support/carbon"
)

// These tests pin Save's Eloquent alignment (Model::save / performUpdate / finishSave,
// illuminate/database v12.55.1) at the unit level:
//
//   - the update carries only the attributes that changed since the model was loaded, plus
//     updated_at stamped at save time — never the whole struct, never the loaded timestamp;
//   - a model whose attributes all read back unchanged runs no statement at all and leaves its
//     stored updated_at alone (the save() gate `$saved = $this->isDirty() ? ... : true`);
//   - a model whose values were never captured — built by hand — writes every column, the empty
//     original bag reading every attribute as dirty.
//
// Oracle receipts, executed against illuminate/database v12.55.1 with real sqlite
// (/tmp/save11/semantics_probe.php, /tmp/save11/events_probe.php; each run prints
// "illuminate/database: v12.55.1" on stderr):
//
//	dirty subset   update "s11_users" set "name" = ?, "updated_at" = ? where "id" = ?
//	               ["new","2026-11-03 01:02:03",1]   (stamped at save time, loaded value 2020-01-01)
//	clean model    no statement at all; stored updated_at stays 2020-01-01 00:00:00
//	caller value   set "updated_at" = ? -> ["2019-05-05 05:05:05"] (no re-stamp, alone or with name)
//	loaded NULL    stamped on a dirty save: ["null-stamp","2026-11-03 01:02:03",1]
//	hand-built     set "id" = ?, "name" = ?, "updated_at" = ? where "id" = ? (exists=true shape;
//	               the port's primary key never joins the update, see saveAssignments)
//	second save    after a dirty save runs nothing (finishSave synced the original bag)
//
// The Go-side captures behind this are documented in model_original.go; the module-level suites
// (tests/database/orm_relation) carry the same shapes through a real sqlite database.

// saveSemanticsUser carries a nullable column and a stamped updated_at, so the dirty set, the
// untouched columns and the stamp are all observable in one model.
type saveSemanticsUser struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	Name      string           `gorm:"column:name"`
	Email     *string          `gorm:"column:email"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (saveSemanticsUser) TableName() string { return "users" }

// newSaveSemanticsQuery builds a query over a recording pool, the same shape newGetColumnsQuery
// uses: reads are served from canned result sets, writes are recorded.
func newSaveSemanticsQuery(t *testing.T) (*OrmBuilder, *recordingSQLDriver) {
	t.Helper()

	recorder := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })

	conditions := Conditions{model: &saveSemanticsUser{}}

	return newDialectQuery(context.Background(), nil, contractsdatabase.Config{}, sqlDB, nil, nil, nil, nil, &conditions, nil), recorder
}

// hydrateSaveSemanticsUser loads the canonical row (id 1, "before", NULL email, updated_at
// 2020-01-01 00:00:00) into model through the recorder, which is what captures the original bag.
func hydrateSaveSemanticsUser(t *testing.T, query *OrmBuilder, recorder *recordingSQLDriver, model *saveSemanticsUser) {
	t.Helper()

	recorder.returnRows(`select * from "users"`, []string{"id", "name", "email", "updated_at"},
		[]driver.Value{int64(1), "before", nil, "2020-01-01 00:00:00"})

	// The read runs on a clone: Where writes onto the handle in this port, and the where the read
	// needs must not double the key clause of the Save that follows.
	load := query.clone()
	require.NoError(t, load.Where("id", 1).Find(model))
	require.Equal(t, uint(1), model.ID)
	require.Equal(t, "before", model.Name)
	require.NotNil(t, model.UpdatedAt)
}

// TestSaveSemanticsUpdatesOnlyDirtyColumnsAndStampsUpdatedAt: changing name alone compiles the
// dirty-only update with a save-time updated_at; the untouched NULL email never joins the SET list
// and the loaded timestamp is not written back.
func TestSaveSemanticsUpdatesOnlyDirtyColumnsAndStampsUpdatedAt(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)

	carbon.SetTestNow(carbon.Parse("2026-11-03 01:02:03"))
	t.Cleanup(carbon.ClearTestNow)

	var model saveSemanticsUser
	hydrateSaveSemanticsUser(t, query, recorder, &model)

	model.Name = "after"
	require.NoError(t, query.Save(&model))

	update := recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)

	stamp, err := model.UpdatedAt.Value()
	require.NoError(t, err)
	assert.Equal(t, []driver.Value{"after", stamp, int64(1)}, update.args)
	assert.Equal(t, "2026-11-03 01:02:03", model.UpdatedAt.ToDateTimeString(),
		"the model carries the save-time stamp, not the loaded value")
}

// TestSaveSemanticsSkipsTheUpdateWhenNothingChanged: a clean model saves without any statement and
// its stored updated_at stays where it was — and after a dirty save, the synced original bag makes
// the next save clean as well.
func TestSaveSemanticsSkipsTheUpdateWhenNothingChanged(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)

	var model saveSemanticsUser
	hydrateSaveSemanticsUser(t, query, recorder, &model)

	statements := recorder.count()
	require.NoError(t, query.Save(&model))
	assert.Equal(t, statements, recorder.count(), "a clean save must run no statement")
	assert.Equal(t, "2020-01-01 00:00:00", model.UpdatedAt.ToDateTimeString(),
		"a clean save must not stamp updated_at")

	model.Name = "changed"
	require.NoError(t, query.Save(&model))
	dirtyCount := recorder.count()
	require.Greater(t, dirtyCount, statements, "the dirty save must write")

	require.NoError(t, query.Save(&model))
	assert.Equal(t, dirtyCount, recorder.count(),
		"finishSave synced the written values, so the next save is clean again")
}

// TestSaveSemanticsWritesEveryColumnWithoutACapturedBag: a hand-built model reads as one whose
// original bag is empty — every column is written and a zero updated_at is stamped, the shape Save
// always had. The primary key never joins the update (saveAssignments' documented rule).
func TestSaveSemanticsWritesEveryColumnWithoutACapturedBag(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)

	carbon.SetTestNow(carbon.Parse("2026-11-03 01:02:03"))
	t.Cleanup(carbon.ClearTestNow)

	model := saveSemanticsUser{ID: 7, Name: "manual"}
	require.NoError(t, query.Save(&model))

	update := recorder.last(t)
	assert.Equal(t, `update "users" set "email" = ?, "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	require.Len(t, update.args, 4)
	assert.Equal(t, nil, update.args[0], "the untouched nullable column is written as NULL")
	assert.Equal(t, "manual", update.args[1])
	assert.Equal(t, int64(7), update.args[3])
}

// TestSaveSemanticsCallerUpdatedAtWinsOverTheStamp: an updated_at the caller changed is dirty, so
// updateTimestamps leaves it alone and the update carries the caller's value.
func TestSaveSemanticsCallerUpdatedAtWinsOverTheStamp(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)

	carbon.SetTestNow(carbon.Parse("2026-11-03 01:02:03"))
	t.Cleanup(carbon.ClearTestNow)

	var model saveSemanticsUser
	hydrateSaveSemanticsUser(t, query, recorder, &model)

	model.Name = "after"
	model.UpdatedAt = carbon.NewDateTime(carbon.Parse("2019-05-05 05:05:05"))
	require.NoError(t, query.Save(&model))

	update := recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)

	stamp, err := model.UpdatedAt.Value()
	require.NoError(t, err)
	assert.Equal(t, []driver.Value{"after", stamp, int64(1)}, update.args)
	assert.Equal(t, "2019-05-05 05:05:05", model.UpdatedAt.ToDateTimeString())
}

// TestSaveSemanticsStampsALoadedNullUpdatedAt: a loaded NULL reads as equivalent to the model's nil
// field, so a dirty save stamps it — the probe's case 3c.
func TestSaveSemanticsStampsALoadedNullUpdatedAt(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)

	carbon.SetTestNow(carbon.Parse("2026-11-03 01:02:03"))
	t.Cleanup(carbon.ClearTestNow)

	recorder.returnRows(`select * from "users"`, []string{"id", "name", "email", "updated_at"},
		[]driver.Value{int64(2), "row", nil, nil})

	var model saveSemanticsUser
	load := query.clone()
	require.NoError(t, load.Where("id", 2).Find(&model))
	require.Nil(t, model.UpdatedAt)

	model.Name = "stamped"
	require.NoError(t, query.Save(&model))

	update := recorder.last(t)
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, update.sql)
	require.Len(t, update.args, 3)
	assert.Equal(t, "stamped", update.args[0])
	stamp, err := model.UpdatedAt.Value()
	require.NoError(t, err)
	assert.Equal(t, stamp, update.args[1])
	assert.Equal(t, "2026-11-03 01:02:03", model.UpdatedAt.ToDateTimeString())
}

// TestToSqlSavePreviewsTheDirtyUpdate: the dry run renders the statement Save would run — the
// dirty-only update for a changed model, nothing at all for a clean one — and writes nothing.
func TestToSqlSavePreviewsTheDirtyUpdate(t *testing.T) {
	query, recorder := newSaveSemanticsQuery(t)

	carbon.SetTestNow(carbon.Parse("2026-11-03 01:02:03"))
	t.Cleanup(carbon.ClearTestNow)

	var model saveSemanticsUser
	hydrateSaveSemanticsUser(t, query, recorder, &model)

	statements := recorder.count()
	assert.Equal(t, "", query.ToSql().Save(&model), "a clean model previews no statement")

	model.Name = "after"
	assert.Equal(t, `update "users" set "name" = ?, "updated_at" = ? where "id" = ?`, query.ToSql().Save(&model))
	assert.Equal(t, statements, recorder.count(), "previewing must not write")
}
