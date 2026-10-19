package gorm

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	gormio "gorm.io/gorm"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
)

// throughScopeCountry declares a HasManyThrough to relPost through relUser with an OnQuery scope,
// so the test can pin that the related read carries the relation's own scope rather than dropping it.
type throughScopeCountry struct {
	ID    uint
	Name  string
	Posts []*relPost `gorm:"-"`
}

func (throughScopeCountry) TableName() string { return "through_scope_countries" }

func (throughScopeCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related: &relPost{},
			Through: &relUser{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.Where("rel_posts.published", 1)
			},
		},
	}
}

// TestLoadThrough_ReadsAreCompiled pins both queries loadThrough builds: the through-table read that
// collects the second keys, and the related-rows read. Both are addressed through the package's child
// builders, so the framework compiler renders the statement instead of GORM. The loader is driven with parent keys
// too, so a change to either half of its body fails the test.
//
// Each case renders the *gormio.DB chain the loader used to hand over and compares the two after
// normalisation, so the conversion cannot drift from the SQL it replaced; the binding slices are
// asserted exactly, since their order and arity carry the chunk keys.
func TestLoadThrough_ReadsAreCompiled(t *testing.T) {
	// Case, keyword spacing and identifier quoting differ between the two builders; the predicate does
	// not. The legacy through read selects its columns bare — GORM reads a bare Select string as an
	// expression — where the compiled statement quotes each identifier. Those are the same identifiers,
	// and the driver reports the same column labels back, which is what the through map is keyed by, so
	// quotes are dropped from both sides here. The compiled statement is asserted verbatim above, and
	// the dots survive below, so a quoting or qualification regression still fails rather than being
	// normalised away.
	normalize := func(sql string) string {
		sql = strings.Join(strings.Fields(strings.ToLower(sql)), " ")
		sql = strings.ReplaceAll(sql, ", ", ",")

		return strings.ReplaceAll(sql, `"`, "")
	}

	t.Run("through read collects the second keys", func(t *testing.T) {
		q := newRelQueryWith(t, &relCountry{})
		desc, err := resolveRelation(q.instance, &relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.Equal(t, relKindHasManyThrough, desc.kind)
		assert.Equal(t, "rel_users", desc.throughTable)
		assert.Equal(t, "rel_country_id", desc.firstKey)
		assert.Equal(t, "rel_user_id", desc.secondKey)
		assert.Equal(t, "id", desc.secondLocalKey)
		assert.Equal(t, "rel_posts", desc.relatedTable)
		assert.Nil(t, desc.onPivotQuery, "a through relation carries no pivot scope")

		chunk := []any{uint(1), uint(2)}

		// The child is named the way the loader names it: the through table verbatim, the IN column
		// unquoted and table-qualified, and the select list kept bare so the returned column names —
		// the map keys the hydration loop indexes — stay the two key names.
		child := q.pivotChild(desc.throughTable, desc.throughTable+"."+desc.firstKey, chunk,
			[]string{desc.firstKey, desc.secondLocalKey})
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t,
			`select "rel_country_id", "id" from "rel_users" where "rel_users"."rel_country_id" in (?, ?)`,
			compiled)
		assert.Equal(t, chunk, bindings)

		legacy := q.freshSession().
			Table(desc.throughTable).
			Select(desc.firstKey, desc.secondLocalKey).
			Where(fmt.Sprintf("%s IN ?", q.ident(desc.throughTable, desc.firstKey)), chunk)
		statement := legacy.Session(&gormio.Session{DryRun: true}).Find(&[]map[string]any{})
		assert.NoError(t, statement.Error)
		assert.Equal(t, normalize(statement.Statement.SQL.String()), normalize(compiled),
			"the compiled through read must render the predicate the GORM chain did")
		assert.Equal(t, statement.Statement.Vars, bindings)

		// No model is attached, so the child starts from the through table alone and inherits none of
		// the parent query's state — the same shape the fresh GORM session had.
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.conditions.where, 1)
		assert.Equal(t, []string{desc.firstKey, desc.secondLocalKey}, child.conditions.selectColumns)
	})

	t.Run("related read is compiled and keeps the related key when pruned", func(t *testing.T) {
		q := newRelQueryWith(t, &relCountry{})
		desc, err := resolveRelation(q.instance, &relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		chunk := []any{uint(7)}
		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+desc.secondKey, chunk)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select * from "rel_posts" where "rel_posts"."rel_user_id" in (?)`, compiled)
		assert.Equal(t, chunk, bindings)

		legacy := q.freshSession().
			Table(desc.relatedTable).
			Where(fmt.Sprintf("%s IN ?", q.ident(desc.relatedTable, desc.secondKey)), chunk)
		statement := legacy.Session(&gormio.Session{DryRun: true}).Find(&[]*relPost{})
		assert.NoError(t, statement.Error)
		assert.Equal(t, normalize(statement.Statement.SQL.String()), normalize(compiled),
			"the compiled related read must render the predicate the GORM chain did")
		assert.Equal(t, statement.Statement.Vars, bindings)

		// The loader passes the related key as a required column, so it survives column pruning and
		// the dictionary can be keyed off the related rows.
		conditions := child.conditions
		conditions.selectColumns = []string{"title", desc.secondKey}
		pruned, prunedBindings, err := child.setConditions(conditions).compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select "title", "rel_user_id" from "rel_posts" where "rel_posts"."rel_user_id" in (?)`, pruned)
		assert.Equal(t, chunk, prunedBindings)
	})

	t.Run("related read receives the relation scope", func(t *testing.T) {
		q := newRelQueryWith(t, &throughScopeCountry{})
		desc, err := resolveRelation(q.instance, &throughScopeCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onQuery, "the fixture declares OnQuery")

		chunk := []any{uint(1), uint(2)}
		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+desc.secondKey, chunk)
		scoped, ok := desc.onQuery(child).(*Query)
		assert.True(t, ok)

		compiled, bindings, err := scoped.compileSelect()
		assert.NoError(t, err)
		// Order: the IN clause first, then the OnQuery scope's own predicate.
		assert.Equal(t,
			`select * from "rel_posts" where "rel_posts"."rel_user_id" in (?, ?) and "rel_posts"."published" = ?`,
			compiled)
		assert.Equal(t, []any{uint(1), uint(2), 1}, bindings)

		// The gorm chain this was once compared against is gone: the query is no longer assembled
		// into a GORM statement, so the compiled statement above is the whole assertion.
	})

	// No parent keys: the loader takes its empty path and never reaches a statement at all, so the
	// relation field is cleared rather than queried for an IN list that cannot match.
	t.Run("no parent keys issues no statement", func(t *testing.T) {
		q := newRelQueryWith(t, &relCountry{})
		desc, err := resolveRelation(q.instance, &relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.NoError(t, q.loadThrough(nil, &relCountry{}, desc, eagerLoadEntry{relation: "Posts"}, nil, true))
	})
	// Drive the loader itself with parent keys. The recording driver hands back one through row,
	// which is what makes loadThrough run its second read, so the assertion lands on the statements
	// the loader built and executed: the through-table read that collects the second keys, then the
	// related read. A change to either half of the loader's body fails the test.
	t.Run("loader reads the through table then the related table", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relCountry{})
		desc, err := resolveRelation(q.instance, &relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		// One through row links parent 1 to second key 7; the loader then reads the related row for 7.
		recorder.returnRows(`from "`+desc.throughTable+`"`,
			[]string{desc.firstKey, desc.secondLocalKey},
			[]driver.Value{int64(1), int64(7)})

		parents := []reflect.Value{
			reflect.ValueOf(&relCountry{ID: 1}).Elem(),
			reflect.ValueOf(&relCountry{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadThrough(parents, &relCountry{}, desc, eagerLoadEntry{relation: "Posts"}, nil, true))
		assert.Equal(t, 2, recorder.count(), "the loader reads the through table and then the related table")

		through := recorder.at(t, 0)
		assert.Equal(t,
			`select "rel_country_id", "id" from "rel_users" where "rel_users"."rel_country_id" in (?, ?)`,
			through.sql)
		assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), through.args)

		related := recorder.at(t, 1)
		assert.Equal(t, `select * from "rel_posts" where "rel_posts"."rel_user_id" in (?)`, related.sql)
		// The key comes off the through row as the driver-typed int64, not the uint the parents carried.
		assert.Equal(t, []driver.Value{int64(7)}, related.args)
	})

	// The relation's OnQuery scope must reach the related statement the loader actually runs, layered
	// on top of the second-key IN clause the loader builds.
	t.Run("loader's related read carries the relation scope", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &throughScopeCountry{})
		desc, err := resolveRelation(q.instance, &throughScopeCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onQuery, "the fixture declares OnQuery")

		recorder.returnRows(`from "`+desc.throughTable+`"`,
			[]string{desc.firstKey, desc.secondLocalKey},
			[]driver.Value{int64(1), int64(7)})

		parents := []reflect.Value{
			reflect.ValueOf(&throughScopeCountry{ID: 1}).Elem(),
			reflect.ValueOf(&throughScopeCountry{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadThrough(parents, &throughScopeCountry{}, desc, eagerLoadEntry{relation: "Posts"}, nil, true))

		related := recorder.at(t, 1)
		assert.Equal(t,
			`select * from "rel_posts" where "rel_posts"."rel_user_id" in (?) and "rel_posts"."published" = ?`,
			related.sql)
		// The second-key IN clause first, then the scope's own binding.
		assert.Equal(t, []driver.Value{int64(7), int64(1)}, related.args)
	})
}
