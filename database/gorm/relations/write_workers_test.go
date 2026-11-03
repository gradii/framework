package relations_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// The S2 step-2 port (write_workers.go): the HasOneOrMany / BelongsTo / through-family write
// workers, now free functions over (Builder, RelationInfo), are pinned on the real
// *gorm.OrmBuilder of the recording transport — the in-memory effects the gorm unit tests
// asserted before the move, the statement the touch compiles through UpdateRows, and the
// single-row read RelationFirst / FindOrNew run.

// --- fixtures ----------------------------------------------------------------------------------

// wwParent declares a touching many-to-many: the shape TouchIfTouching bumps.
type wwParent struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
	Children  []*wwChild       `gorm:"-"`
}

func (wwParent) TableName() string { return "ww_parents" }

func (wwParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Children": contractsorm.Many2Many{
			Related:         &wwChild{},
			Table:           "ww_parent_children",
			ForeignPivotKey: "parent_id",
			RelatedPivotKey: "child_id",
			Touches:         true,
		},
	}
}

type wwChild struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (wwChild) TableName() string { return "ww_children" }

// wwPost belongs to wwAuthor through a plain integer foreign key — the associate / dissociate pair.
type wwPost struct {
	ID       uint      `gorm:"column:id;primaryKey"`
	AuthorID uint      `gorm:"column:author_id"`
	Author   *wwAuthor `gorm:"-"`
}

func (wwPost) TableName() string { return "ww_posts" }

func (wwPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &wwAuthor{}, ForeignKey: "author_id"},
	}
}

type wwAuthor struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (wwAuthor) TableName() string { return "ww_authors" }

// wwNoKey carries no primary key at all — PrimaryKey's ok=false branch.
type wwNoKey struct {
	Name string `gorm:"column:name"`
}

func (wwNoKey) TableName() string { return "ww_no_keys" }

func wwInfo(t *testing.T, builder relations.Builder, parent any, relation string) relations.RelationInfo {
	t.Helper()

	info, err := builder.RelationInfoOf(parent, relation)
	require.NoError(t, err)

	return info
}

// --- BelongsTo mutators --------------------------------------------------------------------------

func TestWriteWorkers_MutateAssociate_SetsForeignKeyWithoutAStatement(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	post := &wwPost{}

	require.NoError(t, relations.MutateAssociate(builder, wwInfo(t, builder, post, "Author"), post, &wwAuthor{ID: 42}, false))

	assert.Equal(t, uint(42), post.AuthorID)
	assert.Empty(t, recorder.take(), "the mutation half persists nothing")
}

func TestWriteWorkers_MutateDissociate_ZeroesTheForeignKey(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	post := &wwPost{AuthorID: 99}

	require.NoError(t, relations.MutateDissociate(builder, wwInfo(t, builder, post, "Author"), post, false))

	assert.Equal(t, uint(0), post.AuthorID)
	assert.Empty(t, recorder.take())
}

func TestWriteWorkers_MutateAssociate_ReportsTheMissingReferenceAndField(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	post := &wwPost{}
	info := wwInfo(t, builder, post, "Author")

	noRefs := info
	noRefs.References = nil
	err := relations.MutateAssociate(builder, noRefs, post, &wwAuthor{ID: 1}, false)
	require.True(t, errors.Is(err, errors.OrmRelationUnsupported), "%v", err)
	assert.Contains(t, err.Error(), "no references")

	noField := info
	noField.References = []relations.RefKey{{PrimaryColumn: "id", ForeignColumn: "missing_id"}}
	err = relations.MutateAssociate(builder, noField, post, &wwAuthor{ID: 1}, false)
	require.True(t, errors.Is(err, errors.OrmRelationUnsupported), "%v", err)
	assert.Contains(t, err.Error(), "no FK field missing_id")
	assert.Contains(t, err.Error(), "wwPost", "the model's Go type name, as parseSchema's Name reported it")
}

// --- HasOneOrMany: the foreign-key stamp and the attribute overlay -------------------------------

func TestWriteWorkers_SetRelationFKOnChild_StampsTheParentKey(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	parent := &sqlUser{ID: 7}
	child := &sqlBook{}

	require.NoError(t, relations.SetRelationFKOnChild(builder, wwInfo(t, builder, parent, "Books"), parent, child))

	assert.Equal(t, uint(7), child.UserID)
	assert.Empty(t, recorder.take(), "in memory only")
}

func TestWriteWorkers_ApplyAttrMap_OverlaysKnownColumnsAndSkipsUnknown(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)
	dest := &sqlBook{ID: 5}

	require.NoError(t, relations.ApplyAttrMap(builder, dest, nil))
	require.NoError(t, relations.ApplyAttrMap(builder, dest, map[string]any{}))
	assert.Equal(t, uint(5), dest.ID, "dest unchanged")

	require.NoError(t, relations.ApplyAttrMap(builder, dest, map[string]any{"user_id": uint(9), "no_such_column": "x"}))
	assert.Equal(t, uint(9), dest.UserID)
	assert.Equal(t, uint(5), dest.ID)

	err := relations.ApplyAttrMap(builder, nil, map[string]any{"user_id": 1})
	assert.True(t, errors.Is(err, errors.OrmQueryInvalidModel), "%v", err)
}

func TestWriteWorkers_MergeAttrMaps_ValuesWin(t *testing.T) {
	merged := relations.MergeAttrMaps(map[string]any{"a": 1, "b": 2}, map[string]any{"b": 3, "c": 4})

	assert.Equal(t, map[string]any{"a": 1, "b": 3, "c": 4}, merged)
}

// --- the parent touch ----------------------------------------------------------------------------

func TestWriteWorkers_TouchIfTouching_CompilesTheParentUpdateAndMirrorsIt(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	frozen := carbon.Now()
	carbon.SetTestNow(frozen)
	t.Cleanup(carbon.ClearTestNow)
	parent := &wwParent{ID: 7}
	info := wwInfo(t, builder, parent, "Children")
	require.True(t, info.Touches)

	require.NoError(t, relations.TouchIfTouching(builder, info, parent, uint(7)))

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `update "ww_parents" set "updated_at" = ? where "id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, 7, statements[0].args[1])
	require.NotNil(t, parent.UpdatedAt, "the bump is mirrored into the in-memory parent")
	assert.Equal(t, frozen.StdTime().Unix(), parent.UpdatedAt.StdTime().Unix())
}

func TestWriteWorkers_TouchIfTouching_SkipsWhenNotTouching(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	parent := &wwParent{ID: 7}
	info := wwInfo(t, builder, parent, "Children")
	info.Touches = false

	require.NoError(t, relations.TouchIfTouching(builder, info, parent, uint(7)))

	assert.Empty(t, recorder.take())
	assert.Nil(t, parent.UpdatedAt)
}

// --- the find-then-write reads ------------------------------------------------------------------

func TestWriteWorkers_RelationFirst_RunsTheConstrainedSingleRowRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	found, err := relations.RelationFirst(builder, &sqlUser{ID: 7}, "Books", map[string]any{"user_id": uint(1)}, &sqlBook{}, false)
	require.NoError(t, err)
	assert.False(t, found, "the recording transport answers every read with no rows")

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.True(t, strings.HasPrefix(statements[0].query, `select * from "sql_books" where`), statements[0].query)
	assert.True(t, strings.HasSuffix(statements[0].query, "limit 1"), statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, 7, statements[0].args[0], "the relation constraint binds first")
	assert.EqualValues(t, 1, statements[0].args[1], "then the attribute where")
}

func TestWriteWorkers_FindOrNew_StampsTheKeyOnAMiss(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)
	parent := &sqlUser{ID: 7}
	dest := &sqlBook{}

	require.NoError(t, relations.FindOrNew(builder, wwInfo(t, builder, parent, "Books"), parent, "Books", 3, dest))

	require.Len(t, recorder.take(), 1, "one Find")
	assert.Equal(t, uint(7), dest.UserID, "a miss pre-sets the foreign key on the zero-valued dest")
	assert.Equal(t, uint(0), dest.ID)
}

// --- the W10 seam member -------------------------------------------------------------------------

func TestWriteWorkers_PrimaryKey_ReportsColumnZeroAndPresence(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	column, isZero, ok, err := builder.PrimaryKey(&sqlBook{})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "id", column)
	assert.True(t, isZero)

	_, isZero, ok, err = builder.PrimaryKey(&sqlBook{ID: 3})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.False(t, isZero)

	column, isZero, ok, err = builder.PrimaryKey(&wwNoKey{Name: "n"})
	require.NoError(t, err)
	assert.False(t, ok, "no primary key: the findOrNew \"no primary key\" branch")
	assert.Equal(t, "", column)
	assert.True(t, isZero)

	_, _, _, err = builder.PrimaryKey(nil)
	assert.True(t, errors.Is(err, errors.OrmQueryInvalidModel), "%v", err)
}
