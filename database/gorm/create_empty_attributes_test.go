package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/support/carbon"
)

// keylessRecord is the Go form of EloquentTestNonIncrementing: no primary key column is declared, so
// no generated key is read back — the port's form of `$incrementing = false`.
type keylessRecord struct {
	Name string
}

func (keylessRecord) TableName() string { return "keyless_records" }

// incrementingRecord is the refuted shape's model: a sole incrementing primary key and no other
// column, so an empty attribute set filters down to a record with no columns at all.
type incrementingRecord struct {
	ID uint `gorm:"primaryKey"`
}

func (incrementingRecord) TableName() string { return "incrementing_records" }

// incrementingTimestampedRecord carries the automatic timestamps, so an empty create with
// timestamps on stamps them before the insert instead of writing a columns-less row.
type incrementingTimestampedRecord struct {
	ID        uint             `gorm:"primaryKey"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (incrementingTimestampedRecord) TableName() string { return "incrementing_timestamped_records" }

// TestCreate_EmptyAttributesOnNonIncrementingModelSkipsTheInsert pins Model::performInsert's guard
// (`if (empty($attributes)) { return true; }` for a non-incrementing model): the zero model and the
// empty attribute map both write nothing — no statement runs — where the empty map used to compile
// `insert into "keyless_records" () values ()` (probe: sqlite `near ")"` syntax error) and the zero
// model wrote a defaults row. An attribute that is carried still inserts.
func TestCreate_EmptyAttributesOnNonIncrementingModelSkipsTheInsert(t *testing.T) {
	t.Run("zero model", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &keylessRecord{})

		zero := keylessRecord{}
		require.NoError(t, query.Create(&zero))

		assert.Equal(t, 0, recorder.count(), "the guard leaves the insert out entirely")
	})

	t.Run("empty attribute map", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &keylessRecord{})

		require.NoError(t, query.Create(map[string]any{}))

		assert.Equal(t, 0, recorder.count())
	})

	t.Run("an explicit nil attribute still inserts", func(t *testing.T) {
		// Eloquent's guard tests the attribute array, not its values: create(['name' => null])
		// carries an attribute, so the insert runs — oracle: `insert into
		// "non_incrementing_users" ("name") values (?)` — and the Go map is that array itself.
		query, recorder := newRecordingRelQuery(t, &keylessRecord{})

		require.NoError(t, query.Create(map[string]any{"name": nil}))

		require.Equal(t, 1, recorder.count())
		assert.Contains(t, recorder.last(t).sql, `insert into "keyless_records" ("name")`)
		assert.Equal(t, []driver.Value{nil}, recorder.last(t).args)
	})

	t.Run("an explicit empty attribute value still inserts", func(t *testing.T) {
		// create(['name' => '']) carries an attribute too (oracle: the same insert); the zero is a
		// value the caller set, not an unset attribute.
		query, recorder := newRecordingRelQuery(t, &keylessRecord{})

		require.NoError(t, query.Create(map[string]any{"name": ""}))

		require.Equal(t, 1, recorder.count())
		assert.Contains(t, recorder.last(t).sql, `insert into "keyless_records" ("name")`)
		assert.Equal(t, []driver.Value{""}, recorder.last(t).args)
	})

	t.Run("a carried attribute still inserts", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &keylessRecord{})

		named := keylessRecord{Name: "Taylor"}
		require.NoError(t, query.Create(&named))

		require.Equal(t, 1, recorder.count())
		assert.Contains(t, recorder.last(t).sql, `insert into "keyless_records"`)
	})
}

// TestCreate_EmptyAttributesOnIncrementingModelEmitsDefaultValues pins performInsert's incrementing
// branch: the empty-attribute guard covers the non-incrementing case only, and an incrementing
// model sends the empty attribute array to insertGetId, which compiles Laravel's empty insert —
// `insert into "incrementing_records" default values` (oracle, illuminate/database v12.55.1,
// sqlite, `$timestamps = false`: create([]) compiles `insert into "users" default values` and the
// generated key is read back). The port used to compile `insert into "incrementing_records" ()
// values ()` (sqlite: near ")" syntax error).
func TestCreate_EmptyAttributesOnIncrementingModelEmitsDefaultValues(t *testing.T) {
	t.Run("zero model", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &incrementingRecord{})
		recorder.returnLastInsertID(7)

		zero := incrementingRecord{}
		require.NoError(t, query.Create(&zero))

		require.Equal(t, 1, recorder.count())
		assert.Equal(t, `insert into "incrementing_records" default values`, recorder.last(t).sql)
		assert.Empty(t, recorder.last(t).args)
		assert.Equal(t, uint(7), zero.ID, "the generated key is read back from the driver")
	})

	t.Run("empty attribute map", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &incrementingRecord{})

		require.NoError(t, query.Create(map[string]any{}))

		require.Equal(t, 1, recorder.count())
		assert.Equal(t, `insert into "incrementing_records" default values`, recorder.last(t).sql)
		assert.Empty(t, recorder.last(t).args)
	})

	t.Run("a batch of records", func(t *testing.T) {
		// Create on a slice is one create per element, so execution runs the empty insert once
		// per row. The statement the literal batch of records would compile —
		// `insert into "incrementing_records" () values (), ()` (oracle, illuminate/database
		// v12.55.1) — is what sqlite rejects with a near ")" syntax error.
		query, recorder := newRecordingRelQuery(t, &incrementingRecord{})
		recorder.returnLastInsertID(7)

		batch := []incrementingRecord{{}, {}}
		require.NoError(t, query.Create(&batch))

		require.Equal(t, 2, recorder.count(), "each record runs its own empty insert")
		assert.Equal(t, `insert into "incrementing_records" default values`, recorder.at(t, 0).sql)
		assert.Equal(t, `insert into "incrementing_records" default values`, recorder.at(t, 1).sql)
		assert.Empty(t, recorder.at(t, 0).args)
		assert.Equal(t, uint(7), batch[0].ID, "the generated key is read back for every row")
		assert.Equal(t, uint(7), batch[1].ID)
	})

	t.Run("timestamps on", func(t *testing.T) {
		query, recorder := newRecordingRelQuery(t, &incrementingTimestampedRecord{})

		stamped := incrementingTimestampedRecord{}
		require.NoError(t, query.Create(&stamped))

		// The stamping leaves the attribute array non-empty, so the insert carries the two
		// timestamp columns (oracle, `$timestamps = true`: create([]) compiles `insert into
		// "users" ("updated_at", "created_at") values (?, ?)`); the port orders record columns by
		// name, so the column set is pinned here, not Laravel's attribute order.
		require.Equal(t, 1, recorder.count())
		assert.Contains(t, recorder.last(t).sql, `insert into "incrementing_timestamped_records" (`)
		assert.Contains(t, recorder.last(t).sql, `"created_at"`)
		assert.Contains(t, recorder.last(t).sql, `"updated_at"`)
		assert.NotNil(t, stamped.CreatedAt)
		assert.NotNil(t, stamped.UpdatedAt)
	})
}
