package orm

import (
	"reflect"

	"github.com/goravel/framework/database/internal/annotation"
)

func GetID(dest any) any {
	if dest == nil {
		return nil
	}

	t := reflect.TypeOf(dest)
	v := reflect.ValueOf(dest)

	return GetIDByReflect(t, v)
}

func GetIDByReflect(t reflect.Type, v reflect.Value) any {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
		v = v.Elem()
	}

	// Only a struct can carry a primary-key field. Anything else — most importantly the slice or
	// array a batch destination is passed as — has no key to read, so it reports none instead of
	// panicking on NumField. That is what lets a batch destination reach the insert branch it is
	// meant for.
	if t.Kind() != reflect.Struct {
		return nil
	}

	for i := 0; i < t.NumField(); i++ {
		if !t.Field(i).IsExported() {
			continue
		}
		if annotation.ParseAnnotation(t.Field(i).Tag.Get("gorm")).PrimaryKey {
			if v.Field(i).IsZero() {
				return nil
			}

			return v.Field(i).Interface()
		}
		if t.Field(i).Type.Kind() == reflect.Struct && t.Field(i).Anonymous {
			id := GetIDByReflect(t.Field(i).Type, v.Field(i))
			if id != nil {
				return id
			}
		}
	}

	return nil
}
