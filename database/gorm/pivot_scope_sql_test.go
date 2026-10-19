package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
)

// This file pins the exact SQL produced when OnPivotQuery is wired up on the Query.Related(...)
// read path for Many2Many / MorphToMany -> new_relation.go:newMany2ManyQuery, by inspecting the
// SQL Related() emits. The eager-load pivot reads that this file used to replicate through the
// retired gorm chain are covered where they are built: eager_loader_test.go and
// morph_to_many_eager_test.go compile those statements through the production child builders.

// --- Fixtures --------------------------------------------------------------

// scopeUser declares a Many2Many to scopeRole with an OnPivotQuery that filters on active = 1.
type scopeUser struct {
	ID    uint
	Name  string
	Roles []*scopeRole `gorm:"-"`
}

func (scopeUser) TableName() string { return "scope_users" }

func (scopeUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{
			Related: &scopeRole{},
			Table:   "scope_user_roles",
			OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery {
				return q.Where("active", 1)
			},
		},
	}
}

type scopeRole struct {
	ID   uint
	Name string
}

func (scopeRole) TableName() string { return "scope_roles" }

// scopeMorphPost declares a MorphToMany to scopeMorphTag with an OnPivotQuery filtering on
// deleted_at IS NULL.
type scopeMorphPost struct {
	ID    uint
	Title string
	Tags  []*scopeMorphTag `gorm:"-"`
}

func (scopeMorphPost) TableName() string { return "scope_morph_posts" }

func (scopeMorphPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related: &scopeMorphTag{},
			Name:    "taggable",
			OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery {
				return q.WhereNull("deleted_at")
			},
		},
	}
}

type scopeMorphTag struct {
	ID   uint
	Name string
}

func (scopeMorphTag) TableName() string { return "scope_morph_tags" }

// --- (1) Related() SQL -----------------------------------------------------

func TestRelated_Many2Many_WithOnPivotQuery_SQL(t *testing.T) {
	q := newRelQueryWith(t, &scopeUser{})
	rel := q.Related(&scopeUser{ID: 7}, "Roles")
	sql := newRelationSQL(t, rel, &[]scopeRole{})
	assert.Equal(t,
		`select * from "scope_roles" inner join "scope_user_roles" ON "scope_user_roles"."scope_role_id" = "scope_roles"."id" where "scope_user_roles"."scope_user_id" = ? and "scope_user_roles"."active" = ?`,
		sql,
	)
}

func TestRelated_MorphToMany_WithOnPivotQuery_SQL(t *testing.T) {
	q := newRelQueryWith(t, &scopeMorphPost{})
	rel := q.Related(&scopeMorphPost{ID: 3}, "Tags")
	sql := newRelationSQL(t, rel, &[]scopeMorphTag{})
	assert.Equal(t,
		`select * from "scope_morph_tags" inner join "taggables" ON "taggables"."scope_morph_tag_id" = "scope_morph_tags"."id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ? and "taggables"."deleted_at" IS NULL`,
		sql,
	)
}

// Sanity: when OnPivotQuery is nil, no extra WHERE is appended.
func TestRelated_Many2Many_WithoutOnPivotQuery_SQL(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	rel := q.Related(&relUser{ID: 7}, "Roles")
	sql := newRelationSQL(t, rel, &[]relRole{})
	assert.Equal(t,
		`select * from "rel_roles" inner join "rel_user_roles" ON "rel_user_roles"."rel_role_id" = "rel_roles"."id" where "rel_user_roles"."rel_user_id" = ?`,
		sql,
	)
}
