package gorm

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/support/carbon"
)

// touchingUnitUser / touchingUnitPost mirror the corpus's EloquentTouching fixtures at the unit
// level: the post declares its user relation as touched, the user's posts relation stays plain.

type touchingUnitUser struct {
	ID        uint                `gorm:"column:id;primaryKey"`
	Name      string              `gorm:"column:name"`
	UpdatedAt *carbon.DateTime    `gorm:"autoUpdateTime;column:updated_at"`
	Posts     []*touchingUnitPost `gorm:"-"`
}

func (touchingUnitUser) TableName() string { return "touching_users" }

func (touchingUnitUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &touchingUnitPost{}, ForeignKey: "user_id"},
	}
}

type touchingUnitPost struct {
	ID     uint              `gorm:"column:id;primaryKey"`
	UserID uint              `gorm:"column:user_id"`
	User   *touchingUnitUser `gorm:"-"`
}

func (touchingUnitPost) TableName() string { return "touching_posts" }

func (touchingUnitPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{Related: &touchingUnitUser{}, ForeignKey: "user_id", Touches: true},
	}
}

func TestTouchedRelationNames_CollectsSortedTouchingRelationsOnly(t *testing.T) {
	names := touchedRelationNames(map[string]contractsorm.Relation{
		"User":   contractsorm.BelongsTo{Related: &touchingUnitUser{}, ForeignKey: "user_id", Touches: true},
		"Posts":  contractsorm.HasMany{Related: &touchingUnitPost{}, ForeignKey: "user_id"},
		"Parent": contractsorm.HasOne{Related: &touchingUnitPost{}, ForeignKey: "parent_id", Touches: true},
		// Many2Many.Touches is the pivot-write behaviour of touchIfTouching, not a save-or-delete
		// owner touch, so the walk does not collect it.
		"Roles": contractsorm.Many2Many{Related: &touchingUnitUser{}, Touches: true},
	})

	assert.Equal(t, []string{"Parent", "User"}, names)
}

func TestTouchColumns_OrientsTheKeyPairPerKind(t *testing.T) {
	belongs, err := relations.ResolveRelation(&touchingUnitPost{}, "User")
	require.NoError(t, err)

	relatedColumn, parentColumn, ok := touchColumns(belongs)
	require.True(t, ok)
	// BelongsTo: the parent carries the foreign key, matched against the related owner key.
	assert.Equal(t, "id", relatedColumn)
	assert.Equal(t, "user_id", parentColumn)

	hasMany, err := relations.ResolveRelation(&touchingUnitUser{}, "Posts")
	require.NoError(t, err)

	relatedColumn, parentColumn, ok = touchColumns(hasMany)
	require.True(t, ok)
	// HasMany: the related carries the foreign key, matched against the parent local key.
	assert.Equal(t, "user_id", relatedColumn)
	assert.Equal(t, "id", parentColumn)
}

func TestTouchRelationUpdate_CompilesTheOwnerTouchStatement(t *testing.T) {
	query := compiledQuery(t, &touchingUnitPost{}, Conditions{})

	desc, err := relations.ResolveRelation(&touchingUnitPost{}, "User")
	require.NoError(t, err)

	update, err := query.touchRelationUpdate(desc, "id", 1)
	require.NoError(t, err)

	// Relation::touch()'s rawUpdate compiles as one unscoped update of the related table matched by
	// the relation's key pair; oracle (illuminate/database v12.55.1, executed against sqlite)
	// rendered the BelongsTo shape as `update "users" set "updated_at" = ? where "users"."id" = ?`
	// — the qualified owner key BelongsTo::addConstraints installs.
	stamp := time.Now()
	statement, bindings, err := update.compileUpdateScoped(map[string]any{"updated_at": stamp}, false)
	require.NoError(t, err)

	assert.Equal(t, `update "touching_users" set "updated_at" = ? where "touching_users"."id" = ?`, statement)
	// The bindings run in statement order: the stamped value first, the key of the where clause second.
	assert.Equal(t, []any{stamp, 1}, bindings)

	// The HasOne / HasMany shape adds the not-null guard its constraint carries; oracle rendered
	// `update "posts" set "updated_at" = ? where "posts"."user_id" = ? and "posts"."user_id" is not null`.
	hasMany, err := relations.ResolveRelation(&touchingUnitUser{}, "Posts")
	require.NoError(t, err)

	hasManyUpdate, err := query.touchRelationUpdate(hasMany, "user_id", 1)
	require.NoError(t, err)

	statement, bindings, err = hasManyUpdate.compileUpdateScoped(map[string]any{"updated_at": stamp}, false)
	require.NoError(t, err)

	assert.Equal(t, `update "touching_posts" set "updated_at" = ? where "touching_posts"."user_id" = ? and "touching_posts"."user_id" is not null`, statement)
	assert.Equal(t, []any{stamp, 1}, bindings)
}

// touchingNullableOwnerPost carries its foreign key as a pointer — the Go shape that can hold
// NULL, which Eloquent's BelongsTo::touch() and getResults() both short-circuit on.
type touchingNullableOwnerPost struct {
	ID     uint              `gorm:"column:id;primaryKey"`
	UserID *uint64           `gorm:"column:user_id"`
	User   *touchingUnitUser `gorm:"-"`
}

func (touchingNullableOwnerPost) TableName() string { return "touching_posts" }

func (touchingNullableOwnerPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{Related: &touchingUnitUser{}, ForeignKey: "user_id", Touches: true},
	}
}

func TestTouchOwners_SkipsBelongsToWithNullOwnerKey(t *testing.T) {
	// A null owner key means neither the stamp nor the load runs; the compiled query has no
	// connection, so any statement attempt would fail this test.
	query := compiledQuery(t, &touchingNullableOwnerPost{}, Conditions{})

	require.NoError(t, query.touchOwners(&touchingNullableOwnerPost{ID: 1}))
}

func TestTouchOwners_SkipsBelongsToWithZeroValueOwnerKey(t *testing.T) {
	// A value-typed foreign key cannot hold NULL; its zero is the Go reading of the unset key, so
	// it stands in for Eloquent's null and neither the stamp nor the load runs. The compiled query
	// has no connection, so any statement attempt would fail this test.
	query := compiledQuery(t, &touchingUnitPost{}, Conditions{})

	require.NoError(t, query.touchOwners(&touchingUnitPost{ID: 1}))
}

func TestTouchOwners_QualifiesTheBelongsToOwnerKeyOnLoad(t *testing.T) {
	// The walk's lazy load is an ordinary relation read; the belongsTo constraint compiles with
	// the qualified owner key, oracle (illuminate/database v12.55.1, sqlite, `$post->user()->toSql()`):
	// `select * from "users" where "users"."id" = ?`.
	query := compiledQuery(t, &touchingUnitPost{}, Conditions{})

	built, ok := ormBuilderOf(query.Related(&touchingUnitPost{ID: 1, UserID: 7}, "User"))
	require.True(t, ok)

	statement, _, err := built.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select * from "touching_users" where "touching_users"."id" = ?`, statement)
}

func TestTouchOwners_LeavesModelsWithoutTouchingRelationsAlone(t *testing.T) {
	// The user declares only a plain HasMany, so the walk must stop before any statement; the
	// compiled query has no connection, so any statement attempt would fail this test.
	query := compiledQuery(t, &touchingUnitUser{}, Conditions{})

	require.NoError(t, query.touchOwners(&touchingUnitUser{ID: 1, Name: "Taylor"}))
}

func TestTouchIdentityOf_ReadsClassAndKey(t *testing.T) {
	identity := touchIdentityOf(&touchingUnitPost{ID: 7})
	require.NotNil(t, identity)

	assert.Equal(t, "7", identity.key)
	assert.Equal(t, indirectStructType(reflect.TypeOf(touchingUnitPost{})), identity.class)

	// A model without a key can never be loaded back, so it carries no walk identity.
	assert.Nil(t, touchIdentityOf(&touchingUnitPost{}))
}

// touchSuppressPost is the increment owner-touch pin's fixture: a post whose User relation is
// touched, carrying a counter column the increment moves.
type touchSuppressPost struct {
	ID     uint              `gorm:"column:id;primaryKey"`
	UserID uint              `gorm:"column:user_id"`
	Score  int               `gorm:"column:score"`
	User   *touchingUnitUser `gorm:"-"`
}

func (touchSuppressPost) TableName() string { return "touching_posts" }

func (touchSuppressPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{Related: &touchingUnitUser{}, ForeignKey: "user_id", Touches: true},
	}
}

// TestIncrementSuppressesTheOwnerTouch pins conditions.withoutTouch on the Builder::increment path
// (builder_shortcuts.go step): the step runs on a copy carrying withoutTouch, so the model-update
// branch it shares with Update does not run finishSave's touchOwners walk. Without the flag an
// increment of a post whose User relation touches would stamp the owner row - a second UPDATE. The
// round-6 verify found the suppression untested (an overlay dropping withoutTouch left
// ./database/gorm/ green); this pins it. Oracle v12.55.1, executed: Builder::increment and
// Model::increment never propagate touches.
func TestIncrementSuppressesTheOwnerTouch(t *testing.T) {
	post := &touchSuppressPost{ID: 1, UserID: 7}
	q, recorder := newRecordingRelQuery(t, post)

	_, err := q.Increment("score", 1)
	require.NoError(t, err)

	require.Equal(t, 1, recorder.count(), "an increment emits only its own UPDATE, never an owner touch")
	assert.Contains(t, recorder.at(t, 0).sql, `"score"`)
	assert.NotContains(t, recorder.at(t, 0).sql, `"touching_users"`, "the owner row must not be touched")
}
