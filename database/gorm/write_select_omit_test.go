package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Select and Omit name columns the way a model does, so a Go field name has to be resolved to the
// column it maps to before the insert record — which is keyed by column — is filtered.
//
// Left unresolved, Select("Name") matched no key and so dropped every column, rendering an empty
// `insert into t () values ()` that every dialect rejects, and Omit("Name") deleted nothing, so an
// omitted value reached the row.
func TestFilterInsertColumnsResolvesGoFieldNames(t *testing.T) {
	selectQuery := compiledQuery(t, &SchemaOuter{}, Conditions{
		selectColumns: []string{"Name"},
	})

	records, err := selectQuery.insertRecords(&SchemaOuter{Name: "kept"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "kept", records[0]["name"], `Select("Name") must keep the "name" column`)
		assert.NotEmpty(t, records[0], `Select("Name") must not drop every column`)
	}

	omitQuery := compiledQuery(t, &SchemaOuter{}, Conditions{
		omit: []string{"Name"},
	})

	records, err = omitQuery.insertRecords(&SchemaOuter{Name: "dropped"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		_, present := records[0]["name"]
		assert.False(t, present, `Omit("Name") must drop the "name" column`)
	}
}

// A column named by its database name already matched, and a bare "*" selects every column, so it
// must restrict nothing rather than filter the record down to nothing.
func TestFilterInsertColumnsAcceptsDBColumnNamesAndWildcard(t *testing.T) {
	columnQuery := compiledQuery(t, &SchemaOuter{}, Conditions{
		selectColumns: []string{"name"},
	})

	records, err := columnQuery.insertRecords(&SchemaOuter{Name: "kept"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "kept", records[0]["name"])
	}

	wildcardQuery := compiledQuery(t, &SchemaOuter{}, Conditions{
		selectColumns: []string{"*"},
	})

	records, err = wildcardQuery.insertRecords(&SchemaOuter{Name: "kept"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "kept", records[0]["name"], `Select("*") must keep every column`)
	}
}
