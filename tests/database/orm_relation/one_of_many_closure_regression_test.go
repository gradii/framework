package orm_relation

import (
	"testing"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These execution tests complement database/gorm/one_of_many_closure_regression_test.go.
// Expected rows were checked against Illuminate Database v12.55.1 on SQLite.
type ofmClosureRegressionUser struct {
	ID                uint
	MatchedCount      int64                     `gorm:"column:matched_count"`
	MatchedExists     bool                      `gorm:"column:matched_exists"`
	Latest            *ofmClosureRegressionPost `gorm:"-"`
	Missing           *ofmClosureRegressionPost `gorm:"-"`
	Invalid           *ofmClosureRegressionPost `gorm:"-"`
	Key               *ofmClosureRegressionPost `gorm:"-"`
	Having            *ofmClosureRegressionPost `gorm:"-"`
	ZeroLimit         *ofmClosureRegressionPost `gorm:"-"`
	Through           *ofmClosureRegressionPost `gorm:"-"`
	BuilderError      *ofmClosureRegressionPost `gorm:"-"`
	SelectHaving      *ofmClosureRegressionPost `gorm:"-"`
	SelectHavingBound *ofmClosureRegressionPost `gorm:"-"`
	SelectHavingMulti *ofmClosureRegressionPost `gorm:"-"`
}

func (ofmClosureRegressionUser) TableName() string { return "u" }

func (ofmClosureRegressionUser) Relations() map[string]contractsorm.Relation {
	hasOne := func(constraint func(contractsorm.Query) contractsorm.Query) contractsorm.HasOne {
		return contractsorm.HasOne{
			Related: &ofmClosureRegressionPost{}, ForeignKey: "u_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("id", "max", constraint)
			},
		}
	}
	countCandidates := func(q contractsorm.Query) contractsorm.Query {
		return q.SelectRaw("count(*) + ? as candidate_count", 10).
			Having("candidate_count > ?", 11).
			OrderBy("candidate_count", "desc")
	}

	return map[string]contractsorm.Relation{
		"Latest": hasOne(nil),
		"Missing": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.WhereHas("MissingRelation")
		}),
		"Invalid": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.OfMany("id", "count")
		}),
		"Key": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.WhereKey(uint(1))
		}),
		"Having": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.Having("max(p.id) > ?", 10)
		}),
		"ZeroLimit": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.Limit(0)
		}),
		"BuilderError": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.Having("p.id", ">", nil)
		}),
		"SelectHaving": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.SelectRaw("count(*) as candidate_count").Having("candidate_count > ?", 1)
		}),
		"SelectHavingBound": hasOne(countCandidates),
		"SelectHavingMulti": contractsorm.HasOne{
			Related: &ofmClosureRegressionPost{}, ForeignKey: "u_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfManyColumns([]contractsorm.OfManyColumn{
					{Column: "published_at", Aggregate: "max"},
					{Column: "id", Aggregate: "max"},
				}, countCandidates)
			},
		},
		"Through": contractsorm.HasOneThrough{
			Related: &ofmClosureRegressionPost{}, Through: &ofmClosureRegressionMiddle{},
			FirstKey: "u_id", SecondKey: "mid_id", LocalKey: "id", SecondLocalKey: "id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfManyColumns([]contractsorm.OfManyColumn{
					{Column: "published_at", Aggregate: "max"},
					{Column: "id", Aggregate: "max"},
				}, func(sub contractsorm.Query) contractsorm.Query {
					return sub.WithoutGlobalScopes(contractsorm.SoftDeletableHasManyThrough)
				})
			},
		},
	}
}

type ofmClosureRegressionPost struct {
	ID          uint
	UID         uint `gorm:"column:u_id"`
	MiddleID    uint `gorm:"column:mid_id"`
	PublishedAt int
	databaseorm.SoftDeletes
}

func (ofmClosureRegressionPost) TableName() string { return "p" }

func (ofmClosureRegressionPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{}
}

type ofmClosureRegressionMiddle struct {
	ID  uint
	UID uint `gorm:"column:u_id"`
	databaseorm.SoftDeletes
}

func (ofmClosureRegressionMiddle) TableName() string { return "mid" }

func newOfmClosureRegressionQuery(t *testing.T) contractsorm.Query {
	t.Helper()

	query := newRelationQuery(t)
	runRelationSchema(t, query,
		`create table u (id integer primary key)`,
		`create table mid (id integer primary key, u_id integer not null, deleted_at text null)`,
		`create table p (id integer primary key, u_id integer not null, mid_id integer not null, published_at integer not null, deleted_at text null)`,
		`insert into u (id) values (1), (2)`,
		`insert into mid (id, u_id, deleted_at) values (1, 1, null), (2, 1, '2026-11-01 00:00:00'), (3, 2, null), (4, 2, '2026-11-01 00:00:00')`,
		`insert into p (id, u_id, mid_id, published_at) values (1, 1, 1, 100), (2, 1, 2, 100), (3, 2, 3, 100), (4, 2, 4, 200)`,
	)

	return query
}

func TestOfManyClosureRegression_ErrorPropagation(t *testing.T) {
	paths := []struct {
		name string
		run  func(contractsorm.Query, string) error
	}{
		{"Related", func(q contractsorm.Query, relation string) error {
			var posts []ofmClosureRegressionPost
			return q.Related(&ofmClosureRegressionUser{ID: 1}, relation).Get(&posts)
		}},
		{"With", func(q contractsorm.Query, relation string) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).With(relation).Get(&users)
		}},
		{"WhereHas", func(q contractsorm.Query, relation string) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).WhereHas(relation).Get(&users)
		}},
		{"WithCount", func(q contractsorm.Query, relation string) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).WithCount(relation + " as matched_count").Get(&users)
		}},
		{"WithExists", func(q contractsorm.Query, relation string) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).WithExists(relation + " as matched_exists").Get(&users)
		}},
	}
	for _, failure := range []struct {
		relation string
		wantErr  error
		message  string
	}{
		{"Missing", errors.OrmRelationNotFound, "MissingRelation"},
		{"Invalid", errors.OrmRelationInvalidOfManyAggregate, "Invalid aggregate [count]"},
	} {
		for _, path := range paths {
			t.Run(failure.relation+"/"+path.name, func(t *testing.T) {
				query := newOfmClosureRegressionQuery(t)
				err := path.run(cloneRelationQuery(t, query), failure.relation)
				require.Error(t, err, "an invalid closure must not execute an unconstrained relation")
				assert.True(t, errors.Is(err, failure.wantErr), "unexpected error: %v", err)
				assert.ErrorContains(t, err, failure.message)
			})
		}
	}
}

// wantIDs names the selected post for users 1 and 2, with zero meaning no match.
// Test both positive and negative parents through each public consumption path.
func assertOfmClosureRegressionResults(t *testing.T, query contractsorm.Query, relation string, loaded func(ofmClosureRegressionUser) *ofmClosureRegressionPost, wantIDs [2]uint) {
	t.Helper()

	t.Run("Related", func(t *testing.T) {
		for index, wantID := range wantIDs {
			parentID := uint(index + 1)
			var posts []ofmClosureRegressionPost
			require.NoError(t, cloneRelationQuery(t, query).Related(&ofmClosureRegressionUser{ID: parentID}, relation).Get(&posts))
			if wantID == 0 {
				assert.Empty(t, posts, "user %d must have no matching post", parentID)
				continue
			}
			require.Len(t, posts, 1, "user %d", parentID)
			assert.Equal(t, wantID, posts[0].ID, "user %d", parentID)
		}
	})

	t.Run("With", func(t *testing.T) {
		var users []ofmClosureRegressionUser
		require.NoError(t, cloneRelationQuery(t, query).Model(&ofmClosureRegressionUser{}).With(relation).OrderBy("id").Get(&users))
		require.Len(t, users, len(wantIDs))
		for index, wantID := range wantIDs {
			assert.Equal(t, uint(index+1), users[index].ID)
			post := loaded(users[index])
			if wantID == 0 {
				assert.Nil(t, post, "user %d must have no matching post", users[index].ID)
				continue
			}
			require.NotNil(t, post, "user %d", users[index].ID)
			assert.Equal(t, wantID, post.ID)
		}
	})

	t.Run("WhereHas", func(t *testing.T) {
		var users []ofmClosureRegressionUser
		require.NoError(t, cloneRelationQuery(t, query).Model(&ofmClosureRegressionUser{}).WhereHas(relation).OrderBy("id").Get(&users))
		wantParents := make([]uint, 0, len(wantIDs))
		for index, wantID := range wantIDs {
			if wantID != 0 {
				wantParents = append(wantParents, uint(index+1))
			}
		}
		gotParents := make([]uint, 0, len(users))
		for _, user := range users {
			gotParents = append(gotParents, user.ID)
		}
		assert.Equal(t, wantParents, gotParents)
	})

	t.Run("WithCount", func(t *testing.T) {
		var users []ofmClosureRegressionUser
		require.NoError(t, cloneRelationQuery(t, query).Model(&ofmClosureRegressionUser{}).WithCount(relation+" as matched_count").OrderBy("id").Get(&users))
		require.Len(t, users, len(wantIDs))
		for index, wantID := range wantIDs {
			assert.Equal(t, uint(index+1), users[index].ID)
			var wantCount int64
			if wantID != 0 {
				wantCount = 1
			}
			assert.Equal(t, wantCount, users[index].MatchedCount, "user %d", users[index].ID)
		}
	})

	t.Run("WithExists", func(t *testing.T) {
		var users []ofmClosureRegressionUser
		require.NoError(t, cloneRelationQuery(t, query).Model(&ofmClosureRegressionUser{}).WithExists(relation+" as matched_exists").OrderBy("id").Get(&users))
		require.Len(t, users, len(wantIDs))
		for index, wantID := range wantIDs {
			assert.Equal(t, uint(index+1), users[index].ID)
			assert.Equal(t, wantID != 0, users[index].MatchedExists, "user %d", users[index].ID)
		}
	})
}

func TestOfManyClosureRegression_ModelContext(t *testing.T) {
	query := newOfmClosureRegressionQuery(t)

	// whereKey(1) selects user 1's older post and excludes user 2. If a failed
	// key condition is discarded, WhereHas and the aggregates match both users.
	assertOfmClosureRegressionResults(t, query, "Key", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost {
		return user.Key
	}, [2]uint{1, 0})
}

func TestOfManyClosureRegression_AggregateClauses(t *testing.T) {
	for _, test := range []struct {
		relation string
		loaded   func(ofmClosureRegressionUser) *ofmClosureRegressionPost
		wantIDs  [2]uint
	}{
		{"Latest", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost { return user.Latest }, [2]uint{2, 4}},
		{"Having", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost { return user.Having }, [2]uint{}},
		{"ZeroLimit", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost { return user.ZeroLimit }, [2]uint{}},
	} {
		t.Run(test.relation, func(t *testing.T) {
			query := newOfmClosureRegressionQuery(t)
			// Both parents have rows without the closure. HAVING max(p.id) > 10
			// and LIMIT 0 must each eliminate the candidates, on every read path.
			assertOfmClosureRegressionResults(t, query, test.relation, test.loaded, test.wantIDs)
		})
	}
}

func TestOfManyClosureRegression_ThroughScopeRemoval(t *testing.T) {
	query := newOfmClosureRegressionQuery(t)

	// User 1: equal published_at values let the id tie-breaker pick post 2 via
	// a deleted intermediate; the outer relation then excludes it. Keeping the
	// through scope on the tie-breaker incorrectly returns post 1 instead.
	// User 2: the innermost scope must exclude post 4's newer published_at, so
	// post 3 survives. Removing the through scope from every level loses post 3.
	assertOfmClosureRegressionResults(t, query, "Through", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost {
		return user.Through
	}, [2]uint{0, 3})
}

func TestOfManyClosureRegression_QueryBuilderError(t *testing.T) {
	for _, path := range []struct {
		name string
		run  func(contractsorm.Query) error
	}{
		{"Plain", func(q contractsorm.Query) error {
			var posts []ofmClosureRegressionPost
			return q.Model(&ofmClosureRegressionPost{}).Having("p.id", ">", nil).Get(&posts)
		}},
		{"Related", func(q contractsorm.Query) error {
			var posts []ofmClosureRegressionPost
			return q.Related(&ofmClosureRegressionUser{ID: 1}, "BuilderError").Get(&posts)
		}},
		{"With", func(q contractsorm.Query) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).With("BuilderError").Get(&users)
		}},
		{"WhereHas", func(q contractsorm.Query) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).WhereHas("BuilderError").Get(&users)
		}},
		{"WithCount", func(q contractsorm.Query) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).WithCount("BuilderError as matched_count").Get(&users)
		}},
		{"WithExists", func(q contractsorm.Query) error {
			var users []ofmClosureRegressionUser
			return q.Model(&ofmClosureRegressionUser{}).WithExists("BuilderError as matched_exists").Get(&users)
		}},
	} {
		t.Run(path.name, func(t *testing.T) {
			query := newOfmClosureRegressionQuery(t)
			err := path.run(cloneRelationQuery(t, query))
			require.Error(t, err, "invalid HAVING must fail instead of returning rows without the filter")
			assert.True(t, errors.Is(err, errors.QueryIllegalOperatorAndValue), "unexpected error: %v", err)
		})
	}
}

func TestOfManyClosureRegression_SelectClauses(t *testing.T) {
	for _, test := range []struct {
		relation string
		loaded   func(ofmClosureRegressionUser) *ofmClosureRegressionPost
	}{
		{"SelectHaving", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost { return user.SelectHaving }},
		{"SelectHavingBound", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost { return user.SelectHavingBound }},
		{"SelectHavingMulti", func(user ofmClosureRegressionUser) *ofmClosureRegressionPost { return user.SelectHavingMulti }},
	} {
		t.Run(test.relation, func(t *testing.T) {
			query := newOfmClosureRegressionQuery(t)
			result, err := query.Exec("delete from p where id = ?", 4)
			require.NoError(t, err)
			require.EqualValues(t, 1, result.RowsAffected)

			// User 1 has two candidates sharing published_at; user 2 has one.
			// candidate_count > 1 (or count(*) + 10 > 11) selects post 2 only.
			// The expression and its bindings must reach every aggregate level;
			// dropping just the HAVING would incorrectly include user 2 as well.
			assertOfmClosureRegressionResults(t, query, test.relation, test.loaded, [2]uint{2, 0})
		})
	}
}
