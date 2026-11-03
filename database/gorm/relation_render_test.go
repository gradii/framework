package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/database/gorm/relations"
)

// compileExistenceSubquery assembles a relation subquery with the framework builder and renders it to
// SQL text and bindings, which is how a compiled statement carries the relation SQL this package
// assembles itself.
//
// Every case pins the rendered text against the exact string the relation tests expect, so the
// subquery builder and the statements built from the same relation cannot drift apart. A
// many-to-many join reads the related table first, the operand order the oracle emits for the
// existence query (`on "far"."id" = "user_role"."related_id"`). The sample model comes from the
// descriptor, which is what makes the builder reachable from the compiled path.
func TestCompileExistenceSubquery_MatchesTheRelationSQL(t *testing.T) {
	tests := []struct {
		name     string
		model    any
		relation string
		sql      string
		bindings int
	}{
		{
			name:     "has one",
			model:    &relUser{},
			relation: "Profile",
			sql:      `select * from "rel_profiles" where "rel_users"."id" = "rel_profiles"."user_id"`,
		},
		{
			name:     "has many",
			model:    &relUser{},
			relation: "Books",
			sql:      `select * from "rel_books" where "rel_users"."id" = "rel_books"."user_id"`,
		},
		{
			name:     "belongs to",
			model:    &relBook{},
			relation: "Author",
			sql:      `select * from "rel_users" where "rel_books"."author_id" = "rel_users"."id"`,
		},
		{
			name:     "many to many",
			model:    &relUser{},
			relation: "Roles",
			sql:      `select * from "rel_roles" inner join "rel_user_roles" on "rel_roles"."id" = "rel_user_roles"."rel_role_id" where "rel_users"."id" = "rel_user_roles"."rel_user_id"`,
		},
		{
			name:     "morph one",
			model:    &relUser{},
			relation: "Logo",
			sql:      `select * from "rel_logos" where "rel_users"."id" = "rel_logos"."logoable_id" and "rel_logos"."logoable_type" = ?`,
			bindings: 1,
		},
		{
			name:     "morph many",
			model:    &relUser{},
			relation: "Houses",
			sql:      `select * from "rel_houses" where "rel_users"."id" = "rel_houses"."houseable_id" and "rel_houses"."houseable_type" = ?`,
			bindings: 1,
		},
		{
			name:     "morph to many",
			model:    &morphPost{},
			relation: "Tags",
			sql:      `select * from "morph_tags" inner join "taggables" on "morph_tags"."id" = "taggables"."morph_tag_id" where "morph_posts"."id" = "taggables"."taggable_id" and "taggables"."taggable_type" = ?`,
			bindings: 1,
		},
		{
			name:     "morphed by many",
			model:    &morphTag{},
			relation: "Posts",
			sql:      `select * from "morph_posts" inner join "taggables" on "morph_posts"."id" = "taggables"."morph_post_id" where "morph_tags"."id" = "taggables"."taggable_id" and "taggables"."taggable_type" = ?`,
			bindings: 1,
		},
		{
			name:     "has many through",
			model:    &relCountry{},
			relation: "Posts",
			sql:      `select * from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_countries"."id" = "rel_users"."rel_country_id"`,
		},
		{
			name:     "has one through",
			model:    &relCountry{},
			relation: "FirstPost",
			sql:      `select * from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_countries"."id" = "rel_users"."rel_country_id"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := newRelQueryWith(t, test.model)

			desc, err := relations.ResolveRelation(test.model, test.relation)
			require.NoError(t, err)
			require.NotNil(t, desc.RelatedModel, "the descriptor must carry a sample of the related model")

			builder, _, err := query.compileExistenceSubquery(desc, nil)
			require.NoError(t, err)
			require.NotNil(t, builder)

			statement, bindings := builder.ToSelectSQL()

			assert.Equal(t, test.sql, statement)
			assert.Len(t, bindings, test.bindings)
		})
	}
}
