package gorm

import (
	"context"
	"database/sql"
	"testing"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/require"
)

// Embedding the required interface does not expose optional version capabilities.
type unversionedDialect struct{ contractsdriver.Dialect }

func TestTransactionDoesNotRequireOptionalVersionDiscovery(t *testing.T) {
	pool := sql.OpenDB(stubTxConnector{})
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	dialect := unversionedDialect{Dialect: baseTestDialect{}}
	components, err := databasedriver.ResolveQueryComponents(dialect, contractsdriver.GrammarOptions{})
	require.NoError(t, err)
	lookups := 0
	components.VersionSource = func(context.Context) (string, error) {
		lookups++
		return "", errors.OrmDriverNotSupported.Args("custom")
	}
	query := NewQuery(context.Background(), nil, contractsdatabase.Config{Driver: "custom"}, pool, nil, nil, nil, nil, nil, nil, components)
	tx, err := query.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.Zero(t, lookups)
}

func TestSuppliedQueryComponentsMustBeComplete(t *testing.T) {
	query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, nil, nil, databasedriver.QueryComponents{})
	_, _, err := query.Table("users").(*Query).compileSelect()
	require.ErrorIs(t, err, errors.DatabaseDialectComponentsMissing)
}
