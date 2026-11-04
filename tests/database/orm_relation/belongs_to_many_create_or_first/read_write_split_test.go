package belongs_to_many_create_or_first

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file closes the last pin the round-11 gate left open (candidate 39): the branch of
// createOrFirstMany2Many that only a genuine read/write divergence reaches.
//
// BelongsToMany::createOrFirst (vendor v12.55.1, BelongsToMany.php:681-695) has three legs:
//
//  1. try { withSavepointIfNeeded(fn () => $this->create(...)) } — the related insert plus the
//     pivot attach;
//  2. catch (UniqueConstraintViolationException) { return $this->related->where($attributes)->first()
//     ?? throw $e ... } — the *plain* read-back, on the connection's read PDO;
//  3. catch (UniqueConstraintViolationException) { return (clone $this)->useWritePdo()
//     ->where($attributes)->first() ?? throw $e; } — the WRITE-PDO pivot-joined relookup, reached
//     when leg 2's plain read-back missed (or when leg 2's attach tripped the pivot's own unique
//     index).
//
// The port encodes leg 3 at database/gorm/relation_writes.go:581 (the read-back-miss arm, reachable
// only when the plain read-back at :575 misses) and again at :620 (the attach-violation arm). Leg 3
// is reachable in production when a concurrent writer commits the winning row between the read
// attempt and the write attempt — and it is only *pinnable* when a read and a write can disagree.
// The corpus harness opens a single sqlite file whose reader pool falls back to the writer, so the
// plain read always sees the writer's own row and leg 3's read-back-miss arm can never be entered;
// the round-11 verify recorded exactly that as the one remaining unpinned behaviour. This file adds
// the read/write-split fixture (newSplitRelationQuery, harness_test.go) that makes the divergence
// buildable, then pins the arm end to end.
//
// ORACLE (fresh, executed 2026-11-05: /tmp/r12/rw_split_probe.php — illuminate/database v12.55.1,
// Capsule configured with distinct 'write'/'read' sqlite database paths, real files). The writer
// holds the winning related row 456 (attr="foo", val="bar") and its pivot row (123, 456); the reader
// is a stale replica carrying only the schema. Executed output:
//
//	distinctPdos=true
//	plainRead.miss=true                                      the relation's where(attrs)->first() on
//	                                                         the read PDO sees nothing
//	writePdoRead.id=456                                      useWritePdo()->where(attrs)->first()
//	                                                         finds the winner on the write PDO
//	createOrFirst.id=456  wasRecentlyCreated=false           leg 3 returned the winner, no rethrow
//	createOrFirst.val='bar'                                  $values are NOT applied on the read-back
//	createOrFirst.pivot={"source_id":123,"related_id":456}   the write-PDO relookup hydrates the pivot
//	relatedRows.write=1  relatedRows.read=0  pivotRows.write=0  the violation path wrote nothing
//
// NON-VACUITY: with the split in place, mutating leg 3's routing so the relookup reads on the reader
// instead of the writer (database/gorm/relation_writes.go:581, the `true` writePdo argument -> false)
// makes the relookup miss too, so the arm re-raises OrmUniqueConstraintViolation and this test goes
// red on the require.NoError. Removing the relookup outright does the same. Proven red-before-green
// via go test -overlay (see the round-12 report).
func TestBelongsToManyCreateOrFirst_WritePdoRelookupReadsPastAStaleReader(t *testing.T) {
	dir := t.TempDir()
	writeDsn := "file:" + filepath.ToSlash(filepath.Join(dir, "write.db")) + "?_pragma=foreign_keys(1)"
	readDsn := "file:" + filepath.ToSlash(filepath.Join(dir, "read.db")) + "?_pragma=foreign_keys(1)"

	split := newSplitRelationQuery(t, writeDsn, readDsn)

	// Schema in BOTH files; the winning rows only in the writer. The reader is a stale replica: its
	// related_table and pivot_table are empty, so a read routed here returns nothing.
	belongsToManyCreateOrFirstSchema(t, split.query)
	for _, statement := range belongsToManyCreateOrFirstSchemaStatements() {
		_, err := split.reader.ExecContext(context.Background(), statement)
		require.NoError(t, err, statement)
	}

	seed := belongsToManyCreateOrFirstRelated{ID: 456, Attr: "foo", Val: "bar"}
	require.NoError(t, cloneRelationQuery(t, split.query).Create(&seed))
	source := &belongsToManyCreateOrFirstSource{ID: 123}
	require.NoError(t, cloneRelationQuery(t, split.query).Relation(source, "Related").Attach([]any{uint64(456)}))

	// Fixture self-check: a plain read through the split handle must NOT see the writer's committed
	// row. If it does, the reader pool collapsed onto the writer and this test would be vacuous.
	var stale belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, split.query).Model(&belongsToManyCreateOrFirstRelated{}).
		Where("attr", "foo").First(&stale))
	assert.Zero(t, stale.ID, "the reader must miss the writer's row — otherwise the read/write split is not in effect")

	// createOrFirst: the related insert trips related_table's unique index (:571), the plain
	// read-back runs on the READER and misses (:575), and the write-PDO relookup (:581) returns the
	// winner — over the writer (oracle: leg 3, BelongsToMany.php:694) — instead of re-raising.
	var result belongsToManyCreateOrFirstRelated
	require.NoError(t, cloneRelationQuery(t, split.query).Relation(source, "Related").CreateOrFirst(
		map[string]any{"attr": "foo"}, map[string]any{"val": "baz"}, &result))

	assert.Equal(t, uint64(456), result.ID)
	assert.Equal(t, "foo", result.Attr)
	assert.Equal(t, "bar", result.Val, "$values are not applied to the row the write read-back returns")
	assert.Equal(t, uint64(123), result.Pivot.SourceID, "the write-PDO relookup hydrates the pivot")
	assert.Equal(t, uint64(456), result.Pivot.RelatedID)

	// The violation path wrote nothing: the winner's row count is unchanged on the writer, the reader
	// stays empty, and the existing pivot row is untouched.
	assert.Equal(t, int64(1), countRows(t, split.writer, "related_table"), "no related row may be inserted on the violation path")
	assert.Equal(t, int64(0), countRows(t, split.reader, "related_table"), "the reader replica must stay empty")
	assert.Equal(t, int64(1), countRows(t, split.writer, "pivot_table"), "the existing pivot row is untouched")
	assert.Equal(t, int64(0), countRows(t, split.reader, "pivot_table"), "the reader replica must stay empty")
}
