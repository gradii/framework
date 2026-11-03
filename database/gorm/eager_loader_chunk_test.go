package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChunkKeys(t *testing.T) {
	cases := []struct {
		name string
		keys []any
		size int
		want [][]any
	}{
		{
			name: "size 0 disables chunking",
			keys: []any{1, 2, 3},
			size: 0,
			want: [][]any{{1, 2, 3}},
		},
		{
			name: "negative size disables chunking",
			keys: []any{1, 2, 3, 4, 5},
			size: -1,
			want: [][]any{{1, 2, 3, 4, 5}},
		},
		{
			name: "len <= size returns single chunk",
			keys: []any{1, 2, 3},
			size: 5,
			want: [][]any{{1, 2, 3}},
		},
		{
			name: "exact multiple",
			keys: []any{1, 2, 3, 4},
			size: 2,
			want: [][]any{{1, 2}, {3, 4}},
		},
		{
			name: "non-exact: last chunk shorter",
			keys: []any{1, 2, 3, 4, 5},
			size: 2,
			want: [][]any{{1, 2}, {3, 4}, {5}},
		},
		{
			name: "size 1 yields one item per chunk",
			keys: []any{1, 2, 3},
			size: 1,
			want: [][]any{{1}, {2}, {3}},
		},
		{
			name: "empty input",
			keys: []any{},
			size: 10,
			want: [][]any{{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chunkKeys(tc.keys, tc.size)
			assert.Equal(t, tc.want, got)
		})
	}
}
