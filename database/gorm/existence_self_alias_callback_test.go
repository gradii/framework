package gorm

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// Round 15b gate: a whereHas callback scoping a self-aliased existence level. Laravel's
// getRelationExistenceQueryForSelfRelation renames the related model's table to the
// laravel_reserved_N hash and reads `<table> as <hash>`, and the callback runs on that query: the
// existence clauses it writes correlate to the hash, and a same-table relation under it compares its
// from with the aliased one, so it does not alias again. Before this pin the port compiled the
// callback's clauses against the plain table name, so the inner exists correlated with the outermost
// row (whereHas('parent', fn => has('child')) read "has a parent and has a child") - a silent wrong
// result. Expectations are illuminate/database v12.55.1 on real sqlite, byte-compared after the hash
// is normalised (php .claude/laravel-oracle/oracle.php /Users/dog/.claude/jobs/013c27a0/tmp/r15bgate-probes/selfnest.php, rows P1-P7; the
// same probe's rows R1/R2/R4 read [2,3], [2] and [1] from the seeded table).

type sacNode struct {
	ID       uint
	ParentID uint
}

func (sacNode) TableName() string { return "sn_nodes" }

func (sacNode) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Parent":   contractsorm.BelongsTo{Related: &sacNode{}, ForeignKey: "parent_id"},
		"Child":    contractsorm.HasOne{Related: &sacNode{}, ForeignKey: "parent_id"},
		"Children": contractsorm.HasMany{Related: &sacNode{}, ForeignKey: "parent_id"},
		"Leaves":   contractsorm.HasMany{Related: &sacLeaf{}, ForeignKey: "node_id"},
	}
}

type sacLeaf struct {
	ID     uint
	NodeID uint
	A      int
}

func (sacLeaf) TableName() string { return "sn_leaves" }

var sacAlias = regexp.MustCompile(`\blaravel_reserved_\d+\b`)

func TestExistenceSelfAlias_CallbackClausesCorrelateToTheAlias(t *testing.T) {
	node := func() *OrmBuilder { return compiledQuery(t, &sacNode{}, Conditions{}) }

	cases := []struct {
		name     string
		query    contractsorm.Query
		sql      string
		bindings []any
	}{
		{"P1 a same-table has inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query { return q.Has("Child") }),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and exists (select * from "sn_nodes" where "R"."id" = "sn_nodes"."parent_id"))`, nil},
		{"P2 another table's whereHas inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Leaves", func(l contractsorm.Query) contractsorm.Query { return l.Where("a", 1) })
		}),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and exists (select * from "sn_leaves" where "R"."id" = "sn_leaves"."node_id" and "a" = ?))`, []any{1}},
		{"P3 a where closure's has inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(w contractsorm.Query) contractsorm.Query { return w.Where("b", 2).Has("Leaves") })
		}),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and ("b" = ? and exists (select * from "sn_leaves" where "R"."id" = "sn_leaves"."node_id")))`, []any{2}},
		{"P4 the callback form of has('children.children')", node().WhereHas("Children", func(q contractsorm.Query) contractsorm.Query { return q.Has("Children") }),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "sn_nodes"."id" = "R"."parent_id" and exists (select * from "sn_nodes" where "R"."id" = "sn_nodes"."parent_id"))`, nil},
		{"P5 the dotted form compiles the same", node().Has("Children.Children"),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "sn_nodes"."id" = "R"."parent_id" and exists (select * from "sn_nodes" where "R"."id" = "sn_nodes"."parent_id"))`, nil},
		{"P6 withCount over a self level whose callback has another relation", node().WithCount(contractsorm.RelationCount{Name: "Parent", Callback: func(q contractsorm.Query) contractsorm.Query { return q.Has("Leaves") }}),
			`select "sn_nodes".*, (select count(*) from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and exists (select * from "sn_leaves" where "R"."id" = "sn_leaves"."node_id")) as "parent_count" from "sn_nodes"`, nil},
		// Rows A-G: php .claude/laravel-oracle/oracle.php /Users/dog/.claude/jobs/013c27a0/tmp/r15bgate-probes/adv.php (v12.55.1). Under the
		// aliased level an inner same-table level does not alias again and its own callback correlates to
		// that inner, unaliased level (A, F); the alias reaches doesntHave, orWhereHas, nested where
		// closures, the dotted chain and the count form (B, C, D, E, G).
		{"A a self level inside the self level's callback resets the alias for its own callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Parent", func(q2 contractsorm.Query) contractsorm.Query { return q2.Has("Leaves") })
		}),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and exists (select * from "sn_nodes" where "R"."parent_id" = "sn_nodes"."id" and exists (select * from "sn_leaves" where "sn_nodes"."id" = "sn_leaves"."node_id")))`, nil},
		{"B doesntHave inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query { return q.DoesntHave("Leaves") }),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and not exists (select * from "sn_leaves" where "R"."id" = "sn_leaves"."node_id"))`, nil},
		{"C orWhereHas inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query { return q.Where("b", 1).OrWhereHas("Leaves") }),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and ("b" = ? or exists (select * from "sn_leaves" where "R"."id" = "sn_leaves"."node_id")))`, []any{1}},
		{"D a has two where closures deep inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query {
			return q.Where(func(w contractsorm.Query) contractsorm.Query {
				return w.Where(func(w2 contractsorm.Query) contractsorm.Query { return w2.Has("Leaves") })
			})
		}),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and ((exists (select * from "sn_leaves" where "R"."id" = "sn_leaves"."node_id"))))`, nil},
		{"E a dotted has inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query { return q.Has("Children.Leaves") }),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and exists (select * from "sn_nodes" where "R"."id" = "sn_nodes"."parent_id" and exists (select * from "sn_leaves" where "sn_nodes"."id" = "sn_leaves"."node_id")))`, nil},
		{"F the unaliased inner level aliases a same-table relation under it again", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("Parent", func(q2 contractsorm.Query) contractsorm.Query { return q2.Has("Child") })
		}),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and exists (select * from "sn_nodes" where "R"."parent_id" = "sn_nodes"."id" and exists (select * from "sn_nodes" as "R" where "sn_nodes"."id" = "R"."parent_id")))`, nil},
		{"G a count has inside the self level's callback", node().WhereHas("Parent", func(q contractsorm.Query) contractsorm.Query { return q.Has("Leaves", ">=", 2) }),
			`select * from "sn_nodes" where exists (select * from "sn_nodes" as "R" where "R"."id" = "sn_nodes"."parent_id" and (select count(*) from "sn_leaves" where "R"."id" = "sn_leaves"."node_id") >= 2)`, nil},
		{"P7 an unaliased level keeps the plain table", node().WhereHas("Leaves", func(q contractsorm.Query) contractsorm.Query { return q.Where("a", 1) }),
			`select * from "sn_nodes" where exists (select * from "sn_leaves" where "sn_nodes"."id" = "sn_leaves"."node_id" and "a" = ?)`, []any{1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statement, bindings, err := tc.query.(*OrmBuilder).addGlobalScopes().compileSelect()
			require.NoError(t, err)
			assert.Equal(t, tc.sql, sacAlias.ReplaceAllString(statement, "R"))
			if tc.bindings == nil {
				assert.Empty(t, bindings)
			} else {
				assert.Equal(t, tc.bindings, bindings)
			}
		})
	}
}
