package global_scopes

// Round-10 (candidate 35) behavioural supplement to the corpus port in
// laravel_port_global_scopes_test.go: it is NOT a port of a Laravel test method, but a Go-side pin
// proving at real sqlite that a related model's user-defined global scope reaches an eager read.
// Laravel applies the related model's global scopes to the eager child through applyScopes(); the
// port queues them through addGlobalScopes in runRelatedQueryCompiled / runMany2ManyQueryCompiled.
// It reuses the corpus harness's schema and relation seeds.

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// eagerScopedRelated is the eager-read target in "table": it declares the ActiveScope and the
// related_id FK the HasMany resolves on (the corpus's globalScopesTestModel carries neither, so it
// can back the existence path but not a relation read).
type eagerScopedRelated struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Active    int    `gorm:"column:active"`
	Foo       string `gorm:"column:foo"`
	RelatedID uint64 `gorm:"column:related_id"`
}

func (eagerScopedRelated) TableName() string { return "table" }

func (eagerScopedRelated) GlobalScopes() []contractsorm.GlobalScope {
	return []contractsorm.GlobalScope{
		{Name: "ActiveScope", Scope: func(q contractsorm.Query) contractsorm.Query { return q.Where("active", 1) }},
	}
}

// eagerScopedOwner reads table2 and eager-loads "Related" from table, whose ActiveScope filters
// active = 1; the relation also carries the corpus's foo = "bar" OnQuery, so the eager child must
// blend both predicates.
type eagerScopedOwner struct {
	ID      uint64                `gorm:"column:id;primaryKey;autoIncrement"`
	Name    string                `gorm:"column:name"`
	Active  int                   `gorm:"column:active"`
	Bar     string                `gorm:"column:bar"`
	Related []*eagerScopedRelated `gorm:"-"`
}

func (eagerScopedOwner) TableName() string { return "table2" }

func (eagerScopedOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Related": contractsorm.HasMany{Related: &eagerScopedRelated{}, ForeignKey: "related_id",
			OnQuery: func(q contractsorm.Query) contractsorm.Query { return q.Where("foo", "bar") }},
	}
}

func TestEagerGlobalScopes_RelatedModelScopeReachesEagerChild(t *testing.T) {
	query := newRelationQuery(t)
	globalScopesSchema(t, query)
	globalScopesSeedRelationRows(t, query)

	owners := []eagerScopedOwner{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&eagerScopedOwner{}).With("Related").Get(&owners))

	byName := map[string]eagerScopedOwner{}
	for _, owner := range owners {
		byName[owner.Name] = owner
	}

	// Owner "a" carries two related rows with foo = "bar" - one active, one not. The related model's
	// ActiveScope must keep only the active one; without the scope on the eager child both load.
	require.Contains(t, byName, "a")
	require.Len(t, byName["a"].Related, 1, "the eager child must carry the related model's ActiveScope")
	require.Equal(t, int(1), byName["a"].Related[0].Active)
}
