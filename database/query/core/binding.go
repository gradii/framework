package core

import "reflect"

type UnitEnum interface{ EnumName() string }
type BackedEnum interface{ EnumValue() any }

// CastBinding preserves standard SQL values while lowering explicit enum cases.
func CastBinding(value any) any {
	rv := reflect.ValueOf(value)
	if rv.IsValid() && rv.Kind() == reflect.Pointer && rv.IsNil() {
		return value
	}
	switch enum := value.(type) {
	case BackedEnum:
		return enum.EnumValue()
	case UnitEnum:
		return enum.EnumName()
	}
	return value
}
