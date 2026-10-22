package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsdatabase "github.com/goravel/framework/contracts/database"
)

// TestGet_WithColumnsAfterSelectRaw reproduces the defect: when a SelectRaw is already installed,
// Get(dest, columns...) should NOT add more columns, but currently does because hasSelectColumns
// only checks for string items, not raw expressions.
func TestGet_WithColumnsAfterSelectRaw(t *testing.T) {
	// Create a query with SelectRaw
	db := newStubGormDB(t)
	baseQuery := NewQuery(context.Background(), nil, contractsdatabase.Config{}, db, nil, nil, nil, &Conditions{model: &relUser{}}, nil)
	
	// Add a raw select
	queryWithRaw := baseQuery.SelectRaw("count(*) as total")
	
	// Now simulate what Get does with columns
	qWithGet := queryWithRaw.(*Query)
	
	// Manually replicate what Get does
	columns := []any{"name", "email"}
	conditions := qWithGet.conditions
	if !hasSelectColumns(conditions) {
		// This would be a bug: hasSelectColumns should return true for raw selects
		// to prevent adding columns
		for _, col := range columnsToStrings(columns) {
			conditions.selectItems = append(conditions.selectItems, col)
		}
	}

	// Check the result - after fix, should only have the raw select
	assert.Len(t, conditions.selectItems, 1,
		"After fix: Get should NOT add columns when SelectRaw is present. Should have only 1 item (the raw select)")

	// The first item should be the raw select
	select0, ok := conditions.selectItems[0].(*Select)
	assert.True(t, ok, "First item should be *Select")
	assert.Equal(t, "count(*) as total", select0.query)
}

func TestGet_WithColumnsAfterSelect_ShouldNotAdd(t *testing.T) {
	// Test that Get also respects regular Select()
	db := newStubGormDB(t)
	baseQuery := NewQuery(context.Background(), nil, contractsdatabase.Config{}, db, nil, nil, nil, &Conditions{model: &relUser{}}, nil)
	
	// Add a regular select
	queryWithSelect := baseQuery.Select("id", "name")
	
	qWithGet := queryWithSelect.(*Query)
	
	// Manually replicate what Get does
	columns := []any{"email", "age"}
	conditions := qWithGet.conditions
	originalLen := len(conditions.selectItems)
	if !hasSelectColumns(conditions) {
		for _, col := range columnsToStrings(columns) {
			conditions.selectItems = append(conditions.selectItems, col)
		}
	}
	
	// This should NOT add columns because Select was already called
	assert.Equal(t, originalLen, len(conditions.selectItems),
		"Get should not add columns when Select was already called")
}

func TestHasSelectColumns_ShouldDetectRawSelects(t *testing.T) {
	// Direct unit test of hasSelectColumns
	
	// Empty conditions
	assert.False(t, hasSelectColumns(Conditions{}), "Empty conditions should return false")

	// Only string columns
	assert.True(t, hasSelectColumns(Conditions{
		selectItems: []any{"name", "email"},
	}), "String columns should return true")

	// Only raw select - after fix, should return true
	assert.True(t, hasSelectColumns(Conditions{
		selectItems: []any{&Select{query: "count(*) as total"}},
	}), "After fix: Raw select should return true")

	// Mix of string and raw
	assert.True(t, hasSelectColumns(Conditions{
		selectItems: []any{"name", &Select{query: "count(*) as total"}},
	}), "Mix should return true")
}
