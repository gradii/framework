package gorm

import (
	"context"
	"testing"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	databasedriver "github.com/gradii/framework/database/driver"
	mysqldialect "github.com/gradii/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type versionContextKey struct{}

func TestORMWithContextRebindsVersionAndKeepsSuppliedProcessor(t *testing.T) {
	sourceContext := context.WithValue(context.Background(), versionContextKey{}, "8.0.32")
	mariaContext := context.WithValue(context.Background(), versionContextKey{}, "10.11.8-MariaDB")
	versionSource := func(ctx context.Context) (string, error) { return ctx.Value(versionContextKey{}).(string), nil }
	dialect := mysqldialect.NewDialect()
	options := contractsdriver.GrammarOptions{Driver: "mysql", Prefix: "app_", ServerVersion: func() (string, error) { return versionSource(sourceContext) }}
	components, err := databasedriver.ResolveQueryComponents(dialect, options)
	require.NoError(t, err)
	components.VersionSource = versionSource
	query := NewQuery(sourceContext, nil, contractsdatabase.Config{Driver: "mysql", Prefix: "app_"}, nil, nil, nil, nil, nil, nil, nil, components).
		Table("users").Select("data->language").(*Query)

	child := query.WithContext(mariaContext).(*Query)
	for _, current := range []*Query{child, child.Clone().(*Query)} {
		statement, _, err := current.compileSelect()
		require.NoError(t, err)
		assert.Equal(t, "select json_value(`data`, '$.\"language\"') from `app_users`", statement)
		assert.Same(t, components.Processor, current.builder.Processor())
	}
	statement, _, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, "select json_unquote(json_extract(`data`, '$.\"language\"')) from `app_users`", statement)
}
