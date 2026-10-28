package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONDocumentsAndNull(t *testing.T) {
	for _, document := range []string{`null`, `{}`, `[]`, `true`, `12345678901234567890`, `"你好🌏"`, `{"name":"O'Reilly"}`} {
		t.Run(document, func(t *testing.T) {
			for _, source := range []any{document, []byte(document)} {
				value := JSON(`{"stale":true}`)
				require.NoError(t, value.Scan(source))
				bound, err := value.Value()
				require.NoError(t, err)
				require.Equal(t, document, bound)
				encoded, err := json.Marshal(value)
				require.NoError(t, err)
				require.JSONEq(t, document, string(encoded))
			}
		})
	}
	value := JSON(`null`)
	require.NotNil(t, value)
	require.NoError(t, value.Scan(nil))
	require.Nil(t, value)
	bound, err := value.Value()
	require.NoError(t, err)
	require.Nil(t, bound)
}

func TestJSONScanOwnsBytesAndPreservesValueOnError(t *testing.T) {
	data := []byte(`{"fresh":1}`)
	var value JSON
	require.NoError(t, value.Scan(data))
	data[2] = 'x'
	require.Equal(t, JSON(`{"fresh":1}`), value)
	for _, source := range []any{"", []byte{}, `{"broken":`, `{} {}`, 7, true} {
		require.Error(t, value.Scan(source))
		require.Equal(t, JSON(`{"fresh":1}`), value)
	}
	for _, invalid := range []JSON{JSON{}, JSON(`invalid`)} {
		_, err := invalid.Value()
		require.Error(t, err)
		_, err = json.Marshal(invalid)
		require.Error(t, err)
	}
}

func TestJSONMapReplacesObjectsAndRejectsNonObjects(t *testing.T) {
	value := JSONMap{"stale": true}
	require.NoError(t, value.Scan(`{"fresh":1}`))
	require.Equal(t, JSONMap{"fresh": float64(1)}, value)
	for _, source := range []any{`[]`, `true`, `42`, `"string"`, "", `{`, 7} {
		require.Error(t, value.Scan(source))
		require.Equal(t, JSONMap{"fresh": float64(1)}, value)
	}
	for _, source := range []any{nil, `null`, []byte(`null`)} {
		require.NoError(t, value.Scan(source))
		require.Nil(t, value)
		bound, err := value.Value()
		require.NoError(t, err)
		require.Nil(t, bound)
	}
	require.NoError(t, value.Scan(`{}`))
	require.NotNil(t, value)
	bound, err := value.Value()
	require.NoError(t, err)
	require.Equal(t, "{}", bound)
	require.NoError(t, json.Unmarshal([]byte(`{"new":true}`), &value))
	require.Equal(t, JSONMap{"new": true}, value)
	_, err = (JSONMap{"bad": make(chan int)}).Value()
	require.Error(t, err)
}
