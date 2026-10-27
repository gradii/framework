package orm

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDeletedAtScanAndValue(t *testing.T) {
	now := time.Now()

	t.Run("scan a time", func(t *testing.T) {
		var deletedAt DeletedAt

		assert.NoError(t, deletedAt.Scan(now))
		assert.True(t, deletedAt.Valid)
		assert.Equal(t, now, deletedAt.Time)
	})

	t.Run("scan nil leaves it unset", func(t *testing.T) {
		deletedAt := DeletedAt{Time: now, Valid: true}

		assert.NoError(t, deletedAt.Scan(nil))
		assert.False(t, deletedAt.Valid)
	})

	t.Run("value is the time when set", func(t *testing.T) {
		value, err := DeletedAt{Time: now, Valid: true}.Value()

		assert.NoError(t, err)
		assert.Equal(t, now, value)
	})

	t.Run("value is NULL when unset", func(t *testing.T) {
		value, err := DeletedAt{}.Value()

		assert.NoError(t, err)
		assert.Nil(t, value)
	})
}

func TestDeletedAtJSON(t *testing.T) {
	now := time.Now()

	t.Run("marshal a set value", func(t *testing.T) {
		data, err := json.Marshal(DeletedAt{Time: now, Valid: true})

		assert.NoError(t, err)
		assert.Contains(t, string(data), now.Format("2006-01-02"))
	})

	t.Run("marshal an unset value as null", func(t *testing.T) {
		data, err := json.Marshal(DeletedAt{})

		assert.NoError(t, err)
		assert.Equal(t, "null", string(data))
	})

	t.Run("unmarshal null clears it", func(t *testing.T) {
		deletedAt := DeletedAt{Time: now, Valid: true}

		assert.NoError(t, json.Unmarshal([]byte("null"), &deletedAt))
		assert.False(t, deletedAt.Valid)
	})

	t.Run("unmarshal a timestamp sets it", func(t *testing.T) {
		var deletedAt DeletedAt

		assert.NoError(t, json.Unmarshal([]byte(`"`+now.Format(time.RFC3339)+`"`), &deletedAt))
		assert.True(t, deletedAt.Valid)
	})
}

func TestDeletedAtIsZero(t *testing.T) {
	assert.True(t, DeletedAt{}.IsZero())
	assert.False(t, DeletedAt{Time: time.Now(), Valid: true}.IsZero())
}

// A DeletedAt must stay interchangeable with sql.NullTime, which is what the conversion relies on.
func TestDeletedAtLayoutMatchesNullTime(t *testing.T) {
	deletedAt := DeletedAt{Time: time.Now(), Valid: true}

	nullTime := (*sql.NullTime)(&deletedAt)

	assert.True(t, nullTime.Valid)
	assert.Equal(t, deletedAt.Time, nullTime.Time)
}
