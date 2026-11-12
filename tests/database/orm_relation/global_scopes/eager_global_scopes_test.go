package global_scopes

// Round-10 (candidate 35) behavioural supplement to the corpus port in
// laravel_port_global_scopes_test.go: it is NOT a port of a Laravel test method, but a Go-side pin
// proving at real sqlite that a scope the RELATION carries reaches an eager read — the builder-local
// form of the related model's global scope, which Laravel applies to the eager child through
// applyScopes(). The relation mounts the scope on its own query (OnQuery), and the eager runner keeps
// it on the child. It reuses the corpus harness's schema and relation seeds.

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// eagerScopedRelated is the eager-read target in "table": it carries the related_id FK the HasMany
// resolves on and a column for the relation's mounted scope to filter (the corpus's
// globalScopesTestModel carries neither, so it can back the existence path but not a relation read).
type eagerScopedRelated struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Active    int    `gorm:"column:active"`
	Foo       string `gorm:"column:foo"`
	RelatedID uint64 `gorm:"column:related_id"`
}

func (eagerScopedRelated) TableName() string { return "table" }

// eagerScopedOwner reads table2 and eager-loads "Related" from table; the relation mounts the
// ActiveScope (active = 1) beside the corpus's foo = "bar" constraint, so the eager child must blend
// both predicates.
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
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.Where("foo", "bar").ApplyScope("ActiveScope", globalScopesActiveScope)
			}},
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
	require.Len(t, byName["a"].Related, 1, "the eager child must carry the relation's mounted ActiveScope")
	require.Equal(t, int(1), byName["a"].Related[0].Active)
}
