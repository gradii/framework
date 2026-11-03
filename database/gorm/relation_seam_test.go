package gorm

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// The S2 seam widening (relation_seam.go): every exported twin is pinned against the internal
// member it forwards to, on the stub builder (no statement runs here — the statement shapes of the
// row primitives are pinned byte for byte against the legacy workers in
// relations/seam_sql_test.go on the recording transport).

// seamParent / seamChild declare a many-to-many carrying every W9 field.
type seamParent struct {
	ID        uint
	UpdatedAt time.Time    `gorm:"autoUpdateTime;column:updated_at"`
	Children  []*seamChild `gorm:"-"`
}

func (seamParent) TableName() string { return "seam_parents" }

func (seamParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Children": contractsorm.Many2Many{
			Related:         &seamChild{},
			Table:           "seam_parent_children",
			ForeignPivotKey: "parent_id",
			RelatedPivotKey: "child_id",
			PivotTimestamps: true,
			Touches:         true,
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("active", 1)
			},
		},
	}
}

type seamChild struct {
	ID uint
}

func (seamChild) TableName() string { return "seam_children" }

func TestSeam_RelationInfoOf_ProjectsTheDescriptorIncludingW9(t *testing.T) {
	q := newRelQueryWith(t, &seamParent{})

	info, err := q.RelationInfoOf(&seamParent{ID: 1}, "Children")
	require.NoError(t, err)
	desc, err := relations.ResolveRelation(&seamParent{ID: 1}, "Children")
	require.NoError(t, err)

	assert.Equal(t, relations.KindMany2Many, info.Kind)
	assert.Equal(t, "seam_parents", info.ParentTable)
	assert.Equal(t, "id", info.ParentPrimaryKeyColumn)
	assert.Equal(t, "seam_parent_children", info.PivotTable)
	assert.Equal(t, "parent_id", info.PivotParentRef.ForeignColumn)
	assert.Equal(t, "child_id", info.PivotRelatedRef.ForeignColumn)

	// W9: the fields the write workers read, equal to the descriptor's own.
	assert.Equal(t, "Children", info.Name)
	assert.Equal(t, desc.Name, info.Name)
	assert.True(t, info.Touches)
	assert.Equal(t, desc.Touches, info.Touches)
	assert.Equal(t, "created_at", info.PivotCreatedAtColumn)
	assert.Equal(t, desc.PivotCreatedAtColumn, info.PivotCreatedAtColumn)
	assert.Equal(t, "updated_at", info.PivotUpdatedAtColumn)
	assert.Equal(t, desc.PivotUpdatedAtColumn, info.PivotUpdatedAtColumn)
	assert.Equal(t, reflect.TypeOf(uint(0)), info.RelatedKeyType)
	assert.Equal(t, desc.RelatedKeyType, info.RelatedKeyType)
	require.NotNil(t, info.OnPivotQuery)
	assert.Equal(t, reflect.ValueOf(desc.OnPivotQuery).Pointer(), reflect.ValueOf(info.OnPivotQuery).Pointer())
}

func TestSeam_RelationInfoOf_HasManyCarriesNoPivotFacts(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	info, err := q.RelationInfoOf(&relUser{ID: 1}, "Books")
	require.NoError(t, err)

	assert.Equal(t, relations.KindHasMany, info.Kind)
	assert.Equal(t, "Books", info.Name)
	assert.False(t, info.Touches)
	assert.Equal(t, "", info.PivotCreatedAtColumn)
	assert.Equal(t, "", info.PivotUpdatedAtColumn)
	assert.Nil(t, info.RelatedKeyType)
	assert.Nil(t, info.OnPivotQuery)
}

func TestSeam_RelationInfoOf_UnknownRelationReportsResolveError(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	_, err := q.RelationInfoOf(&relUser{ID: 1}, "Nope")
	require.Error(t, err)
	_, legacy := relations.ResolveRelation(&relUser{ID: 1}, "Nope")
	assert.Equal(t, legacy.Error(), err.Error())
}

func TestSeam_SetColumn_WritesThroughTheSchema(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	child := &relBook{}

	ok, err := q.SetColumn(child, "user_id", uint(7))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, uint(7), child.UserID)

	ok, err = q.SetColumn(child, "no_such_column", 1)
	require.NoError(t, err)
	assert.False(t, ok, "an absent column reports false, no error — the workers keep their own handling")

	_, err = q.SetColumn(relBook{}, "user_id", uint(1))
	assert.True(t, errors.Is(err, errors.OrmQueryInvalidModel), "a value cannot be written: %v", err)

	_, err = q.SetColumn(nil, "user_id", uint(1))
	assert.True(t, errors.Is(err, errors.OrmQueryInvalidModel), "nil: %v", err)
}

func TestSeam_ColumnValue_IsReadParentColumn(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	parent := &relUser{ID: 9}

	value, err := q.ColumnValue(parent, "id")
	require.NoError(t, err)
	legacy, err := readParentColumn(q, parent, "id")
	require.NoError(t, err)
	assert.Equal(t, uint(9), value)
	assert.Equal(t, legacy, value)

	_, err = q.ColumnValue(parent, "missing")
	require.Error(t, err)
	_, legacyErr := readParentColumn(q, parent, "missing")
	assert.True(t, errors.Is(err, errors.OrmRelationUnsupported))
	assert.Equal(t, legacyErr.Error(), err.Error(), "error text byte-identical to readParentColumn")
}

func TestSeam_ColumnType_ReportsTheFieldType(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	fieldType, ok, err := q.ColumnType(&relBook{}, "user_id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, reflect.TypeOf(uint(0)), fieldType)

	fieldType, ok, err = q.ColumnType(&relBook{}, "missing")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, fieldType)

	_, _, err = q.ColumnType(nil, "id")
	assert.True(t, errors.Is(err, errors.OrmQueryInvalidModel))
}

func TestSeam_TableOf_IsTableNameFor(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	table, err := q.TableOf(&relUser{})
	require.NoError(t, err)
	assert.Equal(t, "rel_users", table)
	legacy, err := tableNameFor(&relUser{})
	require.NoError(t, err)
	assert.Equal(t, legacy, table)
}

func TestSeam_Ident_IsIdent(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	assert.Equal(t, q.ident("rel_user_roles", "user_id"), q.Ident("rel_user_roles", "user_id"))
	assert.Equal(t, `"rel_user_roles"."user_id"`, q.Ident("rel_user_roles", "user_id"))
	assert.Equal(t, q.ident("id"), q.Ident("id"))
}

func TestSeam_NewQueryAndWriteSession_SessionSemantics(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	q.conditions.withoutEvents = true

	fresh, ok := q.NewQuery().(*OrmBuilder)
	require.True(t, ok)
	assert.NotSame(t, q, fresh)
	assert.Nil(t, fresh.conditions.model, "fresh carries no state of its own")
	assert.False(t, fresh.conditions.withoutEvents, "fresh drops the quiet flag — the plain fresh() the associate / createOrFirst persists through")

	session, ok := q.WriteSession().(*OrmBuilder)
	require.True(t, ok)
	assert.NotSame(t, q, session)
	assert.Nil(t, session.conditions.model)
	assert.True(t, session.conditions.withoutEvents, "the write session keeps the caller's event suppression")

	legacy := q.relationWriteSession()
	assert.Equal(t, legacy.conditions.withoutEvents, session.conditions.withoutEvents)
}

func TestSeam_WithSavepoint_OutsideATransactionRunsTheScopeDirectly(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	require.Nil(t, q.tx)

	calls := 0
	require.NoError(t, q.WithSavepoint(func() error { calls++; return nil }))
	assert.Equal(t, 1, calls)

	boom := errors.New("boom")
	err := q.WithSavepoint(func() error { calls++; return boom })
	assert.Equal(t, 2, calls)
	assert.Equal(t, boom, err, "the scope's error passes through unchanged")
}

func TestSeam_InverseWriteBackName_IsInverseWriteBackName(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	declared := &relationDescriptor{Inverse: "Owner"}

	assert.Equal(t, "Owner", q.InverseWriteBackName("Owner"), "no state: the declared name")
	assert.Equal(t, q.inverseWriteBackName(declared), q.InverseWriteBackName("Owner"))

	q.inverseChaperone = &inverseChaperoneState{relation: "User"}
	assert.Equal(t, "User", q.InverseWriteBackName("Owner"), "live state wins")
	assert.Equal(t, q.inverseWriteBackName(declared), q.InverseWriteBackName("Owner"))

	q.WithoutInverseChaperone()
	assert.Equal(t, "", q.InverseWriteBackName("Owner"), "a cleared state means no write-back")
	assert.Equal(t, q.inverseWriteBackName(declared), q.InverseWriteBackName("Owner"))
}

func TestSeam_SetRelation_StoresThePointerOnTheRelationField(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	parent := &relUser{ID: 3}
	child := &relBook{}

	require.NoError(t, q.SetRelation(child, "Author", parent))
	assert.Same(t, parent, child.Author)

	err := q.SetRelation(child, "NoSuchField", parent)
	assert.True(t, errors.Is(err, errors.OrmEagerLoadCannotAssign), "%v", err)

	untouched := relBook{}
	assert.NoError(t, q.SetRelation(untouched, "Author", parent), "a non-pointer model is the guard's no-op")
	assert.Nil(t, untouched.Author)

	other := &relBook{}
	assert.NoError(t, q.SetRelation(other, "Author", nil), "a nil value is the guard's no-op")
	assert.Nil(t, other.Author)
	var nilParent *relUser
	assert.NoError(t, q.SetRelation(other, "Author", nilParent))
	assert.Nil(t, other.Author)
}

func TestSeam_IsUniqueConstraintViolation_IsTheClassifier(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	assert.False(t, q.IsUniqueConstraintViolation(nil))
	violation := errors.New("UNIQUE constraint failed: rel_books.id")
	assert.True(t, q.IsUniqueConstraintViolation(violation))
	assert.Equal(t, isUniqueConstraintViolation(violation), q.IsUniqueConstraintViolation(violation))
	other := errors.New("syntax error")
	assert.False(t, q.IsUniqueConstraintViolation(other))
}

func TestSeam_IsIgnoringTouch_ReadsTheScopePerCall(t *testing.T) {
	q := newRelQueryWith(t, &seamParent{})

	// Model::isIgnoringTouch is true for a class with no automatic updated-at column — relUser —
	// and for a touchable class only while a WithoutTouching scope lists it.
	assert.True(t, q.IsIgnoringTouch(&relUser{}), "no updated-at column: always ignoring")
	assert.False(t, q.IsIgnoringTouch(&seamParent{}))
	require.NoError(t, databaseorm.WithoutTouchingOn([]any{&seamParent{}}, func() error {
		assert.True(t, q.IsIgnoringTouch(&seamParent{}))
		assert.Equal(t, databaseorm.IsIgnoringTouch(&seamParent{}), q.IsIgnoringTouch(&seamParent{}))

		return nil
	}))
	assert.False(t, q.IsIgnoringTouch(&seamParent{}), "read per call: the scope has ended")
}

func TestSeam_MorphAliasOf_IsResolveMorphAlias(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)
	morphmap.Register(map[string]any{"seam_child": &seamChild{}})

	alias, ok, err := q.MorphAliasOf(&seamChild{})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "seam_child", alias)

	alias, ok, err = q.MorphAliasOf(&relRole{})
	require.NoError(t, err)
	assert.False(t, ok, "no table-name fallback here — that is the Associate caller's own step")
	assert.Equal(t, "", alias)

	legacyAlias, legacyOK, legacyErr := relations.ResolveMorphAlias(&seamChild{})
	assert.Equal(t, legacyAlias, "seam_child")
	assert.True(t, legacyOK)
	assert.NoError(t, legacyErr)
}

// The installed path of the descriptor seam guard (relations/descriptor.go descriptorSeamInstalled,
// followup-descriptorseam-guard): this package's init() ran UseDescriptorSeam before any test, so the
// resolver entry points reach their readers and resolve exactly as before — none of them reports
// OrmRelationDescriptorSeamMissing. The absent path is pinned from inside the relations package
// (relations/descriptor_seam_guard_test.go), where the seam variable can be zeroed.
func TestSeam_DescriptorSeamInstalledByInit(t *testing.T) {
	desc, err := relations.ResolveRelation(&seamParent{ID: 1}, "Children")
	require.NoError(t, err)
	require.NotErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)
	require.Equal(t, relations.KindMany2Many, desc.Kind)
	require.Equal(t, "seam_parent_children", desc.PivotTable)

	created, updated, err := relations.ResolvePivotTimestamps(&seamParent{}, &seamChild{}, "", true)
	require.NoError(t, err)
	require.Equal(t, "created_at", created)
	require.Equal(t, "updated_at", updated)

	_, err = relations.ResolveMorphValue(&seamChild{}, "seam_children")
	require.NoError(t, err)
	_, _, err = relations.ResolveMorphAlias(&seamChild{})
	require.NoError(t, err)
}
