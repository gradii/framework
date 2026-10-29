package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/stretchr/testify/assert"
)

// This file ports the lock cases of Laravel's tests/Database/DatabaseQueryBuilderTest.php
// (testMySqlLock, testPostgresLock, testSqlServerLock).
//
// Each corpus method builds the same chain against one dialect builder and asserts the lock clause
// that dialect spells: MySQL appends "for update" / "lock in share mode", Postgres appends
// "for update" / "for share" / a caller string, and SQL Server expresses the lock as a table hint on
// the from clause instead of a trailing clause.

// TestMySqlLock ports Laravel's testMySqlLock.
func TestMySqlLock(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"lock", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock(true)
		}, "select * from `foo` where `bar` = ? for update"},
		{"shared lock", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock(false)
		}, "select * from `foo` where `bar` = ? lock in share mode"},
		{"lock given as text", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock("lock in share mode")
		}, "select * from `foo` where `bar` = ? lock in share mode"},
	})

	// The lock clause is not a binding; each shape carries the where value alone.
	for _, lock := range []any{true, false, "lock in share mode"} {
		builder := NewBuilder(NewMySqlGrammar("")).Select("*").From("foo", "").Where("bar", "=", "baz").Lock(lock)

		assert.Equal(t, []any{"baz"}, builder.GetBindings())
	}
}

// TestPostgresLock ports Laravel's testPostgresLock.
func TestPostgresLock(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"lock", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock(true)
		}, `select * from "foo" where "bar" = ? for update`},
		{"shared lock", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock(false)
		}, `select * from "foo" where "bar" = ? for share`},
		{"lock given as text", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock("for key share")
		}, `select * from "foo" where "bar" = ? for key share`},
	})

	for _, lock := range []any{true, false, "for key share"} {
		builder := NewBuilder(NewPostgresGrammar("")).Select("*").From("foo", "").Where("bar", "=", "baz").Lock(lock)

		assert.Equal(t, []any{"baz"}, builder.GetBindings())
	}
}

// TestSqlServerLock ports Laravel's testSqlServerLock, whose lock lands on the from clause.
func TestSqlServerLock(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"lock", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock(true)
		}, "select * from [foo] with(rowlock,updlock,holdlock) where [bar] = ?"},
		{"shared lock", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock(false)
		}, "select * from [foo] with(rowlock,holdlock) where [bar] = ?"},
		{"lock given as text", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("foo", "").Where("bar", "=", "baz").Lock("with(holdlock)")
		}, "select * from [foo] with(holdlock) where [bar] = ?"},
	})

	for _, lock := range []any{true, false, "with(holdlock)"} {
		builder := NewBuilder(NewSqlServerGrammar("")).Select("*").From("foo", "").Where("bar", "=", "baz").Lock(lock)

		assert.Equal(t, []any{"baz"}, builder.GetBindings())
	}
}
