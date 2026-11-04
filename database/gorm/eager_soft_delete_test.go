package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// A related model that declares the framework's soft-delete field must carry that model's
// SoftDeletingScope on every eager read, exactly as Laravel's does: Relation::noConstraints builds
// the eager child from the related model's new query, whose registered SoftDeletingScope appends
// `deleted_at is null`. The compiled eager runner (runRelatedQueryCompiled / runMany2ManyQueryCompiled)
// binds the related model to the child, so applySoftDelete appends the predicate the same way the
// non-eager relation read already does.
//
// Oracle (illuminate/database v12.55.1, executed against real sqlite):
// /tmp/eager-softdelete/probe.php — E1 a `With("posts")`/`load("posts")` eager read compiles
// `select * from "es_posts" where "es_posts"."user_id" in (1, 2) and "es_posts"."deleted_at" is null`
// (E2 the single-parent shape, `in (1)`); E3 the withTrashed callback drops the predicate; E4
// onlyTrashed renders `is not null`; E6 hasOne and E7 belongsTo carry the predicate; E10 the eager
// many-to-many read appends `and "es_roles"."deleted_at" is null`; E11 the MorphTo per-type read
// appends `and "es_users"."deleted_at" is null`. (E9's "through table is never filtered" note is
// superseded by round 10: a soft-deletable THROUGH model is filtered too — see
// eager_through_soft_delete_test.go; the fixture here uses a non-soft-deletable through model, so
// nothing changes for these pins.) These pins drive the loaders directly, so the assertion is on the
// statement the loader built and ran rather than on a child the test rebuilt by hand.

type esdPost struct {
	databaseorm.SoftDeletes

	ID     uint
	UserID uint
	Title  string
}

func (esdPost) TableName() string { return "esd_posts" }

type esdUser struct {
	ID uint

	Posts []*esdPost `gorm:"-"`
	Roles []*esdRole `gorm:"-"`
}

func (esdUser) TableName() string { return "esd_users" }

func (esdUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &esdPost{}, ForeignKey: "user_id"},
		"Roles": contractsorm.Many2Many{Related: &esdRole{}, Table: "esd_role_user"},
	}
}

type esdAuthor struct {
	databaseorm.SoftDeletes

	ID   uint
	Name string
}

func (esdAuthor) TableName() string { return "esd_authors" }

type esdBook struct {
	ID       uint
	AuthorID uint
	Author   *esdAuthor `gorm:"-"`
}

func (esdBook) TableName() string { return "esd_books" }

func (esdBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &esdAuthor{}, ForeignKey: "author_id"},
	}
}

type esdRole struct {
	databaseorm.SoftDeletes

	ID   uint
	Name string
}

func (esdRole) TableName() string { return "esd_roles" }

// TestEagerRelatedSoftDeletePredicate pins the default predicate on each eager kind whose child is
// the related read. The related model is what the loader binds, so the predicate names its table.
func TestEagerRelatedSoftDeletePredicate(t *testing.T) {
	t.Run("has many", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &esdUser{})
		desc, err := relations.ResolveRelation(&esdUser{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		parents := []reflect.Value{
			reflect.ValueOf(&esdUser{ID: 1}).Elem(),
			reflect.ValueOf(&esdUser{ID: 2}).Elem(),
		}
		assert.NoError(t, q.loadHasOneOrMany(parents, &esdUser{}, desc, eagerLoadEntry{Relation: "Posts"}, nil, true))

		recorded := recorder.last(t)
		assert.Equal(t, `select * from "esd_posts" where "esd_posts"."user_id" in (?, ?) and "esd_posts"."deleted_at" is null`, recorded.sql)
		assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), recorded.args)
	})

	t.Run("belongs to", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &esdBook{})
		desc, err := relations.ResolveRelation(&esdBook{}, "Author")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		parents := []reflect.Value{
			reflect.ValueOf(&esdBook{ID: 1, AuthorID: 7}).Elem(),
		}
		assert.NoError(t, q.loadBelongsTo(parents, &esdBook{}, desc, eagerLoadEntry{Relation: "Author"}, nil))

		recorded := recorder.last(t)
		assert.Equal(t, `select * from "esd_authors" where "esd_authors"."id" in (?) and "esd_authors"."deleted_at" is null`, recorded.sql)
		assert.Equal(t, driverBindings([]any{uint(7)}), recorded.args)
	})

	t.Run("many to many", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &esdUser{})
		desc, err := relations.ResolveRelation(&esdUser{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		recorder.returnRows(`from "esd_roles"`, []string{"id", "name"}, []driver.Value{int64(9), "admin"})

		parents := []reflect.Value{
			reflect.ValueOf(&esdUser{ID: 1}).Elem(),
			reflect.ValueOf(&esdUser{ID: 2}).Elem(),
		}
		assert.NoError(t, q.loadMany2Many(parents, &esdUser{}, desc, eagerLoadEntry{Relation: "Roles"}, nil))

		joined := recorder.at(t, 0)
		assert.Contains(t, joined.sql, `from "esd_roles" inner join "esd_role_user"`)
		assert.Contains(t, joined.sql, `and "esd_roles"."deleted_at" is null`)
	})
}

// TestEagerRelatedSoftDeleteTrashedFamily pins that the trashed switches and the scope removal reach
// the eager child, since they are the child's conditions the predicate consults.
func TestEagerRelatedSoftDeleteTrashedFamily(t *testing.T) {
	cases := []struct {
		name     string
		callback contractsorm.RelationCallback
		sql      string
	}{
		{
			name:     "default",
			callback: nil,
			sql:      `select * from "esd_posts" where "esd_posts"."user_id" in (?) and "esd_posts"."deleted_at" is null`,
		},
		{
			name:     "withTrashed",
			callback: func(query contractsorm.Query) contractsorm.Query { return query.WithTrashed() },
			sql:      `select * from "esd_posts" where "esd_posts"."user_id" in (?)`,
		},
		{
			name:     "onlyTrashed",
			callback: func(query contractsorm.Query) contractsorm.Query { return query.OnlyTrashed() },
			sql:      `select * from "esd_posts" where "esd_posts"."user_id" in (?) and "esd_posts"."deleted_at" is not null`,
		},
		{
			name:     "withoutGlobalScopes",
			callback: func(query contractsorm.Query) contractsorm.Query { return query.WithoutGlobalScopes() },
			sql:      `select * from "esd_posts" where "esd_posts"."user_id" in (?)`,
		},
		{
			// A named removal that is not the soft-delete scope leaves the predicate: the eager
			// child keeps Laravel's SoftDeletingScope.
			name:     "withoutGlobalScope(named user scope)",
			callback: func(query contractsorm.Query) contractsorm.Query { return query.WithoutGlobalScopes("esd_scope") },
			sql:      `select * from "esd_posts" where "esd_posts"."user_id" in (?) and "esd_posts"."deleted_at" is null`,
		},
		{
			// Removing the SoftDeletingScope by name drops the predicate - Laravel's
			// withoutGlobalScope(SoftDeletingScope::class); the binding stays so user scopes still
			// queue (oracle, illuminate/database v12.55.1, /tmp/r12/scope_removal_probe2.php §F9).
			name: "withoutGlobalScope(SoftDeletingScope)",
			callback: func(query contractsorm.Query) contractsorm.Query {
				return query.WithoutGlobalScopes(contractsorm.SoftDeletingScope)
			},
			sql: `select * from "esd_posts" where "esd_posts"."user_id" in (?)`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, &esdUser{})
			desc, err := relations.ResolveRelation(&esdUser{}, "Posts")
			assert.NoError(t, err)
			if err != nil {
				return
			}

			parents := []reflect.Value{reflect.ValueOf(&esdUser{ID: 5}).Elem()}
			assert.NoError(t, q.loadHasOneOrMany(parents, &esdUser{}, desc, eagerLoadEntry{Relation: "Posts", Callback: c.callback}, nil, true))

			assert.Equal(t, c.sql, recorder.last(t).sql)
		})
	}
}

// TestEagerRelatedNonSoftDeleteModelKeepsNoPredicate is the control: a related model that declares no
// soft-delete field reads exactly as before, so the change adds the predicate only where Laravel does.
func TestEagerRelatedNonSoftDeleteModelKeepsNoPredicate(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})
	desc, err := relations.ResolveRelation(&relUser{}, "Books")
	assert.NoError(t, err)
	if err != nil {
		return
	}

	parents := []reflect.Value{
		reflect.ValueOf(&relUser{ID: 1}).Elem(),
		reflect.ValueOf(&relUser{ID: 2}).Elem(),
	}
	assert.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc, eagerLoadEntry{Relation: "Books"}, nil, true))

	assert.Equal(t, `select * from "rel_books" where "rel_books"."user_id" in (?, ?)`, recorder.last(t).sql)
}

// esdComment declares a MorphTo whose resolved type can be a soft-deletable model.
type esdComment struct {
	ID              uint
	CommentableID   uint
	CommentableType string
	Commentable     any `gorm:"-"`
}

func (esdComment) TableName() string { return "esd_comments" }

func (esdComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{Name: "commentable"},
	}
}

// TestEagerMorphToSoftDeletePredicate pins oracle E11: the MorphTo per-type eager read appends the
// resolved type's soft-delete predicate, through the same runRelatedQueryCompiled binding the
// hasMany/belongsTo/m2m cases exercise. The round-9 verify flagged the MorphTo case as covered only
// by the corpus MorphToWithoutConstraints flip, with no root pin.
func TestEagerMorphToSoftDeletePredicate(t *testing.T) {
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)
	morphmap.Register(map[string]any{"esd_post": &esdPost{}})

	q, recorder := newRecordingRelQuery(t, &esdComment{})
	desc, err := relations.ResolveRelation(&esdComment{}, "Commentable")
	require.NoError(t, err)

	recorder.returnRows(`from "esd_posts"`, []string{"id"}, []driver.Value{int64(5)})

	parents := []reflect.Value{
		reflect.ValueOf(&esdComment{CommentableType: "esd_post", CommentableID: 5}).Elem(),
	}
	require.NoError(t, q.loadMorphTo(parents, &esdComment{}, desc, eagerLoadEntry{Relation: "Commentable"}, nil))

	assert.Equal(t, `select * from "esd_posts" where "esd_posts"."id" in (?) and "esd_posts"."deleted_at" is null`, recorder.last(t).sql)
}
