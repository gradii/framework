package gorm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// pivotScopedQuery returns a base query addressed at table and wrapped by the builder-level twin of
// the retired gorm chain, so its Where* predicates compile through the same path production uses.
func pivotScopedQuery(t *testing.T, table string) *queryPivotQuery {
	t.Helper()
	base := newRelQueryWith(t, &relUser{}).Table(table).(*OrmBuilder)

	return newQueryPivotQuery(base.queryGrammar, base, table)
}

func TestQueryPivotQuery_Where_TwoArgs_DefaultsToEqual(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.Where("active", 1)
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."active" = ?`)
}

func TestQueryPivotQuery_Where_ThreeArgs_UsesOperator(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.Where("priority", ">=", 5)
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."priority" >= ?`)
}

// TestQueryPivotQuery_WhereIn pins the expanded IN list: the pivot predicate's slice argument is
// expanded to one placeholder per value, matching the gorm chain this port replaced. Binding the
// slice whole to the fragment's single "?" left one placeholder against N bindings.
func TestQueryPivotQuery_WhereIn(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.WhereIn("scope", []any{"team", "global"})
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."scope" IN (?, ?)`)
}

// TestQueryPivotQuery_WhereNotIn pins the expanded NOT IN list; see TestQueryPivotQuery_WhereIn.
func TestQueryPivotQuery_WhereNotIn(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.WhereNotIn("scope", []any{"archived"})
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."scope" NOT IN (?)`)
}

func TestQueryPivotQuery_WhereNull(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.WhereNull("deleted_at")
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."deleted_at" IS NULL`)
}

func TestQueryPivotQuery_WhereNotNull(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.WhereNotNull("expires_at")
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."expires_at" IS NOT NULL`)
}

func TestQueryPivotQuery_Chained_AccumulatesAllClauses(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.Where("active", 1).
		WhereNull("deleted_at").
		WhereNotNull("expires_at")
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	// All three predicates should be ANDed into the final query.
	assert.Contains(t, sql, `"rel_user_roles"."active" = ?`)
	assert.Contains(t, sql, `"rel_user_roles"."deleted_at" IS NULL`)
	assert.Contains(t, sql, `"rel_user_roles"."expires_at" IS NOT NULL`)
	// Three predicates joined by two ANDs.
	assert.Equal(t, 2, strings.Count(sql, " and "), "three predicates should be joined by two ANDs")
}

func TestApplyOnPivotQueryToQuery_NilCallback_NoOp(t *testing.T) {
	base := newRelQueryWith(t, &relUser{}).Table("rel_user_roles").(*OrmBuilder)
	desc := &relationDescriptor{pivotTable: "rel_user_roles", onPivotQuery: nil}
	out, ok := applyOnPivotQueryToQuery(base.queryGrammar, base, desc).(*OrmBuilder)
	assert.True(t, ok)
	assert.Same(t, base, out, "nil callback must return the original query unchanged")
}

func TestApplyOnPivotQueryToQuery_AppliesCallback(t *testing.T) {
	base := newRelQueryWith(t, &relUser{}).Table("rel_user_roles").(*OrmBuilder)
	desc := &relationDescriptor{
		pivotTable: "rel_user_roles",
		onPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery {
			return q.Where("active", 1)
		},
	}
	out := applyOnPivotQueryToQuery(base.queryGrammar, base, desc)
	sql := newRelationSQL(t, out, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."active" = ?`)
}

// Descriptor wiring: OnPivotQuery declared on Many2Many / MorphToMany / MorphedByMany must land
// on the relationDescriptor.
type pivotWireUser struct {
	ID    uint
	Roles []*relRole `gorm:"-"`
}

func (pivotWireUser) TableName() string { return "pivot_wire_users" }

func (pivotWireUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{
			Related: &relRole{},
			Table:   "pivot_wire_user_roles",
			OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery {
				return q.Where("active", 1)
			},
		},
	}
}

func TestDescriptor_OnPivotQuery_Many2Many(t *testing.T) {
	q := newRelQueryWith(t, &pivotWireUser{})
	desc, err := resolveRelation(&pivotWireUser{}, "Roles")
	assert.NoError(t, err)
	assert.NotNil(t, desc.onPivotQuery, "Many2Many.OnPivotQuery must land on descriptor")

	// Verify the callback actually runs.
	base := q.Table("pivot_wire_user_roles").(*OrmBuilder)
	pq := newQueryPivotQuery(base.queryGrammar, base, "pivot_wire_user_roles")
	desc.onPivotQuery(pq)
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"pivot_wire_user_roles"."active" = ?`)
}

type pivotWireMorphPost struct {
	ID   uint
	Tags []*morphTag `gorm:"-"`
}

func (pivotWireMorphPost) TableName() string { return "pivot_wire_morph_posts" }

func (pivotWireMorphPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{
			Related: &morphTag{},
			Name:    "taggable",
			OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery {
				return q.WhereNull("deleted_at")
			},
		},
	}
}

func TestDescriptor_OnPivotQuery_MorphToMany(t *testing.T) {
	desc, err := resolveRelation(&pivotWireMorphPost{}, "Tags")
	assert.NoError(t, err)
	assert.NotNil(t, desc.onPivotQuery, "MorphToMany.OnPivotQuery must land on descriptor")
}

type pivotWireTag struct {
	ID    uint
	Posts []*morphPost `gorm:"-"`
}

func (pivotWireTag) TableName() string { return "pivot_wire_tags" }

func (pivotWireTag) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.MorphedByMany{
			Related: &morphPost{},
			Name:    "taggable",
			OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery {
				return q.Where("verified", 1)
			},
		},
	}
}

func TestDescriptor_OnPivotQuery_MorphedByMany(t *testing.T) {
	desc, err := resolveRelation(&pivotWireTag{}, "Posts")
	assert.NoError(t, err)
	assert.NotNil(t, desc.onPivotQuery, "MorphedByMany.OnPivotQuery must land on descriptor")
}

// Touches wiring — the relation declarations carry Touches: true through to the descriptor.

type touchesUser struct {
	ID    uint
	Roles []*relRole `gorm:"-"`
}

func (touchesUser) TableName() string { return "touches_users" }

func (touchesUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{Related: &relRole{}, Table: "touches_user_roles", Touches: true},
	}
}

func TestDescriptor_Touches_Many2Many(t *testing.T) {
	desc, err := resolveRelation(&touchesUser{}, "Roles")
	assert.NoError(t, err)
	assert.True(t, desc.touches)
}

type touchesPost struct {
	ID   uint
	Tags []*morphTag `gorm:"-"`
}

func (touchesPost) TableName() string { return "touches_posts" }

func (touchesPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{Related: &morphTag{}, Name: "taggable", Touches: true},
	}
}

func TestDescriptor_Touches_MorphToMany(t *testing.T) {
	desc, err := resolveRelation(&touchesPost{}, "Tags")
	assert.NoError(t, err)
	assert.True(t, desc.touches)
}

type touchesTag struct {
	ID    uint
	Posts []*morphPost `gorm:"-"`
}

func (touchesTag) TableName() string { return "touches_tags" }

func (touchesTag) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.MorphedByMany{Related: &morphPost{}, Name: "taggable", Touches: true},
	}
}

func TestDescriptor_Touches_MorphedByMany(t *testing.T) {
	desc, err := resolveRelation(&touchesTag{}, "Posts")
	assert.NoError(t, err)
	assert.True(t, desc.touches)
}

// Default: when Touches is omitted, descriptor.touches is false. Sanity guard against accidental
// always-on behavior.
func TestDescriptor_Touches_DefaultsFalse(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Roles")
	assert.NoError(t, err)
	assert.False(t, desc.touches)
}

func TestTouchIfTouching_DescTouchesFalse_NoOp(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{touches: false}
	// parent ptr is nil-safe here because the function returns before dereferencing.
	err := q.touchIfTouching(desc, &relUser{ID: 7}, uint(7))
	assert.NoError(t, err)
}

func TestTouchIfTouching_NoUpdatedAtField_NoOp(t *testing.T) {
	// relRole has no UpdatedAt field — touchIfTouching must silently skip even when touches=true.
	q := newRelQueryWith(t, &relRole{})
	desc := &relationDescriptor{touches: true}
	err := q.touchIfTouching(desc, &relRole{ID: 1}, uint(1))
	assert.NoError(t, err)
}

// PivotField wiring — defaults to "Pivot" when omitted, takes the user-specified value otherwise.

type pivotFieldUser struct {
	ID    uint
	Roles []*relRole `gorm:"-"`
}

func (pivotFieldUser) TableName() string { return "pivot_field_users" }

func (pivotFieldUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles":          contractsorm.Many2Many{Related: &relRole{}, Table: "pf_user_roles"},
		"AuditedRoles":   contractsorm.Many2Many{Related: &relRole{}, Table: "pf_audit_roles", PivotField: "AuditPivot"},
		"TaggedPosts":    contractsorm.MorphToMany{Related: &morphTag{}, Name: "taggable", PivotField: "TagPivot"},
		"InversedTagged": contractsorm.MorphedByMany{Related: &morphPost{}, Name: "taggable", PivotField: "InversePivot"},
	}
}

func TestDescriptor_PivotField_DefaultsToPivot(t *testing.T) {
	desc, err := resolveRelation(&pivotFieldUser{}, "Roles")
	assert.NoError(t, err)
	assert.Equal(t, "Pivot", desc.pivotField)
}

func TestDescriptor_PivotField_CustomMany2Many(t *testing.T) {
	desc, err := resolveRelation(&pivotFieldUser{}, "AuditedRoles")
	assert.NoError(t, err)
	assert.Equal(t, "AuditPivot", desc.pivotField)
}

func TestDescriptor_PivotField_CustomMorphToMany(t *testing.T) {
	desc, err := resolveRelation(&pivotFieldUser{}, "TaggedPosts")
	assert.NoError(t, err)
	assert.Equal(t, "TagPivot", desc.pivotField)
}

func TestDescriptor_PivotField_CustomMorphedByMany(t *testing.T) {
	desc, err := resolveRelation(&pivotFieldUser{}, "InversedTagged")
	assert.NoError(t, err)
	assert.Equal(t, "InversePivot", desc.pivotField)
}

// An operator the builder does not know is swapped for "=" and bound as the value, the way Laravel's
// wherePivot inherits Builder::where()'s handling. Before, the operator was concatenated into the
// fragment, so a caller string like "= 1) OR 1=1 --" reached the statement verbatim.
func TestQueryPivotQuery_Where_InvalidOperatorIsBoundNotConcatenated(t *testing.T) {
	pq := pivotScopedQuery(t, "rel_user_roles")
	pq.Where("scope", "= 1) OR 1=1 --", "x")
	sql := newRelationSQL(t, pq.q, &[]map[string]any{})
	assert.Contains(t, sql, `"rel_user_roles"."scope" = ?`)
	assert.NotContains(t, sql, "OR 1=1")
}
