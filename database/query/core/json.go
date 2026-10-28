package core

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"reflect"
)

// NormalizeJSONValue prepares an explicit JSON operation. A Valuer's valid JSON text denotes a
// document; ordinary unquoted SQL text denotes a string scalar. Types that require valid JSON must
// validate it in Value (as database/types.JSON does). Valuer errors are always preserved. Ordinary
// whole-column bindings retain the original Valuer and do not use this document preparation.
func NormalizeJSONValue(value any) (any, error) {
	return normalizeJSONValue(value, false)
}

// NormalizeJSONMembershipValue leaves an ordinary collection for the membership dialect to inspect
// one scalar at a time. Each element's Valuer is evaluated before its shape is checked.
func NormalizeJSONMembershipValue(value any) (any, error) {
	return normalizeJSONValue(value, true)
}

func normalizeJSONValue(value any, membership bool) (any, error) {
	value = CastBinding(value)
	if IsExpression(value) {
		return value, nil
	}
	if _, ok := value.(driver.Valuer); ok {
		converted, err := driver.DefaultParameterConverter.ConvertValue(value)
		if err != nil {
			return nil, err
		}
		var data []byte
		switch v := converted.(type) {
		case string:
			data = []byte(v)
		case []byte:
			data = v
		default:
			return normalizeJSONValue(converted, membership)
		}
		if !json.Valid(data) {
			return string(data), nil
		}
		return decodeJSONValue(data)
	}
	if membership && IsArrayValue(value) {
		if _, marshaler := value.(json.Marshaler); !marshaler {
			// Membership dialects validate one scalar per element, including each element's Valuer.
			// Marshaling the entire list here would inspect a Valuer's implementation fields instead.
			return value, nil
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	if _, ok := value.(json.Marshaler); ok {
		return decodeJSONValue(encoded)
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return nil, nil
		}
		element := reflected.Elem()
		if _, number := element.Interface().(json.Number); !number {
			switch element.Kind() {
			case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				// Native scalar pointers already have the right database/sql conversion. Keep their
				// binding identity; bool and document pointers need the JSON grammar's type dispatch.
				return value, nil
			}
		}
		return normalizeJSONValue(element.Interface(), membership)
	}
	if reflected.Kind() == reflect.Slice && reflected.Type().Elem().Kind() == reflect.Uint8 {
		return string(reflected.Bytes()), nil
	}
	if reflected.Kind() == reflect.Struct {
		return decodeJSONValue(encoded)
	}
	if reflected.Kind() == reflect.Bool {
		return reflected.Bool(), nil
	}
	return value, nil
}

func decodeJSONValue(data []byte) (any, error) {
	// Validate the entire document, including trailing data, before Decode.
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
