package tests

import (
	"errors"
	"fmt"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/goravel/framework/contracts/database/factory"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/support/carbon"
	basedatabase "github.com/goravel/framework/support/database"
)

type contextKey int

const testContextKey contextKey = 0

type Model struct {
	ID uint `gorm:"primaryKey" json:"id"`
	Timestamps
}

type SoftDeletes struct {
	DeletedAt basedatabase.DeletedAt `gorm:"column:deleted_at" json:"deleted_at" db:"deleted_at"`
}

type Timestamps struct {
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at" json:"created_at" db:"created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at" json:"updated_at" db:"updated_at"`
}

type User struct {
	Model
	SoftDeletes
	Name    string
	Bio     *string
	Avatar  string
	Address *Address `gorm:"-"`
	Books   []*Book  `gorm:"-"`
	House   *House   `gorm:"-"`
	Phones  []*Phone `gorm:"-"`
	Roles   []*Role  `gorm:"-"`
	Ratio   float64
	age     int
}

func (User) TableName() string { return "users" }

func (r *User) Factory() factory.Factory {
	return &UserFactory{}
}

func (r *User) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Address": contractsorm.HasOne{
			Related: &Address{},
		},
		"Books": contractsorm.HasMany{
			Related: &Book{},
		},
		"House": contractsorm.MorphOne{
			Related: &House{},
			Name:    "houseable",
		},
		"Phones": contractsorm.MorphMany{
			Related: &Phone{},
			Name:    "phoneable",
		},
		"Roles": contractsorm.Many2Many{
			Related: &Role{},
			Table:   "role_user",
		},
	}
}

func (r *User) DispatchesEvents() map[contractsorm.EventType]func(contractsorm.Event) error {
	return map[contractsorm.EventType]func(contractsorm.Event) error{
		contractsorm.EventCreating: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "creating event with slice struct" || event.Context().Value("event") == "creating event with slice map" {
				panic("dest is slice when creating")
			}

			name := event.GetAttribute("name")
			if name != nil {
				if name.(string) == "event_creating_name" {
					event.SetAttribute("avatar", "event_creating_avatar")
				}
				if name.(string) == "event_creating_by_map_name" {
					event.SetAttribute("avatar", "event_creating_by_map_avatar1")
				}
				if name.(string) == "event_creating_FirstOrCreate_name" {
					event.SetAttribute("avatar", "event_creating_FirstOrCreate_avatar")
				}
				if name.(string) == "event_creating_omit_create_name" {
					event.SetAttribute("avatar", "event_creating_omit_create_avatar")
				}
				if name.(string) == "event_creating_select_create_name" {
					event.SetAttribute("avatar", "event_creating_select_create_avatar")
				}
				if name.(string) == "event_creating_save_name" {
					event.SetAttribute("avatar", "event_creating_save_avatar")
				}
				if name.(string) == "event_creating_IsDirty_name" {
					if event.IsDirty("name") {
						event.SetAttribute("avatar", "event_creating_IsDirty_avatar")
					}
				}
				if name.(string) == "event_context" {
					val := event.Context().Value(testContextKey)
					event.SetAttribute("avatar", val.(string))
				}
				if name.(string) == "event_query" {
					_ = event.Query().Create(&User{Name: "event_query1"})
				}
			}

			return nil
		},
		contractsorm.EventCreated: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "created event with slice struct" || event.Context().Value("event") == "created event with slice map" {
				panic("dest is slice when created")
			}

			name := event.GetAttribute("name")
			if name != nil {
				if name.(string) == "event_created_name" {
					id := event.GetAttribute("ID")
					event.SetAttribute("avatar", fmt.Sprintf("event_created_avatar_%d", id))
				}
				if name.(string) == "event_created_by_map_name" {
					event.SetAttribute("avatar", "event_created_by_map_avatar1")
				}
				if name.(string) == "event_created_FirstOrCreate_name" {
					id := event.GetAttribute("ID")
					event.SetAttribute("avatar", fmt.Sprintf("event_created_FirstOrCreate_avatar_%d", id))
				}
				if name.(string) == "event_created_omit_create_name" {
					id := event.GetAttribute("ID")
					event.SetAttribute("avatar", fmt.Sprintf("event_created_omit_create_avatar_%d", id))
				}
				if name.(string) == "event_created_select_create_name" {
					id := event.GetAttribute("ID")
					event.SetAttribute("avatar", fmt.Sprintf("event_created_select_create_avatar_%d", id))
				}
				if name.(string) == "event_created_save_name" {
					id := event.GetAttribute("ID")
					event.SetAttribute("avatar", fmt.Sprintf("event_created_save_avatar_%d", id))
				}
			}

			return nil
		},
		contractsorm.EventSaving: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "saving event with slice struct" || event.Context().Value("event") == "saving event with slice map" {
				panic("dest is slice when saving")
			}

			name := event.GetAttribute("name")
			switch name.(type) {
			case string:
				if name == "event_saving_create_name" {
					event.SetAttribute("avatar", "event_saving_create_avatar")
				}
				if name == "event_saving_create_by_map_name" {
					event.SetAttribute("avatar", "event_saving_create_by_map_avatar1")
				}
				if name == "event_saving_save_name" {
					event.SetAttribute("avatar", "event_saving_save_avatar")
				}
				if name == "event_saving_FirstOrCreate_name" {
					event.SetAttribute("avatar", "event_saving_FirstOrCreate_avatar")
				}
				if name == "event_saving_omit_create_name" {
					event.SetAttribute("avatar", "event_saving_omit_create_avatar")
				}
				if name == "event_saving_select_create_name" {
					event.SetAttribute("avatar", "event_saving_select_create_avatar")
				}
				if name == "event_save_without_name" {
					event.SetAttribute("avatar", "event_save_without_avatar")
				}
				if name == "event_save_quietly_name" {
					event.SetAttribute("avatar", "event_save_quietly_avatar")
				}
				if name == "event_saving_IsDirty_name" {
					if event.IsDirty("name") {
						event.SetAttribute("avatar", "event_saving_IsDirty_avatar")
					}
				}
			}

			avatar := event.GetAttribute("avatar")
			if avatar != nil && avatar.(string) == "event_saving_single_update_avatar" {
				event.SetAttribute("avatar", "event_saving_single_update_avatar1")
			}

			return nil
		},
		contractsorm.EventSaved: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "saved event with slice struct" || event.Context().Value("event") == "saved event with slice map" {
				panic("dest is slice when saved")
			}

			name := event.GetAttribute("name")
			switch name.(type) {
			case string:
				if name == "event_saved_create_name" {
					event.SetAttribute("avatar", "event_saved_create_avatar")
				}
				if name == "event_saved_create_by_map_name" {
					event.SetAttribute("avatar", "event_saved_create_by_map_avatar1")
				}
				if name == "event_saved_omit_create_name" {
					event.SetAttribute("avatar", "event_saved_omit_create_avatar")
				}
				if name == "event_saved_select_create_name" {
					event.SetAttribute("avatar", "event_saved_select_create_avatar")
				}
				if name == "event_saved_save_name" {
					event.SetAttribute("avatar", "event_saved_save_avatar")
				}
				if name == "event_saved_FirstOrCreate_name" {
					event.SetAttribute("avatar", "event_saved_FirstOrCreate_avatar")
				}
				if name == "event_save_without_name" {
					event.SetAttribute("avatar", "event_saved_without_avatar")
				}
				if name == "event_save_quietly_name" {
					event.SetAttribute("avatar", "event_saved_quietly_avatar")
				}
			}

			avatar := event.GetAttribute("avatar")
			if avatar != nil && avatar.(string) == "event_saved_map_update_avatar" {
				event.SetAttribute("avatar", "event_saved_map_update_avatar1")
			}

			return nil
		},
		contractsorm.EventUpdating: func(event contractsorm.Event) error {
			name := event.GetAttribute("name")
			switch name.(type) {
			case string:
				if name == "event_updating_create_name" {
					event.SetAttribute("avatar", "event_updating_create_avatar")
				}
				if name == "event_updating_save_name" {
					event.SetAttribute("avatar", "event_updating_save_avatar")
				}
				if name == "event_updating_single_update_IsDirty_name1" {
					if event.IsDirty("name") {
						name := event.GetAttribute("name")
						if name != "event_updating_single_update_IsDirty_name1" {
							return errors.New("error")
						}
						event.SetAttribute("avatar", "event_updating_single_update_IsDirty_avatar")
					}
				}
				if name == "event_updating_map_update_IsDirty_name1" {
					if event.IsDirty("name") {
						name := event.GetAttribute("name")
						if name != "event_updating_map_update_IsDirty_name1" {
							return errors.New("error")
						}
						event.SetAttribute("avatar", "event_updating_map_update_IsDirty_avatar")
					}
				}
				if name == "event_updating_model_update_IsDirty_name1" {
					if event.IsDirty("name") {
						name := event.GetAttribute("name")
						if name != "event_updating_model_update_IsDirty_name1" {
							return errors.New("error")
						}
						event.SetAttribute("avatar", "event_updating_model_update_IsDirty_avatar")
					}
				}
			}

			avatar := event.GetAttribute("avatar")
			if avatar != nil {
				if avatar.(string) == "event_updating_save_avatar" {
					event.SetAttribute("avatar", "event_updating_save_avatar1")
				}
				if avatar.(string) == "event_updating_model_update_avatar" {
					id := event.GetOriginal("ID")
					event.SetAttribute("avatar", fmt.Sprintf("event_updating_model_update_avatar_%d", id))
				}
			}

			return nil
		},
		contractsorm.EventUpdated: func(event contractsorm.Event) error {
			name := event.GetAttribute("name")
			switch name.(type) {
			case string:
				if name == "event_updated_create_name" {
					event.SetAttribute("avatar", "event_updated_create_avatar")
				}
				if name == "event_updated_save_name" {
					event.SetAttribute("avatar", "event_updated_save_avatar")
				}
			}

			avatar := event.GetAttribute("avatar")
			if avatar != nil {
				if avatar.(string) == "event_updated_save_avatar" {
					event.SetAttribute("avatar", "event_updated_save_avatar1")
				}
				if avatar.(string) == "event_updated_model_update_avatar" {
					event.SetAttribute("avatar", "event_updated_model_update_avatar1")
				}
			}

			return nil
		},
		contractsorm.EventDeleting: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "deleting event with mass records" {
				panic("dest has no id when deleting")
			}

			name := event.GetAttribute("name")
			if name != nil && name.(string) == "event_deleting_name" {
				return errors.New("deleting error")
			}

			return nil
		},
		contractsorm.EventDeleted: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "deleted event with mass records" {
				panic("dest has no id when deleted")
			}

			name := event.GetAttribute("name")
			if name != nil && name.(string) == "event_deleted_name" {
				return errors.New("deleted error")
			}

			return nil
		},
		contractsorm.EventForceDeleting: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "force deleting event with mass records" {
				panic("dest has no id when force deleting")
			}

			name := event.GetAttribute("name")
			if name != nil && name.(string) == "event_force_deleting_name" {
				return errors.New("force deleting error")
			}

			return nil
		},
		contractsorm.EventForceDeleted: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "force deleted event with mass records" {
				panic("dest has no id when force deleted")
			}

			name := event.GetAttribute("name")
			if name != nil && name.(string) == "event_force_deleted_name" {
				return errors.New("force deleted error")
			}

			return nil
		},
		contractsorm.EventRetrieved: func(event contractsorm.Event) error {
			if event.Context().Value("event") == "retrieved event with slice struct" {
				panic("dest is slice when retrieved")
			}

			name := event.GetAttribute("name")
			if name != nil && name.(string) == "event_retrieved_name" {
				event.SetAttribute("name", "event_retrieved_name1")
			}

			return nil
		},
		contractsorm.EventRestored: func(event contractsorm.Event) error {
			name := event.GetAttribute("name")
			if name != nil && name.(string) == "event_restored_name" {
				event.SetAttribute("name", "event_restored_name1")
			}

			return nil
		},
		contractsorm.EventRestoring: func(event contractsorm.Event) error {
			name := event.GetAttribute("name")
			if name != nil && name.(string) == "event_restoring_name" {
				event.SetAttribute("name", "event_restoring_name1")
			}

			return nil
		},
	}
}

type UserObserver struct{}

func (r *UserObserver) Creating(event contractsorm.Event) error {
	name := event.GetAttribute("name")
	if name != nil {
		if name.(string) == "observer_name" {
			return errors.New("error")
		}
		if name.(string) == "with_context_name" {
			if avatar := event.Context().Value(testContextKey); avatar != nil {
				event.SetAttribute("avatar", avatar.(string))
			}
		}
	}

	return nil
}

func (r *UserObserver) Created(event contractsorm.Event) error {
	return nil
}

func (r *UserObserver) Updated(event contractsorm.Event) error {
	return nil
}

func (r *UserObserver) Deleted(event contractsorm.Event) error {
	return nil
}

func (r *UserObserver) ForceDeleted(event contractsorm.Event) error {
	return nil
}

type UserFactory struct {
}

func (r *UserFactory) Definition() map[string]any {
	faker := gofakeit.New(0)
	return map[string]any{
		"Name":      faker.Name(),
		"Avatar":    faker.Email(),
		"CreatedAt": carbon.NewDateTime(carbon.Now()),
		"UpdatedAt": carbon.NewDateTime(carbon.Now()),
	}
}

type Role struct {
	Model
	Name   string
	Avatar string
	Users  []*User `gorm:"-"`
}

func (Role) TableName() string { return "roles" }

func (r *Role) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Users": contractsorm.Many2Many{
			Related: &User{},
			Table:   "role_user",
		},
	}
}

type Address struct {
	Model
	UserID   uint
	Name     string
	Province string
	User     *User `gorm:"-"`
}

func (Address) TableName() string { return "addresses" }

func (r *Address) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{
			Related: &User{},
		},
	}
}

type Book struct {
	Model
	UserID uint
	Name   string
	User   *User   `gorm:"-"`
	Author *Author `gorm:"-"`
}

func (Book) TableName() string { return "books" }

func (r *Book) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{
			Related: &User{},
		},
		"Author": contractsorm.HasOne{
			Related: &Author{},
		},
	}
}

type Author struct {
	Model
	BookID uint   `db:"book_id"`
	Name   string `db:"name"`
}

func (Author) TableName() string { return "authors" }

func (r *Author) Factory() factory.Factory {
	return &AuthorFactory{}
}

type AuthorFactory struct {
}

func (r *AuthorFactory) Definition() map[string]any {
	faker := gofakeit.New(0)
	return map[string]any{
		"ID":        1,
		"BookID":    2,
		"Name":      faker.Name(),
		"CreatedAt": carbon.NewDateTime(carbon.Now()),
		"UpdatedAt": carbon.NewDateTime(carbon.Now()),
	}
}

type House struct {
	Model
	Name          string
	HouseableID   uint
	HouseableType string
}

func (House) TableName() string { return "houses" }

func (r *House) Factory() string {
	return "house"
}

type Phone struct {
	Model
	Name          string
	PhoneableID   uint
	PhoneableType string
}

func (Phone) TableName() string { return "phones" }

type Product struct {
	Model
	SoftDeletes
	Name   string `db:"name"`
	Weight *int   `db:"weight"`
	Height *int   `db:"height"`
}

func (Product) TableName() string { return "products" }

func (r *Product) Connection() string {
	return "sqlite"
}

type Review struct {
	Model
	SoftDeletes
	Body string
}

func (Review) TableName() string { return "reviews" }

func (r *Review) Connection() string {
	return ""
}

type People struct {
	Model
	SoftDeletes
	Body string
}

func (People) TableName() string { return "peoples" }

func (r *People) Connection() string {
	return "dummy"
}

type Person struct {
	Model
	SoftDeletes
	Name string
}

func (r *Person) Connection() string {
	return "dummy"
}

func (Person) TableName() string { return "people" }

type Box struct {
	Model
	SoftDeletes
	Name string
}

func (Box) TableName() string { return "boxes" }

func (r *Box) Connection() string {
	return "postgres"
}

type Schema struct {
	Model
	Name string
}

func (r *Schema) TableName() string {
	return "goravel.schemas"
}

type JsonData struct {
	Model
	Data string
}

func (JsonData) TableName() string { return "json_data" }

type GlobalScope struct {
	Model
	Name      string
	Avatar    string
	DeletedAt basedatabase.DeletedAt `gorm:"column:deleted_at" json:"deleted_at" db:"deleted_at"`
}

func (GlobalScope) TableName() string { return "global_scopes" }

func (r *GlobalScope) GlobalScopes() map[string]func(contractsorm.Query) contractsorm.Query {
	return map[string]func(contractsorm.Query) contractsorm.Query{
		"name": func(query contractsorm.Query) contractsorm.Query {
			return query.Where("name", "name_scope")
		},
		"avatar": func(query contractsorm.Query) contractsorm.Query {
			return query.Where("avatar", "avatar_scope")
		},
	}
}

// UuidEntity model for testing UUID columns
type UuidEntity struct {
	Model
	Uuid string `json:"uuid"`
	Name string `json:"name"`
}

func (UuidEntity) TableName() string { return "uuid_entities" }

// UlidEntity model for testing ULID columns
type UlidEntity struct {
	ID   string `gorm:"primaryKey;type:char(26)" json:"id"`
	Name string `json:"name"`
	Timestamps
}

func (UlidEntity) TableName() string { return "ulid_entities" }

// MorphableEntity for testing polymorphic relationships
type MorphableEntity struct {
	Model
	Name          string `json:"name"`
	MorphableID   uint   `json:"morphable_id"`
	MorphableType string `json:"morphable_type"`
}

func (MorphableEntity) TableName() string { return "morphable_entities" }

// UuidMorphableEntity for testing UUID morphs
type UuidMorphableEntity struct {
	Model
	Name          string `json:"name"`
	MorphableID   string `gorm:"type:uuid" json:"morphable_id"`
	MorphableType string `json:"morphable_type"`
}

func (UuidMorphableEntity) TableName() string { return "uuid_morphable_entities" }

// UlidMorphableEntity for testing ULID morphs
type UlidMorphableEntity struct {
	Model
	Name          string `json:"name"`
	MorphableID   string `gorm:"type:char(26)" json:"morphable_id"`
	MorphableType string `json:"morphable_type"`
}

func (UlidMorphableEntity) TableName() string { return "ulid_morphable_entities" }

// ColumnExtraAttribute is the model the schema suite's column-extra-attribute test maps its table
// to. The table is stated here rather than derived from the struct name: the ORM no longer
// pluralises a model's table, and this model is used only against the schema suite's singular
// connection, which creates goravel_column_extra_attribute.
type ColumnExtraAttribute struct {
	ID                 uint            `gorm:"primaryKey" json:"id"`
	Name               string          `json:"name"`
	Nullable           *string         `json:"nullable"`
	StringDefault      string          `json:"string_default"`
	IntegerDefault     int             `json:"integer_default"`
	BoolDefault        bool            `json:"bool_default"`
	UseCurrent         carbon.DateTime `json:"use_current"`
	UseCurrentOnUpdate carbon.DateTime `json:"use_current_on_update"`
}

func (ColumnExtraAttribute) TableName() string { return "column_extra_attribute" }
