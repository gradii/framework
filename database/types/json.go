// Package types provides database values using the standard sql.Scanner and
// driver.Valuer interfaces. It has no dependency on an ORM or SQL dialect.
package types

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/gradii/framework/errors"
)

// JSON stores a JSON document. A nil value represents SQL NULL; the bytes
// "null" represent JSON null. Empty non-nil documents are invalid JSON.
type JSON []byte

var _ driver.Valuer = JSON(nil)
var _ sql.Scanner = (*JSON)(nil)

func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	if _, err := decodeJSON(j); err != nil {
		return nil, err
	}
	return string(j), nil
}

// Scan owns its bytes and replaces the receiver only after successful decoding.
func (j *JSON) Scan(source any) error {
	data, err := jsonSource(source)
	if err != nil {
		return err
	}
	if data == nil {
		*j = nil
		return nil
	}
	decoded, err := decodeJSON(data)
	if err != nil {
		return err
	}
	*j = JSON(decoded)
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return decodeJSON(j)
}

func (j *JSON) UnmarshalJSON(data []byte) error { return j.Scan(data) }

// JSONMap stores an object. SQL NULL and JSON null both scan to a nil map;
// use JSON when their distinction must be retained. A non-nil empty map is {}.
type JSONMap map[string]any

var _ driver.Valuer = JSONMap(nil)
var _ sql.Scanner = (*JSONMap)(nil)

func (j JSONMap) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	data, err := json.Marshal(map[string]any(j))
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (j *JSONMap) Scan(source any) error {
	data, err := jsonSource(source)
	if err != nil {
		return err
	}
	if data == nil {
		*j = nil
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*j = decoded
	return nil
}

func (j *JSONMap) UnmarshalJSON(data []byte) error { return j.Scan(data) }

func jsonSource(source any) ([]byte, error) {
	switch value := source.(type) {
	case nil:
		return nil, nil
	case string:
		return []byte(value), nil
	case []byte:
		return value, nil
	default:
		return nil, errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", source), "JSON string or []byte")
	}
}

func decodeJSON(data []byte) ([]byte, error) {
	var decoded json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
