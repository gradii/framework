package reflectx

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapperCachesFieldsAcrossConcurrentDestinations(t *testing.T) {
	type User struct {
		ID int `db:"record_id"`
	}
	var parses atomic.Int32
	mapper := NewMapperTagFunc("db", strings.ToLower, func(tag string) string {
		parses.Add(1)
		return tag
	})
	t.Cleanup(func() { assert.Equal(t, int32(1), parses.Load()) })
	for id := range 32 {
		t.Run(strconv.Itoa(id), func(t *testing.T) {
			t.Parallel()
			user := User{ID: id}
			value := mapper.FieldByName(reflect.ValueOf(&user), "record_id")
			assert.Equal(t, int64(id), value.Int())
			value.SetInt(int64(id + 1))
			assert.Equal(t, id+1, user.ID)
			assert.Same(t, mapper.TypeMap(reflect.TypeOf(user)), mapper.TypeMap(reflect.TypeOf(&user)))
		})
	}
}

func TestMapperStopsRecursiveFields(t *testing.T) {
	type Node struct {
		ID     int   `db:"id"`
		Parent *Node `db:"parent"`
	}
	mapper := NewMapper("db")
	fields := mapper.TypeMap(reflect.TypeFor[Node]())
	assert.Contains(t, fields.Names, "id")
	assert.Contains(t, fields.Names, "parent")
	assert.Len(t, fields.Names, 2)
}

func TestMapperKeepsEmbeddedScannersAsColumns(t *testing.T) {
	type Record struct {
		*sql.NullString `db:"name"`
	}
	mapper := NewMapper("db")
	record := Record{}
	value := mapper.FieldByName(reflect.ValueOf(&record), "name")
	require.True(t, value.IsValid())
	require.NotNil(t, record.NullString)
	require.NoError(t, value.Interface().(sql.Scanner).Scan("Alice"))
	assert.Equal(t, "Alice", record.String)
	assert.True(t, record.Valid)
}

func TestMapperTraversesPointerChains(t *testing.T) {
	type Details struct{ Name string }
	var record struct{ Details **Details }
	mapper := NewMapperFunc("", strings.ToLower)
	value := reflect.ValueOf(&record).Elem()
	indexes := mapper.TypeMap(value.Type()).Names["details.name"].Index
	assert.Empty(t, mapper.ValidFieldMap(value))
	assert.False(t, FieldByIndexesReadOnly(value, indexes).IsValid())
	assert.Nil(t, record.Details)

	field := mapper.FieldByName(value, "details.name")
	require.True(t, field.CanSet())
	field.SetString("Alice")
	require.NotNil(t, record.Details)
	require.NotNil(t, *record.Details)
	assert.Equal(t, "Alice", (*record.Details).Name)
	assert.Equal(t, "Alice", mapper.ValidFieldMap(value)["details.name"].String())
	assert.Equal(t, "Alice", FieldByIndexesReadOnly(value, indexes).String())
}
