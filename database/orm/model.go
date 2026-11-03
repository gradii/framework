package orm

import (
	"github.com/gradii/framework/support/carbon"
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
	DeletedAt DeletedAt `gorm:"column:deleted_at" json:"deleted_at"`
}

// Timestamps is used to add created_at and updated_at timestamps to a model.
type Timestamps struct {
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at" json:"created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at" json:"updated_at"`

	// Disabled mirrors Eloquent's per-instance $timestamps = false flag: while true, a write leaves
	// created_at and updated_at to the caller — a value the caller set is still written, an unset
	// column is left out of an insert so the database's own default applies. Go's zero value must
	// mean "timestamps on", so the flag is Eloquent's negated, hence the name.
	Disabled bool `gorm:"-" json:"-"`
}

// UsesTimestamps reports whether the model instance stamps its automatic timestamps — the
// per-instance half of Eloquent's HasTimestamps::usesTimestamps, read from Disabled. Eloquent's
// other half, the suspension a withoutTimestamps scope installs, lives on the query in Go
// (Query.WithoutTimestamps); the query folds both when it decides whether a write stamps.
func (t Timestamps) UsesTimestamps() bool {
	return !t.Disabled
}
