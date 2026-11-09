package gorm

import (
	"testing"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These regressions pin the OfMany closure review findings. SQL and bindings follow
// Illuminate Database v12.55.1 on SQLite; the matching execution tests live in
// tests/database/orm_relation/one_of_many_closure_regression_test.go.
type ofmClosureRegressionUser struct {
	ID uint
}

func (ofmClosureRegressionUser) TableName() string { return "u" }

func (ofmClosureRegressionUser) Relations() map[string]contractsorm.Relation {
	hasOne := func(constraint func(contractsorm.Query) contractsorm.Query) contractsorm.HasOne {
		return contractsorm.HasOne{
			Related: &ofmcPost{}, ForeignKey: "u_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("id", "max", constraint)
			},
		}
	}

	return map[string]contractsorm.Relation{
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
		"Through": contractsorm.HasOneThrough{
			Related: &ofmClosureRegressionPrice{}, Through: &ofmClosureRegressionMiddle{},
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

type ofmClosureRegressionMiddle struct {
	ID  uint
	UID uint `gorm:"column:u_id"`
	databaseorm.SoftDeletes
}

func (ofmClosureRegressionMiddle) TableName() string { return "mid" }

type ofmClosureRegressionPrice struct {
	ID          uint
	MiddleID    uint `gorm:"column:mid_id"`
	PublishedAt int
	databaseorm.SoftDeletes
}

func (ofmClosureRegressionPrice) TableName() string { return "p" }

// Exercise statement compilation through the public relationship APIs, not just
// oneOfManyJoin: callers must not discard its error and compile a plain relation.
func TestOfManyClosureRegression_ErrorPropagation(t *testing.T) {
	paths := []struct {
		name  string
		build func(contractsorm.Query, string) contractsorm.Query
	}{
		{"WhereHas", func(q contractsorm.Query, relation string) contractsorm.Query {
			return q.WhereHas(relation)
		}},
		{"WithCount", func(q contractsorm.Query, relation string) contractsorm.Query {
			return q.WithCount(relation)
		}},
		{"WithExists", func(q contractsorm.Query, relation string) contractsorm.Query {
			return q.WithExists(relation)
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
				built, ok := ormBuilderOf(path.build(newRelQueryWith(t, &ofmClosureRegressionUser{}), failure.relation))
				require.True(t, ok)

				for attempt := range 2 {
					statement, bindings, err := built.compileSelect()
					require.Error(t, err, "compile %d silently accepted the closure: SQL=%s bindings=%v", attempt+1, statement, bindings)
					assert.True(t, errors.Is(err, failure.wantErr), "unexpected error: %v", err)
					assert.ErrorContains(t, err, failure.message)
					assert.Empty(t, statement)
					assert.Empty(t, bindings)
				}
			})
		}
	}
}

func ofmClosureRegressionRead(t *testing.T, relation string, dest any) *OrmBuilder {
	t.Helper()

	query := newRelQueryWith(t, &ofmClosureRegressionUser{})
	built, ok := ormBuilderOf(query.Related(&ofmClosureRegressionUser{ID: 7}, relation))
	require.True(t, ok)

	return built.dest(dest)
}

func assertOfmClosureRegressionSQL(t *testing.T, built *OrmBuilder, wantSQL string, wantBindings []any) {
	t.Helper()

	// Reusing the same relation must preserve the closure's clauses and bindings.
	for attempt := range 2 {
		statement, bindings, err := built.addGlobalScopes().compileSelect()
		require.NoError(t, err, "compile %d", attempt+1)
		assert.Equal(t, wantSQL, statement, "compile %d", attempt+1)
		assert.Equal(t, wantBindings, bindings, "compile %d", attempt+1)
	}
}

func TestOfManyClosureRegression_ModelContext(t *testing.T) {
	built := ofmClosureRegressionRead(t, "Key", &[]ofmcPost{})

	// ofMany(['id' => 'max'], fn ($q) => $q->whereKey(1)): the key belongs
	// to p, not to the outer u model, and must be qualified before joining.
	assertOfmClosureRegressionSQL(t, built,
		`select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."id" = ? and "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id") as "key" on "key"."id_aggregate" = "p"."id" and "key"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
		[]any{uint(1), uint(7), uint(7)},
	)
}

func TestOfManyClosureRegression_AggregateClauses(t *testing.T) {
	for _, test := range []struct {
		relation string
		sql      string
		bindings []any
	}{
		{
			// ofMany(['id' => 'max'], fn ($q) => $q->havingRaw('max(p.id) > ?', [10])).
			relation: "Having",
			sql:      `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id" having max(p.id) > ?) as "having" on "having"."id_aggregate" = "p"."id" and "having"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			bindings: []any{uint(7), 10, uint(7)},
		},
		{
			// ofMany(['id' => 'max'], fn ($q) => $q->limit(0)).
			relation: "ZeroLimit",
			sql:      `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id" limit 0) as "zero_limit" on "zero_limit"."id_aggregate" = "p"."id" and "zero_limit"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			bindings: []any{uint(7), uint(7)},
		},
	} {
		t.Run(test.relation, func(t *testing.T) {
			built := ofmClosureRegressionRead(t, test.relation, &[]ofmcPost{})
			assertOfmClosureRegressionSQL(t, built, test.sql, test.bindings)
		})
	}
}

func TestOfManyClosureRegression_ThroughScopeRemoval(t *testing.T) {
	built := ofmClosureRegressionRead(t, "Through", &[]ofmClosureRegressionPrice{})

	// ofMany(['published_at' => 'max', 'id' => 'max'], fn ($q) =>
	//     $q->withoutGlobalScope('SoftDeletableHasManyThrough')).
	// Laravel reinstalls the through scope when it reapplies constraints to the
	// innermost subquery. The id tie-breaker honors the removal; the outer relation
	// still excludes deleted intermediates. Removing it from every level is wrong too.
	assertOfmClosureRegressionSQL(t, built,
		`select "p".* from "p" inner join "mid" on "mid"."id" = "p"."mid_id" inner join (select max("p"."id") as "id_aggregate", min("p"."published_at") as "published_at_aggregate", "mid"."u_id" from "p" inner join "mid" on "mid"."id" = "p"."mid_id" inner join (select max("p"."published_at") as "published_at_aggregate", "mid"."u_id" from "p" inner join "mid" on "mid"."id" = "p"."mid_id" where "mid"."u_id" = ? and "p"."deleted_at" is null and "mid"."deleted_at" is null group by "mid"."u_id") as "through" on "through"."published_at_aggregate" = "p"."published_at" and "through"."u_id" = "mid"."u_id" where "p"."deleted_at" is null group by "mid"."u_id") as "through" on "through"."id_aggregate" = "p"."id" and "through"."published_at_aggregate" = "p"."published_at" and "through"."u_id" = "mid"."u_id" where "mid"."u_id" = ? and "p"."deleted_at" is null and "mid"."deleted_at" is null`,
		[]any{uint(7), uint(7)},
	)
}
