package relations

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The S2 seam helpers duplicated into this package (seam.go) are pinned against the behavior of
// the gorm originals they stand in for: isValidParent, dictKey (with derefKeyValue /
// unwrapValuerValue), ormBuilderOf and parseSchema's modelSchema.Name.

type seamModel struct{ ID int }

// seamFakeBuilder stands in for *gorm.OrmBuilder: the embedded nil Builder promotes the whole
// method set, and only the identity of the value matters to BuilderOf.
type seamFakeBuilder struct{ Builder }

// seamFakeQuery is a plain Query implementation that is not a Builder.
type seamFakeQuery struct{ contractsorm.Query }

func TestIsValidParent_PointerToStructOnly(t *testing.T) {
	var nilModel *seamModel
	number := 7

	assert.True(t, IsValidParent(&seamModel{}))
	assert.False(t, IsValidParent(nil))
	assert.False(t, IsValidParent(seamModel{}), "a value is not addressable")
	assert.False(t, IsValidParent(nilModel), "a typed nil pointer")
	assert.False(t, IsValidParent(&number), "a pointer to a non-struct")
}

func TestDictionaryKey_CanonicalStringForm(t *testing.T) {
	seven := 7
	var nilInt *int

	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, ""},
		{"string", "7", "7"},
		{"int", 7, "7"},
		{"uint8", uint8(7), "7"},
		{"int64", int64(7), "7"},
		{"bytes", []byte("key"), "key"},
		{"pointer", &seven, "7"},
		{"nil pointer", nilInt, ""},
		{"valid NullString", sql.NullString{String: "s", Valid: true}, "s"},
		{"invalid NullString", sql.NullString{}, ""},
		{"valid NullInt64 pointer", &sql.NullInt64{Int64: 9, Valid: true}, "9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, DictionaryKey(tc.value))
		})
	}
}

func TestBuilderOf_UnwrapsRelationObjectsToTheBuilder(t *testing.T) {
	fake := &seamFakeBuilder{}

	got, ok := BuilderOf(fake)
	require.True(t, ok)
	assert.True(t, got == Builder(fake), "a Builder is returned as it stands")

	rel := Wrap(fake, nil, "relation", RelationInfo{Kind: KindHasMany}, Hooks{})
	got, ok = BuilderOf(rel)
	require.True(t, ok)
	assert.True(t, got == Builder(fake), "a relation object reports the builder it forwards to, not itself")

	_, ok = BuilderOf(nil)
	assert.False(t, ok, "a nil handle")

	_, ok = BuilderOf(WrapBase(nil, nil, "relation", Hooks{}))
	assert.False(t, ok, "a relation object over no builder")

	_, ok = BuilderOf(&seamFakeQuery{})
	assert.False(t, ok, "a Query that is not a Builder")
}

func TestModelTypeName_UnwrapsToTheStructName(t *testing.T) {
	assert.Equal(t, "seamModel", ModelTypeName(&seamModel{}))
	assert.Equal(t, "seamModel", ModelTypeName(seamModel{}))
	assert.Equal(t, "seamModel", ModelTypeName([]*seamModel{}))
	assert.Equal(t, "seamModel", ModelTypeName(&[]seamModel{}))
	assert.Equal(t, "", ModelTypeName(7))
	assert.Equal(t, "", ModelTypeName(nil))
}
