package tests

import (
	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The ofMany closure, checked against real rows on every available driver: the SQL and binding pins
// live in database/gorm/one_of_many_closure_test.go and one_of_many_closure_regression_test.go;
// these confirm the statements select the rows Laravel's select on each engine. The expectations
// are Laravel's own results for the same schema, seed and relations, run on sqlite, MySQL 9.7 and
// PostgreSQL 18.3 (illuminate/database v12.55.1,
// /Users/dog/.claude/jobs/013c27a0/tmp/ofm/engines_probe.php -> engines_probe.out); all three
// engines agree.

type ofmRowsBook struct {
	Model
	SoftDeletes
	UserID uint
	Name   string
}

func (ofmRowsBook) TableName() string { return "books" }

func (ofmRowsBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.HasOne{Related: &Author{}, ForeignKey: "book_id"},
	}
}

type ofmRowsUser struct {
	Model
	SoftDeletes
	Name          string
	MatchedCount  int64        `gorm:"column:matched_count"`
	MatchedExists bool         `gorm:"column:matched_exists"`
	ClosureX      *ofmRowsBook `gorm:"-"`
	ChainX        *ofmRowsBook `gorm:"-"`
	OrGroup       *ofmRowsBook `gorm:"-"`
	Multi         *ofmRowsBook `gorm:"-"`
	TrashedIn     *ofmRowsBook `gorm:"-"`
	WhereHasIn    *ofmRowsBook `gorm:"-"`
	HavingCount   *ofmRowsBook `gorm:"-"`
}

func (ofmRowsUser) TableName() string { return "users" }

func (ofmRowsUser) Relations() map[string]contractsorm.Relation {
	byID := func(constraint func(contractsorm.Query) contractsorm.Query) contractsorm.HasOne {
		return contractsorm.HasOne{Related: &ofmRowsBook{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfManyColumns([]contractsorm.OfManyColumn{{Column: "id", Aggregate: "max"}}, constraint)
			}}
	}

	return map[string]contractsorm.Relation{
		// ofMany(['id' => 'max'], fn ($q) => $q->where('name', 'like', 'ofm_x%'))
		"ClosureX": byID(func(q contractsorm.Query) contractsorm.Query { return q.Where("name", "like", "ofm_x%") }),
		// ofMany(['id' => 'max'])->where('name', 'like', 'ofm_x%'): the latest row, kept only if it matches.
		"ChainX": contractsorm.HasOne{Related: &ofmRowsBook{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfManyColumns([]contractsorm.OfManyColumn{{Column: "id", Aggregate: "max"}}).Where("name", "like", "ofm_x%")
			}},
		// ofMany(['id' => 'max'], fn ($q) => $q->where('name', 'ofm_y2')->orWhere('name', 'ofm_x1')): the
		// or-chain is grouped before the soft-delete scope, so the trashed ofm_y2 cannot win the aggregate.
		"OrGroup": byID(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("name", "ofm_y2").OrWhere("name", "ofm_x1")
		}),
		// ofMany(['name' => 'max', 'id' => 'max'], fn ($q) => $q->where('name', 'like', 'ofm_x%'))
		"Multi": contractsorm.HasOne{Related: &ofmRowsBook{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfManyColumns([]contractsorm.OfManyColumn{{Column: "name", Aggregate: "max"}, {Column: "id", Aggregate: "max"}},
					func(sub contractsorm.Query) contractsorm.Query { return sub.Where("name", "like", "ofm_x%") })
			}},
		// ofMany(['id' => 'max'], fn ($q) => $q->withTrashed()): a trashed latest row wins the aggregate
		// and the outer soft-delete filter then drops it.
		"TrashedIn": byID(func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() }),
		// ofMany(['id' => 'max'], fn ($q) => $q->whereHas('author'))
		"WhereHasIn": byID(func(q contractsorm.Query) contractsorm.Query { return q.WhereHas("Author") }),
		// ofMany(['id' => 'max'], fn ($q) => $q->havingRaw('count(*) > ?', [1]))
		"HavingCount": byID(func(q contractsorm.Query) contractsorm.Query { return q.Having("count(*) > ?", 1) }),
	}
}

func (s *QueriesRelationshipsTestSuite) TestOfManyClosure_Rows() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			names := []string{"alice", "bob", "carol"}
			users := make(map[string]*User, len(names))
			for _, name := range names {
				user := &User{Name: "ofm_" + name}
				s.Require().NoError(query.Query().Create(user))
				users[name] = user
			}

			labels := map[uint]string{}
			book := func(label, owner, name string, trashed, authored bool) {
				row := &ofmRowsBook{UserID: users[owner].ID, Name: name}
				s.Require().NoError(query.Query().Create(row))
				labels[row.ID] = label
				if authored {
					s.Require().NoError(query.Query().Create(&Author{BookID: row.ID, Name: "ofm_author"}))
				}
				if trashed {
					_, err := query.Query().Delete(row)
					s.Require().NoError(err)
				}
			}
			book("A1", "alice", "ofm_x1", false, true)
			book("A2", "alice", "ofm_y1", false, false)
			book("A3", "alice", "ofm_x2", false, false)
			book("A4", "alice", "ofm_y2", true, false)
			book("A5", "alice", "ofm_y4", false, false)
			book("B1", "bob", "ofm_y3", false, false)
			book("C1", "carol", "ofm_x3", false, false)
			book("C2", "carol", "ofm_x4", true, false)

			label := func(row *ofmRowsBook) string {
				if row == nil {
					return "-"
				}
				return labels[row.ID]
			}

			for _, tt := range []struct {
				relation string
				picked   [3]string // alice, bob, carol; "-" is no row
				loaded   func(ofmRowsUser) *ofmRowsBook
			}{
				{"ClosureX", [3]string{"A3", "-", "C1"}, func(u ofmRowsUser) *ofmRowsBook { return u.ClosureX }},
				{"ChainX", [3]string{"-", "-", "C1"}, func(u ofmRowsUser) *ofmRowsBook { return u.ChainX }},
				{"OrGroup", [3]string{"A1", "-", "-"}, func(u ofmRowsUser) *ofmRowsBook { return u.OrGroup }},
				{"Multi", [3]string{"A3", "-", "C1"}, func(u ofmRowsUser) *ofmRowsBook { return u.Multi }},
				{"TrashedIn", [3]string{"A5", "B1", "-"}, func(u ofmRowsUser) *ofmRowsBook { return u.TrashedIn }},
				{"WhereHasIn", [3]string{"A1", "-", "-"}, func(u ofmRowsUser) *ofmRowsBook { return u.WhereHasIn }},
				{"HavingCount", [3]string{"A5", "-", "-"}, func(u ofmRowsUser) *ofmRowsBook { return u.HavingCount }},
			} {
				s.Run(tt.relation, func() {
					var wantParents []string
					var wantCounts []int64
					var wantExists []bool
					for i, picked := range tt.picked {
						matched := picked != "-"
						if matched {
							wantParents = append(wantParents, "ofm_"+names[i])
						}
						wantCounts = append(wantCounts, map[bool]int64{true: 1, false: 0}[matched])
						wantExists = append(wantExists, matched)
					}
					parents := func() contractsorm.Query {
						return query.Query().Model(&ofmRowsUser{}).Where("name", "like", "ofm_%").OrderBy("id")
					}

					// Related: the relation read per parent.
					for i, name := range names {
						var rows []*ofmRowsBook
						s.Require().NoError(query.Query().Related(&ofmRowsUser{Model: Model{ID: users[name].ID}}, tt.relation).Get(&rows))
						got := "-"
						if len(rows) > 0 {
							s.Require().Len(rows, 1, name)
							got = label(rows[0])
						}
						s.Equal(tt.picked[i], got, "Related %s", name)
					}

					// With: the eager read for every parent at once.
					var loaded []ofmRowsUser
					s.Require().NoError(parents().With(tt.relation).Get(&loaded))
					s.Require().Len(loaded, len(names))
					for i := range names {
						s.Equal(tt.picked[i], label(tt.loaded(loaded[i])), "With %s", names[i])
					}

					var matched []ofmRowsUser
					s.Require().NoError(parents().WhereHas(tt.relation).Get(&matched))
					var gotParents []string
					for _, user := range matched {
						gotParents = append(gotParents, user.Name)
					}
					s.Equal(wantParents, gotParents, "WhereHas")

					var counted []ofmRowsUser
					s.Require().NoError(parents().WithCount(tt.relation + " as matched_count").Get(&counted))
					var gotCounts []int64
					for _, user := range counted {
						gotCounts = append(gotCounts, user.MatchedCount)
					}
					s.Equal(wantCounts, gotCounts, "WithCount")

					var existing []ofmRowsUser
					s.Require().NoError(parents().WithExists(tt.relation + " as matched_exists").Get(&existing))
					var gotExists []bool
					for _, user := range existing {
						gotExists = append(gotExists, user.MatchedExists)
					}
					s.Equal(wantExists, gotExists, "WithExists")
				})
			}
		})
	}
}
