// This file drives the BelongsTo / MorphTo relation-object leaf —
// database/gorm/relations/belongs_to.go — through the real *gorm.OrmBuilder of the recording
// transport (base_semantics_test.go): the GetChild accessor, the Associate / Dissociate /
// Disassociate forwards into the relation write shells, the morph-aware Is comparison and the
// Touch guard are all asserted on the production objects.
//
// Oracle pins (illuminate/database v12.55.1, probes run 2026-11-01 — /tmp/bt_mt_is_oracle.php,
// /tmp/bt_mt_touch_oracle.php, /tmp/bt_mt_write_oracle.php, real models on live sqlite):
//
//   - is()/isNot() (ComparesRelatedModels): equal keys true; an integer key against a numeric
//     string true in either direction (compareKeys' (int) branch); a different key, a model of
//     another table and a model of another connection false; a null parent foreign key, a null
//     model key and a null model false — the keys are weighed before the table or the connection
//     is consulted. A MorphTo whose type column names a class or morph-map alias compares
//     against the resolved class's table, and morphInstanceTo defaults the owner key to
//     $instance->getKeyName(), so the compared key is the type class's key value. A MorphTo whose
//     type is null or empty takes the morphEagerTo branch — a null owner key, so is() is false
//     for every model — and an unregistered type cannot resolve a class (PHP: class-not-found);
//     the port reports no match. The connection comparison weighs the related side's connection:
//     the class's own declaration, or the declaring model's when the class declares none
//     (newRelatedInstance).
//   - touch(): the BelongsTo.php:227 / MorphTo.php:271 guard — a null parent key skips the
//     statement entirely; otherwise the related model's updated-at column is stamped through the
//     relation constraint, `update "morph_targets" set "updated_at" = ? where "morph_targets"."id" = ?`
//     for a type-resolved MorphTo (bindings [stamp, 7]); a related class that ignores touches
//     stamps nothing.
//   - associate()/dissociate(): Laravel's attribute effects are setAttribute(foreign_key, owner
//     key) plus setRelation (MorphTo also setAttribute(morph_type, getMorphClass())); dissociate
//     writes both columns null; nothing is persisted. The Go shells persist the parent right away
//     (the corpus ports' documented divergence), which is what the statement assertions capture.
//     MorphTo::associate falls back to the model's key name when a declared owner key's value is
//     falsy — not ported: the shell reads the declared key unconditionally.
package relations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/support/carbon"
)

// --- fixtures --------------------------------------------------------------------------------------

type btAuthor struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (btAuthor) TableName() string { return "bt_authors" }

// btAuthorAltConn shares btAuthor's table but declares a connection — the model half of the
// connection-equality branch of Is.
type btAuthorAltConn struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (btAuthorAltConn) TableName() string  { return "bt_authors" }
func (btAuthorAltConn) Connection() string { return "bt_alt" }

type btOther struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (btOther) TableName() string { return "bt_others" }

type btBook struct {
	ID       uint64    `gorm:"column:id;primaryKey"`
	AuthorID *uint64   `gorm:"column:author_id"`
	Author   *btAuthor `gorm:"-"`
}

func (btBook) TableName() string { return "bt_books" }

func (btBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &btAuthor{}, ForeignKey: "author_id", OwnerKey: "id"},
	}
}

// btAltAuthorBook is the same relation shape resolving to the connection-declaring author model:
// the relation's connection reference takes btAuthorAltConn's declared name.
type btAltAuthorBook struct {
	ID       uint64  `gorm:"column:id;primaryKey"`
	AuthorID *uint64 `gorm:"column:author_id"`
}

func (btAltAuthorBook) TableName() string { return "bt_books" }

func (btAltAuthorBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &btAuthorAltConn{}, ForeignKey: "author_id", OwnerKey: "id"},
	}
}

// btStringKeyAuthor shares btAuthor's table under a text key — the string-model-key half of the
// compareKeys cast matrix for the BelongsTo kind (oracle: bt.is.int-parent-string-model).
type btStringKeyAuthor struct {
	ID string `gorm:"column:id;primaryKey"`
}

func (btStringKeyAuthor) TableName() string { return "bt_authors" }

// btStringFkBook carries a text foreign key against the integer-keyed btAuthor — the
// string-parent-key half of the cast matrix (oracle: bt.is.string-parent-int-model).
type btStringFkBook struct {
	ID       uint64 `gorm:"column:id;primaryKey"`
	AuthorID string `gorm:"column:author_id"`
}

func (btStringFkBook) TableName() string { return "bt_books" }

func (btStringFkBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &btAuthor{}, ForeignKey: "author_id", OwnerKey: "id"},
	}
}

// btConnAuthorBook declares a connection on the declaring (child) side while its related model
// btAuthor declares none — Eloquent's newRelatedInstance hands the related instance the
// declaring model's connection, so the comparison sees "bt_alt" on the related side (oracle:
// bt.alt-parent.related.connection "alt", bt.alt-parent.is.alt-model true,
// bt.alt-parent.is.plain-model false).
type btConnAuthorBook struct {
	ID       uint64  `gorm:"column:id;primaryKey"`
	AuthorID *uint64 `gorm:"column:author_id"`
}

func (btConnAuthorBook) TableName() string  { return "bt_books" }
func (btConnAuthorBook) Connection() string { return "bt_alt" }

func (btConnAuthorBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &btAuthor{}, ForeignKey: "author_id", OwnerKey: "id"},
	}
}

// --- morph fixtures --------------------------------------------------------------------------------

type btMorphParent struct {
	ID         uint64 `gorm:"column:id;primaryKey"`
	TargetID   uint64 `gorm:"column:target_id"`
	TargetType string `gorm:"column:target_type"`
	Target     any    `gorm:"-"`
}

func (btMorphParent) TableName() string { return "bt_morph_parents" }

func (btMorphParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Target": contractsorm.MorphTo{Name: "target", TypeColumn: "target_type", IDColumn: "target_id"},
	}
}

// btStringFkParent carries a text morph id — the string-parent-key half of the compareKeys cast
// matrix.
type btStringFkParent struct {
	ID         uint64 `gorm:"column:id;primaryKey"`
	TargetID   string `gorm:"column:target_id"`
	TargetType string `gorm:"column:target_type"`
}

func (btStringFkParent) TableName() string { return "bt_string_fk_parents" }

func (btStringFkParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Target": contractsorm.MorphTo{Name: "target", TypeColumn: "target_type", IDColumn: "target_id"},
	}
}

// btAltConnMorphParent declares a connection while its morph type resolves to btMorphTarget,
// which declares none — the related side inherits "bt_alt" (oracle:
// mt.alt-parent.related.connection "alt", mt.alt-parent.is.alt-model true,
// mt.alt-parent.is.plain-model false).
type btAltConnMorphParent struct {
	ID         uint64 `gorm:"column:id;primaryKey"`
	TargetID   uint64 `gorm:"column:target_id"`
	TargetType string `gorm:"column:target_type"`
}

func (btAltConnMorphParent) TableName() string  { return "bt_morph_parents" }
func (btAltConnMorphParent) Connection() string { return "bt_alt" }

func (btAltConnMorphParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Target": contractsorm.MorphTo{Name: "target", TypeColumn: "target_type", IDColumn: "target_id"},
	}
}

// btMorphTarget is the type-resolved target: it names its morph alias through MorphClass() (the
// Go form of getMorphClass()) and carries the updated-at column Touch stamps.
type btMorphTarget struct {
	ID        uint64           `gorm:"column:id;primaryKey"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (btMorphTarget) TableName() string  { return "bt_morph_targets" }
func (btMorphTarget) MorphClass() string { return "bt_morph_target" }

// btMorphTargetAltConn shares the target's table but declares a connection and its own alias.
type btMorphTargetAltConn struct {
	ID        uint64           `gorm:"column:id;primaryKey"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (btMorphTargetAltConn) TableName() string  { return "bt_morph_targets" }
func (btMorphTargetAltConn) Connection() string { return "bt_alt" }
func (btMorphTargetAltConn) MorphClass() string { return "bt_morph_target_alt" }

// btMorphStamplessTarget declares no updated-at column — the class shape that ignores touches.
type btMorphStamplessTarget struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (btMorphStamplessTarget) TableName() string  { return "bt_morph_stampless_targets" }
func (btMorphStamplessTarget) MorphClass() string { return "bt_morph_stampless" }

type btMorphOther struct {
	ID uint64 `gorm:"column:id;primaryKey"`
}

func (btMorphOther) TableName() string { return "bt_morph_others" }

// btStringTarget shares the resolved target's table under a text key — the string-related-key
// half of the compareKeys cast matrix (a Go model cannot flip its key field per instance, so the
// string-keyed shape is its own type on the same table).
type btStringTarget struct {
	ID string `gorm:"column:id;primaryKey"`
}

func (btStringTarget) TableName() string { return "bt_morph_targets" }

// registerBtMorphFixtures (re-)maps the fixtures' morph aliases in the process-wide morph map —
// the MorphTo read path resolves the type column through Find, and Register merges — so each
// morph test starts from its own known registrations regardless of other tests' morph-map use.
func registerBtMorphFixtures() {
	morphmap.Register(map[string]any{
		"bt_morph_target":     &btMorphTarget{},
		"bt_morph_target_alt": &btMorphTargetAltConn{},
		"bt_morph_stampless":  &btMorphStamplessTarget{},
	})
}

// btStatementHasValue reports whether one of the statement arguments — after unwrapping a
// pointer layer — equals want under loose numeric comparison.
func btStatementHasValue(args []any, want any) bool {
	for _, arg := range args {
		if assert.ObjectsAreEqualValues(want, deref(arg)) {
			return true
		}
	}

	return false
}

// --- GetChild --------------------------------------------------------------------------------------

func TestBelongsToObject_GetChildReturnsTheWrappedParent(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	child := &btBook{ID: 1}
	rel := builder.Related(child, "Author")

	// Eloquent's BelongsTo::getChild() returns $this->child — the model the relation hangs off.
	assert.Same(t, child, rel.(contractsorm.BelongsToRelation).GetChild())
}

func TestMorphToObject_GetChildReturnsTheWrappedParent(t *testing.T) {
	registerBtMorphFixtures()

	builder, _ := newRelationTestBuilder(t)
	parent := &btMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_target"}
	rel := builder.Related(parent, "Target")

	// MorphTo inherits BelongsTo::getChild(); MorphToRelation narrows through the same interface.
	assert.Same(t, parent, rel.(contractsorm.BelongsToRelation).GetChild())
}

// --- Associate / Dissociate / Disassociate forwards --------------------------------------------------

func TestBelongsToObject_AssociateWritesTheOwnerKeyAndPersists(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	child := &btBook{ID: 1}
	rel := builder.Related(child, "Author").(contractsorm.BelongsToRelation)
	require.NoError(t, rel.Associate(&btAuthor{ID: 5}))

	// Oracle (write probe): $book->author()->associate($author) sets foreign_key = 5 and caches
	// the owner; Laravel does not persist. The Go shell persists the child — the documented
	// divergence — and the foreign key column is what the statement writes.
	require.NotNil(t, child.AuthorID)
	assert.EqualValues(t, 5, *child.AuthorID)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.True(t, strings.HasPrefix(statements[0].query, `update "bt_books" set `), statements[0].query)
	assert.Contains(t, statements[0].query, `"author_id"`)
	assert.True(t, btStatementHasValue(statements[0].args, 5), "the owner key must be bound: %v", statements[0].args)
	assert.True(t, btStatementHasValue(statements[0].args, 1), "the child's key must select the row: %v", statements[0].args)
}

func TestBelongsToObject_AssociateNilDissociateDisassociateClearTheForeignKey(t *testing.T) {
	cases := map[string]func(rel contractsorm.BelongsToRelation) error{
		"Associate(nil)": func(rel contractsorm.BelongsToRelation) error { return rel.Associate(nil) },
		"Dissociate":     func(rel contractsorm.BelongsToRelation) error { return rel.Dissociate() },
		"Disassociate":   func(rel contractsorm.BelongsToRelation) error { return rel.Disassociate() },
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			builder, recorder := newRelationTestBuilder(t)

			fk := uint64(9)
			child := &btBook{ID: 1, AuthorID: &fk}
			rel := builder.Related(child, "Author").(contractsorm.BelongsToRelation)

			require.NoError(t, call(rel))

			// Oracle: associate(null) is dissociate's attribute effect (foreign_key null); the
			// dissociate/disassociate spellings are the same write. The Go shell clears the
			// nullable column and persists — the documented divergence.
			assert.Nil(t, child.AuthorID)

			statements := recorder.take()
			require.Len(t, statements, 1)
			assert.True(t, strings.HasPrefix(statements[0].query, `update "bt_books" set `), statements[0].query)
			assert.Contains(t, statements[0].query, `"author_id"`)
		})
	}
}

func TestMorphToObject_AssociateWritesKeyAndMorphType(t *testing.T) {
	registerBtMorphFixtures()

	builder, recorder := newRelationTestBuilder(t)

	parent := &btMorphParent{ID: 1, TargetType: "bt_morph_target"}
	rel := builder.Related(parent, "Target").(contractsorm.BelongsToRelation)
	require.NoError(t, rel.Associate(&btMorphTarget{ID: 3}))

	// Oracle (write probe): MorphTo::associate sets foreign_key = the model's key and morph_type
	// = getMorphClass(); the Go writer takes the alias from MorphClass() (else the morph map,
	// else the owner's table name) and persists the parent — the documented divergence.
	assert.EqualValues(t, 3, parent.TargetID)
	assert.Equal(t, "bt_morph_target", parent.TargetType)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.True(t, strings.HasPrefix(statements[0].query, `update "bt_morph_parents" set `), statements[0].query)
	assert.Contains(t, statements[0].query, `"target_id"`)
	assert.Contains(t, statements[0].query, `"target_type"`)
	assert.True(t, btStatementHasValue(statements[0].args, 3), "the morph id must be bound: %v", statements[0].args)
	assert.True(t, btStatementHasValue(statements[0].args, "bt_morph_target"), "the morph type must be bound: %v", statements[0].args)
}

func TestMorphToObject_NilDissociateDisassociateClearKeyAndType(t *testing.T) {
	cases := map[string]func(rel contractsorm.BelongsToRelation) error{
		"Associate(nil)": func(rel contractsorm.BelongsToRelation) error { return rel.Associate(nil) },
		"Dissociate":     func(rel contractsorm.BelongsToRelation) error { return rel.Dissociate() },
		"Disassociate":   func(rel contractsorm.BelongsToRelation) error { return rel.Disassociate() },
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			registerBtMorphFixtures()

			builder, recorder := newRelationTestBuilder(t)

			parent := &btMorphParent{ID: 1, TargetID: 3, TargetType: "bt_morph_target"}
			rel := builder.Related(parent, "Target").(contractsorm.BelongsToRelation)

			require.NoError(t, call(rel))

			// Oracle (write probe): associcate(null) / dissociate() write foreign_key null and
			// morph_type null. The Go model's columns are plain values, so Laravel's null is the
			// field's zero value here (the corpus port's documented divergence); the shell also
			// persists the cleared parent.
			assert.Zero(t, parent.TargetID)
			assert.Zero(t, parent.TargetType)

			statements := recorder.take()
			require.Len(t, statements, 1)
			assert.True(t, strings.HasPrefix(statements[0].query, `update "bt_morph_parents" set `), statements[0].query)
			assert.Contains(t, statements[0].query, `"target_id"`)
			assert.Contains(t, statements[0].query, `"target_type"`)
		})
	}
}

// --- Is / IsNot ------------------------------------------------------------------------------------

func TestBelongsToObject_IsComparesKeysTableAndConnection(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	authorID := uint64(5)
	rel := builder.Related(&btBook{ID: 1, AuthorID: &authorID}, "Author")

	// Oracle: equal keys with the same table and connection match.
	assert.True(t, rel.Is(&btAuthor{ID: 5}))
	assert.False(t, rel.Is(&btAuthor{ID: 6}), "a different owner key is no match")
	assert.True(t, rel.IsNot(&btAuthor{ID: 6}))
	assert.False(t, rel.Is(&btOther{ID: 5}), "a model of another table is no match")
	assert.False(t, rel.Is(&btAuthorAltConn{ID: 5}), "a model of another connection is no match")
	assert.False(t, rel.Is(nil), "nil never matches")
	assert.False(t, rel.Is((*btAuthor)(nil)), "a nil pointer never matches")

	// Oracle: an integer key against a numeric string compares numerically in either direction —
	// the corpus's integer-parent-key / integer-related-key cases (bt.is.int-parent-string-model,
	// bt.is.string-parent-int-model).
	assert.True(t, rel.Is(&btStringKeyAuthor{ID: "5"}), "an integer FK against a numeric-string key compares via (int)")
	assert.False(t, rel.Is(&btStringKeyAuthor{ID: "6"}))

	stringFkRel := builder.Related(&btStringFkBook{ID: 1, AuthorID: "5"}, "Author")
	assert.True(t, stringFkRel.Is(&btAuthor{ID: 5}), "a numeric-string FK against an integer key compares via (int)")
	assert.False(t, stringFkRel.Is(&btAuthor{ID: 6}))

	// A null parent foreign key is no match before any related-side read.
	assert.False(t, builder.Related(&btBook{ID: 1}, "Author").Is(&btAuthor{ID: 5}))

	// Connection equality weighs the related side's connection — the related class's own
	// declaration, or, when it declares none, the declaring model's (newRelatedInstance) —
	// against the model's declared connection.
	altRel := builder.Related(&btAltAuthorBook{ID: 1, AuthorID: &authorID}, "Author")
	assert.True(t, altRel.Is(&btAuthorAltConn{ID: 5}), "the related class's own connection is the reference")
	assert.False(t, altRel.Is(&btAuthor{ID: 5}))

	// The related model declares none, so it inherits the declaring model's "bt_alt": the plain
	// model is a non-match and the alt-connected model matches (oracle:
	// bt.alt-parent.is.plain-model false, bt.alt-parent.is.alt-model true).
	connRel := builder.Related(&btConnAuthorBook{ID: 1, AuthorID: &authorID}, "Author")
	assert.False(t, connRel.Is(&btAuthor{ID: 5}))
	assert.True(t, connRel.Is(&btAuthorAltConn{ID: 5}))
}

func TestMorphToObject_IsUsesTheResolvedTypeTable(t *testing.T) {
	registerBtMorphFixtures()

	builder, _ := newRelationTestBuilder(t)

	// Oracle (is probe, type set through a morph-map-style alias): the relation's related model is
	// the resolved class — table morph_targets, owner key id — so is() compares the parent's morph
	// id against the model's id, plus table and connection equality.
	rel := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_target"}, "Target")
	assert.True(t, rel.Is(&btMorphTarget{ID: 7}))
	assert.False(t, rel.Is(&btMorphTarget{ID: 8}), "a different key is no match")
	assert.True(t, rel.IsNot(&btMorphTarget{ID: 8}))
	assert.False(t, rel.Is(&btMorphOther{ID: 7}), "a model of another table is no match")
	assert.False(t, rel.Is(&btMorphTargetAltConn{ID: 7}), "a model of another connection is no match")
	assert.False(t, rel.Is(nil), "nil never matches")

	// Oracle: integer parent key against a numeric-string model key, and the reverse, both cast
	// through (int) — '7' and '07' equal 7 against the integer side.
	assert.True(t, rel.Is(&btStringTarget{ID: "7"}))
	assert.True(t, rel.Is(&btStringTarget{ID: "07"}))
	assert.False(t, rel.Is(&btStringTarget{ID: "8"}))
	stringParent := builder.Related(&btStringFkParent{ID: 1, TargetID: "7", TargetType: "bt_morph_target"}, "Target")
	assert.True(t, stringParent.Is(&btMorphTarget{ID: 7}))

	// Oracle: a null parent morph id is false before any table read.
	unset := builder.Related(&btMorphParent{ID: 1, TargetType: "bt_morph_target"}, "Target")
	assert.False(t, unset.Is(&btMorphTarget{ID: 7}))

	// The resolved type's table follows the alias: an alias resolving to the
	// connection-declaring class makes its table/connection the reference.
	altSample := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_target_alt"}, "Target")
	assert.True(t, altSample.Is(&btMorphTargetAltConn{ID: 7}))
	assert.False(t, altSample.Is(&btMorphTarget{ID: 7}))

	// A resolved class that declares no connection inherits the declaring model's — the
	// alt-connected model matches, the plain one does not (oracle:
	// mt.alt-parent.related.connection "alt", mt.alt-parent.is.alt-model true,
	// mt.alt-parent.is.plain-model false).
	altParent := builder.Related(&btAltConnMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_target"}, "Target")
	assert.True(t, altParent.Is(&btMorphTargetAltConn{ID: 7}))
	assert.False(t, altParent.Is(&btMorphTarget{ID: 7}))
}

// --- Touch -----------------------------------------------------------------------------------------

func TestMorphToObject_IsIsFalseWhenTheTypeResolvesNoClass(t *testing.T) {
	registerBtMorphFixtures()

	builder, recorder := newRelationTestBuilder(t)

	// Oracle (mt.eager branch): a null type routes relation construction through morphEagerTo,
	// whose owner key stays null — is() weighs a null model key, so the parent-class model
	// (mt.eager.is.parent-class-model) and the target model (mt.eager.is.target-model) are both
	// no match.
	unset := builder.Related(&btMorphParent{ID: 1, TargetID: 7}, "Target")
	assert.False(t, unset.Is(&btMorphParent{ID: 7}))
	assert.False(t, unset.Is(&btMorphTarget{ID: 7}))
	assert.True(t, unset.IsNot(&btMorphTarget{ID: 7}))

	// The empty string takes the same branch (mt.empty.is.parent-class-model false).
	empty := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: ""}, "Target")
	assert.False(t, empty.Is(&btMorphParent{ID: 7}))

	// An unregistered type cannot resolve a related class at all — PHP raises "Class ... not
	// found" when the relation resolves; the object reports no match instead of comparing
	// against an unrelated model.
	ghost := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: "ghost_alias"}, "Target")
	assert.False(t, ghost.Is(&btMorphTarget{ID: 7}))

	// Touch keeps the same zero-row effect the oracle's broken morphEagerTo statement has (the
	// oracle compiles `update "morph_parents" set "updated_at" = ? where "" = ?`, matching no
	// row); the port reports the no-op without emitting a statement.
	result, err := unset.Touch()
	require.NoError(t, err)
	require.Zero(t, result.RowsAffected)
	assert.Empty(t, recorder.take())
}

func TestMorphToObject_TouchStampsTheResolvedTable(t *testing.T) {
	registerBtMorphFixtures()

	builder, recorder := newRelationTestBuilder(t)

	rel := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_target"}, "Target")
	before := time.Now()
	result, err := rel.Touch()
	require.NoError(t, err)
	require.NotNil(t, result)

	// Oracle (touch probe): $parent->relation()->touch() with the type set and the id present —
	// `update "morph_targets" set "updated_at" = ? where "morph_targets"."id" = ?` with bindings
	// [stamp, 1]; the resolved type's table is the update target and the constraint is the
	// qualified owner key, no morph-type filter.
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `update "bt_morph_targets" set "updated_at" = ? where "bt_morph_targets"."id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	stamp, ok := statements[0].args[0].(time.Time)
	require.True(t, ok, "expected a timestamp binding, got %T", statements[0].args[0])
	assert.WithinDuration(t, before, stamp, time.Minute)
	assert.EqualValues(t, 7, deref(statements[0].args[1]))
}

func TestMorphToObject_TouchSkipsAbsentKeyAndIgnoringRelated(t *testing.T) {
	registerBtMorphFixtures()

	t.Run("absent morph id", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)

		// Oracle: MorphTo::touch() — like BelongsTo's (MorphTo.php:271, BelongsTo.php:227) — skips
		// the statement entirely for a null parent key.
		rel := builder.Related(&btMorphParent{ID: 1, TargetType: "bt_morph_target"}, "Target")
		result, err := rel.Touch()
		require.NoError(t, err)
		assert.EqualValues(t, 0, result.RowsAffected)
		assert.Empty(t, recorder.take())
	})

	t.Run("related ignores touches", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)

		// Oracle: a related class without an updated-at column stamps nothing.
		rel := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_stampless"}, "Target")
		result, err := rel.Touch()
		require.NoError(t, err)
		assert.EqualValues(t, 0, result.RowsAffected)
		assert.Empty(t, recorder.take())
	})

	t.Run("explicit column bypasses the model checks", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)

		// The Go signature shadows Query.Touch: an explicitly named column bypasses the ignoring
		// check the default path runs (the documented shadowing rule; PHP's Relation::touch takes
		// no column). A related class the default path skips — no updated-at column — still
		// writes the named column.
		rel := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_stampless"}, "Target")
		_, err := rel.Touch("checked_at")
		require.NoError(t, err)

		statements := recorder.take()
		require.Len(t, statements, 1)
		assert.Equal(t, `update "bt_morph_stampless_targets" set "checked_at" = ? where "bt_morph_stampless_targets"."id" = ?`, statements[0].query)
	})

	t.Run("explicit column on a timestamped target keeps the named column", func(t *testing.T) {
		builder, recorder := newRelationTestBuilder(t)

		// The model-aware update path additionally stamps the model's own updated-at column (the
		// standard Update behaviour), so this assertion keeps to the named column's presence.
		rel := builder.Related(&btMorphParent{ID: 1, TargetID: 7, TargetType: "bt_morph_target"}, "Target")
		_, err := rel.Touch("checked_at")
		require.NoError(t, err)

		statements := recorder.take()
		require.Len(t, statements, 1)
		assert.True(t, strings.HasPrefix(statements[0].query, `update "bt_morph_targets" set `), statements[0].query)
		assert.Contains(t, statements[0].query, `"checked_at"`)
	})
}
