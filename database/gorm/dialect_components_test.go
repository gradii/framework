package gorm

import (
	"context"
	"testing"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestORMWithContextKeepsSuppliedProcessor pins that a branch taken with WithContext keeps the
// processor the connection supplied rather than rebuilding one: the processor carries the
// connection's result post-processing, so a branch that swapped it would post-process its rows
// differently from the query it branched from.
//
// WithContext also re-derives the compiler from the branch's own context, so a version-dependent
// grammar compiles against that context's server version (MySQL 8's json_value against MariaDB's
// json_unquote(json_extract(...))). That SQL is dialect specific and is pinned in the integration
// corpus (tests/database/gorm) against the real driver grammars.
func TestORMWithContextKeepsSuppliedProcessor(t *testing.T) {
	components, err := databasedriver.ResolveQueryComponents(baseTestDialect{}, contractsdriver.GrammarOptions{Driver: "base"})
	require.NoError(t, err)

	query := NewOrmBuilder(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, nil, nil, components).
		Table("users").Select("name").(*OrmBuilder)

	child := query.WithContext(context.Background()).(*OrmBuilder)
	for _, current := range []*OrmBuilder{child, child.Clone().(*OrmBuilder)} {
		_, _, err := current.compileSelect()
		require.NoError(t, err)
		assert.Same(t, components.Processor, current.query.Processor())
	}

	// The receiver is left as it was built.
	assert.Same(t, components.Processor, query.query.Processor())
}
