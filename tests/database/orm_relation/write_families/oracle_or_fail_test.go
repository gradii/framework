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
//	attachOrFail duplicate          -> the pivot's unique constraint propagates; begin + rollback
//	                                  (/tmp/attach_dedup_probe.php B3, round 6)
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

// The attachNew order is observable through failure: oracle B8 (/tmp/lalign_synccore_order_probe.php,
// section B8; illuminate/database v12.55.1, real sqlite) ran a plain sync([7, 9, 8]) against a
// trigger that refuses tag 9 and left [7] — tag 7 was already inserted when tag 9 raised, and tag 8
// was never attempted. Laravel's attachNew attaches one record at a time
// (InteractsWithPivotTable.php:244, `$this->attach($id, $attributes, $touch)` inside the record
// loop), so records ahead of a failure are committed and records behind it never run. A batch
// insert of the three rows would instead abort as one statement and leave nothing. The port runs
// the same shape on seeded tag ids — 3, then the refused 2, then 4 — because the assertion reads
// through the relation join and the ids must exist as tags.
func TestWriteFamilies_SyncAttachesPerRecordBeforeAFailure(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	_, err := cloneRelationQuery(t, query).Exec(fmt.Sprintf(
		`create trigger wf_post_tag_no_two before insert on wf_post_tag when new.tag_id = %d begin select raise(abort, 'tag 2 refused'); end`, tags[1].ID))
	require.NoError(t, err)

	relationship := wfPivotRelation(t, query, &post)

	_, err = relationship.Sync([]any{tags[2].ID, tags[1].ID, tags[3].ID})
	require.Error(t, err, "the tag-2 insert is refused")

	assert.Equal(t, []uint64{tags[2].ID}, wfAttachedTagIDs(t, query, &post, "Tags"),
		"records before the failing one stay attached; the record after it is never attempted")
}

// Oracle B9 (same probe, section B9): with a trigger refusing updates of tag 2,
// syncWithPivotValues([9, 2]) raised AFTER tag 9 was inserted — the final pivot set was [2, 9] and
// tag 2 kept its old role. The insert of the new record ahead of the existing one is what proves
// the walk is per input record: the previous update-first/batch-insert shape would have failed on
// the update before any insert, leaving only [2]. The port's ids are the seeded tags:
// [tags[2], tags[1]] inserts tag 3 first, then updates tag 2.
func TestWriteFamilies_SyncWithPivotValuesInsertsBeforeAFailingUpdate(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	_, err := cloneRelationQuery(t, query).Exec(fmt.Sprintf(
		`insert into wf_post_tag (post_id, tag_id, role) values (%d, %d, 'old')`, post.ID, tags[1].ID))
	require.NoError(t, err)
	_, err = cloneRelationQuery(t, query).Exec(fmt.Sprintf(
		`create trigger wf_post_tag_no_update before update on wf_post_tag when old.tag_id = %d begin select raise(abort, 'update refused'); end`, tags[1].ID))
	require.NoError(t, err)

	relationship := wfPivotRelation(t, query, &post)

	_, err = relationship.SyncWithPivotValues([]any{tags[2].ID, tags[1].ID}, map[string]any{"role": "r"})
	require.Error(t, err, "the tag-2 update is refused")

	assert.ElementsMatch(t, []uint64{tags[1].ID, tags[2].ID}, wfAttachedTagIDs(t, query, &post, "Tags"),
		"the insert of the record ahead of the failing update is already committed")
	row := wfPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPivotRow{}).
		Where("post_id", post.ID).Where("tag_id", tags[1].ID).First(&row))
	assert.Equal(t, "old", row.Role, "the refused update left the old role in place")
}

// Oracle B3 (/tmp/attach_dedup_probe.php, round 6): a duplicate attachOrFail trips the pivot's
// unique constraint inside the transaction — begin + rollback, rows unchanged — while a
// non-duplicate call commits (B4). Before round 6 the port pre-read the pivot table and skipped
// the duplicate silently, so no violation ever surfaced.
func TestWriteFamiliesOrFail_AttachOrFailDuplicatePropagatesTheUniqueConstraint(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	_, err := cloneRelationQuery(t, query).Exec(
		`create unique index wf_post_tag_unique on wf_post_tag (post_id, tag_id)`)
	require.NoError(t, err)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID}))

	err = relationship.AttachOrFail([]any{tags[0].ID})
	require.Error(t, err, "the duplicate trips the pivot's unique constraint (oracle B3)")
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")
	assert.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "Tags"),
		"the rollback leaves the pivot row set untouched")

	require.NoError(t, relationship.AttachOrFail([]any{tags[1].ID}))
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
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
