package relations

import (
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The pivot-accessor surface's projection-level pins (item lalign-pivot-surface): the WithPivot /
// HasPivotColumn / GetPivotColumns state on a bare relation object, the As accessor, the
// ConfigurePivotRead forwarding the leaf performs when a builder carries the capability, and the
// OrderByPivotDesc one-liner. Every value was measured by the oracle
// (php .claude/laravel-oracle/oracle.php /tmp/lalign-probes/pivot_surface.php, illuminate/database
// v12.55.1, sqlite; output /tmp/lalign-probes/pivot_surface.out); the real-descriptor half lives
// in pivot_surface_test.go.

func TestPivotSurface_StateOnAProjection(t *testing.T) {
	rel := Wrap(nil, nil, "roles", RelationInfo{
		Kind:       KindMany2Many,
		PivotTable: "role_user",
		PivotField: "UserPivot",
	}, Hooks{}).(contractsorm.BelongsToManyRelation)

	// Oracle P1a: getPivotColumns() is [] and hasPivotColumn() false for every column — the
	// declared PivotField struct supplies the SELECT columns, while the reported property is the
	// explicit list, empty until WithPivot registers an entry; getPivotAccessor() reports the
	// declared accessor (the Go form of Laravel's class default 'pivot').
	require.Empty(t, rel.GetPivotColumns())
	require.False(t, rel.HasPivotColumn("user_id"))
	require.False(t, rel.HasPivotColumn("level"))
	require.Equal(t, "UserPivot", rel.GetPivotAccessor())

	// Oracle P3/P4: the raw list keeps duplicates and call order; oracle P9: an explicitly
	// registered name — including a default pivot key — is a member afterwards.
	rel = rel.WithPivot("level", "level")
	require.Equal(t, []string{"level", "level"}, rel.GetPivotColumns())
	require.True(t, rel.HasPivotColumn("level"))
	require.False(t, rel.HasPivotColumn("type"))

	rel = rel.WithPivot("type")
	require.Equal(t, []string{"level", "level", "type"}, rel.GetPivotColumns())

	// Oracle P4 (the empty-array call): an empty WithPivot leaves the state as it was.
	rel = rel.WithPivot()
	require.Equal(t, []string{"level", "level", "type"}, rel.GetPivotColumns())

	// Oracle P5: as() replaces the accessor and leaves the column list untouched; an empty
	// accessor restores the declared field.
	rel = rel.As("GroupPivot")
	require.Equal(t, "GroupPivot", rel.GetPivotAccessor())
	require.Equal(t, []string{"level", "level", "type"}, rel.GetPivotColumns())

	rel = rel.As("")
	require.Equal(t, "UserPivot", rel.GetPivotAccessor())

	// A projection that names no field reports the empty accessor, and As still records the
	// caller's name.
	bare := Wrap(nil, nil, "roles", RelationInfo{Kind: KindMany2Many, PivotTable: "role_user"}, Hooks{}).(contractsorm.BelongsToManyRelation)
	require.Equal(t, "", bare.GetPivotAccessor())

	bare = bare.As("subscription")
	require.Equal(t, "subscription", bare.GetPivotAccessor())
}

// pivotSurfaceCaptureBuilder is the pivotReadConfigurer capability stand-in for *gorm.OrmBuilder;
// only ConfigurePivotRead and OrderBy are ever reached. The returned handle forwards both so
// chained calls keep recording on the same builder.
type pivotSurfaceCaptureBuilder struct {
	Builder

	accessors []string
	columns   [][]string
	orders    []string
	dirs      [][]string
}

func (b *pivotSurfaceCaptureBuilder) ConfigurePivotRead(accessor string, columns []string) contractsorm.Query {
	b.accessors = append(b.accessors, accessor)
	b.columns = append(b.columns, columns)

	return &pivotSurfaceCaptureQuery{parent: b}
}

func (b *pivotSurfaceCaptureBuilder) OrderBy(column string, direction ...string) contractsorm.Query {
	b.orders = append(b.orders, column)
	b.dirs = append(b.dirs, direction)

	return &pivotSurfaceCaptureQuery{parent: b}
}

type pivotSurfaceCaptureQuery struct {
	Builder

	parent *pivotSurfaceCaptureBuilder
}

func (q *pivotSurfaceCaptureQuery) ConfigurePivotRead(accessor string, columns []string) contractsorm.Query {
	return q.parent.ConfigurePivotRead(accessor, columns)
}

func (q *pivotSurfaceCaptureQuery) OrderBy(column string, direction ...string) contractsorm.Query {
	return q.parent.OrderBy(column, direction...)
}

func TestPivotSurface_PushesTheFullStateToTheBuilder(t *testing.T) {
	builder := &pivotSurfaceCaptureBuilder{}
	rel := Wrap(builder, nil, "roles", RelationInfo{
		Kind:       KindMany2Many,
		PivotTable: "role_user",
		PivotField: "Pivot",
	}, Hooks{}).(contractsorm.BelongsToManyRelation)

	// Each mutation re-sends the full state (accessor + raw column list) — the state Laravel
	// reads off the relation when get() installs shouldSelect and hydratePivotRelation runs.
	rel = rel.WithPivot("a")
	require.Equal(t, []string{""}, builder.accessors)
	require.Equal(t, [][]string{{"a"}}, builder.columns)

	rel = rel.As("S")
	require.Equal(t, []string{"", "S"}, builder.accessors)
	require.Equal(t, [][]string{{"a"}, {"a"}}, builder.columns)

	rel = rel.WithPivot("b", "a")
	require.Equal(t, []string{"", "S", "S"}, builder.accessors)
	require.Equal(t, [][]string{{"a"}, {"a"}, {"a", "b", "a"}}, builder.columns)

	// The empty call touches nothing; the empty accessor restores the declared field by sending
	// the empty override (ConfigurePivotRead resolves it against the declared field name).
	rel = rel.WithPivot()
	require.Equal(t, []string{"", "S", "S"}, builder.accessors)

	rel.As("")
	require.Equal(t, []string{"", "S", "S", ""}, builder.accessors)
	require.Equal(t, [][]string{{"a"}, {"a"}, {"a", "b", "a"}, {"a", "b", "a"}}, builder.columns)
}

func TestPivotSurface_OrderByPivotDescDelegatesToOrderBy(t *testing.T) {
	builder := &pivotSurfaceCaptureBuilder{}
	rel := Wrap(builder, nil, "roles", RelationInfo{
		Kind:       KindMany2Many,
		PivotTable: "role_user",
		PivotField: "Pivot",
	}, Hooks{}).(contractsorm.BelongsToManyRelation)

	// orderByPivot qualifies the column with the pivot table and defaults the direction to asc;
	// orderByPivotDesc binds 'desc' — Laravel's one-line body (oracle P8: `order by
	// "lax_role_user"."level" desc`).
	rel.OrderByPivot("level")
	require.Equal(t, []string{"role_user.level"}, builder.orders)
	require.Len(t, builder.dirs, 1)
	require.Empty(t, builder.dirs[0], "orderByPivot forwards the caller's empty direction; the builder defaults it to asc")

	rel.OrderByPivotDesc("level")
	require.Equal(t, []string{"role_user.level", "role_user.level"}, builder.orders)
	require.Len(t, builder.dirs, 2)
	require.Equal(t, []string{"desc"}, builder.dirs[1])
}
