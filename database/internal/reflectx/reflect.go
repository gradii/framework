// Adapted from github.com/upper/db/v4/internal/reflectx at 6ccd8f5.
// Copyright (c) 2013, Jason Moiron. See LICENSE in this directory.
// Goravel adds tag normalization and database scalar handling.

// Package reflectx implements extensions to the standard reflect lib suitable
// for implementing marshaling and unmarshaling packages.  The main Mapper type
// allows for Go-compatible named attribute access, including accessing embedded
// struct attributes and the ability to use  functions and struct tags to
// customize field names.
package reflectx

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/gradii/framework/errors"
)

// A FieldInfo is a collection of metadata about a struct field.
type FieldInfo struct {
	Index    []int
	Path     string
	Field    reflect.StructField
	Zero     reflect.Value
	Name     string
	Options  map[string]string
	Embedded bool
	Children []*FieldInfo
	Parent   *FieldInfo
}

// A StructMap is an index of field metadata for a struct.
type StructMap struct {
	Tree  *FieldInfo
	Index []*FieldInfo
	Paths map[string]*FieldInfo
	Names map[string]*FieldInfo
}

// GetByPath returns a *FieldInfo for a given string path.
func (f StructMap) GetByPath(path string) *FieldInfo {
	return f.Paths[path]
}

// GetByTraversal returns a *FieldInfo for a given integer path.  It is
// analogous to reflect.FieldByIndex.
func (f StructMap) GetByTraversal(index []int) *FieldInfo {
	if len(index) == 0 {
		return nil
	}

	tree := f.Tree
	for _, i := range index {
		if i >= len(tree.Children) || tree.Children[i] == nil {
			return nil
		}
		tree = tree.Children[i]
	}
	return tree
}

// Mapper is a general purpose mapper of names to struct fields.  A Mapper
// behaves like most marshallers, optionally obeying a field tag for name
// mapping and a function to provide a basic mapping of fields to names.
type Mapper struct {
	cache      map[reflect.Type]*StructMap
	tagName    string
	tagMapFunc func(string) string
	mapFunc    func(string) string
	mutex      sync.Mutex
}

// NewMapper returns a new mapper which optionally obeys the field tag given
// by tagName.  If tagName is the empty string, it is ignored.
func NewMapper(tagName string) *Mapper {
	return &Mapper{
		cache:   make(map[reflect.Type]*StructMap),
		tagName: tagName,
	}
}

// NewMapperTagFunc returns a new mapper which contains a mapper for field names
// AND a mapper for tag values.  This is useful for tags like json which can
// have values like "name,omitempty".
func NewMapperTagFunc(tagName string, mapFunc, tagMapFunc func(string) string) *Mapper {
	return &Mapper{
		cache:      make(map[reflect.Type]*StructMap),
		tagName:    tagName,
		mapFunc:    mapFunc,
		tagMapFunc: tagMapFunc,
	}
}

// NewMapperFunc returns a new mapper which optionally obeys a field tag and
// a struct field name mapper func given by f.  Tags will take precedence, but
// for any other field, the mapped name will be f(field.Name)
func NewMapperFunc(tagName string, f func(string) string) *Mapper {
	return &Mapper{
		cache:   make(map[reflect.Type]*StructMap),
		tagName: tagName,
		mapFunc: f,
	}
}

// TypeMap returns a mapping of field strings to int slices representing
// the traversal down the struct to reach the field.
func (m *Mapper) TypeMap(t reflect.Type) *StructMap {
	t = Deref(t)
	m.mutex.Lock()
	mapping, ok := m.cache[t]
	if !ok {
		mapping = getMapping(t, m.tagName, m.mapFunc, m.tagMapFunc)
		m.cache[t] = mapping
	}
	m.mutex.Unlock()
	return mapping
}

// FieldMap returns the mapper's mapping of field names to reflect values.  Panics
// if v's Kind is not Struct, or v is not Indirectable to a struct kind.
func (m *Mapper) FieldMap(v reflect.Value) map[string]reflect.Value {
	v = reflect.Indirect(v)
	mustBe(v, reflect.Struct)

	r := map[string]reflect.Value{}
	tm := m.TypeMap(v.Type())
	for tagName, fi := range tm.Names {
		r[tagName] = FieldByIndexes(v, fi.Index)
	}
	return r
}

// ValidFieldMap returns the mapper's mapping of field names to reflect valid
// field values.  Panics if v's Kind is not Struct, or v is not Indirectable to
// a struct kind.
func (m *Mapper) ValidFieldMap(v reflect.Value) map[string]reflect.Value {
	v = reflect.Indirect(v)
	mustBe(v, reflect.Struct)

	r := map[string]reflect.Value{}
	tm := m.TypeMap(v.Type())
	for tagName, fi := range tm.Names {
		v := ValidFieldByIndexes(v, fi.Index)
		if v.IsValid() {
			r[tagName] = v
		}
	}
	return r
}

// FieldByName returns a field by the its mapped name as a reflect.Value.
// Panics if v's Kind is not Struct or v is not Indirectable to a struct Kind.
// Returns zero Value if the name is not found.
func (m *Mapper) FieldByName(v reflect.Value, name string) reflect.Value {
	v = reflect.Indirect(v)
	mustBe(v, reflect.Struct)

	tm := m.TypeMap(v.Type())
	fi, ok := tm.Names[name]
	if !ok {
		return reflect.Value{}
	}
	return FieldByIndexes(v, fi.Index)
}

// FieldsByName returns a slice of values corresponding to the slice of names
// for the value.  Panics if v's Kind is not Struct or v is not Indirectable
// to a struct Kind.  Returns zero Value for each name not found.
func (m *Mapper) FieldsByName(v reflect.Value, names []string) []reflect.Value {
	v = reflect.Indirect(v)
	mustBe(v, reflect.Struct)

	tm := m.TypeMap(v.Type())
	vals := make([]reflect.Value, 0, len(names))
	for _, name := range names {
		fi, ok := tm.Names[name]
		if !ok {
			vals = append(vals, *new(reflect.Value))
		} else {
			vals = append(vals, FieldByIndexes(v, fi.Index))
		}
	}
	return vals
}

// TraversalsByName returns a slice of int slices which represent the struct
// traversals for each mapped name.  Panics if t is not a struct or Indirectable
// to a struct.  Returns empty int slice for each name not found.
func (m *Mapper) TraversalsByName(t reflect.Type, names []string) [][]int {
	t = Deref(t)
	mustBe(t, reflect.Struct)
	tm := m.TypeMap(t)

	r := make([][]int, 0, len(names))
	for _, name := range names {
		fi, ok := tm.Names[name]
		if !ok {
			r = append(r, []int{})
		} else {
			r = append(r, fi.Index)
		}
	}
	return r
}

// FieldByIndexes returns a value for a particular struct traversal, allocating
// pointers along the path. An uninitializable pointer yields an invalid value.
func FieldByIndexes(v reflect.Value, indexes []int) reflect.Value {
	for _, i := range indexes {
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !v.CanSet() {
					return reflect.Value{}
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
		// if this is a pointer, it's possible it is nil
		if v.Kind() == reflect.Pointer && v.IsNil() {
			if !v.CanSet() {
				return reflect.Value{}
			}
			alloc := reflect.New(v.Type().Elem())
			v.Set(alloc)
		}
		if v.Kind() == reflect.Map && v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
	}
	return v
}

// ValidFieldByIndexes returns a value for a particular struct traversal.
func ValidFieldByIndexes(v reflect.Value, indexes []int) reflect.Value {

	for _, i := range indexes {
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if !v.IsValid() {
			return reflect.Value{}
		}
		v = v.Field(i)
		// if this is a pointer, it's possible it is nil
		if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Map) && v.IsNil() {
			return reflect.Value{}
		}
	}

	return v
}

// FieldByIndexesReadOnly returns a value for a particular struct traversal,
// but is not concerned with allocating nil pointers because the value is
// going to be used for reading and not setting.
func FieldByIndexesReadOnly(v reflect.Value, indexes []int) reflect.Value {
	for _, i := range indexes {
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if !v.IsValid() {
			return reflect.Value{}
		}
		v = v.Field(i)
	}
	return v
}

// Deref is Indirect for reflect.Types
func Deref(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// -- helpers & utilities --

type kinder interface {
	Kind() reflect.Kind
}

// mustBe checks a value against the required kind.
func mustBe(v kinder, expected reflect.Kind) {
	k := v.Kind()
	if k != expected {
		panic(errors.DatabaseUnsupportedType.Args(k, expected))
	}
}

type typeQueue struct {
	t  reflect.Type
	fi *FieldInfo
	pp string // Parent path
}

// A copying append that creates a new slice each time.
func apnd(is []int, i int) []int {
	x := make([]int, len(is)+1)
	copy(x, is)
	x[len(x)-1] = i
	return x
}

// getMapping returns a mapping for the t type, using the tagName, mapFunc and
// tagMapFunc to determine the canonical names of fields.
func getMapping(t reflect.Type, tagName string, mapFunc, tagMapFunc func(string) string) *StructMap {
	m := []*FieldInfo{}

	root := &FieldInfo{}
	queue := []typeQueue{}
	queue = append(queue, typeQueue{Deref(t), root, ""})

	for len(queue) != 0 {
		// pop the first item off of the queue
		tq := queue[0]
		queue = queue[1:]
		// Recursive model fields do not introduce an infinite set of paths.
		recursive := tq.fi != root && tq.t == Deref(t)
		for ancestor := tq.fi.Parent; !recursive && ancestor != nil && ancestor != root; ancestor = ancestor.Parent {
			recursive = Deref(ancestor.Field.Type) == tq.t
		}
		if recursive {
			continue
		}
		nChildren := 0
		if tq.t.Kind() == reflect.Struct {
			nChildren = tq.t.NumField()
		}
		tq.fi.Children = make([]*FieldInfo, nChildren)

		// iterate through all of its fields
		for fieldPos := 0; fieldPos < nChildren; fieldPos++ {
			f := tq.t.Field(fieldPos)

			fi := FieldInfo{}
			fi.Field = f
			fi.Zero = reflect.New(f.Type).Elem()
			fi.Options = map[string]string{}

			var tag, name string
			if mapFunc != nil {
				name = mapFunc(f.Name)
			}
			if tagName != "" {
				tag = f.Tag.Get(tagName)
			}
			if tagMapFunc != nil {
				tag = tagMapFunc(tag)
			}
			if tag != "" {
				name = tag
			}

			parts := strings.Split(name, ",")
			if len(parts) > 1 {
				name = parts[0]
				for _, opt := range parts[1:] {
					kv := strings.Split(opt, "=")
					if len(kv) > 1 {
						fi.Options[kv[0]] = kv[1]
					} else {
						fi.Options[kv[0]] = ""
					}
				}
			}

			fi.Name = name

			if tq.pp == "" {
				fi.Path = fi.Name
			} else {
				fi.Path = fmt.Sprintf("%s.%s", tq.pp, fi.Name)
			}

			// if the name is "-", disabled via a tag, skip it
			if name == "-" {
				continue
			}

			// Private embeds can expose exported fields, but private scalar
			// fields cannot be assigned through reflection.
			if !f.IsExported() && (!f.Anonymous || Deref(f.Type).Kind() != reflect.Struct || isScannable(f.Type)) {
				continue
			}

			// bfs search of anonymous embedded structs
			if f.Anonymous && Deref(f.Type).Kind() == reflect.Struct && !isScannable(f.Type) {
				pp := tq.pp
				if tag != "" {
					pp = fi.Path
				}

				fi.Embedded = true
				fi.Index = apnd(tq.fi.Index, fieldPos)
				nChildren := 0
				ft := Deref(f.Type)
				if ft.Kind() == reflect.Struct {
					nChildren = ft.NumField()
				}
				fi.Children = make([]*FieldInfo, nChildren)
				queue = append(queue, typeQueue{Deref(f.Type), &fi, pp})
			} else if Deref(f.Type).Kind() == reflect.Struct && !isScannable(f.Type) {
				fi.Index = apnd(tq.fi.Index, fieldPos)
				fi.Children = make([]*FieldInfo, Deref(f.Type).NumField())
				queue = append(queue, typeQueue{Deref(f.Type), &fi, fi.Path})
			}

			fi.Index = apnd(tq.fi.Index, fieldPos)
			fi.Parent = tq.fi
			tq.fi.Children[fieldPos] = &fi
			m = append(m, &fi)
		}
	}

	flds := &StructMap{Index: m, Tree: root, Paths: map[string]*FieldInfo{}, Names: map[string]*FieldInfo{}}
	for _, fi := range flds.Index {
		flds.Paths[fi.Path] = fi
		if fi.Name != "" && !fi.Embedded {
			flds.Names[fi.Path] = fi
		}
	}

	return flds
}

// Database scalars retain their own Scan/Value behavior instead of exposing
// their implementation fields as additional columns.
func isScannable(t reflect.Type) bool {
	t = Deref(t)
	return t.ConvertibleTo(reflect.TypeFor[time.Time]()) ||
		t.Implements(reflect.TypeFor[sql.Scanner]()) || reflect.PointerTo(t).Implements(reflect.TypeFor[sql.Scanner]()) ||
		t.Implements(reflect.TypeFor[driver.Valuer]()) || reflect.PointerTo(t).Implements(reflect.TypeFor[driver.Valuer]())
}
