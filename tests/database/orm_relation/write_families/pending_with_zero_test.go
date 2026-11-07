package write_families

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// Round 15b (r15b-write-edges, candidate 3): a caller's explicit false/0/null beats a pending
// WithAttributes pair. Eloquent merges $pendingAttributes UNDER the caller's attributes —
// newModelInstance's array_merge($pending, $attributes) and setForeignAttributesForCreate's
// array_key_exists loop (HasOneOrMany.php:498-504) — so any value the caller passed wins, zero or
// not. A Go struct cannot tell an unset field from a zero one; WithZero is how a caller says it
// carries the zero, and the Or* / Make entry points name their map keys the same way. The pending
// merge (applyPendingAttributes) now leaves those zeros alone.
//
// ORACLE (fresh, executed: /tmp/r15b/pending.php — illuminate/database v12.55.1, real sqlite, a
// hasMany relation with withAttributes(['flag' => 9], false)):
//
//	R1 create(['title'=>'r1','flag'=>0])                  insert ("title","flag","pa_user_id") ["r1",0,1]
//	R3 save(new PaPost(['title'=>'r3','flag'=>0]))        insert ("title","flag","pa_user_id") ["r3",0,1]
//	R4 firstOrCreate(['title'=>'r4'],['flag'=>0]) miss    insert ("title","flag","pa_user_id") ["r4",0,1]
//	R5 createOrFirst(['title'=>'r5'],['flag'=>0])         insert ("title","flag","pa_user_id") ["r5",0,1]
//	R6 updateOrCreate(['title'=>'r6'],['flag'=>0]) miss   insert ("title","flag","pa_user_id") ["r6",0,1]
//	R7 firstOrNew(['title'=>'r7'],['flag'=>0]) miss       {"title":"r7","flag":0,"pa_user_id":1}, no write
//	R8 make(['title'=>'r8','flag'=>0])                    {"title":"r8","flag":0,"pa_user_id":1}, no write
//	P7 PaPost::withAttributes(['flag'=>9],false)->firstOrCreate(['title'=>'m7'],['flag'=>0])
//	                                                      insert ("flag","title") [0,"m7"]
//	P1 ...->create(['title'=>'a','flag'=>false])          insert ("flag","title") [false,"a"]
//	P4 / R10, no caller flag                              the pending 9 lands
//
// Here the relation's pending pair is flag=true (wfPendingUser's OnQuery), so a leg that lost to
// the pending value would read true. Pre-change, every leg below but the baseline read true.

// wfPendingPostFlag reads a wf_pend_posts row back with a nullable flag, so a written false and an
// omitted (NULL) column are told apart.
type wfPendingPostFlag struct {
	ID    uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Title string `gorm:"column:title"`
	Flag  *bool  `gorm:"column:flag"`
}

func (wfPendingPostFlag) TableName() string { return "wf_pend_posts" }

// wfStoredFlag returns the flag stored for the post titled title; nil means NULL.
func wfStoredFlag(t *testing.T, query contractsorm.Query, title string) *bool {
	t.Helper()

	var row wfPendingPostFlag
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPendingPostFlag{}).Where("title", title).First(&row))
	require.NotZero(t, row.ID, "the post %q must have been written", title)

	return row.Flag
}

func TestWriteFamilies_CallerZeroBeatsThePendingPair(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedPendingUser(t, query, 1)

	posts := func() contractsorm.HasManyRelation {
		relation, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
		require.True(t, ok)

		return relation
	}
	isFalse := func(t *testing.T, flag *bool, msg string) {
		t.Helper()
		require.NotNil(t, flag, msg+": the flag column must be written, not left NULL")
		assert.False(t, *flag, msg)
	}

	// Baseline (P4): a zero the caller does not name takes the pending value.
	require.NoError(t, posts().Create(&wfPendingPost{Title: "baseline"}))
	baseline := wfStoredFlag(t, query, "baseline")
	require.NotNil(t, baseline)
	assert.True(t, *baseline, "an unnamed zero takes the pending pair")

	// R1: create with WithZero naming flag writes the caller's false.
	created := posts()
	created.WithZero("flag")
	require.NoError(t, created.Create(&wfPendingPost{Title: "create"}))
	isFalse(t, wfStoredFlag(t, query, "create"), "create: the WithZero-named false beats the pending true")

	// R3: save of a new instance, likewise.
	saved := posts()
	saved.WithZero("flag")
	require.NoError(t, saved.Save(&wfPendingPost{Title: "save"}))
	isFalse(t, wfStoredFlag(t, query, "save"), "save: the WithZero-named false beats the pending true")

	// R4/R5/R6: the Or* family's value map names flag, so its false is the caller's.
	var firstOrCreate wfPendingPost
	require.NoError(t, posts().FirstOrCreate(&firstOrCreate, map[string]any{"title": "foc"}, map[string]any{"flag": false}))
	assert.False(t, firstOrCreate.Flag)
	isFalse(t, wfStoredFlag(t, query, "foc"), "firstOrCreate miss: the value-map false beats the pending true")

	var createOrFirst wfPendingPost
	require.NoError(t, posts().CreateOrFirst(&createOrFirst, map[string]any{"title": "cof"}, map[string]any{"flag": false}))
	isFalse(t, wfStoredFlag(t, query, "cof"), "createOrFirst: the value-map false beats the pending true")

	var updateOrCreate wfPendingPost
	require.NoError(t, posts().UpdateOrCreate(&updateOrCreate, map[string]any{"title": "uoc"}, map[string]any{"flag": false}))
	isFalse(t, wfStoredFlag(t, query, "uoc"), "updateOrCreate miss: the value-map false beats the pending true")

	// R7/R8: the in-memory instances keep the caller's false.
	var firstOrNew wfPendingPost
	require.NoError(t, posts().FirstOrNew(&firstOrNew, map[string]any{"title": "fon"}, map[string]any{"flag": false}))
	assert.False(t, firstOrNew.Flag, "firstOrNew miss: the value-map false beats the pending true")
	assert.Equal(t, user.ID, firstOrNew.UserID)

	var made wfPendingPost
	require.NoError(t, posts().Make(&made, map[string]any{"title": "make", "flag": false}))
	assert.False(t, made.Flag, "make: the given false beats the pending true")
	assert.Equal(t, user.ID, made.UserID)

	// P7/P1: the plain builder's pending pair yields the same way.
	var plain wfPendingPost
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPendingPost{}).WithAttributes("flag", true, false).
		FirstOrCreate(&plain, map[string]any{"title": "plain-foc"}, map[string]any{"flag": false}))
	isFalse(t, wfStoredFlag(t, query, "plain-foc"), "builder firstOrCreate miss: the value-map false beats the pending true")

	require.NoError(t, cloneRelationQuery(t, query).WithAttributes("flag", true, false).WithZero("flag").
		Create(&wfPendingPost{Title: "plain-create"}))
	isFalse(t, wfStoredFlag(t, query, "plain-create"), "builder create: the WithZero-named false beats the pending true")
}
