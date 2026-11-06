package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// The of-many relation read (shared by the eager and lazy paths through oneOfManyJoin) answers to the
// relation's own trashed switches the way Laravel's does. Oracle (illuminate/database v12.55.1, real
// sqlite, /Users/dog/.claude/jobs/013c27a0/tmp/r14-probes/probe_ofmany.php -> probe_ofmany.out):
// plain latestOfMany carries the predicate in the aggregate and on the outer query (E1/E5); a
// relation declared withTrashed carries it in neither (E2); a relation declared onlyTrashed carries
// "is not null" on the outer query only (E3).

type omPost struct {
	ID       uint
	OmUserID uint
	databaseorm.SoftDeletes
}

func (omPost) TableName() string { return "om_posts" }

type omUser struct {
	ID         uint
	LatestPost *omPost `gorm:"-"`
}

func (omUser) TableName() string { return "om_users" }

func (omUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"LatestPost": contractsorm.HasOne{Related: &omPost{}, ForeignKey: "om_user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") }},
		"LatestAnyPost": contractsorm.HasOne{Related: &omPost{}, ForeignKey: "om_user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed().OfMany("", "") }},
		"LatestTrashed": contractsorm.HasOne{Related: &omPost{}, ForeignKey: "om_user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed().OfMany("", "") }},
	}
}

func TestOneOfManySoftDelete_RelationTrashedSwitches(t *testing.T) {
	read := func(name string) string {
		q := newRelQueryWith(t, &omPost{})
		return newRelationSQL(t, q.Related(&omUser{ID: 1}, name), &[]omPost{})
	}

	t.Run("plain", func(t *testing.T) {
		assert.Equal(t, `select "om_posts".* from "om_posts" inner join (select MAX("om_posts"."id") as "id_aggregate", "om_posts"."om_user_id" from "om_posts" where "om_posts"."om_user_id" = ? and "om_posts"."om_user_id" is not null and "om_posts"."deleted_at" is null group by "om_posts"."om_user_id") as "latest_post" on "latest_post"."id_aggregate" = "om_posts"."id" and "latest_post"."om_user_id" = "om_posts"."om_user_id" where "om_posts"."om_user_id" = ? and "om_posts"."om_user_id" is not null and "om_posts"."deleted_at" is null`, read("LatestPost"))
	})
	t.Run("withTrashed", func(t *testing.T) {
		assert.Equal(t, `select "om_posts".* from "om_posts" inner join (select MAX("om_posts"."id") as "id_aggregate", "om_posts"."om_user_id" from "om_posts" where "om_posts"."om_user_id" = ? and "om_posts"."om_user_id" is not null group by "om_posts"."om_user_id") as "latest_any_post" on "latest_any_post"."id_aggregate" = "om_posts"."id" and "latest_any_post"."om_user_id" = "om_posts"."om_user_id" where "om_posts"."om_user_id" = ? and "om_posts"."om_user_id" is not null`, read("LatestAnyPost"))
	})
	t.Run("onlyTrashed", func(t *testing.T) {
		assert.Equal(t, `select "om_posts".* from "om_posts" inner join (select MAX("om_posts"."id") as "id_aggregate", "om_posts"."om_user_id" from "om_posts" where "om_posts"."om_user_id" = ? and "om_posts"."om_user_id" is not null group by "om_posts"."om_user_id") as "latest_trashed" on "latest_trashed"."id_aggregate" = "om_posts"."id" and "latest_trashed"."om_user_id" = "om_posts"."om_user_id" where "om_posts"."om_user_id" = ? and "om_posts"."om_user_id" is not null and "om_posts"."deleted_at" is not null`, read("LatestTrashed"))
	})
}
