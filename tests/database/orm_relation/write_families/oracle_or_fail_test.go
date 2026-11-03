package write_families

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file pins the OrFail write family of InteractsWithPivotTable — a 12.55-only addition, not
// in the 12.43.1 corpus checkout — against the oracle (illuminate/database v12.55.1, executed;
// /tmp/lalign_write_or_fail.php). The oracle measured, over a posts <-> tags schema:
//
//	attachOrFail                    -> begin + commit around the call, void return, pivot written
//	detachOrFail([2])               -> begin + commit, removed count 1
//	detachOrFail(null)              -> begin + commit, every row of the parent detached
//	syncOrFail([1, 3])              -> begin + commit, changes {attached: [3]}
//	syncWithoutDetachingOrFail([4]) -> begin + commit, changes {attached: [4]}
//	syncWithPivotValuesOrFail([4], {role: r}) -> begin + commit, changes {detached: [1, 3],
//	                                  updated: [4]}, role written
//	updateExistingPivotOrFail       -> begin + commit, updated count 1
//	toggleOrFail([3, 4])            -> begin + commit, changes {attached: [3], detached: [4]}
//	syncOrFail with a failing attach -> begin + rollback, the previously attached rows restored
//	                                  ([1, 2]); the same failure through the plain sync commits
//	                                  the detach ([] left)
//
// The statement order behind that last pair is pinned by a second executed probe
// (/tmp/lalign_synccore_order_probe.php, same oracle, real sqlite): sync, syncWithoutDetaching,
// toggle and syncWithPivotValues all read the current pivot ids, DELETE the ids to detach, then
// UPDATE / INSERT the rest (InteractsWithPivotTable::sync :123 detach, :133 attachNew; ::toggle
// :39 detach, :50 attach) — so a failing attach lands after the detach has run, and a refused
// detach aborts before any attach. The port's sync cores run the same order.
//
// The transaction pair itself is not directly observable from Go; the rollback is asserted
// through its database effect, exactly as the oracle shows it.

func wfOrFailPost(t *testing.T, query contractsorm.Query) (wfPost, []wfTag) {
	t.Helper()

	user := wfSeedUser(t, query, 1)
	post := wfSeedPost(t, query, user.ID)
	tags := wfSeedTags(t, query, "t1", "t2", "t3", "t4")

	return post, tags
}

func wfPivotRelation(t *testing.T, query contractsorm.Query, post *wfPost) contractsorm.Many2ManyRelation {
	t.Helper()

	relationship, ok := cloneRelationQuery(t, query).Related(post, "Tags").(contractsorm.Many2ManyRelation)
	require.True(t, ok)

	return relationship
}

func wfStrictRelation(t *testing.T, query contractsorm.Query, post *wfPost) contractsorm.Many2ManyRelation {
	t.Helper()

	relationship, ok := cloneRelationQuery(t, query).Related(post, "StrictTags").(contractsorm.Many2ManyRelation)
	require.True(t, ok)

	return relationship
}

func TestWriteFamiliesOrFail_AttachOrFailWritesPivot(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.AttachOrFail([]any{tags[0].ID}))

	assert.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
}

func TestWriteFamiliesOrFail_DetachOrFailRemovesAndReturnsCount(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID, tags[1].ID}))

	removed, err := relationship.DetachOrFail(tags[0].ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)
	assert.Equal(t, []uint64{tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
}

func TestWriteFamiliesOrFail_DetachOrFailNullRemovesEveryRow(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID, tags[1].ID}))

	removed, err := relationship.DetachOrFail()
	require.NoError(t, err)
	assert.Equal(t, int64(2), removed)
	assert.Empty(t, wfAttachedTagIDs(t, query, &post, "Tags"))
}

func TestWriteFamiliesOrFail_SyncOrFailReturnsChanges(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID}))

	changes, err := relationship.SyncOrFail([]any{tags[0].ID, tags[2].ID})
	require.NoError(t, err)
	assert.Len(t, changes.Attached, 1)
	assert.Empty(t, changes.Detached)
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[2].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
}

func TestWriteFamiliesOrFail_SyncWithoutDetachingOrFailAddsOnly(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID}))

	changes, err := relationship.SyncWithoutDetachingOrFail([]any{tags[1].ID})
	require.NoError(t, err)
	assert.Len(t, changes.Attached, 1)
	assert.Empty(t, changes.Detached)
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
}

func TestWriteFamiliesOrFail_SyncWithPivotValuesOrFailUpdatesExisting(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.AttachWithPivot(map[any]map[string]any{
		tags[0].ID: {"role": "r1"},
		tags[1].ID: {"role": "r1"},
	}))

	changes, err := relationship.SyncWithPivotValuesOrFail([]any{tags[1].ID}, map[string]any{"role": "r2"})
	require.NoError(t, err)
	assert.Len(t, changes.Detached, 1)
	assert.Len(t, changes.Updated, 1)
	assert.Equal(t, []uint64{tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))

	row := wfPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPivotRow{}).
		Where("post_id", post.ID).Where("tag_id", tags[1].ID).First(&row))
	assert.Equal(t, "r2", row.Role)
}

func TestWriteFamiliesOrFail_UpdateExistingPivotOrFailReturnsUpdatedCount(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.AttachWithPivot(map[any]map[string]any{tags[0].ID: {"role": "r1"}}))

	updated, err := relationship.UpdateExistingPivotOrFail(tags[0].ID, map[string]any{"role": "r2"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), updated)

	row := wfPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPivotRow{}).
		Where("post_id", post.ID).Where("tag_id", tags[0].ID).First(&row))
	assert.Equal(t, "r2", row.Role)
}

func TestWriteFamiliesOrFail_ToggleOrFailAttachesAndDetaches(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID}))

	changes, err := relationship.ToggleOrFail([]any{tags[0].ID, tags[1].ID})
	require.NoError(t, err)
	assert.Len(t, changes.Attached, 1)
	assert.Len(t, changes.Detached, 1)
	assert.Equal(t, []uint64{tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
}

// Oracle 8 / 8b, and the statement-order probe (/tmp/lalign_synccore_order_probe.php, same
// oracle, real sqlite): sync detaches before it attaches — read the current ids, DELETE, then
// INSERT. With the strict table's NOT NULL note the attach is the statement that fails, so the
// detach has already run: syncOrFail rolls it back and the previously attached rows survive
// ([1, 2]); the plain sync has committed the detach and leaves nothing ([]).
func TestWriteFamiliesOrFail_SyncOrFailAbortsOnFailingAttach(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	_, err := cloneRelationQuery(t, query).Exec(fmt.Sprintf(
		`insert into wf_post_strict (post_id, tag_id, note) values (%d, %d, 'n')`, post.ID, tags[0].ID))
	require.NoError(t, err)
	_, err = cloneRelationQuery(t, query).Exec(fmt.Sprintf(
		`insert into wf_post_strict (post_id, tag_id, note) values (%d, %d, 'n')`, post.ID, tags[1].ID))
	require.NoError(t, err)

	strict := wfStrictRelation(t, query, &post)

	_, err = strict.SyncOrFail([]any{tags[2].ID})
	require.Error(t, err, "the note-less attach violates NOT NULL")
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[1].ID}, wfAttachedTagIDs(t, query, &post, "StrictTags"),
		"the rollback restores the rows the detach removed")

	changes, err := strict.Sync([]any{tags[2].ID})
	require.Error(t, err, "the same attach fails outside a transaction")
	assert.Nil(t, changes)
	assert.Empty(t, wfAttachedTagIDs(t, query, &post, "StrictTags"),
		"the plain sync committed the detach before the attach failed (oracle 8b: [])")
}

// The order is observable through the guard trigger that refuses every delete: toggle detaches
// before it attaches, so a refused detach aborts the call before any attach runs and both the
// plain toggle and toggleOrFail leave the row set untouched (oracle: toggle([1, 2]) from [1] ->
// QueryException, [1]); a toggle that only attaches never reaches the trigger (oracle:
// toggleOrFail([2]) from [1] -> begin + commit, [1, 2]).
func TestWriteFamiliesOrFail_ToggleDetachesBeforeItAttaches(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	_, err := cloneRelationQuery(t, query).Exec(fmt.Sprintf(
		`insert into wf_post_guard (post_id, tag_id) values (%d, %d)`, post.ID, tags[0].ID))
	require.NoError(t, err)

	guard, ok := cloneRelationQuery(t, query).Related(&post, "GuardedTags").(contractsorm.Many2ManyRelation)
	require.True(t, ok)

	changes, err := guard.Toggle([]any{tags[0].ID, tags[1].ID})
	require.Error(t, err, "the guard trigger refuses the detach")
	assert.Nil(t, changes)
	assert.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "GuardedTags"),
		"the detach runs first, so the attach never happened")

	_, err = guard.ToggleOrFail([]any{tags[0].ID, tags[1].ID})
	require.Error(t, err, "the guard trigger refuses the detach inside the transaction too")
	assert.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "GuardedTags"))

	changes, err = guard.ToggleOrFail([]any{tags[1].ID})
	require.NoError(t, err, "an attach-only toggle never reaches the delete trigger")
	assert.Len(t, changes.Attached, 1)
	assert.Empty(t, changes.Detached)
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[1].ID}, wfAttachedTagIDs(t, query, &post, "GuardedTags"))
}

// The nested case: inside an outer transaction the OrFail wrapper rides a savepoint, so a failing
// attach rolls back to it and the outer transaction stays usable and committable.
func TestWriteFamiliesOrFail_AttachOrFailInsideTransactionUsesSavepoint(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	_, err := cloneRelationQuery(t, query).Exec(fmt.Sprintf(
		`insert into wf_post_strict (post_id, tag_id, note) values (%d, %d, 'n')`, post.ID, tags[0].ID))
	require.NoError(t, err)

	tx, err := cloneRelationQuery(t, query).BeginTransaction()
	require.NoError(t, err)

	pivot, ok := tx.Related(&post, "Tags").(contractsorm.Many2ManyRelation)
	require.True(t, ok)
	require.NoError(t, pivot.AttachOrFail([]any{tags[0].ID}))

	strict, ok := tx.Related(&post, "StrictTags").(contractsorm.Many2ManyRelation)
	require.True(t, ok)
	require.Error(t, strict.AttachOrFail([]any{tags[1].ID}), "the note-less attach fails")

	require.NoError(t, pivot.AttachOrFail([]any{tags[2].ID}))
	require.NoError(t, tx.Commit())

	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[2].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
	assert.ElementsMatch(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "StrictTags"))
}
