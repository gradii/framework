package morphmap

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
)

type morphmapPost struct {
	ID uint
}

type morphmapVideo struct {
	ID uint
}

type morphmapUser struct{}

func (morphmapUser) MorphClass() string { return "user" }

type morphmapTag struct{}

func (*morphmapTag) MorphClass() string { return "tag" }

type morphmapEmpty struct{}

func (morphmapEmpty) MorphClass() string { return "" }

func TestRegister_AndLookup(t *testing.T) {
	tests := []struct {
		name      string
		setup     func()
		alias     string
		wantType  reflect.Type
		wantNil   bool
		modelLook any
		wantAlias string
		wantOk    bool
	}{
		{
			name: "registered alias yields fresh pointer to model type",
			setup: func() {
				Reset()
				Register(map[string]any{"post": &morphmapPost{}})
			},
			alias:     "post",
			wantType:  reflect.TypeOf(&morphmapPost{}),
			modelLook: &morphmapPost{},
			wantAlias: "post",
			wantOk:    true,
		},
		{
			name: "value-type sample is normalised to elem type",
			setup: func() {
				Reset()
				Register(map[string]any{"video": morphmapVideo{}})
			},
			alias:     "video",
			wantType:  reflect.TypeOf(&morphmapVideo{}),
			modelLook: &morphmapVideo{},
			wantAlias: "video",
			wantOk:    true,
		},
		{
			name:    "unregistered alias returns nil",
			setup:   Reset,
			alias:   "missing",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			got := Find(tt.alias)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tt.wantType, reflect.TypeOf(got))
			alias, ok := AliasOf(tt.modelLook)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantAlias, alias)
		})
	}
}

func TestRegister_MergeAndReplace(t *testing.T) {
	Reset()
	Register(map[string]any{"post": &morphmapPost{}})
	Register(map[string]any{"video": &morphmapVideo{}}) // merge
	assert.NotNil(t, Find("post"))
	assert.NotNil(t, Find("video"))

	Register(map[string]any{"user": &morphmapUser{}}, false) // replace
	assert.Nil(t, Find("post"))
	assert.Nil(t, Find("video"))
	assert.NotNil(t, Find("user"))
}

func TestRegister_LaterWriteWins(t *testing.T) {
	Reset()
	Register(map[string]any{"post": &morphmapPost{}})
	Register(map[string]any{"post": &morphmapVideo{}}) // overwrite under same alias
	got := Find("post")
	assert.Equal(t, reflect.TypeOf(&morphmapVideo{}), reflect.TypeOf(got))

	// AliasOf should also rebind: Post no longer maps to "post"; Video does.
	_, ok := AliasOf(&morphmapPost{})
	assert.False(t, ok)

	alias, ok := AliasOf(&morphmapVideo{})
	assert.True(t, ok)
	assert.Equal(t, "post", alias)
}

func TestRegister_RebindOnConflictingType(t *testing.T) {
	// Same type registered under two different aliases — only the later alias survives.
	Reset()
	Register(map[string]any{"first": &morphmapPost{}})
	Register(map[string]any{"second": &morphmapPost{}})

	assert.Nil(t, Find("first"))
	got := Find("second")
	assert.Equal(t, reflect.TypeOf(&morphmapPost{}), reflect.TypeOf(got))

	alias, ok := AliasOf(&morphmapPost{})
	assert.True(t, ok)
	assert.Equal(t, "second", alias)
}

func TestMorphValue_PriorityOrder(t *testing.T) {
	tests := []struct {
		name      string
		setup     func()
		input     any
		wantValue string
		wantOk    bool
	}{
		{
			name: "MorphClass method takes precedence over registry",
			setup: func() {
				Reset()
				Register(map[string]any{"registered_user": &morphmapUser{}})
			},
			input:     &morphmapUser{},
			wantValue: "user", // from MorphClass(), not "registered_user"
			wantOk:    true,
		},
		{
			name: "MorphClass works on pointer-receiver method via value caller",
			setup: func() {
				Reset()
			},
			input:     morphmapTag{}, // value, but MorphClass has pointer receiver
			wantValue: "tag",
			wantOk:    true,
		},
		{
			name: "MorphClass works on pointer caller too",
			setup: func() {
				Reset()
			},
			input:     &morphmapTag{},
			wantValue: "tag",
			wantOk:    true,
		},
		{
			name: "empty MorphClass falls through to registry",
			setup: func() {
				Reset()
				Register(map[string]any{"fallback": &morphmapEmpty{}})
			},
			input:     &morphmapEmpty{},
			wantValue: "fallback",
			wantOk:    true,
		},
		{
			name: "registry hit when no MorphClass",
			setup: func() {
				Reset()
				Register(map[string]any{"post": &morphmapPost{}})
			},
			input:     &morphmapPost{},
			wantValue: "post",
			wantOk:    true,
		},
		{
			name:   "no MorphClass and no registry entry yields not-found",
			setup:  Reset,
			input:  &morphmapPost{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			value, ok, err := MorphValue(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantValue, value)
		})
	}
}

func TestAll_Snapshot(t *testing.T) {
	Reset()
	Register(map[string]any{
		"post":  &morphmapPost{},
		"video": &morphmapVideo{},
	})
	snap := All()
	assert.Equal(t, 2, len(snap))
	assert.Equal(t, reflect.TypeOf(morphmapPost{}), snap["post"])
	assert.Equal(t, reflect.TypeOf(morphmapVideo{}), snap["video"])

	// Mutating the snapshot must not affect the registry.
	delete(snap, "post")
	assert.NotNil(t, Find("post"))
}

// The strict-morph tests below port Relation::requireMorphMap / requiresMorphMap /
// enforceMorphMap (Relation.php:437/:447/:459) and the getMorphClass strict branch
// (HasRelationships.php:1006-1022) onto the registry. Oracle v12.55.1, executed
// (/tmp/strictmorphs-probes/strict_morphs_probe.php): a fresh process reports
// requiresMorphMap() false; requireMorphMap() plus an unmapped model throws
// Illuminate\Database\ClassMorphViolationException ("No morph map defined for model [X].");
// 'strict base Pivot getMorphClass => 'Illuminate\\Database\\Eloquent\\Relations\\Pivot'' while
// 'strict base MorphPivot threw' the same violation — the exemption is the base Pivot FQCN only.

// morphmapPivotSubclass is the Go stand-in for `class X extends Pivot`: a distinct type embedding
// relations.Pivot. The carve-out is compared by type identity, never by method set, so embedding
// does not inherit the exemption.
type morphmapPivotSubclass struct {
	relations.Pivot
}

// morphmapClassName renders the class name the registry reports: package path plus type name.
// Computed here independently of morphmap.classOf so the assertions pin the format.
func morphmapClassName(model any) string {
	typ := reflect.TypeOf(model)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ.PkgPath() + "." + typ.Name()
}

func TestRequireMorphMap_DefaultAndToggle(t *testing.T) {
	t.Cleanup(func() { RequireMorphMap(false) })

	RequireMorphMap(false)
	assert.False(t, RequiresMorphMap())

	RequireMorphMap()
	assert.True(t, RequiresMorphMap())

	RequireMorphMap(false)
	assert.False(t, RequiresMorphMap())
}

func TestEnforceMorphMap_SetsFlagAndRegisters(t *testing.T) {
	Reset()
	RequireMorphMap(false)
	t.Cleanup(func() {
		Reset()
		RequireMorphMap(false)
	})

	EnforceMorphMap(map[string]any{"post": &morphmapPost{}})
	assert.True(t, RequiresMorphMap())
	assert.NotNil(t, Find("post"))

	EnforceMorphMap(map[string]any{"video": &morphmapVideo{}}, false) // replace, like morphMap($map, false)
	assert.Nil(t, Find("post"))
	alias, ok := AliasOf(&morphmapVideo{})
	assert.True(t, ok)
	assert.Equal(t, "video", alias)
}

func TestMorphValue_StrictMode(t *testing.T) {
	tests := []struct {
		name          string
		register      map[string]any
		input         any
		wantValue     string
		wantOk        bool
		wantViolation bool
	}{
		{
			name:          "unmapped model raises the class violation",
			input:         &morphmapVideo{},
			wantViolation: true,
		},
		{
			name:      "mapped model resolves before the strict check",
			register:  map[string]any{"post": &morphmapPost{}},
			input:     &morphmapPost{},
			wantValue: "post",
			wantOk:    true,
		},
		{
			name:      "MorphClass override replaces the resolution",
			input:     &morphmapUser{},
			wantValue: "user",
			wantOk:    true,
		},
		{
			name:      "base Pivot is exempt and resolves to its class name",
			input:     &relations.Pivot{},
			wantValue: morphmapClassName(&relations.Pivot{}),
			wantOk:    true,
		},
		{
			name:          "MorphPivot subclass raises — the carve-out is Pivot-FQCN-only",
			input:         &relations.MorphPivot{},
			wantViolation: true,
		},
		{
			name:          "user struct embedding Pivot raises",
			input:         &morphmapPivotSubclass{},
			wantViolation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Reset()
			RequireMorphMap()
			t.Cleanup(func() {
				Reset()
				RequireMorphMap(false)
			})
			if tt.register != nil {
				Register(tt.register)
			}

			value, ok, err := MorphValue(tt.input)
			if tt.wantViolation {
				require.Error(t, err)
				assert.ErrorIs(t, err, errors.OrmClassMorphViolation)
				assert.Equal(t, "No morph map defined for model ["+morphmapClassName(tt.input)+"].", err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantValue, value)
		})
	}
}
