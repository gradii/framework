package scan

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"math"
	"reflect"
	"time"

	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// Assign assigns driver values directly, preserving scanners and the
// JSON and timestamp conversions supported by database reads.
func Assign(target reflect.Value, source any) error {
	hasScanner := reflect.PointerTo(target.Type()).Implements(reflect.TypeFor[sql.Scanner]())
	for source != nil {
		value := reflect.ValueOf(source)
		// A driver byte slice is assignable to a named byte slice, but the
		// destination's Scanner still owns validation and conversion.
		if value.Type().AssignableTo(target.Type()) && (value.Type() == target.Type() || !hasScanner) {
			if value.Kind() == reflect.Slice && !value.IsNil() && value.Type().Elem().Kind() == reflect.Uint8 {
				copyValue := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
				reflect.Copy(copyValue, value)
				value = copyValue
			}
			target.Set(value)

			return nil
		}
		if value.Kind() == reflect.Pointer && value.IsNil() {
			source = nil
			continue
		}
		if valuer, ok := source.(driver.Valuer); ok {
			converted, err := valuer.Value()
			if err != nil {
				return err
			}
			source = converted
			continue
		}
		if value.Kind() != reflect.Pointer {
			break
		}
		source = value.Elem().Interface()
	}

	if target.Kind() == reflect.Pointer {
		if source == nil {
			target.SetZero()

			return nil
		}
		if target.IsNil() {
			value := reflect.New(target.Type().Elem())
			if err := Assign(value.Elem(), source); err != nil {
				return err
			}
			target.Set(value)

			return nil
		}

		return Assign(target.Elem(), source)
	}

	if scanner, ok := target.Addr().Interface().(sql.Scanner); ok {
		// Text-to-time compatibility is local to nullable timestamp types. Other
		// scanners receive exactly the source type supplied by the SQL driver.
		if target.Type().ConvertibleTo(reflect.TypeFor[sql.NullTime]()) {
			var text string
			switch value := source.(type) {
			case string:
				text = value
			case []byte:
				text = string(value)
			}
			if text != "" {
				parsed := carbon.Parse(text)
				if parsed.Error != nil {
					return parsed.Error
				}
				source = parsed.StdTime()
			}
		}

		return scanner.Scan(source)
	}
	if source == nil {
		target.SetZero()

		return nil
	}

	value := reflect.ValueOf(source)
	if value.Kind() == target.Kind() && value.Type().ConvertibleTo(target.Type()) {
		target.Set(value.Convert(target.Type()))

		return nil
	}
	if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
		source = string(value.Bytes())
		value = reflect.ValueOf(source)
	}

	switch target.Kind() {
	case reflect.Bool:
		switch value.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			target.SetBool(value.Int() != 0)
			return nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			target.SetBool(value.Uint() != 0)
			return nil
		case reflect.Float32, reflect.Float64:
			target.SetBool(value.Float() != 0)
			return nil
		}
		var converted sql.Null[bool]
		if err := converted.Scan(source); err == nil {
			target.SetBool(converted.V)
			return nil
		}
	case reflect.String:
		if value.Kind() == reflect.String {
			target.SetString(value.String())
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64 {
			number := value.Float()
			if number != math.Trunc(number) || number < math.MinInt64 || number >= math.MaxInt64 || math.IsNaN(number) {
				break
			}
			source = int64(number)
		}
		var converted sql.Null[int64]
		if err := converted.Scan(source); err == nil && !target.OverflowInt(converted.V) {
			target.SetInt(converted.V)
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64 {
			number := value.Float()
			if number != math.Trunc(number) || number < 0 || number >= math.MaxUint64 || math.IsNaN(number) {
				break
			}
			source = uint64(number)
		}
		var converted sql.Null[uint64]
		if err := converted.Scan(source); err == nil && !target.OverflowUint(converted.V) {
			target.SetUint(converted.V)
			return nil
		}
	case reflect.Float32, reflect.Float64:
		var converted sql.Null[float64]
		if err := converted.Scan(source); err == nil && !target.OverflowFloat(converted.V) {
			target.SetFloat(converted.V)
			return nil
		}
	case reflect.Slice, reflect.Map:
		if value.Kind() == reflect.String {
			if target.Kind() == reflect.Slice && target.Type().Elem().Kind() == reflect.Uint8 {
				target.SetBytes([]byte(value.String()))
				return nil
			}
			if value.Len() == 0 {
				if target.Kind() == reflect.Map {
					target.Set(reflect.MakeMap(target.Type()))
				} else {
					target.Set(reflect.MakeSlice(target.Type(), 0, 0))
				}
				return nil
			}
			decoded := reflect.New(target.Type())
			if err := json.Unmarshal([]byte(value.String()), decoded.Interface()); err != nil {
				return err
			}
			target.Set(decoded.Elem())
			return nil
		}
	case reflect.Struct:
		if target.Type().ConvertibleTo(reflect.TypeFor[time.Time]()) {
			var timestamp time.Time
			switch source := source.(type) {
			case string:
				var err error
				timestamp, err = time.Parse(time.RFC3339Nano, source)
				if err != nil {
					// SQL datetime text commonly omits the RFC3339 separator or
					// zone. Match Carbon's date parsing for native time fields too,
					// without changing any custom Scanner's input contract.
					parsed := carbon.Parse(source)
					if parsed.Error != nil || parsed.IsEmpty() {
						return err
					}
					timestamp = parsed.StdTime()
				}
			case int64:
				timestamp = time.UnixMilli(source)
			case float64:
				timestamp = time.UnixMilli(int64(source))
			default:
				return errors.OrmRowScanValueNotRepresentable.Args(source, value.Type(), target.Type())
			}
			target.Set(reflect.ValueOf(timestamp).Convert(target.Type()))
			return nil
		}
	}

	return errors.OrmRowScanValueNotRepresentable.Args(source, value.Type(), target.Type())
}
