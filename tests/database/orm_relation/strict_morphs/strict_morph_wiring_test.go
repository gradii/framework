package strict_morphs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// This file pins the strict-morph wiring of the morph existence read path, the Go counterpart of
// Laravel's hasMorph: every requested type is resolved with (new $type)->getMorphClass() while the
// per-type orWhere closures are built, and the where() closure runs synchronously
// (QueriesRelationships.php:289), so under strict mode an unmapped type raises
// ClassMorphViolationException at call time. Oracle v12.55.1, executed (fresh probe): `strict
// whereHasMorph unmapped type threw => Illuminate\Database\ClassMorphViolationException: No
// morph map defined for model [...].`; a mixed list [mapped, unmapped] throws naming the unmapped
// type; mapped types build the usual `(<type> = ? and exists (...))` clause and an alias string
// resolves through the map identically. The Go clause builder runs inside WhereNested and keeps
// the lax per-type skip for types whose table does not resolve, so the strict failure is raised by
// the applyMorphRelation pre-flight; this test proves the error surfaces on a real query.

// strictMorphWiringComment is a comment-style parent with a MorphTo relation over a bare
// polymorphic type column pair.
type strictMorphWiringComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	CommentableID   uint64 `gorm:"column:commentable_id" db:"commentable_id"`
	CommentableType string `gorm:"column:commentable_type" db:"commentable_type"`
}

func (strictMorphWiringComment) TableName() string { return "strict_morph_wiring_comments" }

func (strictMorphWiringComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"commentable": contractsorm.MorphTo{Name: "commentable"},
	}
}

// strictMorphWiringPhoto is a bare related model: no MorphClass() override and no morph-map entry,
// so under strict mode its resolution is the violation this file asserts.
type strictMorphWiringPhoto struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
}

func (strictMorphWiringPhoto) TableName() string { return "strict_morph_wiring_photos" }

func TestStrictMorphs_WhereHasMorphStrictModeUnmappedType(t *testing.T) {
	query := newRelationQuery(t)
	runRelationSchema(t, query,
		`create table strict_morph_wiring_comments (id integer primary key autoincrement, commentable_id integer, commentable_type text)`,
		`create table strict_morph_wiring_photos (id integer primary key autoincrement)`,
	)

	morphmap.Reset()
	orm.RequireMorphMap(false)
	t.Cleanup(func() {
		morphmap.Reset()
		orm.RequireMorphMap(false)
	})

	var rows []strictMorphWiringComment

	// Lax: the unmapped type contributes the table-name fallback (the port's documented
	// divergence), the clause compiles and executes — strict mode is the only thing that fails.
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&strictMorphWiringComment{}).
		WhereHasMorph("commentable", []any{&strictMorphWiringPhoto{}}).
		Get(&rows))

	// Strict: the same call raises the class violation, the timing Laravel gives at call time.
	orm.RequireMorphMap()
	err := cloneRelationQuery(t, query).
		Model(&strictMorphWiringComment{}).
		WhereHasMorph("commentable", []any{&strictMorphWiringPhoto{}}).
		Get(&rows)
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)

	// Strict, mapped: the alias resolves before the strict check, the query runs.
	orm.EnforceMorphMap(map[string]any{"wiring_photo": &strictMorphWiringPhoto{}})
	require.NoError(t, cloneRelationQuery(t, query).
		Model(&strictMorphWiringComment{}).
		WhereHasMorph("commentable", []any{&strictMorphWiringPhoto{}}).
		Get(&rows))
}
