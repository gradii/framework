package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/database/query/core"
)

// This file is the gate for the CONDITIONS SQL-MIRROR item. Conditions used to carry gorm-era
// mirrors of SQL state the database/query builder already represents: a staged where slice the
// ORM's internal conditions queued and the compiler drained, a table, and (kept, see
// Conditions) a raw statement. The members are now written straight onto the OrmBuilder's
// persistent builder and read back from its State.
//
// Two kinds of pin live here:
//
//   - the byte-identity pins record the exact statements the ORM compiled BEFORE the migration on
//     every path that wrote to a mirror — the pivot row primitives (the pivot chain's staged
//     predicates plus the pivot scope replay), Find's inline conditions and the FirstOr* fold — and
//     must keep passing after it; the recorded text was captured on the unmodified tree;
//   - the single-source pins assert the new property, that the condition or table is on the
//     builder State the moment it is written; they were red on the unmodified tree (the staged
//     slice and the Conditions table held the state until the statement compiled) and are green
//     now.

type mirrorUser struct {
	ID     uint          `gorm:"column:id;primaryKey"`
	Name   string        `gorm:"column:name"`
	Active int           `gorm:"column:active"`
	Roles  []*mirrorRole `gorm:"-"`
}

func (mirrorUser) TableName() string { return "mirror_users" }

func (mirrorUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{
			Related:         &mirrorRole{},
			Table:           "mirror_role_user",
			ForeignPivotKey: "user_id",
			RelatedPivotKey: "role_id",
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("active", 1)
			},
		},
	}
}

type mirrorRole struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (mirrorRole) TableName() string { return "mirror_roles" }

// newMirrorQuery builds a recording-transport query over mirrorUser, the shape newGetColumnsQuery
// gives: generic grammar, double-quoted identifiers, "?" bindings.
func newMirrorQuery(t *testing.T) (*OrmBuilder, *recordingSQLDriver) {
	t.Helper()

	recorder := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })

	conditions := Conditions{model: &mirrorUser{}}

	return newDialectQuery(context.Background(), nil, contractsdatabase.Config{}, sqlDB, nil, nil, nil, nil, &conditions, nil), recorder
}

// --- byte-identity pins (captured on the unmodified tree) -------------------------------------

// The pivot row primitives replay the pivot scope ahead of the predicates — the scope wrote its
// clause straight to the builder while the staged predicates joined it at compile time — and
// the migration keeps that order.
func TestConditionsMirror_PivotRowPrimitivesAreByteIdentical(t *testing.T) {
	query, recorder := newMirrorQuery(t)
	var seam relations.Builder = query
	info, err := seam.RelationInfoOf(&mirrorUser{ID: 7}, "Roles")
	require.NoError(t, err)

	wheres := []relations.RowWhere{
		{Query: seam.Ident(info.PivotTable, info.PivotParentRef.ForeignColumn) + " = ?", Args: []any{uint(7)}},
		{Query: info.PivotTable + "." + info.PivotRelatedRef.ForeignColumn, Args: []any{[]any{1, 2}}},
	}

	_, err = seam.DeleteRows(info.PivotTable, wheres, info.OnPivotQuery)
	require.NoError(t, err)
	assert.Equal(t, `delete from "mirror_role_user" where "mirror_role_user"."active" = ? and "mirror_role_user"."user_id" = ? and "mirror_role_user"."role_id" in (?, ?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), int64(7), int64(1), int64(2)}, recorder.last(t).args)

	_, err = seam.UpdateRows(info.PivotTable, wheres[:1], info.OnPivotQuery, map[string]any{"note": "x"})
	require.NoError(t, err)
	assert.Equal(t, `update "mirror_role_user" set "note" = ? where "mirror_role_user"."active" = ? and "mirror_role_user"."user_id" = ?`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{"x", int64(1), int64(7)}, recorder.last(t).args)

	_, err = seam.SelectRows(info.PivotTable, []string{"role_id"}, wheres, info.OnPivotQuery)
	require.NoError(t, err)
	assert.Equal(t, `select "role_id" from "mirror_role_user" where "mirror_role_user"."active" = ? and "mirror_role_user"."user_id" = ? and "mirror_role_user"."role_id" in (?, ?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), int64(7), int64(1), int64(2)}, recorder.last(t).args)
}

// Find's inline condition follows the where the caller chained before the call.
func TestConditionsMirror_FindConditionIsByteIdentical(t *testing.T) {
	query, recorder := newMirrorQuery(t)

	var found mirrorUser
	require.NoError(t, query.clone().Where("active", 1).Find(&found, "name", "x"))
	assert.Equal(t, `select * from "mirror_users" where "active" = ? and "name" = ?`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), "x"}, recorder.last(t).args)
}

// The FirstOr* fold follows the chained where in the statement, and a miss copies the fold's own
// equality onto the model and nothing else: the caller's chained where is not part of the lookup
// the model is built from.
func TestConditionsMirror_FirstOrNewIsByteIdenticalAndCopiesTheFoldAlone(t *testing.T) {
	query, recorder := newMirrorQuery(t)

	var fresh mirrorUser
	require.NoError(t, query.clone().Where("active", 1).FirstOrNew(&fresh, map[string]any{"name": "x"}))
	assert.Equal(t, `select * from "mirror_users" where "active" = ? and "name" = ? order by "mirror_users"."id" limit 1`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1), "x"}, recorder.last(t).args)

	assert.Equal(t, "x", fresh.Name, "the fold equality is copied onto the model")
	assert.Equal(t, 0, fresh.Active, "the chained where is not")
}

// --- single-source pins (red on the unmodified tree) -------------------------------------------

// addWhere lands on the builder State the moment it is called, as a Basic clause carrying the
// column, the operator and the value — nothing waits for the statement to compile.
func TestConditionsMirror_AddWhereLandsOnStateAtCallTime(t *testing.T) {
	query := &OrmBuilder{query: querybuilder.NewBuilder(nil)}

	query = query.addWhere(contractsdriver.Where{Query: "name", Args: []any{"x"}}).(*OrmBuilder)

	wheres := query.query.State().Wheres
	require.Len(t, wheres, 1)
	assert.Equal(t, core.WhereBasic, wheres[0].Type)
	assert.Equal(t, "name", wheres[0].Column)
	assert.Equal(t, "=", wheres[0].Operator)
	assert.Equal(t, "x", wheres[0].Value)
	assert.Equal(t, []any{"x"}, query.query.State().Bindings["where"])
}

// Find's inline conditions and the FirstOr* fold land on the builder State when they are written.
func TestConditionsMirror_FindAndFoldConditionsLandOnStateAtCallTime(t *testing.T) {
	query, _ := newMirrorQuery(t)

	found := query.clone().withFindConditions([]any{"name", "x"})
	require.Len(t, found.query.State().Wheres, 1)
	assert.Equal(t, "name", found.query.State().Wheres[0].Column)

	folded, err := query.clone().foldFindCondition(map[string]any{"name": "x", "active": 1})
	require.NoError(t, err)
	require.Len(t, folded.query.State().Wheres, 2, "one clause per map key, in sorted key order")
	assert.Equal(t, "active", folded.query.State().Wheres[0].Column)
	assert.Equal(t, "name", folded.query.State().Wheres[1].Column)
}

// Table() is the builder's from clause, and the write path reads the declared table back from it:
// a bare name as given, an Expression by its raw SQL.
func TestConditionsMirror_TableIsTheBuilderFromClause(t *testing.T) {
	query, _ := newMirrorQuery(t)

	named := query.clone().Table("plain_table").(*OrmBuilder)
	assert.Equal(t, "plain_table", named.query.State().From)
	assert.Equal(t, "plain_table", named.declaredTable())

	qualified := query.clone().Table(querybuilder.Raw("schema.view")).(*OrmBuilder)
	assert.Equal(t, "schema.view", named.clone().Table(querybuilder.Raw("schema.view")).(*OrmBuilder).declaredTable())
	assert.Equal(t, querybuilder.Raw("schema.view"), qualified.query.State().From)

	assert.Nil(t, query.clone().query.State().From, "a query that names no table carries no from clause until the model supplies it at compile time")
	assert.Equal(t, "", query.clone().declaredTable())
}

// The compile-time fallback still derives the table from the model when none was named, and keeps
// the named table when one was.
func TestConditionsMirror_ApplyTableKeepsANamedTable(t *testing.T) {
	query, recorder := newMirrorQuery(t)

	var rows []mirrorUser
	require.NoError(t, query.clone().Get(&rows))
	assert.Equal(t, `select * from "mirror_users"`, recorder.last(t).sql)

	require.NoError(t, query.clone().Table("other_users").Get(&rows))
	assert.Equal(t, `select * from "other_users"`, recorder.last(t).sql)
}
