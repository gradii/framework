package write_families

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteFamilies_DetachEmptyIDsIsANoOp pins Laravel's detach edge shapes against the oracle
// (illuminate/database v12.55.1, executed; /tmp/lalign-cleanups/detach_empty.php):
//
//	detach([])       -> 0, no statement, every pivot row left in place
//	detach(null)     -> 3, `delete from "de_post_tag" where "de_post_tag"."post_id" = ?`
//	detach([2])      -> 1, `... and "de_post_tag"."tag_id" in (?) || [1,2]`
//	detach()         -> the bare detach(null) shape
//	detachOrFail([]) -> 0, no statement, rows intact
func TestWriteFamilies_DetachEmptyIDsIsANoOp(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	post, tags := wfOrFailPost(t, query)

	relationship := wfPivotRelation(t, query, &post)
	require.NoError(t, relationship.Attach([]any{tags[0].ID, tags[1].ID}))

	removed, err := relationship.DetachOrFail([]any{}...)
	require.NoError(t, err)
	assert.EqualValues(t, 0, removed)
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))

	removed, err = relationship.Detach([]any{}...)
	require.NoError(t, err)
	assert.EqualValues(t, 0, removed)
	assert.ElementsMatch(t, []uint64{tags[0].ID, tags[1].ID}, wfAttachedTagIDs(t, query, &post, "Tags"))

	// A bare Detach() is Laravel detach(null): every row of the parent goes.
	removed, err = relationship.Detach()
	require.NoError(t, err)
	assert.EqualValues(t, 2, removed)
	assert.Empty(t, wfAttachedTagIDs(t, query, &post, "Tags"))
}
