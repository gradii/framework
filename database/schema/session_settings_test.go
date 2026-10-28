package schema

import (
	"context"
	"fmt"
	"testing"

	"github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/require"
)

type schemaSettingConnection struct {
	db.Tx
	statements []string
	fail       string
	missing    string
	discarded  bool
	contexts   []error
}

func (c *schemaSettingConnection) Scalar(sql string, _ ...any) (any, error) {
	if sql == c.missing {
		return nil, nil
	}
	return "original_" + sql, nil
}
func (c *schemaSettingConnection) Statement(sql string, _ ...any) error {
	c.statements = append(c.statements, sql)
	if sql == c.fail {
		return errors.SchemaSessionStateUnknown
	}
	return nil
}
func (c *schemaSettingConnection) StatementContext(ctx context.Context, sql string, _ ...any) error {
	c.contexts = append(c.contexts, ctx.Err())
	return c.Statement(sql)
}
func (c *schemaSettingConnection) Discard() error {
	c.discarded = true
	return nil
}
func (c *schemaSettingConnection) CanDiscard() bool { return true }

func TestSchemaSessionSettingsRestoreFailures(t *testing.T) {
	settings := func() []driver.SchemaSessionSetting {
		var result []driver.SchemaSessionSetting
		for _, name := range []string{"first", "second"} {
			result = append(result, driver.SchemaSessionSetting{
				Read: driver.SchemaStatement{SQL: name}, Value: "temporary",
				Compile: func(value any) driver.SchemaStatement {
					return driver.SchemaStatement{SQL: fmt.Sprint(name, "=", value)}
				},
			})
		}
		return result
	}
	t.Run("cancelled work still restores in reverse order", func(t *testing.T) {
		connection := &schemaSettingConnection{}
		schema := &Schema{executor: connection}
		err := schema.withSchemaSessionSettings(settings(), func() error { return context.Canceled })
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, []string{"first=temporary", "second=temporary", "second=original_second", "first=original_first"}, connection.statements)
		require.Equal(t, []error{nil, nil}, connection.contexts)
		require.False(t, connection.discarded)
	})
	t.Run("failed setup also restores", func(t *testing.T) {
		connection := &schemaSettingConnection{fail: "second=temporary"}
		schema := &Schema{executor: connection}
		err := schema.withSchemaSessionSettings(settings(), func() error {
			t.Fatal("callback must not run after failed setup")
			return nil
		})
		require.ErrorIs(t, err, errors.SchemaSessionStateUnknown)
		require.Equal(t, []string{"first=temporary", "second=temporary", "second=original_second", "first=original_first"}, connection.statements)
		require.False(t, connection.discarded)
	})
	t.Run("failed restore attempts remaining restores and discards", func(t *testing.T) {
		connection := &schemaSettingConnection{fail: "second=original_second"}
		schema := &Schema{executor: connection}
		err := schema.withSchemaSessionSettings(settings(), func() error { return context.Canceled })
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, errors.SchemaSessionStateUnknown)
		require.Equal(t, "first=original_first", connection.statements[len(connection.statements)-1])
		require.True(t, connection.discarded)
	})
	t.Run("panic restores before propagating", func(t *testing.T) {
		connection := &schemaSettingConnection{}
		schema := &Schema{executor: connection}
		require.Panics(t, func() {
			_ = schema.withSchemaSessionSettings(settings(), func() error { panic("work panicked") })
		})
		require.Equal(t, []string{"first=temporary", "second=temporary", "second=original_second", "first=original_first"}, connection.statements)
	})
	t.Run("unknown setting does not change the session", func(t *testing.T) {
		connection := &schemaSettingConnection{missing: "first"}
		schema := &Schema{executor: connection}
		err := schema.withSchemaSessionSettings(settings(), func() error {
			t.Fatal("callback must not run without a known original state")
			return nil
		})
		require.ErrorIs(t, err, errors.SchemaSessionStateUnknown)
		require.Empty(t, connection.statements)
		require.False(t, connection.discarded)
	})
}
