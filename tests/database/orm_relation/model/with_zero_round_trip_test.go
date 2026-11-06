package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModel_WithZeroRoundTrip runs the struct write rule against a real sqlite database. A keyed Save
// leaves a zero field out, so the stored value survives; WithZero names the column, so the zero is
// written. No loaded values are tracked (user decision, 2026-11-06), so both saves update.
func TestModel_WithZeroRoundTrip(t *testing.T) {
	query := newRelationQuery(t)
	modelSchema(t, query)

	seeded := modelDirtyProcessUser{Name: "old", Foo: 7}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seeded))

	require.NoError(t, cloneRelationQuery(t, query).Save(&modelDirtyProcessUser{ID: seeded.ID, Name: "new"}))
	var kept modelDirtyProcessUser
	require.NoError(t, cloneRelationQuery(t, query).Where("id", seeded.ID).First(&kept))
	assert.Equal(t, "new", kept.Name)
	assert.Equal(t, 7, kept.Foo, "an unnamed zero field is not written")

	require.NoError(t, cloneRelationQuery(t, query).WithZero("foo").Save(&modelDirtyProcessUser{ID: seeded.ID, Name: "new"}))
	var cleared modelDirtyProcessUser
	require.NoError(t, cloneRelationQuery(t, query).Where("id", seeded.ID).First(&cleared))
	assert.Equal(t, 0, cleared.Foo, "WithZero writes the named zero")
}
