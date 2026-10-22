package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGormWhereAnyEmptyInSlice reproduces the issue where WhereAny with an empty IN slice
// produces malformed SQL instead of the "0 = 1" constant that WhereIn produces.
func TestGormWhereAnyEmptyInSlice(t *testing.T) {
	t.Run("whereFragment with empty IN list should return 0 = 1", func(t *testing.T) {
		fragment, bindings := whereFragment("name", "IN", []any{})

		t.Logf("Fragment: %s", fragment)
		t.Logf("Bindings: %v", bindings)

		assert.Equal(t, "0 = 1", fragment, "Empty IN should produce '0 = 1'")
		assert.Nil(t, bindings, "Empty IN should bind nothing")
	})

	t.Run("whereFragment with empty NOT IN list should return 1 = 1", func(t *testing.T) {
		fragment, bindings := whereFragment("name", "NOT IN", []any{})

		t.Logf("Fragment: %s", fragment)
		t.Logf("Bindings: %v", bindings)

		assert.Equal(t, "1 = 1", fragment, "Empty NOT IN should produce '1 = 1'")
		assert.Nil(t, bindings, "Empty NOT IN should bind nothing")
	})
}
