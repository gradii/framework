package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
)

// The constraint closure OfMany / OfManyColumns take is Laravel's ofMany closure: it runs against
// every aggregate subquery, so its conditions choose the extreme row, while a condition chained on
// the relation query - before or after OfMany - stays on the outer read and filters the chosen row.
// Every expectation below is the oracle's (illuminate/database v12.55.1, real sqlite,
// /Users/dog/.claude/jobs/013c27a0/tmp/ofm/closure_probe.php -> closure_probe.out and
// closure_probe2.php -> closure_probe2.out; the s1..s8 relations there are S1..S8 here).

type ofmcComment struct {
	ID  uint
	PID uint `gorm:"column:p_id"`
}

func (ofmcComment) TableName() string { return "cm" }

type ofmcPost struct {
	ID          uint
	UID         uint `gorm:"column:u_id"`
	Title       string
	PublishedAt string
	databaseorm.SoftDeletes
}

func (ofmcPost) TableName() string { return "p" }

func (ofmcPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Cms": contractsorm.HasMany{Related: &ofmcComment{}, ForeignKey: "p_id"},
	}
}

type ofmcUser struct {
	ID uint
}

func (ofmcUser) TableName() string { return "u" }

func ofmcByID(constraint func(contractsorm.Query) contractsorm.Query) func(contractsorm.Query) contractsorm.Query {
	return func(q contractsorm.Query) contractsorm.Query {
		return q.OfManyColumns([]contractsorm.OfManyColumn{{Column: "id", Aggregate: "max"}}, constraint)
	}
}

func (ofmcUser) Relations() map[string]contractsorm.Relation {
	hasOne := func(onQuery func(contractsorm.Query) contractsorm.Query) contractsorm.HasOne {
		return contractsorm.HasOne{Related: &ofmcPost{}, ForeignKey: "u_id", OnQuery: onQuery}
	}

	return map[string]contractsorm.Relation{
		// ofMany(['id' => 'max'], fn ($q) => $q->where('title', 'x'))
		"S1": hasOne(ofmcByID(func(q contractsorm.Query) contractsorm.Query { return q.Where("title", "x") })),
		// ofMany(['id' => 'max'], fn ($q) => $q->where('title', 'x')->orWhere('title', 'y'))
		"S2": hasOne(ofmcByID(func(q contractsorm.Query) contractsorm.Query {
			return q.Where("title", "x").OrWhere("title", "y")
		})),
		// ofMany(['published_at' => 'max', 'id' => 'max'], fn ($q) => $q->where('title', 'x'))
		"S3": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.OfManyColumns([]contractsorm.OfManyColumn{
				{Column: "published_at", Aggregate: "max"},
				{Column: "id", Aggregate: "max"},
			}, func(sub contractsorm.Query) contractsorm.Query { return sub.Where("title", "x") })
		}),
		// ofMany(['id' => 'max'], fn ($q) => $q->withTrashed())
		"S4": hasOne(ofmcByID(func(q contractsorm.Query) contractsorm.Query { return q.WithTrashed() })),
		// ofMany(['id' => 'max'], fn ($q) => $q->onlyTrashed())
		"S5": hasOne(ofmcByID(func(q contractsorm.Query) contractsorm.Query { return q.OnlyTrashed() })),
		// ofMany(['id' => 'max'], fn ($q) => $q->whereHas('cms'))
		"S6": hasOne(ofmcByID(func(q contractsorm.Query) contractsorm.Query { return q.WhereHas("Cms") })),
		// ofMany(['id' => 'max'], fn ($q) => $q->where('title', 'x'))->where('title', 'after')
		"S7": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return ofmcByID(func(sub contractsorm.Query) contractsorm.Query { return sub.Where("title", "x") })(q).
				Where("title", "after")
		}),
		// ofMany(['id' => 'max'], fn ($q) => $q->join('cm', 'cm.p_id', '=', 'p.id')->where('cm.id', '>', 3))
		"S8": hasOne(ofmcByID(func(q contractsorm.Query) contractsorm.Query {
			return q.Join("cm", "cm.p_id", "=", "p.id").Where("cm.id", ">", 3)
		})),
		// The Go convenience: the string form takes the closure too, and keeps its own casing.
		"S9": hasOne(func(q contractsorm.Query) contractsorm.Query {
			return q.OfMany("id", "MAX", func(sub contractsorm.Query) contractsorm.Query { return sub.Where("title", "x") })
		}),
	}
}

// ofmcRead compiles the relation read Related builds for user 1, with its bindings.
func ofmcRead(t *testing.T, relation string) (string, []any) {
	t.Helper()

	q := newRelQueryWith(t, &ofmcPost{})
	built, ok := ormBuilderOf(q.Related(&ofmcUser{ID: 1}, relation))
	require.True(t, ok)

	statement, bindings, err := built.dest(&[]ofmcPost{}).compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

func TestOfManyClosure_RelationRead(t *testing.T) {
	tests := []struct {
		relation string
		sql      string
		bindings []any
	}{
		{"S1", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id") as "s1" on "s1"."id_aggregate" = "p"."id" and "s1"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{"x", uint(1), uint(1)}},
		// callScope wraps the wheres before the soft-delete scope in one group once the closure's
		// orWhere puts an "or" among them - the relation constraints included.
		{"S2", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where ("title" = ? or "title" = ? and "p"."u_id" = ? and "p"."u_id" is not null) and "p"."deleted_at" is null group by "p"."u_id") as "s2" on "s2"."id_aggregate" = "p"."id" and "s2"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{"x", "y", uint(1), uint(1)}},
		// The closure runs against every aggregate level.
		{"S3", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", min("p"."published_at") as "published_at_aggregate", "p"."u_id" from "p" inner join (select max("p"."published_at") as "published_at_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id") as "s3" on "s3"."published_at_aggregate" = "p"."published_at" and "s3"."u_id" = "p"."u_id" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s3" on "s3"."id_aggregate" = "p"."id" and "s3"."published_at_aggregate" = "p"."published_at" and "s3"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{"x", uint(1), "x", uint(1)}},
		// A removal inside the closure governs the aggregate only.
		{"S4", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."u_id" = ? and "p"."u_id" is not null group by "p"."u_id") as "s4" on "s4"."id_aggregate" = "p"."id" and "s4"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{uint(1), uint(1)}},
		{"S5", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."deleted_at" is not null and "p"."u_id" = ? and "p"."u_id" is not null group by "p"."u_id") as "s5" on "s5"."id_aggregate" = "p"."id" and "s5"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{uint(1), uint(1)}},
		// A whereHas inside the closure correlates to the related table.
		{"S6", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where exists (select * from "cm" where "p"."id" = "cm"."p_id") and "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id") as "s6" on "s6"."id_aggregate" = "p"."id" and "s6"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{uint(1), uint(1)}},
		// A where chained after OfMany stays on the outer query: it filters the chosen row.
		{"S7", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id") as "s7" on "s7"."id_aggregate" = "p"."id" and "s7"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "title" = ? and "p"."deleted_at" is null`,
			[]any{"x", uint(1), uint(1), "after"}},
		// A join the closure writes lands in the aggregate subquery, its bindings with it.
		{"S8", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" inner join "cm" on "cm"."p_id" = "p"."id" where "cm"."id" > ? and "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id") as "s8" on "s8"."id_aggregate" = "p"."id" and "s8"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{3, uint(1), uint(1)}},
		{"S9", `select "p".* from "p" inner join (select MAX("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null group by "p"."u_id") as "s9" on "s9"."id_aggregate" = "p"."id" and "s9"."u_id" = "p"."u_id" where "p"."u_id" = ? and "p"."u_id" is not null and "p"."deleted_at" is null`,
			[]any{"x", uint(1), uint(1)}},
	}

	for _, tt := range tests {
		t.Run(tt.relation, func(t *testing.T) {
			statement, bindings := ofmcRead(t, tt.relation)
			assert.Equal(t, tt.sql, statement)
			assert.Equal(t, tt.bindings, bindings)
		})
	}
}

// TestOfManyClosure_RelationReadCompilesTwice pins that the closure state is not consumed: the same
// relation query compiles to the same statement twice.
func TestOfManyClosure_RelationReadCompilesTwice(t *testing.T) {
	q := newRelQueryWith(t, &ofmcPost{})
	built, ok := ormBuilderOf(q.Related(&ofmcUser{ID: 1}, "S7"))
	require.True(t, ok)
	built = built.dest(&[]ofmcPost{})

	first, firstBindings, err := built.compileSelect()
	require.NoError(t, err)
	second, secondBindings, err := built.compileSelect()
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, firstBindings, secondBindings)
}

func TestOfManyClosure_ExistenceAndCount(t *testing.T) {
	tests := []struct {
		name     string
		query    func(q *OrmBuilder) contractsorm.Query
		sql      string
		bindings []any
	}{
		{"S1 whereHas", func(q *OrmBuilder) contractsorm.Query { return q.WhereHas("S1") },
			`select * from "u" where exists (select * from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s1" on "s1"."id_aggregate" = "p"."id" and "s1"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "p"."deleted_at" is null)`,
			[]any{"x"}},
		{"S1 withCount", func(q *OrmBuilder) contractsorm.Query { return q.WithCount("S1") },
			`select "u".*, (select count(*) from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s1" on "s1"."id_aggregate" = "p"."id" and "s1"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "p"."deleted_at" is null) as "s1_count" from "u"`,
			[]any{"x"}},
		{"S2 whereHas", func(q *OrmBuilder) contractsorm.Query { return q.WhereHas("S2") },
			`select * from "u" where exists (select * from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where ("title" = ? or "title" = ?) and "p"."deleted_at" is null group by "p"."u_id") as "s2" on "s2"."id_aggregate" = "p"."id" and "s2"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "p"."deleted_at" is null)`,
			[]any{"x", "y"}},
		{"S3 whereHas", func(q *OrmBuilder) contractsorm.Query { return q.WhereHas("S3") },
			`select * from "u" where exists (select * from "p" inner join (select max("p"."id") as "id_aggregate", min("p"."published_at") as "published_at_aggregate", "p"."u_id" from "p" inner join (select max("p"."published_at") as "published_at_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s3" on "s3"."published_at_aggregate" = "p"."published_at" and "s3"."u_id" = "p"."u_id" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s3" on "s3"."id_aggregate" = "p"."id" and "s3"."published_at_aggregate" = "p"."published_at" and "s3"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "p"."deleted_at" is null)`,
			[]any{"x", "x"}},
		{"S5 whereHas", func(q *OrmBuilder) contractsorm.Query { return q.WhereHas("S5") },
			`select * from "u" where exists (select * from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."deleted_at" is not null group by "p"."u_id") as "s5" on "s5"."id_aggregate" = "p"."id" and "s5"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "p"."deleted_at" is null)`,
			nil},
		{"S6 whereHas", func(q *OrmBuilder) contractsorm.Query { return q.WhereHas("S6") },
			`select * from "u" where exists (select * from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where exists (select * from "cm" where "p"."id" = "cm"."p_id") and "p"."deleted_at" is null group by "p"."u_id") as "s6" on "s6"."id_aggregate" = "p"."id" and "s6"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "p"."deleted_at" is null)`,
			nil},
		{"S7 whereHas", func(q *OrmBuilder) contractsorm.Query { return q.WhereHas("S7") },
			`select * from "u" where exists (select * from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s7" on "s7"."id_aggregate" = "p"."id" and "s7"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "title" = ? and "p"."deleted_at" is null)`,
			[]any{"x", "after"}},
		{"S7 withCount", func(q *OrmBuilder) contractsorm.Query { return q.WithCount("S7") },
			`select "u".*, (select count(*) from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s7" on "s7"."id_aggregate" = "p"."id" and "s7"."u_id" = "p"."u_id" where "u"."id" = "p"."u_id" and "title" = ? and "p"."deleted_at" is null) as "s7_count" from "u"`,
			[]any{"x", "after"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			built, ok := ormBuilderOf(tt.query(newRelQueryWith(t, &ofmcUser{})))
			require.True(t, ok)

			statement, bindings, err := built.compileSelect()
			require.NoError(t, err)
			assert.Equal(t, tt.sql, statement)
			assert.Equal(t, tt.bindings, bindings)
		})
	}
}

// ofmcEager compiles the eager read of relation for parents 1 and 2 the way the eager loader builds
// it: the in-list child, the relation's OnQuery, then the aggregate join.
func ofmcEager(t *testing.T, relation string) (string, []any) {
	t.Helper()

	desc := &relationDescriptor{
		Name:         relation,
		Kind:         relKindHasOne,
		RelatedTable: "p",
		RelatedModel: &ofmcPost{},
		References: []referenceKey{{
			PrimaryTable:  "u",
			PrimaryColumn: "id",
			ForeignTable:  "p",
			ForeignColumn: "u_id",
		}},
		OnQuery: ofmcUser{}.Relations()[relation].(contractsorm.HasOne).OnQuery,
	}

	q := newRelQueryWith(t, &ofmcUser{})
	child := q.relatedChild(desc.RelatedTable, desc.RelatedTable+"."+desc.References[0].ForeignColumn, []any{uint(1), uint(2)})
	child = child.Model(&ofmcPost{}).(*OrmBuilder)
	if scoped, ok := desc.OnQuery(child).(*OrmBuilder); ok {
		child = scoped
	}

	join, args, err := q.oneOfManyJoin(child, desc, child.conditions.oneOfMany, nil, false)
	require.NoError(t, err)
	child.query.JoinRaw(join, args, "inner")
	child.query.SelectRaw(q.ident(desc.RelatedTable, "*"), nil)

	statement, bindings, err := child.compileSelect()
	require.NoError(t, err)

	return statement, bindings
}

func TestOfManyClosure_EagerRead(t *testing.T) {
	tests := []struct {
		relation string
		sql      string
		bindings []any
	}{
		{"S1", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."u_id" in (1, 2) and "p"."deleted_at" is null group by "p"."u_id") as "s1" on "s1"."id_aggregate" = "p"."id" and "s1"."u_id" = "p"."u_id" where "p"."deleted_at" is null`,
			[]any{"x"}},
		{"S2", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where ("title" = ? or "title" = ? and "p"."u_id" in (1, 2)) and "p"."deleted_at" is null group by "p"."u_id") as "s2" on "s2"."id_aggregate" = "p"."id" and "s2"."u_id" = "p"."u_id" where "p"."deleted_at" is null`,
			[]any{"x", "y"}},
		{"S3", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", min("p"."published_at") as "published_at_aggregate", "p"."u_id" from "p" inner join (select max("p"."published_at") as "published_at_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."u_id" in (1, 2) and "p"."deleted_at" is null group by "p"."u_id") as "s3" on "s3"."published_at_aggregate" = "p"."published_at" and "s3"."u_id" = "p"."u_id" where "title" = ? and "p"."deleted_at" is null group by "p"."u_id") as "s3" on "s3"."id_aggregate" = "p"."id" and "s3"."published_at_aggregate" = "p"."published_at" and "s3"."u_id" = "p"."u_id" where "p"."deleted_at" is null`,
			[]any{"x", "x"}},
		{"S4", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."u_id" in (1, 2) group by "p"."u_id") as "s4" on "s4"."id_aggregate" = "p"."id" and "s4"."u_id" = "p"."u_id" where "p"."deleted_at" is null`,
			nil},
		{"S5", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "p"."deleted_at" is not null and "p"."u_id" in (1, 2) group by "p"."u_id") as "s5" on "s5"."id_aggregate" = "p"."id" and "s5"."u_id" = "p"."u_id" where "p"."deleted_at" is null`,
			nil},
		{"S6", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where exists (select * from "cm" where "p"."id" = "cm"."p_id") and "p"."u_id" in (1, 2) and "p"."deleted_at" is null group by "p"."u_id") as "s6" on "s6"."id_aggregate" = "p"."id" and "s6"."u_id" = "p"."u_id" where "p"."deleted_at" is null`,
			nil},
		{"S7", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" where "title" = ? and "p"."u_id" in (1, 2) and "p"."deleted_at" is null group by "p"."u_id") as "s7" on "s7"."id_aggregate" = "p"."id" and "s7"."u_id" = "p"."u_id" where "title" = ? and "p"."deleted_at" is null`,
			[]any{"x", "after"}},
		{"S8", `select "p".* from "p" inner join (select max("p"."id") as "id_aggregate", "p"."u_id" from "p" inner join "cm" on "cm"."p_id" = "p"."id" where "cm"."id" > ? and "p"."u_id" in (1, 2) and "p"."deleted_at" is null group by "p"."u_id") as "s8" on "s8"."id_aggregate" = "p"."id" and "s8"."u_id" = "p"."u_id" where "p"."deleted_at" is null`,
			[]any{3}},
	}

	for _, tt := range tests {
		t.Run(tt.relation, func(t *testing.T) {
			statement, bindings := ofmcEager(t, tt.relation)
			assert.Equal(t, tt.sql, statement)
			assert.Equal(t, tt.bindings, bindings)
		})
	}
}

// TestOfManyClosure_ClosureErrorSurfaces pins that a condition the closure cannot accept reports its
// error when the relation compiles, rather than being dropped with the subquery.
func TestOfManyClosure_ClosureErrorSurfaces(t *testing.T) {
	q := newRelQueryWith(t, &ofmcPost{})
	child := q.Model(&ofmcPost{}).OfMany("id", "MAX", func(sub contractsorm.Query) contractsorm.Query {
		return sub.OfMany("id", "count")
	}).(*OrmBuilder)

	desc := &relationDescriptor{
		Name: "S1", Kind: relKindHasOne, RelatedTable: "p", RelatedModel: &ofmcPost{},
		References: []referenceKey{{PrimaryTable: "u", PrimaryColumn: "id", ForeignTable: "p", ForeignColumn: "u_id"}},
	}

	_, _, err := q.oneOfManyJoin(child, desc, child.conditions.oneOfMany, nil, false)
	assert.Error(t, err)
}
