package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

func TestScanAggregateConversionsAndNulls(t *testing.T) {
	var sum float64
	require.NoError(t, scanAggregate([]byte("12.75"), &sum))
	assert.Equal(t, 12.75, sum)
	require.NoError(t, scanAggregate(nil, &sum))
	assert.Zero(t, sum)

	var nullable sql.NullFloat64
	require.NoError(t, scanAggregate("12.75", &nullable))
	assert.Equal(t, sql.NullFloat64{Float64: 12.75, Valid: true}, nullable)
	require.NoError(t, scanAggregate(nil, &nullable))
	assert.False(t, nullable.Valid)

	var pointer *float64
	require.NoError(t, scanAggregate([]byte("12.75"), &pointer))
	require.NotNil(t, pointer)
	assert.Equal(t, 12.75, *pointer)
	require.NoError(t, scanAggregate(nil, &pointer))
	assert.Nil(t, pointer)
}

func TestScanAggregateRejectsInvalidValues(t *testing.T) {
	var integer int
	assert.ErrorIs(t, scanAggregate(1.5, &integer), errors.OrmRowScanValueNotRepresentable)
	assert.ErrorIs(t, scanAggregate("invalid", &integer), errors.OrmRowScanValueNotRepresentable)
	assert.ErrorIs(t, scanAggregate(1, nil), errors.DatabaseUnsupportedType)
	assert.ErrorIs(t, scanAggregate(1, (*int)(nil)), errors.DatabaseUnsupportedType)
}
