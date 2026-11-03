package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

type selectOmitUser struct {
	ID     uint
	Name   string
	Avatar string
}

func (selectOmitUser) TableName() string { return "users" }

func TestCreateAndSaveSelectOmitConflict(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(contractsorm.Query, any) error
	}{
		{"Create", func(q contractsorm.Query, value any) error { return q.Create(value) }},
		{"Save", func(q contractsorm.Query, value any) error { return q.Save(value) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Run("raw select permits omit", func(t *testing.T) {
				query, recorder := newPostgresKeyQuery(t, &selectOmitUser{})
				recorder.returnLastInsertID(1)
				value := &selectOmitUser{Name: "kept", Avatar: "omitted"}

				require.NoError(t, operation.run(query.SelectRaw("count(*) as total").Omit("avatar"), value))
				assert.Equal(t, uint(1), value.ID)
				assert.Equal(t, 1, recorder.count())
				// The dialect returning clause (`returning "id"`) is pinned in the integration corpus
				// (tests/database/gorm) against the real Postgres grammar; the base grammar emits no
				// returning clause, and the driver name still rebinds "?" to "$1".
				assert.Equal(t, `insert into "users" ("name") values ($1)`, recorder.last(t).sql)
				assert.Equal(t, []driver.Value{"kept"}, recorder.last(t).args)
			})

			t.Run("named select conflicts with omit", func(t *testing.T) {
				query, recorder := newPostgresKeyQuery(t, &selectOmitUser{})
				err := operation.run(query.Select("name").Omit("avatar"), &selectOmitUser{Name: "kept"})
				assert.ErrorIs(t, err, errors.OrmQuerySelectAndOmitsConflict)
				assert.Equal(t, 0, recorder.count())
			})
		})
	}
}

func TestWildcardSelectionMatchesObserverAndWrite(t *testing.T) {
	for _, selection := range []struct {
		name    string
		columns []string
	}{
		{"default wildcard", nil},
		{"explicit wildcard", []string{"*"}},
		{"wildcard before column", []string{"*", "name"}},
		{"wildcard after column", []string{"name", "*"}},
	} {
		t.Run(selection.name, func(t *testing.T) {
			for _, changes := range []struct {
				name  string
				value any
			}{
				{"map", map[string]any{"name": "after", "avatar": ""}},
				{"struct", selectOmitUser{Name: "after"}},
				{"pointer", &selectOmitUser{Name: "after"}},
			} {
				t.Run(changes.name, func(t *testing.T) {
					model := &selectOmitUser{ID: 1, Name: "before", Avatar: "before"}
					query := compiledQuery(t, model, Conditions{}).Select(selection.columns...).(*OrmBuilder)
					want := map[string]any{"name": "after", "avatar": ""}

					assignments, err := query.updateAssignments(changes.value)
					require.NoError(t, err)
					assert.Equal(t, want, assignments)

					assignments, err = query.saveAssignments(changes.value)
					require.NoError(t, err)
					assert.Equal(t, want, assignments)

					event := NewEvent(query, model, changes.value)
					assert.True(t, event.IsDirty())
					assert.True(t, event.IsDirty("name"))
					assert.True(t, event.IsDirty("avatar"))
					assert.Equal(t, "after", event.GetAttribute("name"))
					assert.Equal(t, "", event.GetAttribute("avatar"))
					assert.False(t, event.validColumn("unknown"))
				})
			}
		})
	}
}

// Select and Omit name columns the way a model does, so a Go field name has to be resolved to the
// column it maps to before the insert record — which is keyed by column — is filtered.
//
// Left unresolved, Select("Name") matched no key and so dropped every column, rendering an empty
// `insert into t () values ()` that every dialect rejects, and Omit("Name") deleted nothing, so an
// omitted value reached the row.
func TestFilterInsertColumnsResolvesGoFieldNames(t *testing.T) {
	selectQuery := compiledQuery(t, &SchemaOuter{}, Conditions{}).Select("Name").(*OrmBuilder)

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
	columnQuery := compiledQuery(t, &SchemaOuter{}, Conditions{}).Select("name").(*OrmBuilder)

	records, err := columnQuery.insertRecords(&SchemaOuter{Name: "kept"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "kept", records[0]["name"])
	}

	wildcardQuery := compiledQuery(t, &SchemaOuter{}, Conditions{}).Select("*").(*OrmBuilder)

	records, err = wildcardQuery.insertRecords(&SchemaOuter{Name: "kept"})
	assert.NoError(t, err)
	if assert.Len(t, records, 1) {
		assert.Equal(t, "kept", records[0]["name"], `Select("*") must keep every column`)
	}
}
