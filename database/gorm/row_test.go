package gorm

import (
	"database/sql"
	"database/sql/driver"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/goravel/framework/database/db"
	"github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
	basedatabase "github.com/goravel/framework/support/database"
)

// TestRow_Err returns the stored error from the Row.
func TestRow_Err_Nil(t *testing.T) {
	r := &Row{err: nil}
	assert.NoError(t, r.Err())
}

func TestRow_Err_NonNil(t *testing.T) {
	expected := assert.AnError
	r := &Row{err: expected}
	assert.Same(t, expected, r.Err())
}

func TestRowScanUsesGormTags(t *testing.T) {
	type User struct {
		ID      uint   `gorm:"primaryKey" db:"user_id"`
		Name    string `gorm:"column:full_name;size:100" db:"display_name" mapstructure:"other_name"`
		Alias   string `gorm:"index"`
		Ignored string `gorm:"-"`
	}
	row := &Row{
		query: &Query{},
		row: map[string]any{
			"id":           int64(1),
			"user_id":      int64(99),
			"full_name":    "ORM name",
			"name":         "unmapped name",
			"display_name": "DB name",
			"other_name":   "mapstructure name",
			"alias":        "alias",
			"ignored":      "unmapped value",
		},
	}

	user := User{Ignored: "keep"}
	assert.NoError(t, row.Scan(&user))
	assert.Equal(t, User{ID: 1, Name: "ORM name", Alias: "alias", Ignored: "keep"}, user)
}

func TestCursorRowsUseGormTags(t *testing.T) {
	type User struct {
		ID   uint   `gorm:"column:record_id;primaryKey" db:"user_id"`
		Name string `gorm:"column:full_name" db:"display_name"`
	}
	rows := []map[string]any{{
		"record_id":    int64(1),
		"user_id":      int64(99),
		"full_name":    "ORM name",
		"display_name": "DB name",
	}}

	var users []User
	assert.NoError(t, assignCursorRows(&users, rows))
	assert.Equal(t, []User{{ID: 1, Name: "ORM name"}}, users)

	var pointers []*User
	assert.NoError(t, assignCursorRows(&pointers, rows))
	assert.Equal(t, []*User{{ID: 1, Name: "ORM name"}}, pointers)
}

func TestQueryReadsUseGormTags(t *testing.T) {
	type User struct {
		ID      uint   `gorm:"column:record_id;primaryKey" db:"user_id"`
		Name    string `gorm:"column:full_name;size:100" db:"display_name"`
		Alias   string `gorm:"index"`
		Ignored string `gorm:"-"`
	}
	for _, method := range []string{"first", "find", "find pointers", "scan", "cursor", "cursor paginate"} {
		t.Run(method, func(t *testing.T) {
			query, recorder := newCursorQuery(t, Conditions{})
			recorder.returnRows("select", []string{"record_id", "full_name", "alias", "ignored"},
				[]driver.Value{int64(1), []byte("Alice"), "a", "ignore"},
				[]driver.Value{int64(2), "Bob", "b", "ignore"})
			q := query.Table("users")
			var users []User
			switch method {
			case "first":
				var user User
				require.NoError(t, q.First(&user))
				assert.Equal(t, User{ID: 1, Name: "Alice", Alias: "a"}, user)
				return
			case "find":
				require.NoError(t, q.Find(&users))
			case "find pointers":
				var pointers []*User
				require.NoError(t, q.Find(&pointers))
				for _, user := range pointers {
					require.NotNil(t, user)
					users = append(users, *user)
				}
			case "scan":
				require.NoError(t, q.Scan(&users))
			case "cursor":
				for row := range q.Cursor() {
					var user User
					require.NoError(t, row.Scan(&user))
					users = append(users, user)
				}
			case "cursor paginate":
				_, err := q.OrderBy("record_id").CursorPaginate(2, "", &users)
				require.NoError(t, err)
			}
			assert.Equal(t, []User{{ID: 1, Name: "Alice", Alias: "a"}, {ID: 2, Name: "Bob", Alias: "b"}}, users)
		})
	}
}

func TestRowScanEmbeddedFieldsAndNulls(t *testing.T) {
	type Identity struct {
		ID uint `gorm:"column:record_id;primaryKey"`
	}
	type Timestamps struct {
		CreatedAt *carbon.DateTime
		UpdatedAt time.Time
	}
	type User struct {
		*Identity
		*Timestamps
		Name      string
		Nickname  sql.NullString
		DeletedAt basedatabase.DeletedAt
	}
	var user *User
	now := time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)
	row := &Row{row: map[string]any{
		"record_id": int64(7), "name": "Alice", "nickname": []byte("Al"),
		"created_at": now, "updated_at": now.Format(time.RFC3339), "deleted_at": now,
	}}
	require.NoError(t, row.Scan(&user))
	require.NotNil(t, user.Identity)
	require.NotNil(t, user.Timestamps)
	require.NotNil(t, user.CreatedAt)
	assert.Equal(t, uint(7), user.ID)
	assert.True(t, now.Equal(user.CreatedAt.StdTime()))
	assert.Equal(t, now, user.UpdatedAt)
	assert.Equal(t, sql.NullString{String: "Al", Valid: true}, user.Nickname)
	assert.True(t, user.DeletedAt.Valid)

	row = &Row{row: map[string]any{"created_at": nil, "updated_at": nil, "nickname": nil, "deleted_at": nil}}
	require.NoError(t, row.Scan(&user))
	assert.Equal(t, "Alice", user.Name)
	assert.Nil(t, user.CreatedAt)
	assert.True(t, user.UpdatedAt.IsZero())
	assert.False(t, user.Nickname.Valid)
	assert.False(t, user.DeletedAt.Valid)
}

func TestRowMappersKeepDBAndGormTagsSeparate(t *testing.T) {
	type User struct {
		Name string `gorm:"column:full_name" db:"display_name"`
	}
	values := map[string]any{"full_name": "ORM name", "display_name": "DB name"}
	var user User
	for range 2 {
		require.NoError(t, db.NewRow(values, nil).Scan(&user))
		assert.Equal(t, "DB name", user.Name)
		require.NoError(t, (&Row{row: values}).Scan(&user))
		assert.Equal(t, "ORM name", user.Name)
	}
}

type rowFailingScanner string

func (*rowFailingScanner) Scan(any) error { return assert.AnError }

type rowFailingBytes []byte

func (*rowFailingBytes) Scan(any) error { return assert.AnError }

func TestRowScanPropagatesScannerErrors(t *testing.T) {
	for _, source := range []any{"fail", []byte("fail")} {
		for _, dest := range []any{
			&struct{ Value rowFailingScanner }{}, &struct{ Value *rowFailingScanner }{},
			&struct{ Value rowFailingBytes }{}, &struct{ Value *rowFailingBytes }{},
		} {
			err := (&Row{row: map[string]any{"value": source}}).Scan(dest)
			assert.ErrorIs(t, err, assert.AnError)
		}
	}

	query, recorder := newCursorQuery(t, Conditions{})
	recorder.returnRows("select", []string{"value"}, []driver.Value{[]byte("fail")})
	var result struct{ Value rowFailingBytes }
	assert.ErrorIs(t, query.Table("users").Scan(&result), assert.AnError)
}

func TestRowScanExportedFieldsInPrivateEmbeds(t *testing.T) {
	type identity struct {
		ID uint `gorm:"column:record_id"`
	}
	t.Run("value", func(t *testing.T) {
		var model struct{ identity }
		require.NoError(t, (&Row{row: map[string]any{"record_id": int64(7)}}).Scan(&model))
		assert.Equal(t, uint(7), model.ID)
	})
	t.Run("initialized pointer", func(t *testing.T) {
		model := struct{ *identity }{identity: &identity{}}
		require.NoError(t, (&Row{row: map[string]any{"record_id": int64(7)}}).Scan(&model))
		assert.Equal(t, uint(7), model.ID)
	})
	t.Run("nil pointer", func(t *testing.T) {
		var model struct{ *identity }
		var err error
		require.NotPanics(t, func() {
			err = (&Row{row: map[string]any{"record_id": int64(7)}}).Scan(&model)
		})
		assert.ErrorIs(t, err, errors.DatabaseFieldNotSettable)
	})
}

func TestRowScanNestedPointerPaths(t *testing.T) {
	type Address struct{ City string }
	var model struct{ Address **Address }
	var err error
	require.NotPanics(t, func() {
		err = (&Row{row: map[string]any{"address.city": "Shanghai"}}).Scan(&model)
	})
	require.NoError(t, err)
	require.NotNil(t, model.Address)
	require.NotNil(t, *model.Address)
	assert.Equal(t, "Shanghai", (*model.Address).City)
}

func TestRowScanRejectsInvalidDestinations(t *testing.T) {
	type User struct{ ID uint }
	for _, dest := range []any{nil, User{}, (*User)(nil), new(int), &map[int]any{}} {
		err := (&Row{row: map[string]any{"id": int64(1)}}).Scan(dest)
		assert.ErrorIs(t, err, errors.DatabaseUnsupportedType)
	}
}

func TestRowScanRejectsLossyConversions(t *testing.T) {
	cases := []struct {
		name   string
		source any
		dest   any
	}{
		{"unsigned overflow", int64(256), &struct{ Value uint8 }{}},
		{"negative unsigned", int64(-1), &struct{ Value uint }{}},
		{"signed overflow", uint64(math.MaxUint64), &struct{ Value int64 }{}},
		{"fractional integer", 1.5, &struct{ Value int }{}},
		{"float integer boundary", float64(math.MaxInt64), &struct{ Value int64 }{}},
		{"float unsigned boundary", float64(math.MaxUint64), &struct{ Value uint64 }{}},
		{"float overflow", math.MaxFloat64, &struct{ Value float32 }{}},
		{"integer to text", int64(42), &struct{ Value string }{}},
		{"invalid integer text", []byte("oops"), &struct{ Value int }{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Row{row: map[string]any{"value": tc.source}}).Scan(tc.dest)
			assert.ErrorIs(t, err, errors.OrmRowScanValueNotRepresentable)
		})
	}
}

func TestRowScanKeepsLargeIntegralFloats(t *testing.T) {
	var result struct {
		Signed   int64
		Unsigned uint64
	}
	number := math.Nextafter(float64(math.MaxInt64), 0)
	require.NoError(t, (&Row{row: map[string]any{"signed": number, "unsigned": number}}).Scan(&result))
	assert.Equal(t, int64(number), result.Signed)
	assert.Equal(t, uint64(number), result.Unsigned)
}

func TestQueryScansMapDestinations(t *testing.T) {
	query, recorder := newCursorQuery(t, Conditions{})
	recorder.returnRows("select", []string{"record_id", "name", "deleted_at"},
		[]driver.Value{int64(7), "Alice", nil})
	var rows []map[string]any
	require.NoError(t, query.Table("users").Find(&rows))
	assert.Equal(t, []map[string]any{{"record_id": int64(7), "name": "Alice", "deleted_at": nil}}, rows)
}
