package model

import (
	"context"
	"testing"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedb "github.com/gradii/framework/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file makes the round-6 hydration-capture coverage permanent. The dirty-only Save the port
// landed (database/gorm/model_original.go) leans on every hydration path capturing the original
// bag, but the round-6 verify proved the captures only with overlay probes
// (/tmp/verify6/zz_capture_verify_test.go) - an open coverage gap this file closes in-tree.
//
// One subtest per capture site: Find(slice) -> the executor slice branch, Chunk -> pageInto,
// Cursor+Row.Scan -> row.go, CursorPaginate -> assignCursorRows. Each hydrates the model through
// that site, changes one column, saves, and asserts the statement carries only the changed column:
// the loaded, unchanged foo stays out of the SET list. Oracle (illuminate/database v12.55.1,
// executed, /tmp/save11/semantics_probe.php): a loaded-and-changed model updates exactly its changed
// attributes.

func captureSeedAndExpect(t *testing.T, query contractsorm.Query) modelDirtyProcessUser {
	t.Helper()

	modelSchema(t, query)
	seeded := modelDirtyProcessUser{Name: "old", Foo: 7}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seeded))

	return seeded
}

func captureSaveAndAssert(t *testing.T, query contractsorm.Query, model *modelDirtyProcessUser) {
	t.Helper()

	model.Name = "changed"
	ctx := databasedb.EnableQueryLog(context.Background())
	withContext, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")
	require.NoError(t, withContext.WithContext(ctx).Model(model).Save(model))
	logs := databasedb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "one dirty-only update")
	assert.Equal(t, `update "users" set "name" = 'changed' where "id" = 1`, logs[0].Query,
		"the hydrated, unchanged foo must stay out of the SET list")
}

func TestHydrationCapture_FindSlice(t *testing.T) {
	query := newRelationQuery(t)
	captureSeedAndExpect(t, query)

	models := []modelDirtyProcessUser{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", 1).Find(&models))
	require.Len(t, models, 1)
	require.Equal(t, 7, models[0].Foo)
	captureSaveAndAssert(t, query, &models[0])
}

func TestHydrationCapture_ChunkPage(t *testing.T) {
	query := newRelationQuery(t)
	captureSeedAndExpect(t, query)

	models := []modelDirtyProcessUser{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&modelDirtyProcessUser{}).Chunk(10, &models, func(page int) error { return nil }))
	require.Len(t, models, 1)
	require.Equal(t, 7, models[0].Foo)
	captureSaveAndAssert(t, query, &models[0])
}

func TestHydrationCapture_CursorRow(t *testing.T) {
	query := newRelationQuery(t)
	captureSeedAndExpect(t, query)

	var model modelDirtyProcessUser
	found := false
	for row := range cloneRelationQuery(t, query).Model(&modelDirtyProcessUser{}).Where("id", 1).Cursor() {
		require.NoError(t, row.Err())
		require.NoError(t, row.Scan(&model))
		found = true
	}
	require.True(t, found)
	require.Equal(t, 7, model.Foo)
	captureSaveAndAssert(t, query, &model)
}

func TestHydrationCapture_CursorPaginate(t *testing.T) {
	query := newRelationQuery(t)
	captureSeedAndExpect(t, query)

	models := []modelDirtyProcessUser{}
	_, err := cloneRelationQuery(t, query).Model(&modelDirtyProcessUser{}).Oldest("id").CursorPaginate(10, "", &models)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, 7, models[0].Foo)
	captureSaveAndAssert(t, query, &models[0])
}

// TestHydrationCapture_CreateRebaselinesTheOriginalBag pins create()'s own capture: the
// syncOriginal at the end of OrmBuilder.create (database/gorm/orm_builder.go:3602, finishSave's
// last step) rebaselines the original bag from the written struct, so a later Save of the model
// Create returned updates only what changes from there on. Round 11's verification found this
// capture covered only by an overlay probe - removing it left every root and tests-module package
// green - so it gets a permanent pin here. Unlike the captures above, no read hydrates the model:
// the model Create returns is the one that is later saved.
func TestHydrationCapture_CreateRebaselinesTheOriginalBag(t *testing.T) {
	query := newRelationQuery(t)
	modelSchema(t, query)

	seeded := modelDirtyProcessUser{Name: "old", Foo: 7}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seeded))
	require.NotZero(t, seeded.ID, "Create must read the generated key back")

	captureSaveAndAssert(t, query, &seeded)
}
