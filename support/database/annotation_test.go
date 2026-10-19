package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		expected Annotation
	}{
		{
			name:     "empty tag maps by convention",
			tag:      "",
			expected: Annotation{},
		},
		{
			name: "primary key",
			tag:  "primaryKey",
			expected: Annotation{
				PrimaryKey: true,
			},
		},
		{
			name: "column and timestamps",
			tag:  "autoCreateTime;column:created_at",
			expected: Annotation{
				Column:         "created_at",
				AutoCreateTime: true,
			},
		},
		{
			name: "update timestamp without a column",
			tag:  "autoUpdateTime",
			expected: Annotation{
				AutoUpdateTime: true,
			},
		},
		{
			name: "plain and unique indexes",
			tag:  "index:idx_name,priority:1",
			expected: Annotation{
				Index:     true,
				IndexName: "idx_name",
			},
		},
		{
			name: "unique index",
			tag:  "uniqueIndex:idx_email",
			expected: Annotation{
				UniqueIndex:     true,
				UniqueIndexName: "idx_email",
			},
		},
		{
			name: "ignored field",
			tag:  "-",
			expected: Annotation{
				Skip: true,
			},
		},
		{
			name: "field kept out of migrations only",
			tag:  "-:migration",
			expected: Annotation{
				SkipMigration: true,
			},
		},
		{
			name:     "unknown keys are ignored rather than rejected",
			tag:      "type:varchar(36);not null;size:36",
			expected: Annotation{},
		},
		{
			name: "combined tag",
			tag:  "primaryKey;type:char(26);column:id",
			expected: Annotation{
				Column:     "id",
				PrimaryKey: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, ParseAnnotation(test.tag))
		})
	}
}
