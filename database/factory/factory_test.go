package factory

import (
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/goravel/framework/contracts/database/factory"
	databaseorm "github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
)

type Model struct {
	ID uint `gorm:"primaryKey" json:"id"`
	Timestamps
}

type SoftDeletes struct {
	DeletedAt databaseorm.DeletedAt `gorm:"column:deleted_at" json:"deleted_at"`
}

type Timestamps struct {
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at" json:"created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at" json:"updated_at"`
}

type User struct {
	Model
	SoftDeletes
	Name   string
	Avatar string
}

func (u *User) Factory() factory.Factory {
	return &UserFactory{}
}

type UserFactory struct {
}

func (u *UserFactory) Definition() map[string]any {
	faker := gofakeit.New(0)
	return map[string]any{
		"Name":      faker.Name(),
		"Avatar":    faker.Email(),
		"CreatedAt": carbon.NewDateTime(carbon.Now()),
		"UpdatedAt": carbon.NewDateTime(carbon.Now()),
		"DeletedAt": databaseorm.DeletedAt{Time: time.Now(), Valid: true},
	}
}

type House struct {
	Model
	Name          string
	HouseableID   uint
	HouseableType string
}

func TestGetRawAttributes(t *testing.T) {
	var house House
	attributes, err := getRawAttributes(&house)
	assert.NotNil(t, err)
	assert.Nil(t, attributes)

	var user User
	attributes, err = getRawAttributes(&user)
	assert.Nil(t, err)
	assert.NotNil(t, attributes)

	var user1 User
	attributes, err = getRawAttributes(&user1, map[string]any{
		"Avatar": "avatar",
	})
	assert.Nil(t, err)
	assert.NotNil(t, attributes)
	assert.True(t, len(attributes["Name"].(string)) > 0)
	assert.Equal(t, "avatar", attributes["Avatar"].(string))
	assert.NotNil(t, attributes["CreatedAt"])
	assert.NotNil(t, attributes["UpdatedAt"])
	assert.NotNil(t, attributes["DeletedAt"])
}

type MappedModel struct {
	*Model
	Label   string `gorm:"column:full_name" db:"display_name"`
	Address *MappedAddress
	Friends []*MappedAddress
	Tags    map[string][]string
}

type MappedAddress struct {
	Name string `gorm:"column:label"`
}

func (*MappedModel) Factory() factory.Factory { return &MappedModelFactory{} }

type MappedModelFactory struct{}

func (*MappedModelFactory) Definition() map[string]any {
	return map[string]any{
		"ID": uint(7), "Label": "default", "CreatedAt": carbon.NewDateTime(carbon.Now()),
	}
}

func TestMakeMapsFieldNamesAndEmbeddedPointers(t *testing.T) {
	var model MappedModel
	require.NoError(t, NewFactoryImpl(nil).Make(&model, map[string]any{"Label": "override"}))
	require.NotNil(t, model.Model)
	assert.Equal(t, uint(7), model.ID)
	assert.Equal(t, "override", model.Label)
	require.NotNil(t, model.CreatedAt)
	assert.False(t, model.CreatedAt.IsZero())
}

func TestMakeCountKeepsDestinationsIndependent(t *testing.T) {
	var models []MappedModel
	require.NoError(t, NewFactoryImpl(nil).Count(2).Make(&models))
	require.Len(t, models, 2)
	assert.NotSame(t, models[0].Model, models[1].Model)
	assert.NotSame(t, models[0].CreatedAt, models[1].CreatedAt)
	assert.Equal(t, "default", models[0].Label)
	assert.Equal(t, "default", models[1].Label)
}

func TestMakeMapsNestedAttributes(t *testing.T) {
	var model MappedModel
	err := NewFactoryImpl(nil).Make(&model, map[string]any{
		"Address": map[string]any{"Name": "Home"},
		"Friends": []any{map[string]any{"Name": "Alice"}, map[string]any{"Name": "Bob"}},
		"Tags":    map[string]any{"roles": []any{"staff", "editor"}},
	})
	require.NoError(t, err)
	assert.Equal(t, &MappedAddress{Name: "Home"}, model.Address)
	assert.Equal(t, []*MappedAddress{{Name: "Alice"}, {Name: "Bob"}}, model.Friends)
	assert.Equal(t, map[string][]string{"roles": {"staff", "editor"}}, model.Tags)
}
