package collection_queueable

import (
	"testing"
)

// This file ports Laravel's tests/Database/DatabaseEloquentCollectionQueueableTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestCollectionQueueable_<method without the test prefix>. The corpus pins Eloquent's
// queueable-collection serialization: Collection::getQueueableIds() must ask each entity for
// QueueableEntity::getQueueableId() instead of reading the model key, so a queued payload of ids
// stays JSON-encodable and restorable.
//
// The Go ORM has no queueable-collection API at all (no getQueueableIds and no QueueableEntity on
// models; queue/ never serializes ORM models, and support/collect exposes only ToJson), so this
// corpus group is PHP-only: the two spy tests assert Mockery call counts and the JSON test guards a
// PHP encoding failure mode that Go's encoding/json cannot produce. Every behaviour claim in the
// skip comments was measured with the oracle (.claude/laravel-oracle/oracle.php against
// illuminate/database v12.55.1), not recalled.

// TestCollectionQueueable_SerializesPivotsEntitiesId ports
// DatabaseEloquentCollectionQueueableTest::testSerializesPivotsEntitiesId (12.x): a Mockery spy of
// Pivot sits in an Eloquent Collection, getQueueableIds() runs, and the spy must have received
// getQueueableId() exactly once. Oracle: a real Pivot with an id attribute returns that raw key
// (AsPivot::getQueueableId); with no id set it returns the composite
// "foreignKey:value:relatedKey:value" form.
func TestCollectionQueueable_SerializesPivotsEntitiesId(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts, through a Mockery call-count spy on Pivot::getQueueableId(), that Collection::getQueueableIds() asks each pivot entity for its queueable id exactly once — the Go ORM has no queueable-collection API to exercise")
}

// TestCollectionQueueable_SerializesModelEntitiesById ports
// DatabaseEloquentCollectionQueueableTest::testSerializesModelEntitiesById (12.x): the same spy
// shape over Model entities — getQueueableIds() must call getQueueableId() once per model.
func TestCollectionQueueable_SerializesModelEntitiesById(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts, through a Mockery call-count spy on Model::getQueueableId(), that Collection::getQueueableIds() asks each model entity for its queueable id exactly once — the Go ORM has no queueable-collection API to exercise")
}

// TestCollectionQueueable_JsonSerializationOfCollectionQueueableIdsWorks ports
// DatabaseEloquentCollectionQueueableTest::testJsonSerializationOfCollectionQueueableIdsWorks
// (12.x). Oracle: json_encode(['ids' => [random_bytes(10)]]) === false, and a real binary-keyed
// Model still yields the binary key from getQueueableIds() — and still fails to encode — so the
// corpus payload encodes only because the mock overrides getQueueableId() to 'mocked' while
// getKey() stays random_bytes(10): the test pins the call path, not a real-world encoding outcome.
func TestCollectionQueueable_JsonSerializationOfCollectionQueueableIdsWorks(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts that Collection::getQueueableIds() routes ids through QueueableEntity::getQueueableId() instead of the raw model key, so json_encode() of the id payload stays encodable for entities whose key is binary — the Go ORM has no queueable-collection API to drive, and json_encode()'s false-on-non-UTF-8 failure mode has no counterpart in Go's encoding/json")
}
