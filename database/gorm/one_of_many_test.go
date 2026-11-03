package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// These tests cover oneOfManyJoin, the shared builder that renders the grouped per-parent aggregate
// subquery OfMany / LatestOfMany / OldestOfMany install on every read of a HasOne / MorphOne
// relation — the relation read path (Related), the eager loader and the existence subqueries.
//
// The expected fragments are Laravel's (measured with .claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1, sqlite grammar): the subquery alias is the relation name in snake
// case, the aggregate column is "<column>_aggregate", and the join condition and grouping match
// ofMany byte for byte. The assertions are exact full statements, not substrings: oneOfManyJoin
// used to return an "INNER JOIN (...)" fragment even though the join list is rendered with an
// "inner" keyword prefix, so the eager OfMany path emitted "inner join INNER JOIN (...)" — invalid
// SQL that a substring check for "INNER JOIN (" happily accepted.
//
// The Eager tests below pin the shape a one-of-many eager read emits: the loader pops its
// parent-key in-list (and a morph relation's type filter) from the child and re-states it INSIDE
// the aggregate subquery, inlined the way Laravel's whereIntegerInRaw inlines integer keys, so the
// outer query carries no constraint — every expected string is the oracle's for the same read.

// renderOfManySQL runs the same steps the eager loader takes for an OfMany condition — register the
// condition on the child (OfMany records the marks its follow-on constraints are delimited by),
// build the join fragment, queue it with the "inner" keyword, and compile the statement — so the
// rendered SQL covers the JoinRaw prefixing the eager path relies on.
func renderOfManySQL(t *testing.T, desc *relationDescriptor, column, aggregate string) string {
	t.Helper()

	q := newRelQueryWith(t, &relUser{})
	child := newRelQueryWith(t, desc.RelatedModel)
	child = child.OfMany(column, aggregate).(*OrmBuilder)

	join, args, err := q.oneOfManyJoin(child, desc, child.conditions.oneOfMany, nil, false)
	assert.NoError(t, err)

	child.query.JoinRaw(join, args, "inner")
	if len(child.query.Selects()) == 0 {
		child.query.SelectRaw(q.ident(desc.RelatedTable, "*"), nil)
	}

	statement, _, err := child.compileSelect()
	assert.NoError(t, err)

	return statement
}

func TestOneOfManyJoin_HasOne_SQL(t *testing.T) {
	desc := &relationDescriptor{
		Name:         "LatestLogin",
		Kind:         relKindHasOne,
		RelatedTable: "rel_books",
		RelatedModel: &relBook{},
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_books",
			ForeignColumn: "user_id",
		}},
	}

	sql := renderOfManySQL(t, desc, "id", "MAX")

	// The child here carries no parent keys, so the join is the keyless shape the existence
	// subqueries render; the eager shape — where the loader's in-list moves inside the subquery —
	// is pinned by the Eager tests below.
	assert.Equal(t, `select "rel_books".* from "rel_books" inner join (select MAX("rel_books"."id") as "id_aggregate", "rel_books"."user_id" from "rel_books" group by "rel_books"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "rel_books"."id" and "latest_login"."user_id" = "rel_books"."user_id"`, sql)
}

// renderEagerOfManySQL runs the steps the eager loader takes for an OfMany relation: build the
// child with the chunk's parent-key in-list (and the morph-type filter for a morph relation),
// apply the relation's own scope, build the join and queue it with the "inner" keyword. The
// rendered statement is the one an eager read emits, bindings included.
func renderEagerOfManySQL(t *testing.T, desc *relationDescriptor, keys []any) (string, []any) {
	t.Helper()

	q := newRelQueryWith(t, &relUser{})
	child := q.relatedChild(desc.RelatedTable, desc.RelatedTable+"."+desc.References[0].ForeignColumn, keys)
	if desc.Kind == relKindMorphOne {
		child = child.Where(desc.RelatedTable+"."+desc.MorphTypeColumn, desc.MorphValue).(*OrmBuilder)
	}
	if desc.OnQuery != nil {
		if scoped, ok := desc.OnQuery(child).(*OrmBuilder); ok {
			child = scoped
		}
	}

	join, args, err := q.oneOfManyJoin(child, desc, child.conditions.oneOfMany, nil, false)
	assert.NoError(t, err)

	child.query.JoinRaw(join, args, "inner")
	if len(child.query.Selects()) == 0 {
		child.query.SelectRaw(q.ident(desc.RelatedTable, "*"), nil)
	}

	statement, bindings, err := child.compileSelect()
	assert.NoError(t, err)

	return statement, bindings
}

// TestEagerOfManyJoin_HasOne_InListInsideSubQuery pins the eager shape Laravel's addEagerConstraints
// writes: the parent-key in-list lives INSIDE the aggregate subquery (inlined, the way
// whereIntegerInRaw inlines integer keys), and the outer query carries no constraint at all.
// Oracle, sqlite: the same statement for the eager read of latest_login with keys 1, 2.
func TestEagerOfManyJoin_HasOne_InListInsideSubQuery(t *testing.T) {
	desc := &relationDescriptor{
		Name:         "LatestLogin",
		Kind:         relKindHasOne,
		RelatedTable: "rel_books",
		RelatedModel: &relBook{},
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_books",
			ForeignColumn: "user_id",
		}},
		OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
	}

	statement, bindings := renderEagerOfManySQL(t, desc, []any{uint(1), uint(2)})

	assert.Empty(t, bindings)
	assert.Equal(t, `select "rel_books".* from "rel_books" inner join (select MAX("rel_books"."id") as "id_aggregate", "rel_books"."user_id" from "rel_books" where "rel_books"."user_id" in (1, 2) group by "rel_books"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "rel_books"."id" and "latest_login"."user_id" = "rel_books"."user_id"`, statement)
}

// TestEagerOfManyJoin_MorphOne_InListInsideSubQuery pins the morph eager shape: the in-list leads
// inside the subquery and the morph-type filter follows it, both removed from the outer query.
// Oracle, sqlite: the same statement for the eager read of current_logo with keys 1, 2; the
// framework's morph value is the parent table name, so the oracle's `?` binds "rel_users" here.
func TestEagerOfManyJoin_MorphOne_InListInsideSubQuery(t *testing.T) {
	desc := &relationDescriptor{
		Name:            "CurrentLogo",
		Kind:            relKindMorphOne,
		RelatedTable:    "rel_logos",
		RelatedModel:    &relLogo{},
		MorphTypeColumn: "logoable_type",
		MorphIDColumn:   "logoable_id",
		MorphValue:      "rel_users",
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_logos",
			ForeignColumn: "logoable_id",
		}},
		OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
	}

	statement, bindings := renderEagerOfManySQL(t, desc, []any{uint(1), uint(2)})

	assert.Equal(t, []any{"rel_users"}, bindings)
	assert.Equal(t, `select "rel_logos".* from "rel_logos" inner join (select MAX("rel_logos"."id") as "id_aggregate", "rel_logos"."logoable_id", "rel_logos"."logoable_type" from "rel_logos" where "rel_logos"."logoable_id" in (1, 2) and "rel_logos"."logoable_type" = ? group by "rel_logos"."logoable_id", "rel_logos"."logoable_type") as "current_logo" on "current_logo"."id_aggregate" = "rel_logos"."id" and "current_logo"."logoable_type" = "rel_logos"."logoable_type" and "current_logo"."logoable_id" = "rel_logos"."logoable_id"`, statement)
}

// TestEagerOfManyJoin_ClosureConstraintLeadsInList pins the order inside the aggregate subquery
// when the relation's own scope adds a constraint: the scope's clause (the Go form of Laravel's
// ofMany closure) comes first, then the in-list. Oracle, sqlite: for keys 1, 2 the subquery reads
// where "type" = ? and "rel_states"."user_id" in (1, 2) group by "rel_states"."user_id".
func TestEagerOfManyJoin_ClosureConstraintLeadsInList(t *testing.T) {
	desc := &relationDescriptor{
		Name:         "FooState",
		Kind:         relKindHasOne,
		RelatedTable: "rel_states",
		RelatedModel: &oomState{},
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_states",
			ForeignColumn: "user_id",
		}},
		OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "").Where("type", "foo") },
	}

	statement, bindings := renderEagerOfManySQL(t, desc, []any{uint(1), uint(2)})

	assert.Equal(t, []any{"foo"}, bindings)
	assert.Equal(t, `select "rel_states".* from "rel_states" inner join (select MAX("rel_states"."id") as "id_aggregate", "rel_states"."user_id" from "rel_states" where "type" = ? and "rel_states"."user_id" in (1, 2) group by "rel_states"."user_id") as "foo_state" on "foo_state"."id_aggregate" = "rel_states"."id" and "foo_state"."user_id" = "rel_states"."user_id"`, statement)
}

// TestEagerOfManyJoin_StringKeysStayParameterized pins the other half of Laravel's whereInMethod
// split: only integer parent keys inline through whereIntegerInRaw; a string-keyed parent (UUID)
// keeps its bindings, so the subquery reads `where "rel_books"."user_id" in (?, ?)` with the keys
// bound. Oracle, sqlite: the same statement for the same eager read with keys "a", "b".
func TestEagerOfManyJoin_StringKeysStayParameterized(t *testing.T) {
	desc := &relationDescriptor{
		Name:         "LatestLogin",
		Kind:         relKindHasOne,
		RelatedTable: "rel_books",
		RelatedModel: &relBook{},
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_books",
			ForeignColumn: "user_id",
		}},
		OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
	}

	statement, bindings := renderEagerOfManySQL(t, desc, []any{"a", "b"})

	assert.Equal(t, []any{"a", "b"}, bindings)
	assert.Equal(t, `select "rel_books".* from "rel_books" inner join (select MAX("rel_books"."id") as "id_aggregate", "rel_books"."user_id" from "rel_books" where "rel_books"."user_id" in (?, ?) group by "rel_books"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "rel_books"."id" and "latest_login"."user_id" = "rel_books"."user_id"`, statement)
}

func TestOneOfManyJoin_MorphOne_SQL(t *testing.T) {
	desc := &relationDescriptor{
		Name:            "CurrentLogo",
		Kind:            relKindMorphOne,
		RelatedTable:    "rel_logos",
		RelatedModel:    &relLogo{},
		MorphTypeColumn: "logoable_type",
		MorphIDColumn:   "logoable_id",
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_logos",
			ForeignColumn: "logoable_id",
		}},
	}

	sql := renderOfManySQL(t, desc, "id", "MIN")

	// The morph type column is carried through the select list, the GROUP BY and the JOIN so a morph
	// relation scopes the aggregate per owner type, not only per owner id; the oracle pins the join
	// order (aggregate column first, then the morph type, then the id).
	assert.Equal(t, `select "rel_logos".* from "rel_logos" inner join (select MIN("rel_logos"."id") as "id_aggregate", "rel_logos"."logoable_id", "rel_logos"."logoable_type" from "rel_logos" group by "rel_logos"."logoable_id", "rel_logos"."logoable_type") as "current_logo" on "current_logo"."id_aggregate" = "rel_logos"."id" and "current_logo"."logoable_type" = "rel_logos"."logoable_type" and "current_logo"."logoable_id" = "rel_logos"."logoable_id"`, sql)
}

func TestOneOfManyJoin_UnsupportedKind_NoOp(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		Name:         "Books",
		Kind:         relKindHasMany,
		RelatedTable: "rel_books",
		RelatedModel: &relBook{},
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_books",
			ForeignColumn: "user_id",
		}},
	}
	child := newRelQueryWith(t, &relBook{})
	child = child.OfMany("id", "MAX").(*OrmBuilder)

	join, args, err := q.oneOfManyJoin(child, desc, child.conditions.oneOfMany, nil, false)
	assert.NoError(t, err)
	assert.Nil(t, args)
	assert.Empty(t, join)
}

func TestOneOfManyJoin_NilCfg_NoOp(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		Name:         "LatestLogin",
		Kind:         relKindHasOne,
		RelatedTable: "rel_books",
		RelatedModel: &relBook{},
		References: []referenceKey{{
			PrimaryTable:  "rel_users",
			PrimaryColumn: "id",
			ForeignTable:  "rel_books",
			ForeignColumn: "user_id",
		}},
	}

	join, args, err := q.oneOfManyJoin(newRelQueryWith(t, &relBook{}), desc, nil, nil, false)
	assert.NoError(t, err)
	assert.Nil(t, args)
	assert.Empty(t, join)
}

func TestQuery_OfManyShortcuts(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	latest := q.LatestOfMany().(*OrmBuilder)
	assert.NotNil(t, latest.conditions.oneOfMany)
	assert.Equal(t, []oneOfManyColumn{{column: "id", aggregate: "MAX"}}, latest.conditions.oneOfMany.columns)
	assert.Equal(t, "MAX", latest.conditions.oneOfMany.keyAggregate)

	oldest := q.OldestOfMany("created_at").(*OrmBuilder)
	assert.NotNil(t, oldest.conditions.oneOfMany)
	assert.Equal(t, []oneOfManyColumn{{column: "created_at", aggregate: "MIN"}}, oldest.conditions.oneOfMany.columns)
	assert.Equal(t, "MAX", oldest.conditions.oneOfMany.keyAggregate)

	// Laravel's ofMany accepts only MIN and MAX; AVG is rejected (oracle: ofMany('score', 'AVG')
	// throws InvalidArgumentException with the message pinned in TestOfMany_InvalidAggregate_Error).
	// The Go ORM carries the error on the query and leaves the one-of-many configuration uninstalled.
	custom := q.OfMany("score", "AVG").(*OrmBuilder)
	assert.True(t, errors.Is(custom.conditions.err, errors.OrmRelationInvalidOfManyAggregate))
	assert.Nil(t, custom.conditions.oneOfMany)

	two := q.LatestOfMany("published_at", "id").(*OrmBuilder)
	assert.Equal(t, []oneOfManyColumn{{column: "published_at", aggregate: "MAX"}, {column: "id", aggregate: "MAX"}}, two.conditions.oneOfMany.columns)

	three := q.OfManyColumns(
		contractsorm.OfManyColumn{Column: "updated_at", Aggregate: "max"},
		contractsorm.OfManyColumn{Column: "created_at", Aggregate: "max"},
	).(*OrmBuilder)
	assert.Equal(t, []oneOfManyColumn{{column: "updated_at", aggregate: "max"}, {column: "created_at", aggregate: "max"}}, three.conditions.oneOfMany.columns)
	assert.Equal(t, "MAX", three.conditions.oneOfMany.keyAggregate)
}

// TestOfMany_InvalidAggregate_Error pins Laravel's ofMany aggregate validation: only MIN and MAX
// are accepted, case-insensitively, and the first offending aggregate is reported with Laravel's
// message "Invalid aggregate [<aggregate>] used within ofMany relation. Available aggregates:
// MIN, MAX" (oracle: ofMany('id', 'count') and ofMany(['id' => 'MAX', 'published_at' => 'avg'])
// both throw InvalidArgumentException with that text; 'max', 'Max' and 'MIN' are accepted). The Go
// ORM records the error on the query — it surfaces when the relation's statement is compiled — and
// leaves the one-of-many configuration uninstalled, so no aggregate join can be built from a
// rejected aggregate.
func TestOfMany_InvalidAggregate_Error(t *testing.T) {
	invalid := newRelQueryWith(t, &relUser{}).OfMany("id", "count").(*OrmBuilder)
	assert.True(t, errors.Is(invalid.conditions.err, errors.OrmRelationInvalidOfManyAggregate))
	assert.ErrorContains(t, invalid.conditions.err, "Invalid aggregate [count] used within ofMany relation. Available aggregates: MIN, MAX")
	assert.Nil(t, invalid.conditions.oneOfMany)

	// OfManyColumns validates every pair in declared order, so the first offending aggregate is
	// the one the message names.
	multi := newRelQueryWith(t, &relUser{}).OfManyColumns(
		contractsorm.OfManyColumn{Column: "id", Aggregate: "MAX"},
		contractsorm.OfManyColumn{Column: "published_at", Aggregate: "avg"},
	).(*OrmBuilder)
	assert.True(t, errors.Is(multi.conditions.err, errors.OrmRelationInvalidOfManyAggregate))
	assert.ErrorContains(t, multi.conditions.err, "Invalid aggregate [avg] used within ofMany relation. Available aggregates: MIN, MAX")
	assert.Nil(t, multi.conditions.oneOfMany)

	// min/max pass in any case, the strtolower check Laravel's ofMany makes.
	for _, aggregate := range []string{"max", "Max", "MIN", "min"} {
		valid := newRelQueryWith(t, &relUser{}).OfMany("created_at", aggregate).(*OrmBuilder)
		assert.NoError(t, valid.conditions.err, aggregate)
		assert.NotNil(t, valid.conditions.oneOfMany, aggregate)
	}
}

// --- relation read path ----------------------------------------------------------------------

// oomUser mirrors the ported corpus fixtures: a LatestLogin HasOne whose OnQuery installs OfMany, a
// FirstLogin shortcut with MIN, and a FooState whose of-many chain adds a where — the Go form of
// Laravel's ofMany closure, which restricts the aggregate subquery before the aggregate is taken.
type oomUser struct {
	ID          uint
	Logins      []*oomLogin `gorm:"-"`
	LatestLogin *oomLogin   `gorm:"-"`
	FirstLogin  *oomLogin   `gorm:"-"`
	FooState    *oomState   `gorm:"-"`
}

func (oomUser) TableName() string { return "rel_users" }

func (oomUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Logins": contractsorm.HasMany{Related: &oomLogin{}, ForeignKey: "user_id"},
		"LatestLogin": contractsorm.HasOne{
			Related: &oomLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		"FirstLogin": contractsorm.HasOne{
			Related: &oomLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("id", "MIN") },
		},
		"FooState": contractsorm.HasOne{
			Related: &oomState{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "").Where("type", "foo") },
		},
	}
}

type oomLogin struct {
	ID     uint
	UserID uint
}

func (oomLogin) TableName() string { return "rel_logins" }

type oomState struct {
	ID     uint
	Type   string
	UserID uint
}

func (oomState) TableName() string { return "rel_states" }

// TestRelated_OneOfMany_ReadPathSQL pins the read path's statement against the oracle
// (.claude/laravel-oracle/oracle.php, sqlite): for the same shape
// $user->latest_login()->getQuery()->toSql() prints exactly this string. The Go form of
// $user->latest_login() is Query.Related; ToSql renders the statement without executing it.
func TestRelated_OneOfMany_ReadPathSQL(t *testing.T) {
	q := newRelQueryWith(t, &oomUser{})
	rel := q.Related(&oomUser{ID: 7}, "LatestLogin")

	sql := newRelationSQL(t, rel, &[]oomLogin{})
	assert.Equal(t, `select "rel_logins".* from "rel_logins" inner join (select MAX("rel_logins"."id") as "id_aggregate", "rel_logins"."user_id" from "rel_logins" where "rel_logins"."user_id" = ? and "rel_logins"."user_id" is not null group by "rel_logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "rel_logins"."id" and "latest_login"."user_id" = "rel_logins"."user_id" where "rel_logins"."user_id" = ? and "rel_logins"."user_id" is not null`, sql)
}

// TestRelated_OneOfMany_ShortcutReadPathSQL pins the ofMany('id', 'MIN') form: the oracle prints the
// same statement with min in place of MAX and the relation's own alias (first_login).
func TestRelated_OneOfMany_ShortcutReadPathSQL(t *testing.T) {
	q := newRelQueryWith(t, &oomUser{})
	rel := q.Related(&oomUser{ID: 7}, "FirstLogin")

	sql := newRelationSQL(t, rel, &[]oomLogin{})
	assert.Equal(t, `select "rel_logins".* from "rel_logins" inner join (select MIN("rel_logins"."id") as "id_aggregate", "rel_logins"."user_id" from "rel_logins" where "rel_logins"."user_id" = ? and "rel_logins"."user_id" is not null group by "rel_logins"."user_id") as "first_login" on "first_login"."id_aggregate" = "rel_logins"."id" and "first_login"."user_id" = "rel_logins"."user_id" where "rel_logins"."user_id" = ? and "rel_logins"."user_id" is not null`, sql)
}

// TestRelated_OneOfMany_ClosureConstraintInSubQuery pins the of-many closure case: the where the
// relation's own chain adds after OfMany is the Go form of Laravel's ofMany closure and belongs
// inside the aggregate subquery, before the relation's foreign-key constraints — the oracle prints
// exactly this statement.
func TestRelated_OneOfMany_ClosureConstraintInSubQuery(t *testing.T) {
	q := newRelQueryWith(t, &oomUser{})
	rel := q.Related(&oomUser{ID: 7}, "FooState")

	sql := newRelationSQL(t, rel, &[]oomState{})
	assert.Equal(t, `select "rel_states".* from "rel_states" inner join (select MAX("rel_states"."id") as "id_aggregate", "rel_states"."user_id" from "rel_states" where "type" = ? and "rel_states"."user_id" = ? and "rel_states"."user_id" is not null group by "rel_states"."user_id") as "foo_state" on "foo_state"."id_aggregate" = "rel_states"."id" and "foo_state"."user_id" = "rel_states"."user_id" where "rel_states"."user_id" = ? and "rel_states"."user_id" is not null`, sql)
}

// TestRelated_OneOfMany_AliasOfManySuffix pins Laravel's getDefaultOneOfManyJoinAlias rule: when
// the relation's snake-case name equals the related table's name, the alias gains an "_of_many"
// suffix (oracle: $model->logins()->getQuery()->toSql() aliases the subquery "logins_of_many").
type loginsUser struct {
	ID uint
}

func (loginsUser) TableName() string { return "suffix_users" }

func (loginsUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Logins": contractsorm.HasOne{
			Related: &loginsLogin{}, ForeignKey: "user_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
	}
}

type loginsLogin struct {
	ID     uint
	UserID uint
}

func (loginsLogin) TableName() string { return "logins" }

func TestRelated_OneOfMany_AliasOfManySuffix(t *testing.T) {
	q := newRelQueryWith(t, &loginsUser{})
	rel := q.Related(&loginsUser{ID: 1}, "Logins")

	sql := newRelationSQL(t, rel, &[]loginsLogin{})
	assert.Equal(t, `select "logins".* from "logins" inner join (select MAX("logins"."id") as "id_aggregate", "logins"."user_id" from "logins" where "logins"."user_id" = ? and "logins"."user_id" is not null group by "logins"."user_id") as "logins_of_many" on "logins_of_many"."id_aggregate" = "logins"."id" and "logins_of_many"."user_id" = "logins"."user_id" where "logins"."user_id" = ? and "logins"."user_id" is not null`, sql)
}

// TestWhereHas_OneOfMany_JoinInsideExistenceSubquery pins the existence path. The oracle renders
// the same join inside `exists (select * from "rel_logins" ... where "rel_users"."id" =
// "rel_logins"."user_id")`; the existence subquery selects `*`, so the string is the oracle's.
func TestWhereHas_OneOfMany_JoinInsideExistenceSubquery(t *testing.T) {
	query := newRelQueryWith(t, &oomUser{}).WhereHas("LatestLogin").(*OrmBuilder)

	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "rel_users" where exists (select * from "rel_logins" inner join (select MAX("rel_logins"."id") as "id_aggregate", "rel_logins"."user_id" from "rel_logins" group by "rel_logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "rel_logins"."id" and "latest_login"."user_id" = "rel_logins"."user_id" where "rel_users"."id" = "rel_logins"."user_id")`, statement)
}

// TestWithCount_OneOfMany_CountsJoinedRows checks the aggregate reads carry the same join: the
// oracle's count subquery is `(select count(*) from "rel_logins" inner join (...) as
// "latest_login" on ... where "rel_users"."id" = "rel_logins"."user_id")`, so per-parent counts
// reduce to the one-of-many row.
func TestWithCount_OneOfMany_CountsJoinedRows(t *testing.T) {
	query := newRelQueryWith(t, &oomUser{}).WithCount("LatestLogin").(*OrmBuilder)

	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	const oracleJoin = `inner join (select MAX("rel_logins"."id") as "id_aggregate", "rel_logins"."user_id" from "rel_logins" group by "rel_logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "rel_logins"."id" and "latest_login"."user_id" = "rel_logins"."user_id"`
	assert.Contains(t, statement, "count(*) from \"rel_logins\" "+oracleJoin)
	assert.Contains(t, statement, `where "rel_users"."id" = "rel_logins"."user_id"`)
}

// TestWithExists_OneOfMany_JoinInsideExists checks the exists-select shape carries the join too.
func TestWithExists_OneOfMany_JoinInsideExists(t *testing.T) {
	query := newRelQueryWith(t, &oomUser{}).WithExists("LatestLogin").(*OrmBuilder)

	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	const oracleJoin = `inner join (select MAX("rel_logins"."id") as "id_aggregate", "rel_logins"."user_id" from "rel_logins" group by "rel_logins"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "rel_logins"."id" and "latest_login"."user_id" = "rel_logins"."user_id"`
	assert.Contains(t, statement, oracleJoin)
	assert.Contains(t, statement, `where "rel_users"."id" = "rel_logins"."user_id"`)
}

// --- multiple aggregates ---------------------------------------------------------------------

// The tests below pin the nested aggregate joins ofMany chains Laravel compiles when a relation
// declares more than one column. The fixtures use the ported corpus's table names, so every
// expected string is the oracle's for the same tables (illuminate/database v12.55.1, sqlite
// grammar, measured with .claude/laravel-oracle/oracle.php).

// oomPrice / oomCorpusState back the multi-aggregate tests.
type oomPrice struct {
	ID          uint
	PublishedAt string
	UserID      uint
}

func (oomPrice) TableName() string { return "prices" }

type oomCorpusState struct {
	ID        uint
	CreatedAt string
	UpdatedAt string
	UserID    uint
}

func (oomCorpusState) TableName() string { return "states" }

func oomPriceDescriptor(name string) *relationDescriptor {
	return &relationDescriptor{
		Name:         name,
		Kind:         relKindHasOne,
		RelatedTable: "prices",
		RelatedModel: &oomPrice{},
		References: []referenceKey{{
			PrimaryTable:  "users",
			PrimaryColumn: "id",
			ForeignTable:  "prices",
			ForeignColumn: "user_id",
		}},
	}
}

func oomStateDescriptor(name string) *relationDescriptor {
	return &relationDescriptor{
		Name:         name,
		Kind:         relKindHasOne,
		RelatedTable: "states",
		RelatedModel: &oomCorpusState{},
		References: []referenceKey{{
			PrimaryTable:  "users",
			PrimaryColumn: "id",
			ForeignTable:  "states",
			ForeignColumn: "user_id",
		}},
	}
}

// renderOneOfManyColumnsSQL runs the same steps renderOfManySQL takes, with the configuration
// supplied by the caller — OfMany / OfManyColumns / LatestOfMany / OldestOfMany plus any follow-on
// constraints — so each multi-aggregate shape can be pinned on its own.
func renderOneOfManyColumnsSQL(t *testing.T, desc *relationDescriptor, configure func(q contractsorm.Query) contractsorm.Query) string {
	t.Helper()

	q := newRelQueryWith(t, &relUser{})
	child := newRelQueryWith(t, desc.RelatedModel)
	child = configure(child).(*OrmBuilder)

	join, args, err := q.oneOfManyJoin(child, desc, child.conditions.oneOfMany, nil, false)
	assert.NoError(t, err)

	child.query.JoinRaw(join, args, "inner")
	if len(child.query.Selects()) == 0 {
		child.query.SelectRaw(q.ident(desc.RelatedTable, "*"), nil)
	}

	statement, _, err := child.compileSelect()
	assert.NoError(t, err)

	return statement
}

// TestOneOfManyJoin_HasOne_MultipleAggregates_SQL pins the nested aggregate joins Laravel compiles
// for ofMany(['published_at' => 'max', 'id' => 'max'], $closure) — the price() shape. Oracle,
// sqlite:
//
//	select "prices".* from "prices" inner join (select max("prices"."id") as "id_aggregate",
//	min("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices"
//	inner join (select max("prices"."published_at") as "published_at_aggregate", "prices"."user_id"
//	from "prices" where "published_at" < ? group by "prices"."user_id") as "price" on
//	"price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" =
//	"prices"."user_id" where "published_at" < ? group by "prices"."user_id") as "price" on
//	"price"."id_aggregate" = "prices"."id" and "price"."published_at_aggregate" =
//	"prices"."published_at" and "price"."user_id" = "prices"."user_id"
func TestOneOfManyJoin_HasOne_MultipleAggregates_SQL(t *testing.T) {
	desc := oomPriceDescriptor("Price")

	sql := renderOneOfManyColumnsSQL(t, desc, func(q contractsorm.Query) contractsorm.Query {
		return q.OfMany("published_at", "max").Where("published_at", "<", "2026-10-31 00:00:00")
	})

	assert.Equal(t, `select "prices".* from "prices" inner join (select max("prices"."id") as "id_aggregate", min("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" inner join (select max("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" where "published_at" < ? group by "prices"."user_id") as "price" on "price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" = "prices"."user_id" where "published_at" < ? group by "prices"."user_id") as "price" on "price"."id_aggregate" = "prices"."id" and "price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" = "prices"."user_id"`, sql)
}

// TestOneOfManyJoin_HasOne_KeyAddedWhenMissing_SQL pins Laravel's key rule: ofMany(['published_at'
// => 'MAX']) omits the key, so it is appended with MAX (uppercase, hardcoded) and the nested shape
// the oracle compiles for price_without_key_in_aggregates is produced.
func TestOneOfManyJoin_HasOne_KeyAddedWhenMissing_SQL(t *testing.T) {
	desc := oomPriceDescriptor("PriceWithoutKeyInAggregates")

	sql := renderOneOfManyColumnsSQL(t, desc, func(q contractsorm.Query) contractsorm.Query {
		return q.OfMany("published_at", "MAX")
	})

	assert.Equal(t, `select "prices".* from "prices" inner join (select MAX("prices"."id") as "id_aggregate", min("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" inner join (select MAX("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" group by "prices"."user_id") as "price_without_key_in_aggregates" on "price_without_key_in_aggregates"."published_at_aggregate" = "prices"."published_at" and "price_without_key_in_aggregates"."user_id" = "prices"."user_id" group by "prices"."user_id") as "price_without_key_in_aggregates" on "price_without_key_in_aggregates"."id_aggregate" = "prices"."id" and "price_without_key_in_aggregates"."published_at_aggregate" = "prices"."published_at" and "price_without_key_in_aggregates"."user_id" = "prices"."user_id"`, sql)
}

// TestOneOfManyJoin_HasOne_ThreeLevels_SQL pins the extra key level the tie-break rule adds when
// two columns distinct from the key are declared: ofMany(['updated_at' => 'max', 'created_at' =>
// 'max']) appends id => MAX, and the oracle compiles three nested subqueries.
func TestOneOfManyJoin_HasOne_ThreeLevels_SQL(t *testing.T) {
	desc := oomStateDescriptor("LatestUpdatedLatestCreatedState")

	sql := renderOneOfManyColumnsSQL(t, desc, func(q contractsorm.Query) contractsorm.Query {
		return q.OfManyColumns(
			contractsorm.OfManyColumn{Column: "updated_at", Aggregate: "max"},
			contractsorm.OfManyColumn{Column: "created_at", Aggregate: "max"},
		)
	})

	assert.Equal(t, `select "states".* from "states" inner join (select MAX("states"."id") as "id_aggregate", min("states"."created_at") as "created_at_aggregate", min("states"."updated_at") as "updated_at_aggregate", "states"."user_id" from "states" inner join (select max("states"."created_at") as "created_at_aggregate", min("states"."updated_at") as "updated_at_aggregate", "states"."user_id" from "states" inner join (select max("states"."updated_at") as "updated_at_aggregate", "states"."user_id" from "states" group by "states"."user_id") as "latest_updated_latest_created_state" on "latest_updated_latest_created_state"."updated_at_aggregate" = "states"."updated_at" and "latest_updated_latest_created_state"."user_id" = "states"."user_id" group by "states"."user_id") as "latest_updated_latest_created_state" on "latest_updated_latest_created_state"."created_at_aggregate" = "states"."created_at" and "latest_updated_latest_created_state"."updated_at_aggregate" = "states"."updated_at" and "latest_updated_latest_created_state"."user_id" = "states"."user_id" group by "states"."user_id") as "latest_updated_latest_created_state" on "latest_updated_latest_created_state"."id_aggregate" = "states"."id" and "latest_updated_latest_created_state"."created_at_aggregate" = "states"."created_at" and "latest_updated_latest_created_state"."updated_at_aggregate" = "states"."updated_at" and "latest_updated_latest_created_state"."user_id" = "states"."user_id"`, sql)
}

// TestOneOfManyJoin_HasOne_OldestOfManyMultiColumn_SQL pins oldestOfMany(['updated_at',
// 'created_at']): every column is aggregated MIN and the key is appended as MAX, exactly as
// ofMany appends it.
func TestOneOfManyJoin_HasOne_OldestOfManyMultiColumn_SQL(t *testing.T) {
	desc := oomStateDescriptor("OldestTwo")

	sql := renderOneOfManyColumnsSQL(t, desc, func(q contractsorm.Query) contractsorm.Query {
		return q.OldestOfMany("updated_at", "created_at")
	})

	assert.Equal(t, `select "states".* from "states" inner join (select MAX("states"."id") as "id_aggregate", min("states"."created_at") as "created_at_aggregate", min("states"."updated_at") as "updated_at_aggregate", "states"."user_id" from "states" inner join (select MIN("states"."created_at") as "created_at_aggregate", min("states"."updated_at") as "updated_at_aggregate", "states"."user_id" from "states" inner join (select MIN("states"."updated_at") as "updated_at_aggregate", "states"."user_id" from "states" group by "states"."user_id") as "oldest_two" on "oldest_two"."updated_at_aggregate" = "states"."updated_at" and "oldest_two"."user_id" = "states"."user_id" group by "states"."user_id") as "oldest_two" on "oldest_two"."created_at_aggregate" = "states"."created_at" and "oldest_two"."updated_at_aggregate" = "states"."updated_at" and "oldest_two"."user_id" = "states"."user_id" group by "states"."user_id") as "oldest_two" on "oldest_two"."id_aggregate" = "states"."id" and "oldest_two"."created_at_aggregate" = "states"."created_at" and "oldest_two"."updated_at_aggregate" = "states"."updated_at" and "oldest_two"."user_id" = "states"."user_id"`, sql)
}

// TestEagerOfManyJoin_HasOne_MultipleAggregates_InListInsideSubQuery pins the eager shape of the
// two-column declaration: the parent-key in-list lives INSIDE the innermost aggregate subquery
// (inlined for integer keys) and the outer query carries no constraint. Oracle, sqlite: the same
// statement for the eager read of a latestOfMany-style two-column relation with keys 1, 2.
func TestEagerOfManyJoin_HasOne_MultipleAggregates_InListInsideSubQuery(t *testing.T) {
	desc := oomPriceDescriptor("LatestTwo")
	desc.OnQuery = func(q contractsorm.Query) contractsorm.Query {
		return q.OfManyColumns(
			contractsorm.OfManyColumn{Column: "published_at", Aggregate: "MAX"},
			contractsorm.OfManyColumn{Column: "id", Aggregate: "MAX"},
		)
	}

	statement, bindings := renderEagerOfManySQL(t, desc, []any{uint(1), uint(2)})

	assert.Empty(t, bindings)
	assert.Equal(t, `select "prices".* from "prices" inner join (select MAX("prices"."id") as "id_aggregate", min("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" inner join (select MAX("prices"."published_at") as "published_at_aggregate", "prices"."user_id" from "prices" where "prices"."user_id" in (1, 2) group by "prices"."user_id") as "latest_two" on "latest_two"."published_at_aggregate" = "prices"."published_at" and "latest_two"."user_id" = "prices"."user_id" group by "prices"."user_id") as "latest_two" on "latest_two"."id_aggregate" = "prices"."id" and "latest_two"."published_at_aggregate" = "prices"."published_at" and "latest_two"."user_id" = "prices"."user_id"`, statement)
}

// --- through one-of-many ---------------------------------------------------------------------

// oomThroughUser / oomThroughPrice back the through one-of-many tests with the ported corpus's
// table names, so the pinned statements are the oracle's for the same tables.
type oomThroughUser struct {
	ID uint
}

func (oomThroughUser) TableName() string { return "users" }

func (oomThroughUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"LatestLogin": contractsorm.HasOneThrough{
			Related: &oomThroughLogin{}, Through: &oomThroughIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
		},
		"Price": contractsorm.HasOneThrough{
			Related: &oomThroughPrice{}, Through: &oomThroughIntermediate{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.OfMany("published_at", "max").Where("published_at", "<", "2026-10-31 00:00:00")
			},
		},
	}
}

type oomThroughIntermediate struct {
	ID     uint
	UserID uint
}

func (oomThroughIntermediate) TableName() string { return "intermediates" }

type oomThroughLogin struct {
	ID             uint
	IntermediateID uint
}

func (oomThroughLogin) TableName() string { return "logins" }

type oomThroughPrice struct {
	ID             uint
	PublishedAt    string
	IntermediateID uint
}

func (oomThroughPrice) TableName() string { return "prices" }

// TestRelated_ThroughOfMany_ReadPathSQL pins the through relation's read path with the aggregate
// join installed — the statement Laravel's ofMany renders for $user->latest_login() (oracle,
// sqlite).
func TestRelated_ThroughOfMany_ReadPathSQL(t *testing.T) {
	q := newRelQueryWith(t, &oomThroughUser{})
	rel := q.Related(&oomThroughUser{ID: 1}, "LatestLogin")

	sql := newRelationSQL(t, rel, &[]oomThroughLogin{})
	assert.Equal(t, `select "logins".* from "logins" inner join "intermediates" on "intermediates"."id" = "logins"."intermediate_id" inner join (select MAX("logins"."id") as "id_aggregate", "intermediates"."user_id" from "logins" inner join "intermediates" on "intermediates"."id" = "logins"."intermediate_id" where "intermediates"."user_id" = ? group by "intermediates"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "logins"."id" and "latest_login"."user_id" = "intermediates"."user_id" where "intermediates"."user_id" = ?`, sql)
}

// TestRelated_ThroughOfMany_MultiAggregate_ReadPathSQL pins the nested aggregate joins for the
// through price() shape with its scoping closure (oracle, sqlite).
func TestRelated_ThroughOfMany_MultiAggregate_ReadPathSQL(t *testing.T) {
	q := newRelQueryWith(t, &oomThroughUser{})
	rel := q.Related(&oomThroughUser{ID: 1}, "Price")

	sql := newRelationSQL(t, rel, &[]oomThroughPrice{})
	assert.Equal(t, `select "prices".* from "prices" inner join "intermediates" on "intermediates"."id" = "prices"."intermediate_id" inner join (select max("prices"."id") as "id_aggregate", min("prices"."published_at") as "published_at_aggregate", "intermediates"."user_id" from "prices" inner join "intermediates" on "intermediates"."id" = "prices"."intermediate_id" inner join (select max("prices"."published_at") as "published_at_aggregate", "intermediates"."user_id" from "prices" inner join "intermediates" on "intermediates"."id" = "prices"."intermediate_id" where "published_at" < ? and "intermediates"."user_id" = ? group by "intermediates"."user_id") as "price" on "price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" = "intermediates"."user_id" where "published_at" < ? group by "intermediates"."user_id") as "price" on "price"."id_aggregate" = "prices"."id" and "price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" = "intermediates"."user_id" where "intermediates"."user_id" = ?`, sql)
}

// renderEagerThroughOfManySQL runs the steps loadThrough's one-of-many branch takes: the related
// child carries the through join and the parent-key in-list, the relation's own scope runs, and
// the aggregate join is queued with the "inner" keyword.
func renderEagerThroughOfManySQL(t *testing.T, desc *relationDescriptor, keys []any) (string, []any) {
	t.Helper()

	q := newRelQueryWith(t, &relUser{})
	child := q.relatedChild(desc.RelatedTable, desc.ThroughTable+"."+desc.FirstKey, keys)
	child.query.JoinRaw(q.throughJoinFragment(desc), nil, "inner")
	if desc.OnQuery != nil {
		if scoped, ok := desc.OnQuery(child).(*OrmBuilder); ok {
			child = scoped
		}
	}

	join, args, err := q.oneOfManyJoin(child, desc, child.conditions.oneOfMany, nil, false)
	assert.NoError(t, err)

	child.query.JoinRaw(join, args, "inner")
	if len(child.query.Selects()) == 0 {
		child.query.SelectRaw(q.ident(desc.RelatedTable, "*"), nil)
	}

	statement, bindings, err := child.compileSelect()
	assert.NoError(t, err)

	return statement, bindings
}

// TestEagerThroughOfMany_SQL pins the through eager read's statement: the parent-key in-list moves
// into the innermost aggregate subquery and the outer query carries no constraint (oracle, sqlite:
// the same statement for the eager read with keys 1, 2).
func TestEagerThroughOfMany_SQL(t *testing.T) {
	desc := &relationDescriptor{
		Name:           "LatestLogin",
		Kind:           relKindHasOneThrough,
		RelatedTable:   "logins",
		ThroughTable:   "intermediates",
		RelatedModel:   &oomThroughLogin{},
		FirstKey:       "user_id",
		SecondKey:      "intermediate_id",
		LocalKey:       "id",
		SecondLocalKey: "id",
		OnQuery:        func(q contractsorm.Query) contractsorm.Query { return q.OfMany("", "") },
	}

	statement, bindings := renderEagerThroughOfManySQL(t, desc, []any{uint(1), uint(2)})

	assert.Empty(t, bindings)
	assert.Equal(t, `select "logins".* from "logins" inner join "intermediates" on "intermediates"."id" = "logins"."intermediate_id" inner join (select MAX("logins"."id") as "id_aggregate", "intermediates"."user_id" from "logins" inner join "intermediates" on "intermediates"."id" = "logins"."intermediate_id" where "intermediates"."user_id" in (1, 2) group by "intermediates"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "logins"."id" and "latest_login"."user_id" = "intermediates"."user_id"`, statement)

	// The corpus’s eager read loads a single user (keys [1]); the oracle inlines the in-list as
	// `in (1)` with no bindings and leaves the outer query unconstrained.
	statement, bindings = renderEagerThroughOfManySQL(t, desc, []any{uint(1)})

	assert.Empty(t, bindings)
	assert.Equal(t, `select "logins".* from "logins" inner join "intermediates" on "intermediates"."id" = "logins"."intermediate_id" inner join (select MAX("logins"."id") as "id_aggregate", "intermediates"."user_id" from "logins" inner join "intermediates" on "intermediates"."id" = "logins"."intermediate_id" where "intermediates"."user_id" in (1) group by "intermediates"."user_id") as "latest_login" on "latest_login"."id_aggregate" = "logins"."id" and "latest_login"."user_id" = "intermediates"."user_id"`, statement)
}

// TestEagerThroughOfMany_MultiAggregate_SQL pins the eager read for the through price() shape with
// its scoping closure: one nested aggregate join per declared column over the through join, the
// closure’s where inside every level, and the parent in-list inside the innermost subquery (oracle,
// sqlite, keys [1]).
func TestEagerThroughOfMany_MultiAggregate_SQL(t *testing.T) {
	desc := &relationDescriptor{
		Name:           "Price",
		Kind:           relKindHasOneThrough,
		RelatedTable:   "prices",
		ThroughTable:   "intermediates",
		RelatedModel:   &oomThroughPrice{},
		FirstKey:       "user_id",
		SecondKey:      "intermediate_id",
		LocalKey:       "id",
		SecondLocalKey: "id",
		OnQuery: func(q contractsorm.Query) contractsorm.Query {
			return q.OfMany("published_at", "max").Where("published_at", "<", "2026-10-31 00:00:00")
		},
	}

	statement, bindings := renderEagerThroughOfManySQL(t, desc, []any{uint(1)})

	assert.Len(t, bindings, 2)
	for _, binding := range bindings {
		assert.Equal(t, "2026-10-31 00:00:00", binding)
	}
	assert.Equal(t, `select "prices".* from "prices" inner join "intermediates" on "intermediates"."id" = "prices"."intermediate_id" inner join (select max("prices"."id") as "id_aggregate", min("prices"."published_at") as "published_at_aggregate", "intermediates"."user_id" from "prices" inner join "intermediates" on "intermediates"."id" = "prices"."intermediate_id" inner join (select max("prices"."published_at") as "published_at_aggregate", "intermediates"."user_id" from "prices" inner join "intermediates" on "intermediates"."id" = "prices"."intermediate_id" where "published_at" < ? and "intermediates"."user_id" in (1) group by "intermediates"."user_id") as "price" on "price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" = "intermediates"."user_id" where "published_at" < ? group by "intermediates"."user_id") as "price" on "price"."id_aggregate" = "prices"."id" and "price"."published_at_aggregate" = "prices"."published_at" and "price"."user_id" = "intermediates"."user_id"`, statement)
}
