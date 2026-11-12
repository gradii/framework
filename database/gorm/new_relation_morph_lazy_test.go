package gorm

import (
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// The lazy MorphTo read — Related(parent, relation) followed by Get / First — resolves the
// related model from the parent's morph type column. A nullable morph pair lives in the pointer
// shapes the model layer maps: *string for the type, *sql.NullString for the id. newMorphToQuery
// was the last struct-field morph feeder that read those raw: a pointer type stringified to its
// address so no morph-map registration matched, while an id wrapper must bind by the value it
// carries and read as absent when null. loadMorphTo and the default builder already deref; these
// tests pin the lazy half through the recording driver.

// morphPtrImage mirrors morphImage with both morph columns in pointer shapes.
type morphPtrImage struct {
	ID            uint64          `gorm:"column:id;primaryKey;autoIncrement"`
	ImageableType *string         `gorm:"column:imageable_type"`
	ImageableID   *sql.NullString `gorm:"column:imageable_id"`
	Imageable     any             `gorm:"-"`
}

func (morphPtrImage) TableName() string { return "morph_ptr_images" }

func (morphPtrImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{Name: "imageable"},
	}
}

// morphWrapperImage is the driver.Valuer-by-value shape of the same columns: the alias lives in
// the wrapper's Value(), never in its fields, and the value form exercises derefKeyValue's
// pointer-receiver unwrap.
type morphWrapperImage struct {
	ID            uint64         `gorm:"column:id;primaryKey;autoIncrement"`
	ImageableType sql.NullString `gorm:"column:imageable_type"`
	ImageableID   sql.NullString `gorm:"column:imageable_id"`
	Imageable     any            `gorm:"-"`
}

func (morphWrapperImage) TableName() string { return "morph_wrapper_images" }

func (morphWrapperImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{Name: "imageable"},
	}
}

func registerLazyMorphAlias(t *testing.T, alias string) {
	t.Helper()
	morphmap.Reset()
	t.Cleanup(morphmap.Reset)
	morphmap.Register(map[string]any{alias: &morphParentLikePost{}})
}

// TestRelated_MorphTo_LazyGet_PointerMorphPairResolves is the regression for the lazy read's raw
// morph feeder: with the type behind a *string the read stringified the pointer address,
// morphmap.Find missed, and Get failed with OrmMorphTypeUnknown before any statement ran. After
// the deref the registered alias resolves the related table, the *sql.NullString id binds by the
// value it wraps, and the row hydrates.
func TestRelated_MorphTo_LazyGet_PointerMorphPairResolves(t *testing.T) {
	registerLazyMorphAlias(t, "ptr_post")

	q, recorder := newRecordingRelQuery(t, &morphPtrImage{})
	recorder.returnRows(`from "morph_parent_like_posts"`, []string{"id", "title"},
		[]driver.Value{int64(42), "probe"},
	)

	alias := "ptr_post"
	id := sql.NullString{String: "42", Valid: true}
	parent := &morphPtrImage{ID: 1, ImageableType: &alias, ImageableID: &id}

	rel := q.Related(parent, "Imageable")

	var got []morphParentLikePost
	require.NoError(t, rel.Get(&got))

	require.Len(t, got, 1)
	assert.Equal(t, uint(42), got[0].ID)
	assert.Equal(t, "probe", got[0].Title)

	require.Equal(t, 1, recorder.count(), "the lazy read runs one statement")
	last := recorder.last(t)
	// Byte-for-byte the oracle's happy-path read (with this fixture's table name): the owner key
	// wears the resolved table's qualification.
	assert.Equal(t, `select * from "morph_parent_like_posts" where "morph_parent_like_posts"."id" = ?`, last.sql)
	assert.Equal(t, []driver.Value{"42"}, last.args, "the wrapper id binds by the value it wraps")

	// The framework-layer binding, before database/sql unwraps anything: the deref must hand the
	// statement the unwrapped key, not the pointer or the wrapper.
	relBuilder, ok := ormBuilderOf(rel)
	require.True(t, ok)
	statement, bindings, err := relBuilder.dest(&morphParentLikePost{}).compileSelect()
	require.NoError(t, err)
	assert.Contains(t, statement, `from "morph_parent_like_posts"`)
	assert.Equal(t, []any{"42"}, bindings)
}

// TestRelated_MorphTo_LazyFirst_PointerMorphPairResolves drives the same defect through the other
// terminal: First compiles the single-row read and must resolve the pointer-typed type column and
// bind the wrapped id exactly as Get does.
func TestRelated_MorphTo_LazyFirst_PointerMorphPairResolves(t *testing.T) {
	registerLazyMorphAlias(t, "ptr_post")

	q, recorder := newRecordingRelQuery(t, &morphPtrImage{})
	recorder.returnRows(`from "morph_parent_like_posts"`, []string{"id", "title"},
		[]driver.Value{int64(42), "probe"},
	)

	alias := "ptr_post"
	id := sql.NullString{String: "42", Valid: true}
	parent := &morphPtrImage{ID: 1, ImageableType: &alias, ImageableID: &id}

	var got morphParentLikePost
	require.NoError(t, q.Related(parent, "Imageable").First(&got))
	assert.Equal(t, uint(42), got.ID)

	require.Equal(t, 1, recorder.count())
	last := recorder.last(t)
	// Oracle: `select * from "ghost_relateds" where "ghost_relateds"."id" = ? limit 1`. compileFirst
	// additionally orders by the model's key when the caller gave none — this ORM's deterministic
	// first() convention — so the statement is asserted around the oracle part.
	assert.True(t, strings.HasPrefix(last.sql, `select * from "morph_parent_like_posts" where "morph_parent_like_posts"."id" = ?`), last.sql)
	assert.True(t, strings.HasSuffix(last.sql, "limit 1"), last.sql)
	assert.Equal(t, []driver.Value{"42"}, last.args)
}

// TestRelated_MorphTo_LazyGet_ValueWrapperMorphPairResolves covers the driver.Valuer-by-value
// shape: sql.NullString value fields reach the morph map and the WHERE through Value(), never
// through their "{alias true}" fields.
func TestRelated_MorphTo_LazyGet_ValueWrapperMorphPairResolves(t *testing.T) {
	registerLazyMorphAlias(t, "wrapper_post")

	q, recorder := newRecordingRelQuery(t, &morphWrapperImage{})
	recorder.returnRows(`from "morph_parent_like_posts"`, []string{"id", "title"},
		[]driver.Value{int64(42), "probe"},
	)

	parent := &morphWrapperImage{
		ID:            1,
		ImageableType: sql.NullString{String: "wrapper_post", Valid: true},
		ImageableID:   sql.NullString{String: "42", Valid: true},
	}

	var got []morphParentLikePost
	require.NoError(t, q.Related(parent, "Imageable").Get(&got))

	require.Len(t, got, 1)
	assert.Equal(t, uint(42), got[0].ID)
	assert.Equal(t, []driver.Value{"42"}, recorder.last(t).args)

	relBuilder, ok := ormBuilderOf(q.Related(parent, "Imageable"))
	require.True(t, ok)
	statement, bindings, err := relBuilder.dest(&morphParentLikePost{}).compileSelect()
	require.NoError(t, err)
	assert.Contains(t, statement, `from "morph_parent_like_posts"`)
	assert.Equal(t, []any{"42"}, bindings)
}

// TestRelated_MorphTo_LazyRead_NullMorphIDStaysAbsent pins the absent half: an invalid id wrapper
// (or a nil pointer) carries no key, the deref reads it as the null check, and the relation stays
// absent. Without the deref the raw wrapper bound as a value parameter — "= ?" with a NULL — and
// the NULL wrapper's "{ false}" fields reached the statement.
func TestRelated_MorphTo_LazyRead_NullMorphIDStaysAbsent(t *testing.T) {
	registerLazyMorphAlias(t, "ptr_post")

	q, recorder := newRecordingRelQuery(t, &morphPtrImage{})
	alias := "ptr_post"

	invalid := &sql.NullString{Valid: false}
	parent := &morphPtrImage{ID: 1, ImageableType: &alias, ImageableID: invalid}

	var got []morphParentLikePost
	require.NoError(t, q.Related(parent, "Imageable").Get(&got))
	assert.Empty(t, got)

	require.Equal(t, 1, recorder.count())
	last := recorder.last(t)
	assert.Equal(t, `select * from "morph_parent_like_posts" where "morph_parent_like_posts"."id" is null`, last.sql,
		"an invalid id wrapper must read as the qualified null check")
	assert.Empty(t, last.args)

	// A nil pointer id is the same absence, through First.
	nilID := &morphPtrImage{ID: 2, ImageableType: &alias}
	var first morphParentLikePost
	require.NoError(t, q.Related(nilID, "Imageable").First(&first))
	assert.Zero(t, first.ID)
	// The same deterministic-first ordering stands between the qualified null check and limit 1.
	firstStatement := recorder.last(t).sql
	assert.True(t, strings.HasPrefix(firstStatement, `select * from "morph_parent_like_posts" where "morph_parent_like_posts"."id" is null`), firstStatement)
	assert.True(t, strings.HasSuffix(firstStatement, "limit 1"), firstStatement)
}

// TestRelated_MorphTo_LazyRead_NilPointerTypeYieldsZeroRowQuery: a nil *string type carries no
// alias and must read as the empty type column — the zero-row guard — not as the unknown type
// "<nil>" the raw fmt.Sprint rendered. Before the deref this failed with OrmMorphTypeUnknown.
func TestRelated_MorphTo_LazyRead_NilPointerTypeYieldsZeroRowQuery(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &morphPtrImage{})

	parent := &morphPtrImage{ID: 1, ImageableID: &sql.NullString{String: "42", Valid: true}}

	var got []morphParentLikePost
	require.NoError(t, q.Related(parent, "Imageable").Get(&got))
	assert.Empty(t, got)

	require.Equal(t, 1, recorder.count())
	assert.Contains(t, recorder.last(t).sql, "1 = 0")
}
