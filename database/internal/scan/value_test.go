package scan

import (
	"reflect"
	"testing"

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
