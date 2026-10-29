package db

import (
	"testing"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	"github.com/stretchr/testify/assert"
)

// TestDialectGroupLimitUsesLegacyMySQLSQL pins that a MySQL connection older than 8.0.11 compiles the
// group limit through Laravel's legacy rewrite, which numbers rows with the @laravel_row session
// variable. The modern rewrite (row_number() over (...)) is what the base grammar produces and is
// pinned in database/db.
func TestDialectGroupLimitUsesLegacyMySQLSQL(t *testing.T) {
	options := contractsdriver.GrammarOptions{Driver: "mysql", ServerVersion: func() (string, error) { return "5.7.44", nil }}
	query := dialectQueryWith(t, "mysql", "users", options, nil, nil, nil, nil)

	sql := query.GroupLimit(2, "team_id").ToSql().Get()
	assert.Contains(t, sql, "@laravel_row")

	modern := dialectQueryWith(t, "mysql", "users", contractsdriver.GrammarOptions{Driver: "mysql", ServerVersion: func() (string, error) { return "8.0.11", nil }}, nil, nil, nil, nil)
	assert.Contains(t, modern.GroupLimit(2, "team_id").ToSql().Get(), "row_number() over")
}
