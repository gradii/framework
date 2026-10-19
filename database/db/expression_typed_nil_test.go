package db

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/support/carbon"
)

// nilCarrier is a driver.Valuer whose pointer receiver would panic if Value() were called on a
// nil pointer, which is what a typed nil behind the interface must never reach.
type nilCarrier struct{}

func (c *nilCarrier) Value() (driver.Value, error) {
	if c == nil {
		panic("Value called on a nil carrier")
	}

	return "carrier", nil
}

// TestExplainRendersTypedNilAsNull pins the fix for the panic a nil *carbon.DateTime caused in
// ToRawSql: a model field declared as a pointer type that carries a driver.Valuer holds a typed
// nil, and the explainer must render NULL instead of calling Value() on it.
func TestExplainRendersTypedNilAsNull(t *testing.T) {
	var carrier *nilCarrier
	assert.Equal(t, "select * from t where a = NULL", Explain("select * from t where a = ?", carrier))

	var dateTime *carbon.DateTime
	assert.Equal(t, "select * from t where created_at = NULL", Explain("select * from t where created_at = ?", dateTime))
}

// TestExplainRendersNilPointerWithoutValuerAsNull covers the other half of the same bug: a nil
// *string (a model field such as Bio) carries no driver.Valuer, so it used to fall through to the
// fmt.Sprint fallback and print "<nil>" into the statement.
func TestExplainRendersNilPointerWithoutValuerAsNull(t *testing.T) {
	var text *string
	assert.Equal(t, `update "users" set "bio" = NULL`, Explain(`update "users" set "bio" = ?`, text))
}

func TestExplainStillRendersNonNilValuer(t *testing.T) {
	assert.Equal(t, "select * from t where a = 'carrier'", Explain("select * from t where a = ?", &nilCarrier{}))
}
