package gorm

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

func compileSugar(t *testing.T, query contractsorm.Query) (string, []any) {
	t.Helper()

	statement, bindings, err := query.(*OrmBuilder).compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

// --- WhereRelation family -----------------------------------------------------------------------

// whereRelation is whereHas with the where written for the caller: a (column, value) pair, or a
// (column, operator, value) triple. Mirrors QueriesRelationships::whereRelation.
func TestWhereRelationIsWhereHasWithOneWhere(t *testing.T) {
	const exists = `select * from "rel_users" where exists (select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "title" %s ?)`

	pair := compiledQuery(t, &relUser{}, Conditions{}).WhereRelation("Books", "title", "Go")
	statement, bindings := compileSugar(t, pair)
	assert.Equal(t, fmt.Sprintf(exists, "="), statement)
	assert.Equal(t, []any{"Go"}, bindings)

	triple := compiledQuery(t, &relUser{}, Conditions{}).WhereRelation("Books", "title", "!=", "Go")
	statement, bindings = compileSugar(t, triple)
	assert.Equal(t, fmt.Sprintf(exists, "!="), statement)
	assert.Equal(t, []any{"Go"}, bindings)
}

// A callback in place of the column constrains the related rows freely, as the Closure branch does.
func TestWhereRelationAcceptsACallback(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).WhereRelation("Books", func(q contractsorm.Query) contractsorm.Query {
		return q.Where("title", "Go").Where("user_id", 1)
	})

	statement, bindings := compileSugar(t, query)
	assert.Equal(t, `select * from "rel_users" where exists (select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "title" = ? and "user_id" = ?)`, statement)
	assert.Equal(t, []any{"Go", 1}, bindings)
}

// The or / doesn't-have variants change only the conjunction and the existence test.
func TestWhereRelationVariants(t *testing.T) {
	or := compiledQuery(t, &relUser{}, Conditions{}).Where("id", 1).OrWhereRelation("Books", "title", "Go")
	statement, bindings := compileSugar(t, or)
	assert.Equal(t, `select * from "rel_users" where "id" = ? or exists (select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "title" = ?)`, statement)
	assert.Equal(t, []any{1, "Go"}, bindings)

	doesnt := compiledQuery(t, &relUser{}, Conditions{}).WhereDoesntHaveRelation("Books", "title", "Go")
	statement, bindings = compileSugar(t, doesnt)
	assert.Equal(t, `select * from "rel_users" where not exists (select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "title" = ?)`, statement)
	assert.Equal(t, []any{"Go"}, bindings)

	orDoesnt := compiledQuery(t, &relUser{}, Conditions{}).Where("id", 1).OrWhereDoesntHaveRelation("Books", "title", "Go")
	statement, bindings = compileSugar(t, orDoesnt)
	assert.Equal(t, `select * from "rel_users" where "id" = ? or not exists (select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "title" = ?)`, statement)
	assert.Equal(t, []any{1, "Go"}, bindings)
}

// --- WithWhereHas -----------------------------------------------------------------------------------

// withWhereHas constrains by the relation and eager loads it under the same callback. Mirrors
// QueriesRelationships::withWhereHas.
func TestWithWhereHasConstrainsAndEagerLoads(t *testing.T) {
	callback := func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "Go") }
	query := compiledQuery(t, &relUser{}, Conditions{}).WithWhereHas("Books", callback).(*OrmBuilder)

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where exists (select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id" and "title" = ?)`, statement)
	assert.Equal(t, []any{"Go"}, bindings)

	require.Len(t, query.conditions.eagerLoad, 1)
	assert.Equal(t, "Books", query.conditions.eagerLoad[0].relation)
	assert.NotNil(t, query.conditions.eagerLoad[0].callback, "the eager load carries the same constraint")
}

// A column list on the relation ("Books:id,title") applies to the eager load alone, as
// Str::before($relation, ':') strips it from the whereHas.
func TestWithWhereHasStripsTheColumnListFromTheExistenceCheck(t *testing.T) {
	query := compiledQuery(t, &relUser{}, Conditions{}).WithWhereHas("Books:id,title").(*OrmBuilder)

	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where exists (select 1 from "rel_books" where "rel_users"."id" = "rel_books"."user_id")`, statement)

	require.Len(t, query.conditions.eagerLoad, 1)
	assert.Equal(t, "Books", query.conditions.eagerLoad[0].relation)
	assert.Equal(t, []string{"id", "title"}, query.conditions.eagerLoad[0].columns)
}

// --- WhereBelongsTo ---------------------------------------------------------------------------------

// whereBelongsTo matches the parent's foreign key against the related models' owner keys with an
// IN list, qualified by the parent table. Mirrors QueriesRelationships::whereBelongsTo.
func TestWhereBelongsToMatchesTheForeignKey(t *testing.T) {
	one := compiledQuery(t, &relBook{}, Conditions{}).WhereBelongsTo(&relUser{ID: 7}, "Author")
	statement, bindings := compileSugar(t, one)
	assert.Equal(t, `select * from "rel_books" where "rel_books"."author_id" in (?)`, statement)
	assert.Equal(t, []any{uint(7)}, bindings)

	many := compiledQuery(t, &relBook{}, Conditions{}).WhereBelongsTo([]*relUser{{ID: 7}, {ID: 8}}, "Author")
	statement, bindings = compileSugar(t, many)
	assert.Equal(t, `select * from "rel_books" where "rel_books"."author_id" in (?, ?)`, statement)
	assert.Equal(t, []any{uint(7), uint(8)}, bindings)

	or := compiledQuery(t, &relBook{}, Conditions{}).Where("id", 1).OrWhereBelongsTo(&relUser{ID: 7}, "Author")
	statement, bindings = compileSugar(t, or)
	assert.Equal(t, `select * from "rel_books" where "id" = ? or "rel_books"."author_id" in (?)`, statement)
	assert.Equal(t, []any{1, uint(7)}, bindings)
}

// The relation defaults to the related model's type name, which is how the PHP method derives it
// from class_basename($related).
func TestWhereBelongsToDefaultsTheRelationToTheModelName(t *testing.T) {
	query := compiledQuery(t, &belongsToPost{}, Conditions{}).WhereBelongsTo(&relUser{ID: 7})

	statement, bindings := compileSugar(t, query)
	assert.Equal(t, `select * from "belongs_to_posts" where "belongs_to_posts"."rel_user_id" in (?)`, statement)
	assert.Equal(t, []any{uint(7)}, bindings)
}

// An empty collection and a relation that is not a BelongsTo are refused, as the PHP method throws.
func TestWhereBelongsToRefusesBadInput(t *testing.T) {
	empty := compiledQuery(t, &relBook{}, Conditions{}).WhereBelongsTo([]*relUser{}, "Author").(*OrmBuilder)
	_, _, err := empty.compileSelect()
	assert.ErrorIs(t, err, errors.OrmWhereBelongsToEmpty)

	notBelongsTo := compiledQuery(t, &relUser{}, Conditions{}).WhereBelongsTo(&relBook{ID: 1}, "Books").(*OrmBuilder)
	_, _, err = notBelongsTo.compileSelect()
	assert.ErrorIs(t, err, errors.OrmWhereBelongsToNotBelongsTo)

	unknown := compiledQuery(t, &relBook{}, Conditions{}).WhereBelongsTo(&relUser{ID: 7}, "Missing").(*OrmBuilder)
	_, _, err = unknown.compileSelect()
	assert.ErrorIs(t, err, errors.OrmRelationNotFound)
}

// belongsToPost declares a BelongsTo whose relation name is the related type's name, which is what
// WhereBelongsTo falls back to when no name is given.
type belongsToPost struct {
	ID        uint
	RelUserID uint
	RelUser   *relUser `gorm:"-"`
}

func (belongsToPost) TableName() string { return "belongs_to_posts" }

func (belongsToPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"relUser": contractsorm.BelongsTo{Related: &relUser{}, ForeignKey: "rel_user_id"},
	}
}
