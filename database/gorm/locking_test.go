package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	querybuilder "github.com/goravel/framework/database/query"
)

func TestRandomOrderFunction(t *testing.T) {
	assert.Equal(t, "RAND()", randomOrderFunction("mysql"))
	assert.Equal(t, "NEWID()", randomOrderFunction("sqlserver"))
	assert.Equal(t, "RANDOM()", randomOrderFunction("postgres"))
	assert.Equal(t, "RANDOM()", randomOrderFunction("sqlite"))
}

// TestCompileSelect_LockRendering pins the lock clause the compiled statement carries. The lock is
// resolved when the statement is compiled (sql.go drives builder.Lock from conditions.lockForUpdate/
// sharedLock), not by a GORM clause, so this keeps it covered in-package rather than only by the
// Docker-gated integration suite's TestLockForUpdate / TestSharedLock.
func TestCompileSelect_LockRendering(t *testing.T) {
	// lockForUpdate() renders the exclusive clause, sharedLock() the shared one.
	sql, _, err := compiledQuery(t, &relUser{}, Conditions{lockForUpdate: true}).compileSelect()
	assert.NoError(t, err)
	assert.Contains(t, sql, "for update")

	sql, _, err = compiledQuery(t, &relUser{}, Conditions{sharedLock: true}).compileSelect()
	assert.NoError(t, err)
	assert.Contains(t, sql, "lock in share mode")

	// Both flags compile through one switch, so lockForUpdate wins: a statement carries one lock clause.
	sql, _, err = compiledQuery(t, &relUser{}, Conditions{lockForUpdate: true, sharedLock: true}).compileSelect()
	assert.NoError(t, err)
	assert.Contains(t, sql, "for update")
	assert.NotContains(t, sql, "share")

	// No lock flag emits no lock clause at all.
	sql, _, err = compiledQuery(t, &relUser{}, Conditions{}).compileSelect()
	assert.NoError(t, err)
	assert.NotContains(t, sql, "for update")
	assert.NotContains(t, sql, "share")
}

// TestCompileSelect_SharedLockPostgres pins the dialect override: Postgres spells the shared lock
// "for share", unlike the base and MySQL grammars' "lock in share mode".
func TestCompileSelect_SharedLockPostgres(t *testing.T) {
	conditions := Conditions{model: &relUser{}, sharedLock: true}
	query := NewQuery(context.Background(), nil, contractsdatabase.Config{Driver: "postgres"}, newStubGormDB(t), nil, nil, nil, &conditions, nil)
	query.queryGrammar, _ = querybuilder.NewGrammar("postgres", "")

	sql, _, err := query.compileSelect()
	assert.NoError(t, err)
	assert.Contains(t, sql, "for share")
}
