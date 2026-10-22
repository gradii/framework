package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMultipleSelectRawCallsAppend verifies that multiple SelectRaw calls append rather than overwrite
func TestMultipleSelectRawCallsAppend(t *testing.T) {
	q := compiledQuery(t, &relUser{}, Conditions{})
	q = q.SelectRaw("count(name) as name_count").(*Query)
	q = q.SelectRaw("sum(id) as total_id").(*Query)

	sql := q.ToSql().Get(&relUser{})

	// EXPECTATION: Both expressions should appear
	assert.Contains(t, sql, "name_count")
	assert.Contains(t, sql, "total_id")
}

// TestSelectRawThenSelectOrder verifies that SelectRaw then Select preserves call order
func TestSelectRawThenSelectOrder(t *testing.T) {
	q := compiledQuery(t, &relUser{}, Conditions{})
	q = q.SelectRaw("count(name) as name_count").(*Query)
	q = q.Select("name").(*Query)

	sql := q.ToSql().Get(&relUser{})

	assert.Contains(t, sql, "name_count")
	assert.Contains(t, sql, "name")

	// EXPECTATION: Raw expression should appear BEFORE the column
	nameCountPos := indexOf(sql, "name_count")
	namePos := indexOf(sql, `"name"`)
	assert.True(t, nameCountPos < namePos, "Expected raw expression before column: %s", sql)
}

// TestSelectThenSelectRawOrder verifies that Select then SelectRaw preserves call order
func TestSelectThenSelectRawOrder(t *testing.T) {
	q := compiledQuery(t, &relUser{}, Conditions{})
	q = q.Select("name").(*Query)
	q = q.SelectRaw("count(name) as name_count").(*Query)

	sql := q.ToSql().Get(&relUser{})

	assert.Contains(t, sql, "name")
	assert.Contains(t, sql, "name_count")

	// EXPECTATION: Column should appear BEFORE the raw expression
	namePos := indexOf(sql, `"name"`)
	nameCountPos := indexOf(sql, "name_count")
	assert.True(t, namePos < nameCountPos, "Expected column before raw expression: %s", sql)
}

// TestSelectRawAppendsToSelectItems verifies that SelectRaw appends to the same list as Select
func TestSelectRawAppendsToSelectItems(t *testing.T) {
	q := compiledQuery(t, &relUser{}, Conditions{})
	q = q.Select("id", "name").(*Query)
	q = q.SelectRaw("1 as one").(*Query)
	q = q.Select("bio").(*Query)

	sql := q.ToSql().Get(&relUser{})
	t.Logf("SQL: %s", sql)

	// All items should be present in order
	assert.Contains(t, sql, `"id"`)
	assert.Contains(t, sql, `"name"`)
	assert.Contains(t, sql, "1 as one")
	assert.Contains(t, sql, `"bio"`)

	// Check order: id < name < one < bio
	idPos := indexOf(sql, `"id"`)
	namePos := indexOf(sql, `"name"`)
	onePos := indexOf(sql, "1 as one")
	bioPos := indexOf(sql, `"bio"`)

	t.Logf("Positions: id=%d, name=%d, one=%d, bio=%d", idPos, namePos, onePos, bioPos)

	assert.True(t, idPos < namePos, "id should come before name")
	assert.True(t, namePos < onePos, "name should come before one")
	assert.True(t, onePos < bioPos, "one should come before bio")
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
