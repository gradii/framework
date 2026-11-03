package write_families

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkdb "github.com/gradii/framework/database/db"
)

// This file pins the attach family's duplicate semantics against the oracle (illuminate/database
// v12.55.1, executed, /tmp/attach_dedup_probe.php): Laravel's attach has NO pivot-existence
// pre-read — the pivot INSERT is handed every id, duplicates included — so a duplicate either
// creates a second row (no unique index) or trips the pivot's unique constraint, whose error
// propagates (attachOrFail's transaction rolls back). The oracle measured, over a posts <-> tags
// schema:
//
//	attach([1,2]) then attach([1,2]) again, no unique index -> one INSERT each, rows [1,1,2,2]
//	attach([1,1]) in one call                               -> one INSERT of two rows
//	attach([1]) on a unique pivot                           -> the single INSERT, no SELECT before it
//	attach([1]) again, unique index                         -> UniqueConstraintViolationException,
//	                                                           rows unchanged; a mixed [2,1] batch is
//	                                                           statement-atomic (rows unchanged)
//	attachOrFail([1]) duplicate                             -> UCV, begin + rollback
//	attachOrFail([2]) new                                   -> begin + commit, row written
//
// Before round 6 the port pre-read the pivot table (`select kid_id ... in (?)`) and silently
// skipped already-attached ids — a documented de-dup divergence; the tests below are the flipped
// pins, and belongs_to_many_sync_touches_parent pins the unconditional parent touch (oracle C1).

// The no-unique fixture: the second attach of the same pair inserts the pair AGAIN (oracle A1b:
// rows [1,1,2,2]) — no skip, no pre-read.
func TestWriteFamilies_AttachDuplicateWithNoUniqueIndexWritesASecondRow(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID}))
	require.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))

	require.NoError(t, relationship.Attach([]any{tags[0].ID}))
	assert.Equal(t, []uint64{tags[0].ID, tags[0].ID}, wfAttachedTagIDs(t, query, &post, "Tags"),
		"the second attach inserts the pair again (oracle A1b)")

	// A duplicate inside one id list rides the same batch (oracle A2: attach([1,1]) -> two rows).
	require.NoError(t, relationship.Attach([]any{tags[1].ID, tags[1].ID}))
	assert.Len(t, wfAttachedTagIDs(t, query, &post, "Tags"), 4)
}

// The attach is exactly one statement — the INSERT; no pivot-existence SELECT runs first
// (oracle A1a/B0: every attach log is the single INSERT).
func TestWriteFamilies_AttachIsASingleInsertWithNoExistenceRead(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	ctx := frameworkdb.EnableQueryLog(context.Background())
	wc, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	require.NoError(t, wc.WithContext(ctx).Relation(&post, "Tags").Attach([]any{tags[0].ID, tags[1].ID}))

	logs := frameworkdb.GetQueryLog(ctx)
	require.Len(t, logs, 1, "the attach runs exactly one statement — no existence read")
	assert.Contains(t, logs[0].Query, `insert into "wf_post_tag"`)
	assert.NotContains(t, logs[0].Query, "select")
}

// The unique fixture: the duplicate insert trips the pivot's unique constraint and the raw
// violation propagates — from the plain attach and from attachOrFail, whose transaction rolls
// back; a mixed new+duplicate batch is statement-atomic (oracle B0-B4).
func TestWriteFamilies_AttachDuplicateTripsTheUniqueConstraint(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	_, err := cloneRelationQuery(t, query).Exec(
		`create unique index wf_post_tag_unique on wf_post_tag (post_id, tag_id)`)
	require.NoError(t, err)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID}))

	err = relationship.Attach([]any{tags[0].ID})
	require.Error(t, err, "the duplicate insert trips the pivot's unique constraint (oracle B1)")
	assert.Contains(t, err.Error(), "UNIQUE constraint failed", "the raw constraint violation propagates")
	assert.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "Tags"),
		"the failed batch wrote nothing")

	// A mixed batch (new + duplicate) is statement-atomic (oracle B2: rows unchanged).
	err = relationship.Attach([]any{tags[1].ID, tags[0].ID})
	require.Error(t, err)
	assert.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))

	// attachOrFail propagates the violation out of its transaction and rolls back (oracle B3).
	err = relationship.AttachOrFail([]any{tags[0].ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")
	assert.Equal(t, []uint64{tags[0].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))

	// A non-duplicate attachOrFail still commits (oracle B4).
	require.NoError(t, relationship.AttachOrFail([]any{tags[1].ID}))
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))
}
