package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsByteSlice verifies the isByteSlice helper correctly identifies byte slices.
// This is the core logic that prevents []byte from being expanded into an IN list.
func TestIsByteSlice(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected bool
	}{
		{"byte slice", []byte("ab"), true},
		{"byte slice pointer", &[]byte{1, 2}, true},
		{"byte array", [3]byte{1, 2, 3}, true},
		{"string slice", []string{"a", "b"}, false},
		{"int slice", []int{1, 2}, false},
		{"nil", nil, false},
		{"string", "hello", false},
		{"int", 42, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isByteSlice(tt.value)
			assert.Equal(t, tt.expected, result, "isByteSlice(%v) should return %v", tt.value, tt.expected)
		})
	}
}

// TestIsSliceExcludesByteSlice verifies that the combined check correctly
// identifies slices but excludes byte slices.
func TestIsSliceExcludesByteSlice(t *testing.T) {
	tests := []struct {
		name              string
		value             any
		shouldExpandToIN  bool
		shouldBindAsValue bool
	}{
		{
			name:              "byte slice should NOT expand to IN",
			value:             []byte("ab"),
			shouldExpandToIN:  false,
			shouldBindAsValue: true,
		},
		{
			name:              "string slice SHOULD expand to IN",
			value:             []string{"a", "b"},
			shouldExpandToIN:  true,
			shouldBindAsValue: false,
		},
		{
			name:              "int slice SHOULD expand to IN",
			value:             []int{1, 2},
			shouldExpandToIN:  true,
			shouldBindAsValue: false,
		},
		{
			name:              "single string should bind as value",
			value:             "hello",
			shouldExpandToIN:  false,
			shouldBindAsValue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the check from applyWhere line 110:
			// case len(args) == 1 && isSlice(args[0]) && !isByteSlice(args[0]):
			shouldExpand := isSlice(tt.value) && !isByteSlice(tt.value)
			assert.Equal(t, tt.shouldExpandToIN, shouldExpand,
				"isSlice(%v) && !isByteSlice(%v) = %v, expected %v",
				tt.value, tt.value, shouldExpand, tt.shouldExpandToIN)
		})
	}
}

// Reproduction test: demonstrate the fix prevents the old fault.
// OLD: []byte("ab") would match isSlice() and expand to IN (?, ?) with [97, 98]
// NEW: []byte("ab") is excluded by !isByteSlice() and binds as single value
func TestByteSliceFaultIsFixed(t *testing.T) {
	byteSlice := []byte("ab")

	// OLD behavior would have passed this check:
	oldBehaviorWouldExpand := isSlice(byteSlice) // true
	assert.True(t, oldBehaviorWouldExpand, "OLD: isSlice([]byte) returned true, would expand to IN")

	// NEW behavior adds the exclusion:
	newBehaviorExpands := isSlice(byteSlice) && !isByteSlice(byteSlice)
	assert.False(t, newBehaviorExpands, "NEW: isSlice && !isByteSlice prevents expansion")

	// Verify isByteSlice correctly identifies it:
	assert.True(t, isByteSlice(byteSlice), "isByteSlice([]byte) should return true")
}
