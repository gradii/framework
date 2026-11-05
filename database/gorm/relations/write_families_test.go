package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasegorm "github.com/gradii/framework/database/gorm"
)

// This file pins the quiet write family's SQL identity: every quiet shell runs the same
// statements as its loud counterpart — only the model events are suppressed. The oracle
// (illuminate/database v12.55.1, executed; /tmp/lalign_write_quiet_force.php) measured
// `insert into "qf_posts" ("title", "user_id") values (?, ?)` for both save and saveQuietly.
// ForceCreate and Create compile the same INSERT only when no withAttributes pending pair is
// recorded; with one, ForceCreate's forced-insert path drops it (HasOneOrMany.php:414-419) while
// Create merges it — see relations/forcecreate_no_pending_test.go.
// Event suppression itself is observer-driven and the recording transport registers no
// observers, so it is asserted against a real database by the orm_relation write_families suite.

func wfQueries(statements []recordedStatement) []string {
	queries := make([]string, 0, len(statements))
	for _, statement := range statements {
		queries = append(queries, statement.query)
	}

	return queries
}

func wfBooksRelation(t *testing.T, builder *databasegorm.OrmBuilder, user *sqlUser) contractsorm.HasOneOrManyRelation {
	t.Helper()

	// Books is a has-many: it narrows through the shared HasOneOrManyRelation surface.
	relation, ok := builder.Related(user, "Books").(contractsorm.HasOneOrManyRelation)
	require.True(t, ok, "Related must return the has-many relation object")

	return relation
}

func wfRolesRelation(t *testing.T, builder *databasegorm.OrmBuilder, user *mmUser) contractsorm.Many2ManyRelation {
	t.Helper()

	relation, ok := builder.Related(user, "Roles").(contractsorm.Many2ManyRelation)
	require.True(t, ok, "Related must return the many-to-many relation object")

	return relation
}

func TestWriteFamiliesQuiet_HasManySaveQuietlyMatchesSaveSQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	loudErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).Save(&sqlBook{UserID: 1})
	loud := wfQueries(recorder.take())
	require.NotEmpty(t, loud, "the loud save must reach the statement recorder")

	quietErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).SaveQuietly(&sqlBook{UserID: 1})
	quiet := wfQueries(recorder.take())

	assert.Equal(t, loud, quiet, "saveQuietly must run the same statements as save")
	assert.Equal(t, loudErr == nil, quietErr == nil)
}

func TestWriteFamiliesQuiet_HasManyCreateQuietlyMatchesCreateSQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	loudErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).Create(&sqlBook{UserID: 1})
	loud := wfQueries(recorder.take())
	require.NotEmpty(t, loud)

	quietErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).CreateQuietly(&sqlBook{UserID: 1})
	quiet := wfQueries(recorder.take())

	assert.Equal(t, loud, quiet, "createQuietly must run the same statements as create")
	assert.Equal(t, loudErr == nil, quietErr == nil)
}

func TestWriteFamiliesQuiet_HasManySaveManyQuietlyMatchesSaveManySQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	loudErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).SaveMany([]*sqlBook{{UserID: 1}, {UserID: 1}})
	loud := wfQueries(recorder.take())
	require.NotEmpty(t, loud)

	quietErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).SaveManyQuietly([]*sqlBook{{UserID: 1}, {UserID: 1}})
	quiet := wfQueries(recorder.take())

	assert.Equal(t, loud, quiet, "saveManyQuietly must run the same statements as saveMany")
	assert.Equal(t, loudErr == nil, quietErr == nil)
}

func TestWriteFamiliesQuiet_HasManyCreateManyQuietlyMatchesCreateManySQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	loudErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).CreateMany([]*sqlBook{{UserID: 1}, {UserID: 1}})
	loud := wfQueries(recorder.take())
	require.NotEmpty(t, loud)

	quietErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).CreateManyQuietly([]*sqlBook{{UserID: 1}, {UserID: 1}})
	quiet := wfQueries(recorder.take())

	assert.Equal(t, loud, quiet, "createManyQuietly must run the same statements as createMany")
	assert.Equal(t, loudErr == nil, quietErr == nil)
}

func TestWriteFamiliesQuiet_ForceCreateMatchesCreateSQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	loudErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).Create(&sqlBook{UserID: 1})
	loud := wfQueries(recorder.take())
	require.NotEmpty(t, loud)

	forceErr := wfBooksRelation(t, builder, &sqlUser{ID: 7}).ForceCreate(&sqlBook{UserID: 1})
	force := wfQueries(recorder.take())

	assert.Equal(t, loud, force, "forceCreate runs the same INSERT as create when no pending attributes are recorded")
	assert.Equal(t, loudErr == nil, forceErr == nil)
}

func TestWriteFamiliesQuiet_Many2ManySaveQuietlyMatchesSaveSQL(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	loudErr := wfRolesRelation(t, builder, &mmUser{ID: 7}).Save(&mmRole{Name: "r"})
	loud := wfQueries(recorder.take())
	require.NotEmpty(t, loud, "the m2m save must reach the statement recorder")

	quietErr := wfRolesRelation(t, builder, &mmUser{ID: 7}).SaveQuietly(&mmRole{Name: "r"})
	quiet := wfQueries(recorder.take())

	assert.Equal(t, loud, quiet, "the m2m saveQuietly must run the same statements as save")
	assert.Equal(t, loudErr == nil, quietErr == nil)
}
