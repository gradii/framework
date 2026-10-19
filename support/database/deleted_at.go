package database

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"time"
)

// DeletedAt records when a row was soft deleted. It is the type orm.SoftDeletes carries.
//
// It is laid out exactly like database/sql.NullTime — Valid reports whether the row is soft
// deleted, and Time holds the moment it happened — so scanning and binding behave the same as the
// GORM type it replaces, and it can be converted to sql.NullTime rather than reimplementing the
// scanner.
//
// The clause interfaces gorm.DeletedAt also implemented are deliberately absent. They were how GORM
// learned to filter soft-deleted rows and to turn a delete into an update; the framework now builds
// that predicate itself, so this type only has to carry the value.
type DeletedAt struct {
	Time  time.Time
	Valid bool
}

// Scan implements sql.Scanner.
func (r *DeletedAt) Scan(value any) error {
	return (*sql.NullTime)(r).Scan(value)
}

// Value implements driver.Valuer. A row that was not soft deleted stores NULL.
func (r DeletedAt) Value() (driver.Value, error) {
	if !r.Valid {
		return nil, nil
	}

	return r.Time, nil
}

// MarshalJSON renders the deletion time, or null when the row was not soft deleted.
func (r DeletedAt) MarshalJSON() ([]byte, error) {
	if !r.Valid {
		return json.Marshal(nil)
	}

	return json.Marshal(r.Time)
}

// UnmarshalJSON restores the deletion time, reading null as "not soft deleted".
func (r *DeletedAt) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		r.Valid = false

		return nil
	}

	if err := json.Unmarshal(data, &r.Time); err != nil {
		return err
	}

	r.Valid = true

	return nil
}

// IsZero reports whether no deletion time is recorded, which is how a soft-delete field is
// recognised as carrying a value.
func (r DeletedAt) IsZero() bool {
	return !r.Valid
}
