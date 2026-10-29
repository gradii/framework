package scan

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sourceScanner struct {
	source any
	err    error
}

func (s *sourceScanner) Scan(value any) error { s.source = value; return s.err }

func TestAssignPreservesScannerInputAndErrors(t *testing.T) {
	for _, source := range []any{nil, "text", []byte("text"), int64(3), true} {
		target := sourceScanner{}
		require.NoError(t, Assign(reflect.ValueOf(&target).Elem(), source))
		require.Equal(t, source, target.source)
		target.err = assert.AnError
		require.ErrorIs(t, Assign(reflect.ValueOf(&target).Elem(), source), assert.AnError)
	}
}

func TestAssignPointerNullAndAllocation(t *testing.T) {
	var target *sourceScanner
	require.NoError(t, Assign(reflect.ValueOf(&target).Elem(), "text"))
	require.Equal(t, "text", target.source)
	require.NoError(t, Assign(reflect.ValueOf(&target).Elem(), nil))
	require.Nil(t, target)
}

func TestAssignNativeTimeAcceptsSQLAndRFC3339Text(t *testing.T) {
	want := time.Date(2001, 2, 3, 4, 5, 6, 123456789, time.UTC)
	for _, source := range []any{"2001-02-03 04:05:06.123456789", []byte("2001-02-03 04:05:06.123456789"), "2001-02-03T06:05:06.123456789+02:00"} {
		var value time.Time
		require.NoError(t, Assign(reflect.ValueOf(&value).Elem(), source))
		require.True(t, value.Equal(want))
		var pointer *time.Time
		require.NoError(t, Assign(reflect.ValueOf(&pointer).Elem(), source))
		require.True(t, pointer.Equal(want))
	}
	for _, source := range []any{"", "invalid", []byte("not a time")} {
		value := want
		require.Error(t, Assign(reflect.ValueOf(&value).Elem(), source))
		require.Equal(t, want, value)
	}
}
