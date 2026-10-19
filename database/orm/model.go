package orm

import (
	"github.com/goravel/framework/support/carbon"
	basedatabase "github.com/goravel/framework/support/database"
)

// Model is the base model for all models in the application.
type Model struct {
	Timestamps
	ID uint `gorm:"primaryKey" json:"id"`
}

// SoftDeletes is used to add soft delete functionality to a model.
//
// The field carries the framework's own type, so the ORM builds the soft-delete predicate and the
// delete rewrite itself instead of asking GORM to.
type SoftDeletes struct {
	DeletedAt basedatabase.DeletedAt `gorm:"column:deleted_at" json:"deleted_at"`
}

// Timestamps is used to add created_at and updated_at timestamps to a model.
type Timestamps struct {
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at" json:"created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at" json:"updated_at"`
}
