package gorm

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	databaseorm "github.com/gradii/framework/database/orm"
)

// errTimestampsCallbackThrown is the sentinel a throwing callback returns: the Go form of "the
// closure threw" is an error, which WithoutTimestamps restores around and returns unchanged.
var errTimestampsCallbackThrown = errors.New("thrown by the callback")

// withoutTimestampsUser carries the framework's own Timestamps, so the query scope and the
// per-instance flag have a model whose automatic columns they suspend.
type withoutTimestampsUser struct {
	databaseorm.Timestamps

	ID    uint   `gorm:"column:id;primaryKey"`
	Label string `gorm:"column:label"`
}

func (withoutTimestampsUser) TableName() string { return "review_timestamps" }

// WithoutTimestamps suspends the automatic update stamp, re-entrantly, keeps a caller-supplied value
// in place, and restores the state after the scope — including when the callback returns an error.
// Mirrors HasTimestamps::withoutTimestamps.
func TestWithoutTimestampsSuspendsAndRestores(t *testing.T) {
	model := &withoutTimestampsUser{ID: 1, Label: "seed"}
	query := compiledQuery(t, model, Conditions{})

	assignments, err := query.updateAssignments(map[string]any{"label": "first"})
	require.NoError(t, err)
	assert.Contains(t, assignments, "updated_at")

	require.NoError(t, query.WithoutTimestamps(func() error {
		assert.False(t, query.UsesTimestamps())

		suspended, err := query.updateAssignments(map[string]any{"label": "second"})
		require.NoError(t, err)
		assert.NotContains(t, suspended, "updated_at")

		require.NoError(t, query.WithoutTimestamps(func() error {
			assert.False(t, query.UsesTimestamps())

			nested, err := query.updateAssignments(map[string]any{"label": "third"})
			require.NoError(t, err)
			assert.NotContains(t, nested, "updated_at")

			return nil
		}))
		assert.False(t, query.UsesTimestamps(), "the inner scope restores to the outer scope's suspension")

		// Only the automatic stamp is suspended: a value the caller supplied is still written.
		explicit, err := query.updateAssignments(map[string]any{"label": "fourth", "updated_at": "manual"})
		require.NoError(t, err)
		assert.Contains(t, explicit, "updated_at")

		return nil
	}))

	assert.True(t, query.UsesTimestamps())

	restored, err := query.updateAssignments(map[string]any{"label": "fifth"})
	require.NoError(t, err)
	assert.Contains(t, restored, "updated_at")

	require.ErrorIs(t, query.WithoutTimestamps(func() error {
		assert.False(t, query.UsesTimestamps())

		return errTimestampsCallbackThrown
	}), errTimestampsCallbackThrown)
	assert.True(t, query.UsesTimestamps(), "a failing callback must still restore the suspension")
}

// The per-instance flag — Eloquent's $timestamps = false — suspends the same way, Query.UsesTimestamps
// reports it, and a suspended insert drops the unset automatic columns so the database's own default
// applies, exactly as Eloquent's insert carries no such attribute.
func TestWithoutTimestampsPerInstanceFlag(t *testing.T) {
	flagged := &withoutTimestampsUser{ID: 1}
	flagged.Disabled = true
	query := compiledQuery(t, flagged, Conditions{})

	assert.False(t, query.UsesTimestamps())

	assignments, err := query.updateAssignments(map[string]any{"label": "skipped"})
	require.NoError(t, err)
	assert.NotContains(t, assignments, "updated_at")

	flaggedInsert := &withoutTimestampsUser{Label: "new"}
	flaggedInsert.Disabled = true
	records, err := compiledQuery(t, flaggedInsert, Conditions{}).insertRecords(flaggedInsert)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.NotContains(t, records[0], "created_at")
	assert.NotContains(t, records[0], "updated_at")

	// A model that does not carry the flag keeps stamping.
	plain := &withoutTimestampsUser{Label: "new"}
	stamped, err := compiledQuery(t, plain, Conditions{}).insertRecords(plain)
	require.NoError(t, err)
	require.Len(t, stamped, 1)
	assert.Contains(t, stamped[0], "created_at")
	assert.Contains(t, stamped[0], "updated_at")
}

// Upsert on an ignored model leaves updated-at out of the update list, like Eloquent's
// addTimestampsToUpsertValues / addUpdatedAtToUpsertColumns.
func TestWithoutTimestampsUpsertSkipsTheUpdatedAtColumn(t *testing.T) {
	flagged := &withoutTimestampsUser{ID: 1}
	flagged.Disabled = true
	query := compiledQuery(t, flagged, Conditions{})

	records := []map[string]any{{"label": "row"}}

	updates, insertOnly, err := query.upsertUpdates(records, nil)
	require.NoError(t, err)
	assert.False(t, insertOnly)
	for _, update := range updates {
		assert.NotEqual(t, "updated_at", update.Column)
	}

	updates, insertOnly, err = query.upsertUpdates(records, []any{map[string]any{"label": "row"}})
	require.NoError(t, err)
	assert.False(t, insertOnly)
	for _, update := range updates {
		assert.NotEqual(t, "updated_at", update.Column)
	}
}

// Touch on a model that ignores timestamps touches nothing, like Eloquent's Builder::touch.
func TestWithoutTimestampsTouchTouchesNothing(t *testing.T) {
	flagged := &withoutTimestampsUser{ID: 1}
	flagged.Disabled = true
	query := compiledQuery(t, flagged, Conditions{}).Where("id", 1).(*OrmBuilder)

	result, err := query.Touch()
	require.NoError(t, err)
	assert.Zero(t, result.RowsAffected)
}
