package gorm

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
)

// newRelQueryWith returns a Query whose conditions.model is preset, ready for build/compile tests.
func newRelQueryWith(t *testing.T, model any) *Query {
	t.Helper()
	db := newStubGormDB(t)
	conditions := Conditions{model: model}
	return NewQuery(context.Background(), nil, contractsdatabase.Config{}, db, nil, nil, nil, &conditions, nil)
}

// --- compileExistenceSubquery (covers HasOne/HasMany/BelongsTo/M2M/Morph/Through SQL) ---

func TestCompileExistenceSubquery_HasMany(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Books")
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	assert.True(t, strings.Contains(sql, "rel_books"))
}

func TestCompileExistenceSubquery_BelongsTo(t *testing.T) {
	q := newRelQueryWith(t, &relBook{})
	desc, err := resolveRelation(q.instance, &relBook{}, "Author")
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	assert.NotNil(t, inner)
}

func TestCompileExistenceSubquery_Many2Many(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Roles")
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	assert.Contains(t, sql, "rel_user_roles")
}

func TestCompileExistenceSubquery_Morph(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Houses")
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	assert.NotNil(t, inner)
}

func TestCompileExistenceSubquery_Through(t *testing.T) {
	q := newRelQueryWith(t, &relCountry{})
	desc, err := resolveRelation(q.instance, &relCountry{}, "Posts")
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	assert.Contains(t, sql, "rel_users") // through table
}

func TestCompileExistenceSubquery_WithCallback(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Books")
	assert.NoError(t, err)
	cb := contractsorm.RelationCallback(func(qq contractsorm.Query) contractsorm.Query {
		return qq.Where("title = ?", "x")
	})
	inner, err := q.compileExistenceSubquery(desc, cb)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	assert.Contains(t, sql, "title")
}

func TestCompileExistenceSubquery_NestedRelation(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Books.Author")
	assert.NoError(t, err)
	inner, err := q.compileExistenceSubquery(desc, nil)
	assert.NoError(t, err)
	assert.NotNil(t, inner)
}

// --- compileAggregateSubquery ---------------------------------------------

func TestCompileAggregateSubquery_Count(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Books")
	assert.NoError(t, err)
	sub := selectSub{relation: "Books", column: "*", function: "count"}
	inner, err := q.compileAggregateSubquery(desc, sub)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	assert.Contains(t, sql, "count(*)")
}

func TestCompileAggregateSubquery_Sum(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Books")
	assert.NoError(t, err)
	sub := selectSub{relation: "Books", column: "id", function: "sum"}
	inner, err := q.compileAggregateSubquery(desc, sub)
	assert.NoError(t, err)
	sql, _ := inner.ToSelectSQL()
	assert.Contains(t, sql, "sum(")
}

func TestCompileAggregateSubquery_Exists(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc, err := resolveRelation(q.instance, &relUser{}, "Books")
	assert.NoError(t, err)
	sub := selectSub{relation: "Books", column: "*", function: "exists"}
	inner, err := q.compileAggregateSubquery(desc, sub)
	assert.NoError(t, err)
	assert.NotNil(t, inner)
}
